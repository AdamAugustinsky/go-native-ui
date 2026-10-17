package pilot

import (
	"gonativeui/fs"
	"gonativeui/ui"
)

// update applies the frame's input to the model before anything is built, so
// the frame that shows a click or a key is the frame it happens in. Geometry
// is the previous frame's, the layout the pointer was hit-tested against.
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
				pl.active = i
			}
		}
	}
	a := pl.panes[pl.active]

	// Titlebar buttons.
	switch {
	case c.PeekSignal(keyBack).Clicked:
		a.goBack()
	case c.PeekSignal(keyFwd).Clicked:
		a.goForward()
	case c.PeekSignal(keyUp).Clicked:
		a.goUp()
	}

	// The divider: the left pane follows the pointer.
	ds := c.PeekSignal(keyDiv)
	if ds.Pressed {
		pl.ratio0 = pl.ratio
	}
	if ds.Dragging || ds.Released {
		wa := pl.workArea()
		if avail := wa[2] - wa[0] - pl.Scale*gutterPt; avail > 0 {
			pl.ratio = min(max(pl.ratio0+ds.DragDelta[0]/avail, 0.05), 0.95)
		}
	}

	// Column headers and the list.
	for i, p := range pl.panes {
		for ci, key := range [3]fs.SortKey{fs.SortName, fs.SortSize, fs.SortModified} {
			if c.PeekSignal(hdrKey(i, ci)).Clicked {
				p.sortBy(key)
				pl.active = i
			}
		}
		if in.Pressed[ui.ButtonLeft] && c.HotKey() == listKey(i) {
			pl.click(c, i, in)
		}
	}

	pl.keys(c, in)
}

// primary is the modifier of shortcuts: ⌘ on macOS, Ctrl elsewhere.
func (pl *Pilot) primary() ui.Mods {
	if pl.mac {
		return ui.ModSuper
	}
	return ui.ModCtrl
}

// click applies a press in the list of pane i.
func (pl *Pilot) click(c *ui.Context, i int, in *ui.Input) {
	p := pl.panes[i]
	v := &p.v
	pl.active = i
	c.SetFocus(0)
	if v.l == nil {
		return
	}
	g := &pl.geo[i]
	row := int((float64(in.MouseY-g.list[1]) + g.scrollY) / float64(g.rowPx))
	if row < 0 || row >= v.nRows() {
		if row >= v.nRows() && in.Mods&(ui.ModShift|pl.primary()) == 0 {
			v.clearSel()
		}
		return
	}
	switch {
	case in.Clicks[ui.ButtonLeft] >= 2:
		p.openRow(row)
	case in.Mods&ui.ModShift != 0:
		v.selectRange(row)
	case in.Mods&pl.primary() != 0:
		v.toggle(v.entryAt(row), row)
	default:
		v.selectOnly(v.entryAt(row), row)
	}
}

// keys handles the frame's key events and typed text.
func (pl *Pilot) keys(c *ui.Context, in *ui.Input) {
	for n := 0; n < in.NKeys; n++ {
		k := in.Keys[n]
		if !k.Down {
			continue
		}
		pl.key(c, k)
	}
	// Typing with the list focused starts the filter; so does an input method
	// composing (its keys never reach us as text).
	a := pl.panes[pl.active]
	fk := c.FocusKey()
	if fk == pathKey(pl.active) || fk == filterKey(pl.active) || !in.Focused {
		return
	}
	if in.NPreedit > 0 || pl.typed(in) {
		a.filterOpen = true
		c.SetFocus(filterKey(pl.active))
	}
}

// typed reports whether the frame's text is a start for the filter: printable,
// not a shortcut, not just a space.
func (pl *Pilot) typed(in *ui.Input) bool {
	if in.NText == 0 || in.Mods&(pl.primary()|ui.ModAlt&0) != 0 {
		return false
	}
	t := in.Text[:in.NText]
	if t[0] < 0x20 || t[0] == 0x7f || (t[0] == ' ' && !pl.panes[pl.active].filterOpen) {
		return false
	}
	return true
}

// move puts the cursor of a on row r (extending the selection with shift) and scrolls it into view.
func (pl *Pilot) move(a *pane, shift bool, r int) {
	a.v.moveCursor(r, shift)
	a.reveal = a.v.curRow
}

func (pl *Pilot) key(c *ui.Context, k ui.KeyEvent) {
	prim := k.Mods&pl.primary() != 0
	shift := k.Mods&ui.ModShift != 0
	alt := k.Mods&ui.ModAlt != 0
	a := pl.panes[pl.active]
	fk := c.FocusKey()
	inPath := fk == pathKey(pl.active)
	inFilter := fk == filterKey(pl.active)
	list := !inPath && !inFilter

	// Shortcuts that work wherever the focus is.
	switch {
	case k.Code == ui.KeyTab && k.Mods&(ui.ModCtrl|ui.ModSuper|ui.ModAlt) == 0:
		// Tab switches panes from the list and from either text field (the field
		// is left, not given a tab character: its focus goes before it is built).
		// A path being edited is dropped like Esc drops it; a filter stays.
		if inPath {
			a.path.SetString(a.dir)
			a.pathErr = ""
		}
		pl.active ^= 1
		c.SetFocus(0)
		return
	case prim && k.Code == 'L' && !shift:
		c.SetFocus(pathKey(pl.active))
		a.path.SelectAll()
		return
	case pl.mac && prim && k.Code == ui.KeyLeftBracket:
		a.goBack()
		return
	case pl.mac && prim && k.Code == ui.KeyRightBracket:
		a.goForward()
		return
	case !pl.mac && alt && !inPath && k.Code == ui.KeyLeft:
		a.goBack()
		return
	case !pl.mac && alt && !inPath && k.Code == ui.KeyRight:
		a.goForward()
		return
	case pl.mac && prim && shift && k.Code == ui.KeyPeriod, !pl.mac && prim && k.Code == 'H':
		if !inPath {
			a.toggleHidden()
		}
		return
	}
	if inPath {
		return // the field edits; Enter and Esc come back through its signal
	}

	v := &a.v
	cur := v.curRow
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
			pl.move(a, shift, 0)
		default:
			pl.move(a, shift, max(cur-1, 0))
		}
	case ui.KeyDown:
		switch {
		case downMod:
			a.openCursor()
		case pl.mac && alt:
			pl.move(a, shift, v.nRows()-1)
		default:
			pl.move(a, shift, cur+1)
		}
	case ui.KeyPageUp:
		pl.move(a, shift, max(cur, 0)-page)
	case ui.KeyPageDown:
		pl.move(a, shift, max(cur, 0)+page)
	case ui.KeyHome:
		pl.move(a, shift, 0)
	case ui.KeyEnd:
		pl.move(a, shift, v.nRows()-1)
	case ui.KeyEnter:
		a.openCursor()
	case ui.KeyBackspace:
		if list {
			a.goUp()
		}
	case 'A':
		if list && prim {
			v.selectAll()
		}
	case ui.KeyEscape:
		switch {
		case inFilter:
			a.closeFilter()
			c.SetFocus(0)
		case a.filterOpen || len(a.q) > 0:
			a.closeFilter()
		default:
			v.clearSel()
		}
	}
}
