package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	geom "github.com/go-spatial/geom"
	slippy "github.com/go-spatial/geom/slippy"
	"github.com/go-spatial/tegola"
	"github.com/go-spatial/tegola/atlas"
	"github.com/go-spatial/tegola/mvt"
	"github.com/go-spatial/tegola/server"
	"github.com/jinzhu/gorm"
	"github.com/paulmach/orb/geojson"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"github.com/teris-io/shortid"

	"github.com/gin-gonic/gin"
)

func listDatasets(c *gin.Context) {
	res := NewRes()
	// uid := c.GetString(identityKey)
	uid := c.Param("user")
	set := userSet.service(uid)
	if set == nil {
		res.Fail(c, 4044)
		return
	}
	var dss []*DataService
	set.D.Range(func(_, v interface{}) bool {
		dss = append(dss, v.(*DataService))
		return true
	})
	res.DoneData(c, dss)
}

func getDatasetInfo(c *gin.Context) {
	res := NewRes()
	// uid := c.GetString(identityKey)
	uid := c.Param("user")
	did := c.Param("id")
	ds := userSet.dataset(uid, did)
	if ds == nil {
		res.Fail(c, 4044)
		return
	}
	res.DoneData(c, ds)
}

func oneClickImport(c *gin.Context) {
	res := NewRes()
	uid := c.Param("user")
	set := userSet.service(uid)
	if set == nil {
		res.Fail(c, 4044)
		return
	}
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
	case ".csv", ".geojson", ".kml", ".gpx", ".zip":
	default:
		res.FailMsg(c, "未知数据格式, 请使用csv/geojson(json)/shapefile(zip)数据.")
		return
	}
	name := strings.TrimSuffix(filename, ext)
	id, _ := shortid.Generate()
	dst := filepath.Join("datasets", uid, name+"."+id+lext)
	if err := c.SaveUploadedFile(file, dst); err != nil {
		log.Errorf(`uploadFiles, saving uploaded file error, details: %s`, err)
		res.Fail(c, 5002)
		return
	}
	dtfiles, err := LoadDatafile(dst)
	if err != nil {
		log.Errorf(`uploadFiles, loading datafile error, details: %s`, err)
		res.FailErr(c, err)
		return
	}
	var tasks []*Task
	for _, df := range dtfiles {
		st := time.Now()
		// srcfile := df.Path
		// switch df.Format {
		// case ".kml", ".gpx":
		// 	err := df.toGeojson()
		// 	if err != nil {
		// 		log.Errorf(`oneClickImport, convert to geojson error, details: %s`, err)
		// 		continue
		// 	}
		// 	df.Path = strings.TrimSuffix(df.Path, df.Format) + ".geojson"
		// 	df.Format = ".geojson"
		// }
		// log.Infof(`%s convert to geojson takes :%v`, srcfile, time.Since(st))
		err = df.UpInsert()
		if err != nil {
			log.Errorf(`uploadFiles, upinsert datafile info error, details: %s`, err)
		}
		dfb := df.getPreview()
		df.Update(dfb)
		df.Overwrite = true
		st = time.Now()
		task := df.dataImport()
		go func(df *Datafile) {
			<-task.Pipe //wait finish
			<-taskQueue
			task.State = "finish"
			task.save()
			if task.Err != "" {
				log.Error(task.Err)
				return
			}
			t := time.Since(st)
			log.Infof("one key import time cost: %v", t)
			ds, err := df.toDataset()
			if err != nil {
				log.Error(err)
				res.FailErr(c, err)
				return
			}

			err = ds.UpInsert()
			if err != nil {
				log.Errorf(`dataImport, upinsert dataset info error, details: %s`, err)
				res.FailErr(c, err)
				return
			}

			if true {
				dss := ds.toService()
				set.D.Store(dss.ID, dss)
			}
		}(df)
		tasks = append(tasks, task)
		//todo goroute 导入，以下事务需在task完成后处理
	}
	res.DoneData(c, tasks)
}

func uploadFile(c *gin.Context) {
	res := NewRes()
	uid := c.Param("user")
	set := userSet.service(uid)
	if set == nil {
		res.Fail(c, 4044)
		return
	}
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
	case ".csv", ".geojson", ".kml", ".gpx", ".zip":
	default:
		res.FailMsg(c, "未知数据格式, 请使用csv/geojson/kml/gpx/shapefile(zip)格式.")
		return
	}
	name := strings.TrimSuffix(filename, ext)
	id, _ := shortid.Generate()
	dst := filepath.Join("datasets", uid, name+"."+id+lext)
	if err := c.SaveUploadedFile(file, dst); err != nil {
		log.Errorf(`uploadFiles, saving uploaded file error, details: %s`, err)
		res.Fail(c, 5002)
		return
	}
	dtfiles, err := LoadDatafile(dst)
	if err != nil {
		log.Errorf(`uploadFiles, loading datafile error, details: %s`, err)
		res.FailErr(c, err)
		return
	}
	var dfbinds []*DatafileBind
	for _, df := range dtfiles {
		err = df.UpInsert()
		if err != nil {
			log.Errorf(`uploadFiles, upinsert datafile info error, details: %s`, err)
		}
		dfb := df.getPreview()
		dfbinds = append(dfbinds, dfb)
	}
	res.DoneData(c, dfbinds)
}

func previewFile(c *gin.Context) {
	res := NewRes()
	uid := c.Param("user")
	set := userSet.service(uid)
	if set == nil {
		res.Fail(c, 4044)
		return
	}
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
	case ".csv", ".geojson", ".shp":
		pv := df.getPreview()
		res.DoneData(c, pv)
	default:
		res.DoneData(c, "unkown format")
	}
}

func importFile(c *gin.Context) {
	res := NewRes()
	uid := c.Param("user")
	set := userSet.service(uid)
	if set == nil {
		res.Fail(c, 4044)
		return
	}
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
	df.Update(dp)
	df.Overwrite = true
	err = df.UpInsert()
	if err != nil {
		log.Errorf(`dataImport, upinsert datafile info error, details: %s`, err)
		res.FailErr(c, err)
		return
	}

	task := df.dataImport()
	if task.Err != "" {
		log.Error(task.Err)
		res.FailMsg(c, task.Err)
		<-task.Pipe
		<-taskQueue
		return
	}
	go func(df *Datafile) {
		<-task.Pipe
		<-taskQueue
		//todo goroute 导入，以下事务需在task完成后处理
		ds, err := df.toDataset()
		if err != nil {
			log.Error(err)
			res.FailErr(c, err)
			return
		}
		err = ds.UpInsert()
		if err != nil {
			log.Errorf(`dataImport, upinsert dataset info error, details: %s`, err)
			res.FailErr(c, err)
			return
		}

		if true {
			dss := ds.toService()
			set.D.Store(dss.ID, dss)
		}
	}(df)
	res.DoneData(c, task)
}

func taskQuery(c *gin.Context) {
	res := NewRes()
	// user := c.Param("user")
	id := c.Param("id")
	task, ok := taskSet.Load(id)
	if ok {
		res.DoneData(c, task)
		return
	}
	dbtask := &Task{ID: id}
	err := dbtask.info()
	if err != nil {
		res.FailMsg(c, "task not found")
		return
	}
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

//downloadDataset 下载数据集
func downloadDataset(c *gin.Context) {
	res := NewRes()
	// uid := c.GetString(identityKey)
	uid := c.Param("user")
	did := c.Param("id")
	ds := userSet.dataset(uid, did)
	if ds == nil {
		log.Errorf(`downloadDataset, %s's data service (%s) not found ^^`, uid, did)
		res.Fail(c, 4044)
		return
	}
	file, err := os.Open(ds.URL)
	if err != nil {
		log.Errorf(`downloadTileset, open %s's tileset (%s) error, details: %s ^^`, uid, did, err)
		res.FailErr(c, err)
		return
	}
	c.Header("Content-type", "application/octet-stream")
	c.Header("Content-Disposition", "attachment; filename= "+ds.ID+".mbtiles")
	io.Copy(c.Writer, file)
	return
}

func viewDataset(c *gin.Context) {
	// res := NewRes()
	uid := c.Param("user")
	did := c.Param("id")
	// tss := userSet.tileset(uid, tid)
	// if tss == nil {
	// 	log.Errorf("tilesets id(%s) not exist in the service", tid)
	// 	res.Fail(c, 4044)
	// 	return
	// }
	//"china-z7.rjA5dSCmR"
	// tileurl :="http://localhost:8080/maps/map/roads/{z}/{x}/{y}.pbf"
	tileurl := fmt.Sprintf(`%s/datasets/%s/x/%s/{z}/{x}/{y}.pbf`, rootURL(c.Request), uid, did) //need use user own service set
	c.HTML(http.StatusOK, "dataset.html", gin.H{
		"Title": "PerView",
		"ID":    did,
		"URL":   tileurl,
		"FMT":   "pbf",
	})
}

func getDistinctValues(c *gin.Context) {
	res := NewRes()
	did := c.Param("id")
	if code := checkDataset(did); code != 200 {
		res.Fail(c, code)
		return
	}
	var body struct {
		Field string `form:"field" json:"field" binding:"required"`
	}
	err := c.Bind(&body)
	if err != nil {
		log.Error(err)
		res.Fail(c, 4001)
		return
	}
	s := fmt.Sprintf(`SELECT distinct(%s) as val,count(*) as cnt FROM "%s" GROUP BY %s;`, body.Field, did, body.Field)
	fmt.Println(s)
	rows, err := db.Raw(s).Rows()
	if err != nil {
		log.Error(err)
		res.Fail(c, 5001)
		return
	}
	defer rows.Close()
	type ValCnt struct {
		Val string
		Cnt int
	}
	var valCnts []ValCnt
	for rows.Next() {
		var vc ValCnt
		// ScanRows scan a row into user
		db.ScanRows(rows, &vc)
		valCnts = append(valCnts, vc)
		// do something
	}
	res.DoneData(c, valCnts)
}

func getGeojson(c *gin.Context) {
	res := NewRes()
	did := c.Param("id")
	fields := c.Query("fields")
	filter := c.Query("filter")
	tbname := strings.ToLower(did)
	if code := checkDataset(tbname); code != 200 {
		res.Fail(c, code)
		return
	}

	selStr := "st_asgeojson(geom) as geom "
	if "" != fields {
		selStr = selStr + "," + fields
	}
	var whr string
	if "" != filter {
		whr = " WHERE " + filter
	}
	s := fmt.Sprintf(`SELECT %s FROM "%s" %s;`, selStr, tbname, whr)
	rows, err := db.Raw(s).Rows() // (*sql.Rows, error)
	if err != nil {
		log.Error(err)
		res.Fail(c, 5001)
		return
	}
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil {
		res.Fail(c, 5001)
		return
	}
	fc := geojson.NewFeatureCollection()
	for rows.Next() {
		// Scan needs an array of pointers to the values it is setting
		// This creates the object and sets the values correctly
		vals := make([]interface{}, len(cols))
		for i := range cols {
			vals[i] = new(sql.RawBytes)
		}
		err = rows.Scan(vals...)
		if err != nil {
			log.Error(err)
		}

		f := geojson.NewFeature(nil)

		for i, t := range cols {
			// skip nil values.
			if vals[i] == nil {
				continue
			}
			rb, ok := vals[i].(*sql.RawBytes)
			if !ok {
				log.Errorf("Cannot convert index %d column %s to type *sql.RawBytes", i, t.Name())
				continue
			}

			switch t.Name() {
			case "geom":
				geom, err := geojson.UnmarshalGeometry([]byte(*rb))
				if err != nil {
					log.Errorf("UnmarshalGeometry from geojson result error, index %d column %s", i, t.Name())
					continue
				}
				f.Geometry = geom.Geometry()
			default:
				v := string(*rb)
				switch cols[i].DatabaseTypeName() {
				case "INT", "INT4":
					f.Properties[t.Name()], _ = strconv.Atoi(v)
				case "NUMERIC", "DECIMAL": //number
					f.Properties[t.Name()], _ = strconv.ParseFloat(v, 64)
				// case "BOOL":
				// case "TIMESTAMPTZ":
				// case "_VARCHAR":
				// case "TEXT", "VARCHAR", "BIGINT":
				default:
					f.Properties[t.Name()] = v
				}
			}

		}
		fc.Append(f)
	}
	var extent []byte
	stbox := fmt.Sprintf(`SELECT st_asgeojson(st_extent(geom)) as extent FROM %s %s;`, tbname, whr)
	db.Raw(stbox).Row().Scan(&extent) // (*sql.Rows, error)
	ext, err := geojson.UnmarshalGeometry(extent)
	if err == nil {
		fc.BBox = geojson.NewBBox(ext.Geometry().Bound())
	}
	gj, err := fc.MarshalJSON()
	if err != nil {
		log.Errorf("unable to MarshalJSON of featureclection.")
		res.FailMsg(c, "unable to MarshalJSON of featureclection.")
		return
	}
	c.JSON(http.StatusOK, json.RawMessage(gj))
}

func queryGeojson(c *gin.Context) {
	res := NewRes()
	did := c.Param("id")

	var body struct {
		Geom   string `form:"geom" json:"geom"`
		Fields string `form:"fields" json:"fields"`
		Filter string `form:"filter" json:"filter"`
	}
	err := c.Bind(&body)
	if err != nil {
		res.Fail(c, 4001)
		return
	}

	selStr := "st_asgeojson(geom) as geom "
	if "" != body.Fields {
		selStr = selStr + "," + body.Fields
	}
	var whrStr string
	if body.Geom != "" {
		whrStr = fmt.Sprintf(` WHERE geom && st_geomfromgeojson('%s')`, body.Geom)
		if "" != body.Filter {
			whrStr = whrStr + " AND " + body.Filter
		}
	} else {
		if "" != body.Filter {
			whrStr = " WHERE " + body.Filter
		}
	}

	s := fmt.Sprintf(`SELECT %s FROM %s  %s;`, selStr, did, whrStr)
	rows, err := db.Raw(s).Rows() // (*sql.Rows, error)
	if err != nil {
		log.Error(err)
		res.Fail(c, 5001)
		return
	}
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil {
		res.Fail(c, 5001)
		return
	}
	fc := geojson.NewFeatureCollection()
	for rows.Next() {
		// Scan needs an array of pointers to the values it is setting
		// This creates the object and sets the values correctly
		vals := make([]interface{}, len(cols))
		for i := range cols {
			vals[i] = new(sql.RawBytes)
		}
		err = rows.Scan(vals...)
		if err != nil {
			log.Error(err)
		}

		f := geojson.NewFeature(nil)

		for i, t := range cols {
			// skip nil values.
			if vals[i] == nil {
				continue
			}
			rb, ok := vals[i].(*sql.RawBytes)
			if !ok {
				log.Errorf("Cannot convert index %d column %s to type *sql.RawBytes", i, t.Name())
				continue
			}

			switch t.Name() {
			case "geom":
				geom, err := geojson.UnmarshalGeometry([]byte(*rb))
				if err != nil {
					log.Errorf("UnmarshalGeometry from geojson result error, index %d column %s", i, t.Name())
					continue
				}
				f.Geometry = geom.Geometry()
			default:
				v := string(*rb)
				switch cols[i].DatabaseTypeName() {
				case "INT", "INT4":
					f.Properties[t.Name()], _ = strconv.Atoi(v)
				case "NUMERIC", "DECIMAL": //number
					f.Properties[t.Name()], _ = strconv.ParseFloat(v, 64)
				// case "BOOL":
				// case "TIMESTAMPTZ":
				// case "_VARCHAR":
				// case "TEXT", "VARCHAR", "BIGINT":
				default:
					f.Properties[t.Name()] = v
				}
			}

		}
		fc.Append(f)
	}
	gj, err := fc.MarshalJSON()
	if err != nil {
		log.Errorf("unable to MarshalJSON of featureclection.")
		res.FailMsg(c, "unable to MarshalJSON of featureclection.")
		return
	}
	res.DoneData(c, json.RawMessage(gj))
}

func cubeQuery(c *gin.Context) {
	res := NewRes()
	var body struct {
		SQL string `form:"sql" json:"sql" binding:"required"`
	}
	err := c.Bind(&body)
	if err != nil {
		res.Fail(c, 4001)
		return
	}
	rows, err := db.Raw(body.SQL).Rows() // (*sql.Rows, error)
	if err != nil {
		log.Error(err)
		res.Fail(c, 5001)
		return
	}
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil {
		log.Error(err)
		res.Fail(c, 5001)
		return
	}
	var t [][]string
	for rows.Next() {
		// Scan needs an array of pointers to the values it is setting
		// This creates the object and sets the values correctly
		vals := make([]sql.RawBytes, len(cols))
		valsScer := make([]interface{}, len(vals))
		for i := range vals {
			valsScer[i] = &vals[i]
		}
		err = rows.Scan(valsScer...)
		if err != nil {
			log.Error(err)
		}
		var r []string
		for _, v := range vals {
			// skip nil values.
			if v == nil {
				continue
			}
			r = append(r, string(v))
		}
		if len(r) == 0 {
			continue
		}
		t = append(t, r)
	}
	res.DoneData(c, t)
}

func queryExec(c *gin.Context) {

	res := NewRes()
	var body struct {
		SQL string `form:"sql" json:"sql" binding:"required"`
	}
	err := c.Bind(&body)
	if err != nil {
		res.Fail(c, 4001)
		return
	}
	rows, err := db.Raw(body.SQL).Rows() // (*sql.Rows, error)
	if err != nil {
		log.Error(err)
		res.Fail(c, 5001)
		return
	}
	defer rows.Close()

	cols, _ := rows.ColumnTypes()
	var ams [][]interface{}
	for rows.Next() {
		// Create a slice of interface{}'s to represent each column,
		// and a second slice to contain pointers to each item in the columns slice.
		columns := make([]sql.RawBytes, len(cols))
		columnPointers := make([]interface{}, len(cols))
		for i := range columns {
			columnPointers[i] = &columns[i]
		}

		// Scan the result into the column pointers...
		if err := rows.Scan(columnPointers...); err != nil {
			log.Error(err)
			continue
		}

		// Create our map, and retrieve the value for each column from the pointers slice,
		// storing it in the map with the name of the column as the key.
		m := make([]interface{}, len(cols))
		for i, col := range columns {
			// if cols[i].Name() == "geom" || cols[i].Name() == "search" {
			// 	continue
			// }
			//"NVARCHAR", "DECIMAL", "BOOL", "INT", "BIGINT".
			v := string(col)
			switch cols[i].DatabaseTypeName() {
			case "INT", "INT4":
				m[i], _ = strconv.Atoi(v)
			case "NUMERIC", "DECIMAL": //number
				m[i], _ = strconv.ParseFloat(v, 64)
			// case "BOOL":
			// case "TIMESTAMPTZ":
			// case "_VARCHAR":
			// case "TEXT", "VARCHAR", "BIGINT":
			default:
				m[i] = v
			}
		}
		// fmt.Print(m)
		ams = append(ams, m)
	}
	res.DoneData(c, ams)
}

func queryBusiness(c *gin.Context) {
	res := NewRes()
	did := c.Param("id")
	var linkTables []string
	if did != "banks" {
		res.DoneData(c, gin.H{
			did: linkTables,
		})
		return
	}
	linkTables = viper.GetStringSlice("business.banks.linked")
	res.DoneData(c, gin.H{
		did: linkTables,
	})
}

func getBuffers(c *gin.Context) {
	res := NewRes()
	did := c.Param("id")
	rs := c.Query("radius")
	t := c.Query("type")
	bsuffix := viper.GetString("buffers.suffix")
	tbname := did + bsuffix //circle
	switch t {
	case "circle":
	case "block", "":
		bprefix := viper.GetString("buffers.prefix")
		tbname = bprefix + tbname
	// case "time", "voronoi":
	default:
		res.FailMsg(c, "unkown buffer type")
		return
	}
	r, _ := strconv.ParseFloat(rs, 64)
	if r == 0 {
		res.FailMsg(c, "invalid radius value")
		return
	}
	if code := buffering(did, r); code != 200 {
		log.Error(codes[code])
		res.Fail(c, code)
		return
	}

	fields := c.Query("fields")
	filter := c.Query("filter")

	selStr := "st_asgeojson(b.geom) as geom "

	if "" != fields {
		flds := strings.Split(fields, ",")
		if len(flds) == 1 {
			selStr = selStr + ", a." + flds[0]
		} else {
			selStr = selStr + ", a." + strings.Join(flds, ", a.")
		}
	}
	whr := " WHERE a.id = b.id "
	if "" != filter {
		whr += " AND ( " + filter + " )"
		whr = strings.Replace(whr, " id ", " a.id ", -1)
		whr = strings.Replace(whr, " id=", " a.id= ", -1)
		whr = strings.Replace(whr, " geom ", " a.geom ", -1)
		whr = strings.Replace(whr, " (geom", " (a.geom ", -1)
		whr = strings.Replace(whr, "geom) ", " a.geom) ", -1)
	}
	s := fmt.Sprintf(`SELECT %s FROM %s as a, %s as b %s;`, selStr, did, tbname, whr)
	rows, err := db.Raw(s).Rows() // (*sql.Rows, error)
	if err != nil {
		log.Error(err)
		res.Fail(c, 5001)
		return
	}
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil {
		res.Fail(c, 5001)
		return
	}
	fc := geojson.NewFeatureCollection()
	for rows.Next() {
		// Scan needs an array of pointers to the values it is setting
		// This creates the object and sets the values correctly
		vals := make([]interface{}, len(cols))
		for i := range cols {
			vals[i] = new(sql.RawBytes)
		}
		err = rows.Scan(vals...)
		if err != nil {
			log.Error(err)
		}

		// f := newFeatrue(t)
		f := geojson.NewFeature(nil)
		for i, t := range cols {
			// skip nil values.
			if vals[i] == nil {
				continue
			}
			rb, ok := vals[i].(*sql.RawBytes)
			if !ok {
				log.Errorf("Cannot convert index %d column %s to type *sql.RawBytes", i, t.Name())
				continue
			}

			switch t.Name() {
			case "geom":
				geom, err := geojson.UnmarshalGeometry([]byte(*rb))
				if err != nil {
					log.Error(err)
					log.Errorf("UnmarshalGeometry from geojson result error, index %d column %s", i, t.Name())
					continue
				}
				f.Geometry = geom.Geometry()
			default:
				v := string(*rb)
				switch cols[i].DatabaseTypeName() {
				case "INT", "INT4":
					f.Properties[t.Name()], _ = strconv.Atoi(v)
				case "NUMERIC", "DECIMAL": //number
					f.Properties[t.Name()], _ = strconv.ParseFloat(v, 64)
				// case "BOOL":
				// case "TIMESTAMPTZ":
				// case "_VARCHAR":
				// case "TEXT", "VARCHAR", "BIGINT":
				default:
					f.Properties[t.Name()] = v
				}
			}

		}
		fc.Append(f)
	}
	var extent []byte
	stbox := fmt.Sprintf(`SELECT st_asgeojson(st_extent(b.geom)) as extent FROM %s as a,%s as b %s;`, did, tbname, whr)
	db.Raw(stbox).Row().Scan(&extent) // (*sql.Rows, error)
	ext, err := geojson.UnmarshalGeometry(extent)
	if err == nil {
		fc.BBox = geojson.NewBBox(ext.Geometry().Bound())
	}
	gj, err := fc.MarshalJSON()
	if err != nil {
		log.Errorf("unable to MarshalJSON of featureclection.")
		res.FailMsg(c, "unable to MarshalJSON of featureclection.")
		return
	}
	c.JSON(http.StatusOK, json.RawMessage(gj))
}

func getModels(c *gin.Context) {
	res := NewRes()
	did := c.Param("id")
	fields := c.Query("fields")
	filter := c.Query("filter")
	needCacl := c.Query("cacl")

	if code := checkDataset(did); code != 200 {
		res.Fail(c, code)
		return
	}
	if needCacl != "" {
		switch did {
		case "m1":
			err := calcM1()
			if err != nil {
				log.Error(err)
				res.FailErr(c, err)
				// return
			}
		case "m2":
			err := calcM2()
			if err != nil {
				log.Error(err)
				res.FailErr(c, err)
				// return
			}
		case "m3":
			err := calcM3()
			if err != nil {
				log.Error(err)
				res.FailErr(c, err)
				// return
			}
		case "m4":
			err := calcM4()
			if err != nil {
				log.Error(err)
				res.FailErr(c, err)
				// return
			}
		case "m5":
			err := calcM5()
			if err != nil {
				log.Error(err)
				res.FailErr(c, err)
				// return
			}
		default:
			res.FailMsg(c, "unkown model name")
			return
		}
	}
	if fields == "" {
		fields = " * "
	}
	if filter != "" {
		filter = " WHERE " + filter
	}

	s := fmt.Sprintf(`SELECT %s FROM %s %s;`, fields, did, filter)
	rows, err := db.Raw(s).Rows() // (*sql.Rows, error)
	if err != nil {
		log.Error(err)
		res.Fail(c, 5001)
		return
	}
	defer rows.Close()
	cols, _ := rows.ColumnTypes()
	var ams []map[string]interface{}
	for rows.Next() {
		// Create a slice of interface{}'s to represent each column,
		// and a second slice to contain pointers to each item in the columns slice.
		columns := make([]sql.RawBytes, len(cols))
		columnPointers := make([]interface{}, len(cols))
		for i := range columns {
			columnPointers[i] = &columns[i]
		}

		// Scan the result into the column pointers...
		if err := rows.Scan(columnPointers...); err != nil {
			log.Error(err)
			continue
		}

		// Create our map, and retrieve the value for each column from the pointers slice,
		// storing it in the map with the name of the column as the key.
		m := make(map[string]interface{})
		for i, col := range columns {
			// if col == nil {
			// continue
			// }
			//"NVARCHAR", "DECIMAL", "BOOL", "INT", "BIGINT".
			v := string(col)
			switch cols[i].DatabaseTypeName() {
			case "INT", "INT4":
				m[cols[i].Name()], _ = strconv.Atoi(v)
			case "NUMERIC", "DECIMAL": //number
				m[cols[i].Name()], _ = strconv.ParseFloat(v, 64)
			// case "BOOL":
			// case "TIMESTAMPTZ":
			// case "_VARCHAR":
			// case "TEXT", "VARCHAR", "BIGINT":
			default:
				m[cols[i].Name()] = v
			}
		}
		// fmt.Print(m)
		ams = append(ams, m)
	}
	c.JSON(http.StatusOK, ams)
}

func searchGeos(c *gin.Context) {
	// res := NewRes()
	searchType := c.Param("name")
	keyword := c.Query("keyword")
	var ams []map[string]interface{}

	log.Println("***********", keyword, "**************")
	if searchType != "search" || keyword == "" {
		// res.Fail(c, 4001)
		c.JSON(http.StatusOK, ams)
		return
	}
	search := func(s string, keyword string) {
		stmt, err := db.DB().Prepare(s)
		if err != nil {
			log.Error(err)
			return
		}
		defer stmt.Close()
		rows, err := stmt.Query(keyword)
		if err != nil {
			log.Error(err)
			return
		}
		defer rows.Close()

		cols, _ := rows.ColumnTypes()
		for rows.Next() {
			// Create a slice of interface{}'s to represent each column,
			// and a second slice to contain pointers to each item in the columns slice.
			columns := make([]sql.RawBytes, len(cols))
			columnPointers := make([]interface{}, len(cols))
			for i := range columns {
				columnPointers[i] = &columns[i]
			}

			// Scan the result into the column pointers...
			if err := rows.Scan(columnPointers...); err != nil {
				log.Error(err)
				continue
			}

			// Create our map, and retrieve the value for each column from the pointers slice,
			// storing it in the map with the name of the column as the key.
			m := make(map[string]interface{})
			for i, col := range columns {
				if col == nil {
					continue
				}
				//"NVARCHAR", "DECIMAL", "BOOL", "INT", "BIGINT".
				v := string(col)
				switch cols[i].DatabaseTypeName() {
				case "INT", "INT4":
					m[cols[i].Name()], _ = strconv.Atoi(v)
				case "NUMERIC", "DECIMAL": //number
					m[cols[i].Name()], _ = strconv.ParseFloat(v, 64)
				// case "BOOL":
				// case "TIMESTAMPTZ":
				// case "_VARCHAR":
				// case "TEXT", "VARCHAR", "BIGINT":
				default:
					m[cols[i].Name()] = v
				}
			}
			// fmt.Print(m)
			ams = append(ams, m)
		}
	}

	st := fmt.Sprintf(`SELECT id,名称,st_asgeojson(geom) as geom FROM regions WHERE 名称 ~ $1;`)
	fmt.Println(st)
	search(st, keyword)
	if len(ams) > 10 {
		c.JSON(http.StatusOK, ams)
		return
	}
	bbox := c.Query("bbox")
	var gfilter string
	if bbox != "" {
		gfilter = fmt.Sprintf(` geom && st_makeenvelope(%s,4326) AND `, bbox)
	}
	limit := c.Query("limit")
	var limiter string
	if limit != "" {
		limiter = fmt.Sprintf(` LIMIT %s `, limit)
	}
	st = fmt.Sprintf(`SELECT id,名称,st_asgeojson(geom) as geom,s 搜索 
	FROM (SELECT id,名称,geom,unnest(search) s FROM banks) x WHERE %s s ~ $1 GROUP BY id,名称,geom,s %s;`, gfilter, limiter)
	fmt.Println(st)
	search(st, keyword)
	if len(ams) > 10 {
		c.JSON(http.StatusOK, ams)
		return
	}
	st = fmt.Sprintf(`SELECT id,名称,st_asgeojson(geom) as geom,s 搜索 
	FROM (SELECT id,名称,geom,unnest(search) s FROM others) x WHERE %s s ~ $1 GROUP BY id,名称,geom,s %s;`, gfilter, limiter)
	fmt.Println(st)
	search(st, keyword)
	if len(ams) > 10 {
		c.JSON(http.StatusOK, ams)
		return
	}
	st = fmt.Sprintf(`SELECT 名称,st_asgeojson(geom) as geom,s 搜索 
	FROM (SELECT 名称,geom,unnest(search) s FROM pois) x WHERE %s s ~ $1 GROUP BY 名称,geom,s %s;`, gfilter, limiter)
	fmt.Println(st)
	search(st, keyword)
	if len(ams) > 10 {
		c.JSON(http.StatusOK, ams)
		return
	}
	c.JSON(http.StatusOK, ams)
}

func upInsertDataset(c *gin.Context) {
	res := NewRes()
	did := c.Param("id")

	if code := checkDataset(did); code != 200 {
		res.Fail(c, code)
		return
	}

	bank := &Bank{}
	err := c.BindJSON(bank)
	if err != nil {
		log.Error(err)
		res.Fail(c, 4001)
		return
	}

	bank.Search = []string{bank.No, bank.Name, bank.Region, bank.Type, bank.Manager}

	if db.Table(did).Where("id = ?", bank.ID).First(&Bank{}).RecordNotFound() {
		db.Omit("geom").Create(bank)
	} else {
		err := db.Table(did).Where("id = ?", bank.ID).Update(bank).Error
		if err != nil {
			log.Error(err)
			res.Fail(c, 5001)
			return
		}
	}

	if bank.X < -180 || bank.X > 180 || bank.Y < -85 || bank.Y > 85 {
		log.Errorf("x, y must be reasonable values, name")
		res.FailMsg(c, "x, y must be reasonable values")
		return
	}
	stgeo := fmt.Sprintf(`UPDATE %s SET geom = ST_GeomFromText('POINT(' || x || ' ' || y || ')',4326) WHERE id=%d;`, did, bank.ID)
	result := db.Exec(stgeo)
	if result.Error != nil {
		log.Errorf("update %s create geom error:%s", did, result.Error.Error())
		res.Fail(c, 5001)
		return
	}

	res.DoneData(c, gin.H{
		"id": bank.ID,
	})
}

func deleteDatasets(c *gin.Context) {
	res := NewRes()
	did := c.Param("id")

	if code := checkDataset(did); code != 200 {
		res.Fail(c, code)
		return
	}

	var body struct {
		ID string `form:"id" json:"id" binding:"required"`
	}
	err := c.Bind(&body)
	if err != nil {
		res.Fail(c, 4001)
		return
	}
	err = db.Where("id = ?", body.ID).Delete(&Bank{}).Error
	if err != nil {
		log.Errorf("delete data : %s; dataid: %s", err, body.ID)
		res.Fail(c, 5001)
		return
	}
	res.Done(c, "")
}

func deleteFeatures(c *gin.Context) {
	res := NewRes()
	did := c.Param("id")

	if code := checkDataset(did); code != 200 {
		res.Fail(c, code)
		return
	}

	var body struct {
		ID string `form:"id" json:"id" binding:"required"`
	}
	err := c.Bind(&body)
	if err != nil {
		res.Fail(c, 4001)
		return
	}
	err = db.Where("id = ?", body.ID).Delete(&Bank{}).Error
	if err != nil {
		log.Errorf("delete data : %s; dataid: %s", err, body.ID)
		res.Fail(c, 5001)
		return
	}
	res.Done(c, "")
}

func createTileLayer(c *gin.Context) {
	res := NewRes()
	uid := c.Param("user")
	did := c.Param("id")
	set := userSet.dataset(uid, did)
	if set == nil {
		res.Fail(c, 4044)
		return
	}
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
}

func getTileLayer(c *gin.Context) {
	res := NewRes()
	uid := c.Param("user")
	log.Info(uid)
	did := c.Param("id")
	// ds := userSet.dataset(uid, did)
	// if ds == nil {
	// 	res.Fail(c, 4044)
	// 	return
	// }
	// lookup our Map
	placeholder, _ := strconv.ParseUint(c.Param("z"), 10, 32)
	z := uint(placeholder)
	placeholder, _ = strconv.ParseUint(c.Param("x"), 10, 32)
	x := uint(placeholder)
	ypbf := c.Param("y")
	ys := strings.Split(ypbf, ".")
	if len(ys) != 2 {
		res.Fail(c, 404)
		return
	}
	placeholder, _ = strconv.ParseUint(ys[0], 10, 32)
	y := uint(placeholder)
	type A struct {
		*atlas.Atlas
	}
	a := A{}
	m, err := a.Map(did)
	if err != nil {
		errMsg := fmt.Sprintf("map (%v) not configured. check your config file", did)
		log.Errorf(errMsg)
		http.Error(c.Writer, errMsg, http.StatusNotFound)
		return
	}

	// filter down the layers we need for this zoom
	m = m.FilterLayersByZoom(z)
	if len(m.Layers) == 0 {
		log.Errorf("map (%v) has no layers, at zoom %v", did, z)
		return
	}
	layers := c.Query("layers")
	if layers != "" {
		m = m.FilterLayersByName(layers)
		if len(m.Layers) == 0 {
			log.Errorf("map (%v) has no layers, for LayerName %v at zoom %v", did, layers, z)
			return
		}
	}

	tile := slippy.NewTile(z, x, y, TileBuffer, tegola.WebMercator)

	{
		// Check to see that the zxy is within the bounds of the map.
		textent := geom.Extent(tile.Bounds())
		if !m.Bounds.Contains(&textent) {
			return
		}
	}

	// check for the debug query string
	if true {
		m = m.AddDebugLayers()
	}

	pbyte, err := m.Encode(c.Request.Context(), tile)
	if err != nil {
		switch err {
		case context.Canceled:
			// TODO: add debug logs
			return
		default:
			errMsg := fmt.Sprintf("error marshalling tile: %v", err)
			log.Error(errMsg)
			http.Error(c.Writer, errMsg, http.StatusInternalServerError)
			return
		}
	}

	// mimetype for mapbox vector tiles
	// https://www.iana.org/assignments/media-types/application/vnd.mapbox-vector-tile
	c.Header("Content-Type", mvt.MimeType)
	c.Header("Content-Encoding", "gzip")
	// c.Header("Content-Type", "application/x-protobuf")
	c.Header("Content-Length", fmt.Sprintf("%d", len(pbyte)))
	c.Writer.WriteHeader(http.StatusOK)
	c.Writer.Write(pbyte)
	log.Info(len(pbyte))
	// check for tile size warnings
	if len(pbyte) > server.MaxTileSize {
		log.Infof("tile z:%v, x:%v, y:%v is rather large - %vKb", z, x, y, len(pbyte)/1024)
	}
}

func createTileMap(c *gin.Context) {
}

func getTileMap(c *gin.Context) {
	res := NewRes()
	uid := c.Param("user")
	log.Info(uid)
	did := c.Param("id")
	// ds := userSet.dataset(uid, did)
	// if ds == nil {
	// 	res.Fail(c, 4044)
	// 	return
	// }
	// lookup our Map
	placeholder, _ := strconv.ParseUint(c.Param("z"), 10, 32)
	z := uint(placeholder)
	placeholder, _ = strconv.ParseUint(c.Param("x"), 10, 32)
	x := uint(placeholder)
	ypbf := c.Param("y")
	ys := strings.Split(ypbf, ".")
	if len(ys) != 2 {
		res.Fail(c, 404)
		return
	}
	placeholder, _ = strconv.ParseUint(ys[0], 10, 32)
	y := uint(placeholder)
	type A struct {
		*atlas.Atlas
	}
	a := A{}
	m, err := a.Map(did)
	if err != nil {
		errMsg := fmt.Sprintf("map (%v) not configured. check your config file", did)
		log.Errorf(errMsg)
		http.Error(c.Writer, errMsg, http.StatusNotFound)
		return
	}

	// filter down the layers we need for this zoom
	m = m.FilterLayersByZoom(z)
	if len(m.Layers) == 0 {
		log.Errorf("map (%v) has no layers, at zoom %v", did, z)
		return
	}
	layers := c.Query("layers")
	if layers != "" {
		m = m.FilterLayersByName(layers)
		if len(m.Layers) == 0 {
			log.Errorf("map (%v) has no layers, for LayerName %v at zoom %v", did, layers, z)
			return
		}
	}

	tile := slippy.NewTile(z, x, y, TileBuffer, tegola.WebMercator)

	{
		// Check to see that the zxy is within the bounds of the map.
		textent := geom.Extent(tile.Bounds())
		if !m.Bounds.Contains(&textent) {
			return
		}
	}

	// check for the debug query string
	if true {
		m = m.AddDebugLayers()
	}

	pbyte, err := m.Encode(c.Request.Context(), tile)
	if err != nil {
		switch err {
		case context.Canceled:
			// TODO: add debug logs
			return
		default:
			errMsg := fmt.Sprintf("error marshalling tile: %v", err)
			log.Error(errMsg)
			http.Error(c.Writer, errMsg, http.StatusInternalServerError)
			return
		}
	}

	// mimetype for mapbox vector tiles
	// https://www.iana.org/assignments/media-types/application/vnd.mapbox-vector-tile
	c.Header("Content-Type", mvt.MimeType)
	c.Header("Content-Encoding", "gzip")
	// c.Header("Content-Type", "application/x-protobuf")
	c.Header("Content-Length", fmt.Sprintf("%d", len(pbyte)))
	c.Writer.WriteHeader(http.StatusOK)
	c.Writer.Write(pbyte)
	log.Info(len(pbyte))
	// check for tile size warnings
	if len(pbyte) > server.MaxTileSize {
		log.Infof("tile z:%v, x:%v, y:%v is rather large - %vKb", z, x, y, len(pbyte)/1024)
	}
}
