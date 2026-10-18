package rex

import (
	"math"
	"testing"

	"gonativeui/text"
	"gonativeui/theme"
)

func TestGrid(t *testing.T) {
	ts, err := text.New()
	if err != nil {
		skipOrFail(t, "text system", err)
	}
	defer ts.Close()
	for _, scale := range []float32{1, 1.5, 2} {
		g := NewGrid(ts, scale)
		// The cell is 7.5 pt wide (within what the font's size resolution
		// allows) on a 16 pt row, in a face where every glyph has one width.
		if want := 7.5 * scale; math.Abs(float64(g.Adv-want)) > 0.04*float64(want) {
			t.Errorf("%vx: cell %v px (%s, %v pt), want about %v", scale, g.Adv, g.Face, g.SizePt, want)
		}
		if g.RowPitch != 16*scale {
			t.Errorf("%vx: row pitch %v, want %v", scale, g.RowPitch, 16*scale)
		}
		if g.Mono == g.MonoBold {
			t.Errorf("%vx: regular and bold are one font", scale)
		}
		i, m := ts.Run(ts.Shape(g.Mono, []byte("i"))).Advance, ts.Run(ts.Shape(g.Mono, []byte("m"))).Advance
		if math.Abs(float64(i-m)) > 0.01*float64(m) {
			t.Errorf("%vx: %s is not monospaced: i %v, m %v", scale, g.Face, i, m)
		}
		if g.Met.H <= 0 || g.Met.Off <= 0 || g.Met.Off >= g.Met.H {
			t.Errorf("%vx: line box %+v", scale, g.Met)
		}
		if math.Abs(g.AdvPt()-7.5) > 0.3 || g.RowPitchPt() != 16 {
			t.Errorf("%vx: grid in points %v × %v", scale, g.AdvPt(), g.RowPitchPt())
		}
	}
}

func TestTerminalAt(t *testing.T) {
	for _, c := range []struct {
		mix, focus float32
		want       Terminal
	}{
		{0, 1, termLightFocused}, {0, 0, termLightDimmed}, {1, 1, termDarkFocused}, {1, 0, termDarkDimmed},
	} {
		if got := TerminalAt(c.mix, c.focus); got != c.want {
			t.Errorf("TerminalAt(%v, %v) = %+v, want %+v", c.mix, c.focus, got, c.want)
		}
	}
	// In between, every colour is between its ends.
	mid := TerminalAt(0.5, 1)
	for name, v := range map[string][3]uint32{
		"FG": {termLightFocused.FG, mid.FG, termDarkFocused.FG}, "Blue": {termLightFocused.Blue, mid.Blue, termDarkFocused.Blue},
		"Red": {termLightFocused.Red, mid.Red, termDarkFocused.Red},
	} {
		lo, hi := theme.Luminance(v[0]<<8), theme.Luminance(v[2]<<8)
		if lo > hi {
			lo, hi = hi, lo
		}
		if l := theme.Luminance(v[1] << 8); l < lo || l > hi {
			t.Errorf("%s at mix 0.5 (%#06x) is not between the light and dark colours", name, v[1])
		}
	}
	if n := testing.AllocsPerRun(100, func() { _ = TerminalAt(0.4, 0.7) }); n != 0 {
		t.Errorf("TerminalAt allocates %v times", n)
	}
}
