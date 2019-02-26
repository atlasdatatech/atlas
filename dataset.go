package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	Owner     string `json:"owner"`
	Path      string `json:"path"`
	Size      int64  `json:"size"`
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
		Path:  dss.URL,
		Type:  dss.Type,
	}
	out.Fields, _ = json.Marshal(dss.Fields)
	return out
}

// LoadDataset setServices returns a ServiceSet that combines all .mbtiles files under
// the directory at baseDir. The DBs will all be served under their relative paths
// to baseDir.
func LoadDataset(dataset string) (*Dataset, error) {
	// 获取所有记录
	fStat, err := os.Stat(dataset)
	if err != nil {
		log.Errorf(`LoadStyle, read style file info error, details: %s`, err)
		return nil, err
	}
	base := filepath.Base(dataset)
	ext := filepath.Ext(dataset)
	name := strings.TrimSuffix(base, ext)
	// id, _ := shortid.Generate()

	out := &Dataset{
		ID:        name,
		Name:      name,
		Owner:     ATLAS,
		Type:      ext,
		Path:      dataset,
		Size:      fStat.Size(),
		UpdatedAt: fStat.ModTime(),
		Fields:    nil,
	}
	switch ext {
	case ".geojson":
		// mb, err := LoadMBTiles(tileset)
		// out.JSON = mb.TileJSON()
	case ".zip":
		// tm, err := LoadTilemap(tileset)
		// out.JSON = tm.TileJSON()
	}

	return out, nil
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

// GetGeoJSON reads a data in the database
func (ds *Dataset) GetGeoJSON(data *[]byte) error {
	return nil
}

// GetJSONConfig load to config
func (ds *Dataset) GetJSONConfig(data *[]byte) error {
	return nil
}
