//go:build linux

package platform

func (w *Window) wlDndKinds()       {}
func (w *Window) wlDndStart() error { return ErrDragUnsupported }
