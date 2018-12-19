package main

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"

	log "github.com/sirupsen/logrus"
)

//FontService struct for font service
type FontService struct {
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

	pbfFile := fontName + "/" + "0-255.pbf" //use 0-255.pbf test the font can be service
	if _, err := os.Stat(pbfFile); os.IsNotExist(err) {
		log.Error(pbfFile, "not exists~")
		return err
	}
	// exists and not has errors
	out := &FontService{
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
		log.Error("ServeFonts, read fonts baseDir error:", err)
	}
	for _, dir := range dirs {
		if dir.IsDir() {
			fontDir := filepath.Join(baseDir, dir.Name())
			fontNames = append(fontNames, fontDir)
		}
	}

	for _, fontName := range fontNames {
		fontID := filepath.Base(fontName)
		err = s.AddFont(fontName, fontID)
		if err != nil {
			log.Errorf("ServeFonts, add font %q error: %v", fontName, err)
		}
	}
	log.Infof("ServeFonts, loading %d fonts-> %v ", len(s.Fonts), s.Fonts)
	return nil
}

//reportFont if has large number of fonts ,should only serve for these reported
func (s *ServiceSet) reportFont(font string) {
	//set a array ServeFonts should serve only
	for k, v := range s.Fonts {
		if k == font && !v.State {
			// s.ServeFont(font) //simply set State to true
		}
	}
}

func getFontsPBF(fontPath string, fontstack string, fontrange string, fallbacks []string) []byte {
	fonts := strings.Split(fontstack, ",")
	contents := make([][]byte, len(fonts))
	var wg sync.WaitGroup
	//need define func, can't use sugar ":="
	var getFontPBF func(index int, font string, fallbacks []string)
	getFontPBF = func(index int, font string, fallbacks []string) {
		//fallbacks unchanging
		defer wg.Done()
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
		pbfFile := filepath.Join(fontPath, font, fontrange)
		content, err := ioutil.ReadFile(pbfFile)
		if err != nil {
			log.Error(err)
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

				log.Warnf(`trying to use '%s' as a fallback ^`, fbName)
				//delete the fbName font in next attempt
				wg.Add(1)
				getFontPBF(index, fbName, fbs)
			}
		} else {
			contents[index] = content
		}
	}

	for i, font := range fonts {
		wg.Add(1)
		go getFontPBF(i, font, fallbacks)
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
		return nil
	}
	if 1 == len(buffers) {
		return buffers[0]
	}
	pbf, err := Combine(buffers, fonts)
	if err != nil {
		log.Error("combine buffers error:", err)
	}
	return pbf
}
