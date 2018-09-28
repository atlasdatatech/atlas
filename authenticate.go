package main

import (
	"net/http"
	"time"

	jwt "github.com/appleboy/gin-jwt"
	"github.com/gin-gonic/gin"
	"github.com/jinzhu/gorm"
)

func payload(data interface{}) jwt.MapClaims {
	if v, ok := data.(*User); ok {
		return jwt.MapClaims{
			jwt.IdentityKey: v.ID,
		}
	}
	return jwt.MapClaims{}
}
func identity(c *gin.Context) interface{} {
	claims := jwt.ExtractClaims(c)
	return &User{
		ID: claims[jwt.IdentityKey].(string),
	}
}

//定义一个回调函数，用来决断用户id和密码是否有效
func authenticator(c *gin.Context) (interface{}, error) {
	//这里的通过从数据库中查询来判断是否为现存用户，生产环境下一般都会使用数据库来存储账号信息，进行检验和判断
	// user := User{} //创建一个临时的存放空间
	//如果这条记录存在的的情况下
	return nil, nil
	//否则返回失败
	// return nil, jwt.ErrFailedAuthentication
}

//定义一个回调函数，用来决断用户在认证成功的前提下，是否有权限对资源进行访问
func authorizator(user interface{}, c *gin.Context) bool {
	if v, ok := user.(string); ok {
		//如果可以正常取出 user 的值，就使用 casbin 来验证一下是否具备资源的访问权限
		return casbinEnforcer.Enforce(v, c.Request.URL.String(), c.Request.Method)
	}
	//默认策略是不允许
	return false
}

//定义一个函数用来处理，认证不成功的情况
func unauthorized(c *gin.Context, code int, message string) {
	c.JSON(code, gin.H{
		"code":    code,
		"message": message,
	})
}

//JWTMiddleware 定义JWT中间件，从相应的配置文件读取默认值
func JWTMiddleware() *jwt.GinJWTMiddleware {
	return &jwt.GinJWTMiddleware{
		//Realm name to display to the user. Required.
		//必要项，显示给用户看的域
		Realm: cfgV.GetString("jwt.realm"),
		//Secret key used for signing. Required.
		//用来进行签名的密钥，就是加盐用的
		Key: []byte(cfgV.GetString("jwt.key")),
		//Duration that a jwt token is valid. Optional, defaults to one hour
		//JWT 的有效时间，默认为30天
		Timeout: cfgV.GetDuration("jwt.timeOut"),
		// This field allows clients to refresh their token until MaxRefresh has passed.
		// Note that clients can refresh their token in the last moment of MaxRefresh.
		// This means that the maximum validity timespan for a token is MaxRefresh + Timeout.
		// Optional, defaults to 0 meaning not refreshable.
		//最长的刷新时间，用来给客户端自己刷新 token 用的，设置为3个月
		MaxRefresh:  cfgV.GetDuration("jwt.timeMax"),
		IdentityKey: cfgV.GetString("jwt.identityKey"),
		PayloadFunc: payload,
		// Callback function that should perform the authentication of the user based on userID and
		// password. Must return true on success, false on failure. Required.
		// Option return user data, if so, user data will be stored in Claim Array.
		//必要项, 这个函数用来判断 User 信息是否合法，如果合法就反馈 true，否则就是 false, 认证的逻辑就在这里
		Authenticator: authenticator,
		// Callback function that should perform the authorization of the authenticated user. Called
		// only after an authentication success. Must return true on success, false on failure.
		// Optional, default to success
		//可选项，用来在 Authenticator 认证成功的基础上进一步的检验用户是否有权限，默认为 success
		Authorizator: authorizator,
		// User can define own Unauthorized func.
		//可以用来息定义如果认证不成功的的处理函数
		// TokenLookup is a string in the form of "<source>:<name>" that is used
		// to extract token from the request.
		// Optional. Default value "header:Authorization".
		// Possible values:
		// - "header:<name>"
		// - "query:<name>"
		// - "cookie:<name>"
		//这个变量定义了从请求中解析 token 的位置和格式
		TokenLookup: cfgV.GetString("jwt.lookup"),
		// TokenLookup: "query:token",
		// TokenLookup: "cookie:token",
		// TokenHeadName is a string in the header. Default value is "Bearer"
		//TokenHeadName 是一个头部信息中的字符串
		TokenHeadName: cfgV.GetString("jwt.headName"),
		// TimeFunc provides the current time. You can override it to use another time value. This is useful for testing or if your server uses a different time zone than your tokens.
		//这个指定了提供当前时间的函数，也可以自定义
		TimeFunc: time.Now,
	}
}

func ensureAuthenticated(c *gin.Context) {
	isAuthenticated, _ := c.Get("isAuthenticated")
	if is, ok := isAuthenticated.(bool); ok && is {
		c.Next()
	} else {
		c.Abort()
		c.Redirect(http.StatusFound, "/login/")
	}
}

func getUser(c *gin.Context) (user *User) {
	if _user, ok := c.Get("User"); ok {
		user, ok = _user.(*User)
		if !ok {
			FATAL("not authorised")
		}
	} else {
		FATAL("not authorised")
	}
	return
}

func getAccount(c *gin.Context) (account *Account) {
	if _account, ok := c.Get("Account"); ok {
		account, ok = _account.(*Account)
		if !ok {
			FATAL("account wasn't found")
		}
	} else {
		FATAL("account wasn't found")
	}
	return
}

func ensureAccount(c *gin.Context) {
	user := getUser(c)
	account := Account{}
	db.Where("id = ?", user.AccountID).First(&account)
	c.Set("Account", &account)
	if cfgV.GetBool("account.verification") {
		if account.Verification != "yes" {
			if yes := rVerificationURL.MatchString(c.Request.URL.Path); !yes {
				c.Redirect(http.StatusFound, "/account/verification/")
				return
			}
		}
	}
	c.Next()
	return
}

func IsAuthenticated(c *gin.Context) {
	c.Set("isAuthenticated", false)
	// user := identity(c)
	user := User{}
	if err := db.Where("id = ?", user.ID).First(&user).Error; err != nil {
		if gorm.IsRecordNotFoundError(err) {
		} else {
			FATAL(err)
		}
	}
	if len(user.Name) > 0 {
		c.Set("Logined", true)
		c.Set("isAuthenticated", true)
		c.Set("UserName", user.Name)
		c.Set("User", &user)
	}

	c.Next()
}
