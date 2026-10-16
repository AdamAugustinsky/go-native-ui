//go:build darwin

// Throwaway phase-0 probe: CoreText shaping + fallback + glyph rasterization
// speed and memory through purego (no cgo). Compare with go-text numbers.
//
//	go run ./probes/coretext
package main

import (
	"fmt"
	"runtime"
	"strconv"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

type CFRange struct{ Loc, Len int }
type CGPoint struct{ X, Y float64 }
type CGRect struct{ X, Y, W, H float64 }

var (
	cfStringCreateWithBytes   func(alloc uintptr, b *byte, n int, enc uint32, ext bool) uintptr
	cfDictionaryCreate        func(alloc uintptr, keys, vals *uintptr, n int, kcb, vcb uintptr) uintptr
	cfAttributedStringCreate  func(alloc, str, attrs uintptr) uintptr
	cfRelease                 func(uintptr)
	cfArrayGetCount           func(uintptr) int
	cfArrayGetValueAtIndex    func(uintptr, int) uintptr
	cfDictionaryGetValue      func(uintptr, uintptr) uintptr
	cfStringGetCString        func(s uintptr, buf *byte, n int, enc uint32) bool
	ctFontCreateWithName      func(name uintptr, size float64, m uintptr) uintptr
	ctFontCopyFamilyName      func(uintptr) uintptr
	ctLineCreateWithAttrStr   func(uintptr) uintptr
	ctLineGetGlyphRuns        func(uintptr) uintptr
	ctRunGetGlyphCount        func(uintptr) int
	ctRunGetGlyphs            func(run uintptr, r CFRange, out *uint16)
	ctRunGetPositions         func(run uintptr, r CFRange, out *CGPoint)
	ctRunGetAttributes        func(uintptr) uintptr
	ctFontDrawGlyphs          func(font uintptr, g *uint16, p *CGPoint, n int, ctx uintptr)
	cgColorSpaceCreateGray    func() uintptr
	cgBitmapContextCreate     func(data unsafe.Pointer, w, h, bpc, bpr int, cs uintptr, info uint32) uintptr
	cgContextSetGrayFillColor func(ctx uintptr, g, a float64)
	cgContextFillRect         func(ctx uintptr, r CGRect)

	kCTFontAttributeName uintptr
	kCFTypeDictKeyCB     uintptr
	kCFTypeDictValCB     uintptr
)

func load() {
	cf, _ := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW)
	ct, _ := purego.Dlopen("/System/Library/Frameworks/CoreText.framework/CoreText", purego.RTLD_NOW)
	cg, _ := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW)
	reg := func(fp any, h uintptr, name string) { purego.RegisterLibFunc(fp, h, name) }
	reg(&cfStringCreateWithBytes, cf, "CFStringCreateWithBytes")
	reg(&cfDictionaryCreate, cf, "CFDictionaryCreate")
	reg(&cfAttributedStringCreate, cf, "CFAttributedStringCreate")
	reg(&cfRelease, cf, "CFRelease")
	reg(&cfArrayGetCount, cf, "CFArrayGetCount")
	reg(&cfArrayGetValueAtIndex, cf, "CFArrayGetValueAtIndex")
	reg(&cfDictionaryGetValue, cf, "CFDictionaryGetValue")
	reg(&cfStringGetCString, cf, "CFStringGetCString")
	reg(&ctFontCreateWithName, ct, "CTFontCreateWithName")
	reg(&ctFontCopyFamilyName, ct, "CTFontCopyFamilyName")
	reg(&ctLineCreateWithAttrStr, ct, "CTLineCreateWithAttributedString")
	reg(&ctLineGetGlyphRuns, ct, "CTLineGetGlyphRuns")
	reg(&ctRunGetGlyphCount, ct, "CTRunGetGlyphCount")
	reg(&ctRunGetGlyphs, ct, "CTRunGetGlyphs")
	reg(&ctRunGetPositions, ct, "CTRunGetPositions")
	reg(&ctRunGetAttributes, ct, "CTRunGetAttributes")
	reg(&ctFontDrawGlyphs, ct, "CTFontDrawGlyphs")
	reg(&cgColorSpaceCreateGray, cg, "CGColorSpaceCreateDeviceGray")
	reg(&cgBitmapContextCreate, cg, "CGBitmapContextCreate")
	reg(&cgContextSetGrayFillColor, cg, "CGContextSetGrayFillColor")
	reg(&cgContextFillRect, cg, "CGContextFillRect")
	sym := func(h uintptr, n string) uintptr { p, _ := purego.Dlsym(h, n); return p }
	kCTFontAttributeName = *(*uintptr)(unsafe.Pointer(sym(ct, "kCTFontAttributeName")))
	kCFTypeDictKeyCB = sym(cf, "kCFTypeDictionaryKeyCallBacks")
	kCFTypeDictValCB = sym(cf, "kCFTypeDictionaryValueCallBacks")
}

func cfstr(s string) uintptr {
	if len(s) == 0 {
		s = " "
	}
	return cfStringCreateWithBytes(0, unsafe.StringData(s), len(s), 0x08000100, false)
}

func goStr(cf uintptr) string {
	var buf [256]byte
	cfStringGetCString(cf, &buf[0], len(buf), 0x08000100)
	for i, c := range buf {
		if c == 0 {
			return string(buf[:i])
		}
	}
	return ""
}

var glyphs [512]uint16
var pos [512]CGPoint

// shape returns total glyphs and the family names of the runs.
func shape(attrs uintptr, s string, families *[]string) int {
	str := cfstr(s)
	as := cfAttributedStringCreate(0, str, attrs)
	line := ctLineCreateWithAttrStr(as)
	runs := ctLineGetGlyphRuns(line)
	total := 0
	for i, n := 0, cfArrayGetCount(runs); i < n; i++ {
		run := cfArrayGetValueAtIndex(runs, i)
		g := ctRunGetGlyphCount(run)
		ctRunGetGlyphs(run, CFRange{}, &glyphs[0])
		ctRunGetPositions(run, CFRange{}, &pos[0])
		total += g
		if families != nil {
			f := cfDictionaryGetValue(ctRunGetAttributes(run), kCTFontAttributeName)
			fam := ctFontCopyFamilyName(f)
			*families = append(*families, goStr(fam))
			cfRelease(fam)
		}
	}
	cfRelease(line)
	cfRelease(as)
	cfRelease(str)
	return total
}

func heapMB() float64 {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return float64(ms.HeapAlloc) / 1e6
}

func main() {
	t0 := time.Now()
	load()
	tLoad := time.Since(t0)
	t0 = time.Now()
	font := ctFontCreateWithName(cfstr(".AppleSystemUIFont"), 26, 0) // 13pt @2x
	keys := [1]uintptr{kCTFontAttributeName}
	vals := [1]uintptr{font}
	attrs := cfDictionaryCreate(0, &keys[0], &vals[0], 1, kCFTypeDictKeyCB, kCFTypeDictValCB)
	tFont := time.Since(t0)

	ascii := make([]string, 1000)
	for i := range ascii {
		ascii[i] = "IMG_" + strconv.Itoa(20240000+i*7) + "_final_v" + strconv.Itoa(i%9) + ".jpeg"
	}
	mixed := []string{
		"プロジェクト計画書_v2.docx", "项目计划书 最终版.pdf", "보고서 2026-10.xlsx",
		"تقرير المشروع النهائي.pdf", "family 👨‍👩‍👧 trip 🏖️.mov", "Résumé – Zoë Ångström.pages",
		"mixed English 日本語 العربية.txt", "😀😃😄😁😆.png", "Ελληνικά αρχεία.zip", "Привет мир.txt",
	}
	// Warm-up + first-use (fallback font loading) cost.
	t0 = time.Now()
	for _, s := range mixed {
		shape(attrs, s, nil)
	}
	tFirstMixed := time.Since(t0)
	for _, s := range ascii[:50] {
		shape(attrs, s, nil)
	}
	const reps = 5
	t0 = time.Now()
	for r := 0; r < reps; r++ {
		for _, s := range ascii {
			shape(attrs, s, nil)
		}
	}
	perASCII := time.Since(t0) / time.Duration(reps*len(ascii))
	t0 = time.Now()
	for r := 0; r < 100; r++ {
		for _, s := range mixed {
			shape(attrs, s, nil)
		}
	}
	perMixed := time.Since(t0) / time.Duration(100*len(mixed))

	for _, s := range mixed {
		var fams []string
		n := shape(attrs, s, &fams)
		fmt.Printf("  %-36q glyphs=%-3d runs=%v\n", s, n, fams)
	}

	// Rasterize glyphs one at a time into a 64x64 gray cell (atlas-style).
	cs := cgColorSpaceCreateGray()
	cell := make([]byte, 64*64)
	ctx := cgBitmapContextCreate(unsafe.Pointer(&cell[0]), 64, 64, 8, 64, cs, 0)
	n := shape(attrs, ascii[123]+ascii[456]+ascii[789], nil)
	t0 = time.Now()
	const rasterReps = 200
	one := [1]CGPoint{{8, 16}}
	for r := 0; r < rasterReps; r++ {
		for i := 0; i < n; i++ {
			cgContextSetGrayFillColor(ctx, 0, 1)
			cgContextFillRect(ctx, CGRect{0, 0, 64, 64})
			cgContextSetGrayFillColor(ctx, 1, 1)
			ctFontDrawGlyphs(font, &glyphs[i], &one[0], 1, ctx)
		}
	}
	perGlyph := time.Since(t0) / time.Duration(rasterReps*n)
	runtime.GC()
	fmt.Printf("dlopen+register %v | font create %v | first mixed pass (fallback loads) %v\n", tLoad, tFont, tFirstMixed)
	fmt.Printf("shape ASCII filename: %v/string | mixed (CJK/Arabic/emoji): %v/string | rasterize glyph 26px: %v/glyph (incl. 4 purego calls ~0.6us)\n",
		perASCII, perMixed, perGlyph)
	fmt.Printf("Go heap after all: %.1f MB (CoreText memory is outside the Go heap)\n", heapMB())
}
