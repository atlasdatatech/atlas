package main

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"encoding/csv"
	"encoding/json"
	"io"
	math "math"
	"os/exec"
	"runtime"
	"strconv"

	geopkg "github.com/atlasdatatech/go-gpkg/gpkg"

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
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/encoding/wkb"
	"github.com/paulmach/orb/geojson"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"golang.org/x/text/encoding/simplifiedchinese"
	// "github.com/paulmach/orb/encoding/wkb"
)

//BUFSIZE 16M
const (
	BUFSIZE   int64 = 1 << 24
	PREROWNUM       = 7
)

// DataSource 文件数据源集定义结构
type DataSource struct {
	ID        string          `json:"id"  gorm:"primary_key"` //字段列表
	Name      string          `json:"name"`                   //字段列表// 数据集名称,现用于更方便的ID
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
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

//UpInsert 更新/创建数据集概要
func (ds *DataSource) toDataset() *Dataset {
	dt := &Dataset{
		ID:      ds.ID,
		Base:    ds.ID,
		Name:    ds.Name,
		Owner:   ds.Owner,
		Public:  true,
		Tag:     ds.Tag,
		Path:    ds.Path,
		Size:    ds.Size,
		Format:  ds.Format,
		Total:   ds.Total,
		Geotype: ds.Geotype,
	}
	if strings.Contains(string(ds.Geotype), ",") {
		dt.Geotype = Point
	}
	dt.FieldsInfo()
	dt.TotalCount()
	dt.Bound()
	return dt
}

//Save 更新/创建数据集概要
func (ds *DataSource) Save() error {
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
	if x != "" && y != "" {
		ds.Geotype = GeoType(x + "," + y)
	} else {
		ds.Geotype = Attribute
	}
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
	// s := time.Now()
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
	// fmt.Printf("total features %d, takes: %v\n", rowNum, time.Since(s))

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
	// if total == 0 {
	// 	return fmt.Errorf(`empty datafile`)
	// }
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
		fn := v.String()
		ns, err := simplifiedchinese.GB18030.NewDecoder().String(fn)
		if err == nil {
			fn = ns
		}
		fields = append(fields, Field{
			Name: fn,
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

	var geoType GeoType
	switch shape.GeometryType {
	case 1: //POINT
		geoType = Point
	case 3: //POLYLINE
		geoType = LineString
	case 5: //POLYGON
		geoType = MultiPolygon
	case 8: //MULTIPOINT
		geoType = MultiPoint
	}

	ds.Format = SHPEXT
	ds.Size = size
	ds.Total = total
	ds.Geotype = geoType
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
	fts = append(fts, "gid serial primary key")
	fields := []Field{}
	err := json.Unmarshal(ds.Fields, &fields)
	if err != nil {
		log.Errorf(`createDataTable, Unmarshal fields error, details:%s`, err)
		return fts
	}
	for _, v := range fields {
		if v.Name == "gid" || v.Name == "geom" {
			continue
		}
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

		fts = append(fts, fmt.Sprintf(`"%s" %s`, v.Name, t))
	}
	if ds.Geotype != Attribute {
		dbtype := ds.Geotype
		if strings.Contains(string(ds.Geotype), ",") {
			dbtype = Point
		}
		fts = append(fts, fmt.Sprintf("geom geometry(%s,4326)", dbtype))
	}
	return fts
}

func (ds *DataSource) getCreateHeadersLite() []string {
	var fts []string
	fts = append(fts, "fid INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL")
	fields := []Field{}
	err := json.Unmarshal(ds.Fields, &fields)
	if err != nil {
		log.Errorf(`getCreateHeadersLite, Unmarshal fields error, details:%s`, err)
		return fts
	}
	for _, v := range fields {
		if v.Name == "fid" || v.Name == "geom" {
			continue
		}
		var t string
		switch v.Type {
		case Bool, Int: //sqlite 无bool类型，自动存储为整型
			t = "INTEGER"
		case Float:
			t = "REAL"
		// case Date://sqlite 无date类型，自动存储为text或者int，这里选择integer
		// 	t = "DATE"
		default:
			t = "TEXT"
		}
		fts = append(fts, fmt.Sprintf(`"%s" %s`, v.Name, t))
	}
	if ds.Geotype != Attribute {
		dbtype := ds.Geotype
		if strings.Contains(string(ds.Geotype), ",") {
			dbtype = Point
		}
		fts = append(fts, fmt.Sprintf("geom %s", strings.ToUpper(string(dbtype)))) //自动转换为gpkg geom type，类型大写
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
	err = dataDB.Exec(fmt.Sprintf(`DROP TABLE if EXISTS "%s";`, tableName)).Error
	if err != nil {
		log.Errorf(`createDataTable, drop table error, details:%s`, err)
		return err
	}
	headers := []string{}
	switch dbType {
	case Sqlite3:
		headers = ds.getCreateHeadersLite()
	case Postgres:
		headers = ds.getCreateHeaders()
	case Spatialite:
		return fmt.Errorf("unsupport spatialite")
	default:
		return fmt.Errorf("unkown db driver type: %s", dbType)
	}
	st = fmt.Sprintf(`CREATE TABLE "%s" (%s);`, tableName, strings.Join(headers, ","))
	log.Infoln(st)
	err = dataDB.Exec(st).Error
	if err != nil {
		log.Errorf(`importData, create table error, details:%s`, err)
		return err
	}
	return nil
}

//getColumnTypes use datatable column, not fields
func (ds *DataSource) getColumnTypes() ([]*sql.ColumnType, error) {
	tableName := strings.ToLower(ds.ID)
	st := fmt.Sprintf(`SELECT * FROM "%s" LIMIT 0`, tableName)
	rows, err := dataDB.Raw(st).Rows() // (*sql.Rows, error)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}

	idName := ""
	switch dbType {
	case Sqlite3:
		idName = "fid"
	case Postgres:
		idName = "gid"
	}

	var pureColumns []*sql.ColumnType
	for _, col := range cols {
		switch col.Name() {
		case idName:
			continue
		}
		pureColumns = append(pureColumns, col)
	}
	return pureColumns, nil
}

//Import 数据源入库import geojson or csv data, can transform from gcj02 or bd09
func (ds *DataSource) Import(task *Task) error {
	tableName := strings.ToLower(ds.ID)
	geoColumn := "geom"
	switch ds.Format {
	case CSVEXT:
		err := ds.createDataTable()
		if err != nil {
			return err
		}
		switch dbType {
		case Sqlite3:
			upperType := strings.ToUpper(string(ds.Geotype))
			if strings.Index(string(ds.Geotype), ",") != -1 {
				upperType = "POINT"
			}
			geoCol := &geopkg.GeometryColumn{
				GeometryColumnTableName:  tableName,
				ColumnName:               geoColumn,
				GeometryType:             upperType,
				SpatialReferenceSystemId: 4326,
			}
			err := dataDB.Save(geoCol).Error
			if err != nil {
				log.Error(err)
			}
			// //⑤create rtreeindex
			sql := fmt.Sprintf(`CREATE VIRTUAL TABLE "rtree_%s_%s" USING rtree(id, minx, maxx, miny, maxy)`, tableName, geoColumn)
			err = dataDB.Exec(sql).Error
			if err != nil {
				log.Error(err)
			}
			// sql_stmt = sqlite3_mprintf ("INSERT INTO gpkg_extensions "
			// "(table_name, column_name, extension_name, definition, scope) "
			// "VALUES (Lower(%Q), Lower(%Q), 'gpkg_rtree_index', "
			// "'GeoPackage 1.0 Specification Annex L', 'write-only')",
			// table, column);
			rtrExt := &geopkg.Extension{
				Table:      tableName,
				Column:     &geoColumn,
				Extension:  "gpkg_rtree_index",
				Definition: "GeoPackage 1.0 Specification Annex L",
				Scope:      "write-only",
			}
			err = dataDB.Save(rtrExt).Error
			if err != nil {
				log.Error(err)
			}
		case Postgres:
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
		prepIndex := func(cols []*sql.ColumnType, name string) int {
			for i, col := range cols {
				if col.Name() == name {
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
		//找到xy字段
		hasgeom := (ds.Geotype != Attribute) && (ix != -1 && iy != -1)
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
		log.Info(`process headers and get count, `, time.Since(t))
		task.Status = "processing"
		t = time.Now()

		pvs := []string{}
		var rstmt *sql.Stmt
		switch dbType {
		case Sqlite3:
			dataDB.Exec("PRAGMA locking_mode=EXCLUSIVE")    //NORMAL
			defer dataDB.Exec("PRAGMA locking_mode=NORMAL") //NORMAL
			for range headers {
				pvs = append(pvs, "?")
			}
			s := fmt.Sprintf(`INSERT OR REPLACE INTO "rtree_%s_%s" VALUES (?, ?, ?, ?, ?);`, tableName, geoColumn)
			rstmt, err = dataDB.DB().Prepare(s)
			if err != nil {
				log.Error(err)
			}
			defer rstmt.Close()
		case Postgres:
			for i, c := range headers {
				if c == "geom" {
					pvs = append(pvs, fmt.Sprintf("ST_GeomFromWKB($%d,4326)", i+1))
					continue
				}
				pvs = append(pvs, fmt.Sprintf("$%d", i+1))
			}
		}
		sql := fmt.Sprintf(`INSERT INTO "%s" (%s) VALUES (%s) ON CONFLICT DO NOTHING;`, tableName, fmt.Sprintf(`"%s"`, strings.Join(headers, `","`)), strings.Join(pvs, ","))
		log.Println(sql)
		stmt, err := dataDB.DB().Prepare(sql)
		if err != nil {
			log.Error(err)
			return err
		}
		defer stmt.Close()

		task.Status = "importing"
		count := 0
		//for extent
		var minx, maxx, miny, maxy float64 = 181, -181, 91, -91
		for {
			row, err := reader.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				continue
			}
			var vals []interface{}
			for i, col := range cols {
				if col.Name() == "geom" {
					continue
				}
				vals = append(vals, valueFormat(col.DatabaseTypeName(), row[i]))
			}

			var x, y float64
			if hasgeom {
				x, _ = strconv.ParseFloat(row[ix], 64)
				y, _ = strconv.ParseFloat(row[iy], 64)
				switch ds.Crs {
				case GCJ02:
					x, y = Gcj02ToWgs84(x, y)
				case BD09:
					x, y = Bd09ToWgs84(x, y)
				default: //WGS84 & CGCS2000
				}
				pt := orb.Point{x, y}
				switch dbType {
				case Sqlite3:
					geom := buildGpkgGeom(pt, 4326)
					vals = append(vals, geom)
					if x < minx {
						minx = x
					}
					if x > maxx {
						maxx = x
					}
					if y < miny {
						miny = y
					}
					if y > maxy {
						maxy = y
					}
				case Postgres:
					wbkdata := wkb.Value(pt)
					vals = append(vals, wbkdata)
				}
			}
			r, err := stmt.Exec(vals...)
			if err != nil {
				log.Error(err)
				continue
			}
			fid, _ := r.LastInsertId()
			if hasgeom && dbType == Sqlite3 {
				rstmt.Exec(fid, x, x, y, y)
			}
			count++
			task.Progress = int(float64(count) / float64(ds.Total) * 100)
		}
		//gpkg provide add dataset to content
		switch dbType {
		case Sqlite3:
			update := time.Now()
			cts := &geopkg.Content{
				ContentTableName:         tableName,
				DataType:                 "features",
				Identifier:               tableName,
				Description:              "none",
				LastChange:               &update,
				MinX:                     minx,
				MinY:                     miny,
				MaxX:                     maxx,
				MaxY:                     maxy,
				SpatialReferenceSystemId: 4326,
			}
			err = dataDB.Save(cts).Error
			if err != nil {
				log.Error(err)
			}
		}
		log.Infof("inserted %d rows, takes: %v", count, time.Since(t))
		return nil
	case GEOJSONEXT:
		s := time.Now()
		err := ds.createDataTable()
		if err != nil {
			return err
		}
		switch dbType {
		case Sqlite3:
			upperType := strings.ToUpper(string(ds.Geotype))
			if strings.Index(string(ds.Geotype), ",") != -1 {
				upperType = "POINT"
			}
			geoCol := &geopkg.GeometryColumn{
				GeometryColumnTableName:  tableName,
				ColumnName:               geoColumn,
				GeometryType:             upperType,
				SpatialReferenceSystemId: 4326,
			}
			err := dataDB.Save(geoCol).Error
			if err != nil {
				log.Error(err)
			}
			// //⑤create rtreeindex
			sql := fmt.Sprintf(`CREATE VIRTUAL TABLE "rtree_%s_%s" USING rtree(id, minx, maxx, miny, maxy)`, tableName, geoColumn)
			err = dataDB.Exec(sql).Error
			if err != nil {
				log.Error(err)
			}
			rtrExt := &geopkg.Extension{
				Table:      tableName,
				Column:     &geoColumn,
				Extension:  "gpkg_rtree_index",
				Definition: "GeoPackage 1.0 Specification Annex L",
				Scope:      "write-only",
			}
			err = dataDB.Save(rtrExt).Error
			if err != nil {
				log.Error(err)
			}
		case Postgres:
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

		pvs := []string{}
		var rstmt *sql.Stmt
		switch dbType {
		case Sqlite3:
			dataDB.Exec("PRAGMA locking_mode=EXCLUSIVE")    //NORMAL
			defer dataDB.Exec("PRAGMA locking_mode=NORMAL") //NORMAL

			for range headers {
				pvs = append(pvs, "?")
			}
			s := fmt.Sprintf(`INSERT OR REPLACE INTO "rtree_%s_%s" VALUES (?, ?, ?, ?, ?);`, tableName, geoColumn)
			rstmt, err = dataDB.DB().Prepare(s)
			if err != nil {
				log.Error(err)
			}
			defer rstmt.Close()
		case Postgres:
			for i, c := range headers {
				if c == "geom" {
					pvs = append(pvs, fmt.Sprintf("ST_GeomFromWKB($%d,4326)", i+1))
					continue
				}
				pvs = append(pvs, fmt.Sprintf("$%d", i+1))
			}
		}
		sql := fmt.Sprintf(`INSERT INTO "%s" (%s) VALUES (%s) ON CONFLICT DO NOTHING;`, tableName, strings.Join(headers, ","), strings.Join(pvs, ","))
		log.Println(sql)
		stmt, err := dataDB.DB().Prepare(sql)
		if err != nil {
			log.Error(err)
			return err
		}
		defer stmt.Close()
		log.Printf("starting importing ,time: %v", time.Since(s).Seconds())
		task.Status = "importing"
		var rowNum int
		bbox := orb.Bound{Min: orb.Point{181, 91}, Max: orb.Point{-181, -91}}
		s = time.Now()
		for decoder.More() {
			ft := &geojson.Feature{}
			err := decoder.Decode(ft)
			if err != nil {
				log.Errorf(`decode feature error, details:%s`, err)
				continue
			}
			//Properties
			vals := make([]interface{}, len(cols))
			for k, v := range ft.Properties {
				ki, ok := headermap[k]
				if ok {
					vals[ki] = v
				}
			}
			//Geometry
			switch ds.Crs {
			case GCJ02:
				ft.Geometry.GCJ02ToWGS84()
			case BD09:
				ft.Geometry.BD09ToWGS84()
			default: //WGS84 & CGCS2000
			}
			gb := orb.Bound{}
			switch dbType {
			case Sqlite3:
				geom := buildGpkgGeom(ft.Geometry, 4326)
				vals[len(cols)-1] = geom
				gb = ft.Geometry.Bound()
				bbox = bbox.Union(gb)
			case Postgres:
				vals[len(cols)-1] = wkb.Value(ft.Geometry)
			}
			r, err := stmt.Exec(vals...)
			if err != nil {
				log.Error(err)
				continue
			}
			rowid, _ := r.LastInsertId()
			switch dbType {
			case Sqlite3:
				rstmt.Exec(rowid, gb.Left(), gb.Right(), gb.Bottom(), gb.Top())
			}
			if rowNum%1000 == 0 {
				log.Printf("inserting %d rows ,time: %v，", rowNum, time.Since(s))
			}
			rowNum++
			task.Progress = int(float64(rowNum) / float64(ds.Total) * 100)
		}
		//gpkg provide add dataset to content
		switch dbType {
		case Sqlite3:
			update := time.Now()
			cts := &geopkg.Content{
				ContentTableName:         tableName,
				DataType:                 "features",
				Identifier:               tableName,
				Description:              "none",
				LastChange:               &update,
				MinX:                     bbox.Left(),
				MinY:                     bbox.Bottom(),
				MaxX:                     bbox.Right(),
				MaxY:                     bbox.Top(),
				SpatialReferenceSystemId: 4326,
			}
			err = dataDB.Save(cts).Error
			if err != nil {
				log.Error(err)
			}
		}
		log.Infof("total features %d, takes: %v", rowNum, time.Since(s))
		return nil
	case SHPEXT, KMLEXT, GPXEXT:
		var params []string
		//设置数据库
		params = append(params, []string{"-f", "PostgreSQL"}...)
		pg := fmt.Sprintf(`PG:dbname=%s host=%s port=%s user=%s password=%s`,
			viper.GetString("db.datadb"), viper.GetString("db.host"), viper.GetString("db.port"), viper.GetString("db.user"), viper.GetString("db.password"))
		params = append(params, pg)
		params = append(params, []string{"-t_srs", "EPSG:4326"}...)
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

func valueFormat(t, v string) interface{} {

	formatBool := func(v string) interface{} {
		if v == "" {
			return nil
		}
		str := strings.ToLower(v)
		switch str {
		case "false", "no", "0":
			return false
		case "true", "yes", "1":
			return true
		default:
			return true
		}
	}

	formatInt := func(v string) interface{} {
		if v == "" {
			return nil
		}
		i64, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return nil
			}
			i64 = int64(f)
		}
		return i64
	}

	formatFloat := func(v string) interface{} {
		if v == "" {
			return nil
		}
		f64, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil
		}
		return f64
	}

	formatDate := func(v string) interface{} {
		if v == "" {
			return nil
		}
		//string shoud filter the invalid time values
		return v
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

func gpkgMakePoint(x, y float64, srid uint32) []byte {
	header := make([]byte, 2+1+1+4+32)
	header[0] = 0x47    // 'G'
	header[1] = 0x50    // 'P'
	header[2] = byte(0) //version --> 1
	p := orb.Point{x, y}
	buf, err := wkb.Marshal(p, binary.BigEndian)
	if err != nil {
		log.Error(err)
		return nil
	}
	header[3] = byte(1 << 1) //flag --> 00000010 正常
	binary.BigEndian.PutUint32(header[4:8], srid)
	binary.BigEndian.PutUint64(header[8:16], math.Float64bits(x))
	binary.BigEndian.PutUint64(header[16:24], math.Float64bits(x))
	binary.BigEndian.PutUint64(header[24:32], math.Float64bits(y))
	binary.BigEndian.PutUint64(header[32:], math.Float64bits(y))
	headerBuf := bytes.Buffer{}
	_, err = headerBuf.Write(header)
	if err != nil {
		log.Error(err)
		return nil
	}
	_, err = headerBuf.Write(buf)
	if err != nil {
		log.Error(err)
		return nil
	}
	return headerBuf.Bytes()
}

func buildGpkgGeom(geom orb.Geometry, srid uint32) []byte {
	//build headers
	header := make([]byte, 2+1+1+4+32)
	header[0] = 0x47         // 'G'
	header[1] = 0x50         // 'P'
	header[2] = byte(0)      //version --> 1
	header[3] = byte(1 << 1) //flag --> 00000010 正常
	binary.BigEndian.PutUint32(header[4:8], srid)
	//1: envelope is [minx, maxx, miny, maxy], 32 bytes
	gb := geom.Bound()
	binary.BigEndian.PutUint64(header[8:16], math.Float64bits(gb.Left()))
	binary.BigEndian.PutUint64(header[16:24], math.Float64bits(gb.Right()))
	binary.BigEndian.PutUint64(header[24:32], math.Float64bits(gb.Bottom()))
	binary.BigEndian.PutUint64(header[32:], math.Float64bits(gb.Top()))

	headerBuf := bytes.Buffer{}
	_, err := headerBuf.Write(header)
	if err != nil {
		log.Error(err)
		return nil
	}
	buf, err := wkb.Marshal(geom, binary.BigEndian)
	if err != nil {
		log.Error(err)
		return nil
	}
	_, err = headerBuf.Write(buf)
	if err != nil {
		log.Error(err)
		return nil
	}
	return headerBuf.Bytes()
}
