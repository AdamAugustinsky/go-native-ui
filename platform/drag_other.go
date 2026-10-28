//go:build !darwin && !windows

package platform

import "runtime"

// Drag and drop is native on macOS and Windows so far (drag.go has the
// contract): elsewhere the window takes no drops, starts no drags, and queues
// none of the drag events. The follow-ups are XDND on X11 and the data-device
// protocol on Wayland.

// NativeDrag reports whether the OS has native drag and drop: false here.
const NativeDrag = false

// DragThreshold is how far the pointer moves with the button held before a
// press becomes a drag, in logical units: the system's default where the
// program does not read the setting (SM_CXDRAG on Windows, gtk-dnd-drag-threshold
// on Linux).
func DragThreshold() float32 {
	if runtime.GOOS == "windows" {
		return dragThresholdWindows
	}
	return dragThresholdLinux
}

// SetDragHandler does nothing here: the window takes no drags.
func (w *Window) SetDragHandler(DragHandler) {}

// SetDropKinds does nothing here: the window takes no drags.
func (w *Window) SetDropKinds(DragKinds) {}

// StartDrag fails with ErrDragUnsupported here.
func (w *Window) StartDrag(*DragSpec) (DragTicket, error) { return 0, ErrDragUnsupported }
