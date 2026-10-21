package gpu

import (
	"math"
	"testing"
)

// fill rasterises p into a fresh mask of its size.
func fillOf(p *Path) (mask []byte, w, h int) {
	w, h = p.size()
	mask = make([]byte, w*h)
	var r pathRaster
	r.fill(p, mask, w, h)
	return mask, w, h
}

// coverage is the sum of the mask's coverage, in pixels.
func coverage(mask []byte) float64 {
	s := 0.0
	for _, v := range mask {
		s += float64(v) / 255
	}
	return s
}

// A rectangle on whole pixels is filled exactly: full inside, nothing
// outside; on half pixels its edge columns are half covered.
func TestPathRectangle(t *testing.T) {
	var p Path
	p.MoveTo(2, 1)
	p.LineTo(8, 1)
	p.LineTo(8, 5)
	p.LineTo(2, 5) // closed when filled
	m, w, h := fillOf(&p)
	if w != 8 || h != 5 {
		t.Fatalf("mask %d×%d, want 8×5", w, h)
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			want := byte(0)
			if x >= 2 && y >= 1 {
				want = 255
			}
			if m[y*w+x] != want {
				t.Fatalf("(%d, %d) = %d, want %d", x, y, m[y*w+x], want)
			}
		}
	}

	p.Reset()
	p.MoveTo(1.5, 0)
	p.LineTo(4, 0)
	p.LineTo(4, 2)
	p.LineTo(1.5, 2)
	p.Close()
	m, w, _ = fillOf(&p)
	if v := m[1]; v < 126 || v > 129 {
		t.Errorf("a half-covered pixel is %d, want ~128", v)
	}
	if m[2] != 255 || m[0] != 0 || m[w+1] != m[1] {
		t.Errorf("row 0 %v, row 1 %v", m[:w], m[w:2*w])
	}
}

// Slanted and curved edges cover what their area says, both ways round,
// and a subpath wound the other way inside another cuts a hole (non-zero).
func TestPathArea(t *testing.T) {
	var p Path
	p.MoveTo(0, 0)
	p.LineTo(20, 0)
	p.LineTo(0, 10) // a right triangle of area 100
	m, _, _ := fillOf(&p)
	if a := coverage(m); math.Abs(a-100) > 0.5 {
		t.Errorf("triangle covers %.2f px, want 100", a)
	}
	p.Reset()
	p.MoveTo(0, 0)
	p.LineTo(0, 10)
	p.LineTo(20, 0) // the same, wound the other way
	m, _, _ = fillOf(&p)
	if a := coverage(m); math.Abs(a-100) > 0.5 {
		t.Errorf("reversed triangle covers %.2f px, want 100", a)
	}

	// a circle of radius 20 from four cubics (k = 0.5523)
	p.Reset()
	circle(&p, 22, 22, 20, false)
	m, _, _ = fillOf(&p)
	if a, want := coverage(m), math.Pi*400; math.Abs(a-want) > want*0.005 {
		t.Errorf("circle covers %.1f px, want %.1f", a, want)
	}
	// with a circle of radius 10 inside it the other way round: a ring
	circle(&p, 22, 22, 10, true)
	m, w, _ := fillOf(&p)
	if a, want := coverage(m), math.Pi*300; math.Abs(a-want) > want*0.01 {
		t.Errorf("ring covers %.1f px, want %.1f", a, want)
	}
	if m[22*w+22] != 0 {
		t.Errorf("the hole's middle is %d", m[22*w+22])
	}

	// a quadratic curve: the area under y = x² / 10 from 0 to 10 is a third
	// of the 10 × 10 box, so the shape above it covers two thirds
	p.Reset()
	p.MoveTo(0, 0)
	p.QuadTo(5, 0, 10, 10)
	p.LineTo(0, 10)
	p.Close()
	m, _, _ = fillOf(&p)
	if a := coverage(m); math.Abs(a-200.0/3) > 0.5 {
		t.Errorf("parabola covers %.2f px, want %.2f", a, 200.0/3)
	}
}

func circle(p *Path, cx, cy, r float32, ccw bool) {
	k := 0.5523 * r
	if !ccw {
		p.MoveTo(cx+r, cy)
		p.CubeTo(cx+r, cy+k, cx+k, cy+r, cx, cy+r)
		p.CubeTo(cx-k, cy+r, cx-r, cy+k, cx-r, cy)
		p.CubeTo(cx-r, cy-k, cx-k, cy-r, cx, cy-r)
		p.CubeTo(cx+k, cy-r, cx+r, cy-k, cx+r, cy)
	} else {
		p.MoveTo(cx+r, cy)
		p.CubeTo(cx+r, cy-k, cx+k, cy-r, cx, cy-r)
		p.CubeTo(cx-k, cy-r, cx-r, cy-k, cx-r, cy)
		p.CubeTo(cx-r, cy+k, cx-k, cy+r, cx, cy+r)
		p.CubeTo(cx+k, cy+r, cx+r, cy+k, cx+r, cy)
	}
	p.Close()
}

// fakeUp counts the textures a PathMask makes, uploads and frees.
type fakeUp struct{ made, uploads, freed int }

func (f *fakeUp) NewTexture(TextureKind, int, int) TextureID { f.made++; return TextureID(f.made) }
func (f *fakeUp) Upload(TextureID, int, int, int, int, []byte, int) {
	f.uploads++
}
func (f *fakeUp) FreeTexture(TextureID) { f.freed++ }

// A mask is made once for a path and kept while the path stays the same,
// which allocates nothing; a changed path gets a new texture and the old one
// is freed.
func TestPathMaskKeepsItsTexture(t *testing.T) {
	var p Path
	var m PathMask
	u := &fakeUp{}
	build := func(x float32) {
		p.Reset()
		p.MoveTo(0, 0)
		p.LineTo(x, 0)
		p.QuadTo(x, 10, 0, 12)
		p.Close()
	}
	build(10)
	q := m.Quad(u, &p, 5, 7, 0xFF0000FF, [4]float32{})
	if q.Mode != ModeMaskR8 || q.Tex == 0 || q.Rect != [4]float32{5, 7, 15, 19} || q.UV != [4]float32{0, 0, 10, 12} {
		t.Fatalf("quad %+v", q)
	}
	if n := testing.AllocsPerRun(100, func() { build(10); m.Quad(u, &p, 5, 7, 0xFF0000FF, [4]float32{}) }); n != 0 {
		t.Errorf("an unchanged path allocates %v times", n)
	}
	if u.made != 1 || u.uploads != 1 {
		t.Errorf("an unchanged path made %d textures, %d uploads", u.made, u.uploads)
	}
	build(11)
	m.Mask(u, &p)
	if u.made != 2 || u.freed != 1 {
		t.Errorf("a changed path: %d made, %d freed", u.made, u.freed)
	}
	m.Release()
	if u.freed != 2 {
		t.Errorf("Release freed %d", u.freed)
	}
	p.Reset()
	if tex, _, _ := m.Mask(u, &p); tex != 0 {
		t.Error("an empty path has a texture")
	}
}
