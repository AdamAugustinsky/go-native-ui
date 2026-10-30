package workspace

import (
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// Keys are the keys that move the focus between panes.
type Keys struct {
	// Dir: the arrow keys with exactly these modifiers move the focus to the
	// pane in their direction (FocusDir); 0 leaves the arrows alone. ⌘⌥
	// (ui.ModSuper|ui.ModAlt) is iTerm's and Rex's choice.
	Dir ui.Mods
	// Cycle: F6 moves the focus to the next pane and Shift+F6 to the one
	// before (FocusNext), as on Windows and in GTK.
	Cycle bool
	// PressAnywhere: a press of any button anywhere in a shown pane's
	// rectangle focuses it, whatever box it lands on, for an app whose
	// content covers the pane's box (a press on a button in the pane). A
	// press on an overlay drawn over the pane counts as a press in it.
	PressAnywhere bool
}

// Events is what Update did.
type Events struct {
	// Focus says the focus moved: a press in a pane, or a key.
	Focus bool
	// Moved says a divider moved under the pointer or the keys: the app
	// shows the panes where they are now, without animating them there.
	Moved bool
}

// Update applies the previous frame's input to the layout, before anything
// is built: a press in a pane's box (PaneKey, which must be clickable)
// focuses it; a divider's box (DividerKey) moves with a drag, and, while it
// has the keyboard focus, with the arrow keys along its axis (16 pt a step, 64
// pt with Shift), Home and End, and the accessibility increment and
// decrement; and keys moves the focus. The dividers do not move while a pane
// is maximised. It allocates nothing.
func (l *Layout) Update(c *ui.Context, keys Keys) Events {
	before := l.focus
	var ev Events
	in := c.Input()
	pressed := keys.PressAnywhere && in.Inside && (in.Pressed[ui.ButtonLeft] || in.Pressed[ui.ButtonRight] || in.Pressed[ui.ButtonMiddle])
	for i := 0; i < l.n; i++ {
		id := l.slots[l.order[i]].id
		if c.PeekSignal(l.PaneKey(id)).Pressed {
			l.SetFocus(id)
		} else if pressed && l.Shown(id) {
			if r := l.Rect(id); in.MouseX >= r[0] && in.MouseX < r[2] && in.MouseY >= r[1] && in.MouseY < r[3] {
				l.SetFocus(id)
			}
		}
	}
	if l.zoom == 0 {
		ev.Moved = l.moveDividers(c)
	}
	moved := false
	if keys.Dir != 0 {
		for _, k := range [...]struct {
			code ui.KeyCode
			d    Dir
		}{{ui.KeyLeft, Left}, {ui.KeyRight, Right}, {ui.KeyUp, Up}, {ui.KeyDown, Down}} {
			if c.TakeKey(k.code, keys.Dir) {
				moved = l.FocusDir(k.d) || moved
			}
		}
	}
	if keys.Cycle {
		if c.TakeKey(ui.KeyF6, 0) {
			moved = l.FocusNext(1) || moved
		}
		if c.TakeKey(ui.KeyF6, ui.ModShift) {
			moved = l.FocusNext(-1) || moved
		}
	}
	if moved {
		l.kbd = true
	}
	ev.Focus = l.focus != before
	return ev
}

// moveDividers moves the dividers with the pointer, the keyboard and the
// accessibility actions, as base.Splitter does.
func (l *Layout) moveDividers(c *ui.Context) bool {
	moved := false
	for i := 0; i < l.nd; i++ {
		nd := &l.nodes[l.divs[i]]
		if nd.avail <= 0 {
			continue
		}
		dk := l.DividerKey(nd.uid)
		ps := c.PeekSignal(dk)
		if ps.Pressed {
			nd.press = nd.ratio
		}
		if ps.Dragging || ps.Released {
			l.setFirst(nd, min(max(nd.press*nd.avail+ps.DragDelta[nd.axis], nd.lo), nd.hi))
			moved = true
		}
		step := float32(0)
		if ps.Action&ui.ActIncrement != 0 {
			step += 16 * l.m.Scale
		}
		if ps.Action&ui.ActDecrement != 0 {
			step -= 16 * l.m.Scale
		}
		if c.FocusKey() == dk {
			big := 16 * l.m.Scale
			if c.Input().Mods&ui.ModShift != 0 {
				big = 64 * l.m.Scale
			}
			fwd, back := ui.KeyRight, ui.KeyLeft
			if nd.axis == ui.AxisY {
				fwd, back = ui.KeyDown, ui.KeyUp
			}
			for _, m := range [...]ui.Mods{0, ui.ModShift} {
				switch {
				case c.TakeKey(fwd, m):
					step += big
				case c.TakeKey(back, m):
					step -= big
				}
			}
			switch {
			case c.TakeKey(ui.KeyHome, 0):
				step = -nd.avail
			case c.TakeKey(ui.KeyEnd, 0):
				step = nd.avail
			}
		}
		if step != 0 {
			l.setFirst(nd, min(max(nd.ratio*nd.avail+step, nd.lo), nd.hi))
			moved = true
			c.RequestFrame()
		}
	}
	return moved
}

// NextPane gives the next box built, pane id's (the box its surface builds,
// keyed PaneKey), its semantics: a group named name, selected while the pane
// has the focus. When the keys moved the focus to it (Update), name is
// announced, so that a screen reader says which pane is in use.
func (l *Layout) NextPane(c *ui.Context, id PaneID, name string) {
	c.NextRole(ui.RoleGroup)
	c.NextSemName(name)
	if id != l.focus {
		return
	}
	c.NextSemState(ui.SemSelected)
	if l.kbd {
		l.kbd = false
		c.AnnouncePolite(name)
	}
}

// NextDivider makes the next box built, the one the look builds for divider d
// with d.Key (a grab area over the gutter: theme.Chrome.Divider), a divider
// for the keyboard and the accessibility tree, as base.Kit.NextDivider does: a
// tab stop (unless DividersOffTab), and a ui.RoleSplitter with the first
// pane's share as its value.
func (l *Layout) NextDivider(c *ui.Context, d *Divider) {
	if !l.DividersOffTab {
		c.NextFlags(ui.Focusable | ui.FocusAnimation)
	}
	c.NextRole(ui.RoleSplitter)
	c.NextSemName(base.CurrentStrings().Divider)
	c.NextSemRange(0, 1, float64(d.Share), 0.01)
	c.NextSemPos(d.Index, l.nd)
}
