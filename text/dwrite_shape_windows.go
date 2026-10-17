//go:build windows

package text

import (
	"math"
	"syscall"
	"unicode"
	"unicode/utf8"
	"unsafe"

	"gonativeui/internal/com"
	"gonativeui/internal/win32"
)

// HRESULTs compared as unsigned values (winerror.h).
const (
	hresultNotSufficient = 0x8007007A // E_NOT_SUFFICIENT_BUFFER (winerror.h:3429)
	hresultNoInterface   = 0x80004002 // E_NOINTERFACE
)

// ---- Go-implemented COM objects ----------------------------------------------
//
// IDWriteTextAnalyzer.AnalyzeScript/AnalyzeBidi and
// IDWriteFontFallback.MapCharacters read the text through an
// IDWriteTextAnalysisSource, and the analyzer reports through an
// IDWriteTextAnalysisSink. Both are tiny COM objects whose vtables hold Go
// callbacks (syscall.NewCallback; at most 2000 can ever be created, we make
// 13 once per process). An object is a struct whose first word is the vtable
// pointer; the second word, invisible to COM, leads back to the System.
//
// The callbacks run on the thread that called into DirectWrite, inside that
// call, so they need no locking, and they only touch System scratch
// (u16/levels/scripts) that the caller prepared.
//
// IDWriteTextAnalysisSource (dwrite.h:4494-4527): GetTextAtPosition 3,
// GetTextBeforePosition 4, GetParagraphReadingDirection 5, GetLocaleName 6,
// GetNumberSubstitution 7. IDWriteTextAnalysisSink (dwrite.h:4628-4659):
// SetScriptAnalysis 3, SetLineBreakpoints 4, SetBidiLevel 5,
// SetNumberSubstitution 6.

type comObject struct {
	vtbl uintptr // must be first
	sys  *System
}

type scriptSpan struct {
	pos, len int
	sa       scriptAnalysis
}

var (
	srcVtbl  [8]uintptr
	sinkVtbl [7]uintptr
)

func initCallbacks() {
	addRef := syscall.NewCallback(comAddRef)
	release := syscall.NewCallback(comRelease)
	srcVtbl = [8]uintptr{
		syscall.NewCallback(srcQueryInterface), addRef, release,
		syscall.NewCallback(srcGetTextAtPosition),
		syscall.NewCallback(srcGetTextBeforePosition),
		syscall.NewCallback(srcGetParagraphReadingDirection),
		syscall.NewCallback(srcGetLocaleName),
		syscall.NewCallback(srcGetNumberSubstitution),
	}
	sinkVtbl = [7]uintptr{
		syscall.NewCallback(sinkQueryInterface), addRef, release,
		syscall.NewCallback(sinkSetScriptAnalysis),
		syscall.NewCallback(sinkSetLineBreakpoints),
		syscall.NewCallback(sinkSetBidiLevel),
		syscall.NewCallback(sinkSetNumberSubstitution),
	}
}

func putPtr(p, v uintptr)        { *(*uintptr)(win32.Ptr(p)) = v }
func putU32(p uintptr, v uint32) { *(*uint32)(win32.Ptr(p)) = v }

func objSys(this uintptr) *System { return (*comObject)(win32.Ptr(this)).sys }

func queryInterface(this, riid, ppv uintptr, want *win32.GUID) uintptr {
	id := (*win32.GUID)(win32.Ptr(riid))
	if *id == iidUnknown || *id == *want {
		putPtr(ppv, this)
		return 0
	}
	putPtr(ppv, 0)
	return hresultNoInterface
}

// The objects live as long as the System and are not reference counted.
func comAddRef(this uintptr) uintptr  { return 1 }
func comRelease(this uintptr) uintptr { return 1 }

func srcQueryInterface(this, riid, ppv uintptr) uintptr {
	return queryInterface(this, riid, ppv, &iidTextSource)
}

func sinkQueryInterface(this, riid, ppv uintptr) uintptr {
	return queryInterface(this, riid, ppv, &iidTextSink)
}

// GetTextAtPosition(UINT32 textPosition, WCHAR const** textString, UINT32* textLength).
func srcGetTextAtPosition(this, pos, pstr, plen uintptr) uintptr {
	s := objSys(this)
	p := int(uint32(pos))
	if p >= len(s.u16) {
		putPtr(pstr, 0)
		putU32(plen, 0)
		return 0
	}
	putPtr(pstr, uintptr(unsafe.Pointer(&s.u16[p])))
	putU32(plen, uint32(len(s.u16)-p))
	return 0
}

// GetTextBeforePosition: the text ending at textPosition.
func srcGetTextBeforePosition(this, pos, pstr, plen uintptr) uintptr {
	s := objSys(this)
	p := int(uint32(pos))
	if p == 0 || p > len(s.u16) {
		putPtr(pstr, 0)
		putU32(plen, 0)
		return 0
	}
	putPtr(pstr, uintptr(unsafe.Pointer(&s.u16[0])))
	putU32(plen, uint32(p))
	return 0
}

// GetParagraphReadingDirection() DWRITE_READING_DIRECTION (0 LTR, 1 RTL).
func srcGetParagraphReadingDirection(this uintptr) uintptr {
	if objSys(this).paraRTL {
		return 1
	}
	return 0
}

// GetLocaleName(UINT32 textPosition, UINT32* textLength, WCHAR const** localeName).
func srcGetLocaleName(this, pos, plen, ploc uintptr) uintptr {
	s := objSys(this)
	putU32(plen, uint32(max(len(s.u16)-int(uint32(pos)), 0)))
	putPtr(ploc, uintptr(unsafe.Pointer(&s.locale[0])))
	return 0
}

// GetNumberSubstitution(UINT32 textPosition, UINT32* textLength,
// IDWriteNumberSubstitution** numberSubstitution): none.
func srcGetNumberSubstitution(this, pos, plen, psub uintptr) uintptr {
	s := objSys(this)
	putU32(plen, uint32(max(len(s.u16)-int(uint32(pos)), 0)))
	putPtr(psub, 0)
	return 0
}

// SetScriptAnalysis(UINT32 textPosition, UINT32 textLength,
// DWRITE_SCRIPT_ANALYSIS const* scriptAnalysis).
func sinkSetScriptAnalysis(this, pos, length, psa uintptr) uintptr {
	s := objSys(this)
	s.scripts = append(s.scripts, scriptSpan{int(uint32(pos)), int(uint32(length)), *(*scriptAnalysis)(win32.Ptr(psa))})
	return 0
}

func sinkSetLineBreakpoints(this, pos, length, pbp uintptr) uintptr { return 0 }

// SetBidiLevel(UINT32 textPosition, UINT32 textLength, UINT8 explicitLevel,
// UINT8 resolvedLevel). The 8-bit arguments arrive in the low byte of a
// register whose upper bits are undefined.
func sinkSetBidiLevel(this, pos, length, explicit, resolved uintptr) uintptr {
	s := objSys(this)
	p, n := int(uint32(pos)), int(uint32(length))
	for i := p; i < p+n && i < len(s.levels); i++ {
		s.levels[i] = uint8(resolved)
	}
	return 0
}

func sinkSetNumberSubstitution(this, pos, length, psub uintptr) uintptr { return 0 }

// ---- Shaping ----

// shapeItem is one run of a line shaped with one face: a contiguous range of
// UTF-16 with one script and one bidi level. Its glyphs, advances and
// offsets are at [g0, g0+n) of System.ig/ia/io, in logical order.
type shapeItem struct {
	g0, n int
	level uint8
	face  FaceID
	width float32
}

// Shape shapes utf8 with font f and returns a handle into the run cache. A
// hit allocates nothing. Invalid UTF-8 is shaped as U+FFFD.
//
// A miss runs DirectWrite's pipeline: IDWriteFontFallback.MapCharacters
// splits the text by the face that covers it (system cascade, e.g. Yu Gothic
// for Japanese, Segoe UI Emoji for emoji); IDWriteTextAnalyzer.AnalyzeBidi and
// AnalyzeScript split it again by bidi level and script; each piece goes
// through GetGlyphs/GetGlyphPlacements (ligatures, kerning, clusters). Pieces
// are put in visual order (UAX #9 rule L2) and the glyphs get pen x
// positions, left to right. The paragraph direction is the first strong
// character's, like CoreText's default. Vertical glyph offsets (marks) are not
// carried by the run format and are dropped; advance offsets are applied.
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
	fe := &s.fonts[f]
	s.gbuf, s.xbuf, s.fbuf = s.gbuf[:0], s.xbuf[:0], s.fbuf[:0]
	s.toUTF16(b)
	var advance float32
	if len(s.u16) > 0 {
		s.paraRTL = firstStrongRTL(b)
		advance = s.shapeLine(fe)
	}
	return s.runs.insert(f, b, s.gbuf, s.xbuf, s.fbuf, advance)
}

// toUTF16 converts to UTF-16 in s.u16, replacing invalid sequences with
// U+FFFD.
func (s *System) toUTF16(b []byte) {
	u := s.u16[:0]
	for i := 0; i < len(b); {
		r, n := utf8.DecodeRune(b[i:])
		i += n
		if r == utf8.RuneError {
			r = 0xFFFD
		}
		if r >= 0x10000 {
			r -= 0x10000
			u = append(u, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3ff)))
		} else {
			u = append(u, uint16(r))
		}
	}
	s.u16 = u
}

// firstStrongRTL reports whether the first letter of b belongs to a
// right-to-left script, which makes the paragraph direction right-to-left.
func firstStrongRTL(b []byte) bool {
	for i := 0; i < len(b); {
		r, n := utf8.DecodeRune(b[i:])
		i += n
		if r != utf8.RuneError && unicode.IsLetter(r) {
			return unicode.In(r, unicode.Arabic, unicode.Hebrew, unicode.Syriac, unicode.Thaana,
				unicode.Nko, unicode.Mandaic, unicode.Samaritan, unicode.Adlam)
		}
	}
	return false
}

func b2u(b bool) uintptr {
	if b {
		return 1
	}
	return 0
}

// shapeLine shapes s.u16 with the font and appends the glyphs to
// s.gbuf/xbuf/fbuf; it returns the line's advance.
func (s *System) shapeLine(fe *fontEnt) float32 {
	n := len(s.u16)
	if cap(s.levels) < n {
		s.levels = make([]uint8, n, n+n/2)
	}
	s.levels = s.levels[:n]
	clear(s.levels)
	s.scripts = s.scripts[:0]
	src, sink := uintptr(unsafe.Pointer(&s.src)), uintptr(unsafe.Pointer(&s.sink))
	if win32.HR(com.Call(s.analyzer, vtAnalyzerAnalyzeBidi, src, 0, uintptr(n), sink)) < 0 {
		clear(s.levels)
	}
	if win32.HR(com.Call(s.analyzer, vtAnalyzerAnalyzeScript, src, 0, uintptr(n), sink)) < 0 {
		s.scripts = s.scripts[:0]
	}

	s.items, s.ig, s.ia, s.io = s.items[:0], s.ig[:0], s.ia[:0], s.io[:0]
	basePx := s.faces[fe.face].sizePx
	for pos := 0; pos < n; {
		s.mapLen, s.out1, s.scale = 0, 0, 1
		hr := win32.HR(com.Call(s.fallback, vtFallbackMapCharacters,
			src, uintptr(pos), uintptr(n-pos), 0, uintptr(unsafe.Pointer(&fe.family[0])),
			uintptr(fe.weight), dwStyleNormal, dwStretchNormal,
			uintptr(unsafe.Pointer(&s.mapLen)), uintptr(unsafe.Pointer(&s.out1)), uintptr(unsafe.Pointer(&s.scale))))
		length := int(s.mapLen)
		if hr < 0 || length <= 0 || length > n-pos {
			length = n - pos
		}
		face := fe.face
		if font := s.out1; font != 0 {
			s.out1 = 0
			sc := s.scale
			if !(sc > 0) {
				sc = 1
			}
			if id, ok := s.faceFromFont(font, basePx*sc); ok {
				face = id
			}
			com.Release(font)
		}
		s.shapeSpan(pos, pos+length, face)
		pos += length
	}

	// Visual order: UAX #9 rule L2 over the items.
	order := s.order[:0]
	maxL, minOdd := uint8(0), uint8(255)
	for i := range s.items {
		order = append(order, int32(i))
		l := s.items[i].level
		maxL = max(maxL, l)
		if l&1 == 1 {
			minOdd = min(minOdd, l)
		}
	}
	for lvl := maxL; lvl >= minOdd && lvl > 0; lvl-- {
		for i := 0; i < len(order); {
			if s.items[order[i]].level < lvl {
				i++
				continue
			}
			j := i
			for j < len(order) && s.items[order[j]].level >= lvl {
				j++
			}
			for a, b := i, j-1; a < b; a, b = a+1, b-1 {
				order[a], order[b] = order[b], order[a]
			}
			i = j
		}
	}
	s.order = order

	// Pen positions (io holds each glyph's displacement to the right). In a
	// right-to-left item the glyph array is in logical order and the item is
	// drawn from its right edge to the left, so glyph k sits at x0 + width -
	// (advances up to and including k) + displacement; the glyphs are emitted
	// in left-to-right (reversed) order.
	var x float32
	for _, oi := range order {
		it := &s.items[oi]
		ads := s.ia[it.g0 : it.g0+it.n]
		offs := s.io[it.g0 : it.g0+it.n]
		gids := s.ig[it.g0 : it.g0+it.n]
		if it.level&1 == 0 {
			cum := x
			for k := range gids {
				s.gbuf = append(s.gbuf, gids[k])
				s.xbuf = append(s.xbuf, cum+offs[k])
				s.fbuf = append(s.fbuf, it.face)
				cum += ads[k]
			}
		} else {
			s.ix = s.ix[:0]
			cum := float32(0)
			for k := range gids {
				cum += ads[k]
				s.ix = append(s.ix, x+it.width-cum+offs[k])
			}
			for k := len(gids) - 1; k >= 0; k-- {
				s.gbuf = append(s.gbuf, gids[k])
				s.xbuf = append(s.xbuf, s.ix[k])
				s.fbuf = append(s.fbuf, it.face)
			}
		}
		x += it.width
	}
	return x
}

// shapeSpan shapes [start, end) of s.u16, which one face covers, splitting at
// script and bidi-level changes.
func (s *System) shapeSpan(start, end int, face FaceID) {
	for p := start; p < end; {
		q, sa := end, scriptAnalysis{}
		for i := range s.scripts {
			sp := &s.scripts[i]
			if p >= sp.pos && p < sp.pos+sp.len {
				sa = sp.sa
				q = min(q, sp.pos+sp.len)
				break
			}
		}
		lvl := s.levels[p]
		for k := p + 1; k < q; k++ {
			if s.levels[k] != lvl {
				q = k
				break
			}
		}
		s.shapeItem(p, q, face, lvl, sa)
		p = q
	}
}

// shapeItem shapes [p, q) of s.u16 with one script and bidi level and
// appends an item. GetGlyphs maps characters to glyphs (clusters, ligatures,
// substitutions); GetGlyphPlacements gives the advances and offsets in pixels
// for the face's em size.
func (s *System) shapeItem(p, q int, face FaceID, level uint8, sa scriptAnalysis) {
	fc := &s.faces[face]
	length := q - p
	rtl := level&1 == 1
	s.sa = sa
	maxG := 3*length/2 + 16
	text := uintptr(unsafe.Pointer(&s.u16[p]))
	loc := uintptr(unsafe.Pointer(&s.locale[0]))
	for attempt := 0; ; attempt++ {
		s.shapeScratch(length, maxG)
		s.n1 = 0
		hr := win32.HR(com.Call(s.analyzer, vtAnalyzerGetGlyphs,
			text, uintptr(length), fc.face, 0, b2u(rtl), uintptr(unsafe.Pointer(&s.sa)), loc, 0,
			0, 0, 0, uintptr(maxG),
			uintptr(unsafe.Pointer(&s.cmap[0])), uintptr(unsafe.Pointer(&s.tprops[0])),
			uintptr(unsafe.Pointer(&s.gidx[0])), uintptr(unsafe.Pointer(&s.gprop[0])), uintptr(unsafe.Pointer(&s.n1))))
		if uint32(hr) == hresultNotSufficient && attempt < 4 {
			maxG *= 2
			continue
		}
		if hr < 0 {
			return
		}
		break
	}
	n := int(s.n1)
	if n == 0 || n > maxG {
		return
	}
	// GetGlyphPlacements(text, clusterMap, textProps, textLength,
	// glyphIndices, glyphProps, glyphCount, fontFace, FLOAT fontEmSize,
	// isSideways, isRTL, analysis, locale, features, featureRangeLengths,
	// featureRanges, glyphAdvances, glyphOffsets): argument 8 is the float.
	hr := win32.HR(s.cf.CallF(s.analyzer, vtAnalyzerGetPlacements, 1<<8,
		text, uintptr(unsafe.Pointer(&s.cmap[0])), uintptr(unsafe.Pointer(&s.tprops[0])), uintptr(length),
		uintptr(unsafe.Pointer(&s.gidx[0])), uintptr(unsafe.Pointer(&s.gprop[0])), uintptr(n),
		fc.face, uintptr(math.Float32bits(fc.sizePx)), 0, b2u(rtl), uintptr(unsafe.Pointer(&s.sa)), loc,
		0, 0, 0,
		uintptr(unsafe.Pointer(&s.gadv[0])), uintptr(unsafe.Pointer(&s.goff[0]))))
	if hr < 0 {
		return
	}
	it := shapeItem{g0: len(s.ig), n: n, level: level, face: face}
	for k := 0; k < n; k++ {
		off := s.goff[k].AdvanceOffset
		if rtl {
			off = -off // a positive advanceOffset moves the glyph left in a right-to-left run
		}
		s.ig = append(s.ig, s.gidx[k])
		s.ia = append(s.ia, s.gadv[k])
		s.io = append(s.io, off)
		it.width += s.gadv[k]
	}
	s.items = append(s.items, it)
}

// shapeScratch makes the shaping buffers big enough for a text of length
// UTF-16 units and maxG glyphs.
func (s *System) shapeScratch(length, maxG int) {
	if cap(s.cmap) < length {
		s.cmap, s.tprops = make([]uint16, length, length*2), make([]uint16, length, length*2)
	}
	s.cmap, s.tprops = s.cmap[:length], s.tprops[:length]
	if cap(s.gidx) < maxG {
		s.gidx, s.gprop = make([]uint16, maxG, maxG*2), make([]uint16, maxG, maxG*2)
		s.gadv, s.goff = make([]float32, maxG, maxG*2), make([]glyphOffset, maxG, maxG*2)
	}
	s.gidx, s.gprop = s.gidx[:maxG], s.gprop[:maxG]
	s.gadv, s.goff = s.gadv[:maxG], s.goff[:maxG]
}

// Run returns the shaped run. The view is valid until the next eviction
// (see RunGeneration).
func (s *System) Run(r RunRef) RunView { return s.runs.view(r) }

// RunGeneration changes whenever the run cache evicts. Compare it before and
// after shaping a batch of strings: if it changed, earlier RunRefs and
// RunViews may be stale and must be re-shaped.
func (s *System) RunGeneration() uint32 { return s.runs.gen }
