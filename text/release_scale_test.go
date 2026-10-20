//go:build darwin || windows

package text

import (
	"fmt"
	"strings"
	"testing"
)

func fontsAt(s *System, scale float32) (ui, mono FontKey) {
	return s.Font(FontDesc{SizePt: 13, Scale: scale, Weight: 400}), s.Font(FontDesc{SizePt: 12, Scale: scale, Weight: 400, Mono: true})
}

// A mixed string: Latin, CJK and emoji, which need fallback faces.
const mixedText = "Hello 日本語 😀"

// Releasing a scale frees its fonts and faces (fallbacks too) for reuse and
// leaves every other scale as it was: the same keys, faces and glyph entries.
func TestReleaseScale(t *testing.T) {
	s := newSys(t)
	scales := []float32{1, 1.25, 1.5, 2, 3}
	type made struct {
		ui, mono FontKey
		adv      float32
		faces    []FaceID
	}
	m := map[float32]made{}
	for _, sc := range scales {
		u, mo := fontsAt(s, sc)
		v := shapeStr(s, u, mixedText)
		m[sc] = made{u, mo, v.Advance, append([]FaceID(nil), v.Faces...)}
	}
	if got := s.Scales(); len(got) != len(scales) {
		t.Fatalf("scales %v", got)
	}
	keep := m[2]
	entry := s.Glyph(keep.faces[0], shapeStr(s, keep.ui, mixedText).Glyphs[0], 0)
	gen, nFonts, nFaces := s.AtlasGeneration(), len(s.fonts), len(s.faces)
	fallbackFaces := 0
	for _, f := range m[1.25].faces {
		if f != m[1.25].faces[0] {
			fallbackFaces++
		}
	}
	if fallbackFaces == 0 {
		t.Log("(this machine's fonts need no fallback face for the mixed text)")
	}

	if n := s.ReleaseScale(1.25); n != 2 {
		t.Fatalf("ReleaseScale released %d fonts, want 2", n)
	}
	if n := s.ReleaseScale(1.25); n != 0 {
		t.Errorf("a second ReleaseScale released %d fonts", n)
	}
	if got := s.Scales(); len(got) != 4 {
		t.Errorf("scales after the release: %v", got)
	}
	if s.Shape(m[1.25].ui, []byte("x")) != 0 || s.Metrics(m[1.25].ui) != (Metrics{}) {
		t.Error("a released key still shapes or has metrics")
	}
	for _, f := range m[1.25].faces {
		if e := s.Glyph(f, 1, 0); e != (GlyphEntry{}) {
			t.Errorf("a released face %d still has glyphs", f)
		}
	}
	// The other scales are untouched: keys, shaping, glyph entries, generation.
	for _, sc := range []float32{1, 1.5, 2, 3} {
		u, _ := fontsAt(s, sc)
		if u != m[sc].ui {
			t.Errorf("scale %v: key %d, was %d", sc, u, m[sc].ui)
		}
		if v := shapeStr(s, u, mixedText); v.Advance != m[sc].adv {
			t.Errorf("scale %v: advance %v, was %v", sc, v.Advance, m[sc].adv)
		}
	}
	if e := s.Glyph(keep.faces[0], shapeStr(s, keep.ui, mixedText).Glyphs[0], 0); e != entry || s.AtlasGeneration() != gen {
		t.Errorf("a glyph of a kept scale moved or the atlas was reset: %v -> %v, generation %d -> %d", entry, e, gen, s.AtlasGeneration())
	}

	// A new scale takes the freed slots, and the released one can come back.
	u, mo := fontsAt(s, 1.75)
	if int(u) >= nFonts || int(mo) >= nFonts || len(s.fonts) != nFonts {
		t.Errorf("new keys %d, %d, %d fonts (was %d): the freed slots were not reused", u, mo, len(s.fonts), nFonts)
	}
	shapeStr(s, u, mixedText)
	if len(s.faces) > nFaces {
		t.Errorf("%d faces (was %d): the freed faces were not reused", len(s.faces), nFaces)
	}
	back, _ := fontsAt(s, 1.25)
	if v := shapeStr(s, back, mixedText); v.Advance != m[1.25].adv {
		t.Errorf("scale 1.25 again: advance %v, was %v", v.Advance, m[1.25].adv)
	}
}

// Many scales used one after another, the oldest released each time: the fonts
// and faces stay a few scales' worth.
func TestReleaseScaleBoundsTheTables(t *testing.T) {
	s := newSys(t)
	var live []float32
	for i := 0; i < 100; i++ {
		sc := 1 + float32(i)/100
		u, mo := fontsAt(s, sc)
		shapeStr(s, u, mixedText)
		shapeStr(s, mo, "0123")
		live = append(live, sc)
		if len(live) > 4 {
			s.ReleaseScale(live[0])
			live = live[1:]
		}
	}
	if got := s.Scales(); len(got) != 4 {
		t.Errorf("scales %v, want 4", got)
	}
	if len(s.fonts) > 10 || len(s.faces) > 40 {
		t.Errorf("%d fonts and %d faces after 100 scales", len(s.fonts), len(s.faces))
	}
}

func overBudgetPara(f FontKey) Para {
	return Para{Text: []byte(strings.Repeat("abcdefg h", 40)), Fonts: []FontKey{f}, Width: 300}
}

func paraWidths(v ParaView) []float32 {
	w := make([]float32, len(v.Lines))
	for i, l := range v.Lines {
		w[i] = l.Width
	}
	return w
}

// A layout outside the paragraph cache's budget does not outlive the fonts it
// was made with: the key of a released scale is reused by another, and the
// same paragraph is laid out afresh.
func TestReleaseScaleForgetsTheOverBudgetLayout(t *testing.T) {
	budget := ParaBudget{Paras: 8, Glyphs: 200, Text: 100}
	s := newSys(t)
	s.ReserveParas(budget)
	old := s.Font(FontDesc{SizePt: 13, Scale: 1, Weight: 400})
	p := overBudgetPara(old)
	if r := s.Layout(&p); r != overRef {
		t.Fatalf("the paragraph is not over the budget: ref %d", r)
	}
	before := paraWidths(s.Paragraph(overRef))
	if n := s.ReleaseScale(1); n != 1 {
		t.Fatalf("released %d fonts", n)
	}
	reused := s.Font(FontDesc{SizePt: 13, Scale: 2, Weight: 400})
	if reused != old {
		t.Fatalf("the slot was not reused: %d, was %d", reused, old)
	}
	p = overBudgetPara(reused)
	s.Layout(&p)
	got := paraWidths(s.Paragraph(overRef))

	fresh := newSys(t)
	fresh.ReserveParas(budget)
	fp := overBudgetPara(fresh.Font(FontDesc{SizePt: 13, Scale: 2, Weight: 400}))
	fresh.Layout(&fp)
	want := paraWidths(fresh.Paragraph(overRef))
	if len(got) == 0 || fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("after the reuse: %v, a fresh layout: %v (before the release: %v)", got, want, before)
	}
}
