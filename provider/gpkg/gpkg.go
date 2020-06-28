// +build cgo

package gpkg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/atlasdatatech/atlas/provider"
	"github.com/go-spatial/geom"
	"github.com/go-spatial/geom/encoding/wkb"
	"github.com/go-spatial/tegola"
	"github.com/go-spatial/tegola/basic"
	"github.com/go-spatial/tegola/dict"
	log "github.com/sirupsen/logrus"
)

const (
	Name                 = "gpkg"
	DefaultSRID          = tegola.WebMercator
	DefaultIDFieldName   = "fid"
	DefaultGeomFieldName = "geom"
)

// config keys
const (
	ConfigKeyFilePath    = "filepath"
	ConfigKeyLayers      = "layers"
	ConfigKeyLayerName   = "name"
	ConfigKeyTableName   = "tablename"
	ConfigKeySQL         = "sql"
	ConfigKeyGeomIDField = "id_fieldname"
	ConfigKeyFields      = "fields"
)

func decodeGeometry(bytes []byte) (*BinaryHeader, geom.Geometry, error) {
	h, err := NewBinaryHeader(bytes)
	if err != nil {
		log.Error("error decoding geometry header: %v", err)
		return h, nil, err
	}

	geo, err := wkb.DecodeBytes(bytes[h.Size():])
	if err != nil {
		log.Errorf("error decoding geometry: %v", err)
		return h, nil, err
	}

	return h, geo, nil
}

type Provider struct {
	// path to the geopackage file
	Filepath string
	// map of layer name and corresponding sql
	layers map[string]Layer
	// reference to the database connection
	db *sql.DB
}

func (p *Provider) AddLayer(layerConf dict.Dicter) error {

	layerName, err := layerConf.String(ConfigKeyLayerName, nil)
	if err != nil {
		return fmt.Errorf("for layer (%s) we got the following error trying to get the layer's name field: %v", layerName, err)
	}
	if layerName == "" {
		return ErrMissingLayerName
	}

	// ensure only one of sql or tablename exist
	_, errTable := layerConf.String(ConfigKeyTableName, nil)
	if _, ok := errTable.(dict.ErrKeyRequired); errTable != nil && !ok {
		return err
	}
	_, errSQL := layerConf.String(ConfigKeySQL, nil)
	if _, ok := errSQL.(dict.ErrKeyRequired); errSQL != nil && !ok {
		return err
	}
	// err != nil <-> key != exists
	if errTable != nil && errSQL != nil {
		return errors.New("'tablename' or 'sql' is required for a feature's config")
	}
	// err == nil <-> key == exists
	if errTable == nil && errSQL == nil {
		return errors.New("'tablename' or 'sql' is required for a feature's config")
	}

	idFieldname := DefaultIDFieldName
	idFieldname, err = layerConf.String(ConfigKeyGeomIDField, &idFieldname)
	if err != nil {
		return fmt.Errorf("for layer (%v) : %v", layerName, err)
	}

	tagFieldnames, err := layerConf.StringSlice(ConfigKeyFields)
	if err != nil { // empty slices are okay
		return fmt.Errorf("for layer (%v), %q field had the following error: %v", layerName, ConfigKeyFields, err)
	}

	// layer container. will be added to the provider after it's configured
	layer := Layer{
		name: layerName,
	}

	if errTable == nil { // layerConf[ConfigKeyTableName] exists
		tablename, err := layerConf.String(ConfigKeyTableName, &idFieldname)
		if err != nil {
			return fmt.Errorf("for layer (%v) %v : %v", layerName, err)
		}

		layer.tablename = tablename
		layer.tagFieldnames = tagFieldnames

		qtext := fmt.Sprintf(`
		SELECT
			c.table_name, c.min_x, c.min_y, c.max_x, c.max_y, c.srs_id, gc.column_name, gc.geometry_type_name, sm.sql
		FROM
			gpkg_contents c JOIN gpkg_geometry_columns gc ON c.table_name == gc.table_name JOIN sqlite_master sm ON c.table_name = sm.tbl_name
		WHERE
			c.data_type = 'features' AND sm.type = 'table' AND c.table_name = '%s';`, tablename)

		row := p.db.QueryRow(qtext)
		var geomCol, geomType, tableSql sql.NullString
		var minX, minY, maxX, maxY sql.NullFloat64
		var srid sql.NullInt64

		if err = row.Scan(&tablename, &minX, &minY, &maxX, &maxY, &srid, &geomCol, &geomType, &tableSql); err != nil {
			return err
		}
		if !tableSql.Valid {
			return fmt.Errorf("invalid sql for table '%v'", tablename)
		}

		// map the returned geom type to a tegola geom type
		tg, err := geomNameToGeom(geomType.String)
		if err != nil {
			log.Errorf("error mapping geom type (%v): %v", geomType, err)
			return err
		}

		bbox := geom.NewExtent(
			[2]float64{minX.Float64, minY.Float64},
			[2]float64{maxX.Float64, maxY.Float64},
		)

		tags, pkCol := extractColsAndPKFromSQL(tableSql.String)
		layer.geomFieldname = geomCol.String
		layer.geomType = tg
		layer.tagFieldnames = tags
		layer.idFieldname = pkCol
		layer.srid = uint64(srid.Int64)
		layer.bbox = *bbox

	} else { // layerConf[ConfigKeySQL] exists
		var customSQL string
		customSQL, err = layerConf.String(ConfigKeySQL, &customSQL)
		if err != nil {
			return fmt.Errorf("for %v layer(%v) has an error: %v", layerName, ConfigKeySQL, err)
		}
		layer.sql = customSQL

		// if a !ZOOM! token exists, all features could be filtered out so we don't have a geometry to inspect it's type.
		// TODO(arolek): implement an SQL parser or figure out a different approach. this is brittle but I can't figure out a better
		// solution without using an SQL parser on custom SQL statements
		allZoomsSQL := "IN (0,1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21,22,23,24)"
		tokenReplacer := strings.NewReplacer(
			">= "+zoomToken, allZoomsSQL,
			">="+zoomToken, allZoomsSQL,
			"=> "+zoomToken, allZoomsSQL,
			"=>"+zoomToken, allZoomsSQL,
			"=< "+zoomToken, allZoomsSQL,
			"=<"+zoomToken, allZoomsSQL,
			"<= "+zoomToken, allZoomsSQL,
			"<="+zoomToken, allZoomsSQL,
			"!= "+zoomToken, allZoomsSQL,
			"!="+zoomToken, allZoomsSQL,
			"= "+zoomToken, allZoomsSQL,
			"="+zoomToken, allZoomsSQL,
			"> "+zoomToken, allZoomsSQL,
			">"+zoomToken, allZoomsSQL,
			"< "+zoomToken, allZoomsSQL,
			"<"+zoomToken, allZoomsSQL,
		)

		customSQL = tokenReplacer.Replace(customSQL)

		// Set bounds & zoom params to include all layers
		// Bounds checks need params: maxx, minx, maxy, miny
		// TODO(arolek): this assumes WGS84. should be more flexible
		customSQL = replaceTokens(customSQL, 0, tegola.WGS84Bounds)

		// Get geometry type & srid from geometry of first row.
		qtext := fmt.Sprintf("SELECT geom FROM (%v) LIMIT 1;", customSQL)

		log.Debugf("qtext: %v", qtext)

		var geomData []byte
		err = p.db.QueryRow(qtext).Scan(&geomData)
		if err == sql.ErrNoRows {
			return fmt.Errorf("layer '%v' with custom SQL has 0 rows: %v", layerName, customSQL)
		} else if err != nil {
			return fmt.Errorf("layer '%v' problem executing custom SQL: %v", layerName, err)
		}

		h, geo, err := decodeGeometry(geomData)
		if err != nil {
			return err
		}

		layer.geomType = geo
		layer.srid = uint64(h.SRSId())
		layer.geomFieldname = DefaultGeomFieldName
		layer.idFieldname = DefaultIDFieldName
	}

	p.layers[layer.name] = layer

	return nil
}

func (p *Provider) Layers() ([]provider.LayerInfo, error) {
	log.Debug("attempting gpkg.Layers()")

	ls := make([]provider.LayerInfo, len(p.layers))

	var i int
	for _, player := range p.layers {
		ls[i] = player
		i++
	}

	log.Debugf("returning LayerInfo array: %v", ls)

	return ls, nil
}

func (p *Provider) TileFeatures(ctx context.Context, layer string, tile provider.Tile, fn func(f *provider.Feature) error) error {
	// log.Debugf("fetching layer %v", layer)

	pLayer := p.layers[layer]

	// read the tile extent
	tileBBox, tileSRID := tile.BufferedExtent()

	// TODO(arolek): reimplement once the geom package has reprojection
	// check if the SRID of the layer differs from that of the tile. tileSRID is assumed to always be WebMercator
	if pLayer.srid != tileSRID {

		minGeo, err := basic.FromWebMercator(pLayer.srid, basic.Point{tileBBox.MinX(), tileBBox.MinY()})
		if err != nil {
			return fmt.Errorf("error converting point: %v ", err)
		}

		maxGeo, err := basic.FromWebMercator(pLayer.srid, basic.Point{tileBBox.MaxX(), tileBBox.MaxY()})
		if err != nil {
			return fmt.Errorf("error converting point: %v ", err)
		}

		tileBBox = &geom.Extent{
			minGeo.AsPoint().X(), minGeo.AsPoint().Y(),
			maxGeo.AsPoint().X(), maxGeo.AsPoint().Y(),
		}
	}

	var qtext string

	if pLayer.tablename != "" {
		// If layer was specified via "tablename" in config, construct query.
		rtreeTablename := fmt.Sprintf("rtree_%v_%s", pLayer.tablename, pLayer.geomFieldname)

		selectClause := fmt.Sprintf("SELECT l.%v, l.%v", pLayer.idFieldname, pLayer.geomFieldname)

		for _, tf := range pLayer.tagFieldnames {
			selectClause += fmt.Sprintf(", l.%v", tf)
		}

		// l - layer table, si - spatial index
		qtext = fmt.Sprintf("%v FROM `%v` l JOIN `%v` si ON l.%v = si.id WHERE l.%v IS NOT NULL AND !BBOX! ORDER BY l.`%v`", selectClause, pLayer.tablename, rtreeTablename, pLayer.idFieldname, pLayer.geomFieldname, pLayer.idFieldname)

		z, _, _ := tile.ZXY()
		qtext = replaceTokens(qtext, z, tileBBox)
	} else {
		// If layer was specified via "sql" in config, collect it
		z, _, _ := tile.ZXY()
		qtext = replaceTokens(pLayer.sql, z, tileBBox)
	}

	// log.Debugf("qtext: %v", qtext)
	rows, err := p.db.Query(qtext)
	if err != nil {
		log.Errorf("err during query: %v - %v", qtext, err)
		return err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return err
	}

	for rows.Next() {
		// check if the context cancelled or timed out
		if ctx.Err() != nil {
			return ctx.Err()
		}

		vals := make([]interface{}, len(cols))
		valPtrs := make([]interface{}, len(cols))
		for i := 0; i < len(cols); i++ {
			valPtrs[i] = &vals[i]
		}

		if err = rows.Scan(valPtrs...); err != nil {
			log.Errorf("err reading row values: %v", err)
			return err
		}

		feature := provider.Feature{
			Tags: map[string]interface{}{},
		}

		for i := range cols {
			// check if the context cancelled or timed out
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if vals[i] == nil {
				continue
			}

			switch cols[i] {
			case pLayer.idFieldname:
				feature.ID, err = provider.ConvertFeatureID(vals[i])
				if err != nil {
					return err
				}

			case pLayer.geomFieldname:
				// log.Debug("extracting geopackage geometry header.", vals[i])
				geomData, ok := vals[i].([]byte)
				if !ok {
					log.Errorf("unexpected column type for geom field. got %t", vals[i])
					return errors.New("unexpected column type for geom field. expected blob")
				}

				h, geo, err := decodeGeometry(geomData)
				if err != nil {
					return err
				}

				feature.SRID = uint64(h.SRSId())
				feature.Geometry = geo

			case "minx", "miny", "maxx", "maxy", "min_zoom", "max_zoom":
				// Skip these columns used for bounding box and zoom filtering
				continue

			default:
				// Grab any non-nil, non-id, non-bounding box, & non-geometry column as a tag
				switch v := vals[i].(type) {

				case []uint8:
					asBytes := make([]byte, len(v))
					for j := 0; j < len(v); j++ {
						asBytes[j] = v[j]
					}

					feature.Tags[cols[i]] = string(asBytes)
				case int64:
					feature.Tags[cols[i]] = v
				case float64:
					feature.Tags[cols[i]] = v

				default:
					// TODO(arolek): return this error?
					log.Errorf("unexpected type for sqlite column data: %v: %T", cols[i], v)
				}
			}
		}

		// pass the feature to the provided call back
		if err = fn(&feature); err != nil {
			return err
		}
	}

	return nil
}

// Close will close the Provider's database connection
func (p *Provider) Close() error {
	return p.db.Close()
}

type GeomTableDetails struct {
	geomFieldname string
	geomType      geom.Geometry
	srid          uint64
	bbox          geom.Extent
}

type GeomColumn struct {
	name         string
	geometryType string
	geom         geom.Geometry // to populate Layer.geomType
	srsId        int
}

func geomNameToGeom(name string) (geom.Geometry, error) {
	switch name {
	case "POINT":
		return geom.Point{}, nil
	case "LINESTRING":
		return geom.LineString{}, nil
	case "POLYGON":
		return geom.Polygon{}, nil
	case "MULTIPOINT":
		return geom.MultiPoint{}, nil
	case "MULTILINESTRING":
		return geom.MultiLineString{}, nil
	case "MULTIPOLYGON":
		return geom.MultiPolygon{}, nil
	}

	return nil, fmt.Errorf("unsupported geometry type: %v", name)
}
