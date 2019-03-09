package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/render"
	"github.com/teris-io/shortid"
)

//listTilesets list user's tilesets
func listTilesets(c *gin.Context) {
	res := NewRes()
	// id := c.GetString(identityKey)
	uid := c.Param("user")
	set := userSet.service(uid)
	if set == nil {
		log.Warnf(`listTilesets, %s's service not found ^^`, uid)
		res.Fail(c, 4043)
		return
	}
	var tss []*Tileset
	set.T.Range(func(_, v interface{}) bool {
		tss = append(tss, v.(*Tileset))
		return true
	})

	if uid != ATLAS && "true" == c.Query("public") {
		set := userSet.service(ATLAS)
		if set != nil {
			set.T.Range(func(_, v interface{}) bool {
				ts, ok := v.(*Tileset)
				if ok {
					if ts.Public {
						tss = append(tss, ts)
					}
				}
				return true
			})
		}
	}

	res.DoneData(c, tss)
}

//getTilesetInfo list user's tilesets info
func getTilesetInfo(c *gin.Context) {
	res := NewRes()
	// id := c.GetString(identityKey)
	uid := c.Param("user")
	tid := c.Param("id")
	ts := userSet.tileset(uid, tid)
	if ts == nil {
		log.Warnf(`getTilesetInfo, %s's tileset (%s) not found ^^`, uid, tid)
		res.Fail(c, 4045)
		return
	}
	res.DoneData(c, ts)
}

//uploadTileset 上传服务集
func uploadTileset(c *gin.Context) {
	res := NewRes()
	// uid := c.GetString(identityKey)
	uid := c.Param("user")
	set := userSet.service(uid)
	if set == nil {
		log.Warnf(`uploadTileset, %s's service not found ^^`, uid)
		res.Fail(c, 4043)
		return
	}
	// style source
	file, err := c.FormFile("file")
	if err != nil {
		log.Warnf(`uploadTileset, read %s's upload file error, details: %s`, uid, err)
		res.Fail(c, 4048)
		return
	}
	ext := filepath.Ext(file.Filename)
	name := strings.TrimSuffix(file.Filename, ext)
	tid, _ := shortid.Generate()
	tid = name + "." + tid
	dst := filepath.Join("tilesets", uid, tid+ext)

	if err := c.SaveUploadedFile(file, dst); err != nil {
		log.Errorf(`uploadTileset, save %s's upload file error, details: %s`, uid, err)
		res.Fail(c, 5002)
		return
	}

	//加载文件
	ts, err := LoadTileset(dst)
	if err != nil {
		log.Errorf("uploadTileset, could not load tileset %s, details: %s", dst, err)
		res.FailErr(c, err)
		return
	}
	//更新user
	ts.Owner = uid
	//入库
	err = ts.UpInsert()
	if err != nil {
		log.Errorf(`uploadTileset, upinsert tileset %s error, details: %s`, dst, err)
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
	// user := c.GetString(identityKey)
	uid := c.Param("user")
	tid := c.Param("id")
	ts := userSet.tileset(uid, tid)
	if ts == nil {
		log.Errorf(`replaceTileset, %s's tileset (%s) not found ^^`, uid, tid)
		res.Fail(c, 4045)
		return
	}
	// style source
	file, err := c.FormFile("file")
	if err != nil {
		log.Errorf(`replaceTileset, read %s's upload file error, details: %s`, uid, err)
		res.Fail(c, 4048)
		return
	}
	ext := filepath.Ext(file.Filename)
	name := strings.TrimSuffix(file.Filename, ext)
	lext := strings.ToLower(ext)
	switch lext {
	case MBTILESEXT:
	default:
		log.Errorf(`replaceTileset, %s' upload tileset format error, details: %s`, uid, file.Filename)
		res.FailMsg(c, "文件格式错误, 请上传正确的.mbtiles服务集")
		return
	}
	ntid, _ := shortid.Generate()
	ntid = name + "." + ntid
	dst := filepath.Join("tilesets", uid, ntid+MBTILESEXT)
	if err := c.SaveUploadedFile(file, dst); err != nil {
		log.Errorf(`replaceTileset, save %s's upload file error, details: %s`, uid, err)
		res.Fail(c, 5002)
		return
	}
	//更新服务
	tileset, err := LoadTileset(dst)
	if err != nil {
		log.Errorf("replaceTileset, could not load tileset %s, details: %s", dst, err)
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
	//替换
	set := userSet.service(uid)
	set.T.Store(tileset.ID, tileset)
	res.DoneData(c, tileset)
}

//publishTileset 上传并发布服务集
func publishTileset(c *gin.Context) {
	res := NewRes()
	uid := c.Param("user")
	set := userSet.service(uid)
	if set == nil {
		res.Fail(c, 4043)
		return
	}
	if runtime.GOOS == "windows" {
		res.FailMsg(c, "current windows server does not support this func")
		return
	}
	file, err := c.FormFile("file")
	if err != nil {
		log.Errorf(`uploadFiles, gin form file error, details: %s`, err)
		res.Fail(c, 4048)
		return
	}
	filename := file.Filename
	ext := filepath.Ext(filename)
	lext := strings.ToLower(ext)
	switch lext {
	case ".geojson", ".zip", ".kml", ".gpx":
	default:
		res.FailMsg(c, "未知数据格式, 请使用geojson/shapefile(zip)/kml/gpx等数据.")
		return
	}
	name := strings.TrimSuffix(filename, ext)
	id, _ := shortid.Generate()
	dst := filepath.Join("tilesets", uid, name+"."+id+lext)
	if err := c.SaveUploadedFile(file, dst); err != nil {
		log.Errorf(`uploadFiles, saving uploaded file error, details: %s`, err)
		res.Fail(c, 5002)
		return
	}

	dtfiles, err := LoadDatafile(dst)
	if err != nil {
		log.Errorf(`publishTileset, loading datafile error, details: %s`, err)
		res.FailErr(c, err)
		return
	}
	var inputfiles []string
	for _, df := range dtfiles {
		df.Owner = uid
		err = df.UpInsert()
		if err != nil {
			log.Errorf(`uploadFiles, upinsert datafile info error, details: %s`, err)
		}
		var infile string
		switch df.Format {
		case ".geojson":
			infile = df.Path
		case ".shp", ".kml", ".gpx":
			err := df.toGeojson()
			if err != nil {
				log.Errorf(`publishTileset, convert to geojson error, details: %s`, err)
				continue
			}
			infile = strings.TrimSuffix(df.Path, df.Format) + ".geojson"
		default:
			continue
		}
		inputfiles = append(inputfiles, infile)
	}
	//publish to mbtiles
	outfile := filepath.Join("tilesets", uid, name+"."+id+MBTILESEXT)
	err = createMbtiles(outfile, inputfiles)
	if err != nil {
		log.Error(err)
		res.FailErr(c, err)
		return
	}

	//加载mbtiles
	ts, err := LoadTileset(outfile)
	if err != nil {
		log.Errorf("uploadTileset, could not load tileset %s, details: %s", dst, err)
		res.FailErr(c, err)
		return
	}
	//入库
	ts.Owner = uid
	err = ts.UpInsert()
	if err != nil {
		log.Errorf(`uploadTileset, upinsert tileset %s error, details: %s`, dst, err)
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

//publishTileset 上传并发布服务集
func rePublishTileset(c *gin.Context) {
	res := NewRes()
	uid := c.Param("user")
	tid := c.Param("id")
	ts := userSet.tileset(uid, tid)
	if ts == nil {
		res.Fail(c, 4045)
		return
	}
	if runtime.GOOS == "windows" {
		res.FailMsg(c, "current windows server does not support this func")
		return
	}
	file, err := c.FormFile("file")
	if err != nil {
		log.Errorf(`uploadFiles, gin form file error, details: %s`, err)
		res.Fail(c, 4048)
		return
	}
	filename := file.Filename
	ext := filepath.Ext(filename)
	lext := strings.ToLower(ext)
	switch lext {
	case ".geojson", ".zip", ".kml", ".gpx":
	default:
		res.FailMsg(c, "未知数据格式, 请使用geojson/shapefile(zip)/kml/gpx等数据.")
		return
	}
	name := strings.TrimSuffix(filename, ext)
	ntid, _ := shortid.Generate()
	ntid = name + "." + ntid
	dst := filepath.Join("tilesets", uid, ntid+lext)
	if err := c.SaveUploadedFile(file, dst); err != nil {
		log.Errorf(`uploadFiles, saving uploaded file error, details: %s`, err)
		res.Fail(c, 5002)
		return
	}

	dtfiles, err := LoadDatafile(dst)
	if err != nil {
		log.Errorf(`rePublishTileset, loading datafile error, details: %s`, err)
		res.FailErr(c, err)
		return
	}
	var inputfiles []string
	for _, df := range dtfiles {
		df.Owner = uid
		err = df.UpInsert()
		if err != nil {
			log.Errorf(`rePublishTileset, upinsert datafile info error, details: %s`, err)
		}
		var infile string
		switch df.Format {
		case ".geojson":
			infile = df.Path
		case ".shp", ".kml", ".gpx":
			err := df.toGeojson()
			if err != nil {
				log.Errorf(`rePublishTileset, convert to geojson error, details: %s`, err)
				continue
			}
			infile = strings.TrimSuffix(df.Path, df.Format) + ".geojson"
		default:
			continue
		}
		inputfiles = append(inputfiles, infile)
	}
	//publish to mbtiles
	outfile := filepath.Join("tilesets", uid, ntid+MBTILESEXT)
	err = createMbtiles(outfile, inputfiles)
	if err != nil {
		log.Error(err)
		res.FailErr(c, err)
		return
	}
	//加载mbtiles
	tile, err := LoadTileset(outfile)
	if err != nil {
		log.Errorf("rePublishTileset, could not load tileset %s, details: %s", dst, err)
		res.FailErr(c, err)
		return
	}
	tile.ID = tid
	tile.Owner = uid
	//入库
	err = tile.UpInsert()
	if err != nil {
		log.Errorf(`rePublishTileset, upinsert tileset %s error, details: %s`, dst, err)
	}
	//加载服务,todo 用户服务无需预加载
	err = tile.Service()
	if err != nil {
		log.Error(err)
		res.FailErr(c, err)
		return
	}
	set := userSet.service(uid)
	set.T.Store(tile.ID, tile)
	res.DoneData(c, tile)
}

//createTileset 从数据集创建服务集
func createTileset(c *gin.Context) {
	res := NewRes()
	// id := c.GetString(identityKey)
	uid := c.Param("user")
	did := c.Param("id")
	dts := userSet.dataset(uid, did)
	if dts == nil {
		log.Warnf(`createTileset, %s's tileset (%s) not found ^^`, uid, did)
		res.Fail(c, 4045)
		return
	}
	path := filepath.Join("tilesets", uid, dts.ID+MBTILESEXT)
	// download
	err := dts.CacheMBTiles(path)
	if err != nil {
		log.Errorf("createTileset, could not load tileset %s, details: %s", path, err)
		res.FailErr(c, err)
		return
	}
	ts, err := LoadTileset(path)
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
	res.FailMsg(c, "系统维护")
	return
	// id := c.GetString(identityKey)
	uid := c.Param("user")
	tid := c.Param("id")
	ts := userSet.tileset(uid, tid)
	if ts == nil {
		log.Errorf(`replaceTileset, %s's tileset (%s) not found ^^`, uid, tid)
		res.Fail(c, 4045)
		return
	}
	dst := ""
	// close(dst)
	// updatembtiles()
	// reload(dst)
	//更新服务
	tileset, err := LoadTileset(dst)
	if err != nil {
		log.Errorf("replaceTileset, could not load tileset %s, details: %s", dst, err)
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
		log.Errorf("replaceTileset, could not load tileset %s, details: %s", dst, err)
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
	// uid := c.GetString(identityKey)
	uid := c.Param("user")
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
	c.Header("Content-Disposition", "attachment; filename= "+ts.ID+MBTILESEXT)
	io.Copy(c.Writer, file)
	return
}

//deleteTileset create a style
func deleteTileset(c *gin.Context) {
	res := NewRes()
	// uid := c.GetString(identityKey)
	uid := c.Param("user")
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

//getTilejson get tilejson
func getTileJSON(c *gin.Context) {
	res := NewRes()
	// uid := c.GetString(identityKey)
	uid := c.Param("user")
	tid := c.Param("id")
	ts := userSet.tileset(uid, tid)
	if ts == nil {
		log.Warnf("getTileJSON, %s's tilesets (%s) not found ^^", uid, tid)
		res.Fail(c, 4045)
		return
	}
	mapurl := fmt.Sprintf(`%s/tilesets/%s/view/%s/`, rootURL(c.Request), uid, tid) //need use user own service set
	format := ts.Format.String()
	tileurl := fmt.Sprintf(`%s/tilesets/%s/x/%s/{z}/{x}/{y}`, rootURL(c.Request), uid, tid) //need use user own service set
	out := map[string]interface{}{
		"tilejson": "2.1.0",
		"id":       tid,
		"scheme":   "xyz",
		"format":   format,
		"tiles":    []string{fmt.Sprintf("%s.%s", tileurl, format)},
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

	if ts.HasUTFGrid {
		out["grids"] = []string{fmt.Sprintf("%s.json", tileurl)}
	}

	c.JSON(http.StatusOK, out)
}

func viewTile(c *gin.Context) {
	res := NewRes()
	uid := c.Param("user")
	tid := c.Param("id")
	tss := userSet.tileset(uid, tid)
	if tss == nil {
		log.Warnf("viewTile, %s's tilesets (%s) not found ^^", uid, tid)
		res.Fail(c, 4045)
		return
	}
	tileurl := fmt.Sprintf(`%s/tilesets/%s/x/%s/`, rootURL(c.Request), uid, tid) //need use user own service set
	c.HTML(http.StatusOK, "tileset.html", gin.H{
		"Title": "PerView",
		"ID":    tid,
		"URL":   tileurl,
		"FMT":   tss.Format.String(),
	})
}

func getTile(c *gin.Context) {
	res := NewRes()
	uid := c.Param("user")
	tid := c.Param("id")
	ts := userSet.tileset(uid, tid)
	if ts == nil {
		log.Errorf("getTile, %s's tilesets (%s) not found ^^", uid, tid)
		res.Fail(c, 4045)
		return
	}
	// split path components to extract tile coordinates x, y and z
	pcs := strings.Split(c.Request.URL.Path[1:], "/")
	// we are expecting at least "tilesets", :user , :id, :z, :x, :y + .ext
	size := len(pcs)
	if size < 5 || pcs[4] == "" {
		res.Fail(c, 4003)
		return
	}
	z, x, y := pcs[size-3], pcs[size-2], pcs[size-1]
	tc, ext, err := tileCoordFromString(z, x, y)
	if err != nil {
		log.Error(err)
		res.Fail(c, 4003)
		return
	}

	var data []byte
	// flip y to match the spec
	tc.y = (1 << uint64(tc.z)) - 1 - tc.y
	isGrid := ext == ".json"
	switch {
	case !isGrid:
		data, err = ts.Tile(c.Request.Context(), tc.z, tc.x, tc.y)
	// case isGrid && tss.Tileset.HasUTFGrid():
	// 	err = tss.Tileset.GetGrid(tc.z, tc.x, tc.y, &data)
	default:
		err = fmt.Errorf("no grid supplied by tile database")
	}
	if err != nil {
		// augment error info
		t := "tile"
		if isGrid {
			t = "grid"
		}
		err = fmt.Errorf("getTile, cannot fetch %s from DB for z=%d, x=%d, y=%d: %v", t, tc.z, tc.x, tc.y, err)
		log.Error(err)
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

	if isGrid {
		c.Header("Content-Type", "application/json")
		if ts.UTFGridCompression == ZLIB {
			c.Header("Content-Encoding", "deflate")
		} else {
			c.Header("Content-Encoding", "gzip")
		}
	} else {
		c.Header("Content-Type", ts.Format.ContentType())
		if ts.Format == PBF {
			c.Header("Content-Encoding", "gzip")
		}
	}
	c.Writer.Write(data)
}
