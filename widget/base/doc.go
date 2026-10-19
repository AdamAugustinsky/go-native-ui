// Package base is the headless layer of the widget set (DECISIONS D40): the
// Kit, which belongs to one window, and the primitives every component is
// built on. A primitive owns behaviour, layout and semantics (the pointer, the
// keyboard, focus, disabled regions, the accessible role, name, value and
// state) and reports the component's visual state; it decides nothing about
// how the component looks. A styled component (package rexui, or the classic
// look of package widget) reads that state, picks its colours and sizes from
// the theme's tokens, and draws through ui's box styles and the few draw
// helpers here (Fill, Text, Icon, FocusRing).
//
// The layering is ui → theme → widget/base → rexui → widget: the styled
// components import base, and widget's calls wrap them, so neither imports
// the other in a cycle.
//
// # A primitive, then a recipe
//
// Every interactive primitive is used in three steps, because ui styles a box
// before it builds it and the visual state comes from the box's animations of
// the previous frame:
//
//	p := k.PressState("Save", base.HoverOff) // identity and visual state: Hot, Down, Focus, Ring, Disabled, Inactive
//	c := k.Context()
//	c.NextBG(fillFor(p))                     // the recipe styles the root from the state
//	c.NextRadius(radius)
//	k.BeginPress(&p, ui.RoleButton, ui.TextSize(2*padX), ui.Px(h), ui.DrawBackground|ui.DrawText, label)
//	k.FocusRing(p.ID, p.Visual, radius)      // outside the shape, not clipped by the control
//	if k.EndPress(&p).Clicked { … }
//
// The other primitives follow the same shape: ToggleState and BeginToggle
// (checkbox, switch, toggle button), ChoiceState and ChoiceItem (radio
// groups, tabs, segmented controls), RangeState and BeginRange (sliders),
// BeginTextInput and TextInput (text and search fields), BeginList and
// BeginTableHeader (lists and tables: selection, keyboard, virtualisation),
// BeginScroll and ScrollBars (scroll views), BeginSplitter (split views), and
// the Popup chain with PopupNav (menus and drop-downs).
//
// # Visual state
//
// Visual carries how hovered, pressed and focused a component is (0 to 1,
// animated by ui), how strongly its keyboard focus ring shows (only when the
// focus came from the keyboard, ui.Context.FocusVisible), whether it is
// disabled (a BeginDisabled region), and whether its window is inactive (not
// the key window: recipes draw the unemphasised look). Hover is a per-component
// policy: with HoverOff, Hot stays 0 and the box asks for no hover animation,
// as AppKit's push buttons, checkboxes and table rows behave; toolbar and
// borderless buttons and menu items pass HoverOn.
//
// # Allocation
//
// Nothing here allocates once a window's working set is warm: options and
// states are small values, labels are strings or byte slices, and no
// primitive creates a closure or calls through an interface per row.
package base
