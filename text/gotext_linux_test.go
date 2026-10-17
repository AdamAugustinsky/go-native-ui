//go:build linux

package text

import (
	"math"
	"os"
	"runtime"
	"testing"
	"time"
)

// These tests run the whole Linux stack against the machine's fontconfig.
// They need libfontconfig and at least one TrueType sans/monospace font
// (fonts-dejavu-core or fonts-liberation on Ubuntu runners). Tests that need
// a CJK or colour emoji font skip with the package name to install when
// fontconfig has none: fonts-noto-cjk, fonts-noto-color-emoji.

func newSys(t *testing.T) *System {
	t.Helper()
	t0 := time.Now()
	s, err := New()
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("fontconfig unavailable on CI: %v (apt install fontconfig fonts-dejavu-core)", err)
		}
		t.Skipf("fontconfig unavailable: %v", err)
	}
	t.Cleanup(s.Close)
	t.Logf("New: %v", time.Since(t0))
	return s
}

func uiFont(t *testing.T, s *System) FontKey {
	t.Helper()
	f := s.Font(FontDesc{SizePt: 13, Scale: 2, Weight: 400})
	skipNoFont(t, s, f)
	return f
}

func skipNoFont(t *testing.T, s *System, f FontKey) {
	t.Helper()
	if int(f) >= len(s.fonts) {
		if os.Getenv("CI") != "" {
			t.Fatal("fontconfig found no usable font on CI (apt install fonts-dejavu-core)")
		}
		t.Skip("fontconfig found no usable font")
	}
}

func TestFcShapeHello(t *testing.T) {
	s := newSys(t)
	t0 := time.Now()
	f := s.Font(FontDesc{SizePt: 13, Scale: 2, Weight: 400})
	t.Logf("first Font (fontconfig config + cache load + match + go-text load): %v", time.Since(t0))
	skipNoFont(t, s, f)
	t.Logf("UI face: %s", s.faceName(s.fonts[f].face))
	t0 = time.Now()
	v := eShape(s, f, "Hello")
	t.Logf("shape Hello: %v", time.Since(t0))
	if len(v.Glyphs) != 5 || len(v.X) != 5 || len(v.Faces) != 5 {
		t.Fatalf("got %d/%d/%d glyphs, want 5", len(v.Glyphs), len(v.X), len(v.Faces))
	}
	for i := 1; i < 5; i++ {
		if !(v.X[i] > v.X[i-1]) {
			t.Fatalf("X not strictly increasing: %v", v.X)
		}
	}
	if !(v.Advance > v.X[4]) || v.Glyphs[2] != v.Glyphs[3] {
		t.Fatalf("advance %v, glyphs %v", v.Advance, v.Glyphs)
	}
	for _, g := range v.Glyphs {
		if g == 0 {
			t.Fatalf("missing glyph in %v", v.Glyphs)
		}
	}
}

func TestFcMetrics(t *testing.T) {
	s := newSys(t)
	f := s.Font(FontDesc{SizePt: 13, Scale: 2})
	skipNoFont(t, s, f)
	m := s.Metrics(f)
	t.Logf("13pt@2x: %+v", m)
	if !(m.Ascent > 0 && m.Descent > 0 && m.CapHeight > 0 && m.XHeight > 0 && m.XHeight < m.CapHeight) {
		t.Fatalf("implausible metrics %+v", m)
	}
	if want := m.Ascent + m.Descent + m.LineGap; math.Abs(float64(m.LineHeight-want)) > 1e-4 {
		t.Fatalf("LineHeight %v != ascent+descent+gap %v", m.LineHeight, want)
	}
	if m.CapHeight < 14 || m.CapHeight > 23 {
		t.Fatalf("cap height %v px for a 26 px font", m.CapHeight)
	}
	if s.Metrics(FontKey(999)) != (Metrics{}) {
		t.Fatal("metrics for an unknown font")
	}
	m1 := s.Metrics(s.Font(FontDesc{SizePt: 13, Scale: 1}))
	if r := m.Ascent / m1.Ascent; math.Abs(float64(r)-2) > 0.05 {
		t.Fatalf("ascent ratio between scale 2 and 1 = %v", r)
	}
}

func TestFcFontDedupAndWeights(t *testing.T) {
	s := newSys(t)
	a := s.Font(FontDesc{SizePt: 13, Scale: 2})
	skipNoFont(t, s, a)
	if b := s.Font(FontDesc{SizePt: 13, Scale: 2, Weight: 400}); a != b {
		t.Fatalf("defaulted and explicit weight 400 are different fonts: %d %d", a, b)
	}
	faces := map[string]bool{}
	for _, w := range []int{400, 500, 600, 700} {
		k := s.Font(FontDesc{SizePt: 13, Scale: 2, Weight: w})
		n := s.faceName(s.fonts[k].face)
		t.Logf("weight %d -> %s (advance of 0: %v)", w, n, s.Metrics(k).MonoAdvance)
		faces[n] = true
	}
	// Installed weights vary by machine (DejaVu has two); bold must differ
	// from regular whenever the family has a bold face.
	reg := eShape(s, a, "Stress-test Snake demo")
	bold := eShape(s, s.Font(FontDesc{SizePt: 13, Scale: 2, Weight: 700}), "Stress-test Snake demo")
	if len(faces) > 1 && !(bold.Advance >= reg.Advance) {
		t.Fatalf("bold (%v) narrower than regular (%v)", bold.Advance, reg.Advance)
	}
	if len(faces) == 1 {
		t.Logf("only one face installed for this family: weights cannot be distinguished")
	}
	// A named family that exists everywhere through fontconfig aliases.
	h := s.Font(FontDesc{Family: "serif", SizePt: 13, Scale: 2})
	skipNoFont(t, s, h)
	t.Logf("serif -> %s", s.faceName(s.fonts[h].face))
}

func TestFcMonoAdvance(t *testing.T) {
	s := newSys(t)
	f := s.Font(FontDesc{Mono: true, SizePt: 13, Scale: 2})
	skipNoFont(t, s, f)
	m := s.Metrics(f)
	if m.MonoAdvance <= 0 {
		t.Fatalf("MonoAdvance = %v", m.MonoAdvance)
	}
	v := eShape(s, f, "iW.M0 lmw")
	t.Logf("mono face %s advance %v", s.faceName(v.Faces[0]), m.MonoAdvance)
	for i := 1; i < len(v.X); i++ {
		if d := v.X[i] - v.X[i-1]; math.Abs(float64(d-m.MonoAdvance)) > 0.05 {
			t.Fatalf("glyph %d advances %v, want %v (X=%v)", i-1, d, m.MonoAdvance, v.X)
		}
	}
	if p := eShape(s, uiFont(t, s), "iW"); p.X[1] == m.MonoAdvance {
		t.Fatal("proportional font looks monospaced")
	}
}

func TestFcFallbackCJK(t *testing.T) {
	s := newSys(t)
	f := uiFont(t, s)
	d := s.fonts[f].desc
	if _, ok := s.finder.fallback(d, s.fonts[f].px, '日'); !ok {
		t.Skip("fontconfig has no font with U+65E5 (apt install fonts-noto-cjk to run this test)")
	}
	primary := s.fonts[f].face
	t0 := time.Now()
	v := eShape(s, f, "日本語")
	t.Logf("shape CJK incl. fallback font load: %v", time.Since(t0))
	if len(v.Glyphs) != 3 {
		t.Fatalf("got %d glyphs for 日本語", len(v.Glyphs))
	}
	for i, face := range v.Faces {
		t.Logf("glyph %d id %d face %q (primary %q)", i, v.Glyphs[i], s.faceName(face), s.faceName(primary))
		if v.Glyphs[i] == 0 {
			t.Errorf("glyph %d is .notdef", i)
		}
	}
	if v.Faces[0] == primary && !s.has(primary, '日') {
		t.Fatal("CJK drawn with a primary font that lacks it")
	}
	m := eShape(s, f, "a日b")
	if len(m.Glyphs) != 3 || m.Faces[0] != primary || m.Faces[2] != primary {
		t.Fatalf("mixed run faces %v", m.Faces)
	}
	if !(m.X[1] > m.X[0] && m.X[2] > m.X[1]) {
		t.Fatalf("mixed X %v", m.X)
	}
	e := s.Glyph(v.Faces[0], v.Glyphs[0], 0)
	if e.W == 0 || sumBytes(glyphPixels(t, s, e)) == 0 {
		t.Fatalf("CJK glyph has no ink: %+v", e)
	}
}

func TestFcArabic(t *testing.T) {
	s := newSys(t)
	f := uiFont(t, s)
	v := eShape(s, f, "مرحبا")
	t.Logf("glyphs %v X %v advance %v face %q", v.Glyphs, v.X, v.Advance, s.faceName(v.Faces[0]))
	if len(v.Glyphs) < 1 || !(v.Advance > 0) {
		t.Fatalf("arabic: %d glyphs, advance %v", len(v.Glyphs), v.Advance)
	}
	for _, g := range v.Glyphs {
		if g == 0 {
			t.Skip("no installed font has Arabic glyphs (DejaVu Sans does: fonts-dejavu-core)")
		}
	}
	if m := eShape(s, f, "abc مرحبا def"); !(m.Advance > v.Advance) {
		t.Fatalf("mixed advance %v <= arabic-only %v", m.Advance, v.Advance)
	}
}

func TestFcInvalidUTF8AndEmpty(t *testing.T) {
	s := newSys(t)
	f := uiFont(t, s)
	if v := eShape(s, f, ""); len(v.Glyphs) != 0 || v.Advance != 0 {
		t.Fatalf("empty string: %+v", v)
	}
	if bad := s.Run(s.Shape(f, []byte{'a', 0xff, 0xfe, 'b'})); len(bad.Glyphs) < 3 || !(bad.Advance > 0) {
		t.Fatalf("invalid UTF-8 run: %+v", bad)
	}
}

func TestFcRasterA(t *testing.T) {
	s := newSys(t)
	f := uiFont(t, s)
	v := eShape(s, f, "A")
	t0 := time.Now()
	e := s.Glyph(v.Faces[0], v.Glyphs[0], 0)
	t.Logf("rasterize A at 26 px: %v", time.Since(t0))
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
	if e2 := s.Glyph(v.Faces[0], v.Glyphs[0], 0); e2 != e {
		t.Fatalf("second lookup %+v != %+v", e2, e)
	}
	if sp := eShape(s, f, " "); s.Glyph(sp.Faces[0], sp.Glyphs[0], 0).W != 0 {
		t.Fatal("space has ink")
	}
}

func TestFcEmojiIsColor(t *testing.T) {
	s := newSys(t)
	f := uiFont(t, s)
	if _, ok := s.emojiFace(f); !ok {
		t.Skip("no CBDT colour emoji font (apt install fonts-noto-color-emoji; COLRv1 builds are unsupported)")
	}
	t0 := time.Now()
	v := eShape(s, f, "😀")
	if len(v.Glyphs) != 1 {
		t.Fatalf("got %d glyphs", len(v.Glyphs))
	}
	t.Logf("emoji face %q, shape %v", s.faceName(v.Faces[0]), time.Since(t0))
	t0 = time.Now()
	e := s.Glyph(v.Faces[0], v.Glyphs[0], 2)
	t.Logf("emoji bitmap decode+scale: %v, entry %+v", time.Since(t0), e)
	if !e.Color || e.W == 0 || e.H == 0 {
		t.Fatalf("entry %+v", e)
	}
	if e != s.Glyph(v.Faces[0], v.Glyphs[0], 0) {
		t.Fatal("colour glyph variants differ across subpixel values")
	}
	px := glyphPixels(t, s, e)
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
	if int(e.H) < 18 || e.BearingY <= 0 {
		t.Fatalf("emoji geometry %+v", e)
	}
	// A ZWJ sequence and a flag are single clusters in the colour font.
	z := eShape(s, f, "👨‍👩‍👧")
	if len(z.Glyphs) < 1 {
		t.Fatal("ZWJ sequence produced no glyphs")
	}
	t.Logf("family ZWJ sequence -> %d glyph(s)", len(z.Glyphs))
}

// TestFcEmojiFontIsReadLazily is the D9 memory requirement against the real
// Noto Color Emoji: loading it and drawing a glyph must not read the image
// table (about 10 MB for the CBDT release) into the Go heap.
func TestFcEmojiFontIsReadLazily(t *testing.T) {
	s := newSys(t)
	f := uiFont(t, s)
	ref, ok := s.finder.emoji(s.fonts[f].desc, s.fonts[f].px)
	if !ok {
		t.Skip("no colour emoji font (apt install fonts-noto-color-emoji)")
	}
	st, err := os.Stat(ref.file)
	if err != nil || st.Size() < 4<<20 {
		t.Skipf("emoji font %s is small or unreadable", ref.file)
	}
	runtime.GC()
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	t0 := time.Now()
	v := eShape(s, f, "😀")
	e := s.Glyph(v.Faces[0], v.Glyphs[0], 0)
	runtime.ReadMemStats(&m1)
	alloc := m1.TotalAlloc - m0.TotalAlloc
	t.Logf("%s (%d MiB): load + shape + one bitmap: %v, %d KiB allocated, entry %+v", ref.file, st.Size()>>20, time.Since(t0), alloc>>10, e)
	if !e.Color {
		t.Skip("the installed emoji font has no CBDT bitmaps (COLRv1 variant)")
	}
	if alloc > uint64(st.Size())/2 {
		t.Fatalf("allocated %d MiB for a %d MiB font: the colour table was read in whole", alloc>>20, st.Size()>>20)
	}
}

func TestFcSubpixelVariants(t *testing.T) {
	s := newSys(t)
	v := eShape(s, uiFont(t, s), "l")
	var cent [4]float64
	for sub := uint8(0); sub < 4; sub++ {
		e := s.Glyph(v.Faces[0], v.Glyphs[0], sub)
		px := glyphPixels(t, s, e)
		var m, mx float64
		for i, p := range px {
			m += float64(p) * (float64(i%int(e.W)) + 0.5)
			mx += float64(p)
		}
		cent[sub] = float64(e.BearingX) + m/mx
	}
	for sub := 1; sub < 4; sub++ {
		if d := cent[sub] - cent[sub-1]; math.Abs(d-0.25) > 0.08 {
			t.Fatalf("centroid moved %.3f px between variants %d and %d, want 0.25", d, sub-1, sub)
		}
	}
}

func TestFcZeroAllocHits(t *testing.T) {
	s := newSys(t)
	ui := uiFont(t, s)
	mono := s.Font(FontDesc{Mono: true, SizePt: 13, Scale: 2, Weight: 500})
	skipNoFont(t, s, mono)
	type item struct {
		f FontKey
		b []byte
	}
	items := []item{
		{ui, []byte("Stress-test Snake demo | rex-snake")},
		{mono, []byte("I inspected demo/snake.mjs and ran it")},
		{ui, []byte("مرحبا بالعالم")},
		{ui, []byte("日本語のテキスト")},
		{ui, []byte("Smile 😀 ok")},
	}
	for _, it := range items {
		v := s.Run(s.Shape(it.f, it.b))
		for i, g := range v.Glyphs {
			for sub := uint8(0); sub < 4; sub++ {
				s.Glyph(v.Faces[i], g, sub)
			}
		}
	}
	// See TestEngineZeroAllocHits: zero in at least one window proves it.
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
		t.Errorf("every window allocated: cache hits are not allocation-free")
	}
}

func TestFcCloseIsIdempotent(t *testing.T) {
	s, err := New()
	if err != nil {
		t.Skipf("fontconfig unavailable: %v", err)
	}
	if f := s.Font(FontDesc{SizePt: 13, Scale: 2}); int(f) < len(s.fonts) {
		eShape(s, f, "x")
	}
	s.Glyph(0, 1, 0)
	s.Close()
	s.Close()
}

func TestFcWeightMapping(t *testing.T) {
	for css, want := range map[int]int{100: 0, 300: 50, 400: 80, 500: 100, 600: 180, 700: 200, 900: 210, 1000: 215, 50: 0, 450: 90} {
		if got := fcWeight(css); got != want {
			t.Errorf("fcWeight(%d) = %d, want %d", css, got, want)
		}
	}
}
