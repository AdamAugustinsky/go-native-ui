package platform

import (
	"sync"
	"sync/atomic"

	"gonativeui/internal/objc"
)

// System values on macOS (see appearance.go, D39). The semantic colours are
// dynamic NSColors: each resolves against the current drawing appearance, so
// the reader makes the light and then the dark appearance current
// (+[NSAppearance setCurrentAppearance:], per thread; the previous one is put
// back), converts each colour to sRGB and reads its four components. The
// appearances are the effectiveAppearance of a probe NSView given Aqua or
// DarkAqua: AppKit does not make the high-contrast appearances by name
// (appearanceNamed: with an AccessibilityHighContrast name returns plain Aqua
// on 27.2), and a view's effective appearance is where it applies Increase
// Contrast. That it does so for a view outside a window is an assumption: no
// test machine had Increase Contrast on. The settings come from NSWorkspace
// (accessibilityDisplayShould…) and +[NSScroller preferredScrollerStyle].
//
// The cache is read again when svStale is set: at first, and by apChanged,
// which every observed notification calls (apSetup also observes the
// scroller-style and accessibility-display notifications for this).

var (
	apFollowing atomic.Bool // apSetup has run: the app follows the system appearance
	svStale     atomic.Bool // the system values are out of date; starts true
	svOnce      sync.Once
	svCur       SystemValues // main thread

	svSel                                  [NumSystemColors]objc.SEL
	svHas                                  [NumSystemColors]bool // NSColor responds to the selector
	svNames                                [2]objc.ID            // Aqua, DarkAqua
	svView                                 objc.ID               // the probe view
	selSetViewAppearance                   objc.SEL
	svWorkspace, svClsScroller             objc.ID
	selCurrentAppearance, selSetCurrentApp objc.SEL
	selAlpha, selObjectAtIndex, selCount   objc.SEL
	selReduceMotion, selReduceTransparency objc.SEL
	selIncreaseContrast, selScrollerStyle  objc.SEL
	selRespondsToSelector                  objc.SEL
)

func init() { svStale.Store(true) }

// ReadSystemValues fills dst with the system's colours and settings (see
// appearance.go). A read from the cache copies it; nothing allocates.
func ReadSystemValues(dst *SystemValues) {
	if !apFollowing.Load() {
		*dst = SystemValues{}
		return
	}
	if svStale.Load() {
		refreshSystemValues()
	}
	*dst = svCur
}

// svSetup resolves the selectors and the appearance names, after apSetup's
// (whose classes and selectors it shares).
func svSetup() {
	apOnce.Do(apSetup)
	appKit, err := loadFramework("/System/Library/Frameworks/AppKit.framework/AppKit")
	if err != nil {
		return
	}
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	selCurrentAppearance, selSetCurrentApp = objc.Sel("currentAppearance"), objc.Sel("setCurrentAppearance:")
	selAlpha, selObjectAtIndex, selCount = objc.Sel("alphaComponent"), objc.Sel("objectAtIndex:"), objc.Sel("count")
	selReduceMotion = objc.Sel("accessibilityDisplayShouldReduceMotion")
	selReduceTransparency = objc.Sel("accessibilityDisplayShouldReduceTransparency")
	selIncreaseContrast = objc.Sel("accessibilityDisplayShouldIncreaseContrast")
	selScrollerStyle = objc.Sel("preferredScrollerStyle")
	selRespondsToSelector = objc.Sel("respondsToSelector:")
	for i, n := range SystemColorNames {
		svSel[i] = objc.Sel(n)
		svHas[i] = objc.SendBool1(apClsColor, selRespondsToSelector, uintptr(svSel[i]))
	}
	for d, n := range [2]string{"NSAppearanceNameAqua", "NSAppearanceNameDarkAqua"} {
		if s := stringConstant(appKit, n); s != 0 {
			if a := objc.Send1(apClsAppearance, selAppearanceNamed, uintptr(s)); a != 0 {
				svNames[d] = objc.Retain(a)
			}
		}
	}
	svView = objc.Send(objc.Send(objc.Cls("NSView"), selAlloc), objc.Sel("init"))
	selSetViewAppearance = objc.Sel("setAppearance:")
	svWorkspace = objc.Retain(objc.Send(objc.Cls("NSWorkspace"), objc.Sel("sharedWorkspace")))
	svClsScroller = objc.Cls("NSScroller")
}

// svReads counts the reads from AppKit (SystemValuesReads).
var svReads atomic.Uint32

// SystemValuesReads counts the times ReadSystemValues has read the system's
// values from AppKit (not from its cache), for tests that check who reads
// them and on which thread.
func SystemValuesReads() uint32 { return svReads.Load() }

// refreshSystemValues re-reads the system values into svCur. The stale flag
// is cleared first, so a change that lands while reading is not lost.
func refreshSystemValues() {
	svOnce.Do(svSetup)
	svStale.Store(false)
	svReads.Add(1)
	var v SystemValues
	pool := objc.F.Call0(objc.PoolPush)
	if svWorkspace != 0 {
		v.ReduceMotion = objc.SendBool(svWorkspace, selReduceMotion)
		v.ReduceTransparency = objc.SendBool(svWorkspace, selReduceTransparency)
		v.IncreaseContrast = objc.SendBool(svWorkspace, selIncreaseContrast)
	}
	v.ReduceTransparency = v.ReduceTransparency || forceReduceTransparency // GNUI_REDUCE_TRANSPARENCY (material_darwin.go)
	if svClsScroller != 0 {
		v.LegacyScrollers = objc.Send(svClsScroller, selScrollerStyle) == 0 // NSScrollerStyleLegacy
	}
	if a, b := svAppearance(0), svAppearance(1); a != 0 && b != 0 {
		v.Known = true
		readSystemColors(a, &v.Colors[0])
		readSystemColors(b, &v.Colors[1])
	}
	objc.F.Call1(objc.PoolPop, pool)
	svCur = v
}

// svAppearance is the effective appearance of the probe view in the light
// (0) or dark (1) mode: with Increase Contrast, the high-contrast one. It
// runs inside an autorelease pool.
func svAppearance(d int) objc.ID {
	if svView == 0 || svNames[d] == 0 {
		return 0
	}
	objc.Send1(svView, selSetViewAppearance, uintptr(svNames[d]))
	return objc.Send(svView, selEffectiveAppearance)
}

// readSystemColors reads every system colour with appearance current. It
// runs inside an autorelease pool.
func readSystemColors(appearance objc.ID, dst *[NumSystemColors]uint32) {
	prev := objc.Send(apClsAppearance, selCurrentAppearance)
	objc.Send1(apClsAppearance, selSetCurrentApp, uintptr(appearance))
	srgb := objc.Send(apClsSpace, selSRGBSpace)
	for i := range dst {
		dst[i] = 0
		if !svHas[i] {
			continue
		}
		c := objc.Send(apClsColor, svSel[i])
		if SystemColor(i) == ColorAlternatingContentBackground {
			if c == 0 || objc.Send(c, selCount) < 2 {
				continue
			}
			c = objc.Send1(c, selObjectAtIndex, 1)
		}
		if c != 0 {
			c = objc.Send1(c, selUsingSpace, uintptr(srgb))
		}
		if c != 0 {
			dst[i] = rgba8(objc.SendF64(c, selRed), objc.SendF64(c, selGreen), objc.SendF64(c, selBlue))&^0xFF |
				alpha8(objc.SendF64(c, selAlpha))
		}
	}
	// Put the previous appearance back. The getter cannot tell an appearance
	// someone set from the fallback to NSApp's, and setting that fallback
	// back would pin it (a later change of NSApp's appearance would no longer
	// reach colours resolved on this thread); nil is the fallback itself.
	if apApp != 0 && prev == objc.Send(apApp, selEffectiveAppearance) {
		prev = 0
	}
	objc.Send1(apClsAppearance, selSetCurrentApp, uintptr(prev))
}

// alpha8 is an alpha in 0..1 as a byte.
func alpha8(a float64) uint32 {
	switch {
	case !(a > 0):
		return 0
	case a >= 1:
		return 255
	}
	return uint32(a*255 + 0.5)
}
