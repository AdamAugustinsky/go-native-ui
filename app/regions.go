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
