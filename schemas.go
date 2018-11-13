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
	Role       pq.StringArray `json:"role" gorm:"type:varchar(64)[]"`
	Phone      string         `json:"phone"`
	Department string         `json:"department"`

	JWT     string    `json:"jwt" gorm:"column:jwt"`
	Expires time.Time `json:"expires"`

	Activation string         `json:"activation"`
	Search     pq.StringArray `json:"search" gorm:"type:varchar(64)[];index"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

//Role 角色表
type Role struct {
	ID   string `form:"id" json:"id" gorm:"unique;index" binding:"required"`
	Name string `form:"name" json:"name" gorm:"unique" binding:"required"`
}

//Asset 资源表
type Asset struct {
	ID  string `form:"id" json:"id" gorm:"unique;index"`
	URL string `form:"url" json:"url" gorm:"unique;column:url" binding:"required"`
}

//AssetGroup 资源组表
type AssetGroup struct {
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

// Bank 本行机构表
type Bank struct {
	ID        uint           `json:"id" gorm:"primary_key"`
	Num       string         `json:"num"`
	Name      string         `json:"name" gorm:"index"`
	State     string         `json:"state"`
	Region    string         `json:"region"`
	Type      string         `json:"type"`
	Admin     string         `json:"admin"`
	Manager   string         `json:"manager"`
	House     string         `json:"house"`
	Area      float32        `json:"area"`
	Term      string         `json:"term"`
	Time      string         `json:"time"`
	Staff     string         `json:"staff"`
	Class     string         `json:"class"`
	Lat       float32        `json:"lat"`
	Lng       float32        `json:"lng"`
	Geom      orb.Point      `sql:"type:geometry(Geometry,4326)"`
	Search    pq.StringArray `json:"search" gorm:"type:varchar(64)[];index"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Money 存款表
type Money struct {
	ID        uint    `json:"id" gorm:"primary_key"`
	Num       string  `json:"num"`
	Year      string  `json:"year" gorm:"index"`
	Total     float32 `json:"total"`
	Corporate float32 `json:"corporate"`
	Personal  float32 `json:"personal"`
	Margin    float32 `json:"margin"`
	Other     float32 `json:"other"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Other 他行机构表
type Other struct {
	ID        uint           `json:"id" gorm:"primary_key"`
	Num       string         `json:"num"`
	Name      string         `json:"name" gorm:"index"`
	Class     string         `json:"class"`
	Address   string         `json:"address"`
	Lat       float32        `json:"lat"`
	Lng       float32        `json:"lng"`
	Geom      orb.Point      `sql:"type:geometry(Geometry,4326)"`
	Search    pq.StringArray `json:"search" gorm:"type:varchar(64)[];index"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Basepoi 基础需求点
type Basepoi struct {
	ID        uint      `json:"id" gorm:"primary_key"`
	Name      string    `json:"name" gorm:"index"`
	Lat       float32   `json:"lat"`
	Lng       float32   `json:"lng"`
	Geom      orb.Point `sql:"type:geometry(Geometry,4326)"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Residential 住宅POI
type Residential struct {
	ID        uint           `json:"id" gorm:"primary_key"`
	Name      string         `json:"name" gorm:"index"`
	Area      float32        `json:"area"`
	Number    float32        `json:"number"`
	Price     float32        `json:"price"`
	Date      string         `json:"date"`
	Lat       float32        `json:"lat"`
	Lng       float32        `json:"lng"`
	Geom      orb.Point      `sql:"type:geometry(Geometry,4326)"`
	Search    pq.StringArray `json:"search" gorm:"type:varchar(64)[];index"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Business 商业POI
type Business struct {
	ID        uint           `json:"id" gorm:"primary_key"`
	Name      string         `json:"name" gorm:"index"`
	Type      string         `json:"type"`
	Hot       string         `json:"hot"`
	Consume   string         `json:"consume"`
	Lat       float32        `json:"lat"`
	Lng       float32        `json:"lng"`
	Geom      orb.Point      `sql:"type:geometry(Geometry,4326)"`
	Search    pq.StringArray `json:"search" gorm:"type:varchar(64)[];index"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Organization 公司单位组织POI
type Organization struct {
	ID        uint           `json:"id" gorm:"primary_key"`
	Name      string         `json:"name" gorm:"index"`
	Type      string         `json:"type"`
	Capital   float32        `json:"capital"`
	Class     string         `json:"class"`
	Lat       float32        `json:"lat"`
	Lng       float32        `json:"lng"`
	Geom      orb.Point      `sql:"type:geometry(Geometry,4326)"`
	Search    pq.StringArray `json:"search" gorm:"type:varchar(64)[];index"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func validate(name string, password string) (bool, error) {
	name = strings.ToLower(name)
	if len(name) == 0 && len(name) < 64 {
		return false, errors.New("name: required and 64 letters limit")
	}
	if ok := rUsername.MatchString(name); !ok {
		return false, errors.New(`name: only use letters, numbers, \'-\', \'_\'`)
	}

	if len(password) == 0 {
		return false, errors.New("password: required")
	}
	if len(password) < 4 {
		return false, errors.New(`password: too weak password, at least 4 necessary`)
	}

	return true, nil
}
