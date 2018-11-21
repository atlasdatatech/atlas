package main

import (
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
	ID        string `form:"id" json:"id" gorm:"primary_key"`
	Title     string `form:"title" json:"title"`
	Summary   string `form:"summary" json:"summary"`
	User      string `form:"user" json:"user"`
	Thumbnail []byte `form:"thumbnail" json:"thumbnail"`
	Config    []byte `form:"config" json:"config"`
	CreatedAt time.Time
	UpdatedAt time.Time
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
	Term    string         `json:"term"`
	Date    string         `json:"date"`
	Staff   string         `json:"staff"`
	Class   string         `json:"class"`
	Lat     float32        `json:"lat"`
	Lng     float32        `json:"lng"`
	Geom    orb.Point      `sql:"type:geometry(Geometry,4326)"`
	Search  pq.StringArray `json:"search" gorm:"type:varchar[];index"`
}

// Saving 存款表
type Saving struct {
	No        uint    `json:"no" gorm:"primary_key"`
	ID        string  `json:"id" gorm:"index"`
	Year      string  `json:"year"`
	Total     float32 `json:"total"`
	Corporate float32 `json:"corporate"`
	Personal  float32 `json:"personal"`
	Margin    float32 `json:"margin"`
	Other     float32 `json:"other"`
}

// Other 他行机构表
type Other struct {
	No      uint           `json:"no" gorm:"primary_key"`
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
	No     uint    `json:"no" gorm:"primary_key"`
	ID     string  `json:"id" gorm:"index"`
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
	No     uint    `json:"no" gorm:"primary_key"`
	ID     string  `json:"id" gorm:"index"`
	Count  float32 `json:"count"`
	Number float32 `json:"number"`
	Result float32 `json:"result"`
}

//M3 竞争度
type M3 struct {
	Name   string  `json:"name"`
	Weight float32 `json:"weight"`
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
