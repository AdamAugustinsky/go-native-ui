package notes

import (
	"testing"

	"gonativeui/rexui"
	"gonativeui/theme"
)

// The open note scrolled under the toolbar: its content lies below the band,
// so the band over it is AppKit's titlebar pocket over the note's colour,
// flat (#FFFFFF, #262626 over #1E1E1E), on every renderer, in light and dark;
// not the blur of the window's white clear colour, which nothing covers there
// (#4C4C4C in dark).
func TestToolbarBandOverNothingIsFlat(t *testing.T) {
	for _, dark := range []bool{false, true} {
		r := newRigWith(t, dark, func(n *Notes) { n.Scrolled = true })
		r.Frames(4)
		if !r.d.under {
			t.Fatal("the note is not scrolled under the toolbar")
		}
		k := r.d.k
		th := k.Theme()
		b := rexui.ScrollEdgeLook.Hard.Backdrop(k.Kit)
		want := b.Over(th.Colors[theme.TextBackground])
		img, _ := r.Offscreen().Frame()
		sbW := int(theme.Snap(sidebarW * th.Scale))
		top := 0 // the band is below the drawn menu bar, if there is one
		if !r.Context().NativeMenus() && !r.d.Traffic && r.d.model != nil {
			top = int(theme.Snap(th.Px.ControlH))
		}
		for _, y := range []int{top + 1, top + 3} {
			for x := sbW + 2; x < img.Rect.Dx()-2; x += 3 {
				o := img.PixOffset(x, y)
				got := [3]uint8{img.Pix[o], img.Pix[o+1], img.Pix[o+2]}
				if got != [3]uint8{uint8(want >> 24), uint8(want >> 16), uint8(want >> 8)} {
					t.Fatalf("dark %v: the band at (%d, %d) is %v, want %08x", dark, x, y, got, want)
				}
			}
		}
	}
}
