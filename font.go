package main

import (
	"database/sql"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/jinzhu/gorm"
	"github.com/teris-io/shortid"

	proto "github.com/golang/protobuf/proto"
	log "github.com/sirupsen/logrus"
)

//Font struct for pbf font save
type Font struct {
	ID          string
	Name        string
	Owner       string
	Path        string
	Size        int64
	Compression bool
}

//FontService struct for font service
type FontService struct {
	ID    string
	Name  string
	URL   string
	State bool
	DB    *sql.DB
}

//toService 加载服务
func (f *Font) toService() *FontService {
	fs := &FontService{
		ID:    f.ID,
		Name:  f.Name,
		URL:   f.Path,
		State: true,
	}
	// fs.DB
	return fs
}

//转为存储
func (fs *FontService) toFont() *Font {
	out := &Font{
		ID:   fs.ID,
		Name: fs.Name,
		Path: fs.URL,
	}
	return out
}

// LoadFont 加载字体.
func LoadFont(font string) (*Font, error) {
	fStat, err := os.Stat(font)
	if err != nil {
		log.Errorf(`ServeStyle, read file stat info error, details: %s`, err)
		return nil, err
	}

	//dir,zip,ttf
	if !fStat.IsDir() {
		ext := filepath.Ext(font)
		switch strings.ToLower(ext) {
		case ".zip":
		case ".ttf":
		}
		return nil, fmt.Errorf("not support format ~")
	}

	//read style.json
	items, err := ioutil.ReadDir(font)
	if err != nil {
		log.Error(err)
		return nil, err
	}
	//create .pbfonts
	cnt := 0
	for _, item := range items {
		if item.IsDir() {
			continue
		}
		name := item.Name()
		lext := strings.ToLower(filepath.Ext(name))
		switch lext {
		case ".pbf":
			pbf := filepath.Join(font, name)
			buf, err := ioutil.ReadFile(pbf)
			if err != nil {
				log.Error(err)
			}
			//insert into .pbfonts
			fmt.Println(buf)
			cnt++
		default:
			log.Warnf("%s unkown sub item format: %s", font, name)
		}
	}

	if cnt != 256 {
		log.Warnf("%s sub pbf items count warning, %d ", font, cnt)
	}

	base := filepath.Base(font)
	id, _ := shortid.Generate()
	out := &Font{
		ID:          id,
		Name:        base,
		Owner:       ATLAS,
		Path:        font + ".pbfonts",
		Size:        fStat.Size(),
		Compression: true,
	}

	return out, nil
}

//UpInsert 创建更新样式存储
//create or update upload data file info into database
func (f *Font) UpInsert() error {
	if f == nil {
		return fmt.Errorf("style may not be nil")
	}
	tmp := &Font{}
	err := db.Where("id = ?", f.ID).First(tmp).Error
	if err != nil {
		if gorm.IsRecordNotFoundError(err) {
			err = db.Create(f).Error
			if err != nil {
				return err
			}
		}
		return err
	}
	err = db.Model(&Font{}).Update(f).Error
	if err != nil {
		return err
	}
	return nil
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

//Combine combine glyph (SDF) PBFs to one
//Returns a re-encoded PBF with the combined
//font faces, composited using array order
//to determine glyph priority.
//@param buffers An array of SDF PBFs.
func Combine(buffers [][]byte, fontstack []string) ([]byte, error) {
	coverage := make(map[uint32]bool)
	result := &Glyphs{}
	for i, buf := range buffers {
		pbf := &Glyphs{}
		err := proto.Unmarshal(buf, pbf)
		if err != nil {
			log.Fatal("unmarshaling error: ", err)
		}

		if stacks := pbf.GetStacks(); stacks != nil && len(stacks) > 0 {
			stack := stacks[0]
			if 0 == i {
				for _, gly := range stack.Glyphs {
					coverage[gly.GetId()] = true
				}
				result = pbf
			} else {
				for _, gly := range stack.Glyphs {
					if !coverage[gly.GetId()] {
						result.Stacks[0].Glyphs = append(result.Stacks[0].Glyphs, gly)
						coverage[gly.GetId()] = true
					}
				}
				result.Stacks[0].Name = proto.String(result.Stacks[0].GetName() + "," + stack.GetName())
			}
		}

		if fontstack != nil {
			result.Stacks[0].Name = proto.String(strings.Join(fontstack, ","))
		}
	}

	glys := result.Stacks[0].GetGlyphs()

	sort.Slice(glys, func(i, j int) bool {
		return glys[i].GetId() < glys[j].GetId()
	})

	return proto.Marshal(result)
}
