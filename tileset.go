package main

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// TileFormat is an enum that defines the tile format of a tile
// in the mbtiles file.  Supported image formats:
//   * PNG
//   * JPG
//   * WEBP
//   * PBF  (vector tile protocol buffers)
// Tiles may be compressed, in which case the type is one of:
//   * GZIP
//   * ZLIB
// Compressed tiles may be PBF or UTFGrids
type TileFormat uint8

// Constants representing TileFormat types
const (
	UNKNOWN TileFormat = iota // UNKNOWN TileFormat cannot be determined from first few bytes of tile
	GZIP                      // encoding = gzip
	ZLIB                      // encoding = deflate
	PNG
	JPG
	PBF
	WEBP
)

// String returns a string representing the TileFormat
func (t TileFormat) String() string {
	switch t {
	case PNG:
		return "png"
	case JPG:
		return "jpg"
	case PBF:
		return "pbf"
	case WEBP:
		return "webp"
	default:
		return ""
	}
}

// ContentType returns the MIME content type of the tile
func (t TileFormat) ContentType() string {
	switch t {
	case PNG:
		return "image/png"
	case JPG:
		return "image/jpeg"
	case PBF:
		return "application/x-protobuf" // Content-Encoding header must be gzip
	case WEBP:
		return "image/webp"
	default:
		return ""
	}
}

//Tileset 样式库
type Tileset struct {
	ID        string `json:"id" gorm:"primary_key"`
	Version   string `json:"version"`
	Name      string `json:"name" gorm:"unique;not null;unique_index"`
	Owner     string `json:"owner" gorm:"index"`
	Type      string `json:"type"`
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Layers    []byte `json:"data" ` //gorm:"type:json"
	JSON      []byte `json:"json" ` //gorm:"column:json;type:json"
	CreatedAt time.Time
	UpdatedAt time.Time
}

// TileService represents an mbtiles file connection.
type TileService struct {
	ID                 string
	Name               string
	Path               string
	Owner              string
	Format             TileFormat // tile format: PNG, JPG, PBF, WEBP
	URL                string     // tile format: PNG, JPG, PBF, WEBP
	Hash               string
	State              bool       // true if UTFGrids have corresponding key / value data that need to be joined and returned with the UTFGrid
	Timestamp          time.Time  // timestamp of file, for cache control headers
	HasUTFGrid         bool       // true if mbtiles file contains additional tables with UTFGrid data
	UTFGridCompression TileFormat // compression (GZIP or ZLIB) of UTFGrids
	HasUTFGridData     bool       // true if UTFGrids have corresponding key / value data that need to be joined and returned with the UTFGrid
	db                 *sql.DB    // database connection for mbtiles file
}

//LoadTileset 创建更新瓦片集服务
//create or update upload data file info into database
func LoadTileset(tileset string) (*Tileset, error) {
	fStat, err := os.Stat(tileset)
	if err != nil {
		log.Errorf(`LoadTileset, read style file info error, details: %s`, err)
		return nil, err
	}
	base := filepath.Base(tileset)
	ext := filepath.Ext(tileset)
	name := strings.TrimSuffix(base, ext)
	// id, _ := shortid.Generate()

	out := &Tileset{
		ID:        name,
		Version:   "8",
		Name:      name,
		Owner:     ATLAS,
		Type:      ext,
		Path:      tileset,
		Size:      fStat.Size(),
		UpdatedAt: fStat.ModTime(),
		Layers:    nil,
		JSON:      nil,
	}
	return out, nil
}

// CreateTileService creates a new StyleService instance.
//loadService 创建更新瓦片集服务
func (ts *Tileset) toService() (*TileService, error) {
	out := &TileService{
		ID:   ts.ID,
		Name: ts.Name,
		Path: ts.Path, //should not add / at the end
	}
	//Saves last modified mbtiles time for setting Last-Modified header
	fStat, err := os.Stat(ts.Path)
	if err != nil {
		return nil, fmt.Errorf("could not read file stats for mbtiles file: %s", ts.Path)
	}
	db, err := sql.Open("sqlite3", ts.Path)
	if err != nil {
		return nil, err
	}
	//query a sample tile to determine format
	var data []byte
	err = db.QueryRow("select tile_data from tiles limit 1").Scan(&data)
	if err != nil {
		return nil, err
	}
	format, err := detectTileFormat(data)
	if err != nil {
		return nil, err
	}
	if format == GZIP {
		format = PBF // GZIP masks PBF, which is only expected type for tiles in GZIP format
	}

	out.db = db
	out.Format = format
	out.Timestamp = fStat.ModTime().Round(time.Second)

	// UTFGrids
	// first check to see if requisite tables exist
	var count int
	err = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='view' AND name = 'grids'").Scan(&count)
	if err != nil {
		return nil, err
	}
	if count == 1 {
		// query a sample grid to detect type
		var gridData []byte
		err = db.QueryRow("select grid from grids where grid is not null LIMIT 1").Scan(&gridData)
		if err != nil {
			if err != sql.ErrNoRows {
				return nil, fmt.Errorf("could not read sample grid to determine type: %v", err)
			}
		} else {
			out.HasUTFGrid = true
			out.UTFGridCompression, err = detectTileFormat(gridData)
			if err != nil {
				return nil, fmt.Errorf("could not determine UTF Grid compression type: %v", err)
			}

			// Check to see if grid_data view exists
			count = 0 // prevent use of prior value
			err = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='view' AND name = 'grid_data'").Scan(&count)
			if err != nil {
				return nil, err
			}
			if count == 1 {
				out.HasUTFGridData = true
			}
		}
	}
	out.State = true
	return out, nil
}

//UpInsert 创建更新瓦片集服务
//create or update upload data file info into database
func (ts *Tileset) UpInsert() error {
	if ts == nil {
		return fmt.Errorf("datafile may not be nil")
	}
	tmp := &Tileset{}
	err := db.Where("id = ?", ts.ID).First(tmp).Error
	if err != nil {
		if gorm.IsRecordNotFoundError(err) {
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

// Tile reads a tile with tile identifiers z, x, y into provided *[]byte.
// data will be nil if the tile does not exist in the database
func (tss *TileService) Tile(ctx context.Context, z uint8, x uint, y uint) ([]byte, error) {
	var data []byte
	err := tss.db.QueryRow("select tile_data from tiles where zoom_level = ? and tile_column = ? and tile_row = ?", z, x, y).Scan(&data)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return data, nil
}

// GetGrid reads a UTFGrid with identifiers z, x, y into provided *[]byte. data
// will be nil if the grid does not exist in the database, and an error will be
// raised. This merges in grid key data, if any exist The data is returned in
// the original compression encoding (zlib or gzip)
func (tss *TileService) GetGrid(z uint8, x uint, y uint, data *[]byte) error {
	if !tss.HasUTFGrid {
		return errors.New("Tileset does not contain UTFgrids")
	}

	err := tss.db.QueryRow("select grid from grids where zoom_level = ? and tile_column = ? and tile_row = ?", z, x, y).Scan(data)
	if err != nil {
		if err == sql.ErrNoRows {
			*data = nil // If this tile does not exist in the database, return empty bytes
			return nil
		}
		return err
	}

	if tss.HasUTFGridData {
		keydata := make(map[string]interface{})
		var (
			key   string
			value []byte
		)

		rows, err := tss.db.Query("select key_name, key_json FROM grid_data where zoom_level = ? and tile_column = ? and tile_row = ?", z, x, y)
		if err != nil {
			return fmt.Errorf("cannot fetch grid data: %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			err := rows.Scan(&key, &value)
			if err != nil {
				return fmt.Errorf("could not fetch grid data: %v", err)
			}
			valuejson := make(map[string]interface{})
			json.Unmarshal(value, &valuejson)
			keydata[key] = valuejson
		}

		if len(keydata) == 0 {
			return nil // there is no key data for this tile, return
		}

		var (
			zreader io.ReadCloser  // instance of zlib or gzip reader
			zwriter io.WriteCloser // instance of zlip or gzip writer
			buf     bytes.Buffer
		)
		reader := bytes.NewReader(*data)

		if tss.UTFGridCompression == ZLIB {
			zreader, err = zlib.NewReader(reader)
			if err != nil {
				return err
			}
			zwriter = zlib.NewWriter(&buf)
		} else {
			zreader, err = gzip.NewReader(reader)
			if err != nil {
				return err
			}
			zwriter = gzip.NewWriter(&buf)
		}

		var utfjson map[string]interface{}
		jsonDecoder := json.NewDecoder(zreader)
		jsonDecoder.Decode(&utfjson)
		zreader.Close()

		// splice the key data into the UTF json
		utfjson["data"] = keydata
		if err != nil {
			return err
		}

		// now re-encode to original zip encoding
		jsonEncoder := json.NewEncoder(zwriter)
		err = jsonEncoder.Encode(utfjson)
		if err != nil {
			return err
		}
		zwriter.Close()
		*data = buf.Bytes()
	}
	return nil
}

// GetInfo reads the metadata table into a map, casting their values into
// the appropriate type
func (tss *TileService) GetInfo() (map[string]interface{}, error) {
	var (
		key   string
		value string
	)
	metadata := make(map[string]interface{})

	rows, err := tss.db.Query("select * from metadata where value is not ''")
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
		err := tss.db.QueryRow("select min(zoom_level), max(zoom_level) from tiles").Scan(&minZoom, &maxZoom)
		if err != nil {
			return metadata, nil
		}
		metadata["minzoom"] = minZoom
		metadata["maxzoom"] = maxZoom
	}
	return metadata, nil
}

// GetHash reads the metadata table center value into a string
func (tss *TileService) GetHash() string {
	var value string
	err := tss.db.QueryRow("select value from metadata where name='center'").Scan(&value)
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

// Close closes the database connection
func (tss *TileService) Close() error {
	return tss.db.Close()
}

// Clean closes the database and delete db record and delete mbtiles file
func (tss *TileService) Clean() error {
	tss.db.Close()
	err := db.Where("id = ?", tss.ID).Delete(Tileset{}).Error
	if err != nil {
		if !gorm.IsRecordNotFoundError(err) {
			return err
		}
	}
	err = os.Remove(tss.Path)
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

	return UNKNOWN, errors.New("Could not detect tile format")
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

type tileCoord struct {
	z    uint8
	x, y uint
}

// tileCoordFromString parses and returns tileCoord coordinates and an optional
// extension from the three parameters. The parameter z is interpreted as the
// web mercator zoom level, it is supposed to be an unsigned integer that will
// fit into 8 bit. The parameters x and y are interpreted as longitude and
// latitude tile indices for that zoom level, both are supposed be integers in
// the integer interval [0,2^z). Additionally, y may also have an optional
// filename extension (e.g. "42.png") which is removed before parsing the
// number, and returned, too. In case an error occured during parsing or if the
// values are not in the expected interval, the returned error is non-nil.
func tileCoordFromString(z, x, y string) (tc tileCoord, ext string, err error) {
	var z64 uint64
	if z64, err = strconv.ParseUint(z, 10, 8); err != nil {
		err = fmt.Errorf("cannot parse zoom level: %v", err)
		return
	}
	tc.z = uint8(z64)
	const (
		errMsgParse = "cannot parse %s coordinate axis: %v"
		errMsgOOB   = "%s coordinate (%d) is out of bounds for zoom level %d"
	)
	ux, err := strconv.ParseUint(x, 10, 32)
	if err != nil {
		err = fmt.Errorf(errMsgParse, "first", err)
		return
	}
	if ux >= (1 << z64) {
		err = fmt.Errorf(errMsgOOB, "x", tc.x, tc.z)
		return
	}
	tc.x = uint(ux)
	s := y
	if l := strings.LastIndex(s, "."); l >= 0 {
		s, ext = s[:l], s[l:]
	}
	uy, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		err = fmt.Errorf(errMsgParse, "y", err)
		return
	}

	if uy >= (1 << z64) {
		err = fmt.Errorf(errMsgOOB, "y", tc.y, tc.z)
		return
	}
	tc.y = uint(uy)
	return
}

//SetupMBTileTables 初始化配置MBTile库
func SetupMBTileTables(path string) (*TileService, error) {
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
	out := &TileService{
		Path: path, //should not add / at the end
		db:   db,
	}
	return out, nil
}
