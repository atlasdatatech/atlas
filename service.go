package main

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jinzhu/gorm"
	log "github.com/sirupsen/logrus"
)

//UserSet 用户入口
type UserSet struct {
	sync.Map
}

func (us *UserSet) service(uid string) *ServiceSet {
	is, ok := us.Load(uid)
	if ok {
		set, ok := is.(*ServiceSet)
		if ok {
			return set
		}
	}
	uss, err := LoadServiceSet(uid)
	if err != nil {
		log.Errorf("load %s's service set error, details: %s", uid, err)
		return nil
	}
	us.Store(uid, uss)
	return uss
}

func (us *UserSet) style(uid, sid string) *StyleService {
	set := us.service(uid)
	if set != nil {
		style, ok := set.S.Load(sid)
		if ok {
			service, ok := style.(*StyleService)
			if ok {
				return service
			}
		}
	}
	return nil
}

func (us *UserSet) font(uid, fid string) *FontService {
	set := us.service(uid)
	if set != nil {
		font, ok := set.F.Load(fid)
		if ok {
			service, ok := font.(*FontService)
			if ok {
				return service
			}
		}
	}
	return nil
}

func (us *UserSet) tileset(uid, tid string) *TileService {
	set := us.service(uid)
	if set != nil {
		tile, ok := set.T.Load(tid)
		if ok {
			service, ok := tile.(*TileService)
			if ok {
				return service
			}
		}
	}
	return nil
}

func (us *UserSet) dataset(uid, did string) *DataService {
	set := us.service(uid)
	if set != nil {
		data, ok := set.D.Load(did)
		if ok {
			service, ok := data.(*DataService)
			if ok {
				return service
			}
		}
	}
	return nil
}

// ServiceSet 服务集，S->style样式服务，F->font字体服务，T->tileset瓦片服务，D->dataset数据服务.
type ServiceSet struct {
	// ID    string
	Owner string
	S     sync.Map // map[string]*StyleService
	F     sync.Map // map[string]*FontService
	T     sync.Map // map[string]*TileService
	D     sync.Map // map[string]*DataService
}

// LoadServiceSet 加载服务集，ATLAS基础服务集，USER用户服务集
func LoadServiceSet(user string) (*ServiceSet, error) {
	s := &ServiceSet{Owner: user}
	//diff styles dir and append new styles
	err := s.AddStyles()
	if err != nil {
		log.Errorf("AddStyles, add new styles error, details:%s", err)
	}
	//serve all altas styles
	err = s.ServeStyles()
	if err != nil {
		log.Errorf("ServeStyles, serve %s's styles error, details:%s", ATLAS, err)
	}
	//diff fonts dir and append new fonts
	err = s.AddFonts()
	if err != nil {
		log.Errorf("AddFonts, add new fonts error, details:%s", err)
	}
	//serve all altas fonts
	err = s.ServeFonts()
	if err != nil {
		log.Errorf("ServeFonts, serve %s's fonts error, details:%s", ATLAS, err)
	}
	// //diff tileset dir and append new tileset
	// s.AddTilesets() //服务启动时，检测未入服务集(mbtiles,pbflayers)
	// if err != nil {
	// 	log.Errorf("AddTilesets, add new tileset error, details:%s", err)
	// }
	//serve all altas tilesets
	s.ServeTilesets() //服务启动时，创建服务集
	if err != nil {
		log.Errorf("ServeTilesets, serve %s's tileset error, details:%s", ATLAS, err)
	}
	// //diff tileset dir and append new dataset
	// s.AddDatasets() //服务启动时，检测未入库数据集
	// if err != nil {
	// 	log.Errorf("AddDatasets, add new dataset error, details:%s", err)
	// }
	//serve all altas datasets
	s.ServeDatasets() //服务启动时，创建数据集
	if err != nil {
		log.Errorf("ServeDatasets, serve %s's dataset error, details:%s", ATLAS, err)
	}

	return s, nil
}

// AddStyles 添加styles目录下未入库的样式
func (s *ServiceSet) AddStyles() error {
	//遍历目录下所有styles
	files := make(map[string]string)
	dir := filepath.Join("styles", s.Owner)
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
					files[item.Name()] = path
				}
			}
		}
	}

	//获取数据库中已有服务
	var styles []Style
	err = db.Where("owner = ?", s.Owner).Find(&styles).Error
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
				log.Errorf("AddStyles, could not load style %s, details: %s", file, err)
				continue
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
func (s *ServiceSet) ServeStyles() error {
	var styles []Style
	err := db.Where("owner = ?", s.Owner).Find(&styles).Error
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
func (s *ServiceSet) AddFonts() error {

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
	dir := filepath.Join("fonts", s.Owner)
	items, err := ioutil.ReadDir(dir)
	if err != nil {
		log.Error(err)
	}
	for _, item := range items {
		//zip & ttf current not support
		path := filepath.Join(dir, item.Name())
		if item.IsDir() {
			if isValid(path) {
				files[item.Name()] = path
			}
		} else {
			name := item.Name()
			ext := filepath.Ext(name)
			if strings.ToLower(ext) == PBFONTEXT {
				files[strings.TrimSuffix(name, ext)] = path
			}
		}
	}

	var fonts []Font
	err = db.Where("owner = ?", s.Owner).Find(&fonts).Error
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
				log.Errorf("AddFonts, could not load font %s, details: %s", file, err)
				continue
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
func (s *ServiceSet) ServeFonts() error {
	var fonts []Font
	err := db.Where("owner = ?", s.Owner).Find(&fonts).Error
	if err != nil {
		return err
	}
	for _, font := range fonts {
		fs := font.toService()
		s.F.Store(fs.ID, fs)
	}
	return nil
}

// AddTilesets 添加tilesets目录下未入库的MBTiles数据源或者未发布的可发布数据源(暂未实现)
func (s *ServiceSet) AddTilesets() error {
	//遍历dir目录下所有.mbtiles
	files := make(map[string]string)
	dir := filepath.Join("tilesets", s.Owner)
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
		if strings.Compare(MBTILESEXT, lext) == 0 {
			files[strings.TrimSuffix(name, ext)] = filepath.Join(dir, name)
		}
	}
	//获取数据库.mbtiles服务
	var tss []Tileset
	err = db.Where("owner = ?", s.Owner).Find(&tss).Error
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
				log.Errorf("AddTilesets, could not load font %s, details: %s", file, err)
				continue
			}
			//入库
			err = tileset.UpInsert()
			if err != nil {
				log.Errorf(`AddTilesets, upinsert font %s error, details: %s`, tileset.ID, err)
			}
			count++
		}
	}

	log.Infof("AddTilesets, append %d tilesets ~", count)
	return nil
}

// ServeTileset 从瓦片集目录库里加载tilesets服务集
func (s *ServiceSet) ServeTileset(id string) error {
	ts := Tileset{}
	err := db.Where("id = ?", id).First(ts).Error
	if err != nil {
		return err
	}
	tss, err := ts.toService()
	if err != nil {
		return err
	}
	s.T.Store(tss.ID, tss)
	return nil
}

// ServeTilesets 加载用户tilesets服务集
func (s *ServiceSet) ServeTilesets() error {
	var tilesets []Tileset
	err := db.Where("owner = ?", s.Owner).Find(&tilesets).Error
	if err != nil {
		return err
	}
	for _, tileset := range tilesets {
		tss, err := tileset.toService()
		if err != nil {
			log.Error(err)
			continue
		}
		s.T.Store(tss.ID, tss)
	}
	return nil
}

// AddDatasets 添加datasets目录下未入库的数据集
func (s *ServiceSet) AddDatasets() error {
	//遍历dir目录下所有.mbtiles
	files := make(map[string]string)
	dir := filepath.Join("datasets", s.Owner)
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
	var dss []Dataset
	err = db.Where("owner = ?", s.Owner).Find(&dss).Error
	if err != nil {
		if !gorm.IsRecordNotFoundError(err) {
			return err
		}
	}
	//借助map加速对比
	quickmap := make(map[string]bool)
	for _, ds := range dss {
		quickmap[ds.ID] = true
	}
	//diff 对比
	count := 0
	for id, file := range files {
		_, ok := quickmap[id]
		if !ok {
			//加载文件
			datafiles, err := LoadDatafile(file)
			if err != nil {
				log.Errorf("AddDatasets, could not load font %s, details: %s", file, err)
				continue
			}
			//入库、导入、加载服务
			for _, df := range datafiles {
				dp := df.getPreview()
				df.Update(dp)
				df.Overwrite = true
				err = df.UpInsert()
				if err != nil {
					log.Errorf(`dataImport, upinsert datafile info error, details: %s`, err)
				}
				task := df.dataImport()
				if task.Err != "" {
					log.Error(err)
					<-task.Pipe
					<-taskQueue
					continue
				}
				go func(df *Datafile) {
					<-task.Pipe
					<-taskQueue
					ds, err := df.toDataset()
					if err != nil {
						log.Error(err)
						return
					}
					err = ds.UpInsert()
					if err != nil {
						log.Errorf(`uploadFiles, upinsert datafile info error, details: %s`, err)
					}
				}(df)
				count++
			}
		}
	}

	log.Infof("AddDatasets, append %d datasets ~", count)
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
func (s *ServiceSet) ServeDatasets() error {
	var datasets []Dataset
	err := db.Where("owner = ?", s.Owner).Find(&datasets).Error
	if err != nil {
		return err
	}
	for _, dataset := range datasets {
		dss := dataset.toService()
		s.D.Store(dss.ID, dss)
	}
	return nil
}
