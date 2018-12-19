package main

import (
	"bytes"
	"io/ioutil"

	log "github.com/sirupsen/logrus"

	"github.com/spf13/viper"
)

//InitConf 用来设定初始配置
//这个函数接收一个 Viper 的指针，然后对这个 Viper 结构进行配置
func InitConf(v *viper.Viper) {
	//使用 toml 的格式配置文件
	v.SetConfigType("toml")
	buf, err := ioutil.ReadFile("atlas.toml")
	if err != nil {
		log.Fatal("read config file error:" + err.Error())
		return
	}
	err = v.ReadConfig(bytes.NewBuffer(buf))
	if err != nil {
		log.Fatal("config file has error:" + err.Error())
		return
	}
	//配置默认值，如果配置内容中没有指定，就使用以下值来作为配置值，给定默认值是一个让程序更健壮的办法
	v.SetDefault("app.port", "8080")

	v.SetDefault("jwt.realm", "atlasmap")
	v.SetDefault("jwt.key", "salta-atad-6221")
	v.SetDefault("jwt.timeOut", "720h")
	v.SetDefault("jwt.timeMax", "2160h")
	v.SetDefault("jwt.identityKey", "name")
	v.SetDefault("jwt.lookup", "header:Authorization, query:token, cookie:Token")
	v.SetDefault("jwt.headName", "Bearer")

	v.SetDefault("attempts.ip", 99)
	v.SetDefault("attempts.user", 9)
	v.SetDefault("attempts.expiration", "-5m")

	v.SetDefault("db.host", "127.0.0.1")
	v.SetDefault("db.port", "5432")
	v.SetDefault("db.user", "postgres")
	v.SetDefault("db.password", "postgres")
	v.SetDefault("db.name", "postgres")

	v.SetDefault("casbin.config", "./auth.conf")

	v.SetDefault("assets.statics", "assets/statics/")
	v.SetDefault("assets.styles", "assets/styles/")
	v.SetDefault("assets.fonts", "assets/fonts/")
	v.SetDefault("assets.tilesets", "assets/tilesets/")
	v.SetDefault("assets.datasets", "assets/datasets/")

}
