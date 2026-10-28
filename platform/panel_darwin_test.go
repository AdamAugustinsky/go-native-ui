//go:build darwin

package platform

import (
	"runtime"
	"testing"
	"time"

	"gonativeui/internal/objc"
	"gonativeui/internal/testlock"
)

// pumpTurns runs the pump for about n × 20 ms (main thread).
func pumpTurns(n int) {
	for i := 0; i < n; i++ {
		Pump(20e6)
	}
}

// pumpPanelUntil runs the pump until ok or about 3 s (main thread).
func pumpPanelUntil(ok func() bool) bool {
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); {
		if ok() {
			return true
		}
		Pump(20e6)
	}
	return ok()
}

// Each Panel field reads back from AppKit as D69 says: a borderless,
// non-activating NSPanel, its level, collection behaviour, sharing type,
// hidesOnDeactivate and shadow; transparent; out of the Window menu; never
// the main window, and key only when it takes the keyboard.
func TestPanelReadBack(t *testing.T) {
	cases := []struct {
		name string
		p    Panel
		b    Backdrop
		want PanelState
	}{
		{"zero", Panel{}, BackdropNone, PanelState{Level: levelNormal, SharingType: 1,
			CollectionBehavior: cbMoveToActiveSpace | cbFullScreenAuxiliary | cbIgnoresCycle, CanBecomeKey: true}},
		{"palette", Panel{Floating: true, HideOnDeactivate: true, Shadow: true, Radius: 26}, BackdropGlass, PanelState{Level: levelFloating, SharingType: 1,
			CollectionBehavior: cbMoveToActiveSpace | cbFullScreenAuxiliary | cbIgnoresCycle, HidesOnDeactivate: true, HasShadow: true, CanBecomeKey: true}},
		{"recording bar", Panel{StatusBar: true, NonActivating: true, AllSpaces: true, ExcludeFromCapture: true, Shadow: true}, BackdropNone, PanelState{Level: levelStatus, SharingType: 0,
			CollectionBehavior: cbCanJoinAllSpaces | cbStationary | cbFullScreenAuxiliary | cbIgnoresCycle, HasShadow: true}},
	}
	var got [3]PanelState
	var plain PanelState
	var err error
	onMain(func() {
		for i, c := range cases {
			p := c.p
			var w *Window
			if w, err = Open(Options{Title: c.name, Width: 400, Height: 120, Hidden: true, Panel: &p, Backdrop: c.b}); err != nil {
				return
			}
			got[i] = w.DebugPanelState()
			if c.b != BackdropNone && w.glassRadius != float64(c.p.Radius) {
				t.Errorf("%s: the backdrop's radius is %v, want %v", c.name, w.glassRadius, c.p.Radius)
			}
			if !w.IsPanel() || !w.Native().Transparent {
				t.Errorf("%s: IsPanel %v, Transparent %v", c.name, w.IsPanel(), w.Native().Transparent)
			}
			w.Close()
		}
		var w *Window
		if w, err = Open(Options{Title: "plain", Width: 300, Height: 200, Hidden: true}); err != nil {
			return
		}
		plain = w.DebugPanelState()
		w.Close()
		Pump(0)
	})
	if err != nil {
		t.Skipf("cannot open a window here: %v", err)
	}
	for i, c := range cases {
		g := got[i]
		if g.StyleMask != panelStyle {
			t.Errorf("%s: styleMask %#x, want borderless | nonactivatingPanel (%#x)", c.name, g.StyleMask, panelStyle)
		}
		if !g.IsNSPanel || g.Opaque || !g.ExcludedFromMenu || g.CanBecomeMain {
			t.Errorf("%s: NSPanel %v, opaque %v, excluded from the Window menu %v, can become main %v", c.name, g.IsNSPanel, g.Opaque, g.ExcludedFromMenu, g.CanBecomeMain)
		}
		w := c.want
		if g.Level != w.Level || g.CollectionBehavior != w.CollectionBehavior || g.SharingType != w.SharingType ||
			g.HidesOnDeactivate != w.HidesOnDeactivate || g.HasShadow != w.HasShadow || g.CanBecomeKey != w.CanBecomeKey {
			t.Errorf("%s: got %+v\nwant level %d, collection %#x, sharing %d, hidesOnDeactivate %v, shadow %v, can become key %v",
				c.name, g, w.Level, w.CollectionBehavior, w.SharingType, w.HidesOnDeactivate, w.HasShadow, w.CanBecomeKey)
		}
	}
	if plain.IsNSPanel || plain.StyleMask&1 == 0 || plain.Level != 0 || plain.SharingType != 1 {
		t.Errorf("an ordinary window changed: %+v", plain)
	}
}

// A non-activating panel shown while another window is key leaves that
// window key and does not become key itself, even when makeKeyWindow, the
// way a click would make it key, is tried. The key window is an ordinary
// window where the app can become active; where it cannot (an unbundled
// test binary started from a shell), a palette panel, which becomes key
// without the app being active, as Spotlight's does.
func TestNonActivatingPanelLeavesTheKeyWindowKey(t *testing.T) {
	testlock.Hold(t, "activation")
	var why, keyKind string
	var keyBefore, keyAfter, panelKey, panelVisible bool
	onMain(func() {
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		w, err := Open(Options{Title: "key", Width: 300, Height: 200})
		if err != nil {
			why = err.Error()
			return
		}
		defer w.Close()
		objc.Send1(w.app, selActivateIgnoring, 1)
		objc.Send1(w.win, selMakeKeyFront, 0)
		keyKind = "window"
		if !pumpPanelUntil(w.KeyWindow) {
			pal, err := Open(Options{Title: "palette", Width: 400, Height: 200, Hidden: true, Panel: &Panel{Floating: true}})
			if err != nil {
				why = err.Error()
				return
			}
			defer pal.Close()
			pal.Show()
			w, keyKind = pal, "palette"
		}
		keyBefore = pumpPanelUntil(w.KeyWindow)
		p, err := Open(Options{Title: "bar", Width: 300, Height: 40, Hidden: true, Panel: &Panel{NonActivating: true, StatusBar: true}})
		if err != nil {
			why = err.Error()
			return
		}
		defer p.Close()
		p.Show()
		pumpTurns(5)
		objc.Send(p.win, objc.Sel("makeKeyWindow"))
		pumpTurns(5)
		keyAfter, panelKey, panelVisible = w.KeyWindow(), p.KeyWindow(), p.Visible()
	})
	if why != "" {
		t.Skipf("cannot open a window here: %s", why)
	}
	if !keyBefore {
		t.Skipf("no window could become key here (the %s)", keyKind)
	}
	t.Logf("the key window: the %s", keyKind)
	if !panelVisible || panelKey || !keyAfter {
		t.Fatalf("after the panel showed: panel visible %v, panel key %v, the %s still key %v", panelVisible, panelKey, keyKind, keyAfter)
	}
}

// A palette (a panel that takes the keyboard) becomes key when shown, and
// HideOnDeactivate hides it when the app deactivates; Show brings it back.
func TestPanelHidesOnDeactivate(t *testing.T) {
	testlock.Hold(t, "activation")
	var why string
	var key, active, hidden, stillActive, back bool
	onMain(func() {
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		p, err := Open(Options{Title: "palette", Width: 500, Height: 300, Hidden: true, Panel: &Panel{Floating: true, HideOnDeactivate: true}})
		if err != nil {
			why = err.Error()
			return
		}
		defer p.Close()
		p.Show()
		active = pumpPanelUntil(func() bool { return objc.SendBool(p.app, selIsActiveApp) })
		key = pumpPanelUntil(p.KeyWindow)
		if !active {
			return
		}
		objc.Send(p.app, objc.Sel("deactivate"))
		hidden = pumpPanelUntil(func() bool { return !p.Visible() })
		stillActive = objc.SendBool(p.app, selIsActiveApp)
		p.Show()
		back = pumpPanelUntil(func() bool { return p.Visible() && p.KeyWindow() })
	})
	if why != "" {
		t.Skipf("cannot open a window here: %s", why)
	}
	if !active {
		t.Skip("the app cannot become active here")
	}
	if !hidden && stillActive {
		// deactivate gives the activation to another app; where none takes
		// it (a CI runner's session) the app stays active and AppKit has no
		// deactivation to hide the panel on
		t.Skipf("the app is still active 3 s after deactivate (key when shown %v, shown and key again %v)", key, back)
	}
	if !key || !hidden || !back {
		t.Fatalf("palette: key when shown %v, hidden on deactivation %v (the app active %v), shown and key again %v", key, hidden, stillActive, back)
	}
}

// 100 shows and hides of a panel (one with glass, one without) leak
// nothing: no window, view, delegate or shell object, and no Go allocation.
func TestPanelShowHideLeaksNothing(t *testing.T) {
	var why string
	var before, after WindowObjects
	var shellBefore, shellAfter ShellCounts
	var mallocs uint64
	onMain(func() {
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		var ps [2]*Window
		for i, b := range [2]Backdrop{BackdropNone, BackdropGlass} {
			p, err := Open(Options{Title: "cycle", Width: 300, Height: 60, Hidden: true, Backdrop: b, Panel: &Panel{NonActivating: true, Floating: true, Radius: 12}})
			if err != nil {
				why = err.Error()
				return
			}
			ps[i] = p
			defer p.Close()
		}
		cycle := func() {
			for _, p := range ps {
				p.Show()
			}
			Pump(0)
			for _, p := range ps {
				p.Hide()
			}
			Pump(0)
		}
		cycle() // warm
		before, shellBefore = LiveWindowObjects(), ShellObjects()
		var m0, m1 runtime.MemStats
		runtime.ReadMemStats(&m0)
		for i := 0; i < 100; i++ {
			cycle()
		}
		runtime.ReadMemStats(&m1)
		mallocs = m1.Mallocs - m0.Mallocs
		after, shellAfter = LiveWindowObjects(), ShellObjects()
	})
	if why != "" {
		t.Skipf("cannot open a window here: %s", why)
	}
	if after != before || shellAfter.Hosts != shellBefore.Hosts || shellAfter.Materials != shellBefore.Materials {
		t.Errorf("100 show/hide cycles: windows %+v → %+v, shell %+v → %+v", before, after, shellBefore, shellAfter)
	}
	if mallocs > 0 {
		t.Errorf("100 show/hide cycles allocated %d Go objects", mallocs)
	}
}
