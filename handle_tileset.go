package main

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/gin-gonic/gin"
	"github.com/teris-io/shortid"
)

//listTilesets list user's tilesets
func listTilesets(c *gin.Context) {
	res := NewRes()
	var tilesets []*TileService
	pubSet.S.Range(func(_, v interface{}) bool {
		tilesets = append(tilesets, v.(*TileService))
		return true
	})
	res.DoneData(c, tilesets)
}

//uploadTileset list user's tilesets
func uploadTileset(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	// style source
	file, err := c.FormFile("file")
	if err != nil {
		log.Errorf(`uploadTileset, get form: %s; user: %s`, err, id)
		res.Fail(c, 4046)
		return
	}
	tilesets := cfgV.GetString("assets.tilesets")
	ext := filepath.Ext(file.Filename)
	name := strings.TrimSuffix(file.Filename, ext)
	tid, _ := shortid.Generate()
	tid = name + "." + tid
	dst := filepath.Join(tilesets, tid+ext)

	if err := c.SaveUploadedFile(file, dst); err != nil {
		log.Errorf(`uploadTileset, upload file: %s; user: %s`, err, id)
		res.Fail(c, 5002)
		return
	}

	//更新服务
	err = pubSet.ServeTileset(dst)
	if err != nil {
		log.Errorf(`uploadTileset, add mbtiles: %s ^^`, err)
	}

	res.DoneData(c, gin.H{
		"tid": tid,
	})
}

//getTilejson get tilejson
func getTilejson(c *gin.Context) {
	res := NewRes()
	// id := c.GetString(identityKey)
	tid := c.Param("tid")
	tileService, ok := pubSet.T.Load(tid)
	if !ok {
		log.Errorf("tilesets id(%s) not exist in the service", tid)
		res.Fail(c, 4044)
		return
	}
	urlPath := c.Request.URL.Path
	url := fmt.Sprintf("%s%s", rootURL(c.Request), urlPath) //need use user own service set
	tileset := tileService.(*TileService).Tileset
	imgFormat := tileset.TileFormat().String()
	out := map[string]interface{}{
		"tilejson": "2.1.0",
		"id":       tid,
		"scheme":   "xyz",
		"format":   imgFormat,
		"tiles":    []string{fmt.Sprintf("%s/{z}/{x}/{y}.%s", url, imgFormat)},
		"map":      url + "/",
	}
	// metadata, err := tileset.GetInfo()
	// if err != nil {
	// 	log.Errorf("getTilejson, get metadata failed: %s; user: %s ^^", err, id)
	// 	res.Fail(c, 5004)
	// 	return
	// }
	// for k, v := range metadata {
	// 	switch k {
	// 	// strip out values above
	// 	case "tilejson", "id", "scheme", "format", "tiles", "map":
	// 		continue

	// 	// strip out values that are not supported or are overridden below
	// 	case "grids", "interactivity", "modTime":
	// 		continue

	// 	// strip out values that come from TileMill but aren't useful here
	// 	case "metatile", "scale", "autoscale", "_updated", "Layer", "Stylesheet":
	// 		continue

	// 	default:
	// 		out[k] = v
	// 	}
	// }

	// if tileset.HasUTFGrid() {
	// 	out["grids"] = []string{fmt.Sprintf("%s/{z}/{x}/{y}.json", url)}
	// }

	c.JSON(http.StatusOK, out)
}

func viewTile(c *gin.Context) {
	res := NewRes()
	tid := c.Param("tid")
	tileService, ok := pubSet.T.Load(tid)
	if !ok {
		log.Errorf("tilesets id(%s) not exist in the service", tid)
		res.Fail(c, 4044)
		return
	}
	tss := tileService.(*TileService)

	c.HTML(http.StatusOK, "data.html", gin.H{
		"Title": "PerView",
		"ID":    tid,
		"URL":   strings.TrimSuffix(c.Request.URL.Path, "/"),
		"FMT":   tss.Tileset.TileFormat().String(),
	})
}

func getTile(c *gin.Context) {
	res := NewRes()
	// split path components to extract tile coordinates x, y and z
	pcs := strings.Split(c.Request.URL.Path[1:], "/")
	// we are expecting at least "tilesets", :user , :id, :z, :x, :y + .ext
	size := len(pcs)
	if size < 5 || pcs[4] == "" {
		res.Fail(c, 4003)
		return
	}
	// tid := c.Param("tid")
	// tileService, ok := pubSet.Tilesets[tid]
	// if !ok {
	// 	log.Errorf("tilesets id(%s) not exist in the service", tid)
	// 	res.Fail(c, 4044)
	// 	return
	// }

	// tileset := tileService.Tileset

	// z, x, y := pcs[size-3], pcs[size-2], pcs[size-1]
	// tc, ext, err := tileCoordFromString(z, x, y)
	// if err != nil {
	// 	log.Error(err)
	// 	res.Fail(c, 4003)
	// 	return
	// }
	var data []byte
	// // flip y to match the spec
	// tc.y = (1 << uint64(tc.z)) - 1 - tc.y
	// isGrid := ext == ".json"
	// switch {
	// case !isGrid:
	// 	data, err = tileset.Tile(tc.z, tc.x, tc.y)
	// case isGrid && tileset.HasUTFGrid():
	// 	err = tileset.GetGrid(tc.z, tc.x, tc.y, &data)
	// default:
	// 	err = fmt.Errorf("no grid supplied by tile database")
	// }
	// if err != nil {
	// 	// augment error info
	// 	t := "tile"
	// 	if isGrid {
	// 		t = "grid"
	// 	}
	// 	err = fmt.Errorf("getTile, cannot fetch %s from DB for z=%d, x=%d, y=%d: %v", t, tc.z, tc.x, tc.y, err)
	// 	log.Error(err)
	// 	res.Fail(c, 5004)
	// 	return
	// }
	// if data == nil || len(data) <= 1 {
	// 	switch tileset.TileFormat() {
	// 	case PNG, JPG, WEBP:
	// 		// Return blank PNG for all image types
	// 		c.Render(
	// 			http.StatusOK, render.Data{
	// 				ContentType: "image/png",
	// 				Data:        BlankPNG(),
	// 			})
	// 	case PBF:
	// 		// Return 204
	// 		c.Writer.WriteHeader(http.StatusNoContent)
	// 	default:
	// 		c.Writer.Header().Set("Content-Type", "application/json")
	// 		c.Writer.WriteHeader(http.StatusNotFound)
	// 		fmt.Fprint(c.Writer, `{"message": "Tile does not exist"}`)
	// 	}
	// }

	// if isGrid {
	// 	c.Writer.Header().Set("Content-Type", "application/json")
	// 	if tileset.UTFGridCompression() == ZLIB {
	// 		c.Writer.Header().Set("Content-Encoding", "deflate")
	// 	} else {
	// 		c.Writer.Header().Set("Content-Encoding", "gzip")
	// 	}
	// } else {
	// 	c.Writer.Header().Set("Content-Type", tileset.ContentType())
	// 	if tileset.TileFormat() == PBF {
	// 		c.Writer.Header().Set("Content-Encoding", "gzip")
	// 	}
	// }
	c.Writer.Write(data)
}