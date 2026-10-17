package text

import (
	"fmt"
	"math/rand"
	"testing"
)

type rect struct{ x, y, w, h int }

func overlaps(a, b rect) bool {
	return a.x < b.x+b.w && b.x < a.x+a.w && a.y < b.y+b.h && b.y < a.y+a.h
}

func TestPackerNoOverlap(t *testing.T) {
	p := packer{w: 256, h: 256}
	rng := rand.New(rand.NewSource(1))
	var got []rect
	for i := 0; i < 2000; i++ {
		w, h := 4+rng.Intn(28), 4+rng.Intn(28)
		x, y, ok := p.alloc(w, h)
		if !ok {
			continue
		}
		r := rect{x, y, w, h}
		if x < 0 || y < 0 || x+w > p.w || y+h > p.h {
			t.Fatalf("rect %v outside page", r)
		}
		for _, o := range got {
			if overlaps(r, o) {
				t.Fatalf("rect %v overlaps %v", r, o)
			}
		}
		got = append(got, r)
	}
	if len(got) < 100 {
		t.Fatalf("only %d rects packed", len(got))
	}
	var used int
	for _, r := range got {
		used += r.w * r.h
	}
	t.Logf("packed %d rects, %.0f%% of the page", len(got), 100*float64(used)/float64(p.w*p.h))
	if _, _, ok := p.alloc(p.w+1, 4); ok {
		t.Fatal("oversized rect accepted")
	}
}

func TestAtlasPaddingAndReset(t *testing.T) {
	a := newAtlas()
	a.pageDim = 128
	a.maxPer = 2
	rng := rand.New(rand.NewSource(2))
	type placed struct {
		pg int
		r  rect
	}
	var cur []placed
	gen := a.gen
	resets := 0
	for i := 0; i < 600; i++ {
		w, h := 3+rng.Intn(20), 3+rng.Intn(20)
		pg, x, y, ok := a.alloc(false, w, h)
		if !ok {
			t.Fatalf("alloc %dx%d failed", w, h)
		}
		if a.gen != gen {
			gen = a.gen
			cur = cur[:0]
			resets++
		}
		r := rect{x, y, w, h}
		if x < glyphPad || y < glyphPad || x+w > a.pageDim-glyphPad || y+h > a.pageDim-glyphPad {
			t.Fatalf("rect %v lacks padding to the page edge", r)
		}
		// Padded rects must not overlap: grow both by the padding.
		pr := rect{x - glyphPad, y - glyphPad, w + 2*glyphPad, h + 2*glyphPad}
		for _, o := range cur {
			if o.pg != pg {
				continue
			}
			po := rect{o.r.x - glyphPad, o.r.y - glyphPad, o.r.w + 2*glyphPad, o.r.h + 2*glyphPad}
			if overlaps(pr, po) {
				t.Fatalf("glyph %v (padded) overlaps %v (padded)", r, o.r)
			}
		}
		cur = append(cur, placed{pg, r})
	}
	if resets == 0 {
		t.Fatal("expected at least one atlas reset with a tiny page cap")
	}
	if len(a.pages) != 2 {
		t.Fatalf("pages = %d, want the cap of 2", len(a.pages))
	}
	t.Logf("%d resets, %d pages", resets, len(a.pages))
}

func TestAtlasDirtyRegion(t *testing.T) {
	a := newAtlas()
	a.pageDim = 64
	pg, x, y, ok := a.alloc(false, 4, 3)
	if !ok {
		t.Fatal("alloc")
	}
	// A new page is dirty in full: its GPU texture starts undefined.
	if dx, dy, dw, dh, _, _, ok := a.dirtyRegion(pg); !ok || dx != 0 || dy != 0 || dw != 64 || dh != 64 {
		t.Fatalf("new page dirty = %d,%d %dx%d ok=%v, want the whole page", dx, dy, dw, dh, ok)
	}
	a.clearDirty(pg)
	src := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	a.write(pg, x, y, 4, 3, src, 4)
	dx, dy, dw, dh, px, stride, ok := a.dirtyRegion(pg)
	if !ok || dx != x || dy != y || dw != 4 || dh != 3 || stride != 64 {
		t.Fatalf("dirty = %d,%d %dx%d stride %d ok=%v", dx, dy, dw, dh, stride, ok)
	}
	for r := 0; r < 3; r++ {
		for c := 0; c < 4; c++ {
			if px[r*stride+c] != src[r*4+c] {
				t.Fatalf("pixel (%d,%d) = %d", c, r, px[r*stride+c])
			}
		}
	}
	a.clearDirty(pg)
	if _, _, _, _, _, _, ok := a.dirtyRegion(pg); ok {
		t.Fatal("still dirty after clear")
	}
	if _, _, _, _, _, _, ok := a.dirtyRegion(99); ok {
		t.Fatal("dirty on a missing page")
	}
}

func TestGlyphTable(t *testing.T) {
	var g glyphTable
	if _, ok := g.lookup(glyphKey(1, 2, 3)); ok {
		t.Fatal("hit in empty table")
	}
	for i := 0; i < 5000; i++ {
		g.insert(glyphKey(FaceID(i%7), uint16(i), uint8(i%4)), GlyphEntry{X: uint16(i), W: 1})
	}
	for i := 0; i < 5000; i++ {
		e, ok := g.lookup(glyphKey(FaceID(i%7), uint16(i), uint8(i%4)))
		if !ok || e.X != uint16(i) {
			t.Fatalf("glyph %d: %v %v", i, e, ok)
		}
	}
	g.clear()
	if _, ok := g.lookup(glyphKey(0, 0, 0)); ok {
		t.Fatal("hit after clear")
	}
}

func insertStr(c *runCache, font FontKey, s string) RunRef {
	n := len(s)
	g, x, f, cl := make([]uint16, n), make([]float32, n), make([]FaceID, n), make([]uint32, n)
	for i := range s {
		g[i], x[i], f[i], cl[i] = uint16(s[i]), float32(i), FaceID(font), uint32(i)|ClusterRTL
	}
	return c.insert(font, []byte(s), g, x, f, cl, float32(n))
}

func checkStr(t *testing.T, c *runCache, font FontKey, s string) {
	t.Helper()
	r, ok := c.lookup(font, []byte(s))
	if !ok {
		t.Fatalf("miss for %q", s)
	}
	v := c.view(r)
	if len(v.Glyphs) != len(s) || v.Advance != float32(len(s)) {
		t.Fatalf("%q: view = %+v", s, v)
	}
	for i := range s {
		if v.Glyphs[i] != uint16(s[i]) || v.X[i] != float32(i) || v.Faces[i] != FaceID(font) || v.Clusters[i] != uint32(i)|ClusterRTL {
			t.Fatalf("%q: glyph %d corrupted: %+v", s, i, v)
		}
	}
	if string(v.Text) != s {
		t.Fatalf("%q: text %q", s, v.Text)
	}
}

func TestRunCacheHitsAndFonts(t *testing.T) {
	c := newRunCache()
	insertStr(c, 1, "hello")
	insertStr(c, 2, "hello")
	checkStr(t, c, 1, "hello")
	checkStr(t, c, 2, "hello")
	if _, ok := c.lookup(3, []byte("hello")); ok {
		t.Fatal("hit for another font")
	}
	if _, ok := c.lookup(1, []byte("hell")); ok {
		t.Fatal("hit for a prefix")
	}
	r := insertStr(c, 1, "")
	if v := c.view(r); len(v.Glyphs) != 0 {
		t.Fatal("empty run has glyphs")
	}
	if v := c.view(0); v.Glyphs != nil || v.Advance != 0 {
		t.Fatal("zero RunRef is not empty")
	}
	// Views are capacity-limited so appending can't scribble on neighbours.
	v := c.view(1)
	if cap(v.Glyphs) != len(v.Glyphs) {
		t.Fatal("view capacity leaks into the slab")
	}
}

func TestRunCacheEviction(t *testing.T) {
	c := newRunCache()
	c.maxRuns = 100
	hot := []string{"hot-a", "hot-b", "hot-c"}
	for _, s := range hot {
		insertStr(c, 0, s)
	}
	gen := c.gen
	for i := 0; i < 1000; i++ {
		s := fmt.Sprintf("cold-%04d", i)
		insertStr(c, 0, s)
		checkStr(t, c, 0, s)
		if len(c.ents) > c.maxRuns {
			t.Fatalf("cache holds %d runs, cap %d", len(c.ents), c.maxRuns)
		}
		// Touch the hot set every iteration: second chance keeps it.
		for _, h := range hot {
			if _, ok := c.lookup(0, []byte(h)); !ok {
				insertStr(c, 0, h)
			}
		}
	}
	if c.gen == gen {
		t.Fatal("no eviction happened")
	}
	for _, h := range hot {
		checkStr(t, c, 0, h)
	}
	// Everything still in the table is intact after the compactions.
	for i := range c.ents {
		e := c.ents[i]
		s := string(c.text[e.textOff : e.textOff+e.textLen])
		checkStr(t, c, e.font, s)
	}
	t.Logf("generation %d, %d runs resident", c.gen, len(c.ents))
}

func TestRunCacheEvictAllWhenAllReferenced(t *testing.T) {
	c := newRunCache()
	c.maxRuns = 8
	for i := 0; i < 8; i++ {
		insertStr(c, 0, fmt.Sprintf("s%d", i)) // all inserted with ref set
	}
	insertStr(c, 0, "overflow")
	if len(c.ents) > 1+c.maxRuns/2 {
		t.Fatalf("no headroom after evicting a fully referenced cache: %d resident", len(c.ents))
	}
	checkStr(t, c, 0, "overflow")
}

// A reset page is dirty in full, so stale glyph pixels in the GPU texture get
// overwritten (they would show through the new glyphs' padding).
func TestAtlasResetDirtiesWholePage(t *testing.T) {
	a := newAtlas()
	a.pageDim = 64
	pg, x, y, ok := a.alloc(false, 4, 3)
	if !ok {
		t.Fatal("alloc")
	}
	a.write(pg, x, y, 4, 3, make([]byte, 12), 4)
	a.clearDirty(pg)
	a.reset()
	if dx, dy, dw, dh, _, _, ok := a.dirtyRegion(pg); !ok || dx != 0 || dy != 0 || dw != 64 || dh != 64 {
		t.Fatalf("after reset dirty = %d,%d %dx%d ok=%v, want the whole page", dx, dy, dw, dh, ok)
	}
}
