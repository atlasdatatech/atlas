package main

import (
	"bytes"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

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
		res.Fail(c, 4001)
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
		res.Fail(c, 4002)
		return
	}
	// attemptLogin
	user := User{}
	if db.Where("name = ?", body.Name).First(&user).RecordNotFound() {
		res.Fail(c, 4041)
		return
	}
	err = bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(body.Password))
	if err != nil {
		attempt := Attempt{IP: clientIP, Name: body.Name}
		db.Create(&attempt)
		res.Fail(c, 4011)
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
	res.DoneData(c, gin.H{
		"token":  user.JWT,
		"expire": user.Expires.Format(time.RFC3339),
		"user":   body.Name,
		"role":   casEnf.GetRolesForUser(body.Name),
	})
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

func renderAccount(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	user := &User{}
	if err := db.Where("name = ?", id).First(&user).Error; err != nil {
		res.FailMsg(c, fmt.Sprintf("renderAccount, get user info: %s; user: %s", err, id))
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
		res.FailMsg(c, fmt.Sprintf("renderAccount, get user info: %s; user: %s", err, id))
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
		log.Error(err)
		res.Fail(c, 4001)
		return
	}
	// validate
	if err := validate(body.Name, body.Password); err != nil {
		log.Error(err)
		res.Fail(c, 4012)
		return
	}
	user := User{}
	if err := db.Where("name = ?", body.Name).First(&user).Error; err != nil {
		if gorm.IsRecordNotFoundError(err) {
		} else {
			log.Error(err)
			res.Fail(c, 5001)
			return
		}
	}
	// duplicate UsernameCheck EmailCheck
	if len(user.Name) != 0 {
		if user.Name == body.Name {
			res.Fail(c, 4031)
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
		log.Error(err)
	}
	user.Activation = "yes"
	user.Search = []string{body.Name, body.Phone, body.Department}
	// insertUserInfo
	err = db.Create(&user).Error
	if err != nil {
		log.Error(err)
		res.Fail(c, 5001)
		return
	}
	//add to user_group
	res.DoneCode(c, 201)
}

func listUsers(c *gin.Context) {
	res := NewRes()
	var users []User
	db.Find(&users)
	res.DoneData(c, users)
}

func getUser(c *gin.Context) {
	res := NewRes()
	name := c.Param("id")
	if name == "" {
		name = c.GetString(identityKey)
	}
	user := &User{}
	if err := db.Where("name = ?", name).First(&user).Error; err != nil {
		if !gorm.IsRecordNotFoundError(err) {
			log.Error(err)
			res.Fail(c, 5001)
		}
		res.Fail(c, 4041)
		return
	}
	res.DoneData(c, user)
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
		log.Error(err)
		res.Fail(c, 4001)
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
		log.Error(err)
		res.Fail(c, 5001)
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
		log.Error(err)
		res.FailErr(c, err)
		return
	}
	if err := db.Model(&User{}).Where("name = ?", id).Update(User{JWT: tokenString, Expires: expire}).Error; err != nil {
		log.Error(err)
		res.Fail(c, 5001)
		return
	}
	_, err = c.Cookie("Token")
	if err != nil {
		log.Errorf("jwtRefresh, test cookie set: %s; user: %s", err, id)
	}

	res.DoneData(c, gin.H{
		"token":  tokenString,
		"expire": expire.Format(time.RFC3339),
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
		log.Error(err)
		res.Fail(c, 4001)
		return
	}

	// user.setPassword(body.Password)
	hashedPassword, _ := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	err = db.Model(&User{}).Where("name = ?", id).Update(User{Password: string(hashedPassword)}).Error
	if err != nil {
		log.Errorf("changePassword, update password: %s; user: %s", err, id)
		res.Fail(c, 5001)
		return
	}
	res.Done(c, "")
}

func deleteUser(c *gin.Context) {
	res := NewRes()
	uid := c.Param("id")
	if uid == "" {
		res.Fail(c, 4001)
		return
	}
	self := c.GetString(identityKey)
	if uid == self {
		res.FailMsg(c, "unable to delete yourself")
		return
	}
	casEnf.DeleteUser(uid)
	err := db.Where("name = ?", uid).Delete(&User{}).Error
	if err != nil {
		log.Error(err)
		res.Fail(c, 5001)
		return
	}

	res.Done(c, "")
}

func getUserRoles(c *gin.Context) {
	res := NewRes()
	uid := c.Param("id")
	if code := checkUser(uid); code != 200 {
		res.Fail(c, code)
		return
	}
	roles := casEnf.GetRolesForUser(uid)
	res.DoneData(c, roles)
}

func getUserMaps(c *gin.Context) {
	res := NewRes()
	id := c.Param("id")
	if code := checkUser(id); code != 200 {
		res.Fail(c, code)
		return
	}
	uperms := casEnf.GetPermissionsForUser(id)

	roles := casEnf.GetRolesForUser(id)
	for _, role := range roles {
		rperms := casEnf.GetPermissionsForUser(role)
		uperms = append(uperms, rperms...)
	}
	res.DoneData(c, uperms)
}

func addUserMap(c *gin.Context) {
	res := NewRes()
	id := c.Param("id")
	if code := checkUser(id); code != 200 {
		res.Fail(c, code)
		return
	}
	mid := c.Param("mid")
	if code := checkMap(mid); code != 200 {
		res.Fail(c, code)
		return
	}
	action := c.Param("action")
	if action == "" {
		res.Fail(c, 4001)
		return
	}

	if !casEnf.AddPolicy(id, mid, action) {
		res.Done(c, "policy already exist")
		return
	}
	res.Done(c, "")
	return
}

func deleteUserMap(c *gin.Context) {
	res := NewRes()
	id := c.Param("id")
	if code := checkUser(id); code != 200 {
		res.Fail(c, code)
		return
	}
	mid := c.Param("mid")
	if code := checkMap(mid); code != 200 {
		res.Fail(c, code)
		return
	}
	action := c.Param("action")
	if action == "" {
		res.Fail(c, 4001)
		return
	}
	if !casEnf.RemovePolicy(id, mid, action) {
		res.Done(c, "policy does not  exist")
		return
	}
	res.Done(c, "")
	return
}

func addUserRole(c *gin.Context) {
	res := NewRes()
	uid := c.Param("id")
	rid := c.Param("rid")

	if code := checkUser(uid); code != 200 {
		res.Fail(c, code)
		return
	}
	if code := checkRole(rid); code != 200 {
		res.Fail(c, code)
		return
	}

	if casEnf.AddRoleForUser(uid, rid) {
		res.Done(c, "")
		return
	}
	res.Done(c, fmt.Sprintf("%s already has %s role", uid, rid))
}

func deleteUserRole(c *gin.Context) {
	res := NewRes()
	uid := c.Param("id")
	rid := c.Param("rid")

	if code := checkUser(uid); code != 200 {
		res.Fail(c, code)
		return
	}
	if code := checkRole(rid); code != 200 {
		res.Fail(c, code)
		return
	}

	if casEnf.DeleteRoleForUser(uid, rid) {
		res.Done(c, "")
		return
	}
	res.Done(c, fmt.Sprintf("%s does not has %s role", uid, rid))
}

func getRoleMaps(c *gin.Context) {
	res := NewRes()
	id := c.Param("id")
	if code := checkRole(id); code != 200 {
		res.Fail(c, code)
		return
	}
	uperms := casEnf.GetPermissionsForUser(id)
	res.DoneData(c, uperms)
}

func addRoleMap(c *gin.Context) {
	res := NewRes()
	id := c.Param("id")
	if code := checkRole(id); code != 200 {
		res.Fail(c, code)
		return
	}
	mid := c.Param("mid")
	if code := checkMap(mid); code != 200 {
		res.Fail(c, code)
		return
	}
	action := c.Param("action")
	if action == "" {
		res.Fail(c, 4001)
		return
	}

	if !casEnf.AddPolicy(id, mid, action) {
		res.Done(c, "policy already exist")
		return
	}
	res.Done(c, "")
	return
}

func deleteRoleMap(c *gin.Context) {
	res := NewRes()
	id := c.Param("id")
	if code := checkRole(id); code != 200 {
		res.Fail(c, code)
		return
	}
	mid := c.Param("mid")
	if code := checkMap(mid); code != 200 {
		res.Fail(c, code)
		return
	}
	action := c.Param("action")
	if action == "" {
		res.Fail(c, 4001)
		return
	}
	if !casEnf.RemovePolicy(id, mid, action) {
		res.Done(c, "policy does not  exist")
		return
	}
	res.Done(c, "")
	return
}

func listRoles(c *gin.Context) {
	// 获取所有记录
	res := NewRes()
	var roles []Role
	db.Find(&roles)
	res.DoneData(c, roles)
}

func createRole(c *gin.Context) {
	res := NewRes()
	role := &Role{}
	err := c.Bind(role)
	if err != nil {
		res.Fail(c, 4001)
		return
	}
	err = db.Create(&role).Error
	if err != nil {
		log.Error(err)
		res.Fail(c, 5001)
		return
	}
	res.DoneCode(c, 201)
}

func deleteRole(c *gin.Context) {
	res := NewRes()
	rid := c.Param("id")
	if code := checkRole(rid); code != 200 {
		res.Fail(c, code)
		return
	}

	casEnf.DeleteRole(rid)
	err := db.Where("id = ?", rid).Delete(&Role{}).Error
	if err != nil {
		log.Errorf("deleteRole, delete role : %s; roleid: %s", err, rid)
		res.Fail(c, 5001)
		return
	}
	res.Done(c, "")
}

func getRoleUsers(c *gin.Context) {
	res := NewRes()
	rid := c.Param("id")
	if code := checkRole(rid); code != 200 {
		res.Fail(c, code)
		return
	}
	users := casEnf.GetUsersForRole(rid)
	res.DoneData(c, users)
}

func listMaps(c *gin.Context) {

	res := NewRes()
	id := c.GetString(identityKey)
	var maps []Map
	if id == "root" || casEnf.HasRoleForUser(id, "super") {
		db.Find(&maps)
		res.DoneData(c, maps)
		return
	}

	uperms := casEnf.GetPermissionsForUser(id)
	roles := casEnf.GetRolesForUser(id)
	for _, role := range roles {
		rperms := casEnf.GetPermissionsForUser(role)
		uperms = append(uperms, rperms...)
	}
	mapids := make(map[string]bool)
	for _, p := range uperms {
		if len(p) == 3 {
			mapids[p[1]] = true
		}
	}
	var ids []string
	for k := range mapids {
		ids = append(ids, k)
	}
	db.Where("id in (?)", ids).Find(&maps)
	res.DoneData(c, maps)
	return
}

func getMap(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	mid := c.Param("id")
	if mid == "" {
		res.Fail(c, 4001)
		return
	}
	if !casEnf.Enforce(id, mid, "GET") {
		res.Fail(c, 403)
		return
	}
	m := &Map{}
	if err := db.Where("id = ?", mid).First(&m).Error; err != nil {
		if !gorm.IsRecordNotFoundError(err) {
			log.Error(err)
			res.Fail(c, 5001)
		}
		res.Fail(c, 4043)
		return
	}
	res.DoneData(c, m)
}

func createMap(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	if id == "root" || casEnf.HasRoleForUser(id, "super") {
		m := &Map{}
		err := c.Bind(m)
		if err != nil {
			res.Fail(c, 4001)
			return
		}
		m.ID, _ = shortid.Generate()
		// insertUser
		err = db.Create(&m).Error
		if err != nil {
			log.Error(err)
			res.Fail(c, 5001)
			return
		}
		res.DoneData(c, gin.H{
			"id": m.ID,
		})
		return
	}
	res.Fail(c, 403)
	return
}

func saveMap(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	mid := c.Param("id")
	if mid == "" {
		res.Fail(c, 4001)
		return
	}
	m := &Map{}
	err := c.Bind(m)
	if err != nil {
		res.Fail(c, 4001)
		return
	}
	if !casEnf.Enforce(id, mid, "POST") {
		res.Fail(c, 403)
		return
	}
	m.ID = mid
	// insertUser
	err = db.Create(&m).Error
	if err != nil {
		log.Error(err)
		res.Fail(c, 5001)
		return
	}
	res.Done(c, "")
}

func updateMap(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	mid := c.Param("id")
	if mid == "" {
		res.Fail(c, 4001)
		return
	}
	if !casEnf.Enforce(id, mid, "PUT") {
		res.Fail(c, 403)
		return
	}
	m := &Map{}
	err := c.Bind(&m)
	if err != nil {
		log.Error(err)
		res.Fail(c, 4001)
		return
	}

	err = db.Model(&Map{}).Where("id = ?", mid).Update(m).Error
	if err != nil {
		log.Error(err)
		res.Fail(c, 5001)
		return
	}
	res.Done(c, "")
}

func deleteMap(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	mid := c.Param("id")
	if mid == "" {
		res.Fail(c, 4001)
		return
	}
	if !casEnf.Enforce(id, mid, "DELETE") {
		res.Fail(c, 403)
		return
	}
	if code := checkMap(mid); code != 200 {
		res.Fail(c, code)
		return
	}
	casEnf.RemoveFilteredPolicy(1, mid)
	err := db.Where("id = ?", mid).Delete(&Map{}).Error
	if err != nil {
		log.Errorf("deleteMap, delete map : %s; mapid: %s", err, mid)
		res.Fail(c, 5001)
		return
	}
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
	res := NewRes()
	res.DoneData(c, pubSet.Styles)
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
		res.Fail(c, 4045)
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
	pubSet.AddStyle(dst, sid)
	res.Done(c, "")
}

//updateStyle create a style
func updateStyle(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	sid := c.Param("sid")
	style, ok := pubSet.Styles[sid]
	if !ok {
		log.Errorf("style id(%s) not exist in the service", sid)
		res.Fail(c, 4044)
		return
	}

	body, err := ioutil.ReadAll(c.Request.Body)
	if err != nil {
		log.Errorf(`updateStyle, get form: %s; user: %s`, err, id)
		res.Fail(c, 5003)
		return
	}
	style.Style = body
	res.Done(c, "")
}

//saveStyle create a style
func saveStyle(c *gin.Context) {
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
	res.Done(c, "")
}

//getStyle get user style by id
func getStyle(c *gin.Context) {
	res := NewRes()
	sid := c.Param("sid")
	style, ok := pubSet.Styles[sid]
	if !ok {
		log.Errorf("getStyle, style not exist in the service, sid: %s ^^", sid)
		res.Fail(c, 4044)
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
		res.Fail(c, 4044)
		return
	}
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

	stylesPath := filepath.Dir(style.URL)
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
	_, ok := pubSet.Styles[sid]
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

//listTilesets list user's tilesets
func listTilesets(c *gin.Context) {
	res := NewRes()
	res.DoneData(c, pubSet.Tilesets)
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
		res.Fail(c, 4045)
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
		res.Fail(c, 5002)
		return
	}

	//更新服务
	err = pubSet.AddMBTile(dst, tid)
	if err != nil {
		log.Errorf(`uploadTileset, add mbtiles: %s ^^`, err)
	}

	res.DoneData(c, gin.H{
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
		res.Fail(c, 4044)
		return
	}
	urlPath := c.Request.URL.Path
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
		res.Fail(c, 5004)
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
		res.Fail(c, 4044)
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
		res.Fail(c, 4003)
		return
	}
	tid := c.Param("tid")
	tileService, ok := pubSet.Tilesets[tid]
	if !ok {
		log.Errorf("tilesets id(%s) not exist in the service", tid)
		res.Fail(c, 4044)
		return
	}

	tileset := tileService.Mbtiles

	z, x, y := pcs[size-3], pcs[size-2], pcs[size-1]
	tc, ext, err := tileCoordFromString(z, x, y)
	if err != nil {
		log.Error(err)
		res.Fail(c, 4003)
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
		res.Fail(c, 5004)
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
	// res := NewRes()
	// res.DoneData(c, pubSet.Datasets)

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  codes[200],
		"data": gin.H{
			"zhuantitu": []string{"banks", "others", "pois"},
			"relitu":    []string{"banks", "others", "pois"},
			"fushequan": []string{"banks"},
			"moxing":    []string{"savings", "m1", "m2", "m3", "m4"},
		},
	})
}

func importDataset(c *gin.Context) {
	res := NewRes()
	name := c.Param("name")

	file, err := c.FormFile(name)
	if err != nil {
		log.Errorf(`importDataset, get form: %s; file: %s`, err, name)
		res.Fail(c, 4045)
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
		res.Fail(c, 5002)
		return
	}
	buf, err := ioutil.ReadFile(dst)
	if err != nil {
		log.Errorf(`importDataset, csv reader failed: %s; file: %s`, err, name)
		res.Fail(c, 5003)
		return
	}
	reader := csv.NewReader(bytes.NewReader(buf))
	csvHeader, err := reader.Read()
	if err != nil {
		log.Errorf(`importDataset, csv reader failed: %s; file: %s`, err, name)
		res.Fail(c, 5003)
		return
	}

	row2values := func(row []string, cols []*sql.ColumnType) string {
		var vals string
		for i, col := range cols {
			// fmt.Println(i, col.DatabaseTypeName(), col.Name())
			switch col.DatabaseTypeName() {
			case "INT", "INT4", "NUMERIC": //number
				if "" == row[i] {
					vals = vals + "null,"
				} else {
					vals = vals + row[i] + ","
				}
			default: //string->"TEXT" "VARCHAR","BOOL",datetime->"TIMESTAMPTZ",pq.StringArray->"_VARCHAR"
				vals = vals + "'" + row[i] + "',"
			}
		}
		vals = strings.TrimSuffix(vals, ",")
		return vals
	}

	clear := func(name string) error {
		s := fmt.Sprintf(`DELETE FROM %s;`, name)
		return db.Exec(s).Error
	}
	insert := func(header string) error {
		if len(strings.Split(header, ",")) != len(csvHeader) {
			log.Errorf("the cvs file format error, file:%s,  should be:%s", name, header)
			return fmt.Errorf("the cvs file format error, file:%s", name)
		}

		s := fmt.Sprintf("SELECT %s FROM %s LIMIT 0", header, name)
		rows, err := db.Raw(s).Rows() // (*sql.Rows, error)
		if err != nil {
			return err
		}
		defer rows.Close()
		cols, err := rows.ColumnTypes()
		if err != nil {
			return err
		}
		var vals []string
		for {
			row, err := reader.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			rval := row2values(row, cols)
			vals = append(vals, fmt.Sprintf(`(%s)`, rval))
		}
		s = fmt.Sprintf(`INSERT INTO %s (%s) VALUES %s;`, name, header, strings.Join(vals, ",")) // ON CONFLICT (id) DO UPDATE SET (%s) = (%s)
		return db.Exec(s).Error
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
		err = clear(name)
		if err != nil {
			log.Error(err)
			res.Fail(c, 5001)
			return
		}
		err = insert(header)
		if err != nil {
			log.Errorf("import %s error:%s", name, err.Error())
			res.Fail(c, 5001)
			return
		}
		update := fmt.Sprintf(`UPDATE %s SET geom = ST_GeomFromText('POINT(' || lat || ' ' || lng || ')',4326)%s;`, name, search)
		result := db.Exec(update)
		if result.Error != nil {
			log.Errorf("update %s create geom error:%s", name, result.Error.Error())
			res.Fail(c, 5001)
			return
		}
		//更新元数据
		pubSet.updateMeta(name, id)
		//更新服务
		err = pubSet.AddDataset(dst, id)
		if err != nil {
			log.Errorf(`importDataset, add %s to dataset service: %s ^^`, name, err)
		}
		res.DoneData(c, gin.H{
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
		err = clear(name)
		if err != nil {
			log.Error(err)
			res.Fail(c, 5001)
			return
		}
		err = insert(header)
		if err != nil {
			log.Errorf("import %s error:%s", name, err.Error())
			res.Fail(c, 5001)
			return
		}
		res.Done(c, "")
	}
}

// func queryDataset(c *gin.Context) {
// 	res := NewRes()
// 	name := c.Param("name")

// 	var body struct {
// 		GeoJSON string `form:"geojson" binding:"required"`
// 		Filter  string `form:"filter"`
// 	}
// 	err := c.Bind(&body)
// 	if err != nil {
// 		res.Fail(c, err)
// 		return
// 	}

// 	var jg map[string]interface{}
// 	err = json.Unmarshal([]byte(body.GeoJSON), &jg)
// 	if err != nil || jg["geometry"] == nil {
// 		res.FailMsg(c, "param geojson format error")
// 		return
// 	}

// 	qf, err := geojson.UnmarshalFeature([]byte(body.GeoJSON))
// 	if err != nil {
// 		log.Errorf("param geojson error: %v", err)
// 		res.Fail(c, err)
// 		return
// 	}

// 	var fields []string
// 	for k := range qf.Properties {
// 		fields = append(fields, k)
// 	}
// 	var fieldsStr string
// 	fieldsStr = strings.Join(fields, ",")
// 	selStr := "st_asbinary(geom) as geom "
// 	if "" != fieldsStr {
// 		selStr = selStr + "," + fieldsStr
// 	}
// 	whrStr := "geom && st_geomfromwkb($1)"
// 	if "" != body.Filter {
// 		whrStr = whrStr + " AND " + body.Filter
// 	}
// 	s := fmt.Sprintf(`SELECT %s FROM %s WHERE %s;`, selStr, name, whrStr)
// 	log.Debugln(s)
// 	var rows *sql.Rows
// 	if qf.BBox.Valid() {
// 		rows, err = db.Raw(s, wkb.Value(qf.BBox.Bound())).Rows() // (*sql.Rows, error)
// 	} else {
// 		rows, err = db.Raw(s, wkb.Value(qf.Geometry)).Rows() // (*sql.Rows, error)
// 	}

// 	if err != nil {
// 		res.Fail(c, err)
// 		return
// 	}
// 	defer rows.Close()
// 	cols, err := rows.ColumnTypes()
// 	if err != nil {
// 		res.Fail(c, err)
// 		return
// 	}
// 	fc := geojson.NewFeatureCollection()
// 	for rows.Next() {
// 		// Scan needs an array of pointers to the values it is setting
// 		// This creates the object and sets the values correctly
// 		vals := make([]interface{}, len(cols))
// 		for i := range cols {
// 			vals[i] = new(sql.RawBytes)
// 		}
// 		err = rows.Scan(vals...)
// 		if err != nil {
// 			log.Error(err)
// 		}

// 		f := geojson.NewFeature(orb.Point{0, 0})
// 		f.Properties = qf.Properties.Clone()

// 		for i, t := range cols {
// 			// skip nil values.
// 			if vals[i] == nil {
// 				continue
// 			}
// 			rb, ok := vals[i].(*sql.RawBytes)
// 			if !ok {
// 				log.Errorf("Cannot convert index %d column %s to type *sql.RawBytes", i, t.Name())
// 				continue
// 			}
// 			log.Debugf("%d,%v,%v", i, *t, *rb)

// 			switch t.Name() {
// 			case "geom":
// 				pt := orb.Point{0, 0}
// 				s := wkb.Scanner(&pt)
// 				err := s.Scan([]byte(*rb))
// 				if err != nil {
// 					log.Errorf("unable to convert geometry field (geom) into bytes.")
// 					log.Error(err)
// 				}
// 				f.Geometry = pt
// 			default:
// 				switch vex := t.ScanType().(type) {
// 				default:
// 					log.Debug(vex)
// 					f.Properties[t.Name()] = string(*rb)
// 				}
// 			}

// 		}
// 		fc.Append(f)
// 	}
// 	gj, err := fc.MarshalJSON()
// 	if err != nil {
// 		log.Errorf("unable to MarshalJSON of featureclection.")
// 	}
// 	c.JSON(http.StatusOK, json.RawMessage(gj))
// }

func queryDatasetGeojson(c *gin.Context) {
	res := NewRes()
	name := c.Param("name")

	var body struct {
		GeoJSON string `form:"geojson" binding:"required"`
		Filter  string `form:"filter"`
	}
	err := c.Bind(&body)
	if err != nil {
		res.Fail(c, 4001)
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
		log.Error(err)
		res.Fail(c, 4001)
		return
	}

	var fields []string

	switch geoj.Type {
	case "Feature":
		//props
		var tags map[string]interface{}
		if err := json.Unmarshal(props, &tags); err != nil {
			res.Fail(c, 4001)
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
	rows, err := db.Raw(s).Rows() // (*sql.Rows, error)
	if err != nil {
		log.Error(err)
		res.Fail(c, 5001)
		return
	}
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil {
		res.Fail(c, 5001)
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
		res.FailMsg(c, "unable to MarshalJSON of featureclection.")
		return
	}
	res.DoneData(c, json.RawMessage(gj))
}

func queryExec(c *gin.Context) {
	res := NewRes()
	var body struct {
		SQL string `form:"sql" json:"sql" binding:"required"`
	}
	err := c.Bind(&body)
	if err != nil {
		res.Fail(c, 4001)
		return
	}
	rows, err := db.Raw(body.SQL).Rows() // (*sql.Rows, error)
	if err != nil {
		log.Error(err)
		res.Fail(c, 5001)
		return
	}
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil {
		log.Error(err)
		res.Fail(c, 5001)
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
	res.DoneData(c, t)
}

func listFonts(c *gin.Context) {
	res := NewRes()
	res.DoneData(c, pubSet.Fonts)
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
		res.Fail(c, 4005)
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
