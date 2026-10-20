//go:build darwin && !gnui_release

package platform

import "sync/atomic"

// assumedKey is the window DebugAssumeKey opted in, nil for none.
var assumedKey atomic.Pointer[Window]

// DebugAssumeKey makes window w count as the key window whatever AppKit says,
// until restore is called: Focused reports true for it, and its losing the
// key window sends no EvFocus. For tests only (it is compiled out of
// gnui_release builds): a live test that posts input to the window of a
// process the system did not activate (a test binary started from a shell),
// or that runs beside other test processes that take the activation. A test
// of the focus itself must not use it: it makes its window really key. Only w
// is affected, and only while opted in; restore (any goroutine, once, for
// example from t.Cleanup) ends it if w is still the one, and does not tell
// the app. Main thread; call it before the window's first frame (an Initer)
// for that frame to be drawn key.
func DebugAssumeKey(w *Window) (restore func()) {
	prev := assumedKey.Swap(w)
	return func() { assumedKey.CompareAndSwap(w, prev) }
}

// forgetAssumedKey ends w's opt-in as it closes: nothing stays set.
func forgetAssumedKey(w *Window) { assumedKey.CompareAndSwap(w, nil) }

// keyAssumed reports whether w is opted in to DebugAssumeKey.
func keyAssumed(w *Window) bool { return w != nil && assumedKey.Load() == w }
