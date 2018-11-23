package main

import (
	"fmt"

	"github.com/jinzhu/gorm"

	_ "github.com/mattn/go-sqlite3" // import sqlite3 driver
	log "github.com/sirupsen/logrus"
)

// DataService represents an mbtiles file connection.
type DataService struct {
	ID      string
	URL     string // geojson service
	Hash    string
	State   bool     // true if UTFGrids have corresponding key / value data that need to be joined and returned with the UTFGrid
	Dataset *Dataset // database connection for mbtiles file
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
		Dataset: dataset,
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
func (dataset *Dataset) GetGeoJSON(data *[]byte) error {
	return nil
}

// GetJSONConfig load to config
func (dataset *Dataset) GetJSONConfig(data *[]byte) error {
	return nil
}
