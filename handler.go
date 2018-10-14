package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
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

type responseAccount struct {
	Response
	Account
}

func index(c *gin.Context) {
	c.HTML(http.StatusOK, "index.html", gin.H{
		"title": "AtlasMap",
	})
}

func renderSignup(c *gin.Context) {
	c.HTML(http.StatusOK, "signup.html", c.Keys)
}

func signup(c *gin.Context) {
	response := newResponse(c)

	var body struct {
		UserName string `form:"username" binding:"required"`
		Email    string `form:"email" binding:"required"`
		Password string `form:"password" binding:"required"`
	}

	// err := json.NewDecoder(c.Request.Body).Decode(&body)
	err := c.Bind(&body)
	if err != nil {
		response.ErrFor["binding"] = err.Error()
	}

	// validate
	validateUsername(&body.UserName, response)
	validateEmail(&body.Email, response)
	validatePassword(&body.Password, response)

	if response.HasErrors() {
		response.Fail()
		return
	}

	user := User{}
	if err := db.Where("name = ?", body.UserName).Or("email = ?", body.Email).First(&user).Error; err != nil {
		if gorm.IsRecordNotFoundError(err) {
		} else {
			response.ErrFor["signup"] = "username/email valadition error."
			response.Fail()
			return
		}
	}
	// duplicateUsernameCheck
	// duplicateEmailCheck
	if len(user.Name) != 0 {
		if user.Name == body.UserName {
			response.ErrFor["username"] = "username already taken."
		}
		if user.Email == body.Email {
			response.ErrFor["email"] = "email already registered."
		}
	}
	if response.HasErrors() {
		response.Fail()
		return
	}

	// createUser
	user.ID, _ = shortid.Generate()
	user.Activation = "yes"
	user.Name = body.UserName

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		FATAL(err)
	}
	user.Password = string(hashedPassword)

	user.Email = strings.ToLower(body.Email)

	//No verification required
	user.JWT, user.JWTExpires, err = authMiddleware.TokenGenerator(&user)
	if err != nil {
		FATAL(err)
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
		hash, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
		if err != nil {
			FATAL(err)
		}
		account.VerificationToken = string(hash)
		verifyURL = "http" + "://" + c.Request.Host + "/account/verification/" + user.Name + "/" + string(token) + "/"
	} else {
		account.Verification = "yes"
	}

	// insertUser
	err = db.Create(&user).Error
	if err != nil {
		response.Errors = append(response.Errors, err.Error())
		response.Fail()
		return
	}
	// insertAccount
	err = db.Create(&account).Error
	if err != nil {
		response.Errors = append(response.Errors, err.Error())
		response.Fail()
		return
	}
	// sendWelcomeEmail
	log.Println("verifyURL: " + verifyURL)
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
		mailConf.Subject = "Your AtlasData Account"
		mailConf.ReplyTo = body.Email
		mailConf.HtmlPath = cfgV.GetString("statics.home") + "email/signup.html"

		if err := mailConf.SendMail(); err != nil {
			log.Println("Error Sending Welcome Email: " + err.Error())
			log.Println("verifyURL: " + verifyURL)
		} else {
			log.Println("Successful Sent Welcome Email.")
		}
	}()

	c.Redirect(http.StatusFound, "/")
	response.Finish()
}

func renderLogin(c *gin.Context) {
	c.HTML(http.StatusOK, "login.html", c.Keys)
}

func login(c *gin.Context) {

	response := newResponse(c)

	var body struct {
		UserName string `form:"username" binding:"required"`
		Password string `form:"password" binding:"required"`
	}

	err := c.Bind(&body)
	if err != nil {
		response.Errors = append(response.Errors, err.Error())
		response.Fail()
		return
	}

	// validate
	if len(body.UserName) == 0 {
		response.ErrFor["username"] = "required"
	}
	if len(body.Password) == 0 {
		response.ErrFor["password"] = "required"
	}
	if response.HasErrors() {
		response.Fail()
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
		response.Errors = append(response.Errors, "You've reached the maximum number of login attempts. Please try again later.")
		response.Fail()
		return
	}

	// attemptLogin
	user := User{}
	if db.Where("name = ?", body.UserName).Or("email = ?", body.UserName).First(&user).RecordNotFound() {
		response.Errors = append(response.Errors, "check username or email")
		response.Fail()
		return
	}

	err = bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(body.Password))

	if err != nil {
		attempt := Attempt{IP: clientIP, Name: body.UserName}
		db.Create(&attempt)
		response.Errors = append(response.Errors, "check password")
		response.Fail()
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
	// response.Finish()
}

func logout(c *gin.Context) {
	c.Redirect(http.StatusFound, "/")
}

func renderForgot(c *gin.Context) {
	c.HTML(http.StatusOK, "forgot.html", c.Keys)
}

func sendReset(c *gin.Context) {
	response := Response{}
	response.Init(c)

	var body struct {
		UserName string `form:"username"`
		Email    string `form:"email" binding:"required"`
		Password string `form:"password"`
	}

	err := c.Bind(&body)
	if err != nil {
		FATAL(err)
	}

	validateEmail(&body.Email, &response)
	if response.HasErrors() {
		response.Fail()
		return
	}

	token := generateToken(21)
	hash, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
	if err != nil {
		FATAL(err)
	}

	user := User{}
	if db.Where("email = ?", body.Email).First(&user).RecordNotFound() {
		response.ErrFor["email"] = "email doesn't exist."
	}

	if response.HasErrors() {
		response.Fail()
		return
	}

	user.ResetPasswordToken = string(hash)
	user.ResetPasswordExpires = time.Now().Add(cfgV.GetDuration("password.restExpiration"))

	if err := db.Save(&user).Error; err != nil {
		response.ErrFor["gormerr"] = err.Error()
		response.Fail()
		return
	}

	resetURL := "http" + "://" + c.Request.Host + "/login/reset/" + body.Email + "/" + string(token) + "/"
	log.Println("resetURL: " + resetURL)
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
			log.Println("Error Sending Rest Password Email: " + err.Error())
			log.Println("resetURL: " + resetURL)
		} else {
			log.Println("Successful Sent Rest Password Email.")
		}

	}()

	//for test
	response.ErrFor["UserName"] = user.Name
	response.ErrFor["email"] = body.Email
	response.ErrFor["token"] = string(token)

	response.Finish()
}

func renderReset(c *gin.Context) {
	c.HTML(http.StatusOK, "reset.html", gin.H{
		"title": "AtlasMap",
		"email": c.Param("email"),
		"token": c.Param("token"),
	}) // can't handle /login/reset/:email:token
}

func resetPassword(c *gin.Context) {
	response := Response{}
	response.Errors = []string{}
	response.ErrFor = make(map[string]string)
	response.Init(c)

	var body struct {
		Confirm  string `form:"confirm" binding:"required"`
		Password string `form:"password" binding:"required"`
	}
	err := c.Bind(&body)
	if err != nil {
		response.ErrFor["binding"] = err.Error()
	}

	password := strings.ToLower(body.Password)
	if len(password) == 0 {
		response.ErrFor["password"] = "required"
	}
	confirm := strings.ToLower(body.Confirm)
	if len(confirm) == 0 {
		response.ErrFor["confirm"] = "required"
	}
	if confirm != password {
		response.Errors = append(response.Errors, "Passwords do not match.")
	}
	if response.HasErrors() {
		response.Fail()
		return
	}

	user := User{}
	err = db.Where("email = ? AND reset_password_expires > ?", c.Param("email"), time.Now()).First(&user).Error
	if err != nil {
		response.Errors = append(response.Errors, err.Error())
		response.Fail()
		return
	}

	err = bcrypt.CompareHashAndPassword([]byte(user.ResetPasswordToken), []byte(c.Param("token")))

	if err == nil {
		hashedPassword, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		user.Password = string(hashedPassword)
		user.ResetPasswordExpires = time.Now() // after reset set token expi
		db.Save(&user)
		response.Finish()
	} else {
		response.Errors = append(response.Errors, err.Error())
		response.Fail()
	}
}

func renderAccount(c *gin.Context) {
	user := &User{}
	if err := db.Where(identityKey+" = ?", c.GetString(identityKey)).First(&user).Error; err != nil {
		FATAL(err)
	}
	account := &Account{}
	if err := db.Where("id = ?", user.AccountID).First(&account).Error; err != nil {
		FATAL(err)
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
	user := &User{}
	if err := db.Where(identityKey+" = ?", c.GetString(identityKey)).First(&user).Error; err != nil {
		FATAL(err)
	}
	account := &Account{}
	if err := db.Where("id = ?", user.AccountID).First(&account).Error; err != nil {
		FATAL(err)
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
	response := newResponse(c)

	token := generateToken(21)
	hash, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
	if err != nil {
		response.ErrFor["VerificationToken"] = err.Error()
		response.Fail()
		return
	}
	user := &User{}
	if err := db.Where(identityKey+" = ?", c.GetString(identityKey)).First(&user).Error; err != nil {
		response.ErrFor["gorm"] = err.Error()
		response.Fail()
		return
	}

	if err := db.Model(&Account{}).Where("id = ?", user.AccountID).Update(Account{VerificationToken: string(hash)}).Error; err != nil {
		response.ErrFor["gorm"] = err.Error()
		response.Fail()
		return
	}

	verifyURL := "http" + "://" + c.Request.Host + "/account/verification/" + user.Name + "/" + string(token) + "/"
	log.Println("verifyURL: " + verifyURL)
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
			log.Println("verifyURL: " + verifyURL)
		} else {
			log.Println("Successful Sent verification Email.")
		}
	}()
	response.Data["mgs"] = "A verification email has been sent."
	response.Finish()
}

func verify(c *gin.Context) {
	response := newResponse(c)
	log.Debug("user:" + c.Param("user"))
	log.Debug("token:" + c.Param("token"))
	user := &User{}
	if err := db.Where("name = ?", c.Param("user")).First(&user).Error; err != nil {
		response.ErrFor["gorm"] = err.Error()
		response.Fail()
		return
	}
	account := &Account{}
	if err := db.Where("id = ?", user.AccountID).First(&account).Error; err != nil {
		response.ErrFor["gorm"] = err.Error()
		response.Fail()
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(account.VerificationToken), []byte(c.Param("token"))); err != nil {
		log.Println("verfiyToken:" + account.VerificationToken)
		response.ErrFor["VerificationToken"] = err.Error()
		response.Fail()
		return
	}

	if err := db.Model(&Account{}).Where("id = ?", account.ID).Updates(Account{VerificationToken: "null", Verification: "yes"}); err != nil {
		response.Finish()
	}
	c.Redirect(http.StatusFound, "/account/")
}

func jwtRefresh(c *gin.Context) {

	response := newResponse(c)

	tokenString, expire, err := authMiddleware.RefreshToken(c)
	if err != nil {
		log.Println("http.StatusUnauthorized")
		response.Errors = append(response.Errors, err.Error())
		return
	}

	uid := c.GetString(identityKey)
	if err := db.Model(&User{}).Where("id = ?", uid).Update(User{JWT: tokenString, JWTExpires: expire}).Error; err != nil {
		log.Println("gorm update jwt error, user id=" + uid)
		response.ErrFor["gorm"] = err.Error()
		return
	}

	response.Finish()

	cookie, err := c.Cookie("JWTToken")
	if err != nil {
		log.Println(err)
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
	user := &User{}
	if err := db.Where(identityKey+" = ?", c.GetString(identityKey)).First(&user).Error; err != nil {
		FATAL(err)
	}
	response := newResponse(c)
	err := user.changePassword(response)
	if err != nil {
		response.Fail()
		return
	}
	response.Finish()
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
	user := c.Param("user") //for user privite tiles
	log.Infof("user:%s", user)

	id := c.Param("sid")
	if strings.HasSuffix(strings.ToLower(id), ".json") {
		id = strings.Split(id, ".")[0]
	}
	style, ok := ss.Styles[id]
	if !ok {
		log.Warnf("The style id(%s) not exist in the service", id)
		c.JSON(http.StatusOK, gin.H{
			"id":    id,
			"error": "Can't find style.",
		})
		return
	}

	var out map[string]interface{}
	json.Unmarshal([]byte(*style.Style), &out)

	fixURL := func(url string, c *gin.Context) string {
		if "" == url || !strings.HasPrefix(url, "local://") {
			return url
		}
		// protoScheme := "http"
		// if 2 == c.Request.ProtoMajor {
		// 	protoScheme = "https"
		// }
		protoScheme := scheme(c.Request)
		return strings.Replace(url, "local://", protoScheme+"://"+c.Request.Host+"/", -1)
	}

	for k, v := range out {
		switch v.(type) {
		case string:
			//style->sprite
			if "sprite" == k && v != nil {
				path := v.(string)
				out["sprite"] = fixURL(path, c)
			}
			//style->glyphs
			if "glyphs" == k && v != nil {
				path := v.(string)
				out["glyphs"] = fixURL(path, c)
			}
		case map[string]interface{}:
			if "sources" == k {
				//style->sources
				sources := v.(map[string]interface{})
				for _, u := range sources {
					source := u.(map[string]interface{})
					if url := source["url"]; url != nil {
						fmt.Println()
						source["url"] = fixURL(url.(string), c)
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
	//public
	user := c.Param("user") //for user privite tiles
	log.Infof("user:%s", user)
	id := c.Param("sid")
	log.Infof("sid:%s", id)
	sprite := c.Param("sprite")
	spritePat := `^sprite(@[23]x)?.(?:json|png)$`
	if ok, _ := regexp.MatchString(spritePat, sprite); !ok {
		c.JSON(http.StatusOK, "sprite pattern error")
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
		_, err = c.Writer.Write(file)
	}
	c.JSON(http.StatusOK, err)
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
	user := c.Param("user") //for user privite tiles
	log.Infof("user:%s", user)

	id := c.Param("tid")
	if strings.HasSuffix(strings.ToLower(id), ".json") {
		id = strings.Split(id, ".")[0]
	}
	tileServie, ok := ss.Tilesets[id]
	if !ok {
		log.Warnf("The tileset id(%s) not exist in the service", id)
		c.JSON(http.StatusOK, gin.H{
			"id":    id,
			"error": "Can't find tileset.",
		})
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
	id := c.Param("tid")
	tileService, ok := ss.Tilesets[id]
	if !ok {
		log.Warnf("The tileset id(%s) not exist in the service", id)
		c.JSON(http.StatusOK, gin.H{
			"id":    id,
			"error": "Can't find tileset.",
		})
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
	//public
	response := newResponse(c)
	// split path components to extract tile coordinates x, y and z
	pcs := strings.Split(c.Request.URL.Path[1:], "/")
	log.Debug(pcs)
	// we are expecting at least "tilesets", :user , :id, :z, :x, :y + .ext
	size := len(pcs)
	if size < 6 || pcs[5] == "" {
		response.ErrFor["StatusBadRequest"] = "requested path is too short"
		response.Fail()
		return
	}
	user := pcs[1]
	log.Debug(user)
	id := pcs[2]
	tileService, ok := ss.Tilesets[id]
	if !ok {
		log.Errorf("The tileset id(%s) not exist in the service", id)
		response.ErrFor["ErrID"] = "Can't find tileset"
		response.Fail()
		return
	}

	tileset := tileService.Mbtiles

	z, x, y := pcs[size-3], pcs[size-2], pcs[size-1]
	tc, ext, err := tileCoordFromString(z, x, y)
	if err != nil {
		response.ErrFor["StatusBadRequest"] = err.Error()
		response.Fail()
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
		response.ErrFor["FetchFailed"] = err.Error()
		response.Fail()
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
	_, err = c.Writer.Write(data)
	c.JSON(http.StatusOK, err)
	//user privite
}

func getGlyphs(c *gin.Context) {
	//public
	user := c.Param("user") //for user privite tiles
	log.Infof("user:%s", user)
	fonts := c.Param("fontstack")
	log.Infof("fontstack:%s", fonts)
	u, err := url.Parse(fonts)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(u.Path)
	fonts = u.String()
	fmt.Println(fonts)

	rgPBF := c.Param("rangepbf")
	rgPBF = strings.ToLower(rgPBF)
	rgPBFPat := `^[\\d]+-[\\d]+.pbf$`
	if ok, _ := regexp.MatchString(rgPBFPat, rgPBF); !ok {
		c.JSON(http.StatusOK, "glyph range pattern error")
		return
	}
	fontsPath := cfgV.GetString("fonts.path")

	//should init first
	lastModified := time.Now().UTC().Format("2006-01-02 03:04:05 PM")
	callbacks := make([]string, 0, len(ss.Fonts))
	for k := range ss.Fonts {
		callbacks = append(callbacks, k)
	}

	pbfFile := getFontsPbf(fontsPath, fonts, rgPBF, callbacks)
	c.Writer.Header().Set("Content-Type", "application/x-protobuf")
	c.Writer.Header().Set("Last-Modified", lastModified)

	c.Writer.Write(pbfFile)
	c.JSON(http.StatusOK, gin.H{})
}

func getFonts(c *gin.Context) {
	c.Writer.Header().Set("Content-Type", "application/json")
	c.JSON(http.StatusOK, ss.Fonts)
}
