package main

import (
	"bytes"

	log "github.com/sirupsen/logrus"

	"github.com/spf13/viper"
)

//InitConf 用来设定初始配置
//这个函数接收一个 Viper 的指针，然后对这个 Viper 结构进行配置
func InitConf(v *viper.Viper) {
	//使用 toml 的格式配置文件
	v.SetConfigType("toml")

	//定义一个 byte 的数组，用来存储配置
	//这种方式是直接把配置写到内存中
	//在测试环境下和配置比较少的情况下，可以直接使用这种方式来快速实现
	var tomlConf = []byte(`
	port="8080"

	[jwt]
		realm="atlasmap"
		key="salta-atad-6221"
		timeOut="720h"
		timeMax="2160h"
		identityKey="id"
		lookup="header:Authorization, query:token, cookie:JWTToken"
		headName="Bearer"

	[password]
		restExpiration = "24h"

	[account]
		verification = true

	[attempts]
		ip = 50
		user = 7
		expiration = "-5m"

	[db]
		host     = "127.0.0.1"
		port     = "5432"
		user     = "postgres"
		password = "postgres"
		name   = "atlas"

	[casbin]
		config = "./auth.conf"
		policy = "./auth.csv"

	[smtp]
		[smtp.from]
			name = "atlasmap"
			address = "atlasdatatech@gmail.com"

		[smtp.credentials]
			user = "atlasdatatech@gmail.com"
			password = "Atlas1226"
			host = "smtp.gmail.com"
			ssl = true

	[statics]
		home = "assets/statics/"
		templates = "assets/statics/templates/*"

	[fonts]
		home = "assets/fonts/"
		path = "assets/fonts/"

	[styles]
		home = "assets/styles/"
		path = "assets/styles/"

	[tilesets]
		home="assets/tilesets/"
		path = "E:/data/tilesets/server/"

	[datasets]
		home = "assets/datasets/"

	`)

	//用来从上面的byte数组中读取配置内容
	err := v.ReadConfig(bytes.NewBuffer(tomlConf))
	if err != nil {
		log.Fatal("config file has error:" + err.Error())
	}
	//配置默认值，如果配置内容中没有指定，就使用以下值来作为配置值，给定默认值是一个让程序更健壮的办法
	v.SetDefault("port", "8080")
	v.SetDefault("jwt.realm", "atlasmap")
	v.SetDefault("jwt.key", "salta-atad-6221")
	v.SetDefault("jwt.timeOut", "720h")
	v.SetDefault("jwt.timeMax", "2160h")
	v.SetDefault("jwt.identityKey", "name")
	v.SetDefault("jwt.lookup", "header:Authorization, query:token, cookie:JWTToken")
	v.SetDefault("jwt.headName", "Bearer")

	v.SetDefault("account.verification", true)

	v.SetDefault("attempts.ip", 99)
	v.SetDefault("attempts.user", 9)
	v.SetDefault("attempts.expiration", "-5m")

	v.SetDefault("db.host", "127.0.0.1")
	v.SetDefault("db.port", "5432")
	v.SetDefault("db.user", "postgres")
	v.SetDefault("db.password", "postgres")
	v.SetDefault("db.name", "test")

	v.SetDefault("casbin.config", "./auth.conf")
	v.SetDefault("casbin.policy", "./auth.csv")

	v.SetDefault("smtp.from.name", "atlasmap")
	v.SetDefault("smtp.from.address", "atlasdatatech@gmail.com")

	v.SetDefault("smtp.credentials.user", "atlasdatatech@gmail.com")
	v.SetDefault("smtp.credentials.password", "Atlas1226")
	v.SetDefault("smtp.credentials.host", "smtp.gmail.com")
	v.SetDefault("smtp.credentials.ssl", true)

	v.SetDefault("statics.home", "assets/statics/")
	v.SetDefault("statics.templates", "assets/statics/templates/*")

}
