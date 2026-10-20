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
| `Stepper`, `StepperField` | — | regular measured, others scaled | NSStepper: 20 × 24 body, two chevrons on a divider. Press and hold repeats at the system key-repeat timing; Up/Down arrows when focused. `StepperField` pairs it with a `TextField` over a `*float32`. |
| `ProgressBar` | determinate, indeterminate (frac < 0) | — | 8 pt bar in a 20 pt frame. The indeterminate segment sweeps only while the bar is visible and the window shown; Reduce Motion parks it. `widget.Kit.ProgressBar` calls it. |
| `LevelIndicator` | continuous capacity | — | 16 pt bar, radius 3, green / yellow / red bands (`LevelOpts{Warning, Critical, Invert}`). |
| `Spinner` | regular (32), small (16) | — | Eight spokes, one step per frame request, stopped when hidden or occluded; Reduce Motion shows one still spoke. |
| `Disclosure` | triangle, push | — | Chevron that turns 90° (push: 180°) over the Standard duration. |
| `HelpButton` | — | regular measured, others scaled | 24 pt circle in a push button's fill holding a "?". |
| `Link` | — | text styles | System link colour, pointing-hand cursor, focus ring, link semantics. Reports a click; never opens a URL. |
| `ToggleButton` | — | mini … extra large | A push button that stays pressed: Default when off, accent when on. |
| `Separator` | 1 pt, or a hairline | — | Horizontal in a column, vertical in a row. |
| `BeginCard` / `EndCard` | Box, Inset (form rows), Rex pane | — | Box: radius 12, no border, optional title. |
| `BeginForm` / `Section` / `Row` / `EndForm` | Grouped (Settings), Aligned (NSGridView) | — | Grouped: inset sections (`CardInset`) of 40 pt rows, label at the leading edge and control at the trailing one, a 1 pt line between rows, a 13 pt semibold header and an 11 pt footer. Aligned: labels right-aligned to a shared column, controls 6 pt beyond it, rows 6 pt apart. Tab order is the rows' order. |
| `BeginDialog` / `Action` / `EndDialog` | — | — | A sheet drawn by us, for custom content: radius 26, 20 pt padding, large capsule buttons with the default last, Return is the default, Escape is Cancel, Tab stays inside, the window behind is inert and does not dim, as behind a macOS sheet. See "Dialogs, alerts and tooltips". |
| `Tooltip` | — | — | The system's tooltip on macOS (a region AppKit shows after its own delay); a drawn one elsewhere. |
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
| Offscreen, default blue accent | 484 of 484 |
| Offscreen, the machine's pink accent | 484 of 484 |
| Live window captures, light and dark | 392 of 402 |

The 484 are the 476 of the first run and the 8 of the form-group sample (D3b).
The form samples, the form layouts and the sheet's radius were read from a later
AppKitRef run on the same MacBook, whose main screen was 1× by then: its text
ink is drawn differently at 1× and its labels no longer matched, so the earlier
run's renders and numbers stand for every older sample and the later run
supplies only the new ones (`measure -write` records both in
`testdata/appkit-27.2.json`).

The live misses, all under the eye's threshold at 1×:

- checkbox radius +0.6 pt: live AppKit adds a faint glass gradient that the
  area-based radius reads as a squarer corner;
- search field radius −0.6 pt;
- the disabled switch's light-mode halo reads 1–3 pt wider through its
  translucent track.

Live AppKit cannot show focus rings or inactive samples in one key window, so
`-live` leaves those to the offscreen comparison.

The lists' samples (page 2, `appkit-lists`; tables, outlines) on 2026-10-19:
86 of 86 offscreen in blue and in pink; 66 of 66 live with the Mac's
scroller style (overlay). The lists follow the system's scroller style as
AppKit does; with legacy scroll bars (both apps run with
`-AppleShowScrollBars Always`, for those processes only) 54 of 66: every
miss is a selection 5 pt wider than AppKit's, as base reserves 12 pt
(`Px.ScrollbarW`) for a legacy bar where AppKit's scroller takes 17. The
unemphasised labels, left out offscreen (below), are within ΔE 1.2 live.

### Controls (D3b)

Stepper, progress, level, spinner, disclosure, help button, link and toggle
button are measured like the rest: `catalog` has 15 more samples (`help`,
`disclosure-closed`, `disclosure-open`, `push-disclosure`, `toggle-off`,
`toggle-on`, `toggle-on-inactive`, `toggle-on-disabled`, `link`, `stepper`,
`progress-bar`, `progress-indeterminate`, `spinner`, `spinner-small`, `level`),
`tools/appkitref` draws AppKit's toggle and link beside them, and
`TestRecipesMatchAppKit` checks them. On 2026-10-19 (MacBook, macOS 27.2,
default blue accent) all 92 measurements of the new samples are within
tolerance. The same run read 550 of 568 overall: the 18 misses are all
`label` ink readings of older samples (push, icon, checkbox, popup), where
AppKitRef's `cacheDisplay` text pixels differ from run to run (the previous
run had none); no geometry or fill measurement missed.

Animation (indeterminate bar, spinner, disclosure) asks for frames only while
the component is on screen and the window is neither hidden nor occluded
(`ui.Input.Hidden`), honours Reduce Motion, and allocates nothing per frame.
`SpinnerOpts{Still, Step}` and `ProgressOpts{Still, Phase}` pin the phase for
screenshots and comparisons. A hold on a stepper keeps the press time in the
box cache and counts repeats from `Context.DT`.

Strokes (spokes, chevrons, the help mark) are drawn as chains of discs, since
the renderer has no line or rotation primitive; their widths were tuned so
the ink total matches AppKit's (ratio 0.94–0.98): stepper 1.45 pt, triangle
1.3 pt, push disclosure 1.2 pt, help "?" 16.5 pt Regular.

Recipe fields without a theme token, each documented where it is declared:
`StepperRecipe` (body, divider, chevron geometry, `RepeatDelay` 0.25 s,
`RepeatInterval` 0.02 s from metrics.json `keyRepeatDelay`/`keyRepeatInterval`),
`ProgressRecipe` (frame, bar, segment width 35 %, period 1.4 s, `RestPhase`),
`LevelRecipe` (bar, frame, radius, band colours), `SpinnerRecipe` (sizes,
spoke length and width, the eight alphas, period 0.8 s), `DisclosureRecipe`,
`HelpButtonRecipe`, `LinkRecipe` (underline offset and thickness),
`ToggleOnLook`.

Not measured, set by eye: the animation periods (indeterminate bar, spinner,
disclosure turn), pressed, disabled and inactive looks of the stepper, help
button and disclosure, the stepper at small and mini, the help button at other
sizes, and a toolbar toggle. `progress-indeterminate`, `level` radius and
toggle widths (the fake text system measures differently) are left out of the
comparison.

### Left to the eye, and why

- `push-glass` offscreen: AppKitRef's `cacheDisplay` renders Liquid Glass as
  a bare label, so the recipe comes from live captures.
- `icon-toolbar`: `NSBezelStyleToolbar` outside a toolbar is a 30 × 23 rounded
  rectangle; ours is the in-toolbar capsule of the window tokens.
- `popup-borderless`, `label-styles`: `cacheDisplay` draws labels twice
  (labelColor reads #060606); live captures match labelColor.
- `box` radius: the title crosses the top-left corner.
- Unemphasised list labels offscreen: `cacheDisplay`'s double draw again
  (#050505 for labelColor's #222222 over the block); checked live.
- `table-inset-emphasized`'s shape: AppKit's columns overflow the view and it
  draws that selection full width; the inset shape is measured on
  `table-inset-fit-*`, whose columns fit.

### Measured values the tokens now hold, and those they do not

D3a reported four gaps in `theme/`. D3b closed them, and the recipes read
the tokens (renders of the gallery, notes, Pilot and rexlook did not change
by a pixel):

- `SizeSpec.Check` and `SizeSpec.CheckRadius` are the checkbox's box and its
  circular radius per size: 16/14/18 pt and 5.5/4.5/6.25 (mini 12 and 4) on
  macOS 27.2. macOS 15 drew 14/12/16 and 3.5; the theme does not distinguish
  OS versions, so those are in the field's comment. `Radii.Checkbox` is the
  regular radius, 5.5.
- `theme.Knob` is the switch's and slider's knob (white in both modes), and
  `theme.FieldOutline` a text field's outline (black at 15 % in light, white
  at 9 % in dark). No `NSColor` names either, so they are theme roles with
  the measured values.
- `theme.ControlText` is `controlTextColor`, a System role read live from the
  system (platform's `ColorControlText`). It equals `Label` on 27.2. The
  labels of checkboxes, radio buttons and switches and a field's text use it.
- `MatToolbar` has a `Solid` fallback (`Raised`), used under Reduce
  Transparency by a theme whose capsule is translucent.

Still in recipe fields:

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
  - Lists: inset 10, rows 10 pt below the top (5 below a header), block
    divider inset 6; header 28 with 1 × 16 column separators; cell padding
    10, 8 at the outer sides; disclosure x 2, width 13, content at 15 + 13
    per level. No role for the unemphasised block divider (Text at 5 %).

## Forms, dialogs, alerts and tooltips

**Form** lays rows out two ways, both measured from AppKit:

- *Grouped* is the Settings layout of `tools/appkitscenes` (the plan's
  "Settings" scene): grouped inset sections, 40 pt rows 41 apart (a 1 pt
  full-width line between them), the label 10 pt from the leading edge and
  the control 10 pt from the trailing one, headers 13 pt semibold 18 pt above
  a section (8 between sections, 6 above the box). Footers are designed:
  11 pt in `Label2`, 6 pt under the box.
- *Aligned* is an `NSGridView` with the grid's default 6 pt spacing: labels
  right-aligned to a column as wide as the widest (give `FormOpts.Labels`, or
  `LabelW`), controls beyond it.

`rexui`'s form test compares our rows with AppKit's frames
(`testdata/appkit-27.2.json`, `layouts`). Tab goes through the rows' controls
in the order they are built. A control in a row is built with an empty label
(`rexui.Switch(k, "", …)`) so the row's label is its caption; the row is a
named group for assistive technology.

**Dialog** is a sheet drawn by us, for content a native alert cannot hold: a
form, a picker, a progress view. It hangs from the titlebar, centred, over a
window that stays inert (a macOS sheet does not dim it); Return is the default action, Escape is Cancel, and
Tab stays inside. Its radius (26) is that of the glass frame of an alert's
panel, read from AppKit (`sheets.alertPanelCornerRadius`; a plain titled
window reads 16), its content is 20 pt from the sides and top and its buttons
are the alert's 28 pt capsules with the default rightmost and 16 pt from the
bottom. The slide is designed.

**Alert is `app.Alert`.** A message, an explanation, a few buttons and a
suppression checkbox are an `NSAlert` sheet on macOS, with the system's
look, keyboard handling and VoiceOver support, and `rexui` does not wrap it:
there is nothing to draw or to add to the call.

```go
t, err := a.Alert(platform.AlertSpec{
	Style: platform.AlertCritical, Message: "Delete 3 notes?", Info: "You can’t undo this.",
	Buttons: []string{"Delete", "Cancel"}, Destructive: []int{0},
})
// the answer arrives later: app.TakeDialog, from a frame
```

Where `a.Alert` returns an error satisfying `errors.Is(err, errors.ErrUnsupported)`
(Windows and Linux so far), the app shows the same question as a `Dialog`,
with the same buttons, in the same order: Cancel first, the default last.

**Tooltip** is `rexui.Tooltip(k, id, text)` after the component. On macOS it
declares a native tooltip region and draws nothing; elsewhere it draws one
from `TooltipLook`.

**Icons** take the weight and size of a text style, as
`NSImage.SymbolConfiguration(textStyle:scale:)` does: `icon.Spec{…}.In(icon.StyleHeadline, icon.ScaleLarge)`.
AppKit rasterises the symbol at that size, weight and scale on macOS
(a test compares it with `textStyle:scale:` for every style and scale); the
drawn glyph of Windows and Linux takes the style's point size and the scale's
factor.

## Owning a component

```sh
go run ./tools/rexui list
go run ./tools/rexui add button ./internal/ui     # button.go and tint.go, as package ui
```

`add` copies the component's file and the files it needs, rewrites the
package clause and the examples, and never overwrites a file unless given
`-f`. The copy builds on `widget/base` and `theme` as `rexui` does.
`tools/rexui`'s test copies each component alone and builds it.
