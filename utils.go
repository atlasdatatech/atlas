package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/axgle/mahonia"
	"github.com/gin-gonic/gin"
	"github.com/jinzhu/gorm"
	"github.com/nfnt/resize"
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

	d := gomail.NewDialer(cfgV.GetString("smtp.credentials.host"), cfgV.GetInt("smtp.credentials.port"), cfgV.GetString("smtp.credentials.user"), cfgV.GetString("smtp.credentials.password"))
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
	if !db.HasTable(did) {
		return 4045
	}
	return 200
}

func updateDatasetInfo(did string) error {
	s := fmt.Sprintf(`SELECT * FROM %s LIMIT 0;`, did)
	rows, err := db.Raw(s).Rows() // (*sql.Rows, error)
	if err != nil {
		return err
	}
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil {
		return err
	}
	var fields []Field
	for _, col := range cols {
		var t string
		switch col.DatabaseTypeName() {
		case "INT", "INT4":
			t = TypeInteger
		case "NUMERIC": //number
			t = TypeReal
		case "BOOL":
			t = TypeBool
		case "TIMESTAMPTZ":
			t = TypeDate
		case "_VARCHAR":
			t = TypeStringArray
		case "TEXT", "VARCHAR":
			t = TypeString
		default:
			t = TypeUnkown
		}
		field := Field{
			Name:   col.Name(),
			Type:   t,
			Format: "",
		}
		fields = append(fields, field)
	}

	jfs, err := json.Marshal(fields)
	if err != nil {
		return err
	}

	ds := &Dataset{
		ID:     did,
		Name:   did,
		Label:  did,
		Type:   TypePolygon,
		Fields: jfs,
	}
	//更新元数据
	err = pubSet.updateInsertDataset(ds)
	if err != nil {
		return err
	}
	//更新服务
	err = pubSet.AddDatasetService(ds)
	if err != nil {
		return err
	}
	return nil
}

func buffering(name string, r float64) int {
	bblocks := cfgV.GetString("buffers.blocks")
	bprefix := cfgV.GetString("buffers.prefix")
	bsuffix := cfgV.GetString("buffers.suffix")
	bscales := cfgV.GetString("buffers.scales")
	btype := cfgV.GetString("buffers.scaleType")
	bname := name + bsuffix
	fname := bprefix + bname

	db.Exec(fmt.Sprintf(`DROP TABLE if EXISTS %s;`, bname))

	if name != "banks" {
		st := fmt.Sprintf(`CREATE TABLE %s AS 
		SELECT id,st_buffer(geom::geography,%f)::geometry as geom FROM %s;`, bname, r, name)
		err := db.Exec(st).Error
		if err != nil {
			log.Error(err)
			return 5001
		}
		updateDatasetInfo(bname)
		return 200
	}

	err := db.Exec(fmt.Sprintf(`CREATE TABLE %s AS 
	SELECT a.id,st_buffer(a.geom::geography,b.scale*%f)::geometry as geom FROM %s a, %s b
	WHERE a."%s"=b.type
	GROUP BY a.id,a.geom,b.scale;`, bname, r, name, bscales, btype)).Error
	if err != nil {
		log.Error(err)
		return 5001
	}
	updateDatasetInfo(bname)

	db.Exec(`DROP TABLE if EXISTS tmp_lines;`)
	err = db.Exec(fmt.Sprintf(`CREATE TABLE tmp_lines AS
	SELECT id,geom FROM 
	(SELECT a.id,st_union(st_boundary(a.geom), st_union(b.geom)) as geom FROM 
	%s as a, 
	%s as b 
	WHERE st_intersects(a.geom,b.geom) 
	GROUP BY a.id,a.geom) as lines;`, bname, bblocks)).Error
	if err != nil {
		log.Error(err)
		return 5001
	}

	db.Exec(`DROP TABLE if EXISTS tmp_polys;`)
	err = db.Exec(`CREATE TABLE tmp_polys AS
	SELECT polys.id, (st_dump(polys.geom)).geom FROM
	(SELECT id,st_polygonize(geom) as geom FROM tmp_lines
	GROUP BY id) as polys
	GROUP BY polys.id,polys.geom;`).Error
	if err != nil {
		log.Error(err)
		return 5001
	}

	db.Exec(fmt.Sprintf(`DROP TABLE if EXISTS %s;`, fname))
	err = db.Exec(fmt.Sprintf(`CREATE TABLE %s AS
	SELECT a.id, st_union(b.geom) as geom FROM %s a, tmp_polys b WHERE st_intersects(a.geom,b.geom) AND a.id=b.id
	GROUP BY a.id;`, fname, name)).Error
	if err != nil {
		log.Error(err)
		return 5001
	}
	err = db.Exec(fmt.Sprintf(`INSERT INTO %s (id,geom)
	SELECT b.id,b.geom FROM %s as b
	WHERE NOT EXISTS (SELECT id FROM %s WHERE id=b.id );`, fname, bname, fname)).Error
	if err != nil {
		log.Error(err)
		return 5001
	}
	updateDatasetInfo(fname)
	return 200
}

func calcM1() error {
	st := fmt.Sprintf(`UPDATE m1 as a SET "名称"=b."名称" FROM (SELECT 机构号,名称 FROM banks) as b WHERE a."机构号"=b."机构号";`)
	query := db.Exec(st)
	if query.Error != nil {
		log.Error(query.Error)
		// return query.Error
	}
	updateDatasetInfo("m1")
	return nil
}

func calcM2() error {
	weights := cfgV.GetString("models.m2.weights")
	cvar := cfgV.GetString("models.m2.const")
	st := fmt.Sprintf(`SELECT "field", "weight" FROM  %s`, weights)
	rows, err := db.Raw(st).Rows() // (*sql.Rows, error)
	if err != nil {
		return err
	}
	defer rows.Close()

	var f string
	var w float64
	fws := make(map[string]string)
	for rows.Next() {
		err = rows.Scan(&f, &w)
		if err != nil {
			log.Error(err)
		}
		fws[f] = strconv.FormatFloat(w, 'E', -1, 64)
	}
	var cacls []string
	//cacl fields scale
	for f, w := range fws {
		cacls = append(cacls, fmt.Sprintf(`COALESCE(%s, 0)*%s`, f, w))
	}

	cacls = append(cacls, cvar) //add const value
	st = fmt.Sprintf(`UPDATE m2 SET "总得分"=(%s);`, strings.Join(cacls, "+"))
	query := db.Exec(st)
	if query.Error != nil {
		return query.Error
	}

	st = fmt.Sprintf(`UPDATE m2 SET "总得分"=99 WHERE "总得分">99;`)
	query = db.Exec(st)
	if query.Error != nil {
		return query.Error
	}

	st = fmt.Sprintf(`UPDATE m2 as a SET "名称"=b."名称" FROM (SELECT 机构号,名称 FROM banks) as b WHERE a."机构号"=b."机构号";`)
	query = db.Exec(st)
	if query.Error != nil {
		log.Error(query.Error)
		// return query.Error
	}
	updateDatasetInfo("m2")
	return nil
}

func calcM3() error {
	bprefix := cfgV.GetString("buffers.prefix")
	bsuffix := cfgV.GetString("buffers.suffix")
	bname := "banks" + bsuffix
	fname := bprefix + bname
	var tcnt int
	db.Raw(`SELECT count(*) FROM pois;`).Row().Scan(&tcnt)
	fcnt := float32(tcnt) // / 100.0

	if !db.HasTable(fname) {
		radius := cfgV.GetFloat64("buffers.radius")
		if code := buffering("banks", radius); code != 200 {
			return fmt.Errorf("buffering error")
		}
	}

	st := fmt.Sprintf(`DROP TABLE IF EXISTS m3_tmp1;
	CREATE TABLE m3_tmp1 AS
	SELECT b.id, count(a.id)/%f as res FROM pois a,%s b WHERE a."类型" in ('1','11') AND st_contains(b.geom,a.geom)
	GROUP BY b.id;
	DROP TABLE IF EXISTS m3_tmp2;
	CREATE TABLE m3_tmp2 AS
	SELECT b.id, count(a.id)/%f as res FROM pois a,%s b WHERE a."类型" in ('2','22') AND st_contains(b.geom,a.geom)
	GROUP BY b.id;
	DROP TABLE IF EXISTS m3_tmp3;
	CREATE TABLE m3_tmp3 AS
	SELECT b.id, count(a.id)/%f as res FROM pois a,%s b WHERE a."类型" in ('3','33') AND st_contains(b.geom,a.geom)
	GROUP BY b.id;

	TRUNCATE TABLE m3;

	INSERT INTO m3(id,"商业资源")
	SELECT id, res FROM m3_tmp1;
	
	UPDATE m3
	SET "对公资源"=s.res
	FROM (SELECT id, res FROM m3_tmp2) AS s
	WHERE m3.id=s.id;
	
	INSERT INTO m3 (id,"对公资源")
	SELECT id, res FROM m3_tmp2 AS s
	WHERE NOT EXISTS (SELECT m3.id FROM m3 WHERE m3.id=s.id );
		
	UPDATE m3
	SET "零售资源"=s.res
	FROM (SELECT id, res FROM m3_tmp3) AS s
	WHERE m3.id=s.id;
	
	INSERT INTO m3 (id,"零售资源")
	SELECT id, res FROM m3_tmp3 AS s
	WHERE NOT EXISTS (SELECT m3.id FROM m3 WHERE m3.id=s.id );
	
	UPDATE m3 SET "总得分"=100*(COALESCE(零售资源, 0)+COALESCE(对公资源, 0)+COALESCE(商业资源, 0));`, fcnt, fname, fcnt, fname, fcnt, fname)
	query := db.Exec(st)
	if query.Error != nil {
		return query.Error
	}
	st = fmt.Sprintf(`UPDATE m3 as a SET "机构号"=b."机构号", "名称"=b."名称" FROM (SELECT id,机构号,名称 FROM banks) as b WHERE a.id=b.id;`)
	query = db.Exec(st)
	if query.Error != nil {
		log.Error(query.Error)
		// return query.Error
	}
	updateDatasetInfo("m3")
	return nil
}

func calcM4() error {
	bprefix := cfgV.GetString("buffers.prefix")
	bsuffix := cfgV.GetString("buffers.suffix")
	bname := "banks" + bsuffix
	fname := bprefix + bname

	if !db.HasTable(fname) {
		radius := cfgV.GetFloat64("buffers.radius")
		if code := buffering("banks", radius); code != 200 {
			return fmt.Errorf("buffering error")
		}
	}

	weights := cfgV.GetString("models.m4.weights")

	scales := cfgV.GetString("models.m4.scales")

	db.Exec(`TRUNCATE TABLE m4;`)

	st := fmt.Sprintf(`INSERT INTO m4(id,"总得分")
	SELECT f.id,sum(weight*g.scale*cnt) FROM 
		(SELECT d.id,d.type,d.cnt,e.weight FROM
			(SELECT id, "银行类别" as name,"网点类型" as type,COUNT(*) as cnt FROM  
				(SELECT b.id,a."银行类别",a."网点类型" FROM others a,%s b 
				WHERE st_contains(b.geom,a.geom) ) c
			GROUP BY c.id, c."银行类别",c."网点类型" ORDER BY c.id, c."银行类别",c."网点类型") d, %s e 
		WHERE d.name=e."type") f,%s g
	WHERE f.type=g.type
	GROUP BY f.id;`, fname, weights, scales)
	query := db.Exec(st)
	if query.Error != nil {
		return query.Error
	}
	st = fmt.Sprintf(`UPDATE m4 as a SET "机构号"=b."机构号","名称"=b."名称" FROM (SELECT id,机构号,名称 FROM banks) as b WHERE a.id=b.id;`)
	query = db.Exec(st)
	if query.Error != nil {
		log.Error(query.Error)
		// return query.Error
	}
	updateDatasetInfo("m4")
	return nil
}

func calcM5() error {
	field := cfgV.GetString("models.m5.field")
	values := cfgV.GetStringSlice("models.m5.values")
	instr := strings.Join(values, "','")
	var bcnt, ocnt int
	db.Raw(`SELECT count(*) FROM banks;`).Row().Scan(&bcnt)
	db.Raw(fmt.Sprintf(`SELECT count(*) FROM others WHERE "%s" in ('%s');`, field, instr)).Row().Scan(&ocnt)
	fbcnt := float32(bcnt)
	focnt := float32(ocnt)

	st := fmt.Sprintf(`UPDATE m5 SET "总得分"=r.result
	FROM
		(SELECT t1."名称",t1.s-t2.s as result FROM
			(SELECT b."名称", count(a."id")/%f as s FROM banks a,regions b WHERE st_contains(b.geom,a.geom)
				GROUP BY b."名称") as t1,
			(SELECT b."名称", count(a."id")/%f as s FROM others a,regions b WHERE st_contains(b.geom,a.geom) AND a."%s" in ('%s')
				GROUP BY b."名称") as t2 
		WHERE t1."名称"=t2."名称"
		GROUP BY t1."名称",t1.s,t2.s) as r
	WHERE r."名称"=m5."名称";`, fbcnt, focnt, field, instr)
	query := db.Exec(st)
	if query.Error != nil {
		return query.Error
	}
	updateDatasetInfo("m5")
	return nil
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
