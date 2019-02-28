// Copyright 2017 Manu Martinez-Almeida.  All rights reserved.
// Use of this source code is governed by a MIT style
// license that can be found in the LICENSE file.

package main

import (
	"html/template"
	"net/http"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
)

func BenchmarkOneRoute(B *testing.B) {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.GET("/ping", func(c *gin.Context) {
	})
	runRequest(B, router, "GET", "/ping")
}

func BenchmarkRecoveryMiddleware(B *testing.B) {
	router := gin.New()
	router.Use(gin.Recovery())
	router.GET("/", func(c *gin.Context) {})
	runRequest(B, router, "GET", "/")
}

func BenchmarkLoggerMiddleware(B *testing.B) {
	router := gin.New()
	router.Use(gin.LoggerWithWriter(newMockWriter()))
	router.GET("/", func(c *gin.Context) {})
	runRequest(B, router, "GET", "/")
}

func BenchmarkManyHandlers(B *testing.B) {
	router := gin.New()
	router.Use(gin.Recovery(), gin.LoggerWithWriter(newMockWriter()))
	router.Use(func(c *gin.Context) {})
	router.Use(func(c *gin.Context) {})
	router.GET("/ping", func(c *gin.Context) {})
	runRequest(B, router, "GET", "/ping")
}

func Benchmark5Params(B *testing.B) {
	gin.DefaultWriter = os.Stdout
	router := gin.New()
	router.Use(func(c *gin.Context) {})
	router.GET("/param/:param1/:params2/:param3/:param4/:param5", func(c *gin.Context) {})
	runRequest(B, router, "GET", "/param/path/to/parameter/john/12345")
}

func BenchmarkOneRouteJSON(B *testing.B) {
	router := gin.New()
	data := struct {
		Status string `json:"status"`
	}{"ok"}
	router.GET("/json", func(c *gin.Context) {
		c.JSON(http.StatusOK, data)
	})
	runRequest(B, router, "GET", "/json")
}

func BenchmarkOneRouteHTML(B *testing.B) {
	router := gin.New()
	t := template.Must(template.New("index").Parse(`
		<html><body><h1>{{.}}</h1></body></html>`))
	router.SetHTMLTemplate(t)

	router.GET("/html", func(c *gin.Context) {
		c.HTML(http.StatusOK, "index", "hola")
	})
	runRequest(B, router, "GET", "/html")
}

func BenchmarkOneRouteSet(B *testing.B) {
	router := gin.New()
	router.GET("/ping", func(c *gin.Context) {
		c.Set("key", "value")
	})
	runRequest(B, router, "GET", "/ping")
}

func BenchmarkOneRouteString(B *testing.B) {
	router := gin.New()
	router.GET("/text", func(c *gin.Context) {
		c.String(http.StatusOK, "this is a plain text")
	})
	runRequest(B, router, "GET", "/text")
}

func BenchmarkManyRoutesFist(B *testing.B) {
	router := gin.New()
	router.Any("/ping", func(c *gin.Context) {})
	runRequest(B, router, "GET", "/ping")
}

func BenchmarkManyRoutesLast(B *testing.B) {
	router := gin.New()
	router.Any("/ping", func(c *gin.Context) {})
	runRequest(B, router, "OPTIONS", "/ping")
}

func Benchmark404(B *testing.B) {
	router := gin.New()
	router.Any("/something", func(c *gin.Context) {})
	router.NoRoute(func(c *gin.Context) {})
	runRequest(B, router, "GET", "/ping")
}

func Benchmark404Many(B *testing.B) {
	router := gin.New()
	router.GET("/", func(c *gin.Context) {})
	router.GET("/path/to/something", func(c *gin.Context) {})
	router.GET("/post/:id", func(c *gin.Context) {})
	router.GET("/view/:id", func(c *gin.Context) {})
	router.GET("/favicon.ico", func(c *gin.Context) {})
	router.GET("/robots.txt", func(c *gin.Context) {})
	router.GET("/delete/:id", func(c *gin.Context) {})
	router.GET("/user/:id/:mode", func(c *gin.Context) {})

	router.NoRoute(func(c *gin.Context) {})
	runRequest(B, router, "GET", "/viewfake")
}

type mockWriter struct {
	headers http.Header
}

func newMockWriter() *mockWriter {
	return &mockWriter{
		http.Header{},
	}
}

func (m *mockWriter) Header() (h http.Header) {
	return m.headers
}

func (m *mockWriter) Write(p []byte) (n int, err error) {
	return len(p), nil
}

func (m *mockWriter) WriteString(s string) (n int, err error) {
	return len(s), nil
}

func (m *mockWriter) WriteHeader(int) {}

func runRequest(B *testing.B, r *gin.Engine, method, path string) {
	// create fake request
	req, err := http.NewRequest(method, path, nil)
	if err != nil {
		panic(err)
	}
	w := newMockWriter()
	B.ReportAllocs()
	B.ResetTimer()
	for i := 0; i < B.N; i++ {
		r.ServeHTTP(w, req)
	}
}
