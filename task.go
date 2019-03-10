package main

import (
	"fmt"

	_ "github.com/mattn/go-sqlite3" // import sqlite3 driver
	// "github.com/paulmach/orb/encoding/wkb"
)

// Task 数据导入信息预览
type Task struct {
	ID       string        `json:"id" form:"id" binding:"required"`
	Name     string        `json:"name" form:"name"`
	Type     string        `json:"type" form:"type" `
	Owner    string        `json:"owner" form:"owner"`
	Fail     int           `json:"fail" form:"fail"`
	Succeed  int           `json:"succeed" form:"succeed"`
	Count    int           `json:"count" form:"count"`
	Progress int           `json:"progress" form:"progress"`
	Status   string        `json:"status"`
	Err      string        `json:"err"`
	Pipe     chan struct{} `json:"-" form:"-" gorm:"-"`
}

func (task *Task) save() error {
	if task == nil {
		return fmt.Errorf("task may not be nil")
	}
	err := db.Create(task).Error
	if err != nil {
		return err
	}
	return nil
}

func (task *Task) update() error {
	if task == nil {
		return fmt.Errorf("task may not be nil")
	}
	err := db.Model(&Task{}).Update(task).Error
	if err != nil {
		return err
	}
	return nil
}

func (task *Task) info() error {
	if task == nil {
		return fmt.Errorf("task may not be nil")
	}
	err := db.Where(`id = ? `, task.ID).First(task).Error
	if err != nil {
		return err
	}
	return nil
}
