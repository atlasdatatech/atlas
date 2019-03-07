package main

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html/template"
	"image/jpeg"
	"image/png"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/axgle/mahonia"
	"github.com/gin-gonic/gin"
	"github.com/jinzhu/gorm"
	"github.com/nfnt/resize"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	gomail "gopkg.in/gomail.v2"
)

var codes = map[int]string{
	0: "检查消息",

	200: "成功",
	201: "已创建",
	202: "已接受",
	204: "无内容",

	400:  "请求无法解析",
	4001: "必填参数校验错误",
	4002: "达到最大尝试登录次数,稍后再试",
	4003: "瓦片请求格式错误",
	4004: "符号请求格式错误",
	4005: "字体请求格式错误",

	401:  "未授权",
	4011: "用户名或密码错误",
	4012: "用户名非法,请使用字母,数字,短划线,下划线组合或用户名需少于32个字符",
	4013: "邮箱非法,请使用能收到验证邮件的正确邮箱",
	4014: "密码非法,请使用至少4位以上密码字符",
	4015: "用户名已注册,请使用新的用户名",
	4016: "邮箱已注册,请使用新的邮箱",

	403:  "禁止访问",
	4031: "邮箱不存在",

	404:  "找不到资源",
	4041: "用户不存在",
	4042: "角色不存在",
	4043: "服务不存在",
	4044: "找不到样式",
	4045: "找不到瓦片集",
	4046: "找不到数据集",
	4047: "找不到字体库",
	4048: "找不到上传文件",
	4049: "地图不存在",

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

// scheme returns the underlying URL scheme of the original request.
func scheme(r *http.Request) string {

	if r.TLS != nil {
		return "https"
	}
	if scheme := r.Header.Get("X-Forwarded-Proto"); scheme != "" {
		return scheme
	}
	if scheme := r.Header.Get("X-Forwarded-Protocol"); scheme != "" {
		return scheme
	}
	if ssl := r.Header.Get("X-Forwarded-Ssl"); ssl == "on" {
		return "https"
	}
	if scheme := r.Header.Get("X-Url-Scheme"); scheme != "" {
		return scheme
	}
	return "http"
}

// rootURL returns the root URL of the service. If s.Domain is non-empty, it
// will be used as the hostname. If s.Path is non-empty, it will be used as a
// prefix.
func rootURL(r *http.Request) string {
	return fmt.Sprintf("%s://%s", scheme(r), r.Host)
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

	d := gomail.NewDialer(viper.GetString("smtp.credentials.host"), viper.GetInt("smtp.credentials.port"), viper.GetString("smtp.credentials.user"), viper.GetString("smtp.credentials.password"))
	return d.DialAndSend(m)
}

func getEscapedString(str string) string {
	return strings.Replace(url.QueryEscape(str), "+", "%20", -1)
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

//CreatePaths 创建用户目录
func CreatePaths(name string) {
	os.MkdirAll(filepath.Join("styles", name), os.ModePerm)
	os.MkdirAll(filepath.Join("tilesets", name), os.ModePerm)
	os.MkdirAll(filepath.Join("datasets", name), os.ModePerm)
	os.MkdirAll(filepath.Join("fonts", name), os.ModePerm)
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
		return 4049
	}
	return 200
}

func checkDataset(did string) int {
	if did == "" {
		return 4001
	}
	if !db.HasTable(did) {
		return 4046
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

//Thumbnail 缩略图
func Thumbnail(width, height uint, b64img string) string {
	if b64img == "" {
		return ""
	}
	pos := strings.Index(b64img, ";base64,")
	imgbuf, err := base64.StdEncoding.DecodeString(b64img[pos+8:])
	if err != nil {
		log.Error(err)
		return ""
	}
	switch b64img[5:pos] {
	case "image/png":
		pngI, err := png.Decode(bytes.NewReader(imgbuf))
		if err != nil {
			log.Error(err)
			return ""
		}
		thumbnail := resize.Thumbnail(width, height, pngI, resize.Lanczos3)
		data := new(bytes.Buffer)
		err = png.Encode(data, thumbnail)
		if err != nil {
			log.Error(err)
			return ""
		}
		src := base64.StdEncoding.EncodeToString(data.Bytes())
		return "data:image/png;base64," + src
	case "image/jpeg":
		jpgI, err := jpeg.Decode(bytes.NewReader(imgbuf))
		if err != nil {
			log.Error(err)
			return ""
		}
		thumbnail := resize.Thumbnail(width, height, jpgI, resize.Lanczos3)
		data := new(bytes.Buffer)
		err = jpeg.Encode(data, thumbnail, &jpeg.Options{
			Quality: 80,
		})
		if err != nil {
			log.Error(err)
			return ""
		}
		src := base64.StdEncoding.EncodeToString(data.Bytes())
		return "data:image/jpeg;base64," + src
	}
	return ""
}

//ConvertToByte 编码转换
func ConvertToByte(src string, srcCode string, targetCode string) []byte {
	srcCoder := mahonia.NewDecoder(srcCode)
	srcResult := srcCoder.ConvertString(src)
	tagCoder := mahonia.NewDecoder(targetCode)
	_, cdata, _ := tagCoder.Translate([]byte(srcResult), true)
	return cdata
}

// FileCopy copies a single file from src to dst
func FileCopy(src, dst string) error {
	var err error
	var srcfd *os.File
	var dstfd *os.File
	var srcinfo os.FileInfo

	if srcfd, err = os.Open(src); err != nil {
		return err
	}
	defer srcfd.Close()

	if dstfd, err = os.Create(dst); err != nil {
		return err
	}
	defer dstfd.Close()

	if _, err = io.Copy(dstfd, srcfd); err != nil {
		return err
	}
	if srcinfo, err = os.Stat(src); err != nil {
		return err
	}
	return os.Chmod(dst, srcinfo.Mode())
}

// DirCopy copies a whole directory recursively
func DirCopy(src string, dst string) error {
	var err error
	var fds []os.FileInfo
	var srcinfo os.FileInfo

	if srcinfo, err = os.Stat(src); err != nil {
		return err
	}

	if err = os.MkdirAll(dst, srcinfo.Mode()); err != nil {
		return err
	}

	if fds, err = ioutil.ReadDir(src); err != nil {
		return err
	}
	for _, fd := range fds {
		srcfp := path.Join(src, fd.Name())
		dstfp := path.Join(dst, fd.Name())

		if fd.IsDir() {
			if err = FileCopy(srcfp, dstfp); err != nil {
				fmt.Println(err)
			}
		} else {
			if err = FileCopy(srcfp, dstfp); err != nil {
				fmt.Println(err)
			}
		}
	}
	return nil
}

//UnZipToDir 解压文件
func UnZipToDir(zipfile string) string {
	ext := filepath.Ext(zipfile)
	dir := strings.TrimSuffix(zipfile, ext)
	err := os.Mkdir(dir, os.ModePerm)
	if err != nil {
		log.Error(err)
	}
	zr, err := zip.OpenReader(zipfile)
	if err != nil {
		log.Error(err)
	}
	defer zr.Close()
	decoder := mahonia.NewDecoder("gbk")

	for _, f := range zr.File {
		name := decoder.ConvertString(f.Name)
		info := f.FileInfo()
		pn := filepath.Join(dir, name)
		log.Infof("Uncompress: %s -> %s", name, pn)
		if info.IsDir() {
			err := os.Mkdir(pn, os.ModePerm)
			if err != nil {
				log.Warnf("unzip %s: %v", zipfile, err)
			}
			continue
		}

		w, err := os.Create(pn)
		if err != nil {
			log.Warnf("Cannot unzip %s: %v", zipfile, err)
			continue
		}
		defer w.Close()
		r, err := f.Open()
		if err != nil {
			log.Warnf("Cannot unzip %s: %v", zipfile, err)
			continue
		}
		defer r.Close()
		_, err = io.Copy(w, r)
		if err != nil {
			log.Warnf("Cannot unzip %s: %v", zipfile, err)
		}
	}
	return dir
}
