package main

import (
	"encoding/json"
	"time"

	"github.com/lib/pq"
	"github.com/paulmach/orb"
)

//Map 登录记录表
type Map struct {
	ID        string `json:"id" gorm:"primary_key"`
	Title     string `json:"title"`
	Summary   string `json:"summary"`
	User      string `json:"user"`
	Action    string `json:"action"`
	Config    []byte `json:"config" gorm:"type:json"`
	Thumbnail string `json:"thumbnail"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

//MapPerm 地图权限表
type MapPerm struct {
	ID      string //role/user id
	MapID   string
	MapName string
	Action  string
}

//MapBind 登录记录表
type MapBind struct {
	ID        string      `form:"id" json:"id"`
	Title     string      `form:"title" json:"title"`
	Summary   string      `form:"summary" json:"summary"`
	User      string      `form:"user" json:"user"`
	Action    string      `form:"action" json:"action"`
	Config    interface{} `form:"config" json:"config"`
	Thumbnail string      `form:"thumbnail" json:"thumbnail"`
}

func (m *Map) toBind() *MapBind {
	out := &MapBind{
		ID:        m.ID,
		Title:     m.Title,
		Summary:   m.Summary,
		User:      m.User,
		Action:    m.Action,
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
		Action:    b.Action,
		Thumbnail: b.Thumbnail,
	}
	// thumb := Thumbnail(300, 168, b.Thumbnail)
	// if thumb == "" {
	// 	out.Thumbnail = b.Thumbnail
	// } else {
	// 	out.Thumbnail = thumb
	// }
	out.Config, _ = json.Marshal(b.Config)
	return out
}

// Bank 本行机构表
type Bank struct {
	ID        uint       `gorm:"primary_key"`
	Brc       string     `json:"机构号" gorm:"column:机构号;index"`
	No        string     `json:"编码" gorm:"column:编码"`
	Name      string     `json:"名称" gorm:"column:名称;index"`
	Status    string     `json:"营业状态" gorm:"column:营业状态"`
	Region    string     `json:"行政区" gorm:"column:行政区"`
	Type      string     `json:"网点类型" gorm:"column:网点类型"`
	Depart    string     `json:"营业部" gorm:"column:营业部"`
	Manager   string     `json:"管理行" gorm:"column:管理行"`
	House     string     `json:"权属" gorm:"column:权属"`
	Area      float32    `json:"营业面积" gorm:"column:营业面积"`
	Term      *time.Time `json:"到期时间" gorm:"column:到期时间"`
	Date      *time.Time `json:"装修时间" gorm:"column:装修时间"`
	Staff     int        `json:"人数" gorm:"column:人数"`
	Level     string     `json:"行评等级" gorm:"column:行评等级"`
	Save      float32    `json:"存款" gorm:"column:存款"`
	Loan      float32    `json:"贷款" gorm:"column:贷款"`
	Profit    float32    `json:"利润" gorm:"column:利润"`
	UpdatedAt time.Time
	X         float32        `json:"x"`
	Y         float32        `json:"y"`
	Geom      orb.Point      `json:"-" sql:"type:geometry(Geometry,4326)"`
	Search    pq.StringArray `json:"search" gorm:"type:varchar[];index"`
}
