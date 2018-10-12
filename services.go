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

	"github.com/atlasdatatech/atlasmap/mbtiles"
)

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

// ServiceInfo consists of two strings that contain the image type and a URL.
type ServiceInfo struct {
	ImageType string `json:"imageType"`
	URL       string `json:"url"`
}

// ServiceSet is the base type for the HTTP handlers which combines multiple
// mbtiles.DB tilesets.
type ServiceSet struct {
	Styles   map[string]*StyleService
	Tilesets map[string]*mbtiles.DB
	Domain   string
	Path     string
}

// New returns a new ServiceSet. Use AddDBOnPath to add a mbtiles file.
func New() *ServiceSet {
	s := &ServiceSet{
		Styles:   make(map[string]*StyleService),
		Tilesets: make(map[string]*mbtiles.DB),
	}
	return s
}

// AddDBOnPath interprets filename as mbtiles file which is opened and which will be
// served under "/services/<urlPath>" by Handler(). The parameter urlPath may not be
// nil, otherwise an error is returned. In case the DB cannot be opened the returned
// error is non-nil.
func (s *ServiceSet) AddDBOnPath(filename string, urlPath string) error {
	var err error
	if urlPath == "" {
		return fmt.Errorf("path parameter may not be empty")
	}
	ts, err := mbtiles.NewDB(filename)
	if err != nil {
		return fmt.Errorf("could not open mbtiles file %q: %v", filename, err)
	}
	s.Tilesets[urlPath] = ts
	return nil
}

// LoadServiceSet interprets filename as mbtiles file which is opened and which will be
func LoadServiceSet() (*ServiceSet, error) {
	s := &ServiceSet{
		Styles:   make(map[string]*StyleService),
		Tilesets: make(map[string]*mbtiles.DB),
	}

	tilesetsPath := cfgV.GetString("tilesets.path")
	stylesPath := cfgV.GetString("styles.path")
	s.ServeMBTiles(tilesetsPath)
	s.ServeStyles(stylesPath)
	log.Infof("Load ServiceSet all successful")
	return s, nil
}

// ServeMBTiles returns a ServiceSet that combines all .mbtiles files under
// the directory at baseDir. The DBs will all be served under their relative paths
// to baseDir.
func (s *ServiceSet) ServeMBTiles(baseDir string) (err error) {
	var filenames []string
	err = filepath.Walk(baseDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if ext := filepath.Ext(p); ext == ".mbtiles" {
			filenames = append(filenames, p)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("unable to scan tilesets: %v", err)
	}

	for _, filename := range filenames {
		subpath, err := filepath.Rel(baseDir, filename)
		if err != nil {
			return fmt.Errorf("unable to extract URL path for %q: %v", filename, err)
		}
		e := filepath.Ext(filename)
		p := filepath.ToSlash(subpath)
		id := strings.ToLower(p[:len(p)-len(e)])
		err = s.AddDBOnPath(filename, id)
		if err != nil {
			return err
		}
	}
	log.Infof("New from %s successful, tol %d", baseDir, len(filenames))
	return nil
}

// NewFromBaseDir returns a ServiceSet that combines all .mbtiles files under
// the directory at baseDir. The DBs will all be served under their relative paths
// to baseDir.
func NewFromBaseDir(baseDir string) (*ServiceSet, error) {
	var filenames []string
	err := filepath.Walk(baseDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if ext := filepath.Ext(p); ext == ".mbtiles" {
			filenames = append(filenames, p)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("unable to scan tilesets: %v", err)
	}

	s := New()

	for _, filename := range filenames {
		subpath, err := filepath.Rel(baseDir, filename)
		if err != nil {
			return nil, fmt.Errorf("unable to extract URL path for %q: %v", filename, err)
		}
		e := filepath.Ext(filename)
		p := filepath.ToSlash(subpath)
		id := strings.ToLower(p[:len(p)-len(e)])
		err = s.AddDBOnPath(filename, id)
		if err != nil {
			return nil, err
		}
	}
	log.Infof("New from %s successful, tol %d", baseDir, len(filenames))
	return s, nil
}

// Size returns the number of tilesets in this ServiceSet
func (s *ServiceSet) Size() int {
	return len(s.Tilesets)
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
