package live

import (
	"runtime"
	"testing"

	"gonativeui/cmd/gallery/gallery"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// The real window: the system draws its close, minimise and zoom buttons over
// the content, over the sidebar's glass, and nothing the user can see or
// operate may be under them but that glass: the sidebar's rows and search
// field start below the toolbar's band, and the toolbar's items after the
// zoom button. Offscreen frames never saw the system's buttons, which is how
// the gallery's menu bar once started at the window's edge, under them. The
// test opens the window, lets the gallery draw, asks AppKit where the buttons
// are, and checks the boxes of the last frame.
func TestLiveWindowKeepsContentOutFromUnderTheSystemButtons(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the window buttons the system draws over a custom titlebar are macOS's")
	}
	g, err := gallery.New(gallery.Options{Rows: 1000})
	if err != nil {
		t.Fatal(err)
	}
	l := widgettest.RunLive(t, gallery.Config(), g, 12)
	t.Logf("live: %d frames at scale %v; the system's window buttons at %v pt (the platform says %v)", l.Frames, l.Scale, l.Buttons, l.PlatformButtons)
	// The window has materials, so its content view is a container that the
	// reading of the buttons does not follow: the platform's own report, which
	// the app lays out by, is the same rectangle there.
	buttons := l.Buttons
	if buttons == [4]float32{} {
		buttons = l.PlatformButtons
	}
	if buttons[2] <= buttons[0] || buttons[3] <= buttons[1] {
		t.Fatalf("no window buttons over the content (%v)", buttons)
	}
	r := [4]float32{buttons[0] * l.Scale, buttons[1] * l.Scale, buttons[2] * l.Scale, buttons[3] * l.Scale}
	for _, b := range l.Context.ContentIn(r, nil) {
		if b.Role == ui.RoleList && b.Rect[0] == 0 && b.Rect[1] == 0 {
			continue // the sidebar's pane: its glass runs up under the buttons, its rows start below the band
		}
		t.Errorf("a box (role %d, key %#x, text %v, image %v) at %v px is under the window buttons at %v px (%v pt, scale %v)",
			b.Role, uint64(b.Key), b.Text, b.Image, b.Rect, r, buttons, l.Scale)
	}
}
