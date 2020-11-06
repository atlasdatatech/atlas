package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

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

//Scene 场景库
type Scene struct {
	Map
}

func getScene(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	mid := c.Param("id")
	if mid == "" {
		res.Fail(c, 4001)
		return
	}
	if !casEnf.Enforce(id, mid, "(READ)|(EDIT)") {
		res.Fail(c, 403)
		return
	}
	m := &Map{}
	if err := db.Where("id = ?", mid).First(&m).Error; err != nil {
		if !gorm.IsRecordNotFoundError(err) {
			log.Error(err)
			res.Fail(c, 5001)
		}
		res.Fail(c, 4049)
		return
	}
	res.DoneData(c, m.toBind())
}

//listStyles 获取地图列表
func listScene2(c *gin.Context) {
	res := NewRes()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	set := userSet.service(uid)
	if set == nil {
		log.Warnf("uploadStyle, %s's service not found ^^", uid)
		res.Fail(c, 4043)
		return
	}
	var styles []Style
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
		tdb = tdb.Where("name ~ ?", kw)
	}
	order, y := c.GetQuery("order")
	if y {
		log.Info(order)
		tdb = tdb.Order(order)
	}
	total := 0
	err := tdb.Model(&Style{}).Count(&total).Error
	if err != nil {
		res.Fail(c, 5001)
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
	err = tdb.Find(&styles).Error
	if err != nil {
		res.Fail(c, 5001)
		return
	}
	res.DoneData(c, gin.H{
		"keyword": kw,
		"order":   order,
		"start":   start,
		"rows":    rows,
		"total":   total,
		"list":    styles,
	})

	// var styles []*Style
	// set.S.Range(func(_, v interface{}) bool {
	// 	s, ok := v.(*Style)
	// 	if ok {
	// 		styles = append(styles, s)
	// 	}
	// 	return true
	// })
	// if uid != ATLAS && "true" == c.Query("public") {
	// 	set := userSet.service(ATLAS)
	// 	if set != nil {
	// 		set.S.Range(func(_, v interface{}) bool {
	// 			s, ok := v.(*Style)
	// 			if ok {
	// 				if s.Public {
	// 					styles = append(styles, s)
	// 				}
	// 			}
	// 			return true
	// 		})
	// 	}
	// }
	// res.DoneData(c, styles)
}

func listScenes(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	var maps []Map
	if id == ATLAS {
		db.Select("id,title,summary,user,thumbnail,created_at,updated_at").Find(&maps)
		for i := 0; i < len(maps); i++ {
			maps[i].Action = "EDIT"
		}
		res.DoneData(c, maps)
		return
	}

	uperms := casEnf.GetPermissionsForUser(id)
	roles, _ := casEnf.GetRolesForUser(id)
	for _, role := range roles {
		rperms := casEnf.GetPermissionsForUser(role)
		uperms = append(uperms, rperms...)
	}
	mapids := make(map[string]string)
	for _, p := range uperms {
		if len(p) == 3 {
			mapids[p[1]] = p[2]
		}
	}
	var ids []string
	for k := range mapids {
		ids = append(ids, k)
	}
	db.Select("id,title,summary,user,thumbnail,created_at,updated_at").Where("id in (?)", ids).Find(&maps)

	//添加每个map对应的该用户的权限
	for i := 0; i < len(maps); i++ {
		maps[i].Action = mapids[maps[i].ID]
	}

	res.DoneData(c, maps)
	return
}

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

	body := &MapBind{}
	err = c.Bind(&body)
	if err != nil {
		log.Error(err)
		resp.Fail(c, 4001)
		return
	}
	scene := body.toScene()
	scene.ID = id
	scene.User = uid
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
