package main

import (
	"encoding/json"

	_ "github.com/mattn/go-sqlite3" // import sqlite3 driver
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
	ID     string `json:"id"`                      //字段列表
	Name   string `json:"name"`                    //字段列表// 数据集名称,现用于更方便的ID
	Label  string `json:"label"`                   //字段列表// 显示标签
	Type   string `json:"type"`                    //字段列表
	Fields []byte `json:"fields" gorm:"type:json"` //字段列表
}

// DataService 数据集定义-接口
type DataService struct {
	ID     string      `form:"id" json:"id"`         //字段列表
	Name   string      `form:"name" json:"name"`     //字段列表// 数据集名称,现用于更方便的ID
	Label  string      `form:"label" json:"label"`   //字段列表// 显示标签
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
		Label: dss.Label,
		Type:  dss.Type,
	}
	out.Fields, _ = json.Marshal(dss.Fields)
	return out
}

func (ds *Dataset) toService() *DataService {
	out := &DataService{
		ID:    ds.ID,
		Name:  ds.Name,
		Label: ds.Label,
		Type:  ds.Type,
	}
	json.Unmarshal(ds.Fields, &out.Fields)
	return out
}

// GetGeoJSON reads a data in the database
func (ds *Dataset) GetGeoJSON(data *[]byte) error {
	return nil
}

// GetJSONConfig load to config
func (ds *Dataset) GetJSONConfig(data *[]byte) error {
	return nil
}
