package main

import (
	"context"
	"database/sql"

	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-spatial/geom"
	"github.com/go-spatial/geom/slippy"
	"github.com/go-spatial/tegola"
	"github.com/go-spatial/tegola/dict"
	"github.com/jinzhu/gorm"
	_ "github.com/mattn/go-sqlite3" // import sqlite3 driver
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
	"github.com/paulmach/orb/maptile"
	"github.com/paulmach/orb/maptile/tilecover"
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
	BBox   orb.Bound

	URL    string // geojson service
	Hash   string
	Status bool
	TLayer *TileLayer
}

// NewTileLayer 新建服务层
func (dts *DataService) NewTileLayer() (*TileLayer, error) {
	tlayer := &TileLayer{
		ID:   dts.ID,
		Name: dts.Name,
	}
	prd, ok := providers["atlas"]
	if !ok {
		return nil, fmt.Errorf("provider not found")
	}
	cfg := dict.Dict{}
	cfg["name"] = dts.Name
	cfg["tablename"] = strings.ToLower(dts.ID)
	err := prd.AddLayer(cfg)
	if err != nil {
		return nil, err
	}
	tlayer.MinZoom = 0
	tlayer.MaxZoom = 20
	tlayer.Provider = prd
	tlayer.ProviderLayerName = dts.Name
	dts.TLayer = tlayer
	return tlayer, nil
}

// CacheMBTiles 新建服务层
func (dts *DataService) CacheMBTiles(path string) error {
	if dts.TLayer == nil {
		_, err := dts.NewTileLayer()
		if err != nil {
			return err
		}
	}
	err := dts.UpdateExtent()
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
		tiles := tilecover.Bound(dts.BBox, maptile.Zoom(z))
		log.Infof("zoom: %d, count: %d", z, len(tiles))
		for t, v := range tiles {
			if !v {
				continue
			}
			tile := slippy.NewTile(uint(t.Z), uint(t.X), uint(t.Y), TileBuffer, tegola.WebMercator)
			// Check to see that the zxy is within the bounds of the map.
			textent := geom.Extent(tile.Bounds())
			if !dts.TLayer.Bounds.Contains(&textent) {
				continue
			}

			pbyte, err := dts.TLayer.Encode(context.Background(), tile)
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
func (dts *DataService) UpdateExtent() error {
	tbname := strings.ToLower(dts.ID)
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
	dts.BBox = bbox
	dts.TLayer.Bounds = &geom.Extent{bbox.Left(), bbox.Bottom(), bbox.Right(), bbox.Top()}
	return nil
}

func (dts *DataService) toDataset() *Dataset {
	out := &Dataset{
		ID:    dts.ID,
		Name:  dts.Name,
		Owner: dts.Owner,
		Type:  dts.Type,
	}
	out.Fields, _ = json.Marshal(dts.Fields)
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
