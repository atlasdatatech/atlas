package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func debugSetup() *gin.Engine {

	initConf("conf.toml")
	var err error
	db, err = initDb()
	if err != nil {
		log.Fatalf("init db error, details: %s", err)
	}

	provd, err = initProvider()
	if err != nil {
		log.Fatalf("init provider error: %s", err)
	}

	casEnf, err = initEnforcer()
	if err != nil {
		log.Fatalf("init enforcer error: %s", err)
	}

	initSystemUser()
	initTaskRouter()
	loadPubServices()

	return setupRouter()
}

// defer db.Close()
func TestPingRoute(t *testing.T) {
	router := debugSetup()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/ping", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "pong", w.Body.String())
}
