package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"time"

	"github.com/atlasdatatech/chardet"
	"github.com/jinzhu/gorm"

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
func (dtfile *Datafile) getEncoding() (string, error) {
	if dtfile == nil {
		return "", fmt.Errorf("dtfile may not be nil")
	}
	//detect text encoding
	buf, err := ioutil.ReadFile(dtfile.Path)
	if err != nil {
		return "", fmt.Errorf(`data file read failed, details: %s`, err)
	}
	return chardet.Mostlike(buf), nil
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
