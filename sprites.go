package atlas

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/axgle/mahonia"
	"github.com/nfnt/resize"
	log "github.com/sirupsen/logrus"
)

//Bin 结构
type Bin struct {
	id       int
	w        int
	h        int
	maxw     int
	maxh     int
	x        int
	y        int
	refcount int
	name     string
}

// NewBin 初始化
func NewBin(id1, w1, h1, maxw1, maxh1, x1, y1 int) *Bin {
	b := &Bin{
		id:       id1,
		w:        w1,
		h:        h1,
		maxw:     maxw1,
		maxh:     maxh1,
		x:        x1,
		y:        y1,
		refcount: 0,
	}
	if b.maxw == -1 {
		b.maxw = b.w
	}
	if b.maxh == -1 {
		b.maxh = b.h
	}
	return b
}

func (bin *Bin) String() string {
	return fmt.Sprintf("id:%d,x:%d,y:%d,w:%d,h:%d", bin.id, bin.x, bin.y, bin.w, bin.h)
}

// Shelf 结构
type Shelf struct {
	x     int
	y     int
	w     int
	h     int
	wfree int
	bins  []*Bin
}

// NewShelf 初始化
func NewShelf(y1, w1, h1 int) *Shelf {
	s := &Shelf{
		x:     0,
		y:     y1,
		w:     w1,
		h:     h1,
		wfree: w1,
	}
	return s
}

func (s *Shelf) alloc(id, w1, h1 int) *Bin {

	if s == nil {
		return nil
	}

	if w1 > s.wfree || h1 > s.h {
		return nil
	}

	x1 := s.x
	s.x += w1
	s.wfree -= w1
	b := NewBin(id, w1, h1, w1, s.h, x1, s.y)
	s.bins = append(s.bins, b)
	return b
}

func (s *Shelf) resize(w1 int) bool {
	s.wfree += (w1 - s.w)
	s.w = w1
	return true
}

// ShelfPack 结构
type ShelfPack struct {
	maxid      int
	width      int
	height     int
	autoResize bool
	shelves    []*Shelf
	freebins   []*Bin
	usedbins   map[int]*Bin
	stats      map[int]int
}

//ShelfPackOptions 选项
type ShelfPackOptions struct {
	autoResize bool
}

//PackOptions  选项
type PackOptions struct {
	inPlace bool
}

// NewShelfPack 初始化
func NewShelfPack(w, h int, spo ShelfPackOptions) *ShelfPack {
	sp := &ShelfPack{
		autoResize: spo.autoResize,
		maxid:      0,
		usedbins:   make(map[int]*Bin),
		stats:      make(map[int]int),
	}
	if w > 0 {
		sp.width = w
	} else {
		sp.width = 64
	}
	if h > 0 {
		sp.height = w
	} else {
		sp.height = 64
	}
	return sp
}

//Pack 打包
func (sp *ShelfPack) Pack(bins []*Bin, opt PackOptions) []*Bin {
	var out []*Bin

	for i := range bins {

		if bins[i].w > 0 && bins[i].h > 0 {
			b := sp.PackOne(bins[i].id, bins[i].w, bins[i].h)
			if b == nil {
				continue
			}
			if opt.inPlace {
				bins[i].id = b.id
				bins[i].x = b.x
				bins[i].y = b.y
			}
			out = append(out, b)
		}

	}
	sp.shrink()

	return out
}

//PackOne 打包
func (sp *ShelfPack) PackOne(id, w, h int) *Bin {
	y := 0
	waste := 0
	var best struct {
		pshelf   *Shelf
		pfreebin *Bin
		waste    int
	}
	best.waste = 1<<31 - 1
	// if id was supplied, attempt a lookup..
	if id != -1 {
		bin, ok := sp.usedbins[id]
		if ok {
			sp.ref(bin)
			return bin
		}
		if id > sp.maxid {
			sp.maxid = id
		}
	} else {
		sp.maxid++
		id = sp.maxid
	}

	// First try to reuse a free bin..

	for i := range sp.freebins {
		// exactly the right height and width, use it..
		if h == sp.freebins[i].maxh && w == sp.freebins[i].maxw {
			return sp.allocFreebin(sp.freebins[i], id, w, h)
		}
		// not enough height or width, skip it..
		if h > sp.freebins[i].maxh || w > sp.freebins[i].maxw {
			continue
		}
		// extra height or width, minimize wasted area..
		if h <= sp.freebins[i].maxh && w <= sp.freebins[i].maxw {
			waste = (sp.freebins[i].maxw * sp.freebins[i].maxh) - (w * h)
			if waste < best.waste {
				best.waste = waste
				best.pfreebin = sp.freebins[i]
			}
		}
	}

	// Next find the best shelf
	for i := range sp.shelves {

		y += sp.shelves[i].h

		// not enough width on this shelf, skip it..
		if w > sp.shelves[i].wfree {
			continue
		}
		// exactly the right height, pack it..
		if h == sp.shelves[i].h {
			return sp.allocShelf(sp.shelves[i], id, w, h)
		}
		// not enough height, skip it..
		if h > sp.shelves[i].h {
			continue
		}
		// extra height, minimize wasted area..
		if h < sp.shelves[i].h {
			waste = (sp.shelves[i].h - h) * w
			if waste < best.waste {
				best.waste = waste
				best.pshelf = sp.shelves[i]
			}
		}
	}

	if best.pfreebin != nil {
		return sp.allocFreebin(best.pfreebin, id, w, h)
	}

	if best.pshelf != nil {
		return sp.allocShelf(best.pshelf, id, w, h)
	}

	// No free bins or shelves.. add shelf..
	if h <= (sp.height-y) && w <= sp.width {
		shelf := NewShelf(y, sp.width, h)
		sp.shelves = append(sp.shelves, shelf)
		return sp.allocShelf(shelf, id, w, h)
	}

	// No room for more shelves..
	// If `autoResize` option is set, grow the sprite as follows:
	//  * double whichever sprite dimension is smaller (`w1` or `h1`)
	//  * if sprite dimensions are equal, grow width before height
	//  * accomodate very large bin requests (big `w` or `h`)
	if sp.autoResize {
		h1 := sp.height
		h2 := sp.height
		w1 := sp.width
		w2 := sp.width

		if w1 <= h1 || w > w1 { // grow width..
			if w > w1 {
				w2 = w * 2
			} else {
				w2 = w1 * 2
			}
		}
		if h1 < w1 || h > h1 { // grow height..
			if h > h1 {
				h2 = h * 2
			} else {
				h2 = h1 * 2
			}
		}

		sp.resize(w2, h2)
		return sp.PackOne(id, w, h) // retry
	}

	return nil
}

func (sp *ShelfPack) shrink() {
	if len(sp.shelves) > 0 {
		w2 := 0
		h2 := 0
		for _, shelf := range sp.shelves {
			h2 += shelf.h
			w := shelf.w - shelf.wfree
			if w > w2 {
				w2 = w
			}
		}
		sp.resize(w2, h2)
	}
}

func (sp *ShelfPack) getBin(id int) *Bin {
	bin, ok := sp.usedbins[id]
	if ok {
		return bin
	}
	return nil
}

func (sp *ShelfPack) ref(bin *Bin) int {
	if bin == nil {
		return 0
	}
	if bin.refcount == 0 {
		sp.stats[bin.h] = (sp.stats[bin.h] | 0) + 1
	}
	bin.refcount++
	return bin.refcount
}

func (sp *ShelfPack) unref(bin *Bin) int {
	if bin == nil {
		return 0
	}
	if bin.refcount == 0 {
		return 0
	}
	if bin.refcount == 1 {
		sp.stats[bin.h]--
		delete(sp.usedbins, bin.id)
		sp.freebins = append(sp.freebins, bin)
	}
	bin.refcount--
	return bin.refcount
}

func (sp *ShelfPack) clear() {
	sp.shelves = sp.shelves[:0]
	sp.freebins = sp.freebins[:0]
	for k := range sp.usedbins {
		delete(sp.usedbins, k)
	}
	for k := range sp.stats {
		delete(sp.stats, k)
	}
	sp.maxid = 0
}
func (sp *ShelfPack) resize(w, h int) bool {
	sp.width = w
	sp.height = h

	for i := range sp.shelves {
		sp.shelves[i].resize(sp.width)
	}
	return true
}

func (sp *ShelfPack) allocFreebin(bin *Bin, id, w, h int) *Bin {

	for i, b := range sp.freebins {
		if b.id == bin.id {
			pos := i + 1
			for ; pos < len(sp.freebins); pos++ {
				if sp.freebins[pos].id != bin.id {
					break
				}
			}
			if pos == len(sp.freebins) {
				sp.freebins = sp.freebins[:i]
				break
			} else {
				sp.freebins = append(sp.freebins[:i], sp.freebins[pos:]...)
				if pos >= len(sp.freebins) {
					break
				}
			}
		}
	}

	bin.id = id
	bin.w = w
	bin.h = h
	bin.refcount = 0
	sp.usedbins[id] = bin
	sp.ref(bin)

	return bin
}

func (sp *ShelfPack) allocShelf(shelf *Shelf, id, w, h int) *Bin {

	bin := shelf.alloc(id, w, h)
	if bin != nil {
		sp.usedbins[id] = bin
		sp.ref(bin)
	}
	return bin
}

func svg2png(svgfile string, scale float32) ([]byte, error) {
	var params []string
	if scale > 0 && scale != 1.0 {
		s := fmt.Sprintf("%f", scale)
		params = append(params, []string{"-z", s}...)
	}
	absPath, err := filepath.Abs(svgfile)
	if err != nil {
		return nil, err
	}
	params = append(params, absPath)
	if runtime.GOOS == "windows" {
		decoder := mahonia.NewDecoder("gbk")
		gbk := strings.Join(params, ",")
		gbk = decoder.ConvertString(gbk)
		params = strings.Split(gbk, ",")
	}
	cmd := exec.Command("rsvg-convert", params...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	err = cmd.Start()
	if err != nil {
		return nil, fmt.Errorf("rsvg-convert start failed, details: '%s'", err)
	}
	err = cmd.Wait()
	if err != nil {
		return nil, fmt.Errorf("rsvg-convert run failed, details: %s", err)
	}
	// err = ioutil.WriteFile("output.png", stdout.Bytes(), os.ModePerm)
	// if err != nil {
	// 	fmt.Printf("svg2png() write tmp file failed,details: %s\n", err)
	// }
	return stdout.Bytes(), nil
}

//Symbol 符号结构
type Symbol struct {
	ID      int         `json:"-" gorm:"primary_key"`
	Name    string      `json:"-" gorm:"unique;not null;unique_index"`
	Width   int         `json:"width"`
	Height  int         `json:"height"`
	X       int         `json:"x"`
	Y       int         `json:"y"`
	Scale   float32     `json:"pixelRatio"`
	Visible bool        `json:"visible"`
	Data    []byte      `json:"-"`
	Image   image.Image `json:"-" gorm:"-"`
}

// ReadIcons 加载Icons为Symbol结构.
func ReadIcons(dir string, scale float32) []*Symbol {
	//遍历目录下所有styles
	var symbols []*Symbol
	items, err := ioutil.ReadDir(dir)
	if err != nil {
		log.Error(err)
	}
	id := 0
	for _, item := range items {
		if item.IsDir() {
			continue
		}
		name := item.Name()
		ext := filepath.Ext(name)
		base := strings.TrimSuffix(name, ext)
		pathfile := filepath.Join(dir, name)
		lowext := strings.ToLower(ext)
		w, h := 0, 0
		switch lowext {
		case ".svg":
			buf, err := svg2png(pathfile, scale)
			if err != nil {
				log.Error(err)
				continue
			}
			b := bytes.NewBuffer(buf)
			img, _, err := image.Decode(b)
			if err != nil {
				log.Error(err)
				continue
			}
			rect := img.Bounds()
			w = rect.Dx()
			h = rect.Dy()
			// var out bytes.Buffer
			// err = png.Encode(&out, img)
			// if err != nil {
			// 	log.Error(err)
			// 	continue
			// }
			id++
			symbol := &Symbol{
				ID:      id,
				Name:    base,
				Width:   w,
				Height:  h,
				Scale:   scale,
				Visible: true,
				// Data:   buf.Bytes(),
				Image: img,
			}

			symbols = append(symbols, symbol)
			log.Printf("id:%d,name:%s,w:%d,h:%d\n", symbol.ID, symbol.Name, symbol.Width, symbol.Height)

		case ".png", ".jpg", ".jpeg", ".bmp", ".gif":
			file, err := os.Open(pathfile)
			if err != nil {
				log.Error(err)
				continue
			}

			img, _, err := image.Decode(file)
			if err != nil {
				log.Error(err)
				continue
			}
			rect := img.Bounds()
			w = rect.Dx()
			h = rect.Dy()
			if scale != 1.0 {
				if scale > 0 && scale < 1.2 {
					w = int(float32(w) * scale)
					h = int(float32(h) * scale)
				}
				img = resize.Resize(uint(w), uint(h), img, resize.Lanczos3)
			}
			// var buf bytes.Buffer
			// err = png.Encode(&buf, img)
			// if err != nil {
			// 	log.Error(err)
			// 	continue
			// }
			id++
			symbol := &Symbol{
				ID:      id,
				Name:    base,
				Width:   w,
				Height:  h,
				Visible: true,
				// Data:   buf.Bytes(),
				Image: img,
			}
			symbols = append(symbols, symbol)
			log.Printf("id:%d,name:%s,w:%d,h:%d\n", symbol.ID, symbol.Name, symbol.Width, symbol.Height)
		default:
			log.Printf("unkown file format: %s", name)
		}
	}

	return symbols
}

// GenIconsFromSprite 加载样式.
func GenIconsFromSprite(dir string) error {
	iconsDir := filepath.Join(dir, "icons")
	_, err := os.Stat(iconsDir)
	if err != nil {
		if os.IsNotExist(err) {
			err = os.Mkdir(iconsDir, os.ModePerm)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	}
	items, err := ioutil.ReadDir(iconsDir)
	if err != nil {
		return err
	}
	if len(items) > 0 {
		return fmt.Errorf("icons dir not empty")
	}

	//find max size sprites
	tset := make(map[string]string)
	items, err = ioutil.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.IsDir() {
			continue
		}
		name := item.Name()
		lname := strings.ToLower(name)
		switch lname {
		case "sprite.json":
			tset[lname] = name
		case "sprite.png":
			tset[lname] = name
		case "sprite@2x.json":
			tset[lname] = name
		case "sprite@2x.png":
			tset[lname] = name
		}
	}
	err = fmt.Errorf("no usebale sprites")
	var pngname string
	jsname, ok := tset["sprite@2x.json"]
	if ok {
		pngname, ok = tset["sprite@2x.png"]
		if !ok {
			jsname, ok = tset["sprite.json"]
			if !ok {
				return err
			}
			pngname, ok = tset["sprite.png"]
			if !ok {
				return err
			}
		}
	} else {
		jsname, ok = tset["sprite.json"]
		if !ok {
			return err
		}
		pngname, ok = tset["sprite.png"]
		if !ok {
			return err
		}
	}
	jfile := filepath.Join(dir, jsname)
	jbuf, err := ioutil.ReadFile(jfile)
	if err != nil {
		return err
	}

	jsmap := make(map[string]Symbol)
	err = json.Unmarshal(jbuf, &jsmap)
	if err != nil {
		return err
	}

	pfile := filepath.Join(dir, pngname)
	pbuf, err := os.Open(pfile)
	if err != nil {
		return err
	}
	sprites, _, err := image.Decode(pbuf)
	if err != nil {
		return err
	}
	for k, symbol := range jsmap {
		sp := image.Point{symbol.X, symbol.Y}
		img := image.NewRGBA(image.Rect(0, 0, symbol.Width, symbol.Height))
		draw.Draw(img, img.Bounds(), sprites, sp, draw.Src)
		x := resize.Resize(uint(float32(symbol.Width)*2), uint(float32(symbol.Height)*2), img, resize.Lanczos3)
		var buf bytes.Buffer
		err = png.Encode(&buf, x)
		if err != nil {
			log.Error(err)
			continue
		}
		err = ioutil.WriteFile(filepath.Join(iconsDir, k+".png"), buf.Bytes(), os.ModePerm)
		if err != nil {
			fmt.Printf("write png file failed,details: %s\n", err)
		}
	}
	return nil
}
