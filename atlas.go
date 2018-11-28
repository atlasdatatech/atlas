package main

import (
	"fmt"

	log "github.com/sirupsen/logrus"
	"github.com/teris-io/shortid"
	"golang.org/x/crypto/bcrypt"

	jwt "github.com/appleboy/gin-jwt"
	"github.com/casbin/gorm-adapter"

	"github.com/casbin/casbin"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/contrib/static"
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

	pgConnInfo := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable", cfgV.GetString("db.host"), cfgV.GetString("db.port"), cfgV.GetString("db.user"), cfgV.GetString("db.password"), cfgV.GetString("db.name"))
	log.Info(pgConnInfo)
	pg, err := gorm.Open("postgres", pgConnInfo)
	if err != nil {
		log.Fatal("gorm pg Error:" + err.Error())
	} else {
		log.Info("Successfully connected!")
		pg.AutoMigrate(&User{}, &Attempt{}, &Role{}, &Map{}, &Dataset{})
		//业务数据表
		pg.AutoMigrate(&Bank{}, &Saving{}, &Other{}, &Poi{}, &M1{}, &M2{}, &M3{}, &M4{}, &M5{})
		db = pg
	}
	defer pg.Close()

	//Init casbin
	casbinAdapter := gormadapter.NewAdapter("postgres", pgConnInfo, true)
	casEnf = casbin.NewEnforcer(cfgV.GetString("casbin.config"), casbinAdapter)

	authMid, err = jwt.New(JWTMiddleware())
	if err != nil {
		log.Fatal("JWT Error:" + err.Error())
	}

	initUserRole("root", Role{ID: "super_group", Name: "超级管理员"})
	initUserRole("admin", Role{ID: "admin_group", Name: "管理员"})
	initUserRole("user", Role{ID: "user_group", Name: "普通用户"})

	createPaths("pub")

	if ss, err := LoadServiceSet(); err != nil {
		log.Errorf("loading public service set error: %s", err.Error())
	} else {
		pubSet = ss
		log.Infof("load public service set successed!")
	}

	r := gin.Default()

	config := cors.DefaultConfig()
	// config.AllowAllOrigins = true
	config.AllowOrigins = []string{"*"}
	config.AllowWildcard = true
	config.AllowCredentials = true
	r.Use(cors.New(config))

	r.Use(static.Serve("/", static.LocalFile("./public", true)))

	// staticsHome := cfgV.GetString("assets.statics")
	// r.Static("/statics", staticsHome)
	// templatesPath := filepath.Join(staticsHome, "/templates/*")
	// r.LoadHTMLGlob(templatesPath)

	bindRoutes(r) // --> cmd/go-getting-started/routers.go

	r.Run(":" + cfgV.GetString("port"))

}

func bindRoutes(r *gin.Engine) {

	//tmp
	r.POST("/test/", test)

	r.GET("/", index)
	r.GET("/login/", renderLogin)
	r.POST("/login/", login)

	//users
	user := r.Group("/users")
	user.Use(authMid.MiddlewareFunc())
	user.Use(NewAuthorizer(casEnf))
	{
		//authn > users
		user.GET("/", listUsers)
		user.POST("/", createUser)
		user.GET("/:id/", getUser)
		user.POST("/:id/", updateUser)
		user.POST("/:id/del/", deleteUser)
		user.GET("/:id/refresh/", jwtRefresh)
		user.POST("/:id/password/", changePassword)

		user.GET("/:id/roles/", getUserRoles)        //该用户拥有哪些角色
		user.POST("/:id/roles/", addUserRole)        //添加用户角色
		user.POST("/:id/roles/del/", deleteUserRole) //删除用户角色

		user.GET("/:id/maps/", getUserMaps) //该用户拥有哪些权限（含资源与操作）
		user.POST("/:id/maps/", addUserMap)
		user.POST("/:id/maps/del/", deleteUserMap)
	}
	//roles
	role := r.Group("/roles")
	role.Use(authMid.MiddlewareFunc())
	role.Use(NewAuthorizer(casEnf))
	{
		//authn > roles
		role.GET("/", listRoles)
		role.POST("/", createRole)
		role.POST("/:id/del/", deleteRole)
		role.GET("/:id/users/", getRoleUsers) //该角色包含哪些用户

		role.GET("/:id/maps/", getRoleMaps) //该用户拥有哪些权限（含资源与操作）
		role.POST("/:id/maps/", addRoleMap)
		role.POST("/:id/maps/del/", deleteRoleMap)
		//authn > assets
	}
	//account
	account := r.Group("/account")
	account.Use(authMid.MiddlewareFunc())
	{
		account.GET("/index/", renderAccount)
		account.GET("/", getUser)
		account.GET("/logout/", logout)
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
		maproute.POST("/", createMap)
		maproute.POST("/:id/", updInsetMap)
		maproute.POST("/:id/del/", deleteMap)
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
		studio.GET("/datasets/upload/", renderDatasetsUpload)
	}

	styles := r.Group("/styles")
	// styles.Use(authMid.MiddlewareFunc())
	{
		// > styles
		styles.GET("/", listStyles)
		styles.POST("/", uploadStyle)
		styles.GET("/:sid", getStyle)              //style.json
		styles.GET("/:sid/", viewStyle)            //view map style
		styles.POST("/:sid/", upSaveStyle)         //updateStyle
		styles.GET("/:sid/sprite:fmt", getSprite)  //style.json
		styles.POST("/:sid/sprite/", uploadSprite) //style.json
	}
	fonts := r.Group("/fonts")
	// fonts.Use(authMid.MiddlewareFunc())
	{
		// > fonts
		fonts.GET("/", listFonts)                  //get font
		fonts.GET("/:fontstack/:range", getGlyphs) //get glyph pbfs
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
		datasets.GET("/", listDatasets)
		datasets.GET("/:name/", getDatasetInfo)
		datasets.POST("/:name/distinct/", getDistinctValues)
		datasets.GET("/:name/geojson/", getGeojson)
		datasets.POST("/:name/import/", importFiles)
		datasets.POST("/:name/query/", queryGeojson)
		datasets.POST("/:name/cube/", cubeQuery)
		datasets.POST("/:name/common/", queryExec)
		datasets.GET("/:name/business/", queryBusiness)
		datasets.POST("/:name/buffers/", queryBuffers)
		datasets.GET("/:name/buffers/", getBuffers)
	}

	//route not found
	// router.NoRoute(renderStatus404)
}

func initUserRole(name string, role Role) {
	password := "1234"
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
	user.Role = []string{role.ID}
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
	if err := db.Create(&role).Error; err != nil {
		log.Errorf("super role create error")
		return
	}
	casEnf.AddGroupingPolicy(name, role.ID)
}
