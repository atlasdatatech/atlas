package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/nfnt/resize"
	log "github.com/sirupsen/logrus"

	"github.com/gin-gonic/gin"
	"github.com/teris-io/shortid"
)

//listStyles list user's style
func listStyles(c *gin.Context) {
	res := NewRes()
	uid := c.Param("user")
	set := userSet.service(uid)
	if set == nil {
		res.Fail(c, 4043)
		return
	}
	var styles []*Style
	set.S.Range(func(_, v interface{}) bool {
		s, ok := v.(*Style)
		if ok {
			styles = append(styles, s)
		}
		return true
	})
	if uid != ATLAS && "true" == c.Query("public") {
		set := userSet.service(ATLAS)
		if set != nil {
			set.S.Range(func(_, v interface{}) bool {
				s, ok := v.(*Style)
				if ok {
					if s.Public {
						styles = append(styles, s)
					}
				}
				return true
			})
		}
	}
	res.DoneData(c, styles)
}

//getStyleInfo get user's style info by id
func getStyleInfo(c *gin.Context) {
	res := NewRes()
	// uid := c.GetString(identityKey)
	uid := c.Param("user")
	sid := c.Param("id")
	style := userSet.style(uid, sid)
	if style == nil {
		log.Errorf("getStyle, style not exist in the service, sid: %s ^^", sid)
		res.Fail(c, 4044)
		return
	}
	res.DoneData(c, style)
}

//getStyleThumbnial get user's style thumbnail by id
func getStyleThumbnial(c *gin.Context) {
	res := NewRes()
	// uid := c.GetString(identityKey)
	uid := c.Param("user")
	sid := c.Param("id")
	s := userSet.style(uid, sid)
	if s == nil {
		log.Errorf("getStyle, style not exist in the service, sid: %s ^^", sid)
		res.Fail(c, 4044)
		return
	}
	file := filepath.Join(s.Path, "thumbnail.jpg")
	buf, err := ioutil.ReadFile(file)
	if err != nil {
		log.Errorf(`getStyleThumbnial, read %s's style (%s) thumbnail.jpg error, details: %s`, uid, sid, err)
		res.FailErr(c, err)
		return
	}
	res.DoneData(c, buf)
}

//publicStyle 分享样式
func publicStyle(c *gin.Context) {
	res := NewRes()
	// uid := c.GetString(identityKey)
	uid := c.Param("user")
	sid := c.Param("id")
	style := userSet.style(uid, sid)
	if style == nil {
		log.Errorf(`publicStyle, %s's style service (%s) not found ^^`, uid, sid)
		res.Fail(c, 4044)
		return
	}
	if style.Public {
		res.FailMsg(c, "already public")
		return
	}

	//添加管理员组的用户管理权限
	casEnf.AddPolicy(USER, fmt.Sprintf("/styles/%s/x/%s/", uid, sid), "GET")
	casEnf.AddPolicy(USER, fmt.Sprintf("/styles/%s/sprite/%s/*", uid, sid), "GET")
	casEnf.AddPolicy(USER, fmt.Sprintf("/tilesets/%s/x/*", uid), "GET")
	casEnf.AddPolicy(USER, fmt.Sprintf("/datasets/%s/x/*", uid), "GET")
	style.Public = true
	err := db.Model(&Style{}).Where("id = ?", style.ID).Update(Style{Public: true}).Error
	if err != nil {
		log.Errorf(`update style db error, details: %s`, err)
		res.Fail(c, 5001)
		return
	}
	res.DoneData(c, "")
}

//privateStyle 关闭分享样式
func privateStyle(c *gin.Context) {
	res := NewRes()
	// uid := c.GetString(identityKey)
	uid := c.Param("user")
	sid := c.Param("id")
	style := userSet.style(uid, sid)
	if style == nil {
		log.Errorf(`publicStyle, %s's style service (%s) not found ^^`, uid, sid)
		res.Fail(c, 4044)
		return
	}
	if !style.Public {
		res.FailMsg(c, "already private")
		return
	}

	//添加管理员组的用户管理权限
	casEnf.RemovePolicy(USER, fmt.Sprintf("/styles/%s/x/%s/", uid, sid), "GET")
	casEnf.RemovePolicy(USER, fmt.Sprintf("/styles/%s/sprite/%s/*", uid, sid), "GET")
	// casEnf.RemovePolicy(USER, fmt.Sprintf("/tilesets/%s/x/*", uid), "GET")
	// casEnf.RemovePolicy(USER, fmt.Sprintf("/datasets/%s/x/*", uid), "GET")
	style.Public = false
	err := db.Model(&Style{}).Where("id = ?", style.ID).Update(Style{Public: false}).Error
	if err != nil {
		log.Errorf(`update style db error, details: %s`, err)
		res.Fail(c, 5001)
		return
	}
	res.DoneData(c, "")
}

//uploadStyle 上传新样式
func uploadStyle(c *gin.Context) {
	res := NewRes()
	// uid := c.GetString(identityKey)
	uid := c.Param("user")
	// style source
	file, err := c.FormFile("file")
	if err != nil {
		log.Errorf(`uploadStyle, get form: %s; user: %s`, err, uid)
		res.Fail(c, 4048)
		return
	}
	ext := filepath.Ext(file.Filename)
	if ".zip" != strings.ToLower(ext) {
		log.Errorf(`uploadStyle, style format error, details: %s; user: %s`, file.Filename, uid)
		res.FailMsg(c, "上传格式错误,请上传zip压缩包格式")
		return
	}
	name := strings.TrimSuffix(file.Filename, ext)
	id, _ := shortid.Generate()
	styleid := name + "." + id
	dst := filepath.Join("styles", uid, styleid+".zip")
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
		log.Errorf("AddStyles, could not load style %s, details: %s", styledir, err)
		res.FailErr(c, err)
		return
	}
	style.Owner = uid
	//入库
	err = style.UpInsert()
	if err != nil {
		log.Errorf(`AddStyles, upinsert style %s error, details: %s`, style.ID, err)
	}
	//加载服务,todo 用户服务无需预加载
	if true {
		set := userSet.service(uid)
		if set == nil {
			log.Errorf("%s's service set not found", uid)
			res.FailMsg(c, "加载服务失败")
			return
		}
		set.S.Store(style.ID, style)
	}

	res.DoneData(c, gin.H{
		"id": style.ID,
	})
}

//uploadStyle 上传新样式
func replaceStyle(c *gin.Context) {
	res := NewRes()
	// uid := c.GetString(identityKey)
	uid := c.Param("user")
	sid := c.Param("id")
	set := userSet.service(uid)
	if set == nil {
		log.Errorf(`replaceStyle, %s's service set not found ^^`, uid)
		res.Fail(c, 4043)
		return
	}
	// style source
	file, err := c.FormFile("file")
	if err != nil {
		log.Errorf(`replaceStyle, get form: %s; user: %s`, err, uid)
		res.Fail(c, 4048)
		return
	}
	ext := filepath.Ext(file.Filename)
	if ".zip" != strings.ToLower(ext) {
		log.Errorf(`replaceStyle, style format error, details: %s; user: %s`, file.Filename, uid)
		res.FailMsg(c, "上传格式错误,请上传zip压缩包格式")
		return
	}
	dst := filepath.Join("styles", uid, sid+".zip")
	if err := c.SaveUploadedFile(file, dst); err != nil {
		log.Errorf(`replaceStyle, upload file: %s; user: %s`, err, uid)
		res.Fail(c, 5002)
		return
	}
	//unzip upload files
	styledir := UnZipToDir(dst)
	//更新服务
	style, err := LoadStyle(styledir)
	if err != nil {
		log.Errorf("replaceStyle, could not load style %s, details: %s", styledir, err)
		res.FailErr(c, err)
		return
	}
	style.Owner = uid
	//入库
	err = style.UpInsert()
	if err != nil {
		log.Errorf(`AddStyles, upinsert style %s error, details: %s`, style.ID, err)
	}
	//加载服务,todo 用户服务无需预加载
	set.S.Store(style.ID, style)
	res.Done(c, "")
}

//downloadStyle 下载样式
func downloadStyle(c *gin.Context) {
	res := NewRes()
	// uid := c.GetString(identityKey)
	uid := c.Param("user")
	sid := c.Param("id")
	style := userSet.style(uid, sid)
	if style == nil {
		log.Errorf(`downloadStyle, %s's style service (%s) not found ^^`, uid, sid)
		res.Fail(c, 4044)
		return
	}
	reader := style.PackStyle()
	c.Header("Content-type", "application/octet-stream")
	c.Header("Content-Disposition", "attachment; filename= "+style.ID+".zip")
	io.Copy(c.Writer, reader)
	return
}

//uploadStyle icons上传
func uploadIcons(c *gin.Context) {
	res := NewRes()
	// uid := c.GetString(identityKey)
	uid := c.Param("user")
	sid := c.Param("id")
	style := userSet.style(uid, sid)
	if style == nil {
		log.Errorf(`uploadIcons, %s's style service (%s) not found ^^`, uid, sid)
		res.Fail(c, 4044)
		return
	}
	// Multipart form
	form, err := c.MultipartForm()
	if err != nil {
		log.Errorf(`uploadIcons, get form: %s; user: %s`, err, uid)
		res.Fail(c, 400)
		return
	}
	dir := filepath.Join(style.Path, "icons")
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		err := os.MkdirAll(dir, os.ModePerm)
		if err != nil {
			log.Errorf(`uploadIcons, make %s's icons dir(%s) error, details: %s`, uid, dir, err)
			res.Fail(c, 5002)
			return
		}
	}
	files := form.File["files"]
	for _, file := range files {
		dst := filepath.Join(dir, file.Filename)
		if err := c.SaveUploadedFile(file, dst); err != nil {
			log.Errorf(`uploadIcons, upload file: %s; user: %s`, err, uid)
			res.Fail(c, 5002)
			return
		}
	}
	generate := true
	if generate {
		if _, err := os.Stat(filepath.Join(style.Path, "sprite@2x.png")); err == nil {
			err := style.GenSprite("sprite@2x.png")
			if err != nil {
				log.Warnf("uploadIcons, generate sprite@2x error")
			}
		}
		if _, err := os.Stat(filepath.Join(style.Path, "sprite.png")); err == nil {
			err := style.GenSprite("sprite.png")
			if err != nil {
				log.Warnf("uploadIcons, generate sprite@2x error")
			}
		}
	}
	res.Done(c, "success")
}

//getSprite get sprite
func getIcon(c *gin.Context) {
	res := NewRes()
	// uid := c.GetString(identityKey)
	uid := c.Param("user")
	sid := c.Param("id")
	style := userSet.style(uid, sid)
	if style == nil {
		log.Errorf("getIcon, style not exist in the service, sid: %s ^^", sid)
		res.Fail(c, 4044)
		return
	}
	name := c.Param("name")
	dir := filepath.Join(style.Path, "icons")
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		regen := c.Param("regenerate")
		if regen != "true" {
			res.FailMsg(c, "icons not exist, if you want regenerate from sprite.json/png, set regenerate true")
			return
		}
		err := GenIconsFromSprite(style.Path)
		if err != nil {
			log.Errorf("GenIconsFromSprite, gen icons error, details: %s", err)
			res.FailMsg(c, "regenerate icons error")
			return
		}
	}
	pathfile := filepath.Join(dir, name)
	pathfile = autoAppendExt(pathfile)
	file, err := ioutil.ReadFile(pathfile)
	if err != nil {
		log.Errorf(`getIcon, read sprite file: %v; user: %s ^^`, err, uid)
		res.Fail(c, 5002)
		return
	}
	ext := filepath.Ext(pathfile)
	if ext == "" {
		ext = ".svg"
	}
	ext = ext[1:]
	c.Header("Content-Type", "image/"+ext)
	c.Writer.Write(file)
}

//updateIcon get sprite
func updateIcon(c *gin.Context) {
	res := NewRes()
	// uid := c.GetString(identityKey)
	uid := c.Param("user")
	sid := c.Param("id")
	style := userSet.style(uid, sid)
	if style == nil {
		log.Errorf("updateIcon, style not exist in the service, sid: %s ^^", sid)
		res.Fail(c, 4044)
		return
	}
	name := c.Param("name")
	var body struct {
		Scale      float64 `json:"scale" form:"scale" binding:"required"`
		Regenerate bool    `json:"regenerate" form:"regenerate"`
	}
	err := c.Bind(&body)
	if err != nil {
		res.Fail(c, 4001)
		return
	}
	if body.Scale == 1 || body.Scale <= 0 || body.Scale > 100 {
		res.FailMsg(c, "scale error")
		return
	}

	dir := filepath.Join(style.Path, "icons")
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if !body.Regenerate {
			res.FailMsg(c, "icons not exist, if you want regenerate from sprite.json/png, set regenerate true")
			return
		}
		err := GenIconsFromSprite(style.Path)
		if err != nil {
			log.Errorf("GenIconsFromSprite, gen icons error, details: %s", err)
			res.FailMsg(c, "regenerate icons error")
			return
		}
	}
	pathfile := filepath.Join(dir, name)
	pathfile = autoAppendExt(pathfile)
	ext := filepath.Ext(pathfile)
	lext := strings.ToLower(ext)
	switch lext {
	case ".svg":
		buf, err := svg2svg(pathfile, body.Scale)
		if err != nil {
			res.FailErr(c, err)
			return
		}
		err = ioutil.WriteFile(pathfile, buf, os.ModePerm)
		if err != nil {
			res.FailErr(c, err)
			return
		}

	default:
		file, err := os.Open(pathfile)
		if err != nil {
			log.Error(err)
			res.FailErr(c, err)
			return
		}

		img, _, err := image.Decode(file)
		if err != nil {
			log.Error(err)
			res.FailErr(c, err)
			return
		}
		rect := img.Bounds()
		w := rect.Dx()
		h := rect.Dy()
		w = int(float64(w) * body.Scale)
		h = int(float64(h) * body.Scale)
		img = resize.Resize(uint(w), uint(h), img, resize.Lanczos3)
		var out bytes.Buffer
		err = png.Encode(&out, img)
		if err != nil {
			log.Error(err)
			res.FailErr(c, err)
			return
		}
		ioutil.WriteFile(pathfile, out.Bytes(), os.ModePerm)
		if err != nil {
			log.Error(err)
			res.FailErr(c, err)
			return
		}
	}

	if body.Regenerate {
		if _, err := os.Stat(filepath.Join(style.Path, "sprite@2x.png")); err == nil {
			err := style.GenSprite("sprite@2x.png")
			if err != nil {
				log.Warnf("uploadIcons, generate sprite@2x error")
			}
		}
		if _, err := os.Stat(filepath.Join(style.Path, "sprite.png")); err == nil {
			err := style.GenSprite("sprite.png")
			if err != nil {
				log.Warnf("uploadIcons, generate sprite@2x error")
			}
		}
	}
	res.Done(c, "success")
}

//upInsertStyle create a style
func deleteIcons(c *gin.Context) {
	res := NewRes()
	// uid := c.GetString(identityKey)
	uid := c.Param("user")
	sid := c.Param("id")
	style := userSet.style(uid, sid)
	if style == nil {
		log.Errorf(`updateSprite, %s's style service (%s) not found ^^`, uid, sid)
		res.Fail(c, 4044)
		return
	}
	var body struct {
		Names      []string `json:"names" form:"names" binding:"required"`
		Regenerate bool     `json:"regenerate" form:"regenerate"`
	}
	err := c.Bind(&body)
	if err != nil {
		res.Fail(c, 4001)
		return
	}
	dir := filepath.Join(style.Path, "icons")
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if !body.Regenerate {
			res.FailMsg(c, "icons not exist, if you want regenerate from sprite.json/png, set regenerate true")
			return
		}
		err := GenIconsFromSprite(style.Path)
		if err != nil {
			log.Errorf("GenIconsFromSprite, gen icons error, details: %s", err)
			res.FailMsg(c, "regenerate icons error")
			return
		}
	}
	var sucs []string
	for _, name := range body.Names {
		dst := filepath.Join(dir, name)
		dst = autoAppendExt(dst)
		err := os.Remove(dst)
		if err == nil {
			sucs = append(sucs, name)
		}
	}
	if body.Regenerate {
		if _, err := os.Stat(filepath.Join(style.Path, "sprite@2x.png")); err == nil {
			err := style.GenSprite("sprite@2x.png")
			if err != nil {
				log.Warnf("uploadIcons, generate sprite@2x error")
			}
		}
		if _, err := os.Stat(filepath.Join(style.Path, "sprite.png")); err == nil {
			err := style.GenSprite("sprite.png")
			if err != nil {
				log.Warnf("uploadIcons, generate sprite@2x error")
			}
		}
	}
	res.DoneData(c, sucs)
}

//uploadSprite sprites符号库
func uploadSprite(c *gin.Context) {
	res := NewRes()
	// uid := c.GetString(identityKey)
	uid := c.Param("user")
	sid := c.Param("id")
	style := userSet.style(uid, sid)
	if style == nil {
		log.Errorf(`uploadSprite, %s's style service (%s) not found ^^`, uid, sid)
		res.Fail(c, 4044)
		return
	}
	// Multipart form
	form, err := c.MultipartForm()
	if err != nil {
		log.Errorf(`uploadSprite, get form: %s; user: %s`, err, uid)
		res.Fail(c, 400)
		return
	}
	var sucs []string
	files := form.File["files"]
	for _, file := range files {
		dst := filepath.Join(style.Path, file.Filename)
		if err := c.SaveUploadedFile(file, dst); err != nil {
			log.Errorf(`uploadSprite, upload file: %s; user: %s`, err, uid)
			res.Fail(c, 5002)
			return
		}
		sucs = append(sucs, file.Filename)
	}
	//todo update to cache
	res.DoneData(c, sucs)
}

//updateSprite 刷新（重新生成）sprites符号库
func updateSprite(c *gin.Context) {
	res := NewRes()
	// uid := c.GetString(identityKey)
	uid := c.Param("user")
	sid := c.Param("id")
	style := userSet.style(uid, sid)
	if style == nil {
		log.Errorf(`updateSprite, %s's style service (%s) not found ^^`, uid, sid)
		res.Fail(c, 4044)
		return
	}
	sprite := c.Param("name")
	spritePat := `^sprite(@[234]x)?.(?:json|png)$`
	if ok, _ := regexp.MatchString(spritePat, sprite); !ok {
		log.Errorf(`updateSprite, get sprite MatchString false, sprite : %s; user: %s ^^`, sprite, uid)
		res.Fail(c, 4004)
		return
	}
	if err := style.GenSprite(sprite); err != nil {
		log.Errorf(`updateSprite, generate %s's style service (%s) sprite  error, details: %s ^^`, uid, sid, err)
		res.FailMsg(c, "generate sprite error")
		return
	}
	res.Done(c, "")
}

//getSprite get sprite
func getSprite(c *gin.Context) {
	res := NewRes()
	// uid := c.GetString(identityKey)
	uid := c.Param("user")
	sid := c.Param("id")
	style := userSet.style(uid, sid)
	if style == nil {
		log.Errorf(`getSprite, %s's style service (%s) not found ^^`, uid, sid)
		res.Fail(c, 4044)
		return
	}
	sprite := c.Param("name")
	spritePat := `^sprite(@[234]x)?.(?:json|png)$`
	if ok, _ := regexp.MatchString(spritePat, sprite); !ok {
		log.Errorf(`getSprite, get sprite MatchString false, sprite : %s; user: %s ^^`, sprite, uid)
		res.Fail(c, 4004)
		return
	}
	pathfile := filepath.Join(style.Path, sprite)
	_, err := os.Stat(pathfile)
	if err != nil {
		if os.IsNotExist(err) {
			err := style.GenSprite(sprite)
			if err != nil {
				log.Errorf(`getSprite, sprite not found, and generate error, details: %s ^^`, err)
				res.FailMsg(c, "not found and generate error")
				return
			}
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

//deleteStyle 删除样式
func deleteStyle(c *gin.Context) {
	res := NewRes()
	// uid := c.GetString(identityKey)
	uid := c.Param("user")
	sid := c.Param("ids")
	sids := strings.Split(sid, ",")
	for _, sid := range sids {
		style := userSet.style(uid, sid)
		if style == nil {
			log.Errorf(`deleteStyle, %s's style service (%s) not found ^^`, uid, sid)
			res.Fail(c, 4044)
			return
		}
		set := userSet.service(uid)
		set.S.Delete(sid)
		err := db.Where("id = ?", style.ID).Delete(Style{}).Error
		if err != nil {
			log.Error(err)
			res.Fail(c, 5001)
			return
		}
		err = os.RemoveAll(style.Path)
		if err != nil && !os.IsNotExist(err) {
			log.Errorf(`deleteStyle, remove %s's style dir (%s) error, details:%s ^^`, uid, sid, err)
			res.FailErr(c, err)
			return
		}
		err = os.Remove(style.Path + ".zip")
		if err != nil && !os.IsNotExist(err) {
			log.Errorf(`deleteStyle, remove %s's style .zip (%s) error, details:%s ^^`, uid, sid, err)
			res.FailErr(c, err)
			return
		}
	}

	res.Done(c, "")
}

//getStyle get user style by id
func getStyle(c *gin.Context) {
	res := NewRes()
	// uid := c.GetString(identityKey)
	uid := c.Param("user")
	sid := c.Param("id")
	s := userSet.style(uid, sid)
	if s == nil {
		log.Errorf(`getStyle, %s's style (%s) not found ^^`, uid, sid)
		res.Fail(c, 4044)
		return
	}
	var style Root
	if err := json.Unmarshal(s.Data, &style); err != nil {
		fmt.Println(err)
	}
	baseurl := rootURL(c.Request)
	fixURL := func(url string) string {
		if "" == url || !strings.HasPrefix(url, "atlas://") {
			return url
		}
		return strings.Replace(url, "atlas:/", baseurl, -1)
	}
	style.Glyphs = fixURL(style.Glyphs)
	style.Sprite = fixURL(style.Sprite)
	for _, src := range style.Sources {
		src.URL = fixURL(src.URL)
		for i := range src.Tiles {
			src.Tiles[i] = fixURL(src.Tiles[i])
		}
	}
	c.JSON(http.StatusOK, &style)
}

//cloneStyle 复制指定用户的公开样式
func cloneStyle(c *gin.Context) {
	res := NewRes()
	self := c.GetString(identityKey)
	uid := c.Param("user")
	sid := c.Param("id")
	style := userSet.style(uid, sid)
	if style == nil {
		log.Errorf("copyStyle, %s's style service (%s) not found ^^", uid, sid)
		res.Fail(c, 4044)
		return
	}
	if !style.Public {
		log.Errorf("copyStyle, %s's style service (%s) not public ^^", uid, sid)
		res.Fail(c, 4044)
		return
	}
	set := userSet.service(self)
	if set == nil {
		log.Errorf("copyStyle, %s's service set not found ^^", uid)
		res.Fail(c, 4043)
		return
	}

	id, _ := shortid.Generate()
	ns := style.Copy()
	suffix := filepath.Ext(style.ID)
	ns.ID = strings.TrimSuffix(style.ID, suffix) + "." + id
	ns.Name = style.Name + "-复制"
	ns.Owner = self
	ns.Path = filepath.Join("styles", self, ns.ID)
	err := DirCopy(style.Path, ns.Path)
	if err != nil {
		log.Errorf("copyStyle, copy %s's styledir to new (%s) error ^^", uid, ns.Path)
		res.FailErr(c, err)
		return
	}
	err = ns.UpInsert()
	if err != nil {
		log.Errorf("copyStyle, upinsert %s's new style error ^^", self)
		res.FailErr(c, err)
		return
	}
	set.S.Store(ns.ID, ns)
	res.DoneData(c, ns)
}

//viewStyle load style map
func viewStyle(c *gin.Context) {
	res := NewRes()
	// uid := c.GetString(identityKey)
	uid := c.Param("user")
	sid := c.Param("id")
	style := userSet.style(uid, sid)
	if style == nil {
		log.Errorf("viewStyle, style not exist in the service, sid: %s ^^", sid)
		res.Fail(c, 4044)
		return
	}
	url := fmt.Sprintf(`%s/styles/%s/x/%s/`, rootURL(c.Request), uid, sid)
	c.HTML(http.StatusOK, "viewer.html", gin.H{
		"Title": "Viewer",
		"ID":    sid,
		"URL":   url,
	})
}

//upInsertStyle create a style
func updateStyle(c *gin.Context) {
	res := NewRes()
	// uid := c.GetString(identityKey)
	uid := c.Param("user")
	sid := c.Param("id")
	style := userSet.style(uid, sid)
	if style == nil {
		log.Errorf(`updateStyle, get %s's style service (%s) error`, uid, sid)
		res.Fail(c, 4044)
		return
	}
	decoder := json.NewDecoder(c.Request.Body)
	var data json.RawMessage
	err := decoder.Decode(&data)
	if err != nil {
		log.Errorf("decode style error, details:%s", err)
		res.FailMsg(c, "decode style error")
		return
	}
	style.Data = data
	res.Done(c, "")
}

//upInsertStyle create a style
func saveStyle(c *gin.Context) {
	res := NewRes()
	// uid := c.GetString(identityKey)
	uid := c.Param("user")
	sid := c.Param("id")
	style := userSet.style(uid, sid)
	if style == nil {
		log.Errorf(`updateStyle, get %s's style service (%s) error`, uid, sid)
		res.Fail(c, 4044)
		return
	}
	err := style.UpInsert()
	if err != nil {
		log.Errorf(`saveStyle, saved %s's style (%s) to db/file error, details: %s`, uid, sid, err)
		res.FailMsg(c, "save style to db/file error")
		return
	}
	res.Done(c, "success")
}
