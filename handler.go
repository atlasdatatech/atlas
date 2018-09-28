package main

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/contrib/sessions"
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
	user.Search = []string{body.UserName, body.Email}

	user.JWT, user.JWTExpires, err = authMiddleware.TokenGenerator(&user)
	if err != nil {
		FATAL(err)
	}

	// createAccount
	account := Account{}
	account.ID, _ = shortid.Generate()

	if cfgV.GetBool("account.verification") {
		account.Verification = "no"
	} else {
		account.Verification = "yes"
	}

	account.UserID = user.ID
	account.Search = []string{body.UserName}
	user.AccountID = account.ID
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
	go func() {
		c.Set("UserName", body.UserName)
		c.Set("Email", body.Email)
		c.Set("LoginURL", "http://"+c.Request.Host+"/login/")

		mailConf := MailConfig{}
		mailConf.Data = c.Keys
		mailConf.From = cfgV.GetString("smtp.from.name") + " <" + cfgV.GetString("smtp.from.address") + ">"
		mailConf.Subject = "Your AtlasData Account"
		mailConf.ReplyTo = body.Email
		mailConf.HtmlPath = "email/signup.html"

		if err := mailConf.SendMail(); err != nil {
			fmt.Println("Error Sending Welcome Email: " + err.Error())
		} else {
			fmt.Println("Successful Sent Welcome Email.")
		}
	}()

	c.HTML(http.StatusOK, "logined.html", gin.H{
		"code":   http.StatusOK,
		"id":     user.ID,
		"token":  user.JWT,
		"expire": user.JWTExpires.Format(time.RFC3339),
	})

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
		"code":   http.StatusOK,
		"id":     user.ID,
		"token":  user.JWT,
		"expire": user.JWTExpires.Format(time.RFC3339),
	})

	c.JSON(http.StatusOK, gin.H{
		"code":   http.StatusOK,
		"id":     user.ID,
		"token":  user.JWT,
		"expire": user.JWTExpires.Format(time.RFC3339),
	})

	response.Finish()
}

func logout(c *gin.Context) {
	session := sessions.Default(c)
	session.Delete("public")
	session.Save()
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

	go func() {
		resetURL := "http" + "://" + c.Request.Host + "/login/reset/" + body.Email + "/" + string(token) + "/"
		println("resetURL: " + resetURL)
		c.Set("ResetURL", resetURL)
		c.Set("UserName", user.Name)
		mailConf := MailConfig{}
		mailConf.Data = c.Keys
		mailConf.From = cfgV.GetString("smtp.from.name") + " <" + cfgV.GetString("smtp.from.address") + ">"
		mailConf.Subject = "Your AtlasMap Account"
		mailConf.ReplyTo = body.Email
		mailConf.HtmlPath = "email/reset.html"

		if err := mailConf.SendMail(); err != nil {
			println("Error Sending Rest Password Email: " + err.Error())
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

	// id := identity(c)
	user := &User{}
	if err := db.Where("id = ?", user.ID).First(&user).Error; err != nil {
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
		"accountid": account.ID,
		"verified":  account.Verification,
		"phone":     account.Phone,
	})
}

func renderVerification(c *gin.Context) {
	account := getAccount(c)
	// user := getUser(c)
	if account.Verification == "yes" {
		c.Redirect(http.StatusFound, "/account/")
		return
	}

	if len(account.VerificationToken) > 0 {
		c.HTML(http.StatusOK, "verify.html", gin.H{
			"tile":    "Atlas Map",
			"hasSend": true,
			"token":   account.VerificationToken,
		})

	}

}

func sendVerification(c *gin.Context) {
	account := getAccount(c)

	user := getUser(c)
	VerifyURL := generateToken(21)
	hash, err := bcrypt.GenerateFromPassword(VerifyURL, bcrypt.DefaultCost)
	if err != nil {
		FATAL(err)
	}
	if err := db.Model(&Account{}).Where("aid = ?", account.ID).Update("verificationToken", string(hash)).Error; err != nil {
		FATAL(err)
	}

	verifyURL := "http" + "://" + c.Request.Host + "/account/verification/" + string(VerifyURL) + "/"
	c.Set("VerifyURL", verifyURL)

	mailConf := MailConfig{}
	mailConf.Data = c.Keys
	mailConf.From = cfgV.GetString("smtp.from.name") + " <" + cfgV.GetString("smtp.from.address") + ">"
	mailConf.Subject = "Your AtlasMap Account"
	mailConf.ReplyTo = user.Email
	mailConf.HtmlPath = "email/verification.html"

	if err := mailConf.SendMail(); err != nil {
		FATAL(err)
	}
}

func verify(c *gin.Context) {
	account := getAccount(c)
	err := bcrypt.CompareHashAndPassword([]byte(account.VerificationToken), []byte(c.Param("token")))
	if err == nil {
		db.Model(&Account{}).Where("id = ?", account.ID).Updates(Account{VerificationToken: "", Verification: "yes"})
	}
	c.Redirect(http.StatusFound, "/account/")
}

func changePassword(c *gin.Context) {
	user := getUser(c)
	response := newResponse(c)
	err := user.changePassword(response)
	if err != nil {
		response.Fail()
		return
	}
	response.Finish()
}
