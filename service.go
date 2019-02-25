package atlas

import (
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
	User string
	S    sync.Map // map[string]*StyleService
	F    sync.Map // map[string]*FontService
	T    sync.Map // map[string]*TileService
	D    sync.Map // map[string]*DataService
}

// LoadServiceSet 加载服务集，ATLAS基础服务集，USER用户服务集
func (s *ServiceSet) LoadServiceSet() error {
	//diff styles dir and append new styles
	err := s.AddStyles("styles")
	if err != nil {
		log.Errorf("AddStyles, add new styles error, details:%s", err)
	}
	//serve all altas styles
	err = s.ServeStyles(ATLAS)
	if err != nil {
		log.Errorf("ServeStyles, serve %s's styles error, details:%s", ATLAS, err)
	}
	//diff fonts dir and append new fonts
	err = s.AddFonts("fonts")
	if err != nil {
		log.Errorf("AddFonts, add new fonts error, details:%s", err)
	}
	//serve all altas fonts
	err = s.ServeFonts(ATLAS)
	if err != nil {
		log.Errorf("ServeFonts, serve %s's fonts error, details:%s", ATLAS, err)
	}
	//diff tileset dir and append new tileset
	s.AddTilesets("tilesets") //服务启动时，检测未入服务集(mbtiles,pbflayers)
	if err != nil {
		log.Errorf("AddTilesets, add new tileset error, details:%s", err)
	}
	//serve all altas tilesets
	s.ServeTilesets("tilesets") //服务启动时，创建服务集
	if err != nil {
		log.Errorf("ServeTilesets, serve %s's tileset error, details:%s", ATLAS, err)
	}
	//diff tileset dir and append new dataset
	s.AddDatasets("tilesets") //服务启动时，检测未入库数据集
	if err != nil {
		log.Errorf("AddDatasets, add new dataset error, details:%s", err)
	}
	//serve all altas datasets
	s.ServeDatasets("tilesets") //服务启动时，创建数据集
	if err != nil {
		log.Errorf("ServeDatasets, serve %s's dataset error, details:%s", ATLAS, err)
	}
	return nil
}

// AddStyles 添加styles目录下未入库的样式
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
			count++
		}
	}

	log.Infof("AddStyles, append %d styles ~", count)
	return nil
}

// ServeStyle 加载启动指定样式服务，load style service
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

// ServeStyles 加载启动指定用户的全部样式服务
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
			count++
		}
	}

	log.Infof("AddFonts, append %d fonts ~", count)
	return nil
}

// ServeFont 加载启动指定字体服务，load font service
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

// ServeFonts 加载启动指定用户的字体服务，当前默认加载公共字体
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
	var tss []Tileset
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
			//加载文件
			tileset, err := LoadTileset(file)
			if err != nil {
				log.Errorf("AddFonts, could not load font %s, details: %s", tileset.ID, err)
			}
			//入库
			err = tileset.UpInsert()
			if err != nil {
				log.Errorf(`AddFonts, upinsert font %s error, details: %s`, tileset.ID, err)
			}
			count++
		}
	}

	log.Infof("AddMBTiles, append %d tilesets ~", count)
	return nil
}

// ServeTileset 从瓦片集目录库里加载tilesets服务集
func (s *ServiceSet) ServeTileset(id string) error {
	ts := Tileset{}
	err := db.Where("id = ?", id).First(ts).Error
	if err != nil {
		return err
	}
	tss := ts.toService()
	s.T.Store(tss.ID, tss)
	return nil
}

// ServeTilesets 加载用户tilesets服务集
func (s *ServiceSet) ServeTilesets(owner string) error {
	var tilesets []Tileset
	err := db.Where("owner = ?", owner).Find(&tilesets).Error
	if err != nil {
		return err
	}
	for _, tileset := range tilesets {
		tss := tileset.toService()
		s.T.Store(tss.ID, tss)
	}
	return nil
}

// AddDatasets 添加datasets目录下未入库的数据集
func (s *ServiceSet) AddDatasets(dir string) error {
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
		if strings.Compare(".geojson", lext) == 0 || strings.Compare(".zip", lext) == 0 {
			files[strings.TrimSuffix(name, ext)] = filepath.Join(dir, name)
		}
	}
	//获取数据库.mbtiles服务
	var tss []Dataset
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
			//加载文件
			dataset, err := LoadDataset(file)
			if err != nil {
				log.Errorf("AddFonts, could not load font %s, details: %s", dataset.ID, err)
			}
			//入库
			err = dataset.UpInsert()
			if err != nil {
				log.Errorf(`AddFonts, upinsert font %s error, details: %s`, dataset.ID, err)
			}
			count++
		}
	}

	log.Infof("AddMBTiles, append %d tilesets ~", count)
	return nil
}

// ServeDataset 从数据集目录库里加载数据集服务
func (s *ServiceSet) ServeDataset(id string) error {
	ds := Dataset{}
	err := db.Where("id = ?", id).First(ds).Error
	if err != nil {
		return err
	}
	dss := ds.toService()
	s.D.Store(dss.ID, dss)
	return nil
}

// ServeDatasets 加载用户数据集服务
func (s *ServiceSet) ServeDatasets(owner string) error {
	var datasets []Dataset
	err := db.Where("owner = ?", owner).Find(&datasets).Error
	if err != nil {
		return err
	}
	for _, dataset := range datasets {
		dss := dataset.toService()
		s.D.Store(dss.ID, dss)
	}
	return nil
}
