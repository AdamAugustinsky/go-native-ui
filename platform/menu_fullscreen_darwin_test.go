//go:build darwin

package platform

import (
	"fmt"
	"testing"
	"time"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
	"gonativeui/internal/testlock"
)

// The full screen item is AppKit's toggleFullScreen:, sent through
// GNUIMenuTarget (which asks the app first), with ⌃⌘F and the title AppKit
// retitles in full screen. (AppKit shows the shortcut as fn F on this macOS
// and keeps ⌃⌘F working: the key mask is either.)
func TestFullScreenItem(t *testing.T) {
	const tcFull = 41
	spec := &MenuSpec{
		Menus: []Menu{
			{Title: "App", Role: MenuRoleApp, Items: []MenuItem{{Action: MenuQuit}}},
			{Title: "View", Items: []MenuItem{{Action: MenuFullScreen, ID: tcFull}}},
		},
		Bar: []int{0, 1},
	}
	var r struct {
		why, title, action, key string
		mask                    uintptr
		ours, enabled           bool
		valid                   bool
	}
	onMain(func() {
		rig, why := openMenuRig(spec, true)
		if why != "" {
			r.why = why
			return
		}
		defer rig.w.Close()
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		item := itemAt(objc.Send(itemAt(objc.Send(rig.w.app, objc.Sel("mainMenu")), 1), objc.Sel("submenu")), 0)
		r.title = titleOf(item)
		r.action = objc.SelName(objc.SEL(objc.Send(item, selAction)))
		r.key = str(objc.Send(item, objc.Sel("keyEquivalent")))
		r.mask = uintptr(objc.Send(item, objc.Sel("keyEquivalentModifierMask")))
		r.ours = objc.Send(item, objc.Sel("target")) == nativeMenus.target
		rig.off[tcFull] = true
		r.enabled = objc.SendBool1(nativeMenus.target, selValidateItem, uintptr(item))
	})
	if r.why != "" {
		t.Skipf("cannot open a window here: %s", r.why)
	}
	if r.title != "Enter Full Screen" || r.action != "toggleFullScreen:" || r.key != "f" || (r.mask != 1<<20|1<<18 && r.mask != 1<<23) || !r.ours {
		t.Errorf("full screen item: title %q, action %q, key %q, mask %#x, our target %v", r.title, r.action, r.key, r.mask, r.ours)
	}
	if r.enabled {
		t.Error("a full screen command the app disabled is enabled")
	}
}

// In full screen the item reads "Exit Full Screen": the window's own
// validation (systemValid, which the target runs for a standard action)
// retitles it, and back again out of it. A style-mask change starts a
// transition; the did-notification and a ready key window finish it.
func TestFullScreenItemTitleFollowsTheWindow(t *testing.T) {
	testlock.Hold(t, "activation")
	spec := &MenuSpec{
		Menus: []Menu{
			{Title: "App", Role: MenuRoleApp, Items: []MenuItem{{Action: MenuQuit}}},
			{Title: "View", Items: []MenuItem{{Action: MenuFullScreen}}},
		},
		Bar: []int{0, 1},
	}
	var why string
	var entered, left bool
	var before, inside, after string
	onMain(func() {
		rig, w := openMenuRig(spec, true)
		if w != "" {
			why = w
			return
		}
		defer rig.w.Close()
		win := rig.w
		item := func() objc.ID {
			return itemAt(objc.Send(itemAt(objc.Send(win.app, objc.Sel("mainMenu")), 1), objc.Sel("submenu")), 0)
		}
		title := func() string {
			objc.SendBool1(nativeMenus.target, selValidateItem, uintptr(item()))
			return titleOf(item())
		}
		var didEnter, didExit bool
		d := objc.Define(fmt.Sprintf("GNUITestMenuFullScreen%d", Now()), objc.Cls("NSObject"), nil)
		in, out := objc.Sel("didEnter:"), objc.Sel("didExit:")
		d.Method(in, "v@:@", func(*ffi.CB) { didEnter = true })
		d.Method(out, "v@:@", func(*ffi.CB) { didExit = true })
		activate := objc.Sel("activate:")
		d.Method(activate, "v@:@", func(*ffi.CB) {
			objc.Send1(win.app, objc.Sel("activateIgnoringOtherApps:"), 1)
			win.Activate()
		})
		observer := objc.Send(objc.Send(d.Register(), selAlloc), selInit)
		center := objc.Send(objc.Cls("NSNotificationCenter"), objc.Sel("defaultCenter"))
		add := objc.Sel("addObserver:selector:name:object:")
		for _, n := range []struct {
			sel  objc.SEL
			name string
		}{{in, "NSWindowDidEnterFullScreenNotification"}, {out, "NSWindowDidExitFullScreenNotification"}} {
			name := objc.Str(n.name)
			objc.Send4(center, add, uintptr(observer), uintptr(n.sel), uintptr(name), uintptr(win.win))
		}
		defer objc.Release(observer)
		defer objc.Send1(center, objc.Sel("removeObserver:"), uintptr(observer))
		ready := func() bool { s := win.DebugState(); return s.Visible && s.AppActive && s.Key }
		toggle := func() {
			if !ready() {
				t.Error("full screen requested before native readiness")
			}
			win.DebugToggleFullScreen()
		}
		// Regression: opening a window is not readiness. Let activation arrive
		// later from the native run loop, as it did on the CI runner.
		perform := objc.Sel("performSelector:withObject:afterDelay:")
		objc.F.Reset().Arg(uintptr(observer)).Arg(uintptr(perform)).Arg(uintptr(activate)).Arg(0).ArgF64(2).Call(objc.MsgSend)
		if !pumpUntil(win, 5*time.Second, ready) {
			t.Errorf("window did not become visible, active and key before full screen: %+v", win.DebugState())
			return
		}
		before = title()
		toggle()
		entered = pumpUntil(win, 5*time.Second, func() bool { return didEnter && ready() })
		if !entered {
			return
		}
		inside = title()
		toggle()
		left = pumpUntil(win, 8*time.Second, func() bool { return didExit && ready() })
		after = title()
	})
	if why != "" {
		t.Skipf("cannot open a window here: %s", why)
	}
	if !entered || !left {
		t.Fatalf("the window did not finish full screen and back with a ready key window (entered %v, left %v)", entered, left)
	}
	if before != "Enter Full Screen" {
		t.Errorf("title %q before full screen", before)
	}
	if inside != "Exit Full Screen" {
		t.Errorf("title %q in full screen, want %q", inside, "Exit Full Screen")
	}
	if after != "Enter Full Screen" {
		t.Errorf("title %q out of full screen, want %q", after, "Enter Full Screen")
	}
}
