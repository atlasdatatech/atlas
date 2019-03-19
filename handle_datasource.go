package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

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
	dst := filepath.Join(dir, uid, name+lext)
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
				log.Error(err)
			}
		}(ds, i)
	}
	wg.Wait()
	return nil
}

func sources2ts(task *Task, dss []*DataSource) (*Tileset, error) {
	s := time.Now()
	var wg sync.WaitGroup
	layers := make([]string, len(dss))
	for i, ds := range dss {
		wg.Add(1)
		go func(ds *DataSource, i int) {
			defer wg.Done()
			outfile := strings.TrimSuffix(ds.Path, ds.Format) + GEOJSONEXT
			err := ds.ToGeojson(outfile)
			if err != nil {
				log.Error(err)
			} else {
				layers[i] = outfile
			}
		}(ds, i)
	}
	wg.Wait()
	log.Infof("convert %d sources to geojson, takes: %v", len(dss), time.Since(s))
	s = time.Now()

	outfile := filepath.Join(viper.GetString("paths.tilesets"), task.Owner, task.ID+MBTILESEXT)

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
	err = cmd.Wait()
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
		ID:    task.ID,
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
