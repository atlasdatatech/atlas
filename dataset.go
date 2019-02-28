package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/jinzhu/gorm"
	_ "github.com/mattn/go-sqlite3" // import sqlite3 driver
	log "github.com/sirupsen/logrus"
	// "github.com/paulmach/orb/encoding/wkb"
)

// FieldType is a convenience alias that can be used for a more type safe way of
// reason and use Series types.
type FieldType string

// Supported Series Types
const (
	String      FieldType = "string"
	Bool                  = "bool"
	Int                   = "int"
	Float                 = "float"
	Date                  = "date"
	StringArray           = "string_array"
	Geojson               = "geojson"
)

//FieldTypes 支持的字段类型
var FieldTypes = []string{"string", "int", "float", "bool", "date"}

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
	ID        string `json:"id"`   //字段列表
	Name      string `json:"name"` //字段列表// 数据集名称,现用于更方便的ID
	Alias     string `json:"alias"`
	Tag       string `json:"tag"`
	Owner     string `json:"owner"`
	Count     int    `json:"count"`
	Type      string `json:"type"`                    //字段列表
	Fields    []byte `json:"fields" gorm:"type:json"` //字段列表
	CreatedAt time.Time
	UpdatedAt time.Time
}

// DataService 数据集定义-接口
type DataService struct {
	ID     string      `form:"id" json:"id"`         //字段列表
	Name   string      `form:"name" json:"name"`     //字段列表// 数据集名称,现用于更方便的ID
	Owner  string      `form:"owner" json:"owner"`   //字段列表// 显示标签
	Type   string      `form:"type" json:"type"`     //字段列表
	Fields interface{} `form:"fields" json:"fields"` //字段列表

	URL   string // geojson service
	Hash  string
	State bool
}

func (dss *DataService) toDataset() *Dataset {
	out := &Dataset{
		ID:    dss.ID,
		Name:  dss.Name,
		Owner: dss.Owner,
		Type:  dss.Type,
	}
	out.Fields, _ = json.Marshal(dss.Fields)
	return out
}

func (ds *Dataset) toService() *DataService {
	out := &DataService{
		ID:    ds.ID,
		Name:  ds.Name,
		Owner: ds.Owner,
		Type:  ds.Type,
	}
	json.Unmarshal(ds.Fields, &out.Fields)
	return out
}

//UpInsert 更新/创建数据集概要
func (ds *Dataset) UpInsert() error {
	if ds == nil {
		return fmt.Errorf("datafile may not be nil")
	}
	tmp := &Dataset{}
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
	err = db.Model(&Dataset{}).Update(ds).Error
	if err != nil {
		return err
	}
	return nil
}

//getEncoding guess data file encoding
func (ds *Dataset) getTags() []string {
	var tags []string
	if ds == nil {
		log.Errorf("datafile may not be nil")
		return tags
	}

	datasets := []Dataset{}
	err := db.Where("owner = ?", ds.Owner).Find(&datasets).Error
	if err != nil {
		if gorm.IsRecordNotFoundError(err) {
			log.Errorf(`getTags, can not find user datafile, user: %s`, ds.Owner)
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

// GetGeoJSON reads a data in the database
func (ds *Dataset) GetGeoJSON(data *[]byte) error {
	return nil
}

// GetJSONConfig load to config
func (ds *Dataset) GetJSONConfig(data *[]byte) error {
	return nil
}
