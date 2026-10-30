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

	selEffectiveAppearance, selBestMatch = objc.SelZ("effectiveAppearance\x00"), objc.SelZ("bestMatchFromAppearancesWithNames:\x00")
	selIsEqualString = objc.SelZ("isEqualToString:\x00")
	selControlAccent, selUsingSpace, selSRGBSpace = objc.SelZ("controlAccentColor\x00"), objc.SelZ("colorUsingColorSpace:\x00"), objc.SelZ("sRGBColorSpace\x00")
	selRed, selGreen, selBlue = objc.SelZ("redComponent\x00"), objc.SelZ("greenComponent\x00"), objc.SelZ("blueComponent\x00")
	selSetAppearance, selAppearance, selAppearanceNamed = objc.SelZ("setAppearance:\x00"), objc.SelZ("appearance\x00"), objc.SelZ("appearanceNamed:\x00")
	selAppearanceChanged = objc.SelZ("gnuiAppearanceChanged:\x00")
	selObserveValue = objc.SelZ("observeValueForKeyPath:ofObject:change:context:\x00")

	apClsAppearance, apClsColor, apClsSpace = objc.Cls("NSAppearance"), objc.Cls("NSColor"), objc.Cls("NSColorSpace")
	apAquaName = objc.Retain(stringConstant(appKit, "NSAppearanceNameAqua"))
	apDarkName = objc.Retain(stringConstant(appKit, "NSAppearanceNameDarkAqua"))
	if apAquaName != 0 && apDarkName != 0 {
		apNameArr = [2]objc.ID{apAquaName, apDarkName}
		apNames = objc.Retain(objc.Send2(objc.Cls("NSArray"), objc.SelZ("arrayWithObjects:count:\x00"), uintptr(unsafe.Pointer(&apNameArr)), 2))
	}
	apApp = objc.Send(objc.Cls("NSApplication"), selSharedApplication)

	observer := objc.Define("GNUIAppearanceObserver", objc.Cls("NSObject"), nil)
	changed := func(*ffi.CB) { apChanged() }
	observer.Method(selAppearanceChanged, "v@:@", changed)
	observer.Method(selObserveValue, "v@:@@@^v", changed)
	apObserver = objc.Send(objc.Send(observer.Register(), selAlloc), objc.SelZ("init\x00"))
	if apApp != 0 {
		objc.Send4(apApp, objc.SelZ("addObserver:forKeyPath:options:context:\x00"), uintptr(apObserver), uintptr(objc.Str("effectiveAppearance")), 0, 0)
	}
	// Arguments are evaluated before the call is assembled: they share objc.F.
	selAddObserver := objc.SelZ("addObserver:selector:name:object:\x00")
	center := objc.Send(objc.Cls("NSNotificationCenter"), objc.SelZ("defaultCenter\x00"))
	observe := func(center, name objc.ID) {
		if center == 0 || name == 0 {
			return
		}
		objc.F.Reset().Arg(uintptr(center)).Arg(uintptr(selAddObserver)).
			Arg(uintptr(apObserver)).Arg(uintptr(selAppearanceChanged)).Arg(uintptr(name)).Arg(0).Call(objc.MsgSend)
	}
	observe(center, stringConstant(appKit, "NSSystemColorsDidChangeNotification"))
	// The system values (ReadSystemValues) also follow the scroller style and
	// the accessibility display options; the latter are posted on the
	// workspace's own centre.
	observe(center, stringConstant(appKit, "NSPreferredScrollerStyleDidChangeNotification"))
	if ws := objc.Send(objc.Cls("NSWorkspace"), objc.SelZ("sharedWorkspace\x00")); ws != 0 {
		observe(objc.Send(ws, objc.SelZ("notificationCenter\x00")), stringConstant(appKit, "NSWorkspaceAccessibilityDisplayOptionsDidChangeNotification"))
	}
	// The distributed center delivers while the app is in the background too
	// (NSNotificationSuspensionBehaviorDeliverImmediately).
	dist := objc.Send(objc.Cls("NSDistributedNotificationCenter"), objc.SelZ("defaultCenter\x00"))
	selAddDist := objc.SelZ("addObserver:selector:name:object:suspensionBehavior:\x00")
	for _, n := range []string{"AppleInterfaceThemeChangedNotification", "AppleColorPreferencesChangedNotification"} {
		name := objc.Str(n)
		objc.F.Reset().Arg(uintptr(dist)).Arg(uintptr(selAddDist)).
			Arg(uintptr(apObserver)).Arg(uintptr(selAppearanceChanged)).Arg(uintptr(name)).Arg(0).Arg(4).Call(objc.MsgSend)
	}
	// From here on ReadSystemValues reads: the serial tells those who asked
	// before (and got nothing) to ask again.
	apFollowing.Store(true)
	apSerial.Add(1)
}

// apChanged records a change the system reported: main thread (or any thread).
func apChanged() {
	apStale.Store(true)
	svStale.Store(true)
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
