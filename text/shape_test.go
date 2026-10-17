//go:build linux || textengine

package text

import (
	"math"
	"math/rand"
	"slices"
	"strings"
	"testing"

	"github.com/go-text/typesetting/bidi"
	"github.com/go-text/typesetting/di"
	"github.com/go-text/typesetting/shaping"
)

// The shaping driver in shape.go replaces go-text's Segmenter + HarfbuzzShaper
// for left-to-right text. These tests keep the old pipeline as a reference
// and require the same glyphs, positions and faces on a mixed corpus and on
// random text, plus the itemization to equal the Segmenter's piece by piece.

// refShaper is the shaping path the engine used before shape.go: go-text's
// bidi paragraphs, Segmenter and HarfbuzzShaper, level run by level run.
type refShaper struct {
	s      *goSystem
	shaper shaping.HarfbuzzShaper
}

// go-text v0.3.5 bug: a reused bidi.Paragraph (also inside a reused
// shaping.Segmenter) can give results that depend on earlier texts, for
// example "ر>ح" shapes as a left-to-right run after "a\n". The reference
// below therefore uses fresh values for every call, which is also what the
// engine now gets for the left-to-right paragraphs it no longer sends there.

// setSize assigns a whole number of pixels to a shaping.Input.Size (a 26.6
// fixed-point integer type from x/image, which we need not import).
func setSize[T ~int32](dst *T, px int) { *dst = T(px << 6) }

// shape shapes str with the go-text pipeline. With legacy the paragraphs
// are cut exactly as the engine did before shape.go: go-text's bidi stops
// before a separator, a text that then starts with one has no level runs,
// and all of the rest of the text is shaped as one left-to-right run (so
// right-to-left text after a newline came out wrong). Without it paragraphs
// are cut as shape.go does: a separator starts a paragraph, except that the
// rest of the text from a separator on is one paragraph if it needs no bidi.
func (r *refShaper) shape(f FontKey, str string, legacy bool) (glyphs []uint16, x []float32, faces []FaceID, pen float32) {
	s := r.s
	s.cur = f
	runes := []rune(str)
	levelRuns := func(bruns []bidiRun) {
		s.bruns = bruns
		s.visualOrder()
		for _, i := range slices.Clone(s.order) {
			br := bruns[i]
			fe := &s.fonts[f]
			dir := di.DirectionLTR
			if br.level%2 == 1 {
				dir = di.DirectionRTL
			}
			ceilPx := int(math.Ceil(float64(fe.px)))
			k := fe.px / float32(ceilPx) / 64
			in := shaping.Input{Text: runes, RunStart: br.start, RunEnd: br.end, Direction: dir, Face: s.faces[fe.face].face}
			setSize(&in.Size, ceilPx)
			var seg shaping.Segmenter
			pieces := seg.Split(in, s)
			emit := func(p shaping.Input) {
				p.Direction = dir
				if p.Face == nil {
					p.Face = in.Face
				}
				fid := s.faceOf[p.Face]
				out := r.shaper.ShapeNoExtents(p)
				for _, g := range out.Glyphs {
					gid := uint16(0xFFFF)
					if g.GlyphID < 0xFFFF {
						gid = uint16(g.GlyphID)
					}
					glyphs = append(glyphs, gid)
					x = append(x, pen+float32(int32(g.XOffset))*k)
					faces = append(faces, fid)
					pen += float32(int32(g.Advance)) * k
				}
			}
			if dir == di.DirectionRTL {
				for i := len(pieces) - 1; i >= 0; i-- {
					emit(pieces[i])
				}
			} else {
				for _, p := range pieces {
					emit(p)
				}
			}
		}
	}
	segment := func(from, to int) (bruns []bidiRun) {
		var para bidi.Paragraph
		runs := para.Segment(runes[from:to], bidi.Neutral)
		for i := 0; i < runs.NumRuns(); i++ {
			ru := runs.Run(i)
			bruns = append(bruns, bidiRun{from + ru.Start, from + ru.End, int(ru.Level)})
		}
		return
	}
	for start := 0; start < len(runes); {
		if legacy {
			bruns := segment(start, len(runes))
			end := len(runes)
			if n := len(bruns); n > 0 {
				end = bruns[n-1].end
			} else {
				bruns = append(bruns, bidiRun{start, end, 0})
			}
			levelRuns(bruns)
			start = end
			continue
		}
		end, body := start+1, start
		if isSeparator(runes[start]) {
			body++
		}
		for ; end < len(runes) && !isSeparator(runes[end]); end++ {
		}
		if body > start && allLTR(runes[start:]) {
			end = len(runes)
		}
		if allLTR(runes[start:end]) {
			levelRuns([]bidiRun{{start, end, 0}})
		} else {
			if body > start {
				levelRuns([]bidiRun{{start, body, 0}})
			}
			levelRuns(segment(body, end))
		}
		start = end
	}
	return
}

func allLTR(rs []rune) bool {
	for _, r := range rs {
		if r >= 0x590 && !ltrRune(r) {
			return false
		}
	}
	return true
}

// shapeEnv is an engine with three fonts and fallback to both Arabic and
// Latin faces, so that face changes happen inside runs.
func shapeEnv(t *testing.T) (*goSystem, []FontKey) {
	t.Helper()
	roboto := fontRef{file: testFont(t, "Roboto-Regular.ttf")}
	amiri := fontRef{file: testFont(t, "Amiri-Regular.ttf")}
	mono := fontRef{file: testFont(t, "UbuntuMono-R.ttf")}
	ff := &fakeFinder{
		ui: roboto, mono: mono,
		families: map[string]fontRef{"amiri": amiri},
		fallbackFn: func(r rune) (fontRef, bool) {
			if r >= 0x600 && r <= 0x77F || r >= 0xFB50 && r <= 0xFEFF {
				return amiri, true
			}
			return roboto, true
		},
	}
	s := newGoSystem(ff)
	t.Cleanup(s.Close)
	return s, []FontKey{
		s.Font(FontDesc{SizePt: 13, Scale: 2, Weight: 400}),
		s.Font(FontDesc{Mono: true, SizePt: 10.5, Scale: 1.5}), // fractional pixel size
		s.Font(FontDesc{Family: "amiri", SizePt: 15, Scale: 2}),
	}
}

var shapeCorpus = []string{
	"", " ", "a", "Hello, world", "file_0001234.dat 12.3 KB 2026-10-17", "AVATAR To Ty fi fl ffi",
	"  leading and trailing  ", "tab\tseparated\tvalues", "two\nparagraphs\r\nand a third\u2029fourth",
	"(parenthesised) [brackets] {braces} «guillemets» “quotes” ‘single’",
	"Привет, мир! Γειά σου κόσμε", "naïve café déjà vu", "e\u0301 a\u0308\u0323 combining", "Å \u212b ﬁ",
	"文件_123.dat 日本語のテキスト ひらがな カタカナ 한국어", "(你好) [世界] mixed (Latin) 文字",
	"abc مرحبا def", "مرحبا abc", "مرحبا بالعالم 123", "abc (مرحبا) def", "אבג hebrew", "a אבג b",
	"1.5 مرحبا 2.5", "\u200Fمرحبا\u200E latin", "\u202Bmixed\u202C \u2067embedded\u2069",
	"👨\u200D👩\u200D👧 family 🇧🇷 flag ❤\uFE0F heart 👍🏽 thumbs", "emoji 😀 and text", "\u200D\u200C joiners \u200D",
	"\u00A0nbsp\u2003em space\u3000ideographic", "\u0000control\u0001chars\u007F", "bad utf8 \xff\xfe end",
	"ünï\u0308cödé \u1E9B\u0323", "ไทย ภาษาไทย", "हिन्दी देवनागरी", "ሰላም", "Ελληνικά (Greek) и Русский",
}

func compareShaping(t *testing.T, s *goSystem, ref *refShaper, f FontKey, str string) {
	t.Helper()
	v := s.Run(s.Shape(f, []byte(str)))
	check := func(legacy bool) {
		t.Helper()
		g, x, fc, pen := ref.shape(f, str, legacy)
		if !slices.Equal(v.Glyphs, g) || !slices.Equal(v.X, x) || !slices.Equal(v.Faces, fc) || v.Advance != pen {
			t.Fatalf("font %d, %q (legacy paragraphs: %v):\n got  glyphs %v\n      X %v\n      faces %v\n      advance %v\n want glyphs %v\n      X %v\n      faces %v\n      advance %v",
				f, str, legacy, v.Glyphs, v.X, v.Faces, v.Advance, g, x, fc, pen)
		}
	}
	check(false)
	// The old paragraph cutting only differs, apart from where a separator
	// is shaped, when right-to-left text follows a separator.
	if i := strings.IndexFunc(str, isSeparator); i < 0 || allLTR([]rune(str[i:])) {
		check(true)
	}
}

func TestShapeMatchesSegmenterPipeline(t *testing.T) {
	s, fonts := shapeEnv(t)
	ref := &refShaper{s: s}
	for _, f := range fonts {
		for _, str := range shapeCorpus {
			compareShaping(t, s, ref, f, str)
		}
	}
}

func TestShapeMatchesSegmenterPipelineRandom(t *testing.T) {
	s, fonts := shapeEnv(t)
	ref := &refShaper{s: s}
	pool := []rune("abcxyzABCXYZ0123456789 .,:;-_/()[]{}<>\"'«»“”\t\n\u00A0\u00E9\u0301\u0308\u0323" +
		"ÀÉÎÕÜçñß ﬁ Привет ΑΒΓαβγ 文件日本語ひらがなカタカナ한국어 مرحباً ٣٤ אבג \u200D\u200C\u200E\u200F\u202B\u202C\u2067\u2069" +
		"👨👩👍🏽🇧🇷❤\uFE0F😀 \u3000\u2003ไทยहिन्दी\u2029")
	rng := rand.New(rand.NewSource(1))
	n := 6000
	if testing.Short() {
		n = 800
	}
	for i := 0; i < n; i++ {
		l := 1 + rng.Intn(24)
		rs := make([]rune, l)
		for k := range rs {
			rs[k] = pool[rng.Intn(len(pool))]
		}
		compareShaping(t, s, ref, fonts[i%len(fonts)], string(rs))
	}
}

// TestSplitMatchesSegmenter compares the itemization (script, language and
// face runs) with shaping.Segmenter.Split on every text that takes the
// left-to-right path.
func TestSplitMatchesSegmenter(t *testing.T) {
	s, fonts := shapeEnv(t)
	texts := slices.Clone(shapeCorpus)
	rng := rand.New(rand.NewSource(2))
	pool := []rune("abc xyz 012 .,-_()[]{}«»“”\u00E9\u0301 Привет Γεια 文件日本語ひらがな한국어 \u200D\u200C 👨👩🇧🇷❤\uFE0F😀 \u3000ไทยहिन्दी") // no right-to-left text
	for i := 0; i < 3000; i++ {
		rs := make([]rune, 1+rng.Intn(20))
		for k := range rs {
			rs[k] = pool[rng.Intn(len(pool))]
		}
		texts = append(texts, string(rs))
	}
	checked := 0
	for i, str := range texts {
		f := fonts[i%len(fonts)]
		s.cur = f
		runes := []rune(str)
		s.runes = runes
		for start := 0; start < len(runes); {
			end := s.paragraphEnd(start)
			pend := end // go-text's paragraphs end before their separator
			if isSeparator(runes[end-1]) {
				pend--
			}
			if pend > start && s.ltrOnly(start, pend) {
				in := shaping.Input{Text: runes, RunStart: start, RunEnd: pend, Direction: di.DirectionLTR, Face: s.faces[s.fonts[f].face].face}
				setSize(&in.Size, 26)
				var seg shaping.Segmenter
				want := seg.Split(in, s)
				s.splitScripts(start, pend)
				s.segs = s.segs[:0]
				s.splitFaces(f)
				if len(want) != len(s.segs) {
					t.Fatalf("%q: %d pieces, Segmenter gives %d", str[:min(len(str), 40)], len(s.segs), len(want))
				}
				for k, w := range want {
					g := s.segs[k]
					fid := s.faceOf[w.Face]
					if g.start != w.RunStart || g.end != w.RunEnd || g.script != w.Script || g.lang != w.Language || g.face != fid {
						t.Fatalf("%q piece %d: got %+v, Segmenter %d..%d script %v lang %q face %d", string(runes[start:pend]), k, g, w.RunStart, w.RunEnd, w.Script, w.Language, fid)
					}
				}
				checked++
			}
			start = end
		}
	}
	if checked < 1000 {
		t.Fatalf("only %d paragraphs took the left-to-right path", checked)
	}
}

// TestLTRRunesAreLevelZero checks the claim behind the left-to-right path
// against go-text's bidi algorithm for every code point: a rune ltrRune
// accepts never produces a right-to-left run, alone or in context.
func TestLTRRunesAreLevelZero(t *testing.T) {
	var p bidi.Paragraph
	check := func(text []rune, r rune) {
		runs := p.Segment(text, bidi.Neutral)
		for i := 0; i < runs.NumRuns(); i++ {
			if ru := runs.Run(i); ru.Level != 0 {
				t.Fatalf("U+%04X in %U has bidi level %d", r, text, ru.Level)
			}
		}
	}
	for r := rune(0x80); r <= 0x10FFFF; r++ {
		if r >= 0xD800 && r < 0xE000 || (r >= 0x590 && !ltrRune(r)) {
			continue
		}
		check([]rune{r}, r)
		check([]rune{r, 'a', '1'}, r)
		check([]rune{'1', r}, r)
		check([]rune{' ', '(', r, ')'}, r)
	}
}

func TestLangForMatchesSegmenter(t *testing.T) {
	s, _ := shapeEnv(t)
	for _, str := range []string{"abc", "Привет", "한국어", "文字", "ไทย", "ελ", " ", "ሰላም", "مرحبا"} {
		runes := []rune(str)
		in := shaping.Input{Text: runes, RunStart: 0, RunEnd: len(runes), Direction: di.DirectionLTR, Face: s.faces[s.fonts[0].face].face}
		s.cur = 0
		var seg shaping.Segmenter
		for _, p := range seg.Split(in, s) {
			if got := s.langFor(p.Script); got != p.Language {
				t.Errorf("%q: language for script %v is %q, Segmenter gives %q", str, p.Script, got, p.Language)
			}
		}
	}
}
