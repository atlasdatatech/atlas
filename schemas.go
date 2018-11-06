package main

import (
	"errors"
	"strings"
	"time"

	"github.com/lib/pq"
)

// User 用户表
type User struct {
	ID         string `json:"id" gorm:"primary_key"`
	Name       string `json:"name" gorm:"unique;not null;unique_index"`
	Password   string `json:"-"`
	Role       string `json:"role"`
	Phone      string `json:"phone" gorm:"index"`
	Department string `json:"department"`

	JWT     string    `json:"jwt" gorm:"column:jwt"`
	Expires time.Time `json:"expires"`

	Activation string         `json:"activation"`
	Search     pq.StringArray `json:"search" gorm:"type:varchar(64)[];index"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

//Attempt 登录记录表
type Attempt struct {
	ID        uint   `gorm:"primary_key"`
	IP        string `gorm:"index"`
	Name      string `gorm:"index"`
	CreatedAt time.Time
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
