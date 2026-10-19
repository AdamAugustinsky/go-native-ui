//go:build darwin

package text

import (
	"fmt"
	"math"
	"runtime"
	"sync"
	"unicode/utf8"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// Entry points resolved once with Dlopen/Dlsym. All calls go through
// ffi.Frame: no purego.RegisterFunc / SyscallN, which allocate.
var sy struct {
	// CoreFoundation
	cfRelease, cfRetain                         uintptr
	cfStringCreateWithBytes, cfStringGetCString uintptr
	cfDictionaryCreate, cfDictionaryGetValue    uintptr
	cfAttributedStringCreate                    uintptr
	cfAttrCreateMutable, cfAttrReplaceString    uintptr
	cfAttrSetAttributes, cfAttrSetAttribute     uintptr
	cfAttrGetLength                             uintptr
	cfAttrBeginEditing, cfAttrEndEditing        uintptr
	cfArrayGetCount, cfArrayGetValueAtIndex     uintptr
	cfNumberCreate                              uintptr
	cfNumberGetValue, cfNumberGetTypeID         uintptr
	cfGetTypeID, cfPreferencesCopyAppValue      uintptr
	kCurrentApplication                         uintptr // kCFPreferencesCurrentApplication
	keyCallBacks, valueCallBacks                uintptr // addresses of the callback structs
	// CoreText
	ctFontCreateUIFontForLanguage, ctFontCreateWithName  uintptr
	ctFontCreateWithFontDescriptor                       uintptr
	ctFontDescriptorCreateWithAttributes                 uintptr
	ctFontDescriptorCreateCopyWithAttributes             uintptr
	ctFontCopyFontDescriptor                             uintptr
	ctFontGetAscent, ctFontGetDescent, ctFontGetLeading  uintptr
	ctFontGetCapHeight, ctFontGetXHeight, ctFontGetSize  uintptr
	ctFontGetSymbolicTraits, ctFontCopyPostScriptName    uintptr
	ctFontGetBoundingRectsForGlyphs, ctFontDrawGlyphs    uintptr
	ctLineCreateWithAttributedString, ctLineGetGlyphRuns uintptr
	ctLineGetTypographicBounds                           uintptr
	ctRunGetGlyphCount, ctRunGetGlyphs                   uintptr
	ctRunGetPositions, ctRunGetAttributes                uintptr
	ctRunGetStringIndices, ctRunGetStatus                uintptr
	ctTypesetterCreate, ctTypesetterSuggestLineBreak     uintptr
	ctTypesetterSuggestClusterBreak, ctTypesetterLine    uintptr
	ctLineGetTrailingWhitespaceWidth                     uintptr
	ctParagraphStyleCreate                               uintptr
	ctFontGetUnderlinePosition, ctFontGetUnderlineThick  uintptr
	ctFontCreateCopyWithSymbolicTraits                   uintptr
	// CoreGraphics
	cgColorSpaceCreateDeviceGray, cgColorSpaceCreateDeviceRGB uintptr
	cgBitmapContextCreate                                     uintptr
	cgContextSetGrayFillColor                                 uintptr
	cgContextSaveGState, cgContextRestoreGState               uintptr
	cgContextScaleCTM                                         uintptr
	cgSetBool                                                 [8]uintptr // (ctx, bool) setters, see ctxFlags
	// constant CFStringRefs (values of the exported globals)
	kFontAttribute, kTraitsAttribute, kWeightTrait, kFamilyAttribute uintptr
	kDesignTrait, kDesignMonospaced                                  uintptr
	kParagraphStyle                                                  uintptr
}

var ctxFlags = [...]struct {
	name string
	on   bool
}{
	{"CGContextSetAllowsAntialiasing", true},
	{"CGContextSetShouldAntialias", true},
	{"CGContextSetAllowsFontSubpixelPositioning", true},
	{"CGContextSetShouldSubpixelPositionFonts", true},
	{"CGContextSetAllowsFontSubpixelQuantization", false},
	{"CGContextSetShouldSubpixelQuantizeFonts", false},
	{"CGContextSetAllowsFontSmoothing", true},
	{"CGContextSetShouldSmoothFonts", false}, // per glyph, see rasterize
}

// shouldSmooth is the index of CGContextSetShouldSmoothFonts in ctxFlags.
const shouldSmooth = 7

var (
	loadOnce sync.Once
	loadErr  error
)

const (
	encUTF8        = 0x08000100
	numFloat64     = 6 // kCFNumberFloat64Type
	numSInt64      = 4 // kCFNumberSInt64Type
	uiFontSystem   = 2 // kCTFontUIFontSystem
	traitColorGlph = 1 << 13
	traitItalic    = 1 << 0 // kCTFontItalicTrait
	bitmapAlphaPre = 1      // kCGImageAlphaPremultipliedLast
	bitmapAlpha    = 7      // kCGImageAlphaOnly
)

func loadSyms() error {
	loadOnce.Do(func() {
		open := func(p string) uintptr {
			h, err := purego.Dlopen(p, purego.RTLD_NOW|purego.RTLD_GLOBAL)
			if err != nil && loadErr == nil {
				loadErr = fmt.Errorf("text: %w", err)
			}
			return h
		}
		cf := open("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation")
		ct := open("/System/Library/Frameworks/CoreText.framework/CoreText")
		cg := open("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics")
		if loadErr != nil {
			return
		}
		sym := func(h uintptr, n string) uintptr {
			p, err := purego.Dlsym(h, n)
			if err != nil && loadErr == nil {
				loadErr = fmt.Errorf("text: %w", err)
			}
			return p
		}
		// The exported CFStringRef globals: read the pointer they hold.
		global := func(h uintptr, n string) uintptr {
			p := sym(h, n)
			if p == 0 {
				return 0
			}
			return *(*uintptr)(ffi.PtrFromC(p))
		}
		sy.cfRelease, sy.cfRetain = sym(cf, "CFRelease"), sym(cf, "CFRetain")
		sy.cfStringCreateWithBytes, sy.cfStringGetCString = sym(cf, "CFStringCreateWithBytes"), sym(cf, "CFStringGetCString")
		sy.cfDictionaryCreate, sy.cfDictionaryGetValue = sym(cf, "CFDictionaryCreate"), sym(cf, "CFDictionaryGetValue")
		sy.cfAttributedStringCreate = sym(cf, "CFAttributedStringCreate")
		sy.cfAttrCreateMutable, sy.cfAttrReplaceString = sym(cf, "CFAttributedStringCreateMutable"), sym(cf, "CFAttributedStringReplaceString")
		sy.cfAttrSetAttributes, sy.cfAttrSetAttribute = sym(cf, "CFAttributedStringSetAttributes"), sym(cf, "CFAttributedStringSetAttribute")
		sy.cfAttrGetLength = sym(cf, "CFAttributedStringGetLength")
		sy.cfAttrBeginEditing, sy.cfAttrEndEditing = sym(cf, "CFAttributedStringBeginEditing"), sym(cf, "CFAttributedStringEndEditing")
		sy.cfArrayGetCount, sy.cfArrayGetValueAtIndex = sym(cf, "CFArrayGetCount"), sym(cf, "CFArrayGetValueAtIndex")
		sy.cfNumberCreate = sym(cf, "CFNumberCreate")
		sy.cfNumberGetValue, sy.cfNumberGetTypeID = sym(cf, "CFNumberGetValue"), sym(cf, "CFNumberGetTypeID")
		sy.cfGetTypeID, sy.cfPreferencesCopyAppValue = sym(cf, "CFGetTypeID"), sym(cf, "CFPreferencesCopyAppValue")
		sy.kCurrentApplication = global(cf, "kCFPreferencesCurrentApplication")
		sy.keyCallBacks, sy.valueCallBacks = sym(cf, "kCFTypeDictionaryKeyCallBacks"), sym(cf, "kCFTypeDictionaryValueCallBacks")

		sy.ctFontCreateUIFontForLanguage, sy.ctFontCreateWithName = sym(ct, "CTFontCreateUIFontForLanguage"), sym(ct, "CTFontCreateWithName")
		sy.ctFontCreateWithFontDescriptor = sym(ct, "CTFontCreateWithFontDescriptor")
		sy.ctFontDescriptorCreateWithAttributes = sym(ct, "CTFontDescriptorCreateWithAttributes")
		sy.ctFontDescriptorCreateCopyWithAttributes = sym(ct, "CTFontDescriptorCreateCopyWithAttributes")
		sy.ctFontCopyFontDescriptor = sym(ct, "CTFontCopyFontDescriptor")
		sy.ctFontGetAscent, sy.ctFontGetDescent, sy.ctFontGetLeading = sym(ct, "CTFontGetAscent"), sym(ct, "CTFontGetDescent"), sym(ct, "CTFontGetLeading")
		sy.ctFontGetCapHeight, sy.ctFontGetXHeight, sy.ctFontGetSize = sym(ct, "CTFontGetCapHeight"), sym(ct, "CTFontGetXHeight"), sym(ct, "CTFontGetSize")
		sy.ctFontGetSymbolicTraits, sy.ctFontCopyPostScriptName = sym(ct, "CTFontGetSymbolicTraits"), sym(ct, "CTFontCopyPostScriptName")
		sy.ctFontGetBoundingRectsForGlyphs, sy.ctFontDrawGlyphs = sym(ct, "CTFontGetBoundingRectsForGlyphs"), sym(ct, "CTFontDrawGlyphs")
		sy.ctLineCreateWithAttributedString, sy.ctLineGetGlyphRuns = sym(ct, "CTLineCreateWithAttributedString"), sym(ct, "CTLineGetGlyphRuns")
		sy.ctLineGetTypographicBounds = sym(ct, "CTLineGetTypographicBounds")
		sy.ctRunGetGlyphCount, sy.ctRunGetGlyphs = sym(ct, "CTRunGetGlyphCount"), sym(ct, "CTRunGetGlyphs")
		sy.ctRunGetPositions, sy.ctRunGetAttributes = sym(ct, "CTRunGetPositions"), sym(ct, "CTRunGetAttributes")
		sy.ctRunGetStringIndices, sy.ctRunGetStatus = sym(ct, "CTRunGetStringIndices"), sym(ct, "CTRunGetStatus")
		sy.ctTypesetterCreate = sym(ct, "CTTypesetterCreateWithAttributedString")
		sy.ctTypesetterSuggestLineBreak = sym(ct, "CTTypesetterSuggestLineBreak")
		sy.ctTypesetterSuggestClusterBreak = sym(ct, "CTTypesetterSuggestClusterBreak")
		sy.ctTypesetterLine = sym(ct, "CTTypesetterCreateLine")
		sy.ctLineGetTrailingWhitespaceWidth = sym(ct, "CTLineGetTrailingWhitespaceWidth")
		sy.ctParagraphStyleCreate = sym(ct, "CTParagraphStyleCreate")
		sy.ctFontGetUnderlinePosition, sy.ctFontGetUnderlineThick = sym(ct, "CTFontGetUnderlinePosition"), sym(ct, "CTFontGetUnderlineThickness")
		sy.ctFontCreateCopyWithSymbolicTraits = sym(ct, "CTFontCreateCopyWithSymbolicTraits")

		sy.cgColorSpaceCreateDeviceGray, sy.cgColorSpaceCreateDeviceRGB = sym(cg, "CGColorSpaceCreateDeviceGray"), sym(cg, "CGColorSpaceCreateDeviceRGB")
		sy.cgBitmapContextCreate = sym(cg, "CGBitmapContextCreate")
		sy.cgContextSetGrayFillColor = sym(cg, "CGContextSetGrayFillColor")
		sy.cgContextSaveGState, sy.cgContextRestoreGState = sym(cg, "CGContextSaveGState"), sym(cg, "CGContextRestoreGState")
		sy.cgContextScaleCTM = sym(cg, "CGContextScaleCTM")
		for i, f := range ctxFlags {
			sy.cgSetBool[i] = sym(cg, f.name)
		}
		sy.kFontAttribute, sy.kTraitsAttribute = global(ct, "kCTFontAttributeName"), global(ct, "kCTFontTraitsAttribute")
		sy.kWeightTrait, sy.kFamilyAttribute = global(ct, "kCTFontWeightTrait"), global(ct, "kCTFontFamilyNameAttribute")
		sy.kDesignTrait, sy.kDesignMonospaced = global(ct, "kCTFontUIFontDesignTrait"), global(ct, "kCTFontUIFontDesignMonospaced")
		sy.kParagraphStyle = global(ct, "kCTParagraphStyleAttributeName")
	})
	return loadErr
}

// fontEnt is a created font.
//
// The CTFont is created at the point size, not at SizePt*Scale pixels:
// CoreText picks the optical size (the opsz axis of SF) and the tracking
// from the font size, so a 13 pt font made at 26 px on a 2x display would be
// set in SF's 26 pt design and spacing, tighter and thinner than every
// AppKit label. AppKit itself shapes in points and rasterises through a
// scaled context; so do we: CoreText's points are multiplied by Scale for the
// pixel outputs (metrics, pen positions, advances) and glyphs are drawn
// through a Scale CTM (rasterize). The fallback fonts CoreText picks in the
// cascade are then point-sized too.
type fontEnt struct {
	desc    FontDesc
	font    uintptr // CTFontRef at desc.SizePt (+1)
	attrs   uintptr // CFDictionary {kCTFontAttributeName: font} (+1), for attributed strings
	face    FaceID
	metrics Metrics
}

// faceEnt is a concrete face: the primary font of a FontKey or a fallback,
// at one pixel scale.
type faceEnt struct {
	font  uintptr // CTFontRef (+1), point-sized
	color bool
	name  string  // PostScript name
	scale float32 // pixels per point the face is rasterised at
}

// bitmap is a reusable CGBitmapContext with its backing memory (Go-owned,
// pointer-free, never moved by the GC).
type bitmap struct {
	ctx, cs      uintptr
	buf          []byte
	w, h, stride int
}

// System is the macOS text system. See the package comment for threading.
type System struct {
	fr *ffi.Frame // this System's call frame; one System per thread

	fonts      []fontEnt
	faces      []faceEnt
	faceByName map[string]FaceID

	runs  *runCache
	atlas *atlas
	paras *paraCache
	pl    ctPara // paragraph layout state (para_darwin.go)

	gray, rgba bitmap

	// Scratch in long-lived heap memory (C writes into it).
	tmp   []byte    // copy of the UTF-8 being shaped
	name  []byte    // face name / key build buffer
	cstr  [256]byte // CFStringGetCString out
	gbuf  []uint16
	xbuf  []float32
	fbuf  []FaceID
	cbuf  []uint32  // cluster per glyph (RunView.Clusters) of the line being shaped
	pos   []float64 // CTRunGetPositions output, x,y pairs
	idx   []int64   // CTRunGetStringIndices output (CFIndex, UTF-16 units)
	u16b  []uint32  // byte offset in the source of each UTF-16 unit of the CFString, plus the length
	san   []byte    // the source with every invalid byte replaced by U+FFFD
	keys  [2]uintptr
	vals  [2]uintptr
	num   [1]float64
	gid   [1]uint16
	pt    [2]float64
	rect  [4]float64 // bounding rect out
	ret   [4]float64 // amd64 hidden CGRect return, unused
	three [3]float64 // ascent/descent/leading out

	closed bool
}

// New loads CoreText and returns an empty System.
func New() (*System, error) {
	if err := loadSyms(); err != nil {
		return nil, err
	}
	s := &System{
		fr:         new(ffi.Frame),
		faceByName: make(map[string]FaceID),
		runs:       newRunCache(),
		atlas:      newAtlas(),
		paras:      newParaCache(),
	}
	s.tmp = make([]byte, 0, 256)
	s.name = make([]byte, 0, 256)
	s.gbuf, s.xbuf, s.fbuf = make([]uint16, 0, 128), make([]float32, 0, 128), make([]FaceID, 0, 128)
	s.cbuf = make([]uint32, 0, 128)
	s.pos = make([]float64, 256)
	s.idx = make([]int64, 128)
	return s, nil
}

// ---- CoreFoundation helpers ----

func (s *System) release(cf uintptr) {
	if cf != 0 {
		s.fr.Call1(sy.cfRelease, cf)
	}
}

// poolPush opens an autorelease pool around a CoreText call sequence.
// CoreText autoreleases internal Objective-C objects (about 1 KB per
// font-fallback shape, measured); without a pool they leak on threads that
// don't run an AppKit event loop. The pool is bound to the OS thread, so the
// goroutine is pinned until poolPop.
func (s *System) poolPush() uintptr {
	runtime.LockOSThread()
	return s.fr.Call0(objc.PoolPush)
}

func (s *System) poolPop(p uintptr) {
	s.fr.Call1(objc.PoolPop, p)
	runtime.UnlockOSThread()
}

func (s *System) retain(cf uintptr) { s.fr.Call1(sy.cfRetain, cf) }

// cfString creates a CFString from UTF-8 bytes; 0 if they're invalid UTF-8.
// The bytes are copied to heap scratch first so C never sees caller memory.
func (s *System) cfString(b []byte) uintptr {
	s.tmp = append(s.tmp[:0], b...)
	if len(s.tmp) == 0 {
		s.tmp = append(s.tmp, 0)
		b = s.tmp[:0]
	}
	p := unsafe.Pointer(&s.tmp[0])
	return s.fr.Reset().Arg(0).ArgPtr(p).Arg(uintptr(len(b))).Arg32(encUTF8).ArgBool(false).Call(sy.cfStringCreateWithBytes).R0()
}

func (s *System) dict1(k, v uintptr) uintptr {
	s.keys[0], s.vals[0] = k, v
	return s.dictN(1)
}

// dictN creates a CFDictionary from the first n entries of s.keys/s.vals.
func (s *System) dictN(n int) uintptr {
	return s.fr.Reset().Arg(0).ArgPtr(unsafe.Pointer(&s.keys[0])).ArgPtr(unsafe.Pointer(&s.vals[0])).
		Arg(uintptr(n)).Arg(sy.keyCallBacks).Arg(sy.valueCallBacks).Call(sy.cfDictionaryCreate).R0()
}

func (s *System) number(v float64) uintptr {
	s.num[0] = v
	return s.fr.Reset().Arg(0).Arg32(numFloat64).ArgPtr(unsafe.Pointer(&s.num[0])).Call(sy.cfNumberCreate).R0()
}

func (s *System) f64(fn, obj uintptr) float64 { return s.fr.Reset().Arg(obj).Call(fn).F64(0) }

// cssWeightToCT maps a CSS weight to a CoreText weight trait (NSFontWeight*).
func cssWeightToCT(w int) float64 {
	pts := [...]struct {
		css int
		ct  float64
	}{{100, -0.8}, {200, -0.6}, {300, -0.4}, {400, 0}, {500, 0.23}, {600, 0.3}, {700, 0.4}, {800, 0.56}, {900, 0.62}}
	if w <= 100 {
		return pts[0].ct
	}
	for i := 1; i < len(pts); i++ {
		if w <= pts[i].css {
			a, b := pts[i-1], pts[i]
			return a.ct + (b.ct-a.ct)*float64(w-a.css)/float64(b.css-a.css)
		}
	}
	return pts[len(pts)-1].ct
}

// traitsAttrs returns the attributes dictionary {traits: {weight, design}}
// (+1). mono selects the system monospaced design.
func (s *System) traitsAttrs(weight int, mono bool) uintptr {
	n := s.number(cssWeightToCT(weight))
	s.keys[0], s.vals[0] = sy.kWeightTrait, n
	cnt := 1
	if mono {
		s.keys[1], s.vals[1] = sy.kDesignTrait, sy.kDesignMonospaced
		cnt = 2
	}
	traits := s.dictN(cnt)
	s.release(n)
	attrs := s.dict1(sy.kTraitsAttribute, traits)
	s.release(traits)
	return attrs
}

// ---- Fonts ----

// Font returns the key of a font, creating it on first use. Fonts live until
// Close. A family that doesn't exist silently resolves to CoreText's default.
func (s *System) Font(d FontDesc) FontKey {
	d = normalizeDesc(d)
	for i := range s.fonts {
		if s.fonts[i].desc == d {
			return FontKey(i)
		}
	}
	if len(s.fonts) >= math.MaxUint16 {
		return 0
	}
	pool := s.poolPush()
	defer s.poolPop(pool)
	pt := float64(d.SizePt)
	font := s.createFont(d, pt)
	if font == 0 {
		font = s.fr.Reset().Arg32(uiFontSystem).ArgF64(pt).Arg(0).Call(sy.ctFontCreateUIFontForLanguage).R0()
	}
	if d.Italic && font != 0 {
		// The italic of the same face (SF has true italics), as AppKit's
		// NSFontManager converts a font to have the italic trait. A family
		// without one keeps its upright face.
		if it := s.fr.Reset().Arg(font).ArgF64(0).Arg(0).Arg32(traitItalic).Arg32(traitItalic).Call(sy.ctFontCreateCopyWithSymbolicTraits).R0(); it != 0 {
			s.release(font)
			font = it
		}
	}
	fe := fontEnt{desc: d, font: font, attrs: s.dict1(sy.kFontAttribute, font)}
	fe.face = s.faceFor(font, d.Scale)
	sc := float64(d.Scale)
	fe.metrics = Metrics{
		Ascent:    float32(s.f64(sy.ctFontGetAscent, font) * sc),
		Descent:   float32(s.f64(sy.ctFontGetDescent, font) * sc),
		LineGap:   float32(s.f64(sy.ctFontGetLeading, font) * sc),
		CapHeight: float32(s.f64(sy.ctFontGetCapHeight, font) * sc),
		XHeight:   float32(s.f64(sy.ctFontGetXHeight, font) * sc),

		UnderlinePos:       float32(-s.f64(sy.ctFontGetUnderlinePosition, font) * sc),
		UnderlineThickness: float32(s.f64(sy.ctFontGetUnderlineThick, font) * sc),
	}
	fe.metrics.LineHeight = fe.metrics.Ascent + fe.metrics.Descent + fe.metrics.LineGap
	s.fonts = append(s.fonts, fe)
	key := FontKey(len(s.fonts) - 1)
	s.fonts[key].metrics.MonoAdvance = s.Run(s.Shape(key, []byte("0"))).Advance
	return key
}

// createFont creates the CTFont for d at size pt (points; see fontEnt).
func (s *System) createFont(d FontDesc, pt float64) uintptr {
	if d.Family != "" {
		fam := s.cfString([]byte(d.Family))
		if fam == 0 {
			return 0
		}
		defer s.release(fam)
		// {family, traits: {weight}}
		n := s.number(cssWeightToCT(d.Weight))
		traits := s.dict1(sy.kWeightTrait, n)
		s.release(n)
		s.keys[0], s.vals[0] = sy.kFamilyAttribute, fam
		s.keys[1], s.vals[1] = sy.kTraitsAttribute, traits
		attrs := s.dictN(2)
		s.release(traits)
		desc := s.fr.Call1(sy.ctFontDescriptorCreateWithAttributes, attrs)
		s.release(attrs)
		if desc == 0 {
			return 0
		}
		f := s.fr.Reset().Arg(desc).ArgF64(pt).Arg(0).Call(sy.ctFontCreateWithFontDescriptor).R0()
		s.release(desc)
		return f
	}
	base := s.fr.Reset().Arg32(uiFontSystem).ArgF64(pt).Arg(0).Call(sy.ctFontCreateUIFontForLanguage).R0()
	if base == 0 || (d.Weight == 400 && !d.Mono) {
		return base
	}
	// Derive the weight / monospaced design from the system font's descriptor.
	baseDesc := s.fr.Call1(sy.ctFontCopyFontDescriptor, base)
	attrs := s.traitsAttrs(d.Weight, d.Mono)
	desc := s.fr.Reset().Arg(baseDesc).Arg(attrs).Call(sy.ctFontDescriptorCreateCopyWithAttributes).R0()
	s.release(attrs)
	s.release(baseDesc)
	if desc == 0 {
		return base
	}
	f := s.fr.Reset().Arg(desc).ArgF64(pt).Arg(0).Call(sy.ctFontCreateWithFontDescriptor).R0()
	s.release(desc)
	if f == 0 {
		return base
	}
	s.release(base)
	return f
}

// Metrics returns the font's metrics in pixels.
func (s *System) Metrics(f FontKey) Metrics {
	if int(f) >= len(s.fonts) {
		return Metrics{}
	}
	return s.fonts[f].metrics
}

// ---- Faces ----

// faceFor maps a CTFont rasterised at scale to a FaceID, retaining new
// fonts. Identity is the CTFontRef pointer (cheap, exact while the font is
// retained) and otherwise PostScript name + size, with the scale.
func (s *System) faceFor(font uintptr, scale float32) FaceID {
	for i := range s.faces {
		if s.faces[i].font == font && s.faces[i].scale == scale {
			return FaceID(i)
		}
	}
	ps := s.fr.Call1(sy.ctFontCopyPostScriptName, font)
	s.name = s.name[:0]
	if ps != 0 {
		if s.fr.Reset().Arg(ps).ArgPtr(unsafe.Pointer(&s.cstr[0])).Arg(uintptr(len(s.cstr))).Arg32(encUTF8).Call(sy.cfStringGetCString).Bool() {
			n := 0
			for n < len(s.cstr) && s.cstr[n] != 0 {
				n++
			}
			s.name = append(s.name, s.cstr[:n]...)
		}
		s.release(ps)
	}
	size := float32(s.f64(sy.ctFontGetSize, font))
	nameLen := len(s.name)
	for _, v := range [2]float32{size, scale} {
		bits := math.Float32bits(v)
		s.name = append(s.name, byte(bits), byte(bits>>8), byte(bits>>16), byte(bits>>24))
	}
	if id, ok := s.faceByName[string(s.name)]; ok {
		return id
	}
	if len(s.faces) >= math.MaxUint16 {
		return 0
	}
	traits := uint32(s.fr.Call1(sy.ctFontGetSymbolicTraits, font))
	s.retain(font)
	s.faces = append(s.faces, faceEnt{
		font: font, color: traits&traitColorGlph != 0,
		name: string(s.name[:nameLen]), scale: scale,
	})
	id := FaceID(len(s.faces) - 1)
	s.faceByName[string(s.name)] = id
	return id
}

func (s *System) faceName(f FaceID) string {
	if int(f) >= len(s.faces) {
		return ""
	}
	return s.faces[f].name
}

// ---- Shaping ----

// Shape shapes utf8 with font f using CoreText (CTLine: bidi, ligatures,
// clusters, the OS font-fallback cascade) and returns a handle into the run
// cache. A hit allocates nothing. Every invalid UTF-8 byte is shaped as U+FFFD
// (one character, and one cluster offset).
func (s *System) Shape(f FontKey, utf8 []byte) RunRef {
	if int(f) >= len(s.fonts) {
		return 0
	}
	if r, ok := s.runs.lookup(f, utf8); ok {
		return r
	}
	return s.shapeMiss(f, utf8)
}

func (s *System) shapeMiss(f FontKey, b []byte) RunRef {
	pool := s.poolPush()
	defer s.poolPop(pool)
	fe := &s.fonts[f]
	s.gbuf, s.xbuf, s.fbuf, s.cbuf = s.gbuf[:0], s.xbuf[:0], s.fbuf[:0], s.cbuf[:0]
	var advance float32
	if len(b) > 0 {
		s.indexUTF16(b)
		str := s.cfString(b)
		if str == 0 {
			str = s.cfString(s.sanitized(b))
		}
		if str != 0 {
			as := s.fr.Reset().Arg(0).Arg(str).Arg(fe.attrs).Call(sy.cfAttributedStringCreate).R0()
			line := s.fr.Call1(sy.ctLineCreateWithAttributedString, as)
			runs := s.fr.Call1(sy.ctLineGetGlyphRuns, line)
			nruns := int(s.fr.Call1(sy.cfArrayGetCount, runs))
			for i := 0; i < nruns; i++ {
				run := s.fr.Call2(sy.cfArrayGetValueAtIndex, runs, uintptr(i))
				s.appendRun(run, fe.face, fe.desc.Scale)
			}
			advance = float32(s.fr.Reset().Arg(line).ArgPtr(unsafe.Pointer(&s.three[0])).
				ArgPtr(unsafe.Pointer(&s.three[1])).ArgPtr(unsafe.Pointer(&s.three[2])).
				Call(sy.ctLineGetTypographicBounds).F64(0) * float64(fe.desc.Scale))
			s.release(line)
			s.release(as)
			s.release(str)
		}
	}
	return s.runs.insert(f, b, s.gbuf, s.xbuf, s.fbuf, s.cbuf, advance)
}

// indexUTF16 fills s.u16b for b: the byte offset of every UTF-16 unit of the
// CFString made from it (both units of a surrogate pair map to the pair's
// first byte), then len(b). An invalid byte is one unit (U+FFFD, see
// sanitized).
func (s *System) indexUTF16(b []byte) {
	s.u16b = s.u16b[:0]
	for i := 0; i < len(b); {
		r, n := utf8.DecodeRune(b[i:])
		s.u16b = append(s.u16b, uint32(i))
		if r >= 0x10000 {
			s.u16b = append(s.u16b, uint32(i))
		}
		i += n
	}
	s.u16b = append(s.u16b, uint32(len(b)))
}

// sanitized returns b with every invalid byte replaced by U+FFFD, so that the
// CFString has one unit per invalid byte and indexUTF16 stays exact.
func (s *System) sanitized(b []byte) []byte {
	s.san = s.san[:0]
	for i := 0; i < len(b); {
		r, n := utf8.DecodeRune(b[i:])
		if r == utf8.RuneError && n == 1 {
			s.san = append(s.san, "\uFFFD"...)
		} else {
			s.san = append(s.san, b[i:i+n]...)
		}
		i += n
	}
	return s.san
}

// appendRun appends one CTRun's glyphs, pen positions (CoreText's points
// times scale), face and clusters.
func (s *System) appendRun(run uintptr, primary FaceID, scale float32) {
	n := int(s.fr.Call1(sy.ctRunGetGlyphCount, run))
	if n == 0 {
		return
	}
	face := primary
	attrs := s.fr.Call1(sy.ctRunGetAttributes, run)
	if attrs != 0 {
		if font := s.fr.Call2(sy.cfDictionaryGetValue, attrs, sy.kFontAttribute); font != 0 {
			face = s.faceFor(font, scale)
		}
	}
	g0 := len(s.gbuf)
	s.gbuf = append(s.gbuf, make([]uint16, n)...)
	if cap(s.pos) < 2*n {
		s.pos = make([]float64, 2*n)
	}
	s.pos = s.pos[:cap(s.pos)]
	if cap(s.idx) < n {
		s.idx = make([]int64, n)
	}
	s.idx = s.idx[:cap(s.idx)]
	// CFRange{0,0} is passed by value in two integer registers.
	s.fr.Reset().Arg(run).Arg(0).Arg(0).ArgPtr(unsafe.Pointer(&s.gbuf[g0])).Call(sy.ctRunGetGlyphs)
	s.fr.Reset().Arg(run).Arg(0).Arg(0).ArgPtr(unsafe.Pointer(&s.pos[0])).Call(sy.ctRunGetPositions)
	s.fr.Reset().Arg(run).Arg(0).Arg(0).ArgPtr(unsafe.Pointer(&s.idx[0])).Call(sy.ctRunGetStringIndices)
	var rtl uint32
	if uint32(s.fr.Call1(sy.ctRunGetStatus, run))&1 != 0 { // kCTRunStatusRightToLeft
		rtl = ClusterRTL
	}
	last := len(s.u16b) - 1
	for i := 0; i < n; i++ {
		s.xbuf = append(s.xbuf, float32(s.pos[2*i]*float64(scale)))
		s.fbuf = append(s.fbuf, face)
		u := int(s.idx[i])
		if u < 0 || u > last {
			u = last
		}
		s.cbuf = append(s.cbuf, s.u16b[u]|rtl)
	}
}

// Run returns the shaped run. The view is valid until the next eviction
// (see RunGeneration). The y positions CoreText reports are ignored: runs
// of one line share a baseline.
func (s *System) Run(r RunRef) RunView { return s.runs.view(r) }

// RunGeneration changes whenever the run cache evicts. Compare it before and
// after shaping a batch of strings: if it changed, earlier RunRefs and
// RunViews may be stale and must be re-shaped.
func (s *System) RunGeneration() uint32 { return s.runs.gen }

// ReserveRuns pre-sizes the run cache for up to runs shaped runs, glyphs
// glyphs and textBytes bytes of source text (clamped to MaxCachedRuns,
// MaxCachedGlyphs and MaxCachedTextBytes), so that shaping new strings until
// the cache holds that much, and every eviction after that, allocates no Go
// memory. It also sizes the shaping scratch for one string of up to
// min(textBytes, 64 KiB) bytes, so that shaping a string longer than any
// before (up to that size) allocates nothing either. It reserves the
// paragraph cache (Layout) too, with the budget ParaBudget{Paras: runs,
// Glyphs: glyphs, Text: textBytes} (see ReserveParas), since wrapped text is
// laid out there. Call it once at setup. Without it the caches
// grow by doubling.
func (s *System) ReserveRuns(runs, glyphs, textBytes int) {
	s.runs.reserve(runs, glyphs, textBytes)
	s.reserveScratch(min(textBytes, maxScratch))
	s.ReserveParas(ParaBudget{Paras: runs, Glyphs: glyphs, Text: textBytes})
}

// reserveScratch sizes the shaping scratch for a string of n bytes: its copy,
// its UTF-16 index and sanitized form, and its glyphs (at most one per UTF-16
// unit, so per byte).
func (s *System) reserveScratch(n int) {
	if cap(s.tmp) < n {
		s.tmp = make([]byte, 0, n)
	}
	if cap(s.san) < 3*n {
		s.san = make([]byte, 0, 3*n)
	}
	if cap(s.u16b) < n+1 {
		s.u16b = make([]uint32, 0, n+1)
	}
	if cap(s.gbuf) < n {
		s.gbuf = make([]uint16, 0, n)
		s.xbuf = make([]float32, 0, n)
		s.fbuf = make([]FaceID, 0, n)
		s.cbuf = make([]uint32, 0, n)
	}
	if cap(s.pos) < 2*n {
		s.pos = make([]float64, 2*n)
	}
	if cap(s.idx) < n {
		s.idx = make([]int64, n)
	}
}

// ---- Rasterization ----

// Glyph returns the atlas entry for a glyph, rasterizing it with CoreText on
// a miss. sub is a GlyphVariant: the x offset in quarter pixels and the
// font-smoothing level of the text colour (SmoothLevel); colour faces ignore
// both (their entry is the plain sub 0 bitmap). A hit allocates nothing.
//
// Entries are valid until AtlasGeneration changes.
func (s *System) Glyph(face FaceID, gid uint16, sub uint8) GlyphEntry {
	if int(face) >= len(s.faces) {
		return GlyphEntry{}
	}
	smooth := min(sub>>smoothShift, smoothLevels)
	sub &= 3
	if s.faces[face].color {
		sub, smooth = 0, 0
	}
	key := glyphKey(face, gid, sub|smooth<<smoothShift)
	if e, ok := s.atlas.glyphs.lookup(key); ok {
		return e
	}
	e := s.rasterize(face, gid, sub, smooth)
	s.atlas.glyphs.insert(key, e)
	return e
}

// smoothLevels is the number of CoreGraphics font-smoothing levels.
const smoothLevels = 4

// smoothGray is the grey fill each SmoothLevel draws with (1..4): the
// middle of the luminance band CoreGraphics maps to that level, so
// CoreGraphics picks the same stem dilation as for the real text colour.
var smoothGray = [smoothLevels + 1]float64{1, 0, 0.5371, 0.7354, 0.8808}

// SmoothLevel returns the font-smoothing level AppKit draws text of colour
// rgba (0xRRGGBBAA) with, for GlyphVariant: 0 when the user turned font
// smoothing off (AppleFontSmoothing = 0), otherwise 1 to 4.
//
// With font smoothing (on unless the user turns it off), CoreGraphics
// thickens glyph stems by an amount picked from the luminance of the fill
// colour, in four bands of the linear luminance Y: below 1/8, below 3/8,
// below 5/8, and above (measured on macOS 27.2: 13 pt SF stems at 2x are
// 2.23 px wide without smoothing, 2.58 with black text, 2.91 with white).
// AppKit labels are drawn that way, so ours are too.
func SmoothLevel(rgba uint32) uint8 {
	smoothPref.Do(readSmoothPref)
	if !smoothOn {
		return 0
	}
	y := 0.2126*srgbLinear[uint8(rgba>>24)] + 0.7152*srgbLinear[uint8(rgba>>16)] + 0.0722*srgbLinear[uint8(rgba>>8)]
	switch {
	case y < 0.125:
		return 1
	case y < 0.375:
		return 2
	case y < 0.625:
		return 3
	}
	return 4
}

var (
	smoothPref sync.Once
	smoothOn   bool
	srgbLinear [256]float64 // sRGB component to linear light
)

// readSmoothPref reads AppleFontSmoothing as AppKit does (the application's
// domain, then the global one): only an explicit 0 turns smoothing off.
func readSmoothPref() {
	for i := range srgbLinear {
		v := float64(i) / 255
		if v <= 0.04045 {
			srgbLinear[i] = v / 12.92
		} else {
			srgbLinear[i] = math.Pow((v+0.055)/1.055, 2.4)
		}
	}
	smoothOn = true
	if loadSyms() != nil || sy.cfPreferencesCopyAppValue == 0 {
		return
	}
	fr := new(ffi.Frame)
	key := fr.Reset().Arg(0).ArgPtr(unsafe.Pointer(unsafe.StringData(prefKey))).Arg(uintptr(len(prefKey))).
		Arg32(encUTF8).ArgBool(false).Call(sy.cfStringCreateWithBytes).R0()
	if key == 0 {
		return
	}
	v := fr.Call2(sy.cfPreferencesCopyAppValue, key, sy.kCurrentApplication)
	fr.Call1(sy.cfRelease, key)
	if v == 0 {
		return
	}
	if fr.Call1(sy.cfGetTypeID, v) == fr.Call0(sy.cfNumberGetTypeID) {
		n := new(int64)
		if fr.Reset().Arg(v).Arg(numSInt64).ArgPtr(unsafe.Pointer(n)).Call(sy.cfNumberGetValue).Bool() && *n == 0 {
			smoothOn = false
		}
	}
	fr.Call1(sy.cfRelease, v)
}

const prefKey = "AppleFontSmoothing"

// AtlasGeneration changes when the atlas is reset because it is full: every
// page is then cleared and all GlyphEntries are stale.
func (s *System) AtlasGeneration() uint32 { return s.atlas.gen }

// rasterize draws one glyph at quarter-pixel offset sub and, for outline
// glyphs, with font-smoothing level smooth (SmoothLevel; 0 is none).
func (s *System) rasterize(face FaceID, gid uint16, sub, smooth uint8) GlyphEntry {
	pool := s.poolPush()
	defer s.poolPop(pool)
	fc := &s.faces[face]
	s.gid[0] = gid
	// CGRect result: arm64 returns it in d0-d3, amd64 through a hidden
	// pointer. Both also fill rect[] (the per-glyph out array), which is what
	// we read.
	s.fr.Reset().SetIndirectResult(unsafe.Pointer(&s.ret[0])).Arg(fc.font).Arg32(0).
		ArgPtr(unsafe.Pointer(&s.gid[0])).ArgPtr(unsafe.Pointer(&s.rect[0])).Arg(1).
		Call(sy.ctFontGetBoundingRectsForGlyphs)
	// The face is point-sized and drawn through a scale CTM: the bounds in
	// pixels are the point bounds times the scale.
	sc := float64(fc.scale)
	bx, by, bw, bh := s.rect[0]*sc, s.rect[1]*sc, s.rect[2]*sc, s.rect[3]*sc
	if !(bw > 0 && bh > 0) || bw > PageSize || bh > PageSize || math.IsNaN(bx+by) {
		return GlyphEntry{}
	}
	sx := float64(sub) * 0.25
	// One extra pixel around the outline bounds for antialiasing and
	// smoothing spill, on top of the atlas's own padding.
	x0 := int(math.Floor(bx+sx)) - 1
	x1 := int(math.Ceil(bx+bw+sx)) + 1
	y0 := int(math.Floor(by)) - 1
	y1 := int(math.Ceil(by+bh)) + 1
	w, h := x1-x0, y1-y0
	if w+2*glyphPad > PageSize || h+2*glyphPad > PageSize {
		return GlyphEntry{}
	}
	bm := s.bitmapFor(fc.color, w, h)
	if bm == nil {
		return GlyphEntry{}
	}
	clear(bm.buf[:h*bm.stride])
	// Bitmap row 0 is the top of the context. The region of interest is the
	// top h rows, i.e. CG y in [bm.h-h, bm.h]; the baseline sits -y0 above
	// that region's bottom edge. The pen is given in the scaled user space.
	s.pt[0] = (sx - float64(x0)) / sc
	s.pt[1] = (float64(bm.h-h) - float64(y0)) / sc
	if !fc.color {
		// An alpha-only context: the bitmap is the coverage whatever the fill,
		// and the fill's grey selects the smoothing level's stem dilation.
		s.fr.Reset().Arg(bm.ctx).ArgBool(smooth > 0).Call(sy.cgSetBool[shouldSmooth])
		s.fr.Reset().Arg(bm.ctx).ArgF64(smoothGray[smooth]).ArgF64(1).Call(sy.cgContextSetGrayFillColor)
	}
	s.fr.Call1(sy.cgContextSaveGState, bm.ctx)
	s.fr.Reset().Arg(bm.ctx).ArgF64(sc).ArgF64(sc).Call(sy.cgContextScaleCTM)
	s.fr.Reset().Arg(fc.font).ArgPtr(unsafe.Pointer(&s.gid[0])).ArgPtr(unsafe.Pointer(&s.pt[0])).
		Arg(1).Arg(bm.ctx).Call(sy.ctFontDrawGlyphs)
	s.fr.Call1(sy.cgContextRestoreGState, bm.ctx)
	pg, ax, ay, ok := s.atlas.alloc(fc.color, w, h)
	if !ok {
		return GlyphEntry{}
	}
	s.atlas.write(pg, ax, ay, w, h, bm.buf, bm.stride)
	return GlyphEntry{
		Page: uint8(pg), Color: fc.color,
		X: uint16(ax), Y: uint16(ay), W: uint16(w), H: uint16(h),
		BearingX: float32(x0), BearingY: float32(y1),
	}
}

// bitmapFor returns a scratch bitmap context of at least w×h.
func (s *System) bitmapFor(color bool, w, h int) *bitmap {
	bm := &s.gray
	if color {
		bm = &s.rgba
	}
	if bm.ctx != 0 && bm.w >= w && bm.h >= h {
		return bm
	}
	s.freeBitmap(bm)
	cw, ch := 128, 128
	for cw < w {
		cw *= 2
	}
	for ch < h {
		ch *= 2
	}
	bpp, info, csFn := 1, uint32(bitmapAlpha), sy.cgColorSpaceCreateDeviceGray
	if color {
		bpp, info, csFn = 4, bitmapAlphaPre, sy.cgColorSpaceCreateDeviceRGB
	}
	bm.buf = make([]byte, cw*ch*bpp)
	bm.w, bm.h, bm.stride = cw, ch, cw*bpp
	bm.cs = s.fr.Call0(csFn)
	bm.ctx = s.fr.Reset().ArgPtr(unsafe.Pointer(&bm.buf[0])).Arg(uintptr(cw)).Arg(uintptr(ch)).Arg(8).
		Arg(uintptr(bm.stride)).Arg(bm.cs).Arg32(info).Call(sy.cgBitmapContextCreate).R0()
	if bm.ctx == 0 {
		s.freeBitmap(bm)
		return nil
	}
	for i := range ctxFlags {
		s.fr.Reset().Arg(bm.ctx).ArgBool(ctxFlags[i].on).Call(sy.cgSetBool[i])
	}
	return bm
}

func (s *System) freeBitmap(bm *bitmap) {
	s.release(bm.ctx)
	s.release(bm.cs)
	*bm = bitmap{}
}

// ---- Atlas access ----

// Pages returns the number of atlas pages allocated so far.
func (s *System) Pages() int { return len(s.atlas.pages) }

// PageInfo returns the size and kind (RGBA if color, else R8) of page p.
func (s *System) PageInfo(p int) (w, h int, color bool) { return s.atlas.pageInfo(p) }

// Dirty returns the region of page p changed since the last ClearDirty, for
// upload to the GPU. pixels starts at the region's top-left texel and ends at
// the last texel of its last row; row r starts at pixels[r*stride]. The
// slice aliases the page: copy or upload it before the next Glyph call.
func (s *System) Dirty(p int) (x, y, w, h int, pixels []byte, stride int, ok bool) {
	return s.atlas.dirtyRegion(p)
}

// ClearDirty marks page p as uploaded.
func (s *System) ClearDirty(p int) { s.atlas.clearDirty(p) }

// Close releases every CoreText/CoreGraphics object. The System must not be
// used afterwards.
func (s *System) Close() {
	if s.closed {
		return
	}
	s.closed = true
	for _, f := range s.fonts {
		s.release(f.attrs)
		s.release(f.font)
	}
	for _, f := range s.faces {
		s.release(f.font)
	}
	s.closePara()
	s.fonts, s.faces = nil, nil
	s.freeBitmap(&s.gray)
	s.freeBitmap(&s.rgba)
}
