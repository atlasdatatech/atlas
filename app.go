package main

import (
	"fmt"
	"log"
	"net/http"

	jwt "github.com/appleboy/gin-jwt"
	"github.com/casbin/gorm-adapter"

	"github.com/casbin/casbin"
	"github.com/gin-gonic/gin"
	"github.com/jinzhu/gorm"
	"github.com/spf13/viper"
)

const VERSION = "1.0"

//定义一个内部全局的 db 指针用来进行认证，数据校验
var db *gorm.DB

//定义一个内部全局的 viper 指针用来进行配置读取
var cfgV *viper.Viper

//定义一个内部全局的 casbin.Enforcer 指针用来进行权限校验
var casbinEnforcer *casbin.Enforcer

var authMiddleware *jwt.GinJWTMiddleware

func main() {
	cfgV = viper.New()
	InitConf(cfgV)

	pgConnInfo := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable", cfgV.GetString("db.host"), cfgV.GetString("db.port"), cfgV.GetString("db.user"), cfgV.GetString("db.password"), cfgV.GetString("db.name"))
	pg, err := gorm.Open("postgres", pgConnInfo)
	if err != nil {
		log.Fatal("gorm pg Error:" + err.Error())
	} else {
		fmt.Println("Successfully connected!")
		pg.AutoMigrate(&User{}, &Account{}, &Attempt{})
		db = pg
	}
	defer pg.Close()

	casbinAdapter := gormadapter.NewAdapter("postgres", pgConnInfo, true)
	casbinEnforcer = casbin.NewEnforcer(cfgV.GetString("casbin.config"), casbinAdapter)

	authMiddleware, err = jwt.New(JWTMiddleware())
	if err != nil {
		log.Fatal("JWT Error:" + err.Error())
	}

	r := gin.Default()

	r.LoadHTMLGlob("templates/*")

	bindRoutes(r) // --> cmd/go-getting-started/routers.go

	r.Run(":" + cfgV.GetString("port"))

	// https
	// put path to cert instead of CONF.TLS.CERT
	// put path to key instead of CONF.TLS.KEY
	/*
		go func() {
				http.ListenAndServe(":80", http.HandlerFunc(redirectToHTTPS))
			}()
			errorHTTPS := router.RunTLS(":443", CONF.TLS.CERT, CONF.TLS.KEY)
			if errorHTTPS != nil {
				log.Fatal("HTTPS doesn't work:", errorHTTPS.Error())
			}
	*/
}

// force redirect to https from http
// necessary only if you use https directly
// put your domain name instead of CONF.ORIGIN
func redirectToHTTPS(w http.ResponseWriter, req *http.Request) {
	//http.Redirect(w, req, "https://" + CONF.ORIGIN + req.RequestURI, http.StatusMovedPermanently)
}

func bindRoutes(r *gin.Engine) {

	//front end
	r.GET("/", index)

	r.GET("/signup/", renderSignup)
	r.POST("/signup/", signup)
	r.GET("/login/", renderLogin)
	r.POST("/login/", login)
	r.GET("/login/forgot/", renderForgot)
	r.POST("/login/forgot/", sendReset)
	r.GET("/login/reset/:email/:token/", renderReset)
	r.POST("/login/reset/:email/:token/", resetPassword)
	r.GET("/logout/", logout)

	//account
	ac := r.Group("/account")
	ac.Use(authMiddleware.MiddlewareFunc())
	{
		ac.GET("/", renderAccount)

		ac.POST("/refresh/", authMiddleware.RefreshHandler)
		//account > verification
		ac.GET("/verification/", renderVerification)
		ac.POST("/verification/", sendVerification)
		ac.GET("/verification/:token/", verify)

		//account > settings
		// ac.GET("/settings/", renderAccountSettings)
		// ac.PUT("/settings/", setSettings)
		ac.PUT("/settings/password/", changePassword)

	}
	//route not found
	// router.NoRoute(renderStatus404)
}
