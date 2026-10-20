//go:build darwin

package platform

import (
	"strconv"

	"gonativeui/internal/objc"
)

var (
	scrReady                                                                    bool
	scrCls                                                                      objc.ID
	scrKey                                                                      objc.ID
	scrSelScreens, scrSelCount, scrSelObjectAtIndex, scrSelFrame, scrSelVisible objc.SEL
	scrSelScale, scrSelDeviceDescription, scrSelObjectForKey, scrSelUnsignedInt objc.SEL
)

// scrSetup loads AppKit and resolves NSScreen and the selectors. Looking a class
// up before its framework is loaded gives nothing, and a program that asks for
// the screens before opening a window (winstate does, to place the window) has
// not loaded AppKit yet. Only a complete setup is remembered, so a failure is
// tried again at the next call. Main thread only, like Screens.
func scrSetup() bool {
	if scrReady {
		return true
	}
	if _, err := loadFramework("/System/Library/Frameworks/AppKit.framework/AppKit"); err != nil {
		return false
	}
	cls := objc.Cls("NSScreen")
	if cls == 0 {
		return false
	}
	scrCls = cls
	scrKey = objc.Retain(objc.Str("NSScreenNumber"))
	scrSelScreens, scrSelCount, scrSelObjectAtIndex = objc.Sel("screens"), objc.Sel("count"), objc.Sel("objectAtIndex:")
	scrSelFrame, scrSelVisible, scrSelScale = objc.Sel("frame"), objc.Sel("visibleFrame"), objc.Sel("backingScaleFactor")
	scrSelDeviceDescription, scrSelObjectForKey, scrSelUnsignedInt = objc.Sel("deviceDescription"), objc.Sel("objectForKey:"), objc.Sel("unsignedIntValue")
	scrReady = true
	return true
}

// Screens returns the displays that are connected, the primary one (the one with
// the menu bar) first. It reads AppKit, loading it if no window has yet, and so
// must be called on the main thread: from the loop's own goroutine, as with
// everything on the Window. It returns nil when there are no displays (a session
// without a window server).
func Screens() []ScreenInfo {
	out := ScreensInto(nil)
	if len(out) == 0 {
		return nil
	}
	return out
}

// ScreensInto appends the displays to dst as Screens returns them; with room
// in dst it allocates nothing (a display's ID is made once). Main thread.
func ScreensInto(dst []ScreenInfo) []ScreenInfo {
	if !scrSetup() {
		return dst
	}
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	arr := objc.Send(scrCls, scrSelScreens)
	if arr == 0 {
		return dst
	}
	n := int(objc.Send(arr, scrSelCount))
	var top float64 // the primary display's top edge in AppKit's bottom-left space
	for i := 0; i < n; i++ {
		s := objc.Send1(arr, scrSelObjectAtIndex, uintptr(i))
		fr, vis := objc.SendRect(s, scrSelFrame), objc.SendRect(s, scrSelVisible)
		if i == 0 {
			top = fr.Y + fr.H // AppKit puts y = 0 at the primary display's bottom edge
		}
		info := ScreenInfo{
			X: fr.X, Y: top - (fr.Y + fr.H), W: fr.W, H: fr.H,
			VisibleX: vis.X, VisibleY: top - (vis.Y + vis.H), VisibleW: vis.W, VisibleH: vis.H,
			Scale: objc.SendF64(s, scrSelScale),
		}
		if desc := objc.Send(s, scrSelDeviceDescription); desc != 0 {
			if num := objc.Send1(desc, scrSelObjectForKey, uintptr(scrKey)); num != 0 {
				info.ID = screenID(uint32(objc.Send(num, scrSelUnsignedInt)))
			}
		}
		dst = append(dst, info)
	}
	return dst
}

// scrIDs are the display IDs made so far, so that a display keeps one string.
var scrIDs []struct {
	num uint32
	id  string
}

func screenID(num uint32) string {
	for _, e := range scrIDs {
		if e.num == num {
			return e.id
		}
	}
	id := strconv.FormatUint(uint64(num), 10)
	scrIDs = append(scrIDs, struct {
		num uint32
		id  string
	}{num, id})
	return id
}
