package main

import (
	"bytes"
	"context"
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
	"sort"
	"strings"
	"time"

	"github.com/atlasdatatech/chardet"
	"github.com/axgle/mahonia"
	"github.com/go-spatial/geom"
	"github.com/go-spatial/geom/slippy"
	"github.com/go-spatial/tegola"
	"github.com/go-spatial/tegola/dict"
	"github.com/jinzhu/gorm"
	shp "github.com/jonas-p/go-shp"
	_ "github.com/mattn/go-sqlite3" // import sqlite3 driver
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
	"github.com/paulmach/orb/maptile"
	"github.com/paulmach/orb/maptile/tilecover"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	// "github.com/paulmach/orb/encoding/wkb"
)

//BUFSIZE 16M
const BUFSIZE int64 = 1 << 24

// Field 字段
type Field struct {
	Name  string `json:"name"`
	Alias string `json:"alias"`
	Type  string `json:"type"`
	Index string `json:"index"`
}

//Fields 字段列表
type Fields []Field

// Dataset 数据集定义结构
type Dataset struct {
	ID        string          `json:"id"`   //字段列表
	Name      string          `json:"name"` //字段列表// 数据集名称,现用于更方便的ID
	Alias     string          `json:"alias"`
	Tag       string          `json:"tag"`
	Owner     string          `json:"owner"`
	Public    bool            `json:"public"`
	Path      string          `json:"path"`
	Format    string          `json:"format"`
	Encoding  string          `json:"encoding"`
	Size      int64           `json:"size"`
	Total     int             `json:"total"`
	Crs       string          `json:"crs"` //WGS84,CGCS2000,GCJ02,BD09
	Geotype   string          `json:"geotype"`
	BBox      orb.Bound       `json:"bbox"`
	Status    bool            `json:"status"`
	Fields    json.RawMessage `json:"fields" gorm:"type:json"` //字段列表
	Rows      [][]string      `json:"rows" gorm:"-"`
	tlayer    *TileLayer
	CreatedAt time.Time
	UpdatedAt time.Time
}

//Service 加载服务
func (dt *Dataset) Service() error {
	dt.Status = true
	return nil
}

// NewTileLayer 新建服务层
func (dt *Dataset) NewTileLayer() (*TileLayer, error) {
	tlayer := &TileLayer{
		ID:   dt.ID,
		Name: dt.Name,
	}
	prd, ok := providers["atlas"]
	if !ok {
		return nil, fmt.Errorf("provider not found")
	}
	cfg := dict.Dict{}
	cfg["name"] = dt.Name
	cfg["tablename"] = strings.ToLower(dt.ID)
	err := prd.AddLayer(cfg)
	if err != nil {
		return nil, err
	}
	tlayer.MinZoom = 0
	tlayer.MaxZoom = 20
	tlayer.Provider = prd
	tlayer.ProviderLayerName = dt.Name
	dt.tlayer = tlayer
	return tlayer, nil
}

// CacheMBTiles 新建服务层
func (dt *Dataset) CacheMBTiles(path string) error {
	if dt.tlayer == nil {
		_, err := dt.NewTileLayer()
		if err != nil {
			return err
		}
	}
	err := dt.UpdateExtent()
	if err != nil {
		log.Errorf(`update datasets extent error, details: %s`, err)
	}
	os.Remove(path)
	dir := filepath.Dir(path)
	os.MkdirAll(dir, os.ModePerm)
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return err
	}
	{
		_, err := db.Exec("PRAGMA synchronous=0")
		if err != nil {
			return err
		}
		_, err = db.Exec("PRAGMA locking_mode=EXCLUSIVE")
		if err != nil {
			return err
		}
		_, err = db.Exec("PRAGMA journal_mode=DELETE")

		if err != nil {
			return err
		}
		_, err = db.Exec("create table if not exists tiles (zoom_level integer, tile_column integer, tile_row integer, tile_data blob);")
		if err != nil {
			return err
		}
		_, err = db.Exec("create table if not exists metadata (name text, value text);")
		if err != nil {
			return err
		}
		_, err = db.Exec("create unique index name on metadata (name);")
		if err != nil {
			return err
		}
		_, err = db.Exec("create unique index tile_index on tiles(zoom_level, tile_column, tile_row);")
		if err != nil {
			return err
		}
	}

	minzoom, maxzoom := 7, 9
	for z := minzoom; z <= maxzoom; z++ {
		tiles := tilecover.Bound(dt.BBox, maptile.Zoom(z))
		log.Infof("zoom: %d, count: %d", z, len(tiles))
		for t, v := range tiles {
			if !v {
				continue
			}
			tile := slippy.NewTile(uint(t.Z), uint(t.X), uint(t.Y), TileBuffer, tegola.WebMercator)
			// Check to see that the zxy is within the bounds of the map.
			textent := geom.Extent(tile.Bounds())
			if !dt.tlayer.Bounds.Contains(&textent) {
				continue
			}

			pbyte, err := dt.tlayer.Encode(context.Background(), tile)
			if err != nil {
				errMsg := fmt.Sprintf("error marshalling tile: %v", err)
				log.Error(errMsg)
				continue
			}
			if len(pbyte) == 0 {
				continue
			}
			log.Infof("%v", t)
			_, err = db.Exec("insert into tiles (zoom_level, tile_column, tile_row, tile_data) values (?, ?, ?, ?);", t.Z, t.X, t.Y, pbyte)
			if err != nil {
				log.Error(err)
				continue
			}
		}
	}
	//should save tilejson
	db.Close()
	return nil
}

// UpdateExtent 更新图层外边框
func (dt *Dataset) UpdateExtent() error {
	tbname := strings.ToLower(dt.ID)
	var extent []byte
	stbox := fmt.Sprintf(`SELECT st_asgeojson(st_extent(geom)) as extent FROM "%s";`, tbname)
	err := db.Raw(stbox).Row().Scan(&extent) // (*sql.Rows, error)
	if err != nil {
		return err
	}
	ext, err := geojson.UnmarshalGeometry(extent)
	if err != nil {
		return err
	}
	bbox := ext.Geometry().Bound()
	dt.BBox = bbox
	dt.tlayer.Bounds = &geom.Extent{bbox.Left(), bbox.Bottom(), bbox.Right(), bbox.Top()}
	return nil
}

//UpInsert 更新/创建数据集概要
func (dt *Dataset) UpInsert() error {
	if dt == nil {
		return fmt.Errorf("datafile may not be nil")
	}
	tmp := &Dataset{}
	err := db.Where("id = ?", dt.ID).First(tmp).Error
	if err != nil {
		if gorm.IsRecordNotFoundError(err) {
			err = db.Create(dt).Error
			if err != nil {
				return err
			}
		}
		return err
	}
	err = db.Model(&Dataset{}).Update(dt).Error
	if err != nil {
		return err
	}
	return nil
}

//getEncoding guess data file encoding
func (dt *Dataset) getTags() []string {
	var tags []string
	if dt == nil {
		log.Errorf("datafile may not be nil")
		return tags
	}

	datasets := []Dataset{}
	err := db.Where("owner = ?", dt.Owner).Find(&datasets).Error
	if err != nil {
		if gorm.IsRecordNotFoundError(err) {
			log.Errorf(`getTags, can not find user datafile, user: %s`, dt.Owner)
			return tags
		}
		log.Errorf(`getTags, get data file info error, details: %s`, err)
		return tags
	}
	mtags := make(map[string]int)
	for _, dataset := range datasets {
		tag := dataset.Tag
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

// LoadFromCSV 从csv数据文件加载数据集信息
func (dt *Dataset) LoadFromCSV() error {
	if dt.Encoding == "" {
		dt.Encoding = likelyEncoding(dt.Path)
	}
	file, err := os.Open(dt.Path)
	if err != nil {
		return err
	}
	defer file.Close()
	reader, err := csvReader(file, dt.Encoding)
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
	dt.Format = ".csv"
	dt.Total = rowNum
	dt.Geotype = x + "," + y
	dt.Rows = records
	flds, err := json.Marshal(fields)
	if err == nil {
		dt.Fields = flds
	}
	return nil
}

// LoadFromJSON 从geojson数据文件加载数据集信息
func (dt *Dataset) LoadFromJSON() error {
	if dt.Encoding == "" {
		dt.Encoding = likelyEncoding(dt.Path)
	}
	file, err := os.Open(dt.Path)
	if err != nil {
		return err
	}
	defer file.Close()

	dec, err := jsonDecoder(file, dt.Encoding)
	if err != nil {
		return err
	}

	s := time.Now()

	err = movetoFeatures(dec)
	if err != nil {
		return err
	}

	prepRow := func(ft *geojson.Feature) []string {
		var row []string
		for _, v := range ft.Properties {
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
			row = append(row, s)
		}
		return row
	}
	ft := &geojson.Feature{}
	var rows [][]string
	var rowNum, preNum int
	for dec.More() {
		if preNum < 7 {
			err := dec.Decode(ft)
			if err != nil {
				log.Errorf(`Decode error, details:%s`, err)
				continue
			}
			rows = append(rows, prepRow(ft))
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

	var fields []Field
	geoType := ft.Geometry.GeoJSONType()
	for k, v := range ft.Properties {
		var t string
		switch v.(type) {
		case bool:
			t = "bool" //or 'timestamp with time zone'
		case float64:
			t = "float"
		default: //string/map[string]interface{}/[]interface{}/nil->对象/数组都作string处理
			t = "string"
		}
		fields = append(fields, Field{
			Name: k,
			Type: t,
		})
	}
	dt.Format = ".geojson"
	dt.Total = rowNum
	dt.Geotype = geoType
	dt.Rows = rows
	flds, err := json.Marshal(fields)
	if err == nil {
		dt.Fields = flds
	}
	return nil
}

// LoadFromShp 从shp数据文件加载数据集信息
func (dt *Dataset) LoadFromShp() error {
	file := dt.Path
	encoding := dt.Encoding
	size := valSizeShp(file)
	if size == 0 {
		return fmt.Errorf("invalid shapefiles")
	}
	shape, err := shp.Open(file)
	// open a shapefile for reading
	if err != nil {
		return err
	}
	defer shape.Close()

	// fields from the attribute table (DBF)
	shpfields := shape.Fields()
	fcount := shape.AttributeCount()
	if fcount == 0 {
		log.Error(`empty datafile`)
		return err
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
	if encoding == "" {
		encoding = "utf-8"
	}
	var mdec mahonia.Decoder
	switch encoding {
	case "gbk", "big5", "gb18030":
		mdec = mahonia.NewDecoder(encoding)
		if mdec == nil {
			encoding = "utf-8"
		}
	}

	var rows [][]string
	for shape.Next() {
		var row []string
		for k := range fields {
			val := shape.Attribute(k)
			switch encoding {
			case "gbk", "big5", "gb18030":
				row = append(row, mdec.ConvertString(val))
			default:
				row = append(row, val)

			}
		}
		rows = append(rows, row)
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

	dt.Format = ".shp"
	dt.Size = size
	dt.Geotype = geoType
	dt.Total = fcount
	dt.Encoding = encoding
	dt.Rows = rows
	jfs, err := json.Marshal(fields)
	if err == nil {
		dt.Fields = jfs
	} else {
		log.Error(err)
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

// LoadDataset 从文件加载数据集
func LoadDataset(file string) (*Dataset, error) {
	// 获取所有记录
	stat, err := os.Stat(file)
	if err != nil {
		return nil, err
	}
	base := filepath.Base(file)
	ext := filepath.Ext(base)
	id := strings.TrimSuffix(base, ext)
	name := strings.TrimSuffix(id, filepath.Ext(id))
	dt := &Dataset{
		ID:     id,
		Name:   name,
		Format: ext,
		Path:   file,
		Size:   stat.Size(),
	}
	switch ext {
	case ".csv":
		err := dt.LoadFromCSV()
		if err != nil {
			return nil, err
		}
	case ".geojson":
		err := dt.LoadFromJSON()
		if err != nil {
			return nil, err
		}
	case ".shp":
		err := dt.LoadFromShp()
		if err != nil {
			return nil, err
		}
	}
	return dt, nil
}

// LoadDatasets 加载数据文件
func LoadDatasets(dir string) ([]*Dataset, error) {
	// 获取所有记录
	var dts []*Dataset
	_, err := os.Stat(dir)
	if err != nil {
		return dts, err
	}
	base := filepath.Base(dir)
	extid := filepath.Ext(base)
	name := strings.TrimSuffix(base, extid)
	files, err := getDatafiles(dir)
	for file, size := range files {
		subase := filepath.Base(file)
		ext := filepath.Ext(file)
		subname := strings.TrimSuffix(subase, ext)
		dt := &Dataset{
			ID:      subname + extid,
			Name:    subname,
			Tag:     name,
			Geotype: "vector",
			Format:  ext,
			Path:    file,
			Size:    size,
		}
		switch ext {
		case ".csv":
			err := dt.LoadFromCSV()
			if err != nil {
				log.Error(err)
				continue
			}
		case ".geojson":
			err := dt.LoadFromJSON()
			if err != nil {
				log.Error(err)
				continue
			}
		case ".shp":
			err := dt.LoadFromShp()
			if err != nil {
				log.Error(err)
				continue
			}
		}
		dts = append(dts, dt)
	}
	return dts, nil
}

//getCreateHeaders auto add 'gid' & 'geom'
func (dt *Dataset) getCreateHeaders() []string {
	var fts []string
	fields := []Field{}
	err := json.Unmarshal(dt.Fields, &fields)
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
	if dt.Geotype != "" {
		dbtype := dt.Geotype
		if strings.Contains(dt.Geotype, ",") {
			dbtype = Point
		}
		fts = append(fts, fmt.Sprintf("geom geometry(%s,4326)", dbtype))
	}
	return fts
}

func (dt *Dataset) createDataTable() error {
	tableName := strings.ToLower(dt.ID)
	st := fmt.Sprintf(`DELETE FROM datasets WHERE id='%s';`, dt.ID)
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
	headers := dt.getCreateHeaders()
	st = fmt.Sprintf(`CREATE TABLE "%s" (%s);`, tableName, strings.Join(headers, ","))
	log.Infoln(st)
	err = db.Exec(st).Error
	if err != nil {
		log.Errorf(`importData, create table error, details:%s`, err)
		return err
	}
	return nil
}

func (dt *Dataset) getColumnTypes() ([]*sql.ColumnType, error) {
	tableName := strings.ToLower(dt.ID)
	var st string
	fields := []Field{}
	err := json.Unmarshal(dt.Fields, &fields)
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

//dataImport import geojson or csv data, can transform from gcj02 or bd09
func (dt *Dataset) dataImport() *Task {
	task := &Task{
		ID:   dt.ID,
		Type: "dataset",
		Pipe: make(chan struct{}),
	}
	//任务队列
	select {
	case taskQueue <- task:
		// default:
		// 	log.Warningf("task queue overflow, request refused...")
		// 	task.Status = "task queue overflow, request refused"
		// 	return task, fmt.Errorf("task queue overflow, request refused")
	}
	//任务集
	taskSet.Store(task.ID, task)
	go func(dt *Dataset, ts *Task) {
		tableName := strings.ToLower(dt.ID)
		switch dt.Format {
		case ".csv", ".geojson":
			err := dt.createDataTable()
			if err != nil {
				task.Err = err.Error()
				task.Pipe <- struct{}{}
				return
			}
			cols, err := dt.getColumnTypes()
			if err != nil {
				task.Err = err.Error()
				task.Pipe <- struct{}{}
				return
			}
			var headers []string
			for _, col := range cols {
				headers = append(headers, col.Name())
			}
			switch dt.Format {
			case ".csv":
				prepValues := func(row []string, cols []*sql.ColumnType) string {
					var vals []string
					for i, col := range cols {
						s := stringFormat(col.DatabaseTypeName(), row[i])
						vals = append(vals, s)
					}
					return strings.Join(vals, ",")
				}
				t := time.Now()
				file, err := os.Open(dt.Path)
				if err != nil {
					task.Err = `open data file error`
					task.Pipe <- struct{}{}
					return
				}
				defer file.Close()
				reader, err := csvReader(file, dt.Encoding)
				csvHeaders, err := reader.Read()
				if err != nil {
					task.Err = fmt.Sprintf(`dataImport, read headers failed: %s`, err)
					task.Pipe <- struct{}{}
					return
				}
				if len(cols) != len(csvHeaders) {
					log.Errorf(`dataImport, dbfield len != csvheader len: %s`, err)
					task.Err = `dbfield len != csvheader len`
					task.Pipe <- struct{}{}
					return
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
				xy := strings.Split(dt.Geotype, ",")
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
						switch dt.Crs {
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
							st := fmt.Sprintf(`INSERT INTO "%s" (%s) VALUES %s ON CONFLICT DO NOTHING;`, tableName, strings.Join(headers, ","), strings.Join(vals, ",")) // ON CONFLICT (id) DO UPDATE SET (%s) = (%s)
							query := db.Exec(st)
							err := query.Error
							if err != nil {
								task.Err = err.Error()
							}
							log.Infof("inserted %d rows, takes: %v", query.RowsAffected, time.Since(t))
						}(vals)
						var nvals []string
						vals = nvals
					}
					task.Progress = int(count / dt.Total / 5)
					count++
				}
				fmt.Println(`csv process `, time.Since(t))
				t = time.Now()
				task.Status = "importing"
				st := fmt.Sprintf(`INSERT INTO "%s" (%s) VALUES %s ON CONFLICT DO NOTHING;`, tableName, strings.Join(headers, ","), strings.Join(vals, ",")) // ON CONFLICT (id) DO UPDATE SET (%s) = (%s)
				query := db.Exec(st)
				err = query.Error
				if err != nil {
					log.Errorf(`task failed, details:%s`, err)
					task.Status = "failed"
				}
				log.Infof("csv insert %d rows, takes: %v/n", count, time.Since(t))
				task.Succeed = count
				task.Progress = 100
				task.Status = "finished"
				task.Err = ""
				task.Pipe <- struct{}{}
				return
			case ".geojson":
				prepValues := func(props geojson.Properties, cols []*sql.ColumnType) string {
					var vals []string
					for i, col := range cols {
						s := interfaceFormat(col.DatabaseTypeName(), props[headers[i]])
						vals = append(vals, s)
					}
					return strings.Join(vals, ",")
				}
				s := time.Now()
				file, err := os.Open(dt.Path)
				if err != nil {
					task.Err = `open data file error`
					task.Pipe <- struct{}{}
					return
				}
				defer file.Close()
				decoder, err := jsonDecoder(file, dt.Encoding)
				if err != nil {
					task.Err = `open data file error`
					task.Pipe <- struct{}{}
					return
				}
				err = movetoFeatures(decoder)
				if err != nil {
					task.Err = `open data file error`
					task.Pipe <- struct{}{}
					return
				}
				type Feature struct {
					Type       string                 `json:"type"`
					Geometry   json.RawMessage        `json:"geometry"`
					Properties map[string]interface{} `json:"properties"`
				}

				t := time.Now()
				task.Status = "processing"
				var rowNum int
				var vals []string
				for decoder.More() {
					// ft := &geojson.Feature{}
					ft := &Feature{}
					err := decoder.Decode(ft)
					if err != nil {
						log.Errorf(`decode feature error, details:%s`, err)
						continue
					}
					rval := prepValues(ft.Properties, cols)
					// switch dt.Crs {
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
								task.Err = err.Error()
							}
							log.Infof("inserted %d rows, takes: %v", query.RowsAffected, time.Since(t))
						}(vals)
						var nvals []string
						vals = nvals
					}
					task.Progress = int(rowNum / dt.Total / 2)
					rowNum++
				}
				fmt.Println("geojson process ", time.Since(t))
				fmt.Printf("total features %d, takes: %v\n", rowNum, time.Since(s))
				task.Status = "importing"
				t = time.Now()
				st := fmt.Sprintf(`INSERT INTO "%s" (%s,geom) VALUES %s ON CONFLICT DO NOTHING;`, tableName, strings.Join(headers, ","), strings.Join(vals, ",")) // ON CONFLICT (id) DO UPDATE SET (%s) = (%s)
				// log.Println(st)
				query := db.Exec(st)
				err = query.Error
				if err != nil {
					task.Err = err.Error()
				}
				fmt.Println("geojson insert ", time.Since(t))
				task.Count = rowNum
				task.Succeed = int(query.RowsAffected)
				task.Progress = 100
				task.Status = "finished"
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
			overwrite := true
			if overwrite {
				// params = append(params, "-overwrite")
				//-overwrite not works
				params = append(params, []string{"-lco", "OVERWRITE=YES"}...)
			} else {
				params = append(params, "-update") //open in update model/用更新模式打开,而不是尝试新建
				params = append(params, "-append")
			}

			switch dt.Format {
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
			absPath, err := filepath.Abs(dt.Path)
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
			task.Status = "finished"
			task.Pipe <- struct{}{}
			return
			//保存任务
		default:
			task.Err = fmt.Sprintf(`dataImport, importing unkown format data:%s`, dt.Format)
			task.Pipe <- struct{}{}
			return
		}
	}(dt, task)

	return task
}

//toDataset 创建Dataset
func (dt *Dataset) updateFromTable() error {
	//info from data table
	tableName := strings.ToLower(dt.ID)
	s := fmt.Sprintf(`SELECT * FROM "%s" LIMIT 0;`, tableName)
	log.Println(s)
	rows, err := db.Raw(s).Rows() // (*sql.Rows, error)
	if err != nil {
		return err
	}
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil {
		return err
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
		return err
	}
	var cnt int
	db.Table(tableName).Count(&cnt)
	dt.Total = cnt
	dt.Fields = jfs
	return nil
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
		case ".csv", ".geojson", ".kml", ".gpx":
			files[filepath.Join(dir, name)] = item.Size()
		case ".shp":
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
	return chardet.Mostlike(buf[:rn])
}

//valSizeShp valid shapefile, return 0 is invalid
func valSizeShp(shp string) int64 {
	ext := filepath.Ext(shp)
	if strings.Compare(".shp", ext) != 0 {
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
		outfile = strings.TrimSuffix(infile, filepath.Ext(infile)) + ".geojson"
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
		outfile = strings.TrimSuffix(infile, filepath.Ext(infile)) + ".geojson"
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
