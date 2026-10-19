package rex

import (
	"gonativeui/app"
	"gonativeui/icon"
	"gonativeui/platform"
	"gonativeui/ui"
)

// The native shell (experimental; macOS): the window's chrome is AppKit's and
// only the panes are the scene's. NativeConfig asks for a Liquid Glass
// backdrop and a toolbar holding the tabs, ⌘ and +; the window's title and
// subtitle are the host block. Init looks at what the window got: with a
// backdrop the scene paints no field (the theme's Backdrop), with a toolbar
// it draws no titlebar controls and keeps the toolbar in step with its tabs.
// Elsewhere the window gets neither and the scene draws everything, as with
// Config. Like app.go, this file is glue: the scene's drawing never sees the
// platform.

// Command ids of the toolbar: the + and ⌘ buttons and the tab group, whose
// tab i chooses cmdTab0+i.
const (
	cmdPlus    = 1
	cmdCommand = 2
	cmdTabs    = 3
	cmdTab0    = 16
)

// toolbarTabW is the width of one native tab in points: the reference's tabs
// share 666 pt among three.
const toolbarTabW = 205

// NativeConfig is Config with the native shell: a Liquid Glass backdrop and
// a compact unified toolbar with the tabs (filled in by the scene), a
// flexible space, ⌘ and +. The title and subtitle are the reference's host
// block. Experimental.
func NativeConfig() app.Config {
	c := Config()
	c.Title, c.Subtitle = "rex-snake", "Mac Studio"
	c.Backdrop = platform.BackdropGlass
	c.ToolbarCompact = true
	c.Toolbar = []platform.ToolbarItem{
		{Kind: platform.ToolbarTabs, ID: cmdTabs, Label: "Tabs", TabWidth: toolbarTabW},
		{Kind: platform.ToolbarFlexibleSpace},
		{Kind: platform.ToolbarButton, ID: cmdCommand, Symbol: "command", Label: "Commands"},
		{Kind: platform.ToolbarButton, ID: cmdPlus, Symbol: "plus", Label: "New Tab"},
	}
	return c
}

// nativeBar is the part of platform.Window the scene drives its toolbar
// through.
type nativeBar interface {
	SetToolbarTabs(id uint16, tabs []platform.ToolbarTab, selected int)
	SetToolbarEnabled(id uint16, enabled bool)
}

// attachNative takes the window's native shell (Init).
func (r *Rex) attachNative(w *platform.Window) {
	if w == nil {
		return
	}
	r.backdrop = w.Backdrop() != platform.BackdropNone
	if w.HasToolbar() {
		r.bar = w
	}
}

// NativeShell reports whether the window has the backdrop and the toolbar.
func (r *Rex) NativeShell() (backdrop, toolbar bool) { return r.backdrop, r.bar != nil }

// commands applies the toolbar's clicks (ui commands).
func (r *Rex) commands(c *ui.Context) {
	for {
		id, ok := c.TakeCommand()
		if !ok {
			return
		}
		switch {
		case id == cmdPlus:
			r.newTab()
		case id >= cmdTab0 && id < cmdTab0+maxTabs:
			r.selectTab(int(id - cmdTab0))
		}
	}
}

// syncBar shows the tabs in the toolbar: a no-op while they are unchanged.
func (r *Rex) syncBar() {
	for i := 0; i < r.nTabs; i++ {
		t := &r.tabs[i]
		r.barTabs[i] = platform.ToolbarTab{ID: cmdTab0 + uint16(i), Symbol: iconSpecs[t.style.icon-icon.NumStandard].Symbol, Label: t.label}
	}
	r.bar.SetToolbarTabs(cmdTabs, r.barTabs[:r.nTabs], r.active)
	r.bar.SetToolbarEnabled(cmdPlus, r.nTabs < maxTabs)
}
