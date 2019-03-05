package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/atlasdatatech/atlas/convert"
	"github.com/go-spatial/geom"
	"github.com/go-spatial/geom/slippy"
	"github.com/go-spatial/tegola"
	"github.com/go-spatial/tegola/basic"
	"github.com/go-spatial/tegola/dict"
	"github.com/go-spatial/tegola/provider"
	"github.com/go-spatial/tegola/provider/debug"
	"github.com/go-spatial/tegola/provider/postgis"

	"github.com/go-spatial/tegola/mvt"
	proto "github.com/golang/protobuf/proto"

	// "github.com/jackc/pgx"
	// "github.com/jackc/pgx/pgtype"

	_ "github.com/mattn/go-sqlite3" // import sqlite3 driver
	log "github.com/sirupsen/logrus"
)

const (
	bboxToken             = "!BBOX!"
	zoomToken             = "!ZOOM!"
	scaleDenominatorToken = "!SCALE_DENOMINATOR!"
	pixelWidthToken       = "!PIXEL_WIDTH!"
	pixelHeightToken      = "!PIXEL_HEIGHT!"
)

//TileBuffer 瓦片缓冲区大小
var TileBuffer = tegola.DefaultTileBuffer
var isSelectQuery = regexp.MustCompile(`(?i)^((\s*)(--.*\n)?)*select`)

//TileMap 瓦片数据集
type TileMap struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Contains an attribution to be displayed when the map is shown to a user.
	// 	This string is sanatized so it can't be abused as a vector for XSS or beacon tracking.
	Attribution string `json:"attribution"`
	// The maximum extent of available map tiles in WGS:84
	// latitude and longitude values, in the order left, bottom, right, top.
	// Default: [-180, -85, 180, 85]
	Bounds *geom.Extent

	// The first value is the longitude, the second is latitude (both in
	// WGS:84 values), the third value is the zoom level.
	Center [3]float64  `json:"center"`
	Layers []TileLayer `json:"layers"`

	SRID uint64
	// MVT output values
	TileExtent uint64
	TileBuffer uint64

	Format TileFormat
}

//TileLayer tile layer
type TileLayer struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	ProviderLayerName string `json:"provider_layer"`
	MinZoom           uint   `json:"min_zoom"`
	MaxZoom           uint   `json:"max_zoom"`
	Provider          provider.Tiler
	DefaultTags       map[string]interface{} `json:"default_tags"`
	GeomType          geom.Geometry
	DontSimplify      bool `json:"dont_simplify"`
	Fields            string
	idFied            string
	geomFied          string
	sql               string
	srid              uint64
}

// NewTileLayer will return the value that will be encoded in the Name field when the layer is encoded as MVT
func NewTileLayer() (*TileLayer, error) {
	pgl := postgis.Layer{}
	log.Info(pgl)
	providers["pg"].Layers()

	return nil, nil
}

// MVTName will return the value that will be encoded in the Name field when the layer is encoded as MVT
func (tl *TileLayer) MVTName() string {
	if tl.Name != "" {
		return tl.Name
	}

	return tl.ProviderLayerName
}

// FilterByZoom 过滤
func (tl *TileLayer) FilterByZoom(zoom uint) bool {
	if (tl.MinZoom <= zoom || tl.MinZoom == 0) && (tl.MaxZoom >= zoom || tl.MaxZoom == 0) {
		return true
	}
	return false
}

//Encode TODO (arolek): support for max zoom
func (tl *TileLayer) Encode(ctx context.Context, tile *slippy.Tile) ([]byte, error) {
	// tile container
	var mvtTile mvt.Tile
	// wait group for concurrent layer fetching
	mvtLayer := mvt.Layer{
		Name:         tl.MVTName(),
		DontSimplify: tl.DontSimplify,
	}

	// fetch layer from data provider
	err := tl.Provider.TileFeatures(ctx, tl.ProviderLayerName, tile, func(f *provider.Feature) error {
		// TODO: remove this geom conversion step once the mvt package has adopted the new geom package
		geo, err := convert.ToTegola(f.Geometry)
		if err != nil {
			return err
		}

		// check if the feature SRID and map SRID are different. If they are then reporject
		if f.SRID != tl.srid {
			// TODO(arolek): support for additional projections
			g, err := basic.ToWebMercator(f.SRID, geo)
			if err != nil {
				return fmt.Errorf("unable to transform geometry to webmercator from SRID (%v) for feature %v due to error: %v", f.SRID, f.ID, err)
			}
			geo = g.Geometry
		}

		// add default tags, but don't overwrite a tag that already exists
		for k, v := range tl.DefaultTags {
			if _, ok := f.Tags[k]; !ok {
				f.Tags[k] = v
			}
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

// TileFormat returns the TileFormat of the DB.
func (tm TileMap) TileFormat() TileFormat {
	return tm.Format
}

//Tile 获取瓦片
func (tm TileMap) Tile(ctx context.Context, z uint8, x uint, y uint) ([]byte, error) {
	tile := slippy.NewTile(uint(z), x, y, TileBuffer, tegola.WebMercator)
	{
		// Check to see that the zxy is within the bounds of the map.
		textent := geom.Extent(tile.Bounds())
		if !tm.Bounds.Contains(&textent) {
			return nil, fmt.Errorf("not contains")
		}
	}

	// check for the debug query string
	if true {
		tm = tm.AddDebugLayers()
	}
	pbyte, err := tm.Encode(ctx, tile)
	if err != nil {
		switch err {
		case context.Canceled:
			// TODO: add debug logs
			return nil, err
		default:
			errMsg := fmt.Sprintf("error marshalling tile: %v", err)
			log.Error(errMsg)
			return nil, err
		}
	}

	return pbyte, nil
}

// AddDebugLayers returns a copy of a Map with the debug layers appended to the layer list
func (tm TileMap) AddDebugLayers() TileMap {
	// make an explicit copy of the layers
	layers := make([]TileLayer, len(tm.Layers))
	copy(layers, tm.Layers)
	tm.Layers = layers

	// setup a debug provider
	debugProvider, _ := debug.NewTileProvider(dict.Dict{})

	tm.Layers = append(layers, []TileLayer{
		{
			Name:              debug.LayerDebugTileOutline,
			ProviderLayerName: debug.LayerDebugTileOutline,
			Provider:          debugProvider,
			GeomType:          geom.LineString{},
			MinZoom:           0,
			MaxZoom:           22,
		},
		{
			Name:              debug.LayerDebugTileCenter,
			ProviderLayerName: debug.LayerDebugTileCenter,
			Provider:          debugProvider,
			GeomType:          geom.Point{},
			MinZoom:           0,
			MaxZoom:           22,
		},
	}...)

	return tm
}

// FilterLayersByZoom returns a copy of a Map with a subset of layers that match the given zoom
func (tm TileMap) FilterLayersByZoom(zoom uint) TileMap {
	var layers []TileLayer

	for i := range tm.Layers {
		if (tm.Layers[i].MinZoom <= zoom || tm.Layers[i].MinZoom == 0) && (tm.Layers[i].MaxZoom >= zoom || tm.Layers[i].MaxZoom == 0) {
			layers = append(layers, tm.Layers[i])
			continue
		}
	}

	// overwrite the Map's layers with our subset
	tm.Layers = layers

	return tm
}

// FilterLayersByName returns a copy of a Map with a subset of layers that match the supplied list of layer names
func (tm TileMap) FilterLayersByName(names ...string) TileMap {
	var layers []TileLayer

	nameStr := strings.Join(names, ",")
	for i := range tm.Layers {
		// if we have a name set, use it for the lookup
		if tm.Layers[i].Name != "" && strings.Contains(nameStr, tm.Layers[i].Name) {
			layers = append(layers, tm.Layers[i])
			continue
		} else if tm.Layers[i].ProviderLayerName != "" && strings.Contains(nameStr, tm.Layers[i].ProviderLayerName) { // default to using the ProviderLayerName for the lookup
			layers = append(layers, tm.Layers[i])
			continue
		}
	}

	// overwrite the Map's layers with our subset
	tm.Layers = layers

	return tm
}

//Encode TODO (arolek): support for max zoom
func (tm TileMap) Encode(ctx context.Context, tile *slippy.Tile) ([]byte, error) {
	// tile container
	var mvtTile mvt.Tile
	// wait group for concurrent layer fetching
	var wg sync.WaitGroup

	// layer stack
	mvtLayers := make([]*mvt.Layer, len(tm.Layers))

	// set our waitgroup count
	wg.Add(len(tm.Layers))

	// iterate our layers
	for i, layer := range tm.Layers {

		// go routine for fetching the layer concurrently
		go func(i int, l TileLayer) {
			mvtLayer := mvt.Layer{
				Name:         l.MVTName(),
				DontSimplify: l.DontSimplify,
			}

			// on completion let the wait group know
			defer wg.Done()

			// fetch layer from data provider
			err := l.Provider.TileFeatures(ctx, l.ProviderLayerName, tile, func(f *provider.Feature) error {
				// TODO: remove this geom conversion step once the mvt package has adopted the new geom package
				geo, err := convert.ToTegola(f.Geometry)
				if err != nil {
					return err
				}

				// check if the feature SRID and map SRID are different. If they are then reporject
				if f.SRID != tm.SRID {
					// TODO(arolek): support for additional projections
					g, err := basic.ToWebMercator(f.SRID, geo)
					if err != nil {
						return fmt.Errorf("unable to transform geometry to webmercator from SRID (%v) for feature %v due to error: %v", f.SRID, f.ID, err)
					}
					geo = g.Geometry
				}

				// add default tags, but don't overwrite a tag that already exists
				for k, v := range l.DefaultTags {
					if _, ok := f.Tags[k]; !ok {
						f.Tags[k] = v
					}
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
					// TODO (arolek): add debug logs
					return
				default:
					z, x, y := tile.ZXY()
					// TODO (arolek): should we return an error to the response or just log the error?
					// we can't just write to the response as the waitgroup is going to write to the response as well
					log.Printf("err fetching tile (z: %v, x: %v, y: %v) features: %v", z, x, y, err)
					if err.Error() != "too much features" {
						return
					}
				}
			}

			// add the layer to the slice position
			mvtLayers[i] = &mvtLayer
		}(i, layer)
	}

	// wait for the waitgroup to finish
	wg.Wait()

	// stop processing if the context has an error. this check is necessary
	// otherwise the server continues processing even if the request was canceled
	// as the waitgroup was not notified of the cancel
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	// add layers to our tile
	mvtTile.AddLayers(mvtLayers...)

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
		out.Layers[i].Provider = providers["pg"]
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
