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
// moves through. It is the list of the Composer (the hosted editor's handoff,
// N4 and D7b) and of Combobox.
//
// # The handoff
//
// The editor keeps the keyboard focus, and so it also keeps Up, Down, Tab,
// Enter and Escape. A native editor (an NSTextView) intercepts them in
// doCommandBySelector: and hands the app a CompletionCmd for each, which the
// app applies to the list's state with CompletionState.Command:
//
//	// each frame, while the app shows the list
//	switch v.comp.Command(cmd, len(items)) { // cmd: the host's command this frame, or CompletionNone
//	case rexui.ResultAccept:
//		v.insert(items[v.comp.Sel]) // Enter and Tab accept the highlighted row
//		v.closeList()
//	case rexui.ResultCancel:
//		v.closeList() // Escape; the text is untouched and the next Enter sends
//	}
//	sig := rexui.CompletionList(k, "Mentions", &v.comp, items, caretRect, rexui.CompletionOpts{Exclude: editorRect})
//	if sig.Chosen >= 0 { v.insert(items[sig.Chosen]); v.closeList() } // a click
//	if sig.Dismissed { v.closeList() }                                  // a press outside
//
// The Composer does all of this for its editor: it turns the hosted editor's
// keys into ui.FieldCommands (base.FieldCommandOf) and offers them to the
// list's commander, as it registers the commander with its drawn field, builds
// the list at the caret with Exclude set to the field, and reports the result
// (ComposerSignal.Completion). The rest of this comment is for an editor of
// your own.
//
// The Composer does all of this for its editor: it turns the hosted editor's
// keys into ui.FieldCommands (base.FieldCommandOf) and offers them to the
// list's commander, as it registers the commander with its drawn field, builds
// the list at the caret with Exclude set to the field, and reports the result
// (ComposerSignal.Completion). The rest of this comment is for an editor of
// your own.
//
// A drawn editor (a ui text field) does the same through ui's field commands:
// CompletionState.Attach registers the state as the commander of the next
// TextField, which offers it Up, Down, Tab, Enter and Escape as the keys or as
// the system's edit actions (macOS delivers the arrows as edit actions) and
// leaves to the text field what the list does not take; CompletionState.Take
// then says what happened:
//
//	v.comp.Attach(c, len(items))
//	f := c.TextField("compose", &v.buf)
//	switch v.comp.Take() { … } // as above
//
// While the list is shown, Up and Down (and Tab and Enter, when a row is
// highlighted) are the list's: the text does not move, nothing is sent.
//
// # Coordinates
//
// The caret rectangle and Exclude are in window pixels: x0, y0, x1, y1, the
// origin at the top left of the window's content, y down, in physical pixels
// (points times the backing scale: Kit.Scale), the units of ui.Context.Rect.
// A text field's caret is ui.Context.RichCaret or the rectangle of the field
// (ui.Context.IMERect); a native editor's is NSTextView's
// firstRectForCharacterRange converted to the window and multiplied by the
// backing scale. The list opens below it with its left edge at the caret, or
// above when there is no room below (CompletionOpts.Above prefers above), and
// stays inside the window. Widths in CompletionOpts and the look are points.
//
// Exclude is the editor's bounds in the same units. When it is set the list is
// placed outside it, against the edge of Exclude on the side that has room
// (below it, else above it) and with its left edge at the caret, so it never
// lies over a hosted native editor, which draws above the window's own content
// (N3b), and ui's host regions never hide it. Without Exclude the list sits
// against the caret rectangle itself, which is right for a drawn editor.
//
// # The caller's obligations
//
// The list takes no keyboard focus (it is a popup that never takes it: the
// focus stays where it is, a native editor's first responder included, so
// opening it does not move it). The caller must:
//
//   - close it (stop building it) when the editor loses the focus: on
//     platform.EvHostFocus away from the editor, when the window stops being
//     key, and when the caret leaves the word being completed;
//   - not open it, and ignore any command it holds, while the input method has
//     marked text (ui.Input.NPreedit > 0, NSTextView's hasMarkedText): the
//     input method owns Return, Escape, Backspace and the arrows then, and a
//     ui text field is not offered them (ui.FieldCommander);
//   - stop building it on Dismissed (a press outside, the press reaches what
//     it landed on) and on ResultCancel.
//
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
	// revealed is Sel+1 for the highlight the list was last scrolled to show: a
	// row is brought into view when the highlight changes, not on every
	// frame, so the wheel can scroll it away.
	revealed int
	rows     int              // how many rows the list showed in the last frame
	n        int              // the number of rows, for Attach
	pending  CompletionResult // what the last command did, for Take
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
			if s.Sel <= 0 { // from none, or from the first row, to the last
				s.Sel = n - 1
			} else {
				s.Sel--
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
	// Exclude is the editor's bounds in window pixels: the list is placed
	// outside it (see the coordinates above); the zero rectangle for none.
	Exclude [4]float32
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
	return completion(k, label, st, items, nil, caret, o, ui.DismissTransient)
}

// completion is CompletionList over items, or over strs when items is nil
// (Combobox's choices, which have only titles).
func completion(k *base.Kit, label string, st *CompletionState, items []CompletionItem, strs []string, caret [4]float32, o CompletionOpts, dismiss ui.Dismissal) CompletionSignal {
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

	// bring the highlight into view when it changes (the keyboard, the
	// pointer, a reset) or when the list holds fewer or more rows than it did
	// (the window or the editor moved: the highlight may be out of the rows
	// that remain); the wheel scrolls it away again and it stays away
	if st.rows != rows {
		st.rows, st.revealed = rows, 0
	}
	if st.Sel >= 0 && st.revealed != st.Sel+1 {
		if st.Sel < st.top {
			st.top = st.Sel
		}
		if st.Sel >= st.top+rows {
			st.top = st.Sel - rows + 1
		}
		st.revealed = st.Sel + 1
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
	anchor := caret
	if ex := o.Exclude; ex[2] > ex[0] && ex[3] > ex[1] { // outside the editor: against its edge, at the caret's x
		anchor = [4]float32{caret[0], ex[1], max(caret[2], caret[0]), ex[3]}
	}
	c.NextOverlay(ui.OverlayOpts{NoFocus: true, Dismiss: dismiss, AvoidHosts: true})
	c.NextSize(ui.Px(w), ui.Children(1))
	ov := c.BeginOverlay(okey, ui.OverlayPopup, anchor, place)
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

// completionCmd is a CompletionState as the field commander of a drawn editor.
type completionCmd CompletionState

// FieldCommand maps the editor's commands to the list's: Up, Down, Tab, Enter
// and Escape (see CompletionState.Attach).
func (s *completionCmd) FieldCommand(cmd ui.FieldCommand, _ rune) bool {
	var c CompletionCmd
	switch cmd {
	case ui.FieldMoveUp:
		c = CompletionUp
	case ui.FieldMoveDown:
		c = CompletionDown
	case ui.FieldInsertTab:
		c = CompletionTab
	case ui.FieldInsertNewline:
		c = CompletionEnter
	case ui.FieldCancel:
		c = CompletionEscape
	default:
		return false
	}
	st := (*CompletionState)(s)
	r := st.Command(c, st.n)
	if r == ResultNone {
		return false // an Enter or Tab with no row highlighted is the editor's own
	}
	st.pending = r
	return true
}

// Attach registers the list's state as the commander of the next ui TextField
// (the drawn editor), for a list of n rows that is showing: the field offers it
// Up, Down, Tab, Enter and Escape, in the order they arrive, as keys or as
// the system's edit actions, and the list takes the ones it has a use for.
// Take says what they did. Call it only while the list is shown, and not while
// the input method has marked text (the field offers nothing then anyway).
func (s *CompletionState) Attach(c *ui.Context, n int) {
	c.NextFieldCommander(s.commander(n), "")
}

// commander is the state as the ui.FieldCommander of a list of n rows: what
// Attach registers with a drawn text field, and what the Composer offers the
// hosted editor's keys (base.FieldCommandOf) to.
func (s *CompletionState) commander(n int) ui.FieldCommander {
	s.n = n
	return (*completionCmd)(s)
}

// Take returns what the commands since the last Take did: the result of the
// last command that did anything (ResultNone if none), and forgets it. Call
// it after the TextField Attach registered the state for.
func (s *CompletionState) Take() CompletionResult {
	r := s.pending
	s.pending = ResultNone
	return r
}
