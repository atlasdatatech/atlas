package main

import (
	"fmt"
	"io/ioutil"
	"path/filepath"

	log "github.com/sirupsen/logrus"
)

//StyleService struct for styles
type StyleService struct {
	ID    string
	URL   string
	Hash  string
	State bool
	Style []byte
}

// CreateStyleService creates a new StyleService instance.
// Connection is closed by runtime on application termination or by calling
// its Close() method.
func CreateStyleService(styleFile string, styleID string) (*StyleService, error) {
	//read style.json
	styleBuf, err := ioutil.ReadFile(styleFile)
	if err != nil {
		log.Error(err)
		return nil, err
	}
	// //if has lager num of tilesets and fonts, should load style first and report fonts and mbtiles for only serve
	// var style map[string]interface{}
	// err = json.Unmarshal(styleBuf, &style)
	// if err != nil {
	// 	log.Error(err)
	// 	return nil, err
	// }
	// // sufPat := `(?i:^(http(s)?:)?)\/\/`
	// for k, v := range style {
	// 	switch vv := v.(type) {
	// 	case []interface{}:
	// 		//style->layers
	// 		if "layers" == k {
	// 			for _, u := range vv {
	// 				layer := u.(map[string]interface{})
	// 				if "symbol" == layer["type"] {
	// 					if layout := layer["layout"]; layout != nil {
	// 						layoutMap := layout.(map[string]interface{})
	// 						if fonts := layoutMap["text-font"]; fonts != nil {
	// 							for _, font := range fonts.([]interface{}) {
	// 								if font != nil {
	// 									reportFont(font.(string))
	// 								}
	// 							}
	// 						} else {
	// 							reportFont("Open Sans Regular")
	// 							reportFont("Arial Unicode MS Regular")
	// 						}
	// 					}
	// 				}
	// 			}
	// 		}
	// 	case map[string]interface{}:
	// 		if "sources" == k {
	// 			//style->sources
	// 			sources := v.(map[string]interface{})
	// 			for _, u := range sources {
	// 				source := u.(map[string]interface{})
	// 				url := source["url"]
	// 				//url 非空且为mbtiles:数据源
	// 				if url != nil && strings.HasPrefix(url.(string), "mbtiles:") {
	// 					mbtilesFile := strings.TrimPrefix(url.(string), "mbtiles://")
	// 					fromData := strings.HasPrefix(mbtilesFile, "{") && strings.HasSuffix(mbtilesFile, "}")
	// 					if fromData {
	// 						mbtilesFile = strings.TrimSuffix(strings.TrimPrefix(mbtilesFile, "{"), "}")
	// 					}
	// 					var identifier = reportMbtiles(mbtilesFile, fromData)
	// 					source["url"] = "local://tilesets/public/" + identifier
	// 				}
	// 			}
	// 		}
	// 	default:
	// 	}
	// }
	// buf, err := json.Marshal(style)

	out := &StyleService{
		ID:    styleID,
		URL:   styleFile, //should not add / at the end
		State: true,
		Style: styleBuf,
	}

	return out, nil
}

// AddStyle interprets filename as mbtiles file which is opened and which will be
// served under "/services/<urlPath>" by Handler(). The parameter urlPath may not be
// nil, otherwise an error is returned. In case the DB cannot be opened the returned
// error is non-nil.
func (s *ServiceSet) AddStyle(styleFile string, styleID string) error {
	var err error
	if "" == styleID && "" == styleFile {
		return fmt.Errorf("styleFile or styleID parameter may not be empty")
	}
	ss, err := CreateStyleService(styleFile, styleID)
	if err != nil {
		return fmt.Errorf("could not load mbtiles file %q: %v", styleFile, err)
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
			log.Errorf("ServeStyles, unable to extract URL path for %s: %v", s.User, err)
		}
		styleID := filepath.Dir(subpath)
		err = s.AddStyle(styleFile, styleID)
		if err != nil {
			log.Errorf(`ServeStyles, add style: %s; user: %s ^^`, err, s.User)
		}
	}
	log.Infof("ServeStyles,serve %d styles for %s ~", len(s.Styles), s.User)
	return nil
}
