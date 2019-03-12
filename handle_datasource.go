package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	log "github.com/sirupsen/logrus"

	"github.com/gin-gonic/gin"
	"github.com/teris-io/shortid"
)

type upFileInfo struct {
	id   string
	name string
	ext  string
	dst  string
	size int64
	dir  string
}

func sources4dt(c *gin.Context) ([]*DataSource, error) {
	uid := c.Param("user")
	file, err := c.FormFile("file")
	if err != nil {
		log.Warnf(`handleSources, read %s's upload file error, details: %s`, uid, err)
		return nil, err
	}
	filename := file.Filename
	ext := filepath.Ext(filename)
	lext := strings.ToLower(ext)
	switch lext {
	case CSVEXT, GEOJSONEXT, KMLEXT, GPXEXT, ZIPEXT:
	default:
		return nil, fmt.Errorf("unsupport format")
	}
	name := strings.TrimSuffix(filename, ext)
	name = strings.Replace(strings.TrimSpace(name), " ", "", -1)
	id, _ := shortid.Generate()
	dst := filepath.Join("datasets", uid, name+"."+id+lext)
	if err := c.SaveUploadedFile(file, dst); err != nil {
		return nil, fmt.Errorf(`handleSources, saving uploaded file error, details: %s`, err)
	}

	var dss []*DataSource
	switch lext {
	case ZIPEXT:
		subdir := UnZipToDir(dst)
		files, err := getDatafiles(subdir)
		if err != nil {
			return nil, err
		}
		for file, size := range files {
			subase, err := filepath.Rel(subdir, file)
			if err != nil {
				subase = filepath.Base(file)
			}
			ext := filepath.Ext(file)
			subname := strings.TrimSuffix(subase, ext)
			list := filepath.SplitList(subase)
			if len(list) > 0 {
				subname = strings.Join(list, "_")
			}
			ds := &DataSource{
				ID:      subname + id,
				Name:    subname,
				Tag:     name,
				Geotype: "vector",
				Format:  ext,
				Path:    file,
				Size:    size,
			}
			err = ds.LoadFrom()
			if err != nil {
				log.Error(err)
				continue
			}
			dss = append(dss, ds)
		}
	case CSVEXT, GEOJSONEXT, KMLEXT, GPXEXT:
		// 获取所有记录
		ds := &DataSource{
			ID:     name + "." + id,
			Name:   name,
			Format: lext,
			Path:   dst,
			Size:   file.Size,
		}
		err := ds.LoadFrom()
		if err != nil {
			return nil, err
		}
		dss = append(dss, ds)
	}

	return dss, nil
}

func sources4ts(c *gin.Context) ([]*DataSource, error) {
	uid := c.Param("user")
	file, err := c.FormFile("file")
	if err != nil {
		log.Warnf(`handleSources, read %s's upload file error, details: %s`, uid, err)
		return nil, err
	}
	filename := file.Filename
	ext := filepath.Ext(filename)
	lext := strings.ToLower(ext)
	switch lext {
	case CSVEXT, GEOJSONEXT, KMLEXT, GPXEXT, ZIPEXT:
	default:
		return nil, fmt.Errorf("unsupport format")
	}
	name := strings.TrimSuffix(filename, ext)
	name = strings.Replace(strings.TrimSpace(name), " ", "", -1)
	id, _ := shortid.Generate()
	dst := filepath.Join("tilesets", uid, name+"."+id+lext)
	if err := c.SaveUploadedFile(file, dst); err != nil {
		return nil, fmt.Errorf(`handleSources, saving uploaded file error, details: %s`, err)
	}

	var dss []*DataSource
	switch lext {
	case ZIPEXT:
		subdir := UnZipToDir(dst)
		files, err := getDatafiles(subdir)
		if err != nil {
			return nil, err
		}
		i := 0
		dsarray := make([]*DataSource, len(files))
		var wg sync.WaitGroup
		for file, size := range files {
			subase, err := filepath.Rel(subdir, file)
			if err != nil {
				subase = filepath.Base(file)
			}
			ext := filepath.Ext(file)
			subname := strings.TrimSuffix(subase, ext)
			list := filepath.SplitList(subase)
			if len(list) > 0 {
				subname = strings.Join(list, "_")
			}
			ds := &DataSource{
				ID:      subname + id,
				Name:    subname,
				Tag:     name,
				Geotype: "vector",
				Format:  ext,
				Path:    file,
				Size:    size,
			}
			wg.Add(1)
			go func(ds *DataSource, i int) {
				defer wg.Done()
				err := ds.ToGeojson()
				if err != nil {
					log.Error(err)
					dsarray[i] = nil
				} else {
					dsarray[i] = ds
				}
			}(ds, i)
		}
		wg.Wait()
		for _, ds := range dsarray {
			if ds != nil {
				dss = append(dss, ds)
			}
		}
		// wait
	case CSVEXT, GEOJSONEXT, KMLEXT, GPXEXT:
		// 获取所有记录
		ds := &DataSource{
			ID:     name + "." + id,
			Name:   name,
			Format: lext,
			Path:   dst,
			Size:   file.Size,
		}
		err := ds.ToGeojson()
		if err != nil {
			return nil, err
		}
		dss = append(dss, ds)
	}
	return dss, nil
}
