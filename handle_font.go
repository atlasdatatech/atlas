package main

import (
	"path/filepath"
	"regexp"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/gin-gonic/gin"
)

func listFonts(c *gin.Context) {
	res := NewRes()
	var fonts []*FontService
	pubSet.F.Range(func(_, v interface{}) bool {
		fonts = append(fonts, v.(*FontService))
		return true
	})
	res.DoneData(c, fonts)
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

	pubSet.F.Range(func(k, v interface{}) bool {
		callbacks = append(callbacks, k.(string))
		fontsPath = v.(*FontService).URL
		return true
	})

	fontsPath = filepath.Dir(fontsPath)
	pbfFile := getFontsPBF(fontsPath, fonts, rgPBF, callbacks)
	lastModified := time.Now().UTC().Format("2006-01-02 03:04:05 PM")
	c.Writer.Header().Set("Content-Type", "application/x-protobuf")
	c.Writer.Header().Set("Last-Modified", lastModified)
	c.Writer.Write(pbfFile)
}
