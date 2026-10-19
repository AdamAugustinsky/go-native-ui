package rex

import (
	"testing"

	"gonativeui/internal/fakeui"
	"gonativeui/theme"
)

// Under Reduce Transparency every material Rex draws is opaque: its glass
// (GlassTint, about 58 % in light and 50 % in dark) gives way to the pane.
func TestReduceTransparencyMakesTheGlassOpaque(t *testing.T) {
	r := Theme().Resolve(fakeui.New(), 2, theme.Light)
	for _, m := range []theme.Mode{theme.Light, theme.Dark} {
		r.SetSystem(&theme.SystemValues{})
		r.Mix(m.T())
		if g := r.Material(theme.MatGlass); r.Colors[g.Fill]&0xFF == 0xFF {
			t.Fatalf("mode %d: the glass is opaque without Reduce Transparency (%08X)", m, r.Colors[g.Fill])
		}
		r.SetSystem(&theme.SystemValues{Known: true, Settings: theme.Settings{ReduceTransparency: true}})
		r.Mix(m.T())
		for mat := range theme.NumMaterials {
			s := r.Material(mat)
			if s.Native {
				t.Errorf("mode %d material %d: live under Reduce Transparency", m, mat)
			}
			if a, b := r.Colors[s.Fill], r.Colors[s.FillInactive]; a&0xFF != 0xFF || b&0xFF != 0xFF {
				t.Errorf("mode %d material %d: fills %08X / %08X are translucent under Reduce Transparency", m, mat, a, b)
			}
		}
		if g := r.Material(theme.MatGlass); g.Fill != theme.Pane {
			t.Errorf("mode %d: the glass's opaque fallback is %v, want Pane", m, g.Fill)
		}
	}
}

// A layer's shadow is the Shadow role's colour at the layer's own opacity:
// a focused pane's tight layer is 7 % in light and 40 % in dark, its ambient
// one 5 % and 30 %, as the chrome draws them (chrome.go, SurfaceShadow).
func TestPaneShadowOpacityIsTheChromes(t *testing.T) {
	r := Theme().Resolve(fakeui.New(), 2, theme.Light)
	for _, c := range []struct {
		t              float32
		rgb            uint32
		tight, ambient float32
	}{{0, light.Shadow >> 8, 0.07, 0.05}, {1, dark.Shadow >> 8, 0.40, 0.30}} {
		r.Mix(c.t)
		sh := r.Elevations[theme.ElevPane].Shadows
		for i, want := range []uint32{theme.ColA(c.rgb, c.tight), theme.ColA(c.rgb, c.ambient)} {
			if got := sh[i].Color(r.Colors[theme.Shadow], r.T); got != want {
				t.Errorf("mix %v layer %d: %08X, the chrome draws %08X", c.t, i, got, want)
			}
		}
	}
}
