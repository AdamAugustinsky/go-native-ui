package rexui_test

import (
	"fmt"
	"testing"

	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
)

// The lone glyph of an avatar ("#" of a blank name, an initial) and the
// digit of a count badge are centred in their disc or capsule: the layout
// audit finds nothing off-centre at 1× and 2×, at every size. (The text box
// is as wide as the shape, with the text centred in it; it used to be the
// text's width plus a pixel of slack, centred, which left the glyph up to
// a pixel to the left.)
func TestAvatarAndBadgeGlyphIsCentred(t *testing.T) {
	type thing struct {
		name  string
		build func(g *rig)
	}
	var things []thing
	for _, nm := range []string{"", "E", "Q", "Ada"} {
		for _, sz := range []float32{28, 36, 64, 96} {
			things = append(things, thing{fmt.Sprintf("avatar %q %v pt", nm, sz), func(g *rig) {
				rexui.Avatar(g.k, nm, rexui.AvatarOpts{Size: sz})
			}})
		}
	}
	for _, n := range []int{1, 9} {
		things = append(things, thing{fmt.Sprintf("badge %d", n), func(g *rig) {
			rexui.BadgeCountN(g.k, n, rexui.BadgeOpts{})
		}})
	}
	for _, scale := range []float32{1, 2} {
		for _, th := range things {
			g := openRig(t, theme.Light, nil, column(th.build))
			g.scale = scale
			g.frames(3)
			for _, f := range g.c.Audit(nil) {
				if f.Check == ui.AuditCentre {
					t.Errorf("%s at %v×: %s", th.name, scale, f)
				}
			}
		}
	}
}
