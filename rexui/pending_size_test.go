package rexui_test

import (
	"testing"

	"gonativeui/icon"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
)

// An icon that is not ready (here one the atlas does not know, which builds
// an empty box as a still-decoding one does) takes the NextSize set for it
// with it: the label after it keeps its natural size.
func TestIconNotReadyConsumesNextSize(t *testing.T) {
	width := func(withIcon bool) float32 {
		var lbl ui.ID
		g := newRig(t, theme.Light, nil, column(func(g *rig) {
			if withIcon {
				g.k.NextSize(g.k.Fixed(10), g.k.Fixed(10))
				g.k.Icon(icon.ID(0xFFFF), 0)
			}
			lbl = rexui.Label(g.k, "Status: 4 selected, 12 items", rexui.LabelOpts{})
		}))
		g.settle()
		r := g.c.Rect(lbl)
		return r[2] - r[0]
	}
	if with, without := width(true), width(false); with != without || with < 40 {
		t.Fatalf("a label after an icon that is not ready is %v wide, %v without the icon", with, without)
	}
}
