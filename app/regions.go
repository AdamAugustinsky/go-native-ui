package app

import (
	"gonativeui/platform"
	"gonativeui/ui"
)

// syncRegions hands the finished frame's pointer map to the window: the
// cursor regions of the boxes that have a ui cursor, the cursor of the box
// that holds the mouse button (a drag keeps its cursor wherever the pointer
// goes), and the titlebar drag map. The platform copies and compares, so only
// a change reaches the OS. Regions are borrowed from emit's finished buffers.
//
// With native tooltips (platform.NativeTooltips) the tooltip regions the
// widgets declared go to the window the same way; the system asks for their
// text through the function set in initUI.
//
// This is the only cursor mechanism: an app marks boxes with
// ui.Context.NextCursor / PushCursor and ui.WindowDrag.
func (a *App) syncRegions() {
	cursors, drags := a.UI.FramePointerRegions()
	a.Win.SetCursorRegions(cursors)
	a.Win.SetCursorOverride(platformCursor(a.UI.ActiveCursor()))
	a.Win.SetDragRegions(drags)

	a.syncTooltips(a.Win)
}

// tooltipTarget is the part of platform.Window that syncTooltips drives.
type tooltipTarget interface {
	SetTooltipRegions([]platform.TooltipRegion)
}

// syncTooltips hands the tooltips the widgets declared in the finished frame to
// the window, when tooltips are native. The platform compares, so only a change
// reaches the OS; the buffers are reused.
func (a *App) syncTooltips(win tooltipTarget) {
	if !a.UI.NativeTooltips() {
		return
	}
	win.SetTooltipRegions(a.UI.FrameTooltipRegions())
}

// platformCursor maps a ui cursor to the platform's; the values are the same
// (a test pins that).
func platformCursor(c ui.Cursor) platform.Cursor { return platform.Cursor(c) }

// textTarget is the part of platform.Window that syncText drives.
type textTarget interface {
	SetIMERect(x, y, w, h float32)
	SetTextInput(active bool)
	SetSecureTextInput(on bool)
	DiscardComposition()
	RequestPaste(tag uint64)
}

// syncText hands the focused text field's state to the window: where its caret
// or composition is (for the input method's candidate window), whether a
// field has focus at all and whether it is a secure field (the system's
// secure keyboard entry, Roman input only), and arms the wake-up for the
// field's next caret blink. The platform compares, so calling it every frame changes nothing when
// nothing moved; start is the platform time the frame began at.
//
// When ui reports the input method's composition orphaned (IMECancel: its field
// lost the focus and committed it, or is gone) the platform's composition is
// discarded, without committing it a second time, and the translator forgets
// it at once so that no field sees it again.
//
// A paste request of a field (the paste chord or the Edit menu) is forwarded to
// the platform with the field's key as the tag; the answer comes back as an
// EvPaste event, which the translator gives to that field only.
func (a *App) syncText(win textTarget, start int64) {
	r, ok := a.UI.IMERect()
	if ok {
		win.SetIMERect(r[0], r[1], r[2], r[3])
	}
	win.SetTextInput(ok)
	win.SetSecureTextInput(a.UI.SecureFieldFocused())
	if tag, ok := a.UI.PasteRequest(); ok {
		win.RequestPaste(uint64(tag))
	}
	if a.UI.IMECancel() {
		win.DiscardComposition()
		a.tr.in.SetPreedit(nil, 0, 0)
	}
	a.wakeAt = 0
	if ns := a.UI.WakeAfter(); ns > 0 {
		a.wakeAt = start + ns
	}
}
