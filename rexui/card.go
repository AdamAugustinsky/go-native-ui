package rexui

import (
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// CardVariant selects a card's look.
type CardVariant uint8

const (
	// CardBox is a group box (NSBox on macOS 27.2): a filled rounded
	// rectangle, radius 12, no border, with an optional title above it.
	CardBox CardVariant = iota
	// CardInset is a grouped form section: CardBox's shape, holding rows
	// (Card.Row) with hairlines between them, as System Settings draws.
	CardInset
	// CardRex is a Rex pane: the theme's surface, opaque with layered
	// shadows when focused, glass over the window's field when not. Its size
	// comes from the kit's NextSize (the parent's by default), as a pane's
	// does, not from its content.
	CardRex

	// NumCardVariants is the number of variants.
	NumCardVariants
)

// CardOpts are a card's options, passed by value.
type CardOpts struct {
	// Variant is the look.
	Variant CardVariant
	// Title is shown above a CardBox (empty: none).
	Title string
	// Slot is a CardRex's slot in the chrome's cache of surfaces: two Rex
	// cards drawn in one frame need two slots (theme.Surface.Slot).
	Slot int
	// Focus is how focused a CardRex is, 0 to 1 (animate it, as a pane's).
	Focus float32
}

// CardRecipe is the look of cards.
type CardRecipe struct {
	// Fill is a box's fill (theme.GroupBox).
	Fill Tint
	// Pad is the space inside a box and a Rex card, RowPadX and RowPadY that
	// inside a form row, TitleX the title's indent and TitleH the height of
	// the band above the box the title takes, in points (TitleX 10 and
	// TitleH 12 measured: the title's line, 14 pt, reaches into the box).
	Pad, RowPadX, RowPadY, TitleX, TitleH float32
	// RowH is a form row's height in points: 40, with the label 10 pt from
	// the leading edge and the control 10 pt from the trailing one, as
	// tools/appkitscenes' Settings form lays it out and AppKitRef's form-group
	// sample measures.
	RowH float32
	// Title colours a box's title, set in TitleStyle.
	Title      Tint
	TitleStyle theme.TextStyle
	// Divider colours the lines between form rows, DividerW thick in points:
	// a full-width NSBox separator in the 1 pt between two rows (a row's pitch
	// is 41).
	Divider  Tint
	DividerW float32
}

// CardLook is the cards' recipe.
var CardLook = CardRecipe{
	Fill: Of(theme.GroupBox),
	Pad:  12, RowPadX: 10, RowPadY: 0, TitleX: 10, TitleH: 12, RowH: 40,
	Title: Of(theme.Label), TitleStyle: theme.StyleSubheadline,
	Divider: Of(theme.Separator), DividerW: 1,
}

// Card is a card being built: BeginCard returns it, Row adds a form row to a
// CardInset, EndCard closes it.
type Card struct {
	variant CardVariant
	outer   ui.ID
	key     ui.Key
	rows    int
	surf    theme.Surface
	kg      theme.KeyGen
	rex     bool // the Rex surface was begun
}

// BeginCard starts a card and makes its content area the parent: a column
// with the theme's gap. Close it with EndCard.
//
//	cd := rexui.BeginCard(k, "network", rexui.CardOpts{Variant: rexui.CardInset})
//	cd.Row(k, "wifi") // 40 pt high; Form builds the label and the control's place
//	rexui.Label(k, "Wi-Fi", rexui.LabelOpts{})
//	k.Spacer()
//	rexui.Switch(k, "Wi-Fi", &v.wifi, rexui.SwitchOpts{})
//	cd.EndRow(k)
//	rexui.EndCard(k, &cd)
func BeginCard(k *base.Kit, label string, o CardOpts) Card {
	c, r := k.Context(), k.Theme()
	rc := &CardLook
	cd := Card{variant: o.Variant % NumCardVariants}
	if cd.variant == CardRex {
		k.ApplySize(ui.Pct(1), ui.Pct(1))
		cd.outer = c.Box(0, label)
		cd.key = c.KeyOf(cd.outer)
		c.Push(cd.outer)
		rr := c.Rect(cd.outer)
		w, h := rr[2]-rr[0], rr[3]-rr[1]
		if r.Chrome != nil && w > 0 && h > 0 {
			cd.surf = theme.Surface{Key: ui.KeyN(cd.key, 1), Slot: o.Slot, Rect: [4]float32{0, 0, w, h},
				Origin: [2]float32{rr[0], rr[1]}, Focus: o.Focus, Opacity: 1}
			r.Chrome.SurfaceShadow(c, r, cd.surf)
			cd.kg = r.Chrome.BeginSurface(c, r, cd.surf)
			cd.rex = true
		}
		// the surface's fill and edges are floating boxes, which draw above
		// the flow: the content is a floating box too, built after them
		theme.At(c, 0, 0, w, h)
		p := k.Pt(rc.Pad)
		c.NextPadding(p, p, p, p)
		c.NextAxis(ui.AxisY)
		c.NextGap(r.Px.Gap)
		c.Push(c.BoxKeyed(base.Floating, ui.KeyN(cd.key, 2), nil))
		return cd
	}
	k.NextGap(0)
	cd.outer = k.BeginContainer(ui.AxisY, ui.CrossStart, label, ui.Pct(1), ui.Children(1), 0)
	cd.key = c.KeyOf(cd.outer)
	if cd.variant == CardBox && o.Title != "" {
		st := &r.Styles[rc.TitleStyle%theme.NumTextStyles]
		c.NextSize(ui.Pct(1), ui.Px(theme.Snap(k.Pt(rc.TitleH))))
		c.BoxKeyed(0, ui.KeyN(cd.key, 5), nil)
		c.NextPos(k.Pt(rc.TitleX), 0)
		c.NextSize(ui.TextSize(base.LabelSlack), ui.Px(st.Line))
		c.NextFont(st.Font)
		c.NextTextColor(rc.Title.RGBA(r))
		c.NextRole(ui.RoleLabel)
		c.NextSemName(o.Title)
		c.BoxKeyed(ui.DrawText|base.Floating, ui.KeyN(cd.key, 3), base.StrBytes(o.Title))
	}
	p := k.Pt(rc.Pad)
	if cd.variant == CardInset {
		p = 0
	}
	c.NextBG(rc.Fill.RGBA(r))
	c.NextRadius(r.Window.GroupRadius)
	c.NextPadding(p, p, p, p)
	c.NextAxis(ui.AxisY)
	c.NextGap(r.Px.Gap)
	if cd.variant == CardInset {
		c.NextGap(0)
	}
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.Push(c.BoxKeyed(ui.DrawBackground, ui.KeyN(cd.key, 4), nil))
	return cd
}

// Row starts a row of a CardInset (a line separates it from the row before):
// a row of the controls built until EndRow, RowH high, padded at the sides,
// its children centred vertically. label identifies the row among the card's
// rows. It returns the row's box.
func (cd *Card) Row(k *base.Kit, label string) ui.ID { return cd.RowHeight(k, label, 0) }

// RowHeight is Row for a row of height pt points (0: the recipe's RowH), for a
// row whose control is taller than a row.
func (cd *Card) RowHeight(k *base.Kit, label string, pt float32) ui.ID {
	cd.separate(k)
	return cd.row(k, label, pt)
}

// separate builds the line that separates a row from the row before it. It
// comes before the row's own Next… settings (role, name, gap), which belong to
// the row and not to the line.
func (cd *Card) separate(k *base.Kit) {
	c, r := k.Context(), k.Theme()
	rc := &CardLook
	if cd.rows > 0 {
		c.NextSize(ui.Pct(1), ui.Px(theme.Snap(k.Pt(rc.DividerW))))
		c.NextBG(rc.Divider.RGBA(r))
		c.BoxKeyed(ui.DrawBackground, ui.KeyN(cd.key, 100+uint64(cd.rows)), nil)
	}
}

// row builds the row itself, after its separator.
func (cd *Card) row(k *base.Kit, label string, pt float32) ui.ID {
	rc := &CardLook
	cd.rows++
	if pt <= 0 {
		pt = rc.RowH
	}
	k.NextPadding(rc.RowPadX, rc.RowPadY, rc.RowPadX, rc.RowPadY)
	k.NextHeight(k.Fixed(pt))
	return k.Row(label)
}

// ID is the card's outermost box.
func (cd *Card) ID() ui.ID { return cd.outer }

// EndRow ends the row Row started.
func (cd *Card) EndRow(k *base.Kit) { k.EndRow() }

// EndCard closes the card BeginCard started.
func EndCard(k *base.Kit, cd *Card) {
	c, r := k.Context(), k.Theme()
	c.Pop() // the content
	if cd.variant == CardRex {
		if cd.rex {
			r.Chrome.EndSurface(c, r, cd.surf, cd.kg)
		}
		c.Pop() // the outer box
		return
	}
	k.EndContainer()
}
