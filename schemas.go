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
	Action    string `json:"action"`
	Thumbnail string `json:"thumbnail"`
	Config    []byte `json:"config" gorm:"type:json"`
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
	Thumbnail string      `form:"thumbnail" json:"thumbnail"`
	Config    interface{} `form:"config" json:"config"`
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
		ID:      b.ID,
		Title:   b.Title,
		Summary: b.Summary,
		User:    b.User,
		Action:  b.Action,
	}
	thumb := Thumbnail(300, 168, b.Thumbnail)
	if thumb == "" {
		out.Thumbnail = b.Thumbnail
	} else {
		out.Thumbnail = thumb
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
	ID      uint           `gorm:"primary_key"`
	No      string         `json:"机构号" gorm:"column:机构号;index"`
	Name    string         `json:"名称" gorm:"column:名称;index"`
	State   string         `json:"营业状态" gorm:"column:营业状态"`
	Region  string         `json:"行政区" gorm:"column:行政区"`
	Type    string         `json:"网点类型" gorm:"column:网点类型"`
	Depart  string         `json:"营业部" gorm:"column:营业部"`
	Manager string         `json:"管理行" gorm:"column:管理行"`
	House   string         `json:"权属" gorm:"column:权属"`
	Area    float32        `json:"营业面积" gorm:"column:营业面积"`
	Term    *time.Time     `json:"到期时间" gorm:"column:到期时间"`
	Date    *time.Time     `json:"装修时间" gorm:"column:装修时间"`
	Staff   int            `json:"人数" gorm:"column:人数"`
	Level   string         `json:"行评等级" gorm:"column:行评等级"`
	X       float32        `json:"x"`
	Y       float32        `json:"y"`
	Geom    orb.Point      `json:"-" sql:"type:geometry(Geometry,4326)"`
	Search  pq.StringArray `json:"search" gorm:"type:varchar[];index"`
}

// Saving 存款表,,,,,,,,
type Saving struct {
	ID       uint    `gorm:"primary_key"`
	BankNo   string  `gorm:"column:机构号;index"`
	Name     string  `gorm:"column:名称"`
	Year     int     `gorm:"column:年份"`
	Total    float32 `gorm:"column:总存款日均"`
	Public   float32 `gorm:"column:单位存款日均"`
	Personal float32 `gorm:"column:个人存款日均"`
	Margin   float32 `gorm:"column:保证金存款日均"`
	Other    float32 `gorm:"column:其他存款日均"`
}

// Other 他行机构表
type Other struct {
	ID      uint           `gorm:"primary_key"`
	No      string         `gorm:"column:机构号;index"`
	Name    string         `gorm:"column:名称;index"`
	Class   string         `gorm:"column:银行类别"`
	Type    string         `gorm:"column:网点类型"`
	Address string         `gorm:"column:地址"`
	SID     string         `gorm:"column:sid"`
	X       float32        ``
	Y       float32        ``
	Geom    orb.Point      `json:"-" sql:"type:geometry(Geometry,4326)"`
	Search  pq.StringArray `json:"search" gorm:"type:varchar[];index"`
}

// Poi 需求点POI
type Poi struct {
	ID      uint           `json:"id" gorm:"primary_key"`
	Name    string         `gorm:"column:名称;index"`
	Type    string         `gorm:"column:类型"`
	Class   string         `gorm:"column:性质"`
	Area    float32        `gorm:"column:建筑面积"`
	Hit     string         `gorm:"column:热度"`
	Per     float32        `gorm:"column:人均消费"`
	Price   float32        `gorm:"column:均价"`
	Houses  int            `gorm:"column:户数"`
	Date    string         `gorm:"column:交付时间"`
	Staff   int            `gorm:"column:职工人数"`
	Remarks string         `gorm:"column:备注"`
	SID     string         `gorm:"column:sid"`
	X       float32        ``
	Y       float32        ``
	Geom    orb.Point      `sql:"type:geometry(Geometry,4326)"`
	Search  pq.StringArray `json:"search" gorm:"type:varchar[];index"`
}

// Plan 规划成果,机构号,名称,类型,年份,规划建议,实施时间,X,Y,sid
type Plan struct {
	ID        uint      `json:"id" gorm:"primary_key"`
	No        string    `gorm:"column:机构号;index"`
	Name      string    `gorm:"column:名称"`
	Type      string    `gorm:"column:类型"`
	Year      string    `gorm:"column:年份"`
	Advice    string    `gorm:"column:规划建议"`
	Implement string    `gorm:"column:实施时间"`
	SID       string    `gorm:"column:sid"`
	X         float32   ``
	Y         float32   ``
	Geom      orb.Point `sql:"type:geometry(Geometry,4326)"`
}

// M1 立地条件
type M1 struct {
	ID     uint    `json:"id" gorm:"primary_key"`
	BankNo string  `gorm:"column:机构号;index"`
	Name   string  `gorm:"column:名称;index"`
	C1     float32 `gorm:"column:商业规模"`
	C2     float32 `gorm:"column:商业人流"`
	C3     float32 `gorm:"column:道路特征"`
	C4     float32 `gorm:"column:快速路"`
	C5     float32 `gorm:"column:位置特征"`
	C6     float32 `gorm:"column:转角位置"`
	C7     float32 `gorm:"column:街巷"`
	C8     float32 `gorm:"column:斜坡"`
	C9     float32 `gorm:"column:公共交通类型"`
	C10    float32 `gorm:"column:距离"`
	C11    float32 `gorm:"column:停车位"`
	C12    float32 `gorm:"column:收费"`
	C13    float32 `gorm:"column:建筑形象"`
	C14    float32 `gorm:"column:营业厅面积"`
	C15    float32 `gorm:"column:装修水准"`
	C16    float32 `gorm:"column:网点类型"`
	Result float32 `gorm:"column:总得分"`
}

//M2 竞争力
type M2 struct {
	ID     uint    `json:"id" gorm:"primary_key"`
	BankNo string  `gorm:"column:机构号;index"`
	Name   string  `gorm:"column:名称;index"`
	C1     float32 `gorm:"column:营业面积"`
	C2     float32 `gorm:"column:人数"`
	C3     float32 `gorm:"column:个人增长"`
	C4     float32 `gorm:"column:个人存量"`
	C5     float32 `gorm:"column:公司存量"`
	Result float32 `gorm:"column:总得分"`
}

//M3 资源分析
type M3 struct {
	ID     uint    `json:"id" gorm:"primary_key"`
	BankNo string  `gorm:"column:机构号;index"`
	Name   string  `gorm:"column:名称;index"`
	C1     float32 `gorm:"column:商业资源"`
	C2     float32 `gorm:"column:对公资源"`
	C3     float32 `gorm:"column:零售资源"`
	Result float32 `gorm:"column:总得分"`
}

//M4 竞争力分析
type M4 struct {
	ID     uint    `json:"id" gorm:"primary_key"`
	BankNo string  `gorm:"column:机构号;index"`
	Name   string  `gorm:"column:名称;index"`
	Result float32 `gorm:"column:总得分"`
}

//M5 宏观战略
type M5 struct {
	ID         uint    `json:"id" gorm:"primary_key"`
	Name       string  `gorm:"column:名称;index"`
	GDP        float32 `gorm:"column:生产总值"`
	Population float32 `gorm:"column:人口"`
	Area       float32 `gorm:"column:房地产成交面积"`
	Price      float32 `gorm:"column:房地产成交均价"`
	Cusume     float32 `gorm:"column:社会消费品零售总额"`
	Industrial float32 `gorm:"column:规模以上工业增加值"`
	Saving     float32 `gorm:"column:金融机构存款"`
	Loan       float32 `gorm:"column:金融机构贷款"`
	Result     float32 `gorm:"column:总得分"`
}

//BufferScale 竞争力字段权重
type BufferScale struct {
	ID    uint `gorm:"primary_key"`
	Type  string
	Scale float32
}

//M2Weight 竞争力字段权重
type M2Weight struct {
	ID     uint `gorm:"primary_key"`
	Field  string
	Weight float32
}

//M4Weight 竞争力字段权重
type M4Weight struct {
	ID     uint `gorm:"primary_key"`
	Type   string
	Weight float32
}

//M4Scale 竞争力字段权重
type M4Scale struct {
	ID    uint `gorm:"primary_key"`
	Type  string
	Scale float32
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
