package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/casbin/casbin"
	jwt "github.com/dgrijalva/jwt-go"
	"github.com/didip/tollbooth"
	"github.com/didip/tollbooth/limiter"
	"github.com/gin-gonic/gin"
)

//SA 签名算法
const SA = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9."

// AuthMidHandler makes  the Middleware interface.
func AuthMidHandler(mw *JWTMiddleware) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := c.GetString(userKey)
		if uid == "" {
			claims, err := mw.GetClaimsFromJWT(c)
			if err != nil {
				if !mw.DisabledAbort {
					c.JSON(http.StatusUnauthorized, gin.H{
						"code": 401,
						"msg":  fmt.Sprintf("sign in first, details: %s", err),
					})
					c.Abort()
				}
				return
			}
			if int64(claims["exp"].(float64)) < mw.TimeFunc().Unix() {
				//找到但是过期了
				tokenString, expire, err := mw.RefreshToken(c)
				if err != nil {
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
		}
	}
}

//ParseAccessTokenUnverified parse claims only
func ParseAccessTokenUnverified(c *gin.Context) (MapClaims, error) {
	tokenString, y := c.GetQuery("access_token")
	if !y {
		return nil, fmt.Errorf("no access_token")
	}
	// parse Claims
	claimBytes, err := jwt.DecodeSegment(tokenString)
	if err != nil {
		return nil, fmt.Errorf("decode base64url padding error")
	}
	dec := json.NewDecoder(bytes.NewBuffer(claimBytes))
	// JSON Decode.  Special case for map type to avoid weird pointer behavior
	var claims MapClaims
	err = dec.Decode(&claims)
	// Handle decode error
	if err != nil {
		return nil, fmt.Errorf("decode claims error")
	}
	return claims, nil
}

// ParseAccessToken parse jwt token
func ParseAccessToken(c *gin.Context) (MapClaims, error) {
	tokenString, y := c.GetQuery("access_token")
	if !y {
		return nil, fmt.Errorf("no access_token")
	}
	token, err := jwt.Parse(SA+tokenString, func(t *jwt.Token) (interface{}, error) {
		return []byte("atlas-cloud-access-token"), nil
	})
	if err != nil {
		return nil, err
	}
	claims := MapClaims{}
	for key, value := range token.Claims.(jwt.MapClaims) {
		claims[key] = value
	}
	return claims, nil
}

// AccessTokenGenerator method that clients can use to get a jwt token.
func AccessTokenGenerator(claims MapClaims) (string, error) {
	sa := "HS256"
	jwtClaims := jwt.MapClaims{}
	for k, v := range claims {
		jwtClaims[k] = v
	}
	token := jwt.NewWithClaims(jwt.GetSigningMethod(sa), jwtClaims)
	tokenString, err := token.SignedString([]byte("atlas-cloud-access-token"))
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(tokenString, SA), nil
}

// AccessMidHandler makes  the Middleware interface.
func AccessMidHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		//只处理成功
		// claims, err := ParseAccessTokenUnverified(c)
		claims, err := ParseAccessToken(c)
		if err == nil {
			who := claims[userKey]
			if who != nil {
				c.Set("ACCESSPAYLOAD", claims)
				c.Set(userKey, who)
				c.Next()
				//成功验证
			}
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
				"msg":  "sign in first, user id is nil",
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
		//uid认证通过,直接放行所有资源
		if uid != "" {
			c.Next()
			return
		}
		//uid认证未通过,且access token认证未通过,所有资源都拒绝
		if user == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code": 401,
				"msg":  "sign in first or has a access token, user id is nil",
			})
			c.Abort()
			return
		}
		resourceid := c.Param("id")
		//启动access token资源鉴权
		if !e.Enforce(user, resourceid, c.Request.Method) {
			c.JSON(http.StatusForbidden, gin.H{
				"code": 403,
				"msg":  "you don't have permission to access this resource, jwt && access_token pass",
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
