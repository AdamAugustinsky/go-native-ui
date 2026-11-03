package rexui

import (
	"gonativeui/icon"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
	"gonativeui/widget/workspace"
)

// A pane card is one of Rex's panes floating on the field: the theme's pane
// surface (its material, shadow, hairline and corners, theme.Chrome) with
// the window's content in it, opaque while it is the pane in use and glass
// while another is. Zap and A3 each built it; this is theirs, on the motion
// tokens (D95).

// PaneCardOpts are a pane card's options, passed by value.
type PaneCardOpts struct {
	// Name is the card's accessible name (a group).
	Name string
	// Layout and Pane place the card where the workspace laid pane Pane out
	// (Layout.Rect, in the coordinates of the box the card is built in,
	// usually the window's) and take its key, surface slot, focus and
	// semantics from the layout (Layout.NextPane): call Layout.Update with
	// Keys.PressAnywhere before building, so a press anywhere in a card
	// focuses it. Without a Layout the card fills the box the kit's next size
	// gives (the parent's by default) and Key, Slot and Focused say the rest.
	Layout *workspace.Layout
	Pane   workspace.PaneID
	// Key is the card's box (a stable key of the app's); Slot its surface
	// slot (theme.Surface.Slot): two cards drawn in one frame need two.
	Key  ui.Key
	Slot int
	// Focused says the card is the pane in use; Focus, when not nil, says it
	// instead (PaneFocus).
	Focused bool
	Focus   *PaneFocus
	// Rest is how focused the card is drawn while another pane is in use, 0
	// to 1: 0 is Rex's glass, more keeps more of the surface opaque so that
	// a busy desktop never shows through its text.
	Rest float32
	// Full, when not 0, is the content's width in pixels while the card
	// glides in or away (theme.Glide) and is narrower than its content: the
	// card's edge sweeps over the content, which keeps its width, clipped.
	Full float32
	// Still draws the card as the pane in use at once: a still render.
	Still bool
	// Flat, when not nil, draws the card as a web app's region rather than
	// Rex's floating glass (FlatPane): the app lays its flat cards edge to
	// edge, with no inset or gutter.
	Flat *FlatPane
}

// FlatPane is a pane card drawn flat, the way a web app draws its sidebar
// and its main column: Fill from edge to edge, square, with no shadow,
// glass, rim or focus dimming, and a hairline in Line along Sides (a
// sidebar's right edge). The card still clips its content, takes the focus
// presses and names its group.
type FlatPane struct {
	// Fill is the region's colour (None: the window's field shows).
	Fill Tint
	// Line is the hairline's colour, drawn the theme's border width inside
	// the card along Sides (none when Sides is 0 or Line is None).
	Line  Tint
	Sides ui.Sides
	// Drag, when not 0, makes the card's top Drag points move the window
	// (a web app's titlebar region: Electron's -webkit-app-region: drag),
	// for a window whose titlebar band lies under its cards. What the card
	// builds there keeps its clicks.
	Drag float32
}

// PaneFocus is the pane card in use for an app that lays its cards out in
// the flow (a splitter, a sidebar) rather than in a workspace.Layout: a
// press in a card's box focuses it (Update), and the cards built with it read
// whether they are the one.
type PaneFocus struct {
	// Key is the card in use (PaneCardOpts.Key).
	Key ui.Key
}

// Update focuses the card among keys that the previous frame's press landed
// in (any button), as Rex focuses a pane on a click, whatever box inside it
// took the press. Call it before building the cards. It reports whether the
// focus moved.
func (f *PaneFocus) Update(c *ui.Context, keys ...ui.Key) bool {
	in := c.Input()
	if !in.Inside || !(in.Pressed[ui.ButtonLeft] || in.Pressed[ui.ButtonRight] || in.Pressed[ui.ButtonMiddle]) {
		return false
	}
	for _, k := range keys {
		r := c.RectOf(k)
		if r[2] > r[0] && in.MouseX >= r[0] && in.MouseX < r[2] && in.MouseY >= r[1] && in.MouseY < r[3] {
			moved := f.Key != k
			f.Key = k
			return moved
		}
	}
	return false
}

// PaneCard is a pane card being built: BeginPaneCard returns it and
// EndPaneCard closes it.
type PaneCard struct {
	// ID is the card's box; Rect its rectangle in its parent's coordinates
	// as the previous frame laid it out.
	ID   ui.ID
	surf theme.Surface
	kg   theme.KeyGen
	// drawn: the surface was built; sweep: the content is laid at Full and
	// clipped
	drawn, sweep bool
	flat         *FlatPane
}

var paneFocusBase = ui.HashString(base.KeyRoot, "rexui/pane focus")

// BeginPaneCard opens a pane card and, in it, the content: a column from
// edge to edge (a PaneHeader and the body pad themselves). Its focus eases
// toward whether it is the pane in use at the theme's dimming rate
// (Motion.Dim), at once under Reduce Motion. Close it with EndPaneCard.
//
//	pc := rexui.BeginPaneCard(k, rexui.PaneCardOpts{Name: "Chat", Key: chatKey, Focus: &v.focus, Rest: 0.82})
//	rexui.BeginPaneHeader(k, "head")
//	rexui.PaneTitle(k, "Chat")
//	rexui.EndPaneHeader(k)
//	… the pane …
//	rexui.EndPaneCard(k, &pc)
func BeginPaneCard(k *base.Kit, o PaneCardOpts) PaneCard {
	c, r := k.Context(), k.Theme()
	key, slot, focused := o.Key, o.Slot, o.Focused
	if o.Focus != nil {
		focused = o.Focus.Key == key
	}
	var flags ui.Flags
	if l := o.Layout; l != nil {
		key, slot, focused = l.PaneKey(o.Pane), l.Slot(o.Pane), l.Focus() == o.Pane
		pr := l.Rect(o.Pane)
		theme.At(c, pr[0], pr[1], pr[2]-pr[0], pr[3]-pr[1])
		flags = base.Floating | ui.Clickable
		l.NextPane(c, o.Pane, o.Name)
	} else {
		k.ApplySize(ui.Pct(1), ui.Pct(1))
		c.NextRole(ui.RoleGroup)
		c.NextSemName(o.Name)
	}
	if o.Flat != nil {
		flags |= ui.Clip | ui.Clickable // what Rex's surface box is
	}
	outer := c.BoxKeyed(flags, key, nil)
	c.Push(outer)
	pc := PaneCard{ID: outer, flat: o.Flat}
	rr := c.Rect(outer)
	w, h := rr[2]-rr[0], rr[3]-rr[1]
	target := o.Rest
	if focused && !k.Inactive() || o.Still {
		target = 1
	}
	rate := r.Motion.Dim
	if o.Still || reduced(k) {
		rate = theme.Instant
	}
	if f := o.Flat; f != nil {
		if fill := f.Fill.RGBA(r); fill != 0 {
			theme.At(c, 0, 0, w, h)
			c.NextBG(fill)
			c.Box(base.Floating|ui.DrawBackground, "flat fill")
		}
		if f.Drag > 0 {
			theme.At(c, 0, 0, w, min(theme.Snap(k.Pt(f.Drag)), h))
			c.Box(base.Floating|ui.WindowDrag, "flat drag")
		}
	} else if r.Chrome != nil && w > 0 && h > 0 {
		pc.surf = theme.Surface{Key: ui.KeyN(paneFocusBase, uint64(key)+1), Slot: slot, Rect: [4]float32{0, 0, w, h},
			Origin: [2]float32{rr[0], rr[1]}, Focus: c.AnimateValue(ui.KeyN(paneFocusBase, uint64(key)), target, rate), Opacity: 1, Top: true}
		r.Chrome.SurfaceShadow(c, r, pc.surf)
		pc.kg = r.Chrome.BeginSurface(c, r, pc.surf)
		pc.drawn = true
	}
	// the surface's fill and edges are floating boxes, which draw above the
	// flow: the content is a floating box too, built after them
	theme.At(c, 0, 0, w, h)
	if o.Full > 0 {
		pc.sweep = true
		c.NextRadius(r.Px.RadiusLarge)
		c.Push(c.Box(base.Floating|ui.Clip, "sweep"))
		c.NextSize(ui.Px(o.Full), ui.Pct(1))
	}
	c.NextAxis(ui.AxisY)
	c.Push(c.BoxKeyed(base.Floating, ui.KeyN(paneFocusBase, uint64(key)+2), nil))
	return pc
}

// EndPaneCard closes the card BeginPaneCard opened.
func EndPaneCard(k *base.Kit, pc *PaneCard) {
	c, r := k.Context(), k.Theme()
	c.Pop() // the content
	if pc.sweep {
		c.Pop()
	}
	if pc.drawn {
		r.Chrome.EndSurface(c, r, pc.surf, pc.kg)
	}
	if f := pc.flat; f != nil && f.Sides != 0 {
		if line := f.Line.RGBA(r); line != 0 {
			rr := c.Rect(pc.ID)
			theme.At(c, 0, 0, rr[2]-rr[0], rr[3]-rr[1])
			c.NextBorder(line, r.Px.BorderW)
			c.NextBorderSides(f.Sides)
			c.Box(base.Floating|ui.DrawBorder, "flat line")
		}
	}
	c.Pop() // the card
}

// PaneHeaderRecipe is a pane card's title row, in points: Height tall, the
// title PadL from the card's leading edge, the actions PadR from its
// trailing one, Gap apart.
type PaneHeaderRecipe struct {
	// Height is the row's height.
	Height float32
	// PadL and PadR are the space at its leading and trailing ends.
	PadL, PadR float32
	// Gap is the space between its parts.
	Gap float32
}

// PaneHeaderLook is the pane headers' recipe: 36 pt, on the 4-point grid.
var PaneHeaderLook = PaneHeaderRecipe{Height: 36, PadL: theme.Space3, PadR: theme.Space2, Gap: theme.Space1}

// BeginPaneHeader opens a pane card's title row (Rex's: the title at the
// leading edge, PaneTitle, and bare symbol actions at the trailing one,
// PaneAction), a row as wide as the card. Close it with EndPaneHeader.
func BeginPaneHeader(k *base.Kit, id string) {
	rc := &PaneHeaderLook
	k.NextWidth(ui.Pct(1))
	k.NextHeight(k.Fixed(rc.Height))
	k.NextPadding(rc.PadL, 0, rc.PadR, 0)
	k.NextGap(rc.Gap)
	k.NextCross(ui.CrossCenter)
	k.Row(id)
}

// EndPaneHeader closes the row BeginPaneHeader opened.
func EndPaneHeader(k *base.Kit) { k.EndRow() }

// PaneTitle is a pane header's title, taking the room the actions leave.
func PaneTitle(k *base.Kit, title string) {
	display, _ := ui.SplitLabel(title)
	k.NextWidth(ui.Grow(1))
	Label(k, k.Context().Fmt().Str(display).Str("###title").String(), LabelOpts{Style: TextSemibold})
}

// PaneActionOpts are a pane action's options.
type PaneActionOpts struct {
	// Tip is the tooltip ("": the label).
	Tip string
	// Recipe, when not nil, draws the action in it, bordered: a toggle that
	// is on (ToggleOnLook).
	Recipe *ButtonRecipe
}

// PaneAction is a pane header's action: a bare symbol (Rex's split, expand,
// close) pressed in the tool's round hit area (theme.HitTool), its label the
// accessible name and the tooltip.
func PaneAction(k *base.Kit, ic icon.ID, label string, o PaneActionOpts) base.Signal {
	hit := theme.Snap(k.Pt(theme.HitTool))
	k.NextSize(ui.Px(hit), ui.Px(hit))
	io := IconButtonOpts{Variant: IconBorderless, Size: theme.SizeSmall, Recipe: o.Recipe}
	if o.Recipe != nil {
		io.Variant = IconBordered
	}
	sig := IconButton(k, ic, label, io)
	tip := o.Tip
	if tip == "" {
		tip, _ = ui.SplitLabel(label)
	}
	Tooltip(k, sig.ID, tip)
	return sig
}
