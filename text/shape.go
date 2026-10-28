//go:build linux || textengine

package text

import (
	"math"
	"unicode"
	"unicode/utf8"

	"github.com/go-text/typesetting/bidi"
	"github.com/go-text/typesetting/di"
	ot "github.com/go-text/typesetting/font/opentype"
	"github.com/go-text/typesetting/harfbuzz"
	"github.com/go-text/typesetting/language"
	"github.com/go-text/typesetting/shaping"
)

// Shaping of a run-cache miss.
//
// go-text's shaping.Segmenter and shaping.HarfbuzzShaper allocate on every
// call (bidi level arrays, one Glyph slice per piece, a closure per glyph in
// the chained-context lookups...), so a list scrolling through endless new
// strings would never be allocation free. Text that cannot need
// bidirectional reordering, which is nearly all UI text, is therefore
// itemized here (script runs, then face runs, the same algorithm as
// shaping.Segmenter, kept in step with it by shape_test.go) and fed straight
// to a reused harfbuzz.Buffer with one cached harfbuzz.Font per face. Other
// text (Hebrew, Arabic and other right-to-left scripts, explicit bidi
// controls) takes shapeBidi, which runs go-text's bidi algorithm on a reused
// Paragraph (patched to keep its scratch, see third_party/typesetting) and
// then the same shaping; TestEngineRTLMissAllocatesNothing holds it at zero.
//
// What still allocates inside harfbuzz.Buffer.Shape depends on the font: see
// the ShapeAllocs numbers in engine_alloc_test.go.

// tabularDigits is FontDesc.TabularDigits as HarfBuzz features.
var tabularDigits = []harfbuzz.Feature{{Tag: ot.MustNewTag("tnum"), Value: 1, Start: harfbuzz.FeatureGlobalStart, End: harfbuzz.FeatureGlobalEnd}}

// gtSeg is a span of runes shaped with one face, script and language.
type gtSeg struct {
	start, end int
	script     language.Script
	lang       language.Language
	face       FaceID
}

func (s *goSystem) shapeMiss(f FontKey, b []byte) RunRef {
	s.runes, s.rbyte = s.runes[:0], s.rbyte[:0]
	// (Not range string(b): that copies text longer than 32 bytes to the heap.)
	for i := 0; i < len(b); {
		r, n := utf8.DecodeRune(b[i:]) // an invalid byte is one U+FFFD rune
		s.runes = append(s.runes, r)
		s.rbyte = append(s.rbyte, uint32(i))
		i += n
	}
	s.rbyte = append(s.rbyte, uint32(len(b)))
	s.gbuf, s.xbuf, s.fbuf, s.cbuf = s.gbuf[:0], s.xbuf[:0], s.fbuf[:0], s.cbuf[:0]
	var pen float32
	s.cur = f
	for start := 0; start < len(s.runes); {
		end := s.paragraphEnd(start)
		if isSeparator(s.runes[start]) && s.ltrOnly(start, len(s.runes)) {
			end = len(s.runes) // see paragraphEnd
		}
		if s.ltrOnly(start, end) {
			s.segs = s.segs[:0]
			s.splitScripts(start, end)
			s.splitFaces(f)
			for i := range s.segs {
				pen = s.shapePiece(f, s.segs[i], false, pen)
			}
		} else {
			pen = s.shapeBidi(f, start, end, pen)
		}
		start = end
	}
	return s.runs.insert(f, b, s.gbuf, s.xbuf, s.fbuf, s.cbuf, pen)
}

// isSeparator reports whether r is a paragraph separator (bidi class B).
func isSeparator(r rune) bool {
	switch r {
	case '\n', '\r', 0x1C, 0x1D, 0x1E, 0x85, 0x2029:
		return true
	}
	return false
}

// paragraphEnd returns the end of the paragraph that starts at start: the
// next paragraph separator or the end of the text. A separator belongs to
// the text that follows it (a paragraph may start with one), so that it is
// shaped, and gets its face, together with that text. (go-text's bidi stops
// before a separator and returns no runs for a text that starts with one;
// the engine used to shape all of the text from there on as one left-to-right
// run, which is still what happens when all of it is left-to-right, but
// right-to-left text after a separator is now shaped as such.)
func (s *goSystem) paragraphEnd(start int) int {
	i := start
	if isSeparator(s.runes[i]) {
		i++
	}
	for ; i < len(s.runes); i++ {
		if isSeparator(s.runes[i]) {
			return i
		}
	}
	return len(s.runes)
}

// ltrOnly reports whether runes [start,end) are certain to resolve to a
// single left-to-right bidi level run (level 0): no right-to-left letters or
// digits and no explicit directional formatting characters, so the whole
// text is one level-0 run whatever the paragraph direction heuristics say.
func (s *goSystem) ltrOnly(start, end int) bool {
	for _, r := range s.runes[start:end] {
		if r < 0x0590 {
			continue
		}
		if !ltrRune(r) {
			return false
		}
	}
	return true
}

// ltrRune reports whether r (>= 0x0590) is never right-to-left, Arabic
// number or an explicit bidi control (UAX #9 classes R, AL, AN, LRE, RLE,
// LRO, RLO, PDF, LRI, RLI, FSI, PDI). It is conservative: false only means
// "use the full algorithm". TestLTRRunesAreLevelZero checks it against
// go-text's bidi for every code point.
func ltrRune(r rune) bool {
	switch {
	case r < 0x0900:
		return false // Hebrew, Arabic, Syriac, Thaana, NKo, Samaritan, Mandaic
	case r < 0x2070:
		switch {
		case r == 0x200E || r == 0x200F, 0x202A <= r && r <= 0x202E, 0x2066 <= r && r <= 0x2069:
			return false // LRM, RLM, embeddings and overrides, isolates
		}
		return true
	case r < 0xFB1D:
		return true
	case r < 0xFE00:
		return false // Hebrew and Arabic presentation forms
	case r < 0xFE70:
		return true // variation selectors, vertical and small forms
	case r < 0xFF00:
		return false // Arabic presentation forms B
	case r < 0x10800:
		return true
	case r < 0x11000:
		return false // Phoenician, Kharoshthi, Old Turkic, Hanifi Rohingya...
	case r < 0x1E800:
		return true
	case r < 0x1F000:
		return false // Adlam, Mende Kikakui, Arabic mathematical symbols
	}
	return true
}

// splitScripts is shaping.Segmenter.splitByScript followed by
// enforceLanguages, for the whole of [start,end), into s.sruns.
func (s *goSystem) splitScripts(start, end int) {
	s.sruns = s.sruns[:0]
	s.delims = s.delims[:0]
	cur := gtSeg{start: start, script: language.Common}
	for i := start; i < end; i++ {
		r := s.runes[i]
		rScript := language.LookupScript(r)

		delimIndex := -1
		if !rScript.Strong() && r >= 0x28 { // the smallest paired delimiter is '('
			delimIndex = lookupDelimIndex(r)
		}
		if delimIndex >= 0 {
			if delimIndex%2 == 0 { // opener
				s.delims = append(s.delims, delimEntry{delimIndex, cur.script})
			} else { // closer: look back for its opener
				j := len(s.delims) - 1
				for ; j >= 0; j-- {
					if s.delims[j].index == delimIndex-1 {
						rScript = s.delims[j].script
						break
					}
				}
				if j == -1 {
					j = 0
				}
				s.delims = s.delims[:j]
			}
		}

		switch {
		case !rScript.Strong() || rScript == cur.script:
		case cur.script == language.Common:
			for k := range s.delims {
				s.delims[k].script = rScript
			}
			cur.script = rScript
		default:
			if i != start {
				cur.end = i
				s.sruns = append(s.sruns, cur)
			}
			cur.start = i
			cur.script = rScript
		}
	}
	cur.end = end
	s.sruns = append(s.sruns, cur)
	for i := range s.sruns {
		s.sruns[i].lang = s.langFor(s.sruns[i].script)
	}
}

type delimEntry struct {
	index  int
	script language.Script
}

// lookupDelimIndex returns the index of ch in pairedDelims, or -1.
func lookupDelimIndex(ch rune) int {
	lower, upper := 0, len(pairedDelims)-1
	for lower <= upper {
		mid := (lower + upper) / 2
		switch {
		case ch < pairedDelims[mid]:
			upper = mid - 1
		case ch > pairedDelims[mid]:
			lower = mid + 1
		default:
			return mid
		}
	}
	return -1
}

// langFor is shaping.Segmenter.enforceLanguages for a text without a
// language: English, unless the script is not written in English.
func (s *goSystem) langFor(sc language.Script) language.Language {
	if l, ok := s.langs[sc]; ok {
		return l
	}
	var out language.Language
	if id, ok := language.NewLangID("en"); ok {
		if !id.UseScript(sc) {
			if l := language.ScriptToLang[sc]; l != 0 {
				id = l
			}
		}
		out = id.Language()
	}
	s.langs[sc] = out
	return out
}

// splitFaces is shaping.Segmenter.splitByFace over s.sruns, into s.segs. The
// primary face of the font is the face a run starts with, so leading spaces
// and format characters stay in it.
func (s *goSystem) splitFaces(f FontKey) {
	primary := s.fonts[f].face
	for _, in := range s.sruns {
		cur := in
		cur.face = primary
		for k := in.start; k < in.end; k++ {
			r := s.runes[k]
			// Space and format characters never change the face.
			if ignoreFaceChange(r) {
				continue
			}
			sel := s.resolve(f, r)
			if cur.face == sel {
				continue
			}
			if k != in.start {
				cur.end = k
				s.segs = append(s.segs, cur)
			}
			cur = in
			cur.start = k
			cur.face = sel
		}
		cur.end = in.end
		s.segs = append(s.segs, cur)
	}
}

// ignoreFaceChange is shaping's rule that spaces, controls and ignorable
// characters do not select a face of their own.
func ignoreFaceChange(r rune) bool {
	if r < 0x80 {
		return r <= ' ' || r == 0x7F
	}
	return unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) ||
		(unicode.Is(unicode.Zs, r) && r != 0x1680) || harfbuzz.IsDefaultIgnorable(r)
}

// shapeBidi shapes the paragraph [start,end) with the go-text bidi and
// segmenter code. A leading separator is shaped on its own (go-text's bidi
// stops at separators), then the text after it level run by level run.
func (s *goSystem) shapeBidi(f FontKey, start, end int, pen float32) float32 {
	body := start
	if isSeparator(s.runes[start]) {
		s.bruns = append(s.bruns[:0], bidiRun{start, start + 1, 0})
		pen = s.shapeLevelRuns(f, pen)
		body++
	}
	if body == end {
		return pen
	}
	runs := s.para.Segment(s.runes[body:end], bidi.Neutral)
	s.bruns = s.bruns[:0]
	for i := 0; i < runs.NumRuns(); i++ {
		r := runs.Run(i)
		s.bruns = append(s.bruns, bidiRun{body + r.Start, body + r.End, int(r.Level)})
	}
	return s.shapeLevelRuns(f, pen)
}

// shapeLevelRuns shapes s.bruns in visual order.
func (s *goSystem) shapeLevelRuns(f FontKey, pen float32) float32 {
	s.visualOrder()
	for _, i := range s.order {
		br := s.bruns[i]
		rtl := br.level%2 == 1
		in := shaping.Input{Text: s.runes, RunStart: br.start, RunEnd: br.end, Direction: di.DirectionLTR, Face: s.faces[s.fonts[f].face].face}
		if rtl {
			in.Direction = di.DirectionRTL
		}
		s.segs = s.segs[:0]
		for _, p := range s.seg.Split(in, s) {
			s.segs = append(s.segs, gtSeg{p.RunStart, p.RunEnd, p.Script, p.Language, s.faceOf[p.Face]})
		}
		if rtl { // pieces are in logical order; the output is visual
			for k := len(s.segs) - 1; k >= 0; k-- {
				pen = s.shapePiece(f, s.segs[k], true, pen)
			}
		} else {
			for k := range s.segs {
				pen = s.shapePiece(f, s.segs[k], false, pen)
			}
		}
	}
	return pen
}

// shapePiece shapes one span (one face, script and direction) with harfbuzz
// and appends its glyphs; it returns the pen after the span.
func (s *goSystem) shapePiece(f FontKey, p gtSeg, rtl bool, pen float32) float32 {
	if p.end <= p.start {
		return pen
	}
	fe := &s.fonts[f]
	fc := &s.faces[p.face]
	if fc.hb == nil {
		fc.hb = harfbuzz.NewFont(fc.face)
	}
	// go-text shapes at whole pixel sizes; the exact fractional size is
	// restored by scaling the (1/64 px) results.
	ceilPx := int(math.Ceil(float64(fe.px)))
	k := fe.px / float32(ceilPx) / 64
	fc.hb.XScale = int32(ceilPx) << 6
	fc.hb.YScale = fc.hb.XScale

	b := s.hbuf
	b.Clear()
	b.AddRunes(s.runes, p.start, p.end-p.start)
	b.Props.Direction = harfbuzz.LeftToRight
	if rtl {
		b.Props.Direction = harfbuzz.RightToLeft
	}
	b.Props.Language = p.lang
	b.Props.Script = p.script
	var feats []harfbuzz.Feature
	if fe.desc.TabularDigits {
		feats = tabularDigits
	}
	s.shapeFn(b, fc.hb, feats)

	for i := range b.Info {
		gid := uint16(0xFFFF)
		if g := b.Info[i].Glyph; g < 0xFFFF {
			gid = uint16(g)
		}
		if b.Info[i].Glyph == 0 && s.lonelySpace(b.Info, i, p.end, rtl) {
			gid = 0xFFFF // a space the face lacks: draw nothing, not .notdef
		}
		s.gbuf = append(s.gbuf, gid)
		s.xbuf = append(s.xbuf, pen+float32(b.Pos[i].XOffset)*k)
		s.fbuf = append(s.fbuf, p.face)
		c := s.rbyte[min(int(b.Info[i].Cluster), len(s.runes))]
		if rtl {
			c |= ClusterRTL
		}
		s.cbuf = append(s.cbuf, c)
		pen += float32(b.Pos[i].XAdvance) * k
	}
	return pen
}

// lonelySpace reports whether glyph i, the .notdef glyph, is the only glyph
// of a cluster made of exactly one space character (shaping's
// replaceNotSupportedSpaces: our face choice assumes every face has the
// space, which is not always true).
func (s *goSystem) lonelySpace(info []harfbuzz.GlyphInfo, i, end int, rtl bool) bool {
	c := info[i].Cluster
	lo, hi := i, i
	for lo > 0 && info[lo-1].Cluster == c {
		lo--
	}
	for hi+1 < len(info) && info[hi+1].Cluster == c {
		hi++
	}
	if lo != hi {
		return false
	}
	var runes int
	if rtl {
		prev := end
		if lo > 0 {
			prev = info[lo-1].Cluster
		}
		runes = prev - c
	} else {
		next := end
		if hi+1 < len(info) {
			next = info[hi+1].Cluster
		}
		runes = next - c
	}
	return runes == 1 && unicode.Is(unicode.Zs, s.runes[c])
}
