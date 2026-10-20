//go:build linux || textengine

package text

import "testing"

func engineFontsAt(s *goSystem, scale float32) (FontKey, FontKey) {
	return s.Font(FontDesc{SizePt: 13, Scale: scale, Weight: 400}), s.Font(FontDesc{SizePt: 12, Scale: scale, Weight: 400, Mono: true})
}

// Latin, and Arabic that needs a fallback face.
const engineMixed = "Hello مرحبا"

// Releasing a scale frees its fonts and faces (fallbacks included) for reuse
// and leaves every other scale as it was: the same keys, faces and glyph
// entries, with no atlas reset.
func TestEngineReleaseScale(t *testing.T) {
	s, _ := newEngine(t)
	scales := []float32{1, 1.25, 1.5, 2, 3}
	type made struct {
		ui  FontKey
		adv float32
	}
	m := map[float32]made{}
	var faces125 []FaceID
	for _, sc := range scales {
		u, mo := engineFontsAt(s, sc)
		v := eShape(s, u, engineMixed)
		eShape(s, mo, "0123")
		m[sc] = made{u, v.Advance}
		if sc == 1.25 {
			faces125 = append(faces125, v.Faces...)
		}
	}
	if len(s.Scales()) != len(scales) {
		t.Fatalf("scales %v", s.Scales())
	}
	keepFaces := eShape(s, m[2].ui, engineMixed).Faces
	entry := s.Glyph(keepFaces[0], eShape(s, m[2].ui, engineMixed).Glyphs[0], 0)
	gen, nFonts, nFaces := s.AtlasGeneration(), len(s.fonts), len(s.faces)
	arabic := false
	for _, f := range faces125 {
		arabic = arabic || f != faces125[0]
	}
	if !arabic {
		t.Fatal("the mixed text did not need a fallback face")
	}

	if n := s.ReleaseScale(1.25); n != 2 {
		t.Fatalf("released %d fonts, want 2", n)
	}
	if n := s.ReleaseScale(1.25); n != 0 {
		t.Errorf("a second release freed %d fonts", n)
	}
	if len(s.Scales()) != 4 {
		t.Errorf("scales %v", s.Scales())
	}
	if s.Shape(m[1.25].ui, []byte("x")) != 0 || s.Metrics(m[1.25].ui) != (Metrics{}) {
		t.Error("a released key still shapes or has metrics")
	}
	for _, f := range faces125 {
		if e := s.Glyph(f, 1, 0); e != (GlyphEntry{}) {
			t.Errorf("a released face %d still has glyphs", f)
		}
	}
	for _, sc := range []float32{1, 1.5, 2, 3} {
		u, _ := engineFontsAt(s, sc)
		if u != m[sc].ui {
			t.Errorf("scale %v: key %d, was %d", sc, u, m[sc].ui)
		}
		if v := eShape(s, u, engineMixed); v.Advance != m[sc].adv {
			t.Errorf("scale %v: advance %v, was %v", sc, v.Advance, m[sc].adv)
		}
	}
	if e := s.Glyph(keepFaces[0], eShape(s, m[2].ui, engineMixed).Glyphs[0], 0); e != entry || s.AtlasGeneration() != gen {
		t.Errorf("a glyph of a kept scale moved or the atlas was reset: %v -> %v", entry, e)
	}

	u, mo := engineFontsAt(s, 1.75)
	if len(s.fonts) != nFonts || int(u) >= nFonts || int(mo) >= nFonts {
		t.Errorf("keys %d, %d with %d fonts (was %d): the slots were not reused", u, mo, len(s.fonts), nFonts)
	}
	eShape(s, u, engineMixed)
	if len(s.faces) > nFaces {
		t.Errorf("%d faces (was %d): the freed faces were not reused", len(s.faces), nFaces)
	}
	back, _ := engineFontsAt(s, 1.25)
	if v := eShape(s, back, engineMixed); v.Advance != m[1.25].adv {
		t.Errorf("scale 1.25 again: advance %v, was %v", v.Advance, m[1.25].adv)
	}
}

func TestEngineReleaseScaleBoundsTheTables(t *testing.T) {
	s, _ := newEngine(t)
	var live []float32
	for i := 0; i < 100; i++ {
		sc := 1 + float32(i)/100
		u, mo := engineFontsAt(s, sc)
		eShape(s, u, engineMixed)
		eShape(s, mo, "0123")
		live = append(live, sc)
		if len(live) > 4 {
			s.ReleaseScale(live[0])
			live = live[1:]
		}
	}
	if len(s.Scales()) != 4 {
		t.Errorf("scales %v, want 4", s.Scales())
	}
	if len(s.fonts) > 10 || len(s.faces) > 40 {
		t.Errorf("%d fonts and %d faces after 100 scales", len(s.fonts), len(s.faces))
	}
	if len(s.fb) > 40 {
		t.Errorf("%d fallback decisions kept", len(s.fb))
	}
}
