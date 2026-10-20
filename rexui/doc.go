// Package rexui is the Rex component library (DECISIONS D40, D52): styled
// components in the Rex language on the metrics measured from macOS 27.2,
// built on the headless primitives of package widget/base. It is shadcn's
// model without a registry: every component is one file that reads only
// tokens, and an app that wants a component of its own copies the file into
// its package (go run ./tools/rexui add button ./internal/ui) and edits it.
//
// # A primitive, then a recipe
//
// A component reads its visual state from a primitive, picks its colours
// from its recipe, takes its sizes from the theme's tokens, and builds boxes:
//
//	func Button(k *base.Kit, label string, o ButtonOpts) base.Signal {
//		c, r := k.Context(), k.Theme()
//		rc := &ButtonRecipes[o.Variant]          // Tints per state: roles, not colours
//		s := &r.Sizes[o.Size]                    // height, padding, radius, font in pixels
//		p := k.PressState(label, rc.Hover)       // Hot, Down, Focus, Ring, Disabled, Inactive
//		fill, fg, _ := rc.Colors(r, p.Visual)
//		c.NextBG(fill)
//		c.NextRadius(s.Radius)
//		…
//		k.BeginPress(&p, ui.RoleButton, ui.TextSize(2*s.PadX), ui.Px(s.Height), flags, label)
//		k.FocusRing(p.ID, p.Visual, s.Radius) // outside the shape, keyboard focus only
//		return k.EndPress(&p)
//	}
//
// Variants and sizes are uint8 enums that index arrays: ButtonRecipes by
// ButtonVariant, theme.Resolved.Sizes by theme.Size. A recipe holds Tints
// (a theme.Role and, for the few colours no role names, an opacity), so the
// light/dark crossfade and the system's live colours cost nothing extra.
// Options are small structs passed by value; the zero value of each is the
// default variant at the regular size.
//
// # States
//
// Every component is keyboard-operable and records its semantics; draws its
// focus ring outside its shape and only for keyboard focus (text fields:
// whenever focused), never in an inactive window; is disabled by colour,
// not by opacity (base.Kit.BeginDisabled); and in an inactive window draws
// the unemphasised look (the primary button loses the accent). Nothing
// reacts to hover where AppKit does not: hover is a recipe's policy
// (base.HoverOn for toolbar and borderless buttons).
//
// # Overriding and owning
//
//   - A token: change the theme before the kit is made (rex.Theme() returns a
//     copy).
//   - A recipe, for the whole app: rexui.ButtonRecipes[rexui.ButtonPrimary].Fill = rexui.Of(theme.Green).
//   - A component: copy its file and own it (README.md).
//
// # Lists
//
// List, Table and Outline (list.go, table.go, outline.go) are AppKit's
// NSTableView and NSOutlineView over base.List: they draw only the visible
// rows, so a list of a million rows costs what one screenful does. The
// selection is emphasised (the accent, white text) only while the list has
// keyboard focus in the key window; ListLook, TableLook and OutlineLook are
// their recipes. Glyphs at AppKit's sizes come from AddListGlyphs.
//
// # Forms, dialogs, alerts and tooltips
//
// Form lays rows out as macOS 27.2's Settings does (grouped inset sections) or
// as an NSGridView does (labels right-aligned to a column). Dialog is a sheet
// drawn by us for custom content; a message and a few buttons are an alert, and
// that is app.Alert, which rexui does not wrap (README.md). Tooltip declares
// the system's tooltip where there is one and draws its own elsewhere.
//
// # Allocation
//
// Building a frame allocates nothing: no closures, interfaces, maps or
// strings are made per frame.
package rexui
