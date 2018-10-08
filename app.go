package main

import (
	"fmt"
	"net/http"

	log "github.com/sirupsen/logrus"

	jwt "github.com/appleboy/gin-jwt"
	"github.com/casbin/gorm-adapter"

	"github.com/casbin/casbin"
	"github.com/gin-gonic/gin"
	"github.com/jinzhu/gorm"
	"github.com/spf13/viper"
)

const VERSION = "1.0"

var identityKey = "id"

//定义一个内部全局的 db 指针用来进行认证，数据校验
var db *gorm.DB

//定义一个内部全局的 viper 指针用来进行配置读取
var cfgV *viper.Viper

//定义一个内部全局的 casbin.Enforcer 指针用来进行权限校验
var casbinEnforcer *casbin.Enforcer

var authMiddleware *jwt.GinJWTMiddleware

var tss *ServiceSet

func main() {

	log.SetLevel(log.DebugLevel)

	cfgV = viper.New()
	InitConf(cfgV)

	identityKey = cfgV.GetString("jwt.identityKey")

	pubdir := cfgV.GetString("tilesets.path")
	if ts, err := NewFromBaseDir(pubdir); err != nil {
		log.Error("public tilesets NewFromBaseDir Error:" + err.Error())
	} else {
		tss = ts
		log.Info("public tilesets NewFromBaseDir successed!")
	}

	pgConnInfo := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable", cfgV.GetString("db.host"), cfgV.GetString("db.port"), cfgV.GetString("db.user"), cfgV.GetString("db.password"), cfgV.GetString("db.name"))
	log.Info(pgConnInfo)
	pg, err := gorm.Open("postgres", pgConnInfo)
	if err != nil {
		log.Fatal("gorm pg Error:" + err.Error())
	} else {
		log.Info("Successfully connected!")
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

	staticsHome := cfgV.GetString("statics.home")
	log.Debug(staticsHome)
	r.Static("/statics", staticsHome)
	templatesPath := cfgV.GetString("statics.templates")
	log.Debug(templatesPath)
	r.LoadHTMLGlob(templatesPath)

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
	account := r.Group("/account")
	account.Use(authMiddleware.MiddlewareFunc())
	{
		account.GET("/", renderAccount)

		//account > verification
		account.GET("/verification/", renderVerification)
		account.POST("/verification/", sendVerification)
		account.GET("/verification/:user/:token/", verify)
		//account jwt
		account.GET("/jwt/refresh/", jwtRefresh)
		//account > settings
		account.GET("/settings/password/", renderChangePassword)
		account.POST("/settings/password/", changePassword)

	}
	//studio
	studio := r.Group("/studio")
	studio.Use(authMiddleware.MiddlewareFunc())
	{
		// > styles
		studio.GET("/", studioIndex)
		// studio.GET("/styles/", listStyles)
		// studio.GET("/tilesets/", listTilesets)
		// studio.GET("/datasets/", listDatasets)
	}

	autoUser := func(c *gin.Context) {
		claims := jwt.ExtractClaims(c)
		user, ok := claims[identityKey]
		log.Debug(c.Request.URL.Path)
		if !ok {
			log.Errorf("can't find %s", user)
			c.Redirect(http.StatusFound, "/login/")
			log.Debug(c.Request.URL.Path)
		} else {
			c.Request.URL.Path = c.Request.URL.Path + user.(string) + "/"
			log.Debug(c.Request.URL.Path)
			r.HandleContext(c)
		}
	}

	styles := r.Group("/styles")
	styles.Use(authMiddleware.MiddlewareFunc())
	{
		// > styles
		styles.GET("/", autoUser)
		styles.GET("/:user/", listStyles)
		// styles.GET("/:user/:sid/", defaultMap) //view map style
		// styles.GET("/:user/:sid/style.json", getStyle)        //style.json
	}
	tilesets := r.Group("/tilesets")
	tilesets.Use(authMiddleware.MiddlewareFunc())
	{
		// > tilesets
		tilesets.GET("/", autoUser)
		tilesets.GET("/:user/", listTilesets)
		tilesets.GET("/:user/:tid", getTilejson) //tilejson
		tilesets.GET("/:user/:tid/", viewTile)   //view
		tilesets.GET("/:user/:tid/:z/:x/:y", getTile)
	}
	datasets := r.Group("/datasets")
	datasets.Use(authMiddleware.MiddlewareFunc())
	{
		// > datasets
		datasets.GET("/", autoUser)
		// datasets.GET("/:user/", listDatasets)
		// datasets.GET("/:user/:did/", getDataset)
		// datasets.GET("/:user/:did/view/", defaultDraw)
		// datasets.GET("/:user/:did/edit/", defaultDraw)

	}

	//route not found
	// router.NoRoute(renderStatus404)
}
