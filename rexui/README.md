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
| `BeginList` / `EndList` | Inset (default), FullWidth; striped | — | NSTableView's selection: selectedContentBackgroundColor with white text while the list has focus in the key window, unemphasised (label-coloured text) otherwise. Selected neighbours merge into one block (inset: rounded at its ends, a 1 pt divider between rows). No hover, no per-row focus ring. |
| `BeginTable` / `TableHeader` / `EndTable` | as `List` | — | 28 pt header with 1 × 16 pt column separators and sort chevrons; cells padded 10 pt, 8 pt at the outer sides. |
| `BeginOutline` / `EndOutline` | as `List` | — | NSOutlineView: disclosure chevrons, 13 pt per level, Left/Right collapse and expand (Option: the subtree). Virtualised over a pre-order `base.Tree`. |
| `BeginPopover` / `EndPopover` | below, above, right, left; transient, semitransient, application-defined | — | NSPopover's look, drawn (see D7a below): 20 pt radius, an 11 pt arrow 2 pt from the anchor that points at its centre, flips when the window has no room. Escape, a press outside and a window that stops being key close a transient one; a semitransient one closes on Escape and a press outside, not on a window that stops being key (measured: `tools/appkitref/popover.sh behavior`). |
| `Banner`, `Toast` | info, success, warning, error | — | The level's colour as a tint, its symbol, a title, a message, an action and a close button. A toast floats at the bottom centre of its parent, fades in, and goes after its time (the pointer over it holds it). |
| `Badge`, `BadgeCountN` | count, status dot, tag | — | A 16 pt count capsule ("99+" above 99), an 8 pt dot, an 18 pt tag. |
| `Avatar`, `AvatarStack` | image (`img.Handle`) or initials, presence dot | — | A circle; the initials on a colour picked by a hash of the name until the picture is ready. |
| `Skeleton` | line, block, avatar | — | A placeholder whose sheen sweeps only while it is on screen and the window shown; Reduce Motion shows the track alone. |
| `EmptyState` | — | — | Glyph, Title 3, message, action, centred in its parent. |
| `TokenField` | — | small … large | NSTokenField: tokens of the blue at 33 %, wrapped onto lines; Enter or a comma ends a token, Backspace selects and removes the last. |
| `Combobox` | — | small … large | NSComboBox: a text field and a 23.5 × 17.5 button holding a chevron; a `CompletionList` below it, Down opens it and Up/Down/Enter/Escape drive it. |
| `ColorWell` | drawn picker, or `Panel` for the system's colour panel | — | NSColorWell's 48 × 24 bezel and inset swatch; a popover of the system colours and a hex field. |
| `MessageBubble`, `BeginBubble` / `EndBubble` | incoming, outgoing, system; sent, sending, failed | — | C2 rich text in a bubble, emoji-only messages large and bubble-less, selectable across bubbles in a `VList` under one selection scope (C4). |
| `ChatListRow`, `DateDivider`, `TypingIndicator` | compact; pill; inline | — | A 56 pt conversation row (avatar, title, two-line preview, time, unread count); a hairline-flanked date; three dots that pulse only while visible. |
| `CompletionList`, `CompletionState` | — | — | The completion popup of a text box: the contract N4 writes the composer against (its doc comment). |

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

### Overlays, feedback, text input and chat (D7a)

Popover, banner and toast, badge, avatar, skeleton, empty state, token field,
combo box, colour well and the chat pieces (message bubble, chat list row,
date divider, typing indicator, completion list) are in `rexui`, in
`catalog` (`appkit-d7a`, the fourth grid page) and in `tools/rexui`.

AppKit has three of them as controls, so those are measured like the rest:
`combo`, `token` and `color-well` and the states beside them (`combo-focused`,
`combo-disabled`, `combo-button`, `token-focused`, `token-disabled`,
`token-chip`, `color-well-swatch`, `color-well-disabled`) are in
`tools/appkitref` and in `testdata/appkit-27.2.json`, which `TestRecipesMatchAppKit`
checks: all of them within tolerance, light and dark. The new keys were
measured on the Mac mini (Mac16,10, macOS 27.2 build 26B5101f, 2026-10-20) and
merged into the committed file without touching its other keys, which
`p6-refmini` is re-measuring. Two readings worth knowing: the token's fill is
the system blue at 33 % whatever the accent (AppKitRef with the red accent
draws the same pixels), and a token is 1.75 pt round, not 2.5.

**The popover is drawn, not hosted.** A hosted `NSPopover` can carry only a
native view; our content (rich text with selection, lists, fields) is built in
the window's own layer, and a native material region is a rounded rectangle
that cannot take the arrow's shape. So the fill is opaque, the colour the real
popover has over the window background. An `NSPopover` is a window the window
server composites, which AppKitRef's offscreen renders cannot show, so it is
measured live: `tools/appkitref/popover.sh` opens a real one on a real window
(`AppKitPopover`, `popover.m`) and captures it with GnuiShot.
`testdata/popover-27.2.json` holds what it measured (body radius 20 pt, arrow
11 pt high and 2 pt from the anchor, fill #F2F2F2 and #2E2E2E, 1 px edge) and
`TestPopoverMatchesAppKit` checks the draw list against it. The drawn popover
does not leave the window; if a popover ever must (content over the title bar
or another window), that one is hosted.

Not in the AppKit comparison, because AppKit has no control or draws none
offscreen: badge, avatar, skeleton, empty state, banner, toast, the chat
pieces and the completion list. They are designed after the same tokens and
shown on the `appkit-d7a` page, audited by `widgettest.AuditLayout` and built
by `TestNoAllocationsPerFrame` with everything else.

**What closes a popover** was measured on the real `NSPopover` (macOS 27.2,
`tools/appkitref/popover.sh behavior`, `popover_behavior.m`, 2026-10-20): a
transient one closes on Escape, on a click anywhere outside and when the app
resigns; the click reaches the control under it, except on the anchor, which it
closes the popover on and does not reach. A semitransient one closes on Escape
and on a click in its window outside it (the anchor included: that click
reaches the anchor and the popover stays open on it) and stays when the app
resigns. An application-defined one closes on nothing and every click passes
through. Return closes none. `ui.OverlayOpts{Dismiss}` has the three
(`DismissTransient`, `DismissSemitransient`, `DismissApplication`; the zero
value is a menu's, which also consumes the click) and `rexui.PopoverBehavior`
maps onto them.

**Text input takes its keys through `ui`'s field commands.** A focused text
field claims the keys it handles at the start of the frame, so a component
that gives Up, Down, Enter, Escape, Tab or Backspace another meaning (the combo
box's list, the token field's selection, a completion list) registers a
`ui.FieldCommander` for one `TextField` (`Context.NextFieldCommander`). The
field offers it the frame's keys and the system's edit actions in the order they
arrived, once each, after composition is handled (nothing while the input
method has marked text), as AppKit's selectors: move up/down/left/right, page
up/down, insert newline, cancel, insert tab/backtab, delete backward/forward at
the start/end of the text, and the insertion of a separator character, offered
at the place it is typed. On macOS the arrows and Backspace arrive as edit
actions (`doCommandBySelector:`), not keys, and the commander sees them the same.
`CompletionList`'s doc comment is the contract for an editor that is not ours
(a native `NSTextView` sends the same commands): units, the exclusion rectangle
and what the caller must do.

**The completion list and the combo box's list take no focus**
(`OverlayOpts.NoFocus`): the focus, a hosted native view's first responder
included, stays where it is when they open and close.

**The colour well drives the real colour panel** with `ColorWellOpts{Panel:
platform.NativeColorPanel}`: pressing it opens the shared `NSColorPanel` with
its colour, the colour set there (continuous, sRGB) is its colour, a colour the
app sets moves the panel, and the panel closes when the user closes it, when
another well takes it, when the well is disabled or no longer built. The path is
`ui.Context.ColorPanel` (a request per frame) → `app` (`syncColorPanel`) →
`platform.Window.ShowColorPanel/SetColorPanelColor/CloseColorPanel` and
`platform.EvColor` back; elsewhere the platform returns `ErrUnsupported` and the
drawn picker stays (`Panel` off). `rexui/live` drives it, and the combo box and
the token field, with real AppKit events.

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
  - Lists: inset 10, rows 10 pt below the top (5 below a header), block
    divider inset 6; header 28 with 1 × 16 column separators; cell padding
    10, 8 at the outer sides; disclosure x 2, width 13, content at 15 + 13
    per level. No role for the unemphasised block divider (Text at 5 %).

## Owning a component

```sh
go run ./tools/rexui list
go run ./tools/rexui add button ./internal/ui     # button.go and tint.go, as package ui
```

`add` copies the component's file and the files it needs, rewrites the
package clause and the examples, and never overwrites a file unless given
`-f`. The copy builds on `widget/base` and `theme` as `rexui` does.
`tools/rexui`'s test copies each component alone and builds it.
