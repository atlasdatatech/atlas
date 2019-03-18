package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/render"
)

//listTilesets 获取服务集列表
func listTilesets(c *gin.Context) {
	res := NewRes()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	set := userSet.service(uid)
	if set == nil {
		log.Warnf(`listTilesets, %s's service not found ^^`, uid)
		res.Fail(c, 4043)
		return
	}

	var tss []Tileset
	tdb := db
	pub, y := c.GetQuery("public")
	if y && strings.ToLower(pub) == "true" {
		if casEnf.Enforce(uid, "list-atlas-ts", c.Request.Method) {
			tdb = tdb.Where("owner = ? and public = ? ", ATLAS, true)
		}
	} else {
		tdb = tdb.Where("owner = ?", uid)
	}
	kw, y := c.GetQuery("keyword")
	if y {
		tdb = tdb.Where("name ~ ?", kw)
	}
	order, y := c.GetQuery("order")
	if y {
		log.Info(order)
		tdb = tdb.Order(order)
	}
	start := 0
	rows := 10
	if offset, y := c.GetQuery("start"); y {
		rs, yr := c.GetQuery("rows") //limit count defaut 10
		if yr {
			ri, err := strconv.Atoi(rs)
			if err == nil {
				rows = ri
			}
		}
		start, _ = strconv.Atoi(offset)
		tdb = tdb.Offset(start).Limit(rows)
	}
	total := 0
	err := tdb.Find(&tss).Offset(0).Limit(-1).Count(&total).Error
	if err != nil {
		res.Fail(c, 5001)
		return
	}
	res.DoneData(c, gin.H{
		"keyword": kw,
		"order":   order,
		"start":   start,
		"rows":    rows,
		"total":   total,
		"list":    tss,
	})

	// var tss []*Tileset
	// set.T.Range(func(_, v interface{}) bool {
	// 	tss = append(tss, v.(*Tileset))
	// 	return true
	// })

	// if uid != ATLAS && "true" == c.Query("public") {
	// 	set := userSet.service(ATLAS)
	// 	if set != nil {
	// 		set.T.Range(func(_, v interface{}) bool {
	// 			ts, ok := v.(*Tileset)
	// 			if ok {
	// 				if ts.Public {
	// 					tss = append(tss, ts)
	// 				}
	// 			}
	// 			return true
	// 		})
	// 	}
	// }

	// res.DoneData(c, tss)
}

//getTilesetInfo 获取服务集信息
func getTilesetInfo(c *gin.Context) {
	res := NewRes()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	tid := c.Param("id")
	ts := userSet.tileset(uid, tid)
	if ts == nil {
		log.Warnf(`getTilesetInfo, %s's tileset (%s) not found ^^`, uid, tid)
		res.Fail(c, 4045)
		return
	}
	res.DoneData(c, ts)
}

//updateTilesetInfo 更新服务集信息
func updateTilesetInfo(c *gin.Context) {
	res := NewRes()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	tid := c.Param("id")
	ts := userSet.tileset(uid, tid)
	if ts == nil {
		log.Warnf(`getTilesetInfo, %s's tileset (%s) not found ^^`, uid, tid)
		res.Fail(c, 4045)
		return
	}
	err := c.Bind(ts)
	if err != nil {
		res.Fail(c, 4001)
		return
	}
	err = ts.Update()
	if err != nil {
		log.Errorf("updateStyleInfo, update %s's tileset (%s) info error, details: %s", uid, tid, err)
		res.FailErr(c, err)
		return
	}
	res.Done(c, "")
}

//uploadTileset 上传服务集
func uploadTileset(c *gin.Context) {
	res := NewRes()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	set := userSet.service(uid)
	if set == nil {
		log.Warnf(`uploadTileset, %s's service not found ^^`, uid)
		res.Fail(c, 4043)
		return
	}
	ds, err := saveSource(c)
	//加载Tileset
	ts, err := LoadTileset(ds)
	if err != nil {
		log.Errorf("uploadTileset, could not load tileset %s, details: %s", ds.Path, err)
		res.FailErr(c, err)
		return
	}
	//更新user
	ts.Owner = uid
	//入库
	err = ts.UpInsert()
	if err != nil {
		log.Errorf(`uploadTileset, upinsert tileset %s error, details: %s`, ts.ID, err)
	}

	//加载服务,todo 用户服务无需预加载
	err = ts.Service()
	if err != nil {
		log.Error(err)
		res.FailErr(c, err)
		return
	}
	set.T.Store(ts.ID, ts)
	res.DoneData(c, ts)
}

//replaceTileset 上传并替换服务集
func replaceTileset(c *gin.Context) {
	res := NewRes()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	tid := c.Param("id")
	ts := userSet.tileset(uid, tid)
	if ts == nil {
		log.Errorf(`replaceTileset, %s's tileset (%s) not found ^^`, uid, tid)
		res.Fail(c, 4045)
		return
	}

	ds, err := saveSource(c)
	//加载Tileset
	tileset, err := LoadTileset(ds)
	if err != nil {
		log.Errorf("replaceTileset, could not load tileset %s, details: %s", ds.Path, err)
		res.FailErr(c, err)
		return
	}
	tileset.ID = tid
	tileset.Owner = uid
	//加载服务,todo 用户服务无需预加载
	err = tileset.Service()
	if err != nil {
		log.Error(err)
		res.FailErr(c, err)
		return
	}
	set := userSet.service(uid)
	set.T.Store(tileset.ID, tileset)
	err = ts.Clean()
	if err != nil {
		log.Error(err)
		res.FailErr(c, err)
		return
	}

	//入库
	err = tileset.UpInsert()
	if err != nil {
		log.Errorf(`replaceTileset, upinsert tileser %s error, details: %s`, tileset.ID, err)
	}

	res.DoneData(c, tileset)
}

//publishTileset 上传并发布服务集
func publishTileset(c *gin.Context) {
	res := NewRes()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	set := userSet.service(uid)
	if set == nil {
		res.Fail(c, 4043)
		return
	}

	ds, err := saveSource(c)
	if err != nil {
		log.Warn(err)
		res.FailErr(c, err)
		return
	}
	dss, _ := loadZipSources(ds)
	taskid := ds.ID
	if tid := c.Param("id"); tid != "" {
		taskid = tid
	}
	task := &Task{
		ID:    taskid,
		Name:  ds.Name + "-发布",
		Owner: uid,
		Type:  TSIMPORT,
		Pipe:  make(chan struct{}),
	}
	//任务队列
	select {
	case taskQueue <- task:
	default:
		log.Warningf("task queue overflow, request refused...")
		res.FailMsg(c, "服务器繁忙,请稍后再试")
		return
	}
	taskSet.Store(task.ID, task)
	task.save()

	go func(task *Task, dss []*DataSource) {
		defer func() {
			task.Pipe <- struct{}{}
		}()
		s := time.Now()
		ts, err := sources2ts(task, dss)
		if err != nil {
			log.Error(err)
			task.Error = err.Error()
			return
		}
		//入库
		ts.Owner = task.Owner
		err = ts.UpInsert()
		if err != nil {
			log.Errorf(`publishTileset, upinsert tileset (%s) error, details: %s`, ts.ID, err)
			task.Error = err.Error()
		}
		//加载服务,todo 用户服务无需预加载
		err = ts.Service()
		if err != nil {
			log.Error(err)
			task.Error = err.Error()
			return
		}
		ts.atlasMark()
		log.Infof("load tilesets(%s), takes: %v", ts.ID, time.Since(s))
		oldts := userSet.tileset(task.Owner, task.ID)
		if oldts != nil {
			oldts.Clean()
		}
		set.T.Store(ts.ID, ts)
		task.Progress = 100
	}(task, dss)

	//退出队列,通知完成消息
	go func(task *Task) {
		<-task.Pipe
		<-taskQueue
		task.update()
	}(task)

	res.DoneData(c, task)
}

//publishTileset 上传并发布服务集
func rePublishTileset(c *gin.Context) {
	res := NewRes()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	tid := c.Param("id")
	ts := userSet.tileset(uid, tid)
	if ts == nil {
		res.Fail(c, 4045)
		return
	}
	publishTileset(c)
}

//createTileset 从数据集创建服务集
func createTileset(c *gin.Context) {
	res := NewRes()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	did := c.Param("id")
	dts := userSet.dataset(uid, did)
	if dts == nil {
		log.Warnf(`createTileset, %s's tileset (%s) not found ^^`, uid, did)
		res.Fail(c, 4045)
		return
	}
	path := filepath.Join(viper.GetString("paths.tilesets"), uid, dts.ID+MBTILESEXT)
	// download
	err := dts.CacheMBTiles(path)
	if err != nil {
		log.Errorf("createTileset, could not load tileset %s, details: %s", path, err)
		res.FailErr(c, err)
		return
	}
	ds := &DataSource{
		ID:    dts.ID,
		Name:  dts.Name,
		Owner: uid,
		Path:  path,
	}
	ts, err := LoadTileset(ds)
	if err != nil {
		log.Errorf("createTileset, could not load tileset %s, details: %s", path, err)
		res.FailErr(c, err)
		return
	}
	ts.ID = did
	ts.Owner = uid
	//入库
	err = ts.UpInsert()
	if err != nil {
		log.Errorf(`replaceTileset, upinsert tileser %s error, details: %s`, ts.ID, err)
	}
	//加载服务,todo 用户服务无需预加载
	err = ts.Service()
	if err != nil {
		log.Error(err)
		res.FailErr(c, err)
		return
	}
	set := userSet.service(uid)
	if set == nil {
		log.Errorf(`replaceTileset, %s's service set not found ^^`, uid)
		res.Fail(c, 4043)
		return
	}
	set.T.Store(ts.ID, ts)
	res.DoneData(c, ts)
}

//updateTileset 从数据集更新服务集
func updateTileset(c *gin.Context) {
	res := NewRes()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	tid := c.Param("id")
	ts := userSet.tileset(uid, tid)
	if ts == nil {
		log.Errorf(`replaceTileset, %s's tileset (%s) not found ^^`, uid, tid)
		res.Fail(c, 4045)
		return
	}
	ds := &DataSource{}
	// close(dst)
	// updatembtiles()
	// reload(dst)
	//更新服务
	tileset, err := LoadTileset(ds)
	if err != nil {
		log.Errorf("replaceTileset, could not load tileset %s, details: %s", ds.Path, err)
		res.FailErr(c, err)
		return
	}
	ts.Close()
	os.Remove(ts.Path)
	tileset.ID = tid
	tileset.Owner = uid
	//入库
	err = tileset.UpInsert()
	if err != nil {
		log.Errorf(`replaceTileset, upinsert tileser %s error, details: %s`, tileset.ID, err)
	}
	//加载服务,todo 用户服务无需预加载
	err = tileset.Service()
	if err != nil {
		log.Errorf("replaceTileset, could not load tileset %s, details: %s", tileset.ID, err)
		res.FailErr(c, err)
		return
	}
	set := userSet.service(uid)
	set.T.Store(tileset.ID, tileset)
	res.DoneData(c, tileset)
}

//downloadTileset 下载服务集
func downloadTileset(c *gin.Context) {
	res := NewRes()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	tid := c.Param("id")
	ts := userSet.tileset(uid, tid)
	if ts == nil {
		log.Warnf(`downloadTileset, %s's tileset (%s) not found ^^`, uid, tid)
		res.Fail(c, 4045)
		return
	}
	file, err := os.Open(ts.Path)
	if err != nil {
		log.Errorf(`downloadTileset, open %s's tileset (%s) error, details: %s ^^`, uid, tid, err)
		res.FailErr(c, err)
		return
	}
	c.Header("Content-type", "application/octet-stream")
	c.Header("Content-Disposition", "attachment; filename= "+ts.Name+"."+ts.ID+MBTILESEXT)
	io.Copy(c.Writer, file)
	return
}

//deleteTileset 删除服务集
func deleteTileset(c *gin.Context) {
	res := NewRes()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	tid := c.Param("ids")
	ts := userSet.tileset(uid, tid)
	if ts == nil {
		log.Errorf(`deleteTileset, %s's tilesete (%s) not found ^^`, uid, tid)
		res.Fail(c, 4045)
		return
	}
	set := userSet.service(uid)
	tids := strings.Split(tid, ",")
	for _, tid := range tids {
		err := ts.Clean()
		if err != nil {
			log.Error(err)
			res.FailErr(c, err)
			return
		}
		set.T.Delete(tid)
		err = db.Where("id = ?", tid).Delete(Tileset{}).Error
		if err != nil {
			log.Error(err)
			res.Fail(c, 5001)
			return
		}
	}
	res.Done(c, "")
}

//getTilejson 获取服务集tilejson
func getTileJSON(c *gin.Context) {
	res := NewRes()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	tid := c.Param("id")
	ts := userSet.tileset(uid, tid)
	if ts == nil {
		log.Warnf("getTileJSON, %s's tilesets (%s) not found ^^", uid, tid)
		res.Fail(c, 4045)
		return
	}
	mapurl := fmt.Sprintf(`%s/ts/view/%s/`, rootURL(c.Request), tid)          //need use user own service set
	tileurl := fmt.Sprintf(`%s/ts/x/%s/{z}/{x}/{y}`, rootURL(c.Request), tid) //need use user own service set
	out := map[string]interface{}{
		"tilejson": "2.1.0",
		"id":       tid,
		"scheme":   "xyz",
		"format":   ts.Format,
		"tiles":    []string{fmt.Sprintf("%s.%s", tileurl, ts.Format)},
		"map":      mapurl,
	}
	metadata, err := ts.GetInfo()
	if err != nil {
		log.Errorf("getTilejson, get metadata failed: %s; user: %s ^^", err, tid)
		res.Fail(c, 5004)
		return
	}
	for k, v := range metadata {
		switch k {
		// strip out values above
		case "tilejson", "id", "scheme", "format", "tiles", "map":
			continue

		// strip out values that are not supported or are overridden below
		case "grids", "interactivity", "modTime":
			continue

		// strip out values that come from TileMill but aren't useful here
		case "metatile", "scale", "autoscale", "_updated", "Layer", "Stylesheet":
			continue

		default:
			out[k] = v
		}
	}

	c.JSON(http.StatusOK, out)
}

//getTile 获取瓦片数据
func getTile(c *gin.Context) {
	res := NewRes()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	tid := c.Param("id")
	ts := userSet.tileset(uid, tid)
	if ts == nil {
		log.Errorf("getTile, %s's tilesets (%s) not found ^^", uid, tid)
		res.Fail(c, 4045)
		return
	}
	c.Param("z")
	c.Param("x")
	c.Param("y")

	// lookup our Map
	placeholder, err := strconv.ParseUint(c.Param("z"), 10, 32)
	if err != nil || placeholder > 22 {
		res.Fail(c, 4003)
		return
	}
	z := uint(placeholder)
	placeholder, err = strconv.ParseUint(c.Param("x"), 10, 32)
	if err != nil || placeholder >= (1<<z) {
		res.Fail(c, 4003)
		return
	}
	x := uint(placeholder)
	ypbf := c.Param("y")
	ys := strings.Split(ypbf, ".")
	if len(ys) != 2 {
		res.Fail(c, 4003)
		return
	}
	placeholder, err = strconv.ParseUint(ys[0], 10, 32)
	if err != nil || placeholder >= (1<<z) {
		res.Fail(c, 4003)
		return
	}
	y := uint(placeholder)

	var data []byte
	// flip y to match the spec
	y = (1 << z) - 1 - y

	data, err = ts.Tile(c.Request.Context(), z, x, y)
	if err != nil {
		log.Errorf("getTile, cannot fetch %s from DB for z=%d, x=%d, y=%d, details: %v", tid, z, x, y, err)
		res.Fail(c, 5004)
		return
	}
	if data == nil || len(data) <= 1 {
		switch ts.Format {
		case PNG, JPG, WEBP:
			// Return blank PNG for all image types
			c.Render(
				http.StatusOK, render.Data{
					ContentType: "image/png",
					Data:        BlankPNG(),
				})
		case PBF:
			// Return 204
			c.Writer.WriteHeader(http.StatusNoContent)
		default:
			c.Header("Content-Type", "application/json")
			c.Writer.WriteHeader(http.StatusNotFound)
			fmt.Fprint(c.Writer, `{"message": "Tile does not exist"}`)
		}
	}

	c.Header("Content-Type", ts.Format.ContentType())
	if ts.Format == PBF {
		c.Header("Content-Encoding", "gzip")
	}
	c.Writer.Write(data)
}

//viewTile 浏览服务集
func viewTile(c *gin.Context) {
	res := NewRes()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	tid := c.Param("id")
	ts := userSet.tileset(uid, tid)
	if ts == nil {
		log.Warnf("viewTile, %s's tilesets (%s) not found ^^", uid, tid)
		res.Fail(c, 4045)
		return
	}
	ptiles, istiles := c.GetQuery("tiles")
	if istiles && strings.Compare(strings.ToLower(ptiles), "yes") == 0 {
		tiles := fmt.Sprintf(`%s/ts/x/%s/{z}/{x}/{y}.pbf`, rootURL(c.Request), tid) //need use user own service set//
		layer := c.Query("layer")
		lrn, lrt := "", "" //"name type"
		lrs := strings.Split(layer, ",")
		if len(lrs) == 2 {
			lrn = lrs[0]
			lrt = lrs[1]
		}
		c.HTML(http.StatusOK, "dataset.html", gin.H{
			"Title":     "服务集预览(T)",
			"Name":      ts.Name + "@" + ts.ID,
			"LayerName": lrn,
			"LayerType": lrt,
			"Format":    ts.Format,
			"URL":       tiles,
		})
		return
	}
	tileurl := fmt.Sprintf(`%s/ts/x/%s/`, rootURL(c.Request), tid) //need use user own service set//{z}/{x}/{y}.pbf
	c.HTML(http.StatusOK, "tileset.html", gin.H{
		"Title":  "服务集预览(TJ)",
		"Name":   ts.Name + "@" + ts.ID,
		"Format": ts.Format,
		"URL":    tileurl,
	})
}
