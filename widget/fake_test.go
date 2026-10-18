package widget

import (
	"testing"
	"unicode/utf8"

	"gonativeui/gpu"
	"gonativeui/menu"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
)

// fakeText is a deterministic text system for the tests: one glyph per rune,
// every glyph advancing half the font size, no ink for spaces. Runs live in
// fixed slots, so shaping allocates nothing once the slots are made.
type fakeText struct {
	descs  []text.FontDesc
	slots  []fakeRun
	runGen uint32
	dirty  bool
}

const (
	fakeSlots = 1 << 12
	fakeGlyph = 160
	fakeBytes = 4 * fakeGlyph
)

type fakeRun struct {
	used bool
	font text.FontKey
	tn   int
	txt  [fakeBytes]byte
	n    int
	g    [fakeGlyph]uint16
	x    [fakeGlyph]float32
	f    [fakeGlyph]text.FaceID
	cl   [fakeGlyph]uint32
	adv  float32
}

func newFakeText() *fakeText {
	return &fakeText{slots: make([]fakeRun, fakeSlots), dirty: true, descs: []text.FontDesc{{SizePt: 13, Scale: 1}}}
}

func (f *fakeText) Font(d text.FontDesc) text.FontKey {
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

func (f *fakeText) size(k text.FontKey) float32 {
	d := f.descs[k]
	return d.SizePt * d.Scale
}

func (f *fakeText) Metrics(k text.FontKey) text.Metrics {
	s := f.size(k)
	return text.Metrics{Ascent: 0.8 * s, Descent: 0.2 * s, LineHeight: 1.2 * s, MonoAdvance: 0.5 * s}
}

func (f *fakeText) Shape(k text.FontKey, b []byte) text.RunRef {
	if len(b) == 0 {
		return 0
	}
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
	adv := 0.5 * f.size(k)
	var x float32
	for i := 0; i < r.tn && r.n < fakeGlyph; {
		ru, sz := utf8.DecodeRune(r.txt[i:r.tn])
		r.g[r.n], r.x[r.n], r.cl[r.n] = uint16(ru), x, uint32(i)
		r.n++
		x += adv
		i += sz
	}
	r.adv = x
	return text.RunRef(i + 1)
}

func (f *fakeText) Run(r text.RunRef) text.RunView {
	if r == 0 {
		return text.RunView{}
	}
	s := &f.slots[r-1]
	return text.RunView{Glyphs: s.g[:s.n], X: s.x[:s.n], Faces: s.f[:s.n], Clusters: s.cl[:s.n], Text: s.txt[:s.tn], Advance: s.adv}
}

func (f *fakeText) RunGeneration() uint32   { return f.runGen }
func (f *fakeText) AtlasGeneration() uint32 { return 0 }
func (f *fakeText) Glyph(face text.FaceID, gid uint16, sub uint8) text.GlyphEntry {
	if gid == ' ' {
		return text.GlyphEntry{}
	}
	return text.GlyphEntry{X: (gid % 100) * 8, Y: (gid / 100) * 10, W: 6, H: 8, BearingX: 1, BearingY: 9}
}
func (f *fakeText) Pages() int                      { return 1 }
func (f *fakeText) PageInfo(p int) (int, int, bool) { return 1024, 1024, false }
func (f *fakeText) ClearDirty(p int)                { f.dirty = false }
func (f *fakeText) Dirty(p int) (x, y, w, h int, pixels []byte, stride int, ok bool) {
	return 0, 0, 0, 0, nil, 0, false
}

var _ ui.Shaper = (*fakeText)(nil)

type fakeUp struct{ n int }

func (u *fakeUp) NewTexture(kind gpu.TextureKind, w, h int) gpu.TextureID {
	u.n++
	return gpu.TextureID(u.n)
}
func (u *fakeUp) Upload(t gpu.TextureID, x, y, w, h int, pixels []byte, stride int) {}

// rig drives frames of a Kit in a window of w×h pixels at scale 1.
type rig struct {
	t     *testing.T
	ts    *fakeText
	c     *ui.Context
	k     *Kit
	in    ui.Input
	list  gpu.DrawList
	w, h  int
	scale float32
	sem   ui.SemTree
	build func(k *Kit)
	time  int64
}

func newRig(t *testing.T, w, h int, build func(k *Kit)) *rig {
	t.Helper()
	ts := newFakeText()
	c := ui.New(ts)
	c.Reserve(4096)
	c.AtlasSync(&fakeUp{})
	g := &rig{t: t, ts: ts, c: c, w: w, h: h, scale: 1, build: build}
	g.k = NewKit(c, ts, rex.Theme())
	g.in.Inside, g.in.Focused = true, true
	g.in.MouseX, g.in.MouseY = -100, -100
	g.list.Quads = make([]gpu.Quad, 0, 1<<13)
	g.sem.Reserve(4096, 1<<16)
	for i := 0; i < 3; i++ {
		g.frame()
	}
	return g
}

// frame runs one 60 Hz frame and clears the per-frame input.
func (g *rig) frame() {
	g.time += 16_666_667
	g.in.Time = g.time
	g.c.Begin(&g.in, g.w, g.h, g.scale, 1.0/60)
	g.k.Begin(g.c)
	g.build(g.k)
	g.k.End()
	g.c.End(&g.list)
	g.in.ClearFrame()
}

// frames runs n frames.
func (g *rig) frames(n int) {
	for i := 0; i < n; i++ {
		g.frame()
	}
}

func (g *rig) move(x, y float32) {
	g.in.MouseX, g.in.MouseY = x, y
	g.frame()
}

// click presses and releases the left button at (x, y), a frame each.
func (g *rig) click(x, y float32) {
	g.in.MouseX, g.in.MouseY = x, y
	g.frame()
	g.in.SetButton(ui.ButtonLeft, true, 1)
	g.frame()
	g.in.SetButton(ui.ButtonLeft, false, 0)
	g.frame()
	g.frame()
}

// key sends a key press and runs a frame.
func (g *rig) key(code ui.KeyCode, mods ui.Mods) {
	g.in.PushKey(ui.KeyEvent{Code: code, Mods: mods, Down: true})
	g.frame()
}

func (g *rig) typ(s string) {
	g.in.AppendText(s)
	g.frame()
}

// semantics exports the tree of the last frame.
func (g *rig) semantics() *ui.SemTree {
	g.c.Semantics(&g.sem)
	return &g.sem
}

// find returns the first node of the exported tree with the role and name.
func (g *rig) find(role ui.Role, name string) (ui.SemNode, bool) {
	t := g.semantics()
	for _, n := range t.Nodes {
		if n.Role == role && string(t.Text(n.Name)) == name {
			return n, true
		}
	}
	return ui.SemNode{}, false
}

func (g *rig) must(role ui.Role, name string) ui.SemNode {
	g.t.Helper()
	n, ok := g.find(role, name)
	if !ok {
		g.t.Fatalf("no %d named %q in the semantic tree", role, name)
	}
	return n
}

// centre of a node's bounds.
func centre(n ui.SemNode) (x, y float32) {
	return (n.Bounds[0] + n.Bounds[2]) / 2, (n.Bounds[1] + n.Bounds[3]) / 2
}

// clickOn clicks the centre of the node with the role and name.
func (g *rig) clickOn(role ui.Role, name string) {
	g.t.Helper()
	x, y := centre(g.must(role, name))
	g.click(x, y)
}

var testMenu = func() *menu.Model {
	m, err := menu.NewModel(
		[]menu.Command{
			{ID: 1, Title: "New", Shortcut: menu.Shortcut{Key: 'N', Primary: true}},
			{ID: 2, Title: "Open…", Shortcut: menu.Shortcut{Key: 'O', Primary: true}},
			{ID: 3, Title: "Save", Shortcut: menu.Shortcut{Key: 'S', Primary: true}},
			{ID: 4, Title: "Show Hidden"},
			{ID: 5, Title: "Copy", Role: menu.RoleCopy, Shortcut: menu.Shortcut{Key: 'C', Primary: true}},
			{ID: 6, Title: "Recent One"},
			{ID: 7, Title: "Recent Two"},
		},
		[]menu.Menu{
			{Title: "File", Items: []menu.Item{
				{Kind: menu.ItemCommand, Command: 1}, {Kind: menu.ItemCommand, Command: 2},
				{Kind: menu.ItemSubmenu, Submenu: 2, Title: "Open Recent"},
				{Kind: menu.ItemSeparator}, {Kind: menu.ItemCommand, Command: 3},
			}},
			{Title: "View", Items: []menu.Item{{Kind: menu.ItemCommand, Command: 4}, {Kind: menu.ItemCommand, Command: 5}}},
			{Title: "Recent", Items: []menu.Item{{Kind: menu.ItemCommand, Command: 6}, {Kind: menu.ItemCommand, Command: 7}}},
		},
		[]int{0, 1})
	if err != nil {
		panic(err)
	}
	return m
}

var _ = theme.Light
