package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "github.com/go-spatial/tegola/provider/postgis"
	"github.com/jinzhu/gorm"
	_ "github.com/mattn/go-sqlite3" // import sqlite3 driver
	log "github.com/sirupsen/logrus"
)

const (
	// MBTILESEXT mbtiles ext format
	MBTILESEXT = ".mbtiles"
)

//Tileset 样式库
type Tileset struct {
	ID        string     `json:"id" gorm:"primary_key"`
	Version   string     `json:"version"`
	Name      string     `json:"name" gorm:"not null"`
	Tag       string     `json:"-"`
	Owner     string     `json:"owner" gorm:"index;not null"`
	Format    TileFormat `json:"format"`
	Public    bool       `json:"public"`
	Path      string     `json:"-"`
	URL       string     `json:"url"`
	Size      int64      `json:"size"`
	Layers    []byte     `json:"layers" ` //gorm:"type:json"
	JSON      []byte     `json:"json" `   //gorm:"column:json;type:json"
	Status    bool       `json:"status" gorm:"-"`
	db        *sql.DB    // database connection for mbtiles file
	timestamp time.Time  // timestamp of file, for cache control headers
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

//LoadTileset 创建更新瓦片集服务
//create or update upload data file info into database
func LoadTileset(ds *DataSource) (*Tileset, error) {
	stat, err := os.Stat(ds.Path)
	if err != nil {
		log.Errorf(`LoadTileset, read style file info error, details: %s`, err)
		return nil, err
	}
	out := &Tileset{
		ID:        ds.ID,
		Version:   "v3",
		Name:      ds.Name,
		Owner:     ds.Owner,
		Public:    true, //服务集默认是公开的
		Path:      ds.Path,
		Size:      stat.Size(),
		UpdatedAt: stat.ModTime(),
		Layers:    nil,
		JSON:      nil,
	}
	//Saves last modified mbtiles time for setting Last-Modified header
	db, err := sql.Open("sqlite3", ds.Path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var data []byte
	err = db.QueryRow("select tile_data from tiles limit 1").Scan(&data)
	if err != nil {
		return nil, err
	}
	format, err := detectTileFormat(data)
	if err != nil {
		format = PBF // GZIP masks PBF, which is only expected type for tiles in GZIP format
		// return nil, err
	}
	if format == GZIP {
		format = PBF // GZIP masks PBF, which is only expected type for tiles in GZIP format
	}
	out.Format = format
	return out, nil
}

// Service creates a new StyleService instance.
//loadService 创建更新瓦片集服务
func (ts *Tileset) Service() error {
	//Saves last modified mbtiles time for setting Last-Modified header
	fStat, err := os.Stat(ts.Path)
	if err != nil {
		// return fmt.Errorf("could not read file stats for mbtiles file: %s", ts.Path)
		return err
	}
	db, err := sql.Open("sqlite3", ts.Path)
	if err != nil {
		return err
	}
	ts.db = db
	ts.timestamp = fStat.ModTime().Round(time.Second)
	ts.Status = true
	return nil
}

//UpInsert 创建更新瓦片集服务
//create or update upload data file info into database
func (ts *Tileset) UpInsert() error {
	tmp := &Tileset{}
	err := db.Where("id = ?", ts.ID).First(tmp).Error
	if err != nil {
		if gorm.IsRecordNotFoundError(err) {
			ts.CreatedAt = time.Time{}
			err = db.Create(ts).Error
			if err != nil {
				return err
			}
		}
		return err
	}
	err = db.Model(&Tileset{}).Update(ts).Error
	if err != nil {
		return err
	}
	return nil
}

//Update 创建更新瓦片集服务
//create or update upload data file info into database
func (ts *Tileset) Update() error {
	err := db.Model(&Tileset{}).Update(ts).Error
	if err != nil {
		return err
	}
	return nil
}

// Tile reads a tile with tile identifiers z, x, y into provided *[]byte.
// data will be nil if the tile does not exist in the database
func (ts *Tileset) Tile(ctx context.Context, z, x, y uint) ([]byte, error) {
	var data []byte
	err := ts.db.QueryRow("select tile_data from tiles where zoom_level = ? and tile_column = ? and tile_row = ?", z, x, y).Scan(&data)
	if err != nil {
		if err == sql.ErrNoRows {
			return data, nil
		}
		return data, err
	}
	return data, nil
}

// GetInfo reads the metadata table into a map, casting their values into
// the appropriate type
func (ts *Tileset) GetInfo() (map[string]interface{}, error) {
	var (
		key   string
		value string
	)
	metadata := make(map[string]interface{})

	rows, err := ts.db.Query("select * from metadata where value is not ''")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		rows.Scan(&key, &value)

		switch key {
		case "maxzoom", "minzoom":
			metadata[key], err = strconv.Atoi(value)
			if err != nil {
				return nil, fmt.Errorf("cannot read metadata item %s: %v", key, err)
			}
		case "bounds", "center":
			metadata[key], err = stringToFloats(value)
			if err != nil {
				return nil, fmt.Errorf("cannot read metadata item %s: %v", key, err)
			}
		case "json":
			err = json.Unmarshal([]byte(value), &metadata)
			if err != nil {
				return nil, fmt.Errorf("unable to parse JSON metadata item: %v", err)
			}
		default:
			metadata[key] = value
		}
	}

	// Supplement missing values by inferring from available data
	_, hasMinZoom := metadata["minzoom"]
	_, hasMaxZoom := metadata["maxzoom"]
	if !(hasMinZoom && hasMaxZoom) {
		var minZoom, maxZoom int
		err := ts.db.QueryRow("select min(zoom_level), max(zoom_level) from tiles").Scan(&minZoom, &maxZoom)
		if err != nil {
			return metadata, nil
		}
		metadata["minzoom"] = minZoom
		metadata["maxzoom"] = maxZoom
	}
	return metadata, nil
}

// GetHash reads the metadata table center value into a string
func (ts *Tileset) GetHash() string {
	var value string
	err := ts.db.QueryRow("select value from metadata where name='center'").Scan(&value)
	if err != nil {
		log.Error(err)
		return ""
	}
	split := strings.Split(value, ",")
	if len(split) != 3 {
		log.Error("metadata center has invalid vaule number(!=3) ^^")
		return ""
	}
	hash := "#" + strings.TrimSpace(split[2]) + "/" + strings.TrimSpace(split[1]) + "/" + strings.TrimSpace(split[0])
	return hash
}

// atlasMark
func (ts *Tileset) atlasMark() error {
	st := `delete from metadata where name = "generator"`
	if ts.db == nil {
		db, err := sql.Open("sqlite3", ts.Path)
		if err != nil {
			return err
		}
		defer db.Close()
		_, err = db.Exec(st)
		if err != nil {
			log.Error(err)
			return err
		}
		return nil
	}
	_, err := ts.db.Exec(st)
	if err != nil {
		log.Error(err)
		return err
	}
	return nil
}

// Close closes the database connection
func (ts *Tileset) Close() error {
	return ts.db.Close()
}

// Clean closes the database and delete db record and delete mbtiles file
func (ts *Tileset) Clean() error {
	ts.db.Close()
	err := db.Where("id = ?", ts.ID).Delete(Tileset{}).Error
	if err != nil {
		if !gorm.IsRecordNotFoundError(err) {
			return err
		}
	}
	err = os.Remove(ts.Path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// detectFileFormat inspects the first few bytes of byte array to determine tile
// format PBF tile format does not have a distinct signature, it will be
// returned as GZIP, and it is up to caller to determine that it is a PBF format
func detectTileFormat(data []byte) (TileFormat, error) {
	patterns := map[TileFormat][]byte{
		GZIP: []byte("\x1f\x8b"), // this masks PBF format too
		ZLIB: []byte("\x78\x9c"),
		PNG:  []byte("\x89\x50\x4E\x47\x0D\x0A\x1A\x0A"),
		JPG:  []byte("\xFF\xD8\xFF"),
		WEBP: []byte("\x52\x49\x46\x46\xc0\x00\x00\x00\x57\x45\x42\x50\x56\x50"),
	}

	for format, pattern := range patterns {
		if bytes.HasPrefix(data, pattern) {
			return format, nil
		}
	}

	return "", errors.New("could not detect tile format")
}

// stringToFloats converts a commma-delimited string of floats to a slice of
// float64 and returns it and the first error that was encountered.
// Example: "1.5,2.1" => [1.5, 2.1]
func stringToFloats(str string) ([]float64, error) {
	split := strings.Split(str, ",")
	var out []float64
	for _, v := range split {
		value, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return out, fmt.Errorf("could not parse %q to floats: %v", str, err)
		}
		out = append(out, value)
	}
	return out, nil
}

//SetupMBTileTables 初始化配置MBTile库
func SetupMBTileTables(path string) (*Tileset, error) {
	os.Remove(path)
	dir := filepath.Dir(path)
	os.MkdirAll(dir, os.ModePerm)
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	_, err = db.Exec("PRAGMA synchronous=0")
	if err != nil {
		return nil, err
	}
	_, err = db.Exec("PRAGMA locking_mode=EXCLUSIVE")
	if err != nil {
		return nil, err
	}
	_, err = db.Exec("PRAGMA journal_mode=DELETE")
	if err != nil {
		return nil, err
	}
	_, err = db.Exec("create table if not exists tiles (zoom_level integer, tile_column integer, tile_row integer, tile_data blob);")
	if err != nil {
		return nil, err
	}
	_, err = db.Exec("create table if not exists metadata (name text, value text);")
	if err != nil {
		return nil, err
	}
	_, err = db.Exec("create unique index name on metadata (name);")
	if err != nil {
		return nil, err
	}
	_, err = db.Exec("create unique index tile_index on tiles(zoom_level, tile_column, tile_row);")
	if err != nil {
		return nil, err
	}
	out := &Tileset{
		Path: path, //should not add / at the end
		db:   db,
	}
	return out, nil
}
