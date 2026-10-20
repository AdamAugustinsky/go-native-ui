//go:build darwin

package platform

import "gonativeui/internal/objc"

// testInstall puts a hand-made window of a test rig in the registry, as Open
// does for a real one: its GNUIView (when it is one) finds it, and so do the
// app-wide lookups. The returned function takes it out again. Unlike
// register, it touches no ivar of an object that is not of our classes (rigs
// stand a view in for the NSWindow).
func testInstall(w *Window) (remove func()) {
	i := 0
	for reg.slots[i] != nil {
		i++
	}
	reg.slots[i], w.slot, w.registered = w, i, true
	reg.open[reg.n] = w
	reg.n++
	isView := w.view != 0 && viewClass != 0 && objc.SendBool1(w.view, selIsKindOfClass, uintptr(viewClass))
	if isView {
		viewDef.Index().Set(w.view, uintptr(i+1))
	}
	return func() {
		if isView {
			viewDef.Index().Set(w.view, 0)
		}
		w.registered = false
		reg.slots[i] = nil
		for k := 0; k < reg.n; k++ {
			if reg.open[k] == w {
				copy(reg.open[k:reg.n], reg.open[k+1:reg.n])
				reg.n--
				reg.open[reg.n] = nil
				break
			}
		}
		if reg.lastKey == w {
			reg.lastKey = nil
		}
		forgetWindow(w)
	}
}
