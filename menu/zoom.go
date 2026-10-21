package menu

import "gonativeui/ui"

// ZoomAction is a change of the window's app zoom that a command with a zoom
// role asked for (RoleZoomIn, RoleZoomOut, RoleActualSize).
type ZoomAction uint8

const (
	// ZoomIn steps the zoom up.
	ZoomIn ZoomAction = iota + 1
	// ZoomOut steps the zoom down.
	ZoomOut
	// ZoomReset returns the zoom to 100 %.
	ZoomReset
)

// zoomQueueSize is how many zoom commands a frame can hold; more are dropped.
const zoomQueueSize = 8

// zoomAction is the action of a zoom role.
func (r Role) zoomAction() (ZoomAction, bool) {
	switch r {
	case RoleZoomIn:
		return ZoomIn, true
	case RoleZoomOut:
		return ZoomOut, true
	case RoleActualSize:
		return ZoomReset, true
	}
	return 0, false
}

// TakeZoom returns the next zoom command that Match gathered in the frame, in
// the order they were delivered, and false when there are none left. A command
// with a zoom role is not queued for Fired or Next: the app (app.App) takes it
// here after the frame is built, and applies it to the window's zoom. The
// commands wait until the next Match of another frame.
func (m *Model) TakeZoom() (ZoomAction, bool) {
	if m.zn == 0 {
		return 0, false
	}
	z := m.zq[0]
	m.zn--
	copy(m.zq[:m.zn], m.zq[1:m.zn+1])
	return z, true
}

// RoleID returns the command that has role r.
func (m *Model) RoleID(r Role) (ID, bool) {
	for i := range m.cmds {
		if m.cmds[i].Role == r {
			return m.cmds[i].ID, true
		}
	}
	return 0, false
}

// StandardCommand returns the command with id and role r as the View menu has
// it, with its standard title and shortcuts, for the zoom roles and
// RoleFullScreen:
//
//   - RoleZoomIn: "Zoom In", ⌘+ with ⌘= as its Alias on macOS, so that the key
//     without Shift zooms in too, as in browsers; Ctrl+= with Ctrl+Shift+=
//     (Ctrl++ on a US layout) elsewhere;
//   - RoleZoomOut: "Zoom Out", ⌘−;
//   - RoleActualSize: "Actual Size", ⌘0;
//   - RoleFullScreen: "Enter Full Screen", ⌃⌘F on macOS (AppKit retitles it
//     "Exit Full Screen" in full screen), F11 elsewhere.
//
// The shortcuts are the characters' ASCII values (ui.KeyCode), which the
// platform layer reports for the physical keys on a US layout. For any other
// role it returns a command with only the id and the role.
func StandardCommand(id ID, r Role) Command {
	c := Command{ID: id, Role: r}
	switch r {
	case RoleZoomIn:
		c.Title = "Zoom In"
		if ui.PrimaryMod() == ui.ModSuper {
			c.Shortcut = Shortcut{Key: '+', Primary: true}
			c.Alias = Shortcut{Key: '=', Primary: true}
		} else {
			c.Shortcut = Shortcut{Key: '=', Primary: true}
			c.Alias = Shortcut{Key: '=', Primary: true, Mods: ui.ModShift}
		}
	case RoleZoomOut:
		c.Title = "Zoom Out"
		c.Shortcut = Shortcut{Key: '-', Primary: true}
	case RoleActualSize:
		c.Title = "Actual Size"
		c.Shortcut = Shortcut{Key: '0', Primary: true}
	case RoleFullScreen:
		c.Title = "Enter Full Screen"
		if ui.PrimaryMod() == ui.ModSuper {
			c.Shortcut = Shortcut{Key: 'F', Primary: true, Mods: ui.ModCtrl}
		} else {
			c.Shortcut = Shortcut{Key: ui.KeyF11}
		}
	}
	return c
}
