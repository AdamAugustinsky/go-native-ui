// Package fakeui is a deterministic text system and a texture uploader for
// tests of code built on ui: one glyph per rune, every glyph advancing half the
// font size, no ink for spaces, runs in fixed slots so that shaping allocates
// nothing once the slots are made. Tests of packages above ui (widget, the
// gallery) use it where the real text system's fonts would make them depend on
// the machine.
package fakeui

import (
	"unicode/utf8"

	"gonativeui/gpu"
	"gonativeui/text"
	"gonativeui/ui"
)

// Text is a deterministic text system for the tests: one glyph per rune,
// every glyph advancing half the font size, no ink for spaces. Runs live in
// fixed slots, so shaping allocates nothing once the slots are made.
// Text is the fake text system; it implements ui.Shaper and the text-system
// interfaces of theme, icon and widget.
type Text struct {
	descs  []text.FontDesc
	slots  []run
	runGen uint32
	dirty  bool
}

const (
	slots    = 1 << 12
	maxGlyph = 160
	maxBytes = 4 * maxGlyph
)

type run struct {
	used bool
	font text.FontKey
	tn   int
	txt  [maxBytes]byte
	n    int
	g    [maxGlyph]uint16
	x    [maxGlyph]float32
	f    [maxGlyph]text.FaceID
	cl   [maxGlyph]uint32
	adv  float32
}

// New returns a fake text system.
func New() *Text {
	return &Text{slots: make([]run, slots), dirty: true, descs: []text.FontDesc{{SizePt: 13, Scale: 1}}}
}

func (f *Text) Font(d text.FontDesc) text.FontKey {
	if d.Scale <= 0 {
		d.Scale = 1
	}
	if d.SizePt <= 0 {
		d.SizePt = 13
	}
	for i, e := range f.descs {
		if e.SizePt == d.SizePt && e.Weight == d.Weight && e.Scale == d.Scale && e.Mono == d.Mono && e.Family == d.Family {
			return text.FontKey(i)
		}
	}
	f.descs = append(f.descs, d)
	return text.FontKey(len(f.descs) - 1)
}

func (f *Text) size(k text.FontKey) float32 {
	d := f.descs[k]
	return d.SizePt * d.Scale
}

func (f *Text) Metrics(k text.FontKey) text.Metrics {
	s := f.size(k)
	return text.Metrics{Ascent: 0.8 * s, Descent: 0.2 * s, LineHeight: 1.2 * s, MonoAdvance: 0.5 * s}
}

func (f *Text) Shape(k text.FontKey, b []byte) text.RunRef {
	if len(b) == 0 {
		return 0
	}
	h := uint64(14695981039346656037) ^ uint64(k)
	for _, c := range b {
		h = (h ^ uint64(c)) * 1099511628211
	}
	i := int(h>>20) % slots
	r := &f.slots[i]
	if r.used && r.font == k && string(r.txt[:r.tn]) == string(b) {
		return text.RunRef(i + 1)
	}
	if r.used {
		f.runGen++
	}
	r.used, r.font, r.n = true, k, 0
	r.tn = copy(r.txt[:], b)
	adv := 0.5 * f.size(k)
	var x float32
	for i := 0; i < r.tn && r.n < maxGlyph; {
		ru, sz := utf8.DecodeRune(r.txt[i:r.tn])
		r.g[r.n], r.x[r.n], r.cl[r.n] = uint16(ru), x, uint32(i)
		r.n++
		x += adv
		i += sz
	}
	r.adv = x
	return text.RunRef(i + 1)
}

func (f *Text) Run(r text.RunRef) text.RunView {
	if r == 0 {
		return text.RunView{}
	}
	s := &f.slots[r-1]
	return text.RunView{Glyphs: s.g[:s.n], X: s.x[:s.n], Faces: s.f[:s.n], Clusters: s.cl[:s.n], Text: s.txt[:s.tn], Advance: s.adv}
}

func (f *Text) RunGeneration() uint32   { return f.runGen }
func (f *Text) AtlasGeneration() uint32 { return 0 }
func (f *Text) Glyph(face text.FaceID, gid uint16, sub uint8) text.GlyphEntry {
	if gid == ' ' {
		return text.GlyphEntry{}
	}
	return text.GlyphEntry{X: (gid % 100) * 8, Y: (gid / 100) * 10, W: 6, H: 8, BearingX: 1, BearingY: 9}
}
func (f *Text) Pages() int                      { return 1 }
func (f *Text) PageInfo(p int) (int, int, bool) { return 1024, 1024, false }
func (f *Text) ClearDirty(p int)                { f.dirty = false }
func (f *Text) Dirty(p int) (x, y, w, h int, pixels []byte, stride int, ok bool) {
	return 0, 0, 0, 0, nil, 0, false
}

var _ ui.Shaper = (*Text)(nil)

// Uploader is a texture uploader that keeps nothing.
type Uploader struct{ n int }

func (u *Uploader) NewTexture(kind gpu.TextureKind, w, h int) gpu.TextureID {
	u.n++
	return gpu.TextureID(u.n)
}
func (u *Uploader) Upload(t gpu.TextureID, x, y, w, h int, pixels []byte, stride int) {}
