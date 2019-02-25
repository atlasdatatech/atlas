package atlas

import (
	"compress/gzip"
	"database/sql"
	"regexp"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/gin-gonic/gin"
)

//listFonts 获取字体服务列表
func listFonts(c *gin.Context) {
	res := NewRes()
	var fonts []*FontService
	is, ok := pubSet.Load(ATLAS)
	if ok {
		is.(*ServiceSet).F.Range(func(_, v interface{}) bool {
			fonts = append(fonts, v.(*FontService))
			return true
		})
	}
	res.DoneData(c, fonts)
}

//getGlyphs 获取字体pbf,需区别于数据pbf,开启gzip压缩以加快传输,get glyph pbf.
func getGlyphs(c *gin.Context) {
	res := NewRes()
	uid := c.GetString(identityKey)
	is, ok := pubSet.Load(ATLAS)
	if !ok {
		res.Fail(c, 4045)
		return
	}
	uset := is.(*ServiceSet)
	fontstack := c.Param("fontstack")
	fontrange := c.Param("range")
	// fontrange = strings.ToLower(fontrange)
	rgPat := `[\d]+-[\d]+.pbf$`
	if ok, _ := regexp.MatchString(rgPat, fontrange); !ok {
		log.Errorf("getGlyphs, range pattern error; range:%s; user:%s", fontrange, uid)
		res.Fail(c, 4005)
		return
	}

	var pbf []byte
	fonts := strings.Split(fontstack, ",")
	switch len(fonts) {
	case 0:
		log.Errorf("getGlyphs, fontstack is nil ~")
		res.Fail(c, 4005)
		return
	case 1:
		iv, ok := uset.F.Load(fontstack)
		if !ok {
			log.Errorf("getGlyphs, fontstack is not found ~")
			res.Fail(c, 4005)
			return
		}
		data, err := iv.(*FontService).Font(fontrange)
		if err != nil {
			log.Errorf("getGlyphs, get pbf font error, details:%s ~", err)
			res.Fail(c, 4005)
			return
		}
		pbf = data

	default: //multi fonts

		var fss []*FontService
		hasdefault := false
		haslost := false
		for _, font := range fonts {
			if font == DEFAULTFONT {
				hasdefault = true
			}

			iv, ok := uset.F.Load(font)
			if !ok {
				haslost = true
				continue
			}
			fss = append(fss, iv.(*FontService))
		}
		//没有默认字体且有丢失字体,则加载默认字体
		if !hasdefault && haslost {
			iv, ok := uset.F.Load(DEFAULTFONT)
			if ok {
				fs, ok := iv.(*FontService)
				if ok {
					fss = append(fss, fs)
				}
			}
		}

		contents := make([][]byte, len(fss))
		var wg sync.WaitGroup
		//need define func, can't use sugar ":="
		getFontPBF := func(fs *FontService, fontrange string, data []byte) {
			//fallbacks unchanging
			defer wg.Done()
			err := fs.DB.QueryRow("select data from fonts where range = ?", fontrange).Scan(&data)
			if err != nil {
				log.Error(err)
				if err == sql.ErrNoRows {
					return
				}
				return
			}
		}
		for i, fs := range fss {
			wg.Add(1)
			go getFontPBF(fs, fontrange, contents[i])
		}
		wg.Wait()

		//if  getFontPBF can't get content,the buffer array is nil, remove the nils
		var buffers [][]byte
		for i, buf := range contents {
			if nil == buf {
				fonts = append(fonts[:i], fonts[i+1:]...)
				continue
			}
			buffers = append(buffers, buf)
		}
		if len(buffers) != len(fonts) {
			log.Error("len(buffers) != len(fonts)")
		}
		if 0 == len(buffers) {
			log.Errorf("getGlyphs, empty pbf font ~")
			res.Fail(c, 4005)
			return
		}
		if 1 == len(buffers) {
			pbf = buffers[0]
		} else {
			c, err := Combine(buffers, fonts)
			if err != nil {
				log.Error("combine buffers error:", err)
				pbf = buffers[0]
			} else {
				pbf = c
			}
		}
	}

	lastModified := time.Now().UTC().Format("2006-01-02 03:04:05 PM")
	c.Header("Content-Type", "application/x-protobuf")
	c.Header("Last-Modified", lastModified)
	gz, err := gzip.NewWriterLevel(c.Writer, gzip.DefaultCompression)
	if err != nil {
		c.Writer.Write(pbf)
		return
	}
	defer func() {
		c.Header("Content-Length", "0")
		gz.Close()
	}()
	gz.Write(pbf)
	c.Header("Content-Encoding", "gzip")
	c.Header("Vary", "Accept-Encoding")
	return
}
