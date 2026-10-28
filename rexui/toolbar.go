package rexui

import (
	"gonativeui/gpu"
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
	// its bar and hairline (the scroll-edge effect) over the content pane
	// (between Panes), as it always does while the window is inactive. The
	// bar is AppKit's titlebar pocket (ScrollEdgeLook.Hard): a blur of what
	// is drawn under the band (build the toolbar after it), over BandFill
	// where nothing is (gpu.Backdrop.Base); with the content below the band,
	// the pocket over the content's colour, flat (#FFFFFF, #262626 over
	// #1E1E1E). Where there is no blur and under Reduce Transparency it is
	// opaque (ScrollEdgeLook.Under). A pane whose scroll view runs under the
	// band draws its own (ContentEdge): the band blurs once.
	ContentUnder bool
	// ContentEdge says the content pane's scroll view runs under the band
	// (base.Kit.NextScrollInset, rexui.TableOpts.TopInset) and draws the
	// scroll edge itself (ScrollEdgeLook, from ScrollBars), the band's one
	// blur (Hard for a table, the thin film for a plain scroll view, as
	// AppKit's): the band over the content pane is clear, whatever
	// ContentUnder, Pocket and BandFill say, and the content shows through
	// it, blurred while it is scrolled under it. With the system's materials
	// (base.Kit.Materials) the capsules are drawn then, not the system's
	// glass, which lies below the window's pixels, under the content.
	ContentEdge bool
	// Pocket is how far below the toolbar, in pixels, the scroll edge
	// reaches: a table's header pinned under the toolbar is in it (§4: the
	// pocket ends at the header's bottom, 80 = 52 + 28), so the bar covers
	// the header, whose own line ends the pocket (the toolbar draws none).
	// Build the header after the toolbar, so that it draws on the bar, and
	// leave its background clear (TableOpts.ClearHeader).
	Pocket float32
	// Traffic draws the traffic lights at the style's place (offscreen
	// renders; in a window the system draws them).
	Traffic bool
	// Glass gives the Rex band's items (Style ToolbarRex: Item, BeginGroup
	// and BeginShared) the glass capsules of the other styles, macOS 26's
	// toolbar items (a RexHit-high capsule, the system's material where the
	// window hosts it), in place of borderless symbols.
	Glass bool
	// BandFill is the colour of the content pane's top under the band while
	// nothing is scrolled under it (the content's background, 0: none). The
	// content lies below the band, which shows it: with hosted materials the
	// band over the content pane is the system's opaque bar (GlassTitlebar)
	// of this colour, as the capsules are below the window's own pixels.
	BandFill uint32
	// Header builds the toolbar as a pane's header row instead of the
	// window's band (a Rex pane's title row): x0, y0, x1, y1 in window
	// pixels, its height y1 - y0 (0: the style's). The row keeps the pointer
	// from what is under it but does not move the window; it has no traffic
	// lights, band fill or scroll edge, and one section, whose items start
	// WideGap from x0 (Panes and NextPane do nothing). Zero: the window's band.
	Header [4]float32
}

// ToolbarRecipe is the toolbar's look. Lengths are in points.
type ToolbarRecipe struct {
	// Glyph colours an item's symbol: labelColor, which the glass's
	// vibrancy turns black (white in dark), as a sidebar row's text
	// (measured #000000 and #FFFFFF); GlyphInactive in an inactive window
	// (measured #A6A6A6 and #666666: lighter than the dimmed title);
	// GlyphDisabled when disabled.
	Glyph, GlyphDisabled Tint
	GlyphInactive        Tint
	// Hover and Press are laid over a capsule's glass under the pointer and
	// while pressed (ButtonToolbar's).
	Hover, Press Tint
	// Rim, RimInactive and Highlight are the capsule's outermost pixel and
	// its bright bottom rim (dark mode; none while inactive).
	Rim, RimInactive, Highlight Tint
	// Title and TitleInactive colour the title (§1, §7); Subtitle the line
	// below it.
	Title, TitleInactive, Subtitle Tint
	// Divider is the line between back and forward, DividerH its height
	// and DividerW its width, from the capsule's middle (measured on the
	// Mac mini: 1 by 20 pt, the separator's colour at 21 % over the light
	// capsule and 26.5 % over the dark one: #C8C8C8 on #FCFCFC, #707070 on
	// #3C3C3C).
	Divider  Tint
	DividerH float32
	DividerW float32
	// Selected fills the chosen segment of an expanded picker.
	Selected Tint
	// SegmentW is an expanded picker's segment (156 for 4, §1).
	SegmentW float32
	// GroupPad is the room a one-symbol item keeps on each side of its
	// symbol ([0] compact, [1] unified), alone or in a group alike: the
	// symbol's exact alignment width as drawn (theme.CapsuleSpec.Glyph at
	// GlyphScale, large: icon.Atlas.AlignWidth) and twice the pad, to the
	// pixel; a unified item is at least the style's ItemW (36), a compact
	// one has no minimum. In a capsule of several items (a group, or
	// BeginShared) the first is EdgePad[0] wider and the last EdgePad[0]
	// and then EdgePad[1] (after the minimum). Measured on the Mac mini's
	// 27.2 (26B5101f) with 21 symbols in lone items, 3-item groups and
	// 2-item groups (tools/appkitscenes/toolbarprobe.m): compact = width +
	// 8.43 (their mean; each within half a point, no formula fits all:
	// trash 18.24 → 27, textformat 22.78 → 31.5, checklist 22.38 → 31 in
	// the middle of Notes' group), unified 4 more; first +4, last +5
	// (unified: max(36, w+4)+1).
	GroupPad [2]float32
	EdgePad  [2]float32
	// Deprecated: ItemPad is unused; GroupPad sizes every item.
	ItemPad float32
	// A menu item is theme.CapsuleSpec.MenuItemW of its symbol's image
	// width at the medium scale (icon.Atlas.MediumWidth: AppKit's 47.5 for
	// square.grid.3x1.folder.badge.plus, 20 wide, and 44.5 for
	// ellipsis.circle, 15). MenuGap is the gap next to a menu or a group
	// (8.5 on the Mac mini's 26B5101f; 4.5 between them on the MacBook's
	// 26B5091g, where the theme's WideGap, 8, is the picker's).
	MenuGap float32
	// SearchGap is the gap before a search item, compact and unified (§1:
	// 10 and 8); SearchW an expanded search field's width (167 on the Mac
	// mini's 26B5101f, 166 on 26B5091g).
	SearchGap [2]float32
	SearchW   float32
	// SidebarGap is the gap between two capsules over the sidebar (the
	// first pane), compact and unified (0: the theme's Capsule.Gap): AppKit
	// centres each in a 44-pt item viewer there, so Notes' sidebar toggle
	// and Delete lie 14.5 apart. SharedGap is the gap between items that
	// share a capsule without being a group (BeginShared; measured 2, Notes'
	// Media and Share).
	SidebarGap [2]float32
	SharedGap  float32
	// FlexAfter is the room a flexible space keeps after a one-symbol item,
	// compact and unified: AppKit's item viewer is wider than a compact
	// item's capsule (Notes' New Note ends 3.5 before the flexible space;
	// the toolbar probe, 3.5 to 4.5), not a unified one's.
	FlexAfter [2]float32
	// EdgeFill and EdgeFillInactive fill the capsules a ContentEdge toolbar
	// draws (not the system's glass), the native glass's colour over the
	// band at rest, measured on the Mac mini's 27.2 (Finder, Notes and
	// Settings): #FAFAFA–#FDFDFD / #3E3E3E–#454545 in a key window,
	// #F0F0F0 / #3A3A3A in an inactive one. The glass lightens over content
	// scrolled under it (#4A4D4F over Finder's dark selection); these do not.
	EdgeFill, EdgeFillInactive Pair
	// InspectorEdge is how far after the inspector pane's edge the toolbar
	// takes it to be: AppKit's tracking separator lies half a point past
	// the split view's divider, so the content pane's last item ends 7.5
	// before the inspector's glass and the inspector's first starts 8.5
	// into it (measured 0.5 on both machines: Search 882.5, Inspector
	// 898.5 with the pane at 890).
	InspectorEdge float32
	// TitleTextX is how far into the title's text field (_NSToolbarTitleField,
	// at the title's place) its text starts: an NSTextField's 2.
	TitleTextX float32
	// TitleMinW is the narrowest the title is shown, in points: the room the
	// items leave it is its to shrink into (the text fades out at its end),
	// and under this it is hidden, before any item goes into the overflow
	// menu. NSToolbar truncates, then hides, the title before it moves an
	// item (issue #16); the width is a guess of a few letters and the
	// ellipsis, not yet measured against AppKit (tools/appkitscenes at
	// narrow widths), which needs an unlocked screen.
	TitleMinW float32
	// RexGlyph, RexGlyphHover and RexFill are the Rex band's borderless
	// items (rexlook's ⌘ and +), RexHit their side, RexRadius their corner.
	RexGlyph, RexGlyphHover, RexFill Pair
	RexHit, RexRadius                float32
	// Traffic are the drawn traffic lights (offscreen only), active, and
	// TrafficInactive all three in an inactive window (§7).
	Traffic         [3]Pair
	TrafficInactive Pair
}

// ToolbarSymbol is the icon spec of a toolbar item's SF Symbol (with a
// fallback rune elsewhere), at the theme's toolbar size and scale
// (theme.CapsuleSpec.Glyph, GlyphScale): add it to the kit (Kit.AddIcon)
// before the first frame.
func ToolbarSymbol(symbol string, fallback rune) icon.Spec {
	c := &theme.MacWindow.Capsule
	return icon.Spec{Symbol: symbol, Fallback: fallback, Pt: c.Glyph, Scale: icon.Scale(c.GlyphScale)}
}

// ToolbarLook is the toolbar's recipe.
var ToolbarLook = ToolbarRecipe{
	Glyph: Of(theme.SidebarLabel), GlyphInactive: Of(theme.ToolbarSymbolInactive), GlyphDisabled: Of(theme.TextDisabled),
	Hover: Of(theme.Fill4), Press: Of(theme.Fill2),
	Rim: Of(theme.CapsuleRim), RimInactive: Of(theme.CapsuleRimInactive), Highlight: Of(theme.CapsuleHighlight),
	Title: Of(theme.TitleText), TitleInactive: Of(theme.TitleTextInactive), Subtitle: Of(theme.Label2),
	Divider: OfAlpha(theme.Separator, 0.21, 0.265), DividerH: 20, DividerW: 1,
	Selected: Of(theme.Fill2),
	SegmentW: 39, GroupPad: [2]float32{4.215, 6.215}, EdgePad: [2]float32{4, 1}, MenuGap: 8.5,
	SearchGap: [2]float32{10, 8}, SearchW: 167,
	SidebarGap: [2]float32{14.5, 0}, SharedGap: 2, FlexAfter: [2]float32{3.5, 0},
	EdgeFill: Solid(0xFAFAFA, 0x414141), EdgeFillInactive: Solid(0xF0F0F0, 0x3A3A3A), TitleTextX: 2, TitleMinW: 48, InspectorEdge: 0.5,
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
	tbMenu
	tbGroup
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
	nGroup  int // the group's parts built so far
	gParts  int // the group's parts last frame
	gKey    ui.Key
	n       uint64
	open    bool
	native  bool // the capsules are the system's glass (GlassNative)

	// the overflow: what a pane has laid out (used), from which item on
	// it is out of room (over), the items moved into the overflow menu
	// (ov), and the entry chosen in it this frame (pick)
	used    float32
	over    bool
	hideGrp bool
	ov      [MaxToolbarOverflow]string
	nOv     int
	pick    int
	ovKey   ui.Key
}

// MaxToolbarOverflow is the most items the overflow menu lists.
const MaxToolbarOverflow = 24

// ToolbarOverflow is what End reports: the names of the items that did not
// fit, in the overflow menu (Names[:N]), and whether its button was pressed
// while menus are the app's (Clicked: show them; with native menus the
// system's menu shows them and a choice comes back as the item's Clicked).
// It is a value, so that keeping it allocates nothing.
type ToolbarOverflow struct {
	// N is how many items are in the menu.
	N int
	// Names are their accessible names, in the band's order.
	Names [MaxToolbarOverflow]string
	// Clicked reports a press on the » button without native menus.
	Clicked bool
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
	// a pane's content under the band is the layer's own pixels, over the
	// system's glass: the capsules are drawn then, over it
	tb.native = (o.Style != theme.ToolbarRex || o.Glass) && !o.ContentEdge && GlassNative(k, GlassToolbarCapsule)
	_, ident := ui.SplitLabel(label)
	tb.key = ui.HashString(c.ParentKey(), ident)
	tb.ovKey, tb.pick = ui.KeyN(tb.key, 6000), -1
	if in := c.Input(); in.HasMenuChoice && in.MenuOwner == tb.ovKey {
		tb.pick = int(in.MenuIndex) // the overflow menu's choice: the items it lists are this frame's too
	}
	w, _ := c.Size()
	tb.w = float32(w)
	if hd := o.Header; hd[2] > hd[0] {
		tb.header(hd, label)
		return tb
	}
	h := theme.Snap(tb.spec.Height)

	// the band moves the window; it is clear while nothing is under it in an
	// active window, and otherwise shows the scroll edge's bar and hairline
	// over the content pane (each pane has its own pocket: the sidebar's and
	// the inspector's glass run to the top without one)
	bar := o.Style != theme.ToolbarRex && theme.ScrollEdgeVisible(o.ContentUnder, !k.Inactive())
	if o.Style != theme.ToolbarRex && !o.ContentEdge && (bar || o.BandFill != 0) {
		bx0, bx1 := float32(0), tb.w
		if o.Panes[0] > 0 {
			bx0 = o.Panes[0]
		}
		if o.Panes[1] > 0 {
			bx1 = o.Panes[1]
		}
		// the bar down to the pocket's end (a header pinned under the
		// toolbar), and the hairline just below it (y 52..52.5 measured: the
		// line is outside the band); at rest, the content's colour
		pk := theme.Snap(max(o.Pocket, 0))
		fill, bg := o.BandFill, uint32(0)
		var blur gpu.Backdrop
		switch {
		case bar && o.ContentUnder && r.Settings.ReduceTransparency: // opaque: the blurred pocket's colour (ScrollEdgeRecipe.Under)
			fill = scrollEdgeUnder(k, 0, nil)
		case bar && o.ContentUnder: // AppKit's titlebar pocket (Hard): a blur of what is drawn under the band, over the content's colour
			blur = scrollEdgeBlur(k, true)
			blur.Base = o.BandFill
			if blur.Base == 0 {
				blur.Base = blur.Tint
			}
			fill, bg = blur.Over(blur.Base), scrollEdgeUnder(k, 0, nil)
		case bar:
			fill = glassFallback(k, GlassTitlebar)
		}
		theme.At(c, bx0, 0, max(bx1-bx0, 0), h+pk)
		flags := base.Floating
		if !GlassNative(k, GlassTitlebar) {
			if bg != 0 { // the blur, flat over nothing; its fallback, opaque (Under)
				c.NextBackdrop(blur)
				fill = bg
			}
			c.NextBG(fill)
			flags |= ui.DrawBackground
		}
		GlassRegion(k, c.BoxKeyed(flags, ui.KeyN(tb.key, 2), nil), GlassTitlebar, fill)
		if bar && pk == 0 {
			theme.At(c, bx0, h+pk, max(bx1-bx0, 0), r.Window.ScrollEdge.Line)
			c.NextBG(r.Colors[theme.ScrollEdgeLine])
			c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(tb.key, 1), nil)
		}
	}
	// the band covers what scrolls under it: the pointer's hover and clicks
	// and the wheel stop at it (its capsules take theirs), but a press on it
	// still moves the window (a WindowDrag area, not a drag map's block)
	theme.At(c, 0, 0, tb.w, h)
	c.NextRole(ui.RoleGroup)
	c.NextSemName(label)
	tb.band = c.BoxKeyed(ui.WindowDrag|ui.Hoverable|base.Floating, tb.key, nil)
	c.StopWheel(tb.band)
	c.Push(tb.band)
	if o.Traffic {
		tb.trafficLights()
	}
	CaptionButtons(k) // Windows' minimise, maximise and close, when the band is the app's there

	// the first pane starts after the traffic lights: FirstGap after the
	// zoom button (the system's, when it is further right than the style's)
	sp := r.Window.TrafficSpacing
	zoom := tb.spec.Traffic[0] + 2*sp + r.Window.TrafficD/2
	sb := c.SystemButtons()
	first := tb.spec.FirstGap
	if first == 0 {
		first = r.Window.Capsule.WideGap
	}
	if c.Decorations().Caption {
		// Windows: no buttons on the left; the items end WideGap before the
		// caption buttons at the band's right end
		zoom = 0
		if sb[2] > sb[0] {
			tb.w = theme.Snap(sb[0] - r.Window.Capsule.WideGap)
		}
	} else if sb[2] > zoom {
		zoom = sb[2]
	}
	tb.beginPane(0, theme.Snap(zoom+first), 0)
	return tb
}

// header starts a pane's header row (ToolbarOpts.Header): a band of its own
// in the pane, whose items lie in band coordinates as the window's do.
func (tb *Toolbar) header(hd [4]float32, label string) {
	c, r := tb.k.Context(), tb.k.Theme()
	tb.o.Panes, tb.o.Traffic = [2]float32{}, false
	if h := hd[3] - hd[1]; h > 0 {
		tb.spec.Height = h
	}
	tb.w = hd[2] - hd[0]
	theme.At(c, hd[0], hd[1], tb.w, theme.Snap(tb.spec.Height))
	c.NextRole(ui.RoleGroup)
	c.NextSemName(label)
	tb.band = c.BoxKeyed(ui.Hoverable|base.Floating, tb.key, nil)
	c.StopWheel(tb.band)
	c.Push(tb.band)
	tb.beginPane(0, r.Window.Capsule.WideGap, 0)
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
	if i == 1 && tb.o.Panes[1] > 0 {
		x1 += theme.Snap(tb.k.Pt(tb.rc.InspectorEdge))
	}
	tb.pane, tb.x0, tb.x1, tb.paneX = i, x, x1, paneX
	tb.cur, tb.fixed, tb.prev, tb.titled = 0, true, tbStart, false
	tb.used, tb.over = 0, false
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
	} else if next == 2 {
		x += theme.Snap(tb.k.Pt(tb.rc.InspectorEdge))
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
	tb.used += px
}

// gap lays the gap a capsule (or a picker) needs after what came before.
func (tb *Toolbar) gap(kind tbKind) {
	if tb.inGroup {
		return
	}
	tb.space(tb.gapW(kind))
	tb.prev = kind
}

// gapW is the gap an item of kind needs after what came before: WideGap
// next to a title, a picker, a menu or a group (on either side: measured
// 8.5 between Finder's Group menu, its share-tag group and its Action menu,
// and between Notes' two groups, on the Mac mini's 27.2, 26B5101f; 4.5 on
// the earlier 26B5091g), Gap between other capsules.
func (tb *Toolbar) gapW(kind tbKind) float32 {
	wg := tb.k.Theme().Window.Capsule
	switch tb.prev {
	case tbStart, tbFlex:
		return 0
	}
	if (kind == tbMenu || kind == tbGroup) && tb.rc.MenuGap > 0 {
		return theme.Snap(tb.k.Pt(tb.rc.MenuGap))
	}
	switch tb.prev {
	case tbTitle, tbPicker, tbMenu, tbGroup:
		return wg.WideGap
	}
	if kind == tbMenu || kind == tbGroup {
		return wg.WideGap
	}
	g := wg.Gap
	if i := tb.styleIndex(); tb.pane == 0 && tb.o.Panes[0] > 0 && tb.rc.SidebarGap[i] > 0 {
		g = theme.Snap(tb.k.Pt(tb.rc.SidebarGap[i]))
	}
	return g
}

// overflows decides whether an item w pixels wide (after its gap) is out
// of room in its pane, as NSToolbar's: then it, and every item after it in
// the pane, goes into the overflow menu at the band's end (End), listed by
// name, and is not built; picked reports that the menu chose it this frame.
// The pane keeps room for the overflow button after the items it shows.
func (tb *Toolbar) overflows(kind tbKind, w float32, name string) (hidden, picked bool) {
	if tb.o.Style == theme.ToolbarRex {
		return false, false
	}
	if tb.inGroup {
		hidden = tb.hideGrp
	} else {
		if !tb.over {
			wg := tb.k.Theme().Window.Capsule
			room := tb.x1 - tb.x0 - wg.WideGap
			if tb.x1 >= tb.w-0.5 { // the last pane: the overflow button's room
				room -= wg.Gap + tb.overflowW()
			}
			tb.over = tb.used+tb.gapW(kind)+w > room+0.5
		}
		hidden = tb.over
	}
	if !hidden {
		return false, false
	}
	if name != "" && tb.nOv < MaxToolbarOverflow { // a group lists its parts, not itself
		picked = tb.nOv == tb.pick
		tb.ov[tb.nOv] = name
		tb.nOv++
	}
	return true, picked
}

// overflowW is the overflow button's width.
func (tb *Toolbar) overflowW() float32 {
	return theme.Snap(tb.spec.ItemW)
}

// styleIndex is 0 for a compact toolbar, 1 for the others (the recipe's
// pairs).
func (tb *Toolbar) styleIndex() int {
	if tb.o.Style == theme.ToolbarCompact {
		return 0
	}
	return 1
}

func (tb *Toolbar) capsuleH() float32 {
	if tb.spec.CapsuleH == 0 {
		return theme.Snap(tb.k.Pt(tb.rc.RexHit))
	}
	return theme.Snap(tb.spec.CapsuleH)
}

// itemW is the width of a one-symbol item: its symbol and the padding, at
// least the style's narrowest (unified), and wider at the ends of a capsule
// of several items (the group's next part is its part'th, of the parts it
// had last frame).
func (tb *Toolbar) itemW(ic icon.ID) float32 {
	if tb.spec.CapsuleH == 0 {
		return theme.Snap(tb.k.Pt(tb.rc.RexHit))
	}
	k := tb.k
	i := tb.styleIndex()
	w := theme.Snap(k.IconAlignWidth(ic) + 2*k.Pt(tb.rc.GroupPad[i]))
	first, last := false, false
	if tb.inGroup && tb.gParts > 1 {
		first, last = tb.nGroup == 0, tb.nGroup == tb.gParts-1
	}
	if first || last {
		w += theme.Snap(k.Pt(tb.rc.EdgePad[0]))
	}
	if i == 1 {
		w = max(w, theme.Snap(tb.spec.ItemW))
	}
	if last {
		w += theme.Snap(k.Pt(tb.rc.EdgePad[1]))
	}
	return w
}

// glyph is the symbol colour in the visual state v.
func (tb *Toolbar) glyph(v base.Visual) uint32 {
	r := tb.k.Theme()
	switch {
	case v.Disabled:
		return tb.rc.GlyphDisabled.RGBA(r)
	case v.Inactive:
		return tb.rc.GlyphInactive.RGBA(r)
	}
	return tb.rc.Glyph.RGBA(r)
}

// capsuleLook styles the next box as a glass capsule of height h: the
// material with its rim, the hover and press of v laid over it.
func (tb *Toolbar) capsuleLook(v base.Visual, h float32) ui.Flags {
	c, r := tb.k.Context(), tb.k.Theme()
	fill := GlassFill(tb.k, GlassToolbarCapsule)
	switch {
	case tb.o.ContentEdge && v.Inactive:
		fill = tb.rc.EdgeFillInactive.At(r.T)
	case tb.o.ContentEdge:
		fill = tb.rc.EdgeFill.At(r.T)
	}
	fill = over(fill, tb.rc.Hover.RGBA(r), v.Hot)
	fill = over(fill, tb.rc.Press.RGBA(r), v.Down)
	c.NextRadius(h / 2)
	if tb.native { // the system's glass has its own rim
		if fill&0xFF == 0 {
			return 0
		}
		c.NextBG(fill)
		return ui.DrawBackground
	}
	rim := tb.rc.Rim.RGBA(r)
	if v.Inactive || v.Disabled {
		rim = tb.rc.RimInactive.RGBA(r)
	}
	c.NextBG(fill)
	c.NextBorder(rim, r.Lines.Hairline)
	return ui.DrawBackground | ui.DrawBorder
}

// glass marks the capsule id as the toolbar's glass (GlassRegion).
func (tb *Toolbar) glass(id ui.ID) {
	if tb.native {
		GlassRegion(tb.k, id, GlassToolbarCapsule, 0)
	}
}

// highlight draws a capsule's bright bottom rim (dark mode), in the root id.
func (tb *Toolbar) highlight(id ui.ID, v base.Visual, h float32) {
	if v.Inactive || v.Disabled || tb.native {
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
	if tb.o.Style == theme.ToolbarRex && !tb.o.Glass {
		tb.gap(tbCapsule)
		return tb.rexItem(ic, name)
	}
	k := tb.k
	h := tb.capsuleH()
	w := tb.itemW(ic)
	if hidden, picked := tb.overflows(tbCapsule, w, name); hidden {
		return base.Signal{Clicked: picked}
	}
	tb.gap(tbCapsule)
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
		tb.glass(p.ID)
		tb.highlight(p.ID, p.Visual, h)
	}
	k.FocusRing(p.ID, p.Visual, h/2)
	tb.advance(w)
	if tb.inGroup {
		tb.nGroup++
	}
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
		tb.used += w
	}
}

// BeginGroup starts a group of items that share one capsule (an
// NSToolbarItemGroup): Item builds its parts, EndGroup closes it.
func (tb *Toolbar) BeginGroup(name string) { tb.beginGroup(name, false) }

// BeginShared starts items that are separate toolbar items side by side,
// which AppKit gives one capsule (Notes' Media and Share): Item builds them,
// EndGroup closes the capsule. They lie SharedGap apart, closer than a
// group's parts.
func (tb *Toolbar) BeginShared(name string) { tb.beginGroup(name, true) }

func (tb *Toolbar) beginGroup(name string, shared bool) {
	if tb.inGroup || tb.o.Style == theme.ToolbarRex && !tb.o.Glass {
		return
	}
	k := tb.k
	c := k.Context()
	h := tb.capsuleH()
	_, ident := ui.SplitLabel(name)
	gkey := ui.HashString(c.ParentKey(), ident)
	// as wide as it was laid out last (its parts decide), remembered while
	// it is in the overflow menu; its parts go there when it is out of room
	wkey := ui.KeyN(gkey, 0x6A0)
	gw := c.KeepValue(wkey, 0, 0)
	if lr := c.RectOf(gkey); lr[2] > lr[0] {
		gw = lr[2] - lr[0]
	}
	c.KeepValue(wkey, 0, gw)
	if hidden, _ := tb.overflows(tbGroup, gw, ""); hidden {
		tb.inGroup, tb.hideGrp = true, true
		return
	}
	tb.gap(tbGroup)
	flags := tb.capsuleLook(base.Visual{Inactive: k.Inactive(), Disabled: k.Disabled()}, h)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	gap := k.Theme().Window.Capsule.GroupGap
	if shared {
		gap = theme.Snap(k.Pt(tb.rc.SharedGap))
	}
	c.NextGap(gap)
	c.NextSize(ui.Children(1), ui.Px(h))
	c.NextRole(ui.RoleGroup)
	c.NextSemName(name)
	tb.group = c.BoxKeyed(flags, gkey, nil)
	tb.glass(tb.group)
	c.Push(tb.group)
	tb.inGroup = true
	tb.nGroup = 0
	// the parts it had last frame; closeGroup stores this frame's over the
	// placeholder (KeepValue's second call in a frame returns the first's)
	tb.gKey = ui.KeyN(gkey, 0x6A1)
	tb.gParts = int(c.KeepValue(tb.gKey, -1, -1))
}

// EndGroup closes the group BeginGroup started.
func (tb *Toolbar) EndGroup() { tb.closeGroup() }

func (tb *Toolbar) closeGroup() {
	if !tb.inGroup {
		return
	}
	if tb.hideGrp {
		tb.inGroup, tb.hideGrp = false, false
		return
	}
	c := tb.k.Context()
	// the ends' widths follow the parts: a changed count lays out again
	c.KeepValue(tb.gKey, -1, float32(tb.nGroup))
	if tb.gParts != tb.nGroup {
		c.RequestFrame()
	}
	tb.highlight(tb.group, base.Visual{Inactive: tb.k.Inactive()}, tb.capsuleH())
	c.Pop()
	tb.inGroup = false
	r := c.Rect(tb.group)
	tb.cur += r[2] - r[0]
	tb.used += r[2] - r[0]
}

// Nav builds the back and forward item: one capsule with a divider
// (NSToolbarItemGroup, navigational). It reports each half's signal; a half
// that cannot go is disabled.
func (tb *Toolbar) Nav(back, forward icon.ID, canBack, canForward bool) (b, f base.Signal) {
	k := tb.k
	c, r := k.Context(), k.Theme()
	h := tb.capsuleH()
	w := theme.Snap(r.Window.Capsule.NavW)
	if hidden, picked := tb.overflows(tbCapsule, w, str.Back); hidden {
		_, pf := tb.overflows(tbCapsule, w, str.Forward)
		return base.Signal{Clicked: picked && canBack}, base.Signal{Clicked: pf && canForward}
	}
	tb.gap(tbCapsule)
	flags := tb.capsuleLook(base.Visual{Inactive: k.Inactive()}, h)
	c.NextAxis(ui.AxisX)
	c.NextRole(ui.RoleGroup)
	c.NextSemName(str.BackForward)
	tb.n++
	gkey := ui.KeyN(tb.key, 2000+tb.n)
	g := c.BoxKeyed(flags, gkey, nil)
	tb.glass(g)
	c.Push(g)
	half := theme.Snap(w / 2)
	for i, ic := range [2]icon.ID{back, forward} {
		name, can := str.Back, canBack
		if i == 1 {
			name, can = str.Forward, canForward
		}
		k.BeginDisabled(!can)
		p := k.PressState(name, base.HoverOn)
		fill := over(0, tb.rc.Hover.RGBA(r), p.Hot)
		fill = over(fill, tb.rc.Press.RGBA(r), p.Down)
		c.NextBG(fill)
		c.NextRadius(h / 2)
		k.BeginPress(&p, ui.RoleButton, ui.Px(half), ui.Px(h), ui.DrawBackground, nil)
		tb.symbol(ui.KeyN(p.Key, 1), ic, tb.glyph(p.Visual))
		k.FocusRing(p.ID, p.Visual, h/2)
		s := k.EndPress(&p)
		k.EndDisabled()
		if i == 0 {
			b = s
		} else {
			f = s
		}
	}
	// the divider starts at the capsule's middle, over the forward half
	dh, dw := theme.Snap(k.Pt(tb.rc.DividerH)), max(theme.Snap(k.Pt(tb.rc.DividerW)), r.Lines.Hairline)
	theme.At(c, half, theme.Snap((h-dh)/2), dw, dh)
	c.NextBG(tb.rc.Divider.RGBA(r))
	c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(gkey, 1), nil)
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
	k := tb.k
	c, r := k.Context(), k.Theme()
	h := tb.capsuleH()
	pw := theme.Snap(r.Window.Capsule.PickerW)
	if !collapsed {
		pw = theme.Snap(k.Pt(tb.rc.SegmentW)) * float32(len(icons))
	}
	if hidden, picked := tb.overflows(tbPicker, pw, label); hidden {
		// chosen from the overflow menu: the next option, as a click on the
		// collapsed picker without native menus
		sig := base.Signal{Clicked: picked}
		if picked && len(icons) > 0 {
			*sel, sig.Changed = (*sel+1)%len(icons), true
		}
		return sig
	}
	tb.gap(tbPicker)
	if collapsed {
		p := k.PressState(label, base.HoverOn)
		w := theme.Snap(r.Window.Capsule.PickerW)
		flags := tb.capsuleLook(p.Visual, h)
		c.NextAxis(ui.AxisX)
		c.NextCross(ui.CrossCenter)
		k.BeginPress(&p, ui.RoleComboBox, ui.Px(w), ui.Px(h), flags, nil)
		tb.glass(p.ID)
		fg := tb.glyph(p.Visual)
		if *sel >= 0 && *sel < len(icons) {
			tb.glyphAndChevron(p.Key, icons[*sel], icon.ChevronUpDown, fg, w, h)
		}
		tb.highlight(p.ID, p.Visual, h)
		k.FocusRing(p.ID, p.Visual, h/2)
		sig := k.EndPress(&p)
		// an assistive technology opens it as a pop-up button: show menu,
		// expand, or press
		if a := c.Signal(p.ID).Action; a&(ui.ActExpand|ui.ActShowMenu) != 0 {
			sig.Clicked = true
		}
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
	tb.glass(g)
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

// glyphAndChevron lays a symbol and a chevron (a pull-down's, a pop-up's)
// centred in a w × h capsule (a menu item, a collapsed picker).
func (tb *Toolbar) glyphAndChevron(key ui.Key, ic, chev icon.ID, fg uint32, w, h float32) {
	k := tb.k
	c := k.Context()
	iw, ih := k.IconSize(ic)
	cw, chh := k.IconSize(chev)
	gap := theme.Snap(k.Pt(3))
	x := theme.Snap((w - iw - gap - cw) / 2)
	if f, disp := k.Icons().Prepare(c, ic, fg); f != 0 {
		theme.At(c, x, theme.Snap((h-ih)/2), iw, ih)
		c.BoxKeyed(f|base.Floating, ui.KeyN(key, 1), disp)
	}
	if f, disp := k.Icons().Prepare(c, chev, fg); f != 0 {
		theme.At(c, x+iw+gap, theme.Snap((h-chh)/2), cw, chh)
		c.BoxKeyed(f|base.Floating, ui.KeyN(key, 2), disp)
	}
}

// Menu builds a menu item (NSMenuToolbarItem): a capsule with a symbol and a
// chevron that opens the menu model's menu at index menuIndex (the system's,
// with native menus); otherwise Clicked tells the app to open its own.
func (tb *Toolbar) Menu(ic icon.ID, name string, menuIndex int) base.Signal {
	k := tb.k
	c := k.Context()
	h := tb.capsuleH()
	w := theme.Snap(k.Theme().Window.Capsule.MenuItemW(k.IconMediumWidth(ic)))
	if hidden, picked := tb.overflows(tbMenu, w, name); hidden {
		return base.Signal{Clicked: picked}
	}
	tb.gap(tbMenu)
	p := k.PressState(name, base.HoverOn)
	flags := tb.capsuleLook(p.Visual, h)
	k.BeginPress(&p, ui.RoleButton, ui.Px(w), ui.Px(h), flags, nil)
	tb.glass(p.ID)
	tb.glyphAndChevron(p.Key, ic, icon.PullDown, tb.glyph(p.Visual), w, h)
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
	if hidden, picked := tb.overflows(tbCapsule, tb.itemW(ic)+tb.searchGapW()-tb.gapW(tbCapsule), name); hidden {
		return base.Signal{Clicked: picked}
	}
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
	tb.space(tb.searchGapW())
}

// searchGapW is the gap before a search item.
func (tb *Toolbar) searchGapW() float32 {
	if tb.prev == tbStart || tb.prev == tbFlex {
		return 0
	}
	return tb.k.Pt(tb.rc.SearchGap[tb.styleIndex()])
}

// Search builds an expanded search item: a search field (Escape and the clear
// button empty it) in a glass capsule of the toolbar's height, widthPt wide
// (0: the recipe's 166). When it does not fit it is listed in the overflow
// menu, and choosing it there shows the field in a popover under the »
// button, with the focus in it; the popover's field is the same buffer.
func (tb *Toolbar) Search(label string, buf *ui.TextBuffer, placeholder string, widthPt float32) base.FieldSignal {
	k := tb.k
	c := k.Context()
	if widthPt <= 0 {
		widthPt = tb.rc.SearchW
	}
	h := tb.capsuleH()
	w := theme.Snap(k.Pt(widthPt))
	if hidden, picked := tb.overflows(tbCapsule, w+tb.searchGapW()-tb.gapW(tbCapsule), label); hidden {
		return tb.overflowedSearch(label, buf, placeholder, widthPt, picked)
	}
	tb.searchGap()
	tb.prev = tbCapsule
	flags := tb.capsuleLook(base.Visual{Inactive: k.Inactive(), Disabled: k.Disabled()}, h)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextSize(ui.Px(w), ui.Px(h))
	tb.n++
	g := c.BoxKeyed(flags, ui.KeyN(tb.key, 3000+tb.n), nil)
	tb.glass(g)
	c.Push(g)
	res := SearchField(k, label, buf, placeholder, FieldOpts{Variant: FieldBorderless})
	tb.highlight(g, base.Visual{Inactive: k.Inactive()}, h)
	c.Pop()
	tb.advance(w)
	return res
}

// overflowedSearch is a search item that did not fit, once the overflow menu
// has chosen it: the search field in a popover under the » button, focused,
// as NSToolbar shows an overflowed search field. The popover is built on
// every frame the item stays in the overflow after that, until it is
// dismissed (Escape, a press elsewhere, the » button); picked is the menu's
// choice of this frame.
func (tb *Toolbar) overflowedSearch(label string, buf *ui.TextBuffer, placeholder string, widthPt float32, picked bool) base.FieldSignal {
	k := tb.k
	c := k.Context()
	if widthPt <= 0 {
		widthPt = tb.rc.SearchW
	}
	open := ui.KeyN(ui.HashString(tb.key, label), 7000) // whether it was shown last frame
	was := c.KeepValue(open, 0, 0) != 0
	if !was && !picked {
		return base.FieldSignal{}
	}
	c.KeepValue(open, 0, 1)
	anchor := c.RectOf(tb.ovKey)
	p := BeginPopover(k, label, anchor, PopoverOpts{Width: widthPt + 2*PopoverLook.Pad})
	k.NextWidth(ui.Pct(1))
	res := SearchField(k, label, buf, placeholder, FieldOpts{})
	if picked {
		c.SetFocus(res.Key)
	}
	EndPopover(k, &p)
	if p.Dismissed {
		c.KeepValue(open, 0, 0)
	}
	return res
}

// Title places the window's title (ToolbarOpts.Title, and Subtitle below it)
// here: TitleGap after the capsule before it, or TitleInset into the pane.
// It takes the room the items leave (an NSToolbar's title view), so a
// Flexible after it in the same pane is the minimum space. The items keep
// their room over the title's: as the pane narrows the title is truncated
// (its text fades at the end), then hidden once it has less than
// ToolbarRecipe.TitleMinW, and only when the items alone do not fit do they
// move into the overflow menu.
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
		if tb.prev == tbCapsule || tb.prev == tbPicker || tb.prev == tbMenu || tb.prev == tbGroup {
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
	// the room the items left it, as the last frame laid it out: it shows
	// the text only if it has the title's minimum (or all of it, for a short
	// title)
	room := c.Rect(box)
	shown := true
	switch {
	case room[3] <= room[1]:
		c.RequestFrame() // not laid out yet (a title squeezed to nothing is as high as the band)
	default:
		need := min(theme.Snap(k.Pt(tb.rc.TitleMinW)), theme.Snap(k.TextWidth(st.Font, tb.o.Title)+k.Pt(tb.rc.TitleTextX)))
		shown = room[2]-room[0] >= need-0.5
	}
	c.Push(box)
	if !shown {
		c.Pop()
		return
	}
	lines := st.Line
	var sub *theme.StyleFont
	if tb.o.Subtitle != "" {
		sub = &r.Styles[theme.StyleSubheadline]
		lines += sub.Line
	}
	top := theme.Snap((h - lines) / 2)
	// the title's text field, its text TitleTextX inside it
	tx := k.Pt(tb.rc.TitleTextX)
	theme.At(c, 0, top, 0, st.Line)
	c.NextSize(ui.TextSize(base.LabelSlack+tx), ui.Px(st.Line))
	c.NextPadding(tx, 0, 0, 0)
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
		c.NextSize(ui.TextSize(base.LabelSlack+tx), ui.Px(sub.Line))
		c.NextPadding(tx, 0, 0, 0)
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
	if g := tb.rc.FlexAfter[tb.styleIndex()]; tb.prev == tbCapsule && g > 0 {
		tb.space(theme.Snap(tb.k.Pt(g)))
	}
	tb.n++
	if tb.titled {
		c.NextSize(ui.Px(tb.k.Theme().Window.Capsule.WideGap), ui.Px(0))
		tb.used += tb.k.Theme().Window.Capsule.WideGap
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

// End closes the toolbar. When items did not fit their panes it builds the
// overflow button (») at the band's end, whose menu lists them (the system's,
// with native menus: a choice comes back, in a later frame, as the item's
// Clicked); otherwise it reports them and the button's press for the app's
// own menu.
func (tb *Toolbar) End() ToolbarOverflow {
	var res ToolbarOverflow
	if !tb.open {
		return res
	}
	c := tb.k.Context()
	tb.closeGroup()
	if tb.nOv > 0 {
		res = tb.overflowButton()
	}
	c.Pop() // the row
	c.Pop() // the band
	tb.open = false
	return res
}

// overflowButton builds the » capsule at the end of the last row.
func (tb *Toolbar) overflowButton() ToolbarOverflow {
	k := tb.k
	c := k.Context()
	res := ToolbarOverflow{N: tb.nOv, Names: tb.ov}
	names := res.Names[:res.N]
	tb.space(tb.gapW(tbCapsule))
	h := tb.capsuleH()
	w := tb.overflowW()
	p := base.Press{Key: tb.ovKey, Name: str.ShowMore, Hover: base.HoverOn, Visual: k.VisualOf(tb.ovKey, base.HoverOn)}
	flags := tb.capsuleLook(p.Visual, h)
	k.BeginPress(&p, ui.RoleButton, ui.Px(w), ui.Px(h), flags, nil)
	tb.glass(p.ID)
	// », two chevrons
	fg := tb.glyph(p.Visual)
	if f, disp := k.Icons().Prepare(c, icon.ChevronRight, fg); f != 0 {
		cw, chh := k.IconSize(icon.ChevronRight)
		d := theme.Snap(k.Pt(3.5))
		x := theme.Snap((w - cw - d) / 2)
		for i := range 2 {
			theme.At(c, x+float32(i)*d, theme.Snap((h-chh)/2), cw, chh)
			c.BoxKeyed(f|base.Floating, ui.KeyN(tb.ovKey, uint64(i)+1), disp)
		}
	}
	tb.highlight(p.ID, p.Visual, h)
	k.FocusRing(p.ID, p.Visual, h/2)
	sig := k.EndPress(&p)
	c.MenuChoice(p.ID, names) // taken as the items were built (BeginToolbar)
	if sig.Clicked && !k.Disabled() {
		if c.NativeMenus() {
			c.RequestMenu(ui.MenuRequest{Kind: ui.MenuList, Owner: tb.ovKey, Options: names, Selected: -1, Rect: c.Rect(p.ID)})
		} else {
			res.Clicked = true
		}
	}
	return res
}
