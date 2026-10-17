package app

import (
	"gonativeui/platform"
	"gonativeui/ui"
)

// syncRegions hands the finished frame's pointer map to the window: the
// cursor regions of the boxes that have a ui cursor, the cursor of the box
// that holds the mouse button (a drag keeps its cursor wherever the pointer
// goes), and the titlebar drag map. The platform copies and compares, so only
// a change reaches the OS; the buffers here are reused, and a frame without
// cursor or WindowDrag boxes visits no box.
//
// This is the only cursor mechanism: an app marks boxes with
// ui.Context.NextCursor / PushCursor and ui.WindowDrag.
func (a *App) syncRegions() {
	a.curUI, a.dragUI = a.UI.PointerRegions(a.curUI, a.dragUI)
	a.curPlat = a.curPlat[:0]
	for i := range a.curUI {
		a.curPlat = append(a.curPlat, platform.CursorRegion{Rect: a.curUI[i].Rect, Cursor: platformCursor(a.curUI[i].Cursor)})
	}
	a.Win.SetCursorRegions(a.curPlat)
	a.Win.SetCursorOverride(platformCursor(a.UI.ActiveCursor()))

	a.dragPl = a.dragPl[:0]
	for i := range a.dragUI {
		a.dragPl = append(a.dragPl, platform.DragRegion{Rect: a.dragUI[i].Rect, Block: a.dragUI[i].Block})
	}
	a.Win.SetDragRegions(a.dragPl)
}

// platformCursor maps a ui cursor to the platform's; the values are the same
// (a test pins that).
func platformCursor(c ui.Cursor) platform.Cursor { return platform.Cursor(c) }

// textTarget is the part of platform.Window that syncText drives.
type textTarget interface {
	SetIMERect(x, y, w, h float32)
	SetTextInput(active bool)
}

// syncText hands the focused text field's state to the window: where its caret
// or composition is (for the input method's candidate window) and whether a
// field has focus at all, and arms the wake-up for the field's next caret
// blink. The platform compares, so calling it every frame changes nothing when
// nothing moved; start is the platform time the frame began at.
func (a *App) syncText(win textTarget, start int64) {
	r, ok := a.UI.IMERect()
	if ok {
		win.SetIMERect(r[0], r[1], r[2], r[3])
	}
	win.SetTextInput(ok)
	a.wakeAt = 0
	if ns := a.UI.WakeAfter(); ns > 0 {
		a.wakeAt = start + ns
	}
}
