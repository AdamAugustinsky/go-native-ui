package ui

// The window's own state as the platform reports it, handed to the UI before
// each frame the way the system appearance is (appearance.go), and the
// requests the UI hands back: what the app and the theme need to lay a custom
// titlebar out around what the operating system draws, or to draw one when it
// does not.

// Decorations says who draws the window's titlebar and how the window is
// shown.
type Decorations struct {
	// Client is true when the app has to draw the titlebar and the window
	// buttons itself because the system does not: a Wayland compositor that
	// asks for client-side decorations (GNOME) or has no decoration
	// protocol. It is false on macOS and Windows, on X11, and under a
	// compositor that draws them.
	Client bool
	// Maximized is true while the window is maximized (the maximize button
	// then restores it).
	Maximized bool
	// Caption is true when the window has a titlebar the app draws
	// (app.Config.Titlebar on Windows) whose caption buttons (minimise,
	// maximise, close) the app draws too, in SystemButtons' rectangle: the
	// OS reports them there to its snap layouts and the window's hit test,
	// and draws nothing. rexui.BeginToolbar draws them (rexui.CaptionButtons).
	Caption bool
}

// WindowAction is something the UI asks the window to do.
type WindowAction uint8

const (
	// ActionNone is no request.
	ActionNone WindowAction = iota
	// ActionClose asks to close the window, as the close button of a
	// system titlebar does: the app receives it like any other close.
	ActionClose
	// ActionMinimise minimises the window.
	ActionMinimise
	// ActionToggleMaximise maximises the window, or restores it.
	ActionToggleMaximise
)

type windowState struct {
	buttons [4]float32
	deco    Decorations
	title   string
	action  WindowAction
}

// SetSystemButtons gives the UI the rectangle in which the operating system
// draws the window buttons over the content (the macOS traffic lights under a
// custom titlebar): x0, y0, x1, y1 in pixels from the window's top left. An
// empty rectangle (the zero value) means the system draws none over the
// content. The app calls it before Begin, every frame, as it does
// SetSystemAppearance; app.Run does.
func (c *Context) SetSystemButtons(r [4]float32) { c.win.buttons = r }

// SystemButtons is the rectangle the system draws the window buttons in, in
// pixels, empty when it draws none over the content. A titlebar keeps its own
// content out of it: the theme's Chrome.Titlebar reports the free part of the
// band in TitlebarResult.Free, and an app that places things in the band uses
// that.
func (c *Context) SystemButtons() [4]float32 { return c.win.buttons }

// SetDecorations gives the UI who draws the window's titlebar and whether the
// window is maximized, for the next frame. The app calls it before Begin,
// every frame; app.Run does.
func (c *Context) SetDecorations(d Decorations) { c.win.deco = d }

// Decorations is what SetDecorations gave: whether the app has to draw the
// titlebar (Kit.BeginWindow then does) and whether the window is maximized.
func (c *Context) Decorations() Decorations { return c.win.deco }

// SetWindowTitle gives the UI the window's title, which a titlebar the app
// draws shows. The app sets it once; app.Run does, from Config.Title.
func (c *Context) SetWindowTitle(s string) { c.win.title = s }

// WindowTitle is the title SetWindowTitle gave.
func (c *Context) WindowTitle() string { return c.win.title }

// RequestWindowAction asks the window to close, minimise or maximise, and
// asks for a frame, in which the app carries it out (a titlebar's buttons
// call it). The last request of a frame wins.
func (c *Context) RequestWindowAction(a WindowAction) {
	c.win.action = a
	c.animating = true
}

// TakeWindowAction returns the window action requested since the last call and
// forgets it; the app calls it each frame.
func (c *Context) TakeWindowAction() WindowAction {
	a := c.win.action
	c.win.action = ActionNone
	return a
}

// ContentBox is a box of the last frame that shows something or takes input:
// it draws text or an image, or has a semantic role.
type ContentBox struct {
	// Key is the box's key.
	Key Key
	// Rect is its rectangle in pixels: x0, y0, x1, y1.
	Rect [4]float32
	// Role is its semantic role (RoleNone for a box that only draws).
	Role Role
	// Text and Image say that it draws text or an image.
	Text, Image bool
}

// ContentIn appends to dst the boxes of the last frame (call it after End) that
// show content or take input and whose rectangles overlap the rectangle r
// (x0, y0, x1, y1 in pixels), in build order. Boxes that only draw a
// background, a border or a shadow, the window drag band and the window's own
// node are not content. A test of a window that the OS draws over uses it to
// check that nothing is under the OS's buttons.
func (c *Context) ContentIn(r [4]float32, dst []ContentBox) []ContentBox {
	for _, id := range c.order {
		b := &c.boxes[id]
		if b.flags&WindowDrag != 0 || id == c.root || b.hid {
			continue
		}
		var role Role
		if rec := c.semOf(id); rec != nil {
			role = rec.role
		}
		text := b.flags&DrawText != 0 && b.txt.Len > 0
		image := b.flags&DrawImage != 0
		if !text && !image && role == RoleNone {
			continue
		}
		if b.rect[0] >= r[2] || b.rect[2] <= r[0] || b.rect[1] >= r[3] || b.rect[3] <= r[1] || b.rect[2] <= b.rect[0] || b.rect[3] <= b.rect[1] {
			continue
		}
		dst = append(dst, ContentBox{Key: b.key, Rect: b.rect, Role: role, Text: text, Image: image})
	}
	return dst
}
