//go:build linux

package platform

import (
	"unsafe"

	"gonativeui/internal/wl"
)

// Wayland pinch (zwp_pointer_gestures_v1, the touchpad gestures libinput
// recognizes): a two-finger pinch and rotation become EvMagnify and EvRotate
// at the pointer, through pinchTracker. Three-finger swipes and holds are the
// compositor's and not bound. Without the global (or a pointer) there are no
// gesture events; X11 has no gestures at all.

type wlGest struct {
	mgr, pinch uintptr
	mgrV       uint32
	pt         pinchTracker
}

// wlGestureBind binds the manager the registry announced.
func (w *Window) wlGestureBind(name, adv uint32) {
	g := &w.wy.gest
	g.mgrV = minU32(adv, 3, uint32(wl.ZwpPointerGesturesV1.Version))
	g.mgr = w.bind(name, wl.ZwpPointerGesturesV1.Addr(), wl.ZwpPointerGesturesV1.NameAddr(), g.mgrV)
	w.wlGestureAttach()
}

// wlGestureAttach asks for the pinch object once there are both the manager
// and a pointer (they arrive in either order).
func (w *Window) wlGestureAttach() {
	y := &w.wy
	g := &y.gest
	if g.mgr == 0 || y.pointer == 0 || g.pinch != 0 {
		return
	}
	// zwp_pointer_gestures_v1.get_pinch_gesture(id, pointer) (1)
	y.args[0], y.args[1] = 0, uint64(y.pointer)
	g.pinch = w.req(g.mgr, 1, wl.ZwpPointerGesturePinchV1.Addr(), g.mgrV, 0)
	listen(g.pinch)
}

// wlGestureDetach drops the pinch object (the pointer went away).
func (w *Window) wlGestureDetach() {
	g := &w.wy.gest
	w.release(g.pinch, 0, 1) // zwp_pointer_gesture_pinch_v1.destroy
	g.pinch = 0
	g.pt = pinchTracker{}
}

func (w *Window) wlGestureClose() {
	g := &w.wy.gest
	w.wlGestureDetach()
	w.release(g.mgr, 2, 2) // zwp_pointer_gestures_v1.release (v2)
	g.mgr = 0
}

func (w *Window) onPinch(op uint32, a unsafe.Pointer) {
	g := &w.wy.gest
	now := nanotime()
	switch op {
	case 0: // begin(serial, time, surface, fingers)
		w.wy.serial = argU(a, 0)
		g.pt.begin()
	case 1: // update(time, dx, dy, scale, rotation)
		g.pt.update(&w.q, argFixed(a, 3), argFixed(a, 4), w.ptrX, w.ptrY, w.mods, now)
	case 2: // end(serial, time, cancelled)
		w.wy.serial = argU(a, 0)
		g.pt.end(&w.q, argU(a, 2) != 0, w.ptrX, w.ptrY, w.mods, now)
	}
}
