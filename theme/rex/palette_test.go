package rex

import (
	"fmt"
	"testing"

	"gonativeui/theme"
)

// Legibility: the text roles hold their contrast on every surface the look
// puts them on, in both modes. The surfaces are the palette's own and the
// samples of the background field and of the unfocused glass at a grid of
// positions over a 1057 × 639 pt window.

const refW = 2114 // the reference window's width, in reference pixels

type backdrop struct {
	name string
	rgba uint32
}

// backdrops returns the colours a text role can sit on in the mode: the
// palette's surfaces, and the field and the unfocused glass sampled over the
// window. The samples that give the lowest contrast with dark text (the
// lightest) or light text (the darkest) are among them.
func backdrops(p *theme.Palette, m *model) []backdrop {
	out := []backdrop{{"Background", p.Background}, {"Surface", p.Surface}, {"SurfaceRaised", p.SurfaceRaised}}
	for y := float32(0); y <= 1278; y += 71 {
		for x := float32(0); x <= refW; x += 106 {
			out = append(out,
				backdrop{fmt.Sprintf("field(%v,%v)", x, y), m.bgAt(x, refW-x, y).rgba(255)},
				backdrop{fmt.Sprintf("glass(%v,%v)", x, y), m.fillAt(refW, x, y)})
		}
	}
	return out
}

func TestTextContrast(t *testing.T) {
	for _, mode := range []struct {
		name string
		p    *theme.Palette
		m    *model
	}{{"light", &light, &lightModel}, {"dark", &dark, &darkModel}} {
		t.Run(mode.name, func(t *testing.T) {
			p := mode.p
			worstText, worstMuted := 99.0, 99.0
			for _, b := range backdrops(p, mode.m) {
				ct, cm := theme.Contrast(p.Text, b.rgba), theme.Contrast(p.TextMuted, b.rgba)
				worstText, worstMuted = min(worstText, ct), min(worstMuted, cm)
				if ct < 7 {
					t.Errorf("Text on %s %#x: %.2f:1, want >= 7", b.name, b.rgba, ct)
				}
				if cm < 4.5 {
					t.Errorf("TextMuted on %s %#x: %.2f:1, want >= 4.5", b.name, b.rgba, cm)
				}
			}
			t.Logf("worst contrast over the backdrops: Text %.2f:1, TextMuted %.2f:1", worstText, worstMuted)

			// Text on the states of a control: hovered and pressed, filled, selected.
			for _, c := range []struct {
				name   string
				fg, bg uint32
				min    float64
			}{
				{"Text on hovered Surface", p.Text, theme.Over(p.HoverOverlay, p.Surface), 7},
				{"Text on pressed Surface", p.Text, theme.Over(p.PressOverlay, p.Surface), 7},
				{"Text on ControlFill", p.Text, theme.Over(p.ControlFill, p.Surface), 7},
				{"TextMuted on ControlFill", p.TextMuted, theme.Over(p.ControlFill, p.Surface), 4.5},
				{"SelectionText on Selection", p.SelectionText, theme.Over(p.Selection, p.Surface), 7},
				{"SelectionText on Selection over Background", p.SelectionText, theme.Over(p.Selection, p.Background), 7},
				{"OnAccent on Accent", p.OnAccent, p.Accent, 4.5},
				{"Accent text on Surface", p.Accent, p.Surface, 4.5},
				{"Accent text on SurfaceRaised", p.Accent, p.SurfaceRaised, 4.5},
			} {
				if got := theme.Contrast(c.fg, c.bg); got < c.min {
					t.Errorf("%s: %.2f:1, want >= %v", c.name, got, c.min)
				} else {
					t.Logf("%s: %.2f:1", c.name, got)
				}
			}
			// The focus ring must show against the surface it rings.
			if got := theme.Contrast(theme.Over(p.FocusRing, p.Surface), p.Surface); got < 3 {
				t.Errorf("FocusRing on Surface: %.2f:1, want >= 3", got)
			}
		})
	}
}

func TestTerminalContrast(t *testing.T) {
	// The terminal content sits on the focused surface or, dimmed, on the
	// unfocused glass. The dark pair is held to 4.5:1 for every colour (7:1
	// for the foreground of a focused pane). The light pair is sampled from the
	// reference and kept as it is: its foreground is held to the same, the
	// colours that fall short are reported.
	for _, c := range []struct {
		name string
		p    *Terminal
		bg   []backdrop
		fg   float64 // least contrast of the foreground
		all  bool    // every colour is held to 4.5:1
	}{
		{"light focused", &termLightFocused, []backdrop{{"Surface", light.Surface}}, 7, false},
		{"light dimmed", &termLightDimmed, glassSamples(&lightModel), 4.5, false},
		{"dark focused", &termDarkFocused, []backdrop{{"Surface", dark.Surface}}, 7, true},
		{"dark dimmed", &termDarkDimmed, glassSamples(&darkModel), 4.5, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			for _, col := range []struct {
				name string
				v    uint32
			}{
				{"FG", c.p.FG}, {"Dim", c.p.Dim}, {"Blue", c.p.Blue}, {"Teal", c.p.Teal}, {"Red", c.p.Red}, {"Green", c.p.Green},
				{"Bullet", c.p.Bullet}, {"Op", c.p.Op}, {"Purple", c.p.Purple}, {"Orange", c.p.Orange}, {"Line", c.p.Line},
			} {
				worst := 99.0
				for _, b := range c.bg {
					worst = min(worst, theme.Contrast(theme.Col(col.v), b.rgba))
				}
				want := 4.5
				if col.name == "FG" {
					want = c.fg
				}
				switch {
				case worst >= want:
				case c.all || col.name == "FG":
					t.Errorf("%s: %.2f:1, want >= %v", col.name, worst, want)
				default:
					t.Logf("%s %#06x: %.2f:1 is below %v (reference colour, kept)", col.name, col.v, worst, want)
				}
			}
		})
	}
}

// glassSamples samples the unfocused glass over the window.
func glassSamples(m *model) []backdrop {
	var out []backdrop
	for y := float32(0); y <= 1278; y += 71 {
		for x := float32(0); x <= refW; x += 106 {
			out = append(out, backdrop{fmt.Sprintf("glass(%v,%v)", x, y), m.fillAt(refW, x, y)})
		}
	}
	return out
}
