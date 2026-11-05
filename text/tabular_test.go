//go:build darwin || windows

package text

import (
	"math"
	"testing"
)

// TabularDigits sets every digit at one width (the system UI font's default
// digits are proportional on macOS), caches apart from the same font without
// it, and leaves other characters alone.
func TestTabularDigits(t *testing.T) {
	s := newSys(t)
	d := FontDesc{SizePt: 13, Scale: 2, Weight: 400}
	dt := d
	dt.TabularDigits = true
	plain, tab := s.Font(d), s.Font(dt)
	if plain == tab {
		t.Fatal("the font with tabular digits is the font without them")
	}
	if again := s.Font(dt); again != tab {
		t.Fatalf("the same description made font %d, then %d", tab, again)
	}
	width := func(f FontKey, str string) float32 { return shapeStr(s, f, str).Advance }
	for _, c := range "0123456789" {
		if w, zero := width(tab, string(c)), width(tab, "0"); w != zero {
			t.Errorf("tabular %c is %v px wide, 0 is %v", c, w, zero)
		}
	}
	if got, want := width(tab, "111"), 3*width(tab, "0"); math.Abs(float64(got-want)) > 0.01 {
		t.Errorf("111 is %v px wide, want 3 zeros = %v", got, want)
	}
	if got, want := s.Metrics(tab).MonoAdvance, width(tab, "0"); got != want {
		t.Errorf("MonoAdvance %v, want the advance of 0 = %v", got, want)
	}
	if got, want := width(tab, "il"), width(plain, "il"); got != want {
		t.Errorf("letters changed width: %v, want %v", got, want)
	}
	// The feature survives the weight and italic derivations of the font.
	for _, v := range []FontDesc{{SizePt: 13, Weight: 600}, {SizePt: 13, Italic: true}} {
		v.TabularDigits = true
		if f := s.Font(v); width(f, "1") != width(f, "0") {
			t.Errorf("%+v: digits are proportional", v)
		}
	}
}

// Tracking adds its spacing after every character, in runs and in paragraphs
// alike, and caches apart from the same font without it.
func TestTracking(t *testing.T) {
	s := newSys(t)
	d := FontDesc{SizePt: 20, Scale: 2, Weight: 500}
	dt := d
	dt.Tracking = -0.025 // a web heading's letter-spacing: -1 px at 20 pt and 2×
	plain, tight := s.Font(d), s.Font(dt)
	if plain == tight {
		t.Fatal("the tracked font is the font without tracking")
	}
	const str = "Construir"
	a, b := shapeStr(s, plain, str), shapeStr(s, tight, str)
	step := dt.Tracking * d.SizePt * d.Scale
	n := float32(len(str))
	if got, want := b.Advance-a.Advance, n*step; math.Abs(float64(got-want)) > 0.05 {
		t.Fatalf("tracked run is %v px longer, want %v", got, want)
	}
	for i := range b.X {
		if got, want := b.X[i]-a.X[i], float32(i)*step; math.Abs(float64(got-want)) > 0.05 {
			t.Fatalf("glyph %d moved %v px, want %v", i, got, want)
		}
	}
	width := func(f FontKey) float32 {
		pv := s.Paragraph(s.Layout(&Para{Text: []byte(str), Fonts: []FontKey{f}}))
		return pv.Lines[0].Width
	}
	if got, want := width(tight)-width(plain), n*step; math.Abs(float64(got-want)) > 0.05 {
		t.Fatalf("tracked paragraph line is %v px longer, want %v", got, want)
	}
}
