package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3" // import sqlite3 driver
	log "github.com/sirupsen/logrus"
)

// Dataset represents an mbtiles file connection.
type Dataset struct {
	Type     string
	filename string  // name of tile mbtiles file
	db       *sql.DB // database connection for mbtiles file
}

// DataService represents an mbtiles file connection.
type DataService struct {
	ID      string
	URL     string // tile format: PNG, JPG, PBF, WEBP
	Hash    string
	Type    string
	State   bool     // true if UTFGrids have corresponding key / value data that need to be joined and returned with the UTFGrid
	Dataset *Dataset // database connection for mbtiles file
}

// CreateDataService creates a new StyleService instance.
// Connection is closed by runtime on application termination or by calling
// its Close() method.
func CreateDataService(setName string, dataID string) (*DataService, error) {

	if setName == "" || dataID == "" {
		return nil, fmt.Errorf("path parameter may not be empty")
	}
	dataset, err := CreateDataset(setName)
	if err != nil {
		return nil, fmt.Errorf("could not open mbtiles file %q: %v", setName, err)
	}

	out := &DataService{
		ID:      dataID,
		URL:     setName, //should not add / at the end
		Type:    dataset.Type,
		State:   true,
		Dataset: dataset,
	}
	return out, nil
}

// AddDataset interprets filename as mbtiles file which is opened and which will be
// served under "/services/<urlPath>" by Handler(). The parameter urlPath may not be
// nil, otherwise an error is returned. In case the DB cannot be opened the returned
// error is non-nil.
func (s *ServiceSet) AddDataset(name string, dataID string) error {
	if dataID == "" || "" == name {
		return fmt.Errorf("path parameter may not be empty")
	}
	ts, err := CreateDataService(name, dataID)
	if err != nil {
		return fmt.Errorf("could not open mbtiles file %q: %v", name, err)
	}
	s.Datasets[dataID] = ts

	return nil
}

func (s *ServiceSet) importBanks(name string, id string) error {
	if id == "" || "" == name {
		return fmt.Errorf("path parameter may not be empty")
	}

	file := filepath.Join(cfgV.GetString("assets.datasets"), "metadata.db")
	db, err := sql.Open("sqlite3", file)
	if err != nil {
		return err
	}
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		log.Fatal(err)
	}
	stmt, err := tx.Prepare("insert into datasets(id, name) values(?, ?)")
	if err != nil {
		log.Fatal(err)
	}
	defer stmt.Close()
	_, err = stmt.Exec(id, name)
	if err != nil {
		log.Fatal(err)
	}
	tx.Commit()

	return nil
}

func (s *ServiceSet) updateMeta(name string, id string) error {
	if id == "" || "" == name {
		return fmt.Errorf("path parameter may not be empty")
	}
	file := filepath.Join(cfgV.GetString("assets.datasets"), "metadata.db")
	db, err := sql.Open("sqlite3", file)
	if err != nil {
		return err
	}
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		log.Fatal(err)
	}
	stmt, err := tx.Prepare("insert into datasets(id, name) values(?, ?)")
	if err != nil {
		log.Fatal(err)
	}
	defer stmt.Close()
	_, err = stmt.Exec(id, name)
	if err != nil {
		log.Fatal(err)
	}
	tx.Commit()

	return nil
}

// ServeDatasets returns a ServiceSet that combines all .mbtiles files under
// the directory at baseDir. The DBs will all be served under their relative paths
// to baseDir.
func (s *ServiceSet) ServeDatasets(baseDir string) (err error) {
	file := filepath.Join(cfgV.GetString("assets.datasets"), "metadata.db")
	db, err := sql.Open("sqlite3", file)
	if err != nil {
		return err
	}
	defer db.Close()
	//Saves last modified mbtiles time for setting Last-Modified header
	_, err = os.Stat(file)
	if err != nil {
		sqlStmt := `
		create table datasets (id text not null primary key, name text);
		delete from datasets;
		`
		_, err = db.Exec(sqlStmt)
		if err != nil {
			log.Printf("%q: %s\n", err, sqlStmt)
			return err
		}
	}

	var (
		id    string
		value string
	)
	metadata := make(map[string]interface{})

	rows, err := db.Query("select id,name from datasets")
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		rows.Scan(&id, &value)
		// err = json.Unmarshal([]byte(value), &metadata)
		// if err != nil {
		// 	return fmt.Errorf("unable to parse JSON metadata item: %v", err)
		// }
		metadata[id] = value

		err = s.AddDataset(value, id)
		if err != nil {
			log.Errorf(`ServeDatasets, add mbtiles: %s; user: %s ^^`, err, s.User)
		}

	}
	log.Infof("ServeDatasets, loaded %d mbtiles for %s ~", len(s.Datasets), s.User)
	return nil
}

// CreateDataset creates a new MBTiles instance.
// Connection is closed by runtime on application termination or by calling
// its Close() method.
func CreateDataset(name string) (*Dataset, error) {
	out := Dataset{
		filename: name,
	}
	return &out, nil
}

// GetGeoJSON reads a tile with tile identifiers z, x, y into provided *[]byte.
// data will be nil if the tile does not exist in the database
func (dataset *Dataset) GetGeoJSON(data *[]byte) error {
	return nil
}

// Close closes the database connection
func (dataset *Dataset) Close() error {
	return dataset.db.Close()
}
