package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
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

// Datafile the data files uploaded.
type Datafile struct {
	ID        string `json:"id"`
	Owner     string `json:"owner"`
	Tag       string `json:"tag"`
	Name      string `json:"name"`
	Format    string `json:"format"`
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Encoding  string `json:"encoding"`
	Srid      string `json:"srid"`
	Type      string `json:"type"`
	Lat       string `json:"lat"`
	Lon       string `json:"lon"`
	Process   string `json:"process"`
	CreatedAt time.Time
	DeletedAt *time.Time `sql:"index"`
}

//upInsert create or update upload data file info into database
func (dtfile *Datafile) upInsert() error {
	if dtfile == nil {
		return fmt.Errorf("dtfile may not be nil")
	}
	df := &Datafile{}
	err := db.Where("id = ?", dtfile.ID).First(df).Error
	if err != nil {
		if gorm.IsRecordNotFoundError(err) {
			err = db.Create(dtfile).Error
			if err != nil {
				return err
			}
		}
		return err
	}
	err = db.Model(&Datafile{}).Update(dtfile).Error
	if err != nil {
		return err
	}
	return nil
}

//getEncoding guess data file encoding
func (dtfile *Datafile) detechEncoding() (string, error) {
	if dtfile == nil {
		return "", fmt.Errorf("dtfile may not be nil")
	}
	//detect text encoding
	buf, err := ioutil.ReadFile(dtfile.Path)
	if err != nil {
		return "", fmt.Errorf(`data file read failed, details: %s`, err)
	}
	en := chardet.Mostlike(buf)
	if dtfile.Encoding == "" {
		dtfile.Encoding = en
	}
	return en, nil
}

//getDataFrame get data preview context
func (dtfile *Datafile) getDataFrame() (dataframe.DataFrame, error) {
	empty := dataframe.New()
	if dtfile == nil {
		return empty, fmt.Errorf("dtfile may not be nil")
	}

	if dtfile.Encoding == "" {
		dtfile.detechEncoding()
	}

	if dtfile.Encoding != "utf-8" {
		buf, err := ioutil.ReadFile(dtfile.Path)
		// ioutil.WriteFile("d:/gbk.csv", buf, os.ModePerm)
		if err != nil {
			log.Errorf(`read data file error, details:%s`, err)
			return empty, err
		}
		//converts gbk to utf-8.
		decode := mahonia.NewDecoder(dtfile.Encoding)
		if decode == nil {
			log.Errorf(`getDataFrame, mahonia new decoder error, data file encoding:%s`, dtfile.Encoding)
			return empty, fmt.Errorf(`getDataFrame, mahonia new decoder error, data file encoding:%s`, dtfile.Encoding)
		}
		_, data, err := decode.Translate(buf, true)
		if err != nil {
			log.Errorf(`getDataFrame, mahonia decode translate error, details:%s`, err)
		}
		// ioutil.WriteFile("d:/utf-8.csv", data, os.ModePerm)
		return dataframe.ReadCSV(bytes.NewReader(data)), nil
	}

	irFile, err := os.Open(dtfile.Path)
	if err != nil {
		log.Errorf(`read data file error, details:%s`, err)
		return empty, err
	}
	defer irFile.Close()
	return dataframe.ReadCSV(irFile), nil
}

//getPreview get data preview context
func (dtfile *Datafile) getPreview() (string, error) {
	if dtfile == nil {
		return "", fmt.Errorf("dtfile may not be nil")
	}
	df := &Datafile{}
	err := db.Where("id = ?", dtfile.ID).First(df).Error
	if err != nil {
		if gorm.IsRecordNotFoundError(err) {
			err = db.Create(dtfile).Error
			if err != nil {
				return "", err
			}
		}
		return "", err
	}
	err = db.Model(&Datafile{}).Update(dtfile).Error
	if err != nil {
		return "", err
	}
	return "", nil
}

//getPreview get data preview context
func (dtfile *Datafile) getXYColumn() string {
	if dtfile == nil {
		log.Errorf("dtfile may not be nil")
		return ""
	}

	df, err := dtfile.getDataFrame()
	if err != nil {
		log.Errorf(`getXYColumn, get dataframe error, details:%s`, err)
		return ""
	}

	xcols := []string{"x", "lon", "longitude", "经度"}
	ycols := []string{"y", "lat", "latitude", "纬度"}

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
		types := df.Types()
		names := df.Names()
		for i, t := range types {
			if t == series.Float {
				ds := df.Select([]string{names[i]}).Describe().Subset([]int{2, 6})
				emin := ds.Elem(0, 1).Float()
				emax := ds.Elem(0, 1).Float()
				if emin > min && emax < max {
					return names[i]
				}
			}
		}
		return ""
	}

	names := df.Names()
	x := getColumn(xcols, names)
	if x == "" {
		x = detechColumn(73, 135)
	}
	y := getColumn(ycols, names)
	if y == "" {
		y = detechColumn(18, 54)
	}

	return x + "," + y
}

// Field represents an mbtiles file connection.
type Field struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Format string `json:"format"`
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
