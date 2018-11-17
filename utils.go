package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	gomail "gopkg.in/gomail.v2"
)

var codes = map[int]string{
	100: "Continue",
	101: "Switching Protocols",
	102: "Processing",
	200: "OK",
	201: "Created",
	202: "Accepted",
	203: "Non-Authoritative Information",
	204: "No Content",
	205: "Reset Content",
	206: "Partial Content",
	207: "Multi-Status",
	300: "Multiple Choices",
	301: "Moved Permanently",
	302: "Moved Temporarily",
	303: "See Other",
	304: "Not Modified",
	305: "Use Proxy",
	307: "Temporary Redirect",
	400: "Bad Request",
	401: "Unauthorized",
	402: "Payment Required",
	403: "Forbidden",
	404: "Not Found",
	405: "Method Not Allowed",
	406: "Not Acceptable",
	407: "Proxy Authentication Required",
	408: "Request Time-out",
	409: "Conflict",
	410: "Gone",
	411: "Length Required",
	412: "Precondition Failed",
	413: "Request Entity Too Large",
	414: "Request-URI Too Large",
	415: "Unsupported Media Type",
	416: "Requested Range Not Satisfiable",
	417: "Expectation Failed",
	418: "I'm a teapot",
	422: "Unprocessable Entity",
	423: "Locked",
	424: "Failed Dependency",
	425: "Unordered Collection",
	426: "Upgrade Required",
	428: "Precondition Required",
	429: "Too Many Requests",
	431: "Request Header Fields Too Large",
	451: "Unavailable For Legal Reasons",
	500: "Internal Server Error",
	501: "Not Implemented",
	502: "Bad Gateway",
	503: "Service Unavailable",
	504: "Gateway Time-out",
	505: "HTTP Version Not Supported",
	506: "Variant Also Negotiates",
	507: "Insufficient Storage",
	509: "Bandwidth Limit Exceeded",
	510: "Not Extended",
	511: "Network Authentication Required",
}

//Res response schema
type Res struct {
	Code    int    `json:"code"`
	Error   string `json:"error"`
	Message string `json:"message"`
}

//NewRes Create Res
func NewRes() *Res {
	return &Res{
		Code:  http.StatusOK,
		Error: codes[http.StatusOK],
	}
}

//Fail failed error
func (res *Res) Fail(c *gin.Context, err error) {
	res.Code = http.StatusOK
	res.Error = err.Error()
	c.JSON(http.StatusOK, res)
}

//FailStr failed string
func (res *Res) FailStr(c *gin.Context, err string) {
	res.Code = http.StatusOK
	res.Error = err
	c.JSON(http.StatusOK, res)
}

//Done done
func (res *Res) Done(c *gin.Context, msg string) {
	res.Code = http.StatusOK
	res.Error = "none"
	if msg == "" {
		res.Message = "done"
	} else {
		res.Message = msg
	}
	c.JSON(http.StatusOK, res)
}

//Reset reset to init
func (res *Res) Reset() {
	res.Code = http.StatusOK
	res.Error = codes[http.StatusOK]
	res.Message = ""
}

//MailConfig email config and data
type MailConfig struct {
	From     string
	ReplyTo  string
	Subject  string
	TextPath string
	HTMLPath string
	Data     interface{}
}

//SendMail send email
func (conf *MailConfig) SendMail() (err error) {
	m := gomail.NewMessage()

	m.SetHeader("From", conf.From)
	m.SetHeader("To", conf.ReplyTo)
	m.SetHeader("Subject", conf.Subject)
	m.SetHeader("ReplyTo", conf.ReplyTo)

	m.AddAlternativeWriter("text/html", func(w io.Writer) error {
		return template.Must(template.ParseFiles(conf.HTMLPath)).Execute(w, conf.Data)
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

//XHR xmlhttprequest
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

func createPaths(name string) {
	styles := cfgV.GetString("assets.styles")
	fonts := cfgV.GetString("assets.fonts")
	tilesets := cfgV.GetString("assets.tilesets")
	datasets := cfgV.GetString("assets.datasets")
	os.MkdirAll(styles, os.ModePerm)
	os.MkdirAll(tilesets, os.ModePerm)
	os.MkdirAll(datasets, os.ModePerm)
	os.MkdirAll(fonts, os.ModePerm)
}
func GID(v interface{}) (gid uint64, err error) {
	switch aval := v.(type) {
	case float64:
		return uint64(aval), nil
	case int64:
		return uint64(aval), nil
	case uint64:
		return aval, nil
	case uint:
		return uint64(aval), nil
	case int8:
		return uint64(aval), nil
	case uint8:
		return uint64(aval), nil
	case uint16:
		return uint64(aval), nil
	case int32:
		return uint64(aval), nil
	case uint32:
		return uint64(aval), nil
	case string:
		return strconv.ParseUint(aval, 10, 64)
	default:
		return gid, fmt.Errorf("unable to convert field into a uint64.")
	}
}
