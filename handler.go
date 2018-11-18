package main

import (
	"bytes"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/encoding/wkb"
	"github.com/paulmach/orb/geojson"
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
	//add to user_group
	res.Done(c, "")
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
	user := &User{}
	db.Select("search").Where("name=?", name).First(user)
	if body.Phone == "" && len(user.Search) == 3 {
		body.Phone = user.Search[1]
	}
	if body.Department == "" && len(user.Search) == 3 {
		body.Department = user.Search[2]
	}
	search := []string{name, body.Phone, body.Department} //更新搜索字段
	err = db.Model(&User{}).Where("name = ?", name).Update(User{Phone: body.Phone, Department: body.Department, Search: search}).Error
	if err != nil {
		log.Errorf("updateUser, update user info: %s; user: %s", err, name)
		res.Fail(c, err)
		return
	}
	res.Done(c, "")
}

func deleteUser(c *gin.Context) {
	res := NewRes()
	uid := c.Param("id")
	self := c.GetString(identityKey)
	if uid == self {
		res.FailStr(c, "unable to delete yourself")
		return
	}
	casEnf.DeleteUser(uid)
	err := db.Where("name = ?", uid).Delete(&User{}).Error
	if err != nil {
		log.Errorf("deleteUser, delete user : %s; user: %s", err, uid)
		res.Fail(c, err)
		return
	}

	res.Done(c, "")
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
	res.Done(c, "")
}

func getUserRoles(c *gin.Context) {
	uid := c.Param("id")
	roles := casEnf.GetRolesForUser(uid)
	c.JSON(http.StatusOK, roles)
}

func addUserRole(c *gin.Context) {
	res := NewRes()
	uid := c.Param("id")
	rid := c.Param("rid")
	if rid == "" || uid == "" {
		res.FailStr(c, "rid and uid must not null")
		return
	}
	if casEnf.AddRoleForUser(uid, rid) {
		res.Done(c, "")
		return
	}
	res.FailStr(c, fmt.Sprintf("the user->%s already has the role->%s", uid, rid))
}

func deleteUserRole(c *gin.Context) {
	res := NewRes()
	uid := c.Param("id")
	rid := c.Param("rid")
	if rid == "" || uid == "" {
		res.FailStr(c, "rid and uid must not null")
		return
	}
	if casEnf.DeleteRoleForUser(uid, rid) {
		res.Done(c, "")
		return
	}
	res.FailStr(c, fmt.Sprintf("the user->%s does not has the role->%s", uid, rid))
}

func getPermissions(c *gin.Context) {
	res := NewRes()
	id := c.Param("id")
	if id == "" {
		res.FailStr(c, "id must not null")
		return
	}
	uperms := casEnf.GetPermissionsForUser(id)
	c.JSON(http.StatusOK, uperms)
}

func addPolicy(c *gin.Context) {
	res := NewRes()
	id := c.Param("id")
	var body struct {
		URL    string `form:"url" json:"url" binding:"required"`
		Action string `form:"action" json:"action" binding:"required"`
	}
	err := c.Bind(&body)
	if err != nil {
		res.Fail(c, err)
		return
	}
	if !casEnf.AddPolicy(id, body.URL, body.Action) {
		res.Done(c, "policy already exist")
		return
	}
	res.Done(c, "")
}

func deletePermissions(c *gin.Context) {
	res := NewRes()
	id := c.Param("id")
	aid := c.Param("aid")
	action := c.Param("action")
	if id == "" || aid == "" || action == "" {
		res.FailStr(c, "asset id can not be nil")
		return
	}

	if !casEnf.RemovePolicy(id, aid, action) {
		res.Done(c, "policy does not  exist")
		return
	}
	res.Done(c, "")
}

func getRoleUsers(c *gin.Context) {
	res := NewRes()
	rid := c.Param("id")
	if rid == "" {
		res.FailStr(c, "rid must not null")
		return
	}
	users := casEnf.GetUsersForRole(rid)
	c.JSON(http.StatusOK, users)
}

func listRoles(c *gin.Context) {
	// 获取所有记录
	var roles []Role
	db.Find(&roles)
	c.JSON(http.StatusOK, roles)
}

func createRole(c *gin.Context) {
	res := NewRes()
	role := &Role{}
	err := c.Bind(role)
	if err != nil {
		res.Fail(c, err)
		return
	}
	err = db.Create(&role).Error
	if err != nil {
		res.Fail(c, err)
		return
	}
	res.Done(c, "")
}

func deleteRole(c *gin.Context) {
	res := NewRes()
	rid := c.Param("id")
	if rid == "" {
		res.FailStr(c, "asset id can not be nil")
		return
	}
	casEnf.DeleteRole(rid)
	err := db.Where("id = ?", rid).Delete(&Role{}).Error
	if err != nil {
		log.Errorf("deleteRole, delete role : %s; roleid: %s", err, rid)
		res.Fail(c, err)
		return
	}
	res.Done(c, "")
}

func listAssets(c *gin.Context) {
	// 获取所有记录
	var assets []Asset
	db.Find(&assets)
	c.JSON(http.StatusOK, assets)
}

func createAsset(c *gin.Context) {
	res := NewRes()
	asset := &Asset{}
	err := c.Bind(asset)
	if err != nil {
		res.Fail(c, err)
		return
	}
	asset.ID, _ = shortid.Generate()
	// insertUser
	err = db.Create(&asset).Error
	if err != nil {
		res.Fail(c, err)
		return
	}
	c.JSON(http.StatusOK, asset)
}

func deleteAsset(c *gin.Context) {
	res := NewRes()
	aid := c.Param("aid")
	if aid == "" {
		res.FailStr(c, "asset id can not be nil")
		return
	}
	asset := &Asset{}
	err := db.Where("id = ?", aid).Find(asset).Error
	if err != nil {
		log.Errorf("deleteAsset, delete asset : %s; assetid: %s", err, aid)
		res.Fail(c, err)
		return
	}
	casEnf.RemoveFilteredPolicy(1, asset.URL)
	casEnf.RemoveFilteredNamedGroupingPolicy("g2", 0, asset.URL)

	err = db.Where("id = ?", aid).Delete(&Asset{}).Error
	if err != nil {
		log.Errorf("deleteAsset, delete asset : %s; assetid: %s", err, aid)
		res.Fail(c, err)
		return
	}
	res.Done(c, "")
}

func listAssetGroups(c *gin.Context) {
	// 获取所有记录
	var ags []AssetGroup
	db.Find(&ags)
	c.JSON(http.StatusOK, ags)
}

func createAssetGroup(c *gin.Context) {
	res := NewRes()
	ag := &AssetGroup{}
	err := c.Bind(ag)
	if err != nil {
		res.Fail(c, err)
		return
	}
	err = db.Create(&ag).Error
	if err != nil {
		res.Fail(c, err)
		return
	}
	res.Done(c, "")
}

func deleteAssetGroup(c *gin.Context) {
	res := NewRes()
	gid := c.Param("id")
	if gid == "" {
		res.FailStr(c, "asset id can not be nil")
		return
	}
	casEnf.RemoveFilteredNamedGroupingPolicy("g2", 1, gid)
	err := db.Where("id = ?", gid).Delete(&AssetGroup{}).Error
	if err != nil {
		log.Errorf("deleteAssetGroup, delete asset group : %s; groupid: %s", err, gid)
		res.Fail(c, err)
		return
	}
	res.Done(c, "")
}

func getGroupAssets(c *gin.Context) {
	res := NewRes()
	gid := c.Param("id")
	if gid == "" {
		res.FailStr(c, "group id can not be nil")
		return
	}
	p := casEnf.GetFilteredNamedGroupingPolicy("g2", 1, gid)
	var assets []string
	for _, v := range p {
		if len(v) > 0 {
			assets = append(assets, v[0])
		}
	}
	c.JSON(http.StatusOK, assets)
}

func addGroupAsset(c *gin.Context) {
	res := NewRes()
	gid := c.Param("id")
	aid := c.Param("aid")
	if gid == "" || aid == "" {
		res.FailStr(c, "group or asset id can not be nil")
		return
	}
	asset := &Asset{}
	if err := db.Where("id = ?", aid).First(&asset).Error; err != nil {
		if gorm.IsRecordNotFoundError(err) {
			if !casEnf.AddNamedGroupingPolicy("g2", aid, gid) {
				res.Done(c, fmt.Sprintf("asset %s already in group %s", aid, gid))
				return
			}
		}
		return
	}

	if !casEnf.AddNamedGroupingPolicy("g2", asset.URL, gid) {
		res.Done(c, fmt.Sprintf("asset %s already in group %s", aid, gid))
		return
	}
	res.Done(c, "")
}

func deleteGroupAsset(c *gin.Context) {
	// c.JSON(http.StatusOK, "deving")
	res := NewRes()
	gid := c.Param("id")
	aid := c.Param("aid")
	if gid == "" || aid == "" {
		res.FailStr(c, "group or asset id can not be nil")
		return
	}
	asset := &Asset{}
	if err := db.Where("id = ?", aid).First(&asset).Error; err != nil {
		res.FailStr(c, fmt.Sprintf("addGroupAsset, get asset url: %s; assetid: %s", err, aid))
		if !gorm.IsRecordNotFoundError(err) {
			log.Errorf("addGroupAsset, get asset info: %s; assetid: %s", err, aid)
		}
		return
	}

	if !casEnf.RemoveNamedGroupingPolicy("g2", asset.URL, gid) {
		res.Done(c, fmt.Sprintf("asset %s does not in group %s", aid, gid))
		return
	}
	res.Done(c, "")
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
	res.Done(c, "")
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

	res.Done(c, "")
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

	res.Done(c, "")
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
	urlPath := c.Request.URL.Path
	// ext := filepath.Ext(urlPath)
	// path := strings.TrimSuffix(urlPath, ext)
	url := fmt.Sprintf("%s%s", pubSet.rootURL(c.Request), urlPath) //need use user own service set
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

func importDataset(c *gin.Context) {
	res := NewRes()
	name := c.Param("name")

	file, err := c.FormFile(name)
	if err != nil {
		log.Errorf(`importDataset, get form: %s; file: %s`, err, name)
		res.FailStr(c, fmt.Sprintf(`importDataset, get form: %s; file: %s`, err, name))
		return
	}

	tilesets := cfgV.GetString("assets.datasets")
	ext := filepath.Ext(file.Filename)
	name = strings.TrimSuffix(file.Filename, ext)
	id, _ := shortid.Generate()
	id = name + "." + id
	dst := filepath.Join(tilesets, id+ext)

	if err := c.SaveUploadedFile(file, dst); err != nil {
		log.Errorf(`importDataset, upload file: %s; file: %s`, err, name)
		res.FailStr(c, fmt.Sprintf(`importDataset, upload file: %s; file: %s`, err, name))
		return
	}
	absDst, _ := filepath.Abs(dst)
	buf, err := ioutil.ReadFile(dst)
	if err != nil {
		log.Errorf(`importDataset, csv reader failed: %s; file: %s`, err, name)
		res.FailStr(c, fmt.Sprintf(`importDataset, csv reader failed: %s; file: %s`, err, name))
		return
	}
	reader := csv.NewReader(bytes.NewReader(buf))
	csvHeader, err := reader.Read()
	if err != nil {
		log.Errorf(`importDataset, csv reader failed: %s; file: %s`, err, name)
		res.FailStr(c, fmt.Sprintf(`importDataset, csv reader failed: %s; file: %s`, err, name))
		return
	}
	//数据入库
	var header, search string
	switch name {
	case "banks", "others", "basepois", "pois":
		switch name {
		case "banks":
			header = "id,name,state,region,type,admin,manager,house,area,term,date,staff,class,lat,lng"
			search = ",search =ARRAY[id,name,region,manager]"
		case "others":
			header = "id,name,class,address,lat,lng"
			search = ",search =ARRAY[id,name,class,address]"
		case "basepois":
			header = "name,class,lat,lng"
		case "pois":
			header = "name,class,type,hit,per,area,households,date,lat,lng"
			search = ",search =ARRAY[name]"
		}
		// header != strings.Join(csvHeader, ",") {
		if len(strings.Split(header, ",")) != len(csvHeader) {
			log.Errorf("the cvs file format error, file:%s,  should be:%s", name, header)
			res.FailStr(c, fmt.Sprintf("the cvs file format error, file:%s", name))
			return
		}
		sql := fmt.Sprintf(`COPY %s(%s) FROM '%s' DELIMITERS ',' CSV HEADER;`, name, header, absDst)
		//clear
		clear := fmt.Sprintf(`DELETE FROM %s;`, name)
		db.Exec(clear)
		result := db.Exec(sql)
		if result.Error != nil {
			log.Errorf("import %s error:%s", name, result.Error.Error())
			res.FailStr(c, fmt.Sprintf("import %s error:%s", name, result.Error.Error()))
			return
		}
		update := fmt.Sprintf(`UPDATE %s SET geom = ST_GeomFromText('POINT(' || lat || ' ' || lng || ')',4326)%s;`, name, search)
		result = db.Exec(update)
		if result.Error != nil {
			log.Errorf("update %s create geom error:%s", name, result.Error.Error())
			res.FailStr(c, fmt.Sprintf("update %s create geom error:%s", name, result.Error.Error()))
			return
		}
		//更新元数据
		pubSet.updateMeta(name, id)
		//更新服务
		err = pubSet.AddDataset(dst, id)
		if err != nil {
			log.Errorf(`importDataset, add %s to dataset service: %s ^^`, name, err)
			res.FailStr(c, fmt.Sprintf(`importDataset, add %s to dataset service: %s ^^`, name, err))
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"id": id,
		})
	case "savings", "m1", "m2", "m3", "m4":
		switch name {
		case "savings":
			header = "id,year,total,corporate,personal,margin,other"
		case "m1":
			header = "id,c1,c2,c3,c4,c5,c6,c7,c8,c9,c10,c11,c12,c13,c14,c15,c16,result"
		case "m2":
			header = "id,count,number,result"
		case "m3":
			header = "name,weight"
		case "m4":
			header = "region,gdp,population,area,price,cusume,industrial,saving,loan"
		}
		// header != strings.Join(csvHeader, ",") {
		if len(strings.Split(header, ",")) != len(csvHeader) {
			log.Errorf("the cvs file format error, file:%s,  should be:%s", name, header)
			res.FailStr(c, fmt.Sprintf("the cvs file format error, file:%s", name))
			return
		}
		sql := fmt.Sprintf(`COPY %s(%s) FROM '%s' DELIMITERS ',' CSV HEADER;`, name, header, absDst)
		result := db.Exec(sql)
		if result.Error != nil {
			log.Errorf("import %s error:%s", name, result.Error.Error())
			res.FailStr(c, fmt.Sprintf("import %s error:%s", name, result.Error.Error()))
			return
		}
		res.Done(c, "")
	}
}

func queryDataset(c *gin.Context) {
	res := NewRes()
	name := c.Param("name")

	var body struct {
		GeoJSON string `form:"geojson" binding:"required"`
		Filter  string `form:"filter"`
	}
	err := c.Bind(&body)
	if err != nil {
		res.Fail(c, err)
		return
	}

	var jg map[string]interface{}
	err = json.Unmarshal([]byte(body.GeoJSON), &jg)
	if err != nil || jg["geometry"] == nil {
		res.FailStr(c, "param geojson format error")
		return
	}

	qf, err := geojson.UnmarshalFeature([]byte(body.GeoJSON))
	if err != nil {
		log.Errorf("param geojson error: %v", err)
		res.Fail(c, err)
		return
	}

	var fields []string
	for k := range qf.Properties {
		fields = append(fields, k)
	}
	var fieldsStr string
	fieldsStr = strings.Join(fields, ",")
	selStr := "st_asbinary(geom) as geom "
	if "" != fieldsStr {
		selStr = selStr + "," + fieldsStr
	}
	whrStr := "geom && st_geomfromwkb($1)"
	if "" != body.Filter {
		whrStr = whrStr + " AND " + body.Filter
	}
	s := fmt.Sprintf(`SELECT %s FROM %s WHERE %s;`, selStr, name, whrStr)
	log.Debugln(s)
	var rows *sql.Rows
	if qf.BBox.Valid() {
		rows, err = db.Raw(s, wkb.Value(qf.BBox.Bound())).Rows() // (*sql.Rows, error)
	} else {
		rows, err = db.Raw(s, wkb.Value(qf.Geometry)).Rows() // (*sql.Rows, error)
	}

	if err != nil {
		res.Fail(c, err)
		return
	}
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil {
		res.Fail(c, err)
		return
	}
	fc := geojson.NewFeatureCollection()
	for rows.Next() {
		// Scan needs an array of pointers to the values it is setting
		// This creates the object and sets the values correctly
		vals := make([]interface{}, len(cols))
		for i := range cols {
			vals[i] = new(sql.RawBytes)
		}
		err = rows.Scan(vals...)
		if err != nil {
			log.Error(err)
		}

		f := geojson.NewFeature(orb.Point{0, 0})
		f.Properties = qf.Properties.Clone()

		for i, t := range cols {
			// skip nil values.
			if vals[i] == nil {
				continue
			}
			rb, ok := vals[i].(*sql.RawBytes)
			if !ok {
				log.Errorf("Cannot convert index %d column %s to type *sql.RawBytes", i, t.Name())
				continue
			}
			log.Debugf("%d,%v,%v", i, *t, *rb)

			switch t.Name() {
			case "geom":
				pt := orb.Point{0, 0}
				s := wkb.Scanner(&pt)
				err := s.Scan([]byte(*rb))
				if err != nil {
					log.Errorf("unable to convert geometry field (geom) into bytes.")
					log.Error(err)
				}
				f.Geometry = pt
			default:
				switch vex := t.ScanType().(type) {
				default:
					log.Debug(vex)
					f.Properties[t.Name()] = string(*rb)
				}
			}

		}
		fc.Append(f)
	}
	gj, err := fc.MarshalJSON()
	if err != nil {
		log.Errorf("unable to MarshalJSON of featureclection.")
	}
	c.JSON(http.StatusOK, json.RawMessage(gj))
}

func queryDatasetGeojson(c *gin.Context) {
	res := NewRes()
	name := c.Param("name")

	var body struct {
		GeoJSON string `form:"geojson" binding:"required"`
		Filter  string `form:"filter"`
	}
	err := c.Bind(&body)
	if err != nil {
		res.Fail(c, err)
		return
	}

	type GeoJSON struct {
		Type       string          `json:"type"`
		BBox       []float64       `json:"bbox"`
		Geometry   json.RawMessage `json:"geometry"`
		Properties interface{}     `json:"properties"`
	}

	var props json.RawMessage
	geoj := GeoJSON{
		Properties: &props,
	}
	err = json.Unmarshal([]byte(body.GeoJSON), &geoj)

	if err != nil {
		res.Fail(c, err)
		return
	}

	var fields []string

	switch geoj.Type {
	case "Feature":
		//props
		var tags map[string]interface{}
		if err := json.Unmarshal(props, &tags); err != nil {
			res.Fail(c, err)
			return
		}

		for k, v := range tags {
			switch v.(type) {
			case string:
			case float64:
			case bool:
			default:
				log.Errorf("now not support interface{} objects,deling as strings, key:%s,value:%v", k, v)
			}
			fields = append(fields, k)
		}
	default:
		log.Errorf("geojson type is not : %q", geoj.Type)
	}

	var fieldsStr string
	fieldsStr = strings.Join(fields, ",")
	selStr := "st_asgeojson(geom) as geom "
	if "" != fieldsStr {
		selStr = selStr + "," + fieldsStr
	}
	var s string
	if len(geoj.BBox) == 4 {
		whrStr := fmt.Sprintf("geom && st_makeenvelope(%f,%f,%f,%f)", geoj.BBox[0], geoj.BBox[1], geoj.BBox[2], geoj.BBox[3])
		if "" != body.Filter {
			whrStr = whrStr + " AND " + body.Filter
		}
		s = fmt.Sprintf(`SELECT %s FROM %s WHERE %s;`, selStr, name, whrStr)
	} else {
		whrStr := fmt.Sprintf(`geom && st_geomfromgeojson('%s')`, geoj.Geometry)
		if "" != body.Filter {
			whrStr = whrStr + " AND " + body.Filter
		}
		s = fmt.Sprintf(`SELECT %s FROM %s WHERE %s;`, selStr, name, whrStr)
	}
	log.Debug(s)
	rows, err := db.Raw(s).Rows() // (*sql.Rows, error)
	if err != nil {
		res.Fail(c, err)
		return
	}
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil {
		res.Fail(c, err)
		return
	}
	fc := geojson.NewFeatureCollection()
	for rows.Next() {
		// Scan needs an array of pointers to the values it is setting
		// This creates the object and sets the values correctly
		vals := make([]interface{}, len(cols))
		for i := range cols {
			vals[i] = new(sql.RawBytes)
		}
		err = rows.Scan(vals...)
		if err != nil {
			log.Error(err)
		}

		var f *geojson.Feature

		for i, t := range cols {
			// skip nil values.
			if vals[i] == nil {
				continue
			}
			rb, ok := vals[i].(*sql.RawBytes)
			if !ok {
				log.Errorf("Cannot convert index %d column %s to type *sql.RawBytes", i, t.Name())
				continue
			}

			switch t.Name() {
			case "geom":
				geom, err := geojson.UnmarshalGeometry([]byte(*rb))
				if err != nil {
					log.Errorf("UnmarshalGeometry from geojson result error, index %d column %s", i, t.Name())
					continue
				}
				f = geojson.NewFeature(geom.Geometry())
			default:
				f.Properties[t.Name()] = string(*rb)
			}

		}
		fc.Append(f)
	}
	gj, err := fc.MarshalJSON()
	if err != nil {
		log.Errorf("unable to MarshalJSON of featureclection.")
	}
	c.JSON(http.StatusOK, json.RawMessage(gj))
}

func queryExec(c *gin.Context) {
	res := NewRes()
	s := c.Param("sql")
	if "" == s {
		res.FailStr(c, "sql can not be null")
		return
	}
	rows, err := db.Raw(s).Rows() // (*sql.Rows, error)
	if err != nil {
		res.Fail(c, err)
		return
	}
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil {
		res.Fail(c, err)
		return
	}
	var t [][]string
	for rows.Next() {
		// Scan needs an array of pointers to the values it is setting
		// This creates the object and sets the values correctly
		vals := make([]sql.RawBytes, len(cols))
		valsScer := make([]interface{}, len(vals))
		for i := range vals {
			valsScer[i] = &vals[i]
		}
		err = rows.Scan(valsScer...)
		if err != nil {
			log.Error(err)
		}
		var r []string
		for _, col := range vals {
			// skip nil values.
			if col == nil {
				continue
			}
			r = append(r, string(col))
		}
		t = append(t, r)
	}
	c.JSON(http.StatusOK, t)
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
