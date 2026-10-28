package icon

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"gonativeui/gpu"
	"gonativeui/text"
	"gonativeui/ui"
)

// rig is an offscreen window: the OS's real text system and renderer (Metal on
// macOS; Direct3D 11 / WARP and OpenGL / llvmpipe on the CI runners).
type rig struct {
	ts   *text.System
	r    *gpu.Renderer
	c    *ui.Context
	in   ui.Input
	list gpu.DrawList
	w, h int
}

func skipOrFail(t testing.TB, what string, err error) {
	t.Helper()
	if os.Getenv("CI") != "" {
		t.Fatalf("no %s on CI: %v", what, err)
	}
	t.Skipf("no %s: %v", what, err)
}

func newText(t testing.TB) *text.System {
	t.Helper()
	runtime.LockOSThread()
	ts, err := text.New()
	if err != nil {
		runtime.UnlockOSThread()
		skipOrFail(t, "text system", err)
	}
	t.Cleanup(func() { ts.Close(); runtime.UnlockOSThread() })
	return ts
}

func newRig(t testing.TB, w, h int) *rig {
	t.Helper()
	ts := newText(t)
	r, err := gpu.NewOffscreenRenderer()
	if err != nil {
		skipOrFail(t, "offscreen renderer", err)
	}
	t.Cleanup(r.Destroy)
	g := &rig{ts: ts, r: r, c: ui.New(ts), w: w, h: h}
	g.c.Reserve(1024)
	g.list.Quads = make([]gpu.Quad, 0, 1<<12)
	g.c.AtlasSync(r)
	g.in.Inside, g.in.Focused = true, true
	g.in.MouseX, g.in.MouseY = -100, -100
	return g
}

func (g *rig) frame(b interface{ Build(*ui.Context) }, scale float32) {
	g.c.Begin(&g.in, g.w, g.h, scale, 1.0/60)
	b.Build(g.c)
	g.c.End(&g.list)
	g.in.ClearFrame()
}

// pixels renders the last frame on a white target: straight RGBA, top row first.
func (g *rig) pixels() []byte {
	pix := make([]byte, g.w*g.h*4)
	g.r.RenderToRGBA(g.w, g.h, [4]float32{1, 1, 1, 1}, &g.list, pix)
	return pix
}

// sheet shows icons in a grid of square cells, each centred in its cell.
type sheet struct {
	a     *Atlas
	ids   []ID
	color uint32
	cell  float32 // pixels
	cols  int
}

func (s *sheet) Build(c *ui.Context) {
	for i, id := range s.ids {
		cx, cy := (float32(i%s.cols)+0.5)*s.cell, (float32(i/s.cols)+0.5)*s.cell
		flags, disp := s.a.Prepare(c, id, s.color)
		w, h := s.a.Size(id)
		c.NextPos(float32(int(cx-w/2+0.5)), float32(int(cy-h/2+0.5)))
		c.BoxKeyed(flags|ui.FloatingX|ui.FloatingY, ui.KeyN(1, uint64(i)), disp)
	}
}

func allIDs(a *Atlas) []ID {
	ids := make([]ID, a.Len())
	for i := range ids {
		ids[i] = ID(i)
	}
	return ids
}

// inkIn scans cell i of the sheet for pixels that are not the white of the
// target. It returns how many there are, the sum over them of red minus green
// (positive for ink tinted red, whatever its coverage) and how many are not
// shades between white and red at all.
func inkIn(g *rig, pix []byte, s *sheet, i int) (n, redness, offHue int) {
	x0, y0 := (i%s.cols)*int(s.cell), (i/s.cols)*int(s.cell)
	for y := y0; y < y0+int(s.cell); y++ {
		for x := x0; x < x0+int(s.cell); x++ {
			o := (y*g.w + x) * 4
			r, gr, b := int(pix[o]), int(pix[o+1]), int(pix[o+2])
			if 3*255-r-gr-b > 3*40 {
				n++
				redness += r - gr
				// white to 0xCC1100 keeps green within 17 of blue and red above green
				if r < gr-2 || gr-b > 24 || b-gr > 6 {
					offHue++
				}
			}
		}
	}
	return
}

// Every icon of the standard set draws on this OS, in the colour asked for, at
// both scales, from the source this OS is meant to use.
func TestStandardIconsDraw(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		g := newRig(t, 6*int(40*scale), 4*int(40*scale))
		a := New(g.ts)
		a.Raster(scale)
		a.Attach(g.r)
		s := &sheet{a: a, ids: allIDs(a), color: 0xCC1100FF, cell: 40 * scale, cols: 6}
		g.frame(s, scale)
		g.frame(s, scale) // a glyph shaped by the first frame is drawn by the next
		pix := g.pixels()
		for _, id := range s.ids {
			src := a.Source(id)
			switch runtime.GOOS {
			case "darwin":
				if src != SourceImage {
					t.Errorf("%vx icon %d (%s): source %v, want an SF Symbol image", scale, id, standard[id].Symbol, src)
				}
			case "windows":
				want := SourceFallback
				if a.SymbolFamily() != "" {
					want = SourceSymbolFont
				}
				if src != want {
					t.Errorf("%vx icon %d (U+%04X): source %v, want %v (symbol font %q)", scale, id, standard[id].Segoe, src, want, a.SymbolFamily())
				}
			default:
				if src != SourceFallback {
					t.Errorf("%vx icon %d (%q): source %v, want the fallback glyph", scale, id, standard[id].Fallback, src)
				}
			}
			n, redness, off := inkIn(g, pix, s, int(id))
			if n < int(4*scale*scale) {
				t.Errorf("%vx icon %d (%v): only %d inked pixels", scale, id, src, n)
				continue
			}
			// Tinted by the colour given: every inked pixel is a shade of it.
			if redness < 15*n || off > n/20 {
				t.Errorf("%vx icon %d: ink is not tinted red (%d inked pixels, redness %d, %d off hue)", scale, id, n, redness, off)
			}
			w, h := a.Size(id)
			if w <= 0 || h <= 0 || w > s.cell || h > s.cell {
				t.Errorf("%vx icon %d: size %v×%v does not fit a %v px cell", scale, id, w, h, s.cell)
			}
		}
		if runtime.GOOS == "windows" {
			t.Logf("symbol font: %q", a.SymbolFamily())
		}
	}
}

// The fallback glyphs are what Linux draws, and what every OS draws when its
// symbols are missing: each standard fallback rune is in the system fonts and
// draws.
func TestFallbackGlyphsDraw(t *testing.T) {
	const scale = 2
	g := newRig(t, 6*int(40*scale), 4*int(40*scale))
	specs := Standard()
	for i := range specs {
		specs[i].Symbol, specs[i].Segoe = "", 0
	}
	a := newAtlas(g.ts, specs)
	a.Raster(scale)
	a.Attach(g.r)
	s := &sheet{a: a, ids: allIDs(a), color: 0x000000FF, cell: 40 * scale, cols: 6}
	g.frame(s, scale)
	g.frame(s, scale)
	pix := g.pixels()
	for _, id := range s.ids {
		if src := a.Source(id); src != SourceFallback {
			t.Errorf("icon %d (%q): source %v, want the fallback glyph", id, specs[id].Fallback, src)
			continue
		}
		if n, _, _ := inkIn(g, pix, s, int(id)); n < 8 {
			t.Errorf("icon %d (%q): only %d inked pixels", id, specs[id].Fallback, n)
		}
	}
}

// On macOS the symbols are rasterised, not set as text: the coverage is in the
// atlas, grows with the scale, and a missing texture draws nothing rather than
// something wrong.
func TestSymbolsAreRasterisedPerScale(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("SF Symbols are macOS only")
	}
	ts := newText(t)
	a := New(ts)
	a.Raster(1)
	w1, h1 := a.Size(Search)
	a.Raster(2)
	w2, h2 := a.Size(Search)
	if w1 <= 0 || h1 <= 0 || w2 < 1.6*w1 || h2 < 1.6*h1 || w2 > 2.6*w1 {
		t.Errorf("search at 1x is %v×%v and at 2x %v×%v; the symbol is not re-rasterised per scale", w1, h1, w2, h2)
	}
	ink := 0
	for _, b := range a.pix {
		if b != 0 {
			ink++
		}
	}
	if ink == 0 {
		t.Error("the atlas has no coverage")
	}
}

// A symbol that does not exist falls back, step by step: the Segoe glyph if the
// system has a symbol font that holds it, else the fallback rune, else nothing.
func TestMissingSymbolFallsBack(t *testing.T) {
	ts := newText(t)
	const noSuchGlyph = 0x10FFFD // a private-use codepoint no font defines
	a := New(ts,
		Spec{Symbol: "no.such.symbol", Segoe: noSuchGlyph, Fallback: 'x', Pt: 12},
		Spec{Symbol: "no.such.symbol", Fallback: 'y', Pt: 12},
		Spec{Symbol: "no.such.symbol", Segoe: noSuchGlyph, Pt: 12},
		Spec{Pt: 12},
		Spec{Fallback: 'z', Pt: 12},
	)
	a.Raster(2)
	x := ID(NumStandard)
	for i, want := range []Source{SourceFallback, SourceFallback, SourceNone, SourceNone, SourceFallback} {
		if got := a.Source(x + ID(i)); got != want {
			t.Errorf("extra icon %d: source %v, want %v", i, got, want)
		}
	}
	if w, h := a.Size(x + 2); w != 0 || h != 0 {
		t.Errorf("an icon with no source has size %v×%v", w, h)
	}
	if w, h := a.Size(x); w <= 0 || h <= 0 {
		t.Errorf("a fallback glyph has size %v×%v", w, h)
	}
	if a.Source(ID(999)) != SourceNone {
		t.Error("an unknown id has a source")
	}
	if w, h := a.Size(ID(999)); w != 0 || h != 0 {
		t.Error("an unknown id has a size")
	}
}

// Drawing icons allocates nothing once their glyphs are cached.
func TestIconFramesAllocateNothing(t *testing.T) {
	g := newRig(t, 6*80, 4*80)
	a := New(g.ts, Spec{Fallback: 'x', Pt: 12})
	a.Raster(2)
	a.Attach(g.r)
	s := &sheet{a: a, ids: allIDs(a), color: 0x000000FF, cell: 80, cols: 6}
	for i := 0; i < 3; i++ {
		g.frame(s, 2)
	}
	if n := testing.AllocsPerRun(50, func() { g.frame(s, 2) }); n != 0 {
		t.Errorf("a frame of %d icons allocates %v times", a.Len(), n)
	}
}

// fakeText resolves families by name to fonts whose glyph for any codepoint is
// the font's key (0 means the font has no glyph for it).
type fakeText struct {
	fonts map[string]text.FontKey
	has   map[text.FontKey]bool
	runs  []text.RunView
}

func (f *fakeText) Font(d text.FontDesc) text.FontKey { return f.fonts[d.Family] }
func (f *fakeText) Metrics(text.FontKey) text.Metrics { return text.Metrics{} }
func (f *fakeText) Shape(k text.FontKey, _ []byte) text.RunRef {
	g := uint16(0)
	if f.has[k] {
		g = uint16(k)
	}
	f.runs = append(f.runs, text.RunView{Glyphs: []uint16{g}})
	return text.RunRef(len(f.runs) - 1)
}
func (f *fakeText) Run(r text.RunRef) text.RunView { return f.runs[r] }

// The symbol font is the first family that has the probe glyph; a family that
// is not installed resolves to a font that lacks it.
func TestFindsTheSymbolFont(t *testing.T) {
	ft := &fakeText{fonts: map[string]text.FontKey{"A": 1, "B": 2, "C": 3, "": 9}, has: map[text.FontKey]bool{2: true, 3: true}}
	a := &Atlas{ts: ft}
	for _, c := range []struct {
		families []string
		want     string
	}{
		{[]string{"A", "B", "C"}, "B"}, {[]string{"C", "B"}, "C"}, {[]string{"A", "Missing"}, ""}, {nil, ""},
	} {
		if got := a.findFamily(c.families); got != c.want {
			t.Errorf("findFamily(%v) = %q, want %q", c.families, got, c.want)
		}
	}
}

// TestPNG writes the standard icons of this OS at 2x, for review:
// GNUI_ICON_PNG=<dir> go test ./icon/ -run PNG
func TestPNG(t *testing.T) {
	dir := os.Getenv("GNUI_ICON_PNG")
	if dir == "" {
		t.Skip("set GNUI_ICON_PNG=<dir> to write the icon sheet")
	}
	const scale = 2
	g := newRig(t, 7*80, 3*80)
	a := New(g.ts)
	a.Raster(scale)
	a.Attach(g.r)
	s := &sheet{a: a, ids: allIDs(a), color: 0x1E2228FF, cell: 80, cols: 7}
	g.frame(s, scale)
	g.frame(s, scale)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "icons-"+runtime.GOOS+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, &image.NRGBA{Pix: g.pixels(), Stride: g.w * 4, Rect: image.Rect(0, 0, g.w, g.h)}); err != nil {
		t.Fatal(err)
	}
}

// Adding icons prepares none; an icon is prepared the first time it is asked
// about, and only then.
func TestOnlyUsedIconsArePrepared(t *testing.T) {
	g := newRig(t, 6*40, 4*40)
	extra := make([]Spec, 300)
	for i := range extra {
		extra[i] = Spec{Symbol: "star", Fallback: rune('A' + i%26), Pt: 12}
	}
	a := New(g.ts, extra...)
	a.Raster(2)
	a.Attach(g.r)
	if a.resolved != 0 {
		t.Fatalf("%d icons prepared before any is used", a.resolved)
	}
	a.Add(Spec{Fallback: 'Z', Pt: 12})
	ids := []ID{NumStandard, NumStandard + 7, NumStandard + 100, NumStandard + 299, NumStandard + 300}
	s := &sheet{a: a, ids: ids, color: 0x000000FF, cell: 40, cols: 6}
	g.frame(s, 2)
	g.frame(s, 2)
	if a.resolved != 5 {
		t.Errorf("%d icons prepared for 5 used", a.resolved)
	}
	a.Size(NumStandard + 7) // used again: not prepared again
	if a.resolved != 5 {
		t.Errorf("%d icons prepared after asking again", a.resolved)
	}
	if a.Source(ID(60000)) != SourceNone || a.resolved != 5 {
		t.Error("an unknown id was prepared")
	}
}

// Preparing an icon for the first time allocates nothing on the Go heap once
// the text system has its fonts and glyph runs: a frame that happens to be the
// first to use an icon must not be the one that allocates.
func TestFirstUseAllocatesNothing(t *testing.T) {
	ts := newText(t)
	extra := make([]Spec, 12)
	for i := range extra {
		extra[i] = Spec{Fallback: rune('A' + i), Pt: 12}
	}
	a := New(ts, extra...)
	a.Raster(2)
	use := func() {
		a.Raster(2) // forgets every icon: the next asks are first uses
		for i := range extra {
			a.Size(NumStandard + ID(i))
		}
		for id := ID(0); id < NumStandard; id++ {
			a.Size(id)
		}
	}
	use() // the fonts and runs of the text system
	if n := testing.AllocsPerRun(20, use); n != 0 {
		t.Errorf("preparing %d icons for the first time allocates %v times", len(extra)+int(NumStandard), n)
	}
}

// Kept icons are prepared by Raster, at every scale; the others wait for their
// first use.
func TestKeptIconsArePreparedAtRaster(t *testing.T) {
	ts := newText(t)
	a := New(ts, Spec{Fallback: 'a', Pt: 12}, Spec{Fallback: 'b', Pt: 12}, Spec{Fallback: 'c', Pt: 12})
	x := ID(NumStandard)
	a.Keep(x+1, ID(60000)) // before Raster: nothing yet; an unknown id is ignored
	if a.resolved != 0 {
		t.Fatalf("%d icons prepared before Raster", a.resolved)
	}
	a.Raster(1)
	if a.resolved != 1 || !a.ents[x+1].done || a.ents[x].done {
		t.Errorf("after Raster: %d prepared, kept %v, other %v", a.resolved, a.ents[x+1].done, a.ents[x].done)
	}
	a.Keep(x + 2) // after Raster: at once
	if a.resolved != 2 || !a.ents[x+2].done {
		t.Errorf("Keep after Raster: %d prepared", a.resolved)
	}
	a.Raster(2)
	if a.resolved != 2 || !a.ents[x+1].done || !a.ents[x+2].done || a.ents[x].done {
		t.Errorf("after a new scale: %d prepared", a.resolved)
	}
	if w1, _ := a.Size(x + 1); w1 <= 0 {
		t.Error("a kept icon has no size")
	}
}

// sheetWithText is a sheet with a line of text over it.
type sheetWithText struct{ *sheet }

func (s sheetWithText) Build(c *ui.Context) {
	s.sheet.Build(c)
	c.NextPos(4, 4)
	c.NextSize(ui.Px(200), ui.Px(24))
	c.NextFont(s.a.ts.Font(text.FontDesc{SizePt: 14, Weight: 400, Scale: 1}))
	c.NextTextColor(0x000000FF)
	c.BoxKeyed(ui.DrawText|ui.FloatingX|ui.FloatingY, ui.KeyN(2, 0), []byte("Device lost"))
}

// After a lost device the renderer is rebuilt in place: the atlas keeps its
// texture id and the texture loses its pixels. The icons, the text icons'
// glyphs and the text must draw as they did.
func TestIconsComeBackAfterADeviceLoss(t *testing.T) {
	g := newRig(t, 6*40, 4*40)
	a := New(g.ts)
	a.Raster(1)
	a.Attach(g.r)
	s := &sheet{a: a, ids: allIDs(a), color: 0xCC1100FF, cell: 40, cols: 6}
	withText := sheetWithText{s}
	g.frame(withText, 1)
	g.frame(withText, 1)
	before := g.pixels()
	inked := 0
	for i := range s.ids {
		n, _, _ := inkIn(g, before, s, i)
		inked += n
	}
	if inked == 0 {
		t.Fatal("the icons draw nothing before the loss")
	}

	g.r.SimulateDeviceLoss()
	if err := g.r.Rebuild(0, 0); err != nil {
		t.Fatal(err)
	}
	g.c.AtlasRestore()   // the app does this for the text
	g.frame(withText, 1) // the icon atlas notices the new epoch in Prepare
	if after := g.pixels(); !bytes.Equal(before, after) {
		diff := 0
		for i := range before {
			if before[i] != after[i] {
				diff++
			}
		}
		t.Errorf("the icons differ after the device was rebuilt: %d of %d bytes", diff, len(before))
	}
}
