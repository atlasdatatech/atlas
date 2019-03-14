package main

import (
	"bytes"
	"errors"
	"io"
	"sort"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/transform"
)

//ErrUnknown 提供的编码格式字符串未知时，会返回本错误
var ErrUnknown = errors.New("unknown codec")

//Codec 映射编码格式字符串到对应的编解码器，因此不需在导入encoding等包
var Codec = map[string]encoding.Encoding{
	"utf-8":   encoding.Nop,
	"gbk":     simplifiedchinese.GBK,
	"big5":    traditionalchinese.Big5,
	"gb18030": simplifiedchinese.GB18030,
}

//NewReader 函数返回一个Reader接口，该接口将从r读取的数据解码后返回；
// codec参数指定编码格式，data为从r读取以检测编码格式的数据；
// 函数会首先解码data，以便返回完整的解码后文本，会自动处理BOM。
func NewReader(r io.Reader, codec string, data []byte) (io.Reader, error) {
	switch codec {
	case "utf-8":
		if len(data) >= 3 && string(data[:3]) == "\xEF\xBB\xBF" {
			data = data[3:]
		}
	case "gb18030":
		if len(data) >= 4 && string(data[:4]) == "\x84\x31\x95\x33" {
			data = data[4:]
		}
	}
	if c, ok := Codec[codec]; ok {
		return transform.NewReader(io.MultiReader(bytes.NewReader(data), r), c.NewDecoder()), nil
	}
	return nil, ErrUnknown
}

//NewWriter 函数返回一个Writer接口，该接口将提供的数据编码后写入w；
// codec参数指定编码格式，如果bom为真，会在w开始处写入BOM标识。
func NewWriter(w io.Writer, codec string, bom bool) (io.Writer, error) {
	if bom {
		switch codec {
		case "utf-8":
			w.Write([]byte("\xEF\xBB\xBF"))
		case "gb18030":
			w.Write([]byte("\x84\x31\x95\x33"))
		}
	}
	if c, ok := Codec[codec]; ok {
		return transform.NewWriter(w, c.NewEncoder()), nil
	}
	return nil, ErrUnknown
}

type detect interface {
	String() string
	Feed(byte) bool
	Priority() float64
}

// 使用bom来确定编码格式
func checkbom(data []byte) string {
	if len(data) >= 3 {
		if string(data[:3]) == "\xEF\xBB\xBF" {
			return "utf-8"
		}
	}
	if len(data) >= 4 {
		if string(data[:4]) == "\x84\x31\x95\x33" {
			return "gb18030"
		}
	}
	return ""
}

func check(data []byte, lst []detect) []detect {
	for _, c := range data {
		for i, l := 0, len(lst); i < l; {
			if !lst[i].Feed(c) {
				copy(lst[i:], lst[i+1:])
				l--
				lst = lst[:l]
			} else {
				i++
			}
		}
	}
	if len(lst) == 0 {
		return nil
	}
	return lst
}

//Mostlike 本函数返回文本最可能的编码格式
func Mostlike(data []byte) string {
	if s := checkbom(data); s != "" {
		return s
	}
	lb := check(data, []detect{&utf8a{}})
	if len(lb) > 0 {
		x, y := -1, -100.0
		for i, l := range lb {
			if r := l.Priority(); y < r {
				x, y = i, r
			}
		}
		return lb[x].String()
	}
	lp := check(data, []detect{&gbk{}, &gb18030{}})
	if len(lp) > 0 {
		x, y := -1, -100.0
		for i, l := range lp {
			if r := l.Priority(); y < r {
				x, y = i, r
			}
		}
		return lp[x].String()
	}
	return ""
}

//Possible 本函数返回文本所有可能的编码格式，可能性越高越靠前
func Possible(data []byte) []string {
	if s := checkbom(data); s != "" {
		return []string{s}
	}
	lb := check(data, []detect{
		&utf8a{},
		&gbk{}, &big5{}, &gb18030{}})
	if l := len(lb); l > 0 {
		x := make(tks, l)
		for i, e := range lb {
			x[i] = tk{e.Priority(), e.String()}
		}
		sort.Stable(sort.Reverse(x))
		s := make([]string, l)
		for i, y := range x {
			s[i] = y.s
		}
		return s
	}
	return nil
}

type tk struct {
	f float64
	s string
}

type tks []tk

func (t tks) Len() int           { return len(t) }
func (t tks) Less(i, j int) bool { return t[i].f < t[j].f }
func (t tks) Swap(i, j int)      { t[i], t[j] = t[j], t[i] }

var freqCH = []float64{
	11.096582,  // 的
	3.6654361,  // 一
	2.6676806,  // 在
	2.6263398,  // 人
	2.5763892,  // 了
	2.5239474,  // 有
	2.4985507,  // 中
	2.4750994,  // 是
	2.3198212,  // 年
	2.1991706,  // 和
	2.1485313,  // 大
	2.0583049,  // 不
	1.6294498,  // 工
	1.5714877,  // 上
	1.5505652,  // 地
	1.5171042,  // 市
	1.4632588,  // 要
	1.3827674,  // 出
	1.3586011,  // 行
	1.3175501,  // 作
	1.2955099,  // 生
	1.2891391,  // 家
	1.2473995,  // 以
	1.2101566,  // 成
	1.2006361,  // 到
	1.1811662,  // 日
	1.1687556,  // 民
	1.1265230,  // 我
	1.0738254,  // 部
	1.0529029,  // 多
	1.0516987,  // 全
	1.0421782,  // 建
	1.0221174,  // 他
	1.0187194,  // 公
	0.99280710, // 展
	0.97053741, // 理
	0.94195711, // 新
	0.92766508, // 方
	0.90901544, // 主
	0.89599532, // 企
	0.87450082, // 制
	0.87105764, // 政
	0.86749028, // 用
	0.84886698, // 同
	0.84326005, // 法
	0.83719403, // 高
	0.81264394, // 本
	0.79300463, // 月
	0.78911364, // 定
	0.77508878, // 化
	0.77478774, // 加
	0.77030219, // 合
	0.76452216, // 品
	0.76275353, // 重
	0.75337227, // 分
	0.74597789, // 力
	0.73151276, // 外
	0.70293998, // 就
	0.70249971, // 等
	0.69440917, // 下
	0.67208680, // 元
	0.67004723, // 社
	0.66755610, // 前
	0.65572510, // 面
	0.64185452, // 也
	0.62394997, // 之
	0.61391394, // 而
	0.61310865, // 利
	0.60858923, // 文
	0.60489393, // 事
	0.60081479, // 可
	0.58719258, // 改
	0.57844351, // 各
	0.56740650, // 好
	0.56673668, // 金
	0.56579968, // 司
	0.56292096, // 其
	0.54127970, // 平
	0.54109531, // 代
	0.53594371, // 天
}

// [\x00-\x7F]
// [\xC0-\xDF][\x80-\xBF]
// [\xE0-\xEF][\x80-\xBF]{2}
// [\xF0-\xF7][\x80-\xBF]{3}
type utf8a struct {
	byte
}

func (u utf8a) String() string {
	return "utf-8"
}

func (u *utf8a) Feed(x byte) bool {
	if u.byte == 0 {
		if x >= 0x00 && x <= 0x7F {
			return true
		}
		if x >= 0xC0 && x <= 0xDF {
			u.byte = 1
			return true
		}
		if x >= 0xE0 && x <= 0xEF {
			u.byte = 2
			return true
		}
		if x >= 0xF0 && x <= 0xF7 {
			u.byte = 3
			return true
		}
	} else {
		if x >= 0x80 && x <= 0xBF {
			u.byte--
			return true
		}
	}
	return false
}

func (u utf8a) Priority() float64 {
	return 0
}

var dictB5 = map[uint32]int{
	0xAABA: 0x00, // 的
	0xA440: 0x01, // 一
	0xA662: 0x02, // 在
	0xA448: 0x03, // 人
	0xA446: 0x04, // 了
	0xA6B3: 0x05, // 有
	0xA4A4: 0x06, // 中
	0xAC4F: 0x07, // 是
	0xA67E: 0x08, // 年
	0xA94D: 0x09, // 和
	0xA46A: 0x0A, // 大
	0xA4A3: 0x0B, // 不
	0xA475: 0x0C, // 工
	0xA457: 0x0D, // 上
	0xA661: 0x0E, // 地
	0xA5AB: 0x0F, // 市
	0xAD6E: 0x10, // 要
	0xA558: 0x11, // 出
	0xA6E6: 0x12, // 行
	0xA740: 0x13, // 作
	0xA5CD: 0x14, // 生
	0xAE61: 0x15, // 家
	0xA548: 0x16, // 以
	0xA6A8: 0x17, // 成
	0xA8EC: 0x18, // 到
	0xA4E9: 0x19, // 日
	0xA5C1: 0x1A, // 民
	0xA7DA: 0x1B, // 我
	0xB3A1: 0x1C, // 部
	0xA668: 0x1D, // 多
	0xA5FE: 0x1E, // 全
	0xABD8: 0x1F, // 建
	0xA54C: 0x20, // 他
	0xA4BD: 0x21, // 公
	0xAE69: 0x22, // 展
	0xB27A: 0x23, // 理
	0xB773: 0x24, // 新
	0xA4E8: 0x25, // 方
	0xA544: 0x26, // 主
	0xA5F8: 0x27, // 企
	0xA8EE: 0x28, // 制
	0xAC46: 0x29, // 政
	0xA5CE: 0x2A, // 用
	0xA650: 0x2B, // 同
	0xAA6B: 0x2C, // 法
	0xB0AA: 0x2D, // 高
	0xA5BB: 0x2E, // 本
	0xA4EB: 0x2F, // 月
	0xA977: 0x30, // 定
	0xA4C6: 0x31, // 化
	0xA55B: 0x32, // 加
	0xA658: 0x33, // 合
	0xAB7E: 0x34, // 品
	0xADAB: 0x35, // 重
	0xA4C0: 0x36, // 分
	0xA44F: 0x37, // 力
	0xA57E: 0x38, // 外
	0xB44E: 0x39, // 就
	0xB5A5: 0x3A, // 等
	0xA455: 0x3B, // 下
	0xA4B8: 0x3C, // 元
	0xAAC0: 0x3D, // 社
	0xAB65: 0x3E, // 前
	0xADB1: 0x3F, // 面
	0xA45D: 0x40, // 也
	0xA4A7: 0x41, // 之
	0xA6D3: 0x42, // 而
	0xA751: 0x43, // 利
	0xA4E5: 0x44, // 文
	0xA8C6: 0x45, // 事
	0xA569: 0x46, // 可
	0xA7EF: 0x47, // 改
	0xA655: 0x48, // 各
	0xA66E: 0x49, // 好
	0xAAF7: 0x4A, // 金
	0xA571: 0x4B, // 司
	0xA8E4: 0x4C, // 其
	0xA5AD: 0x4D, // 平
	0xA54E: 0x4E, // 代
	0xA4D1: 0x4F, // 天
}

// [\x00-\x7F]
// [\xA1-\xF9][\x40-\x7E\xA1-\xFE]
type big5 struct {
	byte
	rune
	hold [80]int
	ttls int
}

func (b big5) String() string {
	return "big5"
}

func (b *big5) Feed(x byte) (ans bool) {
	if b.byte == 0 {
		if x >= 0x00 && x <= 0x7F {
			return true
		}
		if x >= 0xA1 && x <= 0xF9 {
			b.byte = 1
			b.rune = rune(x) << 8
			return true
		}
	} else {
		if (x >= 0x40 && x <= 0x7E) || (x >= 0xA1 && x <= 0xFE) {
			b.byte = 0
			b.rune |= rune(x)
			b.count()
			return true
		}
	}
	return false
}

func (b *big5) Priority() float64 {
	if b.ttls == 0 {
		return 0
	}
	f := 0.0
	for i, x := range b.hold {
		k := 100*float64(x)/float64(b.ttls) - freqCH[i]
		if k >= 0 {
			f += k
		} else {
			f -= k
		}
	}
	return 100 - f
}

func (b *big5) count() {
	if i, ok := dictB5[uint32(b.rune)]; ok {
		b.hold[i]++
		b.ttls++
	}
}

var dictGB = map[uint32]int{
	0xB5C4: 0x00, // 的
	0xD2BB: 0x01, // 一
	0xD4DA: 0x02, // 在
	0xC8CB: 0x03, // 人
	0xC1CB: 0x04, // 了
	0xD3D0: 0x05, // 有
	0xD6D0: 0x06, // 中
	0xCAC7: 0x07, // 是
	0xC4EA: 0x08, // 年
	0xBACD: 0x09, // 和
	0xB4F3: 0x0A, // 大
	0xB2BB: 0x0B, // 不
	0xB9A4: 0x0C, // 工
	0xC9CF: 0x0D, // 上
	0xB5D8: 0x0E, // 地
	0xCAD0: 0x0F, // 市
	0xD2AA: 0x10, // 要
	0xB3F6: 0x11, // 出
	0xD0D0: 0x12, // 行
	0xD7F7: 0x13, // 作
	0xC9FA: 0x14, // 生
	0xBCD2: 0x15, // 家
	0xD2D4: 0x16, // 以
	0xB3C9: 0x17, // 成
	0xB5BD: 0x18, // 到
	0xC8D5: 0x19, // 日
	0xC3F1: 0x1A, // 民
	0xCED2: 0x1B, // 我
	0xB2BF: 0x1C, // 部
	0xB6E0: 0x1D, // 多
	0xC8AB: 0x1E, // 全
	0xBDA8: 0x1F, // 建
	0xCBFB: 0x20, // 他
	0xB9AB: 0x21, // 公
	0xD5B9: 0x22, // 展
	0xC0ED: 0x23, // 理
	0xD0C2: 0x24, // 新
	0xB7BD: 0x25, // 方
	0xD6F7: 0x26, // 主
	0xC6F3: 0x27, // 企
	0xD6C6: 0x28, // 制
	0xD5FE: 0x29, // 政
	0xD3C3: 0x2A, // 用
	0xCDAC: 0x2B, // 同
	0xB7A8: 0x2C, // 法
	0xB8DF: 0x2D, // 高
	0xB1BE: 0x2E, // 本
	0xD4C2: 0x2F, // 月
	0xB6A8: 0x30, // 定
	0xBBAF: 0x31, // 化
	0xBCD3: 0x32, // 加
	0xBACF: 0x33, // 合
	0xC6B7: 0x34, // 品
	0xD6D8: 0x35, // 重
	0xB7D6: 0x36, // 分
	0xC1A6: 0x37, // 力
	0xCDE2: 0x38, // 外
	0xBECD: 0x39, // 就
	0xB5C8: 0x3A, // 等
	0xCFC2: 0x3B, // 下
	0xD4AA: 0x3C, // 元
	0xC9E7: 0x3D, // 社
	0xC7B0: 0x3E, // 前
	0xC3E6: 0x3F, // 面
	0xD2B2: 0x40, // 也
	0xD6AE: 0x41, // 之
	0xB6F8: 0x42, // 而
	0xC0FB: 0x43, // 利
	0xCEC4: 0x44, // 文
	0xCAC2: 0x45, // 事
	0xBFC9: 0x46, // 可
	0xB8C4: 0x47, // 改
	0xB8F7: 0x48, // 各
	0xBAC3: 0x49, // 好
	0xBDF0: 0x4A, // 金
	0xCBBE: 0x4B, // 司
	0xC6E4: 0x4C, // 其
	0xC6BD: 0x4D, // 平
	0xB4FA: 0x4E, // 代
	0xCCEC: 0x4F, // 天
}

// [\x00-\x7F]
// [\x81-\xFE][\x40-\x7E\x80-\xFE]
type gbk struct {
	byte
	rune
	hold [80]int
	ttls int
}

func (g gbk) String() string {
	return "gbk"
}

func (g *gbk) Feed(x byte) (ans bool) {
	if g.byte == 0 {
		if x >= 0x00 && x <= 0x7F {
			return true
		}
		if x >= 0x81 && x <= 0xFE {
			g.byte = 1
			g.rune = rune(x) << 8
			return true
		}
	} else {
		if (x >= 0x40 && x <= 0x7E) || (x >= 0x80 && x <= 0xFE) {
			g.byte = 0
			g.rune |= rune(x)
			g.count()
			return true
		}
	}
	return false
}

func (g *gbk) Priority() float64 {
	if g.ttls == 0 {
		return 0
	}
	f := 0.0
	for i, x := range g.hold {
		k := 100*float64(x)/float64(g.ttls) - freqCH[i]
		if k >= 0 {
			f += k
		} else {
			f -= k
		}
	}
	return 100 - f
}

func (g *gbk) count() {
	if i, ok := dictGB[uint32(g.rune)]; ok {
		g.hold[i]++
		g.ttls++
	}
}

// [\x00-\x7F]
// [\x81-\xFE][\x40-\x7E\x80-\xFE]
// [\x81-\xFE][\x30-\x39][\x81-\xFE][\x30-\x39]
type gb18030 struct {
	byte
}

func (g gb18030) String() string {
	return "gb18030"
}

func (g *gb18030) Feed(x byte) bool {
	switch g.byte {
	case 0:
		if x >= 0x00 && x <= 0x7F {
			return true
		}
		if x >= 0x81 && x <= 0xFE {
			g.byte = 1
			return true
		}
	case 1:
		if (x >= 0x40 && x <= 0x7E) || (x >= 0x80 && x <= 0xFE) {
			g.byte = 0
			return true
		}
		if x >= 0x30 && x <= 0x39 {
			g.byte = 2
			return true
		}
	case 2:
		if x >= 0x81 && x <= 0xFE {
			g.byte = 3
			return true
		}
	default:
		if x >= 0x30 && x <= 0x39 {
			g.byte = 0
			return true
		}
	}
	return false
}

func (g *gb18030) Priority() float64 {
	return -100
}
