//go:build darwin

package power

import (
	"fmt"
	"sync"

	"gonativeui/internal/cf"
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
	"gonativeui/platform"
)

// macOS. Hold goes to IOKit from whatever goroutine calls (a frame of
// internal/cf of its own; IOPMAssertion* are thread-safe). The screens'
// notifications come to a small observer object on the main thread and are
// stored as the latest value of an observation (platform.SourcePower).

const (
	assertionLevelOn = 255 // kIOPMAssertionLevelOn
	typeIdle         = "PreventUserIdleSystemSleep"
	typeDisplay      = "PreventUserIdleDisplaySleep"
)

var iokit struct {
	once            sync.Once
	err             error
	create, release uintptr
}

func iokitReady() error {
	iokit.once.Do(func() {
		if iokit.err = cf.Init(); iokit.err != nil {
			return
		}
		lib, err := cf.Framework("IOKit")
		if err != nil {
			iokit.err = err
			return
		}
		iokit.create = cf.Sym(lib, "IOPMAssertionCreateWithName")
		iokit.release = cf.Sym(lib, "IOPMAssertionRelease")
		if iokit.create == 0 || iokit.release == 0 {
			iokit.err = cf.ErrUnavailable
		}
	})
	return iokit.err
}

func osHold(kind Kind, reason string) (uint32, error) {
	if err := iokitReady(); err != nil {
		return 0, fmt.Errorf("power: IOKit: %w", err)
	}
	fr := cf.Acquire()
	defer cf.Release(fr)
	typ := typeIdle
	if kind == PreventDisplaySleep {
		typ = typeDisplay
	}
	t, name := cf.String(fr, typ), cf.String(fr, reason)
	defer cf.CFRelease(fr, t)
	defer cf.CFRelease(fr, name)
	out := cf.Out(fr)
	// IOReturn: 0 is kIOReturnSuccess.
	st := int32(fr.Reset().Arg(t).Arg(assertionLevelOn).Arg(name).Arg(uintptr(out)).Call(iokit.create).R0())
	if st != 0 {
		return 0, fmt.Errorf("%w (IOReturn %#x)", ErrRefused, uint32(st))
	}
	return uint32(cf.Word(fr)), nil
}

func osRelease(id uint32) error {
	fr := cf.Acquire()
	defer cf.Release(fr)
	if st := int32(fr.Reset().Arg32(id).Call(iokit.release).R0()); st != 0 {
		return fmt.Errorf("power: releasing assertion %d (IOReturn %#x)", id, uint32(st))
	}
	return nil
}

var obs struct {
	once sync.Once
	view *platform.Observation
}

var (
	selScreensSleep = objc.Sel("gnuiScreensSleep:")
	selScreensWake  = objc.Sel("gnuiScreensWake:")
)

func osObserve() error {
	if !platform.AppStarted() {
		return ErrNotStarted
	}
	obs.once.Do(func() {
		obs.view = platform.NewObservation(platform.SourcePower)
		var seq int64
		d := objc.Define("GNUIPowerObserver", objc.Cls("NSObject"), nil)
		set := func(ev Event) func(*ffi.CB) {
			return func(*ffi.CB) { seq++; obs.view.Set(int64(ev), seq) }
		}
		d.Method(selScreensSleep, "v@:@", set(ScreensSleep))
		d.Method(selScreensWake, "v@:@", set(ScreensWake))
		target := objc.Send(objc.Send(d.Register(), objc.Sel("alloc")), objc.Sel("init")) // kept for the process
		ws := objc.Send(objc.Cls("NSWorkspace"), objc.Sel("sharedWorkspace"))
		nc := objc.Send(ws, objc.Sel("notificationCenter"))
		objc.AddObserver(nc, target, selScreensSleep, objc.Retain(objc.Str("NSWorkspaceScreensDidSleepNotification")), 0)
		objc.AddObserver(nc, target, selScreensWake, objc.Retain(objc.Str("NSWorkspaceScreensDidWakeNotification")), 0)
	})
	return nil
}
