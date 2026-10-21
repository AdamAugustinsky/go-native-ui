//go:build darwin

package platform

import (
	"testing"
	"time"

	"gonativeui/internal/objc"
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
// retitles it, and back again out of it. A window that cannot go full screen
// here (a locked screen, an inactive test process) skips the test.
func TestFullScreenItemTitleFollowsTheWindow(t *testing.T) {
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
		rig, w := openMenuRig(spec, false)
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
		fullScreen := func() bool { return objc.Send(win.win, selStyleMask)&(1<<14) != 0 }
		before = title()
		win.DebugToggleFullScreen()
		entered = pumpUntil(win, 5*time.Second, fullScreen)
		if !entered {
			return
		}
		pumpFor(win, 1500*time.Millisecond)
		inside = title()
		win.DebugToggleFullScreen()
		left = pumpUntil(win, 8*time.Second, func() bool { return !fullScreen() })
		pumpFor(win, 1500*time.Millisecond)
		win.Activate() // an app that is not active has no key window to ask
		pumpUntil(win, 2*time.Second, func() bool { return objc.SendBool(win.win, objc.Sel("isKeyWindow")) })
		after = title()
	})
	if why != "" {
		t.Skipf("cannot open a window here: %s", why)
	}
	if !entered || !left {
		t.Skipf("the window did not go full screen and back here (entered %v, left %v)", entered, left)
	}
	if before != "Enter Full Screen" || inside != "Exit Full Screen" || after != "Enter Full Screen" {
		t.Errorf("titles %q, in full screen %q, out of it %q", before, inside, after)
	}
}
