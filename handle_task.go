package main

import (
	"io"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

func listTasks(c *gin.Context) {
	res := NewRes()
	uid := c.GetString(identityKey)
	if uid == "" {
		uid = c.GetString(userKey)
	}

	var tasks []*Task
	taskSet.Range(func(_, v interface{}) bool {
		task, ok := v.(*Task)
		if ok {
			if task.Owner == uid {
				tasks = append(tasks, task)
			}
		}
		return true
	})

	dbtasks := []*Task{}
	err := db.Where(`owner = ? `, uid).Find(&dbtasks).Error
	if err != nil {
		log.Errorf(`listTasks, query %s's tasks info error`, uid)
	}
	for _, t := range dbtasks {
		updated := false
		for i, tt := range tasks {
			if tt.ID == t.ID {
				tasks[i] = tt
				updated = true
				break
			}
		}
		if !updated {
			tasks = append(tasks, t)
		}
	}

	res.DoneData(c, tasks)
}

func taskQuery(c *gin.Context) {
	res := NewRes()
	uid := c.GetString(identityKey)
	if uid == "" {
		uid = c.GetString(userKey)
	}
	ids := c.Param("ids")
	if ids == "" {
		res.Fail(c, 4001)
		return
	}
	var tasks []*Task
	for _, id := range strings.Split(ids, ",") {
		v, ok := taskSet.Load(id)
		if ok {
			task, ok := v.(*Task)
			if ok {
				tasks = append(tasks, task)
				continue
			}
		}
		task := &Task{}
		err := db.Where(`id = ? `, id).First(task).Error
		if err == nil {
			tasks = append(tasks, task)
			continue
		}
		log.Errorf(`taskQuery, query %s's task(%s) info error`, uid, id)
	}

	res.DoneData(c, tasks)
}

func taskStreamQuery(c *gin.Context) {
	id := c.Param("id")
	uid := c.GetString(identityKey)
	if uid == "" {
		uid = c.GetString(userKey)
	}
	log.Info(uid)
	task, ok := taskSet.Load(id)
	if ok {
		// listener := openListener(roomid)
		ticker := time.NewTicker(1 * time.Second)
		// users.Add("connected", 1)
		defer func() {
			// closeListener(roomid, listener)
			ticker.Stop()
			// users.Add("disconnected", 1)
		}()

		c.Stream(func(w io.Writer) bool {
			select {
			// case msg := <-listener:
			// 	messages.Add("outbound", 1)
			// 	c.SSEvent("message", msg)
			case <-ticker.C:
				c.SSEvent("task", task)
			}
			return true
		})
	}
}
