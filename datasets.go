package main

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/atlasdatatech/chardet"
	"github.com/axgle/mahonia"
	"github.com/jinzhu/gorm"
	shp "github.com/jonas-p/go-shp"
	"github.com/kniren/gota/dataframe"
	"github.com/kniren/gota/series"

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

// FieldType is a convenience alias that can be used for a more type safe way of
// reason and use Series types.
type FieldType string

// Supported Series Types
const (
	String FieldType = "string"
	Bool             = "bool"
	Int              = "int"
	Float            = "float"
	Date             = "date"
)

//FieldTypes 支持的字段类型
var FieldTypes = []string{"string", "int", "float", "bool", "date"}

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
	Type      string  `json:"type"`
	Lat       string  `json:"lat"`
	Lon       string  `json:"lon"`
	Fields    []Field `json:"fields"`
	Process   string  `json:"process"`
	CreatedAt time.Time
	DeletedAt *time.Time `sql:"index"`
}

// DataPreview 数据预览
type DataPreview struct {
	ID        string     `json:"id" form:"id" binding:"required"`
	Tags      []string   `json:"tags" form:"tags" binding:"required"`
	Name      string     `json:"name" form:"name" binding:"required"`
	Alias     string     `json:"alias" form:"alias" binding:"required"`
	Encodings []string   `json:"encodings" form:"encodings" binding:"required"`
	Crss      []string   `json:"crss" form:"crss" binding:"required"` //WGS84,CGCS2000,GCJ02,BD09
	Lon       string     `json:"lon" form:"lon"`
	Lat       string     `json:"lat" form:"lat"`
	Type      string     `json:"type" form:"type"`
	Fields    []Field    `json:"fields" form:"fields" binding:"required"`
	Rows      [][]string `json:"rows" form:"rows"`
	Count     int        `json:"count" form:"count"`
	Update    bool       `json:"update" form:"update"`
}

// Field 字段
type Field struct {
	Name  string `json:"name"`
	Alias string `json:"alias"`
	Type  string `json:"type"`
	Index string `json:"index"`
}

//Fields 字段列表
type Fields []Field

// Dataset 数据集定义-后台
type Dataset struct {
	ID     string `json:"id"`                      //字段列表
	Name   string `json:"name"`                    //字段列表// 数据集名称,现用于更方便的ID
	Label  string `json:"label"`                   //字段列表// 显示标签
	Type   string `json:"type"`                    //字段列表
	Fields []byte `json:"fields" gorm:"type:json"` //字段列表
}

// DatasetBind 数据集定义-接口
type DatasetBind struct {
	ID     string      `form:"id" json:"id"`         //字段列表
	Name   string      `form:"name" json:"name"`     //字段列表// 数据集名称,现用于更方便的ID
	Label  string      `form:"label" json:"label"`   //字段列表// 显示标签
	Type   string      `form:"type" json:"type"`     //字段列表
	Fields interface{} `form:"fields" json:"fields"` //字段列表
}

// DataService 数据集服务
type DataService struct {
	ID      string
	URL     string // geojson service
	Hash    string
	State   bool         // true if UTFGrids have corresponding key / value data that need to be joined and returned with the UTFGrid
	Dataset *DatasetBind // database connection for mbtiles file
}

//upInsert 创建更新上传数据文件信息
//create or update upload data file info into database
func (df *Datafile) upInsert() error {
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
func (df *Datafile) getPreview() *DataPreview {
	dp := &DataPreview{}
	if df == nil {
		log.Errorf("datafile may not be nil")
		return dp
	}
	buf, err := df.getDatabuf()
	if err != nil {
		log.Errorf(`getPreview, get databuf error, details:%s`, err)
		return dp
	}
	switch df.Format {
	case ".csv":
		frame := dataframe.ReadCSV(bytes.NewReader(buf))
		names := frame.Names()
		types := frame.Types()
		var fields []Field
		for i, n := range names {
			fields = append(fields, Field{
				Name: n,
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
			types := frame.Types()
			names := frame.Names()
			for i, t := range types {
				if t == series.Float {
					ds := frame.Select([]string{names[i]}).Describe().Subset([]int{2, 6})
					emin := ds.Elem(0, 1).Float()
					emax := ds.Elem(0, 1).Float()
					if emin > min && emax < max {
						return names[i]
					}
				}
			}
			return ""
		}

		xcols := []string{"x", "lon", "longitude", "经度"}
		x := getColumn(xcols, names)
		if x == "" {
			x = detechColumn(73, 135)
		}
		ycols := []string{"y", "lat", "latitude", "纬度"}
		y := getColumn(ycols, names)
		if y == "" {
			y = detechColumn(18, 54)
		}

		n := frame.Nrow()
		if n > 7 {
			frame = frame.Subset([]int{n / 7, 2 * n / 7, 3 * n / 7, 4 * n / 7, 5 * n / 7, 6 * n / 7, n - 1})
		}
		rows := frame.Records()
		dp.Count = n
		dp.Lon = x
		dp.Lat = y
		dp.Type = "Point"
		dp.Fields = fields
		dp.Rows = rows[1:]
		dp.Update = false
	case ".geojson":
		fc, err := geojson.UnmarshalFeatureCollection(buf)
		if err != nil {
			log.Error(err)
			return dp
		}
		fcount := len(fc.Features)
		if fcount == 0 {
			log.Error(`empty datafile`)
			return dp
		}

		var fields []Field
		geoType := fc.Features[0].Geometry.GeoJSONType()
		for k, v := range fc.Features[0].Properties {
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
				rows = append(rows, prepRow(fc.Features[p]))
			}
		} else {
			for _, f := range fc.Features {
				rows = append(rows, prepRow(f))
			}
		}
		dp.Type = geoType
		dp.Count = fcount
		dp.Fields = fields
		dp.Rows = rows
		dp.Update = false
	case ".shp":

	}

	aHead := func(slc []string, cur string) []string {

		if len(slc) == 0 || slc[0] == cur {
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
	dp.Tags = df.getTags()
	dp.Encodings = aHead(Encodings, df.Encoding)
	dp.Crss = aHead(CRSs, df.Crs)

	// copy(dp.Rows, frame.Records())
	return dp
}

//getPreview get data preview context
func (df *Datafile) getShpPreview() *DataPreview {
	dp := &DataPreview{}
	if df == nil {
		log.Errorf("datafile may not be nil")
		return dp
	}

	unzipToDir := func(p string) string {
		ext := filepath.Ext(p)
		dir := strings.TrimSuffix(p, ext)
		err := os.Mkdir(dir, os.ModePerm)
		if err != nil {
			log.Error(err)
		}
		zip, err := zip.OpenReader(p)
		if err != nil {
			log.Error(err)
		}
		defer zip.Close()
		for _, f := range zip.File {
			_, fn := path.Split(f.Name)
			pn := filepath.Join(dir, fn)
			log.Infof("Uncompress: %s -> %s", f.Name, pn)
			w, err := os.Create(pn)
			if err != nil {
				log.Errorf("Cannot unzip %s: %v", p, err)
			}
			defer w.Close()
			r, err := f.Open()
			if err != nil {
				log.Errorf("Cannot unzip %s: %v", p, err)
			}
			defer r.Close()
			_, err = io.Copy(w, r)
			if err != nil {
				log.Errorf("Cannot unzip %s: %v", p, err)
			}
		}
		return dir
	}

	dir := unzipToDir(df.Path)

	getShapfiles := func(dir string) []string {
		var shapeFiles []string
		fileInfos, err := ioutil.ReadDir(dir)
		if err != nil {
			log.Error(err)
		}
		for _, fileInfo := range fileInfos {
			if fileInfo.IsDir() {
				continue
			}
			ext := filepath.Ext(fileInfo.Name())
			if ".shp" == strings.ToLower(ext) {
				shapeFiles = append(shapeFiles, filepath.Join(dir, fileInfo.Name()))
			}
		}
		return shapeFiles
	}
	shpfiles := getShapfiles(dir)
	for _, file := range shpfiles {
		shape, err := shp.Open(file)
		// open a shapefile for reading
		if err != nil {
			log.Fatal(err)
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

		decoder := mahonia.NewDecoder(df.Encoding)

		var rows [][]string
		if fcount > 7 {
			for _, p := range []int{fcount / 7, 2 * fcount / 7, 3 * fcount / 7, 4 * fcount / 7, 5 * fcount / 7, 6 * fcount / 7, fcount - 1} {
				var row []string
				for k := range fields {
					val := shape.ReadAttribute(p, k)
					if df.Encoding != "" && df.Encoding != "utf-8" {
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
					if df.Encoding != "" && df.Encoding != "utf-8" {
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

		dp.Type = geoType
		dp.Count = fcount
		dp.Fields = fields
		dp.Rows = rows
		dp.Update = false

		break
	}

	aHead := func(slc []string, cur string) []string {

		if len(slc) == 0 || slc[0] == cur {
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
	dp.Tags = df.getTags()
	dp.Encodings = aHead(Encodings, df.Encoding)
	dp.Crss = aHead(CRSs, df.Crs)

	// copy(dp.Rows, frame.Records())
	return dp
}

//csvImport import csv data
func (df *Datafile) dataImport() (int64, error) {
	buf, err := df.getDatabuf()
	if err != nil {
		return 0, fmt.Errorf(`get databuf error`)
	}

	prepHeader := func(fields []Field) string {
		var fts []string
		for _, v := range fields {
			fts = append(fts, v.Name)
		}
		return strings.Join(fts, ",")
	}
	header := prepHeader(df.Fields)

	s := fmt.Sprintf(`SELECT %s FROM "%s" LIMIT 0`, header, df.ID)
	rows, err := db.Raw(s).Rows() // (*sql.Rows, error)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil {
		return 0, err
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
				return strconv.FormatInt(i64, 10)
			}
			return "null"
		}
		formatFloat := func(v string) string {
			if v == "" {
				return "null"
			}
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return strconv.FormatFloat(f, 'g', -1, 64)
			}
			return "null"
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
				case "INT":
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
		var vals []string
		frame := dataframe.ReadCSV(bytes.NewReader(buf))
		frame = frame.Select(header)
		records := frame.Records()
		for i, row := range records {
			rval := prepValues(row, cols)
			xy := frame.Subset(i).Select([]string{df.Lon, df.Lat})
			x := xy.Elem(0, 0).Float()
			y := xy.Elem(0, 1).Float()
			switch df.Crs {
			case GCJ02:
				x, y = Gcj02ToWgs84(x, y)
			case BD09:
				x, y = Bd09ToWgs84(x, y)
			default: //WGS84 & CGCS2000
			}
			geom := fmt.Sprintf(`ST_SetSRID(ST_Point(%f, %f),4326)`, x, y)
			vals = append(vals, fmt.Sprintf(`(%s,%s)`, rval, geom))
		}
		s = fmt.Sprintf(`INSERT INTO "%s" (%s,geom) VALUES %s ON CONFLICT DO NOTHING;`, df.ID, header, strings.Join(vals, ",")) // ON CONFLICT (id) DO UPDATE SET (%s) = (%s)
		query := db.Exec(s)
		return query.RowsAffected, nil
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
			for _, col := range cols {
				// fmt.Println(i, col.DatabaseTypeName(), col.Name())
				v := props[col.Name()]
				var s string
				switch col.DatabaseTypeName() {
				case "BOOL":
					s = formatBool(v)
				case "INT":
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

		fc, err := geojson.UnmarshalFeatureCollection(buf)
		if err != nil {
			log.Error(err)
			return 0, err
		}
		fcount := len(fc.Features)
		if fcount == 0 {
			log.Error(`empty datafile`)
			return 0, fmt.Errorf(`empty datafile`)
		}

		var vals []string
		for _, f := range fc.Features {
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
				return 0, err
			}
			// gval := fmt.Sprintf(`st_setsrid(ST_GeomFromWKB('%s'),4326)`, wkb.Value(f.Geometry))
			gval := fmt.Sprintf(`st_setsrid(st_geomfromgeojson('%s'),4326)`, string(geom))

			vals = append(vals, fmt.Sprintf(`(%s,%s)`, rval, gval))
		}

		st := fmt.Sprintf(`INSERT INTO "%s" (%s,geom) VALUES %s ON CONFLICT DO NOTHING;`, df.ID, header, strings.Join(vals, ",")) // ON CONFLICT (id) DO UPDATE SET (%s) = (%s)
		log.Println(st)
		query := db.Exec(st)
		err = query.Error
		if err != nil {
			return 0, err
		}
		return query.RowsAffected, nil
	case ".shp":
	}
	return 0, fmt.Errorf("unkown data fromat")
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
		for _, v := range fields {
			var t string
			switch v.Type {
			case Bool:
				t = "BOOL"
			case Int:
				t = "INT"
			case Float:
				t = "NUMERIC"
			case Date:
				t = "TIMESTAMPTZ"
			default:
				t = "TEXT"
			}
			fts = append(fts, v.Name+" "+t)
		}
		fts = append(fts, fmt.Sprintf("geom geometry(%s,4326)", geoType))
		return strings.Join(fts, ",")
	}
	headers := prepHeader(fields, geoType)
	st := fmt.Sprintf(`CREATE TABLE "%s" (%s);`, name, headers)
	log.Info(st)
	return db.Exec(st).Error
}

//geojsonImport import geojson data
func geojsonImport(df *Datafile, dp *DataPreview) (int, error) {
	return 0, nil
}

func (ds *Dataset) toBind() *DatasetBind {
	out := &DatasetBind{
		ID:    ds.ID,
		Name:  ds.Name,
		Label: ds.Label,
		Type:  ds.Type,
	}
	json.Unmarshal(ds.Fields, &out.Fields)
	return out
}

func (dsb *DatasetBind) toDataset() *Dataset {
	out := &Dataset{
		ID:    dsb.ID,
		Name:  dsb.Name,
		Label: dsb.Label,
		Type:  dsb.Type,
	}
	out.Fields, _ = json.Marshal(dsb.Fields)
	return out
}

// AddDatasetService interprets filename as mbtiles file which is opened and which will be
// served under "/services/<urlPath>" by Handler(). The parameter urlPath may not be
// nil, otherwise an error is returned. In case the DB cannot be opened the returned
// error is non-nil.
func (s *ServiceSet) AddDatasetService(dataset *Dataset) error {
	if dataset == nil {
		return fmt.Errorf("dataset may not be nil")
	}
	out := &DataService{
		ID:      dataset.ID,
		URL:     dataset.Name, //should not add / at the end
		Hash:    "#",          //should not add / at the end
		State:   true,
		Dataset: dataset.toBind(),
	}
	s.Datasets[dataset.Name] = out
	return nil
}

func (s *ServiceSet) updateInsertDataset(dataset *Dataset) error {
	if dataset == nil {
		return fmt.Errorf("dataset may not be nil")
	}
	ds := &Dataset{}
	err := db.Where("id = ?", dataset.ID).First(ds).Error
	if err != nil {
		if gorm.IsRecordNotFoundError(err) {
			err = db.Create(dataset).Error
			if err != nil {
				return err
			}
		}
		return err
	}
	err = db.Model(&Dataset{}).Update(dataset).Error
	if err != nil {
		return err
	}
	return nil
}

// LoadDatasetServices returns a ServiceSet that combines all .mbtiles files under
// the directory at baseDir. The DBs will all be served under their relative paths
// to baseDir.
func (s *ServiceSet) LoadDatasetServices() (err error) {
	// 获取所有记录
	var datasets []Dataset
	err = db.Find(&datasets).Error
	if err != nil {
		log.Errorf(`ServeDatasets, query datasets: %s; user: %s ^^`, err, s.User)
	}
	//clear service
	for k := range s.Datasets {
		delete(s.Datasets, k)
	}
	for _, ds := range datasets {
		err = s.AddDatasetService(&ds)
		if err != nil {
			log.Errorf(`ServeDatasets, add dataset: %s; user: %s ^^`, err, s.User)
		}
	}
	log.Infof("ServeDatasets, loaded %d dataset for %s ~", len(s.Datasets), s.User)
	return nil
}

// GetGeoJSON reads a data in the database
func (ds *Dataset) GetGeoJSON(data *[]byte) error {
	return nil
}

// GetJSONConfig load to config
func (ds *Dataset) GetJSONConfig(data *[]byte) error {
	return nil
}
