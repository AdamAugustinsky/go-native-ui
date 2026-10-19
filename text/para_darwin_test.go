//go:build darwin

package text

import (
	"fmt"
	"math"
	"runtime"
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
	// (DirAuto: the first strong character); natural alignment then puts a
	// right-to-left paragraph at the right. Without it AppKit takes the
	// alignment from the UI language and sets Hebrew flush left.
	psName := uintptr(objc.Str("NSParagraphStyle"))
	for ps := 0; ps < len(str); {
		pe := paraEnd([]byte(str), ps)
		if pe < len(str) {
			pe += newlineAt([]byte(str), pe)
		}
		st := fr.Reset().Arg(alloc("NSMutableParagraphStyle")).Arg(uintptr(objc.Sel("init"))).Call(objc.MsgSend).R0()
		dir := uintptr(0)
		if paraStrongRTL([]byte(str[ps:])) {
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
