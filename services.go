package main

//go:generate go run -tags=dev assets_generate.go

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"
)

// ServiceSet is the base type for the HTTP handlers which combines multiple
// mbtiles.DB tilesets.
type ServiceSet struct {
	User     string
	Domain   string
	Path     string
	Styles   map[string]*StyleService
	Fonts    map[string]*FontService
	Tilesets map[string]*MBTilesService
}

// LoadServiceSet interprets filename as mbtiles file which is opened and which will be
func LoadServiceSet(user string) (*ServiceSet, error) {

	home := cfgV.GetString("users.home")
	if _, err := os.Stat(filepath.Join(home, user)); os.IsNotExist(err) {
		// user path does not exist
		log.Error(err)
		return nil, err
	}
	s := &ServiceSet{
		User:     user,
		Styles:   make(map[string]*StyleService),
		Fonts:    make(map[string]*FontService),
		Tilesets: make(map[string]*MBTilesService),
	}
	tilesets := cfgV.GetString("users.tilesets")
	styles := cfgV.GetString("users.styles")
	fonts := cfgV.GetString("users.fonts")

	tilesetsPath := filepath.Join(home, user, tilesets)
	stylesPath := filepath.Join(home, user, styles)
	fontsPath := filepath.Join(home, user, fonts)
	s.ServeMBTiles(tilesetsPath)
	s.ServeStyles(stylesPath)
	s.ServeFonts(fontsPath)
	log.Infof("Load ServiceSet all successful")
	return s, nil
}

// scheme returns the underlying URL scheme of the original request.
func scheme(r *http.Request) string {

	if r.TLS != nil {
		return "https"
	}
	if scheme := r.Header.Get("X-Forwarded-Proto"); scheme != "" {
		return scheme
	}
	if scheme := r.Header.Get("X-Forwarded-Protocol"); scheme != "" {
		return scheme
	}
	if ssl := r.Header.Get("X-Forwarded-Ssl"); ssl == "on" {
		return "https"
	}
	if scheme := r.Header.Get("X-Url-Scheme"); scheme != "" {
		return scheme
	}
	return "http"
}

// rootURL returns the root URL of the service. If s.Domain is non-empty, it
// will be used as the hostname. If s.Path is non-empty, it will be used as a
// prefix.
func (s *ServiceSet) rootURL(r *http.Request) string {
	host := r.Host
	if len(s.Domain) > 0 {
		host = s.Domain
	}

	root := fmt.Sprintf("%s://%s", scheme(r), host)
	if len(s.Path) > 0 {
		root = fmt.Sprintf("%s/%s", root, s.Path)
	}

	return root
}

type tileCoord struct {
	z    uint8
	x, y uint64
}

// tileCoordFromString parses and returns tileCoord coordinates and an optional
// extension from the three parameters. The parameter z is interpreted as the
// web mercator zoom level, it is supposed to be an unsigned integer that will
// fit into 8 bit. The parameters x and y are interpreted as longitude and
// latitude tile indices for that zoom level, both are supposed be integers in
// the integer interval [0,2^z). Additionally, y may also have an optional
// filename extension (e.g. "42.png") which is removed before parsing the
// number, and returned, too. In case an error occured during parsing or if the
// values are not in the expected interval, the returned error is non-nil.
func tileCoordFromString(z, x, y string) (tc tileCoord, ext string, err error) {
	var z64 uint64
	if z64, err = strconv.ParseUint(z, 10, 8); err != nil {
		err = fmt.Errorf("cannot parse zoom level: %v", err)
		return
	}
	tc.z = uint8(z64)
	const (
		errMsgParse = "cannot parse %s coordinate axis: %v"
		errMsgOOB   = "%s coordinate (%d) is out of bounds for zoom level %d"
	)
	if tc.x, err = strconv.ParseUint(x, 10, 64); err != nil {
		err = fmt.Errorf(errMsgParse, "first", err)
		return
	}
	if tc.x >= (1 << z64) {
		err = fmt.Errorf(errMsgOOB, "x", tc.x, tc.z)
		return
	}
	s := y
	if l := strings.LastIndex(s, "."); l >= 0 {
		s, ext = s[:l], s[l:]
	}
	if tc.y, err = strconv.ParseUint(s, 10, 64); err != nil {
		err = fmt.Errorf(errMsgParse, "y", err)
		return
	}
	if tc.y >= (1 << z64) {
		err = fmt.Errorf(errMsgOOB, "y", tc.y, tc.z)
		return
	}
	return
}
