package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jinzhu/gorm"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
	log "github.com/sirupsen/logrus"
	gomail "gopkg.in/gomail.v2"
)

var codes = map[int]string{
	0: "检查消息",

	200: "成功",
	201: "已创建",
	202: "已接受",
	204: "无内容",

	300: "重定向",

	400:  "请求无法解析",
	4001: "必填参数校验错误",
	4002: "达到最大尝试登录次数,稍后再试",
	4003: "瓦片请求格式错误",
	4004: "符号请求格式错误",
	4005: "字体请求格式错误",

	401:  "未授权",
	4011: "用户名或密码错误",
	4012: "用户名或密码非法",

	403:  "禁止访问",
	4031: "用户已存在",

	404:  "找不到资源",
	4041: "用户不存在",
	4042: "角色不存在",
	4043: "地图不存在",
	4044: "服务不存在",
	4045: "找不到数据集",
	4046: "找不到上传文件",

	408: "请求超时",

	500:  "系统错误",
	5001: "数据库错误",
	5002: "文件读写错误",
	5003: "IO读写错误",
	5004: "MBTiles读写错误",
	5005: "系统配置错误",

	501: "维护中",
	503: "服务不可用",
}

//Res response schema
type Res struct {
	Code int         `json:"code"`
	Msg  string      `json:"msg"`
	Data interface{} `json:"data"`
}

//NewRes Create Res
func NewRes() *Res {
	return &Res{
		Code: http.StatusOK,
		Msg:  codes[http.StatusOK],
	}
}

//Fail failed error
func (res *Res) Fail(c *gin.Context, code int) {
	res.Code = code
	res.Msg = codes[code]
	c.JSON(http.StatusOK, res)
}

//FailErr failed string
func (res *Res) FailErr(c *gin.Context, err error) {
	res.Code = 0
	if err != nil {
		res.Msg = err.Error()
	}
	c.JSON(http.StatusOK, res)
}

//FailMsg failed string
func (res *Res) FailMsg(c *gin.Context, msg string) {
	res.Code = 0
	res.Msg = msg
	c.JSON(http.StatusOK, res)
}

//Done done
func (res *Res) Done(c *gin.Context, msg string) {
	res.Code = http.StatusOK
	res.Msg = codes[http.StatusOK]
	if msg != "" {
		res.Msg = msg
	}
	c.JSON(http.StatusOK, res)
}

//DoneCode done
func (res *Res) DoneCode(c *gin.Context, code int) {
	res.Code = code
	res.Msg = codes[code]
	c.JSON(http.StatusOK, res)
}

//DoneData done
func (res *Res) DoneData(c *gin.Context, data interface{}) {
	res.Code = http.StatusOK
	res.Msg = codes[http.StatusOK]
	res.Data = data
	c.JSON(http.StatusOK, res)
}

//Reset reset to init
func (res *Res) Reset() {
	res.Code = http.StatusOK
	res.Msg = codes[http.StatusOK]
}

//MailConfig email config and data
type MailConfig struct {
	From     string
	ReplyTo  string
	Subject  string
	TextPath string
	HTMLPath string
	Data     interface{}
}

//SendMail send email
func (conf *MailConfig) SendMail() (err error) {
	m := gomail.NewMessage()

	m.SetHeader("From", conf.From)
	m.SetHeader("To", conf.ReplyTo)
	m.SetHeader("Subject", conf.Subject)
	m.SetHeader("ReplyTo", conf.ReplyTo)

	m.AddAlternativeWriter("text/html", func(w io.Writer) error {
		return template.Must(template.ParseFiles(conf.HTMLPath)).Execute(w, conf.Data)
	})

	d := gomail.NewDialer(cfgV.GetString("smtp.credentials.host"), 587, cfgV.GetString("smtp.credentials.user"), cfgV.GetString("smtp.credentials.password"))
	return d.DialAndSend(m)
}

func getEscapedString(str string) string {
	return strings.Replace(url.QueryEscape(str), "+", "%20", -1)
}

var rSlugify1, _ = regexp.Compile(`[^\w ]+`)
var rSlugify2, _ = regexp.Compile(` +`)

var rUsername, _ = regexp.Compile(`^[a-zA-Z0-9\-\_]+$`)
var rEmail, _ = regexp.Compile(`^[a-zA-Z0-9\-\_\.\+]+@[a-zA-Z0-9\-\_\.]+\.[a-zA-Z0-9\-\_]+$`)

var signupProviderReg, _ = regexp.Compile(`/[^a-zA-Z0-9\-\_]/g`)

/**
preparing id
*/

func slugify(str string) string {
	str = strings.ToLower(str)
	str = rSlugify1.ReplaceAllString(str, "")
	str = rSlugify2.ReplaceAllString(str, "-")
	return str
}

func slugifyName(str string) string {
	str = strings.TrimSpace(str)
	return rSlugify2.ReplaceAllString(str, " ")
}

//XHR xmlhttprequest
func XHR(c *gin.Context) bool {
	return strings.ToLower(c.Request.Header.Get("X-Requested-With")) == "xmlhttprequest"
}

func generateToken(n int) []byte {
	b := make([]byte, n)
	_, err := rand.Read(b)
	if err != nil {
		return b
	}
	token := make([]byte, n*2)
	hex.Encode(token, b)
	return token
}

func createPaths(name string) {
	styles := cfgV.GetString("assets.styles")
	fonts := cfgV.GetString("assets.fonts")
	tilesets := cfgV.GetString("assets.tilesets")
	datasets := cfgV.GetString("assets.datasets")
	os.MkdirAll(styles, os.ModePerm)
	os.MkdirAll(tilesets, os.ModePerm)
	os.MkdirAll(datasets, os.ModePerm)
	os.MkdirAll(fonts, os.ModePerm)
}

func checkUser(uid string) int {
	if uid == "" {
		return 4001
	}
	user := &User{}
	if err := db.Where("name = ?", uid).First(&user).Error; err != nil {
		if !gorm.IsRecordNotFoundError(err) {
			log.Error(err)
			return 5001
		}
		return 4041
	}
	return 200
}

func checkRole(rid string) int {
	if rid == "" {
		return 4001
	}
	role := &Role{}
	if err := db.Where("id = ?", rid).First(&role).Error; err != nil {
		if !gorm.IsRecordNotFoundError(err) {
			log.Error(err)
			return 5001
		}
		return 4042
	}
	return 200
}

func checkMap(mid string) int {
	if mid == "" {
		return 4001
	}
	m := &Map{}
	if err := db.Where("id = ?", mid).First(&m).Error; err != nil {
		if !gorm.IsRecordNotFoundError(err) {
			log.Error(err)
			return 5001
		}
		return 4043
	}
	return 200
}

func checkDataset(did string) int {
	if did == "" {
		return 4001
	}
	ds := &Dataset{}
	if err := db.Where("name = ?", did).First(&ds).Error; err != nil {
		if !gorm.IsRecordNotFoundError(err) {
			log.Error(err)
			return 5001
		}
		return 4045
	}
	return 200
}

func buffering(name string, r float64) int {

	db.Exec(`DROP TABLE if EXISTS buffers;`)
	if "banks" != name {
		err := db.Exec(fmt.Sprintf(`CREATE TABLE buffers AS 
		SELECT 机构号,名称,st_buffer(geom::geography,%f)::geometry as geom 
		FROM %s;`, r, name)).Error
		if err != nil {
			log.Error(err)
			return 5001
		}
		return 200
	}

	err := db.Exec(fmt.Sprintf(`CREATE TABLE buffers AS 
						SELECT 机构号,名称,st_buffer(geom::geography,%f)::geometry as geom 
						FROM %s LIMIT 0;`, r, name)).Error
	if err != nil {
		log.Error(err)
		return 5001
	}
	field := cfgV.GetString("buffer.field")
	values := cfgV.GetStringSlice("buffer.values")
	scales := cfgV.GetStringSlice("buffer.scales")
	if len(scales) < len(values) {
		return 5005
	}
	for i, v := range values {
		scale, _ := strconv.ParseFloat(strings.TrimSpace(scales[i]), 32)
		if err != nil {
			log.Error(fmt.Errorf("could not parse %q to floats: %v", scales[i], err))
			return 5005
		}
		r = r * scale

		s := fmt.Sprintf(`INSERT INTO buffers 
						SELECT 机构号,名称,st_buffer(geom::geography,%f)::geometry as geom FROM %s
						WHERE %s='%s';`, r, name, field, v)

		err = db.Exec(s).Error
		if err != nil {
			log.Error(err)
			return 5001
		}
	}

	db.Exec(`DROP TABLE if EXISTS tmp_lines;`)
	err = db.Exec(`CREATE TABLE tmp_lines AS
	SELECT 机构号,名称,geom FROM 
	(SELECT a.机构号,a.名称,st_union(st_boundary(a.geom), st_union(b.geom)) as geom FROM 
	buffers as a, 
	block_lines as b 
	WHERE st_intersects(a.geom,b.geom) 
	GROUP BY a.机构号,a.名称,a.geom) as lines;`).Error
	if err != nil {
		log.Error(err)
		return 5001
	}

	db.Exec(`DROP TABLE if EXISTS tmp_polys;`)
	err = db.Exec(`CREATE TABLE tmp_polys AS
	SELECT polys.机构号, (st_dump(polys.geom)).geom FROM
	(SELECT 机构号,st_polygonize(geom) as geom FROM tmp_lines
	GROUP BY 机构号) as polys
	GROUP BY polys.机构号,polys.geom;`).Error
	if err != nil {
		log.Error(err)
		return 5001
	}
	db.Exec(`DROP TABLE if EXISTS buffers_block;`)
	err = db.Exec(`CREATE TABLE buffers_block AS
	SELECT a.机构号,a.名称,st_union(b.geom) as geom FROM banks a, tmp_polys b WHERE st_intersects(a.geom,b.geom) AND a.机构号=b.机构号
	GROUP BY a.机构号,a.名称;`).Error
	if err != nil {
		log.Error(err)
		return 5001
	}
	err = db.Exec(`INSERT INTO buffers_block (机构号,名称,geom)
	SELECT b.机构号,b.名称,b.geom FROM buffers as b
	WHERE NOT EXISTS (SELECT 机构号 FROM buffers_b WHERE 机构号=b.机构号 );`).Error
	if err != nil {
		log.Error(err)
		return 5001
	}
	return 200
}

func newFeatrue(geoType string) *geojson.Feature {
	var geometry orb.Geometry
	switch geoType {
	case "POINT":
		geometry = orb.Point{}
	case "MULTIPOINT":
		geometry = orb.MultiPoint{}
	case "LINESTRING":
		geometry = orb.LineString{}
	case "MULTILINESTRING":
		geometry = orb.MultiLineString{}
	case "POLYGON":
		geometry = orb.Polygon{}
	case "MULTIPOLYGON":
		geometry = orb.MultiPolygon{}
	default:
		return nil
	}
	return &geojson.Feature{
		Type:       "Feature",
		Geometry:   geometry,
		Properties: make(map[string]interface{}),
	}
	//test
	// var t string
	// s := fmt.Sprintf(`SELECT geometrytype(geom) FROM %s LIMIT 1;`, name)
	// err = db.Raw(s).Row().Scan(&t)
	// if err != nil {
	// 	log.Error(err)
	// 	res.Fail(c, 5001)
	// 	return
	// }
	// if newFeatrue(t) == nil {
	// 	log.Error("postgis 'geometrytype(geom)' return error")
	// 	res.Fail(c, 5001)
	// 	return
	// }
}
