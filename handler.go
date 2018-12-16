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
	"strconv"
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

	var pers []MapPerm
	for _, perm := range uperms {
		m := &Map{}
		db.Where("id = ?", perm[1]).First(&m)
		p := MapPerm{
			ID:      perm[0],
			MapID:   perm[1],
			MapName: m.Title,
			Action:  perm[2],
		}
		pers = append(pers, p)
	}
	res.DoneData(c, pers)
}

func addUserMap(c *gin.Context) {
	res := NewRes()
	id := c.Param("id")
	if code := checkUser(id); code != 200 {
		res.Fail(c, code)
		return
	}
	var body struct {
		MID    string `form:"mid" json:"mid" binding:"required"`
		Action string `form:"action" json:"action" binding:"required"`
	}
	err := c.Bind(&body)
	if err != nil {
		res.Fail(c, 4001)
		return
	}
	if code := checkMap(body.MID); code != 200 {
		res.Fail(c, code)
		return
	}

	if !casEnf.AddPolicy(id, body.MID, body.Action) {
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
	var body struct {
		MID    string `form:"mid" json:"mid" binding:"required"`
		Action string `form:"action" json:"action" binding:"required"`
	}
	err := c.Bind(&body)
	if err != nil {
		res.Fail(c, 4001)
		return
	}
	if code := checkMap(body.MID); code != 200 {
		res.Fail(c, code)
		return
	}
	if !casEnf.RemovePolicy(id, body.MID, body.Action) {
		res.Done(c, "policy does not  exist")
		return
	}
	res.Done(c, "")
	return
}

func addUserRole(c *gin.Context) {
	res := NewRes()
	uid := c.Param("id")
	if code := checkUser(uid); code != 200 {
		res.Fail(c, code)
		return
	}
	var body struct {
		RID string `form:"rid" json:"rid" binding:"required"`
	}
	err := c.Bind(&body)
	if err != nil {
		res.Fail(c, 4001)
		return
	}
	if code := checkRole(body.RID); code != 200 {
		res.Fail(c, code)
		return
	}

	if casEnf.AddRoleForUser(uid, body.RID) {
		user := &User{}
		db.Select("role").Where("name=?", uid).First(user)
		err = db.Model(&User{}).Where("name = ?", uid).Update(User{Role: append(user.Role, body.RID)}).Error
		if err != nil {
			log.Error(err)
			res.Fail(c, 5001)
			return
		}
		res.Done(c, "")
		return
	}
	res.Done(c, fmt.Sprintf("%s already has %s role", uid, body.RID))
}

func deleteUserRole(c *gin.Context) {
	res := NewRes()
	uid := c.Param("id")

	if code := checkUser(uid); code != 200 {
		res.Fail(c, code)
		return
	}
	var body struct {
		RID string `form:"rid" json:"rid" binding:"required"`
	}
	err := c.Bind(&body)
	if err != nil {
		res.Fail(c, 4001)
		return
	}
	if code := checkRole(body.RID); code != 200 {
		res.Fail(c, code)
		return
	}

	if casEnf.DeleteRoleForUser(uid, body.RID) {

		user := &User{}
		db.Select("role").Where("name=?", uid).First(user)
		var roles []string
		for i, r := range user.Role {
			if r == body.RID {
				roles = append(user.Role[:i], user.Role[i+1:]...)
				break
			}
		}
		err = db.Model(&User{}).Where("name = ?", uid).Update(User{Role: roles}).Error
		if err != nil {
			log.Error(err)
			res.Fail(c, 5001)
			return
		}

		res.Done(c, "")
		return
	}
	res.Done(c, fmt.Sprintf("%s does not has %s role", uid, body.RID))
}

func getRoleMaps(c *gin.Context) {
	res := NewRes()
	id := c.Param("id")
	if code := checkRole(id); code != 200 {
		res.Fail(c, code)
		return
	}
	uperms := casEnf.GetPermissionsForUser(id)

	var pers []MapPerm
	for _, perm := range uperms {
		m := &Map{}
		err := db.Where("id = ?", perm[1]).First(&m).Error
		if err == nil { //有错误,说明改权限策略不是用于map的
			p := MapPerm{
				ID:      perm[0],
				MapID:   perm[1],
				MapName: m.Title,
				Action:  perm[2],
			}
			pers = append(pers, p)
		}
	}
	res.DoneData(c, pers)
}

func addRoleMap(c *gin.Context) {
	res := NewRes()
	id := c.Param("id")
	if code := checkRole(id); code != 200 {
		res.Fail(c, code)
		return
	}
	var body struct {
		MID    string `form:"mid" json:"mid" binding:"required"`
		Action string `form:"action" json:"action" binding:"required"`
	}
	err := c.Bind(&body)
	if err != nil {
		res.Fail(c, 4001)
		return
	}
	if code := checkMap(body.MID); code != 200 {
		res.Fail(c, code)
		return
	}

	if !casEnf.AddPolicy(id, body.MID, body.Action) {
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
	var body struct {
		MID    string `form:"mid" json:"mid" binding:"required"`
		Action string `form:"action" json:"action" binding:"required"`
	}
	err := c.Bind(&body)
	if err != nil {
		res.Fail(c, 4001)
		return
	}
	if code := checkMap(body.MID); code != 200 {
		res.Fail(c, code)
		return
	}

	if !casEnf.RemovePolicy(id, body.MID, body.Action) {
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
	group := cfgV.GetString("user.group")
	if rid == group {
		res.FailMsg(c, "unable to system group")
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

func getMapPerms(c *gin.Context) {
	res := NewRes()
	mid := c.Param("id")
	if code := checkMap(mid); code != 200 {
		res.Fail(c, code)
		return
	}

	uperms := casEnf.GetFilteredPolicy(1, mid)

	var pers []MapPerm
	for _, perm := range uperms {
		m := &Map{}
		db.Where("id = ?", perm[1]).First(&m)
		p := MapPerm{
			ID:      perm[0],
			MapID:   perm[1],
			MapName: m.Title,
			Action:  perm[2],
		}
		pers = append(pers, p)
	}
	res.DoneData(c, pers)
}

func listMaps(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	var maps []Map
	if id == "root" {
		db.Select("id,title,summary,user,thumbnail,created_at,updated_at").Find(&maps)
		res.DoneData(c, maps)
		return
	}

	uperms := casEnf.GetPermissionsForUser(id)
	roles := casEnf.GetRolesForUser(id)
	for _, role := range roles {
		rperms := casEnf.GetPermissionsForUser(role)
		uperms = append(uperms, rperms...)
	}
	mapids := make(map[string]string)
	for _, p := range uperms {
		if len(p) == 3 {
			mapids[p[1]] = p[2]
		}
	}
	var ids []string
	for k := range mapids {
		ids = append(ids, k)
	}
	db.Select("id,title,summary,user,thumbnail,created_at,updated_at").Where("id in (?)", ids).Find(&maps)

	//添加每个map对应的该用户的权限
	for _, m := range maps {
		m.Action = mapids[m.ID]
	}

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
	if !casEnf.Enforce(id, mid, "(READ)|(EDIT)") {
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
	res.DoneData(c, m.toBind())
}

func createMap(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	group := cfgV.GetString("user.group")
	if id == "root" || casEnf.HasRoleForUser(id, group) {
		body := &MapBind{}
		err := c.Bind(&body)
		if err != nil {
			log.Error(err)
			res.Fail(c, 4001)
			return
		}
		mm := body.toMap()
		mm.ID, _ = shortid.Generate()
		mm.User = id
		if mm.Action == "" {
			mm.Action = "(READ)|(EDIT)"
		}
		// insertUser
		err = db.Create(mm).Error
		if err != nil {
			log.Error(err)
			res.Fail(c, 5001)
			return
		}
		//管理员创建地图后自己拥有,root不需要
		if id != "root" {
			casEnf.AddPolicy(mm.User, mm.ID, mm.Action)
		}
		res.DoneData(c, gin.H{
			"id": mm.ID,
		})
		return
	}
	res.Fail(c, 403)
	return
}

func updInsetMap(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	mid := c.Param("id")
	if mid == "" {
		res.Fail(c, 4001)
		return
	}
	if !casEnf.Enforce(id, mid, "EDIT") {
		res.Fail(c, 403)
		return
	}
	body := &MapBind{}
	err := c.Bind(&body)
	if err != nil {
		log.Error(err)
		res.Fail(c, 4001)
		return
	}
	mm := body.toMap()
	err = db.Model(&Map{}).Where("id = ?", mid).First(&Map{}).Error
	if err != nil {
		if gorm.IsRecordNotFoundError(err) {
			mm.ID = mid
			err = db.Create(&mm).Error
			if err != nil {
				log.Error(err)
				res.Fail(c, 5001)
				return
			}
			res.Done(c, "")
			return
		}
		log.Error(err)
		res.Fail(c, 5001)
		return
	}
	err = db.Model(&Map{}).Where("id = ?", mid).Update(mm).Error
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
	if !casEnf.Enforce(id, mid, "EDIT") {
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
	pubSet.AddStyle(dst, sid)
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
	style, ok := pubSet.Styles[sid]
	if !ok {
		log.Errorf("style saved, but id(%s) not exist in the service", sid)
		res.Fail(c, 4044)
		return
	}
	style.Style = body
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
		res.Fail(c, 4046)
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

func listDatasets(c *gin.Context) {
	res := NewRes()

	var dss []*DatasetBind
	for _, ds := range pubSet.Datasets {
		dss = append(dss, ds.Dataset)
	}
	res.DoneData(c, dss)
}

func getDatasetInfo(c *gin.Context) {
	res := NewRes()
	name := c.Param("name")
	if code := checkDataset(name); code != 200 {
		res.Fail(c, code)
		return
	}
	ds, ok := pubSet.Datasets[name]
	if !ok {
		res.Fail(c, 4045)
		return
	}
	res.DoneData(c, ds.Dataset)
}

func getDistinctValues(c *gin.Context) {
	res := NewRes()
	name := c.Param("name")
	if code := checkDataset(name); code != 200 {
		res.Fail(c, code)
		return
	}
	var body struct {
		Field string `form:"field" json:"field" binding:"required"`
	}
	err := c.Bind(&body)
	if err != nil {
		log.Error(err)
		res.Fail(c, 4001)
		return
	}
	s := fmt.Sprintf(`SELECT distinct(%s) as val,count(*) as cnt FROM "%s" GROUP BY %s;`, body.Field, name, body.Field)
	fmt.Println(s)
	rows, err := db.Raw(s).Rows()
	if err != nil {
		log.Error(err)
		res.Fail(c, 5001)
		return
	}
	defer rows.Close()
	type ValCnt struct {
		Val string
		Cnt int
	}
	var valCnts []ValCnt
	for rows.Next() {
		var vc ValCnt
		// ScanRows scan a row into user
		db.ScanRows(rows, &vc)
		valCnts = append(valCnts, vc)
		// do something
	}
	res.DoneData(c, valCnts)
}

func importFiles(c *gin.Context) {
	res := NewRes()
	ftype := c.Param("name")
	if ftype != "csv" && ftype != "geojson" {
		res.Fail(c, 400)
		// res.FailMsg(c, "unkonw file type, must be .geojson or .csv")
		return
	}

	file, err := c.FormFile("file")
	if err != nil {
		log.Errorf(`importFiles, get form: %s; type: %s`, err, ftype)
		res.Fail(c, 4046)
		return
	}

	dir := cfgV.GetString("assets.datasets")
	filename := file.Filename
	ext := filepath.Ext(filename)
	name := strings.TrimSuffix(filename, ext)
	id, _ := shortid.Generate()
	id = name + "." + id
	dst := filepath.Join(dir, id+ext)
	if err := c.SaveUploadedFile(file, dst); err != nil {
		log.Errorf(`importFiles, saving tmp file: %s; file: %s`, err, filename)
		res.Fail(c, 5002)
		return
	}
	buf, err := ioutil.ReadFile(dst)
	if err != nil {
		log.Errorf(`importFiles, csv reader failed: %s; file: %s`, err, filename)
		res.Fail(c, 5003)
		return
	}

	var cnt int64
	// datasetType := TypeAttribute

	switch ftype {
	case "geojson":
		if name != "block_lines" && name != "regions" && name != "interests" && name != "static_buffers" {
			res.FailMsg(c, "unkown datasets")
			return
		}
		fc, err := geojson.UnmarshalFeatureCollection(buf)
		if err != nil {
			log.Error(err)
			res.FailErr(c, err)
			return
		}
		db.DropTableIfExists(name)
		createTable := func(fc *geojson.FeatureCollection) error {
			var headers []string
			var fts []string
			var geoType string
			for _, f := range fc.Features {
				geoType = f.Geometry.GeoJSONType()
				for k, v := range f.Properties {
					var t string
					switch v.(type) {
					case bool:
						t = "BOOL" //or 'timestamp with time zone'
					case float64:
						t = "NUMERIC"
					case []interface{}:
						t = "_VARCHAR" //or 'character varying[]'
					default: //string/map[string]interface{}/nil
						t = "TEXT"
					}
					headers = append(headers, k)
					fts = append(fts, k+" "+t)
				}
				break
			}
			//add 'geom geometry(Geometry,4326)'
			geom := fmt.Sprintf("geom geometry(%s,4326)", geoType)
			headers = append(headers, "geom")
			fts = append(fts, geom)

			st := fmt.Sprintf(`CREATE TABLE %s (%s);`, name, strings.Join(fts, ","))
			err := db.Exec(st).Error
			if err != nil {
				return err
			}

			kvsi := make(map[string]int, len(headers))
			kvst := make(map[string]string, len(headers))
			for i, h := range headers {
				kvsi[h] = i
				kvst[h] = strings.Split(fts[i], " ")[1]
			}

			var vals []string
			for _, f := range fc.Features {
				vs := make([]string, len(headers))
				for k, val := range f.Properties {
					var s string
					switch kvst[k] {
					case "BOOL":
						v, ok := val.(bool) // Alt. non panicking version
						if ok {
							s = strconv.FormatBool(v)
						} else {
							s = "null"
						}
					case "NUMERIC":
						v, ok := val.(float64) // Alt. non panicking version
						if ok {
							s = strconv.FormatFloat(v, 'E', -1, 64)
						} else {
							s = "null"
						}
					default: //string,map[string]interface{},[]interface{},time.Time,bool
						if val == nil {
							s = ""
						} else {
							s = val.(string)
						}
						s = "'" + s + "'"
					}
					vs[kvsi[k]] = s
				}
				geom, err := geojson.NewGeometry(f.Geometry).MarshalJSON()
				if err != nil {
					return err
				}
				vs[kvsi["geom"]] = fmt.Sprintf(`st_setsrid(st_geomfromgeojson('%s'),4326)`, string(geom))

				vals = append(vals, fmt.Sprintf(`(%s)`, strings.Join(vs, ",")))
			}

			st = fmt.Sprintf(`INSERT INTO %s (%s) VALUES %s ON CONFLICT DO NOTHING;`, name, strings.Join(headers, ","), strings.Join(vals, ",")) // ON CONFLICT (id) DO UPDATE SET (%s) = (%s)
			// log.Println(st)
			query := db.Exec(st)
			if err, cnt = query.Error, query.RowsAffected; err != nil {
				return err
			}
			return nil
		}

		err = createTable(fc)
		if err != nil {
			log.Error(err)
			res.FailErr(c, err)
			return
		}
		err = updateDatasetInfo(name)
		if err != nil {
			log.Error(err)
			res.FailErr(c, err)
		}
	case "csv":
		reader := csv.NewReader(bytes.NewReader(buf))
		csvHeader, err := reader.Read()
		if err != nil {
			log.Errorf(`importDataset, csv reader failed: %s; file: %s`, err, filename)
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
				case "TIMESTAMPTZ":
					if "" == row[i] {
						vals = vals + "null,"
					} else {
						vals = vals + "'" + row[i] + "',"
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
			err := db.Exec(s).Error
			if err != nil {
				return err
			}
			s = fmt.Sprintf(`DELETE FROM datasets WHERE name='%s';`, name)
			return db.Exec(s).Error
		}
		insert := func(header string) error {
			if len(strings.Split(header, ",")) != len(csvHeader) {
				log.Errorf("the cvs file format error, file:%s,  should be:%s", name, header)
				return fmt.Errorf("the cvs file format error, file:%s", name)
			}

			s := fmt.Sprintf(`SELECT %s FROM "%s" LIMIT 0`, header, name)
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
				log.Debug(rval)
				vals = append(vals, fmt.Sprintf(`(%s)`, rval))
			}
			s = fmt.Sprintf(`INSERT INTO %s (%s) VALUES %s ON CONFLICT DO NOTHING;`, name, header, strings.Join(vals, ",")) // ON CONFLICT (id) DO UPDATE SET (%s) = (%s)
			query := db.Exec(s)
			cnt = query.RowsAffected
			return query.Error
		}

		//数据入库
		var header, search string
		updateGeom := false
		switch name {
		case "banks", "others", "pois", "plans":
			switch name {
			case "banks":
				header = "机构号,名称,营业状态,行政区,网点类型,营业部,管理行,权属,营业面积,到期时间,装修时间,人数,行评等级,X,Y"
				search = ",search =ARRAY[机构号,名称,行政区,网点类型,管理行]"
			case "others":
				header = "机构号,名称,银行类别,网点类型,地址,X,Y,SID"
				search = ",search =ARRAY[机构号,名称,银行类别,地址]"
			case "pois":
				header = "名称,类型,性质,建筑面积,热度,人均消费,均价,户数,交付时间,职工人数,备注,X,Y,SID"
				search = ",search =ARRAY[名称,备注]"
			case "plans":
				header = "机构号,名称,类型,年份,规划建议,实施时间,X,Y,SID"
			}
			updateGeom = true
			// datasetType = TypePoint
		case "savings", "m1", "m2", "m5", "buffer_scales", "m2_weights", "m4_weights", "m4_scales":
			switch name {
			case "savings":
				header = "机构号,名称,年份,总存款日均,单位存款日均,个人存款日均,保证金存款日均,其他存款日均"
			case "m1":
				header = "机构号,商业规模,商业人流,道路特征,快速路,位置特征,转角位置,街巷,斜坡,公共交通类型,距离,停车位,收费,建筑形象,营业厅面积,装修水准,网点类型,总得分"
			case "m2":
				header = "机构号,营业面积,人数,个人增长,个人存量,公司存量"
			case "m5":
				header = "名称,生产总值,人口,房地产成交面积,房地产成交均价,社会消费品零售总额,规模以上工业增加值,金融机构存款,金融机构贷款"
			case "buffer_scales":
				header = "type,scale"
			case "m2_weights":
				header = "field,weight"
			case "m4_weights":
				header = "type,weight"
			case "m4_scales":
				header = "type,scale"
			}
		default:
			res.FailMsg(c, "unkown datasets")
			return
		}

		clear(name)
		err = insert(header)
		if err != nil {
			log.Errorf("import %s error:%s", filename, err.Error())
			res.Fail(c, 5001)
			return
		}
		if updateGeom {
			update := fmt.Sprintf(`UPDATE %s SET geom = ST_GeomFromText('POINT(' || x || ' ' || y || ')',4326)%s;`, name, search)
			result := db.Exec(update)
			if result.Error != nil {
				log.Errorf("update %s create geom error:%s", name, result.Error.Error())
				res.Fail(c, 5001)
				return
			}
		}
		err = updateDatasetInfo(name)
		if err != nil {
			log.Error(err)
			res.FailErr(c, err)
			return
		}
	default:
		return
	}

	res.DoneData(c, gin.H{
		"id":  id,
		"cnt": cnt,
	})
}

func getGeojson(c *gin.Context) {
	res := NewRes()
	name := c.Param("name")
	fields := c.Query("fields")
	filter := c.Query("filter")

	if code := checkDataset(name); code != 200 {
		res.Fail(c, code)
		return
	}

	selStr := "st_asgeojson(geom) as geom "
	if "" != fields {
		selStr = selStr + "," + fields
	}
	var whr string
	if "" != filter {
		whr = " WHERE " + filter
	}
	s := fmt.Sprintf(`SELECT %s FROM %s %s;`, selStr, name, whr)
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

		f := geojson.NewFeature(nil)

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
				f.Geometry = geom.Geometry()
			default:
				v := string(*rb)
				switch cols[i].DatabaseTypeName() {
				case "INT", "INT4":
					f.Properties[t.Name()], _ = strconv.Atoi(v)
				case "NUMERIC", "DECIMAL": //number
					f.Properties[t.Name()], _ = strconv.ParseFloat(v, 64)
				// case "BOOL":
				// case "TIMESTAMPTZ":
				// case "_VARCHAR":
				// case "TEXT", "VARCHAR", "BIGINT":
				default:
					f.Properties[t.Name()] = v
				}
			}

		}
		fc.Append(f)
	}
	var extent []byte
	stbox := fmt.Sprintf(`SELECT st_asgeojson(st_extent(geom)) as extent FROM %s %s;`, name, whr)
	db.Raw(stbox).Row().Scan(&extent) // (*sql.Rows, error)
	ext, err := geojson.UnmarshalGeometry(extent)
	if err == nil {
		fc.BBox = geojson.NewBBox(ext.Geometry().Bound())
	}
	gj, err := fc.MarshalJSON()
	if err != nil {
		log.Errorf("unable to MarshalJSON of featureclection.")
		res.FailMsg(c, "unable to MarshalJSON of featureclection.")
		return
	}
	c.JSON(http.StatusOK, json.RawMessage(gj))
}

func queryGeojson(c *gin.Context) {
	res := NewRes()
	name := c.Param("name")

	var body struct {
		Geom   string `form:"geom" json:"geom"`
		Fields string `form:"fields" json:"fields"`
		Filter string `form:"filter" json:"filter"`
	}
	err := c.Bind(&body)
	if err != nil {
		res.Fail(c, 4001)
		return
	}

	selStr := "st_asgeojson(geom) as geom "
	if "" != body.Fields {
		selStr = selStr + "," + body.Fields
	}
	var whrStr string
	if body.Geom != "" {
		whrStr = fmt.Sprintf(` WHERE geom && st_geomfromgeojson('%s')`, body.Geom)
		if "" != body.Filter {
			whrStr = whrStr + " AND " + body.Filter
		}
	} else {
		if "" != body.Filter {
			whrStr = " WHERE " + body.Filter
		}
	}

	s := fmt.Sprintf(`SELECT %s FROM %s  %s;`, selStr, name, whrStr)
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

		f := geojson.NewFeature(nil)

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
				f.Geometry = geom.Geometry()
			default:
				v := string(*rb)
				switch cols[i].DatabaseTypeName() {
				case "INT", "INT4":
					f.Properties[t.Name()], _ = strconv.Atoi(v)
				case "NUMERIC", "DECIMAL": //number
					f.Properties[t.Name()], _ = strconv.ParseFloat(v, 64)
				// case "BOOL":
				// case "TIMESTAMPTZ":
				// case "_VARCHAR":
				// case "TEXT", "VARCHAR", "BIGINT":
				default:
					f.Properties[t.Name()] = v
				}
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

func cubeQuery(c *gin.Context) {
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
		for _, v := range vals {
			// skip nil values.
			if v == nil {
				continue
			}
			r = append(r, string(v))
		}
		if len(r) == 0 {
			continue
		}
		t = append(t, r)
	}
	res.DoneData(c, t)
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

	cols, _ := rows.ColumnTypes()
	var ams []map[string]interface{}
	for rows.Next() {
		// Create a slice of interface{}'s to represent each column,
		// and a second slice to contain pointers to each item in the columns slice.
		columns := make([]sql.RawBytes, len(cols))
		columnPointers := make([]interface{}, len(cols))
		for i := range columns {
			columnPointers[i] = &columns[i]
		}

		// Scan the result into the column pointers...
		if err := rows.Scan(columnPointers...); err != nil {
			log.Error(err)
			continue
		}

		// Create our map, and retrieve the value for each column from the pointers slice,
		// storing it in the map with the name of the column as the key.
		m := make(map[string]interface{})
		for i, col := range columns {
			if cols[i].Name() == "geom" || cols[i].Name() == "search" {
				continue
			}
			//"NVARCHAR", "DECIMAL", "BOOL", "INT", "BIGINT".
			v := string(col)
			switch cols[i].DatabaseTypeName() {
			case "INT", "INT4":
				m[cols[i].Name()], _ = strconv.Atoi(v)
			case "NUMERIC", "DECIMAL": //number
				m[cols[i].Name()], _ = strconv.ParseFloat(v, 64)
			// case "BOOL":
			// case "TIMESTAMPTZ":
			// case "_VARCHAR":
			// case "TEXT", "VARCHAR", "BIGINT":
			default:
				m[cols[i].Name()] = v
			}
		}
		// fmt.Print(m)
		ams = append(ams, m)
	}
	res.DoneData(c, ams)
}

func queryBusiness(c *gin.Context) {
	res := NewRes()
	name := c.Param("name")
	var linkTables []string
	if name != "banks" {
		res.DoneData(c, gin.H{
			name: linkTables,
		})
		return
	}
	linkTables = cfgV.GetStringSlice("business.banks.linked")
	res.DoneData(c, gin.H{
		name: linkTables,
	})
}

func getBuffers(c *gin.Context) {
	res := NewRes()
	name := c.Param("name")
	rs := c.Query("radius")
	t := c.Query("type")
	bprefix := cfgV.GetString("buffers.prefix")
	bsuffix := cfgV.GetString("buffers.suffix")
	bname := name + bsuffix
	r, _ := strconv.ParseFloat(rs, 64)
	if r != 0 {
		if code := buffering(name, r); code != 200 {
			res.Fail(c, code)
		}
	}
	if t == "block" {
		bname = bprefix + bname
	}

	fields := c.Query("fields")
	filter := c.Query("filter")

	if code := checkDataset(bname); code != 200 {
		res.Fail(c, code)
		return
	}

	selStr := "st_asgeojson(b.geom) as geom "

	if "" != fields {
		flds := strings.Split(fields, ",")
		if len(flds) == 1 {
			selStr = selStr + ", a." + flds[0]
		} else {
			selStr = selStr + ", a." + strings.Join(flds, ", a.")
		}
	}
	whr := " WHERE a.id = b.id "
	if "" != filter {
		whr += " AND ( " + filter + " )"
		whr = strings.Replace(whr, " id ", " a.id ", -1)
		whr = strings.Replace(whr, " id=", " a.id= ", -1)
		whr = strings.Replace(whr, " geom ", " a.geom ", -1)
		whr = strings.Replace(whr, " (geom", " (a.geom ", -1)
		whr = strings.Replace(whr, "geom) ", " a.geom) ", -1)
	}
	s := fmt.Sprintf(`SELECT %s FROM %s as a, %s as b %s;`, selStr, name, bname, whr)
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

		// f := newFeatrue(t)
		f := geojson.NewFeature(nil)
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
					log.Error(err)
					log.Errorf("UnmarshalGeometry from geojson result error, index %d column %s", i, t.Name())
					continue
				}
				f.Geometry = geom.Geometry()
			default:
				v := string(*rb)
				switch cols[i].DatabaseTypeName() {
				case "INT", "INT4":
					f.Properties[t.Name()], _ = strconv.Atoi(v)
				case "NUMERIC", "DECIMAL": //number
					f.Properties[t.Name()], _ = strconv.ParseFloat(v, 64)
				// case "BOOL":
				// case "TIMESTAMPTZ":
				// case "_VARCHAR":
				// case "TEXT", "VARCHAR", "BIGINT":
				default:
					f.Properties[t.Name()] = v
				}
			}

		}
		fc.Append(f)
	}
	var extent []byte
	stbox := fmt.Sprintf(`SELECT st_asgeojson(st_extent(b.geom)) as extent FROM %s as a,%s as b %s;`, name, bname, whr)
	db.Raw(stbox).Row().Scan(&extent) // (*sql.Rows, error)
	ext, err := geojson.UnmarshalGeometry(extent)
	if err == nil {
		fc.BBox = geojson.NewBBox(ext.Geometry().Bound())
	}
	gj, err := fc.MarshalJSON()
	if err != nil {
		log.Errorf("unable to MarshalJSON of featureclection.")
		res.FailMsg(c, "unable to MarshalJSON of featureclection.")
		return
	}
	c.JSON(http.StatusOK, json.RawMessage(gj))
}

func getModels(c *gin.Context) {
	res := NewRes()
	name := c.Param("name")
	fields := c.Query("fields")
	filter := c.Query("filter")
	needCacl := c.Query("cacl")

	if code := checkDataset(name); code != 200 {
		res.Fail(c, code)
		return
	}
	if needCacl != "" {
		switch name {
		case "m1":
			err := calcM1()
			if err != nil {
				log.Error(err)
				res.FailErr(c, err)
				// return
			}
		case "m2":
			err := calcM2()
			if err != nil {
				log.Error(err)
				res.FailErr(c, err)
				// return
			}
		case "m3":
			err := calcM3()
			if err != nil {
				log.Error(err)
				res.FailErr(c, err)
				// return
			}
		case "m4":
			err := calcM4()
			if err != nil {
				log.Error(err)
				res.FailErr(c, err)
				// return
			}
		case "m5":
			err := calcM5()
			if err != nil {
				log.Error(err)
				res.FailErr(c, err)
				// return
			}
		default:
			res.FailMsg(c, "unkown model name")
			return
		}
	}
	if fields == "" {
		fields = " * "
	}
	if filter != "" {
		filter = " WHERE " + filter
	}

	s := fmt.Sprintf(`SELECT %s FROM %s %s;`, fields, name, filter)
	rows, err := db.Raw(s).Rows() // (*sql.Rows, error)
	if err != nil {
		log.Error(err)
		res.Fail(c, 5001)
		return
	}
	defer rows.Close()
	cols, _ := rows.ColumnTypes()
	var ams []map[string]interface{}
	for rows.Next() {
		// Create a slice of interface{}'s to represent each column,
		// and a second slice to contain pointers to each item in the columns slice.
		columns := make([]sql.RawBytes, len(cols))
		columnPointers := make([]interface{}, len(cols))
		for i := range columns {
			columnPointers[i] = &columns[i]
		}

		// Scan the result into the column pointers...
		if err := rows.Scan(columnPointers...); err != nil {
			log.Error(err)
			continue
		}

		// Create our map, and retrieve the value for each column from the pointers slice,
		// storing it in the map with the name of the column as the key.
		m := make(map[string]interface{})
		for i, col := range columns {
			// if col == nil {
			// continue
			// }
			//"NVARCHAR", "DECIMAL", "BOOL", "INT", "BIGINT".
			v := string(col)
			switch cols[i].DatabaseTypeName() {
			case "INT", "INT4":
				m[cols[i].Name()], _ = strconv.Atoi(v)
			case "NUMERIC", "DECIMAL": //number
				m[cols[i].Name()], _ = strconv.ParseFloat(v, 64)
			// case "BOOL":
			// case "TIMESTAMPTZ":
			// case "_VARCHAR":
			// case "TEXT", "VARCHAR", "BIGINT":
			default:
				m[cols[i].Name()] = v
			}
		}
		// fmt.Print(m)
		ams = append(ams, m)
	}
	c.JSON(http.StatusOK, ams)
}

func searchGeos(c *gin.Context) {
	// res := NewRes()
	searchType := c.Param("name")
	keyword := c.Query("keyword")
	var ams []map[string]interface{}

	log.Println("***********", keyword, "**************")
	if searchType != "search" || keyword == "" {
		// res.Fail(c, 4001)
		c.JSON(http.StatusOK, ams)
		return
	}
	search := func(s string, keyword string) {
		stmt, err := db.DB().Prepare(s)
		if err != nil {
			log.Error(err)
			return
		}
		defer stmt.Close()
		rows, err := stmt.Query(keyword)
		if err != nil {
			log.Error(err)
			return
		}
		defer rows.Close()

		cols, _ := rows.ColumnTypes()
		for rows.Next() {
			// Create a slice of interface{}'s to represent each column,
			// and a second slice to contain pointers to each item in the columns slice.
			columns := make([]sql.RawBytes, len(cols))
			columnPointers := make([]interface{}, len(cols))
			for i := range columns {
				columnPointers[i] = &columns[i]
			}

			// Scan the result into the column pointers...
			if err := rows.Scan(columnPointers...); err != nil {
				log.Error(err)
				continue
			}

			// Create our map, and retrieve the value for each column from the pointers slice,
			// storing it in the map with the name of the column as the key.
			m := make(map[string]interface{})
			for i, col := range columns {
				if col == nil {
					continue
				}
				//"NVARCHAR", "DECIMAL", "BOOL", "INT", "BIGINT".
				v := string(col)
				switch cols[i].DatabaseTypeName() {
				case "INT", "INT4":
					m[cols[i].Name()], _ = strconv.Atoi(v)
				case "NUMERIC", "DECIMAL": //number
					m[cols[i].Name()], _ = strconv.ParseFloat(v, 64)
				// case "BOOL":
				// case "TIMESTAMPTZ":
				// case "_VARCHAR":
				// case "TEXT", "VARCHAR", "BIGINT":
				default:
					m[cols[i].Name()] = v
				}
			}
			// fmt.Print(m)
			ams = append(ams, m)
		}
	}

	st := fmt.Sprintf(`SELECT id,名称,st_asgeojson(geom) as geom FROM regions WHERE 名称 ~ $1;`)
	fmt.Println(st)
	search(st, keyword)
	if len(ams) > 10 {
		c.JSON(http.StatusOK, ams)
		return
	}
	bbox := c.Query("bbox")
	var gfilter string
	if bbox != "" {
		gfilter = fmt.Sprintf(` geom && st_makeenvelope(%s,4326) AND `, bbox)
	}
	limit := c.Query("limit")
	var limiter string
	if limit != "" {
		limiter = fmt.Sprintf(` LIMIT %s `, limit)
	}
	st = fmt.Sprintf(`SELECT id,名称,st_asgeojson(geom) as geom,s 搜索 
	FROM (SELECT id,名称,geom,unnest(search) s FROM banks) x WHERE %s s ~ $1 GROUP BY id,名称,geom,s %s;`, gfilter, limiter)
	fmt.Println(st)
	search(st, keyword)
	if len(ams) > 10 {
		c.JSON(http.StatusOK, ams)
		return
	}
	st = fmt.Sprintf(`SELECT id,名称,st_asgeojson(geom) as geom,s 搜索 
	FROM (SELECT id,名称,geom,unnest(search) s FROM others) x WHERE %s s ~ $1 GROUP BY id,名称,geom,s %s;`, gfilter, limiter)
	fmt.Println(st)
	search(st, keyword)
	if len(ams) > 10 {
		c.JSON(http.StatusOK, ams)
		return
	}
	st = fmt.Sprintf(`SELECT 名称,st_asgeojson(geom) as geom,s 搜索 
	FROM (SELECT 名称,geom,unnest(search) s FROM pois) x WHERE %s s ~ $1 GROUP BY 名称,geom,s %s;`, gfilter, limiter)
	fmt.Println(st)
	search(st, keyword)
	if len(ams) > 10 {
		c.JSON(http.StatusOK, ams)
		return
	}
	c.JSON(http.StatusOK, ams)
}

func updateInsertData(c *gin.Context) {
	res := NewRes()
	name := c.Param("name")

	if code := checkDataset(name); code != 200 {
		res.Fail(c, code)
		return
	}

	bank := &Bank{}
	err := c.BindJSON(bank)
	if err != nil {
		log.Error(err)
		res.Fail(c, 4001)
		return
	}

	bank.Search = []string{bank.No, bank.Name, bank.Region, bank.Type, bank.Manager}

	if db.Table(name).Where("id = ?", bank.ID).First(&Bank{}).RecordNotFound() {
		db.Omit("geom").Create(bank)
	} else {
		err := db.Table(name).Where("id = ?", bank.ID).Update(bank).Error
		if err != nil {
			log.Error(err)
			res.Fail(c, 5001)
			return
		}
	}

	if bank.X < -180 || bank.X > 180 || bank.Y < -85 || bank.Y > 85 {
		log.Errorf("x, y must be reasonable values, name")
		res.FailMsg(c, "x, y must be reasonable values")
		return
	}
	stgeo := fmt.Sprintf(`UPDATE %s SET geom = ST_GeomFromText('POINT(' || x || ' ' || y || ')',4326) WHERE id=%d;`, name, bank.ID)
	result := db.Exec(stgeo)
	if result.Error != nil {
		log.Errorf("update %s create geom error:%s", name, result.Error.Error())
		res.Fail(c, 5001)
		return
	}

	res.DoneData(c, gin.H{
		"id": bank.ID,
	})
}

func deleteData(c *gin.Context) {
	res := NewRes()
	name := c.Param("name")

	if code := checkDataset(name); code != 200 {
		res.Fail(c, code)
		return
	}

	var body struct {
		ID string `form:"id" json:"id" binding:"required"`
	}
	err := c.Bind(&body)
	if err != nil {
		res.Fail(c, 4001)
		return
	}
	err = db.Where("id = ?", body.ID).Delete(&Bank{}).Error
	if err != nil {
		log.Errorf("delete data : %s; dataid: %s", err, body.ID)
		res.Fail(c, 5001)
		return
	}
	res.Done(c, "")
}
