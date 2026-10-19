//go:build darwin

package platform

import (
	"strconv"
	"sync"

	"gonativeui/internal/objc"
)

var (
	scrOnce                                                                                  sync.Once
	scrCls                                                                                   objc.ID
	scrKey                                                                                   objc.ID
	scrSelScreens, scrSelCount, scrSelObjectAtIndex, scrSelFrame, scrSelVisible, scrSelScale objc.SEL
	scrSelDeviceDescription, scrSelObjectForKey, scrSelUnsignedInt                           objc.SEL
)

// Screens returns the displays that are connected, the primary one (the one with
// the menu bar) first. It reads AppKit and so must be called on the main
// thread: from the loop's own goroutine, as with everything on the Window. It
// returns nil when there are no displays (a session without a window server).
func Screens() []ScreenInfo {
	scrOnce.Do(func() {
		scrCls = objc.Cls("NSScreen")
		scrKey = objc.Retain(objc.Str("NSScreenNumber"))
		scrSelScreens, scrSelCount, scrSelObjectAtIndex = objc.Sel("screens"), objc.Sel("count"), objc.Sel("objectAtIndex:")
		scrSelFrame, scrSelVisible, scrSelScale = objc.Sel("frame"), objc.Sel("visibleFrame"), objc.Sel("backingScaleFactor")
		scrSelDeviceDescription, scrSelObjectForKey, scrSelUnsignedInt = objc.Sel("deviceDescription"), objc.Sel("objectForKey:"), objc.Sel("unsignedIntValue")
	})
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	arr := objc.Send(scrCls, scrSelScreens)
	if arr == 0 {
		return nil
	}
	n := int(objc.Send(arr, scrSelCount))
	if n == 0 {
		return nil
	}
	out := make([]ScreenInfo, 0, n)
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
				info.ID = strconv.FormatUint(uint64(uint32(objc.Send(num, scrSelUnsignedInt))), 10)
			}
		}
		out = append(out, info)
	}
	return out
}
