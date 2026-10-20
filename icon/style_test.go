package icon

import (
	"testing"

	"gonativeui/theme"
)

// A style's point size and weight are the theme's text style of the same name.
func TestStylesFollowTheTextStyles(t *testing.T) {
	for _, c := range []struct {
		st TextStyle
		ts theme.TextStyle
	}{
		{StyleBody, theme.StyleBody}, {StyleCallout, theme.StyleCallout}, {StyleSubheadline, theme.StyleSubheadline},
		{StyleFootnote, theme.StyleFootnote}, {StyleCaption1, theme.StyleCaption1}, {StyleCaption2, theme.StyleCaption2},
		{StyleHeadline, theme.StyleHeadline}, {StyleTitle3, theme.StyleTitle3}, {StyleTitle2, theme.StyleTitle2},
		{StyleTitle1, theme.StyleTitle1}, {StyleLargeTitle, theme.StyleLargeTitle},
	} {
		spec := theme.MacText[c.ts]
		if c.st.Pt() != spec.Size {
			t.Errorf("style %d: %v pt, the text style's %v", c.st, c.st.Pt(), spec.Size)
		}
		var w float32
		switch spec.Weight {
		case theme.WeightMedium:
			w = 0.23
		case theme.WeightSemibold:
			w = 0.3
		case theme.WeightBold:
			w = 0.4
		}
		if c.st.Weight() != w {
			t.Errorf("style %d: weight %v, the text style's %v", c.st, c.st.Weight(), w)
		}
	}
}

func TestSpecIn(t *testing.T) {
	base := Spec{Symbol: "plus", Segoe: 0xE710, Fallback: '+', Pt: 99, Weight: 0.9}
	got := base.In(StyleHeadline, ScaleLarge)
	want := Spec{Symbol: "plus", Segoe: 0xE710, Fallback: '+', Pt: 13, Weight: 0.4, Scale: ScaleLarge}
	if got != want {
		t.Errorf("In: %+v, want %+v", got, want)
	}
	if base.Pt != 99 {
		t.Error("In changed its receiver")
	}
	if z := (Spec{Symbol: "plus"}).In(StyleBody, ScaleMedium); z.Pt != 13 || z.Weight != 0 || z.Scale != 0 {
		t.Errorf("body at medium: %+v", z)
	}
	if TextStyle(200).Pt() != 13 {
		t.Error("an out-of-range style is body")
	}
}

// Where there is no symbol, the glyph is set at the style's size and the
// scale's factor: the drawn icons of Windows and Linux follow the scales.
func TestGlyphSizeFollowsStyleAndScale(t *testing.T) {
	ts := newText(t)
	g := Spec{Fallback: '+'}
	a := New(ts, g.In(StyleBody, ScaleMedium), g.In(StyleBody, ScaleLarge), g.In(StyleBody, ScaleSmall), g.In(StyleTitle1, ScaleMedium))
	a.Raster(1)
	h := func(i int) float32 { _, hh := a.Size(NumStandard + ID(i)); return hh }
	med, large, small, title := h(0), h(1), h(2), h(3)
	if med <= 0 || !(small < med && med < large && large < title) {
		t.Errorf("heights: small %v, medium %v, large %v, title 1 %v", small, med, large, title)
	}
}
