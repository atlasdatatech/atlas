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

// PBEXT pbf fonts package format
const PBEXT = ".pbfonts"

// DEFAULTFONT 默认字体
const DEFAULTFONT = "Noto Sans Regular"

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

// LoadFont 加载字体.
func LoadFont(path string) (*Font, error) {
	ext := filepath.Ext(path)
	lext := strings.ToLower(ext)
	if lext != PBEXT {
		err := packPBFonts(path)
		if err != nil {
			return nil, err
		}
		path = strings.TrimSuffix(path, ext) + PBEXT
	}
	fStat, err := os.Stat(path)
	if err != nil {
		log.Errorf(`packPBFonts, read path stat info error, details: %s`, err)
		return nil, err
	}
	ext = filepath.Ext(path)
	base := filepath.Base(path)
	name := strings.TrimSuffix(base, ext)
	id, _ := shortid.Generate()
	out := &Font{
		ID:          id,
		Name:        name,
		Owner:       ATLAS,
		Path:        path,
		Size:        fStat.Size(),
		Compression: false,
	}

	return out, nil
}

//SetupPBFonts 初始化配置PBFont库
func packPBFonts(path string) error {
	fStat, err := os.Stat(path)
	if err != nil {
		log.Errorf(`packPBFonts, read path stat info error, details: %s`, err)
		return err
	}

	//dir,zip,ttf
	if !fStat.IsDir() {
		ext := filepath.Ext(path)
		switch strings.ToLower(ext) {
		case ".zip":
		case ".ttf":
		}
		return fmt.Errorf("not support format ~")
	}
	//create .pbfonts
	db, err := sql.Open("sqlite3", path+PBEXT)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec("create table if not exists fonts (range text, data blob);")
	if err != nil {
		return err
	}

	_, err = db.Exec("create table if not exists metadata (name text, value text);")
	if err != nil {
		return err
	}

	_, err = db.Exec("create unique index name on metadata (name);")
	if err != nil {
		return err
	}

	_, err = db.Exec("create unique index font_index on fonts(range);")
	if err != nil {
		return err
	}

	//read font dir
	items, err := ioutil.ReadDir(path)
	if err != nil {
		return err
	}
	//insert into .pbfonts
	count := 0
	for _, item := range items {
		if item.IsDir() {
			continue
		}
		name := item.Name()
		lext := strings.ToLower(filepath.Ext(name))
		switch lext {
		case ".pbf":
			pbf := filepath.Join(path, name)
			buf, err := ioutil.ReadFile(pbf)
			if err != nil {
				log.Error(err)
			}
			_, err = db.Exec("insert into fonts (range, data) values (?, ?)", name, buf)
			if err != nil {
				log.Errorf("insert pbf into pbfonts error, details:%s", err)
			} else {
				count++
			}
		default:
			log.Warnf("%s unkown sub item format: %s", path, name)
		}
	}

	db.Exec("insert into metadata (name, value) values (?, ?)", "name", filepath.Base(path))
	db.Exec("insert into metadata (name, value) values (?, ?)", "size", fStat.Size())
	db.Exec("insert into metadata (name, value) values (?, ?)", "count", count)
	db.Exec("insert into metadata (name, value) values (?, ?)", "compression", false)

	return nil
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
	db, err := sql.Open("sqlite3", f.Path)
	if err != nil {
		log.Errorf("toService, font to service error, details:%s", err)
		return nil
	}
	fs.DB = db
	return fs
}

//UpInsert 更新/创建样式存储
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

//转为存储
func (fs *FontService) toFont() *Font {
	out := &Font{
		ID:   fs.ID,
		Name: fs.Name,
		Path: fs.URL,
	}
	return out
}

//Font 获取字体pbf切片
func (fs *FontService) Font(fontrange string) ([]byte, error) {
	var data []byte
	err := fs.DB.QueryRow("select data from fonts where range = ?", fontrange).Scan(&data)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return data, nil
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
