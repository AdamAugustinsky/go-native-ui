package rexui

import (
	"gonativeui/icon"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// ListStyle is the style of a list, table or outline, as NSTableView's.
type ListStyle uint8

const (
	// ListInset insets the rows 10 pt from the sides, with rounded
	// selections and stripes (NSTableViewStyleInset, Finder's list view):
	// what NSTableViewStyleAutomatic gives a table or outline whose scroll
	// view has no border, on macOS 27.2.
	ListInset ListStyle = iota
	// ListFullWidth runs the rows and their selection from edge to edge
	// (NSTableViewStyleFullWidth, what Automatic gives in a bordered scroll
	// view).
	ListFullWidth

	// NumListStyles is the number of list styles.
	NumListStyles
)

// ListGlyphs are the glyphs of outlines' disclosures and tables' sort
// indicators at AppKit's sizes, which the standard chevrons are larger than
// (AddListGlyphs). The zero value uses the standard chevrons.
type ListGlyphs struct {
	// Collapsed and Expanded are an outline's disclosures.
	Collapsed, Expanded icon.ID
	// SortUp and SortDown are a table header's sort indicators.
	SortUp, SortDown icon.ID
}

// ListGlyphSpecs are the glyphs AddListGlyphs adds: SF Symbols chevrons whose
// ink matches macOS 27.2's (a disclosure 5 × 8.5 pt pointing right, a sort
// indicator about 6 pt wide).
var ListGlyphSpecs = [4]icon.Spec{
	{Symbol: "chevron.right", Segoe: 0xE76C, Fallback: '▸', Pt: 9, Weight: 0.4},
	{Symbol: "chevron.down", Segoe: 0xE70D, Fallback: '▾', Pt: 9, Weight: 0.4},
	{Symbol: "chevron.up", Segoe: 0xE70E, Fallback: '▴', Pt: 7.5, Weight: 0.4},
	{Symbol: "chevron.down", Segoe: 0xE70D, Fallback: '▾', Pt: 7.5, Weight: 0.4},
}

// AddListGlyphs adds ListGlyphSpecs to k's icons; call it before k's first
// Begin and pass the result in the options of tables and outlines.
func AddListGlyphs(k *base.Kit) ListGlyphs {
	return ListGlyphs{k.AddIcon(ListGlyphSpecs[0]), k.AddIcon(ListGlyphSpecs[1]), k.AddIcon(ListGlyphSpecs[2]), k.AddIcon(ListGlyphSpecs[3])}
}

// orStandard is g, or the standard chevrons for the zero value.
func (g ListGlyphs) orStandard() ListGlyphs {
	if g == (ListGlyphs{}) {
		return ListGlyphs{icon.ChevronRight, icon.ChevronDown, icon.ChevronUp, icon.ChevronDown}
	}
	return g
}

// ListOpts are a list's options, passed by value.
type ListOpts struct {
	// Style is the list's style.
	Style ListStyle
	// Striped alternates the rows' background, and the room below the last
	// row, as usesAlternatingRowBackgroundColors does.
	Striped bool
}

// ListRecipe is the look of the rows of lists, tables and outlines (macOS
// 27.2, NSTableView):
//
//   - The selection is solid selectedContentBackgroundColor with white text
//     and glyphs while the list has the focus in the key window
//     (emphasised); otherwise, or in an inactive window, it is
//     unemphasizedSelectedContentBackgroundColor with label-coloured text.
//     The focus shows through the emphasis: there is no focus ring on a
//     row, and no hover.
//   - Contiguous selected rows are one block. Full width, it is square; inset,
//     it is rounded at its ends only, with a 1-pt lighter line between its
//     rows, 6 pt short of each side (Finder; tools/appkitref -live).
//   - Stripes are alternatingContentBackgroundColors[1] on the odd rows,
//     inset and rounded like the selection in the inset style.
type ListRecipe struct {
	// Background is the list's own fill (controlBackgroundColor).
	Background Tint
	// Selection fills an emphasised selection, SelectionUnemph any other.
	Selection, SelectionUnemph Tint
	// Divider is the line between the rows of an emphasised inset block,
	// DividerUnemph of an unemphasised one, composited over the block
	// (measured #1973E4 over #0064E1 and #D1D1D1 over #DCDCDC in light;
	// #196AD6 and #4F4F4F in dark).
	Divider, DividerUnemph Tint
	// DividerInset is how far short of the block's sides the line stops, in
	// points.
	DividerInset float32
	// Stripe fills the odd rows of a striped list.
	Stripe Tint
	// Label colours a row's text, Label2 its secondary text (a date, a
	// size), LabelDisabled both in a disabled list, and OnSelection text
	// and glyphs on an emphasised selection.
	Label, Label2, LabelDisabled, OnSelection Tint
	// Inset is the room at the sides of the rows, PadY above the first row
	// and below the last without a header and HeaderPadY with one, per
	// style, in points (NSTableView's row rectangles: inset tables start
	// their rows 5 pt below the header, 10 pt without one).
	Inset, PadY, HeaderPadY [NumListStyles]float32
	// Rounded says the selection and the stripes have the theme's selection
	// radius (Radii.Selection, 8), per style.
	Rounded [NumListStyles]bool
}

// ListLook is the recipe of lists, tables and outlines.
var ListLook = ListRecipe{
	Background: Of(theme.ControlBackground),
	Selection:  Of(theme.Selection), SelectionUnemph: Of(theme.SelectionUnemph),
	Divider: Of(theme.SelectionDivider), DividerUnemph: OfAlpha(theme.Text, 0.05, 0.05), DividerInset: 6,
	Stripe: Of(theme.AltRow),
	Label:  Of(theme.Label), Label2: Of(theme.Label2), LabelDisabled: Of(theme.TextDisabled), OnSelection: Of(theme.OnSelection),
	Inset:      [NumListStyles]float32{ListFullWidth: 0, ListInset: 10},
	PadY:       [NumListStyles]float32{ListFullWidth: 0, ListInset: 10},
	HeaderPadY: [NumListStyles]float32{ListFullWidth: 0, ListInset: 5},
	Rounded:    [NumListStyles]bool{ListInset: true},
}

// List is an open list: base's List and the look of its rows.
type List struct {
	base.List
	rowLook
}

// rowLook draws the rows of a list, table or outline and holds the colours of
// the row being built.
type rowLook struct {
	style   ListStyle
	striped bool
	emph    bool   // the row being built shows the selection emphasised
	fgTint  Tint   // its text colour, as a recipe names it
	fg, fg2 uint32 // its text colours
}

// listOpts are the base options of a list of style o and a header or not.
func listOpts(k *base.Kit, o ListOpts, header bool) base.ListOpts {
	c, r := k.Context(), k.Theme()
	rc := &ListLook
	s := o.Style % NumListStyles
	pad := rc.PadY[s]
	if header {
		pad = rc.HeaderPadY[s]
	}
	c.NextBG(rc.Background.RGBA(r))
	return base.ListOpts{Pad: [2]float32{rc.Inset[s], pad}, OwnPad: true, Flags: ui.DrawBackground}
}

// BeginList starts a list of rows rows of the theme's row height (24 pt;
// base.Kit.NextRowHeight changes it), with st's selection, in the AppKit
// look: no hover, no focus ring on a row, the selection emphasised while the
// list has the focus in the key window.
//
//	l := rexui.BeginList(k, "notes", &v.list, len(v.notes), rexui.ListOpts{})
//	for r := l.First; r < l.Last; r++ {
//		item := v.list.Item(r)
//		l.Row(r, item, v.title(item))
//		rexui.LabelBytes(k, v.title(item), rexui.LabelOpts{Color: l.Text()})
//	}
//	rexui.EndList(k, &l)
//
// Keyboard and pointer are base.Kit.BeginList's.
func BeginList(k *base.Kit, label string, st *base.ListState, rows int, o ListOpts) List {
	return List{List: k.BeginList(label, st, rows, listOpts(k, o, false)), rowLook: rowLook{style: o.Style % NumListStyles, striped: o.Striped}}
}

// Row starts display row r, which shows item, in the list's look, with name
// as its accessible name (copied; nil for none), and pushes it: its children
// flow left to right. It ends the row before it.
func (l *List) Row(r int, item uint32, name []byte) base.RowState {
	rs := l.RowState(r, item)
	l.begin(&l.List, &rs, name)
	return rs
}

// begin builds the row rs of l in the look and pushes it.
func (lk *rowLook) begin(l *base.List, rs *base.RowState, name []byte) ui.ID {
	k := l.Kit()
	c, r := k.Context(), k.Theme()
	rc := &ListLook
	lk.emph = rs.Selected && rs.Emphasised
	var bg uint32
	switch {
	case rs.Selected && lk.emph:
		bg = rc.Selection.RGBA(r)
	case rs.Selected:
		bg = rc.SelectionUnemph.RGBA(r)
	case lk.striped && rs.Row&1 == 1:
		bg = rc.Stripe.RGBA(r)
	}
	var flags ui.Flags
	if bg&0xFF != 0 {
		flags |= ui.DrawBackground
		c.NextBG(bg)
		if rc.Rounded[lk.style] {
			rad := r.Radii.Selection
			top, bottom := rad, rad
			if rs.Selected && rs.PrevSelected {
				top = 0
			}
			if rs.Selected && rs.NextSelected {
				bottom = 0
			}
			c.NextRadii(top, top, bottom, bottom)
		}
	}
	switch {
	case rs.Disabled:
		lk.fgTint, lk.fg2 = rc.LabelDisabled, rc.LabelDisabled.RGBA(r)
	case lk.emph:
		lk.fgTint, lk.fg2 = rc.OnSelection, rc.OnSelection.RGBA(r)
	default:
		lk.fgTint, lk.fg2 = rc.Label, rc.Label2.RGBA(r)
	}
	lk.fg = lk.fgTint.RGBA(r)
	id := l.BeginRow(rs, name, flags)
	if rs.Selected && rs.NextSelected && rc.Rounded[lk.style] {
		// the line between two rows of an inset block, in the upper one's
		// last point
		lc := rc.Divider.RGBA(r)
		if !lk.emph {
			lc = theme.Over(rc.DividerUnemph.RGBA(r), bg)
		}
		in, lw := pt(k.Scale(), rc.DividerInset), pt(k.Scale(), 1)
		c.NextPos(0, l.RowH()-lw)
		c.NextSize(ui.Pct(1), ui.Px(lw))
		c.NextPadding(in, 0, in, 0)
		c.Push(c.BoxKeyed(base.Floating, ui.KeyN(rs.Key, 0xD1D1), nil))
		c.NextSize(ui.Pct(1), ui.Pct(1))
		c.NextBG(lc)
		c.BoxKeyed(ui.DrawBackground, ui.KeyN(rs.Key, 0xD1D2), nil)
		c.Pop()
	}
	return id
}

// Text is the colour of the row being built's text, for a component's
// options (LabelOpts.Color): labelColor, or white on an emphasised selection.
func (lk *rowLook) Text() Tint { return lk.fgTint }

// TextColor is Text as a colour.
func (lk *rowLook) TextColor() uint32 { return lk.fg }

// Text2Color is the colour of the row's secondary text (secondaryLabelColor;
// white on an emphasised selection).
func (lk *rowLook) Text2Color() uint32 { return lk.fg2 }

// GlyphColor is the colour a template glyph of colour col takes in the row
// being built: col, or white on an emphasised selection.
func (lk *rowLook) GlyphColor(col uint32) uint32 {
	if lk.emph {
		return lk.fg
	}
	return col
}

// Filler stripes the room below the last row of a striped list (List
// FillerRows), as AppKit does. Call it after the last row.
func (l *List) Filler() { l.filler(&l.List) }

func (lk *rowLook) filler(l *base.List) {
	if !lk.striped {
		return
	}
	n := l.FillerRows()
	if n == 0 {
		return
	}
	l.CloseRow()
	k := l.Kit()
	c, r := k.Context(), k.Theme()
	rc := &ListLook
	for i := 0; i < n; i++ {
		row := l.Rows + i
		var flags ui.Flags
		if row&1 == 1 {
			flags = ui.DrawBackground
			c.NextBG(rc.Stripe.RGBA(r))
			if rc.Rounded[lk.style] {
				c.NextRadius(r.Radii.Selection)
			}
		}
		c.NextSize(ui.Pct(1), ui.Px(l.RowH()))
		c.BoxKeyed(flags, ui.KeyN(l.BoxKey(), 3<<32|uint64(i)), nil)
	}
}

// Empty shows msg in the body of a list that has no rows, centred in
// secondaryLabelColor. Call it after the loop that builds the rows; with
// rows it builds nothing.
func (l *List) Empty(msg []byte) {
	if l.Rows != 0 || len(msg) == 0 {
		return
	}
	l.CloseRow()
	k := l.Kit()
	c, r := k.Context(), k.Theme()
	c.NextSize(ui.Pct(1), ui.Px(l.RowH()))
	c.NextFont(r.Styles[theme.StyleBody].Font)
	c.NextTextColor(ListLook.Label2.RGBA(r))
	c.NextAlign(ui.AlignCenter)
	c.NextRole(ui.RoleLabel)
	c.NextSemNameBytes(msg)
	c.BoxKeyed(ui.DrawText|ui.FadeOverflowX, ui.KeyN(l.BoxKey(), 2), msg)
}

// EndList ends the list: the stripes below the last row, the rows, the
// overlay scroll bar, and the list.
func EndList(k *base.Kit, l *List) {
	l.Filler()
	l.CloseRows()
	v := l.ScrollView()
	OverlayBars(k, &v)
	k.CloseList(&l.List)
}

// OverlayBars builds the overlay scroll bars of the view whose outer box is the
// current parent (after its content is closed): a thin knob that thickens
// under the pointer and fades when the view rests.
func OverlayBars(k *base.Kit, v *base.ScrollView) {
	c, r := k.Context(), k.Theme()
	px := &r.Px
	bars := k.ScrollBars(v)
	for a := range bars {
		b := &bars[a]
		if !b.On {
			continue
		}
		bw := b.W
		if a == 0 {
			bw = b.H
		}
		if c.Input().Time == 0 {
			// a frame without a clock (an offscreen render, a test) shows the
			// view at rest, where AppKit's overlay scrollers are hidden
			continue
		}
		k.BeginScrollBar(b)
		tw := theme.Snap(px.ScrollThumbW * (1 + b.Hot))
		in := theme.Snap(px.BorderW * 2)
		if a == 1 {
			theme.At(c, bw-tw-in, b.ThumbOff, tw, b.ThumbLen)
		} else {
			theme.At(c, b.ThumbOff, bw-tw-in, b.ThumbLen, tw)
		}
		c.NextBG(theme.WithAlpha(r.Pal.ScrollThumb, b.Show))
		c.NextRadius(tw / 2)
		c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(b.Key, 1), nil)
		c.Pop()
	}
}
