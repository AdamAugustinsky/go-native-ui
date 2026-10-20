//go:build darwin

package widgettest

import (
	"sync"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// AppKit builds the NSEvent of a scroll or gesture CGEvent with the location
// the window server last saw the pointer at and, whatever window the CGEvent
// names, not the location it carries: a synthetic event reaches the window but
// the point it is "over" is the real pointer's. Moving the real pointer is
// not something a test may do, so the event is re-aimed in process: the
// instance's class is switched to a subclass of NSEvent that answers
// window, windowNumber and locationInWindow from a table, and everything else
// (type, phase, magnification, deltas, modifiers) from the event itself.

var aimed struct {
	once sync.Once
	cls  objc.ID
	set  uintptr // object_setClass
	mu   sync.Mutex
	m    map[objc.ID]aim
}

type aim struct {
	win  objc.ID
	num  uintptr
	x, y float64 // locationInWindow
}

func aimedClass() {
	aimed.once.Do(func() {
		h, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			return
		}
		aimed.set, _ = purego.Dlsym(h, "object_setClass")
		aimed.m = map[objc.ID]aim{}
		get := func(c *ffi.CB) aim {
			aimed.mu.Lock()
			defer aimed.mu.Unlock()
			return aimed.m[objc.ID(c.Int(0))]
		}
		d := objc.Define("GNUIAimedEvent", objc.Cls("NSEvent"), nil)
		d.Method(objc.Sel("window"), "@@:", func(c *ffi.CB) { c.Ret(uintptr(get(c).win)) })
		d.Method(objc.Sel("windowNumber"), "q@:", func(c *ffi.CB) { c.Ret(get(c).num) })
		d.Method(objc.Sel("locationInWindow"), "{CGPoint=dd}@:", func(c *ffi.CB) {
			a := get(c)
			c.RetF64(0, a.x)
			c.RetF64(1, a.y)
		})
		aimed.cls = d.Register()
	})
}

// aimEvent re-aims ev at (x, y) pixels of the window. Main thread.
func (w Win) aimEvent(ev objc.ID, x, y float32) bool {
	aimedClass()
	if aimed.cls == 0 || aimed.set == 0 || ev == 0 {
		return false
	}
	wx, wy := w.windowPoint(x, y)
	num := uintptr(w.Number())
	aimed.mu.Lock()
	if len(aimed.m) > 4096 { // events are autoreleased: the old ones are long gone
		clear(aimed.m)
	}
	aimed.m[ev] = aim{w.Window, num, wx, wy}
	aimed.mu.Unlock()
	objc.F.Call2(aimed.set, uintptr(ev), uintptr(aimed.cls))
	return true
}
