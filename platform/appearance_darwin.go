package platform

import (
	"sync"
	"sync/atomic"
	"unsafe"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// System appearance on macOS (see appearance.go, D32). Dark comes from
// NSApp.effectiveAppearance matched against Aqua and DarkAqua, the accent from
// NSColor.controlAccentColor in sRGB. Both are read when the cache is stale
// (the first call, then after a change), so a frame costs one atomic load.
//
// Changes arrive on the main thread from four observers: key-value observing
// on NSApp's effectiveAppearance (it has already been updated when the
// observer runs), NSSystemColorsDidChangeNotification, and the distributed
// AppleInterfaceThemeChangedNotification and
// AppleColorPreferencesChangedNotification (these can arrive before
// effectiveAppearance has caught up, which the KVO callback then covers: the
// last notification of a change always finds the final value). Each marks the
// cache stale, bumps the serial and calls Wake. A change reaches the app more
// than once at worst, which costs one extra wake.

var (
	apOnce   sync.Once
	apStale  atomic.Bool // the cache is out of date; starts true (set by apSetup's caller)
	apSerial atomic.Uint32
	apCur    Appearance // main thread

	apNameArr [2]objc.ID // the array's backing store while NSArray copies it

	apApp, apNames, apDarkName, apAquaName, apObserver objc.ID
	apClsAppearance, apClsColor, apClsSpace            objc.ID

	selEffectiveAppearance, selBestMatch, selIsEqualString objc.SEL
	selControlAccent, selUsingSpace, selSRGBSpace          objc.SEL
	selRed, selGreen, selBlue                              objc.SEL
	selSetAppearance, selAppearance, selAppearanceNamed    objc.SEL
	selAppearanceChanged, selObserveValue                  objc.SEL
)

func init() { apStale.Store(true) }

// SystemAppearance returns the current system appearance (see appearance.go).
func SystemAppearance() Appearance {
	if apStale.Load() {
		refreshAppearance()
	}
	return apCur
}

// AppearanceSerial counts system appearance changes (see appearance.go).
func AppearanceSerial() uint32 { return apSerial.Load() }

// SetDarkChrome makes the OS-drawn parts of the window match the app's mode
// (see appearance.go): the window's NSAppearance, which covers the traffic
// lights and the menus the window shows.
func (w *Window) SetDarkChrome(dark bool) {
	if w == nil || w.win == 0 {
		return
	}
	apOnce.Do(apSetup)
	pool := objc.F.Call0(objc.PoolPush)
	name := apAquaName
	if dark {
		name = apDarkName
	}
	if a := objc.Send1(apClsAppearance, selAppearanceNamed, uintptr(name)); a != 0 {
		objc.Send1(w.win, selSetAppearance, uintptr(a))
	}
	objc.F.Call1(objc.PoolPop, pool)
}

// apSetup resolves the selectors and constants and installs the observers.
// It runs once, on the first read or SetDarkChrome.
func apSetup() {
	appKit, err := loadFramework("/System/Library/Frameworks/AppKit.framework/AppKit")
	if err != nil {
		return
	}
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)

	selEffectiveAppearance, selBestMatch = objc.Sel("effectiveAppearance"), objc.Sel("bestMatchFromAppearancesWithNames:")
	selIsEqualString = objc.Sel("isEqualToString:")
	selControlAccent, selUsingSpace, selSRGBSpace = objc.Sel("controlAccentColor"), objc.Sel("colorUsingColorSpace:"), objc.Sel("sRGBColorSpace")
	selRed, selGreen, selBlue = objc.Sel("redComponent"), objc.Sel("greenComponent"), objc.Sel("blueComponent")
	selSetAppearance, selAppearance, selAppearanceNamed = objc.Sel("setAppearance:"), objc.Sel("appearance"), objc.Sel("appearanceNamed:")
	selAppearanceChanged = objc.Sel("gnuiAppearanceChanged:")
	selObserveValue = objc.Sel("observeValueForKeyPath:ofObject:change:context:")

	apClsAppearance, apClsColor, apClsSpace = objc.Cls("NSAppearance"), objc.Cls("NSColor"), objc.Cls("NSColorSpace")
	apAquaName = objc.Retain(stringConstant(appKit, "NSAppearanceNameAqua"))
	apDarkName = objc.Retain(stringConstant(appKit, "NSAppearanceNameDarkAqua"))
	if apAquaName != 0 && apDarkName != 0 {
		apNameArr = [2]objc.ID{apAquaName, apDarkName}
		apNames = objc.Retain(objc.Send2(objc.Cls("NSArray"), objc.Sel("arrayWithObjects:count:"), uintptr(unsafe.Pointer(&apNameArr)), 2))
	}
	apApp = objc.Send(objc.Cls("NSApplication"), selSharedApplication)

	imp := ffi.NewCallback(func(*ffi.CB) { apChanged() })
	apObserver = objc.Send(objc.Send(objc.NewClass("GNUIAppearanceObserver", objc.Cls("NSObject"), nil, imp, []objc.Method{
		{Sel: selAppearanceChanged, Types: "v@:@"},
		{Sel: selObserveValue, Types: "v@:@@@^v"},
	}), selAlloc), objc.Sel("init"))
	if apApp != 0 {
		objc.Send4(apApp, objc.Sel("addObserver:forKeyPath:options:context:"), uintptr(apObserver), uintptr(objc.Str("effectiveAppearance")), 0, 0)
	}
	// Arguments are evaluated before the call is assembled: they share objc.F.
	selAddObserver := objc.Sel("addObserver:selector:name:object:")
	center := objc.Send(objc.Cls("NSNotificationCenter"), objc.Sel("defaultCenter"))
	colors := stringConstant(appKit, "NSSystemColorsDidChangeNotification")
	objc.F.Reset().Arg(uintptr(center)).Arg(uintptr(selAddObserver)).
		Arg(uintptr(apObserver)).Arg(uintptr(selAppearanceChanged)).Arg(uintptr(colors)).Arg(0).Call(objc.MsgSend)
	// The distributed center delivers while the app is in the background too
	// (NSNotificationSuspensionBehaviorDeliverImmediately).
	dist := objc.Send(objc.Cls("NSDistributedNotificationCenter"), objc.Sel("defaultCenter"))
	selAddDist := objc.Sel("addObserver:selector:name:object:suspensionBehavior:")
	for _, n := range []string{"AppleInterfaceThemeChangedNotification", "AppleColorPreferencesChangedNotification"} {
		name := objc.Str(n)
		objc.F.Reset().Arg(uintptr(dist)).Arg(uintptr(selAddDist)).
			Arg(uintptr(apObserver)).Arg(uintptr(selAppearanceChanged)).Arg(uintptr(name)).Arg(0).Arg(4).Call(objc.MsgSend)
	}
}

// apChanged records a change the system reported: main thread (or any thread).
func apChanged() {
	apStale.Store(true)
	apSerial.Add(1)
	Wake()
}

// refreshAppearance re-reads the system appearance into apCur. The stale flag
// is cleared first, so a change that lands while reading is not lost.
func refreshAppearance() {
	apOnce.Do(apSetup)
	apStale.Store(false)
	cur := Appearance{}
	if apApp != 0 && apNames != 0 {
		pool := objc.F.Call0(objc.PoolPush)
		if ea := objc.Send(apApp, selEffectiveAppearance); ea != 0 {
			cur.Known = true
			best := objc.Send1(ea, selBestMatch, uintptr(apNames))
			cur.Dark = best != 0 && objc.SendBool1(best, selIsEqualString, uintptr(apDarkName))
		}
		if c := objc.Send(apClsColor, selControlAccent); c != 0 {
			if c = objc.Send1(c, selUsingSpace, uintptr(objc.Send(apClsSpace, selSRGBSpace))); c != 0 {
				cur.Accent = rgba8(objc.SendF64(c, selRed), objc.SendF64(c, selGreen), objc.SendF64(c, selBlue))
			}
		}
		objc.F.Call1(objc.PoolPop, pool)
	}
	apCur = cur
}

// rgba8 packs sRGB components in 0..1 into an opaque 0xRRGGBBAA.
func rgba8(r, g, b float64) uint32 {
	q := func(v float64) uint32 {
		switch {
		case !(v > 0): // also NaN
			return 0
		case v >= 1:
			return 255
		}
		return uint32(v*255 + 0.5)
	}
	return q(r)<<24 | q(g)<<16 | q(b)<<8 | 0xFF
}
