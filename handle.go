package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

func index(c *gin.Context) {
	_, err := authMid.GetClaimsFromJWT(c)
	if err != nil {
		c.HTML(http.StatusOK, "index.html", gin.H{
			"Title": "AtlasMap",
			"Login": true,
		})
	}
	c.Redirect(http.StatusFound, "/studio/")
}

func ping(c *gin.Context) {
	res := NewRes()
	err := db.DB().Ping()
	if err != nil {
		res.FailErr(c, err)
		return
	}
	res.DoneData(c, gin.H{
		"status": "db pong ~",
		"time":   time.Now().Format("2006-01-02 15:04:05"),
	})
}

func crsList(c *gin.Context) {
	res := NewRes()
	res.DoneData(c, CRSs)
}

func encodingList(c *gin.Context) {
	res := NewRes()
	res.DoneData(c, Encodings)
}

func fieldTypeList(c *gin.Context) {
	res := NewRes()
	res.DoneData(c, FieldTypes)
}

func dbSearch(st string, keyword string) (ams []map[string]interface{}, err error) {
	stmt, err := db.DB().Prepare(st)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	rows, err := stmt.Query(keyword)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, _ := rows.ColumnTypes()
	for rows.Next() {
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
		ams = append(ams, m)
	}
	return ams, nil
}

func getTreeNodes(c *gin.Context) {
	res := NewRes()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	tables := viper.GetStringSlice("tree.tables")
	tableids := viper.GetStringSlice("tree.tableids")
	level0s := viper.GetStringSlice("tree.level0s")

	tablename := ""
	where := ""
	level0 := ""
	trimlen := 0
	queryType := c.Query("type")
	switch queryType {
	case tables[0]:
		level0 = level0s[0]
		trimlen = 2
		tablename = strings.ToLower(tableids[0])
		// city, ok := c.GetQuery("name")
		// if ok {
		// 	where = fmt.Sprintf(` WHERE "city" = '%s'`, city)
		// }
	case tables[1]:
		tablename = strings.ToLower(tableids[1])
		level0 = level0s[1]
		trimlen = 3
		dist, ok := c.GetQuery("name")
		if ok {
			where = fmt.Sprintf(` WHERE "%s" = '%s'`, tables[0], dist)
		}
	default:
		res.FailMsg(c, "无法识别的查询类型")
		return
	}

	st := fmt.Sprintf(`SELECT name, code FROM "%s" %s ORDER BY code;`, tablename, where)
	fmt.Println(st)
	rows, err := db.Raw(st).Rows()
	if err != nil {
		log.Error(err)
		res.Fail(c, 5001)
		return
	}
	defer rows.Close()
	type TreeNode struct {
		Name string `json:"name"`
		Code string `json:"code"`
	}
	var nodes []TreeNode
	for rows.Next() {
		var tn TreeNode
		// ScanRows scan a row into user
		db.ScanRows(rows, &tn)
		nodes = append(nodes, tn)
		// do something
	}

	switch c.Query("level") {
	case "0":
		//若name为空，则获取所有指定层
		code := ""
		if len(nodes) > 0 {
			code = nodes[0].Code
			if len(code) > trimlen {
				code = code[0 : len(code)-trimlen]
			}
		}
		nodes = append([]TreeNode{{Name: level0, Code: code}}, nodes...)
	case "1":
	default:
	}

	res.DoneData(c, nodes)
}

func queryTreeNode(c *gin.Context) {
	res := NewRes()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	name := c.Param("name")
	if name == "" {
		res.FailMsg(c, "名称不能为空")
		return
	}
	// tables := viper.GetStringSlice("tree.tables")
	tableids := viper.GetStringSlice("tree.tableids")
	for _, tbid := range tableids {
		st := fmt.Sprintf(`SELECT name,code,comment,st_asgeojson(geom) as geom FROM "%s"  WHERE name = $1 ;`, strings.ToLower(tbid))
		ams, err := dbSearch(st, name)
		if err != nil {
			log.Error(err)
			res.FailErr(c, err)
			return
		}
		if len(ams) > 0 {
			res.DoneData(c, ams)
			return
		}
	}
	res.DoneCode(c, 200)
}

func getListNodes(c *gin.Context) {
	res := NewRes()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	where := ""
	dist, ok := c.GetQuery("districts")
	if ok {
		where += fmt.Sprintf(" WHERE districts = '%s' ", dist)
	}

	street, ok := c.GetQuery("streets")
	if ok {
		if where != "" {
			where += "AND "
		} else {
			where = " WHERE "
		}
		where += fmt.Sprintf(" streets = '%s' ", street)
	}

	tables := viper.GetStringSlice("list.tables")
	tableids := viper.GetStringSlice("list.tableids")
	tablename := ""
	queryType := c.Query("type")
	switch queryType {
	case tables[0]:
		tablename = strings.ToLower(tableids[0])
	case tables[1]:
		tablename = strings.ToLower(tableids[1])
	case tables[2]:
		tablename = strings.ToLower(tableids[2])
	case tables[3]:
		tablename = strings.ToLower(tableids[3])
	default:
		res.FailMsg(c, "无法识别的查询类型")
		return
	}
	st := fmt.Sprintf(`SELECT gid, name FROM "%s" %s ORDER BY name;`, tablename, where)
	fmt.Println(st)
	rows, err := db.Raw(st).Rows()
	if err != nil {
		log.Error(err)
		res.Fail(c, 5001)
		return
	}
	defer rows.Close()
	type ListNode struct {
		Gid  int    `json:"gid"`
		Name string `json:"name"`
	}
	var nodes []ListNode
	for rows.Next() {
		var tn ListNode
		// ScanRows scan a row into user
		db.ScanRows(rows, &tn)
		nodes = append(nodes, tn)
		// do something
	}
	res.DoneData(c, nodes)
}

func searchAdvanced(c *gin.Context) {
	res := NewRes()
	tables := viper.GetStringSlice("search.tables")
	tableids := viper.GetStringSlice("search.tableids")
	fields := viper.GetStringSlice("search.fields")
	matchfields := viper.GetStringSlice("search.matchfields")
	typenames := viper.GetStringSlice("search.typenames")
	types, ok := c.GetQuery("types")
	if ok {
		tbs := strings.Split(types, ",")
		var ntables, ntableids, nfields, nmatchfields, ntypenames []string
		for i, tb := range tables {
			yes := false
			for _, t := range tbs {
				if tb == t {
					yes = true
					break
				}
			}
			if yes {
				ntables = append(ntables, tables[i])
				ntableids = append(ntableids, tableids[i])
				nfields = append(nfields, fields[i])
				nmatchfields = append(nmatchfields, matchfields[i])
				ntypenames = append(ntypenames, typenames[i])
			}
		}
		tables = ntables
		tableids = ntableids
		fields = nfields
		matchfields = nmatchfields
		typenames = ntypenames
		if len(tables) == 0 {
			res.FailMsg(c, "无法识别的查询类型")
			return
		}
	}

	matchsymbol := " ~ "
	if c.Query("matchtype") == "1" {
		matchsymbol = " = "
	}

	geom, ok := c.GetQuery("geom")
	var gfilter string
	if ok {
		gfilter = fmt.Sprintf(` geom && st_makeenvelope(%s,4326) AND `, geom)
	}

	limiter := fmt.Sprintf(` LIMIT 10 `)
	var lmt int64 = 10
	limit, ok := c.GetQuery("limit")
	if ok {
		var err error
		lmt, err = strconv.ParseInt(limit, 10, 32)
		if err == nil {
			limiter = fmt.Sprintf(` LIMIT %s `, limit)
		}
	}

	withgeom := " ,st_asgeojson(geom) as geom "
	withoutgeom, ok := c.GetQuery("withoutgeom")
	if ok {
		if withoutgeom == "1" {
			withgeom = ""
		}
	}

	var ams []map[string]interface{}
	for i, table := range tables {
		tbname := strings.ToLower(tableids[i])
		field := fields[i]
		matchfield := matchfields[i]
		typename := typenames[i]
		st := fmt.Sprintf(`SELECT gid, %s, '%s' as type , '%s' as typename %s FROM "%s" WHERE %s "%s" %s $1 %s ;`, field, table, typename, withgeom, tbname, gfilter, matchfield, matchsymbol, limiter)
		fmt.Println(st)
		rs, err := dbSearch(st, c.Query("keyword"))
		if err != nil {
			log.Error(err)
		}
		ams = append(ams, rs...)
		if len(ams) >= int(lmt) {
			break
		}
	}
	res.DoneData(c, ams)
}

func queryAdvanced(c *gin.Context) {
	res := NewRes()
	gid := c.Param("gid")
	if gid == "" {
		res.FailMsg(c, "gid不能为空")
		return
	}
	tables := viper.GetStringSlice("search.tables")
	tableids := viper.GetStringSlice("search.tableids")
	selectfields := viper.GetStringSlice("search.selectfields")

	tableid := ""
	fields := ""
	queryType := c.Query("type")
	switch queryType {
	case tables[0]:
		tableid = tableids[0]
		fields = selectfields[0]
	case tables[1]:
		tableid = tableids[1]
		fields = selectfields[1]
	case tables[2]:
		tableid = tableids[2]
		fields = selectfields[2]
	case tables[3]:
		tableid = tableids[3]
		fields = selectfields[3]
	default:
		res.FailMsg(c, "无法识别的查询类型")
		return
	}

	if fields == "" {
		dt := Dataset{ID: tableid}
		err := db.First(&dt).Error
		if err != nil {
			log.Error(err)
			res.FailErr(c, err)
			return
		}
		var flds []Field
		err = json.Unmarshal(dt.Fields, &flds)
		if err != nil {
			log.Error(err)
			res.FailErr(c, err)
			return
		}
		var fldstr []string
		for _, f := range flds {
			fldstr = append(fldstr, f.Name)
		}
		fields = strings.Join(fldstr, `","`)
	} else {
		flds := strings.Split(fields, ",")
		fields = strings.Join(flds, `","`)
	}

	st := fmt.Sprintf(`SELECT gid,"%s",st_asgeojson(geom) as geom FROM "%s" WHERE gid = $1 ;`, fields, strings.ToLower(tableid))
	ams, err := dbSearch(st, gid)
	if err != nil {
		res.FailErr(c, err)
		return
	}
	res.DoneData(c, ams)
	return
}

func getRedFile(c *gin.Context) {
	res := NewRes()
	uid := c.GetString(userKey)
	if uid == "" {
		uid = c.GetString(identityKey)
	}
	name := c.Param("name")
	if name == "" {
		res.FailMsg(c, "文件名称不能为空")
		return
	}
	name += ".pdf"
	path := viper.GetString("attachment.path")
	file, err := os.Open(filepath.Join(path, name))
	if err != nil {
		log.Errorf(`open pdf file (%s) error, details: %s ^^`, name, err)
	}
	c.Header("Content-type", "application/pdf")
	io.Copy(c.Writer, file)
	return
}
