package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/didip/tollbooth"
	"github.com/didip/tollbooth/limiter"
	"github.com/go-spatial/tegola/provider"
	"github.com/shiena/ansicolor"
	log "github.com/sirupsen/logrus"
	"github.com/teris-io/shortid"
	"golang.org/x/crypto/bcrypt"

	nested "github.com/antonfisher/nested-logrus-formatter"
	jwt "github.com/appleboy/gin-jwt"
	"github.com/casbin/casbin"
	"github.com/casbin/gorm-adapter"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/contrib/gzip"
	"github.com/gin-gonic/contrib/static"
	"github.com/gin-gonic/gin"
	"github.com/jinzhu/gorm"
	"github.com/spf13/viper"
)

const (
	//VERSION  版本号
	VERSION = "1.0"
	//ATLAS 默认用户名
	ATLAS       = "root"
	identityKey = "id"
)

var (
	db        *gorm.DB
	provd     provider.Tiler
	casEnf    *casbin.Enforcer
	authMid   *jwt.GinJWTMiddleware
	taskQueue = make(chan *Task, 32)
	userSet   UserSet
	taskSet   sync.Map
)

//flag
var (
	hf    bool
	initf bool
	cf    string
)

func init() {

	flag.BoolVar(&hf, "h", false, "this help")
	flag.BoolVar(&initf, "init", false, "init system")
	flag.StringVar(&cf, "c", "conf.toml", "set config `file`")
	// 改变默认的 Usage，flag包中的Usage 其实是一个函数类型。这里是覆盖默认函数实现，具体见后面Usage部分的分析
	flag.Usage = usage
	//InitLog 初始化日志
	log.SetFormatter(&nested.Formatter{
		HideKeys:        true,
		ShowFullLevel:   true,
		TimestampFormat: "2006-01-02 15:04:05.000",
		// FieldsOrder: []string{"component", "category"},
	})

	// // force colors on for TextFormatter
	// log.Formatter = &logrus.TextFormatter{
	//     ForceColors: true,
	// }
	// then wrap the log output with it
	log.SetOutput(ansicolor.NewAnsiColorWriter(os.Stdout))

	log.SetLevel(log.DebugLevel)
}
func usage() {
	fmt.Fprintf(os.Stderr, `atlas version: atlas/0.9.19
Usage: atlas [-h] [-c filename] [-init]

Options:
`)
	flag.PrintDefaults()
}

//InitConf 初始化配置文件
func initConf(cfgFile string) {
	if _, err := os.Stat(cfgFile); os.IsNotExist(err) {
		log.Warnf("config file(%s) not exist", cfgFile)
	}
	viper.SetConfigType("toml")
	viper.SetConfigFile(cfgFile)
	viper.AutomaticEnv() // read in environment variables that match
	//处理配置文件
	// If a config file is found, read it in.
	err := viper.ReadInConfig()
	if err != nil {
		log.Warnf("read config file(%s) error, details: %s", viper.ConfigFileUsed(), err)
	}

	//配置默认值，如果配置内容中没有指定，就使用以下值来作为配置值，给定默认值是一个让程序更健壮的办法
	viper.SetDefault("app.port", "8080")
	viper.SetDefault("jwt.realm", "atlasmap")
	viper.SetDefault("jwt.key", "salta-atad-6221")
	viper.SetDefault("jwt.timeOut", "720h")
	viper.SetDefault("jwt.timeMax", "2160h")
	viper.SetDefault("jwt.identityKey", "name")
	viper.SetDefault("jwt.lookup", "header:Authorization, query:token, cookie:Token")
	viper.SetDefault("jwt.headName", "Bearer")
	viper.SetDefault("app.ips", 127)
	viper.SetDefault("app.ipExpiration", "-1m")
	viper.SetDefault("user.attempts", 7)
	viper.SetDefault("user.attemptsExpiration", "-5m")
	viper.SetDefault("db.host", "127.0.0.1")
	viper.SetDefault("db.port", "5432")
	viper.SetDefault("db.user", "postgres")
	viper.SetDefault("db.password", "postgres")
	viper.SetDefault("db.name", "postgres")
	viper.SetDefault("casbin.config", "./auth.conf")
	viper.SetDefault("statics", "statics/")
	viper.SetDefault("styles", "styles/")
	viper.SetDefault("fonts", "fonts/")
	viper.SetDefault("tilesets", "tilesets/")
	viper.SetDefault("datasets", "datasets/")
}

//initDb 初始化数据库
func initDb() (*gorm.DB, error) {
	pgConnInfo := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		viper.GetString("db.host"), viper.GetString("db.port"), viper.GetString("db.user"), viper.GetString("db.password"), viper.GetString("db.name"))
	pg, err := gorm.Open("postgres", pgConnInfo)
	if err != nil {
		return nil, fmt.Errorf("init gorm pg error, details: %s", err)
	}

	log.Info("init gorm pg successfully")
	//gorm自动构建用户表
	pg.AutoMigrate(&User{}, &Role{}, &Attempt{})
	//gorm自动构建管理
	pg.AutoMigrate(&Map{}, &Style{}, &Font{}, &Datafile{}, &Tileset{}, &Dataset{}, &Task{})
	return pg, nil
}

//initProvider 初始化数据库驱动
func initProvider() (provider.Tiler, error) {
	type prov struct {
		ID   string
		Name string
		Type string
	}
	p := &prov{
		ID:   "123",
		Name: "test",
		Type: "postgis",
	}

	switch p.Type {
	case "postgis":
	case "gpkg":
	}
	provd, err := provider.For(p.Type, nil)
	if err != nil {
		return nil, err
	}
	return provd, nil
}

func initJWT() (*jwt.GinJWTMiddleware, error) {
	jwtmid, err := jwt.New(JWTMidHandler())
	if err != nil {
		return nil, err
	}
	return jwtmid, nil
}

//initEnforcer 初始化资源访问控制
func initEnforcer() (*casbin.Enforcer, error) {
	pgConnInfo := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		viper.GetString("db.host"), viper.GetString("db.port"), viper.GetString("db.user"), viper.GetString("db.password"), viper.GetString("db.name"))
	casbinAdapter := gormadapter.NewAdapter("postgres", pgConnInfo, true)
	enforcer := casbin.NewEnforcer("./auth.conf", casbinAdapter)
	return enforcer, nil
}

//initSystemUser 初始化系统用户
func initSystemUser() {
	CreatePaths(ATLAS)
	name := ATLAS
	password := "1234"
	group := "admin@group"
	role := Role{ID: group, Name: "管理员"}
	user := User{}
	db.Where("name = ?", name).First(&user)
	if user.Name != "" {
		log.Warn("system super user already created")
		return
	}
	// createUser
	user.ID, _ = shortid.Generate()
	user.Name = name
	hashedPassword, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	user.Password = string(hashedPassword)
	user.Group = group
	user.Email = "cloud@atlasdata.cn"
	user.Phone = "17714211819"
	user.Department = "cloud"
	user.Company = "atlasdata"
	user.Verification = "yes"
	//No verification required
	user.JWT, user.JWTExpires, _ = authMid.TokenGenerator(&user)
	user.Activation = "yes"
	user.Role = []string{role.ID}
	// insertUser
	if err := db.Create(&user).Error; err != nil {
		log.Fatal("super user create error")
		return
	}

	if err := db.Create(&role).Error; err != nil {
		log.Fatal("admin@group role create error")
		return
	}
	if casEnf != nil {
		casEnf.AddGroupingPolicy(name, role.ID)
		//添加管理员组的用户管理权限
		casEnf.AddPolicy(role.ID, "/users/*", "(GET)|(POST)")
		casEnf.AddPolicy(role.ID, "/roles/*", "(GET)|(POST)")
	}
}

//initTaskRouter 初始化任务处理线程
func initTaskRouter() {
	iterval := viper.GetDuration("import.task.interval")
	go func() {
		ticker := time.NewTicker(iterval * time.Millisecond)
		for {
			select {
			case <-ticker.C:
				// for task := range taskQueue {
				// }
			}
		}
	}()
}

//loadPubServices 加载ATLAS公共服务
func loadPubServices() {
	pubs := &ServiceSet{Owner: ATLAS}
	err := pubs.LoadServiceSet()
	if err != nil {
		log.Errorf("loading public service set error: %s", err.Error())
	}
	userSet.Store(pubs.Owner, pubs)
}

//setupRouter 初始化GIN引擎并配置路由
func setupRouter() *gin.Engine {
	// gin.SetMode(gin.ReleaseMode)
	// r := gin.New()
	r := gin.Default()
	//gzip
	r.Use(gzip.Gzip(gzip.DefaultCompression))
	//cors
	config := cors.DefaultConfig()
	// config.AllowAllOrigins = true
	config.AllowOrigins = []string{"*"}
	config.AllowWildcard = true
	config.AllowCredentials = true
	r.Use(cors.New(config))
	//public root
	r.Use(static.Serve("/", static.LocalFile("./public", true)))
	statics := viper.GetString("statics.home")
	//static
	r.Static("/statics", statics)
	//template
	templates := viper.GetString("statics.templates") //filepath.Join(statics, "templates/*")
	r.LoadHTMLGlob(templates)

	r.GET("/", index)
	r.GET("/ping", ping)

	sign := r.Group("/sign")
	// Create a limiter, 每IP每秒3次, 每小时回收Bruck
	lter := tollbooth.NewLimiter(3, &limiter.ExpirableOptions{DefaultExpirationTTL: time.Hour})
	sign.Use(LimitMidHandler(lter))
	lter2 := tollbooth.NewLimiter(1.0/60.0, &limiter.ExpirableOptions{DefaultExpirationTTL: 300 * time.Second})
	lter2.SetBurst(10)
	sign.Use(LimitMidHandler(lter2))
	{
		//render
		sign.GET("/up/", renderSignup)
		sign.GET("/in/", renderSignin)
		sign.GET("/reset/", renderForgot)
		sign.GET("/reset/:user/:token/", renderReset)
		//api
		sign.POST("/up/", signup)
		sign.POST("/in/", signin)
		sign.POST("/reset/", sendReset)
		sign.POST("/reset/:user/:token/", resetPassword)
		sign.GET("/verify/:user/:token/", verify)
	}
	//account
	account := r.Group("/account")
	account.Use(authMid.MiddlewareFunc())
	{
		//render
		account.GET("/index/", renderAccount)
		account.GET("/update/", renderUpdateUser)
		account.GET("/password/", renderChangePassword)
		//api
		account.GET("/", getUser)
		account.GET("/logout/", signout)
		account.GET("/verify/", sendVerification)
		account.POST("/update/", updateUser)
		account.GET("/refresh/", jwtRefresh)
		account.POST("/password/", changePassword)
	}
	//users
	user := r.Group("/users")
	user.Use(authMid.MiddlewareFunc())
	user.Use(EnforceMidHandler(casEnf))
	{
		//authn > users
		user.GET("/", listUsers)
		user.POST("/", addUser)
		user.GET("/:id/", getUser)
		user.POST("/:id/", updateUser)
		user.POST("/:id/del/", deleteUser)
		user.GET("/:id/refresh/", jwtRefresh)
		user.POST("/:id/password/", changePassword)
		user.GET("/:id/roles/", getUserRoles)        //该用户拥有哪些角色
		user.POST("/:id/roles/", addUserRole)        //添加用户角色
		user.POST("/:id/roles/del/", deleteUserRole) //删除用户角色
		user.GET("/:id/maps/", getUserMaps)          //该用户拥有哪些权限（含资源与操作）
		user.POST("/:id/maps/", addUserMap)
		user.POST("/:id/maps/del/", deleteUserMap)
	}
	//roles
	role := r.Group("/roles")
	role.Use(authMid.MiddlewareFunc())
	role.Use(EnforceMidHandler(casEnf))
	{
		//authn > roles
		role.GET("/", listRoles)
		role.POST("/", createRole)
		role.POST("/:id/del/", deleteRole)
		role.GET("/:id/users/", getRoleUsers) //该角色包含哪些用户
		role.GET("/:id/maps/", getRoleMaps)   //该用户拥有哪些权限（含资源与操作）
		role.POST("/:id/maps/", addRoleMap)
		role.POST("/:id/maps/del/", deleteRoleMap)
	}

	//maproute
	maproute := r.Group("/maps")
	maproute.Use(authMid.MiddlewareFunc())
	{
		// > map op
		maproute.GET("/", listMaps)
		maproute.GET("/:id/", getMap)
		maproute.GET("/:id/perms/", getMapPerms)
		maproute.GET("/:id/export/", exportMap)
		maproute.POST("/", createMap)
		maproute.POST("/:id/", updInsertMap)
		maproute.POST("/:id/del/", deleteMap)
	}

	//studio
	studio := r.Group("/studio")
	studio.Use(authMid.MiddlewareFunc())
	{
		// > styles
		studio.GET("/", studioIndex)
		studio.GET("/editor/:id", studioEditer)
		studio.GET("/styles/upload/", renderStyleUpload)
		studio.GET("/styles/upload/:id/", renderSpriteUpload)
		studio.GET("/tilesets/upload/", renderTilesetsUpload)
		studio.GET("/datasets/upload/", renderDatasetsUpload)
		studio.GET("/maps/import/", renderMapsImport)

	}
	autoUser := func(c *gin.Context) {
		claims := jwt.ExtractClaims(c)
		user, ok := claims[identityKey]
		if !ok {
			log.Errorf("can't find %s", user)
			c.Redirect(http.StatusFound, "/sign/in/")
		} else {
			c.Request.URL.Path = c.Request.URL.Path + user.(string) + "/"
			r.HandleContext(c)
		}
	}
	styles := r.Group("/styles")
	styles.Use(authMid.MiddlewareFunc())
	{
		// > styles
		styles.GET("/", autoUser)
		styles.POST("/", autoUser)
		styles.GET("/:user/", listStyles)
		styles.POST("/:user/", uploadStyle)
		styles.GET("/:user/x/:id/", getStyle)               //style.json
		styles.GET("/:user/copy/:id/", copyStyle)           //style.json
		styles.POST("/:user/save/:id/", saveStyle)          //style.json
		styles.GET("/:user/download/:id/", downloadStyle)   //style.json
		styles.POST("/:user/replace/:id/", replaceStyle)    //style.json
		styles.GET("/:user/sprite/:id/:fmt", getSprite)     //sprite.json/png
		styles.POST("/:user/sprite/:id/", uploadSprite)     //sprite.json/png
		styles.POST("/:user/sprite/:id/:fmt", updateSprite) //sprite.json/png
		styles.GET("/:user/icon/:id/:name/", getIcon)       //sprite.json/png
		styles.POST("/:user/icon/:id/:name/", updateIcon)   //sprite.json/png
		styles.POST("/:user/icons/:id/", uploadIcons)       //sprite.json/png
		styles.POST("/:user/icons/:id/del/", deleteIcons)   //sprite.json/png

		styles.GET("/:user/view/:id/", viewStyle)      //view map style
		styles.POST("/:user/edit/:id/", updateStyle)   //updateStyle
		styles.POST("/:user/update/:id/", updateStyle) //updateStyle
		styles.POST("/:user/del/:ids/", deleteStyle)   //updateStyle
	}
	fonts := r.Group("/fonts")
	// fonts.Use(authMid.MiddlewareFunc())
	{
		// > fonts
		fonts.GET("/", autoUser)                         //get font
		fonts.POST("/", autoUser)                        //get font
		fonts.GET("/:user/", listFonts)                  //get font
		fonts.POST("/:user/", uploadFont)                //get font
		fonts.POST("/:user/:fontstack/del", deleteFonts) //get font
		fonts.GET("/:user/:fontstack/:range", getGlyphs) //get glyph pbfs
	}

	tilesets := r.Group("/tilesets")
	tilesets.Use(authMid.MiddlewareFunc())
	{
		// > tilesets
		tilesets.GET("/", autoUser)
		tilesets.POST("/", autoUser)
		tilesets.GET("/:user/", listTilesets)
		tilesets.POST("/:user/", uploadTileset)
		tilesets.POST("/:user/from/:dataset/", uploadTileset)
		tilesets.POST("/:user/replace/:id/", replaceTileset)
		tilesets.GET("/:user/x/:id/", getTilejson) //tilejson
		tilesets.GET("/:user/map/:id/:z/:x/:y", getTile)
		tilesets.GET("/:user/layer/:id/:lrs/:z/:x/:y", getTile)
		tilesets.POST("/:user/merge/:ids/", getTile)
		tilesets.POST("/:user/del/:ids/", deleteTileset)
		tilesets.GET("/:user/view/:id/", viewTile) //view
	}

	ds := r.Group("/ds")
	ds.Use(authMid.MiddlewareFunc())
	{
		// > datasets
		ds.GET("/", listDatasets)
		ds.GET("/crs/", crsList)
		ds.GET("/encoding/", encodingList)
		ds.GET("/ftype/", fieldTypeList)
		ds.GET("/info/:id/", getDatasetInfo)
		ds.POST("/upload/", fileUpload)
		ds.GET("/preview/:id/", dataPreview)
		ds.POST("/import/:id/", dataImport)
		ds.GET("/task/:id/", taskQuery)
		ds.GET("/taskstream/:id/", taskStreamQuery)

	}
	datasets := r.Group("/datasets")
	// datasets.Use(authMid.MiddlewareFunc())
	{
		// > datasets
		datasets.GET("/", listDatasets)
		datasets.GET("/:id/", getDatasetInfo)
		datasets.POST("/:id/distinct/", getDistinctValues)
		datasets.GET("/:id/geojson/", getGeojson)
		datasets.POST("/:id/import/", importFiles)
		datasets.POST("/:id/query/", queryGeojson)
		datasets.POST("/:id/cube/", cubeQuery)
		datasets.POST("/:id/common/", queryExec)
		datasets.GET("/:id/business/", queryBusiness)
		datasets.GET("/:id/buffers/", getBuffers)
		datasets.GET("/:id/models/", getModels)
		datasets.GET("/:id/geos/", searchGeos)
		datasets.POST("/:id/update/", updateInsertData)
		datasets.POST("/:id/delete/", deleteData)
	}
	//utilroute
	utilroute := r.Group("/util")
	utilroute.Use(authMid.MiddlewareFunc())
	{
		// > utils
		utilroute.GET("/export/maps/", exportMaps)
		utilroute.POST("/import/maps/", importMaps)
	}
	return r
}

// force redirect to https from http// necessary only if you use https directly// put your domain name instead of CONF.ORIGIN
func redirectToHTTPS(w http.ResponseWriter, req *http.Request) {
	//http.Redirect(w, req, "https://" + CONF.ORIGIN + req.RequestURI, http.StatusMovedPermanently)
}

func main() {
	flag.Parse()
	if hf {
		flag.Usage()
		return
	}
	if cf == "" {
		cf = "conf.toml"
	}
	initConf(cf)
	var err error
	db, err = initDb()
	if err != nil {
		log.Fatalf("init db error, details: %s", err)
	}
	defer db.Close()

	provd, err = initProvider()
	if err != nil {
		log.Fatalf("init provider error: %s", err)
	}

	authMid, err = initJWT()
	if err != nil {
		log.Fatalf("init jwt error: %s", err)
	}

	casEnf, err = initEnforcer()
	if err != nil {
		log.Fatalf("init enforcer error: %s", err)
	}
	initSystemUser()
	if initf {
		return
	}
	initTaskRouter()
	loadPubServices()

	r := setupRouter()

	// log.Infof("Listening and serving HTTP on %s\n", viper.GetString("app.port"))
	r.Run(":" + viper.GetString("app.port"))
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
