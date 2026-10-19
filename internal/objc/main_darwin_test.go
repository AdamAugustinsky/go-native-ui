//go:build darwin

package objc

import (
	"os"
	"runtime"
	"testing"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
)

// AppKit sheets need the main thread and its run loop. The test binary's
// main goroutine is locked to the main thread here; tests run on another
// goroutine and hand main-thread work to onMain.

var mainQ = make(chan func())

func init() { runtime.LockOSThread() }

func TestMain(m *testing.M) {
	done := make(chan int, 1)
	go func() { done <- m.Run() }()
	for {
		select {
		case f := <-mainQ:
			f()
		case code := <-done:
			os.Exit(code)
		}
	}
}

// onMain runs f on the main thread and waits for it. f must not call
// t.Skip or t.Fatal.
func onMain(f func()) {
	d := make(chan struct{})
	mainQ <- func() { defer close(d); f() }
	<-d
}

// libdispatch, for callbacks from GCD threads.
var (
	fnAsyncF, fnGlobalQueue, fnGroupCreate, fnGroupAsyncF, fnGroupWait, fnDispatchRelease, fnDispatchTime uintptr
	fnRunLoopRunInMode, cfDefaultMode                                                                     uintptr
)

func init() {
	h, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW)
	if err != nil {
		panic(err)
	}
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW)
	if err != nil {
		panic(err)
	}
	sym := func(h uintptr, n string) uintptr {
		p, err := purego.Dlsym(h, n)
		if err != nil {
			panic(err)
		}
		return p
	}
	fnAsyncF, fnGlobalQueue = sym(h, "dispatch_async_f"), sym(h, "dispatch_get_global_queue")
	fnGroupCreate, fnGroupAsyncF, fnGroupWait = sym(h, "dispatch_group_create"), sym(h, "dispatch_group_async_f"), sym(h, "dispatch_group_wait")
	fnDispatchRelease, fnDispatchTime = sym(h, "dispatch_release"), sym(h, "dispatch_time")
	fnRunLoopRunInMode = sym(cf, "CFRunLoopRunInMode")
	cfDefaultMode = *(*uintptr)(ffi.PtrFromC(sym(cf, "kCFRunLoopDefaultMode"))) // the CFStringRef the symbol holds
}

var qosClasses = [...]uintptr{0x21, 0x19, 0x15, 0x11, 0x09}

func globalQueue(f *ffi.Frame, i int) uintptr {
	return f.Call2(fnGlobalQueue, qosClasses[i%len(qosClasses)], 0)
}

// runLoop runs the current thread's run loop in the default mode for up to
// seconds, returning early once done reports true.
func runLoop(seconds float64, done func() bool) {
	for t := 0.0; t < seconds && !done(); t += 0.02 {
		F.Reset().Arg(cfDefaultMode).ArgF64(0.02).Arg(0).Call(fnRunLoopRunInMode)
	}
}
