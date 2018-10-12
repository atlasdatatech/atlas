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
	Style string
	URL   string
	Hash  string
}

// CreateStyleService creates a new StyleService instance.
// Connection is closed by runtime on application termination or by calling
// its Close() method.
func CreateStyleService(filename string) (*StyleService, error) {
	//read style.json
	file, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	var style map[string]interface{}
	err = json.Unmarshal(file, &style)
	if err != nil {
		return nil, err
	}

	var spritePath string
	_, id := filepath.Split(filepath.Dir(filename))
	sufPat := `(?i:^(http(s)?:)?)\/\/`
	for k, v := range style {
		switch vv := v.(type) {
		case string:
			//style->sprite
			if "sprite" == k && v != nil {
				path := v.(string)
				if ok, _ := regexp.MatchString(sufPat, path); !ok {
					spritePath = strings.Replace(path, "{style}", id, -1)
					style["sprite"] = "local://styles/public/" + id + "/sprite"
				}
			}
			//style->glyphs
			if "glyphs" == k && v != nil {
				path := v.(string)
				if ok, _ := regexp.MatchString(sufPat, path); !ok {
					style["glyphs"] = "local://fonts/{fontstack}/{range}.pbf"
				}
			}
		case float64:
			//style->glyphs
			if "version" == k {
				fmt.Println(v)
			}
		case []interface{}:
			//style->layers
			if "layers" == k {
				for i, u := range vv {
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
					log.Debug("layer num:", i)
				}
			}
		case map[string]interface{}:
			if "sources" == k {
				//style->sources
				sources := v.(map[string]interface{})
				for i, u := range sources {
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
					log.Debug("sources:", i)
				}
			}
		default:
			fmt.Println(k, "is of a type I don't know how to handle")
		}
	}

	log.Debug(spritePath)

	f, err := json.Marshal(style)
	fmt.Println(string(f))

	out := &StyleService{
		Style: string(f),
		User:  "public",
		ID:    id,
		URL:   "/styles/public/" + id, //should not add / at the end
	}

	return out, nil

}

// AddStyle interprets filename as mbtiles file which is opened and which will be
// served under "/services/<urlPath>" by Handler(). The parameter urlPath may not be
// nil, otherwise an error is returned. In case the DB cannot be opened the returned
// error is non-nil.
func (s *ServiceSet) AddStyle(filename string, urlPath string) error {
	var err error
	if urlPath == "" {
		return fmt.Errorf("path parameter may not be empty")
	}
	ss, err := CreateStyleService(filename)
	if err != nil {
		return fmt.Errorf("could not open mbtiles file %q: %v", filename, err)
	}
	s.Styles[urlPath] = ss
	return nil
}

// ServeStyles returns a StyleService map that combines all style.json files under
// the public directory at baseDir. The styles will all be served under their relative paths
// to baseDir.
func (s *ServiceSet) ServeStyles(baseDir string) (err error) {
	var filenames []string
	dirs, err := ioutil.ReadDir(baseDir)
	if err != nil {
		log.Warn("read styles baseDir error:", err)
	}
	for _, dir := range dirs {
		if dir.IsDir() {
			path := filepath.Join(baseDir, dir.Name())
			files, err := ioutil.ReadDir(path)
			if err != nil {
				log.Fatal(err)
			}
			for _, file := range files {
				ok, err := filepath.Match("style.json", file.Name())
				if ok {
					filenames = append(filenames, filepath.Join(path, file.Name()))
				}
				if err != nil {
					log.Println(err.Error())
				}
			}
		}
	}

	if err != nil {
		return fmt.Errorf("unable to scan styles: %v", err)
	}

	for _, filename := range filenames {
		subpath, err := filepath.Rel(baseDir, filename)
		if err != nil {
			return fmt.Errorf("unable to extract URL path for %q: %v", filename, err)
		}
		id := strings.ToLower(filepath.Dir(subpath))
		err = s.AddStyle(filename, id)
		if err != nil {
			return err
		}
	}
	log.Infof("New from %s successful, tol %d", baseDir, len(filenames))
	return nil
}

func reportMbtiles(mbtile string, fromData bool) string {
	var dataItemID string
	str := `{"puhui": {"mbtiles": "puhui.mbtiles"}, "china": {"mbtiles": "china.mbtiles"}}`
	var datas map[string]interface{}
	json.Unmarshal([]byte(str), &datas)
	for k, v := range datas {
		if fromData {
			if k == mbtile {
				dataItemID = k
			}
		} else {
			vv := v.(map[string]interface{})
			if vv["mbtiles"] == mbtile {
				dataItemID = k
			}
		}
	}

	if dataItemID != "" { // mbtiles exist in the data config
		return dataItemID
	} else if fromData {
		log.Errorf(`ERROR: data "%s" not found!`, mbtile)
		return ""
	} else { //generate data config ?
		// var id = mbtile.substr(0, mbtiles.lastIndexOf('.')) || mbtile
		// while (data[id]) id += '_';
		// data[id] = {
		//   'mbtiles': mbtiles
		// };
		// return id;
		return ""
	}
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
