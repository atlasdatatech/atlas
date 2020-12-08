package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/gin-gonic/gin"
	"github.com/go-spatial/geom"
	"github.com/go-spatial/geom/slippy"
	"github.com/go-spatial/tegola/atlas"
	"github.com/go-spatial/tegola/config"
	"github.com/go-spatial/tegola/dict"
	"github.com/go-spatial/tegola/mapbox/tilejson"
	aprd "github.com/go-spatial/tegola/provider"
	"github.com/go-spatial/tegola/server"
	"github.com/jinzhu/gorm"
	log "github.com/sirupsen/logrus"
	"github.com/teris-io/shortid"
)

//Provider 数据库驱动
type Provider struct {
	ID             string `json:"id" toml:"id" binding:"required" gorm:"primaryKey"`
	Name           string `json:"name" toml:"name" binding:"required"`
	Type           string `json:"type" toml:"type"`
	Owner          string `json:"owner" toml:"owner,omitempty" gorm:"index"`
	Host           string `json:"host" toml:"host" binding:"required"`
	Port           int    `json:"port" toml:"port" binding:"required"`
	User           string `json:"user" toml:"user" binding:"required"`
	Password       string `json:"password" toml:"password" binding:"required"`
	Database       string `json:"database" toml:"database" binding:"required"`
	SRID           int    `json:"srid" toml:"srid" binding:"required"  gorm:"column:srid"`
	MaxConnections string `json:"maxConnections" toml:"max_connections,omitempty"`
}

func toDicter(v interface{}) (dict.Dicter, error) {
	//借用config的providers解析
	type envDict struct {
		Providers []interface{} `toml:"providers"`
	}
	prds := envDict{[]interface{}{v}}
	var buf bytes.Buffer
	err := toml.NewEncoder(&buf).Encode(prds)
	if err != nil {
		return nil, fmt.Errorf("参数编码失败")
	}
	cc, err := config.Parse(&buf, "")
	if err != nil {
		return nil, fmt.Errorf("config parse error")
	}
	return cc.Providers[0], nil
}

// RegisterProvider register data provider backends
func RegisterProvider(prd *Provider) (aprd.TilerUnion, error) {
	// holder for registered providers
	// check if a provider with this name is already registered
	p, ok := providers[prd.ID]
	if ok {
		return p, fmt.Errorf("驱动已存在")
	}
	if prd.Type == "" {
		return p, fmt.Errorf("类型必须指定")
	}
	cfg, err := toDicter(prd)
	if err != nil {
		return p, fmt.Errorf("参数有误")
	}
	// register the provider
	prov, err := aprd.For(prd.Type, cfg)
	if err != nil {
		return p, err
	}

	// add the provider to our map of registered providers
	providers[prd.ID] = prov
	log.Infof("registering provider(type): %v (%v)", prd.Name, prd.Type)
	//最后保存
	err = prd.UpInsert()
	return prov, err
}

//UpInsert 创建更新样式存储
//create or update upload data file info into database
func (prd *Provider) UpInsert() error {
	tmp := &Provider{}
	err := db.Where("id = ?", prd.ID).First(tmp).Error
	if err != nil {
		if gorm.IsRecordNotFoundError(err) {
			err = db.Create(prd).Error
			if err != nil {
				return err
			}
		}
		return err
	}
	err = db.Model(&Provider{}).Update(prd).Error
	if err != nil {
		return err
	}
	return nil
}

//ProviderLayer 驱动图层
type ProviderLayer struct {
	ID         string          `json:"id" toml:"id" gorm:"primaryKey"`
	ProviderID string          `json:"providerId" toml:"providerId" binding:"required"`
	Name       string          `json:"name" toml:"name" binding:"required"`
	TabLeName  string          `json:"tablename" toml:"tablename" binding:"required"`
	SQL        string          `json:"sql" toml:"sql,omitempty"`
	SRID       int             `json:"srid" toml:"srid,omitempty" gorm:"column:srid"`
	GeomField  string          `json:"geomField" toml:"geometry_fieldname,omitempty"`
	IDField    string          `json:"idField" toml:"id_fieldname,omitempty"`
	Fields     string          `json:"fields" toml:"fields,omitempty"`
	GeomType   string          `json:"geomType" toml:"geometry_type,omitempty"`
	Bounds     *geom.Extent    `gorm:"-"`
	Provider   aprd.TilerUnion `gorm:"-"`
}

//UpInsert 创建更新vtlayer
func (prdLyr *ProviderLayer) UpInsert() error {
	tmp := &ProviderLayer{}
	err := db.Where("id = ?", prdLyr.ID).First(tmp).Error
	if err != nil {
		if gorm.IsRecordNotFoundError(err) {
			err = db.Create(prdLyr).Error
			if err != nil {
				return err
			}
		}
		return err
	}
	err = db.Model(&ProviderLayer{}).Update(prdLyr).Error
	if err != nil {
		return err
	}
	return nil
}

// RegisterProviderLayer xxx
func RegisterProviderLayer(player *ProviderLayer) error {
	prd, ok := providers[player.ProviderID]
	if !ok {
		//查找库表
		newPrd := &Provider{}
		if err := db.Where("id = ?", player.ProviderID).First(newPrd).Error; err != nil {
			if !gorm.IsRecordNotFoundError(err) {
				return err
			}
		}
		var err error
		prd, err = RegisterProvider(newPrd)
		if err != nil {
			return err
		}
	}

	//provider内部使用name作为id
	name := player.Name
	player.Name = player.ID
	cfg, err := toDicter(player)
	if err != nil {
		return err
	}
	player.Name = name //恢复name
	err = prd.AddLayer(cfg)
	if err != nil {
		return err
	}
	err = player.UpInsert()
	if err != nil {
		return err
	}
	return nil
}

//AutoMap4ProviderLayer 为ProviderLayer自动创建atlasmap
func AutoMap4ProviderLayer(plryid string) error {
	player := &ProviderLayer{ID: plryid}
	dbres := db.Find(player)
	if dbres.Error != nil {
		log.Warnf(`get prdLayer error, prdlayer(%s) not found ^^`, plryid)
		return dbres.Error
	}

	prd, ok := providers[player.ProviderID]
	if !ok {
		newPrd := &Provider{}
		if err := db.Where("id = ?", player.ProviderID).First(newPrd).Error; err != nil {
			if !gorm.IsRecordNotFoundError(err) {
				return err
			}
			//else is not found error, continue
		}
		var err error
		prd, err = RegisterProvider(newPrd)
		if err != nil {
			return err
		}
	}

	//map name 就是mapid
	bbox, _ := prd.Std.LayerExtent(plryid)
	// minzoom := prd.Std.LayerMinZoom(plryid)
	minzoom := 0
	// maxzoom := prd.Std.LayerMaxZoom(plryid)
	maxzoom := 15
	bounds := fmt.Sprintf("[%f,%f,%f,%f]", bbox.MinX(), bbox.MinY(), bbox.MaxX(), bbox.MaxY())
	min := bbox.Min()
	max := bbox.Max()
	cx := (min[0] + max[0]) / 2
	cy := (min[1] + max[1]) / 2
	center := fmt.Sprintf("[%f,%f,%.1f]", cx, cy, float64(maxzoom))
	mapconf := fmt.Sprintf(`
	[[maps]]
	name = "%s"
	bounds = %s
	center = %s
	attribution = "auto generate from provider layer"
		[[maps.layers]]
		provider_layer = "%s.%s"
		min_zoom = %d
		max_zoom = %d`,
		plryid, bounds, center, player.ProviderID, player.ID, minzoom, maxzoom)

	autocfg, err := config.Parse(strings.NewReader(mapconf), "")
	if err != nil {
		return err
	}
	err = registerMaps(nil, autocfg.Maps, providers)
	if err != nil {
		return err
	}
	log.Info(` auto register map(%s) of vtlyr(%s) ^^`, plryid, player.ID)
	return nil
}

func drivers(c *gin.Context) {
	resp := NewRes()
	err := db.DB().Ping()
	if err != nil {
		resp.FailErr(c, err)
		return
	}
	type Driver struct {
		Name string
		Type string
	}
	var drivers = []Driver{
		{
			"postgis",
			"db",
		}}
	resp.DoneData(c, drivers)
}

//listProviders 获取数据库驱动列表
func listProviders(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	var providers []Provider
	err := db.Find(&providers).Error
	if err != nil {
		resp.Fail(c, 5001)
		return
	}

	resp.DoneData(c, providers)
}

//registerProvider 注册数据库驱动,驱动不允许更新的？
func registerProvider(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	provider := &Provider{}
	err := c.Bind(provider)
	if err != nil {
		log.Error(err)
		resp.Fail(c, 4001)
		return
	}

	if provider.ID == "" {
		provider.ID, _ = shortid.Generate()
	}

	if provider.Type == "" {
		provider.Type = "postgis"
	}

	_, err = RegisterProvider(provider)
	if err != nil {
		log.Error(err)
		resp.FailMsg(c, err.Error())
		return
	}
	resp.DoneData(c, gin.H{
		"id": provider.ID,
	})
	return
}

//getProviderInfo 获取数据库驱动列表
func getProviderInfo(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	sid := c.Param("id")
	prov := &Provider{}
	if err := db.Where("id = ?", sid).First(prov).Error; err != nil {
		if !gorm.IsRecordNotFoundError(err) {
			log.Error(err)
			resp.Fail(c, 5001)
		}
		resp.Fail(c, 4049)
		return
	}
	resp.DoneData(c, prov)
}

//listProviderLayers 获取图层列表
func listProviderLayers(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	var players []ProviderLayer
	err := db.Find(&players).Error
	if err != nil {
		resp.Fail(c, 5001)
		return
	}

	resp.DoneData(c, players)
}

//createProvider 注册数据库驱动
func createProviderLayer(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	player := ProviderLayer{}
	err := c.Bind(&player)
	if err != nil {
		log.Error(err)
		resp.Fail(c, 4001)
		return
	}
	player.ID, _ = shortid.Generate()
	err = RegisterProviderLayer(&player)
	if err != nil {
		log.Error(err)
		resp.FailMsg(c, err.Error())
		return
	}
	resp.DoneData(c, gin.H{
		"id": player.ID,
	})
	return
}

//getProviderLayerInfo 获取数据库驱动列表
func getProviderLayerInfo(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	lid := c.Param("id")
	plry := &ProviderLayer{}
	if err := db.Where("id = ?", lid).First(plry).Error; err != nil {
		if !gorm.IsRecordNotFoundError(err) {
			log.Error(err)
			resp.Fail(c, 5001)
		}
		resp.Fail(c, 4049)
		return
	}
	resp.DoneData(c, plry)
}

func getPrdLayerTileJSON(c *gin.Context) {
	resp := NewRes()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	plryid := c.Param("id")
	if plryid == "" {
		resp.Fail(c, 4001)
		return
	}

	amap, err := atlas.GetMap(plryid)
	if err != nil {
		err = AutoMap4ProviderLayer(plryid)
		if err != nil {
			resp.Fail(c, 4049)
			return
		}
		amap, _ = atlas.GetMap(plryid)
	}
	//get atlas map if not exist then create
	//get atlas map tilejson
	tileJSON := tilejson.TileJSON{
		Attribution: &amap.Attribution,
		Bounds:      amap.Bounds.Extent(),
		Center:      amap.Center,
		Format:      "pbf",
		Name:        &amap.Name,
		Scheme:      tilejson.SchemeXYZ,
		TileJSON:    tilejson.Version,
		Version:     "1.0.0",
		Grids:       make([]string, 0),
		Data:        make([]string, 0),
	}

	// parse our query string
	q := ""
	if c.Query("debug") == "true" {
		amap = amap.AddDebugLayers()
		q = "?debug=true"
	}

	tileurl := fmt.Sprintf(`%s/vtlayers/x/%s/{z}/{x}/{y}%s`, rootURL(c.Request), plryid, q)
	//	build our vector layer details
	layer := tilejson.VectorLayer{
		Version: 2,
		Extent:  4096,
		ID:      amap.Layers[0].MVTName(),
		Name:    amap.Layers[0].MVTName(),
		// MinZoom: amap.Layers[0].MinZoom,
		MinZoom: 0,
		MaxZoom: 15,
		Tiles: []string{
			tileurl,
		},
	}

	tileJSON.MinZoom = 0
	tileJSON.MaxZoom = 15

	switch amap.Layers[0].GeomType.(type) {
	case geom.Point, geom.MultiPoint:
		layer.GeometryType = tilejson.GeomTypePoint
	case geom.Line, geom.LineString, geom.MultiLineString:
		layer.GeometryType = tilejson.GeomTypeLine
	case geom.Polygon, geom.MultiPolygon:
		layer.GeometryType = tilejson.GeomTypePolygon
	default:
		layer.GeometryType = tilejson.GeomTypeUnknown
		// TODO: debug log
	}

	// add our layer to our tile layer response
	tileJSON.VectorLayers = append(tileJSON.VectorLayers, layer)

	// build our URL scheme for the tile grid
	tileJSON.Tiles = append(tileJSON.Tiles, tileurl)

	// content type
	c.Header("Content-Type", "application/json")

	// cache control headers (no-cache)
	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")

	if err := json.NewEncoder(c.Writer).Encode(tileJSON); err != nil {
		log.Printf("error encoding tileJSON for layer (%v)", plryid)
	}
}

func getPrdLayerTiles(c *gin.Context) {
	resp := NewRes()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	plryid := c.Param("id")
	if plryid == "" {
		resp.Fail(c, 4001)
		return
	}
	//直接找map，找不到就塞一个进去，不存储tilelayer和tilemap
	amap, err := atlas.GetMap(plryid)
	if err != nil {
		err = AutoMap4ProviderLayer(plryid)
		if err != nil {
			resp.Fail(c, 4049)
			return
		}
		amap, _ = atlas.GetMap(plryid)
	}
	// lookup our Map
	placeholder, _ := strconv.ParseUint(c.Param("z"), 10, 32)
	z := uint(placeholder)
	placeholder, _ = strconv.ParseUint(c.Param("x"), 10, 32)
	x := uint(placeholder)
	yext := c.Param("y")
	ys := strings.Split(yext, ".")
	if len(ys) != 2 {
		// resp.Fail(c, 404)
		// return
	}
	placeholder, _ = strconv.ParseUint(ys[0], 10, 32)
	y := uint(placeholder)
	//拷贝过滤新图层
	amap = amap.FilterLayersByZoom(z)

	tile := slippy.NewTile(z, x, y)
	pbyte, err := amap.Encode(c.Request.Context(), tile)
	if err != nil {
		switch err {
		case context.Canceled:
			// TODO: add debug logs
			return
		default:
			errMsg := fmt.Sprintf("marshalling tile: %v", err)
			log.Error(errMsg)
			http.Error(c.Writer, errMsg, http.StatusInternalServerError)
			return
		}
	}
	// c.Header("Content-Type", mvt.MimeType)
	c.Header("Content-Encoding", "gzip")
	c.Header("Content-Type", "application/x-protobuf")
	c.Header("Content-Length", fmt.Sprintf("%d", len(pbyte)))
	c.Writer.WriteHeader(http.StatusOK)
	c.Writer.Write(pbyte)
	// check for tile size warnings
	if len(pbyte) > server.MaxTileSize {
		log.Infof("tile z:%v, x:%v, y:%v is rather large - %vKb", z, x, y, len(pbyte)/1024)
	}
}

//prdLayerViewer 浏览服务集
func prdLayerViewer(c *gin.Context) {
	resp := NewRes()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	plryid := c.Param("id")

	amap, err := atlas.GetMap(plryid)
	if err != nil {
		err = AutoMap4ProviderLayer(plryid)
		if err != nil {
			resp.Fail(c, 4049)
			return
		}
		amap, _ = atlas.GetMap(plryid)
	}

	prdlayer := &ProviderLayer{ID: plryid}
	dbres := db.Find(prdlayer)
	if dbres.Error != nil {
		log.Warnf(`get prdLayer error, prdlayer(%s) not found ^^`, plryid)
		resp.Fail(c, 4046)
		return
	}

	url := fmt.Sprintf(`%s/vtlayers/x/%s/{z}/{x}/{y}.pbf`, rootURL(c.Request), plryid) //need use user own service set//

	layerType := "circle"
	switch amap.Layers[0].GeomType.(type) {
	case geom.Point, geom.MultiPoint:
		layerType = "circle"
	case geom.Line, geom.LineString, geom.MultiLineString:
		layerType = "line"
	case geom.Polygon, geom.MultiPolygon:
		layerType = "fill"
	default:
		// TODO: debug log
	}

	c.HTML(http.StatusOK, "dataset.html", gin.H{
		"Title":     "服务集预览(T)",
		"Name":      prdlayer.Name + "@" + plryid,
		"LayerName": prdlayer.ID,
		"LayerType": layerType,
		"Format":    PBF,
		"URL":       url,
		"Zoom":      amap.Layers[0].MaxZoom,
		"Center":    amap.Center,
		"Color":     fmt.Sprintf(`{"%s-color":"#00ffff"}`, layerType),
	})
	return
}
