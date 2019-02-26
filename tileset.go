package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
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

// Tileseter is an interface that represents the shared attributes
// of a tileset.
type Tileseter interface {
	TileType() string
	TileFormat() TileFormat
	Tile(ctx context.Context, z uint8, x uint, y uint) ([]byte, error)
	TileJSON() TileJSON

	// requiring because sub package type switch over all possible types.
	private()
}

// compile time checks
var (
	_ Tileseter = MBTiles{}
	_ Tileseter = TileMap{}
)

func (tm TileMap) private() {}
func (mt MBTiles) private() {}

// AllTilesets lists all possible types and values that a tileset
// interface can be. It should be used only for testing to verify
// functions that accept a Geometry will work in all cases.
var AllTilesets = []Tileseter{
	nil,
	MBTiles{},
	TileMap{},
}

const (
	// MaxZoom will not render tile beyond this zoom level
	MaxZoom = 22
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

//TileJSON tilejson结构定义
type TileJSON struct {
}

// TileService represents an mbtiles file connection.
type TileService struct {
	ID      string
	Name    string
	URL     string // tile format: PNG, JPG, PBF, WEBP
	Hash    string
	Type    string
	State   bool      // true if UTFGrids have corresponding key / value data that need to be joined and returned with the UTFGrid
	Tileset Tileseter // database connection for mbtiles file
}

//LoadTileset 创建更新瓦片集服务
//create or update upload data file info into database
func LoadTileset(tileset string) (*Tileset, error) {
	fStat, err := os.Stat(tileset)
	if err != nil {
		log.Errorf(`LoadStyle, read style file info error, details: %s`, err)
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
	switch ext {
	case ".mbtiles":
		// mb, err := LoadMBTiles(tileset)
		// out.JSON = mb.TileJSON()
	case ".tilemap":
		// tm, err := LoadTilemap(tileset)
		// out.JSON = tm.TileJSON()
	}

	return out, nil
}

// CreateTileService creates a new StyleService instance.
// Connection is closed by runtime on application termination or by calling
// its Close() method.
func (ts *Tileset) toService() *TileService {
	out := &TileService{
		ID:   ts.ID,
		Name: ts.Name,
		URL:  ts.Path, //should not add / at the end
	}
	its, err := ts.LoadService()
	if err != nil {
		return out
	}

	out.Type = its.TileFormat().String()
	out.Tileset = its
	out.State = true
	return out
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

//LoadService 创建更新瓦片集服务
//create or update upload data file info into database
func (ts *Tileset) LoadService() (Tileseter, error) {
	if ts == nil {
		return nil, fmt.Errorf("datafile may not be nil")
	}
	switch ts.Type {
	case ".mbtiles":
		mb, err := LoadMBTiles(ts.Path)
		if err != nil {
			return nil, err
		}
		return mb, nil
		// out.JSON = mb.TileJSON()
	case ".tilemap":
		tm, err := LoadTileMap(ts.Path)
		if err != nil {
			return nil, err
		}
		return tm, nil
		// out.JSON = tm.TileJSON()
	}
	return nil, fmt.Errorf("未知文件类型")
}

// LoadMBTiles creates a new MBTiles instance.
// Connection is closed by runtime on application termination or by calling
// its Close() method.
func LoadMBTiles(pathfile string) (*MBTiles, error) {
	//Saves last modified mbtiles time for setting Last-Modified header
	fStat, err := os.Stat(pathfile)
	if err != nil {
		return nil, fmt.Errorf("could not read file stats for mbtiles file: %s", pathfile)
	}
	_, id := filepath.Split(pathfile)
	id = strings.Split(id, ".")[0]

	db, err := sql.Open("sqlite3", pathfile)
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
	out := MBTiles{
		db:        db,
		Format:    format,
		timestamp: fStat.ModTime().Round(time.Second), // round to nearest second
	}

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
			out.hasUTFGrid = true
			out.utfgridCompression, err = detectTileFormat(gridData)
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
				out.hasUTFGridData = true
			}
		}
	}

	return &out, nil

}

// LoadTileMap creates a new MBTiles instance.
// Co123456789nnection is closed by runtime on application termination or by calling
// its Close() method.
func LoadTileMap(pathfile string) (*TileMap, error) {
	//Saves last modified mbtiles time for setting Last-Modified header
	// fStat, err := os.Stat(pathfile)
	// if err != nil {
	// 	return nil, fmt.Errorf("could not read file stats for mbtiles file: %s", pathfile)
	// }
	// check the conf file exists
	if _, err := os.Stat(pathfile); os.IsNotExist(err) {
		return nil, fmt.Errorf("config file %v not found", pathfile)
	}

	buf, err := ioutil.ReadFile(pathfile)
	if err != nil {
		return nil, err
	}

	out := &TileMap{}
	json.Unmarshal(buf, &out)

	for i := range out.Layers {
		provd.Layers()
		out.Layers[i].Provider = provd
	}

	return out, nil
}

//SaveTileMap 保存配置文件
func SaveTileMap(pathfile string) error {
	var layers []TileLayer
	layer := TileLayer{
		Name:              "places_a",
		ProviderLayerName: "osm_places_a",
		MinZoom:           5,
		MaxZoom:           20,
	}
	layers = append(layers, layer)
	out := &TileMap{
		Name:   pathfile,
		Layers: layers,
	}
	buf, err := json.Marshal(out)
	err = ioutil.WriteFile(pathfile, buf, os.ModePerm)
	if err != nil {
		return err
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
