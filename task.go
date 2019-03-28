package main

import (
	"time"

	_ "github.com/mattn/go-sqlite3" // import sqlite3 driver
	// "github.com/paulmach/orb/encoding/wkb"
)

// Task 数据导入信息预览
type Task struct {
	ID        string        `json:"id" form:"id" binding:"required" gorm:"primary_key"`
	Base      string        `json:"base" form:"base" gorm:"index"`
	Name      string        `json:"name" form:"name"`
	Type      TaskType      `json:"type" form:"type" `
	Owner     string        `json:"owner" form:"owner"`
	Count     int           `json:"count" form:"count"`
	Total     int           `json:"total" form:"total"`
	Progress  int           `json:"progress" form:"progress"`
	Status    string        `json:"status"`
	Error     string        `json:"error" `
	Pipe      chan struct{} `json:"-" form:"-" gorm:"-"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
}

func (task *Task) save() error {
	err := db.Create(task).Error
	if err != nil {
		return err
	}
	return nil
}

func (task *Task) update() error {
	err := db.Model(&Task{}).Update(task).Error
	if err != nil {
		return err
	}
	return nil
}
