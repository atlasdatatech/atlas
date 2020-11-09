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
	Status  string     `json:"status"`
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

//SceneBase 样式库
type SceneBase struct {
	ID        string    `json:"id" gorm:"primary_key"`
	Version   string    `json:"version"`
	Name      string    `json:"name" gorm:"index"`
	Summary   string    `json:"summary"`
	Owner     string    `json:"owner" gorm:"index"`
	Base      string    `json:"base" gorm:"index"`
	URL       string    `json:"url"`
	Public    bool      `json:"public"`
	Status    bool      `json:"status"`
	Thumbnail string    `json:"thumbnail"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

//Scene 样式库
type Scene struct {
	SceneBase
	Config []byte `json:"config"`
}

//SceneBind 样式库
type SceneBind struct {
	SceneBase
	Config interface{} `json:"config"`
}

func (b *SceneBind) toScene() *Scene {
	out := &Scene{
		SceneBase: SceneBase{
			ID:        b.ID,
			Version:   b.Version,
			Name:      b.Name,
			Summary:   b.Summary,
			Owner:     b.Owner,
			Base:      b.Base,
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
		SceneBase: SceneBase{
			ID:        s.ID,
			Version:   s.Version,
			Name:      s.Name,
			Summary:   s.Summary,
			Owner:     s.Owner,
			Base:      s.Base,
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

	id, err := shortid.Generate()
	if err != nil {
		id, _ = shortid.Generate()
	}

	body := &SceneBind{}
	err = c.Bind(&body)
	if err != nil {
		log.Error(err)
		resp.Fail(c, 4001)
		return
	}
	scene := body.toScene()
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

	body := &SceneBind{}
	err := c.Bind(&body)
	if err != nil {
		log.Error(err)
		resp.Fail(c, 4001)
		return
	}
	scene := body.toScene()
	scene.Owner = uid
	// insertUser
	err = db.Model(&Scene{}).Update(scene).Error
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
		out.Status = "decode error"
		out.Message = err.Error()
		return
	}

	if resIn.Status == 0 {
		out.Status = "ok"
	} else {
		out.Status = fmt.Sprintf("%d", resIn.Status)
	}

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
	res := NewRes()
	key := c.Query("key")
	url := fmt.Sprintf(`http://api.map.baidu.com/place/v2/search?query=%s&region=全国&output=json&ak=3yZlMT3ioSaTaa0kioxwulQrROoN97RV`,
		key)
	resp, err := http.Get(url)
	if err != nil {
		log.Errorf("geocoder error, details: %s ~", err)
		res.Fail(c, 4001)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		log.Errorf("geocoder error")
		res.Fail(c, resp.StatusCode)
		return
	}

	c.JSON(http.StatusOK, baiduRespConvert(resp.Body))
}
