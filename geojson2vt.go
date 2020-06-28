package main

import (
	"database/sql"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/paulmach/orb/clip"
	log "github.com/sirupsen/logrus"

	"github.com/paulmach/orb"

	_ "github.com/mattn/go-sqlite3" // import sqlite3 driver
	"github.com/paulmach/orb/encoding/mvt"
	"github.com/paulmach/orb/geojson"
	"github.com/paulmach/orb/maptile"
	"github.com/paulmach/orb/simplify"
)

//VectorTile 矢量瓦片定义
type VectorTile struct {
	Tile          maptile.Tile
	Layer         *mvt.Layer
	PBF           []byte
	NumPoints     int
	NumSimplified int
	NumFeatures   int
	Valid         bool
	BBox          orb.Bound
}

//CoordOffset 坐标偏移量（使坐标值保持正值）
const CoordOffset int64 = 4 << 32

//ShiftRight 右移
func ShiftRight(a int64) int64 {
	return ((a + CoordOffset) >> 1) - (CoordOffset >> 1)
}

//ShiftLeft 左移
func ShiftLeft(a int64) int64 {
	return ((a + (CoordOffset >> 1)) << 1) - CoordOffset
}

//EncodeQuadkey 四叉树编码
func EncodeQuadkey(wx, wy uint64) uint64 {
	var index uint64
	var i uint
	for i = 0; i < 32; i++ {
		v := ((wx >> (32 - (i + 1))) & 1) << 1
		v |= (wy >> (32 - (i + 1))) & 1
		v = v << (64 - 2*(i+1))
		index |= v
	}
	return index
}

var (
	decodex    [256]uint8
	decodey    [256]uint8
	decodeInit = false
)

//DecodeQuadkey 四叉树解码
func DecodeQuadkey(index uint64) (wx, wy uint64) {
	if !decodeInit {
		for ix := 0; ix < 256; ix++ {
			xx, yy := 0, 0
			var i uint8
			for i = 0; i < 32; i++ {
				xx |= ((ix >> (64 - 2*(i+1) + 1)) & 1) << (32 - (i + 1))
				yy |= ((ix >> (64 - 2*(i+1) + 0)) & 1) << (32 - (i + 1))
			}
			decodex[ix] = uint8(xx)
			decodey[ix] = uint8(yy)
		}
		decodeInit = true
	}

	var i uint8
	for i = 0; i < 8; i++ {
		wx |= uint64(decodex[(index>>(8*i))&0xFF]) << (4 * i)
		wy |= uint64(decodey[(index>>(8*i))&0xFF]) << (4 * i)
	}
	return
}

//Project EPSG4326totile
func Project(lon, lat float64, zoom int) (x, y int64) {
	badLon := false
	if math.IsInf(lon, 0) || math.IsNaN(lon) {
		lon = 720
		badLon = true
	}
	if math.IsInf(lat, 0) || math.IsNaN(lat) {
		lat = 89.9
	}

	if lat < -89.9 {
		lat = -89.9
	}
	if lat > 89.9 {
		lat = 89.9
	}
	if lon < -360 && !badLon {
		lon = -360
	}
	if lon > 360 && !badLon {
		lon = 360
	}

	latRad := lat * math.Pi / 180
	var n int64 = 1 << uint(zoom)

	x = int64(float64(n) * ((lon + 180.0) / 360.0))
	y = int64(float64(n) * (1.0 - (math.Log(math.Tan(latRad)+1.0/math.Cos(latRad)) / math.Pi)) / 2.0)

	return
}

//UnProject tile2EPSG4326
func UnProject(x, y int64, zoom int) (lon, lat float64) {
	var n int64 = 1 << uint(zoom)
	lon = float64(360.0*x)/float64(n) - 180.0
	lat = math.Atan(math.Sinh(math.Pi*(1-2.0*float64(y)/float64(n)))) * 180.0 / math.Pi
	return
}

//PointQuadkey ...
func PointQuadkey(p orb.Point) uint64 {
	x, y := Project(p.X(), p.Y(), 32)
	idx := EncodeQuadkey(uint64(x), uint64(y))
	return idx
}

//GetGeomIDXs 获取geom的四叉树编码
func GetGeomIDXs(geom orb.Geometry) []uint64 {
	idxs := []uint64{}
	if geom == nil {
		return idxs
	}
	switch g := geom.(type) {
	case orb.Point:
		idx := PointQuadkey(g)
		idxs = append(idxs, idx)
	case orb.MultiPoint:
		for _, p := range g {
			idx := PointQuadkey(p)
			idxs = append(idxs, idx)
		}
	case orb.LineString:
		for _, p := range g {
			idx := PointQuadkey(p)
			idxs = append(idxs, idx)
		}
	case orb.MultiLineString:
		for _, l := range g {
			for _, p := range l {
				idx := PointQuadkey(p)
				idxs = append(idxs, idx)
			}
		}
	case orb.Ring:
		for _, p := range g {
			idx := PointQuadkey(p)
			idxs = append(idxs, idx)
		}
	case orb.Polygon:
		for _, r := range g {
			// closed := r.Closed()
			for _, p := range r {
				idx := PointQuadkey(p)
				idxs = append(idxs, idx)
			}
		}
	case orb.MultiPolygon:
		for _, a := range g {
			for _, r := range a {
				for _, p := range r {
					idx := PointQuadkey(p)
					idxs = append(idxs, idx)
				}
			}
		}
	case orb.Bound:
		idxs = append(idxs, PointQuadkey(g.Min))
		idxs = append(idxs, PointQuadkey(g.Max))
		idxs = append(idxs, PointQuadkey(g.LeftTop()))
		idxs = append(idxs, PointQuadkey(g.RightBottom()))
	}
	return idxs
}

//GuessMaxZoom ..
func GuessMaxZoom(fc *geojson.FeatureCollection) (maxzoom int) {
	var sum float64
	var count int64
	fullDetail := 12
	idxs := []uint64{}
	for _, f := range fc.Features {
		gidxs := GetGeomIDXs(f.Geometry)
		idxs = append(idxs, gidxs...)
	}
	sidxs := idxs[:]
	sort.Slice(sidxs, func(i, j int) bool { return sidxs[i] < sidxs[j] })

	for i := 1; i < len(sidxs); i++ {
		if idxs[i] != idxs[i-1] {
			sum += math.Log(float64(idxs[i] - idxs[i-1]))
			count++
		}
	}

	if count > 0 {
		avg := math.Exp(sum / float64(count))
		distFt := math.Sqrt(avg) / 33
		want := distFt / 8
		maxzoom = int(math.Ceil(math.Log(360/(want*0.00000274))/math.Log(2) - float64(fullDetail)))

		if maxzoom < 0 {
			maxzoom = 0
		}
		if maxzoom > 32-fullDetail {
			maxzoom = 32 - fullDetail
		}
		log.Printf("guess a maxzoom of %d for features about %d meters apart\n", maxzoom, int(math.Ceil(distFt/3.28)))
	}
	return
}

//SplitTile ..
func SplitTile(db *sql.DB, fc *geojson.FeatureCollection, layerName string, z, x, y uint32) (int, int32) {
	tile := maptile.New(x, y, maptile.Zoom(z))
	bbox := tile.Bound()
	layer := &mvt.Layer{
		Name:     layerName,
		Version:  1,
		Extent:   4096,
		Features: make([]*geojson.Feature, 0, len(fc.Features)),
	}
	bound := orb.Bound{Min: orb.Point{181, 91}, Max: orb.Point{-181, -91}}
	for _, f := range fc.Features {
		gb := f.Geometry.Bound()
		if !bbox.Intersects(gb) {
			continue
		}
		ff := &geojson.Feature{
			ID:         f.ID,
			Type:       f.Type,
			BBox:       geojson.BBox{gb.Min.X(), gb.Min.Y(), gb.Max.X(), gb.Max.Y()},
			Geometry:   f.Geometry,
			Properties: f.Properties,
		}
		layer.Features = append(layer.Features, ff)
		bound = bound.Union(gb)
	}
	fc.BBox = []float64{bound.Min[0], bound.Min[1], bound.Max[0], bound.Max[1]}
	//指定初始瓦片与要素集无交集
	if !bbox.Intersects(bound) {
		log.Warnf("the root tile doed not intersect the feature collenction.")
		return 0, 0
	}
	// log.Infof("the root tile has %d features", len(layer.Features))
	root := VectorTile{
		Tile:  tile,
		Layer: layer,
	}
	maxzoom := GuessMaxZoom(fc)
	splitFeature := func(p VectorTile, t maptile.Tile, resTiles []VectorTile, i int, waitSpliting *sync.WaitGroup) {
		defer waitSpliting.Done()
		s := 1 << t.Z
		d := 360.0 * 64 / 4096 / float64(s)
		b := t.Bound()
		bpad := b.Pad(d)
		cliped := make([]*geojson.Feature, 0)
		for _, f := range p.Layer.Features {
			if !b.Intersects(f.BBox.Bound()) {
				continue
			}
			fg := f.Geometry
			gtype := f.Geometry.GeoJSONType()
			if gtype == "MultiPolygon" || gtype == "Polygon" {
				fg = orb.Clone(f.Geometry)
			}
			g := clip.Geometry(bpad, fg)
			if g == nil {
				continue
			}
			nf := geojson.NewFeature(g)
			nf.ID = f.ID
			nf.Properties = f.Properties
			gb := g.Bound()
			nf.BBox = geojson.BBox{gb.Min.X(), gb.Min.Y(), gb.Max.X(), gb.Max.Y()}
			cliped = append(cliped, nf)
		}
		if len(cliped) == 0 {
			resTiles[i] = VectorTile{
				Valid: false,
			}
			return
		}
		// log.Printf("the %+v tile has %d features", t, len(cliped))
		nfc := &geojson.FeatureCollection{
			Type:     "FeatureCollection",
			Features: cliped,
		}
		// log.Printf("add %v to sublist ~", t)
		resTiles[i] = VectorTile{
			Tile:  t,
			Layer: mvt.NewLayer(p.Layer.Name, nfc),
			Valid: true,
		}
		// log.Printf("added %v to sublist ~", t)
	}

	splitRoot := func(root VectorTile, tilers chan<- VectorTile) {
		defer close(tilers)
		//模拟队列，四分子瓦片完成才能处理root
		cnt := 0
		tilelist := []VectorTile{root}
		for {
			if len(tilelist) == 0 {
				// log.Debug(`vtlist is empty, process : %d`, cnt)
				// log.Printf(`break*************`)
				break
			}
			vt := tilelist[0]       //pop
			tilelist = tilelist[1:] //after pop

			if vt.Tile.Z < maptile.Zoom(maxzoom) { //继续split
				waitSpliting := &sync.WaitGroup{}
				// log.Printf("%d, sub tiles split starting ~", cnt)
				subTiles := vt.Tile.Children()
				subRts := make([]VectorTile, len(subTiles))
				for i, subvt := range subTiles {
					waitSpliting.Add(1)
					go splitFeature(vt, subvt, subRts, i, waitSpliting) //vtlist producers
				}
				// log.Printf("%d, sub tiles split waiting ~", cnt)
				waitSpliting.Wait() //wait for 4 children finished
				for _, st := range subRts {
					if st.Valid {
						tilelist = append(tilelist, st)
					}
				}
			}
			// log.Printf("%d, sub tiles split finished ~", cnt)
			select {
			case tilers <- vt: //切分完毕，加入编码列表tilers
			}
			// log.Printf("%d, add %v to tilers ~", cnt, vt.Tile)
			cnt++
		}
		// log.Printf("split root finished, total: %d ", cnt)
	}

	//saveTile 保存瓦片管道
	var scnt int32
	saveTile := func(savers <-chan VectorTile, wg *sync.WaitGroup) {
		defer wg.Done()
		for vt := range savers {
			_, err := db.Exec("insert into tiles (zoom_level, tile_column, tile_row, tile_data) values (?, ?, ?, ?);", vt.Tile.Z, vt.Tile.X, (1<<vt.Tile.Z)-1-vt.Tile.Y, vt.PBF)
			if err != nil {
				if strings.HasPrefix(err.Error(), "UNIQUE constraint failed") {
					log.Warnf("save %v tile to mbtiles db error ~ %s", vt.Tile, err)
				} else {
					log.Errorf("save %v tile to mbtiles db error ~ %s", vt.Tile, err)
				}
			}
			atomic.AddInt32(&scnt, 1)
			// log.Printf("%d , saved %v ~", scnt, vt.Tile)
		}
	}

	encodeTile := func(encoders <-chan VectorTile, savers chan<- VectorTile, wg *sync.WaitGroup) {
		defer wg.Done()
		select {
		case vt := <-encoders:
			vt.Layer.ProjectToTile(vt.Tile)
			vt.Layer.Simplify(simplify.DouglasPeucker(1.0))
			vt.Layer.RemoveEmpty(1.0, 1.0)
			lyrs := mvt.Layers{vt.Layer}
			pbf, err := mvt.MarshalGzipped(lyrs)
			if err != nil {
				log.Printf("marshal tile(%+v) error", vt.Tile)
				break
			}
			if len(pbf) == 0 {
				log.Warnf("none content tile(%+v)", vt.Tile)
				break
			}
			vt.PBF = pbf
			savers <- vt
		}
	}
	//when tiler closed wait for finish work
	waitSaver := &sync.WaitGroup{}
	savers := make(chan VectorTile, 8)
	waitSaver.Add(1)
	go saveTile(savers, waitSaver)
	//tilers: buffer chan for tiles for tiling
	tilers := make(chan VectorTile, 8)
	//tilelist producers
	go splitRoot(root, tilers)
	//tilers: buffer chan for encoding
	encoders := make(chan VectorTile, 8)
	waitEncoder := &sync.WaitGroup{}
	// tilelist consumer，tilers producers
	for vt := range tilers {
		select {
		case encoders <- vt:
			waitEncoder.Add(1)
			go encodeTile(encoders, savers, waitEncoder)
		}
	}
	//encoders producer closed
	close(encoders)
	// wait for encoders finish
	waitEncoder.Wait()
	close(savers) //
	//wait for encoders/savers finish
	waitSaver.Wait()
	// log.Print("split tiler finished ")
	return maxzoom, scnt
}
