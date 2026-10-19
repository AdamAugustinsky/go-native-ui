//go:build windows

package platform

import "testing"

func TestScreensWindows(t *testing.T) {
	got := Screens()
	if len(got) == 0 {
		t.Fatal("no monitors")
	}
	for i, s := range got {
		t.Logf("screen %d: %+v", i, s)
		if s.W <= 0 || s.H <= 0 || s.VisibleW <= 0 || s.VisibleH <= 0 || s.Scale <= 0 || s.ID == "" {
			t.Errorf("screen %d: %+v", i, s)
		}
	}
	if p := got[0]; p.X != 0 || p.Y != 0 {
		t.Errorf("the primary monitor should be at the origin: %+v", p)
	}
}
