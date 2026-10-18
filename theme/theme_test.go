package theme

import (
	"reflect"
	"testing"

	"gonativeui/text"
)

// fakeText hands out font keys per distinct description and fixed metrics.
type fakeText struct {
	descs []text.FontDesc
}

func (f *fakeText) Font(d text.FontDesc) text.FontKey {
	for i, e := range f.descs {
		if e == d {
			return text.FontKey(i + 1)
		}
	}
	f.descs = append(f.descs, d)
	return text.FontKey(len(f.descs))
}

func (f *fakeText) Metrics(k text.FontKey) text.Metrics {
	d := f.descs[k-1]
	return text.Metrics{Ascent: d.SizePt * d.Scale * 0.8, Descent: d.SizePt * d.Scale * 0.2}
}

// testTheme has every palette field set, distinct per field and per mode.
func testTheme() *Theme {
	t := &Theme{
		Name: "test",
		Metrics: Metrics{
			RadiusSmall: 2, RadiusMedium: 4, RadiusLarge: 8, Pad: 8, Gap: 6, GapSmall: 3,
			ControlH: 28, RowH: 24, IconSize: 16, BorderW: 0.5, FocusRingW: 2, ScrollbarW: 12, ScrollThumbW: 4,
			TitlebarH: 40, Inset: 8, Gutter: 6, DividerGrab: 12, WindowButtonW: 40,
		},
		Fonts: Fonts{
			UI: text.FontDesc{SizePt: 14, Weight: 400}, UISemi: text.FontDesc{SizePt: 14, Weight: 600},
			Small: text.FontDesc{SizePt: 11, Weight: 400}, Mono: text.FontDesc{Mono: true, SizePt: 12, Weight: 400},
			MonoBold: text.FontDesc{Mono: true, SizePt: 12, Weight: 700}, Symbols: text.FontDesc{SizePt: 12, Weight: 400},
		},
	}
	fill := func(p *Palette, base uint32) {
		v := reflect.ValueOf(p).Elem()
		for i := 0; i < v.NumField(); i++ {
			v.Field(i).SetUint(uint64(base + uint32(i)*0x01020304))
		}
	}
	fill(&t.Light, 0x10203040)
	fill(&t.Dark, 0xA0B0C0D0)
	return t
}

func TestMixPalettesMixesEveryRole(t *testing.T) {
	th := testTheme()
	if got := MixPalettes(&th.Light, &th.Dark, 0); got != th.Light {
		t.Errorf("t = 0 is not the light palette: %+v", got)
	}
	if got := MixPalettes(&th.Light, &th.Dark, 1); got != th.Dark {
		t.Errorf("t = 1 is not the dark palette: %+v", got)
	}
	mid := reflect.ValueOf(MixPalettes(&th.Light, &th.Dark, 0.5))
	l, d := reflect.ValueOf(th.Light), reflect.ValueOf(th.Dark)
	for i := 0; i < mid.NumField(); i++ {
		if m := mid.Field(i).Uint(); m == l.Field(i).Uint() || m == d.Field(i).Uint() {
			t.Errorf("%s is not mixed (%#x between %#x and %#x)", mid.Type().Field(i).Name, m, l.Field(i).Uint(), d.Field(i).Uint())
		}
	}
}

func TestResolve(t *testing.T) {
	th := testTheme()
	ts := &fakeText{}
	r := th.Resolve(ts, 2, Dark)

	if r.Px.ControlH != 56 || r.Px.RadiusLarge != 16 || r.Px.TitlebarH != 80 {
		t.Errorf("metrics are not scaled to pixels: %+v", r.Px)
	}
	if r.Px.BorderW != 1 {
		t.Errorf("BorderW = %v at 2x of 0.5 pt, want the 1 px hairline", r.Px.BorderW)
	}
	if r.Dark() != true || r.Pal != th.Dark {
		t.Errorf("resolved in Dark: Dark() = %v, palette %+v", r.Dark(), r.Pal)
	}
	for _, d := range ts.descs {
		if d.Scale != 2 {
			t.Errorf("font %+v was created without the window's scale", d)
		}
	}
	if r.Font.UI == r.Font.UISemi || r.Font.Mono == r.Font.MonoBold {
		t.Errorf("distinct descriptions share a font key: %+v", r.Font)
	}

	// Line boxes: a 14 pt font at 2x is 28 px, ascent 22.4, descent 5.6.
	m := r.Met(r.Font.UI)
	if m.H != 28 || m.Off != 22 {
		t.Errorf("UI line box = %+v, want H 28 Off 22", m)
	}
	if got := r.Met(text.FontKey(999)); got != (FontMet{}) {
		t.Errorf("a foreign font has a line box: %+v", got)
	}

	// Resolving at 1x makes a hairline of at least one pixel out of 0.5 pt.
	if r1 := th.Resolve(ts, 1, Light); r1.Px.BorderW != 1 || r1.Px.FocusRingW != 2 || r1.Pal != th.Light {
		t.Errorf("1x light: %+v", r1.Px)
	}
}

func TestMixFollowsAndDoesNotAllocate(t *testing.T) {
	th := testTheme()
	r := th.Resolve(&fakeText{}, 1, Light)
	r.Mix(0.25)
	want := MixPalettes(&th.Light, &th.Dark, 0.25)
	if r.Pal != want || r.T != 0.25 {
		t.Errorf("Mix(0.25): T %v, palette %+v", r.T, r.Pal)
	}
	r.Mix(7) // clamped
	if r.T != 1 || r.Pal != th.Dark {
		t.Errorf("Mix(7) did not clamp to dark: T %v", r.T)
	}
	tt := float32(0)
	if n := testing.AllocsPerRun(100, func() { tt += 0.01; r.Mix(tt) }); n != 0 {
		t.Errorf("Mix allocates %v times", n)
	}
}

func TestAccent(t *testing.T) {
	th := testTheme()
	th.Light.Accent, th.Dark.Accent = 0x1E66F5FF, 0x7AA2FFFF
	th.Light.FocusRing, th.Light.Selection = 0x1E66F5E6, 0x1E66F52B

	// A theme that ignores the system accent keeps its own.
	r := th.Resolve(&fakeText{}, 1, Light)
	r.SetAccent(0xFF8800FF)
	if r.Pal != th.Light {
		t.Error("a theme without SystemAccent took the accent")
	}

	th.SystemAccent = true
	r = th.Resolve(&fakeText{}, 1, Light)
	r.SetAccent(0xFF8800FF)
	if r.Pal.Accent != 0xFF8800FF {
		t.Errorf("Accent = %#x", r.Pal.Accent)
	}
	if r.Pal.FocusRing != 0xFF8800E6 || r.Pal.Selection != 0xFF88002B {
		t.Errorf("focus ring %#x and selection %#x did not take the hue and keep their alpha", r.Pal.FocusRing, r.Pal.Selection)
	}
	r.Mix(1) // the dark palette has the accent too
	if r.Pal.Accent != 0xFF8800FF {
		t.Errorf("dark Accent = %#x", r.Pal.Accent)
	}
	for _, accent := range []uint32{0xFFD60AFF, 0x0A2A66FF, 0x34C759FF, 0xFF3B30FF, 0x8E8E93FF} {
		r.SetAccent(accent)
		if c := Contrast(r.Pal.Accent, r.Pal.OnAccent); c < 3 {
			t.Errorf("accent %#x: OnAccent %#x has contrast %.1f", accent, r.Pal.OnAccent, c)
		}
	}
	r.SetAccent(0)
	if r.Pal != th.Dark {
		t.Error("SetAccent(0) did not restore the theme's own accent")
	}
}

func TestColor(t *testing.T) {
	if c := Contrast(0x000000FF, 0xFFFFFFFF); c < 20.99 || c > 21.01 {
		t.Errorf("black on white contrast = %v, want 21", c)
	}
	if c := Contrast(0x777777FF, 0x777777FF); c != 1 {
		t.Errorf("same colour contrast = %v", c)
	}
	if got := Over(0x00000080, 0xFFFFFFFF); got != 0x7F7F7FFF && got != 0x808080FF {
		t.Errorf("50%% black over white = %#x", got)
	}
	if got := Over(0x12345600, 0xAABBCCFF); got != 0xAABBCCFF {
		t.Errorf("transparent over bg = %#x", got)
	}
	if got := ColA(0xFFFFFF, 2); got != 0xFFFFFFFF {
		t.Errorf("ColA clamps alpha: %#x", got)
	}
	if got := MixRGBA(0x00000000, 0xFFFFFFFF, 0); got != 0x00000000 {
		t.Errorf("MixRGBA at 0 = %#x", got)
	}
}
