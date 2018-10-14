package main

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/atlasdatatech/atlasmap/glyphs"

	log "github.com/sirupsen/logrus"
)

//FontService
type FontService struct {
	User  string
	ID    string
	URL   string
	State bool
}

// AddFont interprets filename as mbtiles file which is opened and which will be
// served under "/services/<urlPath>" by Handler(). The parameter urlPath may not be
// nil, otherwise an error is returned. In case the DB cannot be opened the returned
// error is non-nil.
func (s *ServiceSet) AddFont(fontName string, fontID string) error {
	var err error
	if fontName == "" {
		log.Errorf("fontName may not be empty")
		return err
	}

	pbfFile := fontName + "/" + "0-255.pbf"
	if _, err := os.Stat(pbfFile); os.IsNotExist(err) {
		log.Error(pbfFile, " not exists~")
		return err
	}
	// exists and not has errors
	out := &FontService{
		User:  "public",
		ID:    fontID,
		URL:   fontName,
		State: true,
	}
	s.Fonts[fontID] = out
	return nil
}

// ServeFonts returns a StyleService map that combines all style.json files under
// the public directory at baseDir. The styles will all be served under their relative paths
// to baseDir.
func (s *ServiceSet) ServeFonts(baseDir string) (err error) {
	var fontNames []string
	dirs, err := ioutil.ReadDir(baseDir)
	if err != nil {
		log.Error("read fonts baseDir error:", err)
	}
	for _, dir := range dirs {
		if dir.IsDir() {
			fontDir := filepath.Join(baseDir, dir.Name())
			fontNames = append(fontNames, fontDir)
		}
	}

	var fontIDs []string
	for _, fontName := range fontNames {

		fontID := filepath.Base(fontName)
		err = s.AddFont(fontName, fontID)
		if err != nil {
			log.Errorf("add font %q error: %v", fontName, err)
		} else {
			fontIDs = append(fontIDs, fontID)
		}
	}
	log.Infof("From %s successful serve %d fonts -> %v", baseDir, len(fontIDs), fontIDs)

	return nil
}

func reportFont(font string) {
	str := `{"fonts": ["Open Sans Regular","Arial Unicode MS Regular"]}`
	var fonts map[string]interface{}
	json.Unmarshal([]byte(str), &fonts)
	for v := range fonts {
		log.Debug("test range map sigin value:", v)
	}

	for _, v := range fonts {
		if v == font {
			log.Debug("set font ", font, " ture")
		}
	}
}

func getFontsPbf(fontPath string, fontstack string, fontrange string, fallbacks []string) []byte {

	fonts := strings.Split(fontstack, ",")
	log.Debug(fonts)
	contents := make([][]byte, len(fonts))

	var wg sync.WaitGroup

	var getFontPBF func(index int, font string, fallbacks []string)
	getFontPBF = func(index int, font string, fallbacks []string) {
		var fbs []string
		if cap(fallbacks) > 0 {
			for _, v := range fallbacks {
				if v == font {
					continue
				} else {
					fbs = append(fbs, v)
				}
			}
		}
		pbfFile := fontPath + font + fontrange + ".pbf"
		content, err := ioutil.ReadFile(pbfFile)
		if err != nil {
			log.Error("Font not found:" + pbfFile)
			if len(fbs) > 0 {
				sl := strings.Split(font, " ")
				fontStyle := sl[len(sl)-1]
				if fontStyle != "Regular" && fontStyle != "Bold" && fontStyle != "Italic" {
					fontStyle = "Regular"
				}
				fbName1 := "Noto Sans " + fontStyle
				fbName2 := "Open Sans " + fontStyle
				var fbName string
				for _, v := range fbs {
					if fbName1 == v || fbName2 == v {
						fbName = v
						break
					}
				}
				if fbName == "" {
					fbName = fbs[0]
				}

				log.Error("ERROR: Trying to use", fbName, "as a fallback")
				//delete the fbName font in next attempt
				getFontPBF(index, fbName, fbs)
				//all fallbacks failed buf nil
				contents[index] = nil
			}
			log.Error("Font load error: " + pbfFile)
		} else {
			contents[index] = content
		}
		wg.Done()
	}

	for i, font := range fonts {
		wg.Add(1)
		go getFontPBF(i, font, fallbacks)
	}

	wg.Wait()

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
	pbf, err := glyphs.Combine(buffers, fonts)
	if err != nil {
		log.Error("combine buffers error:", err)
	}
	return pbf
}
