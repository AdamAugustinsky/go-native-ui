package rexui

import (
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// OutlineOpts are an outline's options, passed by value.
type OutlineOpts struct {
	// Style is the outline's style.
	Style ListStyle
	// Striped alternates the rows' background.
	Striped bool
	// Glyphs are the disclosures (AddListGlyphs; zero: the standard
	// chevrons).
	Glyphs ListGlyphs
}

// OutlineRecipe is the look of an outline's indentation and disclosures (macOS
// 27.2, NSOutlineView): each level is indented by the theme's Lines.Indent
// (13); the disclosure, a chevron pointing right or down, sits in a box
// DisclosureW wide DisclosureX from the level's start, and the row's content
// starts after it.
type OutlineRecipe struct {
	// DisclosureX and DisclosureW place the disclosure in its level, in
	// points (frameOfOutlineCellAtRow: x 2, width 13). The chevron sits
	// DisclosureDX right of the box's middle and DisclosureDY below the
	// row's, collapsed and expanded (measured from AppKit's ink).
	DisclosureX, DisclosureW, DisclosureDX float32
	DisclosureDY                           [2]float32
	// ContentX is where the row's content starts in its level, in points
	// (the outline column's cell, at 15), and TextPad the room before its
	// text (the cell view's 2).
	ContentX, TextPad float32
	// Disclosure colours the chevron (measured #8C8C8C-ish ink over white:
	// tertiaryLabelColor reads lighter), DisclosureDisabled when disabled;
	// on an emphasised selection it is the selection's text colour.
	Disclosure, DisclosureDisabled Tint
}

// OutlineLook is the outlines' recipe.
var OutlineLook = OutlineRecipe{
	DisclosureX: 2, DisclosureW: 13, DisclosureDX: 0.5, DisclosureDY: [2]float32{0.75, 1.75}, ContentX: 15, TextPad: 2,
	Disclosure: Of(theme.Label2), DisclosureDisabled: Of(theme.TextDisabled),
}

// Outline is an open outline: base's, with the look of its rows (a List's).
type Outline struct {
	base.Outline
	rowLook
	glyphs ListGlyphs
	inCell bool
}

// BeginOutline starts an outline of the tree t with the state st:
//
//	ol := rexui.BeginOutline(k, "files", &v.tree, &v.nodes, rexui.OutlineOpts{})
//	for r := ol.First; r < ol.Last; r++ {
//		node := ol.Row(r, v.name(r))
//		rexui.LabelBytes(k, v.nameOf(node), rexui.LabelOpts{Color: ol.Text()})
//	}
//	rexui.EndOutline(k, &ol)
//
// Rows look like a list's (ListLook); expanding and collapsing are base's
// (base.Kit.BeginOutline): Right and Left, with Option the whole subtree,
// and a click on the disclosure.
func BeginOutline(k *base.Kit, label string, st *base.OutlineState, t *base.Tree, o OutlineOpts) Outline {
	s := o.Style % NumListStyles
	return Outline{Outline: k.BeginOutline(label, st, t, listOpts(k, ListOpts{Style: o.Style, Striped: o.Striped}, false)),
		rowLook: rowLook{style: s, striped: o.Striped}, glyphs: o.Glyphs.orStandard()}
}

// Row starts display row r: the row in the list look, its indentation, its
// disclosure if it has children, and a box for its content after them, which
// it pushes (its children flow left to right, starting TextPad in). It ends
// the row before. It returns the node the row shows.
func (ol *Outline) Row(r int, name []byte) uint32 {
	k := ol.Kit()
	c, rs0 := k.Context(), k.Theme()
	ol.CloseRow()
	rs := ol.RowState(r)
	rc := &OutlineLook
	ol.begin(&ol.List, &rs, name)
	sc := k.Scale()
	level := float32(rs.Level) * rs0.Lines.Indent
	rh := ol.RowH()
	if rs.Expandable {
		col := rc.Disclosure.RGBA(rs0)
		switch {
		case rs.Disabled:
			col = rc.DisclosureDisabled.RGBA(rs0)
		case rs.Selected && rs.Emphasised:
			col = ol.fg
		}
		ic, dy := ol.glyphs.Collapsed, rc.DisclosureDY[0]
		if rs.Expanded {
			ic, dy = ol.glyphs.Expanded, rc.DisclosureDY[1]
		}
		dw := pt(sc, rc.DisclosureW)
		ol.Disclosure(&rs, theme.Snap(level+k.Pt(rc.DisclosureX)), 0, dw, rh, 0)
		if f, disp := k.Icons().Prepare(c, ic, col); f != 0 {
			iw, ih := k.IconSize(ic)
			theme.At(c, theme.Snap((dw-iw)/2+k.Pt(rc.DisclosureDX)), theme.Snap((rh-ih)/2+k.Pt(dy)), iw, ih)
			c.BoxKeyed(f|base.Floating, ui.KeyN(rs.Key, 0xD15D), disp)
		}
		c.Pop()
	}
	x := theme.Snap(level + k.Pt(rc.ContentX))
	c.NextSize(ui.Px(x), ui.Pct(1))
	c.BoxKeyed(0, ui.KeyN(rs.Key, 0x1DE7), nil)
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextAxis(ui.AxisX)
	c.NextGap(k.Theme().Px.GapSmall)
	c.NextPadding(pt(sc, rc.TextPad), 0, 0, 0)
	c.Push(c.BoxKeyed(ui.Clip, ui.KeyN(rs.Key, 0xC0), nil))
	ol.inCell = true
	return rs.Item
}

// CloseRow ends the row being built, if one is open.
func (ol *Outline) CloseRow() {
	if ol.inCell {
		ol.Kit().Context().Pop()
		ol.inCell = false
	}
	ol.List.CloseRow()
}

// TextBytes builds the row's text: one line of body text in the row's colour,
// fading out where it does not fit.
func (ol *Outline) TextBytes(b []byte) {
	k := ol.Kit()
	c := k.Context()
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextFont(k.Theme().Styles[theme.StyleBody].Font)
	c.NextTextColor(ol.TextColor())
	c.BoxKeyed(ui.DrawText|ui.FadeOverflowX, ui.KeyN(c.ParentKey(), 1), b)
}

// EndOutline ends the outline: the stripes below the rows, the rows, the
// scroll bar and the outline.
func EndOutline(k *base.Kit, ol *Outline) {
	ol.CloseRow()
	ol.filler(&ol.List)
	ol.CloseRows()
	v := ol.ScrollView()
	OverlayBars(k, &v)
	k.CloseList(&ol.List)
}
