package platform

import "testing"

// The pointer is somewhere on the screens, in their coordinates.
func TestPointerIsOnAScreen(t *testing.T) {
	var x, y float64
	var ok bool
	var scr []ScreenInfo
	onMain(func() {
		x, y, ok = PointerLocation()
		scr = Screens()
	})
	if len(scr) == 0 {
		t.Skip("no screens here")
	}
	if !ok {
		t.Fatal("no pointer location")
	}
	for _, s := range scr {
		if x >= s.X && x <= s.X+s.W && y >= s.Y && y <= s.Y+s.H {
			return
		}
	}
	t.Errorf("the pointer at %g,%g is on no screen: %+v", x, y, scr)
}
