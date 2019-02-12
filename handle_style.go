package main

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/gin-gonic/gin"
	"github.com/teris-io/shortid"
)

//listStyles list user style
func listStyles(c *gin.Context) {
	res := NewRes()
	var styles []*Style
	pubSet.S.Range(func(_, v interface{}) bool {
		styles = append(styles, v.(*Style))
		return true
	})
	res.DoneData(c, styles)
}

//uploadStyle create a style
func uploadStyle(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	// style source
	file, err := c.FormFile("file")
	if err != nil {
		log.Errorf(`uploadStyle, get form: %s; user: %s`, err, id)
		res.Fail(c, 4046)
		return
	}

	styles := cfgV.GetString("assets.styles")
	name := strings.TrimSuffix(file.Filename, filepath.Ext(file.Filename))
	sid, _ := shortid.Generate()
	sid = name + "." + sid
	dst := filepath.Join(styles, sid)
	os.MkdirAll(dst, os.ModePerm)
	dst = filepath.Join(dst, "style.json")

	if err := c.SaveUploadedFile(file, dst); err != nil {
		log.Errorf(`uploadStyle, upload file: %s; user: %s`, err, id)
		res.Fail(c, 5002)
		return
	}
	//更新服务
	style, err := LoadStyle(dst)
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
		pubSet.S.Store(ss.ID, ss)
	}
	res.Done(c, "")
}

//saveStyle create a style
func upSaveStyle(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	user := c.Param("user")
	sid := c.Param("sid")
	body, err := ioutil.ReadAll(c.Request.Body)
	if err != nil {
		log.Errorf(`updateStyle, get form: %s; user: %s`, err, id)
		res.Fail(c, 5003)
		return
	}
	home := cfgV.GetString("users.home")
	styles := cfgV.GetString("users.styles")
	dst := filepath.Join(home, user, styles, sid, "style.json")
	out := make(map[string]interface{})
	json.Unmarshal(body, &out)
	out["id"] = sid
	out["modified"] = time.Now().Format("2006-01-02 03:04:05 PM")
	out["owner"] = id
	file, _ := json.Marshal(out)
	err = ioutil.WriteFile(dst, file, os.ModePerm)
	if err != nil {
		log.Error(err)
		res.Fail(c, 5002)
		return
	}
	style, ok := pubSet.S.Load(sid)
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
	sid := c.Param("sid")
	style, ok := pubSet.S.Load(sid)
	if !ok {
		log.Errorf("getStyle, style not exist in the service, sid: %s ^^", sid)
		res.Fail(c, 4044)
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

//getSprite get sprite
func getSprite(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	sid := c.Param("sid")
	iv, ok := pubSet.S.Load(sid)
	if !ok {
		log.Errorf("getSprite, style not exist in the service, sid: %s ^^", sid)
		res.Fail(c, 4044)
		return
	}
	style := iv.(*Style)
	sprite := c.Param("fmt")
	sprite = "sprite" + sprite
	spritePat := `^sprite(@[2]x)?.(?:json|png)$`
	if ok, _ := regexp.MatchString(spritePat, sprite); !ok {
		log.Errorf(`getSprite, get sprite MatchString false, sprite : %s; user: %s ^^`, sprite, id)
		res.Fail(c, 4004)
		return
	}

	if strings.HasSuffix(strings.ToLower(sprite), ".json") {
		c.Writer.Header().Set("Content-Type", "application/json")
	}
	if strings.HasSuffix(strings.ToLower(sprite), ".png") {
		c.Writer.Header().Set("Content-Type", "image/png")
	}

	stylesPath := filepath.Dir(style.BaseID)
	spriteFile := filepath.Join(stylesPath, sprite)
	file, err := ioutil.ReadFile(spriteFile)
	if err != nil {
		log.Errorf(`getSprite, read sprite file: %v; user: %s ^^`, err, id)
		res.Fail(c, 5002)
		return
	}
	c.Writer.Write(file)
}

func uploadSprite(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	sid := c.Param("sid")

	// Multipart form
	form, err := c.MultipartForm()
	if err != nil {
		log.Errorf(`uploadSprite, get form: %s; user: %s`, err, id)
		res.Fail(c, 400)
		return
	}

	styles := cfgV.GetString("assets.styles")

	files := form.File["files"]
	for _, file := range files {
		dst := filepath.Join(styles, sid, file.Filename)
		if err := c.SaveUploadedFile(file, dst); err != nil {
			log.Errorf(`uploadSprite, upload file: %s; user: %s`, err, id)
			res.Fail(c, 5002)
			return
		}
	}

	res.Done(c, "")
}

//viewStyle load style map
func viewStyle(c *gin.Context) {
	res := NewRes()
	sid := c.Param("sid")
	_, ok := pubSet.S.Load(sid)
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
