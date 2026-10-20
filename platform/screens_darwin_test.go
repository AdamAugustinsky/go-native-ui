//go:build darwin

package platform

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"gonativeui/internal/objc"
)

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

// Screens must work in a program that has not opened a window, so has not loaded
// AppKit: the class is looked up only after the framework is loaded. The check
// needs a process of its own, because a test binary that already used AppKit
// would hide the bug; the child asks for the class before and after.
func TestScreensBeforeAnyWindowDarwin(t *testing.T) {
	if os.Getenv("GNUI_SCREENS_FRESH") == "" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestScreensBeforeAnyWindowDarwin$", "-test.v")
		cmd.Env = append(os.Environ(), "GNUI_SCREENS_FRESH=1")
		out, err := cmd.CombinedOutput()
		if err != nil || strings.Contains(string(out), "--- FAIL") {
			t.Fatalf("fresh process: %v\n%s", err, out)
		}
		return
	}
	var before, after, again objc.ID
	var n1, n2 int
	onMain(func() {
		before = objc.Cls("NSScreen")
		n1 = len(Screens())
		after = objc.Cls("NSScreen")
		n2 = len(Screens())
		again = objc.Cls("NSScreen")
	})
	if before != 0 {
		t.Fatal("AppKit was already loaded when the test began: it cannot show the bug")
	}
	if after == 0 || again == 0 {
		t.Fatal("Screens did not load AppKit: NSScreen is still unknown after it")
	}
	if n1 != n2 {
		t.Errorf("two calls disagree: %d and %d screens", n1, n2)
	}
}
