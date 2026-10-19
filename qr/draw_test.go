package qr

import (
	"math"
	"strings"
	"testing"

	"gonativeui/gpu"
	"gonativeui/internal/fakeui"
	"gonativeui/ui"
)

const (
	testFG = 0x000000FF
	testBG = 0xFFFFFFFF
)

// drawRig runs frames of Draw in a window of w×h pixels.
type drawRig struct {
	c    *ui.Context
	in   ui.Input
	list gpu.DrawList
	w, h int
}

func newDrawRig(w, h int) *drawRig {
	r := &drawRig{c: ui.New(fakeui.New()), w: w, h: h}
	r.c.Reserve(1 << 14)
	r.in.Inside, r.in.Focused = true, true
	r.in.MouseX, r.in.MouseY = -100, -100
	r.list.Quads = make([]gpu.Quad, 0, 1<<14)
	return r
}

// frame builds one frame with the code drawn into a size-pixel square.
func (r *drawRig) frame(m *Code, size float32) {
	r.c.Begin(&r.in, r.w, r.h, 1, 1.0/60)
	Draw(r.c, ui.HashString(0, "qr"), m, size, testFG, testBG)
	r.c.End(&r.list)
	r.in.ClearFrame()
}

// raster paints the solid quads of the list into a w×h bitmap, true where the
// colour is the foreground, and fails the test for a quad that is not a plain
// opaque rectangle on pixel boundaries (that would draw blurred edges).
func raster(t testing.TB, list *gpu.DrawList, w, h int) (dark []bool, bg []bool) {
	t.Helper()
	dark, bg = make([]bool, w*h), make([]bool, w*h)
	for i := range list.Quads {
		q := &list.Quads[i]
		for _, v := range q.Rect {
			if v != float32(math.Trunc(float64(v))) {
				t.Fatalf("quad %d has a rectangle off the pixel grid: %v", i, q.Rect)
			}
		}
		if q.Radii != [4]float32{} || q.Border != 0 || q.Soft != 0 || q.Mode != gpu.ModeShape {
			t.Fatalf("quad %d is not a plain rectangle: %+v", i, q)
		}
		col := q.Color[0]
		for _, c := range q.Color {
			if c != col {
				t.Fatalf("quad %d is not a flat colour", i)
			}
		}
		for y := int(q.Rect[1]); y < int(q.Rect[3]); y++ {
			for x := int(q.Rect[0]); x < int(q.Rect[2]); x++ {
				if x < 0 || y < 0 || x >= w || y >= h {
					t.Fatalf("quad %d leaves the window: %v", i, q.Rect)
				}
				dark[y*w+x], bg[y*w+x] = col == testFG, col == testBG
			}
		}
	}
	return dark, bg
}

func encodeString(t testing.TB, s string, l Level) *Code {
	t.Helper()
	c := new(Code)
	if err := Encode(c, []byte(s), l); err != nil {
		t.Fatal(err)
	}
	return c
}

// When the square is exactly a whole number of pixels per module, the picture
// is the symbol with the quiet zone, each module scale×scale pixels, with no
// pixel left uncovered: checked for 1× and 2× windows and several versions.
func TestDrawIsThePixelPerfectSymbol(t *testing.T) {
	for _, payload := range []string{"hi", strings.Repeat("https://example.com/", 5), strings.Repeat("x", 600)} {
		m := encodeString(t, payload, LevelM)
		n := m.Size() + 2*QuietZone
		for _, scale := range []int{1, 2, 3} {
			side := n * scale
			r := newDrawRig(side+10, side+10)
			r.frame(m, float32(side))
			dark, bg := raster(t, &r.list, r.w, r.h)
			for y := 0; y < side; y++ {
				for x := 0; x < side; x++ {
					want := m.Dark(x/scale-QuietZone, y/scale-QuietZone)
					if dark[y*r.w+x] != want || bg[y*r.w+x] == want {
						t.Fatalf("v%d scale %d: pixel (%d,%d) dark=%v bg=%v, module is dark=%v", m.Version(), scale, x, y, dark[y*r.w+x], bg[y*r.w+x], want)
					}
				}
			}
			for x := side; x < r.w; x++ { // nothing outside the square
				if dark[x] || bg[x] {
					t.Fatalf("v%d scale %d: pixel (%d,0) outside the square is painted", m.Version(), scale, x)
				}
			}
		}
	}
}

// A square that is not a multiple of the symbol still gets whole-pixel
// modules, the quiet zone at least four modules wide and the symbol centred.
func TestDrawFitsAnySquare(t *testing.T) {
	m := encodeString(t, strings.Repeat("https://example.com/", 5), LevelQ)
	n := m.Size() + 2*QuietZone
	for _, side := range []int{n, n + 1, n*2 - 1, n*2 + n/2, 300, 301, 517} {
		r := newDrawRig(side+4, side+4)
		r.frame(m, float32(side))
		dark, bg := raster(t, &r.list, r.w, r.h)
		mod := side / n
		if mod < 1 {
			t.Fatalf("test side %d too small", side)
		}
		// Find the dark bounding box: the symbol's finders touch every edge.
		x0, y0, x1, y1 := side, side, -1, -1
		for y := 0; y < side; y++ {
			for x := 0; x < side; x++ {
				if dark[y*r.w+x] {
					x0, y0, x1, y1 = min(x0, x), min(y0, y), max(x1, x+1), max(y1, y+1)
				}
			}
		}
		if got := x1 - x0; got != m.Size()*mod || y1-y0 != m.Size()*mod {
			t.Errorf("side %d: symbol is %d×%d pixels, want %d (module %d)", side, got, y1-y0, m.Size()*mod, mod)
		}
		if x0 < QuietZone*mod || side-x1 < QuietZone*mod || y0 < QuietZone*mod || side-y1 < QuietZone*mod {
			t.Errorf("side %d: quiet zone %d,%d,%d,%d is under %d", side, x0, y0, side-x1, side-y1, QuietZone*mod)
		}
		if d := x0 - (side - x1); d < -1 || d > 1 {
			t.Errorf("side %d: not centred, margins %d and %d", side, x0, side-x1)
		}
		for y := 0; y < side; y++ { // every pixel of the square is painted
			for x := 0; x < side; x++ {
				if !dark[y*r.w+x] && !bg[y*r.w+x] {
					t.Fatalf("side %d: pixel (%d,%d) is not painted", side, x, y)
				}
			}
		}
		// Module (3,3) of the first finder is dark, in whole pixels.
		px, py := x0+3*mod, y0+3*mod
		if !dark[py*r.w+px] || !dark[(py+mod-1)*r.w+px+mod-1] {
			t.Errorf("side %d: the finder's centre module is not whole", side)
		}
	}
}

func TestDrawEmptyCodeIsABlankSquare(t *testing.T) {
	var m Code
	r := newDrawRig(50, 50)
	r.frame(&m, 40)
	if len(r.list.Quads) != 1 || r.list.Quads[0].Rect != [4]float32{0, 0, 40, 40} {
		t.Errorf("quads %+v", r.list.Quads)
	}
}

// Drawing the same code frame after frame allocates nothing once the box cache
// has grown, and neither does switching between two codes.
func TestDrawAllocatesNothingInSteadyState(t *testing.T) {
	a := encodeString(t, strings.Repeat("https://wa.me/message/ABCDEFGH?x=", 6), LevelM)
	b := encodeString(t, "https://example.com/pair/0123456789", LevelM)
	r := newDrawRig(600, 600)
	for i := 0; i < 4; i++ {
		r.frame(a, 520)
		r.frame(b, 400)
	}
	for _, m := range []*Code{a, b} {
		if n := testing.AllocsPerRun(20, func() { r.frame(m, 520) }); n != 0 {
			t.Errorf("v%d: a frame allocates %v times", m.Version(), n)
		}
	}
	if n := testing.AllocsPerRun(20, func() { r.frame(a, 520); r.frame(b, 400) }); n != 0 {
		t.Errorf("alternating codes allocates %v times", n)
	}
}

func TestFit(t *testing.T) {
	cases := []struct {
		n         int
		side      float32
		mod, offs float32
	}{
		{21, 29, 1, 0}, {21, 58, 2, 0}, {21, 60, 2, 1}, {21, 87.9, 3, 0}, {21, 100, 3, 6},
		{21, 10, 10.0 / 29, 0}, // under a pixel per module
	}
	for _, tc := range cases {
		mod, off := fit(tc.n, tc.side)
		if mod != tc.mod || off != tc.offs {
			t.Errorf("fit(%d, %v) = %v, %v, want %v, %v", tc.n, tc.side, mod, off, tc.mod, tc.offs)
		}
	}
}
