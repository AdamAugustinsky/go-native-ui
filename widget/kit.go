// Package widget is the widget set (DECISIONS D28): immediate-mode calls on a
// per-window Kit that build ui boxes and return their signals by value.
//
//	k := widget.NewKit(a.UI, a.Text, rex.Theme())
//	...
//	func (v *View) Build(c *ui.Context) {
//		k := v.kit
//		k.Begin(c)
//		if k.Button("Open…").Clicked { v.open() }
//		k.Checkbox("Show hidden files", &v.hidden)
//		k.End()
//	}
//
// # State
//
// A widget keeps nothing between frames except in values the app owns (a
// *bool, a *float32, a TableState, a Selection, a ui.TextBuffer) and in ui's
// box cache (hover, press and focus animations). There are no closures. The
// Kit itself holds what belongs to the window and not to one widget: the
// resolved theme, the icon atlas, the open popup, the tooltip hover timer.
//
// # Every widget
//
// is keyboard-operable (it is a tab stop, or part of one, and Enter, Space and
// the arrow keys work as the platform's controls do), draws a focus ring when
// ui.Context.FocusVisible, has a disabled state (BeginDisabled), follows the
// light/dark crossfade, and records its semantics (role, name, value, state,
// range, position) for the accessibility bridges.
//
// # Units
//
// Sizes passed to the widget API are in points (Kit.Fixed, Kit.NextPad); the
// boxes themselves are in pixels, as everywhere in ui.
//
// # Allocation
//
// Building a frame allocates nothing once the working set is warm: the kit's
// scratch buffers, the icon atlas and the theme are made up front or on the
// first frames, and nothing here creates a closure or calls through an
// interface per row.
//
// # Threading
//
// A Kit belongs to one window and is used from the main thread, like the
// ui.Context it builds in.
package widget

import (
	"gonativeui/icon"
	"gonativeui/menu"
	"gonativeui/theme"
	"gonativeui/ui"
)

// TextSystem is what a Kit needs from the text system: fonts and their metrics
// for the theme, shaping for the icon atlas and for measuring text.
// *text.System satisfies it.
type TextSystem = icon.TextSystem

// Look forces the visual state of the next widget, for galleries and
// screenshots that show every state at once. See Kit.Force.
type Look uint8

const (
	// LookHover draws the next widget as if the pointer were over it.
	LookHover Look = 1 << iota
	// LookPressed draws it as if the pointer button were down on it.
	LookPressed
	// LookFocused draws it as if it had keyboard focus, with the focus ring.
	LookFocused
)

// disabledOpacity is how much of its colour a disabled widget keeps.
const disabledOpacity = 0.45

var keyRoot = ui.HashString(0, "widget")

// Kit builds the widgets of one window. Make it with NewKit, call Begin at
// the start of every frame and End at its end.
type Kit struct {
	c     *ui.Context
	ts    TextSystem
	th    *theme.Theme
	r     theme.Resolved
	icons *icon.Atlas
	extra []icon.Spec
	gl    glyphs

	scale  float32
	ready  bool
	fol    theme.Follower
	target theme.Mode // what the follower aimed for in the last Begin

	menu    *menu.Model
	pend    [pendingCommands]menu.ID // commands drawn menus chose, delivered at the next Begin
	npend   int
	scratch []byte
	force   Look
	dis     [16]bool // the BeginDisabled regions
	nDis    int
	nOff    int // how many of them are on

	rowPt      float32 // NextRowHeight, in points; 0 when none
	nw, nh     ui.Size // Size for the next widget (NextSize)
	hasW, hasH bool
	nextAlign  ui.Align
	hasAlign   bool
	nextMono   bool // NextMono

	mouse     [2]float32 // pointer position at the previous Begin
	mouseMove bool       // the pointer moved since then

	lay    layoutState
	pop    popup
	barKey ui.Key // the menu bar the open menu belongs to
	tip    tipState

	// ReduceMotion makes the sweeping segment of an indeterminate ProgressBar
	// stand still (and stop asking for frames), as the system setting for
	// reduced motion asks. Screenshots and tests that wait for a window to
	// settle set it as well.
	ReduceMotion bool

	// TooltipDelayNs is how long the pointer rests on a widget before its
	// tooltip shows (DefaultTooltipDelayNs when zero).
	TooltipDelayNs int64
}

// pendingCommands is how many menu choices wait for the next frame.
const pendingCommands = 8

// NewKit makes the kit of one window. c is the window's ui context, ts its text
// system (a *text.System), th the theme (theme/rex.Theme()). Fonts and icons
// are made at the first Begin, and again when the window's scale changes.
func NewKit(c *ui.Context, ts TextSystem, th *theme.Theme) *Kit {
	k := &Kit{c: c, ts: ts, th: th, scratch: make([]byte, 0, 256)}
	k.gl.k = k
	k.pop.reset()
	return k
}

// AddIcon adds an icon of the app's own to the kit's atlas and returns its ID
// (the standard icons come first). Call it before the first Begin.
func (k *Kit) AddIcon(s icon.Spec) icon.ID {
	k.extra = append(k.extra, s)
	return icon.NumStandard + icon.ID(len(k.extra)-1)
}

// Theme is the resolved theme of the window: pixel metrics, fonts and the
// palette at the current light/dark mix. Valid after the first Begin.
func (k *Kit) Theme() *theme.Resolved { return &k.r }

// Icons is the window's icon atlas.
func (k *Kit) Icons() *icon.Atlas { return k.icons }

// Context is the ui context the kit builds in: the one it was made for and
// the one of its latest Begin. It stays valid after End, so that a driver, a
// test or a command handler can reach the window's context through the kit.
func (k *Kit) Context() *ui.Context { return k.c }

// Focus gives the keyboard focus to the box with the key, in the next frame
// that builds it: the key of a text field (FieldSignal.Key, FieldKey), of a
// list or table (TableKey), or of any focusable box. It is ui.Context.SetFocus
// through the kit.
func (k *Kit) Focus(key ui.Key) { k.c.SetFocus(key) }

// Pt converts points to pixels at the window's scale.
func (k *Kit) Pt(v float32) float32 { return v * k.scale }

// SetPreference sets which mode the window shows: theme.System (the default,
// the system's light or dark mode, which the window follows while it runs and
// shows already in its first frame), theme.ForceLight or theme.ForceDark. A
// change crossfades over about a third of a second (the theme's Motion.Mode);
// only the first Begin takes the mode at once. Kit uses a theme.Follower.
func (k *Kit) SetPreference(p theme.Preference) { k.fol.SetPreference(p) }

// Preference is the preference set with SetPreference.
func (k *Kit) Preference() theme.Preference { return k.fol.Preference() }

// SetMode forces the light or the dark mode: SetPreference(theme.PreferenceOf(m)).
// Return to following the system with SetPreference(theme.System).
func (k *Kit) SetMode(m theme.Mode) { k.fol.SetPreference(theme.PreferenceOf(m)) }

// Mode is the mode the window shows or is fading to, as of the last Begin
// (before the first, the forced mode, or light).
func (k *Kit) Mode() theme.Mode {
	if !k.ready {
		switch k.fol.Preference() {
		case theme.ForceDark:
			return theme.Dark
		}
		return theme.Light
	}
	return k.target
}

// SetAccent makes the accent colour (0xRRGGBBAA) the app's own, whatever the
// system's is, for themes that take the system accent; 0 hands the choice back
// to the system, whose accent the kit applies when it knows one.
func (k *Kit) SetAccent(rgba uint32) { k.fol.SetAccent(rgba) }

// SetMenu sets the command model: its shortcuts are matched in Begin, and
// MenuBar, ContextMenu and Command use it.
func (k *Kit) SetMenu(m *menu.Model) { k.menu = m }

// Menu is the model set with SetMenu, nil if none.
func (k *Kit) Menu() *menu.Model { return k.menu }

// Begin starts a frame on c (the context the kit was made for, between its
// Begin and End). It resolves the theme when the scale changed, steps the
// light/dark crossfade, hands the theme's overlay colours and rates to ui, and
// matches the shortcuts of the menu model, claiming the keys that fire commands.
func (k *Kit) Begin(c *ui.Context) {
	k.c = c
	if s := c.Scale(); !k.ready || s != k.scale {
		k.resolve(s)
	}
	k.nDis, k.nOff, k.force = 0, 0, 0
	k.hasW, k.hasH, k.hasAlign, k.nextMono = false, false, false, false

	k.target = k.fol.Update(c, &k.r)
	k.ready = true
	c.Theme = k.r.UITheme()
	c.Rates = k.r.Motion.Rates()
	c.TextEdit.Selection = k.r.Pal.Selection
	c.TextEdit.Caret = k.r.Pal.Accent

	in := c.Input()
	k.mouseMove = in.MouseX != k.mouse[0] || in.MouseY != k.mouse[1]
	k.mouse = [2]float32{in.MouseX, in.MouseY}

	for i := 0; i < k.npend; i++ { // the menu choices of the last frame
		in.PushCommand(uint16(k.pend[i]))
	}
	k.npend = 0
	if k.menu != nil {
		k.menu.Match(c)
	}
}

// resolve makes the theme, the fonts and the icons for scale s.
func (k *Kit) resolve(s float32) {
	k.scale = s
	mode := k.fol.Target(k.c)
	if k.ready { // a new scale: keep the mix the window is at
		mode = theme.Light
		if k.r.Dark() {
			mode = theme.Dark
		}
	}
	t := k.r.T
	k.r = k.th.Resolve(k.ts, s, mode)
	k.fol.Invalidate()
	if k.icons == nil {
		k.icons = icon.New(k.ts, k.extra...)
	}
	k.icons.Raster(s)
	k.icons.Attach(k.c.Uploader())
	if k.ready {
		k.r.Mix(t)
	}
}

// End finishes the frame: it builds the context menu if one is open. The kit
// keeps its context (Context).
func (k *Kit) End() {
	k.contextMenu()
}

// Command reports whether menu command id fired this frame, once: a shortcut,
// a native menu item, or a choice in a drawn menu (which arrives in the frame
// after the menu closed). It is false without a model (SetMenu).
func (k *Kit) Command(id menu.ID) bool {
	return k.menu != nil && k.menu.Fired(id)
}

// Force makes the next widget draw in the given visual states, whatever the
// pointer and keyboard do. It is for showing every state of a widget at once,
// as the gallery does; the widget still behaves normally.
func (k *Kit) Force(l Look) { k.force = l }

// BeginDisabled starts a region in which the widgets are disabled when on is
// true: drawn dimmed, out of the tab order, and deaf to the pointer, the
// keyboard and any activation already queued. Regions nest (up to 16 deep);
// every call is paired with EndDisabled, whatever on was, so the pair can wrap
// a widget conditionally.
func (k *Kit) BeginDisabled(on bool) {
	if k.nDis < len(k.dis) {
		k.dis[k.nDis] = on
		if on {
			k.nOff++
		}
	}
	k.nDis++
}

// EndDisabled ends the region BeginDisabled started.
func (k *Kit) EndDisabled() {
	if k.nDis == 0 {
		return
	}
	k.nDis--
	if k.nDis < len(k.dis) && k.dis[k.nDis] {
		k.nOff--
	}
}

// off reports whether widgets are disabled now.
func (k *Kit) off() bool { return k.nOff > 0 }
