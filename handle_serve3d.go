package main

import (
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

//Tileset3d 样式库
type Tileset3d struct {
	Base
}

//OnlineImage 样式库
type OnlineImage struct {
	ID        string    `json:"_id" gorm:"primary_key"`
	Type      string    `json:"dataType"`
	Name      string    `json:"cnname"`
	NameEn    string    `json:"enname"`
	URL       string    `json:"url"`
	Coord     string    `json:"coordType"`
	Require   string    `json:"requireField"`
	Thumbnail string    `json:"thumbnail"`
	CreatedAt time.Time `json:"-"`
}

//OnlineTileset 样式库
type OnlineTileset struct {
	ID        string    `json:"_id" gorm:"primary_key"`
	Type      string    `json:"dataType"`
	Name      string    `json:"cnname"`
	NameEn    string    `json:"enname"`
	URL       string    `json:"url"`
	Thumbnail string    `json:"thumbnail"`
	CreatedAt time.Time `json:"-"`
}

//OnlineTerrain 样式库
type OnlineTerrain struct {
	ID        string    `json:"_id" gorm:"primary_key"`
	Type      string    `json:"dataType"`
	Name      string    `json:"cnname"`
	NameEn    string    `json:"enname"`
	URL       string    `json:"url"`
	Thumbnail string    `json:"thumbnail"`
	Normal    bool      `json:"notSupportNormal"`
	Water     bool      `json:"notSupportWater"`
	CreatedAt time.Time `json:"-"`
}

//OnlineSymbol 样式库
type OnlineSymbol struct {
	ID        string    `json:"_id" gorm:"primary_key"`
	Type      string    `json:"type"`
	Name      string    `json:"name"`
	Content   string    `json:"content"`
	Thumbnail string    `json:"thumbnail"`
	CreatedAt time.Time `json:"-"`
}

//OnlineStyle3d 样式库
type OnlineStyle3d struct {
	ID        string    `json:"_id" gorm:"primary_key"`
	Type      string    `json:"type"`
	Name      string    `json:"name"`
	Code      string    `json:"code"`
	Thumbnail string    `json:"thumbnail"`
	CreatedAt time.Time `json:"-"`
}

//listOnlineImages 获取地图列表
func listOnlineImages(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	var images []OnlineImage
	err := db.Find(&images).Error
	if err != nil {
		resp.Fail(c, 5001)
		return
	}

	resp.DoneData(c, images)
}

//listOnlineTiles 获取地图列表
func listOnlineTiles(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	var tilesets []OnlineTileset
	err := db.Find(&tilesets).Error
	if err != nil {
		resp.Fail(c, 5001)
		return
	}

	resp.DoneData(c, tilesets)
}

//listOnlineTerrains 获取地图列表
func listOnlineTerrains(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	var terrains []OnlineTerrain
	err := db.Find(&terrains).Error
	if err != nil {
		resp.Fail(c, 5001)
		return
	}

	resp.DoneData(c, terrains)
}

//listOnlineSymbols 获取地图列表
func listOnlineSymbols(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}
	str := `{"name":"内置标绘","_id":"cesiumlab_symbols","symbols":[],"children":[{"name":"常规","symbols":[],"children":[{"name":"点状","symbols":["2732986035c811ea966d4136f619ed29","2733d0e035c811ea966d4136f619ed29","2735096035c811ea966d4136f619ed29"],"children":[]},{"name":"线状","symbols":["2736900035c811ea966d4136f619ed29","2737a17035c811ea966d4136f619ed29","2739010035c811ea966d4136f619ed29","273a398035c811ea966d4136f619ed29","273b4af035c811ea966d4136f619ed29","273c837035c811ea966d4136f619ed29","273d94e035c811ea966d4136f619ed29","273e7f4035c811ea966d4136f619ed29","273f69a035c811ea966d4136f619ed29","27407b1035c811ea966d4136f619ed29","2741b39035c811ea966d4136f619ed29"],"children":[]},{"name":"面状","symbols":["2742c50035c811ea966d4136f619ed29","2743af6035c811ea966d4136f619ed29","274499c035c811ea966d4136f619ed29","2745842035c811ea966d4136f619ed29","2746bca035c811ea966d4136f619ed29","2747a70035c811ea966d4136f619ed29","2748b87035c811ea966d4136f619ed29","2749c9e035c811ea966d4136f619ed29"],"children":[]}]},{"name":"立体","symbols":[],"children":[{"name":"模型","symbols":["274b026035c811ea966d4136f619ed29","274becc035c811ea966d4136f619ed29","274cd72035c811ea966d4136f619ed29"],"children":[]}]},{"name":"高级","symbols":["274de89035c811ea966d4136f619ed29","275d2ad035c811ea966d4136f619ed29","27642fb035c811ea966d4136f619ed29","62c1e4f0641011eab214bb3d7d537a27","62c28130641011eab214bb3d7d537a27"],"children":[{"name":"图元","symbols":["274f211035c811ea966d4136f619ed29","2750a7b035c811ea966d4136f619ed29","2751e03035c811ea966d4136f619ed29","2752ca9035c811ea966d4136f619ed29","27542a2035c811ea966d4136f619ed29","27553b9035c811ea966d4136f619ed29","2756c23035c811ea966d4136f619ed29","2757fab035c811ea966d4136f619ed29","27595a4035c811ea966d4136f619ed29","275a6bb035c811ea966d4136f619ed29","275ba43035c811ea966d4136f619ed29","275cb5a035c811ea966d4136f619ed29"],"children":[]},{"name":"管道","symbols":["275e3c4035c811ea966d4136f619ed29","275f26a035c811ea966d4136f619ed29","27605f2035c811ea966d4136f619ed29","2761709035c811ea966d4136f619ed29","27625af035c811ea966d4136f619ed29","2763455035c811ea966d4136f619ed29"],"children":[]}]}]}`
	jstr := make(map[string]interface{})
	json.Unmarshal([]byte(str), &jstr)
	xxx := []map[string]interface{}{jstr}
	resp.DoneData(c, xxx)
}

//getOnlineSymbols 获取地图列表
func getOnlineSymbols(c *gin.Context) {
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
		symbols := []OnlineSymbol{}
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

//listOnlineStyle3ds 获取地图列表
func listOnlineStyle3ds(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	var styles []OnlineStyle3d
	err := db.Find(&styles).Error
	if err != nil {
		resp.Fail(c, 5001)
		return
	}

	resp.DoneData(c, styles)
}

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
	err := c.Bind(&body)
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
	sid := c.Param("id")
	body := &SceneBind{}
	err := c.Bind(&body)
	if err != nil {
		log.Error(err)
		resp.Fail(c, 4001)
		return
	}
	scene := body.toScene()
	scene.Owner = uid
	// 更新insertUser
	dbres := db.Model(&Scene{
		Base: Base{
			ID: sid,
		},
	}).Update(scene)

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
	for _, sid := range sids {
		err := db.Where("id = ?", sid).Delete(Scene{}).Error
		if err != nil {
			log.Error(err)
			resp.Fail(c, 5001)
			return
		}
	}
	resp.Done(c, "")
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
	url := fmt.Sprintf(`http://api.map.baidu.com/place/v2/search?query=%s&region=全国&output=json&ak=3yZlMT3ioSaTaa0kioxwulQrROoN97RV`,
		key)
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
