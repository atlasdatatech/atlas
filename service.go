package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jinzhu/gorm"
	log "github.com/sirupsen/logrus"
)

// ServiceSet 服务集，S->style样式服务，F->font字体服务，T->tileset瓦片服务，D->dataset数据服务.
type ServiceSet struct {
	S sync.Map // map[string]*StyleService
	F sync.Map // map[string]*FontService
	T sync.Map // map[string]*TileService
	D sync.Map // map[string]*DataService
}

// LoadServiceSet 加载服务集，ATLAS基础服务集，USER用户服务集
func (s *ServiceSet) LoadServiceSet() error {
	//diff styles dir and append new styles
	s.AddStyles("styles")
	//serve all altas styles
	s.ServeStyles(ATLAS)

	// s.AddMBTiles("tilesets") //服务启动时，检测为入库mbtiles

	s.ServeFonts("fonts")
	s.LoadDataServices()
	return nil
}

// AddStyles 添加styles目录下未入库的style样式
func (s *ServiceSet) AddStyles(dir string) error {
	//遍历目录下所有styles
	files := make(map[string]string)
	items, err := ioutil.ReadDir(dir)
	if err != nil {
		log.Error(err)
	}
	for _, item := range items {
		if item.IsDir() {
			path := filepath.Join(dir, item.Name())
			subs, err := ioutil.ReadDir(path)
			if err != nil {
				log.Error(err)
			}
			for _, sub := range subs {
				if sub.IsDir() {
					continue
				}
				if strings.Compare("style.json", strings.ToLower(sub.Name())) == 0 {
					files[item.Name()] = filepath.Join(path, sub.Name())
				}
			}
		}
	}

	//获取数据库中已有服务
	owner := ATLAS
	if filepath.HasPrefix(dir, "users") {
		paths := filepath.SplitList(dir)
		if len(paths) == 3 {
			owner = paths[1] //user id
		}
	}
	var styles []Style
	err = db.Where("owner = ?", owner).Find(&styles).Error
	if err != nil {
		if !gorm.IsRecordNotFoundError(err) {
			return err
		}
	}
	//借助map加速对比
	quickmap := make(map[string]bool)
	for _, style := range styles {
		quickmap[style.ID] = true
	}
	//diff 对比
	count := 0
	for id, file := range files {
		_, ok := quickmap[id]
		if !ok { //如果服务不存在
			//加载文件
			style, err := LoadStyle(file)
			if err != nil {
				log.Errorf("AddStyles, could not load style %s, details: %s", style.ID, err)
			}
			//入库
			err = style.UpInsert()
			if err != nil {
				log.Errorf(`AddStyles, upinsert style %s error, details: %s`, style.ID, err)
			}
			//加载服务,todo 用户服务无需预加载
			if true {
				ss := style.toService()
				s.S.Store(ss.ID, ss)
			}
			count++
		}
	}

	log.Infof("AddStyles, append %d styles ~", count)
	return nil
}

// ServeStyle 从加载样式服务，load style service
func (s *ServiceSet) ServeStyle(id string) error {
	style := Style{}
	err := db.Where("id = ?", id).First(style).Error
	if err != nil {
		return err
	}
	ss := style.toService()
	s.S.Store(ss.ID, ss)
	return nil
}

// ServeStyles 加载用户样式服务
func (s *ServiceSet) ServeStyles(owner string) error {
	var styles []Style
	err := db.Where("owner = ?", owner).Find(&styles).Error
	if err != nil {
		return err
	}
	for _, style := range styles {
		ss := style.toService()
		s.S.Store(ss.ID, ss)
	}
	return nil
}

// AddFonts 添加fonts目录下未入库的字体
func (s *ServiceSet) AddFonts(dir string) error {

	isValid := func(pbfonts string) bool {
		pbfFile := filepath.Join(pbfonts, "0-255.pbf")
		if _, err := os.Stat(pbfFile); os.IsNotExist(err) {
			log.Error(pbfFile, " not exists~")
			return false
		}
		pbfFile = filepath.Join(pbfonts, "65280-65535.pbf")
		if _, err := os.Stat(pbfFile); os.IsNotExist(err) {
			log.Error(pbfFile, " not exists~")
			return false
		}
		return true
	}

	//遍历目录下所有fonts
	files := make(map[string]string)
	items, err := ioutil.ReadDir(dir)
	if err != nil {
		log.Error(err)
	}
	for _, item := range items {
		//zip & ttf current not support
		if item.IsDir() {
			path := filepath.Join(dir, item.Name())
			if isValid(path) {
				files[item.Name()] = path
			}
		}
	}

	//获取数据库中已有服务
	owner := ATLAS
	if filepath.HasPrefix(dir, "users") {
		paths := filepath.SplitList(dir)
		if len(paths) == 3 {
			owner = paths[1] //user id
		}
	}
	var fonts []Font
	err = db.Where("owner = ?", owner).Find(&fonts).Error
	if err != nil {
		if !gorm.IsRecordNotFoundError(err) {
			return err
		}
	}
	//借助map加速对比
	quickmap := make(map[string]bool)
	for _, font := range fonts {
		quickmap[font.ID] = true
	}
	//diff 对比
	count := 0
	for id, file := range files {
		_, ok := quickmap[id]
		if !ok { //如果服务不存在
			//加载文件
			font, err := LoadFont(file)
			if err != nil {
				log.Errorf("AddFonts, could not load font %s, details: %s", font.ID, err)
			}
			//入库
			err = font.UpInsert()
			if err != nil {
				log.Errorf(`AddFonts, upinsert font %s error, details: %s`, font.ID, err)
			}
			//加载服务,todo 用户服务无需预加载
			if true {
				fs := font.toService()
				s.F.Store(fs.ID, fs)
			}
			count++
		}
	}

	log.Infof("AddFonts, append %d fonts ~", count)
	return nil
}

// ServeFont 从加载字体服务，load font service
func (s *ServiceSet) ServeFont(id string) error {
	font := Font{}
	err := db.Where("id = ?", id).First(font).Error
	if err != nil {
		return err
	}
	fs := font.toService()
	s.F.Store(fs.ID, fs)
	return nil
}

// ServeFonts 加载用户样式服务
func (s *ServiceSet) ServeFonts(owner string) error {
	var fonts []Font
	err := db.Where("owner = ?", owner).Find(&fonts).Error
	if err != nil {
		return err
	}
	for _, font := range fonts {
		fs := font.toService()
		s.F.Store(fs.ID, fs)
	}
	return nil
}

// AddTilesets 添加tilesets目录下未入库的MBTiles数据源或者tilemap配置文件
func (s *ServiceSet) AddTilesets(dir string) error {
	//遍历dir目录下所有.mbtiles
	files := make(map[string]string)
	items, err := ioutil.ReadDir(dir)
	if err != nil {
		log.Error(err)
		return err
	}
	for _, item := range items {
		if item.IsDir() {
			continue
		}
		name := item.Name()
		ext := filepath.Ext(name)
		lext := strings.ToLower(ext)
		if strings.Compare(".mbtiles", lext) == 0 || strings.Compare(".tilemap", lext) == 0 {
			files[strings.TrimSuffix(name, ext)] = filepath.Join(dir, name)
		}
	}
	//获取数据库.mbtiles服务
	var tss []TileService
	err = db.Where("owner = ?", ATLAS).Find(&tss).Error
	if err != nil {
		if !gorm.IsRecordNotFoundError(err) {
			return err
		}
	}
	//借助map加速对比
	quickmap := make(map[string]bool)
	for _, ts := range tss {
		quickmap[ts.ID] = true
	}
	//diff 对比
	count := 0
	for id, file := range files {
		_, ok := quickmap[id]
		if !ok {
			//如果服务不存在，则添加
			err := s.ServeTileset(file)
			if err != nil {
				continue
			}
			count++
		}
	}

	log.Infof("AddMBTiles, append %d tilesets ~", count)
	return nil
}

// ServeTileset 从文件/配置加载tileservice服务并入库
func (s *ServiceSet) ServeTileset(id string) error {

	// // LoadMBTiles(pathfile)
	// mb, err := LoadMBTiles(pathfile)
	// if err != nil {
	// 	log.Errorf(`AddMBTile, LoadMBTiles error, details: %s`, err)
	// }

	// base := filepath.Base(pathfile)
	// ext := filepath.Ext(pathfile)
	// id := strings.TrimSuffix(base, ext)
	// name := strings.Split(id, ".")[0]

	// ts := &Tileset{
	// 	ID:      id,
	// 	Name:    name,
	// 	URL:     pathfile, //should not add / at the end
	// 	Type:    mb.TileFormat().String(),
	// 	Hash:    mb.GetHash(),
	// 	State:   true,
	// 	Size:    fStat.Size(),
	// 	Tileset: mb,
	// }

	// err = ts.UpInsert()
	// if err != nil {
	// 	log.Errorf(`AddMBTile, upinsert dtfile error, details: %s`, err)
	// 	return err
	// }
	// pubSet.T.Store(id, ts)
	return nil
}

// ServeTilesets 从文件/配置加载tileservice服务并入库
func (s *ServiceSet) ServeTilesets(owner string) error {
	// fStat, err := os.Stat(pathfile)
	// if err != nil {
	// 	log.Errorf(`AddMBTile, read file stat info error, details: %s`, err)
	// 	return err
	// }
	// // LoadMBTiles(pathfile)
	// mb, err := LoadMBTiles(pathfile)
	// if err != nil {
	// 	log.Errorf(`AddMBTile, LoadMBTiles error, details: %s`, err)
	// }

	// base := filepath.Base(pathfile)
	// ext := filepath.Ext(pathfile)
	// id := strings.TrimSuffix(base, ext)
	// name := strings.Split(id, ".")[0]

	// ts := &Tileset{
	// 	ID:      id,
	// 	Name:    name,
	// 	URL:     pathfile, //should not add / at the end
	// 	Type:    mb.TileFormat().String(),
	// 	Hash:    mb.GetHash(),
	// 	State:   true,
	// 	Size:    fStat.Size(),
	// 	Tileset: mb,
	// }

	// err = ts.UpInsert()
	// if err != nil {
	// 	log.Errorf(`AddMBTile, upinsert dtfile error, details: %s`, err)
	// 	return err
	// }
	// pubSet.T.Store(id, ts)
	return nil
}

// AddDataService interprets filename as mbtiles file which is opened and which will be
// served under "/services/<urlPath>" by Handler(). The parameter urlPath may not be
// nil, otherwise an error is returned. In case the DB cannot be opened the returned
// error is non-nil.
func (s *ServiceSet) AddDataService(dataset *Dataset) error {
	if dataset == nil {
		return fmt.Errorf("dataset may not be nil")
	}
	out := dataset.toService()
	out.Hash = "#"
	out.State = true
	s.D.Store(dataset.ID, out)
	// s.Datasets[dataset.Name] = out
	return nil
}

func (s *ServiceSet) updateInsertDataset(dataset *Dataset) error {
	if dataset == nil {
		return fmt.Errorf("dataset may not be nil")
	}
	ds := &Dataset{}
	err := db.Where("id = ?", dataset.ID).First(ds).Error
	if err != nil {
		if gorm.IsRecordNotFoundError(err) {
			err = db.Create(dataset).Error
			if err != nil {
				return err
			}
		}
		return err
	}
	err = db.Model(&Dataset{}).Update(dataset).Error
	if err != nil {
		return err
	}
	return nil
}

// LoadDataServices setServices returns a ServiceSet that combines all .mbtiles files under
// the directory at baseDir. The DBs will all be served under their relative paths
// to baseDir.
func (s *ServiceSet) LoadDataServices() (err error) {
	// 获取所有记录
	var datasets []Dataset
	err = db.Find(&datasets).Error
	if err != nil {
		log.Errorf(`ServeDatasets, query datasets: %s ^^`, err)
	}
	//clear service
	//erase map
	s.D.Range(func(key interface{}, value interface{}) bool {
		s.D.Delete(key)
		return true
	})

	// for k := range s.D {
	// 	delete(s.Datasets, k)
	// }
	for _, ds := range datasets {
		err = s.AddDataService(&ds)
		if err != nil {
			log.Errorf(`ServeDatasets, add dataset: %s ^^`, err)
		}
	}
	length := 0
	s.D.Range(func(_, _ interface{}) bool {
		length++
		return true
	})
	log.Infof("ServeDatasets, loaded %d dataset ~", length)
	return nil
}
