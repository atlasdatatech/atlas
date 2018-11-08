package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/render"
	"github.com/jinzhu/gorm"
	"github.com/teris-io/shortid"
	"golang.org/x/crypto/bcrypt"
)

func index(c *gin.Context) {
	_, err := authMid.GetClaimsFromJWT(c)
	if err != nil {
		c.HTML(http.StatusOK, "index.html", gin.H{
			"Title": "AtlasMap",
			"Login": true,
		})
	}
	c.Redirect(http.StatusFound, "/studio/")
}

func renderLogin(c *gin.Context) {
	c.HTML(http.StatusOK, "login.html", gin.H{
		"Title": "AtlasMap",
	})
}

func login(c *gin.Context) {
	res := NewRes()
	var body struct {
		Name     string `form:"name" binding:"required"`
		Password string `form:"password" binding:"required"`
	}
	err := c.Bind(&body)
	if err != nil {
		res.Fail(c, err)
		return
	}
	// validate
	if len(body.Name) == 0 || len(body.Password) == 0 {
		res.FailStr(c, "name or password required")
		return
	}
	body.Name = strings.ToLower(body.Name)
	// abuseFilter
	IPCountChan := make(chan int)
	IPUserCountChan := make(chan int)
	clientIP := c.ClientIP()
	ttl := time.Now().Add(cfgV.GetDuration("attempts.expiration"))
	go func(c chan int) {
		var cnt int
		db.Model(&Attempt{}).Where("ip = ? AND created_at > ?", clientIP, ttl).Count(&cnt)
		c <- cnt
	}(IPCountChan)
	go func(c chan int) {
		var cnt int
		db.Model(&Attempt{}).Where("ip = ? AND name = ? AND created_at > ?", clientIP, body.Name, ttl).Count(&cnt)
		c <- cnt
	}(IPUserCountChan)
	IPCount := <-IPCountChan
	IPUserCount := <-IPUserCountChan
	if IPCount > cfgV.GetInt("attempts.ip") || IPUserCount > cfgV.GetInt("attempts.user") {
		res.FailStr(c, "you've reached the maximum number of login attempts. please try again later")
		return
	}
	// attemptLogin
	user := User{}
	if db.Where("name = ?", body.Name).First(&user).RecordNotFound() {
		res.FailStr(c, "check user name")
		return
	}
	err = bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(body.Password))
	if err != nil {
		attempt := Attempt{IP: clientIP, Name: body.Name}
		db.Create(&attempt)
		res.FailStr(c, "check password")
		return
	}
	//Cookie
	if authMid.SendCookie {
		maxage := int(user.Expires.Unix() - time.Now().Unix())
		c.SetCookie(
			"Token",
			user.JWT,
			maxage,
			"/",
			authMid.CookieDomain,
			authMid.SecureCookie,
			authMid.CookieHTTPOnly,
		)
	}
	c.JSON(http.StatusOK, gin.H{
		"code":    http.StatusOK,
		"token":   user.JWT,
		"expire":  user.Expires.Format(time.RFC3339),
		"message": "success",
	})
}

func renderAccount(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	user := &User{}
	if err := db.Where("name = ?", id).First(&user).Error; err != nil {
		res.FailStr(c, fmt.Sprintf("renderAccount, get user info: %s; user: %s", err, id))
		if !gorm.IsRecordNotFoundError(err) {
			log.Errorf("renderAccount, get user info: %s; user: %s", err, id)
		}
		return
	}

	c.HTML(http.StatusOK, "account.html", user)
}

func renderUpdateUser(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	user := &User{}
	if err := db.Where("name = ?", id).First(&user).Error; err != nil {
		res.FailStr(c, fmt.Sprintf("renderAccount, get user info: %s; user: %s", err, id))
		if !gorm.IsRecordNotFoundError(err) {
			log.Errorf("renderAccount, get user info: %s; user: %s", err, id)
		}
		return
	}

	c.HTML(http.StatusOK, "update.html", user)
}

func renderChangePassword(c *gin.Context) {
	c.HTML(http.StatusOK, "change.html", gin.H{
		"Title": "AtlasMap",
	}) // can't handle /login/reset/:email:token
}

func createUser(c *gin.Context) {
	res := NewRes()
	var body struct {
		Name       string `form:"name" json:"name" binding:"required"`
		Password   string `form:"password" json:"password" binding:"required"`
		Role       string `form:"role" json:"role"`
		Phone      string `form:"phone" json:"phone"`
		Department string `form:"department" json:"department"`
	}
	err := c.Bind(&body)
	if err != nil {
		res.Fail(c, err)
		return
	}
	// validate
	if ok, err := validate(body.Name, body.Password); !ok {
		res.Fail(c, err)
		return
	}
	user := User{}
	if err := db.Where("name = ?", body.Name).First(&user).Error; err != nil {
		if gorm.IsRecordNotFoundError(err) {
		} else {
			res.FailStr(c, "get user info error")
			log.Errorf("createUser, get user info: %s; user: %s", err, body.Name)
			return
		}
	}
	// duplicate UsernameCheck EmailCheck
	if len(user.Name) != 0 {
		if user.Name == body.Name {
			res.FailStr(c, "name already taken")
			return
		}
	}
	// createUser
	user.ID, _ = shortid.Generate()
	user.Name = body.Name
	hashedPassword, _ := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	user.Password = string(hashedPassword)
	user.Role = body.Role
	user.Phone = body.Phone
	user.Department = body.Department
	//No verification required
	user.JWT, user.Expires, err = authMid.TokenGenerator(&user)
	if err != nil {
		res.Fail(c, err)
		return
	}
	user.Activation = "yes"
	user.Search = []string{body.Name, body.Phone, body.Department}
	// insertUser
	err = db.Create(&user).Error
	if err != nil {
		res.Fail(c, err)
		return
	}

	// casEnf.LoadPolicy()
	if strings.Compare(body.Role, "admin") == 0 {
		casEnf.AddGroupingPolicy(user.Name, "admin_group")
	} else {
		casEnf.AddGroupingPolicy(user.Name, "user_group")
	}
	// casEnf.SavePolicy()

	res.Done(c, "done")
}

func listUsers(c *gin.Context) {
	// 获取所有记录
	var users []User
	db.Find(&users)
	c.JSON(http.StatusOK, users)
}

func readUser(c *gin.Context) {
	res := NewRes()
	name := c.Param("id")
	if name == "" {
		name = c.GetString(identityKey)
	}
	user := &User{}
	if err := db.Where("name = ?", name).First(&user).Error; err != nil {
		res.FailStr(c, fmt.Sprintf("readUser, get user info: %s; user: %s", err, name))
		if !gorm.IsRecordNotFoundError(err) {
			log.Errorf("readUser, get user info: %s; user: %s", err, name)
		}
		return
	}
	c.JSON(http.StatusOK, user)
}

func updateUser(c *gin.Context) {
	res := NewRes()
	name := c.Param("id")
	if name == "" {
		name = c.GetString(identityKey)
	}
	var body struct {
		Phone      string `form:"phone" json:"phone"`
		Department string `form:"department" json:"department"`
	}
	err := c.Bind(&body)
	if err != nil {
		res.Fail(c, err)
		return
	}
	search := []string{name, body.Phone, body.Department} //更新搜索字段
	err = db.Model(&User{}).Where("name = ?", name).Update(User{Phone: body.Phone, Department: body.Department, Search: search}).Error
	if err != nil {
		log.Errorf("updateUser, update user info: %s; user: %s", err, name)
		res.Fail(c, err)
		return
	}
	res.Done(c, "done")
}

func deleteUser(c *gin.Context) {
	res := NewRes()
	id := c.Param("id")
	if id == "roo" {
		res.FailStr(c, "unable to delete root")
		return
	}
	//更新角色
	casEnf.RemoveGroupingPolicy(id, "admin_group")
	casEnf.RemoveGroupingPolicy(id, "user_group")

	err := db.Where("name = ?", id).Delete(&User{}).Error
	if err != nil {
		log.Errorf("deleteUser, delete user : %s; user: %s", err, id)
		res.Fail(c, err)
		return
	}

	res.Done(c, "done")
}

func jwtRefresh(c *gin.Context) {
	res := NewRes()
	id := c.Param("id")
	if id == "" {
		id = c.GetString(identityKey)
	}
	tokenString, expire, err := authMid.RefreshToken(c)
	if err != nil {
		log.Errorf("jwtRefresh, refresh token: %s; user: %s", err, id)
		res.Fail(c, err)
		return
	}
	if err := db.Model(&User{}).Where("name = ?", id).Update(User{JWT: tokenString, Expires: expire}).Error; err != nil {
		log.Errorf("jwtRefresh, update jwt: %s; user: %s", err, id)
		res.Fail(c, err)
		return
	}
	_, err = c.Cookie("Token")
	if err != nil {
		log.Errorf("jwtRefresh, test cookie set: %s; user: %s", err, id)
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    http.StatusOK,
		"token":   tokenString,
		"expire":  expire.Format(time.RFC3339),
		"message": "refresh successfully",
	})

}

func changePassword(c *gin.Context) {
	res := NewRes()
	id := c.Param("id")
	if id == "" {
		id = c.GetString(identityKey)
	}

	var body struct {
		Password string `form:"password" binding:"required,gt=3"`
		Confirm  string `form:"confirm" binding:"required,eqfield=Password"`
	}
	err := c.Bind(&body)
	if err != nil {
		res.Fail(c, err)
		return
	}

	// user.setPassword(body.Password)
	hashedPassword, _ := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	err = db.Model(&User{}).Where("name = ?", id).Update(User{Password: string(hashedPassword)}).Error
	if err != nil {
		log.Errorf("changePassword, update password: %s; user: %s", err, id)
		res.Fail(c, err)
		return
	}
	res.Done(c, "done")
}

func changeRole(c *gin.Context) {
	res := NewRes()
	id := c.Param("id")
	var body struct {
		Role string `form:"role" binding:"required"`
	}
	err := c.Bind(&body)
	if err != nil {
		res.Fail(c, err)
		return
	}
	//清空角色
	casEnf.RemoveGroupingPolicy(id, "admin_group")
	casEnf.RemoveGroupingPolicy(id, "user_group")

	err = db.Model(&User{}).Where("name = ?", id).Update(User{Role: body.Role}).Error
	if err != nil {
		log.Errorf("changeRole, update user role: %s; user: %s", err, id)
		res.Fail(c, err)
		return
	}
	//更新角色
	if body.Role == "admin" {
		casEnf.AddGroupingPolicy(id, "admin_group")
	} else {
		casEnf.AddGroupingPolicy(id, "user_group")
	}

	res.Done(c, "done")
}

func logout(c *gin.Context) {
	res := NewRes()
	c.SetCookie(
		"Token",
		"",
		0,
		"/",
		authMid.CookieDomain,
		authMid.SecureCookie,
		authMid.CookieHTTPOnly,
	)
	res.Done(c, "success")
}

func studioIndex(c *gin.Context) {
	//public
	c.HTML(http.StatusOK, "index.html", gin.H{
		"Title":    "AtlasMap",
		"Login":    false,
		"Styles":   pubSet.Styles,
		"Tilesets": pubSet.Tilesets,
	})
}

func studioEditer(c *gin.Context) {
	//public
	id := c.GetString(identityKey) //for user privite tiles
	log.Debug(id)
	user := c.Param("user")
	c.HTML(http.StatusOK, "editor.html", gin.H{
		"Title":    "Creater",
		"User":     user,
		"Styles":   pubSet.Styles,
		"Tilesets": pubSet.Tilesets,
	})
}

//listStyles list user style
func listStyles(c *gin.Context) {
	// id := c.GetString(identityKey)
	c.JSON(http.StatusOK, pubSet.Styles)
}

func renderStyleUpload(c *gin.Context) {
	c.HTML(http.StatusOK, "upload-s.html", gin.H{
		"Title": "AtlasMap",
	})
}

func renderSpriteUpload(c *gin.Context) {
	c.HTML(http.StatusOK, "upload-ss.html", gin.H{
		"Title": "AtlasMap",
		"sid":   c.Param("sid"),
	})
}

//uploadStyle create a style
func uploadStyle(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	// style source
	file, err := c.FormFile("file")
	if err != nil {
		log.Errorf(`uploadStyle, get form: %s; user: %s`, err, id)
		res.FailStr(c, fmt.Sprintf(`uploadStyle, get form: %s; user: %s`, err, id))
		return
	}

	styles := cfgV.GetString("assets.styles")
	name := strings.TrimSuffix(file.Filename, filepath.Ext(file.Filename))
	sid, _ := shortid.Generate()
	sid = name + "." + sid
	dst := filepath.Join(styles, sid)
	os.MkdirAll(dst, os.ModePerm)
	dst = filepath.Join(dst, "style.json")
	fmt.Println(dst)

	if err := c.SaveUploadedFile(file, dst); err != nil {
		log.Errorf(`uploadStyle, upload file: %s; user: %s`, err, id)
		res.FailStr(c, fmt.Sprintf(`uploadStyle, upload file: %s; user: %s`, err, id))
		return
	}

	//更新服务
	pubSet.AddStyle(dst, sid)

	res.Done(c, "done")
}

//updateStyle create a style
func updateStyle(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	log.Debug("updateStyle---------", id)

	sid := c.Param("sid")
	style, ok := pubSet.Styles[sid]
	if !ok {
		log.Errorf("style id(%s) not exist in the service", sid)
		res.FailStr(c, "style not exist in the service")
		return
	}

	body, err := ioutil.ReadAll(c.Request.Body)
	if err != nil {
		log.Errorf(`updateStyle, get form: %s; user: %s`, err, id)
		res.FailStr(c, fmt.Sprintf(`updateStyle, get form: %s; user: %s`, err, id))
		return
	}
	style.Style = body
	var out map[string]interface{}
	json.Unmarshal(style.Style, &out)
	c.JSON(http.StatusOK, &out)
}

//saveStyle create a style
func saveStyle(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	log.Debug("saveStyle---------", id)
	user := c.Param("user")
	sid := c.Param("sid")
	body, err := ioutil.ReadAll(c.Request.Body)
	if err != nil {
		log.Errorf(`updateStyle, get form: %s; user: %s`, err, id)
		res.FailStr(c, fmt.Sprintf(`updateStyle, get form: %s; user: %s`, err, id))
		return
	}
	home := cfgV.GetString("users.home")
	styles := cfgV.GetString("users.styles")
	dst := filepath.Join(home, user, styles, sid, "style.json")
	fmt.Println(dst)
	out := make(map[string]interface{})
	json.Unmarshal(body, &out)
	out["id"] = sid
	out["modified"] = time.Now().Format("2006-01-02 03:04:05 PM")
	out["owner"] = id
	file, err := json.Marshal(out)
	ioutil.WriteFile(dst, file, os.ModePerm)
	c.JSON(http.StatusOK, &out)
}

//getStyle get user style by id
func getStyle(c *gin.Context) {
	res := NewRes()
	sid := c.Param("sid")
	style, ok := pubSet.Styles[sid]
	if !ok {
		log.Errorf("getStyle, style not exist in the service, sid: %s ^^", sid)
		res.FailStr(c, "style not exist in the service")
		return
	}

	var out map[string]interface{}
	json.Unmarshal(style.Style, &out)

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
	style, ok := pubSet.Styles[sid]
	if !ok {
		log.Errorf("getSprite, style not exist in the service, sid: %s ^^", sid)
		res.FailStr(c, "style not exist in the service")
		return
	}
	sprite := c.Param("fmt")
	sprite = "sprite" + sprite
	spritePat := `^sprite(@[2]x)?.(?:json|png)$`
	if ok, _ := regexp.MatchString(spritePat, sprite); !ok {
		log.Errorf(`getSprite, get sprite MatchString false, sprite : %s; user: %s ^^`, sprite, id)
		res.FailStr(c, fmt.Sprintf(`getSprite, get sprite MatchString false, sprite : %s; user: %s ^^`, sprite, id))
		return
	}

	if strings.HasSuffix(strings.ToLower(sprite), ".json") {
		c.Writer.Header().Set("Content-Type", "application/json")
	}
	if strings.HasSuffix(strings.ToLower(sprite), ".png") {
		c.Writer.Header().Set("Content-Type", "image/png")
	}

	stylesPath := filepath.Dir(style.URL)
	spriteFile := filepath.Join(stylesPath, sprite)
	file, err := ioutil.ReadFile(spriteFile)
	if err != nil {
		log.Errorf(`getSprite, read sprite file: %v; user: %s ^^`, err, id)
		res.Fail(c, err)
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
		c.String(http.StatusBadRequest, fmt.Sprintf("get form err: %s", err.Error()))
		// res.FailStr(c, fmt.Sprintf(`uploadSprite, get form: %s; user: %s`, err, id))
		return
	}

	styles := cfgV.GetString("assets.styles")

	files := form.File["files"]
	for _, file := range files {
		dst := filepath.Join(styles, sid, file.Filename)
		if err := c.SaveUploadedFile(file, dst); err != nil {
			log.Errorf(`uploadSprite, upload file: %s; user: %s`, err, id)
			// res.FailStr(c, fmt.Sprintf(`uploadSprite, upload file: %s; user: %s`, err, id))
			c.String(http.StatusBadRequest, fmt.Sprintf("upload file err: %s", err.Error()))
			return
		}
	}

	res.Done(c, "done")
}

//viewStyle load style map
func viewStyle(c *gin.Context) {
	res := NewRes()
	sid := c.Param("sid")
	_, ok := pubSet.Styles[sid]
	if !ok {
		log.Errorf("viewStyle, style not exist in the service, sid: %s ^^", sid)
		res.FailStr(c, "style not exist in the service")
		return
	}
	c.HTML(http.StatusOK, "viewer.html", gin.H{
		"Title": "Viewer",
		"ID":    sid,
		"URL":   strings.TrimSuffix(c.Request.URL.Path, "/"),
	})
}

//listTilesets list user's tilesets
func listTilesets(c *gin.Context) {
	// id := c.GetString(identityKey)
	c.JSON(http.StatusOK, pubSet.Tilesets)
}

func renderTilesetsUpload(c *gin.Context) {
	id := c.GetString(identityKey)
	c.HTML(http.StatusOK, "upload-t.html", gin.H{
		"Title": "AtlasMap",
		"User":  id,
	})
}

//uploadTileset list user's tilesets
func uploadTileset(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	// style source
	file, err := c.FormFile("file")
	if err != nil {
		log.Errorf(`uploadTileset, get form: %s; user: %s`, err, id)
		res.FailStr(c, fmt.Sprintf(`uploadTileset, get form: %s; user: %s`, err, id))
		return
	}
	tilesets := cfgV.GetString("assets.tilesets")
	ext := filepath.Ext(file.Filename)
	name := strings.TrimSuffix(file.Filename, ext)
	tid, _ := shortid.Generate()
	tid = name + "." + tid
	dst := filepath.Join(tilesets, tid+ext)

	if err := c.SaveUploadedFile(file, dst); err != nil {
		log.Errorf(`uploadTileset, upload file: %s; user: %s`, err, id)
		res.FailStr(c, fmt.Sprintf(`uploadTileset, upload file: %s; user: %s`, err, id))
		return
	}

	//更新服务
	err = pubSet.AddMBTile(dst, tid)
	if err != nil {
		log.Errorf(`uploadTileset, add mbtiles: %s ^^`, err)
	}

	c.JSON(http.StatusOK, gin.H{
		"tid": tid,
	})
}

//getTilejson get tilejson
func getTilejson(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	tid := c.Param("tid")
	tileService, ok := pubSet.Tilesets[tid]
	if !ok {
		log.Errorf("tilesets id(%s) not exist in the service", tid)
		res.FailStr(c, "tilesets not exist in the service")
		return
	}

	url := strings.Split(c.Request.URL.Path, ".")[0]
	url = fmt.Sprintf("%s%s", pubSet.rootURL(c.Request), url) //need use user own service set
	tileset := tileService.Mbtiles
	imgFormat := tileset.TileFormatString()
	out := map[string]interface{}{
		"tilejson": "2.1.0",
		"id":       tid,
		"scheme":   "xyz",
		"format":   imgFormat,
		"tiles":    []string{fmt.Sprintf("%s/{z}/{x}/{y}.%s", url, imgFormat)},
		"map":      url + "/",
	}
	metadata, err := tileset.GetInfo()
	if err != nil {
		log.Errorf("getTilejson, get metadata failed: %s; user: %s ^^", err, id)
		res.FailStr(c, fmt.Sprintf("get metadata failed : %s", err.Error()))
		return
	}
	for k, v := range metadata {
		switch k {
		// strip out values above
		case "tilejson", "id", "scheme", "format", "tiles", "map":
			continue

		// strip out values that are not supported or are overridden below
		case "grids", "interactivity", "modTime":
			continue

		// strip out values that come from TileMill but aren't useful here
		case "metatile", "scale", "autoscale", "_updated", "Layer", "Stylesheet":
			continue

		default:
			out[k] = v
		}
	}

	if tileset.HasUTFGrid() {
		out["grids"] = []string{fmt.Sprintf("%s/{z}/{x}/{y}.json", url)}
	}

	c.JSON(http.StatusOK, out)
}

func viewTile(c *gin.Context) {
	res := NewRes()
	tid := c.Param("tid")
	tileService, ok := pubSet.Tilesets[tid]
	if !ok {
		log.Errorf("tilesets id(%s) not exist in the service", tid)
		res.FailStr(c, "tilesets not exist in the service")
		return
	}

	c.HTML(http.StatusOK, "data.html", gin.H{
		"Title": "PerView",
		"ID":    tid,
		"URL":   strings.TrimSuffix(c.Request.URL.Path, "/"),
		"FMT":   tileService.Mbtiles.TileFormatString(),
	})
}

func getTile(c *gin.Context) {
	res := NewRes()
	// split path components to extract tile coordinates x, y and z
	pcs := strings.Split(c.Request.URL.Path[1:], "/")
	// we are expecting at least "tilesets", :user , :id, :z, :x, :y + .ext
	size := len(pcs)
	if size < 5 || pcs[4] == "" {
		res.FailStr(c, "request path is too short")
		return
	}
	tid := c.Param("tid")
	tileService, ok := pubSet.Tilesets[tid]
	if !ok {
		log.Errorf("tilesets id(%s) not exist in the service", tid)
		res.FailStr(c, "tilesets not exist in the service")
		return
	}

	tileset := tileService.Mbtiles

	z, x, y := pcs[size-3], pcs[size-2], pcs[size-1]
	tc, ext, err := tileCoordFromString(z, x, y)
	if err != nil {
		res.Fail(c, err)
		return
	}
	var data []byte
	// flip y to match the spec
	tc.y = (1 << uint64(tc.z)) - 1 - tc.y
	isGrid := ext == ".json"
	switch {
	case !isGrid:
		err = tileset.GetTile(tc.z, tc.x, tc.y, &data)
	case isGrid && tileset.HasUTFGrid():
		err = tileset.GetGrid(tc.z, tc.x, tc.y, &data)
	default:
		err = fmt.Errorf("no grid supplied by tile database")
	}
	if err != nil {
		// augment error info
		t := "tile"
		if isGrid {
			t = "grid"
		}
		err = fmt.Errorf("getTile, cannot fetch %s from DB for z=%d, x=%d, y=%d: %v", t, tc.z, tc.x, tc.y, err)
		log.Error(err)
		res.Fail(c, err)
		return
	}
	if data == nil || len(data) <= 1 {
		switch tileset.TileFormat() {
		case PNG, JPG, WEBP:
			// Return blank PNG for all image types
			c.Render(
				http.StatusOK, render.Data{
					ContentType: "image/png",
					Data:        BlankPNG(),
				})
		case PBF:
			// Return 204
			c.Writer.WriteHeader(http.StatusNoContent)
		default:
			c.Writer.Header().Set("Content-Type", "application/json")
			c.Writer.WriteHeader(http.StatusNotFound)
			fmt.Fprint(c.Writer, `{"message": "Tile does not exist"}`)
		}
	}

	if isGrid {
		c.Writer.Header().Set("Content-Type", "application/json")
		if tileset.UTFGridCompression() == ZLIB {
			c.Writer.Header().Set("Content-Encoding", "deflate")
		} else {
			c.Writer.Header().Set("Content-Encoding", "gzip")
		}
	} else {
		c.Writer.Header().Set("Content-Type", tileset.ContentType())
		if tileset.TileFormat() == PBF {
			c.Writer.Header().Set("Content-Encoding", "gzip")
		}
	}
	c.Writer.Write(data)
}

func renderDatasetsUpload(c *gin.Context) {
	id := c.GetString(identityKey)
	c.HTML(http.StatusOK, "upload-d.html", gin.H{
		"Title": "AtlasMap",
		"User":  id,
	})
}

func listDatasets(c *gin.Context) {
	c.JSON(http.StatusOK, pubSet.Datasets)
}

func uploadDataset(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	// style source
	file, err := c.FormFile("banks")
	if err != nil {
		log.Errorf(`uploadDataset, get form: %s; user: %s`, err, id)
		res.FailStr(c, fmt.Sprintf(`uploadDataset, get form: %s; user: %s`, err, id))
		return
	}
	tilesets := cfgV.GetString("assets.datasets")
	ext := filepath.Ext(file.Filename)
	name := strings.TrimSuffix(file.Filename, ext)
	did, _ := shortid.Generate()
	did = name + "." + did
	dst := filepath.Join(tilesets, did+ext)

	if err := c.SaveUploadedFile(file, dst); err != nil {
		log.Errorf(`uploadTileset, upload file: %s; user: %s`, err, id)
		res.FailStr(c, fmt.Sprintf(`uploadTileset, upload file: %s; user: %s`, err, id))
		return
	}
	absDst, _ := filepath.Abs(dst)
	//数据入库
	// bank := Bank{}
	// db.Create(&Bank{})
	sql := fmt.Sprintf(`COPY banks(num,name,state,region,type,admin,manager,house,area,term,time,staff,class,lat,lng) FROM '%s' DELIMITERS ',' CSV HEADER;`, absDst)
	log.Debug(sql)
	result := db.Exec(sql)
	if result.Error != nil {
		log.Errorf(result.Error.Error())
	}
	sql = `UPDATE banks	SET geom = ST_GeomFromText('POINT(' || lng || ' ' || lat || ')',4326);`
	result = db.Exec(sql)
	if result.Error != nil {
		log.Errorf(result.Error.Error())
	}
	//更新元数据
	pubSet.updateMeta(name, did)
	//更新服务
	err = pubSet.AddDataset(dst, did)
	if err != nil {
		log.Errorf(`uploadTileset, add mbtiles: %s ^^`, err)
	}
	c.JSON(http.StatusOK, gin.H{
		"did": did,
	})
}

func listFonts(c *gin.Context) {
	c.JSON(http.StatusOK, pubSet.Fonts)
}

//getGlyphs get glyph pbf
func getGlyphs(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	fonts := c.Param("fontstack")
	rgPBF := c.Param("range")
	rgPBF = strings.ToLower(rgPBF)
	rgPBFPat := `[\d]+-[\d]+.pbf$`
	if ok, _ := regexp.MatchString(rgPBFPat, rgPBF); !ok {
		log.Errorf("getGlyphs, range pattern error; range:%s; user:%s", rgPBF, id)
		res.FailStr(c, fmt.Sprintf("glyph range pattern error,range:%s", rgPBF))
		return
	}
	//should init first
	var fontsPath string
	var callbacks []string
	for k, v := range pubSet.Fonts {
		callbacks = append(callbacks, k)
		fontsPath = v.URL
	}
	fontsPath = filepath.Dir(fontsPath)
	pbfFile := getFontsPBF(fontsPath, fonts, rgPBF, callbacks)
	lastModified := time.Now().UTC().Format("2006-01-02 03:04:05 PM")
	c.Writer.Header().Set("Content-Type", "application/x-protobuf")
	c.Writer.Header().Set("Last-Modified", lastModified)
	c.Writer.Write(pbfFile)
}
