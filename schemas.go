package main

import (
	"strings"
	"time"

	"github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

// User 用户表
type User struct {
	ID        string `json:"id" gorm:"primary_key"`
	Name      string `json:"name" gorm:"unique;not null;unique_index"`
	Password  string `json:"-"`
	Email     string `json:"email"`
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
	UserID            string         `json:user gorm:"index"`
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

func validateUsername(username *string, r *Response) {
	*username = strings.ToLower(*username)
	if len(*username) == 0 {
		r.ErrFor["username"] = "required"
	} else {
		if ok := rUsername.MatchString(*username); !ok {
			r.ErrFor["username"] = `only use letters, numbers, \'-\', \'_\'`
		}
	}
}

func validateEmail(email *string, r *Response) {
	*email = strings.ToLower(*email)
	if len(*email) == 0 {
		r.ErrFor["email"] = "required"
	} else {
		if ok := rEmail.MatchString(*email); !ok {
			r.ErrFor["email"] = `invalid email format`
		}
	}
}

func validatePassword(password *string, r *Response) {
	if len(*password) == 0 {
		r.ErrFor["password"] = "required"
	} else {
		if len(*password) < 4 {
			r.ErrFor["password"] = `too weak password, at least 4 necessary`
		}
	}
}

func (user *User) changePassword(r *Response) (err error) {

	var body struct {
		Confirm  string `form:"confirm" binding:"required"`
		Password string `form:"password" binding:"required"`
	}

	err = r.c.Bind(&body)
	if err != nil {
		r.ErrFor["binding"] = err.Error()
		FATAL(err)
	}

	// validate
	if len(body.Password) == 0 {
		r.ErrFor["password"] = "required"
	}
	if len(body.Confirm) == 0 {
		r.ErrFor["confirm"] = "required"
	} else if body.Password != body.Confirm {
		r.Errors = append(r.Errors, "Passwords do not match.")
	}

	if r.HasErrors() {
		err = Err
		return
	}
	// user.setPassword(body.Password)
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		FATAL(err)
	}
	err = db.Model(&User{}).Where("id = ?", user.ID).Update(User{Password: string(hashedPassword)}).Error
	if err != nil {
		r.Errors = append(r.Errors, err.Error())
		err = Err
		return
	}

	return
}
