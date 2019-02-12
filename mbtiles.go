package main

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3" // import sqlite3 driver
	log "github.com/sirupsen/logrus"
)

// MBTiles represents an mbtiles file connection.
type MBTiles struct {
	ID                 string
	filename           string     // name of tile mbtiles file
	db                 *sql.DB    // database connection for mbtiles file
	Format             TileFormat // tile format: PNG, JPG, PBF, WEBP
	timestamp          time.Time  // timestamp of file, for cache control headers
	hasUTFGrid         bool       // true if mbtiles file contains additional tables with UTFGrid data
	utfgridCompression TileFormat // compression (GZIP or ZLIB) of UTFGrids
	hasUTFGridData     bool       // true if UTFGrids have corresponding key / value data that need to be joined and returned with the UTFGrid
}

// TileType returns the tileset type.
func (mt MBTiles) TileType() string {
	return "mbtiles"
}

// TileFormat returns the tileset type.
func (mt MBTiles) TileFormat() TileFormat {
	return mt.Format
}

//TileJSON 获取瓦片数据集的tilejson
func (mt MBTiles) TileJSON() TileJSON {
	tilejson := TileJSON{}
	return tilejson
}

// Tile reads a tile with tile identifiers z, x, y into provided *[]byte.
// data will be nil if the tile does not exist in the database
func (mt MBTiles) Tile(z uint8, x uint, y uint) ([]byte, error) {
	var data []byte
	err := mt.db.QueryRow("select tile_data from tiles where zoom_level = ? and tile_column = ? and tile_row = ?", z, x, y).Scan(&data)
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
func (mt MBTiles) GetGrid(z uint8, x uint, y uint, data *[]byte) error {
	if !mt.hasUTFGrid {
		return errors.New("Tileset does not contain UTFgrids")
	}

	err := mt.db.QueryRow("select grid from grids where zoom_level = ? and tile_column = ? and tile_row = ?", z, x, y).Scan(data)
	if err != nil {
		if err == sql.ErrNoRows {
			*data = nil // If this tile does not exist in the database, return empty bytes
			return nil
		}
		return err
	}

	if mt.hasUTFGridData {
		keydata := make(map[string]interface{})
		var (
			key   string
			value []byte
		)

		rows, err := mt.db.Query("select key_name, key_json FROM grid_data where zoom_level = ? and tile_column = ? and tile_row = ?", z, x, y)
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

		if mt.utfgridCompression == ZLIB {
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
func (mt MBTiles) GetInfo() (map[string]interface{}, error) {
	var (
		key   string
		value string
	)
	metadata := make(map[string]interface{})

	rows, err := mt.db.Query("select * from metadata where value is not ''")
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
		err := mt.db.QueryRow("select min(zoom_level), max(zoom_level) from tiles").Scan(&minZoom, &maxZoom)
		if err != nil {
			return metadata, nil
		}
		metadata["minzoom"] = minZoom
		metadata["maxzoom"] = maxZoom
	}
	return metadata, nil
}

// GetHash reads the metadata table center value into a string
func (mt MBTiles) GetHash() string {
	var value string
	err := mt.db.QueryRow("select value from metadata where name='center'").Scan(&value)
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

// HasUTFGrid returns whether the DB has a UTF grid.
func (mt MBTiles) HasUTFGrid() bool {
	return mt.hasUTFGrid
}

// HasUTFGridData returns whether the DB has UTF grid data.
func (mt MBTiles) HasUTFGridData() bool {
	return mt.hasUTFGridData
}

// UTFGridCompression returns the compression type of the UTFGrid in the DB:
// ZLIB or GZIP
func (mt MBTiles) UTFGridCompression() TileFormat {
	return mt.utfgridCompression
}

// TimeStamp returns the time stamp of the DB.
func (mt MBTiles) TimeStamp() time.Time {
	return mt.timestamp
}

// Close closes the database connection
func (mt MBTiles) Close() error {
	return mt.db.Close()
}
