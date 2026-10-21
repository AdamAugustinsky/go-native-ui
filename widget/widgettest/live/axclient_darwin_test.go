//go:build darwin

package live

import (
	"os"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"gonativeui/app"
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
	"gonativeui/widget/widgettest"
)

// In-process AXUIElement calls can dispatch synchronously: use the main thread,
// from a native timer, not Do (which posts a framework wake after each call).
func TestAccessibilityClientIdle(t *testing.T) {
	wantOverlay(t, true)
	fl := &frameLog{Builder: &probe{}}
	l := widgettest.StartLive(t, probeConfig(), fl)
	defer l.Close()
	h, err := purego.Dlopen("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		t.Fatal(err)
	}
	defer purego.Dlclose(h)
	sym := func(name string) uintptr {
		p, err := purego.Dlsym(h, name)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	create, copyAttr, release := sym("AXUIElementCreateApplication"), sym("AXUIElementCopyAttributeValue"), sym("CFRelease")
	count, at, equal := sym("CFArrayGetCount"), sym("CFArrayGetValueAtIndex"), sym("CFEqual")
	var kids, windows, role, title, button, press objc.ID
	l.Do(func(*app.App) {
		kids, role, title = objc.Retain(objc.Str("AXChildren")), objc.Retain(objc.Str("AXRole")), objc.Retain(objc.Str("AXDescription"))
		windows = objc.Retain(objc.Str("AXWindows"))
		button, press = objc.Retain(objc.Str("AXButton")), objc.Retain(objc.Str("Press"))
	})
	f := new(ffi.Frame)
	ax := f.Call1(create, uintptr(os.Getpid()))
	defer func() {
		for _, o := range []uintptr{ax, uintptr(kids), uintptr(windows), uintptr(role), uintptr(title), uintptr(button), uintptr(press)} {
			f.Call1(release, o)
		}
	}()
	copy := func(o uintptr, attr objc.ID) uintptr {
		var value uintptr
		code := int32(f.Reset().Arg(o).Arg(uintptr(attr)).ArgPtr(unsafe.Pointer(&value)).Call(copyAttr).R0())
		if code != 0 && code != -25205 && code != -25212 {
			t.Errorf("AX read: %d", code)
		}
		return value
	}
	var walk func(uintptr, int) bool
	walk = func(o uintptr, depth int) bool {
		if depth == 0 {
			return false
		}
		r, label := copy(o, role), copy(o, title)
		found := r != 0 && label != 0 && f.Call2(equal, r, uintptr(button)) != 0 && f.Call2(equal, label, uintptr(press)) != 0
		for _, v := range []uintptr{r, label} {
			if v != 0 {
				f.Call1(release, v)
			}
		}
		if found {
			return true
		}
		attr := kids
		if o == ax {
			attr = windows
		}
		if children := copy(o, attr); children != 0 {
			defer f.Call1(release, children)
			for i, n := uintptr(0), f.Call1(count, children); i < n; i++ {
				if walk(f.Call2(at, children, i), depth-1) {
					return true
				}
			}
		}
		return found
	}
	var enabled, missing atomic.Bool
	var walks atomic.Int64
	var timer objc.ID
	l.Do(func(*app.App) {
		timer = axIdleTimer(func() {
			if enabled.Load() {
				if !walk(ax, 8) {
					missing.Store(true)
				}
				walks.Add(1)
			}
		})
	})
	defer l.Do(func(*app.App) {
		objc.Send(timer, objc.Sel("invalidate"))
		objc.Release(timer)
		axIdleTick = nil
	})
	l.Frames(3)
	settled := false
	for i := 0; i < 12 && !settled; i++ {
		n := fl.count()
		time.Sleep(250 * time.Millisecond)
		settled = fl.count() == n
	}
	if !settled {
		t.Fatal("the AX client app did not settle")
	}
	before := fl.count()
	enabled.Store(true)
	time.Sleep(500 * time.Millisecond)
	enabled.Store(false)
	frames := fl.finish(l)
	if walks.Load() == 0 || missing.Load() {
		t.Errorf("AX client: %d walks, missing Press button: %v", walks.Load(), missing.Load())
	}
	report(t, "unexplained AX client frames", attribute(frames, before, 0), 0)
	t.Logf("AXUIElement: %d tree walks, %d frames", walks.Load(), len(frames)-before)
}

// Main-thread only, shared across repeated runs; the timer is invalidated
// before clearing its callback so the block retains no finished test.
var axIdleBlock objc.ID
var axIdleTick func()

func axIdleTimer(tick func()) objc.ID {
	axIdleTick = tick
	if axIdleBlock == 0 {
		axIdleBlock = objc.NewGlobalBlock("v16@?0@8", func(*ffi.CB) {
			if axIdleTick != nil {
				axIdleTick()
			}
		})
	}
	cls, sel := objc.Cls("NSTimer"), objc.Sel("scheduledTimerWithTimeInterval:repeats:block:")
	return objc.Retain(objc.ID(objc.F.Reset().Arg(uintptr(cls)).Arg(uintptr(sel)).
		ArgF64(0.02).Arg(1).Arg(uintptr(axIdleBlock)).Call(objc.MsgSend).R0()))
}
