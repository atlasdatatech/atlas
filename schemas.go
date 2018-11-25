package main

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/paulmach/orb"
)

// User 用户表
type User struct {
	ID         string         `json:"id" gorm:"primary_key"`
	Name       string         `json:"name" gorm:"unique;not null;unique_index"`
	Password   string         `json:"-"`
	Role       pq.StringArray `json:"role" gorm:"type:varchar[]"`
	Phone      string         `json:"phone"`
	Department string         `json:"department"`

	JWT     string    `json:"jwt" gorm:"column:jwt"`
	Expires time.Time `json:"expires"`

	Activation string         `json:"activation"`
	Search     pq.StringArray `json:"search" gorm:"type:varchar[];index"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

//Role 角色表
type Role struct {
	ID   string `form:"id" json:"id" gorm:"unique;index" binding:"required"`
	Name string `form:"name" json:"name" gorm:"unique" binding:"required"`
}

//Attempt 登录记录表
type Attempt struct {
	ID        string `gorm:"primary_key"`
	IP        string `gorm:"index"`
	Name      string `gorm:"index"`
	CreatedAt time.Time
}

//Map 登录记录表
type Map struct {
	ID        string `json:"id" gorm:"primary_key"`
	Title     string `json:"title"`
	Summary   string `json:"summary"`
	User      string `json:"user"`
	Thumbnail string `json:"thumbnail"`
	Config    []byte `json:"config" gorm:"type:json"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

//MapBind 登录记录表
type MapBind struct {
	ID        string      `form:"id" json:"id"`
	Title     string      `form:"title" json:"title"`
	Summary   string      `form:"summary" json:"summary"`
	User      string      `form:"user" json:"user"`
	Thumbnail string      `form:"thumbnail" json:"thumbnail"`
	Config    interface{} `form:"config" json:"config"`
}

func (m *Map) toBind() *MapBind {
	out := &MapBind{
		ID:        m.ID,
		Title:     m.Title,
		Summary:   m.Summary,
		User:      m.User,
		Thumbnail: m.Thumbnail,
	}
	json.Unmarshal(m.Config, &out.Config)
	return out
}

func (b *MapBind) toMap() *Map {
	out := &Map{
		ID:        b.ID,
		Title:     b.Title,
		Summary:   b.Summary,
		User:      b.User,
		Thumbnail: b.Thumbnail,
	}
	out.Config, _ = json.Marshal(b.Config)
	return out
}

// Field represents an mbtiles file connection.
type Field struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Format string `json:"format"`
}

// Dataset represents an mbtiles file connection.
type Dataset struct {
	ID     string `json:"id"`                      //字段列表
	Name   string `json:"name"`                    //字段列表// 数据集名称,现用于更方便的ID
	Label  string `json:"label"`                   //字段列表// 显示标签
	Type   string `json:"type"`                    //字段列表
	Fields []byte `json:"fields" gorm:"type:json"` //字段列表
}

// DatasetBind represents an mbtiles file connection.
type DatasetBind struct {
	ID     string      `form:"id" json:"id"`         //字段列表
	Name   string      `form:"name" json:"name"`     //字段列表// 数据集名称,现用于更方便的ID
	Label  string      `form:"label" json:"label"`   //字段列表// 显示标签
	Type   string      `form:"type" json:"type"`     //字段列表
	Fields interface{} `form:"fields" json:"fields"` //字段列表
}

func (d *Dataset) toBind() *DatasetBind {
	out := &DatasetBind{
		ID:    d.ID,
		Name:  d.Name,
		Label: d.Label,
		Type:  d.Type,
	}
	json.Unmarshal(d.Fields, &out.Fields)
	return out
}

func (b *DatasetBind) toDataset() *Dataset {
	out := &Dataset{
		ID:    b.ID,
		Name:  b.Name,
		Label: b.Label,
		Type:  b.Type,
	}
	out.Fields, _ = json.Marshal(b.Fields)
	return out
}

// Bank 本行机构表
type Bank struct {
	ID      string         `json:"id" gorm:"unique;index"`
	Name    string         `json:"name" gorm:"index"`
	State   string         `json:"state"`
	Region  string         `json:"region"`
	Type    string         `json:"type"`
	Admin   string         `json:"admin"`
	Manager string         `json:"manager"`
	House   string         `json:"house"`
	Area    float32        `json:"area"`
	Term    time.Time      `json:"term"`
	Date    time.Time      `json:"date"`
	Staff   int            `json:"staff"`
	Class   string         `json:"class"`
	Lat     float32        `json:"lat"`
	Lng     float32        `json:"lng"`
	Geom    orb.Point      `sql:"type:geometry(Geometry,4326)"`
	Search  pq.StringArray `json:"search" gorm:"type:varchar[];index"`
}

// Saving 存款表
type Saving struct {
	BankID    string  `json:"bank_id" gorm:"index"`
	Year      string  `json:"year"`
	Total     float32 `json:"total"`
	Corporate float32 `json:"corporate"`
	Personal  float32 `json:"personal"`
	Margin    float32 `json:"margin"`
	Other     float32 `json:"other"`
}

// Other 他行机构表
type Other struct {
	ID      string         `json:"id" gorm:"index"`
	Name    string         `json:"name" gorm:"index"`
	Class   string         `json:"class"`
	Address string         `json:"address"`
	Lat     float32        `json:"lat"`
	Lng     float32        `json:"lng"`
	Geom    orb.Point      `sql:"type:geometry(Geometry,4326)"`
	Search  pq.StringArray `json:"search" gorm:"type:varchar[];index"`
}

// Basepoi 基础需求点
type Basepoi struct {
	ID    uint      `json:"id" gorm:"primary_key"`
	Name  string    `json:"name" gorm:"index"`
	Class string    `json:"class"`
	Lat   float32   `json:"lat"`
	Lng   float32   `json:"lng"`
	Geom  orb.Point `sql:"type:geometry(Geometry,4326)"`
}

// Poi 需求点POI
type Poi struct {
	ID         uint           `json:"id" gorm:"primary_key"`
	Name       string         `json:"name" gorm:"index"`
	Class      int            `json:"class"`
	Type       string         `json:"type"`
	Hit        string         `json:"hit"`
	Per        float32        `json:"per"`
	Area       float32        `json:"area"`
	Households int            `json:"households"`
	Date       string         `json:"date"`
	Lat        float32        `json:"lat"`
	Lng        float32        `json:"lng"`
	Geom       orb.Point      `sql:"type:geometry(Geometry,4326)"`
	Search     pq.StringArray `json:"search" gorm:"type:varchar[];index"`
}

// M1 立地条件
type M1 struct {
	BankID string  `json:"bank_id" gorm:"index"`
	C1     float32 `json:"c1"`
	C2     float32 `json:"c2"`
	C3     float32 `json:"c3"`
	C4     float32 `json:"c4"`
	C5     float32 `json:"c5"`
	C6     float32 `json:"c6"`
	C7     float32 `json:"c7"`
	C8     float32 `json:"c8"`
	C9     float32 `json:"c9"`
	C10    float32 `json:"c10"`
	C11    float32 `json:"c11"`
	C12    float32 `json:"c12"`
	C13    float32 `json:"c13"`
	C14    float32 `json:"c14"`
	C15    float32 `json:"c15"`
	C16    float32 `json:"c16"`
	Result float32 `json:"result"`
}

//M2 竞争力
type M2 struct {
	BankID string  `json:"bank_id" gorm:"index"`
	B1     float32 `json:"b1"`
	B2     float32 `json:"b2"`
	B3     float32 `json:"b3"`
	B4     float32 `json:"b4"`
	B5     float32 `json:"b5"`
	B6     float32 `json:"b6"`
	Result float32 `json:"result"`
}

//M3 竞争力
type M3 struct {
	BankID string  `json:"bank_id" gorm:"index"`
	Result float32 `json:"result"`
}

//M4 宏观战略
type M4 struct {
	Region     string  `json:"region"`
	GDP        float32 `json:"name" gorm:"column:gdp"`
	Population float32 `json:"population"`
	Area       float32 `json:"area"`
	Price      float32 `json:"price"`
	Cusume     float32 `json:"cusume"`
	Industrial float32 `json:"industrial"`
	Saving     float32 `json:"saving"`
	Loan       float32 `json:"loan"`
}

func validate(name string, password string) error {
	name = strings.ToLower(name)
	if len(name) == 0 && len(name) < 64 {
		return errors.New("name: required and 64 letters limit")
	}
	if ok := rUsername.MatchString(name); !ok {
		return errors.New(`name: only use letters, numbers, \'-\', \'_\'`)
	}

	if len(password) == 0 {
		return errors.New("password: required")
	}
	if len(password) < 4 {
		return errors.New(`password: too weak password, at least 4 necessary`)
	}

	return nil
}
