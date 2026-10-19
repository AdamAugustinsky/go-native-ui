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
	ax, err := loadFramework("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices")
	if err != nil {
		return false
	}
	sym := func(n string) uintptr { p, _ := purego.Dlsym(ax, n); return p }
	createApp, copyAttr, perform, getValue := sym("AXUIElementCreateApplication"), sym("AXUIElementCopyAttributeValue"), sym("AXUIElementPerformAction"), sym("AXValueGetValue")
	if createApp == 0 || copyAttr == 0 || perform == 0 || getValue == 0 {
		return false
	}
	out := new(uintptr) // heap memory for the out-parameters
	attr := func(el objc.ID, name string) objc.ID {
		s := objc.Str(name)
		*out = 0
		if objc.F.Reset().Arg(uintptr(el)).Arg(uintptr(s)).ArgPtr(unsafe.Pointer(out)).Call(copyAttr).R0() != 0 {
			return 0
		}
		return objc.ID(*out)
	}
	pt := new([2]float64)
	point := func(el objc.ID, name string) (float64, float64, bool) {
		v := attr(el, name)
		if v == 0 {
			return 0, 0, false
		}
		typ := uintptr(1) // kAXValueCGPointType
		if name == "AXSize" {
			typ = 2 // kAXValueCGSizeType
		}
		ok := objc.F.Reset().Arg(uintptr(v)).Arg(typ).ArgPtr(unsafe.Pointer(pt)).Call(getValue).R0()&0xFF != 0
		return pt[0], pt[1], ok
	}
	app := objc.ID(objc.F.Reset().Arg(uintptr(syscall.Getpid())).Call(createApp).R0())
	bar := attr(app, "AXMenuBar")
	kids := attr(bar, "AXChildren")
	// The accessibility menu bar starts with the Apple menu.
	if kids == 0 || i < 0 || i+1 >= int(objc.Send(kids, objc.Sel("count"))) {
		return false
	}
	item := objc.Send1(kids, objc.Sel("objectAtIndex:"), uintptr(i+1))
	press := objc.Str("AXPress")
	opened := menuOpened
	objc.F.Reset().Arg(uintptr(item)).Arg(uintptr(press)).Call(perform)
	// The press opens the menu from the run loop: AppKit's tracking runs in
	// there, until the menu closes.
	until := objc.ID(objc.F.Reset().Arg(uintptr(clsNSDate)).Arg(uintptr(selDateSinceNow)).ArgF64(0.5).Call(objc.MsgSend).R0())
	objc.Send1(objc.Send(objc.Cls("NSRunLoop"), objc.Sel("currentRunLoop")), objc.Sel("runUntilDate:"), uintptr(until))
	if menuOpened > opened {
		return true
	}
	x, y, ok := point(item, "AXPosition")
	_, h, ok2 := point(item, "AXSize")
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
