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
// frame, the content size of ContentSize, the size of the system's window
// buttons, the cursor, drag, tooltip and input-method rectangles (pixels).
// The zoom is the whole window's, chrome included: a custom titlebar
// (Config.Titlebar) is given the zoomed band and the window buttons move to
// its centre (syncTitlebar), and SystemButtons follows. A window's geometry is
// saved in the unit of the OS's window frame (winstate: points on macOS and
// Linux, physical pixels on Windows), derived from ContentSize, in screen
// points, through winstate.FromPoints and ToPoints, never from the ui's
// points (Unzoomed converts them) and never by a scale of your own. Keep the
// zoom itself in winstate.State.Zoom. A frame saved from ui points would grow
// by the zoom with every restart; this is the bug egui apps had
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

// SetZoom sets the zoom factor, limited to MinZoom..MaxZoom and rounded to a
// whole percent, and returns the factor that was set. A value that is not a
// number leaves the zoom as it is.
//
// The rounding bounds the fonts: every effective scale has a set of fonts of
// its own (theme fonts, text icons), so a factor of any precision would make
// a new set for each value it passes through, and a drag or a script through
// the range has hundreds of values. At most fontScales of them (the current
// one and the latest few) are kept; see useFontScale.
// The window keeps its size; the next frame is built and drawn at the new
// scale. It can be called from Init (the first frame is drawn at that zoom),
// from Build and from an event handler, on the main thread.
func (a *App) SetZoom(f float32) float32 {
	if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
		return a.Zoom()
	}
	f = min(max(f, MinZoom), MaxZoom)
	f = float32(math.Round(float64(f)*100)) / 100
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

// syncZoomMenu shows the zoom commands of the menu model as the zoom allows:
// Zoom In below the top of the range, Zoom Out above the bottom, Actual Size
// away from 100 %. The commands are only shown disabled (Model.SetLimited): they
// are still delivered, and the bounds are enforced when they are applied, in
// order, so that Zoom In then Actual Size at 100 % ends at 100 %. The native
// menu reads it when it opens.
func (a *App) syncZoomMenu() {
	if a.menu == nil {
		return
	}
	z := a.Zoom()
	if id, ok := a.menu.RoleID(menu.RoleZoomIn); ok {
		a.menu.SetLimited(id, z >= MaxZoom)
	}
	if id, ok := a.menu.RoleID(menu.RoleZoomOut); ok {
		a.menu.SetLimited(id, z <= MinZoom)
	}
	if id, ok := a.menu.RoleID(menu.RoleActualSize); ok {
		a.menu.SetLimited(id, z == 1)
	}
}

// titlebarSetter is the part of the window that changes the custom titlebar's
// metrics after it is open (platform.Window.SetTitlebar, macOS).
type titlebarSetter interface {
	SetTitlebar(height float32, lights [2]float32)
}

// syncTitlebar gives a window with a custom titlebar (Config.Titlebar) the
// zoomed band: the zoom is the whole window's, as in a browser or an editor,
// so the app's titlebar and toolbar scale with the content, and the system's
// window buttons, which stay at the system's size, move to stay centred in
// the band. It is called at the start of a frame, before the buttons' place is
// read for the ui, so that a frame never mixes the new band with the old
// buttons. It does nothing at the zoom the metrics were set for, so a frame at
// any steady zoom costs one comparison.
//
// The band is Config.Titlebar times the zoom, and the close button's centre is
// Config.TrafficLights with y times the zoom: y keeps its place in the band (a
// centre at half the band stays at half the zoomed band). x stays where it
// is. The buttons keep the inset from the window's edge that every macOS
// window has, whatever the content's zoom; the toolbar's first item clears
// them through SystemButtons, which follows, so its gap to the buttons and the
// band's padding are the zoomed ones. Scaling x too would move the buttons
// away from the edge by a fraction of the zoom for no gain, and AppKit
// refreshes the group's hover area (the glyphs on hover) for a new x only at
// the next title change, so a moved x leaves the glyphs 7 pt off the buttons
// (measured on macOS 15; platform.Window.SetTitlebar).
//
// Where the buttons stay at the system's default place (platform.Options.
// PublicAPIOnly, the gnui_publicapi build) the window ignores the centre and
// SystemButtons says where they are.
func (a *App) syncTitlebar(win any) {
	if a.cfg.Titlebar <= 0 {
		return
	}
	z := a.Zoom()
	was := a.tbZoom
	if was == 0 {
		was = 1
	}
	if z == was {
		return
	}
	set, ok := win.(titlebarSetter)
	if !ok {
		return
	}
	l := a.cfg.TrafficLights
	set.SetTitlebar(a.cfg.Titlebar*z, [2]float32{l[0], l[1] * z})
	a.tbZoom = z
}

// fontScales is how many effective scales keep their fonts alive. The current
// one is always among them. Four cover the common back-and-forth (100 %, a
// step either way, and one more) without a re-creation, and bound the font
// memory to four sets whatever the zoom does.
const fontScales = 4

// useFontScale keeps the fonts of at most fontScales scales alive. It runs at
// the start of every build, before the kit asks for the fonts of the frame's
// scale: when that scale is new and fontScales are already in use, the one
// used longest ago is released (text.System.ReleaseScale: the CTFonts,
// DirectWrite faces or Go font faces, and the keys and ids they held).
//
// That scale is safe to release. It is the fifth most recent, so at least four
// frames at other scales have been built since a frame used it, and the
// renderer has at most three frames in flight (and they hold atlas pages and
// draw lists, never fonts: nothing of the CPU-side fonts is read after
// Build). The glyph images stay in the atlas until its next reset, so the
// frames in flight and the scales kept are not disturbed.
func (a *App) useFontScale() {
	if a.Text == nil {
		return
	}
	s := a.EffectiveScale()
	if a.nScales > 0 && a.scales[0] == s {
		return
	}
	i := -1
	for j := 0; j < a.nScales; j++ {
		if a.scales[j] == s {
			i = j
			break
		}
	}
	if i < 0 {
		// The scales are the app's (its windows share the text system): the
		// one released is the least recently used that no open window is
		// at now (D30); with every kept scale in use, one more is kept.
		i = a.nScales
		for j := a.nScales - 1; j >= 0 && a.nScales == fontScales; j-- {
			if !a.scaleInUse(a.scales[j]) {
				a.Text.ReleaseScale(a.scales[j])
				i = j
				break
			}
		}
		if i == a.nScales {
			if a.nScales == len(a.scales) {
				i = a.nScales - 1 // (more windows at distinct scales than kept: the oldest goes)
				a.Text.ReleaseScale(a.scales[i])
			} else {
				a.nScales++
			}
		}
	}
	copy(a.scales[1:i+1], a.scales[:i])
	a.scales[0] = s
}
