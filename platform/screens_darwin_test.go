//go:build darwin

package platform

import "testing"

func TestScreensDarwin(t *testing.T) {
	var got []ScreenInfo
	onMain(func() { got = Screens() })
	if len(got) == 0 {
		t.Skip("no displays in this session")
	}
	checkScreens(t, got)
}

// checkScreens verifies what any platform's answer must satisfy.
func checkScreens(t *testing.T, got []ScreenInfo) {
	t.Helper()
	ids := map[string]bool{}
	for i, s := range got {
		t.Logf("screen %d: %+v", i, s)
		if s.W <= 0 || s.H <= 0 || s.VisibleW <= 0 || s.VisibleH <= 0 || s.Scale <= 0 {
			t.Errorf("screen %d has an empty size or scale: %+v", i, s)
		}
		if s.VisibleX < s.X || s.VisibleY < s.Y || s.VisibleX+s.VisibleW > s.X+s.W+0.5 || s.VisibleY+s.VisibleH > s.Y+s.H+0.5 {
			t.Errorf("screen %d: the visible area is not inside the frame: %+v", i, s)
		}
		if s.ID != "" && ids[s.ID] {
			t.Errorf("screen %d: duplicate ID %q", i, s.ID)
		}
		ids[s.ID] = true
	}
	if p := got[0]; p.X != 0 || p.Y != 0 {
		t.Errorf("the primary screen should be at the origin: %+v", p)
	}
}
