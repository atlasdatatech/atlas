package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/paulmach/orb/geojson"
	log "github.com/sirupsen/logrus"

	"github.com/gin-gonic/gin"
)

func listDatasets(c *gin.Context) {
	res := NewRes()

	var dss []*DataService
	uid := c.GetString(identityKey)

	is, ok := pubSet.Load(uid)
	if ok {
		is.(*ServiceSet).D.Range(func(_, v interface{}) bool {
			dss = append(dss, v.(*DataService))
			return true
		})
	}

	res.DoneData(c, dss)
}

func getDatasetInfo(c *gin.Context) {
	res := NewRes()
	uid := c.GetString(identityKey)
	name := c.Param("name")
	if code := checkDataset(name); code != 200 {
		res.Fail(c, code)
		return
	}
	is, ok := pubSet.Load(uid)
	if !ok {
		res.Fail(c, 4044)
		return
	}
	ds, ok := is.(*ServiceSet).D.Load(name)
	if !ok {
		res.Fail(c, 4045)
		return
	}
	res.DoneData(c, ds.(*Dataset))
}

func getDistinctValues(c *gin.Context) {
	res := NewRes()
	name := c.Param("name")
	if code := checkDataset(name); code != 200 {
		res.Fail(c, code)
		return
	}
	var body struct {
		Field string `form:"field" json:"field" binding:"required"`
	}
	err := c.Bind(&body)
	if err != nil {
		log.Error(err)
		res.Fail(c, 4001)
		return
	}
	s := fmt.Sprintf(`SELECT distinct(%s) as val,count(*) as cnt FROM "%s" GROUP BY %s;`, body.Field, name, body.Field)
	fmt.Println(s)
	rows, err := db.Raw(s).Rows()
	if err != nil {
		log.Error(err)
		res.Fail(c, 5001)
		return
	}
	defer rows.Close()
	type ValCnt struct {
		Val string
		Cnt int
	}
	var valCnts []ValCnt
	for rows.Next() {
		var vc ValCnt
		// ScanRows scan a row into user
		db.ScanRows(rows, &vc)
		valCnts = append(valCnts, vc)
		// do something
	}
	res.DoneData(c, valCnts)
}

func getGeojson(c *gin.Context) {
	res := NewRes()
	name := c.Param("name")
	fields := c.Query("fields")
	filter := c.Query("filter")

	if code := checkDataset(name); code != 200 {
		res.Fail(c, code)
		return
	}

	selStr := "st_asgeojson(geom) as geom "
	if "" != fields {
		selStr = selStr + "," + fields
	}
	var whr string
	if "" != filter {
		whr = " WHERE " + filter
	}
	s := fmt.Sprintf(`SELECT %s FROM %s %s;`, selStr, name, whr)
	rows, err := db.Raw(s).Rows() // (*sql.Rows, error)
	if err != nil {
		log.Error(err)
		res.Fail(c, 5001)
		return
	}
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil {
		res.Fail(c, 5001)
		return
	}
	fc := geojson.NewFeatureCollection()
	for rows.Next() {
		// Scan needs an array of pointers to the values it is setting
		// This creates the object and sets the values correctly
		vals := make([]interface{}, len(cols))
		for i := range cols {
			vals[i] = new(sql.RawBytes)
		}
		err = rows.Scan(vals...)
		if err != nil {
			log.Error(err)
		}

		f := geojson.NewFeature(nil)

		for i, t := range cols {
			// skip nil values.
			if vals[i] == nil {
				continue
			}
			rb, ok := vals[i].(*sql.RawBytes)
			if !ok {
				log.Errorf("Cannot convert index %d column %s to type *sql.RawBytes", i, t.Name())
				continue
			}

			switch t.Name() {
			case "geom":
				geom, err := geojson.UnmarshalGeometry([]byte(*rb))
				if err != nil {
					log.Errorf("UnmarshalGeometry from geojson result error, index %d column %s", i, t.Name())
					continue
				}
				f.Geometry = geom.Geometry()
			default:
				v := string(*rb)
				switch cols[i].DatabaseTypeName() {
				case "INT", "INT4":
					f.Properties[t.Name()], _ = strconv.Atoi(v)
				case "NUMERIC", "DECIMAL": //number
					f.Properties[t.Name()], _ = strconv.ParseFloat(v, 64)
				// case "BOOL":
				// case "TIMESTAMPTZ":
				// case "_VARCHAR":
				// case "TEXT", "VARCHAR", "BIGINT":
				default:
					f.Properties[t.Name()] = v
				}
			}

		}
		fc.Append(f)
	}
	var extent []byte
	stbox := fmt.Sprintf(`SELECT st_asgeojson(st_extent(geom)) as extent FROM %s %s;`, name, whr)
	db.Raw(stbox).Row().Scan(&extent) // (*sql.Rows, error)
	ext, err := geojson.UnmarshalGeometry(extent)
	if err == nil {
		fc.BBox = geojson.NewBBox(ext.Geometry().Bound())
	}
	gj, err := fc.MarshalJSON()
	if err != nil {
		log.Errorf("unable to MarshalJSON of featureclection.")
		res.FailMsg(c, "unable to MarshalJSON of featureclection.")
		return
	}
	c.JSON(http.StatusOK, json.RawMessage(gj))
}

func queryGeojson(c *gin.Context) {
	res := NewRes()
	name := c.Param("name")

	var body struct {
		Geom   string `form:"geom" json:"geom"`
		Fields string `form:"fields" json:"fields"`
		Filter string `form:"filter" json:"filter"`
	}
	err := c.Bind(&body)
	if err != nil {
		res.Fail(c, 4001)
		return
	}

	selStr := "st_asgeojson(geom) as geom "
	if "" != body.Fields {
		selStr = selStr + "," + body.Fields
	}
	var whrStr string
	if body.Geom != "" {
		whrStr = fmt.Sprintf(` WHERE geom && st_geomfromgeojson('%s')`, body.Geom)
		if "" != body.Filter {
			whrStr = whrStr + " AND " + body.Filter
		}
	} else {
		if "" != body.Filter {
			whrStr = " WHERE " + body.Filter
		}
	}

	s := fmt.Sprintf(`SELECT %s FROM %s  %s;`, selStr, name, whrStr)
	rows, err := db.Raw(s).Rows() // (*sql.Rows, error)
	if err != nil {
		log.Error(err)
		res.Fail(c, 5001)
		return
	}
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil {
		res.Fail(c, 5001)
		return
	}
	fc := geojson.NewFeatureCollection()
	for rows.Next() {
		// Scan needs an array of pointers to the values it is setting
		// This creates the object and sets the values correctly
		vals := make([]interface{}, len(cols))
		for i := range cols {
			vals[i] = new(sql.RawBytes)
		}
		err = rows.Scan(vals...)
		if err != nil {
			log.Error(err)
		}

		f := geojson.NewFeature(nil)

		for i, t := range cols {
			// skip nil values.
			if vals[i] == nil {
				continue
			}
			rb, ok := vals[i].(*sql.RawBytes)
			if !ok {
				log.Errorf("Cannot convert index %d column %s to type *sql.RawBytes", i, t.Name())
				continue
			}

			switch t.Name() {
			case "geom":
				geom, err := geojson.UnmarshalGeometry([]byte(*rb))
				if err != nil {
					log.Errorf("UnmarshalGeometry from geojson result error, index %d column %s", i, t.Name())
					continue
				}
				f.Geometry = geom.Geometry()
			default:
				v := string(*rb)
				switch cols[i].DatabaseTypeName() {
				case "INT", "INT4":
					f.Properties[t.Name()], _ = strconv.Atoi(v)
				case "NUMERIC", "DECIMAL": //number
					f.Properties[t.Name()], _ = strconv.ParseFloat(v, 64)
				// case "BOOL":
				// case "TIMESTAMPTZ":
				// case "_VARCHAR":
				// case "TEXT", "VARCHAR", "BIGINT":
				default:
					f.Properties[t.Name()] = v
				}
			}

		}
		fc.Append(f)
	}
	gj, err := fc.MarshalJSON()
	if err != nil {
		log.Errorf("unable to MarshalJSON of featureclection.")
		res.FailMsg(c, "unable to MarshalJSON of featureclection.")
		return
	}
	res.DoneData(c, json.RawMessage(gj))
}

func cubeQuery(c *gin.Context) {
	res := NewRes()
	var body struct {
		SQL string `form:"sql" json:"sql" binding:"required"`
	}
	err := c.Bind(&body)
	if err != nil {
		res.Fail(c, 4001)
		return
	}
	rows, err := db.Raw(body.SQL).Rows() // (*sql.Rows, error)
	if err != nil {
		log.Error(err)
		res.Fail(c, 5001)
		return
	}
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil {
		log.Error(err)
		res.Fail(c, 5001)
		return
	}
	var t [][]string
	for rows.Next() {
		// Scan needs an array of pointers to the values it is setting
		// This creates the object and sets the values correctly
		vals := make([]sql.RawBytes, len(cols))
		valsScer := make([]interface{}, len(vals))
		for i := range vals {
			valsScer[i] = &vals[i]
		}
		err = rows.Scan(valsScer...)
		if err != nil {
			log.Error(err)
		}
		var r []string
		for _, v := range vals {
			// skip nil values.
			if v == nil {
				continue
			}
			r = append(r, string(v))
		}
		if len(r) == 0 {
			continue
		}
		t = append(t, r)
	}
	res.DoneData(c, t)
}

func queryExec(c *gin.Context) {

	res := NewRes()
	var body struct {
		SQL string `form:"sql" json:"sql" binding:"required"`
	}
	err := c.Bind(&body)
	if err != nil {
		res.Fail(c, 4001)
		return
	}
	rows, err := db.Raw(body.SQL).Rows() // (*sql.Rows, error)
	if err != nil {
		log.Error(err)
		res.Fail(c, 5001)
		return
	}
	defer rows.Close()

	cols, _ := rows.ColumnTypes()
	var ams [][]interface{}
	for rows.Next() {
		// Create a slice of interface{}'s to represent each column,
		// and a second slice to contain pointers to each item in the columns slice.
		columns := make([]sql.RawBytes, len(cols))
		columnPointers := make([]interface{}, len(cols))
		for i := range columns {
			columnPointers[i] = &columns[i]
		}

		// Scan the result into the column pointers...
		if err := rows.Scan(columnPointers...); err != nil {
			log.Error(err)
			continue
		}

		// Create our map, and retrieve the value for each column from the pointers slice,
		// storing it in the map with the name of the column as the key.
		m := make([]interface{}, len(cols))
		for i, col := range columns {
			// if cols[i].Name() == "geom" || cols[i].Name() == "search" {
			// 	continue
			// }
			//"NVARCHAR", "DECIMAL", "BOOL", "INT", "BIGINT".
			v := string(col)
			switch cols[i].DatabaseTypeName() {
			case "INT", "INT4":
				m[i], _ = strconv.Atoi(v)
			case "NUMERIC", "DECIMAL": //number
				m[i], _ = strconv.ParseFloat(v, 64)
			// case "BOOL":
			// case "TIMESTAMPTZ":
			// case "_VARCHAR":
			// case "TEXT", "VARCHAR", "BIGINT":
			default:
				m[i] = v
			}
		}
		// fmt.Print(m)
		ams = append(ams, m)
	}
	res.DoneData(c, ams)
}

func queryBusiness(c *gin.Context) {
	res := NewRes()
	name := c.Param("name")
	var linkTables []string
	if name != "banks" {
		res.DoneData(c, gin.H{
			name: linkTables,
		})
		return
	}
	linkTables = cfgV.GetStringSlice("business.banks.linked")
	res.DoneData(c, gin.H{
		name: linkTables,
	})
}

func getBuffers(c *gin.Context) {
	res := NewRes()
	name := c.Param("name")
	rs := c.Query("radius")
	t := c.Query("type")
	bsuffix := cfgV.GetString("buffers.suffix")
	tbname := name + bsuffix //circle
	switch t {
	case "circle":
	case "block", "":
		bprefix := cfgV.GetString("buffers.prefix")
		tbname = bprefix + tbname
	// case "time", "voronoi":
	default:
		res.FailMsg(c, "unkown buffer type")
		return
	}
	r, _ := strconv.ParseFloat(rs, 64)
	if r == 0 {
		res.FailMsg(c, "invalid radius value")
		return
	}
	if code := buffering(name, r); code != 200 {
		log.Error(codes[code])
		res.Fail(c, code)
		return
	}

	fields := c.Query("fields")
	filter := c.Query("filter")

	selStr := "st_asgeojson(b.geom) as geom "

	if "" != fields {
		flds := strings.Split(fields, ",")
		if len(flds) == 1 {
			selStr = selStr + ", a." + flds[0]
		} else {
			selStr = selStr + ", a." + strings.Join(flds, ", a.")
		}
	}
	whr := " WHERE a.id = b.id "
	if "" != filter {
		whr += " AND ( " + filter + " )"
		whr = strings.Replace(whr, " id ", " a.id ", -1)
		whr = strings.Replace(whr, " id=", " a.id= ", -1)
		whr = strings.Replace(whr, " geom ", " a.geom ", -1)
		whr = strings.Replace(whr, " (geom", " (a.geom ", -1)
		whr = strings.Replace(whr, "geom) ", " a.geom) ", -1)
	}
	s := fmt.Sprintf(`SELECT %s FROM %s as a, %s as b %s;`, selStr, name, tbname, whr)
	rows, err := db.Raw(s).Rows() // (*sql.Rows, error)
	if err != nil {
		log.Error(err)
		res.Fail(c, 5001)
		return
	}
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil {
		res.Fail(c, 5001)
		return
	}
	fc := geojson.NewFeatureCollection()
	for rows.Next() {
		// Scan needs an array of pointers to the values it is setting
		// This creates the object and sets the values correctly
		vals := make([]interface{}, len(cols))
		for i := range cols {
			vals[i] = new(sql.RawBytes)
		}
		err = rows.Scan(vals...)
		if err != nil {
			log.Error(err)
		}

		// f := newFeatrue(t)
		f := geojson.NewFeature(nil)
		for i, t := range cols {
			// skip nil values.
			if vals[i] == nil {
				continue
			}
			rb, ok := vals[i].(*sql.RawBytes)
			if !ok {
				log.Errorf("Cannot convert index %d column %s to type *sql.RawBytes", i, t.Name())
				continue
			}

			switch t.Name() {
			case "geom":
				geom, err := geojson.UnmarshalGeometry([]byte(*rb))
				if err != nil {
					log.Error(err)
					log.Errorf("UnmarshalGeometry from geojson result error, index %d column %s", i, t.Name())
					continue
				}
				f.Geometry = geom.Geometry()
			default:
				v := string(*rb)
				switch cols[i].DatabaseTypeName() {
				case "INT", "INT4":
					f.Properties[t.Name()], _ = strconv.Atoi(v)
				case "NUMERIC", "DECIMAL": //number
					f.Properties[t.Name()], _ = strconv.ParseFloat(v, 64)
				// case "BOOL":
				// case "TIMESTAMPTZ":
				// case "_VARCHAR":
				// case "TEXT", "VARCHAR", "BIGINT":
				default:
					f.Properties[t.Name()] = v
				}
			}

		}
		fc.Append(f)
	}
	var extent []byte
	stbox := fmt.Sprintf(`SELECT st_asgeojson(st_extent(b.geom)) as extent FROM %s as a,%s as b %s;`, name, tbname, whr)
	db.Raw(stbox).Row().Scan(&extent) // (*sql.Rows, error)
	ext, err := geojson.UnmarshalGeometry(extent)
	if err == nil {
		fc.BBox = geojson.NewBBox(ext.Geometry().Bound())
	}
	gj, err := fc.MarshalJSON()
	if err != nil {
		log.Errorf("unable to MarshalJSON of featureclection.")
		res.FailMsg(c, "unable to MarshalJSON of featureclection.")
		return
	}
	c.JSON(http.StatusOK, json.RawMessage(gj))
}

func getModels(c *gin.Context) {
	res := NewRes()
	name := c.Param("name")
	fields := c.Query("fields")
	filter := c.Query("filter")
	needCacl := c.Query("cacl")

	if code := checkDataset(name); code != 200 {
		res.Fail(c, code)
		return
	}
	if needCacl != "" {
		switch name {
		case "m1":
			err := calcM1()
			if err != nil {
				log.Error(err)
				res.FailErr(c, err)
				// return
			}
		case "m2":
			err := calcM2()
			if err != nil {
				log.Error(err)
				res.FailErr(c, err)
				// return
			}
		case "m3":
			err := calcM3()
			if err != nil {
				log.Error(err)
				res.FailErr(c, err)
				// return
			}
		case "m4":
			err := calcM4()
			if err != nil {
				log.Error(err)
				res.FailErr(c, err)
				// return
			}
		case "m5":
			err := calcM5()
			if err != nil {
				log.Error(err)
				res.FailErr(c, err)
				// return
			}
		default:
			res.FailMsg(c, "unkown model name")
			return
		}
	}
	if fields == "" {
		fields = " * "
	}
	if filter != "" {
		filter = " WHERE " + filter
	}

	s := fmt.Sprintf(`SELECT %s FROM %s %s;`, fields, name, filter)
	rows, err := db.Raw(s).Rows() // (*sql.Rows, error)
	if err != nil {
		log.Error(err)
		res.Fail(c, 5001)
		return
	}
	defer rows.Close()
	cols, _ := rows.ColumnTypes()
	var ams []map[string]interface{}
	for rows.Next() {
		// Create a slice of interface{}'s to represent each column,
		// and a second slice to contain pointers to each item in the columns slice.
		columns := make([]sql.RawBytes, len(cols))
		columnPointers := make([]interface{}, len(cols))
		for i := range columns {
			columnPointers[i] = &columns[i]
		}

		// Scan the result into the column pointers...
		if err := rows.Scan(columnPointers...); err != nil {
			log.Error(err)
			continue
		}

		// Create our map, and retrieve the value for each column from the pointers slice,
		// storing it in the map with the name of the column as the key.
		m := make(map[string]interface{})
		for i, col := range columns {
			// if col == nil {
			// continue
			// }
			//"NVARCHAR", "DECIMAL", "BOOL", "INT", "BIGINT".
			v := string(col)
			switch cols[i].DatabaseTypeName() {
			case "INT", "INT4":
				m[cols[i].Name()], _ = strconv.Atoi(v)
			case "NUMERIC", "DECIMAL": //number
				m[cols[i].Name()], _ = strconv.ParseFloat(v, 64)
			// case "BOOL":
			// case "TIMESTAMPTZ":
			// case "_VARCHAR":
			// case "TEXT", "VARCHAR", "BIGINT":
			default:
				m[cols[i].Name()] = v
			}
		}
		// fmt.Print(m)
		ams = append(ams, m)
	}
	c.JSON(http.StatusOK, ams)
}

func searchGeos(c *gin.Context) {
	// res := NewRes()
	searchType := c.Param("name")
	keyword := c.Query("keyword")
	var ams []map[string]interface{}

	log.Println("***********", keyword, "**************")
	if searchType != "search" || keyword == "" {
		// res.Fail(c, 4001)
		c.JSON(http.StatusOK, ams)
		return
	}
	search := func(s string, keyword string) {
		stmt, err := db.DB().Prepare(s)
		if err != nil {
			log.Error(err)
			return
		}
		defer stmt.Close()
		rows, err := stmt.Query(keyword)
		if err != nil {
			log.Error(err)
			return
		}
		defer rows.Close()

		cols, _ := rows.ColumnTypes()
		for rows.Next() {
			// Create a slice of interface{}'s to represent each column,
			// and a second slice to contain pointers to each item in the columns slice.
			columns := make([]sql.RawBytes, len(cols))
			columnPointers := make([]interface{}, len(cols))
			for i := range columns {
				columnPointers[i] = &columns[i]
			}

			// Scan the result into the column pointers...
			if err := rows.Scan(columnPointers...); err != nil {
				log.Error(err)
				continue
			}

			// Create our map, and retrieve the value for each column from the pointers slice,
			// storing it in the map with the name of the column as the key.
			m := make(map[string]interface{})
			for i, col := range columns {
				if col == nil {
					continue
				}
				//"NVARCHAR", "DECIMAL", "BOOL", "INT", "BIGINT".
				v := string(col)
				switch cols[i].DatabaseTypeName() {
				case "INT", "INT4":
					m[cols[i].Name()], _ = strconv.Atoi(v)
				case "NUMERIC", "DECIMAL": //number
					m[cols[i].Name()], _ = strconv.ParseFloat(v, 64)
				// case "BOOL":
				// case "TIMESTAMPTZ":
				// case "_VARCHAR":
				// case "TEXT", "VARCHAR", "BIGINT":
				default:
					m[cols[i].Name()] = v
				}
			}
			// fmt.Print(m)
			ams = append(ams, m)
		}
	}

	st := fmt.Sprintf(`SELECT id,名称,st_asgeojson(geom) as geom FROM regions WHERE 名称 ~ $1;`)
	fmt.Println(st)
	search(st, keyword)
	if len(ams) > 10 {
		c.JSON(http.StatusOK, ams)
		return
	}
	bbox := c.Query("bbox")
	var gfilter string
	if bbox != "" {
		gfilter = fmt.Sprintf(` geom && st_makeenvelope(%s,4326) AND `, bbox)
	}
	limit := c.Query("limit")
	var limiter string
	if limit != "" {
		limiter = fmt.Sprintf(` LIMIT %s `, limit)
	}
	st = fmt.Sprintf(`SELECT id,名称,st_asgeojson(geom) as geom,s 搜索 
	FROM (SELECT id,名称,geom,unnest(search) s FROM banks) x WHERE %s s ~ $1 GROUP BY id,名称,geom,s %s;`, gfilter, limiter)
	fmt.Println(st)
	search(st, keyword)
	if len(ams) > 10 {
		c.JSON(http.StatusOK, ams)
		return
	}
	st = fmt.Sprintf(`SELECT id,名称,st_asgeojson(geom) as geom,s 搜索 
	FROM (SELECT id,名称,geom,unnest(search) s FROM others) x WHERE %s s ~ $1 GROUP BY id,名称,geom,s %s;`, gfilter, limiter)
	fmt.Println(st)
	search(st, keyword)
	if len(ams) > 10 {
		c.JSON(http.StatusOK, ams)
		return
	}
	st = fmt.Sprintf(`SELECT 名称,st_asgeojson(geom) as geom,s 搜索 
	FROM (SELECT 名称,geom,unnest(search) s FROM pois) x WHERE %s s ~ $1 GROUP BY 名称,geom,s %s;`, gfilter, limiter)
	fmt.Println(st)
	search(st, keyword)
	if len(ams) > 10 {
		c.JSON(http.StatusOK, ams)
		return
	}
	c.JSON(http.StatusOK, ams)
}

func updateInsertData(c *gin.Context) {
	res := NewRes()
	name := c.Param("name")

	if code := checkDataset(name); code != 200 {
		res.Fail(c, code)
		return
	}

	bank := &Bank{}
	err := c.BindJSON(bank)
	if err != nil {
		log.Error(err)
		res.Fail(c, 4001)
		return
	}

	bank.Search = []string{bank.No, bank.Name, bank.Region, bank.Type, bank.Manager}

	if db.Table(name).Where("id = ?", bank.ID).First(&Bank{}).RecordNotFound() {
		db.Omit("geom").Create(bank)
	} else {
		err := db.Table(name).Where("id = ?", bank.ID).Update(bank).Error
		if err != nil {
			log.Error(err)
			res.Fail(c, 5001)
			return
		}
	}

	if bank.X < -180 || bank.X > 180 || bank.Y < -85 || bank.Y > 85 {
		log.Errorf("x, y must be reasonable values, name")
		res.FailMsg(c, "x, y must be reasonable values")
		return
	}
	stgeo := fmt.Sprintf(`UPDATE %s SET geom = ST_GeomFromText('POINT(' || x || ' ' || y || ')',4326) WHERE id=%d;`, name, bank.ID)
	result := db.Exec(stgeo)
	if result.Error != nil {
		log.Errorf("update %s create geom error:%s", name, result.Error.Error())
		res.Fail(c, 5001)
		return
	}

	res.DoneData(c, gin.H{
		"id": bank.ID,
	})
}

func deleteData(c *gin.Context) {
	res := NewRes()
	name := c.Param("name")

	if code := checkDataset(name); code != 200 {
		res.Fail(c, code)
		return
	}

	var body struct {
		ID string `form:"id" json:"id" binding:"required"`
	}
	err := c.Bind(&body)
	if err != nil {
		res.Fail(c, 4001)
		return
	}
	err = db.Where("id = ?", body.ID).Delete(&Bank{}).Error
	if err != nil {
		log.Errorf("delete data : %s; dataid: %s", err, body.ID)
		res.Fail(c, 5001)
		return
	}
	res.Done(c, "")
}

func coreOrclQuery(c *gin.Context) {
	res := NewRes()
	os.Setenv("NLS_LANG", "")
	var openString string
	openString = `atlas/1234@127.0.0.1:1521/XE`
	// [username/[password]@]host[:port][/instance_name][?param1=value1&...&paramN=valueN]
	// A normal simple Open to localhost would look like:
	// db, err := sql.Open("oci8", "127.0.0.1")
	// For testing, need to use additional variables
	dbOrcl, err := sql.Open("oci8", openString)
	if err != nil {
		fmt.Printf("Open orcl error is not nil: %v", err)
		return
	}
	if dbOrcl == nil {
		fmt.Println("dbOrcl is nil")
		return
	}

	// defer close database
	defer func() {
		err = dbOrcl.Close()
		if err != nil {
			fmt.Println("Close error is not nil:", err)
		}
	}()

	var rows *sql.Rows
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	rows, err = dbOrcl.QueryContext(ctx, `select 机构号 as a,年份 as b,总存款日均 as c from "savings"`)
	if err != nil {
		fmt.Println("QueryContext error is not nil:", err)
		return
	}
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil {
		log.Error(err)
		res.Fail(c, 5001)
		return
	}
	var t [][]string
	for rows.Next() {
		// Scan needs an array of pointers to the values it is setting
		// This creates the object and sets the values correctly
		vals := make([]sql.RawBytes, len(cols))
		valsScer := make([]interface{}, len(vals))
		for i := range vals {
			valsScer[i] = &vals[i]
		}

		err = rows.Scan(valsScer...)
		if err != nil {
			log.Error(err)
		}

		// sv := &NewSaving{Name: string(vals[0]), BankNo: string(vals[1])}
		// db.Create(sv)

		var r []string
		for _, v := range vals {
			// skip nil values.
			if v == nil {
				continue
			}
			r = append(r, string(v))
		}

		if len(r) == 0 {
			continue
		}
		t = append(t, r)
	}
	res.DoneData(c, t)
}

func setOrclAutoInterval(c *gin.Context) {
	res := NewRes()
	syn := cfgV.GetString("core-orcl.sync")
	if syn != "on" {
		log.Info("atlas turn down sync from core orcl ~")
		res.FailMsg(c, "atlas turn down sync from core orcl ~")
		return
	}

	var body struct {
		Duration float64 `bounding:"required"`
	}
	err := c.BindJSON(&body)
	if err != nil {
		log.Error(err)
		res.Fail(c, 4001)
		return
	}
	if body.Duration <= 0 {
		//关闭同步
		if coreOrclIterval == 0 {
			return
		}
		coreOrclIterval = 0
		coreOrclChan <- struct{}{}
	} else {
		//更新同步时间
		coreOrclIterval = time.Duration(body.Duration) * time.Second
		coreOrclChan <- struct{}{}
		go orclSyncer()
	}
	res.Done(c, "")
}

func coreOrclInfo(c *gin.Context) {
	res := NewRes()
	os.Setenv("NLS_LANG", "")
	var openString string
	openString = `atlas/1234@127.0.0.1:1521/XE`
	// [username/[password]@]host[:port][/instance_name][?param1=value1&...&paramN=valueN]
	// A normal simple Open to localhost would look like:
	// db, err := sql.Open("oci8", "127.0.0.1")
	// For testing, need to use additional variables
	dbOrcl, err := sql.Open("oci8", openString)
	if err != nil {
		fmt.Printf("Open orcl error is not nil: %v", err)
		return
	}
	if dbOrcl == nil {
		fmt.Println("dbOrcl is nil")
		return
	}

	// defer close database
	defer func() {
		err = dbOrcl.Close()
		if err != nil {
			fmt.Println("Close error is not nil:", err)
		}
	}()

	var rows *sql.Rows
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	rows, err = dbOrcl.QueryContext(ctx, `select 机构号 as a,年份 as b,总存款日均 as c from "savings"`)
	if err != nil {
		fmt.Println("QueryContext error is not nil:", err)
		return
	}
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil {
		log.Error(err)
		res.Fail(c, 5001)
		return
	}
	var t [][]string
	for rows.Next() {
		// Scan needs an array of pointers to the values it is setting
		// This creates the object and sets the values correctly
		vals := make([]sql.RawBytes, len(cols))
		valsScer := make([]interface{}, len(vals))
		for i := range vals {
			valsScer[i] = &vals[i]
		}

		err = rows.Scan(valsScer...)
		if err != nil {
			log.Error(err)
		}

		// sv := &NewSaving{Name: string(vals[0]), BankNo: string(vals[1])}
		// db.Create(sv)

		var r []string
		for _, v := range vals {
			// skip nil values.
			if v == nil {
				continue
			}
			r = append(r, string(v))
		}

		if len(r) == 0 {
			continue
		}
		t = append(t, r)
	}
	res.DoneData(c, t)
}
