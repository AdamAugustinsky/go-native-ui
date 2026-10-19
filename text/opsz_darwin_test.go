//go:build darwin

package text

import (
	"math"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// These tests compare the System with the fonts AppKit itself uses, asked for
// through NSFont. A 13 pt label on a 2x display must have the optical size
// and tracking of SF at 13 pt (what AppKit draws), not of SF at 26 pt.

// AppKit is loaded before any CoreText object exists: NSFont's font-instance
// cache must not meet CTFonts created before UIFoundation's bridge class was
// registered (that raises an NSException inside +systemFontOfSize:). Real
// apps load AppKit in platform before text.
var appKit = func() uintptr {
	h, err := purego.Dlopen("/System/Library/Frameworks/AppKit.framework/AppKit", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		panic(err)
	}
	return h
}()

// emProbe is the string tools/appkitref measures (metrics.json emAdvanceProbe).
const emProbe = "Hamburgefonstiv 0123456789"

func ctSym(t *testing.T, lib, name string) uintptr {
	t.Helper()
	h, err := purego.Dlopen(lib, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		t.Fatal(err)
	}
	p, err := purego.Dlsym(h, name)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// nsFont is the NSFont AppKit uses for d at d.SizePt (autoreleased; call it
// inside a pool): +systemFontOfSize:weight:, +monospacedSystemFontOfSize:weight:
// or +fontWithName:size:.
func nsFont(s *System, d FontDesc) uintptr {
	d = normalizeDesc(d)
	cls := uintptr(objc.Cls("NSFont"))
	size, weight := float64(d.SizePt), cssWeightToCT(d.Weight)
	switch {
	case d.Family != "":
		return s.fr.Reset().Arg(cls).Arg(uintptr(objc.Sel("fontWithName:size:"))).Arg(uintptr(objc.Str(d.Family))).ArgF64(size).Call(objc.MsgSend).R0()
	case d.Mono:
		return s.fr.Reset().Arg(cls).Arg(uintptr(objc.Sel("monospacedSystemFontOfSize:weight:"))).ArgF64(size).ArgF64(weight).Call(objc.MsgSend).R0()
	case d.Weight != 400:
		return s.fr.Reset().Arg(cls).Arg(uintptr(objc.Sel("systemFontOfSize:weight:"))).ArgF64(size).ArgF64(weight).Call(objc.MsgSend).R0()
	}
	return s.fr.Reset().Arg(cls).Arg(uintptr(objc.Sel("systemFontOfSize:"))).ArgF64(size).Call(objc.MsgSend).R0()
}

// lineOf returns a CTLine (+1) of str set in font, with the context's fill
// colour as its foreground.
func lineOf(t *testing.T, s *System, font uintptr, str string) uintptr {
	t.Helper()
	fromCtx := *(*uintptr)(ffi.PtrFromC(ctSym(t, ctLib, "kCTForegroundColorFromContextAttributeName")))
	cfTrue := *(*uintptr)(ffi.PtrFromC(ctSym(t, cfLib, "kCFBooleanTrue")))
	s.keys[0], s.vals[0] = sy.kFontAttribute, font
	s.keys[1], s.vals[1] = fromCtx, cfTrue
	attrs := s.dictN(2)
	cs := s.cfString([]byte(str))
	as := s.fr.Reset().Arg(0).Arg(cs).Arg(attrs).Call(sy.cfAttributedStringCreate).R0()
	line := s.fr.Call1(sy.ctLineCreateWithAttributedString, as)
	s.release(as)
	s.release(cs)
	s.release(attrs)
	return line
}

const (
	ctLib = "/System/Library/Frameworks/CoreText.framework/CoreText"
	cfLib = "/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation"
	cgLib = "/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics"
)

// appKitWidth is the typographic width in points of str set in AppKit's
// font for d.
func appKitWidth(t *testing.T, s *System, d FontDesc, str string) float64 {
	t.Helper()
	pool := s.poolPush()
	defer s.poolPop(pool)
	f := nsFont(s, d)
	if f == 0 {
		t.Fatalf("no NSFont for %+v", d)
	}
	line := lineOf(t, s, f, str)
	defer s.release(line)
	return s.fr.Reset().Arg(line).Arg(0).Arg(0).Arg(0).Call(sy.ctLineGetTypographicBounds).F64(0)
}

// emTol is the tolerance on the advance per em, relative: float32 pen
// positions, nothing else. The optical-size error it catches is 0.6-8 %.
const emTol = 5e-4

func TestAdvancePerEmMatchesAppKit(t *testing.T) {
	s := newSys(t)
	check := func(d FontDesc, str string) {
		t.Helper()
		ref := appKitWidth(t, s, d, str) / float64(d.SizePt)
		got := float64(s.Run(s.Shape(s.Font(d), []byte(str))).Advance) / float64(d.SizePt*d.Scale)
		t.Logf("%-28q %+v: AppKit %.4f, ours %.4f /em", str, d, ref, got)
		if math.Abs(got-ref) > emTol*ref {
			t.Errorf("%q %+v: advance per em %.4f, AppKit's %.4f (%+.2f %%)", str, d, got, ref, 100*(got/ref-1))
		}
	}
	for _, pt := range []float32{9, 11, 13, 15, 17, 22, 26} {
		for _, sc := range []float32{1, 2} {
			check(FontDesc{SizePt: pt, Scale: sc}, emProbe)
		}
	}
	for _, sc := range []float32{1, 2} {
		for _, d := range []FontDesc{
			{SizePt: 13, Weight: 700},
			{SizePt: 11, Weight: 600},
			{SizePt: 13, Mono: true},
			{Family: "Menlo", SizePt: 11},
			{Family: "Helvetica Neue", SizePt: 12},
		} {
			d.Scale = sc
			check(d, emProbe)
		}
		// Fallback faces in CoreText's cascade (not SF): Arabic, Hebrew, CJK,
		// colour emoji.
		for _, str := range []string{"مرحبا بالعالم", "שלום עולם", "日本語のテキスト", "Smile 😀😃 ok"} {
			check(FontDesc{SizePt: 13, Scale: sc}, str)
		}
	}
}

// TestScaleIsOnlyAPixelScale: at 2x (and 3x) the same text has the same glyphs,
// faces and layout as at 1x, multiplied by the scale, and the glyph bitmaps
// have the scaled size.
func TestScaleIsOnlyAPixelScale(t *testing.T) {
	s := newSys(t)
	strs := []string{emProbe, "Stress-test Snake demo | rex-snake", "abc مرحبا def", "日本語 and 😀", "office ﬁ AVA"}
	for _, d := range []FontDesc{{SizePt: 13}, {SizePt: 17, Weight: 700}, {SizePt: 11, Mono: true}, {Family: "Menlo", SizePt: 12}} {
		d1 := d
		d1.Scale = 1
		f1 := s.Font(d1)
		m1 := s.Metrics(f1)
		for _, sc := range []float32{2, 3} {
			dn := d
			dn.Scale = sc
			fn := s.Font(dn)
			mn := s.Metrics(fn)
			for _, p := range [][2]float32{{m1.Ascent, mn.Ascent}, {m1.Descent, mn.Descent}, {m1.LineGap, mn.LineGap}, {m1.CapHeight, mn.CapHeight}, {m1.XHeight, mn.XHeight}, {m1.MonoAdvance, mn.MonoAdvance}} {
				if math.Abs(float64(p[1]-sc*p[0])) > 1e-3 {
					t.Errorf("%+v metrics at %vx %+v, at 1x %+v", dn, sc, mn, m1)
					break
				}
			}
			for _, str := range strs {
				a := s.Run(s.Shape(f1, []byte(str)))
				b := s.Run(s.Shape(fn, []byte(str)))
				if len(a.Glyphs) != len(b.Glyphs) || math.Abs(float64(b.Advance-sc*a.Advance)) > 1e-3 {
					t.Errorf("%+v %q: %d glyphs, advance %v at 1x; %d, %v at %vx", dn, str, len(a.Glyphs), a.Advance, len(b.Glyphs), b.Advance, sc)
					continue
				}
				for i := range a.Glyphs {
					if a.Glyphs[i] != b.Glyphs[i] || s.faceName(a.Faces[i]) != s.faceName(b.Faces[i]) ||
						a.Clusters[i] != b.Clusters[i] || math.Abs(float64(b.X[i]-sc*a.X[i])) > 1e-3 {
						t.Errorf("%+v %q glyph %d: 1x (%d %s %v), %vx (%d %s %v)", dn, str, i,
							a.Glyphs[i], s.faceName(a.Faces[i]), a.X[i], sc, b.Glyphs[i], s.faceName(b.Faces[i]), b.X[i])
						break
					}
				}
			}
		}
	}
	// Bitmaps: at every scale a glyph's ink covers its point outline bounds
	// times the scale, rounded out to whole pixels. (Comparing coverage
	// across scales is not exact: CoreGraphics' coverage is not linear in
	// area, and at 1x it shifts small glyphs vertically by up to 0.4 pt.)
	for _, d := range []FontDesc{{SizePt: 13}, {SizePt: 17, Weight: 700}, {SizePt: 11, Mono: true}, {Family: "Menlo", SizePt: 12}} {
		for _, sc := range []float32{1, 2, 3} {
			d.Scale = sc
			f := s.Font(d)
			for _, str := range strs {
				v := s.Run(s.Shape(f, []byte(str)))
				for i, g := range v.Glyphs {
					e := s.Glyph(v.Faces[i], g, 0)
					if e.W == 0 {
						continue
					}
					past, short := inkVsOutline(s, v.Faces[i], g, e)
					tolPast, tolShort := 1.25, 0.25 // the outward rounding; float slack
					if e.Color {
						tolShort = 2 // bitmap strikes, resampled
					}
					if past > tolPast || short > tolShort {
						t.Errorf("%+v %q glyph %d (%+v): ink %.2f px past and %.2f px short of the scaled outline bounds",
							d, str, i, e, past, short)
					}
				}
			}
		}
	}
}

// inkVsOutline compares the ink of e (alpha > 0) with the glyph's outline
// bounds in its point-sized font times the face's scale: how far, at most,
// the ink reaches past an edge and stops short of one, in pixels.
func inkVsOutline(s *System, face FaceID, gid uint16, e GlyphEntry) (past, short float64) {
	pg := s.atlas.pages[e.Page]
	c0, c1, r0, r1 := 1<<30, -1, 1<<30, -1
	for y := 0; y < int(e.H); y++ {
		for x := 0; x < int(e.W); x++ {
			if pg.pix[((int(e.Y)+y)*PageSize+int(e.X)+x)*pg.bpp+pg.bpp-1] > 0 {
				c0, c1, r0, r1 = min(c0, x), max(c1, x), min(r0, y), max(r1, y)
			}
		}
	}
	if c1 < 0 {
		return 0, math.Inf(1)
	}
	l, r := float64(e.BearingX)+float64(c0), float64(e.BearingX)+float64(c1+1)
	top, bot := float64(e.BearingY)-float64(r0), float64(e.BearingY)-float64(r1+1)
	s.gid[0] = gid
	s.fr.Reset().SetIndirectResult(unsafe.Pointer(&s.ret[0])).Arg(s.faces[face].font).Arg32(0).
		ArgPtr(unsafe.Pointer(&s.gid[0])).ArgPtr(unsafe.Pointer(&s.rect[0])).Arg(1).Call(sy.ctFontGetBoundingRectsForGlyphs)
	k := float64(s.faces[face].scale)
	rl, rb, rr, rt := s.rect[0]*k, s.rect[1]*k, (s.rect[0]+s.rect[2])*k, (s.rect[1]+s.rect[3])*k
	past = max(rl-l, r-rr, top-rt, rb-bot)
	short = max(l-rl, rr-r, rt-top, bot-rb)
	return
}

// TestRasterMatchesAppKitLine composites the System's glyphs for a 13 pt
// label at 2x and compares them with CoreText drawing the same string in
// AppKit's NSFont through a 2x context, as a Retina NSTextField does: the
// ink extent and the amount of ink must agree.
func TestRasterMatchesAppKitLine(t *testing.T) {
	s := newSys(t)
	const W, H, ox, base, sc = 512, 128, 10, 80, 2
	d := FontDesc{SizePt: 13, Scale: sc}

	// AppKit: CTLineDraw of the NSFont line, in the System's own scratch
	// context (same antialiasing and smoothing flags), before any glyph
	// is rasterised into it.
	setPos := ctSym(t, cgLib, "CGContextSetTextPosition")
	lineDraw := ctSym(t, ctLib, "CTLineDraw")
	ref := make([]byte, W*H)
	func() {
		pool := s.poolPush()
		defer s.poolPop(pool)
		bm := s.bitmapFor(false, W, H)
		if bm == nil || bm.w != W || bm.h != H {
			t.Fatalf("scratch bitmap %+v", bm)
		}
		clear(bm.buf)
		line := lineOf(t, s, nsFont(s, d), emProbe)
		s.fr.Call1(sy.cgContextSaveGState, bm.ctx)
		s.fr.Reset().Arg(bm.ctx).ArgF64(sc).ArgF64(sc).Call(sy.cgContextScaleCTM)
		s.fr.Reset().Arg(bm.ctx).ArgF64(ox / sc).ArgF64((H - base) / sc).Call(setPos)
		s.fr.Reset().Arg(line).Arg(bm.ctx).Call(lineDraw)
		// The text position is not graphics state: reset it for rasterize.
		s.fr.Reset().Arg(bm.ctx).ArgF64(0).ArgF64(0).Call(setPos)
		s.fr.Call1(sy.cgContextRestoreGState, bm.ctx)
		s.release(line)
		copy(ref, bm.buf[:W*H])
	}()

	ours := make([]byte, W*H)
	v := s.Run(s.Shape(s.Font(d), []byte(emProbe)))
	for i, g := range v.Glyphs {
		pen := ox + v.X[i]
		ix := float32(math.Floor(float64(pen)))
		sub := int((pen-ix)*4 + 0.5)
		if sub == 4 {
			sub, ix = 0, ix+1
		}
		e := s.Glyph(v.Faces[i], g, uint8(sub))
		if e.W == 0 {
			continue
		}
		pg := s.atlas.pages[e.Page]
		gx, gy := int(ix+e.BearingX), int(base-e.BearingY)
		for r := 0; r < int(e.H); r++ {
			for c := 0; c < int(e.W); c++ {
				o := &ours[(gy+r)*W+gx+c]
				*o = byte(min(255, int(*o)+int(pg.pix[(int(e.Y)+r)*PageSize+int(e.X)+c])))
			}
		}
	}

	ink := func(b []byte) (total float64, x0, x1 int) {
		x0, x1 = W, -1
		for y := 0; y < H; y++ {
			for x := 0; x < W; x++ {
				if p := b[y*W+x]; p > 0 {
					total += float64(p)
					if p >= 128 {
						x0, x1 = min(x0, x), max(x1, x)
					}
				}
			}
		}
		return
	}
	rt, rx0, rx1 := ink(ref)
	ot, ox0, ox1 := ink(ours)
	t.Logf("AppKit: ink %.0f, columns %d-%d; ours: ink %.0f (%+.2f %%), columns %d-%d", rt, rx0, rx1, ot, 100*(ot/rt-1), ox0, ox1)
	if rt == 0 {
		t.Fatal("the AppKit line has no ink")
	}
	if abs(ox0-rx0) > 1 || abs(ox1-rx1) > 1 {
		t.Errorf("ink spans columns %d-%d, AppKit's %d-%d", ox0, ox1, rx0, rx1)
	}
	// Quarter-pixel pen quantisation moves ink but does not change its amount.
	if math.Abs(ot/rt-1) > 0.01 {
		t.Errorf("ink %.0f, AppKit's %.0f (%+.2f %%)", ot, rt, 100*(ot/rt-1))
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
