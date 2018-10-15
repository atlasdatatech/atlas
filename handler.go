package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
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
	c.HTML(http.StatusOK, "index.html", gin.H{
		"title": "AtlasMap",
	})
}

func renderSignup(c *gin.Context) {
	c.HTML(http.StatusOK, "signup.html", c.Keys)
}

func signup(c *gin.Context) {

	res := NewRes()

	var body struct {
		UserName string `form:"username" binding:"required"`
		Email    string `form:"email" binding:"required"`
		Password string `form:"password" binding:"required"`
	}

	// err := json.NewDecoder(c.Request.Body).Decode(&body)
	err := c.Bind(&body)
	if err != nil {
		res.Fail(c, err)
		return
	}

	// validate
	if ok, err := validate(body.UserName, body.Email, body.Password); !ok {
		res.Fail(c, err)
		return
	}

	user := User{}
	if err := db.Where("name = ?", body.UserName).Or("email = ?", body.Email).First(&user).Error; err != nil {
		if gorm.IsRecordNotFoundError(err) {
		} else {
			res.FailStr(c, "signup: database error")
			return
		}
	}
	// duplicate UsernameCheck EmailCheck
	if len(user.Name) != 0 {
		if user.Name == body.UserName || user.Email == body.Email {
			res.FailStr(c, "signup: username or email already taken")
			return
		}
	}
	// createUser
	user.ID, _ = shortid.Generate()
	user.Activation = "yes"
	user.Name = body.UserName
	hashedPassword, _ := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	user.Password = string(hashedPassword)
	user.Email = strings.ToLower(body.Email)

	//No verification required
	user.JWT, user.JWTExpires, err = authMiddleware.TokenGenerator(&user)
	if err != nil {
		res.FailStr(c, "token: generate token error")
		return
	}

	user.Search = []string{body.UserName, body.Email}

	// createAccount
	account := Account{}
	account.ID, _ = shortid.Generate()

	user.AccountID = account.ID
	account.UserID = user.ID

	account.Search = []string{body.UserName}
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
		res.FailStr(c, "CreateUser: database create error")
		return
	}
	// insertAccount
	err = db.Create(&account).Error
	if err != nil {
		res.FailStr(c, "CreateAccount: database create error")
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
			"UserName":     body.UserName,
		}
		mailConf.From = cfgV.GetString("smtp.from.name") + " <" + cfgV.GetString("smtp.from.address") + ">"
		mailConf.Subject = "Your AtlasMap Account"
		mailConf.ReplyTo = body.Email
		mailConf.HtmlPath = cfgV.GetString("statics.home") + "email/signup.html"

		if err := mailConf.SendMail(); err != nil {
			log.Error("sending verify email: " + err.Error())
		} else {
			log.Info("successful sent verify email for ", user.Name)
		}
	}()

	c.Redirect(http.StatusFound, "/")
}

func renderLogin(c *gin.Context) {
	c.HTML(http.StatusOK, "login.html", c.Keys)
}

func login(c *gin.Context) {
	res := NewRes()
	var body struct {
		UserName string `form:"username" binding:"required"`
		Password string `form:"password" binding:"required"`
	}
	err := c.Bind(&body)
	if err != nil {
		res.Fail(c, err)
		return
	}

	// validate
	if len(body.UserName) == 0 || len(body.Password) == 0 {
		res.FailStr(c, "username or passwor required")
		return
	}

	body.UserName = strings.ToLower(body.UserName)
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
		db.Model(&Attempt{}).Where("ip = ? AND name = ? AND created_at > ?", clientIP, body.UserName, ttl).Count(&cnt)
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
	if db.Where("name = ?", body.UserName).Or("email = ?", body.UserName).First(&user).RecordNotFound() {
		res.FailStr(c, "check username or email")
		return
	}

	err = bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(body.Password))

	if err != nil {
		attempt := Attempt{IP: clientIP, Name: body.UserName}
		db.Create(&attempt)
		res.Fail(c, errors.New("check password"))
		return
	}

	//Cookie
	if authMiddleware.SendCookie {
		maxage := int(user.JWTExpires.Unix() - time.Now().Unix())
		c.SetCookie(
			"JWTToken",
			user.JWT,
			maxage,
			"/",
			authMiddleware.CookieDomain,
			authMiddleware.SecureCookie,
			authMiddleware.CookieHTTPOnly,
		)
	}

	c.Redirect(http.StatusFound, "/account/")
}

func logout(c *gin.Context) {
	c.Redirect(http.StatusFound, "/")
}

func renderForgot(c *gin.Context) {
	c.HTML(http.StatusOK, "forgot.html", c.Keys)
}

func sendReset(c *gin.Context) {
	res := NewRes()
	var body struct {
		UserName string `form:"username"`
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
		res.FailStr(c, `update: database save resetpassword token error`)
		return
	}

	resetURL := "http" + "://" + c.Request.Host + "/login/reset/" + body.Email + "/" + string(token) + "/"
	log.Debug("loging reset url for debug: " + resetURL)
	go func() {
		mailConf := MailConfig{}
		mailConf.Data = gin.H{
			"ResetURL": resetURL,
			"UserName": user.Name,
		}
		mailConf.From = cfgV.GetString("smtp.from.name") + " <" + cfgV.GetString("smtp.from.address") + ">"
		mailConf.Subject = "Your AtlasMap Account"
		mailConf.ReplyTo = body.Email
		mailConf.HtmlPath = cfgV.GetString("statics.home") + "email/reset.html"

		if err := mailConf.SendMail(); err != nil {
			log.Errorf("sending rest password email for %s,Error: %s ^^", user.Name, err.Error())
		} else {
			log.Info("successful sent rest password email for", user.Name)
		}

	}()

	res.Done(c, string(token))
}

func renderReset(c *gin.Context) {
	c.HTML(http.StatusOK, "reset.html", gin.H{
		"title": "AtlasMap",
		"email": c.Param("email"),
		"token": c.Param("token"),
	}) // can't handle /login/reset/:email:token
}

func resetPassword(c *gin.Context) {
	res := NewRes()
	var body struct {
		Confirm  string `form:"confirm" binding:"required"`
		Password string `form:"password" binding:"required"`
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
		res.Error = "reset password token expired"
		c.JSON(http.StatusOK, res)
		return
	}

	err = bcrypt.CompareHashAndPassword([]byte(user.ResetPasswordToken), []byte(c.Param("token")))
	if err != nil {
		res.Error = "reset password token error"
		c.JSON(http.StatusOK, res)
		return
	}
	hashedPassword, _ := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	user.Password = string(hashedPassword)
	user.ResetPasswordExpires = time.Now() // after reset set token expi

	if err := db.Save(&user).Error; err != nil {
		res.Error = "update user password error: " + err.Error()
		c.JSON(http.StatusOK, res)
		return
	}
	res.Reset()
	res.Message = "reset password successful"
	c.JSON(http.StatusOK, res)
	return
}

func renderAccount(c *gin.Context) {
	res := NewRes()
	user := &User{}
	if err := db.Where(identityKey+" = ?", c.GetString(identityKey)).First(&user).Error; err != nil {
		res.Fail(c, err)
		return
	}
	account := &Account{}
	if err := db.Where("id = ?", user.AccountID).First(&account).Error; err != nil {
		res.Fail(c, err)
		return
	}

	c.HTML(http.StatusOK, "account.html", gin.H{
		"username":  user.Name,
		"email":     user.Email,
		"userid":    user.ID,
		"active":    user.Activation,
		"jwt":       user.JWT,
		"jwtexpire": user.JWTExpires.Format("2006-01-02 03:04:05 PM"),
		"accountid": account.ID,
		"verified":  account.Verification,
		"phone":     account.Phone,
	})
}

func renderVerification(c *gin.Context) {
	res := NewRes()
	user := &User{}
	if err := db.Where(identityKey+" = ?", c.GetString(identityKey)).First(&user).Error; err != nil {
		res.Fail(c, err)
		return
	}
	account := &Account{}
	if err := db.Where("id = ?", user.AccountID).First(&account).Error; err != nil {
		res.Fail(c, err)
		return
	}

	if account.Verification == "yes" {
		c.Redirect(http.StatusFound, "/account/")
		return
	}

	if len(account.VerificationToken) > 0 {
		c.HTML(http.StatusOK, "verify.html", gin.H{
			"tile":     "Atlas Map",
			"hasSend":  true,
			"username": user.Name,
		})
	}
}

func sendVerification(c *gin.Context) {
	res := NewRes()
	token := generateToken(21)
	hash, _ := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
	user := &User{}
	if err := db.Where(identityKey+" = ?", c.GetString(identityKey)).First(&user).Error; err != nil {
		res.Fail(c, err)
		return
	}

	if err := db.Model(&Account{}).Where("id = ?", user.AccountID).Update(Account{VerificationToken: string(hash)}).Error; err != nil {
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
		mailConf.HtmlPath = cfgV.GetString("statics.home") + "email/verification.html"

		if err := mailConf.SendMail(); err != nil {
			log.Println("Error Sending verification Email: " + err.Error())
		} else {
			log.Println("Successful Sent verification Email.")
		}
	}()

	res.Done(c, "A verification email has been sent")
}

func verify(c *gin.Context) {
	res := NewRes()
	log.Debug("user:" + c.Param("user"))
	log.Debug("token:" + c.Param("token"))
	user := &User{}
	if err := db.Where("name = ?", c.Param("user")).First(&user).Error; err != nil {
		res.Fail(c, err)
		return
	}
	account := &Account{}
	if err := db.Where("id = ?", user.AccountID).First(&account).Error; err != nil {
		res.Fail(c, err)
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(account.VerificationToken), []byte(c.Param("token"))); err != nil {
		log.Errorf("account verfiy for user %s failed, token: %s ^^", user.Name, account.VerificationToken)
		res.Fail(c, err)
		return
	}

	if err := db.Model(&Account{}).Where("id = ?", account.ID).Updates(Account{VerificationToken: "null", Verification: "yes"}).Error; err != nil {
		res.Fail(c, err)
		return
	}
	c.Redirect(http.StatusFound, "/account/")
}

func jwtRefresh(c *gin.Context) {
	res := NewRes()
	tokenString, expire, err := authMiddleware.RefreshToken(c)
	if err != nil {
		log.Warn("http.StatusUnauthorized")
		res.Fail(c, err)
		return
	}

	uid := c.GetString(identityKey)
	if err := db.Model(&User{}).Where("id = ?", uid).Update(User{JWT: tokenString, JWTExpires: expire}).Error; err != nil {
		log.Error("database update jwt error, user id=" + uid)
		res.Fail(c, err)
		return
	}

	cookie, err := c.Cookie("JWTToken")
	if err != nil {
		res.Fail(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    http.StatusOK,
		"token":   tokenString,
		"expire":  expire.Format(time.RFC3339),
		"message": "refresh successfully",
		"cookie":  cookie,
	})

}

func renderChangePassword(c *gin.Context) {
	c.HTML(http.StatusOK, "change.html", gin.H{
		"title": "AtlasMap",
	}) // can't handle /login/reset/:email:token
}

func changePassword(c *gin.Context) {
	res := NewRes()
	userID := c.GetString(identityKey)
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
	err = db.Model(&User{}).Where("id = ?", userID).Update(User{Password: string(hashedPassword)}).Error
	if err != nil {
		res.Fail(c, err)
		return
	}
	res.Done(c, "change pass world success")
}

func studioIndex(c *gin.Context) {
	//public
	uid := c.GetString(identityKey) //for user privite tiles
	log.Infof("user:%s", uid)

	c.HTML(http.StatusOK, "index.html", gin.H{
		"Title":    "maps",
		"Styles":   ss.Styles,
		"Tilesets": ss.Tilesets,
	})

	// c.JSON(http.StatusOK, services)
}

func listStyles(c *gin.Context) {
	//user && public
	user := c.Param("user") //for user privite tiles
	log.Infof("user:%s", user)

	var userStyles map[string]*StyleService

	for k, s := range ss.Styles {
		if user == s.User || "public" == s.User {
			out := &StyleService{
				User: s.User,
				ID:   s.ID,
				URL:  s.URL,
			}
			userStyles[k] = out
		}
	}

	c.JSON(http.StatusOK, userStyles)
}

func getStyle(c *gin.Context) {
	//public
	res := NewRes()
	user := c.Param("user") //for user privite tiles
	log.Infof("user:%s", user)

	id := c.Param("sid")
	if strings.HasSuffix(strings.ToLower(id), ".json") {
		id = strings.Split(id, ".")[0]
	}
	style, ok := ss.Styles[id]
	if !ok {
		log.Warnf("The style id(%s) not exist in the service", id)
		res.FailStr(c, fmt.Sprintf("The style id(%s) not exist in the service", id))
		return
	}

	var out map[string]interface{}
	json.Unmarshal([]byte(*style.Style), &out)
	protoScheme := scheme(c.Request)
	fixURL := func(url string) string {
		if "" == url || !strings.HasPrefix(url, "local://") {
			return url
		}
		return strings.Replace(url, "local://", protoScheme+"://"+c.Request.Host+"/", -1)
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
	//user privite
}

func getSprite(c *gin.Context) {
	res := NewRes()
	user := c.Param("user") //for user privite tiles
	log.Infof("user:%s", user)
	id := c.Param("sid")
	log.Infof("sid:%s", id)
	sprite := c.Param("sprite")
	spritePat := `^sprite(@[2]x)?.(?:json|png)$`
	if ok, err := regexp.MatchString(spritePat, sprite); !ok {
		log.Errorf(`get sprite MatchString return: false, MatchString error: %v, sprite param: %s ^^`, err, sprite)
		res.FailStr(c, fmt.Sprintf(`get sprite MatchString return: false, MatchString error: %v, sprite param: %s ^^`, err, sprite))
		return
	}
	stylesPath := cfgV.GetString("styles.path")
	if strings.HasSuffix(strings.ToLower(sprite), ".json") {
		c.Writer.Header().Set("Content-Type", "application/json")
	}
	if strings.HasSuffix(strings.ToLower(sprite), ".png") {
		c.Writer.Header().Set("Content-Type", "image/png")
	}
	spriteFile := stylesPath + id + "/" + sprite
	file, err := ioutil.ReadFile(spriteFile)
	if err != nil {
		res.Fail(c, err)
		return
	}
	c.Writer.Write(file)
}

func viewStyle(c *gin.Context) {
	id := c.Param("sid")
	style, ok := ss.Styles[id]
	if !ok {
		log.Warnf("The style id(%s) not exist in the service", id)
		c.JSON(http.StatusOK, gin.H{
			"id":    id,
			"error": "Can't find style.",
		})
		return
	}
	fmt.Println(style)

	stylejsonURL := strings.TrimSuffix(c.Request.URL.Path, "/")
	// tilejsonURL = tilejsonURL + ".json"

	c.HTML(http.StatusOK, "viewer.html", gin.H{
		"Title": id,
		"ID":    id,
		"URL":   stylejsonURL,
	})

}

func listTilesets(c *gin.Context) {
	//user && public
	user := c.Param("user") //for user privite tiles
	log.Infof("user:%s", user)

	var userTilesets map[string]*MBTilesService

	// for k, t := range ss.Tilesets {
	// 	if user == ts.User || "public" == t.User {
	// 		out := &StyleService{
	// 			User: t.User,
	// 			ID:   t.ID,
	// 			URL:  t.URL,
	// 		}
	// 		userTilesets[k] = out
	// 	}
	// }

	c.JSON(http.StatusOK, userTilesets)

}

func getTilejson(c *gin.Context) {
	//public
	res := NewRes()
	user := c.Param("user") //for user privite tiles
	log.Infof("user:%s", user)

	id := c.Param("tid")
	if strings.HasSuffix(strings.ToLower(id), ".json") {
		id = strings.Split(id, ".")[0]
	}
	tileServie, ok := ss.Tilesets[id]
	if !ok {
		log.Warnf("The tileset id(%s) not exist in the service", id)
		res.FailStr(c, fmt.Sprintf("The tileset id(%s) not exist in the service", id))
		return
	}

	url := strings.Split(c.Request.URL.Path, ".")[0]
	url = fmt.Sprintf("%s%s", ss.rootURL(c.Request), url)
	tileset := tileServie.Mbtiles
	imgFormat := tileset.TileFormatString()
	out := map[string]interface{}{
		"tilejson": "2.1.0",
		"id":       id,
		"scheme":   "xyz",
		"format":   imgFormat,
		"tiles":    []string{fmt.Sprintf("%s/{z}/{x}/{y}.%s", url, imgFormat)},
		"map":      url + "/",
	}
	metadata, err := tileset.GetInfo()
	if err != nil {
		log.WithError(err).Infof("Get metadata failed : %s", err.Error())
		res.FailStr(c, fmt.Sprintf("Get metadata failed : %s", err.Error()))
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
	//user privite
}

func viewTile(c *gin.Context) {
	res := NewRes()
	id := c.Param("tid")
	tileService, ok := ss.Tilesets[id]
	if !ok {
		log.Warnf("The tileset id(%s) not exist in the service", id)
		res.FailStr(c, fmt.Sprintf("The tileset id(%s) not exist in the service", id))
		return
	}

	tilejsonURL := strings.TrimSuffix(c.Request.URL.Path, "/")
	// tilejsonURL = tilejsonURL + ".json"

	c.HTML(http.StatusOK, "data.html", gin.H{
		"Title": id,
		"ID":    id,
		"URL":   tilejsonURL,
		"fmt":   tileService.Mbtiles.TileFormatString(),
	})

}

func getTile(c *gin.Context) {
	res := NewRes()
	// split path components to extract tile coordinates x, y and z
	pcs := strings.Split(c.Request.URL.Path[1:], "/")
	log.Debug(pcs)
	// we are expecting at least "tilesets", :user , :id, :z, :x, :y + .ext
	size := len(pcs)
	if size < 6 || pcs[5] == "" {
		res.FailStr(c, fmt.Sprintf("get tile requested path is too short, urlpath: %s", c.Request.URL.Path))
		return
	}
	user := pcs[1]
	log.Debug(user)
	id := pcs[2]
	tileService, ok := ss.Tilesets[id]
	if !ok {
		log.Errorf("The tileset id(%s) not exist in the service", id)
		res.FailStr(c, fmt.Sprintf("The tileset id(%s) not exist in the service", id))
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
		err = fmt.Errorf("cannot fetch %s from DB for z=%d, x=%d, y=%d: %v", t, tc.z, tc.x, tc.y, err)
		log.WithError(err)
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

func getGlyphs(c *gin.Context) {
	//public
	res := NewRes()
	user := c.Param("user") //for user privite tiles
	log.Infof("user:%s", user)
	fonts := c.Param("fontstack")
	log.Infof("fontstack:%s", fonts)
	rgPBF := c.Param("rangepbf")
	rgPBF = strings.ToLower(rgPBF)
	rgPBFPat := `[\d]+-[\d]+.pbf$`
	if ok, _ := regexp.MatchString(rgPBFPat, rgPBF); !ok {
		res.FailStr(c, fmt.Sprintf("glyph range pattern error,range:%s", rgPBF))
		return
	}
	fontsPath := cfgV.GetString("fonts.path")

	//should init first
	lastModified := time.Now().UTC().Format("2006-01-02 03:04:05 PM")
	var callbacks []string
	for k := range ss.Fonts {
		callbacks = append(callbacks, k)
	}

	pbfFile := getFontsPBF(fontsPath, fonts, rgPBF, callbacks)
	c.Writer.Header().Set("Content-Type", "application/x-protobuf")
	c.Writer.Header().Set("Last-Modified", lastModified)
	c.Writer.Write(pbfFile)
}

func getFonts(c *gin.Context) {
	c.Writer.Header().Set("Content-Type", "application/json")
	c.JSON(http.StatusOK, ss.Fonts)
}
