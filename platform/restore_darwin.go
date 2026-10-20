//go:build darwin

package platform

import "gonativeui/internal/objc"

// skipStateRestoration tells AppKit to restore no windows for a process that
// has no bundle identifier (a test binary, `go run`, anything not packaged as
// an app). AppKit's launch-time restoration (-[NSApplication
// _reopenWindowsAsNecessaryIncludingRestorableState:...], run by
// finishLaunching) has no saved-state directory to look in for such a
// process, and the main thread then sits in the event wait for about five
// seconds before it serves anything: no call posted to it, no event, no first
// frame, with the window already on screen. Measured on macOS 27: 5.1-6.1 s
// from the launch of every unbundled executable; none from the same
// executable once it had a bundle identifier (an embedded Info.plist) or with
// -ApplePersistenceIgnoreState YES.
//
// Nothing here is restorable (windows are not restorable, geometry is the
// app's own: app/winstate), so skipping restoration loses nothing. A bundled
// app is left as it is. Registered, not set: nothing is written to the app's
// preferences. Main thread, before finishLaunching.
func skipStateRestoration() {
	if objc.Send(objc.Send(objc.Cls("NSBundle"), objc.Sel("mainBundle")), objc.Sel("bundleIdentifier")) != 0 {
		return
	}
	yes := objc.Send1(objc.Cls("NSNumber"), objc.Sel("numberWithBool:"), 1)
	defaults := objc.Send2(objc.Cls("NSDictionary"), objc.Sel("dictionaryWithObject:forKey:"), uintptr(yes), uintptr(objc.Str("ApplePersistenceIgnoreState")))
	objc.Send1(objc.Send(objc.Cls("NSUserDefaults"), objc.Sel("standardUserDefaults")), objc.Sel("registerDefaults:"), uintptr(defaults))
}
