package main

import (
	"fmt"
	"path/filepath"

	log "github.com/sirupsen/logrus"
	"github.com/teris-io/shortid"
	"golang.org/x/crypto/bcrypt"

	jwt "github.com/appleboy/gin-jwt"
	"github.com/casbin/gorm-adapter"

	"github.com/casbin/casbin"
	"github.com/gin-contrib/cors"
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
		pg.AutoMigrate(&User{}, &Attempt{}, &Role{}, &Map{})
		//业务数据表
		pg.AutoMigrate(&Bank{}, &Saving{}, &Other{}, &Basepoi{}, &Poi{}, &M1{}, &M2{}, &M3{}, &M4{})
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

	initSuperUser()
	createPaths("pub")

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

	//users
	user := r.Group("/users")
	user.Use(authMid.MiddlewareFunc())
	user.Use(NewAuthorizer(casEnf))
	{
		//authn > users
		user.GET("/", listUsers)
		user.POST("/", createUser)
		user.GET("/:id/", getUser)
		user.PUT("/:id/", updateUser)
		user.DELETE("/:id/", deleteUser)
		user.GET("/:id/refresh/", jwtRefresh)
		user.PUT("/:id/password/", changePassword)

		user.GET("/:id/roles/", getUserRoles)           //该用户拥有哪些角色
		user.POST("/:id/roles/:rid/", addUserRole)      //添加用户角色
		user.DELETE("/:id/roles/:rid/", deleteUserRole) //删除用户角色

		user.GET("/:id/maps/", getUserMaps) //该用户拥有哪些权限（含资源与操作）
		user.POST("/:id/maps/:mid/:action/", addUserMap)
		user.DELETE("/:id/maps/:mid/:action/", deleteUserMap)
	}
	//roles
	role := r.Group("/roles")
	role.Use(authMid.MiddlewareFunc())
	role.Use(NewAuthorizer(casEnf))
	{
		//authn > roles
		role.GET("/", listRoles)
		role.POST("/", createRole)
		role.DELETE("/:id/", deleteRole)
		role.GET("/:id/users/", getRoleUsers) //该角色包含哪些用户

		role.GET("/:id/maps/", getRoleMaps) //该用户拥有哪些权限（含资源与操作）
		role.POST("/:id/maps/:mid/:action/", addRoleMap)
		role.DELETE("/:id/maps/:mid/:action/", deleteRoleMap)
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
		account.PUT("/update/", updateUser)
		account.GET("/refresh/", jwtRefresh)
		account.GET("/password/", renderChangePassword)
		account.POST("/password/", changePassword)
	}
	//maproute
	maproute := r.Group("/maps")
	maproute.Use(authMid.MiddlewareFunc())
	role.Use(NewAuthorizer(casEnf))
	{
		// > map op
		maproute.GET("/", listMaps)
		maproute.GET("/:id/", getMap)
		maproute.POST("/", createMap)
		maproute.POST("/:id/", saveMap)
		maproute.PUT("/:id/", updateMap)
		maproute.DELETE("/:id/", deleteMap)
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
		styles.PUT("/:sid/", updateStyle)          //updateStyle
		styles.POST("/:sid/", saveStyle)           //updateStyle
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
		datasets.POST("/import/:name/", importDataset)
		datasets.POST("/query/:name/", queryDatasetGeojson)
		datasets.POST("/cube/:sql/", queryExec)
	}

	//route not found
	// router.NoRoute(renderStatus404)
}

func initSuperUser() {
	name := "root"
	password := "1234"
	phone := "13579246810"
	department := "system"
	role := Role{ID: "super", Name: "超级管理员"}
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
