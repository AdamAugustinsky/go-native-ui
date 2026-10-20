package rexui

import (
	"gonativeui/icon"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// The Toolbar is a window's titlebar band and its items, laid out as macOS
// 27.2 lays out an NSToolbar (docs/design/liquid-glass-reference.md §1): the
// items are glass capsules in a transparent band, the window's own traffic
// lights at the style's place, the title after the capsule before it. The
// band shows a bar and a hairline only while content is scrolled under it or
// the window is inactive (§4, §7). The band moves the window: its empty parts
// drag it, and a double click there does what the system setting says
// (platform), while the items keep their presses.
//
// The window gets the style's titlebar height and traffic-light centre from
// TitlebarFor, through app.Config.Titlebar and TrafficLights.

// TitlebarFor is what the toolbar style s asks of the window: the titlebar
// height (app.Config.Titlebar) and the close button's centre
// (app.Config.TrafficLights), in points. AppKit places the buttons from these
// (platform, D53); the spacing between them stays AppKit's. This is the one
// place the window-chrome tokens become window options.
//
//	cfg := app.Config{Title: "Files", Width: 1120, Height: 640}
//	cfg.Titlebar, cfg.TrafficLights = rexui.TitlebarFor(theme.ToolbarUnified)
func TitlebarFor(s theme.ToolbarStyle) (heightPt float32, trafficPt [2]float32) {
	if s >= theme.NumToolbarStyles {
		s = theme.ToolbarPlain
	}
	t := &theme.MacWindow.Toolbars[s]
	return t.Height, t.Traffic
}

// ToolbarOpts are a toolbar's options, passed by value.
type ToolbarOpts struct {
	// Style is the toolbar style: Plain (32), Compact (40), Unified (52) or
	// Rex (46, Rex's band: borderless items, no capsules).
	Style theme.ToolbarStyle
	// Title and Subtitle are the window's title, placed by Title (15 pt bold
	// in a unified toolbar, 13 pt bold otherwise) and an optional line
	// below it.
	Title, Subtitle string
	// Panes are the window pixels where the panes under the toolbar meet:
	// the sidebar's trailing edge and the inspector's leading edge (0: no such
	// pane). NextPane moves the items on to the next pane, as an NSToolbar's
	// tracking separators do.
	Panes [2]float32
	// ContentUnder says content is scrolled under the toolbar: the band shows
	// its bar and hairline (the scroll-edge effect), as it always does while
	// the window is inactive.
	ContentUnder bool
	// Pocket is how far below the toolbar, in pixels, the scroll edge
	// reaches: a table's header pinned under the toolbar is in it (§4: the
	// pocket ends at the header's bottom, 80 = 52 + 28), so the bar covers
	// the header and the hairline lies below it. Build the header after the
	// toolbar, so that it draws on the bar.
	Pocket float32
	// Traffic draws the traffic lights at the style's place (offscreen
	// renders; in a window the system draws them).
	Traffic bool
}

// ToolbarRecipe is the toolbar's look. Lengths are in points.
type ToolbarRecipe struct {
	// Glyph colours an item's symbol: labelColor, which the glass's
	// vibrancy turns black (white in dark), as a sidebar row's text
	// (measured #000000 and #FFFFFF); GlyphInactive in an inactive window
	// (measured #A6A6A6 and #666666: lighter than the dimmed title);
	// GlyphDisabled when disabled.
	Glyph, GlyphDisabled Tint
	GlyphInactive        Pair
	// Hover and Press are laid over a capsule's glass under the pointer and
	// while pressed (ButtonToolbar's).
	Hover, Press Tint
	// Rim, RimInactive and Highlight are the capsule's outermost pixel and
	// its bright bottom rim (dark mode; none while inactive).
	Rim, RimInactive, Highlight Tint
	// Title and TitleInactive colour the title (§1, §7); Subtitle the line
	// below it.
	Title, TitleInactive, Subtitle Tint
	// Divider is the line between back and forward, DividerH its height.
	Divider  Tint
	DividerH float32
	// Selected fills the chosen segment of an expanded picker.
	Selected Tint
	// SegmentW is an expanded picker's segment (156 for 4, §1).
	SegmentW float32
	// ItemPad is the room a one-symbol item keeps on each side of its
	// symbol, GroupPad that of an item in a group ([0] compact, [1]
	// unified), ItemMin the narrowest item ([0] compact, [1] unified: a
	// circle). Measured on 27.2 (§1): a compact item is its image and 12
	// (27 for trash, 31.5 for sidebar.left), a unified one at least 36;
	// inside a group, the image and 17–18 (compact) or 21–22.5 (unified).
	// The symbols are 13 pt in both styles (AppKit's images: 15–20 wide).
	ItemPad  float32
	GroupPad [2]float32
	ItemMin  [2]float32
	// MenuPad is the room a menu item keeps besides its symbol (the
	// chevron and the sides), MenuMin its narrowest (44.5 for
	// ellipsis.circle, 47.5 for a 20-wide image).
	MenuPad, MenuMin float32
	// SearchGap is the gap before a search item, compact and unified (§1:
	// 10 and 8); SearchW an expanded search field's width.
	SearchGap [2]float32
	SearchW   float32
	// RexGlyph, RexGlyphHover and RexFill are the Rex band's borderless
	// items (rexlook's ⌘ and +), RexHit their side, RexRadius their corner.
	RexGlyph, RexGlyphHover, RexFill Pair
	RexHit, RexRadius                float32
	// Traffic are the drawn traffic lights (offscreen only), active, and
	// TrafficInactive all three in an inactive window (§7).
	Traffic         [3]Pair
	TrafficInactive Pair
}

// ToolbarLook is the toolbar's recipe.
var ToolbarLook = ToolbarRecipe{
	Glyph: Of(theme.SidebarLabel), GlyphInactive: Solid(0xA6A6A6, 0x666666), GlyphDisabled: Of(theme.TextDisabled),
	Hover: Of(theme.Fill4), Press: Of(theme.Fill2),
	Rim: Of(theme.CapsuleRim), RimInactive: Of(theme.CapsuleRimInactive), Highlight: Of(theme.CapsuleHighlight),
	Title: Of(theme.TitleText), TitleInactive: Of(theme.TitleTextInactive), Subtitle: Of(theme.Label2),
	Divider: Of(theme.Separator), DividerH: 16,
	Selected: Of(theme.Fill2),
	SegmentW: 39, ItemPad: 6, GroupPad: [2]float32{8.5, 10.5}, ItemMin: [2]float32{24, 36},
	MenuPad: 27.5, MenuMin: 44.5,
	SearchGap: [2]float32{10, 8}, SearchW: 166,
	RexGlyph: Solid(0x5F5D62, 0xA3A7B8), RexGlyphHover: Solid(0x1E2227, 0xECEDF3), RexFill: PairA(0x2A2030, 0xFFFFFF, 0.08, 0.08),
	RexHit: 28, RexRadius: 12,
	Traffic:         [3]Pair{Solid(0xF87B72, 0xF35C52), Solid(0xFDC734, 0xFDBF00), Solid(0x7FD457, 0x2CC000)},
	TrafficInactive: Solid(0xEAEAEE, 0x4A4A4A),
}

// what was laid out last in a section: it decides the gap before the next
type tbKind uint8

const (
	tbStart tbKind = iota
	tbCapsule
	tbPicker
	tbTitle
	tbFlex
)

// Toolbar is a toolbar being built: BeginToolbar, the items left to right
// (NextPane between panes), End.
type Toolbar struct {
	k       *base.Kit
	o       ToolbarOpts
	rc      *ToolbarRecipe
	spec    theme.ToolbarSpec
	key     ui.Key
	band    ui.ID
	w       float32
	pane    int     // the section the items go into
	x0      float32 // the section's left edge, window pixels
	x1      float32
	paneX   float32 // the pane's leading edge (the title's inset is from it)
	cur     float32 // pixels laid out in the section so far, until the first flexible item
	fixed   bool    // cur is still exact (no flexible item yet)
	prev    tbKind
	titled  bool // the section has a title: its flexible spaces are the minimum
	row     ui.ID
	group   ui.ID
	inGroup bool
	nGroup  int
	n       uint64
	open    bool
}

// BeginToolbar starts the toolbar of the window at its top: the band, the
// traffic lights if asked, and the first pane's row of items. The label
// identifies it.
//
//	tb := rexui.BeginToolbar(k, "Toolbar", rexui.ToolbarOpts{Style: theme.ToolbarUnified, Title: "Documents", Panes: [2]float32{sidebarW, 0}})
//	if tb.Item(v.sidebarIcon, "Sidebar").Clicked { v.toggleSidebar() }
//	tb.NextPane()
//	back, fwd := tb.Nav(v.back, v.fwd, v.canBack(), v.canForward())
//	tb.Title()
//	tb.Flexible()
//	tb.Picker("View", &v.view, v.viewIcons, v.viewNames, false)
//	tb.Search("Search", &v.query, "Search")
//	tb.End()
func BeginToolbar(k *base.Kit, label string, o ToolbarOpts) Toolbar {
	c, r := k.Context(), k.Theme()
	if o.Style >= theme.NumToolbarStyles {
		o.Style = theme.ToolbarPlain
	}
	tb := Toolbar{k: k, o: o, rc: &ToolbarLook, spec: r.Window.Toolbars[o.Style], open: true}
	_, ident := ui.SplitLabel(label)
	tb.key = ui.HashString(c.ParentKey(), ident)
	w, _ := c.Size()
	tb.w = float32(w)
	h := theme.Snap(tb.spec.Height)

	// the band: clear while nothing is under it in an active window, the
	// scroll edge's bar and hairline otherwise; it moves the window
	bar := o.Style != theme.ToolbarRex && theme.ScrollEdgeVisible(o.ContentUnder, !k.Inactive())
	flags := ui.WindowDrag | base.Floating
	if bar {
		c.NextBG(GlassFill(k, GlassTitlebar))
		flags |= ui.DrawBackground
	}
	theme.At(c, 0, 0, tb.w, h)
	c.NextRole(ui.RoleGroup)
	c.NextSemName(label)
	tb.band = c.BoxKeyed(flags, tb.key, nil)
	c.Push(tb.band)
	if bar {
		// the pocket's bar under a pinned header, and the hairline just below
		// the pocket (y 52..52.5 measured: the line is outside the band)
		pk := theme.Snap(max(o.Pocket, 0))
		if pk > 0 {
			theme.At(c, 0, h, tb.w, pk)
			c.NextBG(GlassFill(k, GlassTitlebar))
			c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(tb.key, 2), nil)
		}
		theme.At(c, 0, h+pk, tb.w, r.Window.ScrollEdge.Line)
		c.NextBG(r.Colors[theme.ScrollEdgeLine])
		c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(tb.key, 1), nil)
	}
	if o.Traffic {
		tb.trafficLights()
	}

	// the first pane starts after the traffic lights: FirstGap after the
	// zoom button (the system's, when it is further right than the style's)
	sp := r.Window.TrafficSpacing
	zoom := tb.spec.Traffic[0] + 2*sp + r.Window.TrafficD/2
	if sb := c.SystemButtons(); sb[2] > zoom {
		zoom = sb[2]
	}
	first := tb.spec.FirstGap
	if first == 0 {
		first = r.Window.Capsule.WideGap
	}
	tb.beginPane(0, theme.Snap(zoom+first), 0)
	return tb
}

// trafficLights draws the window buttons at the style's centres.
func (tb *Toolbar) trafficLights() {
	c, r := tb.k.Context(), tb.k.Theme()
	d := r.Window.TrafficD
	for i := 0; i < 3; i++ {
		p := tb.rc.Traffic[i]
		if tb.k.Inactive() {
			p = tb.rc.TrafficInactive
		}
		cx := tb.spec.Traffic[0] + float32(i)*r.Window.TrafficSpacing
		theme.At(c, theme.Snap(cx-d/2), theme.Snap(tb.spec.Traffic[1]-d/2), theme.Snap(d), theme.Snap(d))
		c.NextBG(p.At(r.T))
		c.NextRadius(d / 2)
		c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(tb.key, 10+uint64(i)), nil)
	}
}

// paneEdges are where pane i of the toolbar lies, window pixels.
func (tb *Toolbar) paneEdges(i int) (x0, x1 float32) {
	p := tb.o.Panes
	edges := [4]float32{0, p[0], p[1], tb.w}
	if p[0] <= 0 {
		edges[1] = 0
	}
	if p[1] <= 0 {
		edges[2] = tb.w
	}
	x0, x1 = edges[i], edges[i+1]
	if x1 < x0 {
		x1 = x0
	}
	return x0, x1
}

// beginPane starts the row of section i at x (window pixels).
func (tb *Toolbar) beginPane(i int, x, paneX float32) {
	c, r := tb.k.Context(), tb.k.Theme()
	_, x1 := tb.paneEdges(i)
	if i == 0 && tb.o.Panes[0] <= 0 {
		_, x1 = tb.paneEdges(1)
		if tb.o.Panes[1] <= 0 {
			x1 = tb.w
		}
	}
	tb.pane, tb.x0, tb.x1, tb.paneX = i, x, x1, paneX
	tb.cur, tb.fixed, tb.prev, tb.titled = 0, true, tbStart, false
	h := theme.Snap(tb.spec.Height)
	theme.At(c, x, 0, max(x1-x, 0), h)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextPadding(0, 0, r.Window.Capsule.WideGap, 0)
	tb.row = c.BoxKeyed(base.Floating, ui.KeyN(tb.key, 100+uint64(i)), nil)
	c.Push(tb.row)
}

// NextPane ends the items of this pane; the next ones lie over the next pane
// (after the sidebar, then the inspector), WideGap after its edge.
func (tb *Toolbar) NextPane() {
	if tb.pane >= 2 {
		return
	}
	c := tb.k.Context()
	tb.closeGroup()
	c.Pop()
	next := tb.pane + 1
	if next == 1 && tb.o.Panes[0] <= 0 {
		next = 2
	}
	x, _ := tb.paneEdges(next)
	if next == 2 && tb.o.Panes[1] <= 0 {
		x = tb.w
	}
	tb.beginPane(next, x+tb.k.Theme().Window.Capsule.WideGap, x)
}

// space builds a gap of px pixels in the row.
func (tb *Toolbar) space(px float32) {
	if px <= 0 {
		return
	}
	c := tb.k.Context()
	tb.n++
	c.NextSize(ui.Px(px), ui.Px(0))
	c.BoxKeyed(0, ui.KeyN(tb.key, 1000+tb.n), nil)
	tb.cur += px
}

// gap lays the gap a capsule (or a picker) needs after what came before.
func (tb *Toolbar) gap(kind tbKind) {
	if tb.inGroup {
		return
	}
	wg := tb.k.Theme().Window.Capsule
	switch tb.prev {
	case tbStart, tbFlex:
	case tbTitle:
		tb.space(wg.WideGap)
	case tbPicker:
		tb.space(wg.WideGap)
	default:
		tb.space(wg.Gap)
	}
	tb.prev = kind
}

func (tb *Toolbar) capsuleH() float32 {
	if tb.spec.CapsuleH == 0 {
		return theme.Snap(tb.k.Pt(tb.rc.RexHit))
	}
	return theme.Snap(tb.spec.CapsuleH)
}

// itemW is the width of a one-symbol item: its symbol and the padding, at
// least the style's narrowest.
func (tb *Toolbar) itemW(ic icon.ID) float32 {
	iw, _ := tb.k.IconSize(ic)
	if tb.spec.CapsuleH == 0 {
		return theme.Snap(tb.k.Pt(tb.rc.RexHit))
	}
	i := 1
	if tb.o.Style == theme.ToolbarCompact {
		i = 0
	}
	pad := tb.rc.ItemPad
	if tb.inGroup {
		pad = tb.rc.GroupPad[i]
	}
	return theme.Snap(max(tb.k.Pt(tb.rc.ItemMin[i]), iw+2*tb.k.Pt(pad)))
}

// glyph is the symbol colour in the visual state v.
func (tb *Toolbar) glyph(v base.Visual) uint32 {
	r := tb.k.Theme()
	switch {
	case v.Disabled:
		return tb.rc.GlyphDisabled.RGBA(r)
	case v.Inactive:
		return tb.rc.GlyphInactive.At(r.T)
	}
	return tb.rc.Glyph.RGBA(r)
}

// capsuleLook styles the next box as a glass capsule of height h: the
// material with its rim, the hover and press of v laid over it.
func (tb *Toolbar) capsuleLook(v base.Visual, h float32) ui.Flags {
	c, r := tb.k.Context(), tb.k.Theme()
	fill := GlassFill(tb.k, GlassToolbarCapsule)
	fill = over(fill, tb.rc.Hover.RGBA(r), v.Hot)
	fill = over(fill, tb.rc.Press.RGBA(r), v.Down)
	rim := tb.rc.Rim.RGBA(r)
	if v.Inactive || v.Disabled {
		rim = tb.rc.RimInactive.RGBA(r)
	}
	c.NextBG(fill)
	c.NextRadius(h / 2)
	c.NextBorder(rim, r.Lines.Hairline)
	return ui.DrawBackground | ui.DrawBorder
}

// highlight draws a capsule's bright bottom rim (dark mode), in the root id.
func (tb *Toolbar) highlight(id ui.ID, v base.Visual, h float32) {
	if v.Inactive || v.Disabled {
		return
	}
	if hl := tb.rc.Highlight.RGBA(tb.k.Theme()); hl&0xFF != 0 && tb.k.Theme().Dark() {
		innerRim(tb.k, id, hl, h/2, ui.SideBottom)
	}
}

// symbol builds icon ic centred in the current parent (a press root).
func (tb *Toolbar) symbol(key ui.Key, ic icon.ID, col uint32) {
	c := tb.k.Context()
	if f, disp := tb.k.Icons().Prepare(c, ic, col); f != 0 {
		c.NextSize(ui.Pct(1), ui.Pct(1))
		c.BoxKeyed(f, key, disp)
	}
}

// Item builds a one-symbol item: its own glass capsule (or, inside a group,
// a part of the group's), a borderless symbol in the Rex band. name is its
// accessible name and tooltip text.
func (tb *Toolbar) Item(ic icon.ID, name string) base.Signal {
	tb.gap(tbCapsule)
	if tb.o.Style == theme.ToolbarRex {
		return tb.rexItem(ic, name)
	}
	k := tb.k
	h := tb.capsuleH()
	w := tb.itemW(ic)
	p := k.PressState(name, base.HoverOn)
	var flags ui.Flags
	if tb.inGroup {
		// a part of the group's capsule: its hover and press as a pill
		fill := over(0, tb.rc.Hover.RGBA(k.Theme()), p.Hot)
		fill = over(fill, tb.rc.Press.RGBA(k.Theme()), p.Down)
		if fill&0xFF != 0 {
			k.Context().NextBG(fill)
			k.Context().NextRadius(h / 2)
			flags = ui.DrawBackground
		}
	} else {
		flags = tb.capsuleLook(p.Visual, h)
	}
	k.BeginPress(&p, ui.RoleButton, ui.Px(w), ui.Px(h), flags, nil)
	tb.symbol(ui.KeyN(p.Key, 1), ic, tb.glyph(p.Visual))
	if !tb.inGroup {
		tb.highlight(p.ID, p.Visual, h)
	}
	k.FocusRing(p.ID, p.Visual, h/2)
	tb.advance(w)
	return k.EndPress(&p)
}

// rexItem is an item of the Rex band: a borderless symbol on a square that
// lights up under the pointer (rexlook's ⌘ and +).
func (tb *Toolbar) rexItem(ic icon.ID, name string) base.Signal {
	k := tb.k
	c, r := k.Context(), k.Theme()
	side := theme.Snap(k.Pt(tb.rc.RexHit))
	p := k.PressState(name, base.HoverOn)
	c.NextBG(theme.ColA(theme.MixRGB(tb.rc.RexFill.Light, tb.rc.RexFill.Dark, r.T), 0.08*p.Hot+0.07*p.Down))
	c.NextRadius(k.Pt(tb.rc.RexRadius) / 2)
	c.NextCursor(ui.CursorPointer)
	k.BeginPress(&p, ui.RoleButton, ui.Px(side), ui.Px(side), ui.DrawBackground, nil)
	col := toward(tb.rc.RexGlyph, tb.rc.RexGlyphHover, max(p.Hot, p.Down), r.T, 1)
	if p.Disabled {
		col = theme.WithAlpha(col, 0.4)
	} else if p.Inactive {
		col = theme.WithAlpha(col, TabsRecipes[TabsRex].InactiveOpacity)
	}
	tb.symbol(ui.KeyN(p.Key, 1), ic, col)
	k.FocusRing(p.ID, p.Visual, k.Pt(tb.rc.RexRadius)/2)
	tb.advance(side)
	return k.EndPress(&p)
}

// advance records w pixels laid out.
func (tb *Toolbar) advance(w float32) {
	if !tb.inGroup {
		tb.cur += w
	}
}

// BeginGroup starts a group of items that share one capsule (an
// NSToolbarItemGroup): Item builds its parts, EndGroup closes it.
func (tb *Toolbar) BeginGroup(name string) {
	if tb.inGroup || tb.o.Style == theme.ToolbarRex {
		return
	}
	tb.gap(tbCapsule)
	k := tb.k
	c := k.Context()
	h := tb.capsuleH()
	_, ident := ui.SplitLabel(name)
	flags := tb.capsuleLook(base.Visual{Inactive: k.Inactive(), Disabled: k.Disabled()}, h)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(k.Theme().Window.Capsule.GroupGap)
	c.NextSize(ui.Children(1), ui.Px(h))
	c.NextRole(ui.RoleGroup)
	c.NextSemName(name)
	tb.group = c.BoxKeyed(flags, ui.HashString(c.ParentKey(), ident), nil)
	c.Push(tb.group)
	tb.inGroup = true
	tb.nGroup = 0
}

// EndGroup closes the group BeginGroup started.
func (tb *Toolbar) EndGroup() { tb.closeGroup() }

func (tb *Toolbar) closeGroup() {
	if !tb.inGroup {
		return
	}
	c := tb.k.Context()
	tb.highlight(tb.group, base.Visual{Inactive: tb.k.Inactive()}, tb.capsuleH())
	c.Pop()
	tb.inGroup = false
	r := c.Rect(tb.group)
	tb.cur += r[2] - r[0]
}

// Nav builds the back and forward item: one capsule with a divider
// (NSToolbarItemGroup, navigational). It reports each half's signal; a half
// that cannot go is disabled.
func (tb *Toolbar) Nav(back, forward icon.ID, canBack, canForward bool) (b, f base.Signal) {
	tb.gap(tbCapsule)
	k := tb.k
	c, r := k.Context(), k.Theme()
	h := tb.capsuleH()
	w := theme.Snap(r.Window.Capsule.NavW)
	flags := tb.capsuleLook(base.Visual{Inactive: k.Inactive()}, h)
	c.NextAxis(ui.AxisX)
	c.NextRole(ui.RoleGroup)
	c.NextSemName("Back/Forward")
	tb.n++
	g := c.BoxKeyed(flags, ui.KeyN(tb.key, 2000+tb.n), nil)
	c.Push(g)
	half := theme.Snap(w / 2)
	for i, ic := range [2]icon.ID{back, forward} {
		name, can := "Back", canBack
		if i == 1 {
			name, can = "Forward", canForward
		}
		k.BeginDisabled(!can)
		p := k.PressState(name, base.HoverOn)
		fill := over(0, tb.rc.Hover.RGBA(r), p.Hot)
		fill = over(fill, tb.rc.Press.RGBA(r), p.Down)
		c.NextBG(fill)
		c.NextRadius(h / 2)
		k.BeginPress(&p, ui.RoleButton, ui.Px(half), ui.Px(h), ui.DrawBackground, nil)
		tb.symbol(ui.KeyN(p.Key, 1), ic, tb.glyph(p.Visual))
		if i == 0 {
			dh := theme.Snap(k.Pt(tb.rc.DividerH))
			theme.At(c, half-r.Lines.Hairline, theme.Snap((h-dh)/2), r.Lines.Hairline, dh)
			c.NextBG(tb.rc.Divider.RGBA(r))
			c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(p.Key, 2), nil)
		}
		s := k.EndPress(&p)
		k.EndDisabled()
		if i == 0 {
			b = s
		} else {
			f = s
		}
	}
	tb.highlight(g, base.Visual{Inactive: k.Inactive()}, h)
	c.Pop()
	tb.advance(w)
	return b, f
}

// Picker builds a select-one group editing *sel (a view switcher): expanded,
// a capsule of one segment per symbol with the chosen one filled; collapsed,
// a pop-up capsule showing the chosen symbol and a chevron, whose options are
// the system's menu (with native menus; otherwise a click moves to the next
// option). names are the options' accessible names.
func (tb *Toolbar) Picker(label string, sel *int, icons []icon.ID, names []string, collapsed bool) base.Signal {
	tb.gap(tbPicker)
	k := tb.k
	c, r := k.Context(), k.Theme()
	h := tb.capsuleH()
	if collapsed {
		p := k.PressState(label, base.HoverOn)
		w := theme.Snap(r.Window.Capsule.PickerW)
		flags := tb.capsuleLook(p.Visual, h)
		c.NextAxis(ui.AxisX)
		c.NextCross(ui.CrossCenter)
		k.BeginPress(&p, ui.RoleComboBox, ui.Px(w), ui.Px(h), flags, nil)
		fg := tb.glyph(p.Visual)
		if *sel >= 0 && *sel < len(icons) {
			tb.glyphAndChevron(p.Key, icons[*sel], fg, w, h)
		}
		tb.highlight(p.ID, p.Visual, h)
		k.FocusRing(p.ID, p.Visual, h/2)
		sig := k.EndPress(&p)
		if sig.Clicked && !k.Disabled() {
			if c.NativeMenus() {
				c.RequestMenu(ui.MenuRequest{Kind: ui.MenuList, Owner: p.Key, Options: names, Selected: *sel, Rect: c.Rect(p.ID)})
			} else if len(icons) > 0 {
				*sel = (*sel + 1) % len(icons)
				sig.Changed = true
			}
		}
		if i, ok := c.MenuChoice(p.ID, names); ok && i != *sel {
			*sel, sig.Changed = i, true
			c.RequestFrame()
		}
		tb.advance(w)
		return sig
	}
	ch := k.ChoiceState(label, sel, len(icons))
	flags := tb.capsuleLook(base.Visual{Inactive: k.Inactive(), Disabled: k.Disabled()}, h)
	c.NextAxis(ui.AxisX)
	c.NextRole(ui.RoleRadioGroup)
	c.NextSemName(ch.Name)
	c.NextSize(ui.Children(1), ui.Px(h))
	g := c.BoxKeyed(flags, ch.Key, nil)
	c.Push(g)
	sw := theme.Snap(k.Pt(tb.rc.SegmentW))
	var ring base.Visual
	for i, ic := range icons {
		name := ""
		if i < len(names) {
			name = names[i]
		}
		it := k.ChoiceItem(&ch, i, name, base.HoverOn)
		if it.Ring > ring.Ring {
			ring = it.Visual
		}
		fill := over(0, tb.rc.Hover.RGBA(r), it.Hot*(1-b2f(it.On)))
		if it.On {
			fill = tb.rc.Selected.RGBA(r)
		}
		fill = over(fill, tb.rc.Press.RGBA(r), it.Down)
		c.NextBG(fill)
		c.NextRadius(h / 2)
		k.BeginChoiceItem(&ch, &it, ui.RoleRadio, ui.Px(sw), ui.Px(h), ui.DrawBackground, nil)
		tb.symbol(ui.KeyN(it.Key, 1), ic, tb.glyph(it.Visual))
		k.EndPress(&it.Press)
	}
	tb.highlight(g, base.Visual{Inactive: k.Inactive()}, h)
	c.Pop()
	k.FocusRing(g, ring, h/2)
	tb.advance(sw * float32(len(icons)))
	return base.Signal{ID: g, Changed: ch.Changed}
}

func b2f(b bool) float32 {
	if b {
		return 1
	}
	return 0
}

// glyphAndChevron lays a symbol and a pull-down chevron centred in a w × h
// capsule (a menu item, a collapsed picker).
func (tb *Toolbar) glyphAndChevron(key ui.Key, ic icon.ID, fg uint32, w, h float32) {
	k := tb.k
	c := k.Context()
	iw, ih := k.IconSize(ic)
	cw, chh := k.IconSize(icon.PullDown)
	gap := theme.Snap(k.Pt(3))
	x := theme.Snap((w - iw - gap - cw) / 2)
	if f, disp := k.Icons().Prepare(c, ic, fg); f != 0 {
		theme.At(c, x, theme.Snap((h-ih)/2), iw, ih)
		c.BoxKeyed(f|base.Floating, ui.KeyN(key, 1), disp)
	}
	if f, disp := k.Icons().Prepare(c, icon.PullDown, fg); f != 0 {
		theme.At(c, x+iw+gap, theme.Snap((h-chh)/2), cw, chh)
		c.BoxKeyed(f|base.Floating, ui.KeyN(key, 2), disp)
	}
}

// Menu builds a menu item (NSMenuToolbarItem): a capsule with a symbol and a
// chevron that opens the menu model's menu at index menuIndex (the system's,
// with native menus); otherwise Clicked tells the app to open its own.
func (tb *Toolbar) Menu(ic icon.ID, name string, menuIndex int) base.Signal {
	tb.gap(tbCapsule)
	k := tb.k
	c := k.Context()
	h := tb.capsuleH()
	iw, _ := k.IconSize(ic)
	w := theme.Snap(max(k.Pt(tb.rc.MenuMin), iw+k.Pt(tb.rc.MenuPad)))
	p := k.PressState(name, base.HoverOn)
	flags := tb.capsuleLook(p.Visual, h)
	k.BeginPress(&p, ui.RoleButton, ui.Px(w), ui.Px(h), flags, nil)
	tb.glyphAndChevron(p.Key, ic, tb.glyph(p.Visual), w, h)
	tb.highlight(p.ID, p.Visual, h)
	k.FocusRing(p.ID, p.Visual, h/2)
	sig := k.EndPress(&p)
	if sig.Clicked && c.NativeMenus() && menuIndex >= 0 {
		c.RequestMenu(ui.MenuRequest{Kind: ui.MenuPullDown, Owner: p.Key, Menu: menuIndex, Rect: c.Rect(p.ID)})
		sig.Clicked = false
	}
	tb.advance(w)
	return sig
}

// SearchButton builds the search item collapsed to a magnifier capsule (a
// unified toolbar short of room) showing ic (a magnifying glass at the
// toolbar's symbol size); the app expands it on Clicked.
func (tb *Toolbar) SearchButton(ic icon.ID, name string) base.Signal {
	tb.searchGap()
	tb.prev = tbStart // the gap is laid
	s := tb.Item(ic, name)
	tb.prev = tbCapsule
	return s
}

func (tb *Toolbar) searchGap() {
	if tb.inGroup {
		return
	}
	i := 1
	if tb.o.Style == theme.ToolbarCompact {
		i = 0
	}
	if tb.prev != tbStart && tb.prev != tbFlex {
		tb.space(tb.k.Pt(tb.rc.SearchGap[i]))
	}
}

// Search builds an expanded search item: a search field (Escape and the clear
// button empty it) in a glass capsule of the toolbar's height, widthPt wide
// (0: the recipe's 166).
func (tb *Toolbar) Search(label string, buf *ui.TextBuffer, placeholder string, widthPt float32) base.FieldSignal {
	tb.searchGap()
	tb.prev = tbCapsule
	k := tb.k
	c := k.Context()
	if widthPt <= 0 {
		widthPt = tb.rc.SearchW
	}
	h := tb.capsuleH()
	w := theme.Snap(k.Pt(widthPt))
	flags := tb.capsuleLook(base.Visual{Inactive: k.Inactive(), Disabled: k.Disabled()}, h)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextSize(ui.Px(w), ui.Px(h))
	tb.n++
	g := c.BoxKeyed(flags, ui.KeyN(tb.key, 3000+tb.n), nil)
	c.Push(g)
	res := SearchField(k, label, buf, placeholder, FieldOpts{Variant: FieldBorderless})
	tb.highlight(g, base.Visual{Inactive: k.Inactive()}, h)
	c.Pop()
	tb.advance(w)
	return res
}

// Title places the window's title (ToolbarOpts.Title, and Subtitle below it)
// here: TitleGap after the capsule before it, or TitleInset into the pane.
// It takes the room the items leave (an NSToolbar's title view), so a
// Flexible after it in the same pane is the minimum space.
func (tb *Toolbar) Title() {
	k := tb.k
	c, r := k.Context(), k.Theme()
	if tb.inGroup {
		return
	}
	g := float32(0)
	if tb.fixed {
		x := tb.x0 + tb.cur
		g = tb.paneX + tb.spec.TitleInset - x
		if tb.prev == tbCapsule || tb.prev == tbPicker {
			g = max(g, tb.spec.TitleGap)
		}
	}
	tb.space(theme.Snap(max(g, 0)))
	tb.prev, tb.titled, tb.fixed = tbTitle, true, false

	st := &r.Styles[tb.spec.Title]
	col := tb.rc.Title.RGBA(r)
	if k.Inactive() {
		col = tb.rc.TitleInactive.RGBA(r)
	}
	h := theme.Snap(tb.spec.Height)
	c.NextAxis(ui.AxisY)
	c.NextCross(ui.CrossStart)
	c.NextSize(ui.Pct(1), ui.Px(h))
	tb.n++
	box := c.BoxKeyed(ui.Clip, ui.KeyN(tb.key, 4000+tb.n), nil)
	c.Push(box)
	lines := st.Line
	var sub *theme.StyleFont
	if tb.o.Subtitle != "" {
		sub = &r.Styles[theme.StyleSubheadline]
		lines += sub.Line
	}
	top := theme.Snap((h - lines) / 2)
	theme.At(c, 0, top, 0, st.Line)
	c.NextSize(ui.TextSize(base.LabelSlack), ui.Px(st.Line))
	c.NextFont(st.Font)
	c.NextTextColor(col)
	c.NextRole(ui.RoleLabel)
	c.NextSemName(tb.o.Title)
	c.BoxKeyed(ui.DrawText|ui.FadeOverflowX|base.Floating, ui.KeyN(tb.key, 5), base.StrBytes(tb.o.Title))
	if sub != nil {
		sc := tb.rc.Subtitle.RGBA(r)
		if k.Inactive() {
			sc = tb.rc.TitleInactive.RGBA(r)
		}
		theme.At(c, 0, top+st.Line, 0, sub.Line)
		c.NextSize(ui.TextSize(base.LabelSlack), ui.Px(sub.Line))
		c.NextFont(sub.Font)
		c.NextTextColor(sc)
		c.BoxKeyed(ui.DrawText|ui.FadeOverflowX|base.Floating, ui.KeyN(tb.key, 6), base.StrBytes(tb.o.Subtitle))
	}
	c.Pop()
}

// Flexible builds a flexible space: it takes the room the items leave (an
// NSToolbarFlexibleSpaceItem). After the title in the same pane the title
// takes the room, and a flexible space is the minimum, WideGap.
func (tb *Toolbar) Flexible() {
	if tb.inGroup {
		return
	}
	c := tb.k.Context()
	tb.n++
	if tb.titled {
		c.NextSize(ui.Px(tb.k.Theme().Window.Capsule.WideGap), ui.Px(0))
	} else {
		c.NextSize(ui.Pct(1), ui.Px(0))
		tb.fixed = false
	}
	c.BoxKeyed(0, ui.KeyN(tb.key, 5000+tb.n), nil)
	tb.prev = tbFlex
}

// Space builds a fixed space of pt points.
func (tb *Toolbar) Space(pt float32) {
	tb.space(theme.Snap(tb.k.Pt(pt)))
	tb.prev = tbFlex
}

// BeginCustom starts a box of width w (ui.Pct(1) takes the room the items
// leave) and the toolbar's height in the row, for the app's own content (Rex
// tabs in a toolbar); EndCustom closes it. It returns the box, whose
// rectangle (ui.Context.Rect) the content can lay itself out in.
func (tb *Toolbar) BeginCustom(label string, w ui.Size) ui.ID {
	tb.gap(tbCapsule)
	c := tb.k.Context()
	_, ident := ui.SplitLabel(label)
	c.NextSize(w, ui.Px(theme.Snap(tb.spec.Height)))
	id := c.BoxKeyed(0, ui.HashString(c.ParentKey(), ident), nil)
	c.Push(id)
	if w.Kind == ui.SizePixels {
		tb.cur += w.Value
	} else {
		tb.fixed = false
	}
	return id
}

// EndCustom closes the box BeginCustom started.
func (tb *Toolbar) EndCustom() { tb.k.Context().Pop() }

// Height is the toolbar's height in pixels: where the content's safe area
// starts.
func (tb *Toolbar) Height() float32 { return theme.Snap(tb.spec.Height) }

// End closes the toolbar.
func (tb *Toolbar) End() {
	if !tb.open {
		return
	}
	c := tb.k.Context()
	tb.closeGroup()
	c.Pop() // the row
	c.Pop() // the band
	tb.open = false
}
