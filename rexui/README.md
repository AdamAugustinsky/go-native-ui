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
| `Label`, `LabelBytes`, `Caption` | the macOS text styles × colour roles | — | Zero value: body text in labelColor. |
| `Tabs` (`TabBar`) | Rex, Neutral | — | Rex's tab capsule (rexlook's titlebar), in a titlebar band or in content: a press chooses, × on hover and a middle click close, a drag reorders live while the others slide, the strip scrolls when the tabs do not fit, ⌃Tab/⌃⇧Tab, ⌘⇧[ ], ⌘1…9 (keys or `TabCommands` menu commands). Tab-list and tab roles. Not AppKit's window tabs. |
| `BeginToolbar` … `End` | Plain 32, Compact 40, Unified 52, Rex 46 | — | Items (`Item`, `Nav`, `Picker`, `Menu`, groups, `Search`/`SearchButton`, `Title`, `Flexible`, `Space`, `BeginCustom`) in glass capsules laid out per pane (`Panes`, `NextPane`). The band's empty parts drag the window. `TitlebarFor(style)` gives `app.Config.Titlebar` and `TrafficLights`. |
| `Sidebar`, `SidebarSearch` | one-line and two-line (`Detail`) rows | medium rows; `RowH`/`HeaderH` | Source list on glass: headers, symbols, disclosure (13 per level), the selection inset 10 / radius 8 in the accent, grey or inactive grey. Up/Down, Left/Right, list and list-item roles. |
| `PathBar` | — | control sizes | NSPathControl's standard style: symbol and name per component, chevrons; the middle names drop out when it is narrow. |

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

### Window chrome (D4)

`cmd/chromedemo` builds the Finder, Settings and Notes scenes of
`tools/appkitscenes` (same window sizes, panes, items and symbols) from
`Toolbar`, `Sidebar`, `PathBar` and `Tabs`, plus a window of Rex's tabs.
`chromedemo -scene NAME -measure` prints the chrome's frames in points;
`-o` renders the scene offscreen. Compared with the D0 dumps and captures
(`appkit-ref/scenes/captures`, macOS 27.2), 2026-10-19:

| Scene | Within ±0.5 pt | Off |
|---|---|---|
| finder | sidebar toggle 96, back/forward 188–274, title 284, collapsed picker 599.5–645.5, Group menu 653.5–701.5, share/tags group 705.5–789.5 (items 8 apart), Action 793.5–838, search 846–882, inspector toggle 898; sidebar rows and headers (52, 71 … 263, 276), row text ink 9 of 10 rows | Shared's text 2.5 right (AppKit puts it 1 after its 26-wide image, not 4) |
| settings | back/forward 223–309, title 319, sidebar search 10, 56, 195 × 24, rows 86, 105 … 425 | — |
| notes | sidebar toggle 91.5 (31 wide vs 31.5), title 250, search field 726–892, rows 40, 64 … 450 (52 tall), headers 24 | Delete 126.5 vs 137.5 (AppKit leaves 14.5 between the sidebar section's items); AppKit merges the adjacent Media and Share into one capsule (2 apart), so the format group lands at 525 vs 517 and New Note at 488 vs 477 |

Colours at the same points, ΔE2000, light/dark × key/inactive: sidebar and
inspector glass ≤ 1.3, selections (emphasised, unemphasised, inactive) ≤ 0.7,
sidebar search fill 0, toolbar capsule ≤ 1.5, scroll-edge bar over the
header ≤ 1.2; ink of the title, headers, rows, details, selected rows, toolbar
symbols and the search placeholder ≤ 3 (2.9 at worst, most 0).

Until the hosted materials (N3) land, the chrome draws each material's
measured fallback; every piece asks for its material through `GlassFill`
(`glass.go`), the one place the native materials take over.

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
- Window chrome (D4):
  - Toolbar symbols are 13 pt in both styles (AppKit's images: 15–20 wide);
    `CapsuleSpec.Glyph` says 16.
  - A one-symbol item is its symbol and 12 (`ToolbarRecipe.ItemPad`), at
    least 24 compact and 36 unified; inside a group, the symbol and 17
    (compact) or 21 (unified). `ToolbarSpec.ItemW` 28 is too wide for
    compact (27 measured for trash).
  - A menu item is its symbol and 27.5, at least 44.5 (44.5 and 47.5
    measured); `CapsuleSpec.MenuW` is one 46.
  - Toolbar symbols are labelColor made opaque by the vibrancy (#000000,
    #FFFFFF; `SidebarLabel`'s value) and #A6A6A6 / #666666 inactive, lighter
    than `TitleTextInactive`.
  - The scroll edge's hairline lies just below the band (y 52–52.5), and
    under a table header pinned below the toolbar the pocket's bar covers
    the header (`ToolbarOpts.Pocket`).
  - Sidebar symbols are 16 pt; the title's field starts 4 after the
    symbol's own width (not a 20-pt column) and draws its text 2 in, as
    the headers' (`SidebarRecipe.FieldInset`).
  - A selected row in an inactive window is #676767 / #999999; a row's
    detail line black 43 % / white 36 % (0.7 white on the accent); a
    two-line row's title is semibold.
  - The sidebar's search field is a filled capsule: #EAEAEA / #4E4E4E,
    #E5E5E5 / #464646 inactive.

## Owning a component

```sh
go run ./tools/rexui list
go run ./tools/rexui add button ./internal/ui     # button.go and tint.go, as package ui
```

`add` copies the component's file and the files it needs, rewrites the
package clause and the examples, and never overwrites a file unless given
`-f`. The copy builds on `widget/base` and `theme` as `rexui` does.
`tools/rexui`'s test copies each component alone and builds it.
