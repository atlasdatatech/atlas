package main

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/axgle/mahonia"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"

	"github.com/gin-gonic/gin"
	"github.com/teris-io/shortid"
)

func saveSource(c *gin.Context) (*DataSource, error) {
	uid := c.GetString(identityKey)
	if uid == "" {
		uid = c.GetString(userKey)
	}
	file, err := c.FormFile("file")
	if err != nil {
		log.Warnf(`saveUploadFile, read upload file error, details: %s`, err)
		return nil, err
	}
	ext := filepath.Ext(file.Filename)
	lext := strings.ToLower(ext)
	path := c.Request.URL.Path
	dir := path[1:]
	dir = dir[:strings.Index(dir, "/")]
	switch lext {
	case CSVEXT, GEOJSONEXT, KMLEXT, GPXEXT, ZIPEXT:
		dir = viper.GetString("paths.uploads")
	case MBTILESEXT:
		dir = viper.GetString("paths.tilesets")
	default:
		return nil, fmt.Errorf("unsupport format")
	}
	id, _ := shortid.Generate()
	name := strings.TrimSuffix(file.Filename, ext)
	dst := filepath.Join(dir, uid, id+"."+name+lext)
	if err := c.SaveUploadedFile(file, dst); err != nil {
		return nil, fmt.Errorf(`handleSources, saving uploaded file error, details: %s`, err)
	}
	ds := &DataSource{
		ID:     id,
		Name:   name,
		Tag:    name,
		Owner:  uid,
		Format: lext,
		Path:   dst,
		Size:   file.Size,
	}
	return ds, nil
}

func loadZipSources(ds *DataSource) ([]*DataSource, error) {
	var dss []*DataSource
	switch ds.Format {
	case ZIPEXT:
		tmpdir := strings.TrimSuffix(ds.Path, filepath.Ext(ds.Path))
		err := UnZipToDir(ds.Path, tmpdir)
		if err != nil {
			return nil, err
		}
		files, err := getDatafiles(tmpdir)
		if err != nil {
			return nil, err
		}
		for file, size := range files {
			subase, err := filepath.Rel(tmpdir, file)
			if err != nil {
				subase = filepath.Base(file)
			}
			ext := filepath.Ext(file)
			subname := filepath.ToSlash(subase)
			subname = strings.Replace(subname, "/", "_", -1)
			subname = strings.TrimSuffix(subname, ext)
			subid, _ := shortid.Generate()
			subds := &DataSource{
				ID:     subid,
				Name:   subname,
				Tag:    ds.Name,
				Owner:  ds.Owner,
				Format: strings.ToLower(ext),
				Path:   file,
				Size:   size,
			}
			dss = append(dss, subds)
		}
	case CSVEXT, GEOJSONEXT, KMLEXT, GPXEXT:
		dss = append(dss, ds)
	}
	if len(dss) == 0 {
		return nil, fmt.Errorf("no valid source file")
	}
	return dss, nil
}

func loadFromSources(dss []*DataSource) error {
	var wg sync.WaitGroup
	for i, ds := range dss {
		wg.Add(1)
		go func(ds *DataSource, i int) {
			defer wg.Done()
			err := ds.LoadFrom()
			if err != nil {
				log.Warn(err)
			}
		}(ds, i)
	}
	wg.Wait()
	return nil
}

func sources2ts(task *Task, dss []*DataSource) (*Tileset, error) {
	s := time.Now()
	var wg sync.WaitGroup
	total := 0
	layers := make([]string, len(dss))
	for i, ds := range dss {
		wg.Add(1)
		go func(ds *DataSource, i int) {
			defer wg.Done()
			total += ds.Total
			switch ds.Format {
			case KMLEXT, GPXEXT:
				var params []string
				params = append(params, ds.Path)
				if runtime.GOOS == "windows" {
					decoder := mahonia.NewDecoder("gbk")
					gbk := strings.Join(params, ",")
					gbk = decoder.ConvertString(gbk)
					params = strings.Split(gbk, ",")
				}
				log.Println(params)
				cmd := exec.Command("togeojson", params...)
				var stdout bytes.Buffer
				cmd.Stdout = &stdout
				err := cmd.Start()
				if err != nil {
					log.Error(err)
					return
				}
				err = cmd.Wait()
				if err != nil {
					log.Error(err)
					return
				}
				outfile := strings.TrimSuffix(ds.Path, ds.Format) + GEOJSONEXT
				err = ioutil.WriteFile(outfile, stdout.Bytes(), os.ModePerm)
				if err != nil {
					log.Errorf("togeojson write geojson file failed,details: %s\n", err)
					return
				}
				layers[i] = outfile
			case GEOJSONEXT, CSVEXT:
				layers[i] = ds.Path
			case SHPEXT:
				if size := valSizeShp(ds.Path); size == 0 {
					log.Errorf("invalid shapefile")
					return
				}
				var params []string
				outfile := strings.TrimSuffix(ds.Path, ds.Format) + GEOJSONEXT
				params = append(params, []string{"-f", "GEOJSON", outfile}...)
				//显示进度,读取outbuffer缓冲区
				params = append(params, "-progress")
				params = append(params, "-skipfailures")
				//-overwrite not works
				params = append(params, []string{"-lco", "OVERWRITE=YES"}...)
				//only for shp
				params = append(params, []string{"-nlt", "PROMOTE_TO_MULTI"}...)
				//设置源文件编码
				switch ds.Encoding {
				case "gbk", "big5", "gb18030":
					params = append(params, []string{"--config", "SHAPE_ENCODING", fmt.Sprintf("%s", strings.ToUpper(ds.Encoding))}...)
					log.Warnf("togeojson, src encoding: %s", ds.Encoding)
				}
				params = append(params, ds.Path)
				//window上参数转码
				if runtime.GOOS == "windows" {
					decoder := mahonia.NewDecoder("gbk")
					gbk := strings.Join(params, ",")
					gbk = decoder.ConvertString(gbk)
					params = strings.Split(gbk, ",")
				}
				cmd := exec.Command("ogr2ogr", params...)
				err := cmd.Start()
				if err != nil {
					log.Error(err)
					return
				}
				err = cmd.Wait()
				if err != nil {
					log.Error(err)
					return
				}
				layers[i] = outfile
			default:
			}
		}(ds, i)
	}
	wg.Wait()
	task.Progress = 20
	log.Infof("convert %d sources to geojson, takes: %v", len(dss), time.Since(s))
	s = time.Now()
	id, _ := shortid.Generate()
	outfile := filepath.Join(viper.GetString("paths.tilesets"), task.Owner, id+MBTILESEXT)
	var params []string
	//显示进度,读取outbuffer缓冲区
	absPath, err := filepath.Abs(outfile)
	if err != nil {
		return nil, err
	}
	params = append(params, "-zg")
	params = append(params, "-o")
	params = append(params, absPath)
	params = append(params, []string{"-n", task.Name}...)
	params = append(params, "--force")
	params = append(params, "--drop-densest-as-needed")
	params = append(params, "--extend-zooms-if-still-dropping")
	params = append(params, layers...)
	fmt.Println(params)
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd := exec.Command("tippecanoe", params...)
	stdoutIn, _ := cmd.StdoutPipe()
	stderrIn, _ := cmd.StderrPipe()
	// var errStdout, errStderr error
	stdout := io.MultiWriter(os.Stdout, &stdoutBuf)
	stderr := io.MultiWriter(os.Stderr, &stderrBuf)
	err = cmd.Start()
	if err != nil {
		return nil, err
	}
	go func() {
		io.Copy(stdout, stdoutIn)
	}()
	go func() {
		io.Copy(stderr, stderrIn)
	}()
	cp := task.Progress
	ticker := time.NewTicker(500 * time.Millisecond)
	go func(task *Task) {
		i := 0
		for {
			select {
			case <-ticker.C:
				i++
				rows := i * 1000
				task.Progress = int(float64(rows)/float64(total)*100) + cp
				if task.Progress > 99 {
					task.Progress = 99
				}
			}
		}
	}(task)

	err = cmd.Wait()
	ticker.Stop()

	if err != nil {
		return nil, err
	}
	layercnt := 0
	for _, l := range layers {
		if l != "" {
			layercnt++
		}
	}
	log.Infof("publish %d data source to tilesets(%s), takes: %v", layercnt, outfile, time.Since(s))
	s = time.Now()

	ds := &DataSource{
		ID:    task.Base,
		Name:  task.Name,
		Owner: task.Owner,
		Path:  outfile,
	}
	//加载mbtiles
	ts, err := LoadTileset(ds)
	if err != nil {
		log.Errorf("publishTileset, could not load the new tileset %s, details: %s", outfile, err)
		return nil, err
	}
	log.Infof("load tilesets(%s), takes: %v", outfile, time.Since(s))
	return ts, nil
}
