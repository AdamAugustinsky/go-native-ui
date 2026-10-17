//go:build !darwin

package platform

// DebugState is only filled in on macOS; elsewhere it is the zero value
// (Valid false).
func (w *Window) DebugState() DebugState { return DebugState{Now: Now()} }
