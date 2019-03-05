package main

import (
	"bytes"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/atlasdatatech/chardet"
	"github.com/axgle/mahonia"
	"github.com/jinzhu/gorm"
	shp "github.com/jonas-p/go-shp"
	"github.com/spf13/viper"

	_ "github.com/mattn/go-sqlite3" // import sqlite3 driver
	// "github.com/paulmach/orb/encoding/wkb"

	"github.com/paulmach/orb/geojson"
	log "github.com/sirupsen/logrus"
)

// CRS coordinate reference system
type CRS string

// Supported CRSs
const (
	WGS84    CRS = "WGS84"
	CGCS2000     = "CGCS2000"
	GCJ02        = "GCJ02"
	BD09         = "BD09"
)

//CRSs 支持的坐标系
var CRSs = []string{"WGS84", "CGCS2000", "GCJ02", "BD09"}

//Encoding text encoding
type Encoding string

// Supported encodings
const (
	UTF8    Encoding = "utf-8"
	GBK              = "gbk"
	BIG5             = "big5"
	GB18030          = "gb18030"
)

//Encodings 支持的编码格式
var Encodings = []string{"utf-8", "gbk", "big5", "gb18030"}

// Datafile 数据文件
type Datafile struct {
	ID        string  `json:"id"`
	Owner     string  `json:"owner"`
	Tag       string  `json:"tag"`
	Name      string  `json:"name"`
	Alias     string  `json:"alias"`
	Format    string  `json:"format"`
	Path      string  `json:"path"`
	Size      int64   `json:"size"`
	Encoding  string  `json:"encoding"`
	Crs       string  `json:"crs"` //WGS84,CGCS2000,GCJ02,BD09
	Geotype   string  `json:"geotype"`
	Lon       string  `json:"lon" form:"lon"`
	Lat       string  `json:"lat" form:"lat"`
	Fields    []Field `json:"fields"`
	Type      string  `json:"type"`
	Overwrite bool    `json:"overwrite"`
	CreatedAt time.Time
	DeletedAt *time.Time `sql:"index"`
}

// DatafileBind 数据预览
type DatafileBind struct {
	ID        string     `json:"id" form:"id" binding:"required"`
	Tags      []string   `json:"tags" form:"tags" binding:"required"`
	Name      string     `json:"name" form:"name" binding:"required"`
	Alias     string     `json:"alias" form:"alias" binding:"required"`
	Encodings []string   `json:"encodings" form:"encodings" binding:"required"`
	Crss      []string   `json:"crss" form:"crss" binding:"required"` //WGS84,CGCS2000,GCJ02,BD09
	Lon       string     `json:"lon" form:"lon"`
	Lat       string     `json:"lat" form:"lat"`
	Geotype   string     `json:"geotype" form:"geotype"`
	Fields    []Field    `json:"fields" form:"fields" binding:"required"`
	Rows      [][]string `json:"rows" form:"rows"`
	Count     int        `json:"count" form:"count"`
	Update    bool       `json:"update" form:"update"`
}

// Task 数据导入信息预览
type Task struct {
	ID       string        `json:"id" form:"id" binding:"required"`
	Name     string        `json:"name" form:"name" binding:"required"`
	Type     string        `json:"type" form:"name" binding:"required"`
	Fail     int           `json:"fail" form:"fail"`
	Succeed  int           `json:"succeed" form:"succeed"`
	Count    int           `json:"count" form:"count"`
	Progress int           `json:"progress" form:"progress"`
	State    string        `json:"state"`
	Err      string        `json:"err"`
	Pipe     chan struct{} `json:"-" form:"-" gorm:"-"`
}

// LoadDatafile 加载数据文件
func LoadDatafile(datafile string) ([]*Datafile, error) {
	// 获取所有记录
	var datafiles []*Datafile
	fstat, err := os.Stat(datafile)
	if err != nil {
		log.Errorf("LoadDataset, read data file stat error,  details: %s", err)
		return datafiles, err
	}
	ext := filepath.Ext(datafile)
	lext := strings.ToLower(ext)
	switch lext {
	case ".csv", ".geojson", ".zip", ".kml", ".gpx":
	default:
		log.Errorf("LoadDataset, unkown datafile format, details: %s", datafile)
		return datafiles, fmt.Errorf("未知数据格式, 请使用csv/geojson(json)/shapefile(zip)数据")
	}
	base := filepath.Base(datafile)
	name := strings.TrimSuffix(base, ext)
	id := filepath.Ext(name)
	if lext == ".zip" {
		var getDatafiles func(dir string) map[string]int64
		getDatafiles = func(dir string) map[string]int64 {
			files := make(map[string]int64)
			fileInfos, err := ioutil.ReadDir(dir)
			if err != nil {
				log.Error(err)
				return files
			}
			for _, fileInfo := range fileInfos {
				if fileInfo.IsDir() {
					subfiles := getDatafiles(filepath.Join(dir, fileInfo.Name()))
					for k, v := range subfiles {
						files[k] = v
					}
				}
				ext := filepath.Ext(fileInfo.Name())
				//处理zip内部数据文件
				switch strings.ToLower(ext) {
				case ".csv", ".geojson", ".kml", ".gpx":
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
		subdir := UnZipToDir(datafile)
		zipDatafiles := getDatafiles(subdir)
		for datafile, size := range zipDatafiles {
			newName := strings.TrimSuffix(filepath.Base(datafile), filepath.Ext(datafile))
			df := &Datafile{
				ID:      newName + id,
				Owner:   ATLAS,
				Name:    newName,
				Tag:     name,
				Geotype: "vector",
				Format:  strings.ToLower(filepath.Ext(datafile)),
				Path:    datafile,
				Size:    size,
			}
			datafiles = append(datafiles, df)
		}
	} else {
		df := &Datafile{
			ID:      name,
			Owner:   ATLAS,
			Name:    strings.TrimSuffix(name, id),
			Geotype: "vector",
			Format:  lext,
			Path:    datafile,
			Size:    fstat.Size(),
		}
		datafiles = append(datafiles, df)
	}

	return datafiles, nil
}

//Update 从datafilebind更新datafile
//create or update upload data file info into database
func (df *Datafile) Update(dfb *DatafileBind) error {
	if df == nil {
		return fmt.Errorf("datafile may not be nil")
	}
	if dfb == nil {
		return fmt.Errorf("datafilebind may not be nil")
	}
	df.ID = dfb.ID
	df.Name = dfb.Name
	df.Alias = dfb.Alias
	df.Encoding = dfb.Encodings[0]
	df.Crs = dfb.Crss[0]
	df.Geotype = dfb.Geotype
	df.Lat = dfb.Lat
	df.Lon = dfb.Lon
	df.Fields = dfb.Fields
	if dfb.Tags != nil && len(dfb.Tags) > 0 {
		df.Tag = dfb.Tags[0]
	}
	return nil
}

//UpInsert 更新/创建上传数据文件信息
//create or update upload data file info into database
func (df *Datafile) UpInsert() error {
	if df == nil {
		return fmt.Errorf("datafile may not be nil")
	}
	tmp := &Datafile{}
	err := db.Where("id = ?", df.ID).First(tmp).Error
	if err != nil {
		if gorm.IsRecordNotFoundError(err) {
			err = db.Create(df).Error
			if err != nil {
				return err
			}
		}
		return err
	}
	err = db.Model(&Datafile{}).Update(df).Error
	if err != nil {
		return err
	}
	return nil
}

//getEncoding guess data file encoding
func (df *Datafile) getTags() []string {
	var tags []string
	if df == nil {
		log.Errorf("datafile may not be nil")
		return tags
	}

	datafiles := []Datafile{}
	err := db.Where("owner = ?", df.Owner).Find(&datafiles).Error
	if err != nil {
		if gorm.IsRecordNotFoundError(err) {
			log.Errorf(`getTags, can not find user datafile, user: %s`, df.Owner)
			return tags
		}
		log.Errorf(`getTags, get data file info error, details: %s`, err)
		return tags
	}
	mtags := make(map[string]int)
	for _, datafile := range datafiles {
		tag := datafile.Tag
		if tag == "" {
			continue
		}
		_, ok := mtags[tag]
		if ok {
			mtags[tag]++
		} else {
			mtags[tag] = 1
		}
	}
	type kv struct {
		Key   string
		Value int
	}

	var ss []kv
	for k, v := range mtags {
		ss = append(ss, kv{k, v})
	}

	sort.Slice(ss, func(i, j int) bool {
		return ss[i].Value > ss[j].Value
	})

	for _, kv := range ss {
		tags = append(tags, kv.Key)
	}
	return tags
}

//getPreview get data preview context
func (df *Datafile) getPreview() *DatafileBind {
	dp := &DatafileBind{}
	if df == nil {
		log.Errorf("datafile may not be nil")
		return dp
	}
	switch df.Format {
	case ".csv":
		buf, err := df.getDatabuf()
		if err != nil {
			log.Errorf(`getPreview, get databuf error, details:%s`, err)
			return dp
		}
		csvReader := csv.NewReader(bytes.NewReader(buf))
		headers, err := csvReader.Read()
		if err != nil {
			log.Errorf(`getPreview, csvReader read headers failed: %s`, err)
			return dp
		}

		var records [][]string
		var rowNum, preNum int
		for {
			row, err := csvReader.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				continue
			}
			if preNum < 7 {
				records = append(records, row)
				preNum++
			}
			rowNum++
		}

		findType := func(arr []string) FieldType {
			var hasFloats, hasInts, hasBools, hasStrings bool
			for _, str := range arr {
				if str == "" {
					continue
				}
				if _, err := strconv.Atoi(str); err == nil {
					hasInts = true
					continue
				}
				if _, err := strconv.ParseFloat(str, 64); err == nil {
					hasFloats = true
					continue
				}
				if str == "true" || str == "false" {
					hasBools = true
					continue
				}
				hasStrings = true
			}
			switch {
			case hasStrings:
				return String
			case hasBools:
				return Bool
			case hasFloats:
				return Float
			case hasInts:
				return Int
			default: //all null or string data
				return String
			}
		}
		types := make([]FieldType, len(headers))
		for i := range headers {
			col := make([]string, len(records))
			for j := 0; j < len(records); j++ {
				col[j] = records[j][i]
			}
			types[i] = findType(col)
		}

		var fields []Field
		for i, name := range headers {
			fields = append(fields, Field{
				Name: name,
				Type: string(types[i])})
		}

		getColumn := func(cols []string, names []string) string {
			for _, c := range cols {
				for _, n := range names {
					if c == strings.ToLower(n) {
						return n
					}
				}
			}
			return ""
		}

		detechColumn := func(min float64, max float64) string {
			for i, name := range headers {
				num := 0
				for _, row := range records {
					f, err := strconv.ParseFloat(row[i], 64)
					if err != nil || f < min || f > max {
						break
					}
					num++
				}
				if num == len(records) {
					return name
				}
			}
			return ""
		}

		xcols := []string{"x", "lon", "longitude", "经度"}
		x := getColumn(xcols, headers)
		if x == "" {
			x = detechColumn(73, 135)
		}
		ycols := []string{"y", "lat", "latitude", "纬度"}
		y := getColumn(ycols, headers)
		if y == "" {
			y = detechColumn(18, 54)
		}

		dp.Count = rowNum
		dp.Lon = x
		dp.Lat = y
		dp.Geotype = "Point"
		dp.Fields = fields
		dp.Rows = records
		dp.Update = false
	case ".geojson":
		buf, err := df.getDatabuf()
		if err != nil {
			log.Errorf(`getPreview, get databuf error, details:%s`, err)
			return dp
		}

		// A FeatureCollection correlates to a GeoJSON feature collection.
		var gjson struct {
			Type     string            `json:"type"`
			Features []json.RawMessage `json:"features"`
		}

		s := time.Now()
		err = json.Unmarshal(buf, &gjson)
		if err != nil {
			log.Fatalln("error:", err)
		}
		fmt.Println(time.Since(s))

		fcount := len(gjson.Features)
		if fcount == 0 {
			log.Error(`empty datafile`)
			return dp
		}

		var fields []Field
		f, err := geojson.UnmarshalFeature(gjson.Features[0])
		if err != nil {
			log.Errorf(`UnmarshalFeature error, details:%s`, err)
			return dp
		}
		geoType := f.Geometry.GeoJSONType()
		for k, v := range f.Properties {
			var t string
			switch v.(type) {
			case bool:
				t = "bool" //or 'timestamp with time zone'
			case float64:
				t = "float"
			default: //string/map[string]interface{}/nil
				t = "string"
			}
			fields = append(fields, Field{
				Name: k,
				Type: t,
			})
		}

		prepRow := func(f *geojson.Feature) []string {
			var row []string
			for _, v := range f.Properties {
				var s string
				switch v.(type) {
				case bool:
					val, ok := v.(bool) // Alt. non panicking version
					if ok {
						s = strconv.FormatBool(val)
					} else {
						s = "null"
					}
				case float64:
					val, ok := v.(float64) // Alt. non panicking version
					if ok {
						s = strconv.FormatFloat(val, 'g', -1, 64)
					} else {
						s = "null"
					}
				default: //string/map[string]interface{}/nil
					if v == nil {
						s = ""
					} else {
						s = v.(string)
					}
				}
				row = append(row, s)
			}
			return row
		}

		var rows [][]string
		if fcount > 7 {
			for _, p := range []int{fcount / 7, 2 * fcount / 7, 3 * fcount / 7, 4 * fcount / 7, 5 * fcount / 7, 6 * fcount / 7, fcount - 1} {
				f, err := geojson.UnmarshalFeature(gjson.Features[p])
				if err != nil {
					log.Errorf(`UnmarshalFeature error, details:%s`, err)
					continue
				}
				rows = append(rows, prepRow(f))
			}
		} else {
			for _, rawf := range gjson.Features {
				f, err := geojson.UnmarshalFeature(rawf)
				if err != nil {
					log.Errorf(`UnmarshalFeature error, details:%s`, err)
					continue
				}
				rows = append(rows, prepRow(f))
			}
		}
		dp.Geotype = geoType
		dp.Count = fcount
		dp.Fields = fields
		dp.Rows = rows
		dp.Update = false
	case ".shp":
		shape, err := shp.Open(df.Path)
		// open a shapefile for reading
		if err != nil {
			log.Fatal(err)
			return dp
		}
		defer shape.Close()

		// fields from the attribute table (DBF)
		shpfields := shape.Fields()
		fcount := shape.AttributeCount()
		if fcount == 0 {
			log.Error(`empty datafile`)
			return dp
		}
		var fields []Field
		for _, v := range shpfields {
			var t string
			switch v.Fieldtype {
			case 'C':
				t = "string"
			case 'N':
				t = "int"
			case 'F':
				t = "float"
			case 'D':
				t = "date"
			}
			fields = append(fields, Field{
				Name: v.String(),
				Type: t,
			})
		}
		//设置默认编码为UTF-8
		if df.Encoding == "" {
			df.Encoding = "utf-8"
		}
		decoder := mahonia.NewDecoder(df.Encoding)

		var rows [][]string
		if fcount > 7 {
			for _, p := range []int{fcount / 7, 2 * fcount / 7, 3 * fcount / 7, 4 * fcount / 7, 5 * fcount / 7, 6 * fcount / 7, fcount - 1} {
				var row []string
				for k := range fields {
					val := shape.ReadAttribute(p, k)
					if df.Encoding != "utf-8" {
						row = append(row, decoder.ConvertString(val))
					} else {
						row = append(row, val)
					}
				}
				rows = append(rows, row)
			}
		} else {
			for shape.Next() {
				var row []string
				for k := range fields {
					val := shape.Attribute(k)
					if df.Encoding != "utf-8" {
						row = append(row, decoder.ConvertString(val))
					} else {
						row = append(row, val)
					}
				}
				rows = append(rows, row)
			}
		}
		var geoType string
		switch shape.GeometryType {
		case 1: //POINT
			geoType = "Point"
		case 3: //POLYLINE
			geoType = "LineString"
		case 5: //POLYGON
			geoType = "MultiPolygon"
		case 8: //MULTIPOINT
			geoType = "MultiPoint"
		}

		dp.Geotype = geoType
		dp.Count = fcount
		dp.Fields = fields
		dp.Rows = rows
		dp.Update = false
	}

	aHead := func(slc []string, cur string) []string {

		if len(slc) == 0 || cur == "" || slc[0] == cur {
			return slc
		}
		if slc[len(slc)-1] == cur {
			slc = append([]string{cur}, slc[:len(slc)-1]...)
			return slc
		}
		for p, v := range slc {
			if v == cur {
				slc = append([]string{cur}, append(slc[:p], slc[p+1:]...)...)
				break
			}
		}
		return slc
	}

	dp.ID = df.ID
	dp.Name = df.Name
	dp.Alias = df.Alias
	dp.Tags = aHead(df.getTags(), df.Tag)
	dp.Encodings = aHead(Encodings, df.Encoding)
	dp.Crss = aHead(CRSs, df.Crs)

	// copy(dp.Rows, frame.Records())
	return dp
}

//dataImport import geojson or csv data, can transform from gcj02 or bd09
func (df *Datafile) dataImport() *Task {
	task := &Task{
		ID:   df.ID,
		Type: "dsimport",
		Pipe: make(chan struct{}),
	}
	//任务队列
	select {
	case taskQueue <- task:
		// default:
		// 	log.Warningf("task queue overflow, request refused...")
		// 	task.State = "task queue overflow, request refused"
		// 	return task, fmt.Errorf("task queue overflow, request refused")
	}
	//任务集
	taskSet.Store(task.ID, task)
	go func(df *Datafile, ts *Task) {
		tableName := strings.ToLower(df.ID)
		switch df.Format {
		case ".csv", ".geojson":
			if df.Overwrite {
				st := fmt.Sprintf(`DELETE FROM datasets WHERE id='%s';`, df.ID)
				err := db.Exec(st).Error
				if err != nil {
					log.Errorf(`dataImport, delete dataset error, details:%s`, err)
				}
				err = db.Exec(fmt.Sprintf(`DROP TABLE if EXISTS "%s";`, tableName)).Error
				if err != nil {
					log.Errorf(`dataImport, drop table error, details:%s`, err)
				}
				//csv geom type is "Point"
				err = createTable(tableName, df.Fields, df.Geotype)
				if err != nil {
					log.Errorf(`importData, create table error, details:%s`, err)
					task.Err = err.Error()
					task.Pipe <- struct{}{}
					return
				}
			}

			prepHeader := func(fields []Field) []string {
				var headers []string
				for _, v := range fields {
					headers = append(headers, v.Name)
				}
				return headers
			}
			headers := prepHeader(df.Fields)

			st := fmt.Sprintf(`SELECT %s FROM "%s" LIMIT 0`, strings.Join(headers, ","), tableName)
			rows, err := db.Raw(st).Rows() // (*sql.Rows, error)
			if err != nil {
				task.Err = err.Error()
				task.Pipe <- struct{}{}
				return
			}
			defer rows.Close()
			cols, err := rows.ColumnTypes()
			if err != nil {
				task.Err = err.Error()
				task.Pipe <- struct{}{}
				return
			}
			switch df.Format {
			case ".csv":
				formatBool := func(v string) string {
					if v == "" {
						return "null"
					}
					str := strings.ToLower(v)
					switch str {
					case "true", "false", "yes", "no", "1", "0":
					default:
						return "null"
					}
					return "'" + str + "'"
				}
				formatInt := func(v string) string {
					if v == "" {
						return "null"
					}
					i64, err := strconv.ParseInt(v, 10, 64)
					if err != nil {
						f, err := strconv.ParseFloat(v, 64)
						if err != nil {
							return "null"
						}
						i64 = int64(f)
					}
					return strconv.FormatInt(i64, 10)
				}
				formatFloat := func(v string) string {
					if v == "" {
						return "null"
					}
					f, err := strconv.ParseFloat(v, 64)
					if err != nil {
						return "null"
					}
					return strconv.FormatFloat(f, 'g', -1, 64)
				}
				formatDate := func(v string) string {
					if v == "" {
						return "null"
					}
					//string shoud filter the invalid time values
					return "'" + v + "'"
				}
				formatString := func(v string) string {
					if v == "" {
						return "null"
					}
					return "'" + v + "'"
				}
				prepValues := func(row []string, cols []*sql.ColumnType) string {
					var vals []string
					for i, col := range cols {
						// fmt.Println(i, col.DatabaseTypeName(), col.Name())
						var s string
						switch col.DatabaseTypeName() {
						case "BOOL":
							s = formatBool(row[i])
						case "INT4":
							s = formatInt(row[i])
						case "NUMERIC": //number
							s = formatFloat(row[i])
						case "TIMESTAMPTZ":
							s = formatDate(row[i])
						default: //string->"TEXT" "VARCHAR","BOOL",datetime->"TIMESTAMPTZ",pq.StringArray->"_VARCHAR"
							s = formatString(row[i])
						}
						vals = append(vals, s)
					}
					return strings.Join(vals, ",")
				}
				t := time.Now()
				buf, err := df.getDatabuf()
				if err != nil {
					task.Err = `get databuf error`
					task.Pipe <- struct{}{}
					return
				}
				csvReader := csv.NewReader(bytes.NewReader(buf))
				csvHeaders, err := csvReader.Read()
				if err != nil {
					task.Err = fmt.Sprintf(`dataImport, csvReader read headers failed: %s`, err)
					task.Pipe <- struct{}{}
					return
				}
				if len(headers) != len(csvHeaders) {
					log.Errorf(`dataImport, dbfield len != csvheader len: %s`, err)
					task.Err = `dbfield len != csvheader len`
					task.Pipe <- struct{}{}
					return
				}

				prepIndex := func(headers []string, name string) int {
					for i, col := range headers {
						if col == name {
							return i
						}
					}
					return -1
				}
				ix := prepIndex(headers, df.Lon)
				iy := prepIndex(headers, df.Lat)
				isgeom := (ix != -1 && iy != -1)
				if isgeom {
					headers = append(headers, "geom")
				}
				for {
					_, err := csvReader.Read()
					if err == io.EOF {
						break
					}
					if err != nil {
						continue
					}
					task.Count++
				}
				if task.Count == 0 {
					task.Err = `empty dataset`
					task.Pipe <- struct{}{}
					return
				}
				tt := time.Since(t)
				log.Info(`process headers and get count, `, tt)
				count := 0
				csvReader = csv.NewReader(bytes.NewReader(buf))
				_, err = csvReader.Read()
				var vals []string
				task.State = "processing"
				t = time.Now()
				for {
					row, err := csvReader.Read()
					if err == io.EOF {
						break
					}
					count++
					if err != nil {
						continue
					}
					rval := prepValues(row, cols)
					if isgeom {
						x, _ := strconv.ParseFloat(row[ix], 64)
						y, _ := strconv.ParseFloat(row[iy], 64)
						switch df.Crs {
						case GCJ02:
							x, y = Gcj02ToWgs84(x, y)
						case BD09:
							x, y = Bd09ToWgs84(x, y)
						default: //WGS84 & CGCS2000
						}
						geom := fmt.Sprintf(`ST_SetSRID(ST_Point(%f, %f),4326)`, x, y)
						vals = append(vals, fmt.Sprintf(`(%s,%s)`, rval, geom))
					} else {
						vals = append(vals, fmt.Sprintf(`(%s)`, rval))
					}
					task.Progress = int(count / task.Count / 5)
				}
				fmt.Println(`csv process `, time.Since(t))
				t = time.Now()
				task.State = "importing"
				st := fmt.Sprintf(`INSERT INTO "%s" (%s) VALUES %s ON CONFLICT DO NOTHING;`, tableName, strings.Join(headers, ","), strings.Join(vals, ",")) // ON CONFLICT (id) DO UPDATE SET (%s) = (%s)
				query := db.Exec(st)
				err = query.Error
				if err != nil {
					log.Errorf(`task failed, details:%s`, err)
					task.State = "failed"
				}
				fmt.Println(`csv insert `, time.Since(t))

				task.Succeed = int(query.RowsAffected)
				task.Progress = 100
				task.State = "finished"
				task.Err = ""
				task.Pipe <- struct{}{}
				return
			case ".geojson":
				formatBool := func(v interface{}) string {
					if v == nil {
						return "null"
					}
					if b, ok := v.(bool); ok {
						return strconv.FormatBool(b)
					}
					//string
					str := strings.ToLower(v.(string))
					switch str {
					case "true", "false", "yes", "no", "1", "0":
					default:
						return "null"
					}
					return "'" + str + "'"
				}
				formatInt := func(v interface{}) string {
					if v == nil {
						return "null"
					}
					if i, ok := v.(int); ok {
						return strconv.FormatInt(int64(i), 10)
					}
					if f, ok := v.(float64); ok {
						return strconv.FormatFloat(f, 'g', -1, 64)
					}
					//string
					i, err := strconv.ParseInt(v.(string), 10, 64)
					if err != nil {
						return strconv.FormatInt(i, 10)
					}
					return "null"
				}
				formatFloat := func(v interface{}) string {
					if v == nil {
						return "null"
					}
					if f, ok := v.(float64); ok {
						return strconv.FormatFloat(f, 'g', -1, 64)
					}
					if i, ok := v.(int); ok {
						return strconv.FormatInt(int64(i), 10)
					}
					//string
					f, err := strconv.ParseFloat(v.(string), 64)
					if err != nil {
						return strconv.FormatFloat(f, 'g', -1, 64)
					}
					return "null"
				}
				formatDate := func(v interface{}) string {
					if v == nil {
						return "null"
					}
					if i64, ok := v.(int64); ok {
						d := time.Unix(i64, 0).Format("2006-01-02 15:04:05")
						return "'" + d + "'"
					}
					if i, ok := v.(int); ok {
						d := time.Unix(int64(i), 0).Format("2006-01-02 15:04:05")
						return "'" + d + "'"
					}
					//string shoud filter the invalid time values
					if s, ok := v.(string); ok {
						return "'" + s + "'"
					}
					return "null"
				}
				formatString := func(v interface{}) string {
					if v == nil {
						return "null"
					}
					if s, ok := v.(string); ok {
						return "'" + s + "'"
					}
					if f, ok := v.(float64); ok {
						s := strconv.FormatFloat(f, 'g', -1, 64)
						return "'" + s + "'"
					}
					if i, ok := v.(int); ok {
						s := strconv.FormatInt(int64(i), 10)
						return "'" + s + "'"
					}
					if b, ok := v.(bool); ok {
						s := strconv.FormatBool(b)
						return "'" + s + "'"
					}
					return "null"
				}
				prepValues := func(props geojson.Properties, cols []*sql.ColumnType) string {
					var vals []string
					for i, col := range cols {
						// fmt.Println(i, col.DatabaseTypeName(), col.Name())
						v := props[headers[i]]
						var s string
						switch col.DatabaseTypeName() {
						case "BOOL":
							s = formatBool(v)
						case "INT4":
							s = formatInt(v)
						case "NUMERIC":
							s = formatFloat(v)
						case "TIMESTAMPTZ":
							s = formatDate(v)
						default: //string->"TEXT" "VARCHAR","BOOL",datetime->"TIMESTAMPTZ",pq.StringArray->"_VARCHAR"
							s = formatString(v)
						}
						vals = append(vals, s)
					}
					return strings.Join(vals, ",")
				}

				buf, err := df.getDatabuf()
				if err != nil {
					task.Err = `get databuf error`
					task.Pipe <- struct{}{}
					return
				}

				// A FeatureCollection correlates to a GeoJSON feature collection.
				var gjson struct {
					Type     string            `json:"type"`
					Features []json.RawMessage `json:"features"`
				}

				s := time.Now()
				err = json.Unmarshal(buf, &gjson)
				if err != nil {
					log.Fatalln("error:", err)
				}
				fmt.Println(time.Since(s))
				count := len(gjson.Features)
				task.State = "processing"
				t := time.Now()
				var vals []string
				for i, rawf := range gjson.Features {
					f, err := geojson.UnmarshalFeature(rawf)
					if err != nil {
						log.Errorf(`UnmarshalFeature error, details:%s`, err)
						continue
					}
					rval := prepValues(f.Properties, cols)
					switch df.Crs {
					case GCJ02:
						f.Geometry.GCJ02ToWGS84()
					case BD09:
						f.Geometry.BD09ToWGS84()
					default: //WGS84 & CGCS2000
					}
					// s := fmt.Sprintf("INSERT INTO ggg (id,geom) VALUES (%d,st_setsrid(ST_GeomFromWKB('%s'),4326))", i, wkb.Value(f.Geometry))
					// err := db.Exec(s).Error
					// if err != nil {
					// 	log.Info(err)
					// }
					geom, err := geojson.NewGeometry(f.Geometry).MarshalJSON()
					if err != nil {
						log.Errorf(`preper geometry error, details:%s`, err)
						continue
					}
					// gval := fmt.Sprintf(`st_setsrid(ST_GeomFromWKB('%s'),4326)`, wkb.Value(f.Geometry))
					gval := fmt.Sprintf(`st_setsrid(st_geomfromgeojson('%s'),4326)`, string(geom))
					vals = append(vals, fmt.Sprintf(`(%s,%s)`, rval, gval))
					task.Progress = int(i / count / 2)
				}
				fmt.Println("geojson process ", time.Since(t))

				task.State = "importing"
				t = time.Now()
				st := fmt.Sprintf(`INSERT INTO "%s" (%s,geom) VALUES %s ON CONFLICT DO NOTHING;`, tableName, strings.Join(headers, ","), strings.Join(vals, ",")) // ON CONFLICT (id) DO UPDATE SET (%s) = (%s)
				// log.Println(st)
				query := db.Exec(st)
				err = query.Error
				if err != nil {
					task.Err = err.Error()
				}
				fmt.Println("geojson insert ", time.Since(t))
				task.Count = count
				task.Succeed = int(query.RowsAffected)
				task.Progress = 100
				task.State = "finished"
				task.Pipe <- struct{}{}
				return
			}
		case ".shp", ".kml", ".gpx":
			var params []string
			//设置数据库
			params = append(params, []string{"-f", "PostgreSQL"}...)
			pg := fmt.Sprintf(`PG:dbname=%s host=%s port=%s user=%s password=%s`,
				viper.GetString("db.database"), viper.GetString("db.host"), viper.GetString("db.port"), viper.GetString("db.user"), viper.GetString("db.password"))
			params = append(params, pg)
			//显示进度,读取outbuffer缓冲区
			params = append(params, "-progress")
			//跳过失败
			// params = append(params, "-skipfailures")//此选项不能开启，开启后windows会非常慢
			params = append(params, []string{"-nln", tableName}...)
			params = append(params, []string{"-lco", "FID=gid"}...)
			params = append(params, []string{"-lco", "GEOMETRY_NAME=geom"}...)
			params = append(params, []string{"-lco", "LAUNDER=NO"}...)
			params = append(params, []string{"-lco", "EXTRACT_SCHEMA_FROM_LAYER_NAME=NO"}...)
			// params = append(params, []string{"-fid", "gid"}...)
			// params = append(params, []string{"-geomfield", "geom"}...)
			//覆盖or更新选项
			if df.Overwrite {
				// params = append(params, "-overwrite")
				//-overwrite not works
				params = append(params, []string{"-lco", "OVERWRITE=YES"}...)
			} else {
				params = append(params, "-update") //open in update model/用更新模式打开,而不是尝试新建
				params = append(params, "-append")
			}

			switch df.Format {
			case ".shp":
				//开启拷贝模式
				//--config PG_USE_COPY YES
				params = append(params, []string{"--config", "PG_USE_COPY", "YES"}...)
				//每个事务group大小
				// params = append(params, "-gt 65536")

				//数据编码选项
				// fmt.Println(df.Encoding)
				//客户端环境变量
				//SET PGCLIENTENCODINUTF8G=GBK or 'SET client_encoding TO encoding_name'
				// params = append(params, []string{"-sql", "SET client_encoding TO GBK"}...)
				//test first select client_encoding;
				//设置源文件编码
				// params = append(params, []string{"--config", "SHAPE_ENCODING", "GBK"}...)
				//PROMOTE_TO_MULTI can be used to automatically promote layers that mix polygon or multipolygons to multipolygons, and layers that mix linestrings or multilinestrings to multilinestrings. Can be useful when converting shapefiles to PostGIS and other target drivers that implement strict checks for geometry types.
				params = append(params, []string{"-nlt", "PROMOTE_TO_MULTI"}...)
			}
			absPath, err := filepath.Abs(df.Path)
			if err != nil {
				log.Error(err)
			}
			params = append(params, absPath)
			if runtime.GOOS == "windows" {
				decoder := mahonia.NewDecoder("gbk")
				gbk := strings.Join(params, ",")
				gbk = decoder.ConvertString(gbk)
				params = strings.Split(gbk, ",")
			}
			// go func(task *ImportTask) {
			task.State = "importing"
			log.Info(params)
			var stdoutBuf, stderrBuf bytes.Buffer
			cmd := exec.Command("ogr2ogr", params...)
			// cmd.Stdout = &stdout
			stdoutIn, _ := cmd.StdoutPipe()
			stderrIn, _ := cmd.StderrPipe()
			// var errStdout, errStderr error
			stdout := io.MultiWriter(os.Stdout, &stdoutBuf)
			stderr := io.MultiWriter(os.Stderr, &stderrBuf)
			err = cmd.Start()
			if err != nil {
				log.Errorf("cmd.Start() failed with '%s'\n", err)
				task.Err = err.Error()
			}
			go func() {
				io.Copy(stdout, stdoutIn)
			}()
			go func() {
				io.Copy(stderr, stderrIn)
			}()
			ticker := time.NewTicker(time.Second)
			go func(task *Task) {
				for range ticker.C {
					p := len(stdoutBuf.Bytes())*2 + 2
					if p < 100 {
						task.Progress = p
					} else {
						task.Progress = 100
					}
				}
			}(task)

			err = cmd.Wait()
			if err != nil {
				log.Errorf("cmd.Run() failed with %s\n", err)
				task.Err = err.Error()
			}
			// if errStdout != nil || errStderr != nil {
			// 	log.Errorf("failed to capture stdout or stderr\n")
			// }
			// outStr, errStr := string(stdoutBuf.Bytes()), string(stderrBuf.Bytes())
			// fmt.Printf("\nout:\n%s\nerr:\n%s\n", outStr, errStr)
			ticker.Stop()
			task.State = "finished"
			task.Pipe <- struct{}{}
			return
			//保存任务
		default:
			task.Err = fmt.Sprintf(`dataImport, importing unkown format data:%s`, df.Format)
			task.Pipe <- struct{}{}
			return
		}
	}(df, task)

	return task
}

//toGeojson import geojson or csv data, can transform from gcj02 or bd09
func (df *Datafile) toGeojson() error {
	switch df.Format {
	case ".shp": //and so on
		var params []string
		//显示进度,读取outbuffer缓冲区
		absPath, err := filepath.Abs(df.Path)
		if err != nil {
			return err
		}
		outfile := strings.TrimSuffix(absPath, filepath.Ext(absPath)) + ".geojson"
		params = append(params, []string{"-f", "GEOJSON", outfile}...)
		params = append(params, "-progress")
		params = append(params, "-skipfailures")
		//覆盖or更新选项
		if df.Overwrite {
			//-overwrite not works
			params = append(params, []string{"-lco", "OVERWRITE=YES"}...)
		} else {
			params = append(params, "-update") //open in update model/用更新模式打开,而不是尝试新建
			params = append(params, "-append")
		}
		params = append(params, []string{"-nlt", "PROMOTE_TO_MULTI"}...)
		params = append(params, absPath)
		if runtime.GOOS == "windows" {
			decoder := mahonia.NewDecoder("gbk")
			gbk := strings.Join(params, ",")
			gbk = decoder.ConvertString(gbk)
			params = strings.Split(gbk, ",")
		}
		cmd := exec.Command("ogr2ogr", params...)
		err = cmd.Start()
		if err != nil {
			return err
		}
		err = cmd.Wait()
		if err != nil {
			return err
		}
	case ".kml", ".gpx":
		var params []string
		//显示进度,读取outbuffer缓冲区
		absPath, err := filepath.Abs(df.Path)
		if err != nil {
			return err
		}

		params = append(params, absPath)
		params = append(params, ">")

		ext := filepath.Ext(absPath)
		outfile := strings.TrimSuffix(absPath, ext)
		params = append(params, outfile+".geojson")

		if runtime.GOOS == "windows" {
			decoder := mahonia.NewDecoder("gbk")
			gbk := strings.Join(params, ",")
			gbk = decoder.ConvertString(gbk)
			params = strings.Split(gbk, ",")
		}
		// go func(task *ImportTask) {
		cmd := exec.Command("togeojson", params...)
		err = cmd.Start()
		if err != nil {
			return err
		}
		err = cmd.Wait()
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("not support format: %s", df.Format)
	}
	return nil
}

//toDataset 创建Dataset
func (df *Datafile) toDataset() (*Dataset, error) {
	//info from data table
	tableName := strings.ToLower(df.ID)
	s := fmt.Sprintf(`SELECT * FROM "%s" LIMIT 0;`, tableName)
	log.Println(s)
	rows, err := db.Raw(s).Rows() // (*sql.Rows, error)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	var fields []Field
	for _, col := range cols {
		var t string
		switch col.DatabaseTypeName() {
		case "INT", "INT4":
			t = Int
		case "NUMERIC": //number
			t = Float
		case "BOOL":
			t = Bool
		case "TIMESTAMPTZ":
			t = Date
		case "_VARCHAR":
			t = StringArray
		case "TEXT", "VARCHAR":
			t = string(String)
		default:
			t = string(String)
		}
		field := Field{
			Name: col.Name(),
			Type: t,
		}
		fields = append(fields, field)
	}

	jfs, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	var cnt int
	db.Table(tableName).Count(&cnt)
	ds := &Dataset{
		ID:     df.ID,
		Name:   df.Name,
		Alias:  df.Alias,
		Tag:    df.Tag,
		Owner:  df.Owner,
		Count:  cnt,
		Type:   Polygon,
		Fields: jfs,
	}
	return ds, nil
}

//getDataFrame get data preview context
func (df *Datafile) getDatabuf() ([]byte, error) {
	if df == nil {
		return nil, fmt.Errorf("datafile may not be nil")
	}

	buf, err := ioutil.ReadFile(df.Path)
	if err != nil {
		log.Errorf(`read datafile error, details:%s`, err)
		return nil, err
	}

	if df.Encoding == "" {
		df.Encoding = chardet.Mostlike(buf)
	}
	// 去除bom
	rmbom := func(data []byte) []byte {
		if len(data) >= 3 {
			if string(data[:3]) == "\xEF\xBB\xBF" {
				return data[3:]
			}
		}
		if len(data) >= 4 {
			if string(data[:4]) == "\x84\x31\x95\x33" {
				return data[4:]
			}
		}
		return data
	}
	buf = rmbom(buf)

	if df.Encoding != "utf-8" {
		//converts gbk to utf-8.
		decode := mahonia.NewDecoder(df.Encoding)
		if decode == nil {
			log.Errorf(`getDataFrame, mahonia new decoder error, data file encoding:%s`, df.Encoding)
			return nil, fmt.Errorf(`getDataFrame, mahonia new decoder error, data file encoding:%s`, df.Encoding)
		}
		_, data, err := decode.Translate(buf, true)
		if err != nil {
			log.Errorf(`getDataFrame, mahonia decode translate error, details:%s`, err)
		}
		return data, nil
	}

	return buf, nil
}

func createTable(name string, fields []Field, geoType string) error {
	prepHeader := func(fields []Field, geoType string) string {
		var fts []string
		fts = append(fts, "gid serial primary key")
		for _, v := range fields {
			var t string
			switch v.Type {
			case Bool:
				t = "BOOL"
			case Int:
				t = "INT4"
			case Float:
				t = "NUMERIC"
			case Date:
				t = "TIMESTAMPTZ"
			default:
				t = "TEXT"
			}
			fts = append(fts, v.Name+" "+t)
		}
		if geoType != "none" {
			fts = append(fts, fmt.Sprintf("geom geometry(%s,4326)", geoType))
		}
		return strings.Join(fts, ",")
	}
	headers := prepHeader(fields, geoType)
	st := fmt.Sprintf(`CREATE TABLE "%s" (%s);`, name, headers)
	log.Info(st)
	return db.Exec(st).Error
}

func createMbtiles(outfile string, infiles []string) error {
	var params []string
	//显示进度,读取outbuffer缓冲区
	absPath, err := filepath.Abs(outfile)
	if err != nil {
		return err
	}
	params = append(params, "-zg")
	params = append(params, "-o")
	params = append(params, absPath)
	params = append(params, "--force")
	params = append(params, infiles...)
	if runtime.GOOS == "windows" {
		decoder := mahonia.NewDecoder("gbk")
		gbk := strings.Join(params, ",")
		gbk = decoder.ConvertString(gbk)
		params = strings.Split(gbk, ",")
	}
	cmd := exec.Command("tippecanoe", params...)
	err = cmd.Start()
	if err != nil {
		return err
	}
	err = cmd.Wait()
	if err != nil {
		return err
	}
	return nil
}

func (task *Task) save() error {
	if task == nil {
		return fmt.Errorf("task may not be nil")
	}
	err := db.Create(task).Error
	if err != nil {
		return err
	}
	return nil
}

func (task *Task) update() error {
	if task == nil {
		return fmt.Errorf("task may not be nil")
	}
	err := db.Model(&Task{}).Update(task).Error
	if err != nil {
		return err
	}
	return nil
}

func (task *Task) info() error {
	if task == nil {
		return fmt.Errorf("task may not be nil")
	}
	err := db.Where(`id = ? `, task.ID).First(task).Error
	if err != nil {
		return err
	}
	return nil
}
