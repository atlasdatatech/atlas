package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jinzhu/gorm"
	log "github.com/sirupsen/logrus"
)

//Style 样式库
type Style struct {
	ID          string `json:"id" gorm:"primary_key"`
	Version     string `json:"version"`
	Name        string `json:"name"`
	Owner       string `json:"owner" gorm:"index"`
	BaseID      string `json:"baseID" gorm:"index"`
	Size        int64  `json:"size"`
	SpritePNG   []byte `json:"spritePNG" gorm:"column:sprite_png"`
	SpriteJSON  []byte `json:"spriteJSON" gorm:"column:sprite_json"`
	SpritePNG2  []byte `json:"spritePNG2" gorm:"column:sprite_png2"`
	SpriteJSON2 []byte `json:"spriteJSON2" gorm:"column:sprite_json2"`
	Data        []byte `json:"data" gorm:"type:json"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

//StyleService 样式服务
type StyleService struct {
	ID          string      `json:"id" form:"id"`
	Name        string      `json:"name" form:"name"`
	Owner       string      `json:"owner" form:"owner"`
	Hash        string      `json:"hash" form:"hash"`
	State       bool        `json:"state" form:"state"`
	SpritePNG   []byte      `json:"spritePNG" form:"spritePNG"`
	SpriteJSON  interface{} `json:"spriteJSON" form:"spriteJSON"`
	SpritePNG2  []byte      `json:"spritePNG2" form:"spritePNG2"`
	SpriteJSON2 interface{} `json:"spriteJSON2" form:"spriteJSON2"`
	Data        interface{} `form:"data" json:"data"`
}

//转为存储
func (ss *StyleService) toStyle() *Style {
	out := &Style{
		ID:         ss.ID,
		Name:       ss.Name,
		SpritePNG:  ss.SpritePNG,
		SpritePNG2: ss.SpritePNG2,
	}
	var err error
	out.SpriteJSON, err = json.Marshal(ss.SpriteJSON)
	if err != nil {
		log.Errorf("marshal sprite json error, details:%s", err)
	}
	out.SpriteJSON2, err = json.Marshal(ss.SpriteJSON2)
	if err != nil {
		log.Errorf("marshal sprite@2x json error, details:%s", err)
	}
	out.Data, err = json.Marshal(ss.Data)
	if err != nil {
		log.Errorf("marshal style json error, details:%s", err)
	}
	return out
}

//转为服务
func (s *Style) toService() *StyleService {
	out := &StyleService{
		ID:         s.ID,
		Name:       s.Name,
		SpritePNG:  s.SpritePNG,
		SpritePNG2: s.SpritePNG2,
	}
	err := json.Unmarshal(s.SpriteJSON, &out.SpriteJSON)
	if err != nil {
		log.Errorf("unmarshal sprite json error, details:%s", err)
	}
	err = json.Unmarshal(s.SpriteJSON2, out.SpriteJSON2)
	if err != nil {
		log.Errorf("unmarshal sprite@2x json error, details:%s", err)
	}
	err = json.Unmarshal(s.Data, &out.Data)
	if err != nil {
		log.Errorf("unmarshal style json error, details:%s", err)
	}
	return out
}

//服务拷贝
func (ss *StyleService) copy() *StyleService {
	out := &StyleService{
		ID:          ss.ID,
		Name:        ss.Name,
		SpritePNG:   ss.SpritePNG,
		SpriteJSON:  ss.SpriteJSON,
		SpritePNG2:  ss.SpritePNG2,
		SpriteJSON2: ss.SpriteJSON2,
		Data:        ss.Data,
	}
	return out
}

// LoadStyle 加载样式.
func LoadStyle(styleFile string) (*Style, error) {
	fStat, err := os.Stat(styleFile)
	if err != nil {
		log.Errorf(`ServeStyle, read file stat info error, details: %s`, err)
		return nil, err
	}
	//read style.json
	styleBuf, err := ioutil.ReadFile(styleFile)
	if err != nil {
		log.Error(err)
		return nil, err
	}

	paths := filepath.SplitList(styleFile)
	log.Info(paths)
	pos := len(paths) - 2
	if pos < 0 {
		return nil, fmt.Errorf(`parse style id error`)
	}
	id := paths[pos]
	// name := strings.Split(id, ".")[0]

	dir := filepath.Dir(styleFile)

	items, err := ioutil.ReadDir(dir)
	if err != nil {
		log.Error(err)
		return nil, err
	}
	out := &Style{
		ID:        id,
		BaseID:    styleFile, //should not add / at the end
		Size:      fStat.Size(),
		UpdatedAt: fStat.ModTime(),
		Data:      styleBuf,
	}
	for _, item := range items {
		if item.IsDir() {
			continue
		}
		name := item.Name()
		lname := strings.ToLower(name)
		switch lname {
		case "sprite.png":
			f := filepath.Join(dir, name)
			buf, err := ioutil.ReadFile(f)
			if err != nil {
				log.Error(err)
			}
			out.SpritePNG = buf
		case "sprite@2x.png":
			f := filepath.Join(dir, name)
			buf, err := ioutil.ReadFile(f)
			if err != nil {
				log.Error(err)
			}
			out.SpritePNG2 = buf
		case "sprite.json":
			f := filepath.Join(dir, name)
			buf, err := ioutil.ReadFile(f)
			if err != nil {
				log.Error(err)
			}
			out.SpriteJSON = buf
		case "sprite@2x.json":
			f := filepath.Join(dir, name)
			buf, err := ioutil.ReadFile(f)
			if err != nil {
				log.Error(err)
			}
			out.SpriteJSON2 = buf
		}
	}

	return out, nil

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

}

//UpInsert 创建更新样式存储
//create or update upload data file info into database
func (s *Style) UpInsert() error {
	if s == nil {
		return fmt.Errorf("style may not be nil")
	}
	tmp := &Style{}
	err := db.Where("id = ?", s.ID).First(tmp).Error
	if err != nil {
		if gorm.IsRecordNotFoundError(err) {
			err = db.Create(s).Error
			if err != nil {
				return err
			}
		}
		return err
	}
	err = db.Model(&Style{}).Update(s).Error
	if err != nil {
		return err
	}
	return nil
}
