package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-spatial/tegola/mapbox/style"

	"github.com/fogleman/gg"
	"github.com/jinzhu/gorm"
	log "github.com/sirupsen/logrus"
)

//Style 样式库
type Style struct {
	ID        string          `json:"id" gorm:"primary_key"`
	Version   string          `json:"version"`
	Name      string          `json:"name" gorm:"index"`
	Summary   string          `json:"summary"`
	Owner     string          `json:"owner" gorm:"index"`
	Public    bool            `json:"public"`
	BaseID    string          `json:"baseID" gorm:"index"`
	Path      string          `json:"path"`
	Size      int64           `json:"size"`
	URL       string          `json:"url"`
	Status    bool            `json:"status"`
	Data      json.RawMessage `json:"data" gorm:"type:json"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

//Service 加载服务
func (s *Style) Service() *Style {
	var output style.Root
	// read the response body
	if err := json.Unmarshal(s.Data, &output); err != nil {
		fmt.Println(err)
	}
	fmt.Printf("%v", output)
	// if len(s.Data) > 0 {
	// 	err := json.Unmarshal(s.Data, &out.Data)
	// 	if err != nil {
	// 		log.Errorf("unmarshal style json error, details:%s", err)
	// 	}
	// }
	// items, err := ioutil.ReadDir(s.Path)
	// if err != nil {
	// 	return out
	// }
	// for _, item := range items {
	// 	if item.IsDir() {
	// 		continue
	// 	}
	// 	name := item.Name()
	// 	lname := strings.ToLower(name)
	// 	switch lname {
	// 	case "thumbnail.jpg":
	// 		f := filepath.Join(s.Path, name)
	// 		buf, err := ioutil.ReadFile(f)
	// 		if err != nil {
	// 			log.Error(err)
	// 		}
	// 		out.Thumbnail = buf
	// 	case "sprite.png":
	// 		f := filepath.Join(s.Path, name)
	// 		buf, err := ioutil.ReadFile(f)
	// 		if err != nil {
	// 			log.Error(err)
	// 		}
	// 		out.SpritePNG = buf
	// 	case "sprite@2x.png":
	// 		f := filepath.Join(s.Path, name)
	// 		buf, err := ioutil.ReadFile(f)
	// 		if err != nil {
	// 			log.Error(err)
	// 		}
	// 		out.SpritePNG2 = buf
	// 	case "sprite.json":
	// 		f := filepath.Join(s.Path, name)
	// 		buf, err := ioutil.ReadFile(f)
	// 		if err != nil {
	// 			log.Error(err)
	// 		}
	// 		out.SpriteJSON = buf
	// 	case "sprite@2x.json":
	// 		f := filepath.Join(s.Path, name)
	// 		buf, err := ioutil.ReadFile(f)
	// 		if err != nil {
	// 			log.Error(err)
	// 		}
	// 		out.SpriteJSON2 = buf
	// 	}
	// }
	s.Status = true
	return s
}

//Copy 服务拷贝
func (s *Style) Copy() *Style {
	out := *s
	out.Data = make([]byte, len(s.Data))
	copy(out.Data, s.Data)
	return &out
}

//PackStyle 打包样式
func (s *Style) PackStyle() *bytes.Buffer {
	// Create a buffer to write our archive to.
	buf := new(bytes.Buffer)
	// Create a new zip archive.
	w := zip.NewWriter(buf)

	// Add some files to the archive.
	style, err := json.Marshal(s.Data)
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

	dir := filepath.Join(s.Path, "icons")
	items, err := ioutil.ReadDir(dir)
	if err == nil {
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
	} else {
		log.Error(err)
	}

	// Make sure to check the error on Close.
	err = w.Close()
	if err != nil {
		log.Fatal(err)
	}
	err = ioutil.WriteFile(filepath.Join(s.Path, "style.zip"), buf.Bytes(), os.ModePerm)
	if err != nil {
		fmt.Printf("write zip style file failed,details: %s\n", err)
	}
	return buf
}

//GenSprite 生成sprites
func (s *Style) GenSprite(sprite string) error {
	scale := 1.0
	prefix := "sprite@"
	if strings.HasPrefix(sprite, prefix) {
		pos := strings.Index(sprite, "x.")
		s, err := strconv.ParseFloat(sprite[len(prefix):pos], 64)
		if err == nil {
			scale = s
		}
	}

	dir := filepath.Join(s.Path, "icons")

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return fmt.Errorf("no icons, can not refresh sprites")
	}

	symbols := ReadIcons(dir, scale) //readIcons(dir, 1)
	sort.Slice(symbols, func(i, j int) bool {
		if symbols[j].Height == symbols[i].Height {
			return symbols[i].ID < symbols[j].ID
		}
		return symbols[j].Height < symbols[i].Height
	})

	sprites := NewShelfPack(1, 1, ShelfPackOptions{autoResize: true})
	var bins []*Bin
	for _, s := range symbols {
		bin := NewBin(s.ID, s.Width, s.Height, -1, -1, -1, -1)
		bins = append(bins, bin)
	}

	results := sprites.Pack(bins, PackOptions{})

	for _, bin := range results {
		for i := range symbols {
			if bin.id == symbols[i].ID {
				symbols[i].X = bin.x
				symbols[i].Y = bin.y
				break
			}
		}
	}
	layout := make(map[string]*Symbol)
	dc := gg.NewContext(sprites.width, sprites.height)
	dc.SetRGBA(0, 0, 0, 0.1)
	for _, s := range symbols {
		dc.DrawImage(s.Image, s.X, s.Y)
		layout[s.Name] = s
	}
	name := strings.TrimSuffix(sprite, filepath.Ext(sprite))
	pathname := filepath.Join(s.Path, name)
	err := dc.SavePNG(pathname + ".png")
	if err != nil {
		log.Errorf("save png file error, details: %s", err)
		return err
	}
	jsonbuf, err := json.Marshal(layout)
	if err != nil {
		log.Errorf("marshal json error, details: %s", err)
		return err
	}
	err = ioutil.WriteFile(pathname+".json", jsonbuf, os.ModePerm)
	if err != nil {
		log.Errorf("save json file error, details: %s", err)
		return err
	}
	return nil
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
	// id, _ := shortid.Generate()
	out := &Style{
		ID:        name, //id==name
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
