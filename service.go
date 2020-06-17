package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jinzhu/gorm"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
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

func (us *UserSet) style(uid, sid string) *Style {
	set := us.service(uid)
	if set != nil {
		style, ok := set.S.Load(sid)
		if ok {
			s, ok := style.(*Style)
			if ok {
				return s
			}
		}
	}
	if casEnf.Enforce(uid, sid, "GET") || DISABLEACCESSTOKEN {
		set = us.service(ATLAS)
		if set != nil {
			style, ok := set.S.Load(sid)
			if ok {
				s, ok := style.(*Style)
				if ok {
					if s.Public {
						return s
					}
				}
			}
		}
	}

	return nil
}

func (us *UserSet) font(uid, fid string) *Font {
	set := us.service(uid)
	if set != nil {
		font, ok := set.F.Load(fid)
		if ok {
			f, ok := font.(*Font)
			if ok {
				return f
			}
		}
	}
	return nil
}

func (us *UserSet) tileset(uid, tid string) *Tileset {
	set := us.service(uid)
	if set != nil {
		tile, ok := set.T.Load(tid)
		if ok {
			ts, ok := tile.(*Tileset)
			if ok {
				return ts
			}
		}
	}
	if casEnf.Enforce(uid, tid, "GET") || DISABLEACCESSTOKEN {
		set = us.service(ATLAS)
		if set != nil {
			tile, ok := set.T.Load(tid)
			if ok {
				ts, ok := tile.(*Tileset)
				if ok {
					if ts.Public {
						return ts
					}
				}
			}
		}
	}
	return nil
}

func (us *UserSet) dataset(uid, did string) *Dataset {
	set := us.service(uid)
	if set != nil {
		data, ok := set.D.Load(did)
		if ok {
			dt, ok := data.(*Dataset)
			if ok {
				return dt
			}
		}
	}
	if casEnf.Enforce(uid, did, "GET") || DISABLEACCESSTOKEN {
		set = us.service(ATLAS)
		if set != nil {
			data, ok := set.D.Load(did)
			if ok {
				dt, ok := data.(*Dataset)
				if ok {
					if dt.Public {
						return dt
					}
				}
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
	// err := s.AddStyles()
	// if err != nil {
	// 	log.Errorf("AddStyles, add new styles error, details:%s", err)
	// }
	//serve all altas styles
	err := s.ServeStyles()
	if err != nil {
		log.Errorf("ServeStyles, serve %s's styles error, details:%s", ATLAS, err)
	}
	//diff fonts dir and append new fonts
	// err = s.AddFonts()
	// if err != nil {
	// 	log.Errorf("AddFonts, add new fonts error, details:%s", err)
	// }
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
	err = s.ServeTilesets() //服务启动时，创建服务集
	if err != nil {
		log.Errorf("ServeTilesets, serve %s's tileset error, details:%s", ATLAS, err)
	}
	// //diff tileset dir and append new dataset
	// s.AddDatasets() //服务启动时，检测未入库数据集
	// if err != nil {
	// 	log.Errorf("AddDatasets, add new dataset error, details:%s", err)
	// }
	//serve all altas datasets
	err = s.ServeDatasets() //服务启动时，创建数据集
	if err != nil {
		log.Errorf("ServeDatasets, serve %s's dataset error, details:%s", ATLAS, err)
	}

	return s, nil
}

// AppendStyles 添加styles目录下未入库的样式
func (ss *ServiceSet) AppendStyles() error {
	//遍历目录下所有styles
	files := make(map[string]string)
	dir := filepath.Join(viper.GetString("paths.styles"), ss.Owner)
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
	err = db.Where("owner = ?", ss.Owner).Find(&styles).Error
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
			style.Owner = ss.Owner
			//入库
			err = style.UpInsert()
			if err != nil {
				log.Errorf(`AddStyles, upinsert style %s error, details: %s`, style.ID, err)
				continue
			}
			err = style.Service()
			if err != nil {
				log.Error(err)
				continue
			}
			ss.S.Store(style.ID, style)
			count++
		}
	}

	log.Infof("AddStyles, append %d styles ~", count)
	return nil
}

// ServeStyle 加载启动指定样式服务，load style service
func ServeStyle(id string) (*Style, error) {
	s := &Style{}
	err := db.Where("id = ?", id).First(s).Error
	if err != nil {
		if gorm.IsRecordNotFoundError(err) {
			log.Errorf(`ServeStyle, style (%s) not found, details: %v`, id, err)
			return nil, fmt.Errorf("style (%s) not found", id)
		}
		log.Errorf(`ServeStyle, load style (%s) error, details: %v`, id, err)
		return nil, fmt.Errorf("load style (%s) error", id)
	}
	err = s.Service()
	if err != nil {
		return nil, err
	}
	// ss.S.Store(s.ID, s)
	return s, nil
}

// ServeStyles 加载启动指定用户的全部样式服务
func (ss *ServiceSet) ServeStyles() error {
	var styles []*Style
	err := db.Where("owner = ?", ss.Owner).Find(&styles).Error
	if err != nil {
		if gorm.IsRecordNotFoundError(err) {
			log.Errorf(`ServeStyles, %s has no styles, details: %v`, ss.Owner, err)
			return fmt.Errorf("there is no styles")
		}
		log.Errorf(`ServeStyles, load %s's styles error, details: %v`, ss.Owner, err)
		return err
	}
	for _, s := range styles {
		err := s.Service()
		if err != nil {
			log.Errorf("ServeStyles, serve %s's style (%s) error, details: %s", ss.Owner, s.ID, err)
			continue
		}
		ss.S.Store(s.ID, s)
	}
	return nil
}

// AppendFonts 添加fonts目录下未入库的字体
func (ss *ServiceSet) AppendFonts() error {
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
	dir := filepath.Join(viper.GetString("paths.fonts"), ss.Owner)
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
	err = db.Where("owner = ?", ss.Owner).Find(&fonts).Error
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
				continue
			}
			err = font.Service()
			if err != nil {
				log.Error(err)
				continue
			}
			ss.F.Store(font.ID, font)
			count++
		}
	}

	log.Infof("AddFonts, append %d fonts ~", count)
	return nil
}

// ServeFont 加载启动指定字体服务，load font service
func ServeFont(id string) (*Font, error) {
	f := &Font{}
	err := db.Where("id = ?", id).First(f).Error
	if err != nil {
		if gorm.IsRecordNotFoundError(err) {
			log.Errorf(`ServeFont, font (%s) not found, details: %v`, id, err)
			return nil, fmt.Errorf("font (%s) not found", id)
		}
		log.Errorf(`ServeFont, load font (%s) error, details: %v`, id, err)
		return nil, fmt.Errorf("load font (%s) error", id)
	}
	err = f.Service()
	if err != nil {
		return nil, err
	}
	// ss.F.Store(f.ID, f)
	return f, nil
}

// ServeFonts 加载启动指定用户的字体服务，当前默认加载公共字体
func (ss *ServiceSet) ServeFonts() error {
	var fs []*Font
	err := db.Where("owner = ?", ss.Owner).Find(&fs).Error
	if err != nil {
		return err
	}
	for _, f := range fs {
		err := f.Service()
		if err != nil {
			log.Errorf("ServeFonts, serve %s's font (%s) error, details: %s", ss.Owner, f.ID, err)
			continue
		}
		ss.F.Store(f.ID, f)
	}
	return nil
}

// AppendTilesets 添加tilesets目录下未入库的MBTiles数据源或者未发布的可发布数据源(暂未实现)
func (ss *ServiceSet) AppendTilesets() error {
	//遍历dir目录下所有.mbtiles
	files := make(map[string]string)
	dir := filepath.Join(viper.GetString("paths.tilesets"), ss.Owner)
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
	err = db.Where("owner = ?", ss.Owner).Find(&tss).Error
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
			ds := &DataSource{
				ID:   id,
				Name: id,
				Path: file,
			}
			tileset, err := LoadTileset(ds)
			if err != nil {
				log.Errorf("AddTilesets, could not load tileset %s, details: %s", file, err)
				continue
			}
			tileset.Owner = ss.Owner
			//入库
			err = tileset.UpInsert()
			if err != nil {
				log.Errorf(`AddTilesets, upinsert font %s error, details: %s`, tileset.ID, err)
				continue
			}
			err = tileset.Service()
			if err != nil {
				log.Error(err)
				continue
			}
			ss.T.Store(tileset.ID, tileset)
			casEnf.AddPolicy(USER, tileset.ID, "GET")
			count++
		}
	}

	log.Infof("AddTilesets, append %d tilesets ~", count)
	return nil
}

// ServeTileset 从瓦片集目录库里加载tilesets服务集
func ServeTileset(id string) (*Tileset, error) {
	ts := &Tileset{}
	err := db.Where("id = ?", id).First(ts).Error
	if err != nil {
		if gorm.IsRecordNotFoundError(err) {
			log.Errorf(`ServeTileset, tileset (%s) not found, details: %v`, id, err)
			return nil, fmt.Errorf("tileset (%s) not found", id)
		}
		log.Errorf(`ServeTileset, load tileset (%s) error, details: %v`, id, err)
		return nil, fmt.Errorf("load tileset (%s) error", id)
	}
	err = ts.Service()
	if err != nil {
		return nil, err
	}
	// ss.T.Store(ts.ID, ts)
	return ts, nil
}

// ServeTilesets 加载用户tilesets服务集
func (ss *ServiceSet) ServeTilesets() error {
	var tss []*Tileset
	err := db.Where("owner = ?", ss.Owner).Find(&tss).Error
	if err != nil {
		return err
	}
	for _, ts := range tss {
		err := ts.Service()
		if err != nil {
			log.Errorf("ServeTilesets, serve %s's tileset (%s) error, details: %s", ss.Owner, ts.ID, err)
			continue
		}
		ss.T.Store(ts.ID, ts)
	}
	return nil
}

// AppendDatasets 添加datasets目录下未入库的数据集
func (ss *ServiceSet) AppendDatasets() error {
	//遍历dir目录下所有.mbtiles
	files := make(map[string]string)
	dir := filepath.Join("datasets", ss.Owner)
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
		switch lext {
		case CSVEXT, GEOJSONEXT, SHPEXT, KMLEXT, GPXEXT:
			files[strings.TrimSuffix(name, ext)] = filepath.Join(dir, name)
		}
	}
	//获取数据库.mbtiles服务
	var dss []Dataset
	err = db.Where("owner = ?", ss.Owner).Find(&dss).Error
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
			base := filepath.Base(file)
			ext := filepath.Ext(base)
			name := strings.TrimSuffix(base, ext)
			lext := strings.ToLower(ext)
			// 获取所有记录
			ds := &DataSource{
				ID:     name,
				Name:   name,
				Format: lext,
				Path:   file,
			}
			err := ds.LoadFrom()
			if err != nil {
				log.Errorf("AppendDatasets, could not load font %s, details: %s", file, err)
				continue
			}
			//入库、导入、加载服务
			ds.Owner = ss.Owner
			err = ds.Save()
			if err != nil {
				log.Errorf(`AppendDatasets, upinsert datafile info error, details: %s`, err)
			}
			task := &Task{
				ID:    ds.ID,
				Name:  ds.Name,
				Owner: ds.Owner,
				Type:  DSIMPORT,
				Pipe:  make(chan struct{}),
			}
			//任务队列,若队列已满,则阻塞
			taskQueue <- task
			//任务集
			taskSet.Store(task.ID, task)
			task.save()
			go func(ds *DataSource, task *Task) {
				st := time.Now()
				err = ds.Import(task)
				log.Infof("import task takes: %v", time.Since(st))
				if err != nil {
					task.Status = "failed"
					task.Error = err.Error()
				} else {
					task.Progress = 100
					task.Status = "finished"
					task.Error = ""
				}
				task.Pipe <- struct{}{}
			}(ds, task)

			go func(ds *DataSource, task *Task) {
				<-task.Pipe
				<-taskQueue
				task.update()
				if task.Error == "" {
					//todo goroute 导入，以下事务需在task完成后处理
					dt := ds.toDataset()
					err = dt.UpInsert()
					if err != nil {
						log.Errorf(`AppendDatasets, upinsert dataset info error, details: %s`, err)
						return
					}
					err = dt.Service()
					if err == nil {
						ss.D.Store(dt.ID, dt)
						casEnf.AddPolicy(USER, dt.ID, "GET")
					}
				} else {
					log.Errorf("import task failed, details: %s", err)
				}
			}(ds, task)
			count++
		}
	}

	log.Infof("AddDatasets, append %d datasets ~", count)
	return nil
}

// ServeDataset 从数据集目录库里加载数据集服务
func ServeDataset(id string) (*Dataset, error) {
	dt := &Dataset{}
	err := db.Where("id = ?", id).First(dt).Error
	if err != nil {
		if gorm.IsRecordNotFoundError(err) {
			log.Errorf(`ServeDataset, dataset (%s) not found, details: %v`, id, err)
			return nil, fmt.Errorf("dataset (%s) not found", id)
		}
		log.Errorf(`ServeDataset, load dataset (%s) error, details: %v`, id, err)
		return nil, fmt.Errorf("load dataset (%s) error", id)
	}
	err = dt.Service()
	if err != nil {
		return nil, err
	}
	// ss.D.Store(dt.ID, dt)
	return dt, nil
}

// ServeDatasets 加载用户数据集服务
func (ss *ServiceSet) ServeDatasets() error {
	var dts []*Dataset
	err := db.Where("owner = ?", ss.Owner).Find(&dts).Error
	if err != nil {
		return err
	}
	for _, dt := range dts {
		err := dt.Service()
		if err != nil {
			log.Errorf("ServeDatasets, serve %s's dataset (%s) error, details: %s", ss.Owner, dt.ID, err)
			continue
		}
		ss.D.Store(dt.ID, dt)
	}
	return nil
}
