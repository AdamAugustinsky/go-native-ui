package live

import (
	"runtime"
	"testing"

	"gonativeui/cmd/pilot/pilot"
	"gonativeui/widget/widgettest"
)

// The real window, as the gallery's live test has it: the system draws its
// window buttons over Pilot's titlebar band, and the navigation buttons and
// the title the band holds must start after them.
func TestLiveWindowKeepsContentOutFromUnderTheSystemButtons(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the window buttons the system draws over a custom titlebar are macOS's")
	}
	b := pilot.NewSynthBench(2000)
	defer b.Close()
	l := widgettest.RunLive(t, pilot.Config(), b, 12)
	t.Logf("live: %d frames at scale %v; the system's window buttons at %v pt", l.Frames, l.Scale, l.Buttons)
	l.CheckClearOfSystemButtons(t)
}
