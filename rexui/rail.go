package rexui

import (
	"gonativeui/icon"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// The rail is an app's leftmost column of destinations, as ChatGPT and the Codex app have it: a
// narrow strip of icon-only buttons (the home, the lists, the plugins), the current one filled,
// every one named by a tooltip, with the settings and the account pushed to its foot. It stays
// when the sidebar beside it goes away.
//
//	rexui.BeginRail(k, "rail")
//	if rexui.RailItem(k, ic.chats, "Conversas", rexui.RailItemOpts{Selected: here == chats}).Clicked {
//		here = chats
//	}
//	rexui.RailRule(k)
//	… one item per plugin …
//	rexui.RailFill(k)
//	rexui.RailItem(k, ic.gear, "Configurações", rexui.RailItemOpts{Tip: "Configurações  ⌘,"})
//	rexui.EndRail(k)

// RailRecipe is the rail's look. Lengths are points.
type RailRecipe struct {
	// Width is the rail's width; Item an item's side and Radius its corners;
	// Gap the space between items and PadY the space above the first and
	// under the last.
	Width, Item, Radius, Gap, PadY float32
	// Hover, Pressed and Selected fill an item under the pointer, pressed
	// and current; Label and LabelSelected colour its symbol (the current one
	// in the stronger colour).
	Hover, Pressed, Selected Tint
	Label, LabelSelected     Tint
	// Rule is RailRule's colour, RuleW its length.
	Rule  Tint
	RuleW float32
	// Badge is the dot at an item's top right corner (RailItemOpts.Badge),
	// Dot its diameter.
	Badge Tint
	Dot   float32
}

// RailLook is the rails' recipe: the list rows' roles (an item is a destination, as a row is),
// on the 4-pt grid.
var RailLook = RailRecipe{
	Width: 56, Item: 40, Radius: 10, Gap: 4, PadY: 8,
	Hover: Of(theme.RowHover), Pressed: Of(theme.RowPressed), Selected: Of(theme.RowSelected),
	Label: Of(theme.Label2), LabelSelected: Of(theme.Label),
	Rule: Of(theme.Separator), RuleW: 24,
	Badge: Of(theme.Accent), Dot: 8,
}

// RailItemOpts are an item's options, passed by value.
type RailItemOpts struct {
	// Selected marks the current destination (filled, and SemSelected for
	// assistive technologies).
	Selected bool
	// Badge draws a dot at the item's top right corner: something new there.
	Badge bool
	// Tip is the tooltip ("": the label's display text), say with the
	// shortcut.
	Tip string
}

// BeginRail opens the rail: a column RailLook.Width wide, as high as its parent, its items
// centred. Close it with EndRail.
func BeginRail(k *base.Kit, id string) {
	c := k.Context()
	rc := &RailLook
	c.NextSize(ui.Px(theme.Snap(k.Pt(rc.Width))), ui.Pct(1))
	c.NextAxis(ui.AxisY)
	c.NextCross(ui.CrossCenter)
	c.NextGap(k.Pt(rc.Gap))
	c.NextPadding(0, k.Pt(rc.PadY), 0, k.Pt(rc.PadY))
	c.NextRole(ui.RoleToolbar)
	_, ident := ui.SplitLabel(id)
	c.NextSemName(ident)
	c.Push(c.Box(0, id))
}

// EndRail closes the rail BeginRail opened.
func EndRail(k *base.Kit) { k.Context().Pop() }

// RailFill takes the rail's free height: the items after it sit at its foot.
func RailFill(k *base.Kit) {
	c := k.Context()
	c.NextSize(ui.Px(1), ui.Grow(1))
	c.Box(0, "rail fill")
}

// RailRule is a short hairline between groups of items.
func RailRule(k *base.Kit) {
	c, r := k.Context(), k.Theme()
	rc := &RailLook
	c.NextSize(ui.Px(theme.Snap(k.Pt(rc.RuleW))), ui.Px(theme.Snap(r.Lines.Hairline)))
	c.NextBG(rc.Rule.RGBA(r))
	c.Box(ui.DrawBackground, "rail rule")
}

// RailItem builds an item: the icon ic centred on a rounded square, label its accessible name
// (ui's label rules) and its tooltip unless o.Tip says otherwise. It reports the frame's signal.
func RailItem(k *base.Kit, ic icon.ID, label string, o RailItemOpts) base.Signal {
	c, r := k.Context(), k.Theme()
	rc := &RailLook
	p := k.PressState(label, base.HoverOn)
	var fill uint32
	switch {
	case o.Selected:
		fill = rc.Selected.RGBA(r)
	case p.Visual.Down > 0:
		fill = rc.Pressed.RGBA(r)
	case p.Visual.Hot > 0:
		fill = theme.ColA(rc.Hover.RGBA(r)>>8, float32(rc.Hover.RGBA(r)&0xFF)/255*p.Visual.Hot)
	}
	fg := rc.Label.RGBA(r)
	if o.Selected || p.Visual.Hot > 0 {
		fg = mix(fg, rc.LabelSelected.RGBA(r), max(p.Visual.Hot, b2f(o.Selected)))
	}
	side := theme.Snap(k.Pt(rc.Item))
	radius := k.Pt(rc.Radius)
	var flags ui.Flags
	if fill&0xFF != 0 {
		flags |= ui.DrawBackground
	}
	c.NextBG(fill)
	c.NextRadius(radius)
	if o.Selected {
		c.NextSemState(ui.SemSelected)
	}
	k.BeginPress(&p, ui.RoleButton, ui.Px(side), ui.Px(side), flags, nil)
	if f, disp := k.Icons().Prepare(c, ic, fg); f != 0 {
		c.NextSize(ui.Pct(1), ui.Pct(1))
		c.BoxKeyed(f, ui.KeyN(p.Key, 1), disp)
	}
	if o.Badge {
		d := theme.Snap(k.Pt(rc.Dot))
		c.NextAnchor(ui.AnchorTopRight, k.Pt(6), k.Pt(6))
		c.NextSize(ui.Px(d), ui.Px(d))
		c.NextRadius(d / 2)
		c.NextBG(rc.Badge.RGBA(r))
		c.NextAuditExempt(ui.AuditCovered.Mask()) // the dot lies over the symbol's corner on purpose
		c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(p.Key, 2), nil)
	}
	k.FocusRing(p.ID, p.Visual, radius)
	sig := k.EndPress(&p)
	tip := o.Tip
	if tip == "" {
		tip, _ = ui.SplitLabel(label)
	}
	Tooltip(k, sig.ID, tip)
	return sig
}
