package pilot

import (
	"gonativeui/ui"
)

// update applies the frame's input to the model before anything is built, so
// the frame that shows a click or a key is the frame it happens in. What the
// table takes itself (a click in the list, the arrow keys, Page Up and Down,
// Home and End, select all, Enter and a double click to open) it does while it
// is built; this is the rest. Geometry is the previous frame's, the layout the
// pointer was hit-tested against.
func (pl *Pilot) update(c *ui.Context) {
	in := c.Input()

	// Coming back to the window: look at both folders again. The watchers have
	// documented gaps (writes in the milliseconds before a watch registers,
	// in-place writes in very large folders, stale subfolder times, renamed
	// ancestors, network volumes), and this is when a person would notice.
	// Selection and scroll position are kept, as for any relist.
	if in.FocusGained {
		for i := range pl.panes {
			pl.Refresh(i, nil)
		}
	}

	// The pane the pointer goes down in has the keyboard.
	if in.Pressed[ui.ButtonLeft] {
		for i := range pl.panes {
			g := &pl.geo[i]
			if in.MouseX >= g.rect[0] && in.MouseX < g.rect[2] && in.MouseY >= g.rect[1] && in.MouseY < g.rect[3] {
				pl.setActive(c, i)
			}
		}
	}

	pl.keys(c, in)
	pl.syncFocus(c)
}

// setActive gives pane i the keyboard: the focus moves to its list, unless it is
// in one of its text fields. (A text field of the other pane that kept the focus
// would edit the same keys.)
func (pl *Pilot) setActive(c *ui.Context, i int) {
	if i == pl.active {
		return
	}
	pl.active = i
	pl.syncFocus(c)
}

// focusList gives the active pane's list the focus.
func (pl *Pilot) focusList(c *ui.Context) { c.SetFocus(listKey(pl.active)) }

// syncFocus keeps the keyboard focus where the active pane is: in its list or
// one of its fields. When it is nowhere (a click on nothing) or in the other
// pane, it moves to the active pane's list. A focus elsewhere (the divider) is
// left alone.
func (pl *Pilot) syncFocus(c *ui.Context) {
	a := pl.active
	fk := c.FocusKey()
	if fk == listKey(a) || fk == pathKey(a) || fk == filterKey(a) {
		return
	}
	if fk == 0 || fk == listKey(a^1) || fk == pathKey(a^1) || fk == filterKey(a^1) {
		pl.focusList(c)
	}
}

// isPrimary reports whether mods make a shortcut. On Windows AltGr arrives as
// Ctrl+Alt and types a character (AltGr+Q is "@" on some layouts), so that
// pair is not Ctrl.
func (pl *Pilot) isPrimary(m ui.Mods) bool {
	if m&pl.primary() == 0 {
		return false
	}
	return !(pl.win && m&ui.ModCtrl != 0 && m&ui.ModAlt != 0)
}

// primary is the modifier of shortcuts: ⌘ on macOS, Ctrl elsewhere.
func (pl *Pilot) primary() ui.Mods {
	if pl.mac {
		return ui.ModSuper
	}
	return ui.ModCtrl
}

// keys handles the frame's key events and typed text: those the table does not
// take.
func (pl *Pilot) keys(c *ui.Context, in *ui.Input) {
	for n := 0; n < in.NKeys; n++ {
		k := in.Keys[n]
		if !k.Down {
			continue
		}
		if pl.key(c, k) {
			c.ClaimKey(n) // Pilot's: ui's own handlers (Tab traversal, the list's Enter) leave it alone
		}
	}
	// Typing with the list focused starts the filter; so does an input method
	// composing (its keys never reach us as text).
	a := pl.panes[pl.active]
	fk := c.FocusKey()
	composing := pl.composing
	pl.composing = in.NPreedit > 0
	if (fk != 0 && fk != listKey(pl.active)) || !in.Focused {
		return
	}
	// Only a composition that begins now starts the filter. One that goes on
	// belongs to the field it started in; if that lost the focus (a click in
	// the list) it was committed there and is dropped, not taken up again.
	if (in.NPreedit > 0 && !composing) || pl.typed(in) {
		a.filterOpen = true
		c.SetFocus(filterKey(pl.active))
	}
}

// typed reports whether the frame's text is a start for the filter: printable,
// not a shortcut, not just a space.
func (pl *Pilot) typed(in *ui.Input) bool {
	if in.NText == 0 || pl.isPrimary(in.Mods) {
		return false
	}
	t := in.Text[:in.NText]
	if t[0] < 0x20 || t[0] == 0x7f || (t[0] == ' ' && !pl.panes[pl.active].filterOpen) {
		return false
	}
	return true
}

// key handles one key-down and reports whether it was Pilot's (the caller then
// claims it). The keys of the list itself (the arrows, Page Up and Down, Home
// and End, with Shift to extend; select all; Enter to open) are the table's
// while the list has the focus; from the filter field Pilot sends the same
// keys to the list.
func (pl *Pilot) key(c *ui.Context, k ui.KeyEvent) bool {
	prim := pl.isPrimary(k.Mods)
	shift := k.Mods&ui.ModShift != 0
	alt := k.Mods&ui.ModAlt != 0
	a := pl.panes[pl.active]
	fk := c.FocusKey()
	inPath := fk == pathKey(pl.active)
	inFilter := fk == filterKey(pl.active)
	inList := fk == 0 || fk == listKey(pl.active)
	if (inPath || inFilter) && c.Input().NPreedit > 0 {
		return true // an input method is composing in the field: the keys are its (as in the field itself)
	}

	// Shortcuts that work wherever the focus is.
	switch {
	case k.Code == ui.KeyTab && k.Mods&(ui.ModCtrl|ui.ModSuper|ui.ModAlt) == 0:
		// Tab switches panes from the list and from either text field (the field
		// is left, not given a tab character: its focus goes before it is built).
		// A path being edited is dropped like Esc drops it; a filter stays.
		if inPath {
			a.path.SetString(a.dir)
			a.pathErr = ""
			a.pathEdit = false
		}
		pl.active ^= 1
		pl.focusList(c)
		c.SetFocusVisible(true)
		return true
	case prim && k.Code == 'L' && !shift:
		a.pathEdit = true // the path field takes the path bar's place
		c.SetFocus(pathKey(pl.active))
		a.path.SelectAll()
		return true
	case prim && k.Code == 'F' && !shift && !alt: // the toolbar's search button
		a.filterOpen = true
		c.SetFocus(filterKey(pl.active))
		return true
	case pl.mac && prim && alt && k.Code == 'S', !pl.mac && prim && !alt && !shift && k.Code == 'B':
		pl.sidebar = !pl.sidebar // Hide Sidebar: Finder's ⌥⌘S; Ctrl+Alt is AltGr on Windows, so Ctrl+B elsewhere
		return true
	case pl.mac && prim && k.Code == ui.KeyLeftBracket:
		a.goBack()
		return true
	case pl.mac && prim && k.Code == ui.KeyRightBracket:
		a.goForward()
		return true
	case !pl.mac && alt && !inPath && k.Code == ui.KeyLeft:
		a.goBack()
		return true
	case !pl.mac && alt && !inPath && k.Code == ui.KeyRight:
		a.goForward()
		return true
	case pl.mac && prim && shift && k.Code == ui.KeyPeriod, !pl.mac && prim && k.Code == 'H':
		if !inPath {
			a.toggleHidden()
		}
		return true
	}
	if inPath {
		return true // the field edits; Enter and Esc come back through its signal
	}
	if !inList && !inFilter {
		return false // the divider or something else has the keyboard
	}

	ts := &a.v.ts
	last := a.v.nRows() - 1
	page := max(pl.geo[pl.active].visRows-1, 1)
	upMod, downMod := alt, alt // parent and open: ⌘↑/⌘↓ on macOS, Alt+↑ / Alt+↓ elsewhere
	if pl.mac {
		upMod, downMod = prim, prim
	}
	switch k.Code {
	case ui.KeyUp:
		switch {
		case upMod:
			a.goUp()
		case pl.mac && alt:
			ts.MoveTo(0, shift)
		case inFilter:
			ts.MoveBy(-1, shift)
		default:
			return false
		}
	case ui.KeyDown:
		switch {
		case downMod:
			a.openCursor()
		case pl.mac && alt:
			ts.MoveTo(last, shift)
		case inFilter:
			ts.MoveBy(1, shift)
		default:
			return false
		}
	case ui.KeyPageUp:
		if !inFilter {
			return false
		}
		ts.MoveBy(-page, shift)
	case ui.KeyPageDown:
		if !inFilter {
			return false
		}
		ts.MoveBy(page, shift)
	case ui.KeyHome:
		if !inFilter {
			return false
		}
		ts.MoveTo(0, shift)
	case ui.KeyEnd:
		if !inFilter {
			return false
		}
		ts.MoveTo(last, shift)
	case ui.KeyEnter:
		if !inFilter {
			return false // the list opens its cursor row (List.Activated)
		}
		a.openCursor()
	case ui.KeySpace:
		return inList // a space does nothing in the list (it would open the cursor row)
	case ui.KeyBackspace:
		if !inList {
			return false
		}
		a.goUp()
	case ui.KeyEscape:
		switch {
		case inFilter:
			a.closeFilter()
			pl.focusList(c)
		case a.filterOpen || len(a.q) > 0:
			a.closeFilter()
		default:
			a.v.ts.Sel.Clear()
		}
	default:
		return false
	}
	return true
}
