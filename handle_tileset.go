package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
		res.Fail(c, 4044)
		return
	}
	var tilesets []*TileService
	set.T.Range(func(_, v interface{}) bool {
		tilesets = append(tilesets, v.(*TileService))
		return true
	})
	res.DoneData(c, tilesets)
}

//uploadTileset list user's tilesets
func uploadTileset(c *gin.Context) {
	res := NewRes()
	// uid := c.GetString(identityKey)
	uid := c.Param("user")
	set := userSet.service(uid)
	if set == nil {
		res.Fail(c, 4044)
		return
	}
	// style source
	file, err := c.FormFile("file")
	if err != nil {
		log.Errorf(`uploadTileset, get form: %s; user: %s`, err, uid)
		res.Fail(c, 4046)
		return
	}
	ext := filepath.Ext(file.Filename)
	name := strings.TrimSuffix(file.Filename, ext)
	tid, _ := shortid.Generate()
	tid = name + "." + tid
	dst := filepath.Join("tilesets", uid, tid+ext)

	if err := c.SaveUploadedFile(file, dst); err != nil {
		log.Errorf(`uploadTileset, upload file: %s; user: %s`, err, uid)
		res.Fail(c, 5002)
		return
	}

	//加载文件
	tileset, err := LoadTileset(dst)
	if err != nil {
		log.Errorf("uploadTileset, could not load tileset %s, details: %s", dst, err)
		res.FailErr(c, err)
		return
	}
	//入库
	err = tileset.UpInsert()
	if err != nil {
		log.Errorf(`uploadTileset, upinsert tileset %s error, details: %s`, dst, err)
	}

	//加载服务,todo 用户服务无需预加载
	if true {
		ts, err := tileset.toService()
		if err != nil {
			log.Error(err)
			res.FailErr(c, err)
			return
		}
		set := userSet.service(uid)
		if set == nil {
			log.Errorf("%s's service set not found", uid)
			res.FailMsg(c, "加载服务失败")
			return
		}
		set.T.Store(ts.ID, ts)
	}
	res.DoneData(c, gin.H{
		"id": tid,
	})
}

//replaceTileset 上传并替换服务集
func replaceTileset(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	uid := c.Param("user")
	tid := c.Param("id")
	ts := userSet.tileset(uid, tid)
	if ts == nil {
		log.Errorf(`replaceTileset, %s's tile service (%s) not found ^^`, uid, tid)
		res.Fail(c, 4044)
		return
	}
	// style source
	file, err := c.FormFile("file")
	if err != nil {
		log.Errorf(`replaceTileset, get file error: %s; user: %s`, err, id)
		res.Fail(c, 4046)
		return
	}
	ext := filepath.Ext(file.Filename)
	name := strings.TrimSuffix(file.Filename, ext)
	lext := strings.ToLower(ext)
	switch lext {
	case ".mbtiles":
	default:
		log.Errorf(`replaceTileset, tileset format error, details: %s; user: %s`, file.Filename, id)
		res.FailMsg(c, "文件格式错误,请上传正确的.mbtiles文件")
		return
	}
	ntid, _ := shortid.Generate()
	ntid = name + "." + ntid
	dst := filepath.Join("tilesets", uid, ntid)
	if err := c.SaveUploadedFile(file, dst); err != nil {
		log.Errorf(`replaceTileset, upload file: %s; user: %s`, err, uid)
		res.Fail(c, 5002)
		return
	}
	//更新服务
	tile, err := LoadTileset(dst)
	if err != nil {
		log.Errorf("replaceTileset, could not load tileset %s, details: %s", dst, err)
		res.FailErr(c, err)
		return
	}
	tile.ID = tid
	tile.Owner = uid
	//加载服务,todo 用户服务无需预加载
	tss, err := tile.toService()
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
	set := userSet.service(uid)
	if set == nil {
		log.Errorf(`replaceTileset, %s's service set not found ^^`, uid)
		res.Fail(c, 4044)
		return
	}
	//替换
	set.T.Store(tss.ID, tss)
	//入库
	err = tile.UpInsert()
	if err != nil {
		log.Errorf(`replaceTileset, upinsert tileser %s error, details: %s`, tile.ID, err)
	}
	res.DoneData(c, gin.H{
		"id": tss.ID,
	})
}

//publishTileset 上传并发布服务集
func publishTileset(c *gin.Context) {
	res := NewRes()
	uid := c.Param("user")
	set := userSet.service(uid)
	if set == nil {
		res.Fail(c, 4044)
		return
	}
	file, err := c.FormFile("file")
	if err != nil {
		log.Errorf(`uploadFiles, gin form file error, details: %s`, err)
		res.Fail(c, 4046)
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
log.Info("receive finished...")
	dtfiles, err := LoadDatafile(dst)
	if err != nil {
		log.Errorf(`publishTileset, loading datafile error, details: %s`, err)
		res.FailErr(c, err)
		return
	}
	var inputfiles []string
	for _, df := range dtfiles {
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
	outfile := filepath.Join("tilesets", uid, name+"."+id+".mbtiles")
log.Println("start create mbtiles")
	err = createMbtiles(outfile, inputfiles)
	if err != nil {
		log.Error(err)
		res.FailErr(c, err)
		return
	}

log.Info("mbtiles created...")
	//加载mbtiles
	tileset, err := LoadTileset(outfile)
	if err != nil {
		log.Errorf("uploadTileset, could not load tileset %s, details: %s", dst, err)
		res.FailErr(c, err)
		return
	}
	//入库
	err = tileset.UpInsert()
	if err != nil {
		log.Errorf(`uploadTileset, upinsert tileset %s error, details: %s`, dst, err)
	}

	//加载服务,todo 用户服务无需预加载
	if true {
		ts, err := tileset.toService()
		if err != nil {
			log.Error(err)
			res.FailErr(c, err)
			return
		}
		set := userSet.service(uid)
		if set == nil {
			log.Errorf("%s's service set not found", uid)
			res.FailMsg(c, "加载服务失败")
			return
		}
		set.T.Store(ts.ID, ts)
	}
	res.DoneData(c, gin.H{
		"id": name + "." + id,
	})
	res.DoneData(c, dtfiles)
}

//publishTileset 上传并发布服务集
func rePublishTileset(c *gin.Context) {
	res := NewRes()
	uid := c.Param("user")
	tid := c.Param("id")
	ts := userSet.tileset(uid, tid)
	if ts == nil {
		res.Fail(c, 4044)
		return
	}
	file, err := c.FormFile("file")
	if err != nil {
		log.Errorf(`uploadFiles, gin form file error, details: %s`, err)
		res.Fail(c, 4046)
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
	outfile := filepath.Join("tilesets", uid, ntid+".mbtiles")
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

	//加载服务,todo 用户服务无需预加载
	if true {
		ts, err := tile.toService()
		if err != nil {
			log.Error(err)
			res.FailErr(c, err)
			return
		}
		set := userSet.service(uid)
		if set == nil {
			log.Errorf("%s's service set not found", uid)
			res.FailMsg(c, "加载服务失败")
			return
		}
		set.T.Store(ts.ID, ts)
	}
	//入库
	err = tile.UpInsert()
	if err != nil {
		log.Errorf(`rePublishTileset, upinsert tileset %s error, details: %s`, dst, err)
	}

	res.DoneData(c, gin.H{
		"id":    tid,
		"newid": ntid,
	})
}

//createTileset 从数据集创建服务集
func createTileset(c *gin.Context) {
	res := NewRes()
	// id := c.GetString(identityKey)
	uid := c.Param("user")
	tid := c.Param("id")
	ts := userSet.tileset(uid, tid)
	if ts == nil {
		log.Errorf(`replaceTileset, %s's tile service (%s) not found ^^`, uid, tid)
		res.Fail(c, 4044)
		return
	}
	dst := ""
	// creatembtiles(dst)
	//更新服务
	tile, err := LoadTileset(dst)
	if err != nil {
		log.Errorf("replaceTileset, could not load tileset %s, details: %s", dst, err)
		res.FailErr(c, err)
		return
	}
	ts.Clean()
	tile.ID = tid
	tile.Owner = uid
	//加载服务,todo 用户服务无需预加载
	tss, err := tile.toService()
	if err != nil {
		log.Error(err)
		res.FailErr(c, err)
		return
	}
	set := userSet.service(uid)
	if set == nil {
		log.Errorf(`replaceTileset, %s's service set not found ^^`, uid)
		res.Fail(c, 4044)
		return
	}

	set.T.Store(tss.ID, tss)
	//入库
	err = tile.UpInsert()
	if err != nil {
		log.Errorf(`replaceTileset, upinsert tileser %s error, details: %s`, tile.ID, err)
	}
	res.DoneData(c, gin.H{
		"id": tss.ID,
	})
}

//updateTileset 从数据集更新服务集
func updateTileset(c *gin.Context) {
	res := NewRes()
	// id := c.GetString(identityKey)
	uid := c.Param("user")
	tid := c.Param("id")
	ts := userSet.tileset(uid, tid)
	if ts == nil {
		log.Errorf(`replaceTileset, %s's tile service (%s) not found ^^`, uid, tid)
		res.Fail(c, 4044)
		return
	}
	dst := ""
	// close(dst)
	// updatembtiles()
	// reload(dst)
	//更新服务
	tile, err := LoadTileset(dst)
	if err != nil {
		log.Errorf("replaceTileset, could not load tileset %s, details: %s", dst, err)
		res.FailErr(c, err)
		return
	}
	ts.Close()
	os.Remove(ts.Path)
	tile.ID = tid
	tile.Owner = uid

	//加载服务,todo 用户服务无需预加载
	tss, err := tile.toService()
	if err != nil {
		log.Errorf("replaceTileset, could not load tileset %s, details: %s", dst, err)
		res.FailErr(c, err)
		return
	}
	set := userSet.service(uid)
	if set == nil {
		log.Errorf(`replaceTileset, %s's service set not found ^^`, uid)
		res.Fail(c, 4044)
		return
	}

	set.T.Store(tss.ID, tss)
	//入库
	err = tile.UpInsert()
	if err != nil {
		log.Errorf(`replaceTileset, upinsert tileser %s error, details: %s`, tile.ID, err)
	}
	res.DoneData(c, gin.H{
		"id": tss.ID,
	})
}

//downloadTileset 下载服务集
func downloadTileset(c *gin.Context) {
	res := NewRes()
	// uid := c.GetString(identityKey)
	uid := c.Param("user")
	tid := c.Param("id")
	ts := userSet.tileset(uid, tid)
	if ts == nil {
		log.Errorf(`downloadTileset, %s's tile service (%s) not found ^^`, uid, tid)
		res.Fail(c, 4044)
		return
	}
	file, err := os.Open(ts.Path)
	if err != nil {
		log.Errorf(`downloadTileset, open %s's tileset (%s) error, details: %s ^^`, uid, tid, err)
		res.FailErr(c, err)
		return
	}
	c.Header("Content-type", "application/octet-stream")
	c.Header("Content-Disposition", "attachment; filename= "+ts.ID+".mbtiles")
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
		log.Errorf(`deleteTileset, %s's tile service (%s) not found ^^`, uid, tid)
		res.Fail(c, 4044)
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
		set.S.Delete(tid)
		err = db.Where("id = ?", tid).Delete(Tileset{}).Error
		if err != nil {
			log.Error(err)
			res.Fail(c, 5001)
			return
		}
		err = os.Remove(ts.Path) // +ts.Tileset.TileType()
		if err != nil {
			log.Errorf(`deleteTileset, remove %s's tilesets (%s) error, details: %s ^^`, uid, tid, err)
			res.FailErr(c, err)
			return
		}
	}
	res.Done(c, "")
}

//getTilejson get tilejson
func getTilejson(c *gin.Context) {
	res := NewRes()
	// uid := c.GetString(identityKey)
	uid := c.Param("user")
	tid := c.Param("id")
	tss := userSet.tileset(uid, tid)
	if tss == nil {
		log.Errorf("tilesets id(%s) not exist in the service", tid)
		res.Fail(c, 4044)
		return
	}
	mapurl := fmt.Sprintf(`%s/tilesets/%s/view/%s/`, rootURL(c.Request), uid, tid) //need use user own service set
	format := tss.Format.String()
	tileurl := fmt.Sprintf(`%s/tilesets/%s/x/%s/{z}/{x}/{y}`, rootURL(c.Request), uid, tid) //need use user own service set
	out := map[string]interface{}{
		"tilejson": "2.1.0",
		"id":       tid,
		"scheme":   "xyz",
		"format":   format,
		"tiles":    []string{fmt.Sprintf("%s.%s", tileurl, format)},
		"map":      mapurl,
	}
	// switch ttype := tss.Tileset.(type) {
	// case *MBTiles:
	// 	fmt.Print("*MBTiles")
	// case TileMap:
	// 	fmt.Print("TileMap")
	// default:
	// 	fmt.Print(ttype)
	// }
	metadata, err := tss.GetInfo()
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

	if tss.HasUTFGrid {
		out["grids"] = []string{fmt.Sprintf("%s.json", tileurl)}
	}

	c.JSON(http.StatusOK, out)
}

func viewTile(c *gin.Context) {
	res := NewRes()
	uid := c.GetString(identityKey)
	tid := c.Param("id")
	tss := userSet.tileset(uid, tid)
	if tss == nil {
		log.Errorf("viewTile, tilesets id(%s) not exist in the service", tid)
		res.Fail(c, 4044)
		return
	}
	tileurl := fmt.Sprintf(`%s/tilesets/%s/x/%s/`, rootURL(c.Request), uid, tid) //need use user own service set
	c.HTML(http.StatusOK, "data.html", gin.H{
		"Title": "PerView",
		"ID":    tid,
		"URL":   tileurl,
		"FMT":   tss.Format.String(),
	})
}

func getTile(c *gin.Context) {
	res := NewRes()
	uid := c.GetString(identityKey)
	tid := c.Param("id")
	tss := userSet.tileset(uid, tid)
	if tss == nil {
		log.Errorf("tilesets id(%s) not exist in the service", tid)
		res.Fail(c, 4044)
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
		data, err = tss.Tile(c.Request.Context(), tc.z, tc.x, tc.y)
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
		switch tss.Format {
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
			c.Writer.Header().Set("Content-Type", "application/json")
			c.Writer.WriteHeader(http.StatusNotFound)
			fmt.Fprint(c.Writer, `{"message": "Tile does not exist"}`)
		}
	}

	if isGrid {
		// c.Writer.Header().Set("Content-Type", "application/json")
		// if tileset.UTFGridCompression() == ZLIB {
		// 	c.Writer.Header().Set("Content-Encoding", "deflate")
		// } else {
		// 	c.Writer.Header().Set("Content-Encoding", "gzip")
		// }
	} else {
		c.Writer.Header().Set("Content-Type", tss.Format.ContentType())
		if tss.Format == PBF {
			c.Writer.Header().Set("Content-Encoding", "gzip")
		}
	}
	c.Writer.Write(data)
}
