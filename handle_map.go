package atlas

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"

	"github.com/gin-gonic/gin"
	"github.com/jinzhu/gorm"
	"github.com/teris-io/shortid"
)

func getMapPerms(c *gin.Context) {
	res := NewRes()
	mid := c.Param("id")
	if code := checkMap(mid); code != 200 {
		res.Fail(c, code)
		return
	}

	uperms := casEnf.GetFilteredPolicy(1, mid)

	var pers []MapPerm
	for _, perm := range uperms {
		m := &Map{}
		db.Where("id = ?", perm[1]).First(&m)
		p := MapPerm{
			ID:      perm[0],
			MapID:   perm[1],
			MapName: m.Title,
			Action:  perm[2],
		}
		pers = append(pers, p)
	}
	res.DoneData(c, pers)
}

func listMaps(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	var maps []Map
	if id == "root" {
		db.Select("id,title,summary,user,thumbnail,created_at,updated_at").Find(&maps)
		for i := 0; i < len(maps); i++ {
			maps[i].Action = "EDIT"
		}
		res.DoneData(c, maps)
		return
	}

	uperms := casEnf.GetPermissionsForUser(id)
	roles := casEnf.GetRolesForUser(id)
	for _, role := range roles {
		rperms := casEnf.GetPermissionsForUser(role)
		uperms = append(uperms, rperms...)
	}
	mapids := make(map[string]string)
	for _, p := range uperms {
		if len(p) == 3 {
			mapids[p[1]] = p[2]
		}
	}
	var ids []string
	for k := range mapids {
		ids = append(ids, k)
	}
	db.Select("id,title,summary,user,thumbnail,created_at,updated_at").Where("id in (?)", ids).Find(&maps)

	//添加每个map对应的该用户的权限
	for i := 0; i < len(maps); i++ {
		maps[i].Action = mapids[maps[i].ID]
	}

	res.DoneData(c, maps)
	return
}

func getMap(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	mid := c.Param("id")
	if mid == "" {
		res.Fail(c, 4001)
		return
	}
	if !casEnf.Enforce(id, mid, "(READ)|(EDIT)") {
		res.Fail(c, 403)
		return
	}
	m := &Map{}
	if err := db.Where("id = ?", mid).First(&m).Error; err != nil {
		if !gorm.IsRecordNotFoundError(err) {
			log.Error(err)
			res.Fail(c, 5001)
		}
		res.Fail(c, 4043)
		return
	}
	res.DoneData(c, m.toBind())
}

func createMap(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	group := viper.GetString("user.group")
	if id == "root" || casEnf.HasRoleForUser(id, group) {
		body := &MapBind{}
		err := c.Bind(&body)
		if err != nil {
			log.Error(err)
			res.Fail(c, 4001)
			return
		}
		mm := body.toMap()
		mm.ID, _ = shortid.Generate()
		mm.User = id
		if mm.Action == "" {
			mm.Action = "(READ)|(EDIT)"
		}
		// insertUser
		err = db.Create(mm).Error
		if err != nil {
			log.Error(err)
			res.Fail(c, 5001)
			return
		}
		//管理员创建地图后自己拥有,root不需要
		if id != "root" {
			casEnf.AddPolicy(mm.User, mm.ID, mm.Action)
		}
		res.DoneData(c, gin.H{
			"id": mm.ID,
		})
		return
	}
	res.Fail(c, 403)
	return
}

func updInsertMap(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	mid := c.Param("id")
	if mid == "" {
		res.Fail(c, 4001)
		return
	}
	if !casEnf.Enforce(id, mid, "EDIT") {
		res.Fail(c, 403)
		return
	}
	body := &MapBind{}
	err := c.Bind(&body)
	if err != nil {
		log.Error(err)
		res.Fail(c, 4001)
		return
	}
	mm := body.toMap()
	err = db.Model(&Map{}).Where("id = ?", mid).First(&Map{}).Error
	if err != nil {
		if gorm.IsRecordNotFoundError(err) {
			mm.ID = mid
			err = db.Create(&mm).Error
			if err != nil {
				log.Error(err)
				res.Fail(c, 5001)
				return
			}
			res.Done(c, "")
			return
		}
		log.Error(err)
		res.Fail(c, 5001)
		return
	}
	err = db.Model(&Map{}).Where("id = ?", mid).Update(mm).Error
	if err != nil {
		log.Error(err)
		res.Fail(c, 5001)
		return
	}
	res.Done(c, "")
}

func deleteMap(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	mid := c.Param("id")
	if mid == "" {
		res.Fail(c, 4001)
		return
	}
	if !casEnf.Enforce(id, mid, "EDIT") {
		res.Fail(c, 403)
		return
	}
	if code := checkMap(mid); code != 200 {
		res.Fail(c, code)
		return
	}
	casEnf.RemoveFilteredPolicy(1, mid)
	err := db.Where("id = ?", mid).Delete(&Map{}).Error
	if err != nil {
		log.Errorf("deleteMap, delete map : %s; mapid: %s", err, mid)
		res.Fail(c, 5001)
		return
	}
	res.Done(c, "")
}

func exportMap(c *gin.Context) {
	res := NewRes()
	id := c.Param("id")
	if id == "" {
		res.FailMsg(c, "map id can not null ~")
		return
	}
	dbmap := Map{}
	if err := db.Where("id = ?", id).First(&dbmap).Error; err != nil {
		if !gorm.IsRecordNotFoundError(err) {
			log.Error(err)
			res.Fail(c, 5001)
			return
		}
		res.FailMsg(c, "map id not found ~")
		return
	}
	maps := []*MapBind{dbmap.toBind()}
	data, _ := json.Marshal(maps)
	yy, mm, dd := time.Now().Date()
	h, m, s := time.Now().Clock()
	filename := fmt.Sprintf(`%s_maps_%d_%d_%d_%d_%d_%d.json`, id, yy, mm, dd, h, m, s)
	reader := bytes.NewReader(data)
	contentLength := int64(len(data))
	contentType := "application/json"
	extraHeaders := map[string]string{
		"Content-Disposition": fmt.Sprintf(`attachment; filename="%s"`, filename),
	}
	c.DataFromReader(http.StatusOK, contentLength, contentType, reader, extraHeaders)
}

func exportMaps(c *gin.Context) {
	id := c.GetString(identityKey)
	var maps []Map

	if id == "root" {
		db.Find(&maps)
		for i := 0; i < len(maps); i++ {
			maps[i].Action = "EDIT"
		}
	} else {
		uperms := casEnf.GetPermissionsForUser(id)
		roles := casEnf.GetRolesForUser(id)
		for _, role := range roles {
			rperms := casEnf.GetPermissionsForUser(role)
			uperms = append(uperms, rperms...)
		}
		mapids := make(map[string]string)
		for _, p := range uperms {
			if len(p) == 3 {
				mapids[p[1]] = p[2]
			}
		}
		var ids []string
		for k := range mapids {
			ids = append(ids, k)
		}
		db.Where("id in (?)", ids).Find(&maps)

		//添加每个map对应的该用户的权限
		for i := 0; i < len(maps); i++ {
			maps[i].Action = mapids[maps[i].ID]
		}
	}

	var bindMaps []*MapBind
	for _, m := range maps {
		bindMaps = append(bindMaps, m.toBind())
	}
	data, _ := json.Marshal(bindMaps)
	yy, mm, dd := time.Now().Date()
	h, m, s := time.Now().Clock()
	filename := fmt.Sprintf(`%s_maps_%d_%d_%d_%d_%d_%d.json`, id, yy, mm, dd, h, m, s)
	// c.Writer.Header().Set("Content-Type", "application/json")
	// c.Writer.Header().Set("Content-Encoding", "deflate")
	reader := bytes.NewReader(data)
	contentLength := int64(len(data))
	contentType := "application/json"
	extraHeaders := map[string]string{
		"Content-Disposition": fmt.Sprintf(`attachment; filename="%s"`, filename),
	}
	c.DataFromReader(http.StatusOK, contentLength, contentType, reader, extraHeaders)
}

func importMaps(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	group := viper.GetString("user.group")
	if id == "root" || casEnf.HasRoleForUser(id, group) {
		file, err := c.FormFile("file")
		if err != nil {
			res.Fail(c, 4046)
			return
		}

		filename := file.Filename
		// ext := filepath.Ext(filename)
		// if !strings.EqualFold(ext, ".json") {
		// }
		f, err := file.Open()
		if err != nil {
			log.Errorf(`read map file error: %s; file: %s`, err, filename)
			res.Fail(c, 5003)
			return
		}
		defer f.Close()
		buf := make([]byte, file.Size)
		f.Read(buf)
		var maps []MapBind
		err = json.Unmarshal(buf, &maps)
		if err != nil {
			log.Errorf(`map file format error: %s; file: %s`, err, filename)
			res.Fail(c, 5003)
			return
		}

		var insertCnt, updateCnt, failedCnt int
		for _, m := range maps {
			mm := m.toMap()
			err = db.Model(&Map{}).Where("id = ?", mm.ID).First(&Map{}).Error
			if err != nil {
				if gorm.IsRecordNotFoundError(err) {
					mm.User = id
					mm.Action = "(READ)|(EDIT)"
					casEnf.AddPolicy(mm.User, mm.ID, mm.Action)
					err = db.Create(&mm).Error
					if err != nil {
						log.Error(err)
						failedCnt++
						continue
					}
					insertCnt++
					continue
				}
				log.Error(err)
				failedCnt++
				continue
			}
			err = db.Model(&Map{}).Where("id = ?", mm.ID).Update(mm).Error
			if err != nil {
				log.Error(err)
				failedCnt++
				continue
			}
			updateCnt++
		}
		res.DoneData(c, gin.H{
			"insert": insertCnt,
			"update": updateCnt,
			"failed": failedCnt,
		})
		return
	}
	res.Fail(c, 403)
}
