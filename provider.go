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
)

//Provider 数据库驱动
type Provider struct {
	ID             string `json:"id" toml:"id" gorm:"primaryKey"`
	Name           string `json:"name" toml:"name" binding:"required"`
	Type           string `json:"type" toml:"type"`
	Owner          string `json:"owner" toml:"owner,omitempty" gorm:"index"`
	Host           string `json:"host" toml:"host" binding:"required"`
	Port           int    `json:"port" toml:"port" binding:"required"`
	User           string `json:"user" toml:"user" binding:"required"`
	Password       string `json:"password" toml:"password" binding:"required"`
	Database       string `json:"database" toml:"database" binding:"required"`
	SRID           int    `json:"srid" toml:"srid" binding:"required"  gorm:"column:srid"`
	MaxConnections int    `json:"maxConnections" toml:"max_connections,omitempty"`
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
	TabLeName  string          `json:"tablename" toml:"tablename"`
	SQL        string          `json:"sql" toml:"sql,omitempty"`
	SRID       int             `json:"srid" toml:"srid,omitempty" gorm:"column:srid"`
	GeomField  string          `json:"geomField" toml:"geometry_fieldname,omitempty"`
	IDField    string          `json:"idField" toml:"id_fieldname,omitempty"`
	Fields     string          `json:"fields" toml:"fields,omitempty"`
	GeomType   string          `json:"geomType" toml:"geometry_type,omitempty"`
	Type       string          `json:"-" toml:"type,omitempty" gorm:"-"`
	Provider   aprd.TilerUnion `json:"-" toml:"-" gorm:"-"`
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
			return fmt.Errorf("%s", MsgList[40410])
		}
		var err error
		prd, err = RegisterProvider(newPrd)
		if err != nil {
			return err
		}
	}
	if prd.Std != nil {
		player.Type = "postgis"
	} else if prd.Mvt != nil {
		player.Type = "mvt_postgis"
	}
	//provider内部使用name作为id
	cfg, err := toDicter(player)
	if err != nil {
		return err
	}
	err = prd.AddLayer(cfg)
	if err != nil {
		return err
	}
	return nil
}

//AutoMap4ProviderLayer 为ProviderLayer自动创建atlasmap
func AutoMap4ProviderLayer(plyrID string) error {
	player := &ProviderLayer{ID: plyrID}
	dbres := db.Find(player)
	if dbres.Error != nil {
		log.Warnf(`get prdLayer error, prdlayer(%s) not found ^^`, plyrID)
		return dbres.Error
	}

	prd, ok := providers[player.ProviderID]
	if !ok {
		newPrd := &Provider{}
		if err := db.Where("id = ?", player.ProviderID).First(newPrd).Error; err != nil {
			if !gorm.IsRecordNotFoundError(err) {
				return err
			}
			return fmt.Errorf("%s", MsgList[40410])
		}
		var err error
		prd, err = RegisterProvider(newPrd)
		if err != nil {
			return err
		}
	}
	//若内部图层不存在，则需注册图层
	plyr, ok := prd.Layer(plyrID)
	if !ok {
		err := RegisterProviderLayer(player)
		if err != nil {
			return err
		}
		log.Infof("重新注册内部驱动图层%s", plyr.ID())
	}

	//map name 就是mapid
	bbox, _ := prd.LayerExtent(plyrID)
	minzoom := 0
	maxzoom := 14
	// minzoom := prd.LayerMinZoom(plryid)
	// maxzoom := prd.LayerMaxZoom(plryid)
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
		id = "%s"
		name = "%s"
		provider_layer = "%s.%s"
		min_zoom = %d
		max_zoom = %d`,
		plyrID, bounds, center, player.ID, player.Name, player.ProviderID, player.ID, minzoom, maxzoom)

	autocfg, err := config.Parse(strings.NewReader(mapconf), "")
	if err != nil {
		return err
	}
	err = registerMaps(nil, autocfg.Maps, providers)
	if err != nil {
		return err
	}
	log.Info(` auto register map(%s) of vtlyr(%s) ^^`, plyrID, player.ID)
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
			"mvt_postgis",
			"v2.4+",
		},
		{
			"postgis",
			"< v2.4",
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
		provider.ID = ShortID()
	}

	if provider.Type == "" {
		provider.Type = "mvt_postgis"
	}

	if provider.MaxConnections == 0 {
		provider.MaxConnections = 100
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
		resp.Fail(c, 40410)
		return
	}
	resp.DoneData(c, prov)
}

//updateProviderInfo 更新指定数据库驱动
func updateProviderInfo(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	id := c.Param("id")
	type prdbind struct {
		Name           string `json:"name"`
		User           string `json:"user"`
		Password       string `json:"password"`
		MaxConnections int    `json:"maxConnections"`
	}
	prd := &prdbind{}
	err := c.Bind(prd)
	if err != nil {
		log.Error(err)
		resp.Fail(c, 4001)
		return
	}
	// 更新insertUser
	dbres := db.Model(Provider{}).Where("id = ?", id).Update(prd)

	if dbres.Error != nil {
		log.Error(err)
		resp.Fail(c, 5001)
		return
	}

	resp.DoneData(c, gin.H{
		"affected": dbres.RowsAffected,
	})
	return
}

//deleteProvider 删除指定数据库
func deleteProvider(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}
	ids := c.Param("ids")
	sids := strings.Split(ids, ",")
	dbres := db.Where("id in (?)", sids).Delete(Provider{})
	if dbres.Error != nil {
		log.Error(dbres.Error)
		resp.Fail(c, 5001)
		return
	}
	resp.DoneData(c, gin.H{
		"affected": dbres.RowsAffected,
	})
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
	player.ID = ShortID()
	err = RegisterProviderLayer(&player)
	if err != nil {
		log.Error(err)
		resp.FailMsg(c, err.Error())
		return
	}
	err = player.UpInsert()
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
		resp.Fail(c, 40411)
		return
	}
	resp.DoneData(c, plry)
}

//updateProviderLayerInfo 更新指定数据库驱动层
func updateProviderLayerInfo(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	id := c.Param("id")
	plyr := &struct {
		Name   string `json:"name" `
		SQL    string `json:"sql"`
		Fields string `json:"fields"`
	}{}
	err := c.Bind(plyr)
	if err != nil {
		log.Error(err)
		resp.Fail(c, 4001)
		return
	}
	// 更新insertUser
	dbres := db.Model(ProviderLayer{}).Where("id = ?", id).Update(plyr)

	if dbres.Error != nil {
		log.Error(err)
		resp.Fail(c, 5001)
		return
	}

	resp.DoneData(c, gin.H{
		"affected": dbres.RowsAffected,
	})
	return
}

//deleteProvider 删除指定数据库驱动层
func deleteProviderLayer(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}
	ids := c.Param("ids")
	sids := strings.Split(ids, ",")
	dbres := db.Where("id in (?)", sids).Delete(ProviderLayer{})
	if dbres.Error != nil {
		log.Error(dbres.Error)
		resp.Fail(c, 5001)
		return
	}
	resp.DoneData(c, gin.H{
		"affected": dbres.RowsAffected,
	})
}

func getPrdLayerTileJSON(c *gin.Context) {
	resp := NewRes()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	plyrid := c.Param("id")
	if plyrid == "" {
		resp.Fail(c, 4001)
		return
	}

	amap, err := atlas.GetMap(plyrid)
	if err != nil {
		err = AutoMap4ProviderLayer(plyrid)
		if err != nil {
			resp.Fail(c, 4049)
			return
		}
		amap, _ = atlas.GetMap(plyrid)
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

	tileurl := fmt.Sprintf(`%s/vtlayers/x/%s/{z}/{x}/{y}%s`, rootURL(c.Request), plyrid, q)
	//	build our vector layer details
	layer := tilejson.VectorLayer{
		Version: 2,
		Extent:  4096,
		ID:      amap.Layers[0].ID,
		Name:    amap.Layers[0].MVTName(),
		MinZoom: amap.Layers[0].MinZoom,
		MaxZoom: amap.Layers[0].MaxZoom,
		Tiles: []string{
			tileurl,
		},
	}

	tileJSON.MinZoom = amap.Layers[0].MinZoom
	tileJSON.MaxZoom = amap.Layers[0].MaxZoom

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
		log.Printf("error encoding tileJSON for layer (%v)", plyrid)
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
	// amap = amap.FilterLayersByZoom(z)

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
	plyrID := c.Param("id")

	amap, err := atlas.GetMap(plyrID)
	if err != nil {
		err = AutoMap4ProviderLayer(plyrID)
		if err != nil {
			resp.Fail(c, 4049)
			return
		}
		amap, _ = atlas.GetMap(plyrID)
	}

	prdlayer := &ProviderLayer{ID: plyrID}
	dbres := db.Find(prdlayer)
	if dbres.Error != nil {
		log.Warnf(`get prdLayer error, prdlayer(%s) not found ^^`, plyrID)
		resp.Fail(c, 4046)
		return
	}

	url := fmt.Sprintf(`%s/vtlayers/x/%s/{z}/{x}/{y}.pbf`, rootURL(c.Request), plyrID) //need use user own service set//

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
		"Name":      prdlayer.Name + "@" + plyrID,
		"LayerName": prdlayer.Name,
		"LayerType": layerType,
		"Format":    PBF,
		"URL":       url,
		"Zoom":      amap.Layers[0].MaxZoom,
		"Center":    amap.Center,
		"Color":     fmt.Sprintf(`{"%s-color":"#00ffff"}`, layerType),
	})
	return
}
