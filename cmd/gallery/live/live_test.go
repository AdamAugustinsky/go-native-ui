package live

import (
	"runtime"
	"testing"

	"gonativeui/cmd/gallery/gallery"
	"gonativeui/widget/widgettest"
)

// The real window: the system draws its close, minimise and zoom buttons over
// the content, in the titlebar band the gallery draws, and nothing the user
// can see or operate may be under them. Offscreen frames never saw the system's
// buttons, which is how the gallery's menu bar once started at the window's
// edge, under them. The test opens the window, lets the gallery draw, asks
// AppKit where the buttons are, and checks the boxes of the last frame.
func TestLiveWindowKeepsContentOutFromUnderTheSystemButtons(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the window buttons the system draws over a custom titlebar are macOS's")
	}
	g, err := gallery.New(gallery.Options{Rows: 1000})
	if err != nil {
		t.Fatal(err)
	}
	l := widgettest.RunLive(t, gallery.Config(), g, 12)
	t.Logf("live: %d frames at scale %v; the system's window buttons at %v pt", l.Frames, l.Scale, l.Buttons)
	l.CheckClearOfSystemButtons(t)
}
