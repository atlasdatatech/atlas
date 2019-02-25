package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fogleman/gg"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"

	"github.com/gin-gonic/gin"
	"github.com/teris-io/shortid"
)

//listStyles list user style
func publicStyles(c *gin.Context) {
	res := NewRes()
	iv, ok := pubSet.Load(ATLAS)
	if !ok {
		res.Fail(c, 4044)
		return
	}
	var styles []*StyleService
	iv.(*ServiceSet).S.Range(func(_, v interface{}) bool {
		s, ok := v.(*StyleService)
		if ok {
			styles = append(styles, s.Simplify())
		}
		return true
	})
	res.DoneData(c, styles)
}

//listStyles list user style
func listStyles(c *gin.Context) {
	res := NewRes()
	uid := c.GetString(identityKey)
	var styles []*StyleService
	is, ok := pubSet.Load(uid)
	if ok {
		is.(*ServiceSet).S.Range(func(_, v interface{}) bool {
			s, ok := v.(*StyleService)
			if ok {
				styles = append(styles, s.Simplify())
			}
			return true
		})
	}
	res.DoneData(c, styles)
}

//uploadStyle 上传新样式
func uploadStyle(c *gin.Context) {
	res := NewRes()
	uid := c.GetString(identityKey)
	// style source
	file, err := c.FormFile("file")
	if err != nil {
		log.Errorf(`uploadStyle, get form: %s; user: %s`, err, uid)
		res.Fail(c, 4046)
		return
	}
	ext := filepath.Ext(file.Filename)
	if ".zip" != strings.ToLower(ext) {
		log.Errorf(`uploadStyle, not .zip format: %s; user: %s`, err, uid)
		res.FailMsg(c, "格式错误,请上传zip压缩包格式")
		return
	}
	udir := filepath.Join("styles", uid)
	os.MkdirAll(udir, os.ModePerm)
	name := strings.TrimSuffix(file.Filename, ext)
	id, _ := shortid.Generate()
	fileid := name + "." + id
	dst := filepath.Join(udir, fileid+".zip")
	if err := c.SaveUploadedFile(file, dst); err != nil {
		log.Errorf(`uploadStyle, upload file: %s; user: %s`, err, id)
		res.Fail(c, 5002)
		return
	}
	//unzip upload files
	styledir := UnZipToDir(dst)
	//更新服务
	style, err := LoadStyle(styledir)
	if err != nil {
		log.Errorf("AddStyles, could not load style %s, details: %s", style.ID, err)
	}
	//入库
	go func() {
		err = style.UpInsert()
		if err != nil {
			log.Errorf(`AddStyles, upinsert style %s error, details: %s`, style.ID, err)
		}
	}()
	//加载服务,todo 用户服务无需预加载
	if true {
		ss := style.toService()
		is, ok := pubSet.Load(uid)
		if ok {
			is.(*ServiceSet).S.Store(ss.ID, ss)
		}
	}
	res.Done(c, "")
}

//downloadStyle 下载样式
func downloadStyle(c *gin.Context) {
	res := NewRes()
	uid := c.GetString(identityKey)
	is, ok := pubSet.Load(uid)
	if !ok {
		res.Fail(c, 4044)
		return
	}
	sid := c.Param("sid")
	iv, ok := is.(*ServiceSet).S.Load(sid)
	if !ok {
		res.Fail(c, 4044)
		return
	}
	reader := iv.(*StyleService).PackStyle()
	contentLength := reader.Len()
	contentType := "application/octet-stream"
	extraHeaders := map[string]string{
		"Content-Disposition": fmt.Sprintf(`attachment; filename="%s"`, iv.(*StyleService).ID),
	}
	c.DataFromReader(http.StatusOK, int64(contentLength), contentType, reader, extraHeaders)
}

//uploadStyle 上传新样式
func uploadIcons(c *gin.Context) {
	res := NewRes()
	uid := c.GetString(identityKey)
	sid := c.Param("sid")
	// Multipart form
	form, err := c.MultipartForm()
	if err != nil {
		log.Errorf(`uploadSprite, get form: %s; user: %s`, err, uid)
		res.Fail(c, 400)
		return
	}
	dir := filepath.Join("styles", uid, sid, "icons")
	files := form.File["files"]
	for _, file := range files {
		dst := filepath.Join(dir, file.Filename)
		if err := c.SaveUploadedFile(file, dst); err != nil {
			log.Errorf(`uploadSprite, upload file: %s; user: %s`, err, uid)
			res.Fail(c, 5002)
			return
		}
	}
	go updateSprite(c)
	res.Done(c, "")
}

//getSprite get sprite
func getIcon(c *gin.Context) {
	res := NewRes()
	uid := c.GetString(identityKey)
	is, ok := pubSet.Load(uid)
	if !ok {
		res.Fail(c, 4044)
		return
	}
	sid := c.Param("sid")
	iv, ok := is.(*ServiceSet).S.Load(sid)
	if !ok {
		log.Errorf("getSprite, style not exist in the service, sid: %s ^^", sid)
		res.Fail(c, 4044)
		return
	}
	name := c.Param("name")
	ext := filepath.Ext(name)
	c.Writer.Header().Set("Content-Type", "image/"+ext)
	pathfile := filepath.Join(iv.(*StyleService).Path, "icons", name)
	file, err := ioutil.ReadFile(pathfile)
	if err != nil {
		log.Errorf(`getSprite, read sprite file: %v; user: %s ^^`, err, uid)
		res.Fail(c, 5002)
		return
	}
	c.Writer.Write(file)
}

//upInsertStyle create a style
func deleteIcons(c *gin.Context) {
	res := NewRes()
	uid := c.GetString(identityKey)
	sid := c.Param("sid")
	dir := filepath.Join("styles", uid, sid, "icons")
	name := c.Param("name")
	names := strings.Split(name, ",")
	for _, name := range names {
		dst := filepath.Join(dir, name)
		os.Remove(dst)
	}
	go updateSprite(c)
	res.Done(c, "")
}

func updateSprite(c *gin.Context) {
	res := NewRes()
	uid := c.GetString(identityKey)
	sid := c.Param("sid")
	fmt := c.Param("fmt")
	sprite := "sprite" + fmt
	spritePat := `^sprite(@[234]x)?.(?:json|png)$`
	if ok, _ := regexp.MatchString(spritePat, sprite); !ok {
		log.Errorf(`getSprite, get sprite MatchString false, sprite : %s; user: %s ^^`, sprite, uid)
		res.Fail(c, 4004)
		return
	}
	var scale float32 = 1.0
	if strings.HasPrefix(fmt, "@") {
		pos := strings.Index(fmt, "x.")
		s, err := strconv.ParseInt(fmt[1:pos], 10, 32)
		if err != nil {
			scale = float32(s)
		}
	}
	dir := filepath.Join("styles", uid, sid, "icons")
	symbols := ReadIcons(dir, scale) //readIcons(dir, 1)
	sort.Slice(symbols, func(i, j int) bool {
		if symbols[j].Height == symbols[i].Height {
			return symbols[i].ID < symbols[j].ID
		}
		return symbols[j].Height < symbols[i].Height
	})

	sprites := NewShelfPack(1, 1, ShelfPackOptions{autoResize: true})
	var bins []*Bin
	for _, s := range symbols {
		bin := NewBin(s.ID, s.Width, s.Height, -1, -1, -1, -1)
		bins = append(bins, bin)
	}

	results := sprites.Pack(bins, PackOptions{})

	for _, bin := range results {
		for i := range symbols {
			if bin.id == symbols[i].ID {
				symbols[i].X = bin.x
				symbols[i].Y = bin.y
				break
			}
		}
	}
	layout := make(map[string]*Symbol)
	dc := gg.NewContext(sprites.width, sprites.height)
	dc.SetRGBA(0, 0, 0, 0.1)
	for _, s := range symbols {
		dc.DrawImage(s.Image, s.X, s.Y)
		layout[s.Name] = s
	}
	name := strings.TrimSuffix(sprite, filepath.Ext(sprite))
	pathname := filepath.Join("styles", uid, sid, name)
	err := dc.SavePNG(pathname + ".png")
	if err != nil {
		log.Error("save png file error")
	}
	jsonbuf, err := json.Marshal(layout)
	if err != nil {
		log.Error("marshal json error")
		return
	}
	err = ioutil.WriteFile(pathname+".json", jsonbuf, os.ModePerm)
	if err != nil {
		log.Error("save json file error")
	}
	res.Done(c, "")
}

//getSprite get sprite
func getSprite(c *gin.Context) {
	res := NewRes()
	uid := c.GetString(identityKey)
	sid := c.Param("sid")
	fmt := c.Param("fmt")
	sprite := "sprite" + fmt
	spritePat := `^sprite(@[234]x)?.(?:json|png)$`
	if ok, _ := regexp.MatchString(spritePat, sprite); !ok {
		log.Errorf(`getSprite, get sprite MatchString false, sprite : %s; user: %s ^^`, sprite, uid)
		res.Fail(c, 4004)
		return
	}
	pathfile := filepath.Join("styles", uid, sid, sprite)
	_, err := os.Stat(pathfile)
	if err != nil {
		if os.IsNotExist(err) {
			updateSprite(c)
		} else {
			return
		}
	}

	file, err := ioutil.ReadFile(pathfile)
	if err != nil {
		log.Errorf(`getSprite, read sprite file: %v; user: %s ^^`, err, uid)
		res.Fail(c, 5002)
		return
	}

	if strings.HasSuffix(strings.ToLower(sprite), ".json") {
		c.Writer.Header().Set("Content-Type", "application/json")
	}
	if strings.HasSuffix(strings.ToLower(sprite), ".png") {
		c.Writer.Header().Set("Content-Type", "image/png")
	}

	c.Writer.Write(file)
}

//upInsertStyle create a style
func updateStyle(c *gin.Context) {
	res := NewRes()
	uid := c.GetString(identityKey)
	is, ok := pubSet.Load(uid)
	if !ok {
		res.Fail(c, 4044)
		return
	}
	user := c.Param("user")
	sid := c.Param("sid")
	body, err := ioutil.ReadAll(c.Request.Body)
	if err != nil {
		log.Errorf(`updateStyle, get form: %s; user: %s`, err, uid)
		res.Fail(c, 5003)
		return
	}
	home := viper.GetString("users.home")
	styles := viper.GetString("users.styles")
	dst := filepath.Join(home, user, styles, sid, "style.json")
	out := make(map[string]interface{})
	json.Unmarshal(body, &out)
	out["id"] = sid
	out["modified"] = time.Now().Format("2006-01-02 03:04:05 PM")
	out["owner"] = uid
	file, _ := json.Marshal(out)
	err = ioutil.WriteFile(dst, file, os.ModePerm)
	if err != nil {
		log.Error(err)
		res.Fail(c, 5002)
		return
	}

	style, ok := is.(*ServiceSet).S.Load(sid)
	if !ok {
		log.Errorf("style saved, but id(%s) not exist in the service", sid)
		res.Fail(c, 4044)
		return
	}
	ss := style.(*Style)
	ss.Data = body
	res.Done(c, "")
}

//getStyle get user style by id
func getStyle(c *gin.Context) {
	res := NewRes()
	uid := c.GetString(identityKey)
	is, ok := pubSet.Load(uid)
	if !ok {
		res.Fail(c, 4044)
		return
	}
	sid := c.Param("sid")
	style, ok := is.(*ServiceSet).S.Load(sid)
	if !ok {
		log.Errorf("getStyle, style not exist in the service, sid: %s ^^", sid)
		res.Fail(c, 4044)
		return
	}

	switch c.Query("option") {
	case "download":
		downloadStyle(c)
		return
	}

	var out map[string]interface{}
	json.Unmarshal(style.(*Style).Data, &out)

	protoScheme := scheme(c.Request)
	fixURL := func(url string) string {
		if "" == url || !strings.HasPrefix(url, "atlas://") {
			return url
		}
		return strings.Replace(url, "atlas://", protoScheme+"://"+c.Request.Host+"/", -1)
	}

	for k, v := range out {
		switch v.(type) {
		case string:
			//style->sprite
			if "sprite" == k && v != nil {
				path := v.(string)
				out["sprite"] = fixURL(path)
			}
			//style->glyphs
			if "glyphs" == k && v != nil {
				path := v.(string)
				out["glyphs"] = fixURL(path)
			}
		case map[string]interface{}:
			if "sources" == k {
				//style->sources
				sources := v.(map[string]interface{})
				for _, u := range sources {
					source := u.(map[string]interface{})
					if url := source["url"]; url != nil {
						source["url"] = fixURL(url.(string))
					}
				}
			}
		default:
		}
	}
	c.JSON(http.StatusOK, &out)
}

//viewStyle load style map
func viewStyle(c *gin.Context) {
	res := NewRes()
	uid := c.GetString(identityKey)
	is, ok := pubSet.Load(uid)
	if !ok {
		res.Fail(c, 4044)
		return
	}
	sid := c.Param("sid")
	_, ok = is.(*ServiceSet).S.Load(sid)
	if !ok {
		log.Errorf("viewStyle, style not exist in the service, sid: %s ^^", sid)
		res.Fail(c, 4044)
		return
	}
	c.HTML(http.StatusOK, "viewer.html", gin.H{
		"Title": "Viewer",
		"ID":    sid,
		"URL":   strings.TrimSuffix(c.Request.URL.Path, "/"),
	})
}
