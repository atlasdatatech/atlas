package main

import (
	"regexp"
	"time"

	"github.com/jinzhu/gorm"
	log "github.com/sirupsen/logrus"

	"github.com/lib/pq"
)

var rUsername, _ = regexp.Compile(`^[a-zA-Z0-9\-\_]+$`)
var rEmail, _ = regexp.Compile(`^[a-zA-Z0-9\-\_\.\+]+@[a-zA-Z0-9\-\_\.]+\.[a-zA-Z0-9\-\_]+$`)
var lenUsername = 32
var lenPassword = 4

// User 用户表
type User struct {
	ID       string `json:"id" gorm:"primary_key"`
	Name     string `json:"name" gorm:"unique;not null;unique_index"`
	Password string `json:"-"`
	Email    string `json:"email" gorm:"unique;not null;unique_index"`

	Phone      string `json:"phone"`
	Department string `json:"department"`
	Company    string `json:"company"`

	Role  pq.StringArray `json:"-" gorm:"type:varchar[]"`
	Group string         `json:"group"`
	Class string         `json:"class"`

	ResetToken        string    `json:"-"`
	ResetExpires      time.Time `json:"-"`
	Activation        string    `json:"activation"`
	Verification      string    `json:"verification"`
	VerificationToken string    `json:"-"`

	AccessToken string `json:"access_token"`
	JWT         string `json:"jwt" gorm:"jwt"`

	CreatedAt time.Time `json:"-"`
	UpdatedAt time.Time `json:"-"`
}

//Role 角色表
type Role struct {
	ID   string `json:"id" form:"id" gorm:"unique;index" binding:"required"`
	Name string `json:"name" form:"name" binding:"required"`
}

//Attempt 登录记录表
type Attempt struct {
	ID        int    `gorm:"primary_key"`
	IP        string `gorm:"index"`
	Name      string `gorm:"index"`
	CreatedAt time.Time
}

func validName(name string) int {
	if len(name) > lenUsername {
		log.Warnf("validName, name length greater than 32, name:'%s'", name)
		return 4012
	}
	if ok := rUsername.MatchString(name); !ok {
		log.Warnf("validName, use unexpected letters, name:'%s'", name)
		return 4012
	}

	if err := db.Where("name = ?", name).First(&User{}).Error; err != nil {
		if gorm.IsRecordNotFoundError(err) {
		} else {
			log.Errorf("validName, encounter unexpected error, db:'%s'", err)
			return 5001
		}
	} else {
		return 4015
	}

	return 200
}

func validEmail(email string) int {
	if ok := rEmail.MatchString(email); !ok {
		log.Warnf("validEmail, invalidate email format, email:'%s'", email)
		return 4013
	}

	if err := db.Where("email = ?", email).First(&User{}).Error; err != nil {
		if gorm.IsRecordNotFoundError(err) {
		} else {
			log.Errorf("validEmail, encounter unexpected error, db:'%s'", err)
			return 5001
		}
	} else {
		return 4016
	}
	return 200
}

func validPassword(password string) int {
	if len(password) < lenPassword {
		log.Warnf("validate signup password length less than 4, password:'%s'", password)
		return 4014
	}
	return 200
}

func createUser(user *User) (bool, error) {
	return true, nil
}
