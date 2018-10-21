package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
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
	claim, err := authMid.GetClaimsFromJWT(c)
	if err != nil {
		c.HTML(http.StatusOK, "index.html", gin.H{
			"Title": "AtlasMap",
			"Login": true,
		})
	}
	log.Debug(claim)
	c.Redirect(http.StatusFound, "/studio/")
}

func renderSignup(c *gin.Context) {
	c.HTML(http.StatusOK, "signup.html", gin.H{
		"Title": "AtlasMap",
	})
}

func signup(c *gin.Context) {
	res := NewRes()
	var body struct {
		Name     string `form:"name" binding:"required"`
		Email    string `form:"email" binding:"required"`
		Password string `form:"password" binding:"required"`
	}
	err := c.Bind(&body)
	if err != nil {
		res.Fail(c, err)
		return
	}
	// validate
	if ok, err := validate(body.Name, body.Email, body.Password); !ok {
		res.Fail(c, err)
		return
	}
	user := User{}
	if err := db.Where("name = ?", body.Name).Or("email = ?", body.Email).First(&user).Error; err != nil {
		if gorm.IsRecordNotFoundError(err) {
		} else {
			res.FailStr(c, "get user info error")
			log.Errorf("signup, get user info: %s; user: %s", err, body.Name)
			return
		}
	}
	// duplicate UsernameCheck EmailCheck
	if len(user.Name) != 0 {
		if user.Name == body.Name || user.Email == body.Email {
			res.FailStr(c, "name or email already taken")
			return
		}
	}
	// createUser
	user.ID, _ = shortid.Generate()
	user.Activation = "yes"
	user.Name = body.Name
	hashedPassword, _ := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	user.Password = string(hashedPassword)
	user.Email = strings.ToLower(body.Email)
	//No verification required
	user.JWT, user.JWTExpires, err = authMid.TokenGenerator(&user)
	if err != nil {
		res.FailStr(c, "generate token error")
		return
	}
	user.Search = []string{body.Name, body.Email}
	// createAccount
	account := Account{}
	account.ID, _ = shortid.Generate()
	user.AccountID = account.ID
	account.UserID = user.ID
	account.Search = []string{body.Name}
	var verifyURL string
	if cfgV.GetBool("account.verification") {
		account.Verification = "no"
		//Create a verification token
		token := generateToken(21)
		hash, _ := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
		account.VerificationToken = string(hash)
		verifyURL = "http" + "://" + c.Request.Host + "/account/verification/" + user.Name + "/" + string(token) + "/"
	} else {
		account.Verification = "yes"
	}
	// insertUser
	err = db.Create(&user).Error
	if err != nil {
		res.FailStr(c, "create user error")
		return
	}
	// insertAccount
	err = db.Create(&account).Error
	if err != nil {
		res.FailStr(c, "create account error")
		return
	}
	// sendWelcomeEmail
	log.Debug("Loging verify url for debug: " + verifyURL)
	go func() {
		mailConf := MailConfig{}
		mailConf.Data = gin.H{
			"VerifyURL":    verifyURL,
			"Verification": account.Verification,
			"LoginURL":     "http://" + c.Request.Host + "/login/",
			"Email":        body.Email,
			"Name":         body.Name,
		}
		mailConf.From = cfgV.GetString("smtp.from.name") + " <" + cfgV.GetString("smtp.from.address") + ">"
		mailConf.Subject = "Your AtlasMap Account"
		mailConf.ReplyTo = body.Email
		mailConf.HTMLPath = cfgV.GetString("statics.home") + "email/signup.html"

		if err := mailConf.SendMail(); err != nil {
			log.Errorf("signup, sending verify email: %s; user: %s", err, body.Name)
		}
	}()
	createPaths(user.Name)

	casEnf.LoadPolicy()
	casEnf.AddGroupingPolicy(user.Name, "user_group")
	casEnf.SavePolicy()

	c.Redirect(http.StatusFound, "/")
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
	if db.Where("name = ?", body.Name).Or("email = ?", body.Name).First(&user).RecordNotFound() {
		res.FailStr(c, "check username or email")
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
		maxage := int(user.JWTExpires.Unix() - time.Now().Unix())
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

	//loading user service
	userSet[user.Name], err = LoadServiceSet(user.Name)
	if err != nil {
		log.Errorf("login,load user service set: %s; user: %s ^^", err.Error(), user.Name)
		res.FailStr(c, "check password")
		return
	}

	c.Redirect(http.StatusFound, "/studio/")
}

func logout(c *gin.Context) {
	c.SetCookie(
		"Token",
		"logout",
		0,
		"/",
		authMid.CookieDomain,
		authMid.SecureCookie,
		authMid.CookieHTTPOnly,
	)
	c.Redirect(http.StatusFound, "/")
}

func renderForgot(c *gin.Context) {
	c.HTML(http.StatusOK, "forgot.html", gin.H{
		"Title": "AtlasMap",
	})
}

func sendReset(c *gin.Context) {
	res := NewRes()
	var body struct {
		Email    string `form:"email" binding:"required"`
		Password string `form:"password"`
	}
	err := c.Bind(&body)
	if err != nil {
		res.Fail(c, err)
		return
	}
	if ok := rEmail.MatchString(body.Email); !ok {
		res.FailStr(c, `email: invalid email`)
		return
	}
	token := generateToken(21)
	hash, _ := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
	user := User{}
	if db.Where("email = ?", body.Email).First(&user).RecordNotFound() {
		res.FailStr(c, `email: email doesn't exist`)
		return
	}

	user.ResetPasswordToken = string(hash)
	user.ResetPasswordExpires = time.Now().Add(cfgV.GetDuration("password.restExpiration"))

	if err := db.Save(&user).Error; err != nil {
		log.Errorf("sendReset,update reset password: %s; email: %s", err, body.Email)
		res.FailStr(c, `update reset password error`)
		return
	}

	resetURL := "http" + "://" + c.Request.Host + "/login/reset/" + body.Email + "/" + string(token) + "/"
	log.Debug("loging reset url for debug: " + resetURL)
	go func() {
		mailConf := MailConfig{}
		mailConf.Data = gin.H{
			"ResetURL": resetURL,
			"Name":     user.Name,
		}
		mailConf.From = cfgV.GetString("smtp.from.name") + " <" + cfgV.GetString("smtp.from.address") + ">"
		mailConf.Subject = "Your AtlasMap Account"
		mailConf.ReplyTo = body.Email
		mailConf.HTMLPath = cfgV.GetString("statics.home") + "email/reset.html"

		if err := mailConf.SendMail(); err != nil {
			log.Errorf("sendReset,sending rest password email: %s; user: %s ^^", err.Error(), user.Name)
		}
	}()

	res.Done(c, string(token))
}

func renderReset(c *gin.Context) {
	c.HTML(http.StatusOK, "reset.html", gin.H{
		"Title": "AtlasMap",
		"Email": c.Param("email"),
		"Token": c.Param("token"),
	}) // can't handle /login/reset/:email:token
}

func resetPassword(c *gin.Context) {
	res := NewRes()
	var body struct {
		Password string `form:"password" binding:"required"`
		Confirm  string `form:"confirm" binding:"required"`
	}
	err := c.Bind(&body)
	if err != nil {
		res.Fail(c, err)
		return
	}

	if len(body.Password) == 0 || len(body.Confirm) == 0 {
		res.FailStr(c, "password and confirm required")
		return
	}
	if body.Password != body.Confirm {
		res.FailStr(c, "passwords do not match")
		return
	}

	user := User{}
	err = db.Where("email = ? AND reset_password_expires > ?", c.Param("email"), time.Now()).First(&user).Error
	if err != nil {
		res.FailStr(c, "reset password token expired")
		return
	}

	err = bcrypt.CompareHashAndPassword([]byte(user.ResetPasswordToken), []byte(c.Param("token")))
	if err != nil {
		res.FailStr(c, "reset password token error")
		return
	}
	hashedPassword, _ := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	user.Password = string(hashedPassword)
	user.ResetPasswordExpires = time.Now() // after reset set token expi

	if err := db.Save(&user).Error; err != nil {
		log.Errorf("resetPassword,update password: %s; user: %s", err, user.Name)
		res.FailStr(c, "update user password error: "+err.Error())
		return
	}
	c.Redirect(http.StatusFound, "/login/")
	// res.Done(c, "reset password successful")
	return
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
	account := &Account{}
	if err := db.Where("id = ?", user.AccountID).First(&account).Error; err != nil {
		log.Errorf("renderAccount, get account info: %s; user: %s", err, id)
		res.FailStr(c, "get account error")
		return
	}

	c.HTML(http.StatusOK, "account.html", gin.H{
		"Title":     "AtlasMap",
		"Name":      user.Name,
		"Email":     user.Email,
		"ID":        user.ID,
		"Active":    user.Activation,
		"JWT":       user.JWT,
		"JWTExpire": user.JWTExpires.Format("2006-01-02 03:04:05 PM"),
		"AccountID": account.ID,
		"Verified":  account.Verification,
		"Phone":     account.Phone,
	})
}

func renderVerification(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	user := &User{}
	if err := db.Where("name = ?", id).First(&user).Error; err != nil {
		res.Fail(c, err)
		if !gorm.IsRecordNotFoundError(err) {
			log.Errorf("renderVerification, get user info: %s; user: %s", err, id)
		}
		return
	}
	account := &Account{}
	if err := db.Where("id = ?", user.AccountID).First(&account).Error; err != nil {
		log.Errorf("renderVerification, get account info: %s; user: %s", err, id)
		res.Fail(c, err)
		return
	}

	if account.Verification == "yes" {
		c.Redirect(http.StatusFound, "/account/")
		return
	}

	if len(account.VerificationToken) > 0 {
		c.HTML(http.StatusOK, "verify.html", gin.H{
			"Title": "AtlasMap",
			"Send":  true,
			"Name":  user.Name,
		})
	}
}

func sendVerification(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	token := generateToken(21)
	hash, _ := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
	user := &User{}
	if err := db.Where("name = ?", id).First(&user).Error; err != nil {
		res.Fail(c, err)
		if !gorm.IsRecordNotFoundError(err) {
			log.Errorf("sendVerification, get user info: %s; user: %s", err, id)
		}
		return
	}

	if err := db.Model(&Account{}).Where("id = ?", user.AccountID).Update(Account{VerificationToken: string(hash)}).Error; err != nil {
		log.Errorf("sendVerification, get account info: %s; user: %s", err, id)
		res.Fail(c, err)
		return
	}

	verifyURL := "http" + "://" + c.Request.Host + "/account/verification/" + user.Name + "/" + string(token) + "/"
	log.Println("loging verify url for debug: " + verifyURL)
	go func() {

		mailConf := MailConfig{}
		mailConf.Data = gin.H{
			"VerifyURL": verifyURL,
		}
		mailConf.From = cfgV.GetString("smtp.from.name") + " <" + cfgV.GetString("smtp.from.address") + ">"
		mailConf.Subject = "Your AtlasMap Account"
		mailConf.ReplyTo = user.Email
		mailConf.HTMLPath = cfgV.GetString("statics.home") + "email/verification.html"

		if err := mailConf.SendMail(); err != nil {
			log.Errorf("sendVerification, sending verification email: %s; user: %s", err, id)
		}
	}()

	res.Done(c, "A verification email has been sent")
}

func verify(c *gin.Context) {
	res := NewRes()
	name := c.Param("user")
	user := &User{}
	if err := db.Where("name = ?", name).First(&user).Error; err != nil {
		res.Fail(c, err)
		if !gorm.IsRecordNotFoundError(err) {
			log.Errorf("verify, get user info: %s; user: %s", err, name)
		}
		return
	}
	account := &Account{}
	if err := db.Where("id = ?", user.AccountID).First(&account).Error; err != nil {
		log.Errorf("verify, get account info: %s; user: %s", err, name)
		res.Fail(c, err)
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(account.VerificationToken), []byte(c.Param("token"))); err != nil {
		res.Fail(c, err)
		return
	}

	if err := db.Model(&Account{}).Where("id = ?", user.AccountID).Updates(Account{VerificationToken: "null", Verification: "yes"}).Error; err != nil {
		log.Errorf("verify,update verification: %s; user: %s ^^", err, name)
		res.Fail(c, err)
		return
	}
	c.Redirect(http.StatusFound, "/account/")
}

func jwtRefresh(c *gin.Context) {
	id := c.GetString(identityKey)
	res := NewRes()
	tokenString, expire, err := authMid.RefreshToken(c)
	if err != nil {
		log.Errorf("jwtRefresh, refresh token: %s; user: %s", err, id)
		res.Fail(c, err)
		return
	}
	if err := db.Model(&User{}).Where("name = ?", id).Update(User{JWT: tokenString, JWTExpires: expire}).Error; err != nil {
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

func renderChangePassword(c *gin.Context) {
	c.HTML(http.StatusOK, "change.html", gin.H{
		"Title": "AtlasMap",
	}) // can't handle /login/reset/:email:token
}

func changePassword(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	var body struct {
		Confirm  string `form:"confirm" binding:"required"`
		Password string `form:"password" binding:"required"`
	}
	err := c.Bind(&body)
	if err != nil {
		res.Fail(c, err)
		return
	}

	// validate
	if len(body.Password) == 0 || len(body.Confirm) == 0 {
		res.FailStr(c, `password and confirm required and can't empty`)
		return
	}
	if body.Password != body.Confirm {
		res.FailStr(c, `passwords do not match`)
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
	res.Done(c, "change pass world success")
}

func studioIndex(c *gin.Context) {
	//public
	res := NewRes()
	id := c.GetString(identityKey) //for user privite tiles
	us, ok := userSet[id]
	if !ok {
		log.Errorf("studioIndex, user's service set not exist; user: %s", id)
		c.Redirect(http.StatusFound, "/login/")
		res.FailStr(c, fmt.Sprintf("user's service set not found; user: %s", id))
		return
	}

	c.HTML(http.StatusOK, "index.html", gin.H{
		"Title":    "AtlasMap",
		"Login":    false,
		"User":     id,
		"Styles":   us.Styles,
		"Tilesets": us.Tilesets,
	})
}

func studioCreater(c *gin.Context) {
	//public
	id := c.GetString(identityKey) //for user privite tiles
	c.HTML(http.StatusOK, "creater.html", gin.H{
		"Title":    "Creater",
		"User":     id,
		"Styles":   userSet[pubUser].Styles,
		"Tilesets": userSet[pubUser].Tilesets,
	})
}

func studioEditer(c *gin.Context) {
	//public
	res := NewRes()
	res.Done(c, "deving")
}

//GetStyleService get styleservice
func GetStyleService(c *gin.Context) (*StyleService, error) {
	id := c.GetString(identityKey)
	user := c.Param("user")
	if id != user && !casEnf.Enforce(id, c.Request.URL.String(), c.Request.Method) {
		return nil, fmt.Errorf("user: %s,url: %s,method: %s,auth: %v ^", id, c.Request.URL.String(), c.Request.Method, false)
	}

	us, ok := userSet[user]
	if !ok {
		var err error
		us, err = LoadServiceSet(user)
		if err != nil {
			return nil, fmt.Errorf("user's service set not found and load err")
		}
		userSet[user] = us
	}
	sid := c.Param("sid")
	style, ok := us.Styles[sid]
	if !ok {
		return nil, fmt.Errorf("style id(%s) not exist in the service", sid)
	}
	return style, nil
}

//listStyles list user style
func listStyles(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	us, ok := userSet[id]
	if !ok {
		log.Errorf(`listStyles, user's service set not found; user: %s`, id)
		res.FailStr(c, fmt.Sprintf("user's service set not found %s", id))
		return
	}
	c.JSON(http.StatusOK, us.Styles)
}

//getStyle get user style by id
func getStyle(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	style, err := GetStyleService(c)
	if err != nil {
		log.Errorf("getStyle, error: %s; user: %s ^^", err, id)
		res.Fail(c, err)
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
	style, err := GetStyleService(c)
	if err != nil {
		log.Errorf("getSprite, error: %s; user: %s ^^", err, id)
		res.Fail(c, err)
		return
	}

	sprite := c.Param("sprite")
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

//viewStyle load style map
func viewStyle(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	_, err := GetStyleService(c)
	if err != nil {
		log.Errorf("viewStyle, error: %s; user: %s ^^", err, id)
		res.Fail(c, err)
		return
	}
	styleID := c.Param("sid")
	c.HTML(http.StatusOK, "viewer.html", gin.H{
		"Title": "Viewer",
		"ID":    styleID,
		"URL":   strings.TrimSuffix(c.Request.URL.Path, "/"),
	})
}

//listTilesets list user's tilesets
func listTilesets(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	us, ok := userSet[id]
	if !ok {
		log.Errorf(`listTilesets, user's service set not found; user: %s`, id)
		res.FailStr(c, fmt.Sprintf("user's service set not found %s", id))
		return
	}
	c.JSON(http.StatusOK, us.Tilesets)
}

//GetTilesetService get from context
func GetTilesetService(c *gin.Context) (*MBTilesService, error) {
	id := c.GetString(identityKey)
	user := c.Param("user")
	if id != user && !casEnf.Enforce(id, c.Request.URL.String(), c.Request.Method) {
		return nil, fmt.Errorf("user: %s,url: %s,method: %s,auth: %v ^", id, c.Request.URL.String(), c.Request.Method, false)
	}
	us, ok := userSet[user]
	if !ok {
		var err error
		us, err = LoadServiceSet(user)
		if err != nil {
			return nil, fmt.Errorf("user's service set not found and load err")
		}
		userSet[user] = us
	}
	tid := c.Param("tid")
	tileService, ok := us.Tilesets[tid]
	if !ok {
		// tileService, ok = userSet[pubUser].Tilesets[tid]
		// if !ok {
		return nil, fmt.Errorf("tilesets id(%s) not exist in the service", tid)
		// }
	}
	return tileService, nil
}

//getTilejson get tilejson
func getTilejson(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	tid := c.Param("tid")
	tileServie, err := GetTilesetService(c)
	if err != nil {
		log.Errorf("getTilejson, error: %s; user: %s", err, id)
		res.Fail(c, err)
		return
	}

	url := strings.Split(c.Request.URL.Path, ".")[0]
	url = fmt.Sprintf("%s%s", userSet[pubUser].rootURL(c.Request), url) //need use user own service set
	tileset := tileServie.Mbtiles
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
	id := c.GetString(identityKey)
	tid := c.Param("tid")
	tileService, err := GetTilesetService(c)
	if err != nil {
		log.Errorf("viewTile, error: %s; user: %s", err, id)
		res.Fail(c, err)
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
	if size < 6 || pcs[5] == "" {
		res.FailStr(c, "request path is too short")
		return
	}
	id := pcs[2]

	tileService, err := GetTilesetService(c) //need to optimize
	if err != nil {
		log.Errorf("getTile, error: %s; user: %s", err, id)
		res.Fail(c, err)
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

func listFonts(c *gin.Context) {
	c.JSON(http.StatusOK, userSet[pubUser].Fonts)
}

//getGlyphs get glyph pbf
func getGlyphs(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	fonts := c.Param("fontstack")
	rgPBF := c.Param("rangepbf")
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
	for k, v := range userSet[pubUser].Fonts {
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
