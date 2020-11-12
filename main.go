package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	geopkg "github.com/atlasdatatech/go-gpkg/gpkg"
	"github.com/didip/tollbooth"
	"github.com/didip/tollbooth/limiter"
	"github.com/go-spatial/tegola/atlas"
	"github.com/go-spatial/tegola/cache"
	"github.com/go-spatial/tegola/config"
	"github.com/go-spatial/tegola/dict"
	"github.com/go-spatial/tegola/provider"
	_ "github.com/go-spatial/tegola/provider/debug"
	_ "github.com/go-spatial/tegola/provider/gpkg"
	_ "github.com/go-spatial/tegola/provider/postgis"

	"github.com/go-spatial/tegola/server"

	"github.com/shiena/ansicolor"
	log "github.com/sirupsen/logrus"
	"github.com/teris-io/shortid"
	"golang.org/x/crypto/bcrypt"

	nested "github.com/antonfisher/nested-logrus-formatter"

	"github.com/casbin/casbin"
	gormadapter "github.com/casbin/gorm-adapter"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/contrib/static"
	"github.com/gin-gonic/gin"
	"github.com/jinzhu/gorm"
	"github.com/spf13/viper"
)

const (
	//VERSION  版本号
	VERSION = "1.0"
	//ATLAS 默认管理员用户名
	ATLAS = "atlas"
	//ADMIN 默认管理员组
	ADMIN = "admin@group"
	//USER 默认用户组
	USER        = "user@group"
	identityKey = "uid"
	userKey     = "id"
	//DISABLEACCESSTOKEN 不使用accesstoken
	DISABLEACCESSTOKEN = true
)

var (
	conf      config.Config
	db        *gorm.DB
	dbType    = Sqlite3
	dataDB    *gorm.DB
	providers = make(map[string]provider.TilerUnion)
	casEnf    *casbin.Enforcer
	authMid   *JWTMiddleware
	taskQueue = make(chan *Task, 16)
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
	//设置打印文件名和行号
	log.SetReportCaller(true)
	log.SetFormatter(&nested.Formatter{
		HideKeys:        true,
		ShowFullLevel:   true,
		TimestampFormat: "2006-01-02 15:04:05.000",
		FieldsOrder:     []string{"component", "category"},
		CallerFirst:     true,
		CustomCallerFormatter: func(f *runtime.Frame) string {
			return fmt.Sprintf(" %s:%d ", path.Base(f.File), f.Line)
			// return fmt.Sprintf(" %s:%d %s ", path.Base(f.File), f.Line, f.Function)
		},
	})
	// force colors on for TextFormatter
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

	log.Infof("Loading config file: %v", cfgFile)
	if conf, err = config.Load(cfgFile); err != nil {
		log.Fatal(err)
	}
	if err = conf.Validate(); err != nil {
		log.Fatal(err)
	}

	//配置默认值，如果配置内容中没有指定，就使用以下值来作为配置值，给定默认值是一个让程序更健壮的办法
	viper.SetDefault("app.port", "8080")
	viper.SetDefault("jwt.auth.realm", "atlasmap")
	viper.SetDefault("jwt.auth.key", "salta-atad-6221")
	viper.SetDefault("jwt.auth.timeOut", "720h")
	viper.SetDefault("jwt.auth.timeMax", "2160h")
	viper.SetDefault("jwt.auth.identityKey", "name")
	viper.SetDefault("jwt.auth.lookup", "header:Authorization, query:token, cookie:token")
	viper.SetDefault("jwt.auth.headName", "Bearer")
	viper.SetDefault("app.ips", 127)
	viper.SetDefault("app.ipExpiration", "-1m")
	viper.SetDefault("user.attempts", 7)
	viper.SetDefault("user.attemptsExpiration", "-5m")
	viper.SetDefault("db.host", "127.0.0.1")
	viper.SetDefault("db.port", "5432")
	viper.SetDefault("db.user", "postgres")
	viper.SetDefault("db.password", "postgres")
	viper.SetDefault("db.sysdb", "atlas")
	viper.SetDefault("db.datadb", "atlasdata")
	viper.SetDefault("casbin.config", "./auth.conf")
	viper.SetDefault("statics", "statics/")

	viper.SetDefault("paths.styles", "styles")
	viper.SetDefault("paths.fonts", "fonts")
	viper.SetDefault("paths.tilesets", "tilesets")
	viper.SetDefault("paths.datasets", "datasets")
	viper.SetDefault("paths.uploads", "tmp")
}

//initSysDb 初始化数据库
func initSysDb() (*gorm.DB, error) {
	var conn string
	dbType = DBType(viper.GetString("db.type"))
	switch dbType {
	case Sqlite3:
		conn = viper.GetString("db.sysdb")
	case Postgres:
		conn = fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
			viper.GetString("db.host"), viper.GetString("db.port"), viper.GetString("db.user"), viper.GetString("db.password"), viper.GetString("db.sysdb"))
	default:
		return nil, fmt.Errorf("unkown database driver")
	}

	//initEnforcer 初始化资源访问控制
	casEnf = casbin.NewEnforcer("./auth.conf", gormadapter.NewAdapter(string(dbType), conn, true))
	if casEnf == nil {
		return nil, fmt.Errorf("init casbin enforcer error")
	}

	db, err := gorm.Open(string(dbType), conn)
	if err != nil {
		return nil, fmt.Errorf("init gorm db error, details: %s", err)
	}
	// db1, err := sql.Open("spatialite", "file:dummy.db?mode=memory&cache=shared")
	// if err != nil {
	// 	log.Println(err)
	// }
	// _, err = db1.Exec("SELECT InitSpatialMetadata()")
	// if err != nil {
	// 	log.Println(err)
	// }
	log.Info("init gorm db successfully")
	//gorm自动构建用户表
	db.AutoMigrate(&User{}, &Role{}, &Attempt{})
	//gorm自动构建管理
	db.AutoMigrate(&Map{}, &Style{}, &Font{}, &Tileset{}, &Dataset{}, &DataSource{}, &Task{})
	db.AutoMigrate(&Scene{}, &OnlineImage{}, &OnlineTileset{}, &OnlineTerrain{}, &OnlineSymbol{})
	return db, nil
}

//initDataDb 初始化数据库
func initDataDb() (*gorm.DB, error) {
	var conn string
	dbType = DBType(viper.GetString("db.type"))
	switch dbType {
	case Sqlite3:
		conn = viper.GetString("db.datadb")
		dataDB, err := gorm.Open(string(dbType), conn)
		if err != nil {
			return nil, fmt.Errorf("init datadb error, details: %s", err)
		}
		err = dataDB.Exec("PRAGMA synchronous=0").Error
		if err != nil {
			return nil, fmt.Errorf("exec PRAGMA synchronous=0 error, details: %s", err)
		}
		//gorm自动构建gpkg表
		err = dataDB.AutoMigrate(&geopkg.Content{}, &geopkg.GeometryColumn{}, &geopkg.SpatialReferenceSystem{}, &geopkg.Extension{}, &geopkg.TileMatrix{}, &geopkg.TileMatrixSet{}).Error
		if err != nil {
			return nil, err
		}
		{ //init spatial refs
			err = dataDB.Exec("INSERT OR REPLACE INTO gpkg_spatial_ref_sys (srs_name, srs_id, organization, organization_coordsys_id, definition) VALUES ('Undefined Cartesian', -1, 'NONE', -1, 'Undefined')").Error
			if err != nil {
				return nil, err
			}
			err = dataDB.Exec("INSERT OR REPLACE INTO gpkg_spatial_ref_sys (srs_name, srs_id, organization, organization_coordsys_id, definition) VALUES ('Undefined Geographic', 0, 'NONE', 0, 'Undefined')").Error
			if err != nil {
				return nil, err
			}
			err = dataDB.Exec("INSERT OR REPLACE INTO gpkg_spatial_ref_sys (srs_name, srs_id, organization, organization_coordsys_id, definition) VALUES ('WGS84', 4326, 'epsg', 4326, 'GEOGCS[\"WGS 84\",DATUM[\"WGS_1984\",SPHEROID[\"WGS 84\",6378137,298.257223563,AUTHORITY[\"EPSG\",\"7030\"]],AUTHORITY[\"EPSG\",\"6326\"]],PRIMEM[\"Greenwich\",0,AUTHORITY[\"EPSG\",\"8901\"]],UNIT[\"degree\",0.0174532925199433,AUTHORITY[\"EPSG\",\"9122\"]],AUTHORITY[\"EPSG\",\"4326\"]]')").Error
			if err != nil {
				return nil, err
			}
		}

		return dataDB, nil
	case Postgres:
		conn = fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
			viper.GetString("db.host"), viper.GetString("db.port"), viper.GetString("db.user"), viper.GetString("db.password"), viper.GetString("db.datadb"))
		dataDB, err := gorm.Open(string(dbType), conn)
		if err != nil {
			return nil, fmt.Errorf("init datadb error, details: %s", err)
		}
		return dataDB, nil
	default:
		return nil, fmt.Errorf("unkown database driver")
	}

}

//initProvider 初始化数据库驱动
func initProviders(provArr []dict.Dicter) (map[string]provider.TilerUnion, error) {
	providers := map[string]provider.TilerUnion{}
	// init our providers
	// but first convert []env.Map -> []dict.Dicter
	for _, p := range provArr {
		// lookup our proivder name
		pname, err := p.String("name", nil)
		if err != nil {
			log.Error(err)
			return providers, err
		}

		// check if a proivder with this name is alrady registered
		_, ok := providers[pname]
		if ok {
			return providers, err
		}

		// lookup our provider type
		ptype, err := p.String("type", nil)
		if err != nil {
			log.Error(err)
			return providers, err
		}

		// register the provider
		prov, err := provider.For(ptype, p)
		if err != nil {
			return providers, err
		}

		// add the provider to our map of registered providers
		providers[pname] = prov
	}

	return providers, nil
}

// Cache registers cache backends
func initCache(config dict.Dicter) (cache.Interface, error) {
	cType, err := config.String("type", nil)
	if err != nil {
		switch err.(type) {
		case dict.ErrKeyRequired:
			return nil, fmt.Errorf("register: cache 'type' parameter missing")
		case dict.ErrKeyType:
			return nil, fmt.Errorf("register: cache 'type' value must be a string")
		default:
			return nil, err
		}
	}

	// register the provider
	return cache.For(cType, config)
}

func initTegolaServer() {
	// if you set the port via the comand line it will override the port setting in the config
	serverPort := string(conf.Webserver.Port)
	// set our server version
	server.Version = VERSION
	server.HostName = string(conf.Webserver.HostName)
	// set the http reply headers
	// server.Headers = conf.Webserver.Headers
	// set tile buffer
	if conf.TileBuffer != nil {
		// server.TileBuffer = float64(*conf.TileBuffer)
	}
	// start our webserver
	server.Start(nil, serverPort)
}

func initAuthJWT() (*JWTMiddleware, error) {
	jwtmid := &JWTMiddleware{
		//Realm name to display to the user. Required.
		//必要项，显示给用户看的域
		Realm: viper.GetString("jwt.auth.realm"),
		//Secret key used for signing. Required.
		//用来进行签名的密钥，就是加盐用的
		Key: []byte(viper.GetString("jwt.auth.key")),
		//Duration that a jwt token is valid. Optional, defaults to one hour
		//JWT 的有效时间，默认为30天
		Timeout: viper.GetDuration("jwt.auth.timeOut"),
		// This field allows clients to refresh their token until MaxRefresh has passed.
		// Note that clients can refresh their token in the last moment of MaxRefresh.
		// This means that the maximum validity timespan for a token is MaxRefresh + Timeout.
		// Optional, defaults to 0 meaning not refreshable.
		//最长的刷新时间，用来给客户端自己刷新 token 用的，设置为3个月
		MaxRefresh: viper.GetDuration("jwt.auth.timeMax"),

		PayloadFunc: func(data interface{}) MapClaims {
			if user, ok := data.(User); ok {
				return MapClaims{
					identityKey: user.Name,
				}
			}
			return MapClaims{}
		},
		// TokenLookup is a string in the form of "<source>:<name>" that is used
		// to extract token from the request.
		// Optional. Default value "header:Authorization".
		// Possible values:
		// - "header:<name>"
		// - "query:<name>"
		// - "cookie:<name>"
		//这个变量定义了从请求中解析 token 的位置和格式
		TokenLookup: viper.GetString("jwt.auth.lookup"),
		// TokenLookup: "query:token",
		// TokenLookup: "cookie:token",
		// TokenHeadName is a string in the header. Default value is "Bearer"
		//TokenHeadName 是一个头部信息中的字符串
		TokenHeadName: viper.GetString("jwt.auth.headName"),
		// TimeFunc provides the current time. You can override it to use another time value. This is useful for testing or if your server uses a different time zone than your tokens.
		//这个指定了提供当前时间的函数，也可以自定义
		TimeFunc: time.Now,
		//设置Cookie
		SendCookie:        true,
		SendAuthorization: true,
		//禁止abort
		// DisabledAbort: true,
	}
	err := jwtmid.MiddlewareInit()
	if err != nil {
		return nil, err
	}
	return jwtmid, nil
}

//initSystemUser 初始化系统用户
func initSystemUser() {
	CreatePaths(ATLAS)
	os.MkdirAll(filepath.Join(viper.GetString("paths.fonts"), ATLAS), os.ModePerm)

	name := ATLAS
	password := "1234"
	role := Role{ID: ADMIN, Name: "管理员"}
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
	user.Group = ADMIN
	user.Email = "cloud@atlasdata.cn"
	user.Phone = "17714211819"
	user.Department = "cloud"
	user.Company = "atlasdata"
	user.Verification = "yes"
	//No verification required
	claims := MapClaims{
		userKey: user.Name,
	}
	var err error
	user.AccessToken, err = AccessTokenGenerator(claims)
	if err != nil {
		log.Error("super user access token generator error")
	}
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
	casEnf.AddGroupingPolicy(name, ADMIN)
	casEnf.AddGroupingPolicy(name, USER)
	//添加管理员组的用户管理权限
	casEnf.AddPolicy(USER, "list-atlas-maps", "GET")
	casEnf.AddPolicy(USER, "list-atlas-fonts", "GET")
	casEnf.AddPolicy(USER, "list-atlas-ts", "GET")
	casEnf.AddPolicy(USER, "list-atlas-datasets", "GET")
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

//setupRouter 初始化GIN引擎并配置路由
func setupRouter() *gin.Engine {
	// gin.SetMode(gin.ReleaseMode)
	// r := gin.New()
	if runtime.GOOS == "windows" {
		gin.DisableConsoleColor()
	}
	r := gin.Default()
	//gzip
	// r.Use(gzip.Gzip(gzip.DefaultCompression))
	//cors
	config := cors.DefaultConfig()
	// config.AllowAllOrigins = true
	config.AllowOrigins = []string{"*"}
	config.AllowWildcard = true
	config.AllowCredentials = true
	config.AddAllowHeaders("Authorization")
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
	r.GET("/crs/", crsList)
	r.GET("/encoding/", encodingList)
	r.GET("/ftype/", fieldTypeList)
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

	//scene 场景接口
	scene := r.Group("/scenes")
	// studio.Use(AuthMidHandler(authMid))
	// studio.Use(UserMidHandler())
	{
		scene.GET("/", listScenes)
		scene.POST("/info/", createScene)
		scene.GET("/info/:id/", getScene)
		scene.POST("/info/:id/", updateScene)
		scene.POST("/delete/:ids/", deleteScene)
	}

	//image 场景接口
	onlines := r.Group("/online")
	// studio.Use(AuthMidHandler(authMid))
	// studio.Use(UserMidHandler())
	{
		onlines.GET("/images/", listOnlineImages)
		onlines.GET("/tilesets/", listOnlineTiles)
		onlines.GET("/terrains/", listOnlineTerrains)
		onlines.GET("/symbols/", listOnlineSymbols)
		onlines.POST("/symbols/", getOnlineSymbols)

	}

	//serve3d 其他接口
	other := r.Group("/other")
	// studio.Use(AuthMidHandler(authMid))
	// studio.Use(UserMidHandler())
	{
		other.GET("/geocoder", geoCoder)
	}

	//studio
	studio := r.Group("/studio")
	studio.Use(AuthMidHandler(authMid))
	studio.Use(UserMidHandler())
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
	//account
	account := r.Group("/account")
	account.Use(AuthMidHandler(authMid))
	account.Use(UserMidHandler())
	{
		//render
		account.GET("/index/", renderAccount)
		account.GET("/update/", renderUpdateUser)
		account.GET("/password/", UserMidHandler(), renderChangePassword)
		//api
		account.GET("/", getUser)
		account.POST("/signout/", signout)
		account.GET("/verify/", sendVerification)
		account.POST("/update/", updateUser)
		account.GET("/jwt/refresh/", authTokenRefresh)
		account.GET("/token/refresh/", authTokenRefresh)
		account.POST("/password/", changePassword)
	}
	//users
	user := r.Group("/users")
	user.Use(AuthMidHandler(authMid))
	user.Use(AdminMidHandler())
	{
		//authn > users
		user.GET("/", listUsers)
		user.POST("/", addUser)
		user.GET("/:id/", getUser)
		user.POST("/:id/", updateUser)
		user.POST("/:id/delete/", deleteUser)
		user.POST("/:id/password/", changePassword)
		user.GET("/:id/roles/", getUserRoles)           //该用户拥有哪些角色
		user.POST("/:id/roles/", addUserRole)           //添加用户角色
		user.POST("/:id/roles/delete/", deleteUserRole) //删除用户角色
		user.GET("/:id/maps/", getUserMaps)             //该用户拥有哪些权限（含资源与操作）
		user.POST("/:id/maps/", addUserMap)
		user.POST("/:id/maps/delete/", deleteUserMap)
	}
	//roles
	role := r.Group("/roles")
	role.Use(AuthMidHandler(authMid))
	role.Use(AdminMidHandler())
	{
		//authn > roles
		role.GET("/", listRoles)
		role.POST("/", createRole)
		role.POST("/:id/delete/", deleteRole)
		role.GET("/:id/users/", getRoleUsers) //该角色包含哪些用户
		role.GET("/:id/maps/", getRoleMaps)   //该用户拥有哪些权限（含资源与操作）
		role.POST("/:id/maps/", addRoleMap)
		role.POST("/:id/maps/delete/", deleteRoleMap)
	}

	//maproute
	maproute := r.Group("/apps")
	maproute.Use(AccessMidHandler())
	maproute.Use(AuthMidHandler(authMid))
	{
		// > map op
		maproute.GET("/", listMaps)
		maproute.GET("/:id/", getMap)
		maproute.GET("/:id/perms/", getMapPerms)
		maproute.GET("/:id/export/", exportMap)
		maproute.POST("/", createMap)
		maproute.POST("/:id/", updInsertMap)
		maproute.POST("/:id/delete/", deleteMap)
	}

	styles := r.Group("/maps")
	styles.Use(AccessMidHandler())
	styles.Use(AuthMidHandler(authMid))
	{
		// > styles
		styles.GET("/", listStyles)
		styles.GET("/info/:id/", getStyleInfo)
		styles.POST("/info/:id/", updateStyleInfo)
		styles.GET("/x/:id/", getStyle)
		styles.GET("/x/:id/sprite:fmt", getSprite)
		styles.POST("/upload/", uploadStyle)
		styles.POST("/public/:id/", publicStyle)
		styles.POST("/private/:id/", privateStyle)
		styles.POST("/create/", createStyle)
		styles.GET("/clone/:id/", cloneStyle)
		styles.POST("/save/:id/", saveStyle)
		styles.POST("/update/:id/", updateStyle)
		styles.POST("/replace/:id/", replaceStyle)
		styles.GET("/download/:id/", downloadStyle)
		styles.POST("/delete/:ids/", deleteStyle)

		styles.POST("/sprite/:id/", uploadSprite)
		styles.POST("/sprite/:id/:name", updateSprite)
		styles.GET("/icon/:id/:name/", getIcon)
		styles.POST("/icon/:id/:name/", updateIcon)
		styles.POST("/icons/:id/", uploadIcons)
		styles.POST("/icons/:id/delete/", deleteIcons)

		styles.GET("/view/:id", getViewStyle)
		styles.GET("/view/:id/", viewStyle) //view map style

		styles.GET("/search/:id/", search)
		styles.POST("/edit/:id/", updateStyle) //updateStyle
	}
	fonts := r.Group("/fonts")
	fonts.Use(AccessMidHandler())
	fonts.Use(AuthMidHandler(authMid))
	{
		// > fonts
		fonts.GET("/", listFonts)                      //get font
		fonts.POST("/upload/", uploadFont)             //upload font
		fonts.POST("/delete/:fontstack/", deleteFonts) //delete font
		fonts.GET("/:fontstack/:range", getGlyphs)     //get glyph pbfs
	}

	tilesets := r.Group("/ts")
	tilesets.Use(AccessMidHandler())
	tilesets.Use(AuthMidHandler(authMid))
	{
		// > tilesets
		tilesets.GET("/", listTilesets)
		tilesets.GET("/info/:id/", getTilesetInfo)     //tilejson
		tilesets.POST("/info/:id/", updateTilesetInfo) //tilejson
		tilesets.GET("/x/:id/", getTileJSON)           //tilejson
		tilesets.GET("/x/:id/:z/:x/:y", getTile)
		tilesets.POST("/upload/", uploadTileset)
		tilesets.POST("/replace/:id/", replaceTileset)
		tilesets.POST("/publish/", publishTileset)
		tilesets.POST("/publish/:id/", rePublishTileset)
		tilesets.POST("/create/:id/", createTilesetLite)
		tilesets.POST("/update/:id/", createTilesetLite)
		tilesets.GET("/download/:id/", downloadTileset)
		tilesets.POST("/delete/:ids/", deleteTileset)
		tilesets.POST("/merge/:ids/", getTile)

		tilesets.GET("/view/:id/", viewTile) //view
	}

	datasets := r.Group("/datasets")
	datasets.Use(AccessMidHandler())
	datasets.Use(AuthMidHandler(authMid))
	{
		// > datasets
		datasets.GET("/", listDatasets)
		datasets.GET("/info/:id/", getDatasetInfo)
		datasets.POST("/info/:id/", updateDatasetInfo)
		datasets.POST("/upload/", uploadFile)
		datasets.GET("/preview/:id/", previewFile)
		datasets.POST("/import/:id/", importFile)
		datasets.POST("/import/", oneClickImport)
		datasets.POST("/update/:id/", upInsertDataset)
		datasets.GET("/download/:id/", downloadDataset)
		datasets.POST("/delete/:id/", deleteDatasets)
		datasets.POST("/delete/:id/:fids/", deleteFeatures)

		datasets.GET("/view/:id/", viewDataset) //view

		datasets.GET("/geojson/:id/", getGeojson)
		datasets.POST("/query/:id/", queryGeojson)
		datasets.POST("/common/:id/", queryExec)

		datasets.GET("/distinct/:id/", getDistinctValues)
		datasets.GET("/search/:id/", search)
		datasets.GET("/buffer/:id/", getBuffers)

		datasets.GET("/x/:id/", getTileLayerJSON)
		datasets.GET("/x/:id/:z/:x/:y", getTileLayer)
		datasets.POST("/x/:id/", createTileLayer)

		datasets.GET("/publish/:id/:min/:max/", publishToMBTiles)

	}
	tasks := r.Group("/tasks")
	tasks.Use(AuthMidHandler(authMid))
	tasks.Use(UserMidHandler())
	{
		tasks.GET("/", listTasks)
		tasks.GET("/info/:ids/", taskQuery)
		tasks.GET("/stream/:id/", taskStreamQuery)
	}
	//utilroute
	utilroute := r.Group("/util")
	utilroute.Use(AuthMidHandler(authMid))
	{
		// > utils
		utilroute.GET("/export/maps/", exportMaps)
		utilroute.POST("/import/maps/", importMaps)
	}

	//searchroute
	searchroute := r.Group("/dm")
	searchroute.Use(AuthMidHandler(authMid))
	{
		// > trees
		searchroute.GET("/tree/", getTreeNodes)
		searchroute.GET("/tree/:name/", queryTreeNode)
		searchroute.GET("/list/", getListNodes)
		searchroute.GET("/list/:gid/", queryAdvanced)
		searchroute.GET("/search/", searchAdvanced)
		searchroute.GET("/search/:gid/", queryAdvanced)
		searchroute.GET("/pdf/:name/", getRedFile)
	}

	return r
}

// force redirect to https from http// necessary only if you use https directly// put your domain name instead of CONF.ORIGIN
func redirectToHTTPS(w http.ResponseWriter, req *http.Request) {
	//http.Redirect(w, req, "https://" + CONF.ORIGIN + req.RequestURI, http.StatusMovedPermanently)
}

func initOnlineSources() {
	{
		imageSrcs := `[
		{
		"_id": "5d401d9c7720c908b40066c9",
		"dataType": "image",
		"cnname": "tianditu_map",
		"enname": "tianditu_map",
		"url": "http://t6.tianditu.com/DataServer?T=vec_w&x={x}&y={y}&l={z}",
		"coordType": "WGS84",
		"requireField": "tk",
		"thumbnail": "https://lab2.cesiumlab.com/upload/b2989021-2a82-457d-8703-69c288154cee/2019_07_30_18_38_17.jpg",
		"date": "2019-07-30 18:38:44"
		},
		{
		"_id": "5d4023c17720c905f4e1f99c",
		"dataType": "image",
		"cnname": "tianditu_image",
		"enname": "tianditu_image",
		"url": "http://t6.tianditu.com/DataServer?T=img_w&x={x}&y={y}&l={z}",
		"coordType": "WGS84",
		"requireField": "tk",
		"thumbnail": "https://lab2.cesiumlab.com/upload/ee3ab9e0-b769-46e4-95e0-91bfe1787792\\2019_07_30_19_32_02.jpg",
		"date": "2019-07-30 19:32:07"
		},
		{
		"_id": "5d4025e27720c905f4e1f99d",
		"dataType": "image",
		"cnname": "google_map",
		"enname": "google_map",
		"url": "http://mt1.google.cn/vt?lyrs=m&gl=CN&x={x}&y={y}&z={z}",
		"coordType": "GCJ02",
		"requireField": " ",
		"thumbnail": "https://lab2.cesiumlab.com/upload/3aed23eb-a2ef-4d49-9893-ec1731c33b4a\\2019_07_30_19_32_22.jpg",
		"date": "2019-07-30 19:32:24"
		},
		{
		"_id": "5d4025ff7720c905f4e1f99e",
		"dataType": "image",
		"cnname": "google_imagewithlabel",
		"enname": "google_imagewithlabel",
		"url": "http://mt1.google.cn/vt?lyrs=s,h&gl=CN&x={x}&y={y}&z={z}",
		"coordType": "GCJ02",
		"requireField": " ",
		"thumbnail": "https://lab2.cesiumlab.com/upload/878564e4-6a86-4752-9d98-68eb6e9bd37e\\2019_07_30_19_32_35.jpg",
		"date": "2019-07-30 19:32:36"
		},
		{
		"_id": "5d4026237720c905f4e1f99f",
		"dataType": "image",
		"cnname": "google_image_label",
		"enname": "google_image_label",
		"url": "http://mt1.google.cn/vt?lyrs=h&gl=CN&x={x}&y={y}&z={z}",
		"coordType": "GCJ02",
		"requireField": " ",
		"thumbnail": "https://lab2.cesiumlab.com/upload/860b5719-701a-4568-8310-59bac025ce29\\2019_07_30_19_32_45.jpg",
		"date": "2019-07-30 19:32:46"
		},
		{
		"_id": "5d4026417720c905f4e1f9a0",
		"dataType": "image",
		"cnname": "google_image",
		"enname": "google_image",
		"url": "http://mt1.google.cn/vt?lyrs=s&x={x}&y={y}&z={z}",
		"coordType": "WGS84",
		"requireField": " ",
		"thumbnail": "https://lab2.cesiumlab.com/upload/97ce8620-a191-4506-911d-a94f4f9d0385\\2019_07_30_19_32_59.jpg",
		"date": "2019-07-30 19:33:01"
		},
		{
		"_id": "5d402bf27720c91a3c59419d",
		"dataType": "image",
		"cnname": "tianditu_map_label",
		"enname": "tianditu_map_label",
		"url": "http://t3.tianditu.com/DataServer?T=cva_w&x={x}&y={y}&l={z}",
		"coordType": "WGS84",
		"requireField": "tk",
		"thumbnail": "https://lab2.cesiumlab.com/upload/1b1cfa39-c316-4e01-9ef4-4ba0e9e90b68\\2019_07_30_19_37_21.jpg",
		"date": "2019-07-30 19:37:22"
		},
		{
		"_id": "5d402c157720c91a3c59419e",
		"dataType": "image",
		"cnname": "gaode_map",
		"enname": "gaode_map",
		"url": "http://webrd04.is.autonavi.com/appmaptile?lang=zh_cn&size=1&scale=1&style=7&x={x}&y={y}&z={z}",
		"coordType": "GCJ02",
		"requireField": " ",
		"thumbnail": "https://lab2.cesiumlab.com/upload/54c71b94-892e-42ee-bdd7-858cf5fa2399\\2019_07_30_19_37_56.jpg",
		"date": "2019-07-30 19:37:57"
		},
		{
		"_id": "5d402c377720c91a3c59419f",
		"dataType": "image",
		"cnname": "gaode_image",
		"enname": "gaode_image",
		"url": "http://webst02.is.autonavi.com/appmaptile?style=6&x={x}&y={y}&z={z}",
		"coordType": "GCJ02",
		"requireField": " ",
		"thumbnail": "https://lab2.cesiumlab.com/upload/45548a6b-5cad-4f0f-8ca1-3c9372cefd99\\2019_07_30_19_38_30.jpg",
		"date": "2019-07-30 19:38:31"
		},
		{
		"_id": "5d402c5d7720c91a3c5941a0",
		"dataType": "image",
		"cnname": "gaode_image_label",
		"enname": "gaode_image_label",
		"url": "http://webst02.is.autonavi.com/appmaptile?style=8&x={x}&y={y}&z={z}",
		"coordType": "GCJ02",
		"requireField": " ",
		"thumbnail": "https://lab2.cesiumlab.com/upload/860ede97-8461-408c-979e-dc4a213849b8\\2019_07_30_19_39_07.jpg",
		"date": "2019-07-30 19:39:09"
		},
		{
		"_id": "5d402c797720c91a3c5941a1",
		"dataType": "image",
		"cnname": "baidu_map",
		"enname": "baidu_map",
		"url": "http://online1.map.bdimg.com/onlinelabel/?qt=tile&x={x}&y={y}&z={z}&styles=pl&scaler=1&p=1",
		"coordType": "BD09",
		"requireField": " ",
		"thumbnail": "https://lab2.cesiumlab.com/upload/e7a26b44-058e-420b-8554-8cf33dc7b7a6\\2019_07_30_19_39_37.jpg",
		"date": "2019-07-30 19:39:37"
		},
		{
		"_id": "5d402c997720c91a3c5941a2",
		"dataType": "image",
		"cnname": "baidu_image",
		"enname": "baidu_image",
		"url": "http://shangetu1.map.bdimg.com/it/u=x={x};y={y};z={z};v=009;type=sate&fm=46",
		"coordType": "BD09",
		"requireField": " ",
		"thumbnail": "https://lab2.cesiumlab.com/upload/764dee7e-4f17-4c93-ab35-54b6c0548054\\2019_07_30_19_40_08.jpg",
		"date": "2019-07-30 19:40:09"
		},
		{
		"_id": "5d402cdd7720c91a3c5941a3",
		"dataType": "image",
		"cnname": "baidu_image_label",
		"enname": "baidu_image_label",
		"url": "http://online6.map.bdimg.com/tile/?qt=tile&x={x}&y={y}&z={z}&styles=sl&v=020",
		"coordType": "BD09",
		"requireField": " ",
		"thumbnail": "https://lab2.cesiumlab.com/upload/dc365474-8f27-48d1-aa5d-95043efb3601\\2019_07_30_19_41_00.jpg",
		"date": "2019-07-30 19:41:17"
		},
		{
		"_id": "5d402d017720c91a3c5941a4",
		"dataType": "image",
		"cnname": "baidu_map_midnight",
		"enname": "baidu_map_midnight",
		"url": "http://api0.map.bdimg.com/customimage/tile?=&x={x}&y={y}&z={z}&scale=1&customid=midnight",
		"coordType": "BD09",
		"requireField": " ",
		"thumbnail": "https://lab2.cesiumlab.com/upload/5ff215b3-2013-4169-a132-e456e65ecfd2\\2019_07_30_19_41_53.jpg",
		"date": "2019-07-30 19:41:53"
		},
		{
		"_id": "5d402d2f7720c91a3c5941a5",
		"dataType": "image",
		"cnname": "baidu_map_dark",
		"enname": "baidu_map_dark",
		"url": "http://api2.map.bdimg.com/customimage/tile?=&x={x}&y={y}&z={z}&scale=1&customid=dark",
		"coordType": "BD09",
		"requireField": " ",
		"thumbnail": "https://lab2.cesiumlab.com/upload/2144191a-8cf0-446c-9c63-b3c4de21d0ba\\2019_07_30_19_42_38.jpg",
		"date": "2019-07-30 19:42:39"
		},
		{
		"_id": "5d402d4d7720c91a3c5941a6",
		"dataType": "image",
		"cnname": "openstreetmap",
		"enname": "openstreetmap",
		"url": "https://c.tile.openstreetmap.org/{z}/{x}/{y}.png",
		"coordType": "WGS84",
		"requireField": " ",
		"thumbnail": "https://lab2.cesiumlab.com/upload/f236ad7a-bc54-4f93-91ca-3e7f8be7faa6\\2019_07_30_19_43_08.jpg",
		"date": "2019-07-30 19:43:09"
		},
		{
		"_id": "5d402dba7720c91a3c5941a7",
		"dataType": "image",
		"cnname": "mapbox_satellite",
		"enname": "mapbox_satellite",
		"url": "https://c.tiles.mapbox.com/v4/mapbox.satellite/{z}/{x}/{y}.png?access_token=pk.eyJ1IjoibWFwYm94IiwiYSI6ImNpejY4M29iazA2Z2gycXA4N2pmbDZmangifQ.-g_vE53SD2WrJ6tFX7QHmA",
		"coordType": "WGS84",
		"requireField": " ",
		"thumbnail": "https://lab2.cesiumlab.com/upload/5bce4539-d132-4751-84ff-0ea77b3ca287\\2019_07_30_19_44_57.jpg",
		"date": "2019-07-30 19:44:58"
		},
		{
		"_id": "5d402de67720c91a3c5941a8",
		"dataType": "image",
		"cnname": "mapbox_streets",
		"enname": "mapbox_streets",
		"url": "https://c.tiles.mapbox.com/v4/mapbox.streets/{z}/{x}/{y}.png?access_token=pk.eyJ1IjoibWFwYm94IiwiYSI6ImNpejY4M29iazA2Z2gycXA4N2pmbDZmangifQ.-g_vE53SD2WrJ6tFX7QHmA",
		"coordType": "WGS84",
		"requireField": " ",
		"thumbnail": "https://lab2.cesiumlab.com/upload/481dd4cb-69e7-45b4-85e2-ed0f5cd3e521\\2019_07_30_19_45_41.jpg",
		"date": "2019-07-30 19:45:42"
		},
		{
		"_id": "5d402e087720c91a3c5941a9",
		"dataType": "image",
		"cnname": "mapbox_light",
		"enname": "mapbox_light",
		"url": "https://c.tiles.mapbox.com/v4/mapbox.light/{z}/{x}/{y}.png?access_token=pk.eyJ1IjoibWFwYm94IiwiYSI6ImNpejY4M29iazA2Z2gycXA4N2pmbDZmangifQ.-g_vE53SD2WrJ6tFX7QHmA",
		"coordType": "WGS84",
		"requireField": " ",
		"thumbnail": "https://lab2.cesiumlab.com/upload/ce49f35e-da93-42e0-b2a8-b5ad39bfa68c\\2019_07_30_19_45_59.jpg",
		"date": "2019-07-30 19:46:16"
		},
		{
		"_id": "5d402e277720c91a3c5941aa",
		"dataType": "image",
		"cnname": "mapbox_dark",
		"enname": "mapbox_dark",
		"url": "https://c.tiles.mapbox.com/v4/mapbox.dark/{z}/{x}/{y}.png?access_token=pk.eyJ1IjoibWFwYm94IiwiYSI6ImNpejY4M29iazA2Z2gycXA4N2pmbDZmangifQ.-g_vE53SD2WrJ6tFX7QHmA",
		"coordType": "WGS84",
		"requireField": " ",
		"thumbnail": "https://lab2.cesiumlab.com/upload/862e6c95-07e8-4e42-9427-9472dd4c63df\\2019_07_30_19_46_46.jpg",
		"date": "2019-07-30 19:46:47"
		},
		{
		"_id": "5d402e4a7720c91a3c5941ab",
		"dataType": "image",
		"cnname": "mapbox_streets_satellite",
		"enname": "mapbox_streets_satellite",
		"url": "https://c.tiles.mapbox.com/v4/mapbox.streets-satellite/{z}/{x}/{y}.png?access_token=pk.eyJ1IjoibWFwYm94IiwiYSI6ImNpejY4M29iazA2Z2gycXA4N2pmbDZmangifQ.-g_vE53SD2WrJ6tFX7QHmA",
		"coordType": "WGS84",
		"requireField": " ",
		"thumbnail": "https://lab2.cesiumlab.com/upload/e2e17da4-ff19-4bf0-9f74-1c4d30790156\\2019_07_30_19_47_21.jpg",
		"date": "2019-07-30 19:47:22"
		},
		{
		"_id": "5d402e6a7720c91a3c5941ac",
		"dataType": "image",
		"cnname": "mapbox_wheatpaste",
		"enname": "mapbox_wheatpaste",
		"url": "https://c.tiles.mapbox.com/v4/mapbox.wheatpaste/{z}/{x}/{y}.png?access_token=pk.eyJ1IjoibWFwYm94IiwiYSI6ImNpejY4M29iazA2Z2gycXA4N2pmbDZmangifQ.-g_vE53SD2WrJ6tFX7QHmA",
		"coordType": "WGS84",
		"requireField": " ",
		"thumbnail": "https://lab2.cesiumlab.com/upload/40130c8b-ad6d-41c4-8f41-65dc30131a4d\\2019_07_30_19_47_53.jpg",
		"date": "2019-07-30 19:47:54"
		},
		{
		"_id": "5d402e8f7720c91a3c5941ad",
		"dataType": "image",
		"cnname": "mapbox_streets_basic",
		"enname": "mapbox_streets_basic",
		"url": "https://c.tiles.mapbox.com/v4/mapbox.streets-basic/{z}/{x}/{y}.png?access_token=pk.eyJ1IjoibWFwYm94IiwiYSI6ImNpejY4M29iazA2Z2gycXA4N2pmbDZmangifQ.-g_vE53SD2WrJ6tFX7QHmA",
		"coordType": "WGS84",
		"requireField": " ",
		"thumbnail": "https://lab2.cesiumlab.com/upload/1562f19d-15c6-408d-9758-0a3947582ca5\\2019_07_30_19_48_30.jpg",
		"date": "2019-07-30 19:48:31"
		},
		{
		"_id": "5d402eb57720c91a3c5941ae",
		"dataType": "image",
		"cnname": "mapbox_comic",
		"enname": "mapbox_comic",
		"url": "https://c.tiles.mapbox.com/v4/mapbox.comic/{z}/{x}/{y}.png?access_token=pk.eyJ1IjoibWFwYm94IiwiYSI6ImNpejY4M29iazA2Z2gycXA4N2pmbDZmangifQ.-g_vE53SD2WrJ6tFX7QHmA",
		"coordType": "WGS84",
		"requireField": " ",
		"thumbnail": "https://lab2.cesiumlab.com/upload/514dc9dd-021c-4bee-81e3-153c2a6efd8e\\2019_07_30_19_49_08.jpg",
		"date": "2019-07-30 19:49:09"
		},
		{
		"_id": "5d402ee17720c91a3c5941af",
		"dataType": "image",
		"cnname": "mapbox_outdoors",
		"enname": "mapbox_outdoors",
		"url": "https://c.tiles.mapbox.com/v4/mapbox.outdoors/{z}/{x}/{y}.png?access_token=pk.eyJ1IjoibWFwYm94IiwiYSI6ImNpejY4M29iazA2Z2gycXA4N2pmbDZmangifQ.-g_vE53SD2WrJ6tFX7QHmA",
		"coordType": "WGS84",
		"requireField": " ",
		"thumbnail": "https://lab2.cesiumlab.com/upload/49190b16-45a9-4b40-81ef-69902321e536\\2019_07_30_19_49_49.jpg",
		"date": "2019-07-30 19:49:53"
		},
		{
		"_id": "5d402efe7720c91a3c5941b0",
		"dataType": "image",
		"cnname": "mapbox_run_bike_hike",
		"enname": "mapbox_run_bike_hike",
		"url": "https://c.tiles.mapbox.com/v4/mapbox.run-bike-hike/{z}/{x}/{y}.png?access_token=pk.eyJ1IjoibWFwYm94IiwiYSI6ImNpejY4M29iazA2Z2gycXA4N2pmbDZmangifQ.-g_vE53SD2WrJ6tFX7QHmA",
		"coordType": "WGS84",
		"requireField": " ",
		"thumbnail": "https://lab2.cesiumlab.com/upload/6bad1ff2-f123-4729-9ec4-b67ceee9470d\\2019_07_30_19_50_21.jpg",
		"date": "2019-07-30 19:50:22"
		},
		{
		"_id": "5d402f237720c91a3c5941b1",
		"dataType": "image",
		"cnname": "mapbox_pencil",
		"enname": "mapbox_pencil",
		"url": "https://c.tiles.mapbox.com/v4/mapbox.pencil/{z}/{x}/{y}.png?access_token=pk.eyJ1IjoibWFwYm94IiwiYSI6ImNpejY4M29iazA2Z2gycXA4N2pmbDZmangifQ.-g_vE53SD2WrJ6tFX7QHmA",
		"coordType": "WGS84",
		"requireField": " ",
		"thumbnail": "https://lab2.cesiumlab.com/upload/e5452ed5-93ca-45ae-91f0-7fa19c90460d\\2019_07_30_19_50_58.jpg",
		"date": "2019-07-30 19:50:59"
		},
		{
		"_id": "5d402f447720c91a3c5941b2",
		"dataType": "image",
		"cnname": "mapbox_pirates",
		"enname": "mapbox_pirates",
		"url": "https://c.tiles.mapbox.com/v4/mapbox.pirates/{z}/{x}/{y}.png?access_token=pk.eyJ1IjoibWFwYm94IiwiYSI6ImNpejY4M29iazA2Z2gycXA4N2pmbDZmangifQ.-g_vE53SD2WrJ6tFX7QHmA",
		"coordType": "WGS84",
		"requireField": " ",
		"thumbnail": "https://lab2.cesiumlab.com/upload/b5526e93-d8bf-4d84-8fd9-392d330c9b4f\\2019_07_30_19_51_31.jpg",
		"date": "2019-07-30 19:51:32"
		},
		{
		"_id": "5d402f6b7720c91a3c5941b3",
		"dataType": "image",
		"cnname": "mapbox_emerald",
		"enname": "mapbox_emerald",
		"url": "https://c.tiles.mapbox.com/v4/mapbox.emerald/{z}/{x}/{y}.png?access_token=pk.eyJ1IjoibWFwYm94IiwiYSI6ImNpejY4M29iazA2Z2gycXA4N2pmbDZmangifQ.-g_vE53SD2WrJ6tFX7QHmA",
		"coordType": "WGS84",
		"requireField": " ",
		"thumbnail": "https://lab2.cesiumlab.com/upload/aa62c222-dce1-4388-8dd9-8e7c58aac484\\2019_07_30_19_52_10.jpg",
		"date": "2019-07-30 19:52:11"
		},
		{
		"_id": "5d402f8f7720c91a3c5941b4",
		"dataType": "image",
		"cnname": "mapbox_high_contrast",
		"enname": "mapbox_high_contrast",
		"url": "https://c.tiles.mapbox.com/v4/mapbox.high-contrast/{z}/{x}/{y}.png?access_token=pk.eyJ1IjoibWFwYm94IiwiYSI6ImNpejY4M29iazA2Z2gycXA4N2pmbDZmangifQ.-g_vE53SD2WrJ6tFX7QHmA",
		"coordType": "WGS84",
		"requireField": " ",
		"thumbnail": "https://lab2.cesiumlab.com/upload/99e8d345-9cb0-49f0-a7bb-c68ab02495bf\\2019_07_30_19_52_46.jpg",
		"date": "2019-07-31 11:21:21"
		},
		{
		"_id": "5d40239d7720c905f4e1f99b",
		"dataType": "image",
		"cnname": "tianditu_image_label",
		"enname": "tianditu_image_label",
		"url": "http://t6.tianditu.com/DataServer?T=cia_w&x={x}&y={y}&l={z}",
		"coordType": "WGS84",
		"requireField": "tk",
		"thumbnail": "https://lab2.cesiumlab.com/upload/e43aeb21-63a5-4639-9ea5-c39369c0bba2\\2019_07_30_19_31_09.jpg",
		"date": "2019-08-20 14:36:44"
		},
		{
		"_id": "5e67150c7720c9077482b6bf",
		"dataType": "image",
		"cnname": "arcgis在线影像",
		"enname": "arcgis_imagery",
		"url": "http://server.arcgisonline.com/arcgis/rest/services/World_Imagery/MapServer/tile/{z}/{y}/{x}",
		"coordType": "WGS84",
		"requireField": "",
		"thumbnail": "https://lab2.cesiumlab.com/upload/c38aab50-46c5-482d-9038-8d25dfb40dcd\\2020_03_10_12_18_07.jpg",
		"date": "2020-03-10 12:25:52"
		}
	]`

		images := []OnlineImage{}
		err := json.Unmarshal([]byte(imageSrcs), &images)
		if err != nil {
			log.Error(err)
		}
		for _, v := range images {
			res := db.Create(v)
			if res.Error != nil {
				log.Error(res.Error)
			}
		}

		fmt.Printf("insert %d rows\n", len(images))
	}
	{
		tileSrcs := `[
	{
	"_id": "5d40272c7720c91510fdcda9",
	"dataType": "model",
	"cnname": "倾斜单体测试",
	"enname": "倾斜单体测试",
	"url": "https://lab.earthsdk.com/model/de2a2300ac2d11e99dbd8fd044883638/tileset.json",
	"thumbnail": "//lab2.cesiumlab.com/upload/4c4f564b-2e21-46e8-b529-604f9ee9aa0e\\2019_08_04_20_54_36.jpg",
	"date": "2019-09-29 10:59:04"
	},
	{
	"_id": "5e60cd447720c90df0e0c80c",
	"dataType": "model",
	"cnname": "谷歌地球",
	"enname": "google earth",
	"url": "https://lab.earthsdk.com/ge/tileset.json",
	"thumbnail": "http://lab2.cesiumlab.com//upload/bc245ab9-8d39-4fb6-915c-2fa2e2f864b4\\2020_03_05_17_58_13.jpg",
	"date": "2020-03-05 17:58:50"
	},
	{
	"_id": "5e9e5b7b7720c91384e85e31",
	"dataType": "model",
	"cnname": "美丽乡村",
	"enname": "Beautiful countryside",
	"url": "http://lab.earthsdk.com/model/b2039420837611eaae25edb63a66f405/tileset.json",
	"thumbnail": "https://lab2.cesiumlab.com/upload/95e67715-82e2-489d-a383-d788794fc734\\2020_04_21_10_33_18.jpg",
	"date": "2020-04-21 10:33:31"
	},
	{
	"_id": "5ee1f33e7720c92388d40bf0",
	"dataType": "model",
	"cnname": "OSM建筑",
	"enname": "OSM buildings",
	"url": "Ion(96188)",
	"thumbnail": "https://lab2.cesiumlab.com/upload/df07c0a6-0b01-4213-b664-80d9c437238b\\2020_06_11_16_58_06.jpg",
	"date": "2020-06-11 17:02:54"
	},
	{
	"_id": "5f1c00cf7720c92388d56fb5",
	"dataType": "model",
	"cnname": "BIM测试纯色",
	"enname": "BIM测试纯色",
	"url": "https://lab.earthsdk.com/model/707d3120ce5b11eab7a4adf1d6568ff7/tileset.json",
	"thumbnail": "//lab2.cesiumlab.com/upload/6cf6c81d-f19f-48b7-8397-2e07a14d587e\\2020_07_25_17_56_25.jpg",
	"date": "2020-07-25 17:57:03"
	},
	{
	"_id": "5d4027197720c91510fdcda8",
	"dataType": "model",
	"cnname": "BIM测试纹理视图",
	"enname": "BIM测试纹理视图",
	"url": "https://lab.earthsdk.com/model/8028ba40ce5b11eab7a4adf1d6568ff7/tileset.json",
	"thumbnail": "//lab2.cesiumlab.com/upload/aebfc8f3-3ac4-4635-993a-bfc1d467dd11\\2020_07_25_17_57_12.jpg",
	"date": "2020-07-25 17:57:20"
	},
	{
	"_id": "5d40273b7720c91510fdcdaa",
	"dataType": "model",
	"cnname": "倾斜测试",
	"enname": "倾斜测试",
	"url": "https://lab.earthsdk.com/model/66327820ce5f11eab7a4adf1d6568ff7/tileset.json",
	"thumbnail": "//lab2.cesiumlab.com/upload/7737a111-a831-4448-a052-ea1884285d39\\2019_08_04_20_54_42.jpg",
	"date": "2020-07-25 18:13:20"
	},
	{
	"_id": "5f1c06197720c92388d56fcc",
	"dataType": "model",
	"cnname": "倾斜测试跨平台优化",
	"enname": "倾斜测试跨平台优化",
	"url": "https://lab.earthsdk.com/model/8c5299e0ce5f11eab7a4adf1d6568ff7/tileset.json",
	"thumbnail": "//lab2.cesiumlab.com/upload/7737a111-a831-4448-a052-ea1884285d39\\2019_08_04_20_54_42.jpg",
	"date": "2020-07-25 18:15:16"
	},
	{
	"_id": "5dcc26be7720c91a7cfcc69c",
	"dataType": "model",
	"cnname": "小工厂",
	"enname": "xiaogongchang",
	"url": "https://lab.earthsdk.com/model/887b3db0cd4f11eab7a4adf1d6568ff7/tileset.json",
	"thumbnail": "https://lab2.cesiumlab.com/upload/e6827796-398c-4dbb-99e3-457f4953bdf2\\2019_11_13_23_51_34.png",
	"date": "2020-07-27 09:13:56"
	},
	{
	"_id": "5d71a78c7720c90c9001d61c",
	"dataType": "model",
	"cnname": "白模测试2",
	"enname": "model test2",
	"url": "https://lab.earthsdk.com/model/3610c2b0d08411eab7a4adf1d6568ff7/tileset.json",
	"thumbnail": "//lab2.cesiumlab.com/upload/4bb1fd43-9799-4dd0-a4fe-f147345539cf\\2019_09_06_08_25_30.jpg",
	"date": "2020-07-28 11:42:29"
	},
	{
	"_id": "5d40274c7720c91510fdcdab",
	"dataType": "model",
	"cnname": "白模测试",
	"enname": "白模测试",
	"url": "https://lab.earthsdk.com/model/17a32610d08411eab7a4adf1d6568ff7/tileset.json",
	"thumbnail": "//lab2.cesiumlab.com/upload/326fae07-ecf2-4978-b7ce-77e0f10b6bdf\\2019_08_04_20_54_17.jpg",
	"date": "2020-07-28 11:42:47"
	},
	{
	"_id": "5de1eb8f7720c90f001bfa8b",
	"dataType": "model",
	"cnname": "故宫_跨平台压缩",
	"enname": "gugong_hailiang",
	"url": "http://lab.earthsdk.com/model/a8aac960dd1811ea819fcd8348f8961f/tileset.json",
	"thumbnail": "http://lab2.cesiumlab.com/upload/89467069-da06-49b3-87fd-037e199ee65a/2019_08_17_18_15_42.jpg",
	"date": "2020-08-13 11:55:39"
	},
	{
	"_id": "5d57d3d17720c9144cb59d96",
	"dataType": "model",
	"cnname": "故宫",
	"enname": "Imperial Palace",
	"url": "https://lab.earthsdk.com/model/70e1bbd0008e11ebae58995d10455715/tileset.json",
	"thumbnail": "//lab2.cesiumlab.com/upload/89467069-da06-49b3-87fd-037e199ee65a\\2019_08_17_18_15_42.jpg",
	"date": "2020-09-27 14:57:26"
	}
	]`

		tiles := []OnlineTileset{}
		err := json.Unmarshal([]byte(tileSrcs), &tiles)
		if err != nil {
			log.Error(err)
		}
		for _, v := range tiles {
			res := db.Create(v)
			if res.Error != nil {
				log.Error(res.Error)
			}
		}

		fmt.Printf("insert %d rows\n", len(tiles))
	}
	{
		terrainSrcs := `[
	{
	"_id": "5d4026ea7720c91510fdcda7",
	"dataType": "terrain",
	"cnname": " cesium官方",
	"enname": "cesium官方",
	"url": "Ion(1)",
	"notSupportNormal": false,
	"notSupportWater": false,
	"thumbnail": "https://lab2.cesiumlab.com/upload/3a6ebc9c-1f15-47a2-ada2-a5cccb018d8c\\2019_08_02_19_46_14.jpg",
	"date": "2019-08-02 19:46:15"
	},
	{
	"_id": "5d40266e7720c905f4e1f9a1",
	"dataType": "terrain",
	"cnname": "世界12级（测试）",
	"enname": "世界12级（测试）",
	"url": "https://lab.earthsdk.com/terrain/42752d50ac1f11e99dbd8fd044883638",
	"notSupportNormal": false,
	"notSupportWater": false,
	"thumbnail": "https://lab2.cesiumlab.com/upload/55f2216c-21ef-4382-b157-afa7c32444ae\\2019_08_02_19_45_45.jpg",
	"date": "2019-08-22 18:35:22"
	},
	{
	"_id": "5d40268e7720c905f4e1f9a2",
	"dataType": "terrain",
	"cnname": "中国14级（测试）",
	"enname": "中国14级（测试）",
	"url": "https://lab.earthsdk.com/terrain/577fd5b0ac1f11e99dbd8fd044883638",
	"notSupportNormal": false,
	"notSupportWater": false,
	"thumbnail": "https://lab2.cesiumlab.com/upload/3fd1ac60-2683-4ae8-a5da-c0250edc836b\\2019_08_02_19_45_38.jpg",
	"date": "2019-08-22 18:35:30"
	}
	]`

		terrains := []OnlineTerrain{}
		err := json.Unmarshal([]byte(terrainSrcs), &terrains)
		if err != nil {
			log.Error(err)
		}
		for _, v := range terrains {
			res := db.Create(v)
			if res.Error != nil {
				log.Error(res.Error)
			}
		}

		fmt.Printf("insert %d rows\n", len(terrains))
	}

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
	db, err = initSysDb()
	if err != nil {
		log.Fatalf("init sysdb error, details: %s", err)
	}
	defer db.Close()

	{
		initOnlineSources()
	}

	dataDB, err = initDataDb()
	if err != nil {
		log.Fatalf("init datadb error, details: %s", err)
	}
	defer dataDB.Close()

	{
		provArr := make([]dict.Dicter, len(conf.Providers))
		for i := range provArr {
			provArr[i] = conf.Providers[i]
		}
		providers, err = initProviders(provArr)
		if err != nil {
			log.Fatalf("could not register providers: %v", err)
		}

		if len(conf.Cache) > 0 {
			// init cache backends
			cache, err := initCache(conf.Cache)
			if err != nil {
				log.Errorf("could not register cache: %v", err)
			}
			if cache != nil {
				atlas.SetCache(cache)
			}
		}
		// initTegolaServer()
	}

	authMid, err = initAuthJWT()
	if err != nil {
		log.Fatalf("init jwt error: %s", err)
	}

	if DISABLEACCESSTOKEN {
		authMid.DisabledAbort = true
	}

	initSystemUser()
	if initf {
		return
	}
	initTaskRouter()

	{
		pubs, err := LoadServiceSet(ATLAS)
		if err != nil {
			log.Fatalf("load %s's service set error, details: %s", ATLAS, err)
		}
		pubs.AppendStyles()
		pubs.AppendFonts()
		pubs.AppendTilesets()
		userSet.Store(ATLAS, pubs)
	}

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
