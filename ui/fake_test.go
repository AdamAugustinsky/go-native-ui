package ui

import (
	"testing"

	"gonativeui/gpu"
	"gonativeui/text"
)

// fakeShaper is a deterministic Shaper: every rune advances 8 px in font 0
// and 10 px in font 1, glyph ids are the rune values, and every glyph but the
// space has a 6×8 bitmap at a position derived from its id. Like the real
// run cache it stores runs in fixed slabs (direct-mapped here, runs of up to
// fakeMaxGlyphs runes), so a miss costs no allocation either; a collision
// evicts and bumps the run generation.
type fakeShaper struct {
	slots    []fakeRun
	runGen   uint32
	atlasGen uint32
	dirty    bool
	shapes   int // cache misses
	calls    int // Shape calls
	pageSize int
}

const (
	fakeSlots     = 1 << 13
	fakeMaxGlyphs = 40
)

type fakeRun struct {
	used  bool
	font  text.FontKey
	n     int
	txt   [fakeMaxGlyphs * 4]byte
	tn    int
	g     [fakeMaxGlyphs]uint16
	x     [fakeMaxGlyphs]float32
	faces [fakeMaxGlyphs]text.FaceID
	adv   float32
}

func newFake() *fakeShaper {
	return &fakeShaper{slots: make([]fakeRun, fakeSlots), dirty: true, pageSize: 1024}
}

func (f *fakeShaper) adv(font text.FontKey) float32 {
	if font == 1 {
		return 10
	}
	return 8
}

func (f *fakeShaper) Metrics(k text.FontKey) text.Metrics {
	if k == 1 {
		return text.Metrics{Ascent: 12, Descent: 4, LineHeight: 18}
	}
	return text.Metrics{Ascent: 10, Descent: 3, LineHeight: 14}
}

func (f *fakeShaper) Shape(k text.FontKey, b []byte) text.RunRef {
	f.calls++
	h := uint64(14695981039346656037) ^ uint64(k)
	for _, c := range b {
		h = (h ^ uint64(c)) * 1099511628211
	}
	i := int(h>>20) % fakeSlots
	r := &f.slots[i]
	if r.used && r.font == k && string(r.txt[:r.tn]) == string(b) {
		return text.RunRef(i + 1)
	}
	if r.used {
		f.runGen++ // eviction
	}
	f.shapes++
	r.used, r.font, r.n = true, k, 0
	r.tn = copy(r.txt[:], b)
	adv := f.adv(k)
	var x float32
	for _, ru := range string(b) {
		if r.n == fakeMaxGlyphs {
			break
		}
		r.g[r.n], r.x[r.n], r.faces[r.n] = uint16(ru), x, 0
		r.n++
		x += adv
	}
	r.adv = x
	return text.RunRef(i + 1)
}

func (f *fakeShaper) Run(r text.RunRef) text.RunView {
	s := &f.slots[r-1]
	return text.RunView{Glyphs: s.g[:s.n], X: s.x[:s.n], Faces: s.faces[:s.n], Advance: s.adv}
}
func (f *fakeShaper) RunGeneration() uint32   { return f.runGen }
func (f *fakeShaper) AtlasGeneration() uint32 { return f.atlasGen }

func (f *fakeShaper) Glyph(face text.FaceID, gid uint16, sub uint8) text.GlyphEntry {
	if gid == ' ' {
		return text.GlyphEntry{}
	}
	return text.GlyphEntry{Page: 0, X: (gid % 100) * 8, Y: (gid / 100) * 10, W: 6, H: 8, BearingX: 1, BearingY: 9}
}

func (f *fakeShaper) Pages() int { return 1 }
func (f *fakeShaper) PageInfo(p int) (int, int, bool) {
	return f.pageSize, f.pageSize, false
}
func (f *fakeShaper) Dirty(p int) (x, y, w, h int, pixels []byte, stride int, ok bool) {
	if !f.dirty {
		return
	}
	return 0, 0, 4, 4, make([]byte, 4*f.pageSize), f.pageSize, true
}
func (f *fakeShaper) ClearDirty(p int) { f.dirty = false }

type fakeUploader struct{ textures, uploads int }

func (u *fakeUploader) NewTexture(kind gpu.TextureKind, w, h int) gpu.TextureID {
	u.textures++
	return gpu.TextureID(u.textures)
}
func (u *fakeUploader) Upload(t gpu.TextureID, x, y, w, h int, pixels []byte, stride int) {
	u.uploads++
}

// harness drives frames of a Context.
type harness struct {
	t    *testing.T
	c    *Context
	sh   *fakeShaper
	in   Input
	list gpu.DrawList
	w, h int
	// scale is the scale of the frames; 0 is 1.
	scale float32
}

func newHarness(t *testing.T, w, h int) *harness {
	sh := newFake()
	hs := &harness{t: t, c: New(sh), sh: sh, w: w, h: h}
	hs.in.Inside = true
	hs.in.Focused = true
	hs.in.MouseX, hs.in.MouseY = -100, -100
	return hs
}

// frame runs one 60 Hz frame; build runs between Begin and End.
func (h *harness) frame(build func(c *Context)) {
	sc := h.scale
	if sc == 0 {
		sc = 1
	}
	h.c.Begin(&h.in, h.w, h.h, sc, 1.0/60)
	if build != nil {
		build(h.c)
	}
	h.c.End(&h.list)
	h.in.ClearFrame()
}

func (h *harness) move(x, y float32) { h.in.MouseX, h.in.MouseY = x, y }

func (h *harness) rect(id ID) [4]float32 { return h.c.Rect(id) }

func near(a, b float32) bool { return a-b < 0.01 && b-a < 0.01 }
