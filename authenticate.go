package main

import (
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"

	jwt "github.com/appleboy/gin-jwt"
	"github.com/gin-gonic/gin"
)

func payload(data interface{}) jwt.MapClaims {
	if user, ok := data.(*User); ok {
		return jwt.MapClaims{
			identityKey: user.Name,
		}
	}
	return jwt.MapClaims{}
}

//定义一个回调函数，用来决断用户id和密码是否有效.暂时弃用
func authenticator(c *gin.Context) (interface{}, error) {
	return nil, nil
}

//定义一个回调函数，用来决断用户在认证成功的前提下，是否有权限对资源进行访问
func authorizator(user interface{}, c *gin.Context) bool {
	if id, ok := user.(string); ok {
		//如果可以正常取出 user 的值，就使用 casbin 来验证一下是否具备资源的访问权限
		log.Debug(id, c.Request.URL.String(), c.Request.Method)
		return casEnf.Enforce(id, c.Request.URL.String(), c.Request.Method)
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

func loginResponse(c *gin.Context, code int, token string, t time.Time) {
	cookie, err := c.Cookie("Token")
	if err != nil {
		log.Error(err)
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    http.StatusOK,
		"token":   token,
		"expire":  t.Format(time.RFC3339),
		"message": "login successfully",
		"cookie":  cookie,
	})
}

func refreshResponse(c *gin.Context, code int, token string, t time.Time) {
	cookie, err := c.Cookie("Token")
	if err != nil {
		log.Error(err)
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    http.StatusOK,
		"token":   token,
		"expire":  t.Format(time.RFC3339),
		"message": "refresh successfully",
		"cookie":  cookie,
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
		IdentityKey: identityKey,
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
		Unauthorized: unauthorized,
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
		//设置Cookie
		SendCookie:      true,
		LoginResponse:   loginResponse,
		RefreshResponse: refreshResponse,
	}
}
