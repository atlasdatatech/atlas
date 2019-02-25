package atlas

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/teris-io/shortid"

	"github.com/jinzhu/gorm"
	log "github.com/sirupsen/logrus"
)

//Style 样式库
type Style struct {
	ID        string `json:"id" gorm:"primary_key"`
	Version   string `json:"version"`
	Name      string `json:"name"`
	Summary   string `json:"summary"`
	Owner     string `json:"owner" gorm:"index"`
	BaseID    string `json:"baseID" gorm:"index"`
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Data      []byte `json:"data" gorm:"type:json"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

//StyleService 样式服务
type StyleService struct {
	ID          string      `json:"id" form:"id"`
	Name        string      `json:"name" form:"name"`
	Summary     string      `json:"summary" form:"summary"`
	Owner       string      `json:"owner" form:"owner"`
	State       bool        `json:"state" form:"state"`
	Path        string      `json:"path"`
	Thumbnail   []byte      `json:"thumbnail" form:"thumbnail"`
	SpritePNG   []byte      `json:"spritePNG" form:"spritePNG"`
	SpriteJSON  []byte      `json:"spriteJSON" form:"spriteJSON"`
	SpritePNG2  []byte      `json:"spritePNG2" form:"spritePNG2"`
	SpriteJSON2 []byte      `json:"spriteJSON2" form:"spriteJSON2"`
	Data        interface{} `form:"data" json:"data"`
}

//转为存储
func (ss *StyleService) toStyle() *Style {
	out := &Style{
		ID:      ss.ID,
		Name:    ss.Name,
		Owner:   ss.Owner,
		Summary: ss.Summary,
		Path:    ss.Path,
	}
	var err error
	if ss.Data != nil {
		out.Data, err = json.Marshal(ss.Data)
		if err != nil {
			log.Errorf("marshal style json error, details:%s", err)
		}
	}
	return out
}

//Simplify 精简服务列表
func (ss *StyleService) Simplify() *StyleService {
	out := &StyleService{
		ID:        ss.ID,
		Name:      ss.Name,
		Summary:   ss.Summary,
		Owner:     ss.Owner,
		State:     ss.State,
		Path:      ss.Path,
		Thumbnail: ss.Thumbnail,
	}
	return out
}

//Copy 服务拷贝
func (ss *StyleService) Copy() *StyleService {
	out := &StyleService{
		ID:          ss.ID,
		Name:        ss.Name,
		Summary:     ss.Summary,
		Owner:       ss.Owner,
		Thumbnail:   ss.Thumbnail,
		SpritePNG:   ss.SpritePNG,
		SpriteJSON:  ss.SpriteJSON,
		SpritePNG2:  ss.SpritePNG2,
		SpriteJSON2: ss.SpriteJSON2,
		Data:        ss.Data,
	}
	return out
}

//PackStyle 打包样式
func (ss *StyleService) PackStyle() *bytes.Buffer {
	// Create a buffer to write our archive to.
	buf := new(bytes.Buffer)
	// Create a new zip archive.
	w := zip.NewWriter(buf)

	// Add some files to the archive.
	style, err := json.Marshal(ss.Data)
	if err != nil {
		log.Errorf("marshal style json error, details:%s", err)
	}
	f, err := w.Create("style.json")
	if err != nil {
		log.Error(err)
		return buf
	}
	_, err = f.Write(style)
	if err != nil {
		log.Error(err)
	}

	dir := filepath.Join(ss.Path, "icons")
	items, err := ioutil.ReadDir(dir)
	if err != nil {
		log.Error(err)
		return buf
	}
	for _, item := range items {
		if item.IsDir() {
			continue
		}
		file := item.Name()
		buf, err := ioutil.ReadFile(filepath.Join(dir, file))
		if err != nil {
			log.Error(err)
			continue
		}
		f, err := w.Create(filepath.Join("icons", file))
		if err != nil {
			log.Error(err)
			continue
		}

		_, err = f.Write(buf)
		if err != nil {
			log.Error(err)
		}

	}

	// Make sure to check the error on Close.
	err = w.Close()
	if err != nil {
		log.Fatal(err)
	}
	err = ioutil.WriteFile(filepath.Join(ss.Path, "style.zip"), buf.Bytes(), os.ModePerm)
	if err != nil {
		fmt.Printf("write zip style file failed,details: %s\n", err)
	}
	return buf
}

// LoadStyle 加载样式.
func LoadStyle(styleDir string) (*Style, error) {
	styleFile := filepath.Join(styleDir, "style.json")
	fStat, err := os.Stat(styleFile)
	if err != nil {
		log.Errorf(`LoadStyle, read style file info error, details: %s`, err)
		return nil, err
	}
	//read style.json
	styleBuf, err := ioutil.ReadFile(styleFile)
	if err != nil {
		log.Error(err)
		return nil, err
	}

	name := filepath.Base(styleDir)
	id, _ := shortid.Generate()
	out := &Style{
		ID:        id,
		Version:   "8",
		Name:      name,
		Owner:     ATLAS,
		BaseID:    styleFile, //should not add / at the end
		Path:      styleDir,
		Size:      fStat.Size(),
		UpdatedAt: fStat.ModTime(),
		Data:      styleBuf,
	}

	return out, nil
}

//转为服务
func (s *Style) toService() *StyleService {
	out := &StyleService{
		ID:      s.ID,
		Name:    s.Name,
		Summary: s.Summary,
		Owner:   s.Owner,
		Path:    s.Path,
		State:   true,
	}

	if len(s.Data) > 0 {
		err := json.Unmarshal(s.Data, &out.Data)
		if err != nil {
			log.Errorf("unmarshal style json error, details:%s", err)
		}
	}
	items, err := ioutil.ReadDir(s.Path)
	if err != nil {
		return out
	}
	for _, item := range items {
		if item.IsDir() {
			continue
		}
		name := item.Name()
		lname := strings.ToLower(name)
		switch lname {
		case "thumbnail.jpg":
			f := filepath.Join(s.Path, name)
			buf, err := ioutil.ReadFile(f)
			if err != nil {
				log.Error(err)
			}
			out.Thumbnail = buf
		case "sprite.png":
			f := filepath.Join(s.Path, name)
			buf, err := ioutil.ReadFile(f)
			if err != nil {
				log.Error(err)
			}
			out.SpritePNG = buf
		case "sprite@2x.png":
			f := filepath.Join(s.Path, name)
			buf, err := ioutil.ReadFile(f)
			if err != nil {
				log.Error(err)
			}
			out.SpritePNG2 = buf
		case "sprite.json":
			f := filepath.Join(s.Path, name)
			buf, err := ioutil.ReadFile(f)
			if err != nil {
				log.Error(err)
			}
			out.SpriteJSON = buf
		case "sprite@2x.json":
			f := filepath.Join(s.Path, name)
			buf, err := ioutil.ReadFile(f)
			if err != nil {
				log.Error(err)
			}
			out.SpriteJSON2 = buf
		}
	}

	return out
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
