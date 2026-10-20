//go:build darwin

package platform

import (
	"syscall"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/objc"
)

// DebugOpenBarMenu is a screenshot hook (macOS only): it opens menu i of the
// menu bar (0 is the application menu) as the user's click would, so that a
// capture can show it. It presses the bar item through the accessibility
// API of our own process (AXPress), which AppKit answers from the run loop:
// this runs it until the menu closes. If no menu opened within half a
// second, it pops a copy of the menu up under the item's title instead. It
// returns whether either worked. Main thread, at a safe point.
func (w *Window) DebugOpenBarMenu(i int) bool {
	ax := loadAX()
	if ax == nil {
		return false
	}
	return w.openBarMenu(ax, i)
}

// DebugOnMenuOpen sets f to run each time a native menu opens, inside
// AppKit's tracking loop, as the user would act while it is open (live tests;
// macOS only). nil removes it. Main thread.
func DebugOnMenuOpen(f func(m DebugMenu)) {
	if f == nil {
		menuOpenHook = nil
		return
	}
	menuOpenHook = func(menu objc.ID) { f(DebugMenu{w: menuWindow(), menu: menu}) }
}

// DebugMenu is a native menu that is open (DebugOnMenuOpen).
type DebugMenu struct {
	w    *Window
	menu objc.ID
}

// Frame lets the menu stay open until the display link has run the window's
// frame handler once (the app draws a frame then if it owes one, as it does
// while the user looks at the menu), for at most a second. It reports
// whether the handler ran: it does not where the display link does not tick
// (a display asleep).
func (m DebugMenu) Frame() bool {
	if m.w == nil {
		return false
	}
	start, deadline := m.w.handlerRuns, Now()+1e9
	mode := objc.Str("NSEventTrackingRunLoopMode")
	loop := objc.Send(objc.Cls("NSRunLoop"), objc.Sel("currentRunLoop"))
	sel := objc.Sel("runMode:beforeDate:")
	for m.w.handlerRuns == start && Now() < deadline {
		until := objc.ID(objc.F.Reset().Arg(uintptr(clsNSDate)).Arg(uintptr(selDateSinceNow)).ArgF64(0.01).Call(objc.MsgSend).R0())
		objc.Send2(loop, sel, uintptr(mode), uintptr(until))
	}
	return m.w.handlerRuns != start
}

// Choose chooses item i of the menu, as a click or Return on it does, and
// closes the menu.
func (m DebugMenu) Choose(i int) {
	objc.Send1(m.menu, objc.Sel("performActionForItemAtIndex:"), uintptr(i))
	m.Close()
}

// Close closes the menu with nothing chosen, as Escape does.
func (m DebugMenu) Close() { objc.Send(m.menu, objc.Sel("cancelTracking")) }

// axAPI is the accessibility calls openBarMenu makes (a test fakes them).
// createApp and copyAttr return references the caller owns (the Create and
// Copy rule), 0 for none; at returns one it does not.
type axAPI struct {
	createApp func(pid int) objc.ID
	copyAttr  func(el objc.ID, name string) objc.ID
	count     func(arr objc.ID) int
	at        func(arr objc.ID, i int) objc.ID
	press     func(el objc.ID)
	value     func(v objc.ID, size bool) (x, y float64, ok bool) // an AXValue point, or size
	release   func(ref objc.ID)
	wait      func() // runs the run loop while the press opens the menu
}

// openBarMenu is DebugOpenBarMenu through ax. Every reference it owns is
// released before it returns, whichever way it does.
func (w *Window) openBarMenu(ax *axAPI, i int) bool {
	var owned [5]objc.ID
	n := 0
	own := func(ref objc.ID) objc.ID {
		if ref != 0 {
			owned[n] = ref
			n++
		}
		return ref
	}
	defer func() {
		for k := 0; k < n; k++ {
			ax.release(owned[k])
		}
	}()
	app := own(ax.createApp(syscall.Getpid()))
	if app == 0 {
		return false
	}
	bar := own(ax.copyAttr(app, "AXMenuBar"))
	if bar == 0 {
		return false
	}
	kids := own(ax.copyAttr(bar, "AXChildren"))
	// The accessibility menu bar starts with the Apple menu.
	if kids == 0 || i < 0 || i+1 >= ax.count(kids) {
		return false
	}
	item := ax.at(kids, i+1)
	opened := menuOpened
	ax.press(item)
	// The press opens the menu from the run loop: AppKit's tracking runs in
	// there, until the menu closes.
	ax.wait()
	if menuOpened > opened {
		return true
	}
	pos, size := own(ax.copyAttr(item, "AXPosition")), own(ax.copyAttr(item, "AXSize"))
	if pos == 0 || size == 0 {
		return false
	}
	x, y, ok := ax.value(pos, false)
	_, h, ok2 := ax.value(size, true)
	main := objc.Send(w.app, objc.Sel("mainMenu"))
	if !ok || !ok2 || main == 0 || i >= int(objc.Send(main, selNumberOfItems)) {
		return false
	}
	sub := objc.Send(objc.Send1(main, selItemAtIndex, uintptr(i)), objc.Sel("submenu"))
	if sub == 0 {
		return false
	}
	cp := objc.Send(sub, objc.Sel("copy"))
	// AX is top-left global; AppKit's screen coordinates are bottom-left on
	// the main display.
	screens := objc.Send(objc.Cls("NSScreen"), objc.Sel("screens"))
	top := objc.SendRect(objc.Send1(screens, objc.Sel("objectAtIndex:"), 0), selFrame).H
	objc.F.Reset().Arg(uintptr(cp)).Arg(uintptr(selPopUpMenu)).Arg(0).ArgPoint(x, top-y-h).Arg(0).Call(objc.MsgSend)
	objc.Release(cp)
	return true
}

// loadAX is the real accessibility API, nil where it cannot be loaded.
func loadAX() *axAPI {
	axLib, err := loadFramework("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices")
	if err != nil {
		return nil
	}
	cfLib, err := loadFramework("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation")
	if err != nil {
		return nil
	}
	sym := func(lib uintptr, n string) uintptr { p, _ := purego.Dlsym(lib, n); return p }
	createApp, copyAttr := sym(axLib, "AXUIElementCreateApplication"), sym(axLib, "AXUIElementCopyAttributeValue")
	perform, getValue, cfRelease := sym(axLib, "AXUIElementPerformAction"), sym(axLib, "AXValueGetValue"), sym(cfLib, "CFRelease")
	if createApp == 0 || copyAttr == 0 || perform == 0 || getValue == 0 || cfRelease == 0 {
		return nil
	}
	out := new(uintptr)   // heap memory for the out-parameter
	pt := new([2]float64) // and for the values
	return &axAPI{
		createApp: func(pid int) objc.ID {
			return objc.ID(objc.F.Reset().Arg(uintptr(pid)).Call(createApp).R0())
		},
		copyAttr: func(el objc.ID, name string) objc.ID {
			s := objc.Str(name)
			*out = 0
			if objc.F.Reset().Arg(uintptr(el)).Arg(uintptr(s)).ArgPtr(unsafe.Pointer(out)).Call(copyAttr).R0() != 0 {
				return 0
			}
			return objc.ID(*out)
		},
		count: func(arr objc.ID) int { return int(objc.Send(arr, objc.Sel("count"))) },
		at:    func(arr objc.ID, i int) objc.ID { return objc.Send1(arr, objc.Sel("objectAtIndex:"), uintptr(i)) },
		press: func(el objc.ID) {
			s := objc.Str("AXPress")
			objc.F.Reset().Arg(uintptr(el)).Arg(uintptr(s)).Call(perform)
		},
		value: func(v objc.ID, size bool) (float64, float64, bool) {
			typ := uintptr(1) // kAXValueCGPointType
			if size {
				typ = 2 // kAXValueCGSizeType
			}
			ok := objc.F.Reset().Arg(uintptr(v)).Arg(typ).ArgPtr(unsafe.Pointer(pt)).Call(getValue).R0()&0xFF != 0
			return pt[0], pt[1], ok
		},
		release: func(ref objc.ID) { objc.F.Reset().Arg(uintptr(ref)).Call(cfRelease) },
		wait: func() {
			until := objc.ID(objc.F.Reset().Arg(uintptr(clsNSDate)).Arg(uintptr(selDateSinceNow)).ArgF64(0.5).Call(objc.MsgSend).R0())
			objc.Send1(objc.Send(objc.Cls("NSRunLoop"), objc.Sel("currentRunLoop")), objc.Sel("runUntilDate:"), uintptr(until))
		},
	}
}
