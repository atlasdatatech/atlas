package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/gin-gonic/gin"
	"github.com/go-spatial/geom"
	"github.com/go-spatial/geom/encoding/mvt"
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
	ID       string `json:"id" toml:"id" binding:"required" gorm:"primaryKey"`
	Name     string `json:"name" toml:"name" binding:"required"`
	Type     string `json:"type" toml:"type" binding:"required"`
	Owner    string `json:"owner" toml:"owner" gorm:"index"`
	Host     string `json:"host" toml:"host" binding:"required"`
	Port     int    `json:"port" toml:"port" binding:"required"`
	User     string `json:"user" toml:"user" binding:"required"`
	Password string `json:"password" toml:"password" binding:"required"`
	Database string `json:"database" toml:"database" binding:"required"`
	SRID     int    `json:"srid" toml:"srid" binding:"required"  gorm:"column:srid"`
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
func RegisterProvider(prd *Provider) error {
	// holder for registered providers
	// check if a provider with this name is already registered
	_, ok := providers[prd.ID]
	if ok {
		return fmt.Errorf("驱动已存在")
	}
	if prd.Type == "" {
		return fmt.Errorf("类型必须指定")
	}
	cfg, err := toDicter(prd)
	if err != nil {
		return fmt.Errorf("参数有误")
	}
	// register the provider
	prov, err := aprd.For(prd.Type, cfg)
	if err != nil {
		return err
	}

	// add the provider to our map of registered providers
	providers[prd.ID] = prov
	log.Infof("registering provider(type): %v (%v)", prd.Name, prd.Type)
	//最后保存
	return prd.UpInsert()
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
	GeomField  string          `json:"geometry_fieldname" toml:"geometry_fieldname,omitempty"`
	IDField    string          `json:"id_fieldname" toml:"id_fieldname,omitempty"`
	Fields     string          `json:"fields" toml:"fields,omitempty"`
	GeomType   string          `json:"geometry_type" toml:"geometry_type,omitempty"`
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

//registerProvider 注册数据库驱动
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

	err = RegisterProvider(provider)
	if err != nil {
		log.Error(err)
		resp.Fail(c, 5001)
		return
	}
	resp.DoneData(c, gin.H{
		"id": provider.ID,
	})
	return
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
	prd, ok := providers[player.ProviderID]
	if !ok {
		//查找库表
		newPrd := &Provider{}
		if err := db.Where("id = ?", player.ProviderID).First(newPrd).Error; err != nil {
			if !gorm.IsRecordNotFoundError(err) {
				log.Error(err)
				resp.Fail(c, 5001)
			}
			resp.FailMsg(c, "数据库驱动未注册，请先注册数据库驱动")
			return
		}
		err = RegisterProvider(newPrd)
		if err != nil {
			resp.FailMsg(c, "数据库驱动注册失败")
			return
		}
		prd, ok = providers[player.ProviderID]
		if !ok {
			log.Error("should never enter here")
		}
	}

	// cfg["name"] = player.Name
	// cfg["tablename"] = player.TabLeName
	cfg, err := toDicter(player)
	if err != nil {
		log.Error("to dicter failed")
		resp.FailMsg(c, err.Error())
		return
	}
	err = prd.Std.AddLayer(cfg)
	if err != nil {
		log.Error("add mvt layer failed")
		resp.FailMsg(c, err.Error())
		return
	}
	err = player.UpInsert()
	if err != nil {
		log.Error("save mvt layer failed")
		resp.FailMsg(c, err.Error())
		return
	}
	resp.DoneData(c, gin.H{
		"id": player.ID,
	})
	return
}

func getPrdLayerTileJSON(c *gin.Context) {
	resp := NewRes()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	plid := c.Param("id")
	if plid == "" {
		resp.Fail(c, 4001)
		return
	}
	prdlayer := &ProviderLayer{ID: plid}
	dbres := db.Find(prdlayer)
	if dbres.Error != nil {
		log.Warnf(`get prdLayer error, prdlayer(%s) not found ^^`, plid)
		resp.Fail(c, 4046)
		return
	}

	prd, ok := providers[prdlayer.ProviderID]
	if !ok {
		//查找库表
		newPrd := &Provider{}
		if err := db.Where("id = ?", prdlayer.ProviderID).First(newPrd).Error; err != nil {
			if !gorm.IsRecordNotFoundError(err) {
				log.Error(err)
				resp.Fail(c, 5001)
			}
			resp.FailMsg(c, "数据库驱动未注册，请先注册数据库驱动")
			return
		}
		err := RegisterProvider(newPrd)
		if err != nil {
			resp.FailMsg(c, "数据库驱动注册失败")
			return
		}
		prd, ok = providers[prdlayer.ProviderID]
		if !ok {
			log.Error("should never enter here")
		}
	}
	prd.Layers()
	attr := "atlas realtime tile layer"
	tileJSON := tilejson.TileJSON{
		Attribution: &attr,
		// Bounds:      dts.tlayer.Bounds.Extent(),
		Bounds:   [4]float64{-180, -85, 180, 85},
		Center:   [3]float64{120, 31, 10},
		Format:   "pbf",
		Name:     &prdlayer.Name,
		Scheme:   tilejson.SchemeXYZ,
		TileJSON: tilejson.Version,
		Version:  "1.0.0",
		Grids:    make([]string, 0),
		Data:     make([]string, 0),
	}
	tileurl := fmt.Sprintf("atlasdata://vtlayers/x/%s/{z}/{x}/{y}.pbf", prdlayer.ID)
	fixurl := c.Query("fixurl")
	if fixurl == "yes" {
		tileurl = fmt.Sprintf(`%s/ts/x/%s/{z}/{x}/{y}`, rootURL(c.Request), prdlayer.ID)
	}

	tileJSON.MinZoom = 0
	tileJSON.MaxZoom = 18
	//	build our vector layer details
	layer := tilejson.VectorLayer{
		Version: 2,
		Extent:  4096,
		ID:      prdlayer.ID,
		Name:    prdlayer.Name,
		MinZoom: 0,
		MaxZoom: 18,
		Tiles: []string{
			tileurl,
		},
	}

	layer.GeometryType = tilejson.GeomType(prdlayer.GeomType)

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
		log.Printf("error encoding tileJSON for layer (%v)", prdlayer.ID)
	}
}

func getPrdLayerTiles(c *gin.Context) {
	resp := NewRes()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	plid := c.Param("id")
	if plid == "" {
		resp.Fail(c, 4001)
		return
	}
	//直接找map，找不到就塞一个进去，不存储tilelayer和tilemap
	amap, err := atlas.GetMap(plid)
	if err != nil {
		prdlayer := &ProviderLayer{ID: plid}
		dbres := db.Find(prdlayer)
		if dbres.Error != nil {
			log.Warnf(`get prdLayer error, prdlayer(%s) not found ^^`, plid)
			resp.Fail(c, 4046)
			return
		}

		prd, ok := providers[prdlayer.ProviderID]
		if !ok {
			//查找库表
			newPrd := &Provider{}
			if err := db.Where("id = ?", prdlayer.ProviderID).First(newPrd).Error; err != nil {
				if !gorm.IsRecordNotFoundError(err) {
					log.Error(err)
					resp.Fail(c, 5001)
				}
				resp.FailMsg(c, "数据库驱动未注册，请先注册数据库驱动")
				return
			}
			err := RegisterProvider(newPrd)
			if err != nil {
				resp.FailMsg(c, "数据库驱动注册失败")
				return
			}
			prd, ok = providers[prdlayer.ProviderID]
			if !ok {
				log.Error("should never enter here")
			}
		}
		//map name 就是mapid
		mapconf := fmt.Sprintf(`
		[[maps]]
		name = "%s"
		  [[maps.layers]]
		  provider_layer = "%s.%s"
		  min_zoom = %d
		  max_zoom = %d`, plid, prdlayer.ProviderID, prdlayer.Name, 0, 20)

		autocfg, err := config.Parse(strings.NewReader(mapconf), "")
		if err != nil {
			log.Warnf(`Parse automap error, prdlayer(%s) ^^`, plid)
			resp.Fail(c, 4046)
			return
		}
		err = registerMaps(nil, autocfg.Maps, providers)
		if err != nil {
			log.Warnf(`register automap error, prdlayer(%s) ^^`, plid)
			resp.Fail(c, 4046)
			return
		}
		lyr, _ := prd.Std.Layer(plid)
		log.Info(` auto register map(%s) of vtlyr(%s) ^^`, plid, lyr)
	}
	// lookup our Map
	placeholder, _ := strconv.ParseUint(c.Param("z"), 10, 32)
	z := uint(placeholder)
	placeholder, _ = strconv.ParseUint(c.Param("x"), 10, 32)
	x := uint(placeholder)
	yext := c.Param("y")
	ys := strings.Split(yext, ".")
	if len(ys) != 2 {
		resp.Fail(c, 404)
		return
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

	// buffer to store our compressed bytes
	var gzipBuf bytes.Buffer

	// compress the encoded bytes
	w := gzip.NewWriter(&gzipBuf)
	_, err = w.Write(pbyte)
	if err != nil {
		http.Error(c.Writer, err.Error(), http.StatusInternalServerError)
		return
	}

	// flush and close the writer
	if err = w.Close(); err != nil {
		http.Error(c.Writer, err.Error(), http.StatusInternalServerError)
		return
	}

	// return encoded, gzipped tile
	// mimetype for mapbox vector tiles
	// https://www.iana.org/assignments/media-types/application/vnd.mapbox-vector-tile
	c.Header("Content-Type", mvt.MimeType)
	c.Header("Content-Encoding", "gzip")
	// c.Header("Content-Type", "application/x-protobuf")
	c.Header("Content-Length", fmt.Sprintf("%d", len(gzipBuf.Bytes())))
	c.Writer.WriteHeader(http.StatusOK)
	c.Writer.Write(gzipBuf.Bytes())
	// check for tile size warnings
	if len(gzipBuf.Bytes()) > server.MaxTileSize {
		log.Infof("tile z:%v, x:%v, y:%v is rather large - %vKb", z, x, y, len(gzipBuf.Bytes())/1024)
	}
}

//prdLayerViewer 浏览服务集
func prdLayerViewer(c *gin.Context) {
	resp := NewRes()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	plid := c.Param("id")
	prdlayer := &ProviderLayer{ID: plid}
	dbres := db.Find(prdlayer)
	if dbres.Error != nil {
		log.Warnf(`get prdLayer error, prdlayer(%s) not found ^^`, plid)
		resp.Fail(c, 4046)
		return
	}
	url := fmt.Sprintf(`%s/vtlayers/x/%s/{z}/{x}/{y}.pbf`, rootURL(c.Request), plid) //need use user own service set//
	lrt := c.Query("type")
	c.HTML(http.StatusOK, "dataset.html", gin.H{
		"Title":     "服务集预览(T)",
		"Name":      prdlayer.Name + "@" + plid,
		"LayerName": prdlayer.Name,
		"LayerType": lrt,
		"Format":    PBF,
		"URL":       url,
	})
	return
}
