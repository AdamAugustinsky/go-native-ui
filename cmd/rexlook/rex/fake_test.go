package rex

import (
	"runtime"
	"testing"

	"gonativeui/gpu"
	"gonativeui/text"
	"gonativeui/ui"
)

// fakeText is a deterministic TextSystem for tests that need no fonts: every
// rune advances 0.6 em, glyph ids are the rune values and every glyph but the
// space has a 6×8 bitmap. Like the real run cache it keeps runs in fixed
// slabs, so a miss allocates nothing either.
type fakeText struct {
	descs  []text.FontDesc
	slots  []fakeRun
	runGen uint32
	dirty  bool
}

const (
	fakeSlots     = 1 << 13
	fakeMaxGlyphs = 80
)

type fakeRun struct {
	used  bool
	font  text.FontKey
	tn    int
	txt   [fakeMaxGlyphs * 4]byte
	n     int
	g     [fakeMaxGlyphs]uint16
	x     [fakeMaxGlyphs]float32
	faces [fakeMaxGlyphs]text.FaceID
	adv   float32
}

func newFake() *fakeText { return &fakeText{slots: make([]fakeRun, fakeSlots), dirty: true} }

func (f *fakeText) Font(d text.FontDesc) text.FontKey {
	for i, e := range f.descs {
		if e == d {
			return text.FontKey(i)
		}
	}
	f.descs = append(f.descs, d)
	return text.FontKey(len(f.descs) - 1)
}

func (f *fakeText) px(k text.FontKey) float32 { return f.descs[k].SizePt * f.descs[k].Scale }

func (f *fakeText) Metrics(k text.FontKey) text.Metrics {
	px := f.px(k)
	return text.Metrics{Ascent: 0.8 * px, Descent: 0.2 * px, LineHeight: 1.2 * px, MonoAdvance: 0.6 * px}
}

func (f *fakeText) Shape(k text.FontKey, b []byte) text.RunRef {
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
		f.runGen++
	}
	r.used, r.font, r.n = true, k, 0
	r.tn = copy(r.txt[:], b)
	adv := 0.6 * f.px(k)
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

func (f *fakeText) Run(r text.RunRef) text.RunView {
	s := &f.slots[r-1]
	return text.RunView{Glyphs: s.g[:s.n], X: s.x[:s.n], Faces: s.faces[:s.n], Advance: s.adv}
}
func (f *fakeText) RunGeneration() uint32   { return f.runGen }
func (f *fakeText) AtlasGeneration() uint32 { return 0 }
func (f *fakeText) Glyph(face text.FaceID, gid uint16, sub uint8) text.GlyphEntry {
	if gid == ' ' {
		return text.GlyphEntry{}
	}
	return text.GlyphEntry{X: (gid % 100) * 8, Y: (gid / 100) * 10, W: 6, H: 8, BearingX: 1, BearingY: 9}
}
func (f *fakeText) Pages() int                    { return 1 }
func (f *fakeText) PageInfo(int) (int, int, bool) { return 1024, 1024, false }
func (f *fakeText) ClearDirty(int)                { f.dirty = false }
func (f *fakeText) Dirty(p int) (x, y, w, h int, pixels []byte, stride int, ok bool) {
	if !f.dirty {
		return
	}
	return 0, 0, 4, 4, make([]byte, 4*1024), 1024, true
}

type fakeUploader struct{ n int }

func (u *fakeUploader) NewTexture(kind gpu.TextureKind, w, h int) gpu.TextureID {
	u.n++
	return gpu.TextureID(u.n)
}
func (u *fakeUploader) Upload(t gpu.TextureID, x, y, w, h int, pixels []byte, stride int) {}

// hrn drives frames of a Rex over the fake text system: no GPU, no fonts.
type hrn struct {
	t     *testing.T
	x     *Rex
	c     *ui.Context
	in    ui.Input
	list  gpu.DrawList
	w, h  int
	scale float32
}

// newHarness creates a harness for a w×h point window at the given scale.
func newHarness(t *testing.T, wPt, hPt int, scale float32) *hrn {
	// New rasterises the system symbols through AppKit on macOS, whose
	// autorelease pools belong to one thread: stay on this one.
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	ft := newFake()
	h := &hrn{t: t, w: int(float32(wPt) * scale), h: int(float32(hPt) * scale), scale: scale}
	h.x = New(ft, scale)
	h.c = ui.New(ft)
	h.c.Reserve(4096)
	h.list.Quads = make([]gpu.Quad, 0, 1<<14)
	h.x.AttachRenderer(&fakeUploader{})
	h.c.AtlasSync(&fakeUploader{})
	h.in.Inside, h.in.Focused = true, true
	h.in.MouseX, h.in.MouseY = -100, -100
	h.settle()
	return h
}

func (h *hrn) frame() {
	h.c.Begin(&h.in, h.w, h.h, h.scale, 1.0/60)
	h.x.Build(h.c)
	h.c.End(&h.list)
	h.in.ClearFrame()
}

func (h *hrn) frames(n int) {
	for i := 0; i < n; i++ {
		h.frame()
	}
}

// settle runs until nothing animates, failing if it never does.
func (h *hrn) settle() {
	h.t.Helper()
	for i := 0; i < 900; i++ {
		h.frame()
		if !h.c.Animating() {
			return
		}
	}
	h.t.Fatal("animations never settled")
}

func (h *hrn) move(x, y float32) { h.in.MouseX, h.in.MouseY = x, y }

func (h *hrn) click(x, y float32) {
	h.move(x, y)
	h.frame()
	h.in.SetButton(ui.ButtonLeft, true, 1)
	h.frame()
	h.in.SetButton(ui.ButtonLeft, false, 0)
	h.frame()
}

func (h *hrn) cur() *tab { return &h.x.tabs[h.x.active] }

// tscript is a script that also runs the frame its input is for.
type tscript struct {
	script
	frame func()
}

func (s *tscript) step()       { s.script.step(); s.frame() }
func (s *tscript) structural() { s.script.structural(); s.frame() }
