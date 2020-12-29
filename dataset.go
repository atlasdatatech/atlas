package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"

	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/go-spatial/geom/encoding/mvt"
	"github.com/go-spatial/geom/slippy"
	aprd "github.com/go-spatial/tegola/provider"
	proto "github.com/golang/protobuf/proto"
	"github.com/jinzhu/gorm"
	_ "github.com/mattn/go-sqlite3" // import sqlite3 driver
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/encoding/wkb"
	"github.com/paulmach/orb/geojson"

	geopkg "github.com/atlasdatatech/go-gpkg/gpkg"
	log "github.com/sirupsen/logrus"
	// "github.com/paulmach/orb/encoding/wkb"
)

// Field 字段
type Field struct {
	Name  string    `json:"name"`
	Alias string    `json:"alias"`
	Type  FieldType `json:"type"`
	Index string    `json:"index"`
}

// Dataset 数据集定义结构
type Dataset struct {
	ID        string          `json:"id" gorm:"primary_key"` //字段列表
	Name      string          `json:"name"`                  //字段列表// 数据集名称,现用于更方便的ID
	Tag       string          `json:"-"`
	Owner     string          `json:"owner"`
	Public    bool            `json:"public"`
	Path      string          `json:"-"`
	Base      string          `json:"-" gorm:"index"`
	Format    string          `json:"format"`
	Size      int64           `json:"size"`
	Total     int             `json:"total"`
	Geotype   GeoType         `json:"geotype"`
	BBox      orb.Bound       `json:"bbox"`
	Fields    json.RawMessage `json:"fields" gorm:"type:json"` //字段列表
	Status    bool            `json:"status" gorm:"-"`
	tlayer    *TileLayer
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

//Service 加载服务
func (dt *Dataset) Service() error {
	dt.Status = true
	return nil
}

//UpInsert 更新/创建数据集概要
func (dt *Dataset) UpInsert() error {
	tmp := &Dataset{}
	err := db.Where("id = ?", dt.ID).First(tmp).Error
	if err != nil {
		if gorm.IsRecordNotFoundError(err) {
			dt.CreatedAt = time.Time{}
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
	switch dbType {
	case Sqlite3:
		ct := &geopkg.Content{
			ContentTableName: tbname,
		}
		err := dataDB.First(ct).Error // (*sql.Rows, error)
		if err != nil {
			return bbox, err
		}
		dt.BBox = orb.Bound{
			Min: orb.Point{ct.MinX, ct.MinY},
			Max: orb.Point{ct.MaxX, ct.MaxY},
		}
	case Postgres:
		var extent []byte
		stbox := fmt.Sprintf(`SELECT st_asgeojson(st_extent(geom)) as extent FROM "%s";`, tbname)
		err := dataDB.Raw(stbox).Row().Scan(&extent) // (*sql.Rows, error)
		if err != nil {
			return bbox, err
		}
		ext, err := geojson.UnmarshalGeometry(extent)
		if err != nil {
			return bbox, err
		}
		bbox = ext.Geometry().Bound()
		dt.BBox = bbox
	case Spatialite:

	}

	return dt.BBox, nil
}

//TotalCount 获取数据集要素总数
func (dt *Dataset) TotalCount() (int, error) {
	tableName := strings.ToLower(dt.ID)
	var total int
	err := dataDB.Table(tableName).Count(&total).Error
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
	rows, err := dataDB.Raw(s).Rows() // (*sql.Rows, error)
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
		var t FieldType
		switch col.DatabaseTypeName() {
		case "INT", "INT4", "INTEGER":
			t = Int
		case "NUMERIC", "REAL": //number
			t = Float
		case "BOOL":
			t = Bool
		case "TIMESTAMPTZ":
			t = Date
		case "_VARCHAR":
			t = StringArray
		case "TEXT", "VARCHAR":
			t = String
		default:
			t = String
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
func (dt *Dataset) GeoType() (GeoType, error) {
	tbname := strings.ToLower(dt.ID)

	switch dbType {
	case Sqlite3:
		var geotype string
		stbox := fmt.Sprintf(`SELECT geometry_type_name AS geotype FROM gpkg_geometry_columns WHERE table_name = "%s";`, tbname)
		err := dataDB.Raw(stbox).Row().Scan(&geotype) // (*sql.Rows, error)
		if err != nil {
			return "", err
		}
		for _, t := range GeoTypes {
			if geotype == strings.ToUpper(string(t)) {
				dt.Geotype = t
				break
			}
		}
	case Postgres:
		var geotype string
		stbox := fmt.Sprintf(`SELECT st_geometrytype(geom) as geotype FROM "%s" limit 1;`, tbname)
		err := dataDB.Raw(stbox).Row().Scan(&geotype) // (*sql.Rows, error)
		if err != nil {
			return "", err
		}
		dt.Geotype = GeoType(strings.TrimPrefix(geotype, "ST_"))
	}

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

	prd, ok := providers[PROVIDERID]
	if !ok {
		return nil, fmt.Errorf("provider not found")
	}

	//组织参数，创建providelayer
	player := &ProviderLayer{
		ProviderID: PROVIDERID,
		ID:         dt.ID,
		Name:       dt.ID,
		TabLeName:  strings.ToLower(dt.ID),
		Type:       "mvt_postgis",
		SRID:       4326,
	}
	//provider内部使用name作为id
	cfg, err := toDicter(player)
	if err != nil {
		return nil, err
	}
	err = prd.AddLayer(cfg)
	if err != nil {
		return nil, err
	}
	tlayer := &TileLayer{
		ID:      dt.ID,
		Name:    dt.ID,
		MinZoom: 0,
		MaxZoom: 19,
		SRID:    3857, //注意tilelayer的目标srid
	}
	tlayer.Provider = prd //layer持有了provider
	tlayer.ProviderLayerID = dt.ID
	dt.tlayer = tlayer
	return tlayer, nil
}

//Encode TODO (arolek): support for max zoom
func (dt *Dataset) Encode(ctx context.Context, tile *slippy.Tile) ([]byte, error) {

	// tile container
	var mvtTile mvt.Tile
	// wait group for concurrent layer fetching
	mvtLayer := mvt.Layer{
		Name: dt.Name,
	}
	prd, ok := providers[PROVIDERID]
	if !ok {
		return nil, fmt.Errorf("provider not found")
	}

	if prd.Std == nil {
		return nil, fmt.Errorf(".Std is null")
	}
	ptile := aprd.NewTile(tile.Z, tile.X, tile.Y,
		uint(TileBuffer), uint(dt.tlayer.SRID))
	// fetch layer from data provider
	err := prd.Std.TileFeatures(ctx, dt.ID, ptile, func(f *aprd.Feature) error {
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

// Dump2GeoJSON 缓存服务层
func (dt *Dataset) Dump2GeoJSON() (*geojson.FeatureCollection, error) {
	tableName := strings.ToLower(dt.ID)
	switch dbType {
	case Sqlite3:
		// qtext := fmt.Sprintf(`SELECT l.* FROM "%v" l JOIN "rtree_%v_geom" si ON l.fid = si.id WHERE l.geom IS NOT NULL AND minx <= %f AND maxx >= %f AND miny <= %f AND maxy >= %f ;`, tableName, tableName, b.Right(), b.Left(), b.Top(), b.Bottom())
		qtext := fmt.Sprintf(`SELECT * FROM "%v";`, tableName)
		rows, err := dataDB.Raw(qtext).Rows()
		if err != nil {
			// log.Errorf("dump geojson from %v error: %v", dt.ID, err)
			return nil, err
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil {
			return nil, err
		}
		fc := geojson.NewFeatureCollection()
		for rows.Next() {
			// check if the context cancelled or timed out
			vals := make([]interface{}, len(cols))
			valPtrs := make([]interface{}, len(cols))
			for i := 0; i < len(cols); i++ {
				valPtrs[i] = &vals[i]
			}

			if err = rows.Scan(valPtrs...); err != nil {
				log.Errorf("err reading row values: %v", err)
				return nil, err
			}

			f := geojson.NewFeature(nil)
			for i := range cols {
				if vals[i] == nil {
					continue
				}
				switch cols[i] {
				case "fid":
					f.ID = vals[i]
				case "geom":
					// log.Debug("extracting geopackage geometry header.", vals[i])
					bytes, ok := vals[i].([]byte)
					if !ok {
						log.Errorf("unexpected column type for geom field. got %t", vals[i])
						return nil, fmt.Errorf("unexpected column type for geom field. expected blob")
					}
					geom, err := wkb.Unmarshal(bytes[40:])
					if err != nil {
						return nil, err
					}
					f.Geometry = geom
				case "minx", "miny", "maxx", "maxy", "min_zoom", "max_zoom":
					// Skip these columns used for bounding box and zoom filtering
					continue
				default:
					// Grab any non-nil, non-id, non-bounding box, & non-geometry column as a tag
					switch v := vals[i].(type) {
					case []byte:
						asBytes := make([]byte, len(v))
						for j := 0; j < len(v); j++ {
							asBytes[j] = v[j]
						}
						f.Properties[cols[i]] = string(asBytes)
					case int64:
						f.Properties[cols[i]] = v
					case float64:
						f.Properties[cols[i]] = v
					default:
						// TODO(arolek): return this error?
						log.Errorf("unexpected type for sqlite column data: %v: %T", cols[i], v)
					}
				}
			}
			fc.Append(f)
		}
		return fc, nil
	case Postgres:
	case Spatialite:
	default:
	}
	return nil, fmt.Errorf("unsupported dirver")
}

// GeoJSON2MBTiles 缓存服务层
func (dt *Dataset) GeoJSON2MBTiles(outPathFile string, layerName string, force bool) error {
	st := time.Now()
	db, err := CreateMBTileTables(outPathFile, force)
	if err != nil {
		return err
	}
	defer db.Close()

	fc, err := dt.Dump2GeoJSON()
	if err != nil {
		return err
	}
	total := len(fc.Features)
	minzoom := 0
	t := time.Now()
	maxzoom, scnt := SplitTile(db, fc, layerName, 0, 0, 0)
	log.Printf("tiler finished,time:%.2f s", time.Since(t).Seconds())

	bound := fc.BBox.Bound()
	// log.Printf("bbox: %v ", bound)
	db.Exec("insert into metadata (name, value) values (?, ?)", "name", layerName)
	db.Exec("insert into metadata (name, value) values (?, ?)", "bounds", fmt.Sprintf("%f,%f,%f,%f", bound.Left(), bound.Bottom(), bound.Right(), bound.Top()))
	// db.Exec("insert into metadata (name, value) values (?, ?)", "bounds", "-180,-85,180,85")
	db.Exec("insert into metadata (name, value) values (?, ?)", "center", fmt.Sprintf("%f,%f,%d", bound.Center().X(), bound.Center().Y(), maxzoom))
	db.Exec("insert into metadata (name, value) values (?, ?)", "maxzoom", maxzoom)
	db.Exec("insert into metadata (name, value) values (?, ?)", "minzoom", minzoom)
	db.Exec("insert into metadata (name, value) values (?, ?)", "format", "pbf")

	type VectorLayer struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		MinZoom     int    `json:"minzoom"`
		MaxZoom     int    `json:"maxzoom"`
	}
	type Layer struct {
		Name     string `json:"layer"`
		Count    int    `json:"count"`
		Geometry string `json:"geometry"`
	}
	type TileStata struct {
		LayerCount int     `json:"layerCount"`
		Layers     []Layer `json:"layers"`
	}
	type TileJSON struct {
		VectorLayers []VectorLayer `json:"vector_layers"`
		Tilestats    TileStata     `json:"tilestats"`
	}

	vl := VectorLayer{
		ID:      layerName,
		Name:    layerName,
		MinZoom: minzoom,
		MaxZoom: maxzoom,
	}
	vectorlayer := []VectorLayer{vl}
	lyr := Layer{
		Name:     layerName,
		Count:    total,
		Geometry: string(dt.Geotype),
	}
	tilestats := TileStata{
		LayerCount: 1,
		Layers:     []Layer{lyr},
	}
	tilejson := TileJSON{
		VectorLayers: vectorlayer,
		Tilestats:    tilestats,
	}
	data, err := json.Marshal(tilejson)
	log.Printf("tilejson:%s", string(data))
	if err == nil {
		db.Exec("insert into metadata (name, value) values (?, ?)", "json", string(data))
	}
	db.Close()
	log.Printf("tiler finished, tiles: %d , time: %.2f s , maxzoom guess: %d ", scnt, time.Since(st).Seconds(), maxzoom)
	return nil
}
