package main

import (
	"flag"
	"fmt"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/didip/tollbooth"
	"github.com/didip/tollbooth/limiter"
	"github.com/go-spatial/geom"
	"github.com/go-spatial/tegola"
	"github.com/go-spatial/tegola/atlas"
	"github.com/go-spatial/tegola/cache"
	"github.com/go-spatial/tegola/config"
	"github.com/go-spatial/tegola/dict"
	"github.com/go-spatial/tegola/provider"
	"github.com/go-spatial/tegola/server"

	"github.com/shiena/ansicolor"
	log "github.com/sirupsen/logrus"
	"github.com/teris-io/shortid"
	"golang.org/x/crypto/bcrypt"

	nested "github.com/antonfisher/nested-logrus-formatter"
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
	//ATLAS 默认管理员用户名
	ATLAS = "atlas"
	//ADMIN 默认管理员组
	ADMIN = "admin@group"
	//USER 默认用户组
	USER        = "user@group"
	identityKey = "uid"
	userKey     = "id"
	//DISABLEACCESSTOKEN 不使用accesstoken
	DISABLEACCESSTOKEN = false
)

var (
	conf      config.Config
	db        *gorm.DB
	providers = make(map[string]provider.Tiler)
	casEnf    *casbin.Enforcer
	authMid   *JWTMiddleware
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
	viper.SetDefault("db.database", "postgres")
	viper.SetDefault("casbin.config", "./auth.conf")
	viper.SetDefault("statics", "statics/")

	viper.SetDefault("paths.styles", "styles")
	viper.SetDefault("paths.fonts", "fonts")
	viper.SetDefault("paths.tilesets", "tilesets")
	viper.SetDefault("paths.datasets", "datasets")
	viper.SetDefault("paths.uploads", "tmp")
}

//initDb 初始化数据库
func initDb() (*gorm.DB, error) {
	pgConnInfo := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		viper.GetString("db.host"), viper.GetString("db.port"), viper.GetString("db.user"), viper.GetString("db.password"), viper.GetString("db.database"))
	pg, err := gorm.Open("postgres", pgConnInfo)
	if err != nil {
		return nil, fmt.Errorf("init gorm pg error, details: %s", err)
	}
	log.Info("init gorm pg successfully")
	//gorm自动构建用户表
	pg.AutoMigrate(&User{}, &Role{}, &Attempt{})
	//gorm自动构建管理
	pg.AutoMigrate(&Map{}, &Style{}, &Font{}, &Tileset{}, &Dataset{}, &DataSource{}, &Task{})
	return pg, nil
}

//initProvider 初始化数据库驱动
func initProviders(provArr []dict.Dicter) (map[string]provider.Tiler, error) {
	providers := map[string]provider.Tiler{}
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

// Maps registers maps with with atlas
func initMaps(a *atlas.Atlas, maps []config.Map, providers map[string]provider.Tiler) error {

	// iterate our maps
	for _, m := range maps {
		newMap := atlas.NewWebMercatorMap(string(m.Name))
		newMap.Attribution = html.EscapeString(string(m.Attribution))

		// convert from env package
		centerArr := [3]float64{}
		for i, v := range m.Center {
			centerArr[i] = float64(v)
		}

		newMap.Center = centerArr

		if len(m.Bounds) == 4 {
			newMap.Bounds = geom.NewExtent(
				[2]float64{float64(m.Bounds[0]), float64(m.Bounds[1])},
				[2]float64{float64(m.Bounds[2]), float64(m.Bounds[3])},
			)
		}

		// iterate our layers
		for _, l := range m.Layers {
			// split our provider name (provider.layer) into [provider,layer]
			providerLayer := strings.Split(string(l.ProviderLayer), ".")

			// we're expecting two params in the provider layer definition
			if len(providerLayer) != 2 {
				return fmt.Errorf("invalid provider layer (%v) for map (%v)", l.ProviderLayer, m.Name)
			}

			// lookup our proivder
			provider, ok := providers[providerLayer[0]]
			if !ok {
				return fmt.Errorf("provider (%v) not defined", providerLayer[0])
			}

			// read the provider's layer names
			layerInfos, err := provider.Layers()
			if err != nil {
				return fmt.Errorf("error fetching layer info from provider (%v)", providerLayer[0])
			}

			// confirm our providerLayer name is registered
			var found bool
			var layerGeomType tegola.Geometry
			for i := range layerInfos {
				if layerInfos[i].Name() == providerLayer[1] {
					found = true

					// read the layerGeomType
					layerGeomType = layerInfos[i].GeomType()
					break
				}
			}
			if !found {
				return fmt.Errorf("map (%v) 'provider_layer' (%v) is not registered with provider (%v)", m.Name, l.ProviderLayer, providerLayer[0])
			}

			var defaultTags map[string]interface{}
			if l.DefaultTags != nil {
				var ok bool
				defaultTags, ok = l.DefaultTags.(map[string]interface{})
				if !ok {
					return fmt.Errorf("'default_tags' for 'provider_layer' (%v) should be a TOML table", l.ProviderLayer)
				}
			}

			var minZoom uint
			if l.MinZoom != nil {
				minZoom = uint(*l.MinZoom)
			}

			var maxZoom uint
			if l.MaxZoom != nil {
				maxZoom = uint(*l.MaxZoom)
			}

			// add our layer to our layers slice
			newMap.Layers = append(newMap.Layers, atlas.Layer{
				Name:              string(l.Name),
				ProviderLayerName: providerLayer[1],
				MinZoom:           minZoom,
				MaxZoom:           maxZoom,
				Provider:          provider,
				DefaultTags:       defaultTags,
				GeomType:          layerGeomType,
				DontSimplify:      bool(l.DontSimplify),
			})
		}

		a.AddMap(newMap)
	}

	return nil
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
	server.Headers = conf.Webserver.Headers
	// set tile buffer
	if conf.TileBuffer != nil {
		server.TileBuffer = float64(*conf.TileBuffer)
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

//initEnforcer 初始化资源访问控制
func initEnforcer() (*casbin.Enforcer, error) {
	pgConnInfo := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		viper.GetString("db.host"), viper.GetString("db.port"), viper.GetString("db.user"), viper.GetString("db.password"), viper.GetString("db.database"))
	casbinAdapter := gormadapter.NewAdapter("postgres", pgConnInfo, true)
	enforcer := casbin.NewEnforcer("./auth.conf", casbinAdapter)
	return enforcer, nil
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
	r.Use(gzip.Gzip(gzip.DefaultCompression))
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
	//x
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
	maproute.Use(AuthMidHandler(authMid))
	maproute.Use(AccessMidHandler())
	// maproute.Use(ResourceMidHandler(casEnf))
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
	styles.Use(AuthMidHandler(authMid))
	styles.Use(AccessMidHandler())
	// styles.Use(ResourceMidHandler(casEnf))
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

		styles.GET("/view/:id/", viewStyle)    //view map style
		styles.POST("/edit/:id/", updateStyle) //updateStyle
	}
	fonts := r.Group("/fonts")
	fonts.Use(AuthMidHandler(authMid))
	fonts.Use(AccessMidHandler())
	// fonts.Use(ResourceMidHandler(casEnf))
	{
		// > fonts
		fonts.GET("/", listFonts)                      //get font
		fonts.POST("/upload/", uploadFont)             //upload font
		fonts.POST("/delete/:fontstack/", deleteFonts) //delete font
		fonts.GET("/:fontstack/:range", getGlyphs)     //get glyph pbfs
	}

	tilesets := r.Group("/ts")
	tilesets.Use(AuthMidHandler(authMid))
	tilesets.Use(AccessMidHandler())
	// tilesets.Use(ResourceMidHandler(casEnf))
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
		tilesets.POST("/create/:id/", createTileset)
		tilesets.POST("/update/:id/", createTileset)
		tilesets.GET("/download/:id/", downloadTileset)
		tilesets.POST("/delete/:ids/", deleteTileset)
		tilesets.POST("/merge/:ids/", getTile)

		tilesets.GET("/view/:id/", viewTile) //view
	}

	datasets := r.Group("/datasets")
	datasets.Use(AuthMidHandler(authMid))
	datasets.Use(AccessMidHandler())
	// datasets.Use(ResourceMidHandler(casEnf))
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

		datasets.POST("/distinct/:id/", getDistinctValues)
		datasets.GET("/search/:id/", searchGeos)
		datasets.GET("/buffer/:id/", getBuffers)

		datasets.GET("/x/:id/", getTileLayerJSON)
		datasets.GET("/x/:id/:z/:x/:y", getTileLayer)
		datasets.POST("/x/:id/", createTileLayer)

	}
	tasks := r.Group("/tasks")
	tasks.Use(AuthMidHandler(authMid))
	tasks.Use(UserMidHandler())
	{
		tasks.GET("/", listTasks)
		tasks.GET("/info/:id/", taskQuery)
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

	{
		provArr := make([]dict.Dicter, len(conf.Providers))
		for i := range provArr {
			provArr[i] = conf.Providers[i]
		}
		providers, err = initProviders(provArr)
		if err != nil {
			log.Fatalf("could not register providers: %v", err)
		}
		// init our maps
		if err = initMaps(nil, conf.Maps, providers); err != nil {
			log.Fatalf("could not register maps: %v", err)
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

	casEnf, err = initEnforcer()
	if err != nil {
		log.Fatalf("init enforcer error: %s", err)
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
