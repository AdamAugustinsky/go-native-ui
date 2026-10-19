package base

import (
	"unsafe"

	"gonativeui/icon"
	"gonativeui/menu"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/ui"
)

// TextSystem is what a Kit needs from the text system: fonts and their metrics
// for the theme, shaping for the icon atlas and for measuring text.
// *text.System satisfies it.
type TextSystem = icon.TextSystem

// Look forces the visual state of the next component, for galleries and
// screenshots that show every state at once. See Kit.Force.
type Look uint8

const (
	// LookHover draws the next component as if the pointer were over it
	// (when its hover policy is HoverOn).
	LookHover Look = 1 << iota
	// LookPressed draws it as if the pointer button were down on it.
	LookPressed
	// LookFocused draws it as if it had keyboard focus, with the focus ring.
	LookFocused
	// LookInactive draws it as if its window were not the key window.
	LookInactive
)

// KeyRoot is the key the kit's own boxes (popups, dialogs, tooltips) derive
// from.
var KeyRoot = ui.HashString(0, "widget")

// Kit builds the components of one window. Make it with NewKit, call Begin at
// the start of every frame and End at its end. It holds what belongs to the
// window and not to one component: the resolved theme, the icon atlas, the
// light/dark follower, the disabled regions, the layout requests for the next
// component, the open popup chain and the tooltip timer.
type Kit struct {
	c     *ui.Context
	ts    TextSystem
	th    *theme.Theme
	r     theme.Resolved
	icons *icon.Atlas
	extra []icon.Spec

	scale  float32
	ready  bool
	fol    theme.Follower
	target theme.Mode // what the follower aimed for in the last Begin

	menu  *menu.Model
	pend  [pendingCommands]menu.ID // commands drawn menus chose, delivered at the next Begin
	npend int
	force Look
	dis   [16]bool // the BeginDisabled regions
	nDis  int
	nOff  int // how many of them are on

	rowPt      float32 // NextRowHeight, in points; 0 when none
	nw, nh     ui.Size // Size for the next component (NextSize)
	hasW, hasH bool
	nextAlign  ui.Align
	hasAlign   bool
	nextMono   bool // NextMono

	mouse     [2]float32 // pointer position at the previous Begin
	mouseMove bool       // the pointer moved since then

	lay layoutState
	pop Popup
	tip tipState

	// ReduceMotion makes animations that loop (an indeterminate progress bar)
	// stand still and stop asking for frames, as the system setting for
	// reduced motion asks. Screenshots and tests that wait for a window to
	// settle set it as well.
	ReduceMotion bool

	// TooltipDelayNs is how long the pointer rests on a component before its
	// tooltip shows (DefaultTooltipDelayNs when zero).
	TooltipDelayNs int64
}

// pendingCommands is how many menu choices wait for the next frame.
const pendingCommands = 8

// NewKit makes the kit of one window. c is the window's ui context, ts its text
// system (a *text.System), th the theme (theme/rex.Theme()). Fonts and icons
// are made at the first Begin, and again when the window's scale changes.
func NewKit(c *ui.Context, ts TextSystem, th *theme.Theme) *Kit {
	k := &Kit{c: c, ts: ts, th: th}
	k.pop.Reset()
	return k
}

// AddIcon adds an icon of the app's own to the kit's atlas and returns its ID
// (the standard icons come first). Call it before the first Begin.
func (k *Kit) AddIcon(s icon.Spec) icon.ID {
	k.extra = append(k.extra, s)
	return icon.NumStandard + icon.ID(len(k.extra)-1)
}

// Theme is the resolved theme of the window: pixel metrics, fonts and the
// palette at the current light/dark mix. Valid after the first Begin; the
// pointer stays the same for the kit's life.
func (k *Kit) Theme() *theme.Resolved { return &k.r }

// Icons is the window's icon atlas (nil before the first Begin).
func (k *Kit) Icons() *icon.Atlas { return k.icons }

// TextSystem is the text system the kit was made with.
func (k *Kit) TextSystem() TextSystem { return k.ts }

// Context is the ui context the kit builds in: the one it was made for and
// the one of its latest Begin. It stays valid after End, so that a driver, a
// test or a command handler can reach the window's context through the kit.
func (k *Kit) Context() *ui.Context { return k.c }

// Focus gives the keyboard focus to the box with the key, in the next frame
// that builds it: the key of a text field (FieldSignal.Key, FieldKey), of a
// list or table (TableKey), or of any focusable box. It is ui.Context.SetFocus
// through the kit.
func (k *Kit) Focus(key ui.Key) { k.c.SetFocus(key) }

// Scale is the window's pixels per point, as of the last Begin.
func (k *Kit) Scale() float32 { return k.scale }

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

// PinSystem fixes the system's colours and settings the window shows to s,
// whatever the platform says (theme.Follower.PinSystem): for tests, and for
// offscreen renders that reproduce a machine's appearance. nil returns to the
// platform's.
func (k *Kit) PinSystem(s *theme.SystemValues) { k.fol.PinSystem(s) }

// SetMenu sets the command model: its shortcuts are matched in Begin, and
// the drawn menus and Command use it.
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
	c.TextEdit.Selection = k.r.Colors[theme.TextSelection]
	c.TextEdit.Caret = k.r.Colors[theme.Caret]

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

// End finishes the frame. The kit keeps its context (Context). A styled kit
// that draws popups at the end of the frame (a context menu) does so before
// calling it.
func (k *Kit) End() {}

// Command reports whether menu command id fired this frame, once: a shortcut,
// a native menu item, or a choice in a drawn menu (which arrives in the frame
// after the menu closed). It is false without a model (SetMenu).
func (k *Kit) Command(id menu.ID) bool {
	return k.menu != nil && k.menu.Fired(id)
}

// Force makes the next component draw in the given visual states, whatever
// the pointer, the keyboard and the window do. It is for showing every state
// of a component at once, as the gallery does; the component still behaves
// normally. The next state read (PressState, VisualOf and the others)
// consumes it.
func (k *Kit) Force(l Look) { k.force = l }

// TakeForce returns the forced look set with Force and clears it.
func (k *Kit) TakeForce() Look {
	f := k.force
	k.force = 0
	return f
}

// BeginDisabled starts a region in which the components are disabled when on
// is true: out of the tab order, deaf to the pointer, the keyboard and any
// activation already queued, and drawn in their disabled look. Regions nest (up
// to 16 deep); every call is paired with EndDisabled, whatever on was, so the
// pair can wrap a component conditionally.
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

// Disabled reports whether components built now are disabled (inside a
// BeginDisabled region that is on).
func (k *Kit) Disabled() bool { return k.nOff > 0 }

// Inactive reports whether the window is not the key window (it does not have
// the keyboard focus): components draw their unemphasised look then, as an
// inactive window's selection, default button and titlebar do.
func (k *Kit) Inactive() bool { return !k.c.Input().Focused }

// MouseMoved reports whether the pointer moved since the previous frame: a
// popup list moves its highlight to the row under the pointer only then, so
// that the keyboard's highlight is not taken by a pointer standing still.
func (k *Kit) MouseMoved() bool { return k.mouseMove }

// TextWidth is the advance in pixels of s in font f.
func (k *Kit) TextWidth(f text.FontKey, s string) float32 { return k.TextWidthBytes(f, StrBytes(s)) }

// TextWidthBytes is TextWidth for bytes.
func (k *Kit) TextWidthBytes(f text.FontKey, b []byte) float32 {
	if len(b) == 0 {
		return 0
	}
	return k.ts.Run(k.ts.Shape(f, b)).Advance
}

// StrBytes views s as bytes without copying; the bytes must not be changed.
func StrBytes(s string) []byte { return unsafe.Slice(unsafe.StringData(s), len(s)) }

// Visible reports whether the rectangle r (x0, y0, x1, y1, window pixels)
// shows in the window.
func (k *Kit) Visible(r [4]float32) bool {
	w, h := k.c.Size()
	return r[2] > r[0] && r[3] > r[1] && r[2] > 0 && r[3] > 0 && r[0] < float32(w) && r[1] < float32(h)
}
