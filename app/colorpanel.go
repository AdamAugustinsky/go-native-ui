package app

import (
	"math"

	"gonativeui/platform"
	"gonativeui/ui"
)

// The system colour panel (platform/colorpanel.go), driven by a colour well:
// the well asks for it each frame it is active (ui.Context.ColorPanel) and
// this follows the request after the frame: the panel is shown for a new
// owner, moved to a colour the app changed, and closed when no well asks for
// it. The user's changes and the closing of the panel come back as
// ui.Input.ColorPick and ColorClosed (translator).

// colorTarget is the part of platform.Window that syncColorPanel drives.
type colorTarget interface {
	ShowColorPanel(r, g, b, a float32) error
	SetColorPanelColor(r, g, b, a float32) error
	CloseColorPanel()
}

// colorPanelSync is what the app knows of the panel it showed.
type colorPanelSync struct {
	shown bool
	owner ui.Key
	rgba  uint32 // the colour the panel has
}

// rgbaOf packs sRGB components, 0 to 1, as 0xRRGGBBAA.
func rgbaOf(r, g, b, a float32) uint32 {
	q := func(v float32) uint32 { return uint32(math.Round(float64(min(max(v, 0), 1)) * 255)) }
	return q(r)<<24 | q(g)<<16 | q(b)<<8 | q(a)
}

func rgbaParts(c uint32) (r, g, b, a float32) {
	return float32(c>>24) / 255, float32(c>>16&0xFF) / 255, float32(c>>8&0xFF) / 255, float32(c&0xFF) / 255
}

// onColor notes what the user did in the panel, before the translator turns it
// into input: the colour it has now, or that it is gone.
func (a *App) onColor(e *platform.Event) {
	if e.Code == 1 {
		a.cp.shown = false
		return
	}
	a.cp.rgba = rgbaOf(e.X, e.Y, e.DX, e.DY)
}

// syncColorPanel follows the frame's colour panel request. A panel the user
// moved is not moved back: what it reported (a.cp.rgba) is the colour the
// well takes, and the well's request carries the same colour next frame.
func (a *App) syncColorPanel(win colorTarget) {
	cp := &a.cp
	owner, rgba, ok := a.UI.ColorPanelRequest()
	switch {
	case !ok:
		if cp.shown {
			win.CloseColorPanel()
			cp.shown = false
		}
	case !cp.shown || owner != cp.owner:
		r, g, b, al := rgbaParts(rgba)
		if win.ShowColorPanel(r, g, b, al) == nil {
			cp.shown, cp.owner, cp.rgba = true, owner, rgba
		} else { // no panel here: the well opens its own picker in the next frame
			a.tr.in.SetColorFailed()
			a.UI.RequestFrame()
		}
	case rgba != cp.rgba:
		r, g, b, al := rgbaParts(rgba)
		if win.SetColorPanelColor(r, g, b, al) == nil {
			cp.rgba = rgba
		}
	}
}
