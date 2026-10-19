//go:build darwin && !gnui_publicapi

package platform

import (
	"math"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// AppKit's private titlebar metrics (lights_darwin.go) and every call of
// them. A build with the gnui_publicapi tag leaves this file out and takes
// lights_public_darwin.go instead, so the selector names are not in a store
// binary (DECISIONS.md D53).

// privateAPIBuild is false in a gnui_publicapi build.
const privateAPIBuild = true

var (
	selSetTitlebarHeight    = objc.Sel("setTitlebarHeight:")                  // NSWindow, private
	selTitlebarHeight       = objc.Sel("titlebarHeight")                      // NSWindow, private
	selSetButtonGroupOffset = objc.Sel("setStandardWindowButtonGroupOffset:") // NSWindow, private; an NSSize whose height points up
	selButtonGroupOffset    = objc.Sel("standardWindowButtonGroupOffset")     // NSWindow, private

	// The selectors' signatures, checked once per process (lightsABIOK):
	// 0 not yet, 1 as expected, -1 not.
	lightsABI                                            int8
	fnClassGetMethod, fnMethodEncoding, fnObjectGetClass uintptr
	lightsEncodingOf                                     = methodEncoding // tests replace it to simulate a changed method
	lightsOffsetSkew                                     [2]float64       // tests: added, once more on every call, to the offset AppKit is given
	lightsOffsetCalls                                    float64
)

// titlebarMetricsUsable reports whether AppKit has the titlebar metrics,
// with the signatures they are called with.
func (w *Window) titlebarMetricsUsable() bool {
	return objc.SendBool1(w.win, selRespondsTo, uintptr(selSetTitlebarHeight)) &&
		objc.SendBool1(w.win, selRespondsTo, uintptr(selTitlebarHeight)) &&
		objc.SendBool1(w.win, selRespondsTo, uintptr(selSetButtonGroupOffset)) &&
		objc.SendBool1(w.win, selRespondsTo, uintptr(selButtonGroupOffset)) &&
		lightsABIOK(w.win)
}

// methodEncoding is the type encoding of obj's instance method sel, without
// the frame offsets ("v24@0:8d16" is "v@:d"); "" if there is none.
func methodEncoding(obj objc.ID, sel objc.SEL) string {
	if fnClassGetMethod == 0 {
		if lib, err := loadFramework("/usr/lib/libobjc.A.dylib"); err == nil {
			fnClassGetMethod, _ = purego.Dlsym(lib, "class_getInstanceMethod")
			fnMethodEncoding, _ = purego.Dlsym(lib, "method_getTypeEncoding")
			fnObjectGetClass, _ = purego.Dlsym(lib, "object_getClass")
		}
	}
	if fnClassGetMethod == 0 || fnMethodEncoding == 0 || fnObjectGetClass == 0 {
		return ""
	}
	m := objc.F.Call2(fnClassGetMethod, objc.F.Call1(fnObjectGetClass, uintptr(obj)), uintptr(sel))
	if m == 0 {
		return ""
	}
	p := objc.F.Call1(fnMethodEncoding, m)
	if p == 0 {
		return ""
	}
	var b []byte
	for i := uintptr(0); i < 256; i++ {
		c := *(*byte)(ffi.PtrFromC(p + i))
		if c == 0 {
			break
		}
		if c < '0' || c > '9' {
			b = append(b, c)
		}
	}
	return string(b)
}

// lightsABIOK reports whether AppKit's titlebar metrics have the signatures
// they are called with: setTitlebarHeight: takes a double and titlebarHeight
// returns one; the button-group offset is a struct of two doubles (NSSize,
// passed and returned in two floating-point registers). Checked once per
// process, on the first window.
func lightsABIOK(win objc.ID) bool {
	if lightsABI == 0 {
		lightsABI = -1
		pair := func(e string) bool {
			for _, name := range [...]string{"CGSize", "NSSize", "CGPoint", "NSPoint"} {
				if e == "{"+name+"=dd}" {
					return true
				}
			}
			return false
		}
		set, get := lightsEncodingOf(win, selSetButtonGroupOffset), lightsEncodingOf(win, selButtonGroupOffset)
		if lightsEncodingOf(win, selSetTitlebarHeight) == "v@:d" && lightsEncodingOf(win, selTitlebarHeight) == "d@:" &&
			len(set) > 3 && set[:3] == "v@:" && pair(set[3:]) &&
			len(get) > 2 && get[len(get)-2:] == "@:" && pair(get[:len(get)-2]) {
			lightsABI = 1
		}
	}
	return lightsABI == 1
}

// placeNative sets AppKit's titlebar height and button-group offset where
// they differ from ours. From a frame-change notification (inside AppKit's
// layout) that is all: AppKit's layout places the buttons and posts their
// frame changes, which measure them. Otherwise, when it changed them or has
// not calibrated yet, it lays the titlebar out and checks the close button:
// if its centre is off it corrects the offset by the difference (AppKit's
// default inset is not ours to know), and if that does not land either it
// gives up and leaves AppKit's default.
func (w *Window) placeNative(btn [3]objc.ID) {
	changed := false
	if h := objc.F.Reset().Arg(uintptr(w.win)).Arg(uintptr(selTitlebarHeight)).Call(objc.MsgSend).F64(0); math.Abs(h-w.titlebar) > 0.01 {
		objc.F.Reset().Arg(uintptr(w.win)).Arg(uintptr(selSetTitlebarHeight)).ArgF64(w.titlebar).Call(objc.MsgSend)
		changed = true
	}
	if ox, oy := objc.SendPoint(w.win, selButtonGroupOffset); ox != w.tb.offset[0] || oy != w.tb.offset[1] {
		w.setGroupOffset()
		changed = true
	}
	if w.tb.inNote || (!changed && w.tb.calibrated) {
		return
	}
	frame := objc.Send(objc.Send(objc.Send(btn[0], selSuperview), selSuperview), selSuperview) // the theme frame
	for try := 0; ; try++ {
		objc.Send(frame, selLayoutSubtree)
		cx, cy := w.buttonCentre(btn[0])
		dx, dy := float64(w.lights[0])-cx, float64(w.lights[1])-cy
		if math.Abs(dx) <= 0.25 && math.Abs(dy) <= 0.25 {
			w.tb.calibrated = true
			break
		}
		if try == 2 { // AppKit does not put them where asked: back to its default
			w.tb.native = false
			w.tb.offset = [2]float64{}
			objc.F.Reset().Arg(uintptr(w.win)).Arg(uintptr(selSetButtonGroupOffset)).ArgPoint(0, 0).Call(objc.MsgSend)
			objc.F.Reset().Arg(uintptr(w.win)).Arg(uintptr(selSetTitlebarHeight)).ArgF64(-1).Call(objc.MsgSend) // AppKit's "no custom height"
			objc.Send(frame, selLayoutSubtree)
			break
		}
		w.tb.offset[0] += dx
		w.tb.offset[1] -= dy
		w.setGroupOffset()
	}
	if !w.tb.native {
		objc.Send(frame, selUpdateTrackingAreas)
		return
	}
	w.updateButtonTracking()
}

func (w *Window) setGroupOffset() {
	x, y := w.tb.offset[0], w.tb.offset[1]
	if lightsOffsetSkew != [2]float64{} {
		lightsOffsetCalls++
		x, y = x+lightsOffsetSkew[0]*lightsOffsetCalls, y+lightsOffsetSkew[1]*lightsOffsetCalls
	}
	objc.F.Reset().Arg(uintptr(w.win)).Arg(uintptr(selSetButtonGroupOffset)).ArgPoint(x, y).Call(objc.MsgSend)
}
