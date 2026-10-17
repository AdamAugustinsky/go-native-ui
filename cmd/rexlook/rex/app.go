package rex

import (
	"gonativeui/app"
)

// The window the scene is made for: the reference's size, the custom
// titlebar and its traffic lights, and the smallest window the layout keeps
// its panes usable in (TestLayoutAnySize runs from there up).
const (
	MinWidthPt  = 600
	MinHeightPt = 400
)

// Config is the window of the scene: 1057×639 pt with a 46 pt custom titlebar
// on macOS and the traffic lights at (21, 22).
func Config() app.Config {
	return app.Config{
		Title: "rexlook", Width: WindowWidthPt, Height: WindowHeightPt,
		MinWidth: MinWidthPt, MinHeight: MinHeightPt,
		Titlebar: TitlebarPt, TrafficLights: [2]float32{21, 22},
		Clear: [4]float32{0.9, 0.9, 0.9, 1}, // the background mesh covers every pixel
	}
}

var _ app.Initer = (*Rex)(nil)

// Init implements app.Initer: it takes the window's text system and scale,
// puts the icons in the renderer's atlas and sizes the box cache for the
// largest scene (about 730 boxes at the reference size, 8 panes). It runs on
// the window-loop thread, which the system symbols need.
func (r *Rex) Init(a *app.App) error {
	_, _, scale := a.Size()
	r.init(a.Text, scale)
	r.AttachRenderer(a.R)
	a.UI.Reserve(4096)
	return nil
}
