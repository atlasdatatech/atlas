package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/render"
	"github.com/jinzhu/gorm"
	_ "github.com/mattn/go-oci8"
	"github.com/teris-io/shortid"
)

func index(c *gin.Context) {
	_, err := authMid.GetClaimsFromJWT(c)
	if err != nil {
		c.HTML(http.StatusOK, "index.html", gin.H{
			"Title": "AtlasMap",
			"Login": true,
		})
	}
	c.Redirect(http.StatusFound, "/studio/")
}

func ping(c *gin.Context) {
	res := NewRes()
	err := db.DB().Ping()
	if err != nil {
		res.FailErr(c, err)
		return
	}
	dt := time.Now().Format("2006-01-02 15:04:05")
	res.DoneData(c, gin.H{
		"status": fmt.Sprintf(`%s → %s living ~`, dt, currentDB),
	})
}

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
	group := cfgV.GetString("user.group")
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

func updInsetMap(c *gin.Context) {
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

//listStyles list user style
func listStyles(c *gin.Context) {
	res := NewRes()
	res.DoneData(c, pubSet.Styles)
}

//uploadStyle create a style
func uploadStyle(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	// style source
	file, err := c.FormFile("file")
	if err != nil {
		log.Errorf(`uploadStyle, get form: %s; user: %s`, err, id)
		res.Fail(c, 4046)
		return
	}

	styles := cfgV.GetString("assets.styles")
	name := strings.TrimSuffix(file.Filename, filepath.Ext(file.Filename))
	sid, _ := shortid.Generate()
	sid = name + "." + sid
	dst := filepath.Join(styles, sid)
	os.MkdirAll(dst, os.ModePerm)
	dst = filepath.Join(dst, "style.json")

	if err := c.SaveUploadedFile(file, dst); err != nil {
		log.Errorf(`uploadStyle, upload file: %s; user: %s`, err, id)
		res.Fail(c, 5002)
		return
	}
	//更新服务
	pubSet.AddStyle(dst, sid)
	res.Done(c, "")
}

//saveStyle create a style
func upSaveStyle(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	user := c.Param("user")
	sid := c.Param("sid")
	body, err := ioutil.ReadAll(c.Request.Body)
	if err != nil {
		log.Errorf(`updateStyle, get form: %s; user: %s`, err, id)
		res.Fail(c, 5003)
		return
	}
	home := cfgV.GetString("users.home")
	styles := cfgV.GetString("users.styles")
	dst := filepath.Join(home, user, styles, sid, "style.json")
	out := make(map[string]interface{})
	json.Unmarshal(body, &out)
	out["id"] = sid
	out["modified"] = time.Now().Format("2006-01-02 03:04:05 PM")
	out["owner"] = id
	file, _ := json.Marshal(out)
	err = ioutil.WriteFile(dst, file, os.ModePerm)
	if err != nil {
		log.Error(err)
		res.Fail(c, 5002)
		return
	}
	style, ok := pubSet.Styles[sid]
	if !ok {
		log.Errorf("style saved, but id(%s) not exist in the service", sid)
		res.Fail(c, 4044)
		return
	}
	style.Style = body
	res.Done(c, "")
}

//getStyle get user style by id
func getStyle(c *gin.Context) {
	res := NewRes()
	sid := c.Param("sid")
	style, ok := pubSet.Styles[sid]
	if !ok {
		log.Errorf("getStyle, style not exist in the service, sid: %s ^^", sid)
		res.Fail(c, 4044)
		return
	}

	var out map[string]interface{}
	json.Unmarshal(style.Style, &out)

	protoScheme := scheme(c.Request)
	fixURL := func(url string) string {
		if "" == url || !strings.HasPrefix(url, "atlas://") {
			return url
		}
		return strings.Replace(url, "atlas://", protoScheme+"://"+c.Request.Host+"/", -1)
	}

	for k, v := range out {
		switch v.(type) {
		case string:
			//style->sprite
			if "sprite" == k && v != nil {
				path := v.(string)
				out["sprite"] = fixURL(path)
			}
			//style->glyphs
			if "glyphs" == k && v != nil {
				path := v.(string)
				out["glyphs"] = fixURL(path)
			}
		case map[string]interface{}:
			if "sources" == k {
				//style->sources
				sources := v.(map[string]interface{})
				for _, u := range sources {
					source := u.(map[string]interface{})
					if url := source["url"]; url != nil {
						source["url"] = fixURL(url.(string))
					}
				}
			}
		default:
		}
	}
	c.JSON(http.StatusOK, &out)
}

//getSprite get sprite
func getSprite(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	sid := c.Param("sid")
	style, ok := pubSet.Styles[sid]
	if !ok {
		log.Errorf("getSprite, style not exist in the service, sid: %s ^^", sid)
		res.Fail(c, 4044)
		return
	}
	sprite := c.Param("fmt")
	sprite = "sprite" + sprite
	spritePat := `^sprite(@[2]x)?.(?:json|png)$`
	if ok, _ := regexp.MatchString(spritePat, sprite); !ok {
		log.Errorf(`getSprite, get sprite MatchString false, sprite : %s; user: %s ^^`, sprite, id)
		res.Fail(c, 4004)
		return
	}

	if strings.HasSuffix(strings.ToLower(sprite), ".json") {
		c.Writer.Header().Set("Content-Type", "application/json")
	}
	if strings.HasSuffix(strings.ToLower(sprite), ".png") {
		c.Writer.Header().Set("Content-Type", "image/png")
	}

	stylesPath := filepath.Dir(style.URL)
	spriteFile := filepath.Join(stylesPath, sprite)
	file, err := ioutil.ReadFile(spriteFile)
	if err != nil {
		log.Errorf(`getSprite, read sprite file: %v; user: %s ^^`, err, id)
		res.Fail(c, 5002)
		return
	}
	c.Writer.Write(file)
}

func uploadSprite(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	sid := c.Param("sid")

	// Multipart form
	form, err := c.MultipartForm()
	if err != nil {
		log.Errorf(`uploadSprite, get form: %s; user: %s`, err, id)
		res.Fail(c, 400)
		return
	}

	styles := cfgV.GetString("assets.styles")

	files := form.File["files"]
	for _, file := range files {
		dst := filepath.Join(styles, sid, file.Filename)
		if err := c.SaveUploadedFile(file, dst); err != nil {
			log.Errorf(`uploadSprite, upload file: %s; user: %s`, err, id)
			res.Fail(c, 5002)
			return
		}
	}

	res.Done(c, "")
}

//viewStyle load style map
func viewStyle(c *gin.Context) {
	res := NewRes()
	sid := c.Param("sid")
	_, ok := pubSet.Styles[sid]
	if !ok {
		log.Errorf("viewStyle, style not exist in the service, sid: %s ^^", sid)
		res.Fail(c, 4044)
		return
	}
	c.HTML(http.StatusOK, "viewer.html", gin.H{
		"Title": "Viewer",
		"ID":    sid,
		"URL":   strings.TrimSuffix(c.Request.URL.Path, "/"),
	})
}

//listTilesets list user's tilesets
func listTilesets(c *gin.Context) {
	res := NewRes()
	res.DoneData(c, pubSet.Tilesets)
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
	err = pubSet.AddMBTile(dst, tid)
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
	id := c.GetString(identityKey)
	tid := c.Param("tid")
	tileService, ok := pubSet.Tilesets[tid]
	if !ok {
		log.Errorf("tilesets id(%s) not exist in the service", tid)
		res.Fail(c, 4044)
		return
	}
	urlPath := c.Request.URL.Path
	url := fmt.Sprintf("%s%s", rootURL(c.Request), urlPath) //need use user own service set
	tileset := tileService.Mbtiles
	imgFormat := tileset.TileFormatString()
	out := map[string]interface{}{
		"tilejson": "2.1.0",
		"id":       tid,
		"scheme":   "xyz",
		"format":   imgFormat,
		"tiles":    []string{fmt.Sprintf("%s/{z}/{x}/{y}.%s", url, imgFormat)},
		"map":      url + "/",
	}
	metadata, err := tileset.GetInfo()
	if err != nil {
		log.Errorf("getTilejson, get metadata failed: %s; user: %s ^^", err, id)
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

	if tileset.HasUTFGrid() {
		out["grids"] = []string{fmt.Sprintf("%s/{z}/{x}/{y}.json", url)}
	}

	c.JSON(http.StatusOK, out)
}

func viewTile(c *gin.Context) {
	res := NewRes()
	tid := c.Param("tid")
	tileService, ok := pubSet.Tilesets[tid]
	if !ok {
		log.Errorf("tilesets id(%s) not exist in the service", tid)
		res.Fail(c, 4044)
		return
	}

	c.HTML(http.StatusOK, "data.html", gin.H{
		"Title": "PerView",
		"ID":    tid,
		"URL":   strings.TrimSuffix(c.Request.URL.Path, "/"),
		"FMT":   tileService.Mbtiles.TileFormatString(),
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
	tid := c.Param("tid")
	tileService, ok := pubSet.Tilesets[tid]
	if !ok {
		log.Errorf("tilesets id(%s) not exist in the service", tid)
		res.Fail(c, 4044)
		return
	}

	tileset := tileService.Mbtiles

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
		err = tileset.GetTile(tc.z, tc.x, tc.y, &data)
	case isGrid && tileset.HasUTFGrid():
		err = tileset.GetGrid(tc.z, tc.x, tc.y, &data)
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
		switch tileset.TileFormat() {
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
		c.Writer.Header().Set("Content-Type", "application/json")
		if tileset.UTFGridCompression() == ZLIB {
			c.Writer.Header().Set("Content-Encoding", "deflate")
		} else {
			c.Writer.Header().Set("Content-Encoding", "gzip")
		}
	} else {
		c.Writer.Header().Set("Content-Type", tileset.ContentType())
		if tileset.TileFormat() == PBF {
			c.Writer.Header().Set("Content-Encoding", "gzip")
		}
	}
	c.Writer.Write(data)
}

func listFonts(c *gin.Context) {
	res := NewRes()
	res.DoneData(c, pubSet.Fonts)
}

//getGlyphs get glyph pbf
func getGlyphs(c *gin.Context) {
	res := NewRes()
	id := c.GetString(identityKey)
	fonts := c.Param("fontstack")
	rgPBF := c.Param("range")
	rgPBF = strings.ToLower(rgPBF)
	rgPBFPat := `[\d]+-[\d]+.pbf$`
	if ok, _ := regexp.MatchString(rgPBFPat, rgPBF); !ok {
		log.Errorf("getGlyphs, range pattern error; range:%s; user:%s", rgPBF, id)
		res.Fail(c, 4005)
		return
	}
	//should init first
	var fontsPath string
	var callbacks []string
	for k, v := range pubSet.Fonts {
		callbacks = append(callbacks, k)
		fontsPath = v.URL
	}
	fontsPath = filepath.Dir(fontsPath)
	pbfFile := getFontsPBF(fontsPath, fonts, rgPBF, callbacks)
	lastModified := time.Now().UTC().Format("2006-01-02 03:04:05 PM")
	c.Writer.Header().Set("Content-Type", "application/x-protobuf")
	c.Writer.Header().Set("Last-Modified", lastModified)
	c.Writer.Write(pbfFile)
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
	group := cfgV.GetString("user.group")
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
