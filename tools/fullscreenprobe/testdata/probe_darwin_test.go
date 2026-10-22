//go:build darwin

package app

import (
	"fmt"
	"os"
	"sync/atomic"
	"testing"

	"github.com/ebitengine/purego"
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
	"gonativeui/internal/testlock"
	"gonativeui/platform"
)

// A fixed, single attempt: the experiment driver, never this test, selects
// the predeclared repetitions. Existing outcome checks and budgets are kept.
func TestFullscreenProbeOnce(t *testing.T) {
	testlock.Hold(t, "activation")
	if os.Getenv("FS_PROBE_DELAYED") == "1" {
		t.Setenv("GNUI_TEST_DELAY_FULLSCREEN_READY", "1")
	}
	r, ok := greenButtonAttempt(t)
	if ok {
		t.Log(r.timeline)
		checkGreenButton(t, r)
	}
}

var fsProbe struct {
	turns, entries, timers, sources, waits, wakes, exits uint64
	lastTurn, maxTurnGap                                 int64
	completed                                            atomic.Uint64
	block                                                objc.ID
	addCompleted                                         objc.SEL
}

func fsProbeStart(a *App) func() {
	fsProbe.addCompleted = objc.Sel("addCompletedHandler:")
	fsProbe.block = objc.NewGlobalBlock("v@?@", func(*ffi.CB) { fsProbe.completed.Add(1) })
	lib, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		panic(err)
	}
	sym := func(name string) uintptr {
		p, err := purego.Dlsym(lib, name)
		if err != nil {
			panic(err)
		}
		return p
	}
	create, add, remove, release := sym("CFRunLoopObserverCreateWithHandler"), sym("CFRunLoopAddObserver"), sym("CFRunLoopRemoveObserver"), sym("CFRelease")
	mode := *(*uintptr)(ffi.PtrFromC(sym("kCFRunLoopCommonModes")))
	loop := uintptr(objc.Send(objc.Send(objc.Cls("NSRunLoop"), objc.Sel("mainRunLoop")), objc.Sel("getCFRunLoop")))
	block := objc.NewGlobalBlock("v@?@Q", func(c *ffi.CB) {
		switch c.Int(2) {
		case 1:
			fsProbe.entries++
		case 2:
			fsProbe.timers++
		case 4:
			fsProbe.sources++
		case 32:
			fsProbe.waits++
		case 64:
			fsProbe.wakes++
		case 128:
			fsProbe.exits++
		}
	})
	observer := objc.F.Reset().Arg(0).Arg(0x0fffffff).Arg(1).Arg(0).Arg(uintptr(block)).Call(create).R0()
	if observer == 0 {
		panic("cannot create run-loop observer")
	}
	objc.F.Call3(add, loop, observer, mode)
	return func() { objc.F.Call3(remove, loop, observer, mode); objc.F.Call1(release, observer) }
}

func fsProbeTurn() {
	now := platform.Now()
	if fsProbe.lastTurn != 0 {
		fsProbe.maxTurnGap = max(fsProbe.maxTurnGap, now-fsProbe.lastTurn)
	}
	fsProbe.lastTurn = now
	fsProbe.turns++
}

func fsProbeFrame(a *App) {
	if fsProbe.block != 0 && a.fr.CmdBuf != 0 {
		objc.Send1(objc.ID(a.fr.CmdBuf), fsProbe.addCompleted, uintptr(fsProbe.block))
	}
}

func fsProbeSnapshot(a *App) string {
	if a == nil {
		return "not-started"
	}
	return fmt.Sprintf("probe{turns=%d maxTurnGapMs=%d commits=%d gpuCompleted=%d cfEntry=%d cfBeforeTimers=%d cfBeforeSources=%d cfBeforeWait=%d cfAfterWait=%d cfExit=%d} %s %s",
		fsProbe.turns, fsProbe.maxTurnGap/1e6, a.frames, fsProbe.completed.Load(),
		fsProbe.entries, fsProbe.timers, fsProbe.sources, fsProbe.waits, fsProbe.wakes, fsProbe.exits,
		platform.DebugMainLoop(), a.StateLine(nil, platform.Now()))
}
