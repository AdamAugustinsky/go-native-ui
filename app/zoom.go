package app

import (
	"math"

	"gonativeui/menu"
)

// App zoom: the window's content is drawn larger or smaller while the window
// keeps its size on the screen.
//
// The window has a zoom factor, 1 at the start. The scale the content is
// built and drawn at is the backing scale (pixels per point of the display)
// times the zoom: ui.Context.Begin gets that product, so a zoom change goes
// the way a scale change does. A widget.Kit resolves the theme again (theme.Resolve:
// metrics, fonts), rasterises the icons again and uploads them to the atlas
// when it sees the new scale, the scroll offsets of boxes are carried over in
// proportion and a base.VList stays on its anchor item. A zoom change
// allocates, as a scale change does; frames at any zoom allocate nothing.
//
// # Units
//
// The ui's points are zoomed points: at zoom 1.5 a ui point is 1.5 points of
// the screen. Everything the window system sees is in screen points or in
// pixels, which the zoom does not touch: Config.Width and Height, the window
// frame, the content size of ContentSize, the system's window buttons, the
// cursor, drag, tooltip and input-method rectangles (pixels). A window's
// geometry is saved in screen points: derive it from ContentSize or the
// platform's frame, never from the ui's points (Unzoomed converts them), and
// keep the zoom itself in winstate.State.Zoom. A frame saved from ui points
// would grow by the zoom with every restart; this is the bug egui apps had
// (docs/research/egui.md §2.6).
//
// # Commands
//
// A menu.Model with commands that have the roles menu.RoleZoomIn,
// RoleZoomOut and RoleActualSize (menu.StandardCommand makes them, ⌘+ and ⌘=,
// ⌘− and ⌘0 on macOS) zooms the window that was given the model with SetMenu:
// the commands Match gathers (from the menu bar, a drawn menu or a shortcut)
// are applied when the frame is built, and the next frame is drawn at the new
// zoom. The items are disabled at the ends of the range.
const (
	// MinZoom and MaxZoom bound the zoom factor. 50 % keeps 13 pt text
	// readable (6.5 pt) and 300 % keeps a 960×600 window at 320×200 ui
	// points, small but usable; browsers stop at 25 % and 500 %, where a
	// desktop UI no longer holds together.
	MinZoom, MaxZoom = 0.5, 3.0
)

// zoomSteps are the factors ZoomIn and ZoomOut move between: the presets
// browsers use inside 50–300 %, ten percent apart around 100 % where users
// look for a slightly bigger or smaller text, and growing to a quarter or half
// step further out where a ten percent change is not seen.
var zoomSteps = [...]float32{0.5, 0.67, 0.75, 0.8, 0.9, 1, 1.1, 1.25, 1.5, 1.75, 2, 2.5, 3}

// Zoom is the window's zoom factor: 1 is actual size.
func (a *App) Zoom() float32 {
	if a.zoom == 0 {
		return 1
	}
	return a.zoom
}

// SetZoom sets the zoom factor, limited to MinZoom..MaxZoom, and returns the
// factor that was set. A value that is not a number leaves the zoom as it is.
// The window keeps its size; the next frame is built and drawn at the new
// scale. It can be called from Init (the first frame is drawn at that zoom),
// from Build and from an event handler, on the main thread.
func (a *App) SetZoom(f float32) float32 {
	if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
		return a.Zoom()
	}
	f = min(max(f, MinZoom), MaxZoom)
	if f == a.Zoom() {
		return f
	}
	a.zoom = f
	a.resized = true  // the first frame at the new scale is followed by one that settles
	a.tr.dirty = true // repaint, and settle the hover; a zoom is not input latency
	a.p.invalidate()
	a.syncZoomMenu()
	return f
}

// ZoomIn moves the zoom to the next step above it (50 67 75 80 90 100 110 125
// 150 175 200 250 300 %) and returns the new factor.
func (a *App) ZoomIn() float32 {
	z := a.Zoom()
	for _, s := range zoomSteps {
		if s > z+zoomEps {
			return a.SetZoom(s)
		}
	}
	return a.SetZoom(MaxZoom)
}

// ZoomOut moves the zoom to the next step below it and returns the new factor.
func (a *App) ZoomOut() float32 {
	z := a.Zoom()
	for i := len(zoomSteps) - 1; i >= 0; i-- {
		if zoomSteps[i] < z-zoomEps {
			return a.SetZoom(zoomSteps[i])
		}
	}
	return a.SetZoom(MinZoom)
}

// ZoomReset returns the zoom to 100 %.
func (a *App) ZoomReset() { a.SetZoom(1) }

// zoomEps tells a factor between steps from a step (SetZoom takes any value,
// a restored one may be 1.2000000476837158).
const zoomEps = 1e-4

// EffectiveScale is the scale the content is built and drawn at: pixels per
// ui point, which is the backing scale (Size) times Zoom.
func (a *App) EffectiveScale() float32 { return a.scale * a.Zoom() }

// ContentSize is the size of the window's content in screen points, whatever
// the zoom: the pixel size over the backing scale. It is what to save as the
// window's size (winstate.State.Frame, less the title bar). The ui's own
// extent in points is this divided by the zoom.
func (a *App) ContentSize() (w, h float32) {
	return float32(a.w) / a.scale, float32(a.h) / a.scale
}

// Unzoomed converts v from ui points to screen points (v times the zoom).
func (a *App) Unzoomed(v float32) float32 { return v * a.Zoom() }

// applyMenuZoom applies the zoom commands that the menu model gathered in the
// frame just built. When the zoom changed, the frame leaves a settle owed.
func (a *App) applyMenuZoom() {
	if a.menu == nil {
		return
	}
	for {
		act, ok := a.menu.TakeZoom()
		if !ok {
			return
		}
		before := a.Zoom()
		switch act {
		case menu.ZoomIn:
			a.ZoomIn()
		case menu.ZoomOut:
			a.ZoomOut()
		case menu.ZoomReset:
			a.ZoomReset()
		}
		if a.Zoom() != before {
			a.settle = true // another frame is owed: the one at the new scale
		}
	}
}

// syncZoomMenu enables the zoom commands of the menu model as the zoom allows:
// Zoom In below the top of the range, Zoom Out above the bottom, Actual Size
// away from 100 %. The native menu reads it when it opens.
func (a *App) syncZoomMenu() {
	if a.menu == nil {
		return
	}
	z := a.Zoom()
	if id, ok := a.menu.RoleID(menu.RoleZoomIn); ok {
		a.menu.SetEnabled(id, z < MaxZoom)
	}
	if id, ok := a.menu.RoleID(menu.RoleZoomOut); ok {
		a.menu.SetEnabled(id, z > MinZoom)
	}
	if id, ok := a.menu.RoleID(menu.RoleActualSize); ok {
		a.menu.SetEnabled(id, z != 1)
	}
}
