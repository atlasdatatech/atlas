package main

import (
	"crypto/rand"
	"encoding/hex"
	"html/template"
	"io"
	"net/url"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	gomail "gopkg.in/gomail.v2"
)

type MailConfig struct {
	From     string
	ReplyTo  string
	Subject  string
	TextPath string
	HtmlPath string
	Data     interface{}
}

func (conf *MailConfig) SendMail() (err error) {
	m := gomail.NewMessage()

	m.SetHeader("From", conf.From)
	m.SetHeader("To", conf.ReplyTo)
	m.SetHeader("Subject", conf.Subject)
	m.SetHeader("ReplyTo", conf.ReplyTo)

	m.AddAlternativeWriter("text/html", func(w io.Writer) error {
		return template.Must(template.ParseFiles(conf.HtmlPath)).Execute(w, conf.Data)
	})

	d := gomail.NewDialer(cfgV.GetString("smtp.credentials.host"), 587, cfgV.GetString("smtp.credentials.user"), cfgV.GetString("smtp.credentials.password"))
	return d.DialAndSend(m)
}

func getEscapedString(str string) string {
	return strings.Replace(url.QueryEscape(str), "+", "%20", -1)
}

var rSlugify1, _ = regexp.Compile(`[^\w ]+`)
var rSlugify2, _ = regexp.Compile(` +`)

var rUsername, _ = regexp.Compile(`^[a-zA-Z0-9\-\_]+$`)
var rEmail, _ = regexp.Compile(`^[a-zA-Z0-9\-\_\.\+]+@[a-zA-Z0-9\-\_\.]+\.[a-zA-Z0-9\-\_]+$`)

var rVerificationURL, _ = regexp.Compile(`^\/account\/verification\/`)
var signupProviderReg, _ = regexp.Compile(`/[^a-zA-Z0-9\-\_]/g`)

/**
preparing id
*/

func slugify(str string) string {
	str = strings.ToLower(str)
	str = rSlugify1.ReplaceAllString(str, "")
	str = rSlugify2.ReplaceAllString(str, "-")
	return str
}

func slugifyName(str string) string {
	str = strings.TrimSpace(str)
	return rSlugify2.ReplaceAllString(str, " ")
}

func XHR(c *gin.Context) bool {
	return strings.ToLower(c.Request.Header.Get("X-Requested-With")) == "xmlhttprequest"
}

func generateToken(n int) []byte {
	b := make([]byte, n)
	_, err := rand.Read(b)
	if err != nil {
		return b
	}
	token := make([]byte, n*2)
	hex.Encode(token, b)
	return token
}
