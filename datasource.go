package main

import (
	"bytes"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"io"
	"os/exec"
	"runtime"
	"strconv"

	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/axgle/mahonia"
	"github.com/jinzhu/gorm"
	shp "github.com/jonas-p/go-shp"
	_ "github.com/mattn/go-sqlite3" // import sqlite3 driver
	"github.com/paulmach/orb/geojson"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	// "github.com/paulmach/orb/encoding/wkb"
)

//BUFSIZE 16M
const (
	BUFSIZE   int64 = 1 << 24
	PREROWNUM       = 7
)

// DataSource 文件数据源集定义结构
type DataSource struct {
	ID        string          `json:"id"`   //字段列表
	Name      string          `json:"name"` //字段列表// 数据集名称,现用于更方便的ID
	Owner     string          `json:"owner"`
	Tag       string          `json:"tag"`
	Path      string          `json:"path"`
	Format    string          `json:"format"`
	Encoding  string          `json:"encoding"`
	Size      int64           `json:"size"`
	Total     int             `json:"total"`
	Crs       string          `json:"crs"` //WGS84,CGCS2000,GCJ02,BD09
	Geotype   GeoType         `json:"geotype"`
	Rows      [][]string      `json:"rows" gorm:"-"`
	Fields    json.RawMessage `json:"fields" gorm:"type:json"` //字段列表
	CreatedAt time.Time
	UpdatedAt time.Time
}

//UpInsert 更新/创建数据集概要
func (ds *DataSource) toDataset() *Dataset {
	dt := &Dataset{
		ID:      ds.ID,
		Name:    ds.Name,
		Owner:   ds.Owner,
		Tag:     ds.Tag,
		Path:    ds.Path,
		Size:    ds.Size,
		Format:  ds.Format,
		Total:   ds.Total,
		Geotype: ds.Geotype,
	}
	// if strings.Contains(ds.Geotype, ",") {
	// 	dt.Geotype = Point
	// }
	dt.GeoType()
	dt.FieldsInfo()
	dt.TotalCount()
	dt.Bound()
	return dt
}

//Insert 更新/创建数据集概要
func (ds *DataSource) Insert() error {
	tmp := &DataSource{}
	err := db.Where("id = ?", ds.ID).First(tmp).Error
	if err != nil {
		if gorm.IsRecordNotFoundError(err) {
			err = db.Create(ds).Error
			if err != nil {
				return err
			}
		}
		return err
	}
	err = db.Model(&DataSource{}).Update(ds).Error
	if err != nil {
		return err
	}
	return nil
}

//Update 更新/创建数据集概要
func (ds *DataSource) Update() error {
	err := db.Model(&DataSource{}).Update(ds).Error
	if err != nil {
		return err
	}
	return nil
}

//ToGeojson 数据源转为geojson
func (ds *DataSource) ToGeojson() error {
	npath := strings.TrimSuffix(ds.Path, ds.Format) + GEOJSONEXT
	switch ds.Format {
	case KMLEXT, GPXEXT:
		err := kg2Geojson(ds.Path, npath)
		if err != nil {
			return err
		}
		ds.Path = npath
		ds.Format = GEOJSONEXT
	case GEOJSONEXT, CSVEXT:
		return nil
	case SHPEXT:
		err := shp2Geojson(ds.Path, npath)
		if err != nil {
			return err
		}
		ds.Path = npath
		ds.Format = GEOJSONEXT
	default:
		return fmt.Errorf("unsupport format")
	}
	return nil
}

// LoadFrom 从数据文件加载信息
func (ds *DataSource) LoadFrom() error {
	switch ds.Format {
	case CSVEXT:
		return ds.LoadFromCSV()
	case GEOJSONEXT:
		return ds.LoadFromJSON()
	case SHPEXT:
		return ds.LoadFromShp()
	}
	return fmt.Errorf("unkown format")
}

// LoadFromCSV 从csv数据文件加载数据集信息
func (ds *DataSource) LoadFromCSV() error {
	if ds.Encoding == "" {
		ds.Encoding = likelyEncoding(ds.Path)
	}
	file, err := os.Open(ds.Path)
	if err != nil {
		return err
	}
	defer file.Close()
	reader, err := csvReader(file, ds.Encoding)
	if err != nil {
		return err
	}
	headers, err := reader.Read()
	if err != nil {
		return err
	}
	var records [][]string
	var rowNum, preNum int
	for {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}
		if preNum < PREROWNUM {
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
			Type: types[i]})
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
	ds.Format = CSVEXT
	ds.Total = rowNum
	ds.Geotype = GeoType(x + "," + y)
	ds.Crs = "WGS84"
	ds.Rows = records
	flds, err := json.Marshal(fields)
	if err == nil {
		ds.Fields = flds
	}
	return nil
}

// LoadFromJSON 从geojson数据文件加载数据集信息
func (ds *DataSource) LoadFromJSON() error {
	if ds.Encoding == "" {
		ds.Encoding = likelyEncoding(ds.Path)
	}
	file, err := os.Open(ds.Path)
	if err != nil {
		return err
	}
	defer file.Close()

	dec, err := jsonDecoder(file, ds.Encoding)
	if err != nil {
		return err
	}
	s := time.Now()
	err = movetoFeatures(dec)
	if err != nil {
		return err
	}

	prepAttrRow := func(fields []Field, props geojson.Properties) []string {
		var row []string
		for _, field := range fields {
			var s string
			v, ok := props[field.Name]
			if ok {
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
				case map[string]interface{}, []interface{}:
					buf, err := json.Marshal(v)
					if err == nil {
						s = string(buf)
					}
				default: //string/map[string]interface{}/[]interface{}/nil->对象/数组都作string处理
					if v == nil {
						s = ""
					} else {
						s, _ = v.(string)
					}
				}
			}
			row = append(row, s)
		}
		return row
	}

	var rows [][]string
	var rowNum, preNum int
	ft := &geojson.Feature{}
	err = dec.Decode(ft)
	if err != nil {
		log.Errorf(`geojson data format error, details:%s`, err)
		return err
	}
	rowNum++
	preNum++
	geoType := ft.Geometry.GeoJSONType()
	var fields []Field
	for k, v := range ft.Properties {
		var t FieldType
		switch v.(type) {
		case bool:
			t = Bool //or 'timestamp with time zone'
		case float64:
			t = Float
		default: //string/map[string]interface{}/[]interface{}/nil->对象/数组都作string处理
			t = String
		}
		fields = append(fields, Field{
			Name: k,
			Type: t,
		})
	}

	row := prepAttrRow(fields, ft.Properties)
	rows = append(rows, row)

	for dec.More() {
		if preNum < PREROWNUM {
			ft := &geojson.Feature{}
			err := dec.Decode(ft)
			if err != nil {
				log.Errorf(`geojson data format error, details:%s`, err)
				continue
			}
			rows = append(rows, prepAttrRow(fields, ft.Properties))
			preNum++
		} else {
			var ft struct {
				Type string `json:"type"`
			}
			err := dec.Decode(&ft)
			if err != nil {
				log.Errorf(`Decode error, details:%s`, err)
				continue
			}
		}
		rowNum++
	}
	fmt.Printf("total features %d, takes: %v\n", rowNum, time.Since(s))

	ds.Format = GEOJSONEXT
	ds.Total = rowNum
	ds.Geotype = GeoType(geoType)
	ds.Crs = "WGS84"
	ds.Rows = rows
	flds, err := json.Marshal(fields)
	if err == nil {
		ds.Fields = flds
	}
	return nil
}

// LoadFromShp 从shp数据文件加载数据集信息
func (ds *DataSource) LoadFromShp() error {
	size := valSizeShp(ds.Path)
	if size == 0 {
		return fmt.Errorf("invalid shapefiles")
	}
	shape, err := shp.Open(ds.Path)
	if err != nil {
		return err
	}
	defer shape.Close()
	// fields from the attribute table (DBF)
	shpfields := shape.Fields()
	total := shape.AttributeCount()
	if total == 0 {
		log.Error(`empty datafile`)
		return fmt.Errorf(`empty datafile`)
	}
	var fields []Field
	for _, v := range shpfields {
		var t FieldType
		switch v.Fieldtype {
		case 'C':
			t = String
		case 'N':
			t = Int
		case 'F':
			t = Float
		case 'D':
			t = Date
		}
		fields = append(fields, Field{
			Name: v.String(),
			Type: t,
		})
	}

	rowstxt := ""
	var rows [][]string
	preRowNum := 0
	for shape.Next() {
		if preRowNum > PREROWNUM {
			break
		}
		var row []string
		for k := range fields {
			v := shape.Attribute(k)
			row = append(row, v)
			rowstxt += v
		}
		rows = append(rows, row)
		preRowNum++
	}

	if ds.Encoding == "" {
		ds.Encoding = Mostlike([]byte(rowstxt))
	}
	var mdec mahonia.Decoder
	switch ds.Encoding {
	case "gbk", "big5", "gb18030":
		mdec = mahonia.NewDecoder(ds.Encoding)
		if mdec != nil {
			var records [][]string
			for _, row := range rows {
				var record []string
				for _, v := range row {
					record = append(record, mdec.ConvertString(v))
				}
				records = append(records, record)
			}
			rows = records
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

	ds.Format = SHPEXT
	ds.Size = size
	ds.Total = total
	ds.Geotype = GeoType(geoType)
	ds.Crs = "WGS84"
	ds.Rows = rows
	jfs, err := json.Marshal(fields)
	if err == nil {
		ds.Fields = jfs
	} else {
		log.Error(err)
	}
	return nil
}

//getCreateHeaders auto add 'gid' & 'geom'
func (ds *DataSource) getCreateHeaders() []string {
	var fts []string
	fields := []Field{}
	err := json.Unmarshal(ds.Fields, &fields)
	if err != nil {
		log.Errorf(`createDataTable, Unmarshal fields error, details:%s`, err)
		return fts
	}
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
	if ds.Geotype != "" {
		dbtype := ds.Geotype
		if strings.Contains(string(ds.Geotype), ",") {
			dbtype = Point
		}
		fts = append(fts, fmt.Sprintf("geom geometry(%s,4326)", dbtype))
	}
	return fts
}

func (ds *DataSource) createDataTable() error {
	tableName := strings.ToLower(ds.ID)
	st := fmt.Sprintf(`DELETE FROM datasets WHERE id='%s';`, ds.ID)
	err := db.Exec(st).Error
	if err != nil {
		log.Errorf(`createDataTable, delete dataset error, details:%s`, err)
		return err
	}
	err = db.Exec(fmt.Sprintf(`DROP TABLE if EXISTS "%s";`, tableName)).Error
	if err != nil {
		log.Errorf(`createDataTable, drop table error, details:%s`, err)
		return err
	}
	headers := ds.getCreateHeaders()
	st = fmt.Sprintf(`CREATE TABLE "%s" (%s);`, tableName, strings.Join(headers, ","))
	log.Infoln(st)
	err = db.Exec(st).Error
	if err != nil {
		log.Errorf(`importData, create table error, details:%s`, err)
		return err
	}
	return nil
}

func (ds *DataSource) getColumnTypes() ([]*sql.ColumnType, error) {
	tableName := strings.ToLower(ds.ID)
	var st string
	fields := []Field{}
	err := json.Unmarshal(ds.Fields, &fields)
	if err != nil {
		st = fmt.Sprintf(`SELECT * FROM "%s" LIMIT 0`, tableName)
	} else {
		var headers []string
		for _, v := range fields {
			headers = append(headers, v.Name)
		}
		st = fmt.Sprintf(`SELECT %s FROM "%s" LIMIT 0`, strings.Join(headers, ","), tableName)
	}

	rows, err := db.Raw(st).Rows() // (*sql.Rows, error)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}

	var pureColumns []*sql.ColumnType

	for _, col := range cols {
		switch col.Name() {
		case "gid", "geom":
			continue
		}
		pureColumns = append(pureColumns, col)
	}
	return pureColumns, nil
}

//Import 数据源入库import geojson or csv data, can transform from gcj02 or bd09
func (ds *DataSource) Import(task *Task) error {
	tableName := strings.ToLower(ds.ID)
	switch ds.Format {
	case CSVEXT, GEOJSONEXT:
		err := ds.createDataTable()
		if err != nil {
			return err
		}
		cols, err := ds.getColumnTypes()
		if err != nil {
			return err
		}
		var headers []string
		headermap := make(map[string]int)
		for i, col := range cols {
			headers = append(headers, col.Name())
			headermap[col.Name()] = i
		}

		switch ds.Format {
		case CSVEXT:
			prepValues := func(row []string, cols []*sql.ColumnType) string {
				var vals []string
				for i, col := range cols {
					s := stringFormat(col.DatabaseTypeName(), row[i])
					vals = append(vals, s)
				}
				return strings.Join(vals, ",")
			}
			t := time.Now()
			file, err := os.Open(ds.Path)
			if err != nil {
				return err
			}
			defer file.Close()
			reader, err := csvReader(file, ds.Encoding)
			csvHeaders, err := reader.Read()
			if err != nil {
				return err
			}
			if len(cols) != len(csvHeaders) {
				log.Errorf(`dataImport, dbfield len != csvheader len: %s`, err)
			}
			prepIndex := func(cols []*sql.ColumnType, name string) int {
				for i, col := range cols {
					if col.Name() == strings.ToLower(name) {
						return i
					}
				}
				return -1
			}
			ix, iy := -1, -1
			xy := strings.Split(string(ds.Geotype), ",")
			if len(xy) == 2 {
				ix = prepIndex(cols, xy[0])
				iy = prepIndex(cols, xy[1])
			}
			isgeom := (ix != -1 && iy != -1)
			if isgeom {
				headers = append(headers, "geom")
			}
			tt := time.Since(t)
			log.Info(`process headers and get count, `, tt)
			var vals []string
			task.Status = "processing"
			t = time.Now()
			count := 0
			for {
				row, err := reader.Read()
				if err == io.EOF {
					break
				}
				if err != nil {
					continue
				}
				rval := prepValues(row, cols)
				if isgeom {
					x, _ := strconv.ParseFloat(row[ix], 64)
					y, _ := strconv.ParseFloat(row[iy], 64)
					switch ds.Crs {
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
				if count%1000 == 0 {
					go func(vs []string) {
						t := time.Now()
						st := fmt.Sprintf(`INSERT INTO "%s" (%s) VALUES %s ON CONFLICT DO NOTHING;`, tableName, strings.Join(headers, ","), strings.Join(vs, ",")) // ON CONFLICT (id) DO UPDATE SET (%s) = (%s)
						query := db.Exec(st)
						err := query.Error
						if err != nil {
							log.Error(err)
						}
						log.Infof("inserted %d rows, takes: %v", query.RowsAffected, time.Since(t))
					}(vals)
					var nvals []string
					vals = nvals
				}
				task.Progress = int(count / ds.Total / 5)
				count++
			}
			t = time.Now()
			task.Status = "importing"
			st := fmt.Sprintf(`INSERT INTO "%s" (%s) VALUES %s ON CONFLICT DO NOTHING;`, tableName, strings.Join(headers, ","), strings.Join(vals, ",")) // ON CONFLICT (id) DO UPDATE SET (%s) = (%s)
			query := db.Exec(st)
			err = query.Error
			if err != nil {
				log.Errorf(`task failed, details:%s`, err)
			}
			log.Infof("inserted %d rows, takes: %v/n", count, time.Since(t))
			return nil
		case GEOJSONEXT:
			prepValues := func(props geojson.Properties, cols []*sql.ColumnType) string {
				vals := make([]string, len(cols))
				for k, v := range props {
					ki, ok := headermap[strings.ToLower(k)]
					if ok {
						vals[ki] = interfaceFormat(cols[ki].DatabaseTypeName(), v)
					}
				}
				// for i, col := range cols {
				// 	var s string
				// 	v, ok := props[headers[i]]
				// 	if ok {
				// 		s = interfaceFormat(col.DatabaseTypeName(), v)
				// 	}
				// 	vals = append(vals, s)
				// }
				return strings.Join(vals, ",")
			}
			s := time.Now()
			file, err := os.Open(ds.Path)
			if err != nil {
				return err
			}
			defer file.Close()
			decoder, err := jsonDecoder(file, ds.Encoding)
			if err != nil {
				return err
			}
			err = movetoFeatures(decoder)
			if err != nil {
				return err
			}
			type Feature struct {
				Type       string                 `json:"type"`
				Geometry   json.RawMessage        `json:"geometry"`
				Properties map[string]interface{} `json:"properties"`
			}

			task.Status = "processing"
			var rowNum int
			var vals []string
			for decoder.More() {
				ft := &Feature{}
				err := decoder.Decode(ft)
				if err != nil {
					log.Errorf(`decode feature error, details:%s`, err)
					continue
				}
				rval := prepValues(ft.Properties, cols)
				// switch ds.Crs {
				// case GCJ02:
				// 	ft.Geometry.GCJ02ToWGS84()
				// case BD09:
				// 	ft.Geometry.BD09ToWGS84()
				// default: //WGS84 & CGCS2000
				// }

				// s := fmt.Sprintf("INSERT INTO ggg (id,geom) VALUES (%d,st_setsrid(ST_GeomFromWKB('%s'),4326))", i, wkb.Value(f.Geometry))
				// err := db.Exec(s).Error
				// if err != nil {
				// 	log.Info(err)
				// }
				// geom, err := geojson.NewGeometry(ft.Geometry).MarshalJSON()
				// if err != nil {
				// 	log.Errorf(`preper geometry error, details:%s`, err)
				// 	continue
				// }
				// gval := fmt.Sprintf(`st_setsrid(ST_GeomFromWKB('%s'),4326)`, wkb.Value(f.Geometry))
				// gval := fmt.Sprintf(`st_setsrid(st_geomfromgeojson('%s'),4326)`, string(geom))
				gval := fmt.Sprintf(`st_setsrid(st_force2d(st_geomfromgeojson('%s')),4326)`, ft.Geometry)
				vals = append(vals, fmt.Sprintf(`(%s,%s)`, rval, gval))
				// fmt.Printf(`(%s,%s)/n`, rval, gval)
				if rowNum%1000 == 0 {
					go func(vs []string) {
						t := time.Now()
						st := fmt.Sprintf(`INSERT INTO "%s" (%s,geom) VALUES %s ON CONFLICT DO NOTHING;`, tableName, strings.Join(headers, ","), strings.Join(vs, ",")) // ON CONFLICT (id) DO UPDATE SET (%s) = (%s)
						query := db.Exec(st)
						err := query.Error
						if err != nil {
							log.Error(err)
						}
						log.Infof("inserted %d rows, takes: %v", query.RowsAffected, time.Since(t))
					}(vals)
					var nvals []string
					vals = nvals
				}
				task.Progress = int(rowNum / ds.Total / 2)
				rowNum++
			}
			log.Info("geojson process ", time.Since(s))
			task.Status = "importing"
			st := fmt.Sprintf(`INSERT INTO "%s" (%s,geom) VALUES %s ON CONFLICT DO NOTHING;`, tableName, strings.Join(headers, ","), strings.Join(vals, ",")) // ON CONFLICT (id) DO UPDATE SET (%s) = (%s)
			query := db.Exec(st)
			err = query.Error
			if err != nil {
				log.Error(err)
			}
			log.Infof("total features %d, takes: %v\n", rowNum, time.Since(s))
			return nil
		}
	case SHPEXT, KMLEXT, GPXEXT:
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
		overwrite := true
		if overwrite {
			// params = append(params, "-overwrite")
			//-overwrite not works
			params = append(params, []string{"-lco", "OVERWRITE=YES"}...)
		} else {
			params = append(params, "-update") //open in update model/用更新模式打开,而不是尝试新建
			params = append(params, "-append")
		}

		switch ds.Format {
		case SHPEXT:
			//开启拷贝模式
			//--config PG_USE_COPY YES
			params = append(params, []string{"--config", "PG_USE_COPY", "YES"}...)
			//每个事务group大小
			// params = append(params, "-gt 65536")

			//数据编码选项
			//客户端环境变量
			//SET PGCLIENTENCODINUTF8G=GBK or 'SET client_encoding TO encoding_name'
			// params = append(params, []string{"-sql", "SET client_encoding TO GBK"}...)
			//test first select client_encoding;
			//设置源文件编码
			switch ds.Encoding {
			case "gbk", "big5", "gb18030":
				params = append(params, []string{"--config", "SHAPE_ENCODING", fmt.Sprintf("%s", strings.ToUpper(ds.Encoding))}...)
			}
			//PROMOTE_TO_MULTI can be used to automatically promote layers that mix polygon or multipolygons to multipolygons, and layers that mix linestrings or multilinestrings to multilinestrings. Can be useful when converting shapefiles to PostGIS and other target drivers that implement strict checks for geometry types.
			params = append(params, []string{"-nlt", "PROMOTE_TO_MULTI"}...)
		}
		absPath, err := filepath.Abs(ds.Path)
		if err != nil {
			log.Error(err)
		}
		params = append(params, absPath)
		if runtime.GOOS == "windows" {
			paramsString := strings.Join(params, ",")
			decoder := mahonia.NewDecoder("gbk")
			paramsString = decoder.ConvertString(paramsString)
			params = strings.Split(paramsString, ",")
		}
		task.Status = "importing"
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
		ticker.Stop()
		if err != nil {
			log.Errorf("cmd.Run() failed with %s\n", err)
			return err
		}
		// if errStdout != nil || errStderr != nil {
		// 	log.Errorf("failed to capture stdout or stderr\n")
		// }
		// outStr, errStr := string(stdoutBuf.Bytes()), string(stderrBuf.Bytes())
		// fmt.Printf("\nout:\n%s\nerr:\n%s\n", outStr, errStr)
		return nil
		//保存任务
	default:
		return fmt.Errorf(`dataImport, importing unkown format data:%s`, ds.Format)
	}
	return nil
}

func csvReader(r io.Reader, encoding string) (*csv.Reader, error) {
	switch encoding {
	case "gbk", "big5", "gb18030":
		decoder := mahonia.NewDecoder(encoding)
		if decoder == nil {
			return csv.NewReader(r), fmt.Errorf(`create %s encoder error`, encoding)
		}
		dreader := decoder.NewReader(r)
		return csv.NewReader(dreader), nil
	default:
		return csv.NewReader(r), nil
	}
}

func jsonDecoder(r io.Reader, encoding string) (*json.Decoder, error) {
	switch encoding {
	case "gbk", "big5", "gb18030": //buf reader convert
		mdec := mahonia.NewDecoder(encoding)
		if mdec == nil {
			return json.NewDecoder(r), fmt.Errorf(`create %s encoder error`, encoding)
		}
		mdreader := mdec.NewReader(r)
		return json.NewDecoder(mdreader), nil
	default:
		return json.NewDecoder(r), nil
	}
}

func getDatafiles(dir string) (map[string]int64, error) {
	files := make(map[string]int64)
	items, err := ioutil.ReadDir(dir)
	if err != nil {
		return files, err
	}
	for _, item := range items {
		name := item.Name()
		if item.IsDir() {
			subfiles, _ := getDatafiles(filepath.Join(dir, name))
			for k, v := range subfiles {
				files[k] = v
			}
		}
		ext := filepath.Ext(name)
		//处理zip内部数据文件
		switch ext {
		case CSVEXT, GEOJSONEXT, KMLEXT, GPXEXT:
			files[filepath.Join(dir, name)] = item.Size()
		case SHPEXT:
			shp := filepath.Join(dir, name)
			size := valSizeShp(shp)
			if size > 0 {
				files[shp] = size
			}
		default:
		}
	}
	return files, nil
}

func likelyEncoding(file string) string {
	stat, err := os.Stat(file)
	if err != nil {
		return ""
	}
	bufsize := BUFSIZE
	if stat.Size() < bufsize {
		bufsize = stat.Size()
	}
	r, err := os.Open(file)
	if err != nil {
		return ""
	}
	defer r.Close()
	buf := make([]byte, bufsize)
	rn, err := r.Read(buf)
	if err != nil {
		return ""
	}
	return Mostlike(buf[:rn])
}

//valSizeShp valid shapefile, return 0 is invalid
func valSizeShp(shp string) int64 {
	ext := filepath.Ext(shp)
	if strings.Compare(SHPEXT, ext) != 0 {
		return 0
	}
	info, err := os.Stat(shp)
	if err != nil {
		return 0
	}
	total := info.Size()

	pathname := strings.TrimSuffix(shp, ext)
	info, err = os.Stat(pathname + ".dbf")
	if err != nil {
		return 0
	}
	total += info.Size()

	info, err = os.Stat(pathname + ".shx")
	if err != nil {
		return 0
	}
	total += info.Size()

	info, err = os.Stat(pathname + ".prj")
	if err != nil {
		return 0
	}
	total += info.Size()

	return total
}

//shp2Geojson convert shapefile to geojson
func shp2Geojson(infile, outfile string) error {
	if size := valSizeShp(infile); size == 0 {
		return fmt.Errorf("invalid shapefile")
	}
	var params []string
	//显示进度,读取outbuffer缓冲区
	// absPath, err := filepath.Abs(shp)
	// if err != nil {
	// 	return err
	// }
	if outfile == "" {
		outfile = strings.TrimSuffix(infile, filepath.Ext(infile)) + GEOJSONEXT
	}
	params = append(params, []string{"-f", "GEOJSON", outfile}...)
	params = append(params, "-progress")
	params = append(params, "-skipfailures")
	//-overwrite not works
	params = append(params, []string{"-lco", "OVERWRITE=YES"}...)
	//only for shp
	params = append(params, []string{"-nlt", "PROMOTE_TO_MULTI"}...)
	params = append(params, infile)
	//window上参数转码
	if runtime.GOOS == "windows" {
		decoder := mahonia.NewDecoder("gbk")
		gbk := strings.Join(params, ",")
		gbk = decoder.ConvertString(gbk)
		params = strings.Split(gbk, ",")
	}
	cmd := exec.Command("ogr2ogr", params...)
	err := cmd.Start()
	if err != nil {
		return err
	}
	err = cmd.Wait()
	if err != nil {
		return err
	}
	return nil
}

//kg2Geojson convert kml/gpx to geojson
func kg2Geojson(infile, outfile string) error {
	var params []string
	//显示进度,读取outbuffer缓冲区
	// absPath, err := filepath.Abs(df.Path)
	// if err != nil {
	// 	return err
	// }
	params = append(params, infile)
	if runtime.GOOS == "windows" {
		decoder := mahonia.NewDecoder("gbk")
		gbk := strings.Join(params, ",")
		gbk = decoder.ConvertString(gbk)
		params = strings.Split(gbk, ",")
	}
	log.Println(params)
	cmd := exec.Command("togeojson", params...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	err := cmd.Start()
	if err != nil {
		return err
	}
	err = cmd.Wait()
	if err != nil {
		return err
	}
	if outfile == "" {
		outfile = strings.TrimSuffix(infile, filepath.Ext(infile)) + GEOJSONEXT
	}
	err = ioutil.WriteFile(outfile, stdout.Bytes(), os.ModePerm)
	if err != nil {
		log.Errorf("togeojson write geojson file failed,details: %s\n", err)
		return err
	}
	return nil
}

func toMbtiles(outfile string, infiles []string) error {
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
	fmt.Println(params)
	cmd := exec.Command("tippecanoe", params...)
	err = cmd.Start()
	fmt.Println("cmd.start...")
	if err != nil {
		return err
	}
	err = cmd.Wait()
	fmt.Println("cmd.wait...")
	fmt.Println(err)
	if err != nil {
		return err
	}
	return nil
}

func interfaceFormat(t string, v interface{}) string {

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
			s = strings.Replace(s, "'", "''", -1)
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

	switch t {
	case "BOOL":
		return formatBool(v)
	case "INT4":
		return formatInt(v)
	case "NUMERIC":
		return formatFloat(v)
	case "TIMESTAMPTZ":
		return formatDate(v)
	default: //string->"TEXT" "VARCHAR","BOOL",datetime->"TIMESTAMPTZ",pq.StringArray->"_VARCHAR"
		return formatString(v)
	}
}

func stringFormat(t, v string) string {

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
		if replace := true; replace {
			v = strings.Replace(v, "'", "''", -1)
		}
		return "'" + v + "'"
	}

	switch t {
	case "BOOL":
		return formatBool(v)
	case "INT4":
		return formatInt(v)
	case "NUMERIC": //number
		return formatFloat(v)
	case "TIMESTAMPTZ":
		return formatDate(v)
	default: //string->"TEXT" "VARCHAR","BOOL",datetime->"TIMESTAMPTZ",pq.StringArray->"_VARCHAR"
		return formatString(v)
	}
}

//movetoFeatures move decoder to features
func movetoFeatures(decoder *json.Decoder) error {
	_, err := decoder.Token()
	if err != nil {
		return err
	}
out:
	for {
		t, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		switch v := t.(type) {
		case string:
			if v == "features" {
				t, err := decoder.Token()
				if err != nil {
					return err
				}
				d, ok := t.(json.Delim)
				if ok {
					ds := d.String()
					if ds == "[" {
						break out
					}
				}
			}
		}
	}
	return nil
}
