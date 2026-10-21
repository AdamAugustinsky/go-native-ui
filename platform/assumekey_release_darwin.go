//go:build darwin && gnui_release

package platform

import "gonativeui/internal/objc"

// DebugAssumeKey is compiled out of gnui_release builds: it does nothing,
// and the focus is always AppKit's.
func DebugAssumeKey(w *Window) (restore func()) { return func() {} }

func keyAssumed(*Window) bool { return false }

func forgetAssumedKey(*Window) {}

func registerAssumedEdit(*objc.ClassDef) {}

func assumedEditValid(objc.ID) (valid, ok bool) { return false, false }
