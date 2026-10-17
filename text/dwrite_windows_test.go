//go:build windows

package text

import (
	"errors"
	"math"
	"runtime"
	"testing"
	"time"
)

func newSys(t *testing.T) *System {
	t.Helper()
	// The shaping callbacks and the float-argument frame belong to one
	// thread; the platform's render thread is locked in real use.
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	s, err := New()
	if errors.Is(err, errNoFactory) {
		t.Skipf("DirectWrite unavailable: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func uiFont(s *System) FontKey { return s.Font(FontDesc{SizePt: 13, Scale: 2, Weight: 400}) }

func shapeStr(s *System, f FontKey, str string) RunView { return s.Run(s.Shape(f, []byte(str))) }

func TestShapeHello(t *testing.T) {
	s := newSys(t)
	f := uiFont(s)
	t.Logf("UI font: %s (family %q)", s.faceName(s.fonts[f].face), s.defFamily)
	v := shapeStr(s, f, "Hello")
	if len(v.Glyphs) != 5 || len(v.X) != 5 || len(v.Faces) != 5 {
		t.Fatalf("got %d/%d/%d glyphs, want 5", len(v.Glyphs), len(v.X), len(v.Faces))
	}
	for i := 1; i < 5; i++ {
		if !(v.X[i] > v.X[i-1]) {
			t.Fatalf("X not strictly increasing: %v", v.X)
		}
	}
	if !(v.Advance > v.X[4]) {
		t.Fatalf("advance %v does not extend past the last pen %v", v.Advance, v.X[4])
	}
	if v.X[0] != 0 {
		t.Fatalf("first pen at %v, want 0", v.X[0])
	}
	// "ll" in Hello: identical glyphs advance identically.
	if v.Glyphs[2] != v.Glyphs[3] {
		t.Fatalf("l glyphs differ: %v", v.Glyphs)
	}
	for _, g := range v.Glyphs {
		if g == 0 {
			t.Fatalf("missing glyph in %v", v.Glyphs)
		}
	}
	for _, face := range v.Faces {
		if face != s.fonts[f].face {
			t.Fatalf("Latin text left the primary face: %v", v.Faces)
		}
	}
}

func TestMetrics(t *testing.T) {
	s := newSys(t)
	m := s.Metrics(uiFont(s))
	t.Logf("13pt@2x: %+v", m)
	if !(m.Ascent > 0 && m.Descent > 0 && m.CapHeight > 0 && m.XHeight > 0 && m.XHeight < m.CapHeight) {
		t.Fatalf("implausible metrics %+v", m)
	}
	if want := m.Ascent + m.Descent + m.LineGap; math.Abs(float64(m.LineHeight-want)) > 1e-4 {
		t.Fatalf("LineHeight %v != ascent+descent+gap %v", m.LineHeight, want)
	}
	// 13pt at 2x is 26 px: Segoe UI cap height is ~0.7 em.
	if m.CapHeight < 15 || m.CapHeight > 22 {
		t.Fatalf("cap height %v px for a 26 px font", m.CapHeight)
	}
	if s.Metrics(FontKey(999)) != (Metrics{}) {
		t.Fatal("metrics for an unknown font")
	}
}

func TestFontDedupAndWeights(t *testing.T) {
	s := newSys(t)
	a := s.Font(FontDesc{SizePt: 13, Scale: 2})
	b := s.Font(FontDesc{SizePt: 13, Scale: 2, Weight: 400})
	if a != b {
		t.Fatalf("defaulted and explicit weight 400 are different fonts: %d %d", a, b)
	}
	faces := map[int]FaceID{}
	for _, w := range []int{400, 500, 600, 700} {
		k := s.Font(FontDesc{SizePt: 13, Scale: 2, Weight: w})
		faces[w] = s.fonts[k].face
		t.Logf("weight %d -> %s (advance of 0: %v)", w, s.faceName(faces[w]), s.Metrics(k).MonoAdvance)
	}
	// 500 may match the regular face (CSS-style matching with no medium face
	// installed) and 600 may match bold, but bold must differ from regular.
	if faces[400] == faces[700] {
		t.Fatalf("weights 400 and 700 map to the same face: %v", faces)
	}
	if faces[400] == faces[600] {
		t.Logf("note: weight 600 resolved to the regular face %q", s.faceName(faces[400]))
	}
	reg := shapeStr(s, uiFont(s), "Stress-test Snake demo")
	bold := shapeStr(s, s.Font(FontDesc{SizePt: 13, Scale: 2, Weight: 700}), "Stress-test Snake demo")
	if !(bold.Advance > reg.Advance) {
		t.Fatalf("bold (%v) should be wider than regular (%v)", bold.Advance, reg.Advance)
	}
	// Scale multiplies pixel metrics.
	m1 := s.Metrics(s.Font(FontDesc{SizePt: 13, Scale: 1}))
	m2 := s.Metrics(s.Font(FontDesc{SizePt: 13, Scale: 2}))
	if r := m2.Ascent / m1.Ascent; math.Abs(float64(r)-2) > 0.05 {
		t.Fatalf("ascent ratio between scale 2 and 1 = %v", r)
	}
	// A named family, and an unknown one that falls back to the default.
	h := s.Font(FontDesc{Family: "Arial", SizePt: 13, Scale: 2})
	t.Logf("Arial -> %s", s.faceName(s.fonts[h].face))
	if n := s.faceName(s.fonts[h].face); len(n) < 5 || n[:5] != "Arial" {
		t.Fatalf("family face = %q", n)
	}
	u := s.Font(FontDesc{Family: "No Such Family 123", SizePt: 13, Scale: 2})
	if s.fonts[u].face != s.fonts[a].face {
		t.Fatalf("unknown family resolved to %q, want the default %q", s.faceName(s.fonts[u].face), s.faceName(s.fonts[a].face))
	}
}

func TestMonoAdvance(t *testing.T) {
	s := newSys(t)
	f := s.Font(FontDesc{Mono: true, SizePt: 13, Scale: 2})
	m := s.Metrics(f)
	if m.MonoAdvance <= 0 {
		t.Fatalf("MonoAdvance = %v", m.MonoAdvance)
	}
	v := shapeStr(s, f, "iW.M0 lmw")
	t.Logf("mono face %s advance %v", s.faceName(v.Faces[0]), m.MonoAdvance)
	for i := 1; i < len(v.X); i++ {
		if d := v.X[i] - v.X[i-1]; math.Abs(float64(d-m.MonoAdvance)) > 1e-3 {
			t.Fatalf("glyph %d advances %v, want %v (X=%v)", i-1, d, m.MonoAdvance, v.X)
		}
	}
	if want := m.MonoAdvance * float32(len(v.X)); math.Abs(float64(v.Advance-want)) > 1e-2 {
		t.Fatalf("run advance %v, want %v", v.Advance, want)
	}
	// And the UI font is proportional.
	p := shapeStr(s, uiFont(s), "iW")
	if p.X[1] == m.MonoAdvance {
		t.Fatal("proportional font looks monospaced")
	}
}

func TestFallbackCJK(t *testing.T) {
	s := newSys(t)
	f := uiFont(s)
	primary := s.fonts[f].face
	v := shapeStr(s, f, "日本語")
	if len(v.Glyphs) != 3 {
		t.Fatalf("got %d glyphs for 日本語", len(v.Glyphs))
	}
	for i, face := range v.Faces {
		t.Logf("glyph %d id %d face %d %q (primary %q)", i, v.Glyphs[i], face, s.faceName(face), s.faceName(primary))
	}
	for i, g := range v.Glyphs {
		if g == 0 {
			t.Skipf("glyph %d is .notdef: this Windows image has no CJK font (Japanese supplemental fonts not installed)", i)
		}
	}
	for i, face := range v.Faces {
		if face == primary {
			t.Errorf("glyph %d stayed in the UI font %q, which has no CJK coverage; font fallback did not run", i, s.faceName(face))
		}
	}
	// Mixed script: faces change within the line and X stays monotonic.
	m := shapeStr(s, f, "a日b")
	if len(m.Glyphs) != 3 || m.Faces[0] != primary || m.Faces[2] != primary || m.Faces[1] == primary {
		t.Fatalf("mixed run faces %v", m.Faces)
	}
	if !(m.X[1] > m.X[0] && m.X[2] > m.X[1]) {
		t.Fatalf("mixed X %v", m.X)
	}
}

func TestArabic(t *testing.T) {
	s := newSys(t)
	v := shapeStr(s, uiFont(s), "مرحبا")
	t.Logf("glyphs %v X %v advance %v face %q", v.Glyphs, v.X, v.Advance, s.faceName(v.Faces[0]))
	if len(v.Glyphs) < 1 || !(v.Advance > 0) {
		t.Fatalf("arabic: %d glyphs, advance %v", len(v.Glyphs), v.Advance)
	}
	for _, g := range v.Glyphs {
		if g == 0 {
			t.Fatal("missing glyph")
		}
	}
	for i := 1; i < len(v.X); i++ {
		if v.X[i] < v.X[i-1] {
			t.Fatalf("pen positions go backwards: %v", v.X)
		}
	}
	// Mixed LTR/RTL still produces one consistent line width.
	m := shapeStr(s, uiFont(s), "abc مرحبا def")
	if !(m.Advance > v.Advance) {
		t.Fatalf("mixed advance %v <= arabic-only %v", m.Advance, v.Advance)
	}
}

// TestBidiVisualOrder checks that runs come out left to right in display
// order for both paragraph directions.
func TestBidiVisualOrder(t *testing.T) {
	s := newSys(t)
	f := uiFont(s)
	abc, def := shapeStr(s, f, "abc"), shapeStr(s, f, "def")
	ar := shapeStr(s, f, "مرحبا")
	same := func(got, want []uint16) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}
	// LTR paragraph (first strong letter is Latin): abc, Arabic, def.
	m := shapeStr(s, f, "abc مرحبا def")
	n := len(m.Glyphs)
	if n < 3+len(ar.Glyphs)+3 {
		t.Fatalf("mixed run has %d glyphs", n)
	}
	if !same(m.Glyphs[:3], abc.Glyphs) || !same(m.Glyphs[n-3:], def.Glyphs) {
		t.Errorf("LTR paragraph: edges are %v ... %v, want %v ... %v", m.Glyphs[:3], m.Glyphs[n-3:], abc.Glyphs, def.Glyphs)
	}
	// RTL paragraph (first strong letter is Arabic): the Latin text, logically
	// last, is displayed at the left.
	r := shapeStr(s, f, "مرحبا abc")
	if !same(r.Glyphs[:3], abc.Glyphs) {
		t.Errorf("RTL paragraph: leftmost glyphs %v, want abc %v", r.Glyphs[:3], abc.Glyphs)
	}
	for i := 1; i < len(m.X); i++ {
		if m.X[i] < m.X[i-1] {
			t.Errorf("mixed pen positions go backwards at %d: %v", i, m.X)
			break
		}
	}
}

func TestInvalidUTF8AndEmpty(t *testing.T) {
	s := newSys(t)
	f := uiFont(s)
	v := shapeStr(s, f, "")
	if len(v.Glyphs) != 0 || v.Advance != 0 {
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

func pixels(t *testing.T, s *System, e GlyphEntry) []byte {
	t.Helper()
	w, _, color := s.PageInfo(int(e.Page))
	if w != PageSize || color != e.Color {
		t.Fatalf("page info (%d, %v) for entry %+v", w, color, e)
	}
	p := s.atlas.pages[e.Page]
	var out []byte
	for r := 0; r < int(e.H); r++ {
		o := ((int(e.Y)+r)*PageSize + int(e.X)) * p.bpp
		out = append(out, p.pix[o:o+int(e.W)*p.bpp]...)
	}
	return out
}

func sum(b []byte) (n int) {
	for _, v := range b {
		n += int(v)
	}
	return
}

func TestRasterA(t *testing.T) {
	s := newSys(t)
	f := uiFont(s)
	v := shapeStr(s, f, "A")
	t0 := time.Now()
	e := s.Glyph(v.Faces[0], v.Glyphs[0], 0)
	t.Logf("first rasterization of A: %v, entry %+v", time.Since(t0), e)
	if e.Color || e.W == 0 || e.H == 0 {
		t.Fatalf("entry %+v", e)
	}
	px := pixels(t, s, e)
	if sum(px) == 0 {
		t.Fatal("rasterized A has no coverage")
	}
	var mx byte
	for _, p := range px {
		mx = max(mx, p)
	}
	if mx < 180 {
		t.Fatalf("max coverage %d: stems should be nearly opaque", mx)
	}
	// Geometry: the glyph's top sits about cap height above the baseline
	// (the bounds include the antialiasing fringe).
	m := s.Metrics(f)
	if e.BearingY < m.CapHeight-2 || e.BearingY > m.CapHeight+4 {
		t.Fatalf("BearingY %v vs cap height %v", e.BearingY, m.CapHeight)
	}
	if int(e.H) < int(m.CapHeight) || e.BearingX > 3 || e.BearingX < -5 {
		t.Fatalf("entry %+v", e)
	}
	// The atlas padding around the rect in the page is untouched.
	p := s.atlas.pages[e.Page]
	for x := int(e.X) - 1; x <= int(e.X)+int(e.W); x++ {
		if p.pix[(int(e.Y)-1)*PageSize+x] != 0 || p.pix[(int(e.Y)+int(e.H))*PageSize+x] != 0 {
			t.Fatal("atlas padding row is not empty")
		}
	}
	for y := int(e.Y) - 1; y <= int(e.Y)+int(e.H); y++ {
		if p.pix[y*PageSize+int(e.X)-1] != 0 || p.pix[y*PageSize+int(e.X)+int(e.W)] != 0 {
			t.Fatal("atlas padding column is not empty")
		}
	}
	// A second lookup returns the same entry.
	if e2 := s.Glyph(v.Faces[0], v.Glyphs[0], 0); e2 != e {
		t.Fatalf("second lookup %+v != %+v", e2, e)
	}
}

func TestSpaceHasNoInk(t *testing.T) {
	s := newSys(t)
	v := shapeStr(s, uiFont(s), " ")
	e := s.Glyph(v.Faces[0], v.Glyphs[0], 0)
	if e.W != 0 || e.H != 0 {
		t.Fatalf("space entry %+v", e)
	}
	if s.Pages() != 0 {
		t.Fatal("a space allocated atlas storage")
	}
}

func TestEmojiIsColor(t *testing.T) {
	s := newSys(t)
	f := uiFont(s)
	v := shapeStr(s, f, "😀")
	if len(v.Glyphs) != 1 {
		t.Fatalf("got %d glyphs", len(v.Glyphs))
	}
	face := v.Faces[0]
	t.Logf("emoji face %q (COLR table: %v), primary %q", s.faceName(face), s.faces[face].color, s.faceName(s.fonts[f].face))
	if face == s.fonts[f].face || v.Glyphs[0] == 0 {
		t.Skipf("no emoji font on this Windows image (glyph %d in %q)", v.Glyphs[0], s.faceName(face))
	}
	t0 := time.Now()
	e := s.Glyph(face, v.Glyphs[0], 2) // subpixel is ignored for colour glyphs
	t.Logf("first rasterization of the emoji: %v, entry %+v", time.Since(t0), e)
	if e.W == 0 || e.H == 0 {
		t.Fatalf("entry %+v", e)
	}
	if !e.Color {
		t.Skipf("DirectWrite returned no COLR v0 layers for this emoji (entry is monochrome %+v); colour glyphs unavailable on this OS image", e)
	}
	if e != s.Glyph(face, v.Glyphs[0], 0) {
		t.Fatal("colour glyph variants differ across subpixel values")
	}
	_, _, color := s.PageInfo(int(e.Page))
	if !color {
		t.Fatal("colour glyph landed on an R8 page")
	}
	px := pixels(t, s, e)
	if len(px) != int(e.W)*int(e.H)*4 || sum(px) == 0 {
		t.Fatal("emoji bitmap is empty")
	}
	// Premultiplied: no channel exceeds alpha. And it has coloured (non-grey) pixels.
	coloured := false
	for i := 0; i < len(px); i += 4 {
		r, g, b, a := px[i], px[i+1], px[i+2], px[i+3]
		if r > a || g > a || b > a {
			t.Fatalf("pixel %d not premultiplied: %v", i/4, px[i:i+4])
		}
		if a > 0 && (int(r)-int(b) > 30 || int(b)-int(r) > 30) {
			coloured = true
		}
	}
	if !coloured {
		t.Fatal("emoji bitmap has no colour")
	}
	if int(e.H) < 20 || e.BearingY <= 0 {
		t.Fatalf("emoji geometry %+v", e)
	}
}

func TestDirtyLifecycle(t *testing.T) {
	s := newSys(t)
	if s.Pages() != 0 {
		t.Fatal("pages before first glyph")
	}
	v := shapeStr(s, uiFont(s), "A")
	e := s.Glyph(v.Faces[0], v.Glyphs[0], 0)
	x, y, w, h, px, stride, ok := s.Dirty(int(e.Page))
	if !ok {
		t.Fatal("not dirty after rasterizing")
	}
	if x != int(e.X) || y != int(e.Y) || w != int(e.W) || h != int(e.H) || stride != PageSize {
		t.Fatalf("dirty %d,%d %dx%d stride %d, entry %+v", x, y, w, h, stride, e)
	}
	nz := 0
	for r := 0; r < h; r++ {
		nz += sum(px[r*stride : r*stride+w])
	}
	if nz == 0 {
		t.Fatal("dirty pixels are empty")
	}
	// A second glyph extends the region; a cache hit does not dirty anything.
	s.ClearDirty(int(e.Page))
	if _, _, _, _, _, _, ok := s.Dirty(int(e.Page)); ok {
		t.Fatal("dirty after ClearDirty")
	}
	s.Glyph(v.Faces[0], v.Glyphs[0], 0)
	if _, _, _, _, _, _, ok := s.Dirty(int(e.Page)); ok {
		t.Fatal("cache hit dirtied the page")
	}
	v2 := shapeStr(s, uiFont(s), "B")
	e2 := s.Glyph(v2.Faces[0], v2.Glyphs[0], 0)
	x, y, w, h, _, _, ok = s.Dirty(int(e2.Page))
	if !ok || x > int(e2.X) || y > int(e2.Y) || x+w < int(e2.X)+int(e2.W) || y+h < int(e2.Y)+int(e2.H) {
		t.Fatalf("dirty %d,%d %dx%d does not cover %+v", x, y, w, h, e2)
	}
	if w2, h2, _ := s.PageInfo(99); w2 != 0 || h2 != 0 {
		t.Fatal("PageInfo of a missing page")
	}
}

func TestSubpixelVariants(t *testing.T) {
	s := newSys(t)
	v := shapeStr(s, uiFont(s), "l")
	var cent [4]float64
	var bitmaps [4][]byte
	for sub := uint8(0); sub < 4; sub++ {
		e := s.Glyph(v.Faces[0], v.Glyphs[0], sub)
		px := pixels(t, s, e)
		bitmaps[sub] = px
		var m, mx float64
		for i, p := range px {
			c := i % int(e.W)
			m += float64(p) * (float64(c) + 0.5)
			mx += float64(p)
		}
		cent[sub] = float64(e.BearingX) + m/mx
		t.Logf("sub %d: bearing %v size %dx%d centroid x %.3f", sub, e.BearingX, e.W, e.H, cent[sub])
	}
	for sub := 1; sub < 4; sub++ {
		if string(bitmaps[sub]) == string(bitmaps[sub-1]) {
			t.Fatalf("subpixel variants %d and %d are identical bitmaps (DirectWrite snapped)", sub-1, sub)
		}
		if cent[sub] <= cent[sub-1] {
			t.Fatalf("centroid did not move right between variants %d and %d (%.3f -> %.3f)", sub-1, sub, cent[sub-1], cent[sub])
		}
	}
	// 0.75 px in total, with room for the hinting and filtering DirectWrite does.
	if d := cent[3] - cent[0]; d < 0.5 || d > 1.0 {
		t.Fatalf("centroid moved %.3f px over three quarter-pixel steps, want about 0.75", d)
	}
}

func TestAtlasResetOnFull(t *testing.T) {
	s := newSys(t)
	s.atlas.pageDim = 64
	s.atlas.maxPer = 1
	f := s.Font(FontDesc{SizePt: 20, Scale: 2})
	v := shapeStr(s, f, "The quick brown fox jumps over the lazy dog 0123456789")
	gen := s.AtlasGeneration()
	for round := 0; round < 3; round++ {
		for i, g := range v.Glyphs {
			e := s.Glyph(v.Faces[i], g, uint8(round))
			if e.W == 0 {
				continue
			}
			if int(e.X)+int(e.W) > 64 || int(e.Y)+int(e.H) > 64 {
				t.Fatalf("entry outside the page: %+v", e)
			}
		}
	}
	if s.AtlasGeneration() == gen {
		t.Fatal("no reset although a 64×64 page cannot hold the glyph set")
	}
}

func TestRunCacheEvictionWithShaper(t *testing.T) {
	s := newSys(t)
	f := uiFont(s)
	s.runs.maxRuns = 50
	gen := s.RunGeneration()
	for i := 0; i < 400; i++ {
		str := []byte{'a' + byte(i%26), 'a' + byte(i/26%26), 'x'}
		v := s.Run(s.Shape(f, str))
		if len(v.Glyphs) != 3 {
			t.Fatalf("%q: %d glyphs", str, len(v.Glyphs))
		}
	}
	if s.RunGeneration() == gen {
		t.Fatal("no eviction")
	}
}

// TestTimings logs what a cache miss costs, for the CI log (no assertions).
func TestTimings(t *testing.T) {
	s := newSys(t)
	f := uiFont(s)
	strs := []string{"Stress-test Snake demo | rex-snake", "日本語のテキスト", "مرحبا بالعالم", "Smile 😀 ok"}
	for _, str := range strs {
		t0 := time.Now()
		v := shapeStr(s, f, str)
		d1 := time.Since(t0)
		t0 = time.Now()
		for i, g := range v.Glyphs {
			s.Glyph(v.Faces[i], g, 0)
		}
		d2 := time.Since(t0)
		t0 = time.Now()
		shapeStr(s, f, str)
		t.Logf("%q: %d glyphs, shape miss %v, rasterize all %v, shape hit %v", str, len(v.Glyphs), d1, d2, time.Since(t0))
	}
	t.Logf("faces after shaping: %d", len(s.faces))
	for i := range s.faces {
		t.Logf("  face %d %q %.1fpx colr=%v", i, s.faces[i].name, s.faces[i].sizePx, s.faces[i].color)
	}
}

func TestZeroAllocHits(t *testing.T) {
	s := newSys(t)
	ui := uiFont(s)
	mono := s.Font(FontDesc{Mono: true, SizePt: 13, Scale: 2, Weight: 500})
	type item struct {
		f FontKey
		b []byte
	}
	items := []item{
		{ui, []byte("Stress-test Snake demo | rex-snake")},
		{mono, []byte("I inspected demo/snake.mjs and ran it")},
		{ui, []byte("日本語のテキスト")},
		{ui, []byte("مرحبا بالعالم")},
		{ui, []byte("Smile 😀 ok")},
	}
	// Warm: shape and rasterize everything at every subpixel offset.
	for _, it := range items {
		v := s.Run(s.Shape(it.f, it.b))
		for i, g := range v.Glyphs {
			for sub := uint8(0); sub < 4; sub++ {
				s.Glyph(v.Faces[i], g, sub)
			}
		}
	}
	// The shaping/atlas code itself never allocates on a hit (a regression
	// shows up in every window, thousands of objects each). The Go runtime can
	// still make a stray 16-32 byte allocation of its own in the background,
	// so the exact delta is measured over several windows and must be 0 in at
	// least one, which a real per-hit allocation can never satisfy.
	const iters, windows = 2000, 8
	sink, clean := 0, 0
	for w := 0; w < windows; w++ {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		for n := 0; n < iters; n++ {
			it := items[n%len(items)]
			r := s.Shape(it.f, it.b)
			v := s.Run(r)
			for i, g := range v.Glyphs {
				e := s.Glyph(v.Faces[i], g, uint8(n+i))
				sink += int(e.W)
			}
			sink += int(s.Font(FontDesc{SizePt: 13, Scale: 2, Weight: 400}))
		}
		runtime.ReadMemStats(&after)
		dm, db := after.Mallocs-before.Mallocs, after.TotalAlloc-before.TotalAlloc
		t.Logf("window %d: %d shape+run, ~%d glyph hits: Mallocs +%d, TotalAlloc +%d bytes", w, iters, iters*25, dm, db)
		if dm == 0 && db == 0 {
			clean++
		}
	}
	_ = sink
	if clean == 0 {
		t.Errorf("every window allocated: cache hits are not allocation-free")
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	s, err := New()
	if errors.Is(err, errNoFactory) {
		t.Skipf("DirectWrite unavailable: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	shapeStr(s, uiFont(s), "x")
	s.Glyph(0, 1, 0)
	s.Close()
	s.Close()
}
