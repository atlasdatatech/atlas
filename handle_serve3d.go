package main

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jinzhu/gorm"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"github.com/teris-io/shortid"
)

//RespIn body
type RespIn struct {
	Status  int       `json:"status"`
	Message string    `json:"message"`
	Results []PlaceIn `json:"results"`
}

//RespOut body
type RespOut struct {
	Status  int        `json:"status"`
	Message string     `json:"message"`
	Results []PlaceOut `json:"results"`
}

//Place 地点
type Place struct {
	Name     string   `json:"name"`
	Location Location `json:"location"`
	Address  string   `json:"address"`
	Province string   `json:"province"`
	City     string   `json:"city"`
}

//PlaceIn xx
type PlaceIn struct {
	Place
	Area string `json:"area"`
}

//PlaceOut xx
type PlaceOut struct {
	Place
	District string `json:"district"`
}

//Location x,y
type Location struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

//Base 样式库
type Base struct {
	ID        string    `json:"id" gorm:"primary_key"`
	Version   string    `json:"version"`
	Name      string    `json:"name" gorm:"index"`
	Summary   string    `json:"summary"`
	Owner     string    `json:"owner" gorm:"index"`
	URL       string    `json:"url"`
	Public    bool      `json:"public"`
	Status    bool      `json:"status"`
	Thumbnail string    `json:"thumbnail"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

//Scene 样式库
type Scene struct {
	Base
	Config []byte `json:"config"`
}

//SceneBind 样式库
type SceneBind struct {
	Base
	Config interface{} `json:"config"`
}

func (b *SceneBind) toScene() *Scene {
	out := &Scene{
		Base: Base{
			ID:        b.ID,
			Version:   b.Version,
			Name:      b.Name,
			Summary:   b.Summary,
			Owner:     b.Owner,
			URL:       b.URL,
			Public:    b.Public,
			Status:    b.Status,
			Thumbnail: b.Thumbnail,
		},
	}
	// thumb := Thumbnail(300, 168, b.Thumbnail)
	// if thumb == "" {
	// 	out.Thumbnail = b.Thumbnail
	// } else {
	// 	out.Thumbnail = thumb
	// }
	out.Config, _ = json.Marshal(b.Config)
	return out
}

func (s *Scene) toBind() *SceneBind {
	out := &SceneBind{
		Base: Base{
			ID:        s.ID,
			Version:   s.Version,
			Name:      s.Name,
			Summary:   s.Summary,
			Owner:     s.Owner,
			URL:       s.URL,
			Public:    s.Public,
			Status:    s.Status,
			Thumbnail: s.Thumbnail,
		},
	}
	// thumb := Thumbnail(300, 168, b.Thumbnail)
	// if thumb == "" {
	// 	out.Thumbnail = b.Thumbnail
	// } else {
	// 	out.Thumbnail = thumb
	// }
	json.Unmarshal(s.Config, &out.Config)
	return out
}

//UpInsert 创建场景
func (s *Scene) UpInsert() error {
	tmp := &Scene{}
	err := db.Where("id = ?", s.ID).First(tmp).Error
	if err != nil {
		if gorm.IsRecordNotFoundError(err) {
			s.CreatedAt = time.Time{}
			err = db.Create(s).Error
			if err != nil {
				return err
			}
		}
		return err
	}
	err = db.Model(&Scene{}).Update(s).Error
	if err != nil {
		return err
	}
	return nil
}

//Olmap OnlineMap在线底图
type Olmap struct {
	ID        string    `form:"_id" json:"_id" gorm:"primary_key"`
	Type      string    `form:"dataType" json:"dataType"`
	Name      string    `form:"name" json:"name"`
	NameCn    string    `form:"cnname" json:"cnname"`
	NameEn    string    `form:"enname" json:"enname"`
	URL       string    `form:"url" json:"url"`
	Coord     string    `form:"coordType" json:"coordType"`
	Require   string    `form:"requireField" json:"requireField"`
	Thumbnail string    `form:"thumbnail" json:"thumbnail"`
	CreatedAt time.Time `form:"-" json:"-"`
}

//Tileset3d 三维服务
type Tileset3d struct {
	ID        string    `form:"_id" json:"_id" gorm:"primary_key"`
	Type      string    `form:"dataType" json:"dataType"`
	Name      string    `form:"name" json:"name"`
	NameCn    string    `form:"cnname" json:"cnname"`
	NameEn    string    `form:"enname" json:"enname"`
	URL       string    `form:"url" json:"url"`
	Thumbnail string    `form:"thumbnail" json:"thumbnail"`
	CreatedAt time.Time `form:"-" json:"-"`
}

//Terrain3d 地形服务
type Terrain3d struct {
	ID        string    `form:"_id" json:"_id" gorm:"primary_key"`
	Type      string    `form:"dataType" json:"dataType"`
	Name      string    `form:"name" json:"name"`
	NameCn    string    `form:"cnname" json:"cnname"`
	NameEn    string    `form:"enname" json:"enname"`
	URL       string    `form:"url" json:"url"`
	Thumbnail string    `form:"thumbnail" json:"thumbnail"`
	Normal    bool      `form:"notSupportNormal" json:"notSupportNormal"`
	Water     bool      `form:"notSupportWater" json:"notSupportWater"`
	CreatedAt time.Time `json:"-"`
}

//GroupNode 符号库节点结构
type GroupNode struct {
	ID       string      `form:"_id" json:"_id"`
	Name     string      `form:"name" json:"name"`
	Symbols  []string    `form:"symbols" json:"symbols"`
	Children []GroupNode `form:"children" json:"children"`
}

//Symbol3dGroup 三维符号
type Symbol3dGroup struct {
	ID        string    `form:"_id" json:"_id" gorm:"primary_key"`
	Type      string    `form:"type" json:"type"`
	Name      string    `form:"name" json:"name"`
	Content   string    `form:"content" json:"content"`
	Thumbnail string    `form:"thumbnail" json:"thumbnail"`
	CreatedAt time.Time `json:"-"`
}

//Symbol3d 三维符号
type Symbol3d struct {
	ID        string    `form:"_id" json:"_id" gorm:"primary_key"`
	GroupID   string    `form:"groupId" json:"groupId"`
	Type      string    `form:"type" json:"type"`
	Name      string    `form:"name" json:"name"`
	Content   string    `form:"content" json:"content"`
	Thumbnail string    `form:"thumbnail" json:"thumbnail"`
	CreatedAt time.Time `json:"-"`
}

//Style3d 三维样式
type Style3d struct {
	ID        string    `form:"_id" json:"_id" gorm:"primary_key"`
	Type      string    `form:"type" json:"type"`
	Name      string    `form:"name" json:"name"`
	Code      string    `form:"code" json:"code"`
	Thumbnail string    `form:"thumbnail" json:"thumbnail"`
	CreatedAt time.Time `form:"-" json:"-"`
}

//**********************************************
//listOnlineMaps 获取在线底图列表
func listOnlineMaps(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	var olmaps []Olmap
	err := db.Find(&olmaps).Error
	if err != nil {
		resp.Fail(c, 5001)
		return
	}

	resp.DoneData(c, olmaps)
}

//getOnlineMap 获取在线底图详细信息
func getOnlineMap(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	sid := c.Param("id")
	olmap := &Olmap{}
	if err := db.Where("id = ?", sid).First(&olmap).Error; err != nil {
		if !gorm.IsRecordNotFoundError(err) {
			log.Error(err)
			resp.Fail(c, 5001)
		}
		resp.Fail(c, 4049)
		return
	}

	resp.DoneData(c, olmap)
}

//createOnlineMap 创建在线底图
func createOnlineMap(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	olmap := &Olmap{}
	err := c.Bind(olmap)
	if err != nil {
		log.Error(err)
		resp.Fail(c, 4001)
		return
	}
	id, err := shortid.Generate()
	if err != nil {
		id, _ = shortid.Generate()
	}
	//丢掉原来的id使用新的id
	olmap.ID = id
	// insertUser
	err = db.Create(olmap).Error
	if err != nil {
		log.Error(err)
		resp.Fail(c, 5001)
		return
	}
	//管理员创建地图后自己拥有,root不需要
	resp.DoneData(c, gin.H{
		"id": olmap.ID,
	})
	return
}

//updateOnlineMap 更新在线底图信息
func updateOnlineMap(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	id := c.Param("id")
	olmap := &Olmap{}
	err := c.Bind(olmap)
	if err != nil {
		log.Error(err)
		resp.Fail(c, 4001)
		return
	}
	// 更新insertUser
	dbres := db.Model(Olmap{}).Where("id = ?", id).Update(olmap)

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

//deleteOnlineMap 删除在线底图
func deleteOnlineMap(c *gin.Context) {
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
	dbres := db.Where("id in (?)", sids).Delete(Olmap{})
	if dbres.Error != nil {
		log.Error(dbres.Error)
		resp.Fail(c, 5001)
		return
	}
	resp.DoneData(c, gin.H{
		"affected": dbres.RowsAffected,
	})
}

//**********************************************
//listTilesets3d 获取三维服务列表
func listTilesets3d(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	var tileset3d []Tileset3d
	err := db.Find(&tileset3d).Error
	if err != nil {
		resp.Fail(c, 5001)
		return
	}

	resp.DoneData(c, tileset3d)
}

//getOnlineImage 获取三维服务详细信息
func getTileset3d(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	sid := c.Param("id")
	tileset3d := &Tileset3d{}
	if err := db.Where("id = ?", sid).First(&tileset3d).Error; err != nil {
		if !gorm.IsRecordNotFoundError(err) {
			log.Error(err)
			resp.Fail(c, 5001)
		}
		resp.Fail(c, 4049)
		return
	}

	resp.DoneData(c, tileset3d)
}

//createTileset3d 创建新的三维服务
func createTileset3d(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	tileset3d := &Tileset3d{}
	err := c.Bind(tileset3d)
	if err != nil {
		log.Error(err)
		resp.Fail(c, 4001)
		return
	}
	id, err := shortid.Generate()
	if err != nil {
		id, _ = shortid.Generate()
	}
	//丢掉原来的id使用新的id
	tileset3d.ID = id
	// insertUser
	err = db.Create(tileset3d).Error
	if err != nil {
		log.Error(err)
		resp.Fail(c, 5001)
		return
	}
	//管理员创建地图后自己拥有,root不需要
	resp.DoneData(c, gin.H{
		"id": tileset3d.ID,
	})
	return
}

//updateTileset3d 更新指定三维服务
func updateTileset3d(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	id := c.Param("id")
	tileset3d := &Tileset3d{}
	err := c.Bind(tileset3d)
	if err != nil {
		log.Error(err)
		resp.Fail(c, 4001)
		return
	}
	// 更新insertUser
	dbres := db.Model(Tileset3d{}).Where("id = ?", id).Update(tileset3d)

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

//deleteTileset3d 删除指定三维服务
func deleteTileset3d(c *gin.Context) {
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
	dbres := db.Where("id in (?)", sids).Delete(Tileset3d{})
	if dbres.Error != nil {
		log.Error(dbres.Error)
		resp.Fail(c, 5001)
		return
	}
	resp.DoneData(c, gin.H{
		"affected": dbres.RowsAffected,
	})
}

//**********************************************
//listTerrains3d 获取地形服务列表
func listTerrains3d(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	var terrain []Terrain3d
	err := db.Find(&terrain).Error
	if err != nil {
		resp.Fail(c, 5001)
		return
	}

	resp.DoneData(c, terrain)
}

//getTerrain3d 获取地形服务详细信息
func getTerrain3d(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	sid := c.Param("id")
	terrain := &Terrain3d{}
	if err := db.Where("id = ?", sid).First(&terrain).Error; err != nil {
		if !gorm.IsRecordNotFoundError(err) {
			log.Error(err)
			resp.Fail(c, 5001)
		}
		resp.Fail(c, 4049)
		return
	}

	resp.DoneData(c, terrain)
}

//createTerrain3d 创建新的地形服务
func createTerrain3d(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	terrain := &Terrain3d{}
	err := c.Bind(terrain)
	if err != nil {
		log.Error(err)
		resp.Fail(c, 4001)
		return
	}
	id, err := shortid.Generate()
	if err != nil {
		id, _ = shortid.Generate()
	}
	//丢掉原来的id使用新的id
	terrain.ID = id
	// insertUser
	err = db.Create(terrain).Error
	if err != nil {
		log.Error(err)
		resp.Fail(c, 5001)
		return
	}
	//管理员创建地图后自己拥有,root不需要
	resp.DoneData(c, gin.H{
		"id": terrain.ID,
	})
	return
}

//updateTerrain3d 更新指定地形服务
func updateTerrain3d(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	id := c.Param("id")
	terrain := &Terrain3d{}
	err := c.Bind(terrain)
	if err != nil {
		log.Error(err)
		resp.Fail(c, 4001)
		return
	}
	// 更新insertUser
	dbres := db.Model(Terrain3d{}).Where("id = ?", id).Update(terrain)

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

//deleteTerrain3d 删除指定地形服务
func deleteTerrain3d(c *gin.Context) {
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
	dbres := db.Where("id in (?)", sids).Delete(Terrain3d{})
	if dbres.Error != nil {
		log.Error(dbres.Error)
		resp.Fail(c, 5001)
		return
	}
	resp.DoneData(c, gin.H{
		"affected": dbres.RowsAffected,
	})
}

//**********************************************
//listOnlineSymbols 获取地图列表
func getSymbolGroups3dList(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	var groups []Symbol3dGroup
	err := db.Find(&groups).Error
	if err != nil {
		resp.Fail(c, 5001)
		return
	}
	// str := `{"name":"内置标绘","_id":"cesiumlab_symbols","symbols":[],"children":[{"name":"常规","symbols":[],"children":[{"name":"点状","symbols":["2732986035c811ea966d4136f619ed29","2733d0e035c811ea966d4136f619ed29","2735096035c811ea966d4136f619ed29"],"children":[]},{"name":"线状","symbols":["2736900035c811ea966d4136f619ed29","2737a17035c811ea966d4136f619ed29","2739010035c811ea966d4136f619ed29","273a398035c811ea966d4136f619ed29","273b4af035c811ea966d4136f619ed29","273c837035c811ea966d4136f619ed29","273d94e035c811ea966d4136f619ed29","273e7f4035c811ea966d4136f619ed29","273f69a035c811ea966d4136f619ed29","27407b1035c811ea966d4136f619ed29","2741b39035c811ea966d4136f619ed29"],"children":[]},{"name":"面状","symbols":["2742c50035c811ea966d4136f619ed29","2743af6035c811ea966d4136f619ed29","274499c035c811ea966d4136f619ed29","2745842035c811ea966d4136f619ed29","2746bca035c811ea966d4136f619ed29","2747a70035c811ea966d4136f619ed29","2748b87035c811ea966d4136f619ed29","2749c9e035c811ea966d4136f619ed29"],"children":[]}]},{"name":"立体","symbols":[],"children":[{"name":"模型","symbols":["274b026035c811ea966d4136f619ed29","274becc035c811ea966d4136f619ed29","274cd72035c811ea966d4136f619ed29"],"children":[]}]},{"name":"高级","symbols":["274de89035c811ea966d4136f619ed29","275d2ad035c811ea966d4136f619ed29","27642fb035c811ea966d4136f619ed29","62c1e4f0641011eab214bb3d7d537a27","62c28130641011eab214bb3d7d537a27"],"children":[{"name":"图元","symbols":["274f211035c811ea966d4136f619ed29","2750a7b035c811ea966d4136f619ed29","2751e03035c811ea966d4136f619ed29","2752ca9035c811ea966d4136f619ed29","27542a2035c811ea966d4136f619ed29","27553b9035c811ea966d4136f619ed29","2756c23035c811ea966d4136f619ed29","2757fab035c811ea966d4136f619ed29","27595a4035c811ea966d4136f619ed29","275a6bb035c811ea966d4136f619ed29","275ba43035c811ea966d4136f619ed29","275cb5a035c811ea966d4136f619ed29"],"children":[]},{"name":"管道","symbols":["275e3c4035c811ea966d4136f619ed29","275f26a035c811ea966d4136f619ed29","27605f2035c811ea966d4136f619ed29","2761709035c811ea966d4136f619ed29","27625af035c811ea966d4136f619ed29","2763455035c811ea966d4136f619ed29"],"children":[]}]}]}`
	// jstr := make(map[string]interface{})
	// json.Unmarshal([]byte(str), &jstr)
	// xxx := []map[string]interface{}{jstr}
	resp.DoneData(c, groups)
}

//getSymbolGroup3d 获取地图列表
func getSymbolGroup3d(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	sid := c.Param("id")
	group3d := &Symbol3dGroup{}
	if err := db.Where("id = ?", sid).First(&group3d).Error; err != nil {
		if !gorm.IsRecordNotFoundError(err) {
			log.Error(err)
			resp.Fail(c, 5001)
		}
		resp.Fail(c, 4049)
		return
	}

	resp.DoneData(c, group3d)
}

//updateTileset3d 更新指定三维符号库
func updateSymbolGroup3d(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	id := c.Param("id")
	group3d := &Symbol3dGroup{}
	err := c.Bind(group3d)
	if err != nil {
		log.Error(err)
		resp.Fail(c, 4001)
		return
	}
	// 更新insertUser
	dbres := db.Model(Symbol3dGroup{}).Where("id = ?", id).Update(group3d)

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

//listSymbols3d 获取地形服务详细信息
func listSymbols3d(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}
	var body struct {
		IDs string `form:"ids" json:"ids"`
	}
	err := c.Bind(&body)
	if err != nil {
		log.Error(err)
		resp.Fail(c, 4001)
		return
	}
	if body.IDs != "" {
		ids := strings.Split(body.IDs, ",")
		symbols := []Symbol3d{}
		res := db.Find(&symbols, ids)
		if res.Error != nil {
			log.Error(err)
			resp.FailMsg(c, err.Error())
			return
		}
		resp.DoneData(c, symbols)
		return
	}
	resp.Done(c, "")
}

//getSymbols3d 获取地图列表
func getSymbol3d(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	sid := c.Param("id")
	symbol3d := &Symbol3d{}
	if err := db.Where("id = ?", sid).First(&symbol3d).Error; err != nil {
		if !gorm.IsRecordNotFoundError(err) {
			log.Error(err)
			resp.Fail(c, 5001)
		}
		resp.Fail(c, 4049)
		return
	}

	resp.DoneData(c, symbol3d)
}

//createTileset3d 创建新的三维服务
func createSymbol3d(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	symbol3d := &Symbol3d{}
	err := c.Bind(symbol3d)
	if err != nil {
		log.Error(err)
		resp.Fail(c, 4001)
		return
	}
	id, err := shortid.Generate()
	if err != nil {
		id, _ = shortid.Generate()
	}
	//丢掉原来的id使用新的id
	symbol3d.ID = id
	// insertUser
	err = db.Create(symbol3d).Error
	if err != nil {
		log.Error(err)
		resp.Fail(c, 5001)
		return
	}
	//更新symbolgroup
	if symbol3d.GroupID != "" {
		group := Symbol3dGroup{
			ID: symbol3d.GroupID,
		}
		dbres := db.Find(&group)
		if dbres.RowsAffected != 1 {
			log.Error(dbres.Error.Error())
			goto r
		}
		data := group.Content
		content := GroupNode{}
		err := json.Unmarshal([]byte(data), &content)
		if err != nil {
			log.Error("unmarsh content error")
			goto r
		}
		content.Symbols = append(content.Symbols, symbol3d.ID)
		contentData, err := json.Marshal(content)
		if err != nil {
			log.Error("Marshal content error")
			goto r
		}
		group.Content = string(contentData)
		dbres = db.Model(&Symbol3dGroup{}).Update(&group)
		if dbres.RowsAffected != 1 {
			log.Error(dbres.Error.Error())
		}
	}

r:
	resp.DoneData(c, gin.H{
		"id": symbol3d.ID,
	})
	return
}

//updateTileset3d 更新指定三维服务
func updateSymbol3d(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	id := c.Param("id")
	symbol3d := &Symbol3d{}
	err := c.Bind(symbol3d)
	if err != nil {
		log.Error(err)
		resp.Fail(c, 4001)
		return
	}
	// 更新insertUser
	dbres := db.Model(Symbol3d{}).Where("id = ?", id).Update(symbol3d)

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

//deleteTileset3d 删除指定三维符号
func deleteSymbol3d(c *gin.Context) {
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
	dbres := db.Where("id in (?)", sids).Delete(Symbol3d{})
	if dbres.Error != nil {
		log.Error(dbres.Error)
		resp.Fail(c, 5001)
		return
	}
	resp.DoneData(c, gin.H{
		"affected": dbres.RowsAffected,
	})
}

//**********************************************
//listStyles3d 获取地图列表
func listStyles3d(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	var styles []Style3d
	err := db.Find(&styles).Error
	if err != nil {
		resp.Fail(c, 5001)
		return
	}

	resp.DoneData(c, styles)
}

func getStyle3d(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	sid := c.Param("id")
	style3d := &Style3d{}
	if err := db.Where("id = ?", sid).First(&style3d).Error; err != nil {
		if !gorm.IsRecordNotFoundError(err) {
			log.Error(err)
			resp.Fail(c, 5001)
		}
		resp.Fail(c, 4049)
		return
	}

	resp.DoneData(c, style3d)
}

//createStyle3d 创建新样式
func createStyle3d(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	style := &Style3d{}
	err := c.Bind(style)
	if err != nil {
		log.Error(err)
		resp.Fail(c, 4001)
		return
	}
	id, err := shortid.Generate()
	if err != nil {
		id, _ = shortid.Generate()
	}
	//丢掉原来的id使用新的id
	style.ID = id
	// insertUser
	err = db.Create(style).Error
	if err != nil {
		log.Error(err)
		resp.Fail(c, 5001)
		return
	}
	//管理员创建地图后自己拥有,root不需要
	resp.DoneData(c, gin.H{
		"id": style.ID,
	})
	return
}

//updateOnlineStyle3d 更新3d样式
func updateStyle3d(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}
	id := c.Param("id")

	style := &Style3d{}
	err := c.Bind(style)
	if err != nil {
		log.Error(err)
		resp.Fail(c, 4001)
		return
	}
	// 更新insertUser
	dbres := db.Model(Style3d{}).Where("id = ?", id).Update(style)

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

//deleteOnlineStyle3d 删除样式
func deleteStyle3d(c *gin.Context) {
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
	dbres := db.Where("id in (?)", sids).Delete(Style3d{})
	if dbres.Error != nil {
		log.Error(dbres.Error)
		resp.Fail(c, 5001)
		return
	}
	resp.DoneData(c, gin.H{
		"affected": dbres.RowsAffected,
	})
}

//**********************************************
//listStyles 获取地图列表
func listScenes(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	var scenes []Scene
	tdb := db
	pub, y := c.GetQuery("public")
	if y && strings.ToLower(pub) == "true" {
		if casEnf.Enforce(uid, "list-atlas-maps", c.Request.Method) {
			tdb = tdb.Where("owner = ? and public = ? ", ATLAS, true)
		}
	} else {
		tdb = tdb.Where("owner = ?", uid)
	}
	kw, y := c.GetQuery("keyword")
	if y {
		tdb = tdb.Where(fmt.Sprintf(`name like '%%%s%%'`, kw))
	}
	order, y := c.GetQuery("order")
	if y {
		log.Info(order)
		tdb = tdb.Order(order)
	}
	total := 0
	err := tdb.Model(&Style{}).Count(&total).Error
	if err != nil {
		resp.Fail(c, 5001)
		return
	}
	start := 0
	rows := 10
	if offset, y := c.GetQuery("start"); y {
		rs, yr := c.GetQuery("rows") //limit count defaut 10
		if yr {
			ri, err := strconv.Atoi(rs)
			if err == nil {
				rows = ri
			}
		}
		start, _ = strconv.Atoi(offset)
		tdb = tdb.Offset(start).Limit(rows)
	}
	err = tdb.Find(&scenes).Error
	if err != nil {
		resp.Fail(c, 5001)
		return
	}

	resp.DoneData(c, gin.H{
		"keyword": kw,
		"order":   order,
		"start":   start,
		"rows":    rows,
		"total":   total,
		"list":    scenes,
	})
}

func getScene(c *gin.Context) {

	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	sid := c.Param("id")
	scene := &Scene{}
	if err := db.Where("id = ?", sid).First(&scene).Error; err != nil {
		if !gorm.IsRecordNotFoundError(err) {
			log.Error(err)
			resp.Fail(c, 5001)
		}
		resp.Fail(c, 4049)
		return
	}

	resp.DoneData(c, scene.toBind())
}

// createScene xxx
func createScene(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	body := &SceneBind{}
	err := c.Bind(body)
	if err != nil {
		log.Error(err)
		resp.Fail(c, 4001)
		return
	}
	scene := body.toScene()
	id, err := shortid.Generate()
	if err != nil {
		id, _ = shortid.Generate()
	}
	//丢掉原来的id使用新的id
	scene.ID = id
	scene.Owner = uid
	// insertUser
	err = db.Create(scene).Error
	if err != nil {
		log.Error(err)
		resp.Fail(c, 5001)
		return
	}
	//管理员创建地图后自己拥有,root不需要
	resp.DoneData(c, gin.H{
		"id": scene.ID,
	})
	return
}

// updateScene xxx
func updateScene(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}
	id := c.Param("id")
	body := &SceneBind{}
	err := c.Bind(body)
	if err != nil {
		log.Error(err)
		resp.Fail(c, 4001)
		return
	}
	scene := body.toScene()
	scene.Owner = uid
	// 更新insertUser
	dbres := db.Model(&Scene{}).Where("id = ?", id).Update(scene)

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

//deleteStyle 删除样式
func deleteScene(c *gin.Context) {
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
	// for _, sid := range sids {
	// 	err := db.Where("id = ?", sid).Delete(Scene{}).Error
	// 	if err != nil {
	// 		log.Error(err)
	// 		resp.Fail(c, 5001)
	// 		return
	// 	}
	// }
	dbres := db.Where("id in (?)", sids).Delete(Scene{})
	if dbres.Error != nil {
		log.Error(dbres.Error)
		resp.Fail(c, 5001)
		return
	}
	resp.DoneData(c, gin.H{
		"affected": dbres.RowsAffected,
	})
}

func baiduRespConvert(body io.Reader) (out RespOut) {
	resIn := RespIn{}
	jdecoder := json.NewDecoder(body)
	err := jdecoder.Decode(&resIn)
	if err != nil {
		out.Status = -1
		out.Message = err.Error()
		return
	}
	out.Status = resIn.Status
	out.Message = resIn.Message

	for _, pIn := range resIn.Results {
		pOut := PlaceOut{}
		pOut.Place = pIn.Place
		// 需要坐标变换，否则直接赋值即可
		pOut.Location.Lng, pOut.Location.Lat = Bd09ToWgs84(pIn.Location.Lng, pIn.Location.Lat)
		pOut.District = pIn.Area
		out.Results = append(out.Results, pOut)
	}

	return
}

func geoCoder(c *gin.Context) {
	resp := NewResp()
	key := c.Query("key")
	api := viper.GetString("geocoder.api")
	url := fmt.Sprintf(api, key)
	// url := fmt.Sprintf(`http://api.map.baidu.com/place/v2/search?query=%s&region=全国&output=json&ak=3yZlMT3ioSaTaa0kioxwulQrROoN97RV`,key)
	res, err := http.Get(url)
	if err != nil {
		log.Errorf("geocoder error, details: %s ~", err)
		resp.Fail(c, 4001)
		return
	}
	defer res.Body.Close()

	if res.StatusCode != 200 {
		log.Errorf("geocoder error")
		resp.FailMsg(c, "geocoder error")
		return
	}
	bd := baiduRespConvert(res.Body)
	if bd.Status != 0 {
		resp.FailMsg(c, bd.Message)
		return
	}
	resp.DoneData(c, bd.Results)
}

func getShortID(c *gin.Context) {
	resp := NewResp()
	id, err := shortid.Generate()
	if err != nil {
		id, err = shortid.Generate()
		if err != nil {
			id, err = shortid.Generate()
			if err != nil {
				resp.FailMsg(c, "shortid generate error")
				return
			}
		}
	}
	resp.DoneData(c, gin.H{
		"id": id,
	})
}

func tilesProxy(c *gin.Context) {
	resp := NewResp()
	uri := c.Param("uri")
	host := viper.GetString("proxy.host")
	appid := viper.GetString("proxy.appid")
	key := viper.GetString("proxy.key")
	timestamp := "1603949582000" //strconv.FormatInt(time.Now().Unix(), 10)
	hash := md5.New()
	hash.Write([]byte(appid + key + uri + timestamp))
	token := strings.ToUpper(hex.EncodeToString(hash.Sum(nil)))
	claims := fmt.Sprintf(`{"appId":"%s","ms":%s,"token":"%s"}`, appid, timestamp, token)
	body := bytes.NewBuffer([]byte(claims))
	url := host + uri
	client := &http.Client{}
	req, err := http.NewRequest("PUT", url, body)
	if err != nil {
		resp.FailMsg(c, err.Error())
		return
	}

	// req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Content-Type", "application/json;charset=utf-8")
	//     return JSON.stringify(claims);
	res, err := client.Do(req)
	if err != nil {
		resp.FailMsg(c, err.Error())
		return
	}
	defer res.Body.Close()

	contentType := res.Header.Get("Content-Type")
	contentLength := res.ContentLength
	// c.Header("Content-Type", contentType)
	// rbody, err := ioutil.ReadAll(res.Body)
	// if err != nil {
	// resp.FailMsg(c, err.Error())
	// return
	// }
	// c.Writer.Write(rbody)
	c.DataFromReader(http.StatusOK, contentLength, contentType, res.Body, map[string]string{})
}
