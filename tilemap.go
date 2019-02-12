package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/atlasdatatech/atlas/convert"
	"github.com/go-spatial/geom"
	"github.com/go-spatial/geom/slippy"
	"github.com/go-spatial/tegola"
	"github.com/go-spatial/tegola/basic"
	"github.com/go-spatial/tegola/dict"

	"github.com/go-spatial/tegola/mvt"
	"github.com/go-spatial/tegola/provider"
	"github.com/go-spatial/tegola/provider/debug"
	proto "github.com/golang/protobuf/proto"
	"github.com/paulmach/orb"

	_ "github.com/mattn/go-sqlite3" // import sqlite3 driver
	log "github.com/sirupsen/logrus"
)

//TileMap 瓦片数据集
type TileMap struct {
	ID   string
	Name string
	// Contains an attribution to be displayed when the map is shown to a user.
	// 	This string is sanatized so it can't be abused as a vector for XSS or beacon tracking.
	Attribution string
	// The maximum extent of available map tiles in WGS:84
	// latitude and longitude values, in the order left, bottom, right, top.
	// Default: [-180, -85, 180, 85]
	Bound *orb.Bound
	// The first value is the longitude, the second is latitude (both in
	// WGS:84 values), the third value is the zoom level.
	Center [3]float64
	Layers []TileLayer

	SRID uint64
	// MVT output values
	TileExtent uint64
	TileBuffer uint64

	Format TileFormat
}

//TileLayer tile layer
type TileLayer struct {
	// optional. if not set, the ProviderLayerName will be used
	Name              string
	ProviderLayerName string
	MinZoom           uint
	MaxZoom           uint
	// instantiated provider
	Provider provider.Tiler
	// default tags to include when encoding the layer. provider tags take precedence
	DefaultTags map[string]interface{}
	GeomType    geom.Geometry
	// DontSimplify indicates wheather feature simplification should be applied.
	// We use a negative in the name so the default is to simplify
	DontSimplify bool
}

// MVTName will return the value that will be encoded in the Name field when the layer is encoded as MVT
func (l *TileLayer) MVTName() string {
	if l.Name != "" {
		return l.Name
	}

	return l.ProviderLayerName
}

// TileType returns the tileset type.
func (tm TileMap) TileType() string {
	return "tilemap"
}

// TileFormat returns the TileFormat of the DB.
func (tm TileMap) TileFormat() TileFormat {
	return tm.Format
}

//TileJSON 获取瓦片数据集的tilejson
func (tm TileMap) TileJSON() TileJSON {
	tilejson := TileJSON{}
	return tilejson
}

//Tile 获取瓦片
func (tm TileMap) Tile(z uint8, x uint, y uint) ([]byte, error) {
	return nil, nil
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
			MaxZoom:           MaxZoom,
		},
		{
			Name:              debug.LayerDebugTileCenter,
			ProviderLayerName: debug.LayerDebugTileCenter,
			Provider:          debugProvider,
			GeomType:          geom.Point{},
			MinZoom:           0,
			MaxZoom:           MaxZoom,
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
				default:
					z, x, y := tile.ZXY()
					// TODO (arolek): should we return an error to the response or just log the error?
					// we can't just write to the response as the waitgroup is going to write to the response as well
					log.Printf("err fetching tile (z: %v, x: %v, y: %v) features: %v", z, x, y, err)
				}
				return
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
