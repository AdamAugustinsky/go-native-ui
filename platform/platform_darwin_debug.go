//go:build darwin

package platform

import "gonativeui/internal/objc"

// SimulateLiveResize is a test hook (macOS only): it runs the same code
// path as a user dragging a window edge, without a mouse. It enters live
// resize mode, sets each content size in turn (in logical units; AppKit
// delivers windowDidResize: synchronously, which calls the frame handler)
// and leaves live resize mode. Call it from the main thread between Wait
// calls, with a frame handler registered.
func (w *Window) SimulateLiveResize(sizes [][2]float64) {
	w.enterLiveResize()
	for _, s := range sizes {
		objc.F.Reset().Arg(uintptr(w.win)).Arg(uintptr(selSetContentSize)).ArgPoint(s[0], s[1]).Call(objc.MsgSend)
	}
	w.exitLiveResize()
}
