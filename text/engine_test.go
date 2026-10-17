//go:build linux || textengine

package text

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/go-text/typesetting/font"
)

// These tests drive the engine with a fake fontFinder and the fonts that ship
// in go-text's own test data (Roboto, Amiri, Ubuntu Mono), so they are
// deterministic and need neither fontconfig nor installed fonts. They run on
// Linux and, with -tags textengine, on any OS.

var (
	fontDirOnce sync.Once
	fontDir     string
)

// testFont returns the path of a font file in go-text's testdata, skipping
// the test if the module cache is not available.
func testFont(t *testing.T, name string) string {
	t.Helper()
	fontDirOnce.Do(func() {
		out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/go-text/typesetting").Output()
		if err == nil {
			fontDir = filepath.Join(strings.TrimSpace(string(out)), "font", "testdata")
		}
	})
	p := filepath.Join(fontDir, name)
	if fontDir == "" {
		t.Skip("cannot locate go-text's test fonts (go list failed)")
	}
	if _, err := os.Stat(p); err != nil {
		t.Skipf("test font missing: %v", err)
	}
	return p
}

// fakeFinder resolves roles to fixed files.
type fakeFinder struct {
	ui, mono, emojiRef fontRef
	families           map[string]fontRef
	fallbackFn         func(r rune) (fontRef, bool)
	fallbackCalls      int
	closed             bool
}

func (f *fakeFinder) primary(d FontDesc, px float32) (fontRef, bool) {
	if r, ok := f.families[d.Family]; ok && d.Family != "" {
		return r, true
	}
	if d.Mono {
		return f.mono, true
	}
	return f.ui, true
}

func (f *fakeFinder) emoji(d FontDesc, px float32) (fontRef, bool) {
	return f.emojiRef, f.emojiRef.file != ""
}

func (f *fakeFinder) fallback(d FontDesc, px float32, r rune) (fontRef, bool) {
	f.fallbackCalls++
	if f.fallbackFn == nil {
		return fontRef{}, false
	}
	return f.fallbackFn(r)
}

func (f *fakeFinder) close() { f.closed = true }

func isArabic(r rune) bool { return r >= 0x600 && r <= 0x6FF }

func newEngine(t *testing.T) (*goSystem, *fakeFinder) {
	t.Helper()
	roboto := fontRef{file: testFont(t, "Roboto-Regular.ttf")}
	amiri := fontRef{file: testFont(t, "Amiri-Regular.ttf")}
	ff := &fakeFinder{
		ui:   roboto,
		mono: fontRef{file: testFont(t, "UbuntuMono-R.ttf")},
		fallbackFn: func(r rune) (fontRef, bool) {
			if isArabic(r) {
				return amiri, true
			}
			return fontRef{}, false
		},
	}
	s := newGoSystem(ff)
	t.Cleanup(s.Close)
	return s, ff
}

func ui(s *goSystem) FontKey { return s.Font(FontDesc{SizePt: 13, Scale: 2, Weight: 400}) }

func eShape(s *goSystem, f FontKey, str string) RunView { return s.Run(s.Shape(f, []byte(str))) }

func glyphPixels(t *testing.T, s *goSystem, e GlyphEntry) []byte {
	t.Helper()
	p := s.atlas.pages[e.Page]
	var out []byte
	for r := 0; r < int(e.H); r++ {
		o := ((int(e.Y)+r)*PageSize + int(e.X)) * p.bpp
		out = append(out, p.pix[o:o+int(e.W)*p.bpp]...)
	}
	return out
}

func sumBytes(b []byte) (n int) {
	for _, v := range b {
		n += int(v)
	}
	return
}

func TestEngineShapeHello(t *testing.T) {
	s, _ := newEngine(t)
	f := ui(s)
	v := eShape(s, f, "Hello")
	if len(v.Glyphs) != 5 || len(v.X) != 5 || len(v.Faces) != 5 {
		t.Fatalf("got %d/%d/%d glyphs, want 5", len(v.Glyphs), len(v.X), len(v.Faces))
	}
	for i := 1; i < 5; i++ {
		if !(v.X[i] > v.X[i-1]) {
			t.Fatalf("X not strictly increasing: %v", v.X)
		}
	}
	if v.X[0] != 0 || !(v.Advance > v.X[4]) {
		t.Fatalf("X[0]=%v advance %v last pen %v", v.X[0], v.Advance, v.X[4])
	}
	if v.Glyphs[2] != v.Glyphs[3] {
		t.Fatalf("l glyphs differ: %v", v.Glyphs)
	}
	for _, g := range v.Glyphs {
		if g == 0 {
			t.Fatalf("missing glyph in %v", v.Glyphs)
		}
	}
	// 26 px Roboto: "Hello" is about 2.5 em wide at this size... sanity bounds.
	if v.Advance < 40 || v.Advance > 90 {
		t.Fatalf("advance %v px for Hello at 26 px", v.Advance)
	}
}

func TestEngineFractionalSize(t *testing.T) {
	s, _ := newEngine(t)
	a := eShape(s, s.Font(FontDesc{SizePt: 13, Scale: 1}), "Stress-test demo").Advance
	b := eShape(s, s.Font(FontDesc{SizePt: 19.5, Scale: 1}), "Stress-test demo").Advance
	if r := b / a; math.Abs(float64(r)-1.5) > 0.01 {
		t.Fatalf("advance ratio for 1.5x size = %v", r)
	}
}

func TestEngineMetrics(t *testing.T) {
	s, _ := newEngine(t)
	f := ui(s)
	m := s.Metrics(f)
	t.Logf("13pt@2x: %+v", m)
	if !(m.Ascent > 0 && m.Descent > 0 && m.CapHeight > 0 && m.XHeight > 0 && m.XHeight < m.CapHeight) {
		t.Fatalf("implausible metrics %+v", m)
	}
	if want := m.Ascent + m.Descent + m.LineGap; math.Abs(float64(m.LineHeight-want)) > 1e-4 {
		t.Fatalf("LineHeight %v != %v", m.LineHeight, want)
	}
	if m.CapHeight < 15 || m.CapHeight > 22 {
		t.Fatalf("cap height %v px for a 26 px font", m.CapHeight)
	}
	if s.Metrics(FontKey(999)) != (Metrics{}) {
		t.Fatal("metrics for an unknown font")
	}
	m1 := s.Metrics(s.Font(FontDesc{SizePt: 13, Scale: 1}))
	if r := m.Ascent / m1.Ascent; math.Abs(float64(r)-2) > 0.05 {
		t.Fatalf("ascent ratio between scale 2 and 1 = %v", r)
	}
	if a, b := s.Font(FontDesc{SizePt: 13, Scale: 2}), s.Font(FontDesc{SizePt: 13, Scale: 2, Weight: 400}); a != b {
		t.Fatalf("defaulted and explicit weight 400 are different fonts: %d %d", a, b)
	}
}

func TestEngineMonoAdvance(t *testing.T) {
	s, _ := newEngine(t)
	f := s.Font(FontDesc{Mono: true, SizePt: 13, Scale: 2})
	m := s.Metrics(f)
	if m.MonoAdvance <= 0 {
		t.Fatalf("MonoAdvance = %v", m.MonoAdvance)
	}
	v := eShape(s, f, "iW.M0 lmw")
	for i := 1; i < len(v.X); i++ {
		if d := v.X[i] - v.X[i-1]; math.Abs(float64(d-m.MonoAdvance)) > 0.05 {
			t.Fatalf("glyph %d advances %v, want %v (X=%v)", i-1, d, m.MonoAdvance, v.X)
		}
	}
	if want := m.MonoAdvance * float32(len(v.X)); math.Abs(float64(v.Advance-want)) > 0.1 {
		t.Fatalf("run advance %v, want %v", v.Advance, want)
	}
	if p := eShape(s, ui(s), "iW"); p.X[1] == m.MonoAdvance {
		t.Fatal("proportional font looks monospaced")
	}
}

func TestEngineFallbackAndBidi(t *testing.T) {
	s, ff := newEngine(t)
	f := ui(s)
	primary := s.fonts[f].face
	v := eShape(s, f, "مرحبا")
	t.Logf("arabic: glyphs %v X %v advance %v face %q", v.Glyphs, v.X, v.Advance, s.faceName(v.Faces[0]))
	if len(v.Glyphs) < 1 || !(v.Advance > 0) {
		t.Fatalf("arabic: %d glyphs, advance %v", len(v.Glyphs), v.Advance)
	}
	for i, g := range v.Glyphs {
		if g == 0 {
			t.Fatal("missing glyph")
		}
		if v.Faces[i] == primary {
			t.Fatalf("Arabic glyph %d drawn with the Latin primary font", i)
		}
		if i > 0 && v.X[i] < v.X[i-1] {
			t.Fatalf("X decreases within a RTL run: %v", v.X)
		}
	}
	calls := ff.fallbackCalls
	eShape(s, f, "مرحبا مرحبا")
	if ff.fallbackCalls != calls {
		t.Fatalf("fallback lookups are not cached: %d -> %d", calls, ff.fallbackCalls)
	}

	// Mixed LTR/RTL/LTR: Latin on both sides keeps the primary face, the
	// Arabic run is in the middle, X is monotonic, and the total is the sum.
	m := eShape(s, f, "abc مرحبا def")
	if m.Faces[0] != primary || m.Faces[len(m.Faces)-1] != primary {
		t.Fatalf("outer faces %v / %v, want the primary", m.Faces[0], m.Faces[len(m.Faces)-1])
	}
	arabicFaces := 0
	for _, fc := range m.Faces {
		if fc != primary {
			arabicFaces++
		}
	}
	if arabicFaces < 1 {
		t.Fatal("no fallback face in the mixed run")
	}
	for i := 1; i < len(m.X); i++ {
		if m.X[i] < m.X[i-1] {
			t.Fatalf("mixed X decreases at %d: %v", i, m.X)
		}
	}
	if !(m.Advance > v.Advance) {
		t.Fatalf("mixed advance %v <= arabic-only %v", m.Advance, v.Advance)
	}
	// In a RTL paragraph (Arabic first) the Latin ends up on the left.
	r := eShape(s, f, "مرحبا abc")
	if r.Faces[0] != primary {
		t.Fatalf("RTL paragraph: leftmost glyph face %q, want the Latin primary", s.faceName(r.Faces[0]))
	}
	if r.Faces[len(r.Faces)-1] == primary {
		t.Fatal("RTL paragraph: rightmost glyph should be Arabic")
	}
	// A multi-paragraph string shapes completely.
	if p := eShape(s, f, "ab\ncd"); len(p.Glyphs) != 5 {
		t.Fatalf("two paragraphs shaped to %d glyphs", len(p.Glyphs))
	}
}

func TestVisualOrder(t *testing.T) {
	cases := []struct {
		levels []int
		want   []int
	}{
		{[]int{0}, []int{0}},
		{[]int{0, 1, 0}, []int{0, 1, 2}},
		{[]int{0, 1, 2, 1, 0}, []int{0, 3, 2, 1, 4}},
		{[]int{1, 2, 1}, []int{2, 1, 0}},
		{[]int{2, 1}, []int{1, 0}},
		{[]int{0, 1, 0, 1, 0}, []int{0, 1, 2, 3, 4}},
	}
	for _, c := range cases {
		var s goSystem
		for _, l := range c.levels {
			s.bruns = append(s.bruns, bidiRun{level: l})
		}
		s.visualOrder()
		if !slices.Equal(s.order, c.want) {
			t.Errorf("levels %v: order %v, want %v", c.levels, s.order, c.want)
		}
	}
}

func TestEngineInvalidUTF8AndEmpty(t *testing.T) {
	s, _ := newEngine(t)
	f := ui(s)
	if v := eShape(s, f, ""); len(v.Glyphs) != 0 || v.Advance != 0 {
		t.Fatalf("empty string: %+v", v)
	}
	bad := s.Run(s.Shape(f, []byte{'a', 0xff, 0xfe, 'b'}))
	if len(bad.Glyphs) < 3 || !(bad.Advance > 0) {
		t.Fatalf("invalid UTF-8 run: %+v", bad)
	}
	if s.Run(s.Shape(FontKey(500), []byte("x"))).Glyphs != nil {
		t.Fatal("shaping with an unknown font returned glyphs")
	}
}

func TestEngineRasterA(t *testing.T) {
	s, _ := newEngine(t)
	f := ui(s)
	v := eShape(s, f, "A")
	e := s.Glyph(v.Faces[0], v.Glyphs[0], 0)
	if e.Color || e.W == 0 || e.H == 0 {
		t.Fatalf("entry %+v", e)
	}
	px := glyphPixels(t, s, e)
	var mx byte
	for _, p := range px {
		mx = max(mx, p)
	}
	if mx < 200 {
		t.Fatalf("max coverage %d: stems should be nearly opaque", mx)
	}
	m := s.Metrics(f)
	if e.BearingY < m.CapHeight-1 || e.BearingY > m.CapHeight+3 {
		t.Fatalf("BearingY %v vs cap height %v", e.BearingY, m.CapHeight)
	}
	if int(e.H) < int(m.CapHeight) || e.BearingX > 2 || e.BearingX < -4 {
		t.Fatalf("entry %+v", e)
	}
	p := s.atlas.pages[e.Page]
	for x := int(e.X) - 1; x <= int(e.X)+int(e.W); x++ {
		if p.pix[(int(e.Y)-1)*PageSize+x] != 0 || p.pix[(int(e.Y)+int(e.H))*PageSize+x] != 0 {
			t.Fatal("atlas padding row is not empty")
		}
	}
	if e2 := s.Glyph(v.Faces[0], v.Glyphs[0], 0); e2 != e {
		t.Fatalf("second lookup %+v != %+v", e2, e)
	}
	// The apex row of the A is narrow and its base wide: ink moves outwards
	// going down (checks the y axis is not flipped).
	width := func(row int) int {
		n := 0
		for x := 0; x < int(e.W); x++ {
			if px[row*int(e.W)+x] > 128 {
				n++
			}
		}
		return n
	}
	if top, bot := width(2), width(int(e.H)-3); !(top < bot) {
		t.Fatalf("A is %d px wide near the top and %d near the bottom: upside down?", top, bot)
	}
}

func TestEngineSpaceHasNoInk(t *testing.T) {
	s, _ := newEngine(t)
	v := eShape(s, ui(s), " ")
	if e := s.Glyph(v.Faces[0], v.Glyphs[0], 0); e.W != 0 || e.H != 0 {
		t.Fatalf("space entry %+v", e)
	}
	if s.Pages() != 0 {
		t.Fatal("a space allocated atlas storage")
	}
}

func TestEngineDirtyLifecycle(t *testing.T) {
	s, _ := newEngine(t)
	f := ui(s)
	v := eShape(s, f, "A")
	e := s.Glyph(v.Faces[0], v.Glyphs[0], 0)
	x, y, w, h, px, stride, ok := s.Dirty(int(e.Page))
	if !ok || x != int(e.X) || y != int(e.Y) || w != int(e.W) || h != int(e.H) || stride != PageSize {
		t.Fatalf("dirty %d,%d %dx%d stride %d ok=%v, entry %+v", x, y, w, h, stride, ok, e)
	}
	nz := 0
	for r := 0; r < h; r++ {
		nz += sumBytes(px[r*stride : r*stride+w])
	}
	if nz == 0 {
		t.Fatal("dirty pixels are empty")
	}
	s.ClearDirty(int(e.Page))
	if _, _, _, _, _, _, ok := s.Dirty(int(e.Page)); ok {
		t.Fatal("dirty after ClearDirty")
	}
	s.Glyph(v.Faces[0], v.Glyphs[0], 0)
	if _, _, _, _, _, _, ok := s.Dirty(int(e.Page)); ok {
		t.Fatal("cache hit dirtied the page")
	}
	e2 := s.Glyph(eShape(s, f, "B").Faces[0], eShape(s, f, "B").Glyphs[0], 0)
	x, y, w, h, _, _, ok = s.Dirty(int(e2.Page))
	if !ok || x > int(e2.X) || y > int(e2.Y) || x+w < int(e2.X)+int(e2.W) || y+h < int(e2.Y)+int(e2.H) {
		t.Fatalf("dirty %d,%d %dx%d does not cover %+v", x, y, w, h, e2)
	}
}

func TestEngineSubpixelVariants(t *testing.T) {
	s, _ := newEngine(t)
	v := eShape(s, ui(s), "l")
	var cent [4]float64
	var maps [4][]byte
	for sub := uint8(0); sub < 4; sub++ {
		e := s.Glyph(v.Faces[0], v.Glyphs[0], sub)
		px := glyphPixels(t, s, e)
		maps[sub] = px
		var m, mx float64
		for i, p := range px {
			m += float64(p) * (float64(i%int(e.W)) + 0.5)
			mx += float64(p)
		}
		cent[sub] = float64(e.BearingX) + m/mx
	}
	for sub := 1; sub < 4; sub++ {
		if bytes.Equal(maps[sub], maps[sub-1]) {
			t.Fatalf("subpixel variants %d and %d are identical", sub-1, sub)
		}
		if d := cent[sub] - cent[sub-1]; math.Abs(d-0.25) > 0.08 {
			t.Fatalf("centroid moved %.3f px between variants %d and %d, want 0.25", d, sub-1, sub)
		}
	}
}

func TestEngineAtlasResetOnFull(t *testing.T) {
	s, _ := newEngine(t)
	s.atlas.pageDim = 64
	s.atlas.maxPer = 1
	f := s.Font(FontDesc{SizePt: 20, Scale: 2})
	v := eShape(s, f, "The quick brown fox jumps over the lazy dog 0123456789")
	gen := s.AtlasGeneration()
	for round := 0; round < 3; round++ {
		for i, g := range v.Glyphs {
			if e := s.Glyph(v.Faces[i], g, uint8(round)); e.W != 0 && (int(e.X)+int(e.W) > 64 || int(e.Y)+int(e.H) > 64) {
				t.Fatalf("entry outside the page: %+v", e)
			}
		}
	}
	if s.AtlasGeneration() == gen {
		t.Fatal("no reset although a 64×64 page cannot hold the glyph set")
	}
}

func TestEngineRunCacheEviction(t *testing.T) {
	s, _ := newEngine(t)
	f := ui(s)
	s.runs.maxRuns = 50
	gen := s.RunGeneration()
	for i := 0; i < 400; i++ {
		str := []byte{'a' + byte(i%26), '0' + byte(i/26%10), 'x'}
		if v := s.Run(s.Shape(f, str)); len(v.Glyphs) != 3 {
			t.Fatalf("%q: %d glyphs", str, len(v.Glyphs))
		}
	}
	if s.RunGeneration() == gen {
		t.Fatal("no eviction")
	}
}

func TestEngineZeroAllocHits(t *testing.T) {
	s, _ := newEngine(t)
	f := ui(s)
	mono := s.Font(FontDesc{Mono: true, SizePt: 13, Scale: 2, Weight: 500})
	items := []struct {
		f FontKey
		b []byte
	}{
		{f, []byte("Stress-test Snake demo | rex-snake")},
		{mono, []byte("I inspected demo/snake.mjs and ran it")},
		{f, []byte("مرحبا بالعالم abc")},
	}
	for _, it := range items {
		v := s.Run(s.Shape(it.f, it.b))
		for i, g := range v.Glyphs {
			for sub := uint8(0); sub < 4; sub++ {
				s.Glyph(v.Faces[i], g, sub)
			}
		}
	}
	// The runtime itself can make a stray allocation in the background, so
	// the exact delta must be 0 in at least one of several windows; a real
	// per-hit allocation shows up in every window.
	const iters, windows = 2000, 8
	sink, clean := 0, 0
	for w := 0; w < windows; w++ {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		for n := 0; n < iters; n++ {
			it := items[n%len(items)]
			v := s.Run(s.Shape(it.f, it.b))
			for i, g := range v.Glyphs {
				sink += int(s.Glyph(v.Faces[i], g, uint8(n+i)).W)
			}
			sink += int(s.Font(FontDesc{SizePt: 13, Scale: 2, Weight: 400}))
		}
		runtime.ReadMemStats(&after)
		dm, db := after.Mallocs-before.Mallocs, after.TotalAlloc-before.TotalAlloc
		t.Logf("window %d: Mallocs +%d, TotalAlloc +%d bytes", w, dm, db)
		if dm == 0 && db == 0 {
			clean++
		}
	}
	_ = sink
	if clean == 0 {
		t.Error("every window allocated: cache hits are not allocation-free")
	}
}

func TestEngineCloseIsIdempotent(t *testing.T) {
	s, ff := newEngine(t)
	eShape(s, ui(s), "x")
	s.Glyph(0, 1, 0)
	s.Close()
	s.Close()
	if !ff.closed {
		t.Fatal("finder not closed")
	}
}

func TestEmojiPresentation(t *testing.T) {
	for _, r := range []rune{0x1F600, 0x1F680, 0x1F9D1, 0x1FA70, 0x1F1E6, 0x2B50, 0x231A, 0x2705, 0x2650} {
		if !isEmojiPresentation(r) {
			t.Errorf("U+%04X should have emoji presentation", r)
		}
	}
	for _, r := range []rune{'A', '1', 0x2764, 0x263A, 0x2600, 0x00A9, 0x4E00, 0x1F100} {
		if isEmojiPresentation(r) {
			t.Errorf("U+%04X should not have emoji presentation", r)
		}
	}
}

// ---- Colour bitmaps (CBDT), on a synthetic font ----

// writeSfnt serializes fonts (each a set of tables) as an sfnt file, or as a
// TrueType collection when there are several.
func writeSfnt(fonts []map[uint32][]byte) []byte {
	type placed struct {
		tag, off, n uint32
	}
	var body bytes.Buffer
	dirs := make([][]placed, len(fonts))
	hdr := 12 // sfnt header, or TTC header
	if len(fonts) > 1 {
		hdr = 12 + 4*len(fonts)
	}
	dirBytes := 0
	for _, f := range fonts {
		dirBytes += 12 + 16*len(f)
	}
	base := hdr + dirBytes
	if len(fonts) == 1 {
		base = 12 + 16*len(fonts[0])
	}
	for i, f := range fonts {
		tags := make([]uint32, 0, len(f))
		for t := range f {
			tags = append(tags, t)
		}
		slices.Sort(tags)
		for _, t := range tags {
			for (base+body.Len())%4 != 0 {
				body.WriteByte(0)
			}
			dirs[i] = append(dirs[i], placed{t, uint32(base + body.Len()), uint32(len(f[t]))})
			body.Write(f[t])
		}
	}
	var out bytes.Buffer
	be := binary.BigEndian
	put32 := func(v uint32) { _ = binary.Write(&out, be, v) }
	put16 := func(v uint16) { _ = binary.Write(&out, be, v) }
	writeDir := func(d []placed) {
		put32(0x00010000)
		put16(uint16(len(d)))
		put16(0)
		put16(0)
		put16(0)
		for _, p := range d {
			put32(p.tag)
			put32(0)
			put32(p.off)
			put32(p.n)
		}
	}
	if len(fonts) > 1 {
		put32(tagTTCF)
		put32(0x00010000)
		put32(uint32(len(fonts)))
		off := hdr
		for _, f := range fonts {
			put32(uint32(off))
			off += 12 + 16*len(f)
		}
	}
	for _, d := range dirs {
		writeDir(d)
	}
	for (hdr+dirBytes)%4 != 0 {
		out.WriteByte(0)
	}
	out.Write(body.Bytes())
	return out.Bytes()
}

func readTables(t *testing.T, file []byte) map[uint32][]byte {
	t.Helper()
	r := bytes.NewReader(file)
	d, err := readSfntDirectory(r, 0)
	if err != nil {
		t.Fatal(err)
	}
	m := map[uint32][]byte{}
	for _, tb := range d.tables {
		m[tb.tag] = file[tb.offset : tb.offset+tb.length]
	}
	return m
}

// cbTables builds CBLC/CBDT tables with one PNG glyph (index format 1, image
// format 17, ppem 24). pad bytes of zeros follow the image in CBDT.
func cbTables(gid uint16, pngData []byte, w, h, pad int) (cblc, cbdtT []byte) {
	be := binary.BigEndian
	var d bytes.Buffer
	_ = binary.Write(&d, be, uint32(0x00030000))
	d.Write([]byte{byte(h), byte(w), 1, byte(h) - 4, byte(w) + 2}) // SmallGlyphMetrics: bearingX 1, bearingY h-4
	_ = binary.Write(&d, be, uint32(len(pngData)))
	d.Write(pngData)
	imgLen := 5 + 4 + len(pngData)
	d.Write(make([]byte, pad))

	var c bytes.Buffer
	_ = binary.Write(&c, be, uint16(3)) // major
	_ = binary.Write(&c, be, uint16(0))
	_ = binary.Write(&c, be, uint32(1))  // numSizes
	_ = binary.Write(&c, be, uint32(56)) // indexSubTableArrayOffset
	_ = binary.Write(&c, be, uint32(8+16))
	_ = binary.Write(&c, be, uint32(1)) // numberOfIndexSubTables
	_ = binary.Write(&c, be, uint32(0)) // colorRef
	c.Write(make([]byte, 24))           // hori + vert line metrics
	_ = binary.Write(&c, be, gid)
	_ = binary.Write(&c, be, gid)
	c.Write([]byte{24, 24, 32, 1}) // ppemX, ppemY, bitDepth, flags
	// IndexSubTableArray (1 element) at 56
	_ = binary.Write(&c, be, gid)
	_ = binary.Write(&c, be, gid)
	_ = binary.Write(&c, be, uint32(8))
	// IndexSubHeader + IndexData1 at 64
	_ = binary.Write(&c, be, uint16(1))  // index format
	_ = binary.Write(&c, be, uint16(17)) // image format
	_ = binary.Write(&c, be, uint32(4))  // image data offset (past the CBDT version)
	_ = binary.Write(&c, be, uint32(0))
	_ = binary.Write(&c, be, uint32(imgLen))
	return c.Bytes(), d.Bytes()
}

// testPNG is a w×h image: left half red, right half blue, opaque, except the
// top-left pixel which is 50% red (straight alpha).
func testPNG(w, h int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if x < w/2 {
				img.SetNRGBA(x, y, color.NRGBA{255, 0, 0, 255})
			} else {
				img.SetNRGBA(x, y, color.NRGBA{0, 0, 255, 255})
			}
		}
	}
	img.SetNRGBA(0, 0, color.NRGBA{255, 0, 0, 128})
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

// colorFontFile writes a copy of Roboto with a CBDT bitmap for 'A' and
// returns its path and the glyph id of 'A'.
func colorFontFile(t *testing.T, pad int) (string, uint16) {
	t.Helper()
	src, err := os.ReadFile(testFont(t, "Roboto-Regular.ttf"))
	if err != nil {
		t.Fatal(err)
	}
	face, err := font.ParseTTF(bytes.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	gid, ok := face.NominalGlyph('A')
	if !ok {
		t.Fatal("no A in Roboto")
	}
	tabs := readTables(t, src)
	tabs[tagCBLC], tabs[tagCBDT] = cbTables(uint16(gid), testPNG(24, 24), 24, 24, pad)
	p := filepath.Join(t.TempDir(), "color.ttf")
	if err := os.WriteFile(p, writeSfnt([]map[uint32][]byte{tabs}), 0o644); err != nil {
		t.Fatal(err)
	}
	return p, uint16(gid)
}

func TestSfntViewHidesTablesAndSelectsCollectionFont(t *testing.T) {
	src, err := os.ReadFile(testFont(t, "Roboto-Regular.ttf"))
	if err != nil {
		t.Fatal(err)
	}
	tabs := readTables(t, src)
	withBitmap := map[uint32][]byte{}
	for k, v := range tabs {
		withBitmap[k] = v
	}
	withBitmap[tagCBLC], withBitmap[tagCBDT] = cbTables(5, testPNG(8, 8), 8, 8, 0)
	// Font 0 is plain Roboto; font 1 has the bitmap tables.
	ttc := writeSfnt([]map[uint32][]byte{tabs, withBitmap})
	path := filepath.Join(t.TempDir(), "c.ttc")
	if err := os.WriteFile(path, ttc, 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for i, wantCB := range []bool{false, true} {
		d, err := readSfntDirectory(f, i)
		if err != nil {
			t.Fatal(err)
		}
		_, has := d.find(tagCBDT)
		if has != wantCB {
			t.Fatalf("font %d: CBDT present = %v", i, has)
		}
		if (openCBDT(f, d) != nil) != wantCB {
			t.Fatalf("font %d: openCBDT mismatch", i)
		}
		size, _ := fileSize(f)
		v := newSfntView(f, size, d, hideBitmaps)
		viewDir, err := readSfntDirectory(v, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := viewDir.find(tagCBDT); ok {
			t.Fatalf("font %d: hidden table still listed", i)
		}
		if len(viewDir.tables) != len(tabs) {
			t.Fatalf("font %d: view lists %d tables, want %d", i, len(viewDir.tables), len(tabs))
		}
	}
	if _, err := readSfntDirectory(f, 2); err == nil {
		t.Fatal("collection index out of range accepted")
	}
	// go-text parses the view as a normal font.
	s := newGoSystem(&fakeFinder{ui: fontRef{file: path, index: 1}, mono: fontRef{file: path, index: 0}})
	defer s.Close()
	if _, err := s.loadData(fontRef{file: path, index: 1}); err != nil {
		t.Fatal(err)
	}
	if eShape(s, ui(s), "Hello").Advance <= 0 {
		t.Fatal("cannot shape with a collection member")
	}
}

func TestColorGlyphFromCBDT(t *testing.T) {
	path, gid := colorFontFile(t, 0)
	ff := &fakeFinder{ui: fontRef{file: path}, mono: fontRef{file: path}}
	s := newGoSystem(ff)
	defer s.Close()
	f := s.Font(FontDesc{SizePt: 12, Scale: 1}) // 12 px: a 24 ppem strike at half size
	v := eShape(s, f, "A")
	if len(v.Glyphs) != 1 || v.Glyphs[0] != gid {
		t.Fatalf("shaped %v, want glyph %d", v.Glyphs, gid)
	}
	e := s.Glyph(v.Faces[0], v.Glyphs[0], 2)
	if !e.Color || e.W != 12 || e.H != 12 {
		t.Fatalf("entry %+v, want a 12×12 colour glyph", e)
	}
	if e != s.Glyph(v.Faces[0], v.Glyphs[0], 0) {
		t.Fatal("colour glyph differs across subpixel values")
	}
	if e.BearingX != 1 || e.BearingY != 10 { // (1, 20) × 0.5
		t.Fatalf("bearings %v, %v", e.BearingX, e.BearingY)
	}
	if _, _, color := s.PageInfo(int(e.Page)); !color {
		t.Fatal("colour glyph on an R8 page")
	}
	px := glyphPixels(t, s, e)
	at := func(x, y int) [4]byte { o := (y*int(e.W) + x) * 4; return [4]byte(px[o : o+4]) }
	if got := at(8, 6); got != [4]byte{0, 0, 255, 255} {
		t.Errorf("right half = %v, want blue", got)
	}
	if got := at(3, 6); got != [4]byte{255, 0, 0, 255} {
		t.Errorf("left half = %v, want red", got)
	}
	// The 50%-red source pixel is premultiplied and averaged with its three
	// opaque red neighbours: alpha (128+3*255)/4 and premultiplied r = alpha.
	if got := at(0, 0); got[1] != 0 || got[2] != 0 || got[0] != got[3] || got[3] < 200 || got[3] > 230 {
		t.Errorf("corner pixel = %v, want premultiplied red with alpha ≈ 223", got)
	}
	// Other glyphs of the colour font have no bitmap: no ink, no crash.
	if e := s.Glyph(v.Faces[0], gid+1, 0); e.W != 0 {
		t.Fatalf("glyph without bitmap has an entry %+v", e)
	}
}

// TestColorFontIsReadLazily is the D9 memory requirement: a colour font's
// image table is never read whole. The synthetic font carries 24 MiB of CBDT.
func TestColorFontIsReadLazily(t *testing.T) {
	path, _ := colorFontFile(t, 24<<20)
	st, _ := os.Stat(path)
	if st.Size() < 24<<20 {
		t.Fatalf("test font is only %d bytes", st.Size())
	}
	s := newGoSystem(&fakeFinder{ui: fontRef{file: path}, mono: fontRef{file: path}})
	defer s.Close()
	runtime.GC()
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	f := s.Font(FontDesc{SizePt: 12, Scale: 1})
	v := eShape(s, f, "A")
	if e := s.Glyph(v.Faces[0], v.Glyphs[0], 0); !e.Color || e.W == 0 {
		t.Fatalf("entry %+v", e)
	}
	runtime.ReadMemStats(&m1)
	alloc := m1.TotalAlloc - m0.TotalAlloc
	t.Logf("loading a font with a %d MiB CBDT and rasterizing one colour glyph allocated %d KiB", 24, alloc>>10)
	if alloc > 6<<20 {
		t.Fatalf("allocated %d MiB: the image table was read in whole", alloc>>20)
	}
}

func TestEmojiPreferredOverMonochromePrimary(t *testing.T) {
	path, _ := colorFontFile(t, 0)
	roboto := fontRef{file: testFont(t, "Roboto-Regular.ttf")}
	ff := &fakeFinder{ui: roboto, mono: roboto, emojiRef: fontRef{file: path}}
	s := newGoSystem(ff)
	defer s.Close()
	f := ui(s)
	// The colour font here is Roboto plus bitmaps, so it covers every rune the
	// primary does. An emoji-presentation rune that the primary has a glyph
	// for must still go to the emoji font; a text-presentation one must not.
	if !s.has(s.fonts[f].face, 'A') {
		t.Fatal("setup")
	}
	if id := s.resolve(f, 'A'); id != s.fonts[f].face {
		t.Fatal("plain letter should use the primary font")
	}
	emojiID, ok := s.emojiFace(f)
	if !ok || !s.faces[emojiID].color {
		t.Fatal("colour emoji face not found")
	}
	// No emoji font -> the primary keeps everything.
	ff.emojiRef = fontRef{}
	s2 := newGoSystem(ff)
	defer s2.Close()
	if _, ok := s2.emojiFace(ui(s2)); ok {
		t.Fatal("emoji face without an emoji font")
	}
	// A non-colour "emoji" candidate (outline font) is rejected.
	ff.emojiRef = roboto
	s3 := newGoSystem(ff)
	defer s3.Close()
	if _, ok := s3.emojiFace(ui(s3)); ok {
		t.Fatal("outline font accepted as colour emoji font")
	}
}

func TestFontFallsBackWhenMatchIsUnreadable(t *testing.T) {
	roboto := fontRef{file: testFont(t, "Roboto-Regular.ttf")}
	ff := &fakeFinder{
		ui:         fontRef{file: filepath.Join(t.TempDir(), "gone.ttf")},
		fallbackFn: func(r rune) (fontRef, bool) { return roboto, true },
	}
	s := newGoSystem(ff)
	defer s.Close()
	f := s.Font(FontDesc{SizePt: 13, Scale: 1})
	if int(f) >= len(s.fonts) {
		t.Fatal("no font although a fallback exists")
	}
	if v := eShape(s, f, "Hi"); len(v.Glyphs) != 2 {
		t.Fatalf("shaped %d glyphs", len(v.Glyphs))
	}
	// Nothing usable at all: an invalid key, empty runs, no panic.
	s2 := newGoSystem(&fakeFinder{ui: ff.ui})
	defer s2.Close()
	k := s2.Font(FontDesc{SizePt: 13, Scale: 1})
	if int(k) < len(s2.fonts) || len(eShape(s2, k, "x").Glyphs) != 0 || s2.Metrics(k) != (Metrics{}) {
		t.Fatal("an unusable font should give an inert key")
	}
}
