package main

import (
	"fmt"
	"testing"
)

func TestPack1(t *testing.T) {
	t.Log("batch pack() allocates same height bins on existing shelf")
	sprite := NewShelfPack(64, 64, ShelfPackOptions{})
	var bins []*Bin
	bins = append(bins, NewBin(-1, 10, 10, -1, -1, -1, -1))
	bins = append(bins, NewBin(-1, 10, 10, -1, -1, -1, -1))
	bins = append(bins, NewBin(-1, 10, 10, -1, -1, -1, -1))
	results := sprite.Pack(bins, PackOptions{})
	for i, unit := range []struct {
		expected string
	}{
		{"id:1,x:0,y:0,w:10,h:10"},
		{"id:2,x:10,y:0,w:10,h:10"},
		{"id:3,x:20,y:0,w:10,h:10"},
	} {
		if results[i].String() != unit.expected {
			t.Errorf("Pack: [%v], actually: [%v]", unit, results[i])
		}
	}

}
func TestPack2(t *testing.T) {
	t.Log("batch pack() allocates larger bins on new shelf")
	sprite := NewShelfPack(64, 64, ShelfPackOptions{})
	var bins []*Bin
	bins = append(bins, NewBin(-1, 10, 10, -1, -1, -1, -1))
	bins = append(bins, NewBin(-1, 10, 15, -1, -1, -1, -1))
	bins = append(bins, NewBin(-1, 10, 20, -1, -1, -1, -1))
	results := sprite.Pack(bins, PackOptions{})
	for i, unit := range []struct {
		expected string
	}{
		{"id:1,x:0,y:0,w:10,h:10"},
		{"id:2,x:0,y:10,w:10,h:15"},
		{"id:3,x:0,y:25,w:10,h:20"},
	} {
		if results[i].String() != unit.expected {
			t.Errorf("Pack: [%v], actually: [%v]", unit, results[i])
		}
	}

}
func TestPack3(t *testing.T) {
	t.Log("batch pack() allocates shorter bins on existing shelf, minimizing waste")
	sprite := NewShelfPack(64, 64, ShelfPackOptions{})
	var bins []*Bin
	bins = append(bins, NewBin(-1, 10, 10, -1, -1, -1, -1))
	bins = append(bins, NewBin(-1, 10, 15, -1, -1, -1, -1))
	bins = append(bins, NewBin(-1, 10, 20, -1, -1, -1, -1))
	bins = append(bins, NewBin(-1, 10, 9, -1, -1, -1, -1))
	results := sprite.Pack(bins, PackOptions{})
	for i, unit := range []struct {
		expected string
	}{
		{"id:1,x:0,y:0,w:10,h:10"},
		{"id:2,x:0,y:10,w:10,h:15"},
		{"id:3,x:0,y:25,w:10,h:20"},
		{"id:4,x:10,y:0,w:10,h:9"},
	} {
		if results[i].String() != unit.expected {
			t.Errorf("Pack: [%v], actually: [%v]", unit, results[i])
		}
	}

}
func TestPack4(t *testing.T) {
	t.Log("batch pack() sets `id`, `x`, `y` properties on bins with `inPlace` option")
	sprite := NewShelfPack(64, 64, ShelfPackOptions{})
	var bins []*Bin
	bins = append(bins, NewBin(-1, 10, 10, -1, -1, -1, -1))
	bins = append(bins, NewBin(-1, 10, 10, -1, -1, -1, -1))
	bins = append(bins, NewBin(-1, 10, 10, -1, -1, -1, -1))
	results := sprite.Pack(bins, PackOptions{inPlace: true})
	for i, unit := range []struct {
		expected string
	}{
		{"id:1,x:0,y:0,w:10,h:10"},
		{"id:2,x:10,y:0,w:10,h:10"},
		{"id:3,x:20,y:0,w:10,h:10"},
	} {
		if results[i].String() != unit.expected {
			t.Errorf("Pack: [%v], actually: [%v]", unit, results[i])
		}
	}

}
func TestPack5(t *testing.T) {
	t.Log("packOne() allocates same height bins on existing shelf")
	sprite := NewShelfPack(20, 20, ShelfPackOptions{})
	var bins []*Bin
	bins = append(bins, NewBin(-1, 10, 10, -1, -1, -1, -1))
	bins = append(bins, NewBin(-1, 10, 10, -1, -1, -1, -1))
	bins = append(bins, NewBin(-1, 10, 30, -1, -1, -1, -1))
	bins = append(bins, NewBin(-1, 10, 10, -1, -1, -1, -1))
	results := sprite.Pack(bins, PackOptions{inPlace: true})
	for i, unit := range []struct {
		expected string
	}{
		{"id:1,x:0,y:0,w:10,h:10"},
		{"id:2,x:10,y:0,w:10,h:10"},
		{"id:4,x:0,y:10,w:10,h:10"},
	} {
		if results[i].String() != unit.expected {
			t.Errorf("Pack: [%v], actually: [%v]", unit, results[i])
		}
	}

}
func TestPack6(t *testing.T) {
	t.Log("packOne() allocates same height bins on existing shelf")
	var bins []*Bin
	bins = append(bins, NewBin(-1, 10, 10, -1, -1, -1, -1))
	bins = append(bins, NewBin(-1, 5, 15, -1, -1, -1, -1))
	bins = append(bins, NewBin(-1, 25, 15, -1, -1, -1, -1))
	bins = append(bins, NewBin(-1, 10, 20, -1, -1, -1, -1))

	sprite := NewShelfPack(10, 10, ShelfPackOptions{autoResize: true})
	results := sprite.Pack(bins, PackOptions{})
	for i, unit := range []struct {
		expected string
	}{
		{"id:1,x:0,y:0,w:10,h:10"},
		{"id:2,x:0,y:10,w:5,h:15"},
		{"id:3,x:5,y:10,w:25,h:15"},
		{"id:4,x:0,y:25,w:10,h:20"},
	} {
		if results[i].String() != unit.expected {
			t.Errorf("Pack: [%v], actually: [%v]", unit, results[i])
		}
	}

	if sprite.width != 30 {
		t.Errorf("sprite.width: [%v], actually: [%v]", 30, sprite.width)
	}
}
func TestPackone1(t *testing.T) {
	t.Log("packOne() allocates bins with numeric id")
	sprite := NewShelfPack(64, 64, ShelfPackOptions{})
	bin := sprite.PackOne(1000, 10, 10)
	expected := "id:1000,x:0,y:0,w:10,h:10"
	if bin.String() != expected {
		t.Errorf("Bin: [%v], actually: [%v]", expected, bin)
	}
}
func TestPackone2(t *testing.T) {
	t.Log("packOne() allocates bins with numeric id")
	sprite := NewShelfPack(64, 64, ShelfPackOptions{})
	bin1 := sprite.PackOne(-1, 10, 10)
	bin2 := sprite.PackOne(-1, 10, 10)
	if bin1.id != 1 {
		t.Errorf("Bin: [%v], actually: [%v]", 1, bin1.id)
	}
	if bin2.id != 2 {
		t.Errorf("Bin: [%v], actually: [%v]", 2, bin2.id)
	}
}
func TestPackone3(t *testing.T) {
	t.Log("packOne() allocates bins with numeric id")
	sprite := NewShelfPack(64, 64, ShelfPackOptions{})
	bin1 := sprite.PackOne(1, 10, 10)
	bin2 := sprite.PackOne(-1, 10, 10)
	if bin1.id != 1 {
		t.Errorf("Bin: [%v], actually: [%v]", 1, bin1.id)
	}
	if bin2.id != 2 {
		t.Errorf("Bin: [%v], actually: [%v]", 2, bin2.id)
	}
}
func TestPackone4(t *testing.T) {
	t.Log("packOne() allocates bins with numeric id")
	sprite := NewShelfPack(64, 64, ShelfPackOptions{})
	bin1 := sprite.PackOne(1000, 10, 10)
	expected := "id:1000,x:0,y:0,w:10,h:10"
	if bin1.String() != expected {
		t.Errorf("Bin: [%v], actually: [%v]", expected, bin1)
	}
	bin2 := sprite.PackOne(1000, 10, 10)
	if bin2.String() != expected {
		t.Errorf("Bin: [%v], actually: [%v]", expected, bin2)
	}

}
func TestPackone5(t *testing.T) {
	t.Log("packOne() allocates bins with numeric id")
	sprite := NewShelfPack(10, 10, ShelfPackOptions{})
	bin1 := sprite.PackOne(-1, 10, 10)
	expected := "id:1,x:0,y:0,w:10,h:10"
	if bin1.String() != expected {
		t.Errorf("Bin: [%v], actually: [%v]", expected, bin1)
	}
	bin2 := sprite.PackOne(-1, 10, 10)
	if bin2 != nil {
		t.Errorf("Bin: [nil], actually: [%v]", bin2)
	}

}
func TestPackone6(t *testing.T) {
	t.Log("packOne() considers max bin dimensions when reusing a free bin")
	sprite := NewShelfPack(64, 64, ShelfPackOptions{})
	sprite.PackOne(1, 10, 10)
	bin2 := sprite.PackOne(2, 10, 15)
	sprite.unref(bin2)
	bin3 := sprite.PackOne(3, 10, 13)
	expected := "id:3,x:0,y:10,w:10,h:13"
	if bin3.String() != expected {
		t.Errorf("Bin: [%v], actually: [%v]", expected, bin3)
	}
	//reused bin3
	if bin2 != bin3 {
		t.Errorf("Bin: [nil], actually: [%v]", bin2)
	}
	sprite.unref(bin3)
	bin4 := sprite.PackOne(4, 10, 14)
	expected = "id:4,x:0,y:10,w:10,h:14"
	if bin4.String() != expected {
		t.Errorf("Bin: [%v], actually: [%v]", expected, bin4)
	}
	//reused bin2
	if bin4 != bin2 {
		t.Errorf("Bin: [nil], actually: [%v]", bin2)
	}
	//reused bin3
	if bin4 != bin3 {
		t.Errorf("Bin: [nil], actually: [%v]", bin3)
	}
}
func TestPackone7(t *testing.T) {
	t.Log("packOne() test ref and unref")
	sprite := NewShelfPack(64, 64, ShelfPackOptions{})
	bin1 := sprite.PackOne(1, 10, 10)
	if bin1.refcount != 1 {
		t.Errorf("Bin: [%v], actually: [%v]", 1, bin1.refcount)
	}
	rc := sprite.ref(bin1)
	if rc != 2 {
		t.Errorf("Bin: [%v], actually: [%v]", 1, rc)
	}
	sprite.unref(bin1)
	if bin1.refcount != 1 {
		t.Errorf("Bin: [%v], actually: [%v]", 1, bin1.refcount)
	}
}
func TestPackone8(t *testing.T) {
	t.Log("packOne() test clear")
	sprite := NewShelfPack(10, 10, ShelfPackOptions{})
	bin1 := sprite.PackOne(-1, 10, 10)
	expected := "id:1,x:0,y:0,w:10,h:10"
	if bin1.String() != expected {
		t.Errorf("Bin: [%v], actually: [%v]", expected, bin1)
	}
	bin2 := sprite.PackOne(-1, 10, 10)
	if bin2 != nil {
		t.Errorf("Bin: [%v], actually: [%v]", nil, bin2)
	}

	sprite.clear()
	bin3 := sprite.PackOne(-1, 10, 10)
	expected = "id:1,x:0,y:0,w:10,h:10"
	if bin3.String() != expected {
		t.Errorf("Bin: [%v], actually: [%v]", expected, bin3)
	}
}
func TestPackone9(t *testing.T) {
	t.Log("packOne() test shrink")
	size := 20
	sprite := NewShelfPack(size, size, ShelfPackOptions{})
	w, h := 10, 5
	bin1 := sprite.PackOne(-1, w, h)
	expected := fmt.Sprintf("id:1,x:0,y:0,w:%d,h:%d", w, h)
	if bin1.String() != expected {
		t.Errorf("Bin: [%v], actually: [%v]", expected, bin1)
	}
	if sprite.width != size || sprite.height != size {
		t.Errorf("sprite: [%v,%v], actually: [%v,%v]", size, size, sprite.width, sprite.height)
	}
	sprite.shrink()
	if sprite.width != w || sprite.height != h {
		t.Errorf("sprite: [%v,%v], actually: [%v,%v]", w, h, sprite.width, sprite.height)
	}
}
func TestPackone10(t *testing.T) {
	t.Log("packOne() test resize")
	sprite := NewShelfPack(10, 10, ShelfPackOptions{})
	bin1 := sprite.PackOne(1, 10, 10)
	expected := "id:1,x:0,y:0,w:10,h:10"
	if bin1.String() != expected {
		t.Errorf("Bin: [%v], actually: [%v]", expected, bin1)
	}
	r := sprite.resize(20, 10)
	if r != true {
		t.Errorf("sprite:resize error")
	}
	if sprite.width != 20 || sprite.height != 10 {
		t.Errorf("sprite: [%v,%v], actually: [%v,%v]", 20, 10, sprite.width, sprite.height)
	}
	bin2 := sprite.PackOne(2, 10, 10)
	expected = "id:2,x:10,y:0,w:10,h:10"
	if bin2.String() != expected {
		t.Errorf("Bin: [%v], actually: [%v]", expected, bin2)
	}
	sprite.resize(20, 20)
	if sprite.width != 20 || sprite.height != 20 {
		t.Errorf("sprite: [%v,%v], actually: [%v,%v]", 20, 20, sprite.width, sprite.height)
	}
}
