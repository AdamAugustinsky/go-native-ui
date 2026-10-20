package main

import (
	"gonativeui/app"
	"gonativeui/internal/objc"
)

var selIsKeyWindow objc.SEL

// activate makes the app active and its window key, as a click would: the
// text view's caret blinks only in the key window (-idle measures it).
func activate(a *app.App) {
	objc.Send1(objc.Send(objc.Cls("NSApplication"), objc.Sel("sharedApplication")), objc.Sel("activateIgnoringOtherApps:"), 1)
	objc.Send1(objc.ID(a.Win.Native().NSWindow), objc.Sel("makeKeyAndOrderFront:"), 0)
}

// keyWindow reports whether the window is the key window (every frame: the
// selector is registered once).
func keyWindow(a *app.App) bool {
	if selIsKeyWindow == 0 {
		selIsKeyWindow = objc.Sel("isKeyWindow")
	}
	return objc.SendBool(objc.ID(a.Win.Native().NSWindow), selIsKeyWindow)
}
