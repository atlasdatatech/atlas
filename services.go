package main

//go:generate go run -tags=dev assets_generate.go

import (
	log "github.com/sirupsen/logrus"
)

// ServiceSet is the base type for the HTTP handlers which combines multiple
// mbtiles.DB tilesets.
type ServiceSet struct {
	User     string
	Styles   map[string]*StyleService
	Fonts    map[string]*FontService
	Tilesets map[string]*TileService
	Datasets map[string]*DataService
}

// LoadServiceSet interprets filename as mbtiles file which is opened and which will be
func LoadServiceSet() (*ServiceSet, error) {
	s := &ServiceSet{
		Styles:   make(map[string]*StyleService),
		Fonts:    make(map[string]*FontService),
		Tilesets: make(map[string]*TileService),
		Datasets: make(map[string]*DataService),
	}
	tilesets := cfgV.GetString("assets.tilesets")
	styles := cfgV.GetString("assets.styles")
	fonts := cfgV.GetString("assets.fonts")
	s.ServeMBTiles(tilesets)
	s.ServeStyles(styles)
	s.ServeFonts(fonts)
	s.LoadDatasetServices()
	log.Infof("Load ServiceSet all successful")
	return s, nil
}
