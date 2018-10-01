package main

import (
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
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
		mailConf.HtmlPath = "email/signup.html"

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
	c.HTML(http.StatusOK, "logined.html", gin.H{
		"code": http.StatusOK,
		"name": user.Name,
	})
	//response
	cookie, err := c.Cookie("JWTToken")
	if err != nil {
		log.Println(err)
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    http.StatusOK,
		"id":      user.ID,
		"name":    user.Name,
		"token":   user.JWT,
		"expire":  user.JWTExpires.Format(time.RFC3339),
		"message": "login successfully",
		"cookie":  cookie,
	})

	response.Finish()
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
		mailConf.HtmlPath = "email/reset.html"

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
	uid := c.GetString(identityKey)
	user := &User{}
	if err := db.Where("id = ?", uid).First(&user).Error; err != nil {
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
	uid := c.GetString(identityKey)
	user := &User{}
	if err := db.Where("id = ?", uid).First(&user).Error; err != nil {
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

	uid := c.GetString(identityKey)
	token := generateToken(21)
	hash, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
	if err != nil {
		response.ErrFor["VerificationToken"] = err.Error()
		response.Fail()
		return
	}
	user := &User{}
	if err := db.Where("id = ?", uid).First(&user).Error; err != nil {
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
		mailConf.HtmlPath = "email/verification.html"

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
	log.Println("user:" + c.Param("user"))
	log.Println("token:" + c.Param("token"))
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
	uid := c.GetString(identityKey)
	user := &User{}
	if err := db.Where("id = ?", uid).First(&user).Error; err != nil {
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
