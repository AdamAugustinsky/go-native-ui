package rexui

import (
	"slices"

	"gonativeui/icon"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// The Sidebar is a source list as macOS 27.2 draws one
// (docs/design/liquid-glass-reference.md §2, §3, §7): flush, full-height
// glass; group headers of 11 pt bold; rows of 32 with an accent symbol and
// the title after it; the selection a rounded block inset 10 with radius 8,
// the accent with white semibold text while the sidebar has the keyboard in
// the key window, grey otherwise. The rows start below the toolbar and
// scroll under it.
//
// It builds on widget/base's scroll view and press primitives; the selection
// is drawn in one place (sidebarSelection) so that it can move to the list
// component's recipe once there is one.

// SidebarItem is one row of a sidebar, as the app describes it every frame.
// Items follow each other in display order; a row's children follow it with
// a greater Level.
type SidebarItem struct {
	// ID is the row's stable identity, unique and not 0: the selection and
	// the row's state follow it.
	ID uint32
	// Title is the row's text; Detail a second line below it, smaller and
	// lighter (a note's date and first line), which makes the title
	// semibold.
	Title, Detail string
	// Glyph is the row's symbol, drawn in Color (RGBA; 0: the accent, as
	// macOS 27 draws sidebar symbols).
	Glyph Glyph
	Color uint32
	// Tile draws the symbol white on a rounded square of this colour (RGBA;
	// 0: none), as System Settings' sidebar does: TileSize square, radius
	// TileRadius, in the place of a symbol. Give the tiles their symbols at
	// TileGlyphPt (SidebarTileIcon, or SidebarTileSymbol for SF Symbols).
	Tile uint32
	// Header makes the row a group header: small bold text, not selectable;
	// the rows up to the next header are its group.
	Header bool
	// Level is how deep the row is nested (0 for a row of a group).
	Level uint8
	// Open is the disclosure state of a row with children, or of a header
	// whose group can be hidden (nil: none). The sidebar toggles it.
	Open *bool
	// Height replaces the row's height, in points (0: SidebarOpts.RowH):
	// a row the app builds (SidebarOpts.Row) of its own height.
	Height float32
}

// SidebarState is the state of a sidebar, owned by the app.
type SidebarState struct {
	// Sel is the selected row's ID (0: none). With SidebarOpts.Multiple it is
	// the primary row among several selected: the one last chosen, which the
	// keys move from, and always one of the selected rows. An app that sets
	// it to a row outside them has that row alone selected; Select does the
	// same for any row.
	Sel uint32
	// Scroll is the scroll view's state.
	Scroll base.ScrollState

	anchor, anchSel uint32   // the row a Shift range extends from, and the Sel it was set for
	picked          []uint32 // the selected rows' IDs, ascending; empty: Sel alone

	vis    []int32         // the visible, selectable rows' indexes, for the keys
	find   base.TypeAhead  // type-to-find
	pend   [4]base.NavMove // moves a text field sent (Steer), for the next Sidebar
	npend  int
	enter  bool // Enter from the field (Steer)
	cmdr   sidebarCmd
	menuFn func(*[]ui.MenuItem) // buildMenu, made once
	menu   func(id uint32, items *[]ui.MenuItem)
	menuID uint32

	renaming uint32 // the row being renamed (Rename), 0 for none
	rename   ui.TextBuffer
	renameAt uint8 // 0 opened, 1 focus asked, 2 had the focus
	renamed  uint32
	row      SidebarRow // the row SidebarOpts.Row builds
}

// Rename starts renaming the row id in place: its title becomes a text field
// holding title, which takes the focus. Enter ends it with
// SidebarSignal.Renamed set (RenameText is the new name); Escape, or the
// field losing the focus, ends it without.
func (s *SidebarState) Rename(id uint32, title string) {
	s.renaming, s.renameAt = id, 0
	s.rename.SetString(title)
	s.rename.SetCaret(len(title))
}

// Renaming is the row being renamed, 0 for none.
func (s *SidebarState) Renaming() uint32 { return s.renaming }

// RenameText is the rename field's text.
func (s *SidebarState) RenameText() string { return s.rename.String() }

// Steer makes the next ui TextField (a search field over the sidebar) steer
// the sidebar's selection: Up, Down, Page Up and Page Down move it as the
// sidebar's own keys do, as keys or as macOS's edit actions, and Enter
// activates the selected row (SidebarSignal.Activated), while the field
// keeps the caret and the focus. The moves apply in the next Sidebar call:
// build the field before the sidebar, in the same frame.
func (s *SidebarState) Steer(c *ui.Context) {
	s.cmdr.st = s
	c.NextFieldCommander(&s.cmdr, "")
}

// sidebarCmd is a SidebarState as a text field's commander (Steer).
type sidebarCmd struct{ st *SidebarState }

// FieldCommand queues the field's moves for the sidebar.
func (s *sidebarCmd) FieldCommand(cmd ui.FieldCommand, _ rune) bool {
	st := s.st
	m := base.NavNone
	switch cmd {
	case ui.FieldMoveUp:
		m = base.NavUp
	case ui.FieldMoveDown:
		m = base.NavDown
	case ui.FieldPageUp:
		m = base.NavPageUp
	case ui.FieldPageDown:
		m = base.NavPageDown
	case ui.FieldInsertNewline:
		if st.Sel == 0 {
			return false
		}
		st.enter = true
		return true
	default:
		return false
	}
	if st.npend < len(st.pend) {
		st.pend[st.npend] = m
		st.npend++
	}
	return true
}

// buildMenu builds the menu of the row menuID (SidebarOpts.Menu).
func (s *SidebarState) buildMenu(items *[]ui.MenuItem) { s.menu(s.menuID, items) }

// Reserve makes room for n selected rows, so that selecting does not allocate.
func (s *SidebarState) Reserve(n int) {
	if cap(s.picked) < n {
		s.picked = append(make([]uint32, 0, n), s.picked...)
	}
}

// sync drops the selected rows when the app has set Sel to a row outside
// them (or to 0): Sel is the truth then.
func (s *SidebarState) sync() {
	if len(s.picked) == 0 {
		return
	}
	if _, ok := slices.BinarySearch(s.picked, s.Sel); !ok || s.Sel == 0 {
		s.picked = s.picked[:0]
	}
}

// Has reports whether the row id is selected.
func (s *SidebarState) Has(id uint32) bool {
	s.sync()
	if len(s.picked) == 0 {
		return id != 0 && id == s.Sel
	}
	_, ok := slices.BinarySearch(s.picked, id)
	return ok
}

// Count is the number of selected rows.
func (s *SidebarState) Count() int {
	s.sync()
	switch {
	case len(s.picked) > 0:
		return len(s.picked)
	case s.Sel != 0:
		return 1
	}
	return 0
}

// AppendSelected appends the selected rows' IDs, in increasing order, to dst.
func (s *SidebarState) AppendSelected(dst []uint32) []uint32 {
	s.sync()
	if len(s.picked) > 0 {
		return append(dst, s.picked...)
	}
	if s.Sel != 0 {
		dst = append(dst, s.Sel)
	}
	return dst
}

// Select selects the row id alone (0: none).
func (s *SidebarState) Select(id uint32) {
	s.picked = s.picked[:0]
	s.Sel = id
	s.anchor, s.anchSel = id, id
}

// Clear deselects every row.
func (s *SidebarState) Clear() { s.Select(0) }

// SelectAll selects every row a click could select: the rows of items that are
// not headers and not hidden under a closed row. It is the Select All command
// of a sidebar with Multiple; the sidebar takes the primary key chord itself
// while it has the keyboard, but an app with a menu command gets the command
// instead, and calls this.
func (s *SidebarState) SelectAll(items []SidebarItem) {
	s.sync()
	s.picked = s.picked[:0]
	w := sidebarWalk{hide: -1}
	for i := range items {
		if w.row(&items[i]) {
			s.picked = append(s.picked, items[i].ID)
		}
	}
	slices.Sort(s.picked)
	s.settle(s.anchorRow())
}

// settle ends a change of the selected rows: with one left it is Sel; Sel is
// one of them, else the last; anchor is where a Shift range will start.
func (s *SidebarState) settle(anchor uint32) {
	switch n := len(s.picked); {
	case n == 0:
		s.Sel = 0
	case n == 1:
		s.Sel = s.picked[0]
		s.picked = s.picked[:0]
	default:
		if _, ok := slices.BinarySearch(s.picked, s.Sel); !ok || s.Sel == 0 {
			s.Sel = s.picked[n-1]
		}
	}
	if anchor == 0 {
		anchor = s.Sel
	}
	s.anchor, s.anchSel = anchor, s.Sel
}

// anchorRow is the row a range extends from: where the last plain or ⌘-click,
// or the key, chose, else Sel (the app may have moved Sel since).
func (s *SidebarState) anchorRow() uint32 {
	if s.anchSel == s.Sel && s.anchor != 0 {
		return s.anchor
	}
	return s.Sel
}

// toggle flips the row id, as ⌘-click does.
func (s *SidebarState) toggle(id uint32) {
	was := s.Has(id)
	if len(s.picked) == 0 && s.Sel != 0 && !was {
		s.picked = append(s.picked, s.Sel)
	}
	if was {
		if len(s.picked) == 0 {
			s.Sel = 0
		} else if i, ok := slices.BinarySearch(s.picked, id); ok {
			s.picked = slices.Delete(s.picked, i, i+1)
		}
		s.settle(0)
		return
	}
	if len(s.picked) == 0 && s.Sel == 0 {
		s.Sel = id
	} else {
		i, _ := slices.BinarySearch(s.picked, id)
		s.picked = slices.Insert(s.picked, i, id)
		s.Sel = id
	}
	s.settle(id)
}

// press applies a press on the row id with the modifiers held, and reports
// whether the selection changed.
func (s *SidebarState) press(items []SidebarItem, id uint32, mods, primary ui.Mods) bool {
	switch {
	case mods&ui.ModShift != 0:
		s.selectRange(items, s.anchorRow(), id)
	case mods&primary != 0:
		s.toggle(id)
	default:
		if s.Count() == 1 && s.Sel == id {
			return false
		}
		s.Select(id)
	}
	return true
}

// selectRange selects the rows from a to b (IDs, in either order), as
// Shift-click does; the cursor, Sel, goes to b and the anchor stays at a.
func (s *SidebarState) selectRange(items []SidebarItem, a, b uint32) {
	ia, ib := -1, -1
	w := sidebarWalk{hide: -1}
	for i := range items {
		if !w.row(&items[i]) {
			continue
		}
		if items[i].ID == a {
			ia = i
		}
		if items[i].ID == b {
			ib = i
		}
	}
	if ia < 0 || ib < 0 {
		s.Select(b)
		return
	}
	lo, hi := min(ia, ib), max(ia, ib)
	s.picked = s.picked[:0]
	w = sidebarWalk{hide: -1}
	for i := 0; i <= hi; i++ {
		if w.row(&items[i]) && i >= lo {
			s.picked = append(s.picked, items[i].ID)
		}
	}
	slices.Sort(s.picked)
	s.Sel = b
	s.settle(a)
}

// prune drops selected rows that are no longer among the items.
func (s *SidebarState) prune(items []SidebarItem) {
	s.sync()
	if len(s.picked) == 0 {
		return
	}
	n := 0
	for i := range items {
		if !items[i].Header {
			if _, ok := slices.BinarySearch(s.picked, items[i].ID); ok {
				n++
			}
		}
	}
	if n == len(s.picked) {
		return
	}
	// (a row went: rare, so a search of the items for each selected row)
	s.picked = slices.DeleteFunc(s.picked, func(id uint32) bool {
		return !slices.ContainsFunc(items, func(it SidebarItem) bool { return it.ID == id && !it.Header })
	})
	s.settle(s.anchorRow())
}

// sidebarWalk goes through the items in order and says which rows are visible
// and selectable: not a header, and not under a closed row or a hidden group.
type sidebarWalk struct{ hide int } // hide: rows deeper than this level are hidden (-1: none; -2: to the next header)

// row reports whether it is such a row; call it for every item in order.
func (w *sidebarWalk) row(it *SidebarItem) bool {
	if it.Header {
		w.hide = -1
		if it.Open != nil && !*it.Open {
			w.hide = -2
		}
		return false
	}
	if w.hide == -2 || (w.hide >= 0 && int(it.Level) > w.hide) {
		return false
	}
	w.hide = -1
	if it.Open != nil && !*it.Open {
		w.hide = int(it.Level)
	}
	return true
}

// SidebarOpts are a sidebar's options, passed by value.
type SidebarOpts struct {
	// Top is the room above the rows, in pixels: the toolbar's height. The
	// sidebar's glass runs under the toolbar to the window's top; the rows
	// scroll below it (the capsules over the glass are below the window's
	// pixels when the glass is the system's).
	Top float32
	// Pad is extra room below Top, in points, where the rows do not scroll
	// either: a search field's place (SidebarSearch).
	Pad float32
	// RowH and HeaderH replace the rows' and the headers' heights, in points
	// (0: the medium sidebar's 32 and 19); BodyHeaders sets the headers in
	// body text, as a list of notes does.
	RowH, HeaderH float32
	BodyHeaders   bool
	// Multiple allows several rows to be selected, as in Notes and Finder's
	// sidebar: ⌘-click (the platform's primary modifier) toggles a row,
	// Shift-click selects the rows from the last chosen to the one clicked,
	// Shift with Up or Down extends the selection and the primary modifier
	// with A selects every row. SidebarState says which rows are selected;
	// Sel is the one last chosen.
	Multiple bool
	// Row builds a row's content in place of the symbol and the title: in
	// the row's box (pushed, its children flowing left to right, centred,
	// after the indentation and the disclosure), over the selection the
	// sidebar draws. Give a function made once (a method value kept in the
	// app's state), so that a frame allocates nothing. Headers stay the
	// sidebar's.
	Row func(k *base.Kit, it *SidebarItem, row *SidebarRow)
	// Menu builds the context menu of the row id (rexui.ContextMenu: a
	// right-click, Shift+F10 or the accessibility action); the choice is
	// SidebarSignal.MenuRow and MenuItem. Give a function made once.
	Menu func(id uint32, items *[]ui.MenuItem)
}

// SidebarRow is a row a SidebarOpts.Row function builds: its box and state.
type SidebarRow struct {
	// ID and Key are the row's box.
	ID  ui.ID
	Key ui.Key
	// Selected: the row is selected; Emphasised: the selection is the accent
	// (the sidebar has the keyboard in the key window).
	Selected, Emphasised bool
	// Text and Text2 are the colours of its text and secondary text in its
	// state (white on the accent).
	Text, Text2 uint32
	// Renaming: the row is being renamed (SidebarState.Rename): build
	// RenameField where its title goes.
	Renaming bool
	// W and H are its size in pixels.
	W, H float32

	k    *base.Kit
	st   *SidebarState
	root ui.Key
}

// RenameField builds the inline rename field of a row being renamed, as wide
// as what is left of the row (Grow), where the row's title goes.
func (row *SidebarRow) RenameField() {
	if row.Renaming {
		sidebarRename(row.k, row.st, row.root, ui.Grow(1))
	}
}

// SidebarSignal is what a sidebar reports for a frame.
type SidebarSignal struct {
	base.Signal
	// Under is set while rows are scrolled under the toolbar: the toolbar
	// shows its scroll edge over the sidebar.
	Under bool
	// Toggled is the row whose disclosure changed (0: none).
	Toggled uint32
	// Activated is the row Enter (from the sidebar, or a field that steers
	// it) or a double click opened (0: none).
	Activated uint32
	// MenuRow and MenuItem are the row and the item chosen in a context menu
	// (SidebarOpts.Menu), when MenuChosen.
	MenuRow    uint32
	MenuItem   int
	MenuChosen bool
	// Renamed is the row renamed in place (SidebarState.Rename) and confirmed
	// with Enter (0: none); SidebarState.RenameText is its new name.
	Renamed uint32
}

// SidebarRecipe is the sidebar's look. Lengths are in points.
type SidebarRecipe struct {
	// Text, TextInactive colour a row's title; Header, HeaderInactive a
	// group header's.
	Text, TextInactive, Header, HeaderInactive Tint
	// Sel, SelUnemph and SelInactive fill the selection: emphasised (the
	// sidebar has the keyboard in the key window), not, and in an inactive
	// window; OnSel is the emphasised selection's text and symbol, and
	// OnSelInactive the selected row's text in an inactive window (measured
	// #676767 and #999999: darker in light, lighter in dark than the other
	// rows').
	Sel, SelUnemph, SelInactive, OnSel, OnSelInactive Tint
	// Symbol colours symbols that set no Color (the accent).
	Symbol Tint
	// SymbolGap is the space between the symbol and the title's text field:
	// the title starts after the symbol's own width (AppKit's image views
	// are the symbols' sizes, 18–26 wide, so the fields start at 41–47.5); a
	// symbol narrower than DotW (a tag's dot) sits centred in the 20-pt
	// image view (field at 43). PlainTextX is the field's start in a row
	// without a symbol. FieldInset is how far into its field AppKit draws
	// the text (NSTextField's 2): the ink lies there, after the field's x
	// (theme.SidebarSpec.HeaderX too).
	SymbolGap, DotW, PlainTextX, FieldInset float32
	// DisclosureRoom is how far right every row's content moves once any
	// row has children (NSOutlineView keeps a column for the disclosures
	// then: the cells at 25 rather than 16), DisclosureX a row's disclosure
	// box (DisclosureLook.Box, 13 wide, the row's height) from the row's
	// left at level 0, Indent more per level: measured on the Mac mini's
	// 27.2 with Finder's Documents two levels deep (tools/appkitscenes
	// -nested): the box at 12 (Documents) and 25 (its child), the chevron
	// centred in it, the symbols at 28 and 41; the chevron DisclosureDY
	// below the row's middle (the ink's middle 0.75 below it closed, 1.5
	// open; ours 1 higher centred). RowChevron colours a row's
	// disclosure (secondaryLabelColor: #6D6D6D over #EDEDED, #A2A3A2 over
	// #262626), but on the accent selection, where it is the text's.
	DisclosureRoom, DisclosureX, DisclosureDY float32
	RowChevron                                Tint
	// Detail colours the second line over the glass and the grey
	// selections (the vibrant secondary label: black at 43 %, white at 36 %,
	// measured #868686 on #ECECEC and #747474 on #262626); DetailOnSel is
	// OnSel's alpha for it on the accent (measured 0.7).
	Detail      Tint
	DetailOnSel float32
	// DetailTop is a two-line row's title from the row's top (Notes' 8; the
	// second line 2 below the title's line).
	DetailTop float32
	// HeaderChevronRight is a header's disclosure from the trailing edge
	// (centre), shown under the pointer; Chevron its colour.
	HeaderChevronRight float32
	Chevron            Tint
	// Thumb is the scroll bar's thumb, ThumbW its width.
	Thumb  Tint
	ThumbW float32
	// LegacyKnob and LegacySlot colour a legacy scroll bar (the system's
	// scroller style; the rows leave room for it): an opaque knob in a
	// slot as wide, 3 pt clear of the bar's sides and ends (measured on the
	// Mac mini: #A5A5A5 in #D8D8D8, #5A5A5A in #343434), and the same in an
	// inactive window (#ABABAB in #DEDEDE, #585858 in #323232).
	LegacyKnob, LegacyKnobInactive, LegacySlot, LegacySlotInactive Pair
	// SearchFill and SearchFillInactive fill a search field on the
	// sidebar's glass (SidebarSearch): a capsule of the field's height,
	// measured #EAEAEA and #4E4E4E, #E5E5E5 and #464646 inactive (§2, the
	// MacBook's 26B5091g); SearchFills replaces them when set.
	SearchFill, SearchFillInactive Tint
	// SearchRim is the bright rim inside its edge in an active window
	// (#FFFFFF 1 pt and #727272 0.5 pt wide) and SearchGlow the colour its
	// fill fades from at the top and the bottom (#F2F2F2, #5F5F5F on the
	// Mac mini: 1 pt in #F1F1F1 and #606060; #F4F4F4, #565656 on the
	// MacBook), SearchEdge and
	// SearchEdgeInactive the hairline just outside it (#D8D8D8 and #8C8C8C;
	// #CDCDCD and #181818 inactive).
	SearchRim, SearchGlow, SearchEdge, SearchEdgeInactive Pair
	// TextEnd is how far before the sidebar's trailing edge a row's text
	// ends (AppKit's cell view ends 16 short, its text field 4 more, the
	// text 2 inside the field; a two-line row's 14, 6 and 2): longer text
	// ends there with an ellipsis.
	TextEnd float32
	// TileSize, TileRadius and TileGlyphPt are a tile's square, its corner
	// and its symbol's size (SidebarItem.Tile; measured 20, 5 and 11);
	// TileGlyph colours the symbol; TileInactive is the tile's opacity in an
	// inactive window (measured 0.5: #0070F4 shows as #79B1F4 on #F2F2F2).
	TileSize, TileRadius, TileGlyphPt, TileInactive float32
	TileGlyph                                       Tint
	// SearchFills are the search field's fill, active and inactive, as the
	// Mac mini's 27.2 (26B5101f) draws it, the target: #E9E9E9 / #565656
	// and #E3E3E3 / #4D4D4D (the MacBook's in SearchFill's roles). Zero:
	// SearchFill and SearchFillInactive.
	SearchFills [2]Pair
}

// SidebarLook is the sidebar's recipe (the colours are theme roles measured
// on 27.2: SidebarFill, SidebarSel …).
var SidebarLook = SidebarRecipe{
	Text: Of(theme.SidebarLabel), TextInactive: Of(theme.SidebarLabelInactive),
	Header: Of(theme.SidebarHeader), HeaderInactive: Of(theme.SidebarHeaderInactive),
	Sel: Of(theme.SidebarSel), SelUnemph: Of(theme.SidebarSelUnemph), SelInactive: Of(theme.SidebarSelInactive), OnSel: Of(theme.OnAccent),
	OnSelInactive: Of(theme.SelectedRowTextInactive),
	Symbol:        Of(theme.Accent),
	SymbolGap:     4, DotW: 12, PlainTextX: 20, FieldInset: 2, DisclosureRoom: 9, DisclosureX: 12, DisclosureDY: 1, RowChevron: Of(theme.Label2),
	Detail: Of(theme.SidebarDetail), DetailOnSel: 0.7, DetailTop: 8, HeaderChevronRight: 17.5, Chevron: Of(theme.Label3),
	Thumb: OfAlpha(theme.Label, 0.25, 0.3), ThumbW: 6,
	LegacyKnob: Solid(0xA5A5A5, 0x5A5A5A), LegacyKnobInactive: Solid(0xABABAB, 0x585858),
	LegacySlot: Solid(0xD8D8D8, 0x343434), LegacySlotInactive: Solid(0xDEDEDE, 0x323232),
	SearchFill: Of(theme.SidebarSearchFill), SearchFillInactive: Of(theme.SidebarSearchFillInactive),
	SearchFills: [2]Pair{Solid(0xE9E9E9, 0x565656), Solid(0xE3E3E3, 0x4D4D4D)},
	SearchRim:   Solid(0xFFFFFF, 0x727272), SearchGlow: Solid(0xF2F2F2, 0x5F5F5F), SearchEdge: Solid(0xD8D8D8, 0x8C8C8C), SearchEdgeInactive: Solid(0xCDCDCD, 0x181818),
	TextEnd:  22,
	TileSize: 20, TileRadius: 5, TileGlyphPt: 11, TileInactive: 0.5, TileGlyph: Of(theme.OnAccent),
}

// SidebarTileIcon is the icon spec of a tile's Lucide icon
// (SidebarItem.Tile): the icon named at the tile's glyph size, for
// Kit.AddIcon.
func SidebarTileIcon(name string, fallback rune) icon.Spec {
	return icon.Spec{Lucide: name, Fallback: fallback, Pt: SidebarLook.TileGlyphPt}
}

// SidebarTileSymbol is the icon spec of a tile's SF Symbol (SidebarItem.Tile):
// the symbol named at the tile's size and weight, for an app that draws the
// system's symbols; SidebarTileIcon draws Lucide's.
func SidebarTileSymbol(symbol string, fallback rune) icon.Spec {
	return icon.Spec{Symbol: symbol, Fallback: fallback, Pt: SidebarLook.TileGlyphPt, Weight: 0.23}
}

// Sidebar builds a sidebar of items in the current parent (it takes the
// parent's size unless the kit's NextSize says otherwise) that edits st.Sel.
// A press selects a row; with the keyboard (the sidebar takes the focus when
// pressed) Up and Down move the selection, Left and Right close and open a
// row's children. Changed is set in the frame the selection changed. With
// SidebarOpts.Multiple a press with a modifier edits a set of rows instead
// (SidebarState.Has, Count and AppendSelected read it).
//
//	s := rexui.Sidebar(k, "Sidebar", &v.side, v.places, rexui.SidebarOpts{Top: tb.Height()})
//	if s.Changed { v.open(v.side.Sel) }
func Sidebar(k *base.Kit, label string, st *SidebarState, items []SidebarItem, o SidebarOpts) SidebarSignal {
	c, r := k.Context(), k.Theme()
	rc := &SidebarLook
	sp := &r.Window.Sidebar
	_, ident := ui.SplitLabel(label)
	key := ui.HashString(c.ParentKey(), ident)
	var res SidebarSignal

	// the glass, full height, and the box that takes the keyboard
	// (the rows scroll in the room below Top: the band above them is the
	// window's titlebar, where the toolbar's capsules are, which a hosted
	// glass shows below the window's pixels)
	k.ApplySize(ui.Pct(1), ui.Pct(1))
	c.NextBG(GlassFill(k, GlassSidebar))
	c.NextRole(ui.RoleList)
	c.NextSemName(label)
	c.NextPadding(0, o.Top+theme.Snap(k.Pt(o.Pad)), 0, 0)
	flags := ui.DrawBackground | ui.Clip | ui.Focusable | ui.FocusList
	if k.Disabled() {
		flags = ui.DrawBackground | ui.Clip | ui.Disabled
	}
	root := c.BoxKeyed(flags, key, nil)
	GlassRegion(k, root, GlassSidebar, 0)
	c.Push(root)
	focused := c.Focused(root) && !k.Disabled()
	emph := focused && !k.Inactive()

	// the keyboard, before the rows show the selection
	var changed, keyed bool
	if o.Multiple {
		st.prune(items)
	} else if len(st.picked) > 0 {
		st.picked = st.picked[:0] // (from a time it had Multiple)
	}
	if o.Menu != nil {
		st.menu = o.Menu
		if st.menuFn == nil {
			st.menuFn = st.buildMenu
		}
	}
	st.visible(items)
	page := 8
	if rh := theme.Snap(sp.RowH); rh > 0 {
		rr := c.Rect(root)
		page = max(int((rr[3]-rr[1]-o.Top-theme.Snap(k.Pt(o.Pad)))/rh)-1, 1)
	}
	for i := range st.npend { // a field's moves (Steer)
		if sidebarMove(st, items, st.pend[i], false, page) {
			changed = true
		}
	}
	st.npend = 0
	if st.enter {
		st.enter = false
		res.Activated = st.Sel
	}
	if focused {
		var ch bool
		ch, res.Toggled, res.Activated = sidebarKeys(c, st, items, o.Multiple, page, res.Activated)
		changed = changed || ch
	}
	keyed = changed

	k.NextSize(ui.Pct(1), ui.Pct(1))
	v := k.BeginScroll("rows", &st.Scroll, base.ScrollVertical)
	_, sy := c.Scroll(v.Inner)
	res.Under = sy > 0.5

	// a row with children anywhere moves every row's content right, for
	// the disclosures' column
	room := float32(0)
	for i := range items {
		if !items[i].Header && items[i].Open != nil {
			room = k.Pt(rc.DisclosureRoom)
			break
		}
	}

	headers := 0
	y := float32(0) // the content's height so far: where the next row starts in it
	hide := -1      // rows deeper than this level are hidden (a closed row's children); -2 to the next header
	for i := range items {
		it := &items[i]
		if it.Header {
			hide = -1
		} else if hide == -2 || (hide >= 0 && int(it.Level) > hide) {
			continue
		} else {
			hide = -1
		}
		if it.Header {
			if headers > 0 {
				c.NextSize(ui.Pct(1), ui.Px(theme.Snap(sp.GroupGap)))
				c.BoxKeyed(0, ui.KeyN(key, 2<<32|uint64(it.ID)), nil)
				y += theme.Snap(sp.GroupGap)
			}
			headers++
			hh, t := sidebarHeader(k, key, it, rc, &o)
			if t {
				res.Toggled = it.ID
			}
			y += hh
			if it.Open != nil && !*it.Open {
				hide = -2
			}
			continue
		}
		pressed, toggled, rh := sidebarRow(k, key, it, st, emph, rc, &o, room, &res)
		switch {
		case pressed && o.Multiple:
			if st.press(items, it.ID, c.Input().Mods, ui.PrimaryMod()) {
				changed = true
				c.RequestFrame() // the rows built before this one show the new selection
			}
		case pressed && st.Sel != it.ID:
			st.Select(it.ID)
			changed = true
		}
		if keyed && st.Sel == it.ID { // a row the keyboard selected comes into view
			c.ScrollIntoView(v.Inner, [4]float32{0, y, 1, y + rh})
		}
		y += rh
		if toggled {
			res.Toggled = it.ID
		}
		if it.Open != nil && !*it.Open {
			hide = int(it.Level)
		}
	}
	k.CloseScrollContent(&v)
	sidebarThumb(k, &v, rc)
	k.CloseScroll(&v)
	c.Pop()
	res.Signal = k.SignalOf(root)
	res.Renamed, st.renamed = st.renamed, 0
	res.Focused = focused
	res.Changed = changed
	return res
}

// SidebarSearch builds the search field a sidebar can have above its rows
// (System Settings'): a filled capsule on the glass, as wide as its parent
// unless the kit's NextWidth says otherwise and the sidebar search's height
// (24), holding a borderless search field. Place it SearchInset from the
// sidebar's sides, 4 below the toolbar, and give the sidebar a Pad of 34.
//
//	rexui.SidebarSearch(k, "Search Settings", &v.query, "Search")
func SidebarSearch(k *base.Kit, label string, buf *ui.TextBuffer, placeholder string) base.FieldSignal {
	c, r := k.Context(), k.Theme()
	rc := &SidebarLook
	h := theme.Snap(r.Window.Sidebar.SearchH)
	fill, edge := rc.SearchFill.RGBA(r), rc.SearchEdge.At(r.T)
	if k.Inactive() {
		fill, edge = rc.SearchFillInactive.RGBA(r), rc.SearchEdgeInactive.At(r.T)
	}
	i := 0
	if k.Inactive() {
		i = 1
	}
	if rc.SearchFills[i] != (Pair{}) {
		fill = rc.SearchFills[i].At(r.T)
	}
	k.ApplySize(ui.Pct(1), ui.Px(h))
	c.NextBG(fill)
	c.NextRadius(h / 2)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	_, ident := ui.SplitLabel(label)
	capKey := ui.KeyN(ui.HashString(c.ParentKey(), ident), 0xCA95)
	cp := c.BoxKeyed(ui.DrawBackground, capKey, nil)
	c.Push(cp)
	rr := c.Rect(cp)
	w := rr[2] - rr[0]
	lw := r.Lines.Hairline
	if !k.Inactive() {
		// the bright rim at the edge, then a glow that fades into the fill
		// towards the middle (measured: #FFFFFF for 1 pt, #F4F4F4 1.5 pt in,
		// the fill 7 pt in; #727272 for 0.5 pt, #565656, the fill)
		glow := rc.SearchGlow.At(r.T)
		half := theme.Snap(h / 2)
		theme.At(c, 0, 0, w, half)
		c.NextBG4(glow, glow, fill, fill)
		c.NextRadii(h/2, h/2, 0, 0)
		c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(capKey, 1), nil)
		theme.At(c, 0, half, w, h-half)
		c.NextBG4(fill, fill, glow, glow)
		c.NextRadii(0, 0, h/2, h/2)
		c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(capKey, 2), nil)
		theme.At(c, 0, 0, w, h)
		c.NextBorder(rc.SearchRim.At(r.T), theme.Snap(k.Pt(1-0.5*r.T)))
		c.NextRadius(h / 2)
		c.BoxKeyed(ui.DrawBorder|base.Floating, ui.KeyN(capKey, 5), nil)
	}
	// the edge, a hairline just outside the capsule
	theme.At(c, -lw, -lw, w+2*lw, h+2*lw)
	c.NextBorder(edge, lw)
	c.NextRadius(h/2 + lw)
	c.BoxKeyed(ui.DrawBorder|base.Floating, ui.KeyN(capKey, 3), nil)
	// the field over them (floating boxes draw in the order they are built)
	theme.At(c, 0, 0, w, h)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.Push(c.BoxKeyed(base.Floating, ui.KeyN(capKey, 4), nil))
	res := SearchField(k, label, buf, placeholder, FieldOpts{Variant: FieldBorderless})
	c.Pop()
	c.Pop()
	return res
}

// sidebarRow builds one row; it reports a press on it and a click on its
// disclosure.
func sidebarRow(k *base.Kit, key ui.Key, it *SidebarItem, st *SidebarState, emph bool, rc *SidebarRecipe, o *SidebarOpts, room float32, res *SidebarSignal) (pressed, toggled bool, h float32) {
	c, r := k.Context(), k.Theme()
	sp := &r.Window.Sidebar
	sel := st.Has(it.ID)
	rkey := ui.KeyN(key, 1<<32|uint64(it.ID))
	ps := c.PeekSignal(rkey)
	pressed = (ps.Pressed || ps.Action&ui.ActPress != 0) && !k.Disabled()
	if pressed {
		sel = true
	}
	if ps.DoubleClicked && !k.Disabled() && st.renaming != it.ID {
		res.Activated = it.ID
	}
	h = theme.Snap(sp.RowH)
	if o.RowH > 0 {
		h = theme.Snap(k.Pt(o.RowH))
	}
	if it.Height > 0 {
		h = theme.Snap(k.Pt(it.Height))
	}
	var state ui.SemState
	if sel {
		state = ui.SemSelected
	}
	if it.Open != nil && *it.Open {
		state |= ui.SemExpanded
	}
	c.NextSize(ui.Pct(1), ui.Px(h))
	c.NextRole(ui.RoleListItem)
	c.NextSemName(it.Title)
	c.NextSemState(state)
	flags := ui.Clickable
	if k.Disabled() {
		flags = ui.Disabled
	}
	row := c.BoxKeyed(flags, rkey, nil)
	if o.Menu != nil {
		st.menuID = it.ID
		if item, ok := ContextMenu(k, row, st.menuFn); ok {
			res.MenuRow, res.MenuItem, res.MenuChosen = it.ID, item, true
		}
	}
	if rr := c.Rect(row); rr[3] > rr[1] && c.ShownRect(row) == [4]float32{} && st.renaming != it.ID {
		return pressed, false, h
	}
	c.Push(row)
	rr := c.Rect(row)
	w := rr[2] - rr[0]

	textCol, symCol := rc.Text.RGBA(r), it.Color
	if symCol == 0 {
		symCol = rc.Symbol.RGBA(r)
	}
	if k.Inactive() {
		textCol = rc.TextInactive.RGBA(r)
		symCol = desaturate(symCol)
	}
	if sel {
		sidebarSelection(k, rkey, w, h, emph)
		switch {
		case emph:
			textCol, symCol = rc.OnSel.RGBA(r), rc.OnSel.RGBA(r)
		case k.Inactive():
			textCol = rc.OnSelInactive.RGBA(r)
		}
	}

	indent := float32(it.Level) * sp.Indent
	if it.Open != nil {
		// in the disclosures' column, left of the row's content
		col := rc.RowChevron.RGBA(r)
		if sel && emph {
			col = textCol
		}
		toggled = sidebarChevron(k, rkey, key, it.Open, theme.Snap(k.Pt(rc.DisclosureX)+indent), k.Pt(rc.DisclosureDY), h, col, false)
	}
	indent += room
	if o.Row != nil {
		cr := &st.row // (state's: a row passed to the app's function would escape)
		*cr = SidebarRow{ID: row, Key: rkey, Selected: sel, Emphasised: sel && emph, Text: textCol, Text2: rc.Detail.RGBA(r),
			Renaming: st.renaming == it.ID, W: w, H: h, k: k, st: st, root: key}
		if sel && emph {
			cr.Text2 = theme.WithAlpha(rc.OnSel.RGBA(r), rc.DetailOnSel)
		}
		// the content's box, after the indentation and the disclosure
		x := theme.Snap(sp.IconX + indent)
		theme.At(c, x, 0, max(w-x-theme.Snap(k.Pt(rc.TextEnd))+theme.Snap(k.Pt(rc.FieldInset)), 0), h)
		c.NextAxis(ui.AxisX)
		c.NextCross(ui.CrossCenter)
		c.NextGap(k.Pt(rc.SymbolGap))
		c.Push(c.BoxKeyed(ui.Clip|base.Floating, ui.KeyN(rkey, 6), nil))
		o.Row(k, it, cr)
		c.Pop()
		c.Pop()
		return pressed, toggled, h
	}
	textX := k.Pt(rc.PlainTextX+rc.FieldInset) + indent
	if id, ok := it.Glyph.ID(); ok && it.Tile != 0 {
		// a tile: the symbol white on a rounded square of the row's colour
		ts := theme.Snap(k.Pt(rc.TileSize))
		if k.Inactive() { // half transparent, the symbol too (measured)
			c.PushOpacity(rc.TileInactive)
		}
		theme.At(c, theme.Snap(sp.IconX+indent), theme.Snap((h-ts)/2), ts, ts)
		c.NextBG(it.Tile)
		c.NextRadius(k.Pt(rc.TileRadius))
		c.NextJustify(ui.JustifyCenter)
		c.NextCross(ui.CrossCenter)
		c.NextAxis(ui.AxisX)
		// (the symbol is cut at the square's edge, as AppKit's tile image cuts
		// a wide one: battery.100)
		c.Push(c.BoxKeyed(ui.DrawBackground|ui.Clip|base.Floating, ui.KeyN(rkey, 5), nil))
		if f, disp := k.Icons().Prepare(c, id, rc.TileGlyph.RGBA(r)); f != 0 {
			c.BoxKeyed(f, ui.KeyN(rkey, 1), disp)
		}
		c.Pop()
		if k.Inactive() {
			c.PopOpacity()
		}
		textX = sp.IconX + indent + ts + k.Pt(rc.SymbolGap+rc.FieldInset)
	} else if ok {
		iw, ih := k.IconSize(id)
		if f, disp := k.Icons().Prepare(c, id, symCol); f != 0 {
			box := iw
			if iw < k.Pt(rc.DotW) {
				box = sp.Icon
			}
			theme.At(c, theme.Snap(sp.IconX+indent+(box-iw)/2), theme.Snap((h-ih)/2), iw, ih)
			c.BoxKeyed(f|base.Floating, ui.KeyN(rkey, 1), disp)
			textX = sp.IconX + indent + box + k.Pt(rc.SymbolGap+rc.FieldInset)
		}
	}
	// the selected row's title is semibold, and so is every title of a
	// two-line row (a list of notes, as AppKit's Notes draws it)
	ts := &r.Styles[theme.StyleBody]
	if sel || it.Detail != "" {
		ts = &r.Styles[theme.StyleBodySemibold]
	}
	tw := max(w-theme.Snap(textX)-theme.Snap(k.Pt(rc.TextEnd)), 0)
	ty := theme.Snap((h - ts.Line) / 2)
	var dt *theme.StyleFont
	if it.Detail != "" {
		dt = &r.Styles[theme.StyleSubheadline]
		ty = theme.Snap(k.Pt(rc.DetailTop))
	}
	if st.renaming == it.ID {
		theme.At(c, theme.Snap(textX-k.Pt(rc.FieldInset)), 0, tw+k.Pt(rc.FieldInset), h)
		c.NextAxis(ui.AxisX)
		c.NextCross(ui.CrossCenter)
		c.Push(c.BoxKeyed(base.Floating, ui.KeyN(rkey, 7), nil))
		sidebarRename(k, st, key, ui.Pct(1))
		c.Pop()
	} else {
		theme.At(c, theme.Snap(textX), ty, tw, ts.Line)
		c.NextFont(ts.Font)
		c.NextTextColor(textCol)
		c.BoxKeyed(ui.DrawText|ui.EllipsisOverflowX|base.Floating, ui.KeyN(rkey, 2), base.StrBytes(it.Title))
	}
	if dt != nil {
		col := rc.Detail.RGBA(r)
		if sel && emph {
			col = theme.WithAlpha(rc.OnSel.RGBA(r), rc.DetailOnSel)
		}
		theme.At(c, theme.Snap(textX), ty+ts.Line+theme.Snap(k.Pt(2)), tw, dt.Line)
		c.NextFont(dt.Font)
		c.NextTextColor(col)
		c.BoxKeyed(ui.DrawText|ui.EllipsisOverflowX|base.Floating, ui.KeyN(rkey, 4), base.StrBytes(it.Detail))
	}
	c.Pop()
	return pressed, toggled, h
}

// sidebarSelection draws the selection behind a row of width w and height h
// (the current parent): inset from the sidebar's sides, the full row high,
// rounded; the accent while emphasised, else grey (§3, §7). It is the
// sidebar's whole selection look, in one place.
func sidebarSelection(k *base.Kit, rkey ui.Key, w, h float32, emph bool) {
	c, r := k.Context(), k.Theme()
	rc := &SidebarLook
	sel := r.Window.Selection
	fill := rc.SelUnemph.RGBA(r)
	switch {
	case k.Inactive():
		fill = rc.SelInactive.RGBA(r)
	case emph:
		fill = rc.Sel.RGBA(r)
	}
	inset := theme.Snap(sel.Inset)
	theme.At(c, inset, 0, max(w-2*inset, 0), h)
	c.NextBG(fill)
	c.NextRadius(sel.Radius)
	c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(rkey, 3), nil)
}

// sidebarHeader builds a group header of height h; it reports a press on its
// disclosure, which shows under the pointer and while it has the focus (Tab
// reaches it; Space toggles it, as an assistive technology's press does).
func sidebarHeader(k *base.Kit, key ui.Key, it *SidebarItem, rc *SidebarRecipe, o *SidebarOpts) (h float32, toggled bool) {
	c, r := k.Context(), k.Theme()
	sp := &r.Window.Sidebar
	hkey := ui.KeyN(key, 3<<32|uint64(it.ID))
	h = theme.Snap(sp.HeaderH)
	if o.HeaderH > 0 {
		h = theme.Snap(k.Pt(o.HeaderH))
	}
	c.NextSize(ui.Pct(1), ui.Px(h))
	c.NextRole(ui.RoleGroup)
	c.NextSemName(it.Title)
	hb := c.BoxKeyed(ui.Hoverable|ui.HotAnimation, hkey, nil)
	if rr := c.Rect(hb); rr[3] > rr[1] && c.ShownRect(hb) == [4]float32{} {
		return h, false
	}
	c.Push(hb)
	rr := c.Rect(hb)
	w := rr[2] - rr[0]
	col := rc.Header.RGBA(r)
	if k.Inactive() {
		col = rc.HeaderInactive.RGBA(r)
	}
	st := &r.Styles[theme.StyleSidebarHeader]
	if o.BodyHeaders {
		st = &r.Styles[theme.StyleBody]
	}
	hx := theme.Snap(sp.HeaderX + k.Pt(rc.FieldInset))
	theme.At(c, hx, theme.Snap((h-st.Line)/2), max(w-hx-k.Pt(28), 0), st.Line)
	c.NextFont(st.Font)
	c.NextTextColor(col)
	c.BoxKeyed(ui.DrawText|ui.EllipsisOverflowX|base.Floating, ui.KeyN(hkey, 1), base.StrBytes(it.Title))
	if it.Open != nil {
		ckey := ui.KeyN(hkey, 9)
		show := max(c.HotTOf(hkey), c.HotTOf(ckey), c.FocusTOf(ckey))
		if !*it.Open {
			show = 1 // a hidden group keeps its chevron, as AppKit's does
		}
		// built while hidden too: the keyboard and assistive technologies
		// reach it
		toggled = sidebarChevron(k, hkey, key, it.Open, theme.Snap(w-k.Pt(rc.HeaderChevronRight)-k.Pt(6.5)), 0, h, theme.WithAlpha(rc.Chevron.RGBA(r)|0xFF, show), true)
	}
	c.Pop()
	return h, toggled
}

// sidebarChevron builds the disclosure chevron of a row or header at x in a
// row of height h, dy below its middle, which turns over the Standard duration, and toggles *open
// on a click or an assistive technology's press. A header's takes the focus
// from the keyboard (Tab; Space or Enter toggles it); a click on it leaves
// the focus with the sidebar (root).
func sidebarChevron(k *base.Kit, rkey, root ui.Key, open *bool, x, dy, h float32, col uint32, header bool) bool {
	c, r := k.Context(), k.Theme()
	ckey := ui.KeyN(rkey, 9)
	ps := c.PeekSignal(ckey)
	toggled := k.PressedBefore(ckey)
	if toggled {
		*open = !*open
		if header && ps.Clicked && !ps.Activated {
			c.SetFocus(root)
		}
	}
	tgt := float32(0)
	if *open {
		tgt = 1
	}
	rate := RateFor(r.Motion.Standard)
	if reduced(k) {
		rate = theme.Instant
	}
	t := c.AnimateValue(ui.KeyN(ckey, 1), tgt, rate)
	side := theme.Snap(k.Pt(DisclosureLook.Box))
	theme.At(c, x, theme.Snap((h-side)/2+dy), side, side)
	c.NextRole(ui.RoleButton)
	if header {
		c.NextSemName(str.ShowOrHide)
	} else {
		c.NextSemName(str.Disclosure)
	}
	var state ui.SemState
	if *open {
		state = ui.SemExpanded
	}
	c.NextSemState(state)
	flags := ui.Clickable | ui.HotAnimation | ui.ActiveAnimation | base.Floating
	if header {
		flags |= ui.Focusable | ui.FocusAnimation
	}
	if k.Disabled() {
		flags = ui.Disabled | base.Floating
	}
	cb := c.BoxKeyed(flags, ckey, nil)
	c.Push(cb)
	// over the sidebar's glass: the chevron in the colour it composites to
	under := glassFallback(k, GlassSidebar)
	chevron(c, ckey, 100, &DisclosureLook.Closed, t*DisclosureLook.Open, DisclosureLook.Width, k.Scale(), ink(col, under))
	if header {
		k.FocusRing(cb, k.VisualOf(ckey, base.HoverOn), side/2)
	}
	c.Pop()
	return toggled
}

// visible makes st.vis the indexes of the visible, selectable rows, in
// order.
func (st *SidebarState) visible(items []SidebarItem) {
	st.vis = st.vis[:0]
	w := sidebarWalk{hide: -1}
	for i := range items {
		if w.row(&items[i]) {
			st.vis = append(st.vis, int32(i))
		}
	}
}

// cursor is Sel's place among the visible rows, -1 when it is not one.
func (st *SidebarState) cursor(items []SidebarItem) int {
	for j, i := range st.vis {
		if items[i].ID == st.Sel {
			return j
		}
	}
	return -1
}

// sidebarMove applies move m to the selection over the visible rows (a page
// is page rows), with extend the range from the anchor, and reports whether
// it changed.
func sidebarMove(st *SidebarState, items []SidebarItem, m base.NavMove, extend bool, page int) bool {
	n := len(st.vis)
	if n == 0 || m == base.NavNone {
		return false
	}
	cur := st.cursor(items)
	to := base.NavStep(cur, n, nil, m, page, false)
	if to < 0 || to == cur && !(extend && st.Count() > 1) {
		return false
	}
	id := items[st.vis[to]].ID
	if extend {
		st.selectRange(items, st.anchorRow(), id)
	} else {
		st.Select(id)
	}
	return true
}

// sidebarKeys applies the keys of the focused sidebar: Up, Down, Page Up,
// Page Down, Home and End move the selection (base.NavStep, as every list's
// keys), with multi Shift extends it and the primary modifier with A selects
// every row; Right and Left open and close a row's children (Left on a child
// goes to its parent); Enter activates the selected row; typing finds a row
// by its title, ignoring case and accents. It reports a changed selection, a
// toggled row and the activated one (act, unless Enter went down).
func sidebarKeys(c *ui.Context, st *SidebarState, items []SidebarItem, multi bool, page int, act uint32) (changed bool, toggled, activated uint32) {
	activated = act
	n := len(st.vis)
	if n == 0 {
		return false, 0, activated
	}
	if multi && c.TakeKey('A', ui.PrimaryMod()) {
		st.SelectAll(items)
		return true, 0, activated
	}
	if m, extend := base.TakeNavKey(c, multi); m != base.NavNone {
		return sidebarMove(st, items, m, extend, page), 0, activated
	}
	cur := st.cursor(items)
	switch {
	case cur >= 0 && c.TakeKey(ui.KeyEnter, 0):
		activated = st.Sel
	case cur >= 0 && c.TakeKey(ui.KeyRight, 0):
		if it := &items[st.vis[cur]]; it.Open != nil && !*it.Open {
			*it.Open, toggled = true, it.ID
		}
	case cur >= 0 && c.TakeKey(ui.KeyLeft, 0):
		it := &items[st.vis[cur]]
		if it.Open != nil && *it.Open {
			*it.Open, toggled = false, it.ID
		} else if it.Level > 0 { // to the parent
			for j := cur - 1; j >= 0; j-- {
				if items[st.vis[j]].Level < it.Level {
					st.Select(items[st.vis[j]].ID)
					return true, toggled, activated
				}
			}
		}
	}
	if q := st.find.Feed(c.Input()); q != nil {
		start := max(cur, 0)
		if len(q) == 1 || cur < 0 {
			start = cur + 1
		}
		for i := range n {
			j := (start + i) % n
			if it := &items[st.vis[j]]; base.FoldHasPrefix(it.Title, base.BytesStr(q)) {
				if it.ID != st.Sel {
					st.Select(it.ID)
					return true, toggled, activated
				}
				break
			}
		}
	}
	return false, toggled, activated
}

// sidebarRename builds the inline rename field, w wide, and ends the
// rename: Enter confirms it
// (SidebarSignal.Renamed), Escape or the focus leaving cancels it.
func sidebarRename(k *base.Kit, st *SidebarState, root ui.Key, w ui.Size) {
	c := k.Context()
	k.NextWidth(w)
	sig := TextField(k, str.Rename+"###rename", &st.rename, "", FieldOpts{Size: theme.SizeSmall})
	if st.renameAt == 0 {
		st.renameAt = 1
		c.SetFocus(sig.Key)
		c.SetFocusVisible(false)
	}
	switch {
	case sig.Submitted:
		st.renamed = st.renaming
		st.renaming = 0
	case sig.Focused:
		st.renameAt = 2
		if c.TakeKey(ui.KeyEscape, 0) {
			st.renaming = 0
		}
	case st.renameAt == 2: // the focus left it
		st.renaming = 0
	}
	if st.renaming == 0 {
		c.SetFocus(root) // back to the sidebar's keys
	}
}

// sidebarThumb builds the scroll bar of the sidebar's view: an overlay
// thumb, or with the system's legacy scrollers a knob in its slot, in the
// room the rows left for it.
func sidebarThumb(k *base.Kit, v *base.ScrollView, rc *SidebarRecipe) {
	c, r := k.Context(), k.Theme()
	bars := k.ScrollBars(v)
	b := &bars[1]
	if !b.On {
		return
	}
	k.BeginScrollBar(b)
	if b.Legacy {
		knob, slot := rc.LegacyKnob, rc.LegacySlot
		if k.Inactive() {
			knob, slot = rc.LegacyKnobInactive, rc.LegacySlotInactive
		}
		in := theme.Snap(r.Lines.ScrollerKnobInset)
		tw := b.W - 2*in
		theme.At(c, in, b.SlotOff, tw, b.SlotLen)
		c.NextBG(slot.At(r.T))
		c.NextRadius(tw / 2)
		c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(b.Key, 2), nil)
		theme.At(c, in, b.ThumbOff, tw, b.ThumbLen)
		c.NextBG(knob.At(r.T))
		c.NextRadius(tw / 2)
		c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(b.Key, 1), nil)
		c.Pop()
		return
	}
	tw := theme.Snap(k.Pt(rc.ThumbW) * (1 + b.Hot) / 2)
	theme.At(c, b.W-tw-theme.Snap(k.Pt(2)), b.ThumbOff, tw, b.ThumbLen)
	c.NextBG(theme.WithAlpha(rc.Thumb.RGBA(r), b.Show))
	c.NextRadius(tw / 2)
	c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(b.Key, 1), nil)
	c.Pop()
}

// desaturate is c's grey of the same luminance: an inactive window's sidebar
// symbols (§7).
func desaturate(c uint32) uint32 {
	r, g, b := float32(c>>24&255), float32(c>>16&255), float32(c>>8&255)
	y := uint32(0.2126*r + 0.7152*g + 0.0722*b + 0.5)
	return y<<24 | y<<16 | y<<8 | c&0xFF
}

// SidebarIcons are Lucide icons for common sidebar places, at the sidebar's
// icon size (16 pt, T3 Code's size-4 and AppKit's medium sidebar images); add
// the ones a sidebar uses to the kit (Kit.AddIcon) before the first frame.
var SidebarIcons = struct {
	Recents, Applications, Desktop, Documents, Downloads, Cloud, Shared, Disk, Network, Folder, Tag icon.Spec
}{
	Recents:      icon.Spec{Lucide: "clock", Fallback: '◷', Pt: 16},
	Applications: icon.Spec{Lucide: "layout-grid", Fallback: '▦', Pt: 16},
	Desktop:      icon.Spec{Lucide: "monitor", Fallback: '▭', Pt: 16},
	Documents:    icon.Spec{Lucide: "file-text", Fallback: '▢', Pt: 16},
	Downloads:    icon.Spec{Lucide: "circle-arrow-down", Fallback: '⇩', Pt: 16},
	Cloud:        icon.Spec{Lucide: "cloud", Fallback: '☁', Pt: 16},
	Shared:       icon.Spec{Lucide: "users", Fallback: '▣', Pt: 16},
	Disk:         icon.Spec{Lucide: "hard-drive", Fallback: '▤', Pt: 16},
	Network:      icon.Spec{Lucide: "globe", Fallback: '◎', Pt: 16},
	Folder:       icon.Spec{Lucide: "folder", Fallback: '▣', Pt: 16},
	Tag:          icon.Spec{Lucide: "tag", Fallback: '●', Pt: 16},
}
