//go:build darwin && gnui_release

package platform

// DebugAssumeKey is compiled out of gnui_release builds: it does nothing,
// and the focus is always AppKit's.
func DebugAssumeKey(w *Window) (restore func()) { return func() {} }

func keyAssumed(*Window) bool { return false }

func forgetAssumedKey(*Window) {}
