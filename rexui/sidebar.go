package rexui

import (
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
	// Header makes the row a group header: small bold text, not selectable;
	// the rows up to the next header are its group.
	Header bool
	// Level is how deep the row is nested (0 for a row of a group).
	Level uint8
	// Open is the disclosure state of a row with children, or of a header
	// whose group can be hidden (nil: none). The sidebar toggles it.
	Open *bool
}

// SidebarState is the state of a sidebar, owned by the app.
type SidebarState struct {
	// Sel is the selected row's ID (0: none).
	Sel uint32
	// Scroll is the scroll view's state.
	Scroll base.ScrollState
}

// SidebarOpts are a sidebar's options, passed by value.
type SidebarOpts struct {
	// Top is the room above the first row, in pixels: the toolbar's height
	// (the sidebar's glass runs under the toolbar to the window's top).
	Top float32
	// Pad is extra room below Top, in points (a search field's place).
	Pad float32
	// RowH and HeaderH replace the rows' and the headers' heights, in points
	// (0: the medium sidebar's 32 and 19); BodyHeaders sets the headers in
	// body text, as a list of notes does.
	RowH, HeaderH float32
	BodyHeaders   bool
}

// SidebarSignal is what a sidebar reports for a frame.
type SidebarSignal struct {
	base.Signal
	// Under is set while rows are scrolled under the toolbar: the toolbar
	// shows its scroll edge over the sidebar.
	Under bool
	// Toggled is the row whose disclosure changed (0: none).
	Toggled uint32
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
	Sel, SelUnemph, SelInactive, OnSel Tint
	OnSelInactive                      Pair
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
	// Detail colours the second line over the glass and the grey
	// selections (the vibrant secondary label: black at 43 %, white at 36 %,
	// measured #868686 on #ECECEC and #747474 on #262626); DetailOnSel is
	// OnSel's alpha for it on the accent (measured 0.7).
	Detail      Pair
	DetailOnSel float32
	// HeaderChevronRight is a header's disclosure from the trailing edge
	// (centre), shown under the pointer; Chevron its colour.
	HeaderChevronRight float32
	Chevron            Tint
	// Thumb is the scroll bar's thumb, ThumbW its width.
	Thumb  Tint
	ThumbW float32
	// SearchFill and SearchFillInactive fill a search field on the
	// sidebar's glass (SidebarSearch): a capsule of the field's height,
	// measured #EAEAEA and #4E4E4E, #E5E5E5 and #464646 inactive (§2).
	SearchFill, SearchFillInactive Pair
}

// SidebarLook is the sidebar's recipe (the colours are theme roles measured
// on 27.2: SidebarFill, SidebarSel …).
var SidebarLook = SidebarRecipe{
	Text: Of(theme.SidebarLabel), TextInactive: Of(theme.SidebarLabelInactive),
	Header: Of(theme.SidebarHeader), HeaderInactive: Of(theme.SidebarHeaderInactive),
	Sel: Of(theme.SidebarSel), SelUnemph: Of(theme.SidebarSelUnemph), SelInactive: Of(theme.SidebarSelInactive), OnSel: Of(theme.OnAccent),
	OnSelInactive: Solid(0x676767, 0x999999),
	Symbol:        Of(theme.Accent),
	SymbolGap:     4, DotW: 12, PlainTextX: 20, FieldInset: 2,
	Detail: PairA(0x000000, 0xFFFFFF, 0.43, 0.36), DetailOnSel: 0.7, HeaderChevronRight: 17.5, Chevron: Of(theme.Label3),
	Thumb: OfAlpha(theme.Label, 0.25, 0.3), ThumbW: 6,
	SearchFill: Solid(0xEAEAEA, 0x4E4E4E), SearchFillInactive: Solid(0xE5E5E5, 0x464646),
}

// Sidebar builds a sidebar of items in the current parent (it takes the
// parent's size unless the kit's NextSize says otherwise) that edits st.Sel.
// A press selects a row; with the keyboard (the sidebar takes the focus when
// pressed) Up and Down move the selection, Left and Right close and open a
// row's children. Changed is set in the frame the selection changed.
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
	k.ApplySize(ui.Pct(1), ui.Pct(1))
	c.NextBG(GlassFill(k, GlassSidebar))
	c.NextRole(ui.RoleList)
	c.NextSemName(label)
	flags := ui.DrawBackground | ui.Clip | ui.Focusable
	if k.Disabled() {
		flags = ui.DrawBackground | ui.Clip | ui.Disabled
	}
	root := c.BoxKeyed(flags, key, nil)
	c.Push(root)
	focused := c.Focused(root) && !k.Disabled()
	emph := focused && !k.Inactive()

	// the keyboard, before the rows show the selection
	if focused {
		res.Changed, res.Toggled = sidebarKeys(c, st, items)
	}

	k.NextSize(ui.Pct(1), ui.Pct(1))
	v := k.BeginScroll("rows", &st.Scroll, base.ScrollVertical)
	c.NextSize(ui.Pct(1), ui.Px(o.Top+k.Pt(o.Pad)))
	c.BoxKeyed(0, ui.KeyN(key, 1), nil)
	_, sy := c.Scroll(v.Inner)
	res.Under = sy > 0.5

	headers := 0
	hide := -1 // rows deeper than this level are hidden (a closed row's children); -2 to the next header
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
			}
			headers++
			if t := sidebarHeader(k, key, it, rc, &o); t {
				res.Toggled = it.ID
			}
			if it.Open != nil && !*it.Open {
				hide = -2
			}
			continue
		}
		pressed, toggled := sidebarRow(k, key, it, st.Sel == it.ID, emph, rc, &o)
		if pressed && st.Sel != it.ID {
			st.Sel, res.Changed = it.ID, true
		}
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
	res.Focused = focused
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
	fill := rc.SearchFill.At(r.T)
	if k.Inactive() {
		fill = rc.SearchFillInactive.At(r.T)
	}
	k.ApplySize(ui.Pct(1), ui.Px(h))
	c.NextBG(fill)
	c.NextRadius(h / 2)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	_, ident := ui.SplitLabel(label)
	c.Push(c.BoxKeyed(ui.DrawBackground, ui.HashString(c.ParentKey(), ident+" capsule"), nil))
	res := SearchField(k, label, buf, placeholder, FieldOpts{Variant: FieldBorderless})
	c.Pop()
	return res
}

// sidebarRow builds one row; it reports a press on it and a click on its
// disclosure.
func sidebarRow(k *base.Kit, key ui.Key, it *SidebarItem, sel, emph bool, rc *SidebarRecipe, o *SidebarOpts) (pressed, toggled bool) {
	c, r := k.Context(), k.Theme()
	sp := &r.Window.Sidebar
	rkey := ui.KeyN(key, 1<<32|uint64(it.ID))
	ps := c.PeekSignal(rkey)
	pressed = (ps.Pressed || ps.Action&ui.ActPress != 0) && !k.Disabled()
	if pressed {
		sel = true
	}
	h := theme.Snap(sp.RowH)
	if o.RowH > 0 {
		h = theme.Snap(k.Pt(o.RowH))
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
			textCol = rc.OnSelInactive.At(r.T)
		}
	}

	indent := float32(it.Level) * sp.Indent
	if it.Open != nil {
		// in the indentation before the cell (x 16, the symbol's 3 before it)
		toggled = sidebarChevron(k, rkey, it.Open, theme.Snap(sp.IconX+indent-k.Pt(3+DisclosureLook.Box)), h, textCol, false)
	}
	textX := k.Pt(rc.PlainTextX+rc.FieldInset) + indent
	if id, ok := it.Glyph.ID(); ok {
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
	st := &r.Styles[theme.StyleBody]
	if sel || it.Detail != "" {
		st = &r.Styles[theme.StyleBodySemibold]
	}
	tw := max(w-theme.Snap(textX)-theme.Snap(r.Window.Selection.Inset)-k.Pt(6), 0)
	ty := theme.Snap((h - st.Line) / 2)
	var dt *theme.StyleFont
	if it.Detail != "" {
		dt = &r.Styles[theme.StyleSubheadline]
		ty = theme.Snap((h - st.Line - k.Pt(2) - dt.Line) / 2)
	}
	theme.At(c, theme.Snap(textX), ty, tw, st.Line)
	c.NextFont(st.Font)
	c.NextTextColor(textCol)
	c.BoxKeyed(ui.DrawText|ui.FadeOverflowX|base.Floating, ui.KeyN(rkey, 2), base.StrBytes(it.Title))
	if dt != nil {
		col := rc.Detail.At(r.T)
		if sel && emph {
			col = theme.WithAlpha(rc.OnSel.RGBA(r), rc.DetailOnSel)
		}
		theme.At(c, theme.Snap(textX), ty+st.Line+theme.Snap(k.Pt(2)), tw, dt.Line)
		c.NextFont(dt.Font)
		c.NextTextColor(col)
		c.BoxKeyed(ui.DrawText|ui.FadeOverflowX|base.Floating, ui.KeyN(rkey, 4), base.StrBytes(it.Detail))
	}
	c.Pop()
	return pressed, toggled
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

// sidebarHeader builds a group header; it reports a click on its
// disclosure, which shows under the pointer.
func sidebarHeader(k *base.Kit, key ui.Key, it *SidebarItem, rc *SidebarRecipe, o *SidebarOpts) (toggled bool) {
	c, r := k.Context(), k.Theme()
	sp := &r.Window.Sidebar
	hkey := ui.KeyN(key, 3<<32|uint64(it.ID))
	h := theme.Snap(sp.HeaderH)
	if o.HeaderH > 0 {
		h = theme.Snap(k.Pt(o.HeaderH))
	}
	c.NextSize(ui.Pct(1), ui.Px(h))
	c.NextRole(ui.RoleGroup)
	c.NextSemName(it.Title)
	hb := c.BoxKeyed(ui.Hoverable|ui.HotAnimation, hkey, nil)
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
	c.BoxKeyed(ui.DrawText|ui.FadeOverflowX|base.Floating, ui.KeyN(hkey, 1), base.StrBytes(it.Title))
	if it.Open != nil {
		show := max(c.HotTOf(hkey), c.HotTOf(ui.KeyN(hkey, 9)))
		if !*it.Open {
			show = 1 // a hidden group keeps its chevron, as AppKit's does
		}
		if show > 0.004 {
			toggled = sidebarChevron(k, hkey, it.Open, theme.Snap(w-k.Pt(rc.HeaderChevronRight)-k.Pt(6.5)), h, theme.WithAlpha(rc.Chevron.RGBA(r)|0xFF, show), true)
		}
	}
	c.Pop()
	return toggled
}

// sidebarChevron builds the disclosure chevron of a row or header at x in a
// row of height h, which turns over the Standard duration, and toggles *open
// on a click.
func sidebarChevron(k *base.Kit, rkey ui.Key, open *bool, x, h float32, col uint32, header bool) bool {
	c, r := k.Context(), k.Theme()
	ckey := ui.KeyN(rkey, 9)
	toggled := c.PeekSignal(ckey).Clicked && !k.Disabled()
	if toggled {
		*open = !*open
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
	theme.At(c, x, theme.Snap((h-side)/2), side, side)
	c.NextRole(ui.RoleButton)
	if header {
		c.NextSemName("Show or Hide")
	} else {
		c.NextSemName("Disclosure")
	}
	var state ui.SemState
	if *open {
		state = ui.SemExpanded
	}
	c.NextSemState(state)
	cb := c.BoxKeyed(ui.Clickable|ui.HotAnimation|ui.ActiveAnimation|base.Floating, ckey, nil)
	c.Push(cb)
	// over the sidebar's glass: the chevron in the colour it composites to
	under := GlassFill(k, GlassSidebar)
	chevron(c, ckey, 100, &DisclosureLook.Closed, t*DisclosureLook.Open, DisclosureLook.Width, k.Scale(), ink(col, under))
	c.Pop()
	return toggled
}

// sidebarKeys applies the arrow keys to the selection and the disclosures
// of the visible rows; it reports a changed selection and a toggled row.
func sidebarKeys(c *ui.Context, st *SidebarState, items []SidebarItem) (changed bool, toggled uint32) {
	// the visible, selectable rows in order
	var vis [256]int16
	n := 0
	cur := -1
	hide := -1
	for i := range items {
		it := &items[i]
		if it.Header {
			hide = -1
			if it.Open != nil && !*it.Open {
				hide = -2
			}
			continue
		}
		if hide == -2 || (hide >= 0 && int(it.Level) > hide) {
			continue
		}
		hide = -1
		if it.Open != nil && !*it.Open {
			hide = int(it.Level)
		}
		if n < len(vis) {
			if it.ID == st.Sel {
				cur = n
			}
			vis[n] = int16(i)
			n++
		}
	}
	if n == 0 {
		return false, 0
	}
	to := -1
	switch {
	case c.TakeKey(ui.KeyDown, 0):
		to = min(cur+1, n-1)
		if cur < 0 {
			to = 0
		}
	case c.TakeKey(ui.KeyUp, 0):
		to = max(cur-1, 0)
	case cur >= 0 && c.TakeKey(ui.KeyRight, 0):
		if it := &items[vis[cur]]; it.Open != nil && !*it.Open {
			*it.Open, toggled = true, it.ID
		}
	case cur >= 0 && c.TakeKey(ui.KeyLeft, 0):
		it := &items[vis[cur]]
		if it.Open != nil && *it.Open {
			*it.Open, toggled = false, it.ID
		} else if it.Level > 0 { // to the parent
			for j := cur - 1; j >= 0; j-- {
				if items[vis[j]].Level < it.Level {
					to = j
					break
				}
			}
		}
	}
	if to >= 0 && to != cur {
		st.Sel = items[vis[to]].ID
		return true, toggled
	}
	return false, toggled
}

// sidebarThumb builds the overlay scroll bar of the sidebar's view.
func sidebarThumb(k *base.Kit, v *base.ScrollView, rc *SidebarRecipe) {
	c, r := k.Context(), k.Theme()
	bars := k.ScrollBars(v)
	b := &bars[1]
	if !b.On {
		return
	}
	k.BeginScrollBar(b)
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

// SidebarIcons are SF Symbols for common sidebar places, at the sidebar's
// symbol size (16 pt: AppKit's medium sidebar images, 18–26 × 18–21 on
// 27.2); add the ones a sidebar uses to the kit (Kit.AddIcon) before
// the first frame.
var SidebarIcons = struct {
	Recents, Applications, Desktop, Documents, Downloads, Cloud, Shared, Disk, Network, Folder, Tag icon.Spec
}{
	Recents:      icon.Spec{Symbol: "clock", Fallback: '◷', Pt: 16},
	Applications: icon.Spec{Symbol: "square.grid.2x2", Fallback: '▦', Pt: 16},
	Desktop:      icon.Spec{Symbol: "menubar.dock.rectangle", Fallback: '▭', Pt: 16},
	Documents:    icon.Spec{Symbol: "doc", Fallback: '▢', Pt: 16},
	Downloads:    icon.Spec{Symbol: "arrow.down.circle", Fallback: '⇩', Pt: 16},
	Cloud:        icon.Spec{Symbol: "icloud", Fallback: '☁', Pt: 16},
	Shared:       icon.Spec{Symbol: "folder.badge.person.crop", Fallback: '▣', Pt: 16},
	Disk:         icon.Spec{Symbol: "internaldrive", Fallback: '▤', Pt: 16},
	Network:      icon.Spec{Symbol: "network", Fallback: '◎', Pt: 16},
	Folder:       icon.Spec{Symbol: "folder", Fallback: '▣', Pt: 16},
	Tag:          icon.Spec{Symbol: "circle.fill", Fallback: '●', Pt: 9},
}
