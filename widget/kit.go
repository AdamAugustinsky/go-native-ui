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
// # Layers
//
// The behaviour, layout and semantics of every widget live in package
// widget/base (DECISIONS D40): widget.Kit embeds a *base.Kit, the state types
// (Selection, TableState, ScrollState …) are base's, and each call here is the
// look drawn over one of base's primitives. Calls that only lay out or read
// the window (Row, Column, NextSize, Theme, Force, BeginDisabled …) are base's,
// promoted through the embedding. A component library that draws its own
// look (package rexui) builds on the same primitives through Kit.Kit.
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
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// TextSystem is what a Kit needs from the text system: fonts and their metrics
// for the theme, shaping for the icon atlas and for measuring text.
// *text.System satisfies it.
type TextSystem = base.TextSystem

// Look forces the visual state of the next widget, for galleries and
// screenshots that show every state at once. See Kit.Force.
type Look = base.Look

// The looks Kit.Force takes.
const (
	// LookHover draws the next widget as if the pointer were over it.
	LookHover = base.LookHover
	// LookPressed draws it as if the pointer button were down on it.
	LookPressed = base.LookPressed
	// LookFocused draws it as if it had keyboard focus, with the focus ring.
	LookFocused = base.LookFocused
	// LookInactive draws it as if its window were not the key window (the
	// classic look draws no difference).
	LookInactive = base.LookInactive
)

// disabledOpacity is how much of its colour a disabled widget keeps.
const disabledOpacity = 0.45

// keyRoot is the key the kit's own boxes derive from.
var keyRoot = base.KeyRoot

// Kit builds the widgets of one window. Make it with NewKit, call Begin at
// the start of every frame and End at its end. Its embedded *base.Kit holds
// the window's state and the primitives; Kit adds the look.
type Kit struct {
	*base.Kit

	// what the look reads, cached from the base kit at Begin
	c     *ui.Context
	r     *theme.Resolved
	icons *icon.Atlas
	scale float32
	pop   *base.Popup

	gl      glyphs
	scratch []byte
}

// NewKit makes the kit of one window. c is the window's ui context, ts its text
// system (a *text.System), th the theme (theme/rex.Theme()). Fonts and icons
// are made at the first Begin, and again when the window's scale changes.
func NewKit(c *ui.Context, ts TextSystem, th *theme.Theme) *Kit {
	b := base.NewKit(c, ts, th)
	k := &Kit{Kit: b, c: c, r: b.Theme(), pop: b.Popup(), scratch: make([]byte, 0, 256)}
	k.gl.k = k
	return k
}

// Begin starts a frame on c (the context the kit was made for, between its
// Begin and End). It resolves the theme when the scale changed, steps the
// light/dark crossfade, hands the theme's overlay colours and rates to ui, and
// matches the shortcuts of the menu model, claiming the keys that fire commands
// (base.Kit.Begin).
func (k *Kit) Begin(c *ui.Context) {
	k.Kit.Begin(c)
	k.c, k.icons, k.scale = c, k.Kit.Icons(), k.Kit.Scale()
}

// End finishes the frame: it builds the context menu if one is open. The kit
// keeps its context (Context).
func (k *Kit) End() {
	k.contextMenu()
	k.Kit.End()
}

// off reports whether widgets are disabled now.
func (k *Kit) off() bool { return k.Kit.Disabled() }
