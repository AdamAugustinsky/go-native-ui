package platform

import "gonativeui/internal/objc"

var selMouseLocation = objc.SelZ("mouseLocation\x00")

// pointerLocation reads +[NSEvent mouseLocation] (AppKit's bottom-left space)
// and turns it as ScreensInto turns frames.
func pointerLocation() (x, y float64, ok bool) {
	if !scrSetup() {
		return 0, 0, false
	}
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	arr := objc.Send(scrCls, scrSelScreens)
	if arr == 0 || objc.Send(arr, scrSelCount) == 0 {
		return 0, 0, false
	}
	fr := objc.SendRect(objc.Send1(arr, scrSelObjectAtIndex, 0), scrSelFrame)
	px, py := objc.SendPoint(objc.Cls("NSEvent"), selMouseLocation)
	return px, fr.Y + fr.H - py, true
}
