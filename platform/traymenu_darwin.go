//go:build darwin

package platform

import "gonativeui/internal/objc"

// Services that show native menus of their own (app/tray's status items)
// build them here, so that they are the same menus as the menu bar's and the
// Dock's, and deliver the commands their own way.

// BuildCommandMenu builds menu idx of spec as a standalone NSMenu, retained
// (release it with objc.Release): its command items (those with an ID and no
// standard action) send action to target, an Objective-C object the caller
// owns, instead of to the window; the other items are as in any native menu.
// The spec is read during the call only. Main thread, after StartApp; it
// returns 0 when the app object is not started or idx is out of range.
func BuildCommandMenu(spec *MenuSpec, idx int, target, action uintptr) uintptr {
	if spec == nil || idx < 0 || idx >= len(spec.Menus) || !appObj.started {
		return 0
	}
	ensureMenuTarget()
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	m := buildMenu(spec, idx, 0, nil)
	retargetMenu(m, objc.ID(target), objc.SEL(action), 0)
	return uintptr(m)
}

// DeliverAppCommand delivers menu command id to the app (EvCommand), as the
// Dock menu's commands are. Main thread.
func DeliverAppCommand(id uint16) { appCommand(uintptr(id)) }

// AppStarted reports whether the application object exists (StartApp ran).
func AppStarted() bool { return appObj.started }
