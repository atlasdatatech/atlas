package main

import (
	"bytes"
	"compress/gzip"
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
	"github.com/go-spatial/tegola/mvt"
	"github.com/go-spatial/tegola/provider"
	proto "github.com/golang/protobuf/proto"
	"github.com/jinzhu/gorm"
	_ "github.com/mattn/go-sqlite3" // import sqlite3 driver
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
	"github.com/paulmach/orb/maptile"
	"github.com/paulmach/orb/maptile/tilecover"
	log "github.com/sirupsen/logrus"
	// "github.com/paulmach/orb/encoding/wkb"
)

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
	Tag       string          `json:"-"`
	Owner     string          `json:"owner"`
	Public    bool            `json:"public"`
	Path      string          `json:"-"`
	Format    string          `json:"format"`
	Size      int64           `json:"size"`
	Total     int             `json:"total"`
	Geotype   string          `json:"geotype"`
	BBox      orb.Bound       `json:"bbox"`
	Fields    json.RawMessage `json:"fields" gorm:"type:json"` //字段列表
	Status    bool            `json:"status" gorm:"-"`
	tlayer    *TileLayer
	CreatedAt time.Time
	UpdatedAt time.Time
}

//Service 加载服务
func (dt *Dataset) Service() error {
	dt.Status = true
	return nil
}

//Insert 更新/创建数据集概要
func (dt *Dataset) Insert() error {
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

//Update 更新获取数据集概要
func (dt *Dataset) Update() error {
	err := db.Model(&Dataset{}).Update(dt).Error
	if err != nil {
		return err
	}
	return nil
}

// Bound 更新获取数据范围
func (dt *Dataset) Bound() (orb.Bound, error) {
	bbox := orb.Bound{}
	tbname := strings.ToLower(dt.ID)
	var extent []byte
	stbox := fmt.Sprintf(`SELECT st_asgeojson(st_extent(geom)) as extent FROM "%s";`, tbname)
	err := db.Raw(stbox).Row().Scan(&extent) // (*sql.Rows, error)
	if err != nil {
		return bbox, err
	}
	ext, err := geojson.UnmarshalGeometry(extent)
	if err != nil {
		return bbox, err
	}
	bbox = ext.Geometry().Bound()
	dt.BBox = bbox
	return dt.BBox, nil
}

//TotalCount 获取数据集要素总数
func (dt *Dataset) TotalCount() (int, error) {
	tableName := strings.ToLower(dt.ID)
	var total int
	err := db.Table(tableName).Count(&total).Error
	if err != nil {
		return 0, err
	}
	dt.Total = total
	return dt.Total, nil
}

//FieldsInfo 更新获取字段信息
func (dt *Dataset) FieldsInfo() ([]Field, error) {
	//info from data table
	tableName := strings.ToLower(dt.ID)
	s := fmt.Sprintf(`SELECT * FROM "%s" LIMIT 0;`, tableName)
	rows, err := db.Raw(s).Rows() // (*sql.Rows, error)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
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
		return nil, err
	}
	dt.Fields = jfs
	return fields, nil
}

// GeoType 更新获取几何类型
func (dt *Dataset) GeoType() (string, error) {
	tbname := strings.ToLower(dt.ID)
	var geotype string
	stbox := fmt.Sprintf(`SELECT st_geometrytype(geom) as geotype FROM "%s" limit 1;`, tbname)
	err := db.Raw(stbox).Row().Scan(&geotype) // (*sql.Rows, error)
	if err != nil {
		return "", err
	}
	dt.Geotype = strings.TrimPrefix(geotype, "ST_")
	return dt.Geotype, nil
}

//Tags guess data file encoding
func (dt *Dataset) Tags() []string {
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

// NewTileLayer 新建服务层
func (dt *Dataset) NewTileLayer() (*TileLayer, error) {
	tlayer := &TileLayer{
		ID:      dt.ID,
		Name:    dt.Name,
		MinZoom: 0,
		MaxZoom: 20,
	}
	prd, ok := providers["atlas"]
	if !ok {
		return nil, fmt.Errorf("provider not found")
	}
	tlayer.Provider = prd
	tlayer.ProviderLayerName = dt.ID
	dt.tlayer = tlayer
	cfg := dict.Dict{}
	cfg["name"] = dt.ID
	cfg["tablename"] = strings.ToLower(dt.ID)
	err := prd.AddLayer(cfg)
	if err != nil {
		return nil, err
	}
	return tlayer, nil
}

//Encode TODO (arolek): support for max zoom
func (dt *Dataset) Encode(ctx context.Context, tile *slippy.Tile) ([]byte, error) {
	// tile container
	var mvtTile mvt.Tile
	// wait group for concurrent layer fetching
	mvtLayer := mvt.Layer{
		Name:         dt.Name,
		DontSimplify: false,
	}
	prd, ok := providers["atlas"]
	if !ok {
		return nil, fmt.Errorf("provider not found")
	}
	// fetch layer from data provider
	err := prd.TileFeatures(ctx, dt.ID, tile, func(f *provider.Feature) error {
		// TODO: remove this geom conversion step once the mvt package has adopted the new geom package
		geo, err := ToTegola(f.Geometry)
		if err != nil {
			return err
		}

		mvtLayer.AddFeatures(mvt.Feature{
			ID:       &f.ID,
			Tags:     f.Tags,
			Geometry: geo,
		})

		return nil
	})
	if err != nil {
		switch err {
		case context.Canceled:
			return nil, err
			// TODO (arolek): add debug logs
		default:
			z, x, y := tile.ZXY()
			// TODO (arolek): should we return an error to the response or just log the error?
			// we can't just write to the response as the waitgroup is going to write to the response as well
			log.Printf("err fetching tile (z: %v, x: %v, y: %v) features: %v", z, x, y, err)
			if err.Error() != "too much features" {
				return nil, err
			}
		}
	}

	// stop processing if the context has an error. this check is necessary
	// otherwise the server continues processing even if the request was canceled
	// as the waitgroup was not notified of the cancel
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	// add layers to our tile
	mvtTile.AddLayers(&mvtLayer)

	z, x, y := tile.ZXY()

	// TODO (arolek): change out the tile type for VTile. tegola.Tile will be deprecated
	tegolaTile := tegola.NewTile(uint(z), uint(x), uint(y))

	// generate our tile
	vtile, err := mvtTile.VTile(ctx, tegolaTile)

	if err != nil {
		return nil, err
	}

	// encode our mvt tile
	tileBytes, err := proto.Marshal(vtile)
	if err != nil {
		return nil, err
	}

	// buffer to store our compressed bytes
	var gzipBuf bytes.Buffer

	// compress the encoded bytes
	w := gzip.NewWriter(&gzipBuf)
	_, err = w.Write(tileBytes)
	if err != nil {
		return nil, err
	}

	// flush and close the writer
	if err = w.Close(); err != nil {
		return nil, err
	}

	// return encoded, gzipped tile
	return gzipBuf.Bytes(), nil
}

// CacheMBTiles 新建服务层
func (dt *Dataset) CacheMBTiles(path string) error {
	if dt.tlayer == nil {
		_, err := dt.NewTileLayer()
		if err != nil {
			return err
		}
	}
	dt.Bound()
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
