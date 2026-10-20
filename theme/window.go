package theme

// Window-level tokens (D52): the geometry of a macOS 27.2 window measured
// live by tools/appkitscenes (docs/design/liquid-glass-reference.md), for the
// window chrome (D4) and the layouts built on it.

// ToolbarStyle is the style of a window's titlebar and toolbar.
type ToolbarStyle uint8

const (
	// ToolbarPlain is a titled window with a full-size content view and no
	// toolbar: 32 pt.
	ToolbarPlain ToolbarStyle = iota
	// ToolbarCompact is a unified compact toolbar: 40 pt, 24 pt capsules.
	ToolbarCompact
	// ToolbarUnified is a unified toolbar: 52 pt, 36 pt capsules.
	ToolbarUnified
	// ToolbarRex is Rex's titlebar band: 46 pt, tabs in a capsule.
	ToolbarRex

	// NumToolbarStyles is the number of toolbar styles.
	NumToolbarStyles
)

// ToolbarSpec is one toolbar style's geometry, in points from the window's
// top left (pixels in a Resolved theme).
type ToolbarSpec struct {
	// Height is the titlebar plus the toolbar: where content's safe area
	// starts and the scroll-edge line lies.
	Height float32
	// Traffic is the close button's centre (x, y). The minimise and zoom
	// buttons follow at WindowSpec.TrafficSpacing. This is the unit
	// app.Config.TrafficLights and TrafficCentrePt take.
	Traffic [2]float32
	// Title is the title's text style; TitleY the top of its line box.
	Title  TextStyle
	TitleY float32
	// TitleGap is the space between the title and the toolbar capsule
	// before it.
	TitleGap float32
	// TitleInset is the space between the title and the content pane's
	// leading edge when no capsule precedes it in the pane (unified 18,
	// compact 10). TitleX applies both.
	TitleInset float32
	// CapsuleH is the height of a toolbar item's capsule (0: none) and
	// CapsuleY its top; the radius is CapsuleH/2.
	CapsuleH, CapsuleY float32
	// ItemW is the width of a one-icon item's capsule: the narrowest, since
	// an item is its symbol and 12 (measured on 27.2: 27 for trash, 31.5 for
	// sidebar.left in the compact style; at least 36, a circle, in the
	// unified one).
	ItemW float32
	// FirstGap is the space between the zoom button's right edge and the
	// first item.
	FirstGap float32
}

// CapsuleSpec is how toolbar items group into glass capsules, in points.
type CapsuleSpec struct {
	// GroupGap is the space between items inside one capsule (a group).
	GroupGap float32
	// Gap is the space between two neighbouring capsules; WideGap next to a
	// menu, a flexible space or a tracking separator.
	Gap, WideGap float32
	// NavW is the width of the back/forward capsule; PickerW a select-one
	// group collapsed to a pop-up.
	NavW, PickerW float32
	// MenuW is the narrowest a menu item is, MenuPad what it takes besides
	// its symbol (the chevron and the sides): one minimum plus a fit to the
	// item's content, not several widths (MenuItemW). Measured on 27.2:
	// 44.5 for ellipsis.circle and 47.5 for a 20-wide symbol.
	MenuW, MenuPad float32
	// Glyph is the point size of a toolbar symbol: 13 in both styles (the
	// symbols AppKit draws are 15 to 20 pt wide).
	Glyph float32
}

// SidebarSpec is a source-list sidebar's geometry (medium row size), in
// points from the sidebar's left edge and a row's top.
type SidebarSpec struct {
	// RowH is a row; HeaderH a group header row; GroupGap the extra space
	// above each group after the first.
	RowH, HeaderH, GroupGap float32
	// IconX and Icon place a row's symbol (Icon square); TextX its text;
	// HeaderX a header's text.
	IconX, Icon, TextX, HeaderX float32
	// Indent is the indentation per level.
	Indent float32
	// SearchH is the search field's height (a capsule); SearchInset its
	// distance from the sidebar's sides.
	SearchH, SearchInset float32
}

// SelectionSpec is the shape of an inset selection (sidebar rows, inset
// tables): Inset from the content's sides, full row height, Radius. In a
// table, contiguous rows merge into one block with a Divider-wide line of
// SelectionDivider between them.
type SelectionSpec struct {
	// Inset is the distance from each side of the content.
	Inset float32
	// Radius is the corner radius.
	Radius float32
	// Divider is the width of the line between merged rows.
	Divider float32
}

// ScrollEdgeSpec is the toolbar's scroll-edge effect (NSScrollPocket): no bar
// while nothing is under the toolbar in an active window; a near-opaque bar
// over a blurred copy of the content and a hairline at the toolbar's bottom
// once content is under it, and always in an inactive window
// (ScrollEdgeVisible). A table's header joins the pocket.
type ScrollEdgeSpec struct {
	// Line is the width of the line under the bar (a Lines.Hairline).
	Line float32
	// AccessoryPad is the band added above and below a bottom accessory (a
	// composer) for its own pocket.
	AccessoryPad float32
}

// WindowSpec is the window-level tokens.
type WindowSpec struct {
	// Radius is the window's corner radius (16, all styles; the system draws
	// it).
	Radius float32
	// TrafficD is a window button's diameter; TrafficSpacing the distance
	// between two buttons' centres.
	TrafficD, TrafficSpacing float32
	// Toolbars are the toolbar styles, indexed by ToolbarStyle.
	Toolbars [NumToolbarStyles]ToolbarSpec
	// Capsule is how items group into capsules (unified; compact items are
	// CapsuleH tall in the same arrangement).
	Capsule CapsuleSpec
	// Sidebar is the sidebar.
	Sidebar SidebarSpec
	// Selection is the inset selection.
	Selection SelectionSpec
	// GroupRadius is a group box's radius; it has no border and hairlines
	// between its rows.
	GroupRadius float32
	// ScrollEdge is the scroll-edge effect.
	ScrollEdge ScrollEdgeSpec
}

// MacWindow is the window geometry of macOS 27.2 (build 26B5091g), measured
// by tools/appkitscenes and tools/appkitref; Rex's style is the Rex
// benchmark's band.
var MacWindow = WindowSpec{
	Radius: 16, TrafficD: TrafficDiameterPt, TrafficSpacing: TrafficSpacingPt,
	Toolbars: [NumToolbarStyles]ToolbarSpec{
		ToolbarPlain:   {Height: 32, Traffic: [2]float32{16, 16}, Title: StyleWindowTitle, TitleY: 8, TitleGap: 10, TitleInset: 10},
		ToolbarCompact: {Height: 40, Traffic: [2]float32{19, 20}, Title: StyleWindowTitle, TitleY: 12, TitleGap: 10, TitleInset: 10, CapsuleH: 24, CapsuleY: 8, ItemW: 27, FirstGap: 19.5},
		ToolbarUnified: {Height: 52, Traffic: [2]float32{26, 26}, Title: StyleToolbarTitle, TitleY: 16.5, TitleGap: 10, TitleInset: 18, CapsuleH: 36, CapsuleY: 8, ItemW: 36, FirstGap: 17},
		ToolbarRex:     {Height: 46, Traffic: [2]float32{23, 23}, Title: StyleWindowTitle, TitleY: 15, TitleGap: 10, TitleInset: 10},
	},
	Capsule:     CapsuleSpec{GroupGap: 8, Gap: 4, WideGap: 8, NavW: 86, PickerW: 46, MenuW: 44.5, MenuPad: 27.5, Glyph: 13},
	Sidebar:     SidebarSpec{RowH: 32, HeaderH: 19, GroupGap: 13, IconX: 19, Icon: 20, TextX: 43, HeaderX: 14, Indent: 13, SearchH: 24, SearchInset: 10},
	Selection:   SelectionSpec{Inset: 10, Radius: 8, Divider: 1},
	GroupRadius: 12,
	ScrollEdge:  ScrollEdgeSpec{Line: 0.5, AccessoryPad: 9},
}

// MenuItemW is the width of a menu item whose symbol is symbolW wide: the
// symbol and MenuPad, at least MenuW. Points or pixels, as c is.
func (c *CapsuleSpec) MenuItemW(symbolW float32) float32 { return max(symbolW+c.MenuPad, c.MenuW) }

// TitleX is where the title starts: TitleGap after the right edge of the
// last capsule before it (prevRight), but at least TitleInset into the
// content pane that starts at paneX. Pass a prevRight of 0 or less when no
// capsule precedes the title. Points or pixels, as t is.
func (t *ToolbarSpec) TitleX(paneX, prevRight float32) float32 {
	return max(paneX+t.TitleInset, prevRight+t.TitleGap)
}

// TrafficCentre is the close button's centre of style s in points from the
// window's top left (MacWindow), the unit app.Config.TrafficLights takes.
func TrafficCentre(s ToolbarStyle) [2]float32 {
	if s >= NumToolbarStyles {
		s = ToolbarPlain
	}
	return MacWindow.Toolbars[s].Traffic
}

// ScrollEdgeVisible reports whether the toolbar's scroll-edge bar and line
// show: when content is scrolled under the toolbar, and always while the
// window is inactive.
func ScrollEdgeVisible(contentUnder, windowActive bool) bool { return contentUnder || !windowActive }

func (w WindowSpec) scaled(k float32) WindowSpec {
	w.Radius *= k
	w.TrafficD *= k
	w.TrafficSpacing *= k
	for i := range w.Toolbars {
		t := &w.Toolbars[i]
		for _, p := range [...]*float32{&t.Height, &t.Traffic[0], &t.Traffic[1], &t.TitleY, &t.TitleGap, &t.TitleInset, &t.CapsuleH, &t.CapsuleY, &t.ItemW, &t.FirstGap} {
			*p *= k
		}
	}
	c := &w.Capsule
	for _, p := range [...]*float32{&c.GroupGap, &c.Gap, &c.WideGap, &c.NavW, &c.PickerW, &c.MenuW, &c.MenuPad} {
		*p *= k
	}
	s := &w.Sidebar
	for _, p := range [...]*float32{&s.RowH, &s.HeaderH, &s.GroupGap, &s.IconX, &s.Icon, &s.TextX, &s.HeaderX, &s.Indent, &s.SearchH, &s.SearchInset} {
		*p *= k
	}
	w.Selection.Inset *= k
	w.Selection.Radius *= k
	w.Selection.Divider *= k
	w.GroupRadius *= k
	w.ScrollEdge.Line = 1
	w.ScrollEdge.AccessoryPad *= k
	return w
}
