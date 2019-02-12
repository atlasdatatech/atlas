package main

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jinzhu/gorm"
	_ "github.com/mattn/go-sqlite3" // import sqlite3 driver
)

// TilesetI is an interface that represents the shared attributes
// of a tileset.
type TilesetI interface {
	TileType() string
	TileFormat() TileFormat
	Tile(z uint8, x uint, y uint) ([]byte, error)
	TileJSON() TileJSON

	// requiring because sub package type switch over all possible types.
	private()
}

// compile time checks
var (
	_ TilesetI = MBTiles{}
	_ TilesetI = TileMap{}
)

func (tm TileMap) private() {}
func (mt MBTiles) private() {}

// AllTilesets lists all possible types and values that a tileset
// interface can be. It should be used only for testing to verify
// functions that accept a Geometry will work in all cases.
var AllTilesets = []TilesetI{
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
	Name      string `json:"name"`
	Owner     string `json:"owner" gorm:"index"`
	Type      string `json:"type"`
	Size      string `json:"size"`
	Layers    []byte `json:"data" gorm:"type:json"`
	JSON      []byte `json:"json" gorm:"column:json,type:json"`
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
	State   bool     // true if UTFGrids have corresponding key / value data that need to be joined and returned with the UTFGrid
	Tileset TilesetI // database connection for mbtiles file
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

// CreateTileService creates a new StyleService instance.
// Connection is closed by runtime on application termination or by calling
// its Close() method.
func CreateTileService(filePathName string, tileID string) (*TileService, error) {

	if filePathName == "" || tileID == "" {
		return nil, fmt.Errorf("path parameter may not be empty")
	}
	mbtiles, err := LoadMBTiles(filePathName)
	if err != nil {
		return nil, fmt.Errorf("could not open mbtiles file %q: %v", filePathName, err)
	}

	out := &TileService{
		ID:      tileID,
		URL:     filePathName, //should not add / at the end
		Type:    mbtiles.TileFormat().String(),
		Hash:    mbtiles.GetHash(),
		State:   true,
		Tileset: mbtiles,
	}
	return out, nil
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
