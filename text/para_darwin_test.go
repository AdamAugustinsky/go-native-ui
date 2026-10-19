//go:build darwin

package text

import (
	"fmt"
	"math"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
	"unsafe"

	"gonativeui/internal/objc"
)

// The paragraph layout against AppKit: the same attributed text (the NSFonts
// AppKit uses for our font descriptions, see opsz_darwin_test.go) set by an
// NSLayoutManager in an NSTextContainer of the same width with no line
// fragment padding, which is the text system of NSTextView and of NSTextField's
// field editor. Line breaks must be identical; widths and heights agree to
// float precision.

// akLine is one line fragment of AppKit's layout.
type akLine struct {
	start, end int             // bytes
	used       float64         // the used rectangle's width, points
	y, h       float64         // the line fragment rectangle's top and height, points
	baseline   float64         // from the top of the text container
	glyphX     map[int]float64 // the x of the first glyph of each character (byte offset), points
}

// akStyle is a span of the reference text: its bytes and font.
type akStyle struct {
	off, n int
	desc   FontDesc
}

// appKitLayout lays out str (with the base font and the span fonts) in a text
// container wptPt points wide and returns its line fragments.
func appKitLayout(t *testing.T, s *System, str string, base FontDesc, spans []akStyle, wPt float64) []akLine {
	t.Helper()
	if runtime.GOARCH != "arm64" {
		t.Skip("the NSRect and NSRange returns are read from arm64 registers")
	}
	pool := s.poolPush()
	defer s.poolPop(pool)
	fr := s.fr
	u16 := indexUTF16Into(nil, []byte(str))
	u16Of := func(off int) int {
		for i, b := range u16 {
			if int(b) >= off {
				return i
			}
		}
		return len(u16) - 1
	}
	alloc := func(cls string) uintptr {
		return fr.Reset().Arg(uintptr(objc.Cls(cls))).Arg(uintptr(objc.Sel("alloc"))).Call(objc.MsgSend).R0()
	}
	nsStr := uintptr(objc.Str(str))
	storage := fr.Reset().Arg(alloc("NSTextStorage")).Arg(uintptr(objc.Sel("initWithString:"))).Arg(nsStr).Call(objc.MsgSend).R0()
	defer objc.Release(objc.ID(storage))
	fontName := uintptr(objc.Str("NSFont"))
	addFont := func(d FontDesc, u0, u1 int) {
		f := nsFont(s, d)
		if d.Italic {
			fm := fr.Reset().Arg(uintptr(objc.Cls("NSFontManager"))).Arg(uintptr(objc.Sel("sharedFontManager"))).Call(objc.MsgSend).R0()
			f = fr.Reset().Arg(fm).Arg(uintptr(objc.Sel("convertFont:toHaveTrait:"))).Arg(f).Arg(1).Call(objc.MsgSend).R0()
		}
		fr.Reset().Arg(storage).Arg(uintptr(objc.Sel("addAttribute:value:range:"))).Arg(fontName).Arg(f).
			Arg(uintptr(u0)).Arg(uintptr(u1 - u0)).Call(objc.MsgSend)
	}
	addFont(base, 0, len(u16)-1)
	for _, sp := range spans {
		addFont(sp.desc, u16Of(sp.off), u16Of(sp.off+sp.n))
	}
	// Each paragraph's base writing direction, explicit, as Layout sets it
	// for DirAuto, but resolved by CoreText's own bidi algorithm
	// (ctNaturalRTL), not by ours; natural alignment then puts a right-to-left
	// paragraph at the right. Without it AppKit takes the alignment from the
	// UI language and sets Hebrew flush left.
	psName := uintptr(objc.Str("NSParagraphStyle"))
	for ps := 0; ps < len(str); {
		pe := paraEnd([]byte(str), ps)
		if pe < len(str) {
			pe += newlineAt([]byte(str), pe)
		}
		st := fr.Reset().Arg(alloc("NSMutableParagraphStyle")).Arg(uintptr(objc.Sel("init"))).Call(objc.MsgSend).R0()
		dir := uintptr(0)
		if ctNaturalRTL(t, s, s.Font(base), str[ps:pe]) {
			dir = 1
		}
		fr.Reset().Arg(st).Arg(uintptr(objc.Sel("setBaseWritingDirection:"))).Arg(dir).Call(objc.MsgSend)
		fr.Reset().Arg(storage).Arg(uintptr(objc.Sel("addAttribute:value:range:"))).Arg(psName).Arg(st).
			Arg(uintptr(u16Of(ps))).Arg(uintptr(u16Of(pe) - u16Of(ps))).Call(objc.MsgSend)
		objc.Release(objc.ID(st))
		ps = pe
	}
	lm := fr.Reset().Arg(alloc("NSLayoutManager")).Arg(uintptr(objc.Sel("init"))).Call(objc.MsgSend).R0()
	defer objc.Release(objc.ID(lm))
	tc := fr.Reset().Arg(alloc("NSTextContainer")).Arg(uintptr(objc.Sel("initWithSize:"))).ArgF64(wPt).ArgF64(1e7).Call(objc.MsgSend).R0()
	defer objc.Release(objc.ID(tc))
	fr.Reset().Arg(tc).Arg(uintptr(objc.Sel("setLineFragmentPadding:"))).ArgF64(0).Call(objc.MsgSend)
	fr.Reset().Arg(lm).Arg(uintptr(objc.Sel("addTextContainer:"))).Arg(tc).Call(objc.MsgSend)
	fr.Reset().Arg(storage).Arg(uintptr(objc.Sel("addLayoutManager:"))).Arg(lm).Call(objc.MsgSend)
	fr.Reset().Arg(lm).Arg(uintptr(objc.Sel("ensureLayoutForTextContainer:"))).Arg(tc).Call(objc.MsgSend)
	ng := int(fr.Reset().Arg(lm).Arg(uintptr(objc.Sel("numberOfGlyphs"))).Call(objc.MsgSend).R0())
	var out []akLine
	rng := new([2]uintptr)
	for g := 0; g < ng; {
		fr.Reset().Arg(lm).Arg(uintptr(objc.Sel("lineFragmentRectForGlyphAtIndex:effectiveRange:"))).Arg(uintptr(g)).ArgPtr(unsafe.Pointer(rng)).Call(objc.MsgSend)
		y, h := fr.F64(1), fr.F64(3)
		fr.Reset().Arg(lm).Arg(uintptr(objc.Sel("lineFragmentUsedRectForGlyphAtIndex:effectiveRange:"))).Arg(uintptr(g)).Arg(0).Call(objc.MsgSend)
		used := fr.F64(2)
		fr.Reset().Arg(lm).Arg(uintptr(objc.Sel("characterRangeForGlyphRange:actualGlyphRange:"))).Arg(rng[0]).Arg(rng[1]).Arg(0).Call(objc.MsgSend)
		c0, cn := int(fr.R0()), int(fr.R1())
		fr.Reset().Arg(lm).Arg(uintptr(objc.Sel("locationForGlyphAtIndex:"))).Arg(rng[0]).Call(objc.MsgSend)
		bl := y + fr.F64(1)
		xs := map[int]float64{}
		for gi := int(rng[0]); gi < int(rng[0]+rng[1]); gi++ {
			if !fr.Reset().Arg(lm).Arg(uintptr(objc.Sel("isValidGlyphIndex:"))).Arg(uintptr(gi)).Call(objc.MsgSend).Bool() {
				continue
			}
			ci := int(fr.Reset().Arg(lm).Arg(uintptr(objc.Sel("characterIndexForGlyphAtIndex:"))).Arg(uintptr(gi)).Call(objc.MsgSend).R0())
			fr.Reset().Arg(lm).Arg(uintptr(objc.Sel("locationForGlyphAtIndex:"))).Arg(uintptr(gi)).Call(objc.MsgSend)
			x := fr.F64(0)
			if bo := int(u16[min(ci, len(u16)-1)]); ci < len(u16)-1 {
				if _, ok := xs[bo]; !ok {
					xs[bo] = x
				}
			}
		}
		out = append(out, akLine{start: int(u16[c0]), end: int(u16[min(c0+cn, len(u16)-1)]), used: used, y: y, h: h, baseline: bl, glyphX: xs})
		if rng[1] == 0 {
			break
		}
		g = int(rng[0] + rng[1])
	}
	runtime.KeepAlive(rng)
	return out
}

// ctNaturalRTL reports whether CoreText resolves the paragraph str (up to its
// first newline) right to left when it is given no direction, from its own
// implementation of the bidi algorithm: a neutral character put before the
// text takes the paragraph's direction, so it is the leftmost glyph of a
// left-to-right paragraph and the rightmost of a right-to-left one.
func ctNaturalRTL(t *testing.T, s *System, f FontKey, str string) bool {
	t.Helper()
	str = str[:paraEnd([]byte(str), 0)]
	b := []byte("!" + str)
	v := s.Run(s.Shape(f, b))
	// The probe's grapheme (with the marks that a text starting with one
	// gives it).
	pe := NextGrapheme(b, 0)
	probe, others := -1, 0
	for g := range v.Glyphs {
		if int(v.Clusters[g]&^ClusterRTL) < pe {
			if probe < 0 {
				probe = g
			}
		} else {
			others++
		}
	}
	if probe < 0 {
		t.Fatalf("%q: no glyph for the probe", str)
	}
	if others == 0 {
		return false // nothing to resolve: left to right (rule P3)
	}
	left, right := true, true
	for g := range v.Glyphs {
		if int(v.Clusters[g]&^ClusterRTL) < pe {
			continue
		}
		left = left && v.X[g] > v.X[probe]
		right = right && v.X[g] < v.X[probe]
	}
	if left == right {
		t.Fatalf("%q: the probe is neither leftmost nor rightmost", str)
	}
	return right
}

// FirstStrongRTL against CoreText's own resolution of the paragraph
// direction.
func TestFirstStrongMatchesCoreText(t *testing.T) {
	s := newSys(t)
	f := s.Font(FontDesc{SizePt: 13, Scale: 2})
	strs := []string{
		"hello", "שלום", "مرحبا", "123 שלום", "(hi) שלום", "\u200Fabc", "\u200Eשלום", "日本 שלום",
		"१२३ שלום", "٣٤ שלום", "ܫܠܡܐ abc", "ދިވެހި abc", "ߒߞߏ abc", "\u0301abc", "\u05B0\u05D0 abc",
		"\u061Cabc", "© ® ™ abc", "€5 שלום", "- • שלום abc", "𞤀𞤣𞤤𞤢𞤥 abc", "ꯃꯤꯇꯩ שלום", "𐤀𐤁 abc",
		"\u2067abc\u2069 שלום", "\u2066שלום\u2069 abc", "\u2068שלום\u2069 123", "\u2067abc def",
		"\u2067\u2066abc\u2069 def\u2069 שלום", "\u2069שלום abc", "\u202Babc\u202C שלום", "\u202Eשלום\u202C abc",
	}
	for _, pc := range paraCases() {
		strs = append(strs, pc.text)
	}
	for _, str := range strs {
		if got, want := FirstStrongRTL([]byte(str)), ctNaturalRTL(t, s, f, str); got != want {
			t.Errorf("FirstStrongRTL(%q) = %v, CoreText resolves it %v", str, got, want)
		}
	}
}

// paraCase is one sample of the comparison.
type paraCase struct {
	name  string
	text  string
	base  FontDesc
	spans []akStyle
	wPt   float64
}

func paraCases() []paraCase {
	body := FontDesc{SizePt: 13, Weight: 400, Scale: 2}
	bold := FontDesc{SizePt: 13, Weight: 700, Scale: 2}
	ital := FontDesc{SizePt: 13, Weight: 400, Italic: true, Scale: 2}
	mono := FontDesc{SizePt: 13, Weight: 400, Mono: true, Scale: 2}
	at := func(s, sub string, d FontDesc) akStyle {
		i := strings.Index(s, sub)
		if i < 0 {
			panic(sub)
		}
		return akStyle{off: i, n: len(sub), desc: d}
	}
	rtlStyled := "זוהי בדיקה של טקסט מודגש עם link.com באמצע ועוד מילים"
	styled := "Ship it *today*, then check `go test ./...` and the **release notes** before Friday — thanks!"
	return []paraCase{
		{name: "latin", base: body, wPt: 180,
			text: "The list keeps its place while history loads above and new messages arrive below, a million rows of mixed heights without jumps."},
		{name: "styled", base: body, wPt: 170, text: styled,
			spans: []akStyle{at(styled, "*today*", ital), at(styled, "`go test ./...`", mono), at(styled, "**release notes**", bold)}},
		{name: "mixed scripts", base: body, wPt: 150,
			text: "Meeting at 10 会议在十点 👍🏽 مرحبا بالعالم and Ελληνικά, Русский текст, हिन्दी पाठ 🎉"},
		{name: "hebrew paragraph", base: body, wPt: 140,
			text: "שלום לכולם, הפגישה תתקיים מחר בשעה 10:00 ב-Zoom ואחר כך נדבר על הפרויקט החדש"},
		{name: "arabic paragraph", base: body, wPt: 160,
			text: "مرحبا بكم في التطبيق الجديد، يمكنكم إرسال الرسائل والصور والملفات بسهولة تامة مع WhatsApp"},
		{name: "paragraphs", base: body, wPt: 160,
			text: "First paragraph is left to right and wraps onto a second line.\nשלום עולם, זו פסקה מימין לשמאל שגם היא נשברת\n\nlast"},
		{name: "long url", base: body, wPt: 120,
			text: "see https://example.com/a/very/long/path/that/does/not/fit/anywhere?q=1&r=2 ok"},
		{name: "thai", base: body, wPt: 120,
			text: "ภาษาไทยไม่มีการเว้นวรรคระหว่างคำ แต่ต้องตัดบรรทัดตามคำ"},
		{name: "japanese", base: body, wPt: 110,
			text: "日本語の文章は、文字と文字の間で改行できます。句読点は行頭に来ません。"},
		{name: "emoji", base: body, wPt: 100,
			text: "🎉🎊👍🏽👨‍👩‍👧‍👦🇧🇷 🇯🇵 ❤️ 1️⃣ party time 🥳🥳🥳🥳🥳🥳🥳"},
		{name: "large", base: FontDesc{SizePt: 22, Weight: 600, Scale: 1}, wPt: 200,
			text: "A heading that is set larger and wraps"},
		{name: "devanagari", base: body, wPt: 90, text: "हिन्दी पाठ और कुछ शब्द यहाँ"},
		{name: "times thai", base: FontDesc{Family: "Times New Roman", SizePt: 13, Scale: 2}, wPt: 120, text: "Times text ภาษาไทยไม่มีการเว้นวรรค and more"},
		{name: "menlo emoji", base: FontDesc{Family: "Menlo", SizePt: 13, Scale: 2}, wPt: 120, text: "mono 🎉 code_with_long_identifiers()"},
		{name: "sf17", base: FontDesc{SizePt: 17, Scale: 2}, wPt: 160, text: "SF at 17 pt 🎉 ภาษา with a fallback"},
		{name: "sf11 bidi", base: FontDesc{SizePt: 11, Scale: 2}, wPt: 130, text: "Reply to שלום עולם and مرحبا 123 in one line, wrapped"},
		{name: "rtl styled", base: body, wPt: 130, text: rtlStyled,
			spans: []akStyle{at(rtlStyled, "בדיקה", bold), at(rtlStyled, "link.com", ital)}},
	}
}

func (pc paraCase) para(s *System) (Para, []Span) {
	fonts := []FontKey{s.Font(pc.base)}
	var spans []Span
	for _, sp := range pc.spans {
		fonts = append(fonts, s.Font(sp.desc))
		spans = append(spans, Span{Off: uint32(sp.off), Len: uint32(sp.n), Style: uint16(len(fonts) - 1)})
	}
	sc := normalizeDesc(pc.base).Scale
	return Para{Text: []byte(pc.text), Spans: spans, Fonts: fonts, Width: float32(pc.wPt) * sc}, spans
}

func TestParagraphMatchesAppKit(t *testing.T) {
	s := newSys(t)
	for _, pc := range paraCases() {
		t.Run(pc.name, func(t *testing.T) {
			p, _ := pc.para(s)
			sc := float64(normalizeDesc(pc.base).Scale)
			v := s.Paragraph(s.Layout(&p))
			ref := appKitLayout(t, s, pc.text, pc.base, pc.spans, pc.wPt)
			var got, want []string
			for _, l := range v.Lines {
				got = append(got, fmt.Sprintf("%q", pc.text[l.Start:l.End]))
			}
			for _, l := range ref {
				want = append(want, fmt.Sprintf("%q", pc.text[l.start:l.end]))
			}
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Fatalf("lines differ\nours:\n%s\nAppKit:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
			for i, l := range v.Lines {
				r := ref[i]
				t.Logf("%-40q width %.3f/%.3f pt  top %.2f/%.2f  h %.3f/%.3f  base %.2f/%.2f", pc.text[l.Start:l.End],
					float64(l.Width)/sc, r.used, float64(l.Top)/sc, r.y, float64(l.Height)/sc, r.h, float64(l.Baseline-l.Top)/sc, r.baseline-r.y)
				// AppKit's used rectangle includes the white space at the end
				// of a line, up to the container's width.
				used := math.Min(float64(l.Advance)/sc, pc.wPt)
				if d := math.Abs(used - r.used); d > 0.01 {
					t.Errorf("line %d %q: width %.3f pt, AppKit %.3f", i, pc.text[l.Start:l.End], used, r.used)
				}
				if d := math.Abs(float64(l.Height)/sc - r.h); d > 0.01 {
					t.Errorf("line %d %q: height %.3f pt, AppKit %.3f", i, pc.text[l.Start:l.End], float64(l.Height)/sc, r.h)
				}
				if d := math.Abs(float64(l.Top)/sc - r.y); d > 0.02 {
					t.Errorf("line %d %q: top %.3f pt, AppKit %.3f", i, pc.text[l.Start:l.End], float64(l.Top)/sc, r.y)
				}
				// (AppKit reports the location of an empty line's newline glyph
				// at the line's bottom.)
				if d := math.Abs(float64(l.Baseline-l.Top)/sc - (r.baseline - r.y)); d > 0.01 && l.G1 > l.G0 {
					t.Errorf("line %d %q: baseline %.3f pt below the top, AppKit %.3f", i, pc.text[l.Start:l.End], float64(l.Baseline-l.Top)/sc, r.baseline-r.y)
				}
			}
			// Glyph positions, which is the visual order of bidirectional
			// lines and the natural alignment of right-to-left ones: each
			// character's first glyph where AppKit has it.
			compared := 0
			for i, l := range v.Lines {
				left := v.LineLeft(i, float32(pc.wPt*sc))
				seen := map[int]bool{}
				for g := l.G0; g < l.G1; g++ {
					c := int(v.Clusters[g] &^ ClusterRTL)
					if seen[c] {
						continue
					}
					seen[c] = true
					ax, ok := ref[i].glyphX[c]
					if r, _ := utf8.DecodeRuneInString(pc.text[c:]); !ok || r >= 0x0600 && r <= 0x06FF {
						// (Arabic: AppKit's glyph stream assigns the glyphs of
						// joined letters and ligatures to characters its own way.)
						continue
					}
					compared++
					if x := float64(left+v.X[g]) / sc; math.Abs(x-ax) > 0.05 {
						t.Errorf("line %d: the glyph of %q at x %.3f pt, AppKit %.3f", i, pc.text[c:min(c+4, len(pc.text))], x, ax)
					}
				}
			}
			if compared < len(pc.text)/8 {
				t.Errorf("only %d glyph positions compared", compared)
			}
			if d := math.Abs(float64(v.Height)/sc - (ref[len(ref)-1].y + ref[len(ref)-1].h)); d > 0.02 {
				t.Errorf("height %.3f pt, AppKit %.3f", float64(v.Height)/sc, ref[len(ref)-1].y+ref[len(ref)-1].h)
			}
		})
	}
}

func TestParagraphDirectionsCoreText(t *testing.T) {
	s := newSys(t)
	f := s.Font(FontDesc{SizePt: 13, Scale: 2})
	txt := "First paragraph.\nשלום עולם\n123 שלום\n(hi)"
	p := Para{Text: []byte(txt), Fonts: []FontKey{f}, Width: 1000}
	check := func(dir Dir, want ...bool) {
		t.Helper()
		p.Dir = dir
		v := s.Paragraph(s.Layout(&p))
		if len(v.Lines) != len(want) {
			t.Fatalf("dir %d: %d lines", dir, len(v.Lines))
		}
		for i, w := range want {
			if got := v.Lines[i].Flags&LineRTL != 0; got != w {
				t.Errorf("dir %d line %d %q: rtl %v", dir, i, txt[v.Lines[i].Start:v.Lines[i].End], got)
			}
			if v.Lines[i].Flags&LineHard == 0 && i < len(want)-1 {
				t.Errorf("line %d: not ended by its newline", i)
			}
		}
		// A right-to-left line's glyphs run right to left: the first
		// character of the line is the rightmost glyph.
		for i, ln := range v.Lines {
			if ln.Flags&LineRTL == 0 || dir != DirAuto {
				continue
			}
			first, firstX := -1, float32(-1)
			for g := ln.G0; g < ln.G1; g++ {
				if int(v.Clusters[g]&^ClusterRTL) == int(ln.Start) {
					first, firstX = int(g), v.X[g]
				}
			}
			for g := ln.G0; g < ln.G1; g++ {
				if first >= 0 && v.X[g] > firstX+0.01 && v.Clusters[g]&ClusterRTL != 0 {
					t.Errorf("line %d: glyph %d right of the line's first character", i, g)
				}
			}
		}
	}
	check(DirAuto, false, true, true, false)
	check(DirLTR, false, false, false, false)
	check(DirRTL, true, true, true, true)
}

func TestParagraphEllipsisCoreText(t *testing.T) {
	s := newSys(t)
	f := s.Font(FontDesc{SizePt: 13, Scale: 2})
	for _, txt := range []string{
		"The list keeps its place while history loads above and new messages arrive below",
		"שלום לכולם, הפגישה תתקיים מחר בשעה עשר ואחר כך נדבר על הפרויקט",
		"short\nsecond paragraph that is cut",
	} {
		const w = 300
		p := Para{Text: []byte(txt), Fonts: []FontKey{f}, Width: w, MaxLines: 2, Ellipsis: true}
		v := s.Paragraph(s.Layout(&p))
		if len(v.Lines) != 2 || !v.Truncated {
			t.Fatalf("%q: %d lines, truncated %v", txt, len(v.Lines), v.Truncated)
		}
		ln := v.Lines[1]
		if ln.Flags&LineEllipsis == 0 || ln.Width > w+0.01 || ln.Width < w/3 {
			t.Errorf("%q: last line %q flags %b width %v", txt, txt[ln.Start:ln.End], ln.Flags, ln.Width)
		}
		// The ellipsis is the visually last glyph: rightmost, or leftmost on a
		// right-to-left line, and points at the cut.
		ell, ex := -1, float32(0)
		for g := ln.G0; g < ln.G1; g++ {
			if v.Clusters[g]&^ClusterRTL == ln.End && v.X[g] >= 0 {
				ell, ex = int(g), v.X[g]
			}
		}
		if ell < 0 {
			t.Fatalf("%q: no ellipsis glyph", txt)
		}
		for g := ln.G0; g < ln.G1; g++ {
			if int(g) == ell || v.X[g] < 0 {
				continue
			}
			if rtl := ln.Flags&LineRTL != 0; rtl && v.X[g] < ex || !rtl && v.X[g] > ex {
				t.Errorf("%q: glyph %d at %v is beyond the ellipsis at %v", txt, g, v.X[g], ex)
			}
		}
		// Without Ellipsis the text is just cut after two lines.
		p.Ellipsis = false
		if v = s.Paragraph(s.Layout(&p)); len(v.Lines) != 2 || v.Lines[1].Flags&LineEllipsis != 0 {
			t.Errorf("%q: cut without ellipsis: %d lines", txt, len(v.Lines))
		}
	}
}

// The ellipsis takes the font of the last character kept: the cut leaves room
// for it, it is drawn in it, and the line is as high as it makes it.
func TestParagraphEllipsisFontCoreText(t *testing.T) {
	s := newSys(t)
	small := s.Font(FontDesc{SizePt: 13, Scale: 2})
	big := s.Font(FontDesc{SizePt: 30, Scale: 2})
	faceOf := func(f FontKey) FaceID {
		v := s.Run(s.Shape(f, []byte("A")))
		return v.Faces[0]
	}
	lh := func(f FontKey) float32 { // AppKit's line height for the font alone
		v := s.Paragraph(s.Layout(&Para{Text: []byte("A"), Fonts: []FontKey{f}}))
		return v.Lines[0].Height
	}
	txt := "ABCDEFG more words"
	for _, c := range []struct {
		name  string
		span  Span
		width float32
		face  FaceID
		inBig bool
	}{
		{"cut in the big font", Span{Off: 0, Len: 7, Style: 1}, 180, faceOf(big), true},
		{"big font left out", Span{Off: 8, Len: 10, Style: 1}, 120, faceOf(small), false},
		{"nothing kept", Span{Off: 0, Len: 1, Style: 1}, 20, faceOf(big), true},
	} {
		p := Para{Text: []byte(txt), Spans: []Span{c.span}, Fonts: []FontKey{small, big}, Width: c.width, MaxLines: 1, Ellipsis: true}
		v := s.Paragraph(s.Layout(&p))
		ln := v.Lines[0]
		ell := -1
		for g := ln.G0; g < ln.G1; g++ {
			if v.Glyphs[g] != 0 && v.Clusters[g]&^ClusterRTL == ln.End {
				ell = int(g)
			}
		}
		if ell < 0 {
			t.Fatalf("%s: no ellipsis", c.name)
		}
		if v.Faces[ell] != c.face || (v.SpanOf[ell] == 1) != c.inBig {
			t.Errorf("%s: the ellipsis is in face %d span %d, want face %d", c.name, v.Faces[ell], v.SpanOf[ell], c.face)
		}
		if ln.Width > c.width+0.01 && ln.End > ln.Start {
			t.Errorf("%s: %q… is %v wide, over %v", c.name, txt[ln.Start:ln.End], ln.Width, c.width)
		}
		if want := lh(big); c.inBig && ln.Height < want-0.01 {
			t.Errorf("%s: line height %v, under the ellipsis font's %v", c.name, ln.Height, want)
		}
		t.Logf("%s: %q… width %v height %v", c.name, txt[ln.Start:ln.End], ln.Width, ln.Height)
	}
}

// Selection geometry on real layouts: part of a ligature, and the white
// space that hangs left of a right-to-left line broken after it.
func TestParagraphGeometryCoreText(t *testing.T) {
	s := newSys(t)
	hf := s.Font(FontDesc{Family: "Hoefler Text", SizePt: 13, Scale: 2})
	txt := "office fi"
	v := s.Paragraph(s.Layout(&Para{Text: []byte(txt), Fonts: []FontKey{hf}, Width: 1000}))
	if len(v.Glyphs) >= len(txt) {
		t.Fatalf("no ligatures: %d glyphs", len(v.Glyphs))
	}
	for _, r := range [][2]int{{1, 2}, {2, 3}, {3, 4}, {7, 8}, {8, 9}, {2, 5}} {
		rects := v.RangeRects(r[0], r[1], 1000, nil)
		_, a := v.Caret(r[0], 1000)
		_, b := v.Caret(r[1], 1000)
		if len(rects) != 1 || math.Abs(float64(rects[0][0]-a)) > 0.01 || math.Abs(float64(rects[0][2]-b)) > 0.01 || b-a < 1 {
			t.Errorf("%q [%d, %d): %v, carets %v..%v", txt, r[0], r[1], rects, a, b)
		}
	}

	f := s.Font(FontDesc{SizePt: 13, Scale: 2})
	txt = "שלום עולם   טוב מאוד"
	const w = 130
	v = s.Paragraph(s.Layout(&Para{Text: []byte(txt), Fonts: []FontKey{f}, Width: w}))
	ln := v.Lines[0]
	if got := txt[ln.Start:ln.End]; got != "שלום עולם   " {
		t.Fatalf("first line %q", got)
	}
	x0, x1 := v.LineExtent(0)
	left := v.LineLeft(0, w)
	if x0 >= 0 || math.Abs(float64(x1-ln.Width)) > 0.01 {
		t.Fatalf("extent %v..%v, line width %v advance %v", x0, x1, ln.Width, ln.Advance)
	}
	// The hanging spaces (bytes 17, 18, 19) are hit and covered left of the
	// origin.
	if g := v.GlyphAt(left+x0+1, 5, w); g < 0 || v.Clusters[g]&^ClusterRTL < 17 {
		t.Errorf("hanging space not hit: glyph %d", g)
	}
	if r := v.RangeRects(17, 20, w, nil); len(r) != 1 || math.Abs(float64(r[0][0]-(left+x0))) > 0.01 || math.Abs(float64(r[0][2]-left)) > 0.01 {
		t.Errorf("hanging spaces covered by %v, want %v..%v", r, left+x0, left)
	}
	// The rightmost cell, the first letter's, ends at the line's width.
	if g := v.GlyphAt(left+ln.Width-0.5, 5, w); g < 0 || v.Clusters[g]&^ClusterRTL != 0 {
		t.Errorf("the first letter is not at the right end: glyph %d", g)
	}
	if g := v.GlyphAt(left+ln.Width+0.5, 5, w); g >= 0 {
		t.Errorf("glyph %d right of the line", g)
	}
	if r := v.RangeRects(0, 2, w, nil); len(r) != 1 || math.Abs(float64(r[0][2]-(left+ln.Width))) > 0.01 {
		t.Errorf("the first letter covered by %v, line ends at %v", r, left+ln.Width)
	}
	for off := 0; off < int(ln.End); off = NextGrapheme([]byte(txt), off) {
		i, x := v.Caret(off, w)
		if got := v.HitTest(x, v.Lines[i].Top+1, w); got != off {
			t.Errorf("caret of %d at %v hit-tests to %d", off, x, got)
		}
	}
}

// Every caret position of a line hit-tests back to itself, on left-to-right
// and right-to-left lines.
func TestParagraphCaretRoundTripCoreText(t *testing.T) {
	s := newSys(t)
	f := s.Font(FontDesc{SizePt: 13, Scale: 2})
	for _, txt := range []string{
		"The list keeps its place while history loads",
		"שלום לכולם, הפגישה תתקיים מחר",
		"office fi flow 👍🏽 done",
	} {
		const w = 400
		v := s.Paragraph(s.Layout(&Para{Text: []byte(txt), Fonts: []FontKey{f}, Width: w}))
		for off := 0; off <= len(txt); off = NextGrapheme([]byte(txt), off) {
			i, x := v.Caret(off, w)
			ln := v.Lines[i]
			y := ln.Top + ln.Height/2
			if got := v.HitTest(x, y, w); got != off {
				t.Errorf("%q: caret of %d at x %.2f hit-tests to %d", txt, off, x, got)
			}
			if off == len(txt) {
				break
			}
		}
	}
}

// A fresh System reserved with ReserveRuns lays out new paragraphs through
// CoreText without allocating, its first layouts included: many lines, many
// spans and fonts, ellipses, bidirectional text, evictions. (Only the faces
// of the fallback fonts are found first, by shaping the same text: a face is
// made once per font.)
func TestParagraphReservedMissesCoreText(t *testing.T) {
	var nl []byte
	for i := 0; i < 5000; i++ { // 5000 lines
		if i%5 == 0 {
			nl = append(nl, "שלום ab"[:2+i%7]...)
		}
		nl = append(nl, '\n')
	}
	var styled []byte
	var spans []Span
	words := []string{"hello ", "שלום ", "مرحبا ", "👍🏽 ", "日本語 ", "link.com "}
	for i := 0; i < 300; i++ {
		w := words[i%len(words)]
		spans = append(spans, Span{Off: uint32(len(styled)), Len: uint32(len(w) - 1), Style: uint16(i % 32), Tag: uint32(i)})
		styled = append(styled, w...)
	}
	allocFree(t, "fresh reserved CoreText layouts", func() func() {
		s := newSys(t)
		s.ReserveRuns(1024, 1<<16, 1<<16)
		fonts := make([]FontKey, 32)
		for i := range fonts {
			fonts[i] = s.Font(FontDesc{SizePt: float32(11 + i%3*2), Weight: 400 + 300*(i%2), Scale: 2})
		}
		paras := []Para{
			{Text: nl, Fonts: fonts[:1], Width: 300},
			{Text: styled, Spans: spans, Fonts: fonts, Width: 240},
			{Text: styled, Spans: spans, Fonts: fonts, Width: 240, MaxLines: 4, Ellipsis: true},
			{Text: nl, Fonts: fonts[:3], Width: 300, MaxLines: 7, Ellipsis: true},
		}
		for i := 0; i < 1100; i++ { // past the 1024 layouts of the budget
			paras = append(paras, Para{Text: styled[:30+i%400], Spans: spans[:i%7], Fonts: fonts[:2+i%30], Width: float32(100 + i)})
		}
		for _, f := range fonts {
			s.Shape(f, styled)
			s.Shape(f, nl)
			s.Shape(f, []byte(ellipsis))
		}
		return func() {
			for i := range paras {
				s.Layout(&paras[i])
			}
		}
	})
}

func TestParagraphLayoutAllocationsCoreText(t *testing.T) {
	s := newSys(t)
	s.ReserveParas(ParaBudget{Paras: 4096, Glyphs: 1 << 18, Text: 1 << 18})
	f := s.Font(FontDesc{SizePt: 13, Scale: 2})
	b := s.Font(FontDesc{SizePt: 13, Weight: 700, Scale: 2})
	words := []string{"hello", "שלום", "مرحبا", "👍🏽", "日本語", "world", "link.com", "and"}
	txt := make([]byte, 0, 256)
	spans := make([]Span, 1)
	p := &Para{Fonts: []FontKey{f, b}, Width: 240, Spans: spans}
	n := 0
	make1 := func() {
		n++
		txt = txt[:0]
		for w := 0; w < 3+n%9; w++ {
			txt = append(append(txt, words[(n+w*3)%len(words)]...), ' ')
		}
		txt = strconv.AppendInt(txt, int64(n%5000), 10)
		spans[0] = Span{Off: 0, Len: uint32(len(words[n%len(words)])), Style: 1}
		p.Text = txt
	}
	for i := 0; i < 300; i++ { // the fonts, faces and fallbacks the words need
		make1()
		s.Layout(p)
	}
	allocFree(t, "laying out new paragraphs through CoreText", func() func() {
		return func() {
			for i := 0; i < 200; i++ {
				make1()
				s.Layout(p)
			}
		}
	})
	if a := testing.AllocsPerRun(100, func() { s.Layout(p) }); a != 0 {
		t.Fatalf("%v allocations per cache hit", a)
	}
}
