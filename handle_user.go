package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jinzhu/gorm"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"github.com/teris-io/shortid"
	"golang.org/x/crypto/bcrypt"
)

func signup(c *gin.Context) {
	res := NewRes()
	var body struct {
		Name     string `json:"name" form:"name" binding:"required"`
		Email    string `json:"email" form:"email" binding:"required"`
		Password string `json:"password" form:"password" binding:"required"`
	}
	err := c.Bind(&body)
	if err != nil {
		log.Warnf(`signup, info error, details: '%s' ~`, err)
		res.Fail(c, 4001)
		return
	}
	// validate signup name
	if code := validName(body.Name); code != 200 {
		res.Fail(c, code)
		return
	}
	// validate signup email
	if code := validEmail(body.Email); code != 200 {
		res.Fail(c, code)
		return
	}
	// validate signup email
	if code := validPassword(body.Password); code != 200 {
		res.Fail(c, code)
		return
	}
	// createUser
	user := User{}
	user.ID, _ = shortid.Generate()
	user.Name = body.Name
	hashedPassword, _ := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	user.Password = string(hashedPassword)
	user.Email = strings.ToLower(body.Email)
	//No verification required
	user.AccessToken, _, err = accessMid.TokenGenerator(user)
	if err != nil {
		log.Errorf(`signup, token generator error, details: '%s' ~`, err)
		res.FailMsg(c, "signup, token generator error")
		return
	}

	user.Group = USER
	user.Activation = "yes"
	var verifyURL string
	if viper.GetBool("user.verification") {
		user.Verification = "no"
		//Create a verification token
		token := generateToken(21)
		hash, _ := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
		user.VerificationToken = string(hash)
		verifyURL = rootURL(c.Request) + "/sign/verify/" + user.Name + "/" + string(token) + "/"
	} else {
		user.Verification = "yes"
	}
	// insertUser
	err = db.Create(&user).Error
	if err != nil {
		log.Errorf(`signup, create user error, details: '%s' ~`, err)
		res.FailMsg(c, "signup, create user error")
		// res.Fail(c, 5001)
		return
	}
	// sendWelcomeEmail
	log.Debug("Loging verify url for debug: " + verifyURL)
	go func() {
		mailConf := MailConfig{}
		mailConf.Data = gin.H{
			"VerifyURL":    verifyURL,
			"Verification": user.Verification,
			"SigninURL":    rootURL(c.Request) + "/sign/in/",
			"Email":        body.Email,
			"Name":         body.Name,
		}
		mailConf.From = viper.GetString("smtp.from.name") + " <" + viper.GetString("smtp.from.address") + ">"
		mailConf.Subject = "地图云-用户注册邮件"
		mailConf.ReplyTo = body.Email
		mailConf.HTMLPath = viper.GetString("statics.home") + "email/signup.html"

		if err := mailConf.SendMail(); err != nil {
			log.Errorf(`signup, sending verify email error, user: %s, details: '%s' ~`, body.Name, err)
		}
	}()
	CreatePaths(user.Name)
	if casEnf != nil {
		casEnf.LoadPolicy()
		casEnf.AddGroupingPolicy(user.Name, USER)
		casEnf.SavePolicy()
	}
	res.Done(c, "注册成功，验证邮件已发送")
}

func addUser(c *gin.Context) {
	res := NewRes()
	var body struct {
		Name       string `json:"name" form:"name" binding:"required"`
		Password   string `json:"password" form:"password" binding:"required"`
		Phone      string ` json:"phone" form:"phone"`
		Department string `json:"department" form:"department"`
		Company    string `json:"company" form:"company"`
	}
	err := c.Bind(&body)
	if err != nil {
		log.Errorf(`addUser, info error, details: %s ~`, err)
		res.Fail(c, 4001)
		return
	}
	// validate
	if code := validName(body.Name); code != 200 {
		res.Fail(c, code)
		return
	}
	// validate
	if code := validPassword(body.Password); code != 200 {
		res.Fail(c, code)
		return
	}
	// createUser
	user := User{}
	user.ID, _ = shortid.Generate()
	user.Name = body.Name
	hashedPassword, _ := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	user.Password = string(hashedPassword)
	user.Phone = body.Phone
	user.Department = body.Department
	user.Company = body.Company
	//No verification required
	user.AccessToken, _, err = accessMid.TokenGenerator(user)
	if err != nil {
		log.Errorf(`addUser, token generator error, details: '%s' ~`, err)
		res.FailMsg(c, "addUser, token generator error")
		return
	}

	user.Email = user.ID //"cloud@atlasdata.cn"
	user.Activation = "yes"
	user.Verification = "yes"
	err = db.Create(&user).Error
	if err != nil {
		log.Errorf(`addUser, create user error, details: '%s' ~`, err)
		res.Fail(c, 5001)
		return
	}
	//add to user_group
	res.Done(c, "")
}

func signin(c *gin.Context) {
	res := NewRes()
	var body struct {
		Name     string `json:"name" form:"name" binding:"required"`
		Password string `json:"password" form:"password" binding:"required"`
	}
	err := c.Bind(&body)
	if err != nil {
		log.Warnf(`signin, info error, details: '%s' ~`, err)
		res.Fail(c, 4001)
		return
	}

	body.Name = strings.ToLower(body.Name)

	// attemptLogin abuseFilter
	IPCountChan := make(chan int)
	clientIP := c.ClientIP()
	ttl := time.Now().Add(viper.GetDuration("user.attemptExpiration"))
	go func(c chan int) {
		var cnt int
		db.Model(&Attempt{}).Where("ip = ? AND created_at > ?", clientIP, ttl).Count(&cnt)
		c <- cnt
	}(IPCountChan)
	IPCount := <-IPCountChan
	if IPCount > viper.GetInt("user.attempts") {
		res.Fail(c, 4002)
		return
	}

	user := User{}
	if db.Where("name = ?", body.Name).Or("email = ?", body.Name).First(&user).RecordNotFound() {
		res.Fail(c, 4041)
		return
	}
	err = bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(body.Password))
	if err != nil {
		attempt := Attempt{IP: c.ClientIP(), Name: body.Name}
		db.Create(&attempt)
		res.Fail(c, 4011)
		return
	}

	tokenString, expire, err := authMid.TokenGenerator(user)
	//send cookie
	if authMid.SendCookie {
		maxage := int(expire.Unix() - time.Now().Unix())
		c.SetCookie(
			"token",
			tokenString,
			maxage,
			"/",
			authMid.CookieDomain,
			authMid.SecureCookie,
			authMid.CookieHTTPOnly,
		)
	}
	if authMid.SendAuthorization {
		c.Header("Authorization", authMid.TokenHeadName+" "+tokenString)
	}
	user.JWT = tokenString
	go func() {
		err = db.Model(&User{}).Where("name = ?", user.Name).Update(User{JWT: tokenString}).Error
		if err != nil {
			log.Error(err)
			return
		}
	}()
	res.DoneData(c, user)
}

func sendReset(c *gin.Context) {
	res := NewRes()
	var body struct {
		Email string `json:"email" form:"email" binding:"required"`
	}
	err := c.Bind(&body)
	if err != nil {
		log.Warnf(`sendReset, info error, details: '%s' ~`, err)
		res.Fail(c, 4001)
		return
	}
	email := strings.ToLower(body.Email)
	if ok := rEmail.MatchString(email); !ok {
		log.Warnf("sendReset, invalidate email format, email:'%s'", email)
		res.Fail(c, 4013)
		return
	}

	token := generateToken(21)
	hash, _ := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
	user := User{}
	if db.Where("email = ?", body.Email).First(&user).RecordNotFound() {
		log.Warnf(`sendReset, email doesn't exist, email: %s`, body.Email)
		res.Fail(c, 4031)
		return
	}

	user.ResetToken = string(hash)
	user.ResetExpires = time.Now().Add(viper.GetDuration("user.resetExpiration"))

	if err := db.Save(&user).Error; err != nil {
		log.Errorf("sendReset, update reset token error, details: %s; email: %s", err, body.Email)
		res.Fail(c, 5001)
		return
	}

	resetURL := fmt.Sprintf(`%s/resetpw.html?user=%s&token=%s`, rootURL(c.Request), user.Name, string(token))
	log.Debug("loging reset url for debug: " + resetURL)
	go func() {
		mailConf := MailConfig{}
		mailConf.HTMLPath = resetURL
		mailConf.From = viper.GetString("smtp.from.name") + " <" + viper.GetString("smtp.from.address") + ">"
		mailConf.Subject = "地图云-重置密码邮件"
		mailConf.ReplyTo = body.Email

		if err := mailConf.SendMail(); err != nil {
			log.Errorf("sendReset,sending rest password email error, details: %s; user: %s ^^", err.Error(), user.Name)
		}
	}()

	res.Done(c, "")
}

func resetPassword(c *gin.Context) {
	res := NewRes()
	var body struct {
		Password string `form:"password" binding:"required,gt=3"`
		Confirm  string `form:"confirm" binding:"required,eqfield=Password"`
	}

	err := c.Bind(&body)
	if err != nil {
		log.Warnf(`resetPassword, info error, details: '%s' ~`, err)
		res.Fail(c, 4001)
		return
	}
	name := c.Param("user")
	user := User{}
	err = db.Where("name = ?", name).First(&user).Error
	if err != nil {
		if gorm.IsRecordNotFoundError(err) {
			log.Warnf(`resetPassword, user not found ~, id: %s ~`, name)
			res.Fail(c, 4041)
			return
		}
		log.Errorf(`resetPassword, get user info error, details: %s, id: %s ~`, err, name)
		res.Fail(c, 5001)
		return
	}
	if !time.Now().Before(user.ResetExpires) {
		log.Warn(`resetPassword, reset password token expired ~`, err)
		res.FailMsg(c, "reset password token expired")
		return
	}

	err = bcrypt.CompareHashAndPassword([]byte(user.ResetToken), []byte(c.Param("token")))
	if err != nil {
		log.Warnf("resetPassword, reset password token error, id: %s, token: %s", user.Name, c.Param("token"))
		res.FailMsg(c, "reset password token error")
		return
	}
	hashedPassword, _ := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	user.Password = string(hashedPassword)
	user.ResetExpires = time.Now() // after reset set token expi

	if err := db.Save(&user).Error; err != nil {
		log.Errorf("resetPassword, update password: %s; id: %s", err, user.Name)
		res.Fail(c, 5001)
		return
	}
	res.Done(c, "重置完成")
}

func verify(c *gin.Context) {
	res := NewRes()
	name := c.Param("user")
	user := &User{}
	if err := db.Where("name = ?", name).First(&user).Error; err != nil {
		if gorm.IsRecordNotFoundError(err) {
			log.Warnf("verify, user not found, id: %s", name)
			res.Fail(c, 4041)
			return
		}
		log.Errorf(`verify, get user info error, details: %s, id: %s ~`, err, name)
		res.Fail(c, 5001)
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.VerificationToken), []byte(c.Param("token"))); err != nil {
		log.Warnf("verify, verify token error, id: %s, token: %s", name, c.Param("token"))
		res.FailMsg(c, "verify token error")
		return
	}

	if err := db.Model(&User{}).Where("name = ?", name).Updates(User{VerificationToken: "null", Verification: "yes"}).Error; err != nil {
		log.Errorf("verify, update verification info error, details: %s; id: %s ^^", err, name)
		res.Fail(c, 5001)
		return
	}
	res.Done(c, "验证完成")
}

func signout(c *gin.Context) {
	res := NewRes()
	//remove auth cookie
	c.SetCookie(
		"token",
		"",
		0,
		"/",
		authMid.CookieDomain,
		authMid.SecureCookie,
		authMid.CookieHTTPOnly,
	)
	res.Done(c, "")
}

func sendVerification(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	token := generateToken(21)
	hash, _ := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
	user := &User{}
	if err := db.Where("name = ?", id).First(&user).Error; err != nil {
		log.Errorf("sendVerification, get user info error, details: %s; id: %s", err, id)
		res.Fail(c, 5001)
		return
	}

	if err := db.Model(&User{}).Where("name = ?", id).Update(User{VerificationToken: string(hash)}).Error; err != nil {
		log.Errorf("sendVerification, update user info error, details: %s; id: %s", err, id)
		res.Fail(c, 5001)
		return
	}

	verifyURL := rootURL(c.Request) + "/sign/verify/" + id + "/" + string(token) + "/"
	log.Println("loging verify url for debug: " + verifyURL)

	go func() {
		mailConf := MailConfig{}
		mailConf.Data = gin.H{
			"VerifyURL": verifyURL,
		}
		mailConf.From = viper.GetString("smtp.from.name") + " <" + viper.GetString("smtp.from.address") + ">"
		mailConf.Subject = "地图云-账号验证邮件"
		mailConf.ReplyTo = user.Email
		mailConf.HTMLPath = viper.GetString("statics.home") + "email/verification.html"

		if err := mailConf.SendMail(); err != nil {
			log.Errorf(`SendMail, sending verification email error, user: %s, details: '%s' ~`, id, err)
		}
	}()

	res.Done(c, "验证邮件已发送")
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
	uid := c.Param("id")
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	var body struct {
		Phone      string `json:"phone" form:"phone"`
		Department string `json:"department" form:"department"`
		Company    string `json:"company" form:"company"`
	}
	err := c.Bind(&body)
	if err != nil {
		log.Error(err)
		res.Fail(c, 4001)
		return
	}
	err = db.Model(&User{}).Where("name = ?", uid).Update(User{Phone: body.Phone, Department: body.Department, Company: body.Company}).Error
	if err != nil {
		log.Error(err)
		res.Fail(c, 5001)
		return
	}
	res.Done(c, "")
}

func authTokenRefresh(c *gin.Context) {
	res := NewRes()
	uid := c.Param("id")
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	tokenString, expire, err := authMid.RefreshToken(c)
	if err != nil {
		log.Error(err)
		res.FailErr(c, err)
		return
	}
	// set cookie
	if authMid.SendCookie {
		maxage := int(expire.Unix() - time.Now().Unix())
		c.SetCookie(
			"token",
			tokenString,
			maxage,
			"/",
			authMid.CookieDomain,
			authMid.SecureCookie,
			authMid.CookieHTTPOnly,
		)
	}
	res.DoneData(c, gin.H{
		"token":  tokenString,
		"expire": expire.Format(time.RFC3339),
	})
}

func accessTokenRefresh(c *gin.Context) {
	res := NewRes()
	uid := c.Param("id")
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	user := User{Name: uid}
	tokenString, _, err := accessMid.TokenGenerator(user)
	if err != nil {
		log.Error(err)
		res.FailErr(c, err)
		return
	}
	if err := db.Model(&User{}).Where("name = ?", uid).Update(User{AccessToken: tokenString}).Error; err != nil {
		log.Error(err)
		res.Fail(c, 5001)
		return
	}
	res.DoneData(c, gin.H{
		"token": tokenString,
	})
}

func accessTokenAdd(c *gin.Context) {

}

func changePassword(c *gin.Context) {
	res := NewRes()

	var body struct {
		Password    string `json:"password" form:"password"`
		NewPassword string `json:"newpassword" form:"newpassword" binding:"required,gt=3"`
		NewConfirm  string `json:"newconfirm" form:"newconfirm" binding:"required,eqfield=NewPassword"`
	}
	err := c.Bind(&body)
	if err != nil {
		log.Error(err)
		res.Fail(c, 4001)
		return
	}
	uid := c.Param("id")
	if uid == "" {
		uid = c.GetString(identityKey)
		user := User{}
		if db.Where("name = ?", uid).First(&user).RecordNotFound() {
			res.Fail(c, 4041)
			return
		}
		err = bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(body.Password))
		if err != nil {
			log.Warn(err)
			res.FailMsg(c, "密码错误")
			return
		}
	}

	// user.setPassword(body.Password)
	hashedPassword, _ := bcrypt.GenerateFromPassword([]byte(body.NewPassword), bcrypt.DefaultCost)
	err = db.Model(&User{}).Where("name = ?", uid).Update(User{Password: string(hashedPassword)}).Error
	if err != nil {
		log.Errorf("changePassword, update password: %s; user: %s", err, uid)
		res.Fail(c, 5001)
		return
	}
	res.Done(c, "")
}

func listUsers(c *gin.Context) {
	res := NewRes()
	var users []User
	db.Find(&users)
	res.DoneData(c, users)
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
	tmp := &Role{}
	if err := db.Where("id = ?", role.ID).First(&tmp).Error; err != nil {
		if gorm.IsRecordNotFoundError(err) {
			err = db.Create(&role).Error
			if err != nil {
				log.Error(err)
				res.Fail(c, 5001)
				return
			}
			res.Done(c, "")
			return
		}
		res.Fail(c, 5001)
		return
	}
	res.FailMsg(c, "角色ID已存在")
	return
}

func deleteRole(c *gin.Context) {
	res := NewRes()
	rid := c.Param("id")
	if code := checkRole(rid); code != 200 {
		res.Fail(c, code)
		return
	}
	group := viper.GetString("user.group")
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
