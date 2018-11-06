package main

import (
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/gin-contrib/cors"
	log "github.com/sirupsen/logrus"
	"github.com/teris-io/shortid"
	"golang.org/x/crypto/bcrypt"

	jwt "github.com/appleboy/gin-jwt"
	"github.com/casbin/gorm-adapter"

	"github.com/casbin/casbin"
	"github.com/gin-gonic/gin"
	"github.com/jinzhu/gorm"
	"github.com/spf13/viper"
)

//VERSION server version
const VERSION = "1.0"

var identityKey = "id"

//定义一个内部全局的 db 指针用来进行认证，数据校验
var db *gorm.DB

//定义一个内部全局的 viper 指针用来进行配置读取
var cfgV *viper.Viper

//定义一个内部全局的 casbin.Enforcer 指针用来进行权限校验
var casEnf *casbin.Enforcer

var authMid *jwt.GinJWTMiddleware

var pubSet *ServiceSet

func main() {
	log.SetLevel(log.DebugLevel)
	cfgV = viper.New()
	InitConf(cfgV)
	identityKey = cfgV.GetString("jwt.identityKey")
	if ss, err := LoadServiceSet(); err != nil {
		log.Errorf("loading public service set error: %s", err.Error())
	} else {
		pubSet = ss
		log.Infof("load public service set successed!")
	}
	pgConnInfo := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable", cfgV.GetString("db.host"), cfgV.GetString("db.port"), cfgV.GetString("db.user"), cfgV.GetString("db.password"), cfgV.GetString("db.name"))
	log.Info(pgConnInfo)
	pg, err := gorm.Open("postgres", pgConnInfo)
	if err != nil {
		log.Fatal("gorm pg Error:" + err.Error())
	} else {
		log.Info("Successfully connected!")
		pg.AutoMigrate(&User{}, &Attempt{})
		db = pg
	}
	defer pg.Close()
	//Init casbin
	casbinAdapter := gormadapter.NewAdapter("postgres", pgConnInfo, true)
	casEnf = casbin.NewEnforcer(cfgV.GetString("casbin.config"), casbinAdapter)
	//for test
	// casEnf.LoadPolicy()
	//初始化添加默认规则,三条策略可修改
	casEnf.AddPolicy("admin_group", "/account/*", "(GET)|(POST)|(PUT)")
	casEnf.AddPolicy("user_group", "/account/*", "GET")
	// casEnf.SavePolicy()

	authMid, err = jwt.New(JWTMiddleware())
	if err != nil {
		log.Fatal("JWT Error:" + err.Error())
	}

	initSuperUser()

	r := gin.Default()
	r.Use(cors.Default())
	staticsHome := cfgV.GetString("assets.statics")
	r.Static("/statics", staticsHome)
	templatesPath := filepath.Join(staticsHome, "/templates/*")
	r.LoadHTMLGlob(templatesPath)

	bindRoutes(r) // --> cmd/go-getting-started/routers.go

	r.Run(":" + cfgV.GetString("port"))

}

func bindRoutes(r *gin.Engine) {

	//tmp
	r.GET("/", index)
	r.GET("/login/", renderLogin)
	r.POST("/login/", login)

	//admin
	admin := r.Group("/users")
	admin.Use(authMid.MiddlewareFunc())
	{
		//admin > users
		admin.GET("/", listUsers)
		admin.POST("/", createUser)
		admin.GET("/:id/", readUser)
		admin.PUT("/:id/", updateUser)
		admin.GET("/:id/refresh/", jwtRefresh)
		admin.PUT("/:id/password/", changePassword)
		admin.PUT("/:id/role/", changeRole)
		admin.DELETE("/:id/", deleteUser)
	}
	//account
	account := r.Group("/account")
	account.Use(authMid.MiddlewareFunc())
	{

		account.GET("/index/", renderAccount)
		account.GET("/", readUser)
		account.GET("/logout/", logout)
		account.GET("/update/", renderUpdateUser)
		account.PUT("/update/", updateUser)
		account.GET("/refresh/", jwtRefresh)
		account.GET("/password/", renderChangePassword)
		account.POST("/password/", changePassword)
	}

	autoUser := func(c *gin.Context) {
		claims := jwt.ExtractClaims(c)
		user, ok := claims[identityKey]
		if !ok {
			log.Errorf("can't find %s", user)
			c.Redirect(http.StatusFound, "/login/")
		} else {
			c.Request.URL.Path = c.Request.URL.Path + user.(string) + "/"
			r.HandleContext(c)
		}
	}

	//studio
	studio := r.Group("/studio")
	// studio.Use(authMid.MiddlewareFunc())
	{
		// > styles
		studio.GET("/", studioIndex)
		studio.GET("/editor/:sid", studioEditer)
		studio.GET("/styles/upload/", renderStyleUpload)
		studio.GET("/styles/upload/:sid/", renderSpriteUpload)
		studio.GET("/tilesets/upload/", renderTilesetsUpload)
	}

	styles := r.Group("/styles")
	// styles.Use(authMid.MiddlewareFunc())
	{
		// > styles
		styles.GET("/", listStyles)
		styles.POST("/", uploadStyle)
		styles.GET("/:sid", getStyle)              //style.json
		styles.GET("/:sid/", viewStyle)            //view map style
		styles.PUT("/:sid/", updateStyle)          //updateStyle
		styles.POST("/:sid/", saveStyle)           //updateStyle
		styles.GET("/:sid/sprite:fmt", getSprite)  //style.json
		styles.POST("/:sid/sprite/", uploadSprite) //style.json
	}
	fonts := r.Group("/fonts")
	// fonts.Use(authMid.MiddlewareFunc())
	{
		// > fonts
		fonts.GET("/", listFonts)                     //get font
		fonts.GET("/:fontstack/:rangepbf", getGlyphs) //get glyph pbfs
	}

	tilesets := r.Group("/tilesets")
	// tilesets.Use(authMid.MiddlewareFunc())
	{
		// > tilesets
		tilesets.GET("/", listTilesets)
		tilesets.POST("/", uploadTileset)
		tilesets.GET("/:tid", getTilejson) //tilejson
		tilesets.GET("/:tid/", viewTile)   //view
		tilesets.GET("/:tid/:z/:x/:y", getTile)
	}
	datasets := r.Group("/datasets")
	// datasets.Use(authMid.MiddlewareFunc())
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

func initSuperUser() {
	name := "root"
	password := "1234"
	role := "super"
	phone := "13579246810"
	department := "system"
	user := User{}
	db.Where("name = ?", name).First(&user)
	if user.Name != "" {
		log.Warn("super user already created")
		return
	}
	// createUser
	user.ID, _ = shortid.Generate()
	user.Name = name
	hashedPassword, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	user.Password = string(hashedPassword)
	user.Role = role
	user.Phone = phone
	user.Department = department
	//No verification required
	user.JWT, user.Expires, _ = authMid.TokenGenerator(&user)
	user.Activation = "yes"
	user.Search = []string{name, phone, department}
	// insertUser
	if err := db.Create(&user).Error; err != nil {
		log.Errorf("super user create error")
		return
	}
}