package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"sort"
	"strings"
	"time"

	"github.com/atlasdatatech/chardet"
	"github.com/axgle/mahonia"
	"github.com/jinzhu/gorm"
	"github.com/kniren/gota/dataframe"
	"github.com/kniren/gota/series"

	_ "github.com/mattn/go-sqlite3" // import sqlite3 driver
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

//CRSs supported CRSs
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

//Encodings supported encodings
var Encodings = []string{"utf-8", "gbk", "big5", "gb18030"}

// FieldType is a convenience alias that can be used for a more type safe way of
// reason and use Series types.
type FieldType string

// Supported Series Types
const (
	String FieldType = "string"
	Int              = "int"
	Float            = "float"
	Bool             = "bool"
	Date             = "date"
)

//FieldTypes supported types
var FieldTypes = []string{"string", "int", "float", "bool", "date"}

// Datafile the data files uploaded.
type Datafile struct {
	ID        string `json:"id"`
	Owner     string `json:"owner"`
	Tag       string `json:"tag"`
	Name      string `json:"name"`
	Alias     string `json:"alias"`
	Format    string `json:"format"`
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Encoding  string `json:"encoding"`
	Crs       string `json:"crs"` //WGS84,CGCS2000,GCJ02,BD09
	Type      string `json:"type"`
	Lat       string `json:"lat"`
	Lon       string `json:"lon"`
	Process   string `json:"process"`
	CreatedAt time.Time
	DeletedAt *time.Time `sql:"index"`
}

// DataPreview the data files uploaded.
type DataPreview struct {
	ID        string     `json:"id" form:"id" binding:"required"`
	Tags      []string   `json:"tags" form:"tags" binding:"required"`
	Name      string     `json:"name" form:"name" binding:"required"`
	Alias     string     `json:"alias" form:"alias" binding:"required"`
	Encodings []string   `json:"encodings" form:"encodings" binding:"required"`
	Crss      []string   `json:"crss" form:"crss" binding:"required"` //WGS84,CGCS2000,GCJ02,BD09
	Lon       string     `json:"lon" form:"lon" binding:"required"`
	Lat       string     `json:"lat" form:"lat" binding:"required"`
	Fields    []Field    `json:"fields" form:"fields" binding:"required"`
	Rows      [][]string `json:"rows" form:"rows"`
}

//upInsert create or update upload data file info into database
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

//getDataFrame get data preview context
func (df *Datafile) getDataFrame() (dataframe.DataFrame, error) {
	empty := dataframe.New()
	if df == nil {
		return empty, fmt.Errorf("datafile may not be nil")
	}

	buf, err := ioutil.ReadFile(df.Path)
	if err != nil {
		log.Errorf(`read datafile error, details:%s`, err)
		return empty, err
	}

	if df.Encoding == "" {
		df.Encoding = chardet.Mostlike(buf)
	}

	if df.Encoding != "utf-8" {
		//converts gbk to utf-8.
		decode := mahonia.NewDecoder(df.Encoding)
		if decode == nil {
			log.Errorf(`getDataFrame, mahonia new decoder error, data file encoding:%s`, df.Encoding)
			return empty, fmt.Errorf(`getDataFrame, mahonia new decoder error, data file encoding:%s`, df.Encoding)
		}
		_, data, err := decode.Translate(buf, true)
		if err != nil {
			log.Errorf(`getDataFrame, mahonia decode translate error, details:%s`, err)
		}
		// ioutil.WriteFile("d:/utf-8.csv", data, os.ModePerm)
		return dataframe.ReadCSV(bytes.NewReader(data)), nil
	}

	return dataframe.ReadCSV(bytes.NewReader(buf)), nil
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

	frame, err := df.getDataFrame()
	if err != nil {
		log.Errorf(`dataPreview, get dataframe error, details:%s`, err)
		return dp
	}

	var fields []Field
	names := frame.Names()
	types := frame.Types()
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
	if df.Lon == "" {
		df.Lon = x
	}
	ycols := []string{"y", "lat", "latitude", "纬度"}
	y := getColumn(ycols, names)
	if y == "" {
		y = detechColumn(18, 54)
	}
	if df.Lat == "" {
		df.Lat = y
	}

	n := frame.Nrow()
	if n > 7 {
		frame = frame.Subset([]int{n / 7, 2 * n / 7, 3 * n / 7, 4 * n / 7, 5 * n / 7, 6 * n / 7, n - 1})
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
	dp.Lon = df.Lon
	dp.Lat = df.Lat
	dp.Tags = df.getTags()
	dp.Encodings = aHead(Encodings, df.Encoding)
	dp.Crss = aHead(CRSs, df.Crs)
	dp.Fields = fields
	dp.Rows = frame.Records()
	// copy(dp.Rows, frame.Records())
	return dp
}

//getPreview get data preview context
func (df *Datafile) getFields() []Field {
	var fields []Field
	if df == nil {
		log.Errorf("datafile may not be nil")
		return fields
	}

	frame, err := df.getDataFrame()
	if err != nil {
		log.Errorf(`getXYColumn, get dataframe error, details:%s`, err)
		return fields
	}
	names := frame.Names()
	types := frame.Types()
	for i, n := range names {
		fields = append(fields, Field{
			Name: n,
			Type: string(types[i])})
	}
	return fields
}

//getPreview get data preview context
func (df *Datafile) detechLonLat() (string, string) {
	if df == nil {
		log.Errorf("datafile may not be nil")
		return "", ""
	}

	frame, err := df.getDataFrame()
	if err != nil {
		log.Errorf(`getXYColumn, get dataframe error, details:%s`, err)
		return "", ""
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

	names := frame.Names()
	xcols := []string{"x", "lon", "longitude", "经度"}
	x := getColumn(xcols, names)
	if x == "" {
		x = detechColumn(73, 135)
	}
	if df.Lon == "" {
		df.Lon = x
	}
	ycols := []string{"y", "lat", "latitude", "纬度"}
	y := getColumn(ycols, names)
	if y == "" {
		y = detechColumn(18, 54)
	}
	if df.Lat == "" {
		df.Lat = y
	}

	return x, y
}

//getPreview get data preview context
func (dp *DataPreview) toDatafile() *Datafile {
	if dp == nil {
		log.Errorf("dataPreview may not be nil")
		return nil
	}
	df := &Datafile{
		ID:       dp.ID,
		Name:     dp.Name,
		Alias:    dp.Alias,
		Encoding: dp.Encodings[0],
		Crs:      dp.Crss[0],
		Lon:      dp.Lon,
		Lat:      dp.Lat,
	}
	if dp.Tags != nil && len(dp.Tags) > 0 {
		df.Tag = dp.Tags[0]
	}

	return df
}

// Field represents an mbtiles file connection.
type Field struct {
	Name  string `json:"name"`
	Alias string `json:"alias"`
	Type  string `json:"type"`
	Index string `json:"index"`
}

// Dataset represents an mbtiles file connection.
type Dataset struct {
	ID     string `json:"id"`                      //字段列表
	Name   string `json:"name"`                    //字段列表// 数据集名称,现用于更方便的ID
	Label  string `json:"label"`                   //字段列表// 显示标签
	Type   string `json:"type"`                    //字段列表
	Fields []byte `json:"fields" gorm:"type:json"` //字段列表
}

// DatasetBind represents an mbtiles file connection.
type DatasetBind struct {
	ID     string      `form:"id" json:"id"`         //字段列表
	Name   string      `form:"name" json:"name"`     //字段列表// 数据集名称,现用于更方便的ID
	Label  string      `form:"label" json:"label"`   //字段列表// 显示标签
	Type   string      `form:"type" json:"type"`     //字段列表
	Fields interface{} `form:"fields" json:"fields"` //字段列表
}

// DataService represents an mbtiles file connection.
type DataService struct {
	ID      string
	URL     string // geojson service
	Hash    string
	State   bool         // true if UTFGrids have corresponding key / value data that need to be joined and returned with the UTFGrid
	Dataset *DatasetBind // database connection for mbtiles file
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
