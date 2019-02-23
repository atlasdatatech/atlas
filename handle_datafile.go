package main

import (
	"bytes"
	"database/sql"
	"encoding/csv"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jinzhu/gorm"
	"github.com/paulmach/orb/geojson"
	log "github.com/sirupsen/logrus"

	"github.com/gin-gonic/gin"
	"github.com/teris-io/shortid"
)

func crsList(c *gin.Context) {
	res := NewRes()
	res.DoneData(c, CRSs)
}

func encodingList(c *gin.Context) {
	res := NewRes()
	res.DoneData(c, Encodings)
}

func fieldTypeList(c *gin.Context) {
	res := NewRes()
	res.DoneData(c, FieldTypes)
}

func fileUpload(c *gin.Context) {
	res := NewRes()
	user := c.GetString(identityKey)
	file, err := c.FormFile("file")
	if err != nil {
		log.Errorf(`uploadFiles, gin form file error, details: %s`, err)
		res.Fail(c, 4046)
		return
	}
	filename := file.Filename
	ext := filepath.Ext(filename)
	lext := strings.ToLower(ext)
	switch lext {
	case ".csv", ".geojson", ".json", ".zip":
	case ".mbtiles":
	default:
		res.FailMsg(c, "未知数据格式, 请使用csv/geojson/zip(shapefile)数据.")
		return
	}
	name := strings.TrimSuffix(filename, ext)
	id, _ := shortid.Generate()
	t := c.Param("type")
	var dir string
	switch t {
	case "ds":
		dir = "datasets"
	case "ts":
		dir = "tilesets"
	default:
		res.FailMsg(c, "未知数据类型, 请使用csv/geojson/zip(shapefile)数据.")
		return
	}
	dst := filepath.Join(dir, name+"."+id+ext)

	if err := c.SaveUploadedFile(file, dst); err != nil {
		log.Errorf(`uploadFiles, saving uploaded file error, details: %s`, err)
		res.Fail(c, 5002)
		return
	}

	var dtfiles []Datafile
	if lext == ".zip" {
		getDatafiles := func(dir string) map[string]int64 {
			files := make(map[string]int64)
			fileInfos, err := ioutil.ReadDir(dir)
			if err != nil {
				log.Error(err)
				return files
			}
			for _, fileInfo := range fileInfos {
				if fileInfo.IsDir() {
					continue
				}
				ext := filepath.Ext(fileInfo.Name())
				//处理zip内部数据文件
				switch strings.ToLower(ext) {
				case ".csv", ".geojson", ".json":
					files[filepath.Join(dir, fileInfo.Name())] = fileInfo.Size()
				case ".shp":
					otherShpFile := func(ext string) int64 {
						for _, file := range fileInfos {
							if file.IsDir() {
								continue
							}
							name := file.Name()
							e := filepath.Ext(name)
							if strings.ToLower(ext) == strings.ToLower(e) {
								if e != ext { //rename to lower .ext for linux posible error
									os.Rename(filepath.Join(dir, name), filepath.Join(dir, strings.TrimSuffix(name, e)+strings.ToLower(e)))
								}
								return file.Size()
							}
						}
						return 0
					}
					size := fileInfo.Size()
					fsize := otherShpFile(".dbf")
					if fsize > 0 {
						size += fsize
					} else {
						continue
					}
					fsize = otherShpFile(".shx")
					if fsize > 0 {
						size += fsize
					} else {
						continue
					}
					fsize = otherShpFile(".prj")
					if fsize > 0 {
						size += fsize
					} else {
						continue
					}

					files[filepath.Join(dir, fileInfo.Name())] = size
				default:
					//other shp files
				}
			}
			return files
		}
		subdir := UnZipToDir(dst)
		zipDatafiles := getDatafiles(subdir)
		for datafile, size := range zipDatafiles {
			newName := strings.TrimSuffix(filepath.Base(datafile), filepath.Ext(datafile))
			df := Datafile{
				ID:      newName + "." + id,
				Owner:   user,
				Name:    newName,
				Tag:     name,
				Geotype: "vector",
				Format:  strings.ToLower(filepath.Ext(datafile)),
				Path:    datafile,
				Size:    size,
				Type:    t,
			}
			err = df.UpInsert()
			if err != nil {
				log.Errorf(`uploadFiles, upinsert datafile info error, details: %s`, err)
				res.FailErr(c, err)
				return
			}
			dtfiles = append(dtfiles, df)
		}
	} else {
		df := Datafile{
			ID:      name + "." + id,
			Owner:   user,
			Name:    name,
			Geotype: "vector",
			Format:  lext,
			Path:    dst,
			Size:    file.Size,
			Type:    t,
		}
		err = df.UpInsert()
		if err != nil {
			log.Errorf(`uploadFiles, upinsert datafile info error, details: %s`, err)
			res.FailErr(c, err)
			return
		}
		dtfiles = append(dtfiles, df)
	}

	res.DoneData(c, dtfiles)
}

func dataPreview(c *gin.Context) {
	res := NewRes()
	user := c.GetString(identityKey)
	log.Println(user)
	id := c.Param("id")
	df := &Datafile{}
	err := db.Where("id = ?", id).First(df).Error
	if err != nil {
		if gorm.IsRecordNotFoundError(err) {
			log.Errorf(`dataPreview, can not find datafile, id: %s`, id)
			res.FailMsg(c, "datafile not found")
			return
		}
		log.Errorf(`dataPreview, get datafile info error, details: %s`, err)
		res.Fail(c, 5001)
		return
	}
	encoding := strings.ToLower(c.Query("encoding"))
	switch encoding {
	case "":
	case "utf-8", "gbk", "big5":
		df.Encoding = encoding
	default:
		df.Encoding = "gb18030"
	}
	switch df.Format {
	case ".csv", ".geojson", ".json", ".shp":
		pv := df.getPreview()
		res.DoneData(c, pv)
	default:
		res.DoneData(c, "unkown format")
	}
}

func dataImport(c *gin.Context) {
	res := NewRes()
	uid := c.GetString(identityKey)
	dp := &DatafileBind{}
	err := c.Bind(&dp)
	if err != nil {
		log.Error(err)
		res.Fail(c, 4001)
		return
	}
	//GeometryCollection,Point,MultiPoint,LineString,MultiLineString,Polygon,MultiPolygon

	df := &Datafile{}
	err = db.Where("id = ?", dp.ID).First(df).Error
	if err != nil {
		if gorm.IsRecordNotFoundError(err) {
			log.Errorf(`can not find datafile, id: %s`, dp.ID)
			res.FailMsg(c, `can not find datafile`)
			return
		}
		log.Errorf(`get datafile info error, details: %s`, err)
		res.Fail(c, 5001)
		return
	}
	df.ID = dp.ID
	df.Name = dp.Name
	df.Alias = dp.Alias
	df.Encoding = dp.Encodings[0]
	df.Crs = dp.Crss[0]
	df.Geotype = dp.Geotype
	df.Fields = dp.Fields
	if dp.Tags != nil && len(dp.Tags) > 0 {
		df.Tag = dp.Tags[0]
	}

	task := &Task{}
	df.Overwrite = true
	switch df.Format {
	case ".csv", ".geojson", ".json":
		task, err = df.dataImport(dp)
		if err != nil {
			log.Error(err)
			res.FailErr(c, err)
			return
		}
	case ".shp":
		task, err = df.ogrImport()
		if err != nil {
			log.Error(err)
			res.FailErr(c, err)
			return
		}
	case ".mbtiles":
		task, err = df.serveMBTiles(uid)
		if err != nil {
			log.Error(err)
			res.FailErr(c, err)
			return
		}
	}

	err = updateDatasetInfo(df.ID)
	if err != nil {
		log.Error(err)
		res.FailErr(c, err)
		return
	}

	err = df.UpInsert()
	if err != nil {
		log.Errorf(`dataImport, upinsert datafile info error, details: %s`, err)
		res.FailErr(c, err)
		return
	}
	res.DoneData(c, task)
}

func taskQuery(c *gin.Context) {
	res := NewRes()
	user := c.GetString(identityKey)
	log.Println(user)
	id := c.Param("id")
	task, ok := taskSet.Load(id)
	if ok {
		res.DoneData(c, task)
		return
	}
	dbtask := &Task{ID: id}
	dbtask.info()
	res.DoneData(c, dbtask)
}

func taskStreamQuery(c *gin.Context) {

	id := c.Param("id")
	task, ok := taskSet.Load(id)
	if ok {
		// listener := openListener(roomid)
		ticker := time.NewTicker(1 * time.Second)
		// users.Add("connected", 1)
		defer func() {
			// closeListener(roomid, listener)
			ticker.Stop()
			// users.Add("disconnected", 1)
		}()

		c.Stream(func(w io.Writer) bool {
			select {
			// case msg := <-listener:
			// 	messages.Add("outbound", 1)
			// 	c.SSEvent("message", msg)
			case <-ticker.C:
				c.SSEvent("task", task)
			}
			return true
		})
	}
}

func importFiles(c *gin.Context) {
	res := NewRes()
	ftype := c.Param("name")
	if ftype != "csv" && ftype != "geojson" {
		res.Fail(c, 400)
		// res.FailMsg(c, "unkonw file type, must be .geojson or .csv")
		return
	}

	file, err := c.FormFile("file")
	if err != nil {
		log.Errorf(`importFiles, get form: %s; type: %s`, err, ftype)
		res.Fail(c, 4046)
		return
	}

	dir := cfgV.GetString("assets.datasets")
	filename := file.Filename
	ext := filepath.Ext(filename)
	name := strings.TrimSuffix(filename, ext)
	id, _ := shortid.Generate()
	id = name + "." + id
	dst := filepath.Join(dir, id+ext)
	if err := c.SaveUploadedFile(file, dst); err != nil {
		log.Errorf(`importFiles, saving tmp file: %s; file: %s`, err, filename)
		res.Fail(c, 5002)
		return
	}
	buf, err := ioutil.ReadFile(dst)
	if err != nil {
		log.Errorf(`importFiles, csv reader failed: %s; file: %s`, err, filename)
		res.Fail(c, 5003)
		return
	}

	var cnt int64
	// datasetType := TypeAttribute

	switch ftype {
	case "geojson":
		if name != "block_lines" && name != "regions" && name != "interests" && name != "static_buffers" {
			res.FailMsg(c, "unkown datasets")
			return
		}
		fc, err := geojson.UnmarshalFeatureCollection(buf)
		if err != nil {
			log.Error(err)
			res.FailErr(c, err)
			return
		}
		db.DropTableIfExists(name)
		createTable := func(fc *geojson.FeatureCollection) error {
			var headers []string
			var fts []string
			var geoType string
			for _, f := range fc.Features {
				geoType = f.Geometry.GeoJSONType()
				for k, v := range f.Properties {
					var t string
					switch v.(type) {
					case bool:
						t = "BOOL" //or 'timestamp with time zone'
					case int:
						t = "INT"
					case float64:
						t = "NUMERIC"
					case []interface{}:
						t = "_VARCHAR" //or 'character varying[]'
					default: //string/map[string]interface{}/nil
						t = "TEXT"
					}
					headers = append(headers, k)
					fts = append(fts, k+" "+t)
				}
				break
			}
			//add 'geom geometry(Geometry,4326)'
			geom := fmt.Sprintf("geom geometry(%s,4326)", geoType)
			headers = append(headers, "geom")
			fts = append(fts, geom)

			st := fmt.Sprintf(`CREATE TABLE %s (%s);`, name, strings.Join(fts, ","))
			err := db.Exec(st).Error
			if err != nil {
				return err
			}

			kvsi := make(map[string]int, len(headers))
			kvst := make(map[string]string, len(headers))
			for i, h := range headers {
				kvsi[h] = i
				kvst[h] = strings.Split(fts[i], " ")[1]
			}

			var vals []string
			for _, f := range fc.Features {
				vs := make([]string, len(headers))
				for k, val := range f.Properties {
					var s string
					switch kvst[k] {
					case "BOOL":
						v, ok := val.(bool) // Alt. non panicking version
						if ok {
							s = strconv.FormatBool(v)
						} else {
							s = "null"
						}
					case "NUMERIC":
						v, ok := val.(float64) // Alt. non panicking version
						if ok {
							s = strconv.FormatFloat(v, 'E', -1, 64)
						} else {
							s = "null"
						}
					default: //string,map[string]interface{},[]interface{},time.Time,bool
						if val == nil {
							s = ""
						} else {
							s = val.(string)
						}
						s = "'" + s + "'"
					}
					vs[kvsi[k]] = s
				}
				geom, err := geojson.NewGeometry(f.Geometry).MarshalJSON()
				if err != nil {
					return err
				}
				vs[kvsi["geom"]] = fmt.Sprintf(`st_setsrid(st_geomfromgeojson('%s'),4326)`, string(geom))

				vals = append(vals, fmt.Sprintf(`(%s)`, strings.Join(vs, ",")))
			}

			st = fmt.Sprintf(`INSERT INTO "%s" (%s) VALUES %s ON CONFLICT DO NOTHING;`, name, strings.Join(headers, ","), strings.Join(vals, ",")) // ON CONFLICT (id) DO UPDATE SET (%s) = (%s)
			// log.Println(st)
			query := db.Exec(st)
			if err, cnt = query.Error, query.RowsAffected; err != nil {
				return err
			}
			return nil
		}

		err = createTable(fc)
		if err != nil {
			log.Error(err)
			res.FailErr(c, err)
			return
		}
		err = updateDatasetInfo(name)
		if err != nil {
			log.Error(err)
			res.FailErr(c, err)
		}
	case "csv":
		reader := csv.NewReader(bytes.NewReader(buf))
		csvHeader, err := reader.Read()
		if err != nil {
			log.Errorf(`importDataset, csv reader failed: %s; file: %s`, err, filename)
			res.Fail(c, 5003)
			return
		}

		row2values := func(row []string, cols []*sql.ColumnType) string {
			var vals string
			for i, col := range cols {
				// fmt.Println(i, col.DatabaseTypeName(), col.Name())
				switch col.DatabaseTypeName() {
				case "INT", "INT4", "NUMERIC": //number
					if "" == row[i] {
						vals = vals + "null,"
					} else {
						vals = vals + row[i] + ","
					}
				case "TIMESTAMPTZ":
					if "" == row[i] {
						vals = vals + "null,"
					} else {
						vals = vals + "'" + row[i] + "',"
					}
				default: //string->"TEXT" "VARCHAR","BOOL",datetime->"TIMESTAMPTZ",pq.StringArray->"_VARCHAR"
					vals = vals + "'" + row[i] + "',"
				}
			}
			vals = strings.TrimSuffix(vals, ",")
			return vals
		}

		clear := func(name string) error {
			s := fmt.Sprintf(`DELETE FROM %s;`, name)
			err := db.Exec(s).Error
			if err != nil {
				return err
			}
			s = fmt.Sprintf(`DELETE FROM datasets WHERE name='%s';`, name)
			return db.Exec(s).Error
		}
		insert := func(header string) error {
			if len(strings.Split(header, ",")) != len(csvHeader) {
				log.Errorf("the cvs file format error, file:%s,  should be:%s", name, header)
				return fmt.Errorf("the cvs file format error, file:%s", name)
			}

			s := fmt.Sprintf(`SELECT %s FROM "%s" LIMIT 0`, header, name)
			rows, err := db.Raw(s).Rows() // (*sql.Rows, error)
			if err != nil {
				return err
			}
			defer rows.Close()
			cols, err := rows.ColumnTypes()
			if err != nil {
				return err
			}
			var vals []string
			for {
				row, err := reader.Read()
				if err == io.EOF {
					break
				}
				if err != nil {
					return err
				}
				rval := row2values(row, cols)
				log.Debug(rval)
				vals = append(vals, fmt.Sprintf(`(%s)`, rval))
			}
			s = fmt.Sprintf(`INSERT INTO "%s" (%s) VALUES %s ON CONFLICT DO NOTHING;`, name, header, strings.Join(vals, ",")) // ON CONFLICT (id) DO UPDATE SET (%s) = (%s)
			query := db.Exec(s)
			cnt = query.RowsAffected
			return query.Error
		}

		//数据入库
		var header, search string
		updateGeom := false
		switch name {
		case "banks", "others", "pois", "plans":
			switch name {
			case "banks":
				header = "机构号,名称,营业状态,行政区,网点类型,营业部,管理行,权属,营业面积,到期时间,装修时间,人数,行评等级,X,Y"
				search = ",search =ARRAY[机构号,名称,行政区,网点类型,管理行]"
			case "others":
				header = "机构号,名称,银行类别,网点类型,地址,X,Y,SID"
				search = ",search =ARRAY[机构号,名称,银行类别,地址]"
			case "pois":
				header = "名称,类型,性质,建筑面积,热度,人均消费,均价,户数,交付时间,职工人数,备注,X,Y,SID"
				search = ",search =ARRAY[名称,备注]"
			case "plans":
				header = "机构号,名称,类型,年份,规划建议,实施时间,X,Y,SID"
			}
			updateGeom = true
			// datasetType = TypePoint
		case "savings", "m1", "m2", "m5", "buffer_scales", "m2_weights", "m4_weights", "m4_scales":
			switch name {
			case "savings":
				header = "机构号,名称,年份,总存款日均,单位存款日均,个人存款日均,保证金存款日均,其他存款日均"
			case "m1":
				header = "机构号,商业规模,商业人流,道路特征,快速路,位置特征,转角位置,街巷,斜坡,公共交通类型,距离,停车位,收费,建筑形象,营业厅面积,装修水准,网点类型,总得分"
			case "m2":
				header = "机构号,营业面积,人数,个人增长,个人存量,公司存量"
			case "m5":
				header = "名称,生产总值,人口,房地产成交面积,房地产成交均价,社会消费品零售总额,规模以上工业增加值,金融机构存款,金融机构贷款"
			case "buffer_scales":
				header = "type,scale"
			case "m2_weights":
				header = "field,weight"
			case "m4_weights":
				header = "type,weight"
			case "m4_scales":
				header = "type,scale"
			}
		default:
			res.FailMsg(c, "unkown datasets")
			return
		}

		clear(name)
		err = insert(header)
		if err != nil {
			log.Errorf("import %s error:%s", filename, err.Error())
			res.Fail(c, 5001)
			return
		}
		if updateGeom {
			update := fmt.Sprintf(`UPDATE %s SET geom = ST_GeomFromText('POINT(' || x || ' ' || y || ')',4326)%s;`, name, search)
			result := db.Exec(update)
			if result.Error != nil {
				log.Errorf("update %s create geom error:%s", name, result.Error.Error())
				res.Fail(c, 5001)
				return
			}
		}
		err = updateDatasetInfo(name)
		if err != nil {
			log.Error(err)
			res.FailErr(c, err)
			return
		}
	default:
		return
	}

	res.DoneData(c, gin.H{
		"id":  id,
		"cnt": cnt,
	})
}
