package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"regexp"
	"strings"

	log "github.com/sirupsen/logrus"
)

//StyleService
type StyleService struct {
	User  string
	ID    string
	URL   string
	Hash  string
	State bool
	Style *string
}

// CreateStyleService creates a new StyleService instance.
// Connection is closed by runtime on application termination or by calling
// its Close() method.
func CreateStyleService(styleFile string, styleID string) (*StyleService, error) {
	//read style.json
	file, err := ioutil.ReadFile(styleFile)
	if err != nil {
		log.Error(err)
		return nil, err
	}
	var style map[string]interface{}
	err = json.Unmarshal(file, &style)
	if err != nil {
		log.Error(err)
		return nil, err
	}

	sufPat := `(?i:^(http(s)?:)?)\/\/`
	for k, v := range style {
		switch vv := v.(type) {
		case string:
			//style->sprite
			if "sprite" == k && v != nil {
				path := v.(string)
				if ok, _ := regexp.MatchString(sufPat, path); !ok {
					style["sprite"] = "local://styles/public/" + styleID + "/sprite"
				}
			}
			//style->glyphs
			if "glyphs" == k && v != nil {
				path := v.(string)
				if ok, _ := regexp.MatchString(sufPat, path); !ok {
					style["glyphs"] = "local://fonts/{fontstack}/{range}.pbf"
				}
			}
		case []interface{}:
			//style->layers
			if "layers" == k {
				for _, u := range vv {
					layer := u.(map[string]interface{})
					if "symbol" == layer["type"] {
						if layout := layer["layout"]; layout != nil {
							layoutMap := layout.(map[string]interface{})
							if fonts := layoutMap["text-font"]; fonts != nil {
								for _, font := range fonts.([]interface{}) {
									if font != nil {
										reportFont(font.(string))
									}
								}
							} else {
								reportFont("Open Sans Regular")
								reportFont("Arial Unicode MS Regular")
							}
						}
					}
				}
			}
		case map[string]interface{}:
			if "sources" == k {
				//style->sources
				sources := v.(map[string]interface{})
				for _, u := range sources {
					source := u.(map[string]interface{})
					url := source["url"]
					//url 非空且为mbtiles:数据源
					if url != nil && strings.HasPrefix(url.(string), "mbtiles:") {
						mbtilesFile := strings.TrimPrefix(url.(string), "mbtiles://")
						fromData := strings.HasPrefix(mbtilesFile, "{") && strings.HasSuffix(mbtilesFile, "}")
						if fromData {
							mbtilesFile = strings.TrimSuffix(strings.TrimPrefix(mbtilesFile, "{"), "}")
						}

						var identifier = reportMbtiles(mbtilesFile, fromData)
						source["url"] = "local://tilesets/public/" + identifier
					}
				}
			}
		default:
		}
	}

	f, err := json.Marshal(style)
	styleJSON := string(f)

	out := &StyleService{
		Style: &styleJSON,
		User:  "public",
		ID:    styleID,
		URL:   "/styles/public/" + styleID, //should not add / at the end
	}

	return out, nil

}

// AddStyle interprets filename as mbtiles file which is opened and which will be
// served under "/services/<urlPath>" by Handler(). The parameter urlPath may not be
// nil, otherwise an error is returned. In case the DB cannot be opened the returned
// error is non-nil.
func (s *ServiceSet) AddStyle(styleFile string, styleID string) error {
	var err error
	if styleID == "" {
		return fmt.Errorf("path parameter may not be empty")
	}
	ss, err := CreateStyleService(styleFile, styleID)
	if err != nil {
		return fmt.Errorf("could not open mbtiles file %q: %v", styleFile, err)
	}
	s.Styles[styleID] = ss
	return nil
}

// ServeStyles returns a StyleService map that combines all style.json files under
// the public directory at baseDir. The styles will all be served under their relative paths
// to baseDir.
func (s *ServiceSet) ServeStyles(baseDir string) (err error) {
	var fileNames []string
	dirs, err := ioutil.ReadDir(baseDir)
	if err != nil {
		log.Error(err)
	}
	for _, dir := range dirs {
		if dir.IsDir() {
			path := filepath.Join(baseDir, dir.Name())
			files, err := ioutil.ReadDir(path)
			if err != nil {
				log.Error(err)
			}
			for _, file := range files {
				ok, err := filepath.Match("style.json", file.Name())
				if ok {
					fileNames = append(fileNames, filepath.Join(path, file.Name()))
				}
				if err != nil {
					log.Error(err.Error())
				}
			}
		}
	}

	for _, styleFile := range fileNames {
		subpath, err := filepath.Rel(baseDir, styleFile)
		if err != nil {
			log.Errorf("unable to extract URL path for %q: %v", styleFile, err)
		}
		styleID := filepath.Dir(subpath)
		err = s.AddStyle(styleFile, styleID)
		if err != nil {
			log.Error(err)
		}
	}
	log.Infof("New from %s successful, tol %d", baseDir, len(fileNames))
	return nil
}
