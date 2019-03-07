package main

import (
	"bytes"
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
	var styles []*StyleService
	set.S.Range(func(_, v interface{}) bool {
		s, ok := v.(*StyleService)
		if ok {
			styles = append(styles, s)
		}
		return true
	})
	if uid != ATLAS && "true" == c.Query("public") {
		set := userSet.service(ATLAS)
		if set != nil {
			set.S.Range(func(_, v interface{}) bool {
				s, ok := v.(*StyleService)
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
	style := userSet.style(uid, sid)
	if style == nil {
		log.Errorf("getStyle, style not exist in the service, sid: %s ^^", sid)
		res.Fail(c, 4044)
		return
	}
	res.DoneData(c, style.Thumbnail)
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
		ss := style.toService()
		set := userSet.service(uid)
		if set == nil {
			log.Errorf("%s's service set not found", uid)
			res.FailMsg(c, "加载服务失败")
			return
		}
		set.S.Store(ss.ID, ss)
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
	ss := style.toService()
	set.S.Store(ss.ID, ss)
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

//uploadStyle 上传新样式
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
	dir := filepath.Join(style.URL, "icons")
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
		if _, err := os.Stat(filepath.Join(style.URL, "sprite@2x.png")); err == nil {
			err := style.GenSprite("sprite@2x.png")
			if err != nil {
				log.Warnf("uploadIcons, generate sprite@2x error")
			}
		}
		if _, err := os.Stat(filepath.Join(style.URL, "sprite.png")); err == nil {
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
	pathfile := filepath.Join(style.URL, "icons", name)
	ext := filepath.Ext(name)
	if ext == "" {
		if _, err := os.Stat(pathfile + ".svg"); err == nil {
			pathfile = pathfile + ".svg"
			ext = "svg"
		} else if _, err := os.Stat(pathfile + ".png"); err == nil {
			pathfile = pathfile + ".png"
			ext = "png"
		}
	} else {
		ext = ext[1:]
	}
	file, err := ioutil.ReadFile(pathfile)
	if err != nil {
		log.Errorf(`getIcon, read sprite file: %v; user: %s ^^`, err, uid)
		res.Fail(c, 5002)
		return
	}
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
		Scale float64 `json:"scale" form:"scale" binding:"required"`
	}
	err := c.Bind(&body)
	if err != nil {
		res.Fail(c, 4001)
		return
	}

	pathfile := filepath.Join(style.URL, "icons", name)

	ext := filepath.Ext(name)
	if ext == "" {
		if _, err := os.Stat(pathfile + ".svg"); err == nil {
			pathfile = pathfile + ".svg"
			ext = "svg"
		} else if _, err := os.Stat(pathfile + ".png"); err == nil {
			pathfile = pathfile + ".png"
			ext = "png"
		} else if _, err := os.Stat(pathfile + ".jpg"); err == nil {
			pathfile = pathfile + ".jpg"
			ext = "jpg"
		} else if _, err := os.Stat(pathfile + ".bmp"); err == nil {
			pathfile = pathfile + ".bmp"
			ext = "bmp"
		} else if _, err := os.Stat(pathfile + ".gif"); err == nil {
			pathfile = pathfile + ".gif"
			ext = "gif"
		} else {
			return
		}
	} else {
		ext = ext[1:]
	}
	lext := strings.ToLower(ext)
	switch lext {
	case "svg":
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
		res.Done(c, "success")
		return
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
		if body.Scale != 1.0 {
			if body.Scale > 0 && body.Scale < 2 {
				w = int(float64(w) * body.Scale)
				h = int(float64(h) * body.Scale)
			}
			img = resize.Resize(uint(w), uint(h), img, resize.Lanczos3)
		}
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
		res.Done(c, "success")
	}
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
		Names []string `json:"names" form:"names" binding:"required"`
	}
	err := c.Bind(&body)
	if err != nil {
		res.Fail(c, 4001)
		return
	}
	dir := filepath.Join(style.URL, "icons")
	for _, name := range body.Names {
		dst := filepath.Join(dir, name)
		os.Remove(dst)
	}
	generate := true
	if generate {
		if _, err := os.Stat(filepath.Join(style.URL, "sprite@2x.png")); err == nil {
			err := style.GenSprite("sprite@2x.png")
			if err != nil {
				log.Warnf("uploadIcons, generate sprite@2x error")
			}
		}
		if _, err := os.Stat(filepath.Join(style.URL, "sprite.png")); err == nil {
			err := style.GenSprite("sprite.png")
			if err != nil {
				log.Warnf("uploadIcons, generate sprite@2x error")
			}
		}
	}
	res.Done(c, "")
}

//uploadSprite 上传新样式
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
		dst := filepath.Join(style.URL, file.Filename)
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
	pathfile := filepath.Join(style.URL, sprite)
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

//upInsertStyle create a style
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
		err = os.RemoveAll(style.URL)
		if err != nil {
			log.Errorf(`deleteStyle, remove %s's style dir (%s) error, details:%s ^^`, uid, sid, err)
			res.FailErr(c, err)
			return
		}
		err = os.Remove(style.URL + ".zip")
		if err != nil {
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
	style := userSet.style(uid, sid)
	if style == nil {
		log.Errorf("getStyle, style not exist in the service, sid: %s ^^", sid)
		res.Fail(c, 4044)
		return
	}
	// var out map[string]interface{}
	// json.Unmarshal(style.(*StyleService).Data, &out)

	protoScheme := scheme(c.Request)
	fixURL := func(url string) string {
		if "" == url || !strings.HasPrefix(url, "atlas://") {
			return url
		}
		return strings.Replace(url, "atlas://", protoScheme+"://"+c.Request.Host+"/", -1)
	}

	out, ok := style.Data.(map[string]interface{})
	if !ok {
		log.Errorf("getStyle, style json error, sid: %s ^^", sid)
		res.FailMsg(c, "style json error")
		return
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
	set := userSet.service(self)
	if set == nil {
		log.Errorf("copyStyle, %s's service set not found ^^", uid)
		res.Fail(c, 4043)
		return
	}
	id, _ := shortid.Generate()
	ns := style.Copy()
	suffix := filepath.Ext(ns.ID)
	ns.ID = strings.TrimSuffix(ns.ID, suffix) + "." + id
	ns.Name = ns.Name + "-复制"
	ns.Owner = self
	oldpath := ns.URL
	ns.URL = strings.Replace(ns.URL, uid, self, 1)
	err := DirCopy(oldpath, ns.URL)
	if err != nil {
		log.Errorf("copyStyle, copy %s's styledir to new (%s) error ^^", uid, ns.URL)
		res.FailErr(c, err)
		return
	}
	err = ns.toStyle().UpInsert()
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

	body, err := ioutil.ReadAll(c.Request.Body)
	if err != nil {
		res.Fail(c, 5003)
		return
	}
	style.Data = body
	needsavedb := false
	if needsavedb {
		style.toStyle().UpInsert()
		// //save to file decrept
		// dst := filepath.Join(style.Path, "style.json")
		// out := make(map[string]interface{})
		// json.Unmarshal(body, &out)
		// out["id"] = sid
		// out["modified"] = time.Now().Format("2006-01-02 03:04:05 PM")
		// out["owner"] = uid
		// buf, err := json.Marshal(out)
		// if err != nil {
		// 	log.Error(err)
		// 	res.FailErr(c, err)
		// 	return
		// }
		// err = ioutil.WriteFile(dst, buf, os.ModePerm)
		// if err != nil {
		// 	log.Error(err)
		// 	res.Fail(c, 5002)
		// 	return
		// }
	}

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

	body, err := ioutil.ReadAll(c.Request.Body)
	if err != nil {
		res.Fail(c, 5003)
		return
	}
	style.Data = body
	needsavedb := true
	if needsavedb {
		style.toStyle().UpInsert()
	}
	res.Done(c, "")
}
