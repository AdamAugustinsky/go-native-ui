package rexui

import (
	"gonativeui/icon"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// The completion popup list: the list of candidates a text box offers while it
// is being typed in (mentions, emoji, slash commands, a combo box's choices).
// It is a drawn popup below or above a caret rectangle with rows the keyboard
// moves through. It is the contract the composer's completion handoff
// (N4, D7b) is written against, and the list of Combobox.
//
// # The handoff
//
// The editor keeps the keyboard focus, and so it also keeps Up, Down, Tab,
// Enter and Escape: it intercepts them (a native text view in
// doCommandBySelector:, a drawn field by taking the keys before it builds) and
// hands the app a CompletionCmd for each, which the app applies to the
// list's state with CompletionState.Command:
//
//	// each frame, while the app shows the list
//	switch v.comp.Command(cmd, len(items)) { // cmd: the host's command this frame, or CompletionNone
//	case rexui.ResultAccept:
//		v.insert(items[v.comp.Sel]) // Enter and Tab accept the highlighted row
//		v.closeList()
//	case rexui.ResultCancel:
//		v.closeList() // Escape; the text is untouched and the next Enter sends
//	}
//	sig := rexui.CompletionList(k, "Mentions", &v.comp, items, caretRect, rexui.CompletionOpts{})
//	if sig.Chosen >= 0 { v.insert(items[sig.Chosen]); v.closeList() } // a click
//	if sig.Dismissed { v.closeList() }                                  // a press outside
//
// While the list is shown, Up and Down (and Tab and Enter) are the host's:
// the text does not move, nothing is sent. The caret rectangle is the editor's
// caret in window pixels (x0, y0, x1, y1: ui.Context.RichCaret, a text
// field's, or NSTextView's firstRectForCharacterRange converted to the
// window); the list opens below it with its left edge at the caret, or above
// when there is no room below, and stays inside the window.
//
// The list is an overlay: it draws above the window and takes presses, but not
// the keyboard focus: it asks for the focus to go back to CompletionOpts.Owner
// in the frame after it opens (the overlay machinery moves the focus into an
// overlay when it opens), so a drawn field keeps its caret and ring. A native
// editor that holds the real first responder sets no Owner. Nothing the list
// draws may lie over a hosted native view (N3b): place it clear of the
// editor's frame, as the caret rectangle's side that has room.

// CompletionItem is one candidate.
type CompletionItem struct {
	// Title is the text the row shows and the accessible name; Detail the
	// secondary text after it (a handle, a shortcut, a description).
	Title, Detail string
	// Icon is the glyph before the title, when HasIcon (icon.ID 0 is a real
	// icon).
	Icon    icon.ID
	HasIcon bool
}

// CompletionCmd is a command the editor hands over for the list, as the
// keys it intercepted.
type CompletionCmd uint8

const (
	// CompletionNone is no command this frame.
	CompletionNone CompletionCmd = iota
	// CompletionUp and CompletionDown move the highlight, wrapping at the
	// ends.
	CompletionUp
	CompletionDown
	// CompletionTab and CompletionEnter accept the highlighted row.
	CompletionTab
	CompletionEnter
	// CompletionEscape cancels.
	CompletionEscape
)

// CompletionResult is what a command did.
type CompletionResult uint8

const (
	// ResultNone: nothing happened (no command, or an accept with no row
	// highlighted).
	ResultNone CompletionResult = iota
	// ResultMoved: the highlight moved.
	ResultMoved
	// ResultAccept: the highlighted row (CompletionState.Sel) is accepted.
	ResultAccept
	// ResultCancel: the list is cancelled.
	ResultCancel
)

// CompletionState is the list's state, owned by the app: the zero value
// highlights the first row. Make a new one, or call Reset, each time the list
// opens or its items change.
type CompletionState struct {
	// Sel is the highlighted row, -1 for none.
	Sel int
	// none is set by Reset(…, none): the zero value of Sel is row 0
	started bool
	top     int     // the first row shown
	acc     float32 // scroll not yet worth a row, in pixels
	restore bool    // the focus is to go back to the owner at the next frame
}

// Reset starts the list over for n items: the first row highlighted, or none
// when n is 0 (or highlight is false).
func (s *CompletionState) Reset(n int, highlight bool) {
	*s = CompletionState{started: true, Sel: -1}
	if n > 0 && highlight {
		s.Sel = 0
	}
}

// sel is the highlight, with the zero value's meaning (the first row).
func (s *CompletionState) init(n int) {
	if !s.started {
		s.started = true
		s.Sel = 0
		if n == 0 {
			s.Sel = -1
		}
	}
	if s.Sel >= n {
		s.Sel = n - 1
	}
}

// Command applies the host's command to the list of n items and says what it
// did. Up and Down move the highlight (wrapping, and from none to the last
// or the first row), Tab and Enter accept a highlighted row, Escape cancels.
func (s *CompletionState) Command(cmd CompletionCmd, n int) CompletionResult {
	s.init(n)
	switch cmd {
	case CompletionUp:
		if n > 0 {
			s.Sel = (s.Sel - 1 + n) % n
			if s.Sel < 0 {
				s.Sel = n - 1
			}
			return ResultMoved
		}
	case CompletionDown:
		if n > 0 {
			s.Sel = (s.Sel + 1) % n
			return ResultMoved
		}
	case CompletionTab, CompletionEnter:
		if s.Sel >= 0 && s.Sel < n {
			return ResultAccept
		}
	case CompletionEscape:
		return ResultCancel
	}
	return ResultNone
}

// CompletionOpts are the list's options, passed by value.
type CompletionOpts struct {
	// Owner is the key of the box that must keep the keyboard focus (a drawn
	// field's: base.FieldKey); 0 for none, as when the editor is native.
	Owner ui.Key
	// MaxRows is how many rows show before the list scrolls (0: 8).
	MaxRows int
	// Width is the list's width in points; 0 fits the widest row, at least
	// CompletionLook.MinW.
	Width float32
	// Above makes the list prefer the space above the caret (a composer at
	// the bottom of the window), flipping below when there is none.
	Above bool
}

// CompletionRecipe is the look of the list: a menu's, drawn on the raised
// surface, with the emphasised selection of a key window.
type CompletionRecipe struct {
	// RowH is a row's height, Pad the panel's padding, RowPad a row's inset
	// from the panel's sides and Gap the space between a row's parts, all in
	// points; Radius is the panel's corner radius and RowRadius a row's;
	// MinW the narrowest the list is.
	RowH, Pad, RowPad, Gap, Radius, RowRadius, MinW float32
	// Selected is the highlighted row's fill while the window is key,
	// SelectedInactive in one that is not; Text and Detail colour an
	// ordinary row, OnSelected a highlighted one's (its detail too).
	Selected, SelectedInactive, Text, Detail, OnSelected Tint
}

// CompletionLook is the lists' recipe: a menu's metrics (24 pt rows, theme's
// MenuItemH) and radius.
var CompletionLook = CompletionRecipe{
	RowH: 24, Pad: 5, RowPad: 8, Gap: 8, Radius: 10, RowRadius: 6, MinW: 180,
	Selected: Of(theme.Selection), SelectedInactive: Of(theme.SelectionUnemph),
	Text: Of(theme.Label), Detail: Of(theme.Label2), OnSelected: Of(theme.OnSelection),
}

// CompletionSignal is what the list reports.
type CompletionSignal struct {
	// ID is the list's panel.
	ID ui.ID
	// Chosen is the row clicked this frame, -1 for none.
	Chosen int
	// Dismissed: a press outside the list, or Escape with the focus in it:
	// the app stops building it.
	Dismissed bool
}

// CompletionList builds the list of items below or above the caret rectangle
// (see the handoff above). The label is its accessible name. A move of the
// pointer over a row highlights it; a click chooses it.
func CompletionList(k *base.Kit, label string, st *CompletionState, items []CompletionItem, caret [4]float32, o CompletionOpts) CompletionSignal {
	return completion(k, label, st, items, nil, caret, o)
}

// completion is CompletionList over items, or over strs when items is nil
// (Combobox's choices, which have only titles).
func completion(k *base.Kit, label string, st *CompletionState, items []CompletionItem, strs []string, caret [4]float32, o CompletionOpts) CompletionSignal {
	c, r := k.Context(), k.Theme()
	rc := &CompletionLook
	sc := k.Scale()
	n := len(items)
	if items == nil {
		n = len(strs)
	}
	st.init(n)
	display, ident := ui.SplitLabel(label)
	okey := ui.HashString(ui.HashString(c.ParentKey(), "completion"), ident)
	sig := CompletionSignal{Chosen: -1}

	// the overlay opens and takes the focus; give it back to the owner in the frame after
	opening := !c.OverlayOpen(okey)
	if st.restore && o.Owner != 0 {
		c.SetFocus(o.Owner)
		st.restore = false
	}
	if opening {
		st.restore = true
		c.RequestFrame()
	}

	rows := o.MaxRows
	if rows <= 0 {
		rows = 8
	}
	rows = min(rows, n)
	rowH, pad, rowPad, gap := pt(sc, rc.RowH), pt(sc, rc.Pad), pt(sc, rc.RowPad), pt(sc, rc.Gap)
	// the widest row: icon, title, detail
	w := pt(sc, o.Width)
	if o.Width <= 0 {
		ts, ds := r.Styles[theme.StyleBody].Font, r.Styles[theme.StyleCallout].Font
		var widest float32
		iconW, _ := k.IconSize(icon.Check)
		for i := 0; i < n; i++ {
			rw := k.TextWidth(ts, completionTitle(items, strs, i)) + 2*rowPad
			if items != nil {
				if items[i].Detail != "" {
					rw += gap + k.TextWidth(ds, items[i].Detail)
				}
				if items[i].HasIcon {
					rw += iconW + gap
				}
			}
			widest = max(widest, rw)
		}
		w = max(theme.Snap(widest+2*pad), pt(sc, rc.MinW))
	}

	// keep the highlight in view; the wheel scrolls
	if st.Sel >= 0 {
		if st.Sel < st.top {
			st.top = st.Sel
		}
		if st.Sel >= st.top+rows {
			st.top = st.Sel - rows + 1
		}
	}
	prev := c.RectOf(ui.KeyN(okey, 1))
	if dy := c.Input().ScrollY; dy != 0 && c.Input().MouseY >= prev[1] && c.Input().MouseY < prev[3] && c.Input().MouseX >= prev[0] && c.Input().MouseX < prev[2] {
		st.acc -= dy
		for st.acc >= rowH {
			st.top++
			st.acc -= rowH
		}
		for st.acc <= -rowH {
			st.top--
			st.acc += rowH
		}
	}
	st.top = max(min(st.top, n-rows), 0)

	place := ui.PlaceBelow
	if o.Above {
		place = ui.PlaceAbove
	}
	c.NextSize(ui.Px(w), ui.Children(1))
	ov := c.BeginOverlay(okey, ui.OverlayPopup, caret, place)
	sig.Dismissed = ov.Dismissed

	// the panel
	el := &r.Elevations[theme.ElevRaised]
	sh := el.Shadows[0]
	c.NextBG(theme.Over(r.Colors[theme.Raised], r.Colors[theme.Window]|0xFF))
	c.NextBorder(r.Colors[el.EdgeRole], r.Lines.Hairline)
	c.NextRadius(pt(sc, rc.Radius))
	c.NextShadow(ui.Shadow{OffY: sh.Y, Sigma: sh.Blur, Color: sh.Color(r.Colors[theme.Shadow], r.T)})
	c.NextPad(pad)
	c.NextAxis(ui.AxisY)
	c.NextSize(ui.Px(w), ui.Children(1))
	c.NextRole(ui.RoleList)
	c.NextSemName(display)
	panel := c.BoxKeyed(ui.DrawBackground|ui.DrawBorder|ui.DrawShadow, ui.KeyN(okey, 1), nil)
	sig.ID = panel
	c.Push(panel)

	active := !k.Inactive()
	moved := k.MouseMoved()
	for j := 0; j < rows; j++ {
		i := st.top + j
		rkey := ui.KeyN(okey, uint64(100+i))
		selected := i == st.Sel
		fill, fg, fg2 := uint32(0), rc.Text.RGBA(r), rc.Detail.RGBA(r)
		if selected {
			fill = rc.SelectedInactive.RGBA(r)
			if active {
				fill = rc.Selected.RGBA(r)
				fg, fg2 = rc.OnSelected.RGBA(r), rc.OnSelected.RGBA(r)
			}
		}
		c.NextBG(fill)
		c.NextRadius(pt(sc, rc.RowRadius))
		c.NextAxis(ui.AxisX)
		c.NextCross(ui.CrossCenter)
		c.NextGap(gap)
		c.NextPadding(rowPad, 0, rowPad, 0)
		c.NextSize(ui.Pct(1), ui.Px(rowH))
		c.NextRole(ui.RoleListItem)
		c.NextSemName(completionTitle(items, strs, i))
		c.NextSemPos(i, n)
		var state ui.SemState
		if selected {
			state = ui.SemSelected
		}
		c.NextSemState(state)
		if items != nil {
			c.NextSemValueBytes(base.StrBytes(items[i].Detail))
		}
		row := c.BoxKeyed(ui.DrawBackground|ui.Clickable, rkey, nil)
		c.Push(row)
		if items != nil && items[i].HasIcon {
			k.Icon(items[i].Icon, fg)
		}
		c.NextSize(ui.TextSize(base.LabelSlack), ui.Px(rowH))
		c.NextFont(r.Styles[theme.StyleBody].Font)
		c.NextTextColor(fg)
		c.BoxKeyed(ui.DrawText|ui.FadeOverflowX, ui.KeyN(rkey, 1), base.StrBytes(completionTitle(items, strs, i)))
		if items != nil && items[i].Detail != "" {
			c.NextSize(ui.Pct(1), ui.Px(rowH))
			c.NextFont(r.Styles[theme.StyleCallout].Font)
			c.NextTextColor(fg2)
			c.NextAlign(ui.AlignRight)
			c.BoxKeyed(ui.DrawText|ui.FadeOverflowX, ui.KeyN(rkey, 2), base.StrBytes(items[i].Detail))
		}
		c.Pop()
		s := c.Signal(row)
		if s.Hovering && moved && st.Sel != i {
			st.Sel = i
		}
		if s.Clicked {
			sig.Chosen = i
		}
	}
	c.Pop()
	c.EndOverlay()
	return sig
}

// completionTitle is row i's title, from items or, when there are none, strs.
func completionTitle(items []CompletionItem, strs []string, i int) string {
	if items == nil {
		return strs[i]
	}
	return items[i].Title
}

// takeEditorKey takes the key code (with no modifier) from this frame's input
// before the text field that has the focus is built, so that the field does
// not see it, and reports whether there was one. A focused field claims the
// keys it handles at the start of the frame (ui's key claiming), so
// TakeKey cannot get Up, Down, Enter, Escape or Backspace from it; a component
// that gives them another meaning (a list's highlight, a token's selection)
// takes them here, which is what the completion handoff of a drawn editor does.
func takeEditorKey(c *ui.Context, code ui.KeyCode) bool {
	in := c.Input()
	for i := 0; i < in.NKeys; i++ {
		if e := &in.Keys[i]; e.Down && e.Code == code && e.Mods == 0 {
			e.Down = false
			return true
		}
	}
	return false
}
