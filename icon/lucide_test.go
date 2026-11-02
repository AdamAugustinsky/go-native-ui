package icon

import (
	"bytes"
	"testing"

	"gonativeui/third_party/lucide"
)

// Every name in the vendored set, aliases included, decodes and draws ink:
// the set compiled whole, and nothing in it is an element the rasteriser
// skips.
func TestEveryLucideIconDraws(t *testing.T) {
	if err := lucide.Err(); err != nil {
		t.Fatal(err)
	}
	names := lucide.Names()
	if len(names) < 1500 {
		t.Fatalf("only %d Lucide names", len(names))
	}
	var r lucideRaster
	var buf []byte
	for _, n := range names {
		ic, ok := lucide.Lookup(n)
		if !ok {
			t.Errorf("%s: listed but not found", n)
			continue
		}
		if !ic.Decode(nopVisitor{}) {
			t.Errorf("%s: malformed", n)
			continue
		}
		buf = r.draw(buf, ic, 24, 2)
		ink := 0
		for _, b := range buf {
			if b > 0x40 {
				ink++
			}
		}
		if ink < 4 { // signal-zero and wifi-zero are one dot
			t.Errorf("%s: only %d inked pixels at 24 px", n, ink)
		}
	}
	if _, ok := lucide.Lookup("no-such-icon"); ok {
		t.Error("an unknown name was found")
	}
}

type nopVisitor struct{}

func (nopVisitor) Begin(byte)        {}
func (nopVisitor) Op(byte, [6]int32) {}
func (nopVisitor) End()              {}

// Every standard icon names a Lucide icon of the set, and so does every
// system-symbol spec's counterpart: an app on any OS gets the same drawing.
func TestStandardIconsAreLucide(t *testing.T) {
	for id, sp := range Standard() {
		if sp.Lucide == "" {
			t.Errorf("standard icon %d has no Lucide name", id)
			continue
		}
		if _, ok := lucide.Lookup(sp.Lucide); !ok {
			t.Errorf("standard icon %d: no Lucide icon %q", id, sp.Lucide)
		}
		if sp.Fallback == 0 {
			t.Errorf("standard icon %d (%s) has no fallback glyph", id, sp.Lucide)
		}
	}
	for id, sp := range SystemStandard() {
		if sp.Lucide != "" || sp.Symbol == "" {
			t.Errorf("system standard icon %d: Lucide %q, Symbol %q", id, sp.Lucide, sp.Symbol)
		}
	}
}

// A Lucide icon is a whole number of pixels on a side at every scale, its
// point size times the scale: 16 px at 1x and 32 px at 2x for a 16 pt icon,
// so the texels map one to one onto the screen's pixels.
func TestLucideSidesAreWholePixels(t *testing.T) {
	ts := newText(t)
	a := New(ts, Lucide("folder"), Lucide("folder").Small(), Lucide("folder").Size(20), Lucide("folder").In(StyleBody, ScaleMedium))
	for _, c := range []struct {
		scale float32
		want  [4]float32
	}{
		{1, [4]float32{16, 14, 20, 16}},
		{2, [4]float32{32, 28, 40, 32}},
		{1.5, [4]float32{24, 21, 30, 24}},
	} {
		a.Raster(c.scale)
		for i, want := range c.want {
			id := NumStandard + ID(i)
			if a.Source(id) != SourceLucide {
				t.Fatalf("%vx icon %d: source %v", c.scale, i, a.Source(id))
			}
			if w, h := a.Size(id); w != want || h != want {
				t.Errorf("%vx icon %d: %v×%v, want %v×%v", c.scale, i, w, h, want, want)
			}
		}
	}
}

// Drawn through the atlas, a Lucide icon's pixels on screen are its texels:
// at 1x and 2x the image is placed on whole pixels and not resampled, so it
// is exactly as crisp as the coverage the rasteriser made (which is what a
// browser draws for the same SVG at that size).
func TestLucideIconsAreCrisp(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		cell := 40 * scale
		g := newRig(t, 4*int(cell), int(cell))
		a := New(g.ts, Lucide("minus"), Lucide("search"), Lucide("folder"), Lucide("square"))
		a.Raster(scale)
		a.Attach(g.r)
		ids := []ID{NumStandard, NumStandard + 1, NumStandard + 2, NumStandard + 3}
		s := &sheet{a: a, ids: ids, color: 0x000000FF, cell: cell, cols: 4}
		g.frame(s, scale)
		pix := g.pixels()
		for i, id := range ids {
			e := a.ents[id]
			// the sheet centres the image on whole pixels
			x0 := int(float32(i)*cell + cell/2 - e.w/2 + 0.5)
			y0 := int(cell/2 - e.h/2 + 0.5)
			worst := 0
			for y := 0; y < e.ih; y++ {
				for x := 0; x < e.iw; x++ {
					want := 255 - int(a.pix[(e.y+y)*a.w+e.x+x])
					got := int(pix[((y0+y)*g.w+x0+x)*4])
					worst = max(worst, abs(got-want))
				}
			}
			if worst > 8 {
				t.Errorf("%vx %s: a pixel differs from its texel by %d", scale, a.specs[id].Lucide, worst)
			}
		}
		// minus is a horizontal stroke: at 2x its middle rows are solid ink
		if scale == 2 {
			e := a.ents[ids[0]]
			solid := 0
			for y := 0; y < e.ih; y++ {
				if a.pix[(e.y+y)*a.w+e.x+e.iw/2] == 0xFF {
					solid++
				}
			}
			if solid < 2 {
				t.Errorf("2x minus: %d solid rows, want 2 or more", solid)
			}
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// Many Lucide icons at a large size do not fit the first atlas: it grows,
// and each icon keeps texels of its own that are its own raster.
func TestManyLucideIconsGrowTheAtlas(t *testing.T) {
	ts := newText(t)
	names := lucide.Names()
	var specs []Spec
	for i := 0; i < len(names) && len(specs) < 200; i += 9 {
		specs = append(specs, Lucide(names[i]).Size(32))
	}
	a := newAtlas(ts, specs)
	a.Raster(2)
	w0, h0 := a.w, a.h
	for id := range specs {
		a.Size(ID(id))
	}
	if a.w*a.h <= w0*h0 {
		t.Fatalf("the atlas did not grow: %d×%d", a.w, a.h)
	}
	var r lucideRaster
	var own []byte
	for id, sp := range specs {
		e := a.ents[id]
		if e.src != SourceLucide || e.iw != 64 || e.ih != 64 {
			t.Errorf("%s: source %v, %d×%d", sp.Lucide, e.src, e.iw, e.ih)
			continue
		}
		ic, _ := lucide.Lookup(sp.Lucide)
		own = r.draw(own, ic, 64, 2)
		for y := 0; y < 64; y++ {
			if !bytes.Equal(a.pix[(e.y+y)*a.w+e.x:(e.y+y)*a.w+e.x+64], own[y*64:(y+1)*64]) {
				t.Errorf("%s: texels differ from its raster at row %d", sp.Lucide, y)
				break
			}
		}
		for j := id + 1; j < len(specs); j++ {
			q := a.ents[j]
			if e.x < q.x+q.iw && q.x < e.x+e.iw && e.y < q.y+q.ih && q.y < e.y+e.ih {
				t.Errorf("icons %d and %d overlap", id, j)
			}
		}
	}
}

// A Lucide name the set lacks falls back as a symbol does: to the SF Symbol,
// the Segoe glyph or the fallback rune.
func TestMissingLucideIconFallsBack(t *testing.T) {
	ts := newText(t)
	a := New(ts, Spec{Lucide: "no-such-icon", Fallback: 'x', Pt: 12})
	a.Raster(2)
	if got := a.Source(NumStandard); got != SourceFallback {
		t.Errorf("source %v, want the fallback glyph", got)
	}
}

// The weight thickens or thins the stroke around Lucide's 2 units, within
// 1 to 3.
func TestLucideStroke(t *testing.T) {
	for _, c := range []struct{ weight, want float32 }{{0, 2}, {0.23, 2.2875}, {0.4, 2.5}, {-0.4, 1.5}, {2, 3}, {-2, 1}} {
		if got := float32(lucideStroke(c.weight)); got != c.want {
			t.Errorf("lucideStroke(%v) = %v, want %v", c.weight, got, c.want)
		}
	}
}

// Rasterising Lucide icons for the first time, at any size, allocates nothing
// once the raster's buffers have grown to the largest.
func TestFirstUseOfLucideAllocatesNothing(t *testing.T) {
	ts := newText(t)
	names := lucide.Names()
	var specs []Spec
	for i := 0; i < 40; i++ {
		specs = append(specs, Lucide(names[i*37%len(names)]).Size(float32(12+i%8)))
	}
	a := New(ts, specs...)
	a.Raster(2)
	use := func() {
		a.Raster(2)
		for id := ID(0); id < ID(a.Len()); id++ {
			a.Size(id)
		}
	}
	use()
	if n := testing.AllocsPerRun(5, use); n != 0 {
		t.Errorf("preparing %d icons for the first time allocates %v times", a.Len(), n)
	}
}
