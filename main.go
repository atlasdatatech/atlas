package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/go-spatial/tegola/provider"
	log "github.com/sirupsen/logrus"
	"github.com/teris-io/shortid"
	"golang.org/x/crypto/bcrypt"

	nested "github.com/antonfisher/nested-logrus-formatter"
	jwt "github.com/appleboy/gin-jwt"
	"github.com/casbin/casbin"
	"github.com/casbin/gorm-adapter"
	"github.com/didip/tollbooth"
	"github.com/didip/tollbooth/limiter"
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
	pubSet    sync.Map
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
		HideKeys:    true,
		FieldsOrder: []string{"component", "category"},
	})
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
	pg.AutoMigrate(&User{}, &Attempt{}, &Role{})
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
	user.Department = "system"
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
		log.Fatal("admin group create error")
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
	pubs := &ServiceSet{User: ATLAS}
	err := pubs.LoadServiceSet()
	if err != nil {
		log.Fatalf("loading public service set error: %s", err.Error())
	}
	pubSet.Store(pubs.User, pubs)
}

//setupRouter 初始化GIN引擎并配置路由
func setupRouter() *gin.Engine {
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
	jwtmid, err := jwt.New(JWTMidHandler())
	if err != nil {
		log.Fatalf("JWT Error:" + err.Error())
	}
	authMid = jwtmid

	r.GET("/", index)
	r.GET("/ping", ping)
	sign := r.Group("/sign")

	// Create a limiter, 每IP每秒3次, 触发等待5分钟
	limiter := tollbooth.NewLimiter(3, &limiter.ExpirableOptions{DefaultExpirationTTL: 300 * time.Second})
	sign.Use(LimitMidHandler(limiter))
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
	//account
	account := r.Group("/account")
	account.Use(authMid.MiddlewareFunc())
	{
		account.GET("/index/", renderAccount)
		account.GET("/", getUser)
		account.GET("/signout/", signout)
		account.GET("/verify/", sendVerification)
		account.GET("/update/", renderUpdateUser)
		account.POST("/update/", updateUser)
		account.GET("/refresh/", jwtRefresh)
		account.GET("/password/", renderChangePassword)
		account.POST("/password/", changePassword)
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
		studio.GET("/editor/:sid", studioEditer)
		studio.GET("/styles/upload/", renderStyleUpload)
		studio.GET("/styles/upload/:sid/", renderSpriteUpload)
		studio.GET("/tilesets/upload/", renderTilesetsUpload)
		studio.GET("/datasets/upload/", renderDatasetsUpload)
		studio.GET("/maps/import/", renderMapsImport)

	}

	styles := r.Group("/styles")
	// styles.Use(authMid.MiddlewareFunc())
	{
		// > styles
		styles.GET("/", listStyles)
		styles.POST("/", uploadStyle)
		styles.GET("/:sid", getStyle)             //style.json
		styles.GET("/:sid/", viewStyle)           //view map style
		styles.POST("/:sid/", updateStyle)        //updateStyle
		styles.GET("/:sid/sprite:fmt", getSprite) //sprite.json/png
	}
	fonts := r.Group("/fonts")
	// fonts.Use(authMid.MiddlewareFunc())
	{
		// > fonts
		fonts.GET("/", listFonts)                  //get font
		fonts.GET("/:fontstack/:range", getGlyphs) //get glyph pbfs
	}

	ts := r.Group("/ts")
	// tilesets.Use(authMid.MiddlewareFunc())
	{
		// > tilesets
		ts.GET("/", listTilesets)
		// ts.GET("/", listLayers)
		ts.POST("/", uploadTileset)
		ts.GET("/json/:mid", getTilejson) //tilejson
		// ts.GET("/map/:mid/:lid/", getTile)
		// ts.POST("/map/:mid/:lid/", getTile)
		ts.GET("/map/:mid/:z/:x/:y", getTile)
		// ts.POST("/merge/:mid1/:mid2/", viewTile) //view
		ts.GET("/view/:mid/", viewTile) //view
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

	ds := r.Group("/ds")
	ds.Use(authMid.MiddlewareFunc())
	{
		// > datasets
		ds.GET("/", listDatasets)
		ds.GET("/crs/", crsList)
		ds.GET("/encoding/", encodingList)
		ds.GET("/ftype/", fieldTypeList)
		ds.GET("/info/:name/", getDatasetInfo)
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
		datasets.GET("/:name/", getDatasetInfo)
		datasets.POST("/:name/distinct/", getDistinctValues)
		datasets.GET("/:name/geojson/", getGeojson)
		datasets.POST("/:name/import/", importFiles)
		datasets.POST("/:name/query/", queryGeojson)
		datasets.POST("/:name/cube/", cubeQuery)
		datasets.POST("/:name/common/", queryExec)
		datasets.GET("/:name/business/", queryBusiness)
		datasets.GET("/:name/buffers/", getBuffers)
		datasets.GET("/:name/models/", getModels)
		datasets.GET("/:name/geos/", searchGeos)
		datasets.POST("/:name/update/", updateInsertData)
		datasets.POST("/:name/delete/", deleteData)
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

// force redirect to https from http
// necessary only if you use https directly
// put your domain name instead of CONF.ORIGIN
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

	casEnf, err = initEnforcer()
	if err != nil {
		log.Fatalf("init enforcer error: %s", err)
	}
	if initf {
		initSystemUser()
		return
	}
	initTaskRouter()
	loadPubServices()

	r := setupRouter()

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
