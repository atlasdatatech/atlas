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
	db.AutoMigrate(&Scene{}, &OnlineMap{}, &Tileset3d{}, &Terrain3d{}, &Symbol3d{}, &Style3d{})
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
		scene.POST("/create/", createScene)
		scene.GET("/info/:id/", getScene)
		scene.POST("/info/:id/", updateScene)
		scene.DELETE("/delete/:ids/", deleteScene)
	}

	olmaps := r.Group("/olmaps")
	// studio.Use(AuthMidHandler(authMid))
	// studio.Use(UserMidHandler())
	{
		olmaps.GET("/", listOnlineMaps)
		olmaps.POST("/create/", createOnlineMap)
		olmaps.GET("/info/:id/", getOnlineMap)
		olmaps.POST("/info/:id/", updateOnlineMap)
		olmaps.DELETE("/delete/:ids/", deleteOnlineMap)
	}

	ts3d := r.Group("/ts3d")
	// studio.Use(AuthMidHandler(authMid))
	// studio.Use(UserMidHandler())
	{
		ts3d.GET("/", listTilesets3d)
		ts3d.POST("/create/", createTileset3d)
		ts3d.GET("/info/:id/", getTileset3d)
		ts3d.POST("/info/:id/", updateTileset3d)
		ts3d.DELETE("/delete/:ids/", deleteTileset3d)
	}

	terrain3d := r.Group("/terrains3d")
	// studio.Use(AuthMidHandler(authMid))
	// studio.Use(UserMidHandler())
	{
		terrain3d.GET("/", listTerrains3d)
		terrain3d.POST("/create/", createTerrain3d)
		terrain3d.GET("/info/:id/", getTerrain3d)
		terrain3d.POST("/info/:id/", updateTerrain3d)
		terrain3d.DELETE("/delete/:ids/", deleteTerrain3d)
	}

	symbols3d := r.Group("/symbols3d")
	// studio.Use(AuthMidHandler(authMid))
	// studio.Use(UserMidHandler())
	{
		symbols3d.GET("/", getSymbols3dList)
		symbols3d.POST("/", listSymbols3d)
		symbols3d.POST("/create/", createSymbol3d)
		symbols3d.GET("/info/:id/", getSymbols3d)
		symbols3d.POST("/info/:id/", updateSymbol3d)
		symbols3d.DELETE("/delete/:ids/", deleteSymbol3d)
	}

	styles3d := r.Group("/styles3d")
	// studio.Use(AuthMidHandler(authMid))
	// studio.Use(UserMidHandler())
	{
		styles3d.GET("/", listStyles3d)
		styles3d.POST("/create/", createStyle3d)
		styles3d.GET("/info/:id/", getStyle3d)
		styles3d.POST("/info/:id/", updateStyle3d)
		styles3d.DELETE("/delete/:ids/", deleteStyle3d)
	}

	//image 场景接口
	onlines := r.Group("/online")
	// studio.Use(AuthMidHandler(authMid))
	// studio.Use(UserMidHandler())
	{
		onlines.GET("/images/", listOnlineMaps)
		onlines.GET("/tilesets/", listTilesets3d)
		onlines.GET("/terrains/", listTerrains3d)
		onlines.GET("/symbols/", getSymbols3dList)
		onlines.POST("/symbols/", listSymbols3d)
		onlines.GET("/styles3d/", listStyles3d)
		onlines.POST("/styles3d/", createStyle3d)
		onlines.DELETE("/styles3d/:id/", deleteStyle3d)
	}

	//serve3d 其他接口
	other := r.Group("/other")
	// studio.Use(AuthMidHandler(authMid))
	// studio.Use(UserMidHandler())
	{
		other.GET("/geocoder", geoCoder)
	}

	//serve3d 其他接口
	proxy := r.Group("/dh3dts")
	// studio.Use(AuthMidHandler(authMid))
	// studio.Use(UserMidHandler())
	{
		proxy.GET("/*uri", tilesProxy)
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
		olmaps := []OnlineMap{}
		err := json.Unmarshal([]byte(initialOlMaps), &olmaps)
		if err != nil {
			log.Error(err)
		}
		for _, v := range olmaps {
			res := db.Create(v)
			if res.Error != nil {
				log.Error(res.Error)
			}
		}
		fmt.Printf("insert %d rows\n", len(olmaps))
	}
	{
		tiles := []Tileset3d{}
		err := json.Unmarshal([]byte(initialTilesets), &tiles)
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
		terrains := []Terrain3d{}
		err := json.Unmarshal([]byte(initialTerrains), &terrains)
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
	{
		symbols := []Symbol3d{}
		err := json.Unmarshal([]byte(initialSymbols), &symbols)
		if err != nil {
			log.Error(err)
		}
		for _, v := range symbols {
			res := db.Create(v)
			if res.Error != nil {
				log.Error(res.Error)
			}
		}
		fmt.Printf("insert %d rows\n", len(symbols))
	}
	{
		styles := []Style3d{}
		err := json.Unmarshal([]byte(initialStyles), &styles)
		if err != nil {
			log.Error(err)
		}
		for _, v := range styles {
			res := db.Create(v)
			if res.Error != nil {
				log.Error(res.Error)
			}
		}
		fmt.Printf("insert %d rows\n", len(styles))
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
