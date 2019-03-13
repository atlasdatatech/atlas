package main

import (
	"io"
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
	log.Info(uid)
	id := c.Param("id")
	task, ok := taskSet.Load(id)
	if ok {
		res.DoneData(c, task)
		return
	}
	dbtask := &Task{ID: id}
	err := dbtask.info()
	if err != nil {
		res.FailMsg(c, "task not found")
		return
	}
	res.DoneData(c, dbtask)
}

func taskQuery(c *gin.Context) {
	res := NewRes()
	uid := c.GetString(identityKey)
	if uid == "" {
		uid = c.GetString(userKey)
	}
	log.Info(uid)
	id := c.Param("id")
	task, ok := taskSet.Load(id)
	if ok {
		res.DoneData(c, task)
		return
	}
	dbtask := &Task{ID: id}
	err := dbtask.info()
	if err != nil {
		res.FailMsg(c, "task not found")
		return
	}
	res.DoneData(c, dbtask)
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
