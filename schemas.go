package main

import (
	"errors"
	"strings"
	"time"

	"github.com/lib/pq"
)

// User 用户表
type User struct {
	ID        string `json:"id" gorm:"primary_key"`
	Name      string `json:"name" gorm:"unique;not null;unique_index"`
	Password  string `json:"-"`
	Email     string `json:"email" gorm:"unique;not null;unique_index"`
	AccountID string `json:"account" gorm:"index"`

	JWT        string    `json:"-" gorm:"column:jwt"`
	JWTExpires time.Time `json:"jwtExpires" gorm:"column:jwt_expires"`

	Activation           string         `json:"activation"`
	ResetPasswordToken   string         `json:"-"`
	ResetPasswordExpires time.Time      `json:"resetPasswordExpires"`
	Search               pq.StringArray `json:"search" gorm:"type:varchar(64)[];index"`
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// Account 账户信息表
type Account struct {
	ID                string         `json:"id" gorm:"primary_key"`
	UserID            string         `json:"user" gorm:"index"`
	Verification      string         `json:"verification"`
	VerificationToken string         `json:"-"`
	Company           string         `json:"company"`
	Phone             string         `json:"phone"`
	Search            pq.StringArray `json:"search" gorm:"type:varchar(64)[];index"`
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

//Attempt 登录记录表
type Attempt struct {
	ID        uint   `gorm:"primary_key"`
	IP        string `gorm:"index"`
	Name      string `gorm:"index"`
	CreatedAt time.Time
	DeletedAt *time.Time `sql:"index"`
}

func validate(name string, email string, password string) (bool, error) {
	name = strings.ToLower(name)
	if len(name) == 0 && len(name) < 64 {
		return false, errors.New("name: required and 64 letters limit")
	}
	if ok := rUsername.MatchString(name); !ok {
		return false, errors.New(`name: only use letters, numbers, \'-\', \'_\'`)
	}

	email = strings.ToLower(email)
	if len(email) == 0 {
		return false, errors.New("email: required")
	}
	if ok := rEmail.MatchString(email); !ok {
		return false, errors.New(`email: invalid email format`)
	}

	if len(password) == 0 {
		return false, errors.New("password: required")
	}
	if len(password) < 4 {
		return false, errors.New(`password: too weak password, at least 4 necessary`)
	}

	return true, nil
}
