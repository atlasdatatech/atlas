package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io/ioutil"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/go-spatial/geom"
	"github.com/go-spatial/geom/slippy"
	"github.com/go-spatial/tegola"
	"github.com/go-spatial/tegola/atlas"
	"github.com/go-spatial/tegola/basic"
	"github.com/go-spatial/tegola/config"
	aprd "github.com/go-spatial/tegola/provider"
	"github.com/go-spatial/tegola/provider/debug"

	"github.com/jinzhu/gorm"

	"github.com/go-spatial/geom/encoding/mvt"
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
	ID              string                 `json:"id" toml:"name"`
	Name            string                 `json:"name" toml:"name" binding:"required"`
	MinZoom         uint                   `json:"min_zoom"`
	MaxZoom         uint                   `json:"max_zoom"`
	Bounds          *geom.Extent           `gorm:"-"`
	Provider        aprd.TilerUnion        `gorm:"-"`
	ProviderLayerID string                 `json:"provider_layer" toml:"provider_layer" binding:"required"`
	GeomType        geom.Geometry          `gorm:"-"`
	DefaultTags     map[string]interface{} `json:"default_tags" gorm:"-"`
	DontSimplify    bool                   `json:"dont_simplify" toml:"dont_simplify" `
	DontClip        bool                   `json:"dont_clip" toml:"dont_clip" `
	SRID            uint64
}

//UpInsert 创建更新瓦片集服务
//create or update upload data file info into database
func (tl *TileLayer) UpInsert() error {
	if tl == nil {
		return fmt.Errorf("datafile may not be nil")
	}
	tmp := &TileLayer{}
	err := db.Where("id = ?", tl.ID).First(tmp).Error
	if err != nil {
		if gorm.IsRecordNotFoundError(err) {
			err = db.Create(tl).Error
			if err != nil {
				return err
			}
		}
		return err
	}
	err = db.Model(&TileLayer{}).Update(tl).Error
	if err != nil {
		return err
	}
	return nil
}

// MVTName will return the value that will be encoded in the Name field when the layer is encoded as MVT
func (tl *TileLayer) MVTName() string {
	if tl.Name != "" {
		return tl.Name
	}

	return tl.ProviderLayerID
}

// FilterByZoom 过滤
func (tl *TileLayer) FilterByZoom(zoom uint) bool {
	if (tl.MinZoom <= zoom || tl.MinZoom == 0) && (tl.MaxZoom >= zoom || tl.MaxZoom == 0) {
		return false
	}
	return true
}

//MVTEncode encode for mvt_postgis
func (tl *TileLayer) MVTEncode(ctx context.Context, tile *slippy.Tile) ([]byte, error) {
	if tl.Provider.Mvt == nil {
		return nil, fmt.Errorf(".Mvt is null")
	}

	ptile := aprd.NewTile(tile.Z, tile.X, tile.Y,
		uint(TileBuffer), uint(tl.SRID))

	lry := aprd.Layer{
		ID:      tl.ID,
		MVTName: tl.MVTName(),
	}
	layers := []aprd.Layer{lry}
	data, err := tl.Provider.Mvt.MVTForLayers(ctx, ptile, layers)
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

	// buffer to store our compressed bytes
	var gzipBuf bytes.Buffer

	// compress the encoded bytes
	w := gzip.NewWriter(&gzipBuf)
	_, err = w.Write(data)
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

//Encode TODO (arolek): support for max zoom
func (tl *TileLayer) Encode(ctx context.Context, tile *slippy.Tile) ([]byte, error) {
	if tl.Provider.Std == nil {
		return nil, fmt.Errorf(".Std is null")
	}
	// tile container
	var mvtTile mvt.Tile
	// wait group for concurrent layer fetching
	mvtLayer := mvt.Layer{
		Name: tl.MVTName(),
	}

	ptile := aprd.NewTile(tile.Z, tile.X, tile.Y,
		uint(TileBuffer), uint(tl.SRID))
	// fetch layer from data provider
	err := tl.Provider.Std.TileFeatures(ctx, tl.ProviderLayerID, ptile, func(f *aprd.Feature) error {
		// skip row if geometry collection empty.
		g, ok := f.Geometry.(geom.Collection)
		if ok && len(g.Geometries()) == 0 {
			return nil
		}

		geo := f.Geometry
		if f.SRID != tl.SRID {
			g, err := basic.ToWebMercator(f.SRID, geo)
			if err != nil {
				return fmt.Errorf("unable to transform geometry to webmercator from SRID (%v) for feature %v due to error: %w", f.SRID, f.ID, err)
			}
			geo = g
		}
		geo = mvt.PrepareGeo(geo, tile.Extent3857(), float64(mvt.DefaultExtent))

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

	// generate our tile
	vtile, err := mvtTile.VTile(ctx)
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
	tile := slippy.NewTile(uint(z), x, y)

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
	// debugProvider, _ := debug.NewTileProvider(dict.Dict{})

	tm.Layers = append(layers, []TileLayer{
		{
			Name:            debug.LayerDebugTileOutline,
			ProviderLayerID: debug.LayerDebugTileOutline,
			// Provider:          debugProvider,
			GeomType: geom.LineString{},
			MinZoom:  0,
			MaxZoom:  22,
		},
		{
			Name:            debug.LayerDebugTileCenter,
			ProviderLayerID: debug.LayerDebugTileCenter,
			// Provider:          debugProvider,
			GeomType: geom.Point{},
			MinZoom:  0,
			MaxZoom:  22,
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
		} else if tm.Layers[i].ProviderLayerID != "" && strings.Contains(nameStr, tm.Layers[i].ProviderLayerID) { // default to using the ProviderLayerName for the lookup
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
				Name: l.MVTName(),
			}
			// on completion let the wait group know
			defer wg.Done()

			if l.Provider.Std == nil {
				return
			}
			ptile := aprd.NewTile(tile.Z, tile.X, tile.Y,
				uint(TileBuffer), uint(tm.SRID))
			// fetch layer from data provider
			err := l.Provider.Std.TileFeatures(ctx, l.ProviderLayerID, ptile, func(f *aprd.Feature) error {
				// TODO: remove this geom conversion step once the mvt package has adopted the new geom package
				geo, err := ToTegola(f.Geometry)
				if err != nil {
					return err
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

	// generate our tile
	vtile, err := mvtTile.VTile(ctx)

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
		Name:            "places_a",
		ProviderLayerID: "osm_places_a",
		MinZoom:         5,
		MaxZoom:         20,
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

// TileFormat returns the TileFormat of the DB.
func (tm TileMap) toAtlasMap() atlas.Map {
	newMap := atlas.NewWebMercatorMap(string(tm.Name))
	newMap.Attribution = html.EscapeString(string(tm.Attribution))

	// convert from env package
	for i, v := range tm.Center {
		newMap.Center[i] = float64(v)
	}

	if len(tm.Bounds) == 4 {
		newMap.Bounds = geom.NewExtent(
			[2]float64{float64(tm.Bounds[0]), float64(tm.Bounds[1])},
			[2]float64{float64(tm.Bounds[2]), float64(tm.Bounds[3])},
		)
	}

	if tm.TileBuffer != 0 {
		newMap.TileBuffer = uint64(tm.TileBuffer)
	}
	return newMap
}

//*******************************************************************
// registerMaps registers maps with with atlas
func registerMaps(a *atlas.Atlas, maps []config.Map, providers map[string]aprd.TilerUnion) error {
	var (
		layerer aprd.Layerer
	)

	// iterate our maps
	for _, m := range maps {
		newMap := webMercatorMapFromConfigMap(m)

		// iterate our layers
		for _, l := range m.Layers {
			prdID, plyrID, err := l.ProviderLayerID()
			if err != nil {
				return fmt.Errorf("ErrProviderLayerInvalid,ProviderLayer:%s,Map:%s", string(l.ProviderLayer), string(m.Name))
			}

			// find our layer provider
			layerer, err = selectProvider(prdID, string(m.Name), &newMap, providers)
			if err != nil {
				return err
			}

			layer, err := atlasLayerFromConfigLayer(&l, plyrID, layerer)
			if err != nil {
				return err
			}
			newMap.Layers = append(newMap.Layers, layer)
		}
		a.AddMap(newMap)
	}
	return nil
}

func webMercatorMapFromConfigMap(cfg config.Map) (newMap atlas.Map) {
	newMap = atlas.NewWebMercatorMap(string(cfg.Name))
	newMap.Attribution = html.EscapeString(string(cfg.Attribution))

	// convert from env package
	for i, v := range cfg.Center {
		newMap.Center[i] = float64(v)
	}

	if len(cfg.Bounds) == 4 {
		newMap.Bounds = geom.NewExtent(
			[2]float64{float64(cfg.Bounds[0]), float64(cfg.Bounds[1])},
			[2]float64{float64(cfg.Bounds[2]), float64(cfg.Bounds[3])},
		)
	}

	if cfg.TileBuffer != nil {
		newMap.TileBuffer = uint64(*cfg.TileBuffer)
	}
	return newMap
}

func layerInfosFindByID(infos []aprd.LayerInfo, lyrID string) aprd.LayerInfo {
	if len(infos) == 0 {
		return nil
	}
	for i := range infos {
		if infos[i].ID() == lyrID {
			return infos[i]
		}
	}
	return nil
}

func atlasLayerFromConfigLayer(cfg *config.MapLayer, mapName string, layerProvider aprd.Layerer) (layer atlas.Layer, err error) {
	var (
		// providerLayer is primary used for error reporting.
		providerLayer = string(cfg.ProviderLayer)
		ok            bool
	)
	// read the provider's layer names
	// don't care about the error.
	prdID, plyrID, _ := cfg.ProviderLayerID()
	layerInfos, err := layerProvider.Layers()
	if err != nil {
		return layer, fmt.Errorf("ErrFetchingLayerInfo,Provider:%s,error:%s",
			string(prdID), err.Error())
	}
	layerInfo := layerInfosFindByID(layerInfos, plyrID)
	if layerInfo == nil {
		return layer, fmt.Errorf("ErrProviderLayerNotRegistered,Map:%s,Provider:%s,ProviderLayer:%s",
			mapName, prdID, providerLayer)
	}
	layer.GeomType = layerInfo.GeomType()

	if cfg.DefaultTags != nil {
		if layer.DefaultTags, ok = cfg.DefaultTags.(map[string]interface{}); !ok {
			return layer, fmt.Errorf("ErrDefaultTagsInvalid,ProviderLayer:%s",
				providerLayer)
		}
	}

	// if layerProvider is not a provider.Tiler this will return nil, so
	// no need to check ok, as nil is what we want here.
	layer.Provider, _ = layerProvider.(aprd.Tiler)

	layer.ID = string(cfg.ID)
	layer.Name = string(cfg.Name)
	layer.ProviderLayerID = plyrID
	layer.DontSimplify = bool(cfg.DontSimplify)
	layer.DontClip = bool(cfg.DontClip)

	if cfg.MinZoom != nil {
		layer.MinZoom = uint(*cfg.MinZoom)
	}
	if cfg.MaxZoom != nil {
		layer.MaxZoom = uint(*cfg.MaxZoom)
	}
	return layer, nil
}

func selectProvider(prdID string, mapName string, newMap *atlas.Map, providers map[string]aprd.TilerUnion) (aprd.Layerer, error) {
	if newMap.HasMVTProvider() {
		if newMap.MVTProviderID() != prdID {
			return nil, fmt.Errorf("ErrMVTDifferentProviders,Original:%s,Current:%s",
				newMap.MVTProviderID(), prdID)
		}
		return newMap.MVTProvider(), nil
	}
	if prvd, ok := providers[prdID]; ok {
		// Need to see what type of provider we got.
		if prvd.Std != nil {
			return prvd.Std, nil
		}
		if prvd.Mvt == nil {
			return nil, fmt.Errorf("ErrProviderNotFound, %s", prdID)
		}
		if len(newMap.Layers) != 0 {
			return nil, fmt.Errorf("ErrMixedProviders, Map:%s", string(mapName))
		}
		return newMap.SetMVTProvider(prdID, prvd.Mvt), nil
	}
	return nil, fmt.Errorf("ErrProviderNotFound, %s", prdID)
}
