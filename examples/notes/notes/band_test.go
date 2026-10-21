package notes

import (
	"testing"

	"gonativeui/rexui"
	"gonativeui/theme"
)

// The note runs under the toolbar's band (ToolbarOpts.ContentEdge). At rest
// nothing is under the band, so it shows the note's colour, flat (#FFFFFF,
// #1E1E1E), on every renderer, in light and dark. Scrolled, the band is
// AppKit's titlebar pocket over the note: the blur of the text under it (D4c),
// whose flat part is the recipe's colour (#FFFFFF, #262626 over #1E1E1E) and
// whose text is soft, never the crisp copy a pocket-less band would show, and
// not the blur of the window's white clear colour (#4C4C4C in dark).
func TestToolbarBandOverTheNote(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, scrolled := range []bool{false, true} {
			r := newRigWith(t, dark, func(n *Notes) { n.Scrolled = scrolled })
			r.Frames(4)
			if r.d.under != scrolled {
				t.Fatalf("dark %v: the note is scrolled under the toolbar: %v, want %v", dark, r.d.under, scrolled)
			}
			k := r.d.k
			th := k.Theme()
			bg := th.Colors[theme.TextBackground]
			want, tol := bg, 0
			if scrolled {
				b := rexui.ScrollEdgeLook.Hard.Backdrop(k.Kit)
				want, tol = b.Over(bg), 0x20
			}
			img, _ := r.Offscreen().Frame()
			sbW := int(theme.Snap(sidebarW * th.Scale))
			top := 0 // the band is below the drawn menu bar, if there is one
			if !r.Context().NativeMenus() && !r.d.Traffic && r.d.model != nil {
				top = int(theme.Snap(th.Px.ControlH))
			}
			for _, y := range []int{top + 1, top + 3} {
				for x := sbW + 2; x < img.Rect.Dx()-2; x += 3 {
					o := img.PixOffset(x, y)
					for c, w := range [3]int{int(want >> 24), int(want >> 16 & 0xFF), int(want >> 8 & 0xFF)} {
						if d := int(img.Pix[o+c]) - w; d > tol || d < -tol {
							t.Fatalf("dark %v scrolled %v: the band at (%d, %d) is %v, want %08x within %d", dark, scrolled, x, y, img.Pix[o:o+3], want, tol)
						}
					}
				}
			}
		}
	}
}
