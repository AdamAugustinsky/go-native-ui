package rexui

import (
	"math"

	"gonativeui/icon"
	"gonativeui/menu"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// Tabs are Rex's tabs (rexlook's titlebar capsule, the look the user chose
// over AppKit's tab bars): the tabs share a capsule equally up to a maximum
// width, the chosen one sits on a pill that glides to it, and an unchosen one
// lights up under the pointer. A tab is chosen on the press, as AppKit's
// window tabs are; a close button shows on the hovered tab, and a middle click
// closes it too. A tab dragged sideways moves through the others, which slide
// out of its way; it settles into its place when released. When the tabs do
// not fit at their minimum width the strip scrolls (the wheel or a trackpad
// over it) and keeps the chosen tab in view. ⌃Tab and ⌃⇧Tab, ⌘⇧] and ⌘⇧[
// choose the next and previous tab, and ⌘1…⌘8 and ⌘9 a tab and the last (as
// menu commands, TabBar.Commands, or as keys when no menu claims them).
//
// The app owns the tabs: it describes them every frame as a []Tab and applies
// what the bar reports (TabsEvents): the choice, a close, a move. The bar
// keeps only what the interaction needs between frames (TabBar).
//
// The bar works in the titlebar band (rexlook) or in content. In a band built
// at the window's root, its window-drag map comes from the band: the tabs
// take their presses, the space around them moves the window.

// MaxTabs is the most tabs a TabBar handles; tabs past it are not shown.
const MaxTabs = 64

// Tab is one tab, as the app describes it every frame.
type Tab struct {
	// ID is the tab's stable identity, unique in the bar and not 0: the
	// tab's keys, animations and drag follow it when tabs move.
	ID uint64
	// Title is the tab's name; Detail more text after it (Rex's path), drawn
	// lighter while the tab is not chosen. Both fade out where they do not
	// fit.
	Title, Detail string
	// Glyph is a symbol before the title (Tabs draws it; with BeginTab the
	// app draws what it likes in the Lead).
	Glyph Glyph
	// Lead is the room, in points from the tab's leading edge, before the
	// title (0: the recipe's, which leaves room for the glyph if there is
	// one).
	Lead float32
	// NoClose hides the close button and ignores a middle click.
	NoClose bool
}

// TabsVariant selects the tabs' look.
type TabsVariant uint8

const (
	// TabsRex is Rex's capsule: a lilac-tinted capsule with a white pill in
	// light, ink in dark (rexlook's titlebar).
	TabsRex TabsVariant = iota
	// TabsNeutral is the same shapes in neutral greys, for apps that are not
	// tinted.
	TabsNeutral

	// NumTabsVariants is the number of variants.
	NumTabsVariants
)

// scaled is p at mix t with both its alphas scaled by a.
func scaled(p Pair, a, t float32) uint32 {
	return theme.ColA(theme.MixRGB(p.Light, p.Dark, t), theme.Lerp(p.Alpha[0]*a, p.Alpha[1]*a, t))
}

// toward is p mixed toward q by u (both at mix t), alpha from p scaled by a.
func toward(p, q Pair, u, t, a float32) uint32 {
	l, d := theme.MixRGB(p.Light, q.Light, u), theme.MixRGB(p.Dark, q.Dark, u)
	return theme.ColA(theme.MixRGB(l, d, t), theme.Lerp(p.Alpha[0], p.Alpha[1], t)*a)
}

// TabsRecipe is the look and feel of tabs. Lengths are in points; rates in
// theme.Motion's unit.
type TabsRecipe struct {
	// Height is a tab's height; Inset the capsule's padding around the tabs
	// (x, y); MinW and MaxW a tab's width limits; CapsuleRadius and
	// PillRadius the corners (the tabs' hover and press fills take
	// PillRadius too).
	Height        float32
	Inset         [2]float32
	MinW, MaxW    float32
	CapsuleRadius float32
	PillRadius    float32
	// Capsule fills the capsule top to bottom; CapsuleBackdrop replaces it
	// over a system material (theme.Resolved.Backdrop): its tint half
	// covering the material keeps the titles' contrast.
	Capsule, CapsuleBackdrop [2]Pair
	// Pill fills the chosen tab top to bottom, PillRim draws its two end
	// caps, PillShadow its two shadows (y offset and blur in points; the
	// alphas at rest in a key window, scaled toward ShadowInactive of them
	// while the window is not key).
	Pill           [2]Pair
	PillRim        Pair
	PillShadow     [2]Pair
	PillShadowGeom [2][2]float32
	ShadowInactive float32
	// Sep is the separator between two tabs that are both unchosen (at
	// SepAlpha), SepW wide from SepY0 to SepY1 below the capsule's top.
	Sep          Pair
	SepAlpha     float32
	SepW         float32
	SepY0, SepY1 float32
	// Hover lights an unchosen tab under the pointer; Press darkens a held
	// tab.
	Hover, Press Pair
	// Name and Detail colour the title and the detail: [0] unchosen, [1]
	// chosen, mixed by the choice's animation.
	Name, Detail [2]Pair
	// InactiveOpacity is how much of the tabs' content shows while the
	// window is not key (the titlebar's dimming), and KeyRate the rate of
	// that change.
	InactiveOpacity, KeyRate float32
	// LabelAlpha are the label alphas over a backdrop, which keep AppKit's
	// contrast floors (docs/design/native-shell-spike.md): [role] = {key
	// window light, key window dark, not key} for the chosen tab, an
	// unchosen tab's name, and the detail.
	LabelAlpha [3][3]float32
	// ChooseRate is how fast the pill glides; SlideRate how fast tabs slide
	// to their places after a move, a close or a new tab.
	ChooseRate, SlideRate float32
	// UIFont sets the titles in the theme's UI font (Rex: 14 pt, the
	// reference's); otherwise in Style.
	UIFont bool
	Style  theme.TextStyle
	// TextX is where the title starts without a glyph, TextXGlyph with one,
	// GlyphX the glyph's centre; TextEnd the room kept after the title;
	// Baseline the title's baseline below the tab's top.
	TextX, TextXGlyph, GlyphX, TextEnd, Baseline float32
	// Close is the close button's side, CloseRight its centre from the
	// tab's trailing edge; CloseFill lights it under the pointer.
	Close, CloseRight float32
	CloseFill         Pair
	// DragStart is how far the pointer moves before a press becomes a drag;
	// Wheel how far a wheel notch scrolls the strip; FadeW the width of the
	// fades at a scrolled strip's ends.
	DragStart, Wheel, FadeW float32
}

// TabsRecipes are the variants' recipes. TabsRex is rexlook's titlebar as it
// was drawn before it became this component (its rexcompare is 40/40):
// lengths in points are its reference pixels halved.
var TabsRecipes = [NumTabsVariants]TabsRecipe{
	TabsRex: {
		Height: 30, Inset: [2]float32{1.5, 2}, MinW: 20, MaxW: 260, CapsuleRadius: 17, PillRadius: 15,
		Capsule:         [2]Pair{Solid(0xE8DCE9, 0x3B343B), Solid(0xE2DCE2, 0x37313A)},
		CapsuleBackdrop: [2]Pair{PairA(0xE8DCE9, 0x3B343B, 0.5, 0.55), PairA(0xE2DCE2, 0x37313A, 0.5, 0.55)},
		Pill:            [2]Pair{Solid(0xFFFAFF, 0x4B4248), Solid(0xFCF8FC, 0x453E44)},
		PillRim:         Solid(0xBEB9C0, 0x7A6E76),
		PillShadow:      [2]Pair{PairA(0x3A2A4A, 0x000000, 0.13, 0.40), PairA(0x3A2A4A, 0x000000, 0.14, 0.45)},
		PillShadowGeom:  [2][2]float32{{1, 1.25}, {0, 0.75}},
		ShadowInactive:  0.45,
		Sep:             PairA(0x8C8290, 0xC2B6BD, 1, 0.6), SepAlpha: 0.45, SepW: 1, SepY0: 8, SepY1: 25.5,
		Hover:           PairA(0xFFFFFF, 0xFFFFFF, 0.55, 0.10),
		Press:           PairA(0x3A2A4A, 0xFFFFFF, 0.09, 0.08),
		Name:            [2]Pair{Solid(0x5A5A60, 0xB0A7AB), Solid(0x1E2227, 0xF3EFF0)},
		Detail:          [2]Pair{Solid(0x928F95, 0x948B90), Solid(0x1E2227, 0xF3EFF0)},
		InactiveOpacity: 0.55, KeyRate: 12,
		LabelAlpha: [3][3]float32{
			tabLabelChosen: {0.85, 0.92, 0.52},
			tabLabelName:   {0.78, 0.86, 0.48},
			tabLabelDetail: {0.64, 0.84, 0.44},
		},
		ChooseRate: 26, SlideRate: 26,
		UIFont: true, Style: theme.StyleTabTitle,
		TextX: 14, TextXGlyph: 34, GlyphX: 18, TextEnd: 10, Baseline: 19.5,
		Close: 16, CloseRight: 13, CloseFill: PairA(0x2A2030, 0xFFFFFF, 0.08, 0.08),
		DragStart: 4, Wheel: 24, FadeW: 18,
	},
	TabsNeutral: {
		Height: 24, Inset: [2]float32{2, 2}, MinW: 40, MaxW: 220, CapsuleRadius: 14, PillRadius: 12,
		Capsule:         [2]Pair{PairA(0x000000, 0xFFFFFF, 0.06, 0.08), PairA(0x000000, 0xFFFFFF, 0.06, 0.08)},
		CapsuleBackdrop: [2]Pair{PairA(0x000000, 0xFFFFFF, 0.06, 0.08), PairA(0x000000, 0xFFFFFF, 0.06, 0.08)},
		Pill:            [2]Pair{Solid(0xFFFFFF, 0x5A5A5E), Solid(0xFFFFFF, 0x545458)},
		PillRim:         PairA(0x000000, 0x000000, 0.06, 0.2),
		PillShadow:      [2]Pair{PairA(0x000000, 0x000000, 0.10, 0.30), PairA(0x000000, 0x000000, 0.08, 0.25)},
		PillShadowGeom:  [2][2]float32{{1, 1.5}, {0, 0.5}},
		ShadowInactive:  0.5,
		Sep:             PairA(0x000000, 0xFFFFFF, 0.2, 0.2), SepAlpha: 1, SepW: 1, SepY0: 7, SepY1: 21,
		Hover:           PairA(0x000000, 0xFFFFFF, 0.05, 0.06),
		Press:           PairA(0x000000, 0xFFFFFF, 0.08, 0.10),
		Name:            [2]Pair{PairA(0x000000, 0xFFFFFF, 0.70, 0.70), PairA(0x000000, 0xFFFFFF, 0.88, 0.92)},
		Detail:          [2]Pair{PairA(0x000000, 0xFFFFFF, 0.45, 0.45), PairA(0x000000, 0xFFFFFF, 0.55, 0.55)},
		InactiveOpacity: 0.6, KeyRate: 12,
		LabelAlpha: [3][3]float32{
			tabLabelChosen: {0.85, 0.92, 0.52},
			tabLabelName:   {0.70, 0.78, 0.48},
			tabLabelDetail: {0.50, 0.60, 0.40},
		},
		ChooseRate: 26, SlideRate: 26,
		Style: theme.StyleBody,
		TextX: 12, TextXGlyph: 30, GlyphX: 17, TextEnd: 10, Baseline: 16,
		Close: 16, CloseRight: 12, CloseFill: PairA(0x000000, 0xFFFFFF, 0.08, 0.10),
		DragStart: 4, Wheel: 24, FadeW: 16,
	},
}

// The label roles over a backdrop (TabsRecipe.LabelAlpha).
const (
	tabLabelChosen = iota
	tabLabelName
	tabLabelDetail
)

// TabsOpts are a tab bar's options, passed by value.
type TabsOpts struct {
	// Variant is the look.
	Variant TabsVariant
	// X0 and X1 are where the bar may lie and Y its top, in pixels in the
	// current parent's coordinates: the capsule starts at X0, is as wide as
	// its tabs at their width limits (all of X1 − X0 with Fill), and as tall
	// as the recipe's tab and insets.
	X0, X1, Y float32
	// Fill makes the tabs share all of X0..X1, however wide that makes them.
	Fill bool
	// Focusable makes the bar a tab stop (in content): the chosen tab takes
	// the focus, and while it has it the arrow keys and ⌃Tab move the
	// choice; ⌘1…⌘9 stay the window's tabs'. In a titlebar the tabs do not
	// take the focus, as AppKit's window tabs do not, and take the window's
	// tab shortcuts whatever has the focus.
	Focusable bool
	// NoDrag keeps the tabs in place.
	NoDrag bool
}

// TabsEvents is what the bar reports for a frame. Indexes are into the []Tab
// the frame was given; -1 is none.
type TabsEvents struct {
	// Select is the tab to choose: pressed, or reached with the keyboard.
	Select int
	// Close is the tab whose close button was clicked or that was
	// middle-clicked.
	Close int
	// MoveFrom and MoveTo: the tab at MoveFrom was dragged to MoveTo. The
	// app moves it there (the tabs between shift by one), in this frame.
	MoveFrom, MoveTo int
}

func noTabsEvents() TabsEvents { return TabsEvents{Select: -1, Close: -1, MoveFrom: -1, MoveTo: -1} }

// TabBar is the state of one tab bar, owned by the app: what the
// interaction needs between frames. The zero value is ready to use; it must
// not be copied once in use.
type TabBar struct {
	// Commands, when set (Commands[0] != 0), are the menu commands that
	// choose tabs 1 to 8 and the last (⌘1…⌘9; TabCommands makes them).
	Commands [9]menu.ID
	// Mod is the modifier of the number and bracket shortcuts (0: the
	// platform's, ui.PrimaryMod: ⌘ on macOS, Ctrl elsewhere). An app whose
	// shortcuts are ⌘ everywhere (rexlook) sets ui.ModSuper.
	Mod ui.Mods

	key       ui.Key
	variant   TabsVariant
	focusable bool
	noDrag    bool
	n         int
	sel       int
	ids       [MaxTabs]uint64
	rects     [MaxTabs][4]float32 // window pixels, last frame, as shown
	capRect   [4]float32          // window pixels
	pillRect  [4]float32          // window pixels
	strip     [2]float32          // the strip's visible extent, window pixels
	slot      float32             // a slot's width, pixels
	fit       bool                // the tabs fitted (no scroll)
	scroll    float32             // overflow scroll, pixels
	revealSel bool                // scroll the chosen tab into view
	selID     uint64              // the chosen tab's id last frame (a change reveals it)

	press    uint64     // the tab the left button went down on
	pressIdx int        // its index then: Escape during the drag puts it back
	pressAt  [2]float32 // where, window pixels
	grab     float32    // the pointer's x in the pressed tab, pixels
	dragging bool
	dragX    float32 // the pointer's x while dragging, window pixels
	drop     uint64  // the tab dropped last: it settles from dropAt
	dropGen  uint64
	dropAt   float32 // its slot position when released
	middle   uint64  // the tab the middle button went down on
}

// TabCommands are the menu commands for ⌘1…⌘8 and ⌘9 with ids first,
// first+1 … first+8, titled "Show Tab 1" … "Show Last Tab" (Strings.ShowTab
// and ShowLastTab, as they are when it is called), for the app's menu model
// (a Window menu). Set TabBar.Commands to their ids.
func TabCommands(first menu.ID) []menu.Command {
	cmds := make([]menu.Command, 9)
	for i := range cmds {
		title := str.ShowLastTab
		if i < 8 {
			title = str.ShowTab + " " + string(rune('1'+i))
		}
		cmds[i] = menu.Command{ID: first + menu.ID(i), Title: title, Shortcut: menu.Shortcut{Key: ui.KeyCode('1' + i), Primary: true}}
	}
	return cmds
}

// TabKey is the key of tab id's box: its signal, hover and press.
func (b *TabBar) TabKey(id uint64) ui.Key { return ui.KeyN(ui.KeyN(b.key, 7), id) }

func (b *TabBar) closeKey(id uint64) ui.Key { return ui.KeyN(ui.KeyN(b.key, 8), id) }

// TabRect is where tab i was shown in the last frame, in window pixels (x0,
// y0, x1, y1).
func (b *TabBar) TabRect(i int) [4]float32 {
	if i < 0 || i >= b.n {
		return [4]float32{}
	}
	return b.rects[i]
}

// CapsuleRect is where the capsule was built in the last frame, and
// PillRect the pill (zero without a chosen tab), in window pixels.
func (b *TabBar) CapsuleRect() [4]float32 { return b.capRect }

// PillRect is where the pill was built in the last frame, in window pixels.
func (b *TabBar) PillRect() [4]float32 { return b.pillRect }

// Dragging reports whether a tab is being dragged.
func (b *TabBar) Dragging() bool { return b.dragging }

// Held is the id of the tab the left button went down on and still holds
// (0 for none), as of the last Update: an app that takes a tab dragged out
// of the strip (onto its content, to split it there) follows the pointer
// while it is held, and acts on the release, when Held is still the tab's
// before that frame's Update.
func (b *TabBar) Held() uint64 { return b.press }

// Update applies the frame's input to the bar before anything that depends
// on the choice is built: presses, the close buttons, middle clicks, drags,
// the scroll and the keys. sel is the chosen tab. Call it once per frame
// before Begin (Tabs calls both).
func (b *TabBar) Update(k *base.Kit, tabs []Tab, sel int) TabsEvents {
	ev := noTabsEvents()
	c := k.Context()
	n := min(len(tabs), MaxTabs)
	if b.key == 0 || k.Disabled() {
		return ev
	}
	in := c.Input()
	rc := &TabsRecipes[b.variant%NumTabsVariants]
	prim := b.Mod
	if prim == 0 {
		prim = ui.PrimaryMod()
	}

	// the pointer: a press chooses, a click on × or a middle click closes
	for i := 0; i < n; i++ {
		id := tabs[i].ID
		s := c.PeekSignal(b.TabKey(id))
		if s.Pressed || s.Action&ui.ActPress != 0 {
			ev.Select = i
			if b.focusable && s.Pressed { // the pressed tab takes the focus, as a pressed control does
				c.SetFocus(b.TabKey(id))
			}
			b.press, b.pressIdx, b.pressAt, b.dragging = id, i, [2]float32{in.MouseX, in.MouseY}, false
			for j := 0; j < b.n; j++ { // where it was shown, wherever it is in tabs now
				if b.ids[j] == id {
					b.grab = in.MouseX - b.rects[j][0]
					break
				}
			}
		}
		if !tabs[i].NoClose && k.PressedBefore(b.closeKey(id)) {
			ev.Close = i
		}
	}
	if in.Pressed[ui.ButtonMiddle] {
		b.middle = b.tabAt(in.MouseX, in.MouseY)
	}
	if in.Released[ui.ButtonMiddle] {
		if id := b.tabAt(in.MouseX, in.MouseY); id != 0 && id == b.middle {
			if i := indexOf(tabs[:n], id); i >= 0 && !tabs[i].NoClose {
				ev.Close = i
			}
		}
		b.middle = 0
	}

	// a drag: the tab follows the pointer, and moves through the others
	if b.press != 0 {
		s := c.PeekSignal(b.TabKey(b.press))
		i := indexOf(tabs[:n], b.press)
		switch {
		case i < 0 || !s.Dragging:
			if b.dragging && i >= 0 { // dropped: settle from where it is
				b.drop, b.dropAt = b.press, b.dragSlot(n)
				b.dropGen++
			}
			b.press, b.dragging = 0, false
		case !b.dragging && !b.noDrag && n > 1 && abs32(in.MouseX-b.pressAt[0]) >= k.Pt(rc.DragStart):
			b.dragging = true
		}
		if b.dragging && i >= 0 && c.TakeKey(ui.KeyEscape, 0) {
			// cancelled: back to where it started, settling from where it is
			b.drop, b.dropAt = b.press, b.dragSlot(n)
			b.dropGen++
			if to := min(b.pressIdx, n-1); to != i && to >= 0 {
				ev.MoveFrom, ev.MoveTo = i, to
			}
			b.press, b.dragging = 0, false
		}
		if b.dragging && i >= 0 {
			b.dragX = in.MouseX
			if to := int(b.dragSlot(n) + 0.5); to != i && to >= 0 && to < n {
				ev.MoveFrom, ev.MoveTo = i, to
			}
		}
	}

	// the wheel over a strip that does not fit
	if !b.fit && in.MouseX >= b.capRect[0] && in.MouseX < b.capRect[2] && in.MouseY >= b.capRect[1] && in.MouseY < b.capRect[3] {
		d := in.ScrollX + in.ScrollY
		if !in.Precise {
			d *= k.Pt(rc.Wheel)
		}
		b.scroll -= d
	}

	// the keys: a window's tabs (in the titlebar) take the window's tab
	// shortcuts; tabs in content take them, and the arrows, while they hold
	// the focus
	focused := b.focusable && sel >= 0 && sel < n && c.FocusKey() == b.TabKey(tabs[sel].ID)
	if !b.focusable || focused {
		next := 0
		switch {
		case c.TakeKey(ui.KeyTab, ui.ModCtrl), c.TakeKey(ui.KeyRightBracket, prim|ui.ModShift):
			next = 1
		case c.TakeKey(ui.KeyTab, ui.ModCtrl|ui.ModShift), c.TakeKey(ui.KeyLeftBracket, prim|ui.ModShift):
			next = -1
		}
		if next != 0 && n > 1 {
			ev.Select = (sel + next + n) % n
		}
	}
	for d := 0; d < 9 && !b.focusable; d++ {
		fired := b.Commands[0] != 0 && k.Command(b.Commands[d])
		if fired || c.TakeKey(ui.KeyCode('1'+d), prim) {
			if d == 8 {
				ev.Select = n - 1
			} else if d < n {
				ev.Select = d
			}
		}
	}
	if focused && n > 1 {
		switch {
		case c.TakeKey(ui.KeyRight, 0):
			ev.Select = (sel + 1) % n
		case c.TakeKey(ui.KeyLeft, 0):
			ev.Select = (sel + n - 1) % n
		}
		if ev.Select >= 0 && ev.Select != sel {
			c.SetFocus(b.TabKey(tabs[ev.Select].ID))
			c.SetFocusVisible(true)
		}
	}
	if ev.Select >= 0 && ev.Select != sel {
		b.revealSel = true
	}
	return ev
}

// tabAt is the tab shown at (x, y) in the last frame, 0 for none.
func (b *TabBar) tabAt(x, y float32) uint64 {
	if x < b.strip[0] || x >= b.strip[1] {
		return 0
	}
	for i := 0; i < b.n; i++ {
		r := b.rects[i]
		if x >= r[0] && x < r[2] && y >= r[1] && y < r[3] {
			return b.ids[i]
		}
	}
	return 0
}

// dragSlot is the dragged tab's position in slots (0 is the first), from the
// pointer, within the n slots.
func (b *TabBar) dragSlot(n int) float32 {
	if b.slot <= 0 {
		return 0
	}
	p := (b.dragX - b.grab - b.strip[0] + b.scroll) / b.slot
	return min(max(p, 0), float32(n-1))
}

func indexOf(tabs []Tab, id uint64) int {
	for i := range tabs {
		if tabs[i].ID == id {
			return i
		}
	}
	return -1
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// TabsBuild is a tab bar being built: Begin builds the capsule, the pill and
// the separators; BeginTab and EndTab each tab, in order; End closes the bar.
type TabsBuild struct {
	b     *TabBar
	k     *base.Kit
	tabs  []Tab
	rc    *TabsRecipe
	n     int
	sel   int
	ox    float32 // the capsule's origin in the parent, pixels
	oy    float32
	wox   float32 // the parent's origin in the window, pixels
	woy   float32
	kw    float32 // the window's key state, animated
	tabY0 float32 // the tabs' top and bottom in the parent, snapped
	tabY1 float32
	top   float32          // the tabs' top in the parent, unsnapped
	pos   [MaxTabs]float32 // each tab's x in the parent, unsnapped
	act   [MaxTabs]float32
	hot   [MaxTabs]float32
	slotW float32
	order [MaxTabs]int8 // the order the tabs are built in: the dragged one last
	font  text.FontKey
	met   theme.FontMet
	cap   ui.ID
	open  bool
}

// TabCell is one tab being built (BeginTab to EndTab): where it is, how
// chosen and hovered it is, for the content the app draws in it.
type TabCell struct {
	// Index is the tab's index in the []Tab.
	Index int
	// ID is the tab's box, the current parent from BeginTab to EndTab.
	ID ui.ID
	// Rect is the tab's box in the coordinates of the bar's parent
	// (snapped), X its unsnapped left edge there: a box d pixels into the
	// tab goes at Snap(X+d) − Rect[0] in the tab's own coordinates.
	Rect [4]float32
	X    float32
	// Chosen is how chosen the tab is (the pill's animation), Hot how
	// hovered, KeyW the window's key state (1: key), all 0 to 1.
	Chosen, Hot, KeyW float32
	// Backdrop: a system material shows behind the bar (labels dim by
	// colour, not by opacity).
	Backdrop bool
	dimmed   bool
}

// Tabs builds a tab bar of tabs that edits *sel, drawing each tab's glyph,
// and reports the frame's closes and moves (the choice is applied to *sel).
// The app removes a closed tab and moves a moved one in its own list.
//
//	ev := rexui.Tabs(k, "Tabs", &v.bar, v.tabs, &v.sel, rexui.TabsOpts{X0: x, X1: x1, Y: y})
//	if ev.Close >= 0 { v.close(ev.Close) }
//	if ev.MoveFrom >= 0 { v.move(ev.MoveFrom, ev.MoveTo) }
func Tabs(k *base.Kit, label string, b *TabBar, tabs []Tab, sel *int, o TabsOpts) TabsEvents {
	b.focusable, b.noDrag = o.Focusable, o.NoDrag
	ev := b.Update(k, tabs, *sel)
	if ev.Select >= 0 {
		*sel = ev.Select
	}
	// this frame shows tabs in their order before the move the app makes
	// after this call; *sel follows the chosen tab to its new place
	shown := *sel
	if ev.MoveFrom >= 0 {
		*sel = movedIndex(*sel, ev.MoveFrom, ev.MoveTo)
	}
	t := b.Begin(k, label, tabs, shown, o)
	for i := 0; i < t.N(); i++ {
		cell := t.BeginTab(i)
		t.Glyph(&cell)
		t.EndTab(&cell)
	}
	t.End()
	return ev
}

// movedIndex is where index i is after the element at from moves to to.
func movedIndex(i, from, to int) int {
	switch {
	case i == from:
		return to
	case from < i && i <= to:
		return i - 1
	case to <= i && i < from:
		return i + 1
	}
	return i
}

// Begin starts building the bar of tabs with tabs[sel] chosen: the capsule,
// the pill under the chosen tab and the separators. The label is the tab
// list's accessible name and identifies it among its siblings. Build each
// tab with BeginTab and EndTab, in order, then End.
func (b *TabBar) Begin(k *base.Kit, label string, tabs []Tab, sel int, o TabsOpts) TabsBuild {
	c, r := k.Context(), k.Theme()
	b.focusable, b.noDrag = o.Focusable, o.NoDrag
	rc := &TabsRecipes[o.Variant%NumTabsVariants]
	n := min(len(tabs), MaxTabs)
	s := r.Scale
	display, ident := ui.SplitLabel(label)
	b.key = ui.HashString(c.ParentKey(), ident)
	b.variant = o.Variant % NumTabsVariants
	t := TabsBuild{b: b, k: k, tabs: tabs[:n], rc: rc, n: n, sel: sel}
	if rc.UIFont {
		t.font, t.met = r.Font.UI, r.Met(r.Font.UI)
	} else {
		st := &r.Styles[rc.Style]
		t.font, t.met = st.Font, theme.FontMet{H: st.Line, Off: st.Baseline}
	}

	// the window's key state dims the bar while another window has the
	// keyboard, as AppKit's titlebar content does
	keyTgt := float32(0)
	if !k.Inactive() {
		keyTgt = 1
	}
	keyRate, slideRate, chooseRate := rc.KeyRate, rc.SlideRate, rc.ChooseRate
	if reduced(k) { // Reduce Motion: every change at once
		keyRate, slideRate, chooseRate = theme.Instant, theme.Instant, theme.Instant
	}
	t.kw = c.AnimateValue(ui.KeyN(b.key, 1), keyTgt, keyRate)

	// a tab chosen by the app, or at the start, comes into view as one
	// chosen with the pointer or the keyboard does
	if sel >= 0 && sel < n && tabs[sel].ID != b.selID {
		b.revealSel = true
	}

	// geometry: the tabs share the room equally, within their limits; the
	// count animates, so a close or a new tab widens or narrows the others
	// smoothly while a resize follows the window exactly
	nAnim := c.AnimateValue(ui.KeyN(b.key, 2), float32(n), slideRate)
	if n == 0 {
		nAnim = 0
	}
	ix, iy := rc.Inset[0]*s, rc.Inset[1]*s
	room := o.X1 - o.X0 - 2*ix
	slot := float32(0)
	if nAnim > 0 {
		slot = room / nAnim
		if !o.Fill {
			slot = min(slot, rc.MaxW*s)
		}
		slot = max(slot, rc.MinW*s)
	}
	t.slotW = slot
	full := slot * nAnim
	b.fit = full <= room+0.5
	if b.fit {
		b.scroll = 0
	} else {
		maxScroll := full - room
		if b.revealSel && sel >= 0 && sel < n {
			x0, x1 := float32(sel)*slot, float32(sel+1)*slot
			b.scroll = min(max(b.scroll, x1-room), x0)
		}
		b.scroll = min(max(b.scroll, 0), maxScroll)
		full = room
	}
	b.revealSel = false
	capX1 := o.X0 + ix + full + ix
	if o.Fill {
		capX1 = o.X1
	}
	capH := rc.Height*s + 2*iy
	t.ox, t.oy = theme.Snap(o.X0), theme.Snap(o.Y)
	capsule := [4]float32{t.ox, t.oy, theme.Snap(capX1), theme.Snap(o.Y + capH)}
	t.top = o.Y + iy
	t.tabY0, t.tabY1 = theme.Snap(t.top), theme.Snap(t.top+rc.Height*s)

	// each tab's place: its slot, animated in slots so that tabs slide when
	// they move and a resize moves nothing; the dragged tab follows the
	// pointer, and a dropped one settles from where it was let go
	x0 := o.X0 + ix - b.scroll
	for i := 0; i < n; i++ {
		id := tabs[i].ID
		var p float32
		switch {
		case b.dragging && id == b.press:
			p = b.dragSlot(n)
			c.AnimateValueFrom(ui.KeyN(ui.KeyN(ui.KeyN(b.key, 3), id), b.dropGen+1), p, p, slideRate)
		case id == b.drop:
			p = c.AnimateValueFrom(ui.KeyN(ui.KeyN(ui.KeyN(b.key, 3), id), b.dropGen), b.dropAt, float32(i), slideRate)
		default:
			p = c.AnimateValue(ui.KeyN(ui.KeyN(b.key, 4), id), float32(i), slideRate)
		}
		t.pos[i] = x0 + p*slot
	}

	// how chosen each tab is drives the pill, the titles and the separators
	var sw, sx0, sx1 float32
	for i := 0; i < n; i++ {
		tgt := float32(0)
		if i == sel {
			tgt = 1
		}
		t.act[i] = c.AnimateValue(ui.KeyN(ui.KeyN(b.key, 5), tabs[i].ID), tgt, chooseRate)
		sw += t.act[i]
		sx0 += t.act[i] * theme.Snap(t.pos[i])
		sx1 += t.act[i] * theme.Snap(t.pos[i]+slot)
	}

	// what the next frame's input reads
	t.wox, t.woy = 0, 0
	if pid := c.Parent(); pid != 0 {
		pr := c.Rect(pid)
		t.wox, t.woy = pr[0], pr[1]
	}
	b.n, b.sel, b.slot = n, sel, slot
	b.selID = 0
	if sel >= 0 && sel < n {
		b.selID = tabs[sel].ID
	}
	b.strip = [2]float32{t.wox + o.X0 + ix, t.wox + o.X0 + ix + full}
	b.capRect = [4]float32{t.wox + capsule[0], t.woy + capsule[1], t.wox + capsule[2], t.woy + capsule[3]}
	for i := 0; i < n; i++ {
		b.ids[i] = tabs[i].ID
		b.rects[i] = [4]float32{t.wox + theme.Snap(t.pos[i]), t.woy + t.tabY0, t.wox + theme.Snap(t.pos[i]+slot), t.woy + t.tabY1}
	}

	// the capsule: a group that clips what is inside it (the pill's
	// shadows stay within)
	capTop, capBot := rc.Capsule[0].At(r.T), rc.Capsule[1].At(r.T)
	if r.Backdrop {
		capTop, capBot = rc.CapsuleBackdrop[0].At(r.T), rc.CapsuleBackdrop[1].At(r.T)
	}
	theme.At(c, capsule[0], capsule[1], capsule[2]-capsule[0], capsule[3]-capsule[1])
	c.NextBG4(capTop, capTop, capBot, capBot)
	c.NextRadius(rc.CapsuleRadius * s)
	c.NextRole(ui.RoleTabList)
	c.NextSemName(display)
	t.cap = c.BoxKeyed(ui.DrawBackground|ui.Clip|base.Floating, b.key, nil)
	c.Push(t.cap)
	t.open = true

	// the pill, with the rim around its two end caps only
	b.pillRect = [4]float32{}
	if sel >= 0 && sel < n {
		pill := [4]float32{theme.Snap(t.pos[sel]), t.tabY0, theme.Snap(t.pos[sel] + slot), t.tabY1}
		if sw > 0.001 && !(b.dragging && tabs[sel].ID == b.press) {
			pill[0], pill[2] = theme.Snap(sx0/sw), theme.Snap(sx1/sw)
		}
		b.pillRect = [4]float32{t.wox + pill[0], t.woy + pill[1], t.wox + pill[2], t.woy + pill[3]}
		theme.At(c, pill[0]-t.ox, pill[1]-t.oy, pill[2]-pill[0], pill[3]-pill[1])
		pTop, pBot := rc.Pill[0].At(r.T), rc.Pill[1].At(r.T)
		c.NextBG4(pTop, pTop, pBot, pBot)
		c.NextRadius(rc.PillRadius * s)
		sh := theme.Lerp(rc.ShadowInactive, 1, t.kw)
		g := &rc.PillShadowGeom
		c.NextShadows(ui.Shadow{OffY: g[0][0] * s, Sigma: g[0][1] * s, Color: scaled(rc.PillShadow[0], sh, r.T)},
			ui.Shadow{OffY: g[1][0] * s, Sigma: g[1][1] * s, Color: scaled(rc.PillShadow[1], sh, r.T)})
		c.NextBorder(rc.PillRim.At(r.T), max(1, s/2))
		c.NextBorderSides(ui.SideLeft | ui.SideRight)
		c.BoxKeyed(ui.DrawBackground|ui.DrawShadow|ui.DrawBorder|base.Floating, ui.KeyN(b.key, 6), nil)
	}

	// separators between two neighbouring tabs that are both unchosen
	for i := 0; i+1 < n; i++ {
		a := (1 - t.act[i]) * (1 - t.act[i+1]) * rc.SepAlpha
		if a < 0.004 || (b.dragging && (tabs[i].ID == b.press || tabs[i+1].ID == b.press)) {
			continue
		}
		e := t.pos[i+1]
		hw := rc.SepW * s / 2
		y0, y1 := theme.Snap(o.Y+rc.SepY0*s), theme.Snap(o.Y+rc.SepY1*s)
		theme.At(c, theme.Snap(e-hw)-t.ox, y0-t.oy, theme.Snap(e+hw)-theme.Snap(e-hw), y1-y0)
		c.NextBG(scaled(rc.Sep, a, r.T))
		c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(ui.KeyN(b.key, 9), uint64(i)), nil)
	}

	for i := 0; i < n; i++ {
		t.order[i] = int8(i)
	}
	if b.dragging { // the dragged tab over the others
		if j := indexOf(t.tabs, b.press); j >= 0 {
			copy(t.order[j:n-1], t.order[j+1:n])
			t.order[n-1] = int8(j)
		}
	}
	return t
}

// N is the number of tabs to build.
func (t *TabsBuild) N() int { return t.n }

// BeginTab builds the j-th tab in drawing order (the dragged tab comes last)
// and makes it the current parent: its hover and press fills, then the
// window's dimming. Draw the tab's leading content (TabCell.X, Tab.Lead),
// then call EndTab, which draws the title and the close button.
func (t *TabsBuild) BeginTab(j int) TabCell {
	c, r, b, rc := t.k.Context(), t.k.Theme(), t.b, t.rc
	i := int(t.order[j])
	tab := &t.tabs[i]
	s := r.Scale
	key := b.TabKey(tab.ID)
	hot := c.HotTOf(key)
	if b.dragging {
		hot = 0
	}
	t.hot[i] = hot
	act := t.act[i]
	rc0 := [4]float32{theme.Snap(t.pos[i]) - t.ox, t.tabY0 - t.oy, theme.Snap(t.pos[i]+t.slotW) - t.ox, t.tabY1 - t.oy}

	theme.At(c, rc0[0], rc0[1], rc0[2]-rc0[0], rc0[3]-rc0[1])
	hv := rc.Hover
	c.NextBG(theme.ColA(theme.MixRGB(hv.Light, hv.Dark, r.T), theme.Lerp(hv.Alpha[0], hv.Alpha[1], r.T)*hot*(1-act)))
	c.NextRadius(rc.PillRadius * s)
	var state ui.SemState
	if i == t.sel {
		state = ui.SemSelected
	}
	c.NextRole(ui.RoleTab)
	c.NextSemName(tab.Title)
	c.NextSemState(state)
	c.NextSemPos(i, t.n)
	// the arrow, as over AppKit's tabs and segments: no pointing hand
	flags := ui.DrawBackground | ui.Clip | ui.Clickable | ui.HotAnimation | ui.ActiveAnimation | base.Floating
	if b.focusable && i == t.sel {
		flags |= ui.Focusable | ui.FocusAnimation
	}
	if t.k.Disabled() { // no presses, and disabled to assistive technologies
		flags = ui.DrawBackground | ui.Clip | ui.Disabled | base.Floating
	}
	id := c.BoxKeyed(flags, key, nil)
	c.Push(id)
	cell := TabCell{Index: i, ID: id, Rect: [4]float32{rc0[0] + t.ox, t.tabY0, rc0[2] + t.ox, t.tabY1}, X: t.pos[i], Chosen: act, Hot: hot, KeyW: t.kw, Backdrop: r.Backdrop}

	// held down: the tab darkens under the pointer (a segment's press), on
	// the pill as on an unchosen tab; it was chosen on the press already
	if p := c.ActiveTOf(key); p > 0.004 && !b.dragging {
		theme.At(c, 0, 0, rc0[2]-rc0[0], rc0[3]-rc0[1])
		c.NextBG(scaled(rc.Press, p, r.T))
		c.NextRadius(rc.PillRadius * s)
		c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(key, 6), nil)
	}
	if b.focusable && i == t.sel {
		v := t.k.VisualOf(key, base.HoverOff)
		if v.Ring > 0.01 && !v.Inactive && !t.k.Disabled() { // no ring while another window has the keyboard
			theme.At(c, 0, 0, rc0[2]-rc0[0], rc0[3]-rc0[1])
			c.NextBorder(theme.WithAlpha(r.Colors[theme.FocusRing], v.Ring), base.FocusRingWidthPt*s)
			c.NextRadius(rc.PillRadius * s)
			c.BoxKeyed(ui.DrawBorder|base.Floating, ui.KeyN(key, 5), nil)
		}
	}
	c.PushOpacity(theme.Lerp(rc.InactiveOpacity, 1, t.kw))
	cell.dimmed = true
	return cell
}

// Glyph draws the tab's glyph, if it has one, where the recipe puts it.
func (t *TabsBuild) Glyph(cell *TabCell) {
	tab := &t.tabs[cell.Index]
	id, ok := tab.Glyph.ID()
	if !ok || tab.Lead != 0 {
		return
	}
	c, r := t.k.Context(), t.k.Theme()
	col := toward(t.rc.Name[0], t.rc.Name[1], cell.Chosen, r.T, 1)
	if f, disp := t.k.Icons().Prepare(c, id, col); f != 0 {
		w, h := t.k.IconSize(id)
		cx := cell.X + t.rc.GlyphX*r.Scale
		cy := t.top + t.rc.Height*r.Scale/2
		theme.At(c, theme.Snap(cx-w/2)-cell.Rect[0], theme.Snap(cy-h/2)-t.tabY0, w, h)
		c.BoxKeyed(f|base.Floating, ui.KeyN(t.b.TabKey(tab.ID), 7), disp)
	}
}

// EndTab draws the tab's title and detail, which fade out where they do not
// fit, and its close button under the pointer, and closes the tab.
func (t *TabsBuild) EndTab(cell *TabCell) {
	c, r, b, rc := t.k.Context(), t.k.Theme(), t.b, t.rc
	tab := &t.tabs[cell.Index]
	s := r.Scale
	key := b.TabKey(tab.ID)
	act := cell.Chosen
	nameCol := toward(rc.Name[0], rc.Name[1], act, r.T, 1)
	detCol := toward(rc.Detail[0], rc.Detail[1], act, r.T, 1)
	if cell.Backdrop {
		// over the material: label colours whose alpha keeps AppKit's
		// contrast floors, dimmed by colour rather than by opacity
		c.PopOpacity()
		cell.dimmed = false
		nameCol = t.label(theme.Lerp(t.labelAlpha(tabLabelName), t.labelAlpha(tabLabelChosen), act))
		detCol = t.label(theme.Lerp(t.labelAlpha(tabLabelDetail), t.labelAlpha(tabLabelChosen), act))
	}

	// the close button shows under the pointer, and the title makes room
	ck := b.closeKey(tab.ID)
	show := float32(0)
	if !tab.NoClose && !b.dragging {
		show = max(cell.Hot, c.HotTOf(ck))
	}
	lead := tab.Lead * s
	if lead == 0 {
		lead = rc.TextX * s
		if _, ok := tab.Glyph.ID(); ok {
			lead = rc.TextXGlyph * s
		}
	}
	x := cell.X + lead
	end := cell.Rect[2] - rc.TextEnd*s - show*max(rc.CloseRight+rc.Close/2+2-rc.TextEnd, 0)*s
	base0 := t.top + rc.Baseline*s - t.tabY0
	tx := cell.Rect[0]
	if room := end - x; room > 4 {
		nw := t.k.TextWidth(t.font, tab.Title)
		t.titleRun(c, ui.KeyN(key, 10), tab.Title, x-tx, base0, nameCol, min(nw, room), nw > room)
		if px := x + nw; tab.Detail != "" && px < end-4 {
			pw := t.k.TextWidth(t.font, tab.Detail)
			t.titleRun(c, ui.KeyN(key, 11), tab.Detail, px-tx, base0, detCol, min(pw, end-px), pw > end-px)
		}
	}

	// the button is there whenever the tab can close, shown or not: an
	// assistive technology presses it without the pointer
	if !tab.NoClose && !b.dragging {
		w := cell.Rect[2] - cell.Rect[0]
		side := theme.Snap(rc.Close * s)
		cx := w - rc.CloseRight*s
		cy := (t.tabY1 - t.tabY0) / 2
		hot, down := c.HotTOf(ck), c.ActiveTOf(ck)
		cf := rc.CloseFill
		theme.At(c, theme.Snap(cx-side/2), theme.Snap(cy-side/2), side, side)
		flags := ui.Clickable | ui.HotAnimation | ui.ActiveAnimation | base.Floating
		if t.k.Disabled() {
			flags = ui.Disabled | base.Floating
		}
		if show > 0.004 {
			c.NextBG(theme.ColA(theme.MixRGB(cf.Light, cf.Dark, r.T), (theme.Lerp(cf.Alpha[0], cf.Alpha[1], r.T)*hot+0.07*down)*show))
			c.NextRadius(side / 2)
			flags |= ui.DrawBackground
		}
		c.NextRole(ui.RoleButton)
		c.NextSemName(str.CloseTab)
		cb := c.BoxKeyed(flags, ck, nil)
		c.Push(cb)
		if show > 0.004 {
			if f, disp := t.k.Icons().Prepare(c, icon.Close, theme.WithAlpha(nameCol, show)); f != 0 {
				c.BoxKeyed(f, ui.KeyN(ck, 1), disp)
			}
		}
		c.Pop()
	}

	if cell.dimmed {
		c.PopOpacity()
	}
	c.Pop()
}

// titleRun puts a title run of advance adv (or the room left, when it
// overflows and fades) with its pen at (x, baseline) in the tab.
func (t *TabsBuild) titleRun(c *ui.Context, key ui.Key, s string, x, baseline float32, color uint32, room float32, over bool) {
	w := float32(math.Ceil(float64(room))) + 2
	var fx ui.Flags
	if over {
		w, fx = room, ui.FadeOverflowX
	}
	ix := float32(math.Floor(float64(x)))
	frac := x - ix
	c.NextFont(t.font)
	c.NextTextColor(color)
	c.NextPos(ix, baseline-t.met.Off)
	c.NextPadding(frac, 0, 0, 0)
	c.NextSize(ui.Px(w+frac), ui.Px(t.met.H))
	c.BoxKeyed(ui.DrawText|base.Floating|fx, key, base.StrBytes(s))
}

// label is the text colour over a backdrop: black (light) or white (dark) at
// alpha a, as the system's label colours are.
func (t *TabsBuild) label(a float32) uint32 {
	return theme.ColA(theme.MixRGB(0x000000, 0xFFFFFF, t.k.Theme().T), a)
}

// labelAlpha is role's label alpha now: the key window's for the look (mixed
// light to dark), toward the not-key one as the window loses key.
func (t *TabsBuild) labelAlpha(role int) float32 {
	a := &t.rc.LabelAlpha[role]
	return theme.Lerp(a[2], theme.Lerp(a[0], a[1], t.k.Theme().T), t.kw)
}

// End closes the bar.
func (t *TabsBuild) End() {
	if !t.open {
		return
	}
	c, r, b, rc := t.k.Context(), t.k.Theme(), t.b, t.rc
	// fades at the ends of a strip that scrolls, toward the capsule's colour
	if !b.fit && b.n > 0 {
		cr := c.Rect(t.cap)
		w, h := cr[2]-cr[0], cr[3]-cr[1]
		top, bot := rc.Capsule[0].At(r.T), rc.Capsule[1].At(r.T)
		if r.Backdrop {
			top, bot = rc.CapsuleBackdrop[0].At(r.T), rc.CapsuleBackdrop[1].At(r.T)
		}
		fw := rc.FadeW * r.Scale
		if b.scroll > 0.5 {
			theme.At(c, 0, 0, fw, h)
			c.NextBG4(top, top&^0xFF, bot, bot&^0xFF)
			c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(b.key, 12), nil)
		}
		if b.scroll < t.slotW*float32(b.n)-(b.strip[1]-b.strip[0])-0.5 && w > fw {
			theme.At(c, w-fw, 0, fw, h)
			c.NextBG4(top&^0xFF, top, bot&^0xFF, bot)
			c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(b.key, 13), nil)
		}
	}
	c.Pop()
	t.open = false
}
