//go:build darwin

package widgettest

import (
	"math"

	"gonativeui/app"
	"gonativeui/internal/objc"
)

var (
	selStdButton    = objc.Sel("standardWindowButton:")
	selConvertPoint = objc.Sel("convertPoint:toView:")
	selBounds       = objc.Sel("bounds")
	selContentView  = objc.Sel("contentView")
)

// readSystemButtons asks AppKit where the window's close, minimise and zoom
// buttons are: the union of their frames converted to the window's coordinates
// and turned to the content's, in points from its top left. It is empty when
// there are no buttons or the window is not above the content (a standard
// window: the buttons sit in a title bar of their own above the content view).
func readSystemButtons(a *app.App) [4]float32 {
	if a.Win == nil {
		return [4]float32{}
	}
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	win := objc.ID(a.Win.Native().NSWindow)
	content := objc.Send(win, selContentView)
	if win == 0 || content == 0 {
		return [4]float32{}
	}
	// the content view's frame in window coordinates
	objc.F.Reset().Arg(uintptr(content)).Arg(uintptr(selConvertPoint)).ArgPoint(0, 0).Arg(0).Call(objc.MsgSend)
	cx, cy := objc.F.F64(0), objc.F.F64(1)
	cb := objc.SendRect(content, selBounds)
	u := [4]float64{math.MaxFloat64, math.MaxFloat64, -math.MaxFloat64, -math.MaxFloat64}
	for i := 0; i < 3; i++ {
		b := objc.Send1(win, selStdButton, uintptr(i))
		if b == 0 {
			return [4]float32{}
		}
		bb := objc.SendRect(b, selBounds)
		// both corners: a flipped view's origin is its top left
		for _, p := range [2][2]float64{{0, 0}, {bb.W, bb.H}} {
			objc.F.Reset().Arg(uintptr(b)).Arg(uintptr(selConvertPoint)).ArgPoint(p[0], p[1]).Arg(0).Call(objc.MsgSend)
			x, y := objc.F.F64(0)-cx, objc.F.F64(1)-cy // from the content's bottom left
			u[0], u[1] = math.Min(u[0], x), math.Min(u[1], y)
			u[2], u[3] = math.Max(u[2], x), math.Max(u[3], y)
		}
	}
	r := [4]float32{float32(u[0]), float32(cb.H - u[3]), float32(u[2]), float32(cb.H - u[1])}
	if r[1] >= float32(cb.H) || r[3] <= 0 || r[0] >= float32(cb.W) || r[2] <= 0 || r[3] > float32(cb.H) {
		return [4]float32{} // the buttons are outside the content (a standard window's title bar)
	}
	return r
}
