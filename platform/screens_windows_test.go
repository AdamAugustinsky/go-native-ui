//go:build windows

package platform

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

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

// Screens, called before any window exists, makes the process DPI aware first, so
// that monitor sizes and scales are real and not the virtualised ones an unaware
// process gets. A process of its own tests it, since awareness is set once.
func TestScreensMakeTheProcessDPIAware(t *testing.T) {
	if os.Getenv("GNUI_SCREENS_FRESH") == "" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestScreensMakeTheProcessDPIAware$", "-test.v")
		cmd.Env = append(os.Environ(), "GNUI_SCREENS_FRESH=1")
		out, err := cmd.CombinedOutput()
		if err != nil || strings.Contains(string(out), "--- FAIL") {
			t.Fatalf("fresh process: %v\n%s", err, out)
		}
		return
	}
	isAware := func() bool {
		r, _, _ := user32d.NewProc("IsProcessDPIAware").Call()
		return uint32(r) != 0
	}
	if isAware() {
		t.Fatal("the process was already DPI aware when the test began: it cannot show the bug")
	}
	if len(Screens()) == 0 {
		t.Fatal("no monitors")
	}
	if !isAware() {
		t.Error("Screens left the process DPI unaware: sizes and scales are virtualised")
	}
}

var user32d = syscall.NewLazyDLL("user32.dll")
