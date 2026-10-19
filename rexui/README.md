# rexui

The Rex design system's components: styled controls in the Rex language, on
the metrics measured from macOS 27.2, built on the headless primitives of
`widget/base`. The package doc (`doc.go`) explains the recipe pattern; this
file lists what there is, how close it is to AppKit, and how that is checked.

`widget`'s look functions (`k.Button`, `k.Checkbox`, `k.TextField`, `k.Select`,
`k.Tabs`, `k.Label` …) call these components with the default variant and size,
so existing apps get them unchanged. An app that wants more calls `rexui`
directly with `k.Kit`, or copies a component and owns it.

## Components

| Component | Variants | Sizes | Notes |
|---|---|---|---|
| `Button`, `ButtonIcon` | Default, Primary, Destructive, Plain, Toolbar, Glass | mini … extra large | Primary is the accent in a key window and Default's look in an inactive one. Destructive is the alert style (red on red at 25 %). Toolbar is the window tokens' capsule. Glass is a live window's Liquid Glass button. |
| `IconButton` | Bordered, Borderless, Toolbar, Primary, Destructive | mini … extra large | Toolbar is 28 × 24 (36 × 36 at extra large, the unified toolbar's item). Primary and Destructive are Bordered in those buttons' colours. |
| `Checkbox`, `CheckboxTri` | — | mini … large | Box 16/14/18 pt with continuous-corner-equivalent radii. |
| `RadioGroup` | vertical, horizontal | mini … large | Arrow keys move the choice and the focus together. |
| `Switch` | — | mini … large | The knob slides over the Standard duration and floats on a two-layer shadow in light. |
| `Slider` | — | regular, small | 6 pt (small 4 pt) track, 20 × 16 knob. |
| `SegmentedControl` | radio group or tab list role | regular, small | Accent pill, Label3 dividers between unselected segments. |
| `TextField` | Default, Mono, Borderless | small … large | 1 pt outline outside the frame. The ring shows whenever focused. |
| `SearchField` | — | small … large | Capsule outline, magnifier, filled-circle clear button, Escape clears. |
| `Select` | pop-up, borderless, pull-down | regular, small | With native menus (macOS) the options are the system's pop-up menu (`RequestMenu`, `MenuChoice`); otherwise `widget.Kit.Select` draws the open list. |
| `Separator` | 1 pt, or a hairline | — | Horizontal in a column, vertical in a row. |
| `BeginCard` / `EndCard` | Box, Inset (form rows), Rex pane | — | Box: radius 12, no border, optional title. |
| `Label`, `LabelBytes`, `Caption` | the macOS text styles × colour roles | — | Zero value: body text in labelColor. |
| `BeginList` / `EndList` | Inset (default), FullWidth; striped | — | NSTableView's selection: selectedContentBackgroundColor with white text while the list has focus in the key window, unemphasised (label-coloured text) otherwise. Selected neighbours merge into one block (inset: rounded at its ends, a 1 pt divider between rows). No hover, no per-row focus ring. |
| `BeginTable` / `TableHeader` / `EndTable` | as `List` | — | 28 pt header with 1 × 16 pt column separators and sort chevrons; cells padded 10 pt, 8 pt at the outer sides. |
| `BeginOutline` / `EndOutline` | as `List` | — | NSOutlineView: disclosure chevrons, 13 pt per level, Left/Right collapse and expand (Option: the subtree). Virtualised over a pre-order `base.Tree`. |

Every component is keyboard-operable and records its semantics. Its focus
ring sits outside the shape, shows for keyboard focus only (text fields:
whenever focused), and never shows in an inactive window. Disabled is a
colour, not an opacity. Building a frame allocates nothing (the
`TestNoAllocationsPerFrame` test builds every sample).

## How close it is, and how that is checked

`rexui/catalog` draws each component as the sample of the same name in
`tools/appkitref`, with AppKit's frame. Each sample says what to measure:

- the width along a probe row and the height through its middle, ring included;
- the top-left corner radius, from the area the corner leaves uncovered;
- the fill at the probe, the label's ink and the focus ring's colour.

Tolerances are ±0.5 pt for lengths and radii, ΔE2000 ≤ 2 for fills and rings,
and ΔE2000 ≤ 3 for label ink.

- **CI.** `TestRecipesMatchAppKit` takes those measurements from the draw
  list (geometry and composited colours, not pixels, so it runs anywhere).
  It compares them with AppKit's numbers in `testdata/appkit-27.2.json`,
  written by `go run ./tools/appkitref/measure -ref <AppKitRef run> -write`.
  Apple's numbers are committed; its pixels are not.
- **Offscreen pixels.** `go run ./tools/appkitref/ours -o DIR -ref metrics.json`
  renders our samples. `go run ./tools/appkitref/measure -ref <AppKitRef run>
  -ours DIR` prints the comparison table. `sh tools/appkitref/sheets.sh`
  composes side-by-side sheets.
- **Live windows.** `go run ./cmd/gallery -scene appkit` (and `appkit-type`)
  shows the samples in AppKitRef's grid. `AppKitRef -live NAMES.txt` shows
  AppKit's in the same grid. `tools/appkitref/live.sh` captures both with
  GnuiShot on the test Mac. `measure -cut` cuts the captures into cells, and
  `measure -live` compares them.

Results on 2026-10-19 (MacBook, macOS 27.2, build 26B5091g):

| Comparison | Within tolerance |
|---|---|
| Offscreen, default blue accent | 476 of 476 |
| Offscreen, the machine's pink accent | 476 of 476 |
| Live window captures, light and dark | 392 of 402 |

The live misses, all under the eye's threshold at 1×:

- checkbox radius +0.6 pt: live AppKit adds a faint glass gradient that the
  area-based radius reads as a squarer corner;
- search field radius −0.6 pt;
- the disabled switch's light-mode halo reads 1–3 pt wider through its
  translucent track.

Live AppKit cannot show focus rings or inactive samples in one key window, so
`-live` leaves those to the offscreen comparison.

### Left to the eye, and why

- `push-glass` offscreen: AppKitRef's `cacheDisplay` renders Liquid Glass as
  a bare label, so the recipe comes from live captures.
- `icon-toolbar`: `NSBezelStyleToolbar` outside a toolbar is a 30 × 23 rounded
  rectangle; ours is the in-toolbar capsule of the window tokens.
- `popup-borderless`, `label-styles`: `cacheDisplay` draws labels twice
  (labelColor reads #060606); live captures match labelColor.
- `box` radius: the title crosses the top-left corner.

### Measured values the tokens lack or get wrong

These live in named recipe fields, each documented with its measurement. They
are reported against `theme/` (D1), not changed there:

- `SizeSpec.Check` is 14/12/16 (macOS 15). macOS 27.2's box is 16/14/18
  (`CheckboxRecipe.Box`), and its continuous corner is best matched by a
  circular radius of 5.5/4.5/6.25, not `Radii.Checkbox` 3.5.
- There are no roles for a switch or slider knob (OnAccent is used) or for a
  text field's outline (black at 15 % / white at 9 %, via `Tint.Alpha`).
- There is no `controlTextColor` role. Editable field text uses Label, which
  equals it on macOS.
- `MatToolbar` has no `Solid`: harmless while `CapsuleFill` is opaque, wrong
  for a theme that makes it translucent.
- Geometry no token holds:
  - Switch: knob widths 32/21/26/38 and insets 2 (mini 1.5).
  - Slider: height 16, track 6/4, knob 20 × 16.
  - Radio dot: 5 pt.
  - Mixed dash: 7 × 1.75.
  - Segmented divider: 1 × 12 pt.
  - Separator: 1 pt (NSBox), not a hairline.
  - Field inset: 6.
  - Search: magnifier centred at 13.75, text at 24, clear button centred 12 from the end.
  - Pop-up chevron: centred 14 from the end (borderless 10, pull-down 13.25); borderless label pad 8.
  - Box: title band 12, title x 10.

## Owning a component

```sh
go run ./tools/rexui list
go run ./tools/rexui add button ./internal/ui     # button.go and tint.go, as package ui
```

`add` copies the component's file and the files it needs, rewrites the
package clause and the examples, and never overwrites a file unless given
`-f`. The copy builds on `widget/base` and `theme` as `rexui` does.
`tools/rexui`'s test copies each component alone and builds it.
