package app

import (
	"os"
	"os/exec"
	"runtime"
	"testing"

	"gonativeui/menu"
	"gonativeui/platform"
	"gonativeui/ui"
)

func TestTitlebarCapabilityIsBoundOnce(t *testing.T) {
	if os.Getenv("GNUI_TITLEBAR_ALLOC_CHILD") == "" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestTitlebarCapabilityIsBoundOnce$")
		// Serialize from process startup: T.Run's parent can otherwise still
		// allocate its channel waiter and scheduler threads during our loop.
		// Keep the exact counters; do not warm or average the measured loop.
		cmd.Env = append(os.Environ(), "GNUI_TITLEBAR_ALLOC_CHILD=1", "DBUS_SESSION_BUS_ADDRESS=disabled:", "GOMAXPROCS=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return
	}
	if runtime.GOOS == "linux" { // settle the init-time portal before counting
		platform.SystemAppearance()
	}
	a := &App{cfg: Config{Titlebar: 46}, zoom: 1.5}
	plain, capable := &fakeWindow{}, &titlebarWindow{}
	a.syncTitlebar(plain) // a non-macOS window at non-default zoom
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range 50000 {
		a.syncTitlebar(plain)
	}
	runtime.ReadMemStats(&after)
	if n := after.Mallocs - before.Mallocs; n != 0 {
		t.Fatalf("%d allocations while syncing an unsupported titlebar", n)
	}
	a.syncTitlebar(capable)
	a.zoom = 2
	a.syncTitlebar(plain)
	if capable.setCall != 1 {
		t.Fatal("the previous window received the new window's titlebar")
	}
	a.syncTitlebar(capable)
	if capable.setCall != 2 || capable.height != 92 {
		t.Fatal("the rebound window did not receive its titlebar")
	}
}

// The zoom commands become native View items that deliver their ids, with the
// standard titles and key equivalents (⌘+ ⌘− ⌘0 on macOS); the alias has no
// item of its own, Match takes it.
func TestNativeZoomItems(t *testing.T) {
	cases := []struct {
		role  menu.Role
		title string
		key   rune
	}{
		{menu.RoleZoomIn, "Zoom In", '+'},
		{menu.RoleZoomOut, "Zoom Out", '-'},
		{menu.RoleActualSize, "Actual Size", '0'},
	}
	for i, c := range cases {
		cmd := menu.StandardCommand(menu.ID(i+1), c.role)
		it := commandItem(&cmd)
		if it.ID != uint16(i+1) || it.Title != c.title || it.Action != platform.MenuNone {
			t.Errorf("%s: item %+v", c.title, it)
		}
		if appMenuRole(c.role) {
			t.Errorf("%s would move to the application menu", c.title)
		}
		if ui.PrimaryMod() == ui.ModSuper && (it.Key != c.key || it.Mods != platform.ModSuper) {
			t.Errorf("%s: key equivalent %q %v, want ⌘%q", c.title, it.Key, it.Mods, c.key)
		}
	}
}

type titlebarWindow struct {
	fakeMacWindow
	height  float32
	lights  [2]float32
	setCall int
}

// SetTitlebar moves the buttons as AppKit does: centred on the lights, at the
// system's size (60×14).
func (f *titlebarWindow) SetTitlebar(h float32, l [2]float32) {
	f.height, f.lights, f.setCall = h, l, f.setCall+1
	f.buttons = [4]float32{l[0] - 7, l[1] - 7, l[0] + 53, l[1] + 7}
}

// With a custom titlebar the zoom is the whole window's: the band is zoomed
// and the buttons are put at its centre before the frame reads where they are.
func TestZoomGivesTheWindowTheZoomedTitlebar(t *testing.T) {
	a := &App{appObject: &appObject{}, UI: ui.New(nil), cfg: Config{Titlebar: 46, TrafficLights: [2]float32{21, 23}}, scale: 2}
	w := &titlebarWindow{fakeMacWindow: fakeMacWindow{buttons: [4]float32{14, 16, 74, 30}}}
	a.syncWindowFrom(w, w)
	if w.setCall != 0 {
		t.Fatal("the titlebar was set at 100 %, as opened")
	}

	a.SetZoom(1.5)
	a.syncWindowFrom(w, w)
	if w.setCall != 1 || w.height != 69 || w.lights != [2]float32{21, 34.5} {
		t.Fatalf("titlebar %v with lights %v (%d calls), want 69 and (21, 34.5)", w.height, w.lights, w.setCall)
	}
	if c := (w.buttons[1] + w.buttons[3]) / 2; c != w.height/2 {
		t.Errorf("the buttons are centred at %v, the band at %v", c, w.height/2)
	}
	// The ui got the buttons where they are now, in pixels of the display (not of the zoom).
	if got, want := a.UI.SystemButtons(), [4]float32{w.buttons[0] * 2, w.buttons[1] * 2, w.buttons[2] * 2, w.buttons[3] * 2}; got != want {
		t.Errorf("the ui has the buttons at %v, want %v", got, want)
	}
	for i := 0; i < 5; i++ {
		a.syncWindowFrom(w, w)
	}
	if w.setCall != 1 {
		t.Errorf("%d calls over steady frames", w.setCall)
	}
	if n := testing.AllocsPerRun(100, func() { a.syncWindowFrom(w, w) }); n != 0 {
		t.Errorf("%v allocations per steady frame", n)
	}

	a.ZoomReset()
	a.syncWindowFrom(w, w)
	if w.setCall != 2 || w.height != 46 || w.lights != [2]float32{21, 23} {
		t.Errorf("after the reset: titlebar %v, lights %v", w.height, w.lights)
	}

	// No custom titlebar: nothing to set.
	b := &App{appObject: &appObject{}, UI: ui.New(nil), cfg: Config{}, scale: 2}
	w2 := &titlebarWindow{}
	b.SetZoom(2)
	b.syncWindowFrom(w2, w2)
	if w2.setCall != 0 {
		t.Error("a window with the system's titlebar was given metrics")
	}
}
