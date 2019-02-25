package main

import (
	"compress/gzip"

	"github.com/gin-gonic/gin"
)

// GzipWriter 压缩
type GzipWriter struct {
	gin.ResponseWriter
	writer *gzip.Writer
}

//WriteString 压缩字符串
func (g *GzipWriter) WriteString(s string) (int, error) {
	return g.writer.Write([]byte(s))
}

//Write 压缩字节流数据
func (g *GzipWriter) Write(data []byte) (int, error) {
	return g.writer.Write(data)
}
