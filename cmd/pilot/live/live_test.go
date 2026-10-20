package live

import (
	"runtime"
	"testing"

	"gonativeui/cmd/pilot/pilot"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// The real window: the system draws its window buttons over the sidebar's glass
// and the toolbar's band, and the toolbar's items and the sidebar's rows must
// start after and below them. The sidebar's own box runs under them to the
// window's top (it is the glass), so that box is allowed there; anything else
// that draws or takes input is not.
func TestLiveWindowKeepsContentOutFromUnderTheSystemButtons(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the window buttons the system draws over a custom titlebar are macOS's")
	}
	b := pilot.NewSynthBench(2000)
	defer b.Close()
	l := widgettest.RunLive(t, pilot.Config(), b, 12)
	t.Logf("live: %d frames at scale %v; the system's window buttons at %v pt", l.Frames, l.Scale, l.PlatformButtons)
	pb := l.PlatformButtons
	if pb[2] <= pb[0] || pb[3] <= pb[1] {
		t.Fatalf("the platform reports no window buttons (%v): the window has no custom titlebar", pb)
	}
	r := [4]float32{pb[0] * l.Scale, pb[1] * l.Scale, pb[2] * l.Scale, pb[3] * l.Scale}
	for _, bx := range l.Context.ContentIn(r, nil) {
		if bx.Role == ui.RoleList && !bx.Text && !bx.Image {
			continue // the sidebar's glass, whose rows start below the toolbar
		}
		t.Errorf("a box (role %d, text %v, image %v, key %#x) at %v px is under the window buttons at %v px", bx.Role, bx.Text, bx.Image, uint64(bx.Key), bx.Rect, r)
	}
}
