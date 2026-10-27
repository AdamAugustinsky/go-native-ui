# Rex: the default look, and an app in it

Rex is the look of every app on this framework (D17, D40): Superlogical's
Rex, a smooth coloured field behind the window, glass panes with soft
shadows and hairline edges, capsule controls, and the system's fonts. It is
not a macOS skin. `theme/rex` is the theme every kit is made with,
`rexui`'s recipes default to it, and AppKit's own look (rounded rectangles,
blocks of the accent) is an opt-in for an app that wants it
(`rexui.UseAppKitControls`).

This chapter is what Zap's move to Rex taught, written as rules so the next
app starts where Zap ended. Each rule names the mistake it prevents.

## 1. The theme: Rex, in your colours

Make the kit with `rex.Theme()`, or with `rex.ThemeWithField(fc)` when the app
has a colour of its own. Never leave a flat white or grey window background:
the field is what makes a Rex window read as Rex, and a plain background
looked senseless in Zap's first light mode.

```go theme/rex/rex.go
// ThemeWithField is Theme with its window background, and the glass of its
// unfocused surfaces, in other colours (FieldColors).
func ThemeWithField(fc FieldColors) *theme.Theme {
```

`FieldColors` holds the light and dark gradient stops (`FieldStop`: a row and
its left, middle and right colours), the glass of unfocused panes in dark
(`DarkGlass`), and the `Scrim` laid over the system's material when the
window is transparent. A brand accent is the theme's accent, not paint in
the app: set `SystemAccent = false` and `Light.WithAccent` /
`Dark.WithAccent`, and mark the roles you replaced in `Theme.Own` so the
system's live colours do not overwrite them. Then recolour the recipes that
carry Rex's lilac to the field's hue: `rexui.TabsRecipes[rexui.TabsRex]` (the
capsule, its pill, the separators). Zap's `gnui/theme/rexlook.go` does all of
this for WhatsApp's green in about a hundred lines.

## 2. Transparency is on by default

A Rex window shows the desktop softly through its field. Ask for the material
in the window's config, always, and decide per frame whether the field covers
it, so the setting changes at once with no new window:

```go cmd/rexlook/rex/native.go
	c.Backdrop = platform.BackdropGlass
```

```go cmd/rexlook/rex/rex.go
	r.k.Begin(c) // the fonts and icons at this scale, and the mode and accent of the frame
	r.th.SetBackdrop(r.backdrop)
```

`SetBackdrop` takes "the window has a material and the app wants it seen" and
turns it off while the system's Reduce Transparency is set, so an app never
forgets that check. Give the user a Transparency setting, on by default, and
show its row only on macOS, where the material exists.

## 3. Controls: rexui, as it comes

Every control is a `rexui` component. A button drawn by hand from boxes is how
Zap's voice-message buttons ended up in an old look and overflowing their
bar; a component gets the theme, the states, the focus ring and the size
tokens for nothing.

- Push buttons, toggle buttons and segmented controls are capsules at every
  size. A toggle that is on, and a chosen segment, are a quiet tint of the
  accent (14 % light, 24 % dark) under the label's colour, not a block of the
  accent. These are the defaults; do not reset them.
- An icon-only action is `IconButton`, round. A bar of actions (a
  recorder, a selection bar) is a capsule: `ComposerLook`'s shape or a
  `Capsule` button row, never a rectangle with square ends.
- A person is `rexui.Avatar` with a `Backdrop` role naming what it sits on
  (`theme.Field`, `theme.Window`), so the ring and the initials' contrast are
  right; a stack of people is `AvatarStack`.
- Colour choices (swatches) are circles.
- Metadata (times, sizes, counts, hints) is `LabelOpts.MonoPt`, the
  monospaced face Rex sets it in.

```go rexui/appkit.go
func UseAppKitControls() {
```

## 4. Layout: let the engine place things

Every one of Zap's visible layout bugs was arithmetic the app did that the
engine could have done.

- **Centre and pin with anchors.** A box laid over another (an arrow in a
  round button, a badge on an avatar, a floating button) is placed with
  `c.NextCenter()` or `c.NextAnchor(ui.AnchorBottomRight, inset, inset)`,
  never with a position computed from sizes. Zap's "scroll to newest" arrow
  was 2 px off until it did. `NextOffset` nudges a box optically without
  moving its siblings.

```go ui/anchor_test.go
			c.NextSize(Px(40), Px(40))
			p = c.Open(0, "button")
			c.NextCenter()
```

- **Rows that can run out of room wrap.** A row of chips or filters is a flow
  (`BeginFlow`/`EndFlow`) that folds onto a second line, not a row that
  overflows or scrolls sideways.

```go examples/images/main.go
	c.BeginFlow("tiles", ui.FlowOpts{GapX: c.Pt(12), GapY: c.Pt(12)}) // the tiles wrap onto lines
```

- **Flex and grid are built in** (GUIDE §3): `Grow(weight)` for the room that
  is left, `NextMinSize`/`NextMaxSize` for bounds, `NextAlignSelf` for one
  child's cross alignment, `BeginGrid` for equal columns that follow the
  width.
- **Scroll bars do not cover content.** A `VList` under a legacy (always
  shown) scroll bar leaves room for it with `VListOpts.BarRoom =
  k.VListBarRoom(label, st)`.
- **Content clears what floats over it.** The last message stops a gap above
  a floating composer; anything under a floating bar gets that bar's height
  as bottom padding.

Check, do not eyeball: the debug overlay's Run Layout Audit (⌘⌥⇧A,
`GNUI_DEBUG=overlay`) and `widgettest.AuditLayout` in a test report overlaps,
overflow and off-centre content. A centring test measures the ink's bounding
box, not its centroid: a glyph's weight (an arrowhead) pulls the centroid
off a centre that is right.

## 5. The app's name

`gnui.json`'s `displayName` is the name the menu bar and Dock show, and
`bundleName` the `.app`'s own name, which is what the app switcher shows; set
both when the product name differs from the binary's. Zap kept its old zip
name for its updater and still reads "Zap" in ⌘Tab.

## 6. Before you call a screen done

- It is built from `rexui` components on `rex.Theme()` or `ThemeWithField`.
- Transparency is on, `SetBackdrop` runs each frame, and the setting is a
  macOS-only row.
- No position is computed from sizes; nothing overflows its bar at the
  narrowest window; rows of chips wrap.
- The layout audit is clean in light and dark, and a render of the screen in
  both was looked at.
