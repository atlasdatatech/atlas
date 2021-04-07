package main

import (
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	gs "github.com/hishamkaram/geoserver"
	"github.com/jinzhu/gorm"
	log "github.com/sirupsen/logrus"
)

//Geoserver Geoserver实例管理
type Geoserver struct {
	ID         string    `form:"id" json:"id" gorm:"primary_key"`
	Name       string    `form:"name" json:"name" binding:"required"`
	ServiceURL string    `form:"url" json:"url" binding:"required"`
	UserName   string    `form:"username" json:"username" binding:"required"`
	Password   string    `form:"password" json:"password" binding:"required"`
	Thumbnail  string    `form:"thumbnail" json:"thumbnail"`
	CreatedAt  time.Time `form:"-" json:"-"`
}

//**********************************************
//listGeoserverServices 获取Geoserver实例列表
func listGeoserverServices(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	var geoservers []Geoserver
	err := db.Find(&geoservers).Error
	if err != nil {
		resp.Fail(c, 5001)
		return
	}

	resp.DoneData(c, geoservers)
}

//getGeoserverService 获取Geoserver实例信息
func getGeoserverService(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	sid := c.Param("id")
	geoserver := &Geoserver{}
	if err := db.Where("id = ?", sid).First(&geoserver).Error; err != nil {
		if !gorm.IsRecordNotFoundError(err) {
			log.Error(err)
			resp.Fail(c, 5001)
		}
		resp.Fail(c, 4049)
		return
	}

	resp.DoneData(c, geoserver)
}

//createGeoserverService 注册Geoserver实例
func createGeoserverService(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	geoserver := &Geoserver{}
	err := c.Bind(geoserver)
	if err != nil {
		log.Error(err)
		resp.Fail(c, 4001)
		return
	}
	id := ShortID()
	//丢掉原来的id使用新的id
	geoserver.ID = id
	// insertUser
	err = db.Create(geoserver).Error
	if err != nil {
		log.Error(err)
		resp.Fail(c, 5001)
		return
	}
	//管理员创建地图后自己拥有,root不需要
	resp.DoneData(c, gin.H{
		"id": geoserver.ID,
	})
	return
}

//updateGeoserverService 更新Geoserver实例信息
func updateGeoserverService(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	id := c.Param("id")
	geoserver := &Geoserver{}
	err := c.Bind(geoserver)
	if err != nil {
		log.Error(err)
		resp.Fail(c, 4001)
		return
	}
	// 更新insertUser
	dbres := db.Model(Olmap{}).Where("id = ?", id).Update(geoserver)

	if dbres.Error != nil {
		log.Error(err)
		resp.Fail(c, 5001)
		return
	}

	resp.DoneData(c, gin.H{
		"affected": dbres.RowsAffected,
	})
	return
}

//deleteGeoserverService 删除Geoserver实例信息
func deleteGeoserverService(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}
	ids := c.Param("ids")
	sids := strings.Split(ids, ",")
	dbres := db.Where("id in (?)", sids).Delete(Geoserver{})
	if dbres.Error != nil {
		log.Error(dbres.Error)
		resp.Fail(c, 5001)
		return
	}
	resp.DoneData(c, gin.H{
		"affected": dbres.RowsAffected,
	})
}

//getGeoserverLayers 获取Geoserver图层列表
func getGeoserverLayers(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	sid := c.Param("id")
	geoserver := &Geoserver{}
	if err := db.Where("id = ?", sid).First(&geoserver).Error; err != nil {
		if !gorm.IsRecordNotFoundError(err) {
			log.Error(err)
			resp.Fail(c, 5001)
		}
		resp.Fail(c, 4049)
		return
	}

	gsCatalog := gs.GetCatalog(geoserver.ServiceURL, geoserver.UserName, geoserver.Password)
	ls, err := gsCatalog.GetLayers("")
	if err != nil {
		resp.Fail(c, 4049)
		return
	}
	resp.DoneData(c, ls)
}

//getGWCLayers 获取Geoserver实例信息
func getGWCLayers(c *gin.Context) {
	resp := NewResp()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	if uid == "" {
		uid = ATLAS
	}

	sid := c.Param("id")
	geoserver := &Geoserver{}
	if err := db.Where("id = ?", sid).First(&geoserver).Error; err != nil {
		if !gorm.IsRecordNotFoundError(err) {
			log.Error(err)
			resp.Fail(c, 5001)
		}
		resp.Fail(c, 4049)
		return
	}

	gsCatalog := gs.GetCatalog(geoserver.ServiceURL, geoserver.UserName, geoserver.Password)
	ls, err := gsCatalog.GetLayers("")
	if err != nil {
		resp.Fail(c, 4049)
		return
	}
	resp.DoneData(c, ls)
}
