package rex

import (
	"gonativeui/app"
	"gonativeui/platform"
	"gonativeui/ui"
)

// The native shell (experimental; macOS): the window's material and its ⌘
// and + buttons are AppKit's; the tabs, the host block and the panes are the
// scene's. NativeConfig asks for a Liquid Glass backdrop and the two native
// buttons, keeping the custom titlebar band with the system's traffic
// lights. Init looks at what the window got: with a backdrop the scene paints
// no field (the theme's Backdrop) and its tab capsule turns translucent; with
// the buttons it draws no ⌘ and + of its own. Elsewhere the window gets
// neither and the scene draws everything, as with Config. Like app.go, this
// file is glue: the scene's drawing never sees the platform.

// Command ids of the native buttons.
const (
	cmdPlus    = 1
	cmdCommand = 2
)

// NativeConfig is Config with the native shell: a Liquid Glass backdrop and
// native ⌘ and + buttons where the scene draws its own (glass circles with
// glass; borderless symbols otherwise). Experimental.
func NativeConfig(glass bool) app.Config {
	c := Config()
	c.Title = "rex-snake"
	c.Backdrop = platform.BackdropGlass
	style := platform.ButtonPlain
	if glass {
		style = platform.ButtonGlass
	}
	// The reference's glyph centres: 115.5 and 46.5 reference pixels from the
	// right edge, 45 from the top.
	c.Buttons = []platform.NativeButton{
		{ID: cmdCommand, Symbol: "command", Label: "Commands", X: -57.75, Y: 22.5, Size: 28, Style: style},
		{ID: cmdPlus, Symbol: "plus", Label: "New Tab", X: -23.25, Y: 22.5, Size: 28, Style: style},
	}
	return c
}

// nativeButtons is the part of platform.Window the scene drives its native
// buttons through.
type nativeButtons interface {
	SetButtonEnabled(id uint16, enabled bool)
}

// attachNative takes the window's native shell (Init).
func (r *Rex) attachNative(w *platform.Window) {
	if w == nil {
		return
	}
	r.backdrop = w.Backdrop() != platform.BackdropNone
	if w.HasButtons() {
		r.btns = w
	}
}

// NativeShell reports whether the window has the backdrop and the native
// buttons.
func (r *Rex) NativeShell() (backdrop, buttons bool) { return r.backdrop, r.btns != nil }

// commands applies the native buttons' clicks (ui commands).
func (r *Rex) commands(c *ui.Context) {
	for {
		id, ok := c.TakeCommand()
		if !ok {
			return
		}
		if id == cmdPlus {
			r.newTab()
		}
	}
}
