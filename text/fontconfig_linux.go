//go:build linux

package text

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
)

// fontconfig through dlopen (libfontconfig.so.1), called with internal/ffi.
// It answers two questions for the text engine: which font file is the
// system "sans-serif" / "monospace" / a named family at a weight, and which
// installed font has a glyph for a character the primary font lacks.
//
// Discovery uses fontconfig's own cache (FcInitLoadConfigAndFonts maps the
// per-directory cache files the system maintains); this package never scans
// or stats font directories (D9). Matching is FcFontMatch for the primary
// font and FcFontSort (trimmed, so only fonts that add coverage) for the
// fallback order, which is the order the user's fontconfig rules produce.
// Coverage is checked on the FcCharSet in the cached pattern, so a candidate
// font file is only opened once it is known to have the character.

var fc struct {
	initLoadConfig                                      uintptr
	patternCreate, patternDestroy                       uintptr
	patternAddString, patternAddInteger                 uintptr
	patternAddDouble, patternAddBool                    uintptr
	configSubstitute, defaultSubstitute                 uintptr
	fontMatch, fontSort, fontSetDestroy                 uintptr
	patternGetString, patternGetInteger, patternGetBool uintptr
	patternGetCharSet, charSetHasChar                   uintptr
	oFamily, oWeight, oSlant, oPixelSize, oColor, oFile *byte // property names (NUL-terminated)
	oIndex, oVariable, oCharset, oFontFormat            *byte
	config                                              uintptr
	loadOnce, cfgOnce                                   sync.Once
	loadErr, cfgErr                                     error
}

func cname(s string) *byte { return &append([]byte(s), 0)[0] }

func loadFontconfig() error {
	fc.loadOnce.Do(func() {
		h, err := purego.Dlopen("libfontconfig.so.1", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			if h, err = purego.Dlopen("libfontconfig.so", purego.RTLD_NOW|purego.RTLD_GLOBAL); err != nil {
				fc.loadErr = fmt.Errorf("text: fontconfig not available: %w", err)
				return
			}
		}
		for _, s := range []struct {
			name string
			dst  *uintptr
		}{
			{"FcInitLoadConfigAndFonts", &fc.initLoadConfig},
			{"FcPatternCreate", &fc.patternCreate}, {"FcPatternDestroy", &fc.patternDestroy},
			{"FcPatternAddString", &fc.patternAddString}, {"FcPatternAddInteger", &fc.patternAddInteger},
			{"FcPatternAddDouble", &fc.patternAddDouble}, {"FcPatternAddBool", &fc.patternAddBool},
			{"FcConfigSubstitute", &fc.configSubstitute}, {"FcDefaultSubstitute", &fc.defaultSubstitute},
			{"FcFontMatch", &fc.fontMatch}, {"FcFontSort", &fc.fontSort}, {"FcFontSetDestroy", &fc.fontSetDestroy},
			{"FcPatternGetString", &fc.patternGetString}, {"FcPatternGetInteger", &fc.patternGetInteger},
			{"FcPatternGetBool", &fc.patternGetBool}, {"FcPatternGetCharSet", &fc.patternGetCharSet},
			{"FcCharSetHasChar", &fc.charSetHasChar},
		} {
			p, err := purego.Dlsym(h, s.name)
			if err != nil || p == 0 {
				fc.loadErr = fmt.Errorf("text: libfontconfig: missing symbol %s", s.name)
				return
			}
			*s.dst = p
		}
		fc.oFamily, fc.oWeight, fc.oSlant, fc.oPixelSize = cname("family"), cname("weight"), cname("slant"), cname("pixelsize")
		fc.oColor, fc.oFile, fc.oIndex, fc.oVariable = cname("color"), cname("file"), cname("index"), cname("variable")
		fc.oCharset, fc.oFontFormat = cname("charset"), cname("fontformat")
	})
	return fc.loadErr
}

// fcWeight maps a CSS weight to fontconfig's scale (FcWeightFromOpenType).
func fcWeight(css int) int {
	pts := [...]struct{ css, fc int }{{100, 0}, {200, 40}, {300, 50}, {350, 55}, {380, 75}, {400, 80},
		{500, 100}, {600, 180}, {700, 200}, {800, 205}, {900, 210}, {1000, 215}}
	if css <= pts[0].css {
		return pts[0].fc
	}
	for i := 1; i < len(pts); i++ {
		if css <= pts[i].css {
			a, b := pts[i-1], pts[i]
			return a.fc + (b.fc-a.fc)*(css-a.css)/(b.css-a.css)
		}
	}
	return pts[len(pts)-1].fc
}

// fcEntry is what the engine needs from one font pattern.
type fcEntry struct {
	ref     fontRef
	charset uintptr // FcCharSet*, valid while the owning set lives
}

type fcSetKey struct {
	family string
	mono   bool
	weight int
	italic bool
}

// fcSet is an FcFontSort result with its entries extracted.
type fcSet struct {
	set     uintptr
	entries []fcEntry
}

// fcFinder implements fontFinder. Single-threaded, like the System.
type fcFinder struct {
	fr   *ffi.Frame
	sets map[fcSetKey]*fcSet

	// Scratch handed to C by pointer: heap memory, never a goroutine stack.
	str    [1]uintptr // FcChar8** out
	cs     [1]uintptr // FcCharSet** out
	ival   [1]int32
	result [1]int32 // FcResult
	cbuf   []byte
	closed bool
}

func newFcFinder() (*fcFinder, error) {
	if err := loadFontconfig(); err != nil {
		return nil, err
	}
	return &fcFinder{fr: new(ffi.Frame), sets: make(map[fcSetKey]*fcSet), cbuf: make([]byte, 0, 256)}, nil
}

// config loads fontconfig's configuration and cached font set on first use.
func (f *fcFinder) config() (uintptr, error) {
	fc.cfgOnce.Do(func() {
		fc.config = f.fr.Call0(fc.initLoadConfig)
		if fc.config == 0 {
			fc.cfgErr = errors.New("text: FcInitLoadConfigAndFonts failed")
		}
	})
	return fc.config, fc.cfgErr
}

func (f *fcFinder) cstring(s string) *byte {
	f.cbuf = append(append(f.cbuf[:0], s...), 0)
	return &f.cbuf[0]
}

func (f *fcFinder) addString(pat uintptr, obj *byte, s string) {
	f.fr.Reset().Arg(pat).ArgPtr(unsafe.Pointer(obj)).ArgPtr(unsafe.Pointer(f.cstring(s))).Call(fc.patternAddString)
}

func (f *fcFinder) addInt(pat uintptr, obj *byte, v int) {
	f.fr.Reset().Arg(pat).ArgPtr(unsafe.Pointer(obj)).Arg32(uint32(v)).Call(fc.patternAddInteger)
}

// pattern builds a substituted pattern (+1; the caller destroys it).
func (f *fcFinder) pattern(cfg uintptr, family string, weight int, italic bool, px float32, color bool) uintptr {
	pat := f.fr.Call0(fc.patternCreate)
	if pat == 0 {
		return 0
	}
	f.addString(pat, fc.oFamily, family)
	f.addInt(pat, fc.oWeight, fcWeight(weight))
	slant := 0 // FC_SLANT_ROMAN
	if italic {
		slant = 100 // FC_SLANT_ITALIC
	}
	f.addInt(pat, fc.oSlant, slant)
	if px > 0 {
		f.fr.Reset().Arg(pat).ArgPtr(unsafe.Pointer(fc.oPixelSize)).ArgF64(float64(px)).Call(fc.patternAddDouble)
	}
	if color {
		f.fr.Reset().Arg(pat).ArgPtr(unsafe.Pointer(fc.oColor)).Arg32(1).Call(fc.patternAddBool)
	}
	f.fr.Reset().Arg(cfg).Arg(pat).Arg32(0).Call(fc.configSubstitute) // FcMatchPattern
	f.fr.Call1(fc.defaultSubstitute, pat)
	return pat
}

func familyOf(d FontDesc) string {
	switch {
	case d.Family != "":
		return d.Family
	case d.Mono:
		return "monospace"
	}
	return "sans-serif"
}

// goString copies a NUL-terminated C string.
func goString(p uintptr) string {
	if p == 0 {
		return ""
	}
	n := 0
	for *(*byte)(unsafe.Add(ffi.PtrFromC(p), n)) != 0 {
		n++
	}
	return string(unsafe.Slice((*byte)(ffi.PtrFromC(p)), n))
}

// entry extracts file, index, variable-ness and coverage from a pattern.
// ok is false for patterns without a file or in a format go-text cannot
// read (PCF, Type 1, ...).
func (f *fcFinder) entry(p uintptr) (e fcEntry, ok bool) {
	fr := f.fr
	if int32(fr.Reset().Arg(p).ArgPtr(unsafe.Pointer(fc.oFile)).Arg32(0).ArgPtr(unsafe.Pointer(&f.str[0])).Call(fc.patternGetString).R0()) != 0 {
		return e, false
	}
	e.ref.file = goString(f.str[0])
	if e.ref.file == "" {
		return e, false
	}
	f.str[0] = 0
	if int32(fr.Reset().Arg(p).ArgPtr(unsafe.Pointer(fc.oFontFormat)).Arg32(0).ArgPtr(unsafe.Pointer(&f.str[0])).Call(fc.patternGetString).R0()) == 0 {
		switch goString(f.str[0]) {
		case "TrueType", "CFF":
		default:
			return e, false
		}
	}
	f.ival[0] = 0
	if int32(fr.Reset().Arg(p).ArgPtr(unsafe.Pointer(fc.oIndex)).Arg32(0).ArgPtr(unsafe.Pointer(&f.ival[0])).Call(fc.patternGetInteger).R0()) == 0 {
		e.ref.index = int(f.ival[0] & 0xFFFF)
		e.ref.variable = f.ival[0]>>16 != 0 // a named instance of a variable font
	}
	f.ival[0] = 0
	if int32(fr.Reset().Arg(p).ArgPtr(unsafe.Pointer(fc.oVariable)).Arg32(0).ArgPtr(unsafe.Pointer(&f.ival[0])).Call(fc.patternGetBool).R0()) == 0 && f.ival[0] == 1 {
		e.ref.variable = true
	}
	f.cs[0] = 0
	if int32(fr.Reset().Arg(p).ArgPtr(unsafe.Pointer(fc.oCharset)).Arg32(0).ArgPtr(unsafe.Pointer(&f.cs[0])).Call(fc.patternGetCharSet).R0()) == 0 {
		e.charset = f.cs[0]
	}
	return e, true
}

// match returns the entry FcFontMatch picks for the pattern.
func (f *fcFinder) match(cfg, pat uintptr) (fcEntry, bool) {
	f.result[0] = 0
	m := f.fr.Reset().Arg(cfg).Arg(pat).ArgPtr(unsafe.Pointer(&f.result[0])).Call(fc.fontMatch).R0()
	if m == 0 {
		return fcEntry{}, false
	}
	defer f.fr.Call1(fc.patternDestroy, m)
	return f.entry(m)
}

func (f *fcFinder) primary(d FontDesc, px float32) (fontRef, bool) {
	cfg, err := f.config()
	if err != nil {
		return fontRef{}, false
	}
	pat := f.pattern(cfg, familyOf(d), d.Weight, d.Italic, px, false)
	if pat == 0 {
		return fontRef{}, false
	}
	defer f.fr.Call1(fc.patternDestroy, pat)
	if e, ok := f.match(cfg, pat); ok {
		return e.ref, true
	}
	// The best match is a format we cannot read: take the first readable
	// font in sort order.
	if s := f.sorted(d); s != nil && len(s.entries) > 0 {
		return s.entries[0].ref, true
	}
	return fontRef{}, false
}

func (f *fcFinder) emoji(d FontDesc, px float32) (fontRef, bool) {
	cfg, err := f.config()
	if err != nil {
		return fontRef{}, false
	}
	pat := f.pattern(cfg, "emoji", 400, false, px, true)
	if pat == 0 {
		return fontRef{}, false
	}
	defer f.fr.Call1(fc.patternDestroy, pat)
	if e, ok := f.match(cfg, pat); ok {
		return e.ref, true
	}
	return fontRef{}, false
}

// sorted returns the trimmed FcFontSort list for d, computed on first use.
func (f *fcFinder) sorted(d FontDesc) *fcSet {
	key := fcSetKey{d.Family, d.Mono, d.Weight, d.Italic}
	if s, ok := f.sets[key]; ok {
		return s
	}
	f.sets[key] = nil
	cfg, err := f.config()
	if err != nil {
		return nil
	}
	pat := f.pattern(cfg, familyOf(d), d.Weight, d.Italic, 0, false)
	if pat == 0 {
		return nil
	}
	defer f.fr.Call1(fc.patternDestroy, pat)
	f.result[0] = 0
	set := f.fr.Reset().Arg(cfg).Arg(pat).Arg32(1).Arg(0).ArgPtr(unsafe.Pointer(&f.result[0])).Call(fc.fontSort).R0()
	if set == 0 {
		return nil
	}
	s := &fcSet{set: set}
	// struct FcFontSet { int nfont; int sfont; FcPattern **fonts; }
	n := int(*(*int32)(ffi.PtrFromC(set)))
	fonts := *(*uintptr)(unsafe.Add(ffi.PtrFromC(set), 8))
	if n > 0 && fonts != 0 {
		for _, p := range unsafe.Slice((*uintptr)(ffi.PtrFromC(fonts)), n) {
			if e, ok := f.entry(p); ok {
				s.entries = append(s.entries, e)
			}
		}
	}
	f.sets[key] = s
	return s
}

func (f *fcFinder) fallback(d FontDesc, px float32, r rune) (fontRef, bool) {
	s := f.sorted(d)
	if s == nil {
		return fontRef{}, false
	}
	for i := range s.entries {
		e := &s.entries[i]
		if e.charset != 0 && f.fr.Reset().Arg(e.charset).Arg32(uint32(r)).Call(fc.charSetHasChar).R0()&0xFFFFFFFF != 0 {
			return e.ref, true
		}
	}
	return fontRef{}, false
}

func (f *fcFinder) close() {
	if f.closed {
		return
	}
	f.closed = true
	for _, s := range f.sets {
		if s != nil {
			f.fr.Call1(fc.fontSetDestroy, s.set)
		}
	}
	f.sets = nil
}
