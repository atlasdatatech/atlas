package main

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	gs "github.com/hishamkaram/geoserver"
	"github.com/jinzhu/gorm"
	log "github.com/sirupsen/logrus"
)

//Geoserver Geoserver实例管理
type Geoserver struct {
	ID         string    `form:"id" json:"id" gorm:"primary_key"`
	Name       string    `form:"name" json:"name" binding:"required"`
	ServiceURL string    `form:"url" json:"url" binding:"required"`
	UserName   string    `form:"username" json:"username" binding:"required"`
	Password   string    `form:"password" json:"password" binding:"required"`
	Thumbnail  string    `form:"thumbnail" json:"thumbnail"`
	CreatedAt  time.Time `form:"-" json:"-"`
}

// NodeType 节点类型
type NodeType string

// Supported db drivers
const (
	GROUP NodeType = "group"
	LAYER          = "layer"
)

//Bound BBox
type Bound struct {
	Minx float64 `json:"minx,omitempty"`
	Maxx float64 `json:"maxx,omitempty"`
	Miny float64 `json:"miny,omitempty"`
	Maxy float64 `json:"maxy,omitempty"`
	CRS  string  `json:"crs,omitempty"`
}

//LayerNode
type LayerNode struct {
	ID       string       `json:"id,omitempty"`
	Name     string       `json:"name,omitempty"`
	Type     NodeType     `json:"type,omitempty"`
	Geom     GeoType      `json:"geom,omitempty"`
	BBox     Bound        `json:"bbox,omitempty"`
	Tiles    []string     `json:"tiles,omitempty"`
	Children []*LayerNode `json:"children,omitempty"`
}

type SrcLayer struct {
	Class string `json:"@class,omitempty"`
	Name  string `json:"name,omitempty"`
	Href  string `json:"href,omitempty"`
}

type PubLayer struct {
	Type string `json:"@type,omitempty"`
	Name string `json:"name,omitempty"`
	Href string `json:"href,omitempty"`
}

type GsAttribute struct {
	Name    string `json:"name,omitempty"`
	Type    string `json:"type,omitempty"`
	Binding string `json:"binding,omitempty"`
}

//**********************************************
//listGeoserverServices 获取Geoserver实例列表
func listGeoserverServices(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	var geoservers []Geoserver
	err := db.Find(&geoservers).Error
	if err != nil {
		resp.Fail(c, 5001)
		return
	}

	resp.DoneData(c, geoservers)
}

//getGeoserverService 获取Geoserver实例信息
func getGeoserverService(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	sid := c.Param("id")
	geoserver := &Geoserver{}
	if err := db.Where("id = ?", sid).First(&geoserver).Error; err != nil {
		if !gorm.IsRecordNotFoundError(err) {
			log.Error(err)
			resp.Fail(c, 5001)
		}
		resp.Fail(c, 4049)
		return
	}

	resp.DoneData(c, geoserver)
}

//createGeoserverService 注册Geoserver实例
func createGeoserverService(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	geoserver := &Geoserver{}
	err := c.Bind(geoserver)
	if err != nil {
		log.Error(err)
		resp.Fail(c, 4001)
		return
	}
	id := ShortID()
	//丢掉原来的id使用新的id
	geoserver.ID = id
	// insertUser
	err = db.Create(geoserver).Error
	if err != nil {
		log.Error(err)
		resp.Fail(c, 5001)
		return
	}
	//管理员创建地图后自己拥有,root不需要
	resp.DoneData(c, gin.H{
		"id": geoserver.ID,
	})
	return
}

//updateGeoserverService 更新Geoserver实例信息
func updateGeoserverService(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	id := c.Param("id")
	geoserver := &Geoserver{}
	err := c.Bind(geoserver)
	if err != nil {
		log.Error(err)
		resp.Fail(c, 4001)
		return
	}
	// 更新insertUser
	dbres := db.Model(Olmap{}).Where("id = ?", id).Update(geoserver)

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

//deleteGeoserverService 删除Geoserver实例信息
func deleteGeoserverService(c *gin.Context) {
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
	dbres := db.Where("id in (?)", sids).Delete(Geoserver{})
	if dbres.Error != nil {
		log.Error(dbres.Error)
		resp.Fail(c, 5001)
		return
	}
	resp.DoneData(c, gin.H{
		"affected": dbres.RowsAffected,
	})
}

//getGeoserverLayers 获取Geoserver图层列表
func getGeoserverLayers(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	sid := c.Param("id")
	geoserver := &Geoserver{}
	if err := db.Where("id = ?", sid).First(&geoserver).Error; err != nil {
		if !gorm.IsRecordNotFoundError(err) {
			log.Error(err)
			resp.Fail(c, 5001)
		}
		resp.Fail(c, 4049)
		return
	}

	gsCatalog := gs.GetCatalog(geoserver.ServiceURL, geoserver.UserName, geoserver.Password)
	ls, err := gsCatalog.GetLayers("")
	if err != nil {
		resp.Fail(c, 4049)
		return
	}
	resp.DoneData(c, ls)
}

//getGWCLayers 获取Geoserver实例信息
func getGWCLayers(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	sid := c.Param("id")
	geoserver := &Geoserver{}
	if err := db.Where("id = ?", sid).First(&geoserver).Error; err != nil {
		if !gorm.IsRecordNotFoundError(err) {
			log.Error(err)
			resp.Fail(c, 5001)
		}
		resp.Fail(c, 4049)
		return
	}
	gsCatalog := gs.GetCatalog(geoserver.ServiceURL, geoserver.UserName, geoserver.Password)
	targetURL := gsCatalog.ParseURL("gwc", "rest", "layers")
	httpRequest := gs.HTTPRequest{
		Method: "GET",
		Accept: "application/json",
		URL:    targetURL,
		Query:  nil,
	}
	response, responseCode := gsCatalog.DoRequest(httpRequest)
	if responseCode != 200 {
		log.Error(string(response))
		resp.Fail(c, responseCode)
		return
	}
	layerNames := []string{}
	err := gsCatalog.DeSerializeJSON(response, &layerNames)
	if err != nil {
		resp.FailMsg(c, err.Error())
		return
	}
	resp.DoneData(c, layerNames)
}

//getGWCLayers 获取Geoserver实例信息
func getGWCLayer(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	sid := c.Param("id")
	geoserver := &Geoserver{}
	if err := db.Where("id = ?", sid).First(&geoserver).Error; err != nil {
		if !gorm.IsRecordNotFoundError(err) {
			log.Error(err)
			resp.Fail(c, 5001)
		}
		resp.Fail(c, 4049)
		return
	}
	layerName := c.Param("name")
	gsCatalog := gs.GetCatalog(geoserver.ServiceURL, geoserver.UserName, geoserver.Password)
	targetURL := gsCatalog.ParseURL("rest", "layergroups", layerName+".json")
	httpRequest := gs.HTTPRequest{
		Method: "GET",
		Accept: "application/json",
		URL:    targetURL,
		Query:  nil,
	}
	respBody, respCode := gsCatalog.DoRequest(httpRequest)
	//非200,当作layer处理,直接按featureTpye矢量类型进行详细信息获取
	if respCode != 200 {
		layer, err := GetGsSourceLayerInfo(gsCatalog, layerName)
		if err != nil {
			log.Error(err)
			resp.FailMsg(c, err.Error())
			return
		}
		turl := gsCatalog.ParseURL("gwc/servicer/tms/1.0.0/", layerName, "@EPSG:900913@pbf/{z}/{x}/{y}.pbf")
		layer.Tiles = []string{turl}
		resp.DoneData(c, layer)
		return
	}
	//定义group返回结构
	var groupResp struct {
		LayerGroup struct {
			Publishables struct {
				Published interface{}
				// Published []*PubLayer `json:"published,omitempty"`
			} `json:"publishables,omitempty"`
			Bounds Bound `json:"bounds,omitempty"`
		} `json:"layerGroup,omitempty"`
	}
	err := gsCatalog.DeSerializeJSON(respBody, &groupResp)
	if err != nil {
		log.Error(err)
		resp.FailMsg(c, err.Error())
		return
	}

	lyrNode := LayerNode{
		ID:   layerName,
		Name: layerName,
		Type: GROUP,
	}
	lyrNode.BBox = groupResp.LayerGroup.Bounds
	turl := gsCatalog.ParseURL("gwc/servicer/tms/1.0.0/", layerName, "@EPSG:900913@pbf/{z}/{x}/{y}.pbf")
	lyrNode.Tiles = []string{turl}

	layers, err := GetGroupLayers(gsCatalog, groupResp.LayerGroup.Publishables.Published)
	if err != nil {
		log.Error(err)
		resp.FailMsg(c, err.Error())
		return
	}
	lyrNode.Children = layers
	resp.DoneData(c, lyrNode)
	return
}

func GetGroupLayer(g *gs.GeoServer, layerName string) (tileLayer *LayerNode, err error) {
	targetURL := g.ParseURL("rest", "layergroups", layerName+".json")
	httpRequest := gs.HTTPRequest{
		Method: "GET",
		Accept: "application/json",
		URL:    targetURL,
		Query:  nil,
	}
	respBody, respCode := g.DoRequest(httpRequest)
	if respCode != 200 {
		log.Error(err)
		return nil, err
	}
	var groupResp struct {
		LayerGroup struct {
			Publishables struct {
				// Published []*PubLayer `json:"published,omitempty"`
				Published interface{}
			} `json:"publishables,omitempty"`
			Bounds Bound `json:"bounds,omitempty"`
		} `json:"layerGroup,omitempty"`
	}
	err = g.DeSerializeJSON(respBody, &groupResp)
	if err != nil {
		log.Error(err)
		return nil, err
	}
	lyrNode := LayerNode{
		ID:   layerName,
		Name: layerName,
		Type: GROUP,
	}
	lyrNode.BBox = groupResp.LayerGroup.Bounds

	layers, err := GetGroupLayers(g, groupResp.LayerGroup.Publishables.Published)
	if err != nil {
		log.Error(err)
	} else {
		lyrNode.Children = layers
	}

	return &lyrNode, nil
}

func GetGroupLayers(g *gs.GeoServer, published interface{}) (layers []*LayerNode, err error) {
	// 判断Published是对象还是数组
	pubLyrs := []*PubLayer{}
	_, ok := published.([]interface{})
	if ok {
		b, _ := json.Marshal(published)
		err = json.Unmarshal(b, &pubLyrs)
		if err != nil {
			log.Error(err)
			return
		}
	} else {
		b, _ := json.Marshal(published)
		pl := &PubLayer{}
		err = json.Unmarshal(b, pl)
		if err != nil {
			log.Error(err)
			return
		}
		pubLyrs = append(pubLyrs, pl)
	}

	for _, lyr := range pubLyrs {
		switch lyr.Type {
		case "layer":
			layer, err := GetGsSourceLayerInfo(g, lyr.Name)
			if err != nil {
				log.Error(err)
				continue
			}
			layers = append(layers, layer)
		case "layerGroup":
			layer, err := GetGroupLayer(g, lyr.Name)
			if err != nil {
				log.Error(err)
				continue
			}
			layers = append(layers, layer)
		}
	}

	return
}

func GetGsSourceLayerInfo(g *gs.GeoServer, layerName string) (layer *LayerNode, err error) {
	wslyr := strings.Split(layerName, ":")
	wsName := ""
	lyrName := layerName
	if len(wslyr) == 2 {
		wsName = wslyr[0]
		lyrName = wslyr[1]
	}
	targetURL := g.ParseURL("rest", "workspaces", wsName, "featuretypes", lyrName)
	httpRequest := gs.HTTPRequest{
		Method: "GET",
		Accept: "application/json",
		URL:    targetURL,
		Query:  nil,
	}
	response, responseCode := g.DoRequest(httpRequest)
	if responseCode != 200 {
		log.Error(string(response))
		err = g.GetError(responseCode, response)
		return
	}

	var layerResponse struct {
		FeatureType struct {
			LatLonBoundingBox Bound `json:"latLonBoundingBox,omitempty"`
			Attributes        struct {
				Attribute []*GsAttribute `json:"attribute,omitempty"`
			} `json:"attributes,omitempty"`
		} `json:"featureType,omitempty"`
	}
	g.DeSerializeJSON(response, &layerResponse)
	lyrNode := LayerNode{
		ID:   layerName,
		Name: lyrName,
		Type: LAYER,
	}
	lyrNode.BBox = layerResponse.FeatureType.LatLonBoundingBox
	for _, attr := range layerResponse.FeatureType.Attributes.Attribute {
		if strings.ToLower(attr.Name) == "geom" {
			lyrNode.Geom = GeoType(attr.Binding[strings.LastIndex(attr.Binding, ".")+1:])
			break
		}
		// layerResponse.FeatureType.Attributes.Attribute[i].Type = attr.Binding[strings.LastIndex(attr.Binding, "."):]
	}
	return &lyrNode, nil
}
