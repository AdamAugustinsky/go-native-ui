//go:build darwin

package main

import "gonativeui/internal/objc"

// probe asks AppKit about the app's windows (how many, how many visible),
// whether the app is active, and its activation policy.
func probe() (windows, visible int, active bool, policy int) {
	nsApp := objc.Send(objc.Cls("NSApplication"), objc.Sel("sharedApplication"))
	wins := objc.Send(nsApp, objc.Sel("windows"))
	windows = int(objc.Send(wins, objc.Sel("count")))
	for i := 0; i < windows; i++ {
		w := objc.Send1(wins, objc.Sel("objectAtIndex:"), uintptr(i))
		if objc.SendBool(w, objc.Sel("isVisible")) {
			visible++
		}
	}
	return windows, visible, objc.SendBool(nsApp, objc.Sel("isActive")), int(objc.Send(nsApp, objc.Sel("activationPolicy")))
}
