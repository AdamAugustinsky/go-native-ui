//go:build windows

package text

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"syscall"
	"unsafe"

	"gonativeui/internal/com"
	"gonativeui/internal/win32"
)

// DirectWrite back end. Layout of the work:
//
//   - dwrite_windows.go (this file): loading, fonts, faces, metrics,
//     rasterization, atlas access.
//   - dwrite_shape_windows.go: Shape (IDWriteTextAnalyzer, font fallback,
//     bidi) and the Go-implemented COM objects DirectWrite calls back into.
//
// COM is called through hand-written vtable slots with internal/com (no cgo).
// Every slot below was extracted from the C `typedef struct <Interface>Vtbl`
// of the mingw-w64 headers (mingw-w64-headers/include/{dwrite,dwrite_2,
// dwrite_3}.h, master, fetched 2026-10-17; line numbers are those revision's)
// and the parameter list read from the same header. Slots are zero-based and
// include IUnknown (QueryInterface 0, AddRef 1, Release 2).
//
// IDWriteFactory (dwrite.h:5235-5371): GetSystemFontCollection 3,
// CreateTextAnalyzer 21, CreateGlyphRunAnalysis 23.
// IDWriteFactory2 (dwrite_2.h:2942-3136, derives IDWriteFactory1): the
// IDWriteFactory slots, then GetSystemFontFallback 26, TranslateColorGlyphRun
// 28 (the COLR v0 form with separate FLOAT origin parameters).
const (
	vtFactoryGetSystemFontCollection = 3
	vtFactoryCreateTextAnalyzer      = 21
	vtFactoryCreateGlyphRunAnalysis  = 23
	vtFactory2GetSystemFontFallback  = 26
	vtFactory2TranslateColorGlyphRun = 28
)

// IDWriteFontCollection (dwrite.h:2348-2374): GetFontFamilyCount 3,
// GetFontFamily 4, FindFamilyName 5.
// IDWriteFontFamily (dwrite.h:2209-2245): GetFamilyNames 6,
// GetFirstMatchingFont 7.
// IDWriteFont (dwrite.h:1942-1992): GetFontFamily 3, GetWeight 4,
// GetStretch 5, GetStyle 6, GetFaceNames 8, CreateFontFace 13.
// IDWriteFontFace (dwrite.h:1694-1781): GetFiles 4, GetIndex 5,
// GetSimulations 6, GetMetrics 8, TryGetFontTable 12, ReleaseFontTable 13.
// IDWriteFontFile (dwrite.h:1090-1111): GetReferenceKey 3.
// IDWriteLocalizedStrings (dwrite.h:1381-1418): GetCount 3, GetStringLength 7,
// GetString 8.
const (
	vtCollectionGetFontFamily  = 4
	vtCollectionFindFamilyName = 5
	vtFamilyGetFamilyNames     = 6
	vtFamilyGetFirstMatching   = 7
	vtFontGetFontFamily        = 3
	vtFontGetWeight            = 4
	vtFontGetStretch           = 5
	vtFontGetStyle             = 6
	vtFontGetFaceNames         = 8
	vtFontCreateFontFace       = 13
	vtFaceGetFiles             = 4
	vtFaceGetIndex             = 5
	vtFaceGetSimulations       = 6
	vtFaceGetMetrics           = 8
	vtFaceTryGetFontTable      = 12
	vtFaceReleaseFontTable     = 13
	vtFileGetReferenceKey      = 3
	vtStringsGetCount          = 3
	vtStringsGetStringLength   = 7
	vtStringsGetString         = 8
)

// IDWriteFontFallback (dwrite_2.h:428-440): MapCharacters 3.
// IDWriteTextAnalyzer (dwrite.h:4821-4902): AnalyzeScript 3, AnalyzeBidi 4,
// GetGlyphs 7, GetGlyphPlacements 8.
// IDWriteGlyphRunAnalysis (dwrite.h:5024-5048): GetAlphaTextureBounds 3,
// CreateAlphaTexture 4.
// IDWriteColorGlyphRunEnumerator (dwrite_2.h:2699-2715): MoveNext 3,
// GetCurrentRun 4.
const (
	vtFallbackMapCharacters  = 3
	vtAnalyzerAnalyzeScript  = 3
	vtAnalyzerAnalyzeBidi    = 4
	vtAnalyzerGetGlyphs      = 7
	vtAnalyzerGetPlacements  = 8
	vtAnalysisGetBounds      = 3
	vtAnalysisCreateTexture  = 4
	vtColorEnumMoveNext      = 3
	vtColorEnumGetCurrentRun = 4
)

// Enum values (dwrite.h, dcommon.h, mingw-w64 master).
const (
	dwFactoryShared    = 0 // DWRITE_FACTORY_TYPE_SHARED (dwrite.h:302)
	dwStretchNormal    = 5 // DWRITE_FONT_STRETCH_NORMAL (dwrite.h:352)
	dwStyleNormal      = 0 // DWRITE_FONT_STYLE_NORMAL (dwrite.h:360)
	dwMeasureNatural   = 0 // DWRITE_MEASURING_MODE_NATURAL (dcommon.h:43)
	dwRenderNaturalSym = 5 // DWRITE_RENDERING_MODE_NATURAL_SYMMETRIC (dwrite.h:408)
	dwTextureAliased   = 0 // DWRITE_TEXTURE_ALIASED_1x1 (dwrite.h:563)
	dwTextureCleartype = 1 // DWRITE_TEXTURE_CLEARTYPE_3x1 (dwrite.h:564)
	dwNoPaletteIndex   = 0xFFFF
	tagCOLR            = 'C' | 'O'<<8 | 'L'<<16 | 'R'<<24 // DWRITE_MAKE_OPENTYPE_TAG
	maxColorLayers     = 64
)

// Struct layouts. All of them are 64-bit-pointer layouts, identical on
// amd64 and arm64 (the sizes are asserted below).

// fontMetrics is DWRITE_FONT_METRICS (dwrite.h:566): 20 bytes.
type fontMetrics struct {
	UnitsPerEm             uint16
	Ascent, Descent        uint16
	LineGap                int16
	CapHeight, XHeight     uint16
	UnderlinePos           int16
	UnderlineThickness     uint16
	StrikethroughPos       int16
	StrikethroughThickness uint16
}

// glyphRun is DWRITE_GLYPH_RUN (dwrite.h:607): 48 bytes.
type glyphRun struct {
	FontFace   uintptr
	FontEmSize float32
	GlyphCount uint32
	Indices    uintptr // const UINT16*
	Advances   uintptr // const FLOAT*
	Offsets    uintptr // const DWRITE_GLYPH_OFFSET*
	IsSideways int32
	BidiLevel  uint32
}

// glyphOffset is DWRITE_GLYPH_OFFSET (dwrite.h:587).
type glyphOffset struct{ AdvanceOffset, AscenderOffset float32 }

// scriptAnalysis is DWRITE_SCRIPT_ANALYSIS (dwrite.h:706): UINT16 script and
// a 32-bit enum.
type scriptAnalysis struct {
	Script uint16
	Shapes uint32
}

// colorGlyphRun is DWRITE_COLOR_GLYPH_RUN (dwrite_2.h:160): the glyph run,
// its description pointer, the baseline origin, a DWRITE_COLOR_F (four
// FLOATs) and the palette index; 88 bytes.
type colorGlyphRun struct {
	Run              glyphRun
	Desc             uintptr
	OriginX, OriginY float32
	Color            [4]float32
	Palette          uint16
}

type winRect struct{ Left, Top, Right, Bottom int32 }

var (
	_ = [1]struct{}{}[unsafe.Sizeof(fontMetrics{})-20]
	_ = [1]struct{}{}[unsafe.Sizeof(glyphRun{})-48]
	_ = [1]struct{}{}[unsafe.Sizeof(glyphOffset{})-8]
	_ = [1]struct{}{}[unsafe.Sizeof(scriptAnalysis{})-8]
	_ = [1]struct{}{}[unsafe.Sizeof(colorGlyphRun{})-88]
	_ = [1]struct{}{}[unsafe.Offsetof(colorGlyphRun{}.OriginX)-56]
	_ = [1]struct{}{}[unsafe.Offsetof(colorGlyphRun{}.Color)-64]
	_ = [1]struct{}{}[unsafe.Offsetof(colorGlyphRun{}.Palette)-80]
	_ = [1]struct{}{}[unsafe.Sizeof(winRect{})-16]
)

// GUIDs (dwrite.h:4457, 4593, 5107; dwrite_2.h:2891).
var (
	iidUnknown    = win32.GUID{Data1: 0x00000000, Data2: 0x0000, Data3: 0x0000, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidFactory    = win32.GUID{Data1: 0xb859ee5a, Data2: 0xd838, Data3: 0x4b5b, Data4: [8]byte{0xa2, 0xe8, 0x1a, 0xdc, 0x7d, 0x93, 0xdb, 0x48}}
	iidFactory2   = win32.GUID{Data1: 0x0439fc60, Data2: 0xca44, Data3: 0x4994, Data4: [8]byte{0x8d, 0xee, 0x3a, 0x9a, 0xf7, 0xb7, 0x32, 0xec}}
	iidTextSource = win32.GUID{Data1: 0x688e1a58, Data2: 0x5094, Data3: 0x47c8, Data4: [8]byte{0xad, 0xc8, 0xfb, 0xce, 0xa6, 0x0a, 0xe9, 0x2b}}
	iidTextSink   = win32.GUID{Data1: 0x5810cd44, Data2: 0x0ca0, Data3: 0x4701, Data4: [8]byte{0xb3, 0xfa, 0xbe, 0xc5, 0x18, 0x2a, 0xe4, 0xf6}}
)

// System font families tried for the default fonts, in order. The system
// collection groups fonts into weight/stretch/style families, so one family
// answers every CSS weight.
var (
	uiFamilies   = []string{"Segoe UI Variable Text", "Segoe UI"}
	monoFamilies = []string{"Cascadia Mono", "Consolas", "Courier New"}
)

var dw struct {
	once          sync.Once
	err           error
	createFactory uintptr
}

// errNoFactory marks the failures that mean DirectWrite itself is unusable
// (as opposed to a missing font); tests skip on it.
var errNoFactory = errors.New("text: DirectWrite is unavailable")

func loadDWrite() error {
	dw.once.Do(func() {
		dll := win32.LoadSystem("dwrite.dll")
		dw.createFactory = win32.Proc(dll, "DWriteCreateFactory")
		if dw.createFactory == 0 {
			dw.err = fmt.Errorf("%w: cannot load dwrite.dll", errNoFactory)
			return
		}
		initCallbacks()
	})
	return dw.err
}

// fontEnt is a created font.
type fontEnt struct {
	desc    FontDesc
	family  []uint16 // NUL-terminated family name, the base for font fallback
	weight  uint32
	face    FaceID
	metrics Metrics
}

// faceEnt is a concrete face at a concrete size: the primary face of a
// FontKey or a fallback face.
type faceEnt struct {
	face   uintptr // IDWriteFontFace (+1)
	sizePx float32 // em size in pixels
	color  bool    // the font has a COLR table: glyphs may be colour
	name   string  // "Family Face", for diagnostics
}

// layer is one colour layer of a colour glyph during rasterization.
type layer struct {
	analysis uintptr
	rect     winRect
	color    [4]float32
}

// System is the Windows text system on DirectWrite. See the package comment
// for threading: one goroutine/OS thread per System (the shaping callbacks
// run on the calling thread).
type System struct {
	factory, factory2, fallback, analyzer, collection uintptr
	cf                                                com.Frame // float-argument calls (arm64 trampoline state)

	fonts     []fontEnt
	faces     []faceEnt
	faceByKey map[string]FaceID
	defFamily string
	runs      *runCache
	atlas     *atlas
	closed    bool

	// Shaping state, filled by callbacks and read by shapeText.
	src, sink comObject
	u16       []uint16
	levels    []uint8
	scripts   []scriptSpan
	paraRTL   bool
	locale    [8]uint16

	// Shaping scratch (heap, written by DirectWrite).
	cmap, tprops, gidx, gprop []uint16
	gadv                      []float32
	goff                      []glyphOffset
	items                     []shapeItem
	order                     []int32
	ig                        []uint16
	ia                        []float32
	io                        []float32
	ix                        []float32
	gbuf                      []uint16
	xbuf                      []float32
	fbuf                      []FaceID
	sa                        scriptAnalysis

	// Out parameters and small buffers the OS reads or writes through
	// pointers.
	out1, out2 uintptr
	n1, n2     uint32
	b1         int32
	mapLen     uint32
	scale      float32
	index      uint32
	files      [4]uintptr
	keyPtr     uintptr
	keySize    uint32
	tablePtr   uintptr
	tableSize  uint32
	tableCtx   uintptr
	name       []byte
	wname      []uint16
	metricsBuf fontMetrics

	// Rasterization.
	run    glyphRun
	gid    [1]uint16
	adv1   [1]float32
	off1   glyphOffset
	rect   winRect
	tex    []byte // CreateAlphaTexture output (3 bytes per pixel)
	cov    []byte
	rgba   []byte
	facc   []float32
	layers [maxColorLayers]layer
}

// New loads DirectWrite, creates the shared factory and resolves the default
// UI font. It fails on systems older than Windows 8.1 (no font fallback).
func New() (*System, error) {
	if err := loadDWrite(); err != nil {
		return nil, err
	}
	s := &System{
		faceByKey: make(map[string]FaceID),
		runs:      newRunCache(),
		atlas:     newAtlas(),
	}
	s.src = comObject{vtbl: uintptr(unsafe.Pointer(&srcVtbl[0])), sys: s}
	s.sink = comObject{vtbl: uintptr(unsafe.Pointer(&sinkVtbl[0])), sys: s}
	for i, c := range "en-us" {
		s.locale[i] = uint16(c)
	}
	s.name = make([]byte, 0, 512)
	s.gbuf, s.xbuf, s.fbuf = make([]uint16, 0, 128), make([]float32, 0, 128), make([]FaceID, 0, 128)

	if hr := win32.HR(win32.Call(dw.createFactory, dwFactoryShared, uintptr(unsafe.Pointer(&iidFactory)), uintptr(unsafe.Pointer(&s.out1)))); hr < 0 {
		return nil, fmt.Errorf("%w: %w", errNoFactory, hrError("DWriteCreateFactory", hr))
	}
	s.factory, s.out1 = s.out1, 0
	if hr := win32.QueryInterface(s.factory, &iidFactory2, &s.out1); hr < 0 {
		s.Close()
		return nil, hrError("QueryInterface(IDWriteFactory2) (Windows 8.1 or later is required)", hr)
	}
	s.factory2, s.out1 = s.out1, 0
	if hr := win32.HR(com.Call(s.factory2, vtFactory2GetSystemFontFallback, uintptr(unsafe.Pointer(&s.out1)))); hr < 0 {
		s.Close()
		return nil, hrError("GetSystemFontFallback", hr)
	}
	s.fallback, s.out1 = s.out1, 0
	if hr := win32.HR(com.Call(s.factory, vtFactoryCreateTextAnalyzer, uintptr(unsafe.Pointer(&s.out1)))); hr < 0 {
		s.Close()
		return nil, hrError("CreateTextAnalyzer", hr)
	}
	s.analyzer, s.out1 = s.out1, 0
	if hr := win32.HR(com.Call(s.factory, vtFactoryGetSystemFontCollection, uintptr(unsafe.Pointer(&s.out1)), 0)); hr < 0 {
		s.Close()
		return nil, hrError("GetSystemFontCollection", hr)
	}
	s.collection, s.out1 = s.out1, 0

	font, fam := s.findFont(uiFamilies, 400)
	if font == 0 {
		s.Close()
		return nil, errors.New("text: no system UI font (Segoe UI) found")
	}
	com.Release(font)
	s.defFamily = fam
	return s, nil
}

func hrError(what string, hr int32) error {
	return fmt.Errorf("text: %s failed: HRESULT %#08x", what, uint32(hr))
}

// ---- Fonts ----

// utf16z returns the NUL-terminated UTF-16 form of str in a scratch buffer
// (valid until the next utf16z call).
func (s *System) utf16z(str string) []uint16 {
	s.wname = s.wname[:0]
	for _, r := range str {
		if r >= 0x10000 {
			r -= 0x10000
			s.wname = append(s.wname, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3ff)))
		} else {
			s.wname = append(s.wname, uint16(r))
		}
	}
	s.wname = append(s.wname, 0)
	return s.wname
}

// findFont returns the IDWriteFont (+1) of the first installed family in
// names, matched to the CSS weight, and the family's name; 0 if none exists.
func (s *System) findFont(names []string, weight int) (font uintptr, family string) {
	for _, n := range names {
		p := &s.utf16z(n)[0]
		s.b1 = 0
		if hr := win32.HR(com.Call(s.collection, vtCollectionFindFamilyName, uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&s.index)), uintptr(unsafe.Pointer(&s.b1)))); hr < 0 || s.b1 == 0 {
			continue
		}
		s.out1 = 0
		if hr := win32.HR(com.Call(s.collection, vtCollectionGetFontFamily, uintptr(s.index), uintptr(unsafe.Pointer(&s.out1)))); hr < 0 {
			continue
		}
		fam := s.out1
		s.out1 = 0
		hr := win32.HR(com.Call(fam, vtFamilyGetFirstMatching, uintptr(weight), dwStretchNormal, dwStyleNormal, uintptr(unsafe.Pointer(&s.out1))))
		com.Release(fam)
		if hr < 0 || s.out1 == 0 {
			continue
		}
		font, s.out1 = s.out1, 0
		return font, n
	}
	return 0, ""
}

// Font returns the key of a font, creating it on first use. Fonts live until
// Close. A family that isn't installed silently resolves to the default.
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
	weight := min(max(d.Weight, 1), 999)
	var font uintptr
	var fam string
	switch {
	case d.Family != "":
		font, fam = s.findFont([]string{d.Family}, weight)
	case d.Mono:
		font, fam = s.findFont(monoFamilies, weight)
	default:
		font, fam = s.findFont(uiFamilies, weight)
	}
	if font == 0 {
		font, fam = s.findFont([]string{s.defFamily}, weight)
	}
	if font == 0 {
		return 0
	}
	px := d.SizePt * d.Scale
	fe := fontEnt{desc: d, weight: uint32(weight)}
	fe.family = append([]uint16(nil), s.utf16z(fam)...)
	face, ok := s.faceFromFont(font, px)
	com.Release(font)
	if !ok {
		return 0
	}
	fe.face = face
	s.fonts = append(s.fonts, fe)
	key := FontKey(len(s.fonts) - 1)

	// Metrics from the primary face.
	fc := &s.faces[fe.face]
	com.Call(fc.face, vtFaceGetMetrics, uintptr(unsafe.Pointer(&s.metricsBuf)))
	dm := s.metricsBuf
	if dm.UnitsPerEm != 0 {
		k := px / float32(dm.UnitsPerEm)
		m := Metrics{
			Ascent:    float32(dm.Ascent) * k,
			Descent:   float32(dm.Descent) * k,
			LineGap:   float32(max(dm.LineGap, 0)) * k,
			CapHeight: float32(dm.CapHeight) * k,
			XHeight:   float32(dm.XHeight) * k,
		}
		m.LineHeight = m.Ascent + m.Descent + m.LineGap
		s.fonts[key].metrics = m
	}
	s.fonts[key].metrics.MonoAdvance = s.Run(s.Shape(key, []byte("0"))).Advance
	return key
}

// Metrics returns the font's metrics in pixels.
func (s *System) Metrics(f FontKey) Metrics {
	if int(f) >= len(s.fonts) {
		return Metrics{}
	}
	return s.fonts[f].metrics
}

// ---- Faces ----

// faceFromFont maps an IDWriteFont at an em size in pixels to a FaceID,
// creating the IDWriteFontFace on first use. Face identity is the font
// file's reference key, the face index, the simulations and the weight,
// stretch and style of the IDWriteFont (the latter distinguish the named
// instances of one variable font file), plus the size. It does not take
// ownership of font.
func (s *System) faceFromFont(font uintptr, px float32) (FaceID, bool) {
	s.out1 = 0
	if font == 0 || win32.HR(com.Call(font, vtFontCreateFontFace, uintptr(unsafe.Pointer(&s.out1)))) < 0 {
		return 0, false
	}
	face := s.out1
	s.out1 = 0

	// Build the identity key in s.name.
	s.name = s.name[:0]
	s.n1 = 0
	if win32.HR(com.Call(face, vtFaceGetFiles, uintptr(unsafe.Pointer(&s.n1)), 0)) >= 0 && s.n1 > 0 && int(s.n1) <= len(s.files) {
		n := s.n1
		if win32.HR(com.Call(face, vtFaceGetFiles, uintptr(unsafe.Pointer(&s.n1)), uintptr(unsafe.Pointer(&s.files[0])))) >= 0 {
			for i := 0; i < int(n); i++ {
				f := s.files[i]
				if win32.HR(com.Call(f, vtFileGetReferenceKey, uintptr(unsafe.Pointer(&s.keyPtr)), uintptr(unsafe.Pointer(&s.keySize)))) >= 0 && s.keyPtr != 0 {
					s.name = append(s.name, unsafe.Slice((*byte)(win32.Ptr(s.keyPtr)), min(int(s.keySize), 256))...)
				}
				com.Release(f)
				s.files[i] = 0
			}
		}
	}
	for _, v := range [...]uint32{
		uint32(com.Call(face, vtFaceGetIndex)), uint32(com.Call(face, vtFaceGetSimulations)),
		uint32(com.Call(font, vtFontGetWeight)), uint32(com.Call(font, vtFontGetStretch)), uint32(com.Call(font, vtFontGetStyle)),
		math.Float32bits(px),
	} {
		s.name = append(s.name, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
	}
	if id, ok := s.faceByKey[string(s.name)]; ok {
		com.Release(face)
		return id, true
	}
	if len(s.faces) >= math.MaxUint16 {
		com.Release(face)
		return 0, false
	}
	fc := faceEnt{face: face, sizePx: px, name: s.fontName(font)}
	s.b1 = 0
	s.tablePtr, s.tableCtx = 0, 0
	if hr := win32.HR(com.Call(face, vtFaceTryGetFontTable, tagCOLR, uintptr(unsafe.Pointer(&s.tablePtr)), uintptr(unsafe.Pointer(&s.tableSize)), uintptr(unsafe.Pointer(&s.tableCtx)), uintptr(unsafe.Pointer(&s.b1)))); hr >= 0 && s.b1 != 0 {
		fc.color = true
		com.Call(face, vtFaceReleaseFontTable, s.tableCtx)
	}
	s.faces = append(s.faces, fc)
	id := FaceID(len(s.faces) - 1)
	s.faceByKey[string(s.name)] = id
	return id, true
}

// fontName returns "Family Face" (the first localized string of each) for
// diagnostics.
func (s *System) fontName(font uintptr) string {
	fam := ""
	s.out1 = 0
	if win32.HR(com.Call(font, vtFontGetFontFamily, uintptr(unsafe.Pointer(&s.out1)))) >= 0 {
		f := s.out1
		s.out1 = 0
		if win32.HR(com.Call(f, vtFamilyGetFamilyNames, uintptr(unsafe.Pointer(&s.out1)))) >= 0 {
			fam = s.firstString(s.out1)
		}
		com.Release(f)
	}
	face := ""
	s.out1 = 0
	if win32.HR(com.Call(font, vtFontGetFaceNames, uintptr(unsafe.Pointer(&s.out1)))) >= 0 {
		face = s.firstString(s.out1)
	}
	return fam + " " + face
}

// firstString reads string 0 of an IDWriteLocalizedStrings and releases it.
func (s *System) firstString(strs uintptr) string {
	defer com.Release(strs)
	if strs == 0 || win32.HR(com.Call(strs, vtStringsGetStringLength, 0, uintptr(unsafe.Pointer(&s.n1)))) < 0 {
		return ""
	}
	n := int(s.n1)
	buf := make([]uint16, n+1)
	if win32.HR(com.Call(strs, vtStringsGetString, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(n+1))) < 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

func (s *System) faceName(f FaceID) string {
	if int(f) >= len(s.faces) {
		return ""
	}
	return s.faces[f].name
}

// ---- Rasterization ----

// Glyph returns the atlas entry for a glyph, rasterizing it with DirectWrite
// on a miss. sub (0..3) is the x offset in quarter pixels; glyphs of faces
// with colour glyphs (COLR) ignore it (their entry is the sub 0 bitmap). A hit
// allocates nothing.
//
// Glyphs are grayscale: DirectWrite's ClearType 3×1 alpha texture is
// averaged over the three sub-pixels of each pixel. (The renderer's atlas has
// R8 coverage pages and premultiplied RGBA pages; an LCD mode would need a
// third page kind and dual-source blending. ARCHITECTURE §6 keeps that for
// later.) Colour emoji (COLR v0 layers, e.g. Segoe UI Emoji) are composited
// into the RGBA page; a glyph the font has no colour layers for is drawn as
// monochrome coverage.
//
// Entries are valid until AtlasGeneration changes.
func (s *System) Glyph(face FaceID, gid uint16, sub uint8) GlyphEntry {
	if int(face) >= len(s.faces) {
		return GlyphEntry{}
	}
	if s.faces[face].color {
		sub = 0
	}
	sub &= 3
	key := glyphKey(face, gid, sub)
	if e, ok := s.atlas.glyphs.lookup(key); ok {
		return e
	}
	e := s.rasterize(face, gid, sub)
	s.atlas.glyphs.insert(key, e)
	return e
}

// AtlasGeneration changes when the atlas is reset because it is full: every
// page is then cleared and all GlyphEntries are stale.
func (s *System) AtlasGeneration() uint32 { return s.atlas.gen }

func (s *System) setRun(fc *faceEnt, gid uint16) {
	s.gid[0], s.adv1[0], s.off1 = gid, 0, glyphOffset{}
	s.run = glyphRun{
		FontFace: fc.face, FontEmSize: fc.sizePx, GlyphCount: 1,
		Indices:  uintptr(unsafe.Pointer(&s.gid[0])),
		Advances: uintptr(unsafe.Pointer(&s.adv1[0])),
		Offsets:  uintptr(unsafe.Pointer(&s.off1)),
	}
}

// newAnalysis creates an IDWriteGlyphRunAnalysis for run at a baseline
// origin (+1; 0 on failure). IDWriteFactory::CreateGlyphRunAnalysis
// (dwrite.h:5371): glyphRun, FLOAT pixelsPerDip, transform, renderingMode,
// measuringMode, FLOAT baselineX, FLOAT baselineY, out.
func (s *System) newAnalysis(run uintptr, ox, oy float32) uintptr {
	s.out1 = 0
	hr := win32.HR(s.cf.CallF(s.factory, vtFactoryCreateGlyphRunAnalysis, 1<<1|1<<5|1<<6,
		run, uintptr(math.Float32bits(1)), 0, dwRenderNaturalSym, dwMeasureNatural,
		uintptr(math.Float32bits(ox)), uintptr(math.Float32bits(oy)), uintptr(unsafe.Pointer(&s.out1))))
	if hr < 0 {
		return 0
	}
	a := s.out1
	s.out1 = 0
	return a
}

// bounds returns the texture bounds of an analysis (ClearType 3×1) in
// s.rect and whether it has ink.
func (s *System) bounds(a uintptr) bool {
	s.rect = winRect{}
	if win32.HR(com.Call(a, vtAnalysisGetBounds, dwTextureCleartype, uintptr(unsafe.Pointer(&s.rect)))) < 0 {
		return false
	}
	return s.rect.Right > s.rect.Left && s.rect.Bottom > s.rect.Top
}

// coverage fills s.cov with the grayscale coverage of an analysis over rect
// (w×h). It averages the three sub-pixel samples of every pixel.
func (s *System) coverage(a uintptr, r winRect) bool {
	w, h := int(r.Right-r.Left), int(r.Bottom-r.Top)
	if cap(s.tex) < 3*w*h {
		s.tex = make([]byte, 3*w*h)
	}
	if cap(s.cov) < w*h {
		s.cov = make([]byte, w*h)
	}
	s.tex, s.cov = s.tex[:3*w*h], s.cov[:w*h]
	s.rect = r
	if win32.HR(com.Call(a, vtAnalysisCreateTexture, dwTextureCleartype, uintptr(unsafe.Pointer(&s.rect)), uintptr(unsafe.Pointer(&s.tex[0])), uintptr(len(s.tex)))) < 0 {
		return false
	}
	for i := range s.cov {
		s.cov[i] = byte((uint(s.tex[3*i]) + uint(s.tex[3*i+1]) + uint(s.tex[3*i+2]) + 1) / 3)
	}
	return true
}

func (s *System) rasterize(face FaceID, gid uint16, sub uint8) GlyphEntry {
	fc := &s.faces[face]
	s.setRun(fc, gid)
	if fc.color && s.factory2 != 0 {
		if e, ok := s.rasterizeColor(fc); ok {
			return e
		}
		s.setRun(fc, gid) // the enumeration reuses the scratch
	}
	a := s.newAnalysis(uintptr(unsafe.Pointer(&s.run)), float32(sub)*0.25, 0)
	if a == 0 {
		return GlyphEntry{}
	}
	defer com.Release(a)
	if !s.bounds(a) {
		return GlyphEntry{}
	}
	r := s.rect
	w, h := int(r.Right-r.Left), int(r.Bottom-r.Top)
	if w+2*glyphPad > PageSize || h+2*glyphPad > PageSize {
		return GlyphEntry{}
	}
	if !s.coverage(a, r) {
		return GlyphEntry{}
	}
	pg, ax, ay, ok := s.atlas.alloc(false, w, h)
	if !ok {
		return GlyphEntry{}
	}
	s.atlas.write(pg, ax, ay, w, h, s.cov, w)
	return GlyphEntry{
		Page: uint8(pg), X: uint16(ax), Y: uint16(ay), W: uint16(w), H: uint16(h),
		BearingX: float32(r.Left), BearingY: float32(-r.Top),
	}
}

// rasterizeColor draws a glyph with COLR layers into an RGBA page: the layers
// are composited, each as grayscale coverage filled with its palette colour,
// in float arithmetic and premultiplied once at the end. It reports false if
// the glyph has no colour layers (DWRITE_E_NOCOLOR) or anything fails; the
// caller then draws the monochrome outline. The "foreground colour" palette
// index (0xFFFF) is drawn black.
func (s *System) rasterizeColor(fc *faceEnt) (GlyphEntry, bool) {
	s.out1 = 0
	// IDWriteFactory2::TranslateColorGlyphRun (dwrite_2.h:3114): FLOAT
	// originX, FLOAT originY, glyphRun, description, measuringMode,
	// transform, paletteIndex, out.
	hr := win32.HR(s.cf.CallF(s.factory2, vtFactory2TranslateColorGlyphRun, 1<<0|1<<1,
		0, 0, uintptr(unsafe.Pointer(&s.run)), 0, dwMeasureNatural, 0, 0, uintptr(unsafe.Pointer(&s.out1))))
	if hr < 0 || s.out1 == 0 {
		return GlyphEntry{}, false
	}
	en := s.out1
	s.out1 = 0
	defer com.Release(en)

	n := 0
	var u winRect
	for n < maxColorLayers {
		s.b1 = 0
		if win32.HR(com.Call(en, vtColorEnumMoveNext, uintptr(unsafe.Pointer(&s.b1)))) < 0 || s.b1 == 0 {
			break
		}
		s.out2 = 0
		if win32.HR(com.Call(en, vtColorEnumGetCurrentRun, uintptr(unsafe.Pointer(&s.out2)))) < 0 || s.out2 == 0 {
			break
		}
		cr := (*colorGlyphRun)(win32.Ptr(s.out2))
		a := s.newAnalysis(uintptr(unsafe.Pointer(cr)), cr.OriginX, cr.OriginY)
		if a == 0 {
			continue
		}
		if !s.bounds(a) {
			com.Release(a)
			continue
		}
		l := &s.layers[n]
		l.analysis, l.rect = a, s.rect
		if cr.Palette == dwNoPaletteIndex {
			l.color = [4]float32{0, 0, 0, 1}
		} else {
			l.color = cr.Color
		}
		if n == 0 {
			u = s.rect
		} else {
			u.Left, u.Top = min(u.Left, s.rect.Left), min(u.Top, s.rect.Top)
			u.Right, u.Bottom = max(u.Right, s.rect.Right), max(u.Bottom, s.rect.Bottom)
		}
		n++
	}
	release := func() {
		for i := 0; i < n; i++ {
			com.Release(s.layers[i].analysis)
			s.layers[i].analysis = 0
		}
	}
	if n == 0 {
		return GlyphEntry{}, false
	}
	w, h := int(u.Right-u.Left), int(u.Bottom-u.Top)
	if w+2*glyphPad > PageSize || h+2*glyphPad > PageSize {
		release()
		return GlyphEntry{}, false
	}
	if cap(s.facc) < w*h*4 {
		s.facc = make([]float32, w*h*4)
	}
	s.facc = s.facc[:w*h*4]
	clear(s.facc)
	for i := 0; i < n; i++ {
		l := &s.layers[i]
		if !s.coverage(l.analysis, l.rect) {
			continue
		}
		lw := int(l.rect.Right - l.rect.Left)
		lh := int(l.rect.Bottom - l.rect.Top)
		for y := 0; y < lh; y++ {
			for x := 0; x < lw; x++ {
				c := float32(s.cov[y*lw+x]) * (1.0 / 255) * l.color[3]
				if c == 0 {
					continue
				}
				d := s.facc[((int(l.rect.Top-u.Top)+y)*w+int(l.rect.Left-u.Left)+x)*4:]
				k := 1 - c
				d[0] = l.color[0]*c + d[0]*k
				d[1] = l.color[1]*c + d[1]*k
				d[2] = l.color[2]*c + d[2]*k
				d[3] = c + d[3]*k
			}
		}
	}
	release()
	if cap(s.rgba) < w*h*4 {
		s.rgba = make([]byte, w*h*4)
	}
	s.rgba = s.rgba[:w*h*4]
	for i, v := range s.facc {
		s.rgba[i] = byte(min(max(v, 0), 1)*255 + 0.5)
	}
	pg, ax, ay, ok := s.atlas.alloc(true, w, h)
	if !ok {
		return GlyphEntry{}, false
	}
	s.atlas.write(pg, ax, ay, w, h, s.rgba, w*4)
	return GlyphEntry{
		Page: uint8(pg), Color: true, X: uint16(ax), Y: uint16(ay), W: uint16(w), H: uint16(h),
		BearingX: float32(u.Left), BearingY: float32(-u.Top),
	}, true
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

// Close releases every DirectWrite object. The System must not be used
// afterwards.
func (s *System) Close() {
	if s.closed {
		return
	}
	s.closed = true
	for _, f := range s.faces {
		com.Release(f.face)
	}
	s.fonts, s.faces = nil, nil
	for _, p := range [...]*uintptr{&s.collection, &s.analyzer, &s.fallback, &s.factory2, &s.factory} {
		com.Release(*p)
		*p = 0
	}
}
