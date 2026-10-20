package app

import "gonativeui/ui"

// windowSource is the part of platform.Window that syncWindow reads and
// drives. *platform.Window has all of it on every OS (the calls that do not
// apply are no-ops there).
type windowSource interface {
	ClientDecorations() bool
	Maximized() bool
	Minimize()
	ToggleMaximize()
	RequestClose()
}

// buttonsSource is the part of the window that knows where the system draws
// the window buttons over the content (macOS with a custom titlebar), in
// points.
type buttonsSource interface{ SystemButtons() [4]float32 }

// syncWindow hands the window's state to the UI for the frame about to be
// built, as syncAppearance does the system appearance, and carries out what
// the UI asked of the window in the last one:
//
//   - ui.Context.SetSystemButtons: where the OS draws the window buttons over
//     the content (in pixels; empty where it draws none there), so that a
//     titlebar leaves them room;
//   - ui.Context.SetDecorations: whether the app has to draw the titlebar and
//     whether the window is maximized (Wayland client-side decorations);
//   - ui.Context.SetWindowTitle: Config.Title, for that titlebar;
//   - the window action a titlebar button requested (close, minimise,
//     maximise), which asked for the frame it is carried out in.
//
// It reads cached values and allocates nothing.
func (a *App) syncWindow() {
	if a.Win != nil { // a nil *platform.Window in the interface would not be nil
		a.syncWindowFrom(a.Win, a.buttons)
	}
}

// syncWindowFrom is syncWindow on win; b is win's buttonsSource, nil when it
// has none.
func (a *App) syncWindowFrom(win windowSource, b buttonsSource) {
	switch a.UI.TakeWindowAction() {
	case ui.ActionClose:
		win.RequestClose()
	case ui.ActionMinimise:
		win.Minimize()
	case ui.ActionToggleMaximise:
		win.ToggleMaximize()
	}
	a.syncTitlebar(win)
	a.UI.SetDecorations(ui.Decorations{Client: win.ClientDecorations(), Maximized: win.Maximized()})
	a.UI.SetWindowTitle(a.cfg.Title)
	var r [4]float32
	if b != nil {
		p := b.SystemButtons()
		r = [4]float32{p[0] * a.scale, p[1] * a.scale, p[2] * a.scale, p[3] * a.scale}
	}
	a.UI.SetSystemButtons(r) // after syncTitlebar: where the buttons are now
}
