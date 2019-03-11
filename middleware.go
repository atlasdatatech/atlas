package main

import (
	"net/http"
	"time"

	"github.com/casbin/casbin"
	"github.com/didip/tollbooth"
	"github.com/didip/tollbooth/limiter"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// AuthMidHandler makes  the Middleware interface.
func AuthMidHandler(mw *JWTMiddleware) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, err := mw.GetClaimsFromJWT(c)
		if err != nil {
			log.Errorf("get token error,%s", err)
			// c.Header("WWW-Authenticate", "JWT realm="+mw.Realm)
			if !mw.DisabledAbort {
				c.JSON(http.StatusUnauthorized, gin.H{
					"code": 401,
					"msg":  "sign in first",
				})
				c.Abort()
			}
			return
		}
		if int64(claims["exp"].(float64)) < mw.TimeFunc().Unix() {
			//找到但是过期了
			tokenString, expire, err := mw.RefreshToken(c)
			if err != nil {
				// c.Header("WWW-Authenticate", "JWT realm="+mw.Realm)
				if !mw.DisabledAbort {
					c.JSON(http.StatusUnauthorized, gin.H{
						"code": 401,
						"msg":  ErrExpiredToken.Error(),
					})
					c.Abort()
				}
				return
			}

			// set cookie
			if mw.SendCookie {
				maxage := int(expire.Unix() - time.Now().Unix())
				c.SetCookie(
					"token",
					tokenString,
					maxage,
					"/",
					mw.CookieDomain,
					mw.SecureCookie,
					mw.CookieHTTPOnly,
				)
			}

		}
		//成功验证
		c.Set("PAYLOAD", claims)
		identity := claims[identityKey]
		if identity != nil {
			c.Set(identityKey, identity)
		}
		c.Next()
	}
}

// AccessMidHandler makes  the Middleware interface.
func AccessMidHandler(mw *JWTMiddleware) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := c.GetString(identityKey)
		//完成认证,则放行
		if uid == "" {
			//否则,获取query token
			claims, err := mw.GetClaimsFromJWT(c)
			if err != nil {
				//没找到
				// c.Header("WWW-Authenticate", "JWT realm="+mw.Realm)
				if !mw.DisabledAbort {
					c.JSON(http.StatusUnauthorized, gin.H{
						"code": 401,
						"msg":  "sign in first or has a access token",
					})
					c.Abort()
				}
				return
			}
			if int64(claims["exp"].(float64)) < mw.TimeFunc().Unix() {
				//找到但是过期了
				// c.Header("WWW-Authenticate", "JWT realm="+mw.Realm)
				if !mw.DisabledAbort {
					c.JSON(http.StatusUnauthorized, gin.H{
						"code": 401,
						"msg":  "access token expired",
					})
					c.Abort()
				}
				return
			}
			//成功验证
			who := claims[userKey]
			if who == nil {
				// c.Header("WWW-Authenticate", "JWT realm="+mw.Realm)
				if !mw.DisabledAbort {
					c.JSON(http.StatusUnauthorized, gin.H{
						"msg": "access token error",
					})
					c.Abort()
				}
				return
			}
			//成功验证
			c.Set("PAYLOAD", claims)
			c.Set(userKey, who)
			c.Next()
		}
	}
}

//AdminMidHandler returns the authorizer, uses a Casbin enforcer as input
func AdminMidHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := c.GetString(identityKey)
		if uid == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code": 401,
				"msg":  "sign in first",
			})
			c.Abort()
			return
		}
		if uid != ATLAS { //默认隐含ATLAS不为空
			c.JSON(http.StatusForbidden, gin.H{
				"code": 403,
				"msg":  "you don't have permission to access",
			})
			c.Abort()
		}
	}
}

//UserMidHandler returns the authorizer, uses a Casbin enforcer as input
func UserMidHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := c.GetString(identityKey)
		if uid == "" {
			//uid认证未通过
			c.JSON(http.StatusUnauthorized, gin.H{
				"code": 401,
				"msg":  "sign in first",
			})
			c.Abort()
			return
		}
		//uid认证通过,正常访问行为
		c.Next()
	}
}

//ResourceMidHandler returns the authorizer, uses a Casbin enforcer as input
func ResourceMidHandler(e *casbin.Enforcer) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := c.GetString(identityKey)
		user := c.GetString(userKey)
		owner := c.Param("user")
		//uid认证通过,
		if uid != "" {
			//且访问的是自己的资源，放行
			if uid == owner {
				c.Next()
				return
			}
			//且访问的是其他的资源，资源鉴权
			//暂时关掉所有通过jwt访问非自有资源权限，后续添加组织、域、资源组等概念后开放
			// c.JSON(http.StatusForbidden, gin.H{
			// 	"code": 403,
			// 	"msg":  "need add to group or has a access token",
			// })
			// c.Abort()
			// return
			if !e.Enforce(uid, c.Request.URL.Path, c.Request.Method) {
				c.JSON(http.StatusForbidden, gin.H{
					"code": 403,
					"msg":  "you don't have permission to access this resource",
				})
				c.Abort()
				return
			}
			return
		}
		//uid认证未通过	//且token认证未通过,返回
		if user == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code": 401,
				"msg":  "sign in first or has a access token",
			})
			c.Abort()
			return
		}
		//启动资源鉴权
		if !e.Enforce(user, c.Request.URL.Path, c.Request.Method) {
			c.JSON(http.StatusForbidden, gin.H{
				"code": 403,
				"msg":  "you don't have permission to access this resource",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

//LimitMidHandler Rate-limiter
func LimitMidHandler(lmt *limiter.Limiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		httpError := tollbooth.LimitByRequest(lmt, c.Writer, c.Request)
		if httpError != nil {
			c.Data(httpError.StatusCode, lmt.GetMessageContentType(), []byte(httpError.Message))
			c.Abort()
		} else {
			c.Next()
		}
	}
}
