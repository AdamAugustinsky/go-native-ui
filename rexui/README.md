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
| `Button`, `ButtonIcon` | Default, Primary, Destructive, Plain, Toolbar, Glass | mini … extra large | Rex's capsules at every size (`UseAppKitControls` gives AppKit's rounded rectangles). Primary is the accent in a key window and Default's look in an inactive one. Destructive is the alert style (red on red at 25 %). Toolbar is the window tokens' capsule. Glass is a live window's Liquid Glass button. |
| `IconButton` | Bordered, Borderless, Toolbar, Primary, Destructive | mini … extra large | Toolbar is 28 × 24 (36 × 36 at extra large, the unified toolbar's item). Primary and Destructive are Bordered in those buttons' colours. Bordered ones are capsules, circles when square. |
| `Checkbox`, `CheckboxTri` | — | mini … large | Box 16/14/18 pt with continuous-corner-equivalent radii. |
| `RadioGroup` | vertical, horizontal | mini … large | Arrow keys move the choice and the focus together. |
| `Switch` | — | mini … large | The knob slides over the Standard duration and floats on a two-layer shadow in light. |
| `Slider` | — | regular, small | 6 pt (small 4 pt) track, 20 × 16 knob. |
| `SegmentedControl` | radio group or tab list role | regular, small | A capsule track; the chosen segment a quiet tint of the accent under the label's colour; Label3 dividers between unselected segments. |
| `TextField` | Default, Mono, Borderless | small … large | 1 pt outline outside the frame. The ring shows whenever focused. `FieldOpts.Secure`: a password field (NSSecureTextField): bullets only, no copy or cut, no undo history, secure keyboard entry and Roman input while focused, `AXSecureTextField`; `TextBuffer.Wipe` zeroes the text. |
| `SearchField` | — | small … large | Capsule outline, magnifier, filled-circle clear button, Escape clears. |
| `Select` | pop-up, borderless, pull-down | regular, small | With native menus (macOS) the options are the system's pop-up menu (`RequestMenu`, `MenuChoice`); otherwise `widget.Kit.Select` draws the open list. |
| `Stepper`, `StepperField` | — | regular measured, others scaled | NSStepper: 20 × 24 body, two chevrons on a divider. Press and hold repeats at the system key-repeat timing; Up/Down arrows when focused. `StepperField` pairs it with a `TextField` over a `*float32`. |
| `ProgressBar` | determinate, indeterminate (frac < 0) | — | 8 pt bar in a 20 pt frame. The indeterminate segment sweeps only while the bar is visible and the window shown; Reduce Motion parks it. `widget.Kit.ProgressBar` calls it. |
| `LevelIndicator` | continuous capacity | — | 16 pt bar, radius 3, green / yellow / red bands (`LevelOpts{Warning, Critical, Invert}`). |
| `Spinner` | regular (32), small (16) | — | Eight spokes, one step per frame request, stopped when hidden or occluded; Reduce Motion shows one still spoke. |
| `Disclosure` | triangle, push | — | Chevron that turns 90° (push: 180°) over the Standard duration. |
| `HelpButton` | — | regular measured, others scaled | 24 pt circle in a push button's fill holding a "?". |
| `Link` | — | text styles | System link colour, pointing-hand cursor, focus ring, link semantics. Reports a click; never opens a URL. |
| `ToggleButton` | — | mini … extra large | A capsule push button that stays pressed: Default when off, a quiet tint of the accent when on. |
| `Separator` | 1 pt, or a hairline | — | Horizontal in a column, vertical in a row. |
| `BeginCard` / `EndCard` | Box, Inset (form rows), Rex pane | — | Box: radius 12, no border, optional title. |
| `BeginForm` / `Section` / `Row` / `EndForm` | Grouped (Settings), Aligned (NSGridView) | — | Grouped: inset sections (`CardInset`) of 40 pt rows, label at the leading edge and control at the trailing one, a 1 pt line between rows, a 13 pt semibold header and an 11 pt footer. Aligned: labels right-aligned to a shared column, controls 6 pt beyond it, rows 6 pt apart. Tab order is the rows' order. |
| `BeginDialog` / `Action` / `EndDialog` | — | — | A sheet drawn by us, for custom content: radius 26, 20 pt padding, large capsule buttons with the default last, Return is the default, Escape is Cancel, Tab stays inside, the window behind is inert and does not dim, as behind a macOS sheet. See "Dialogs, alerts and tooltips". |
| `Tooltip` | — | — | The system's tooltip on macOS (a region AppKit shows after its own delay); a drawn one elsewhere. |
| `Label`, `LabelBytes`, `Caption` | the macOS text styles × colour roles | — | Zero value: body text in labelColor. |
| `Tabs` (`TabBar`) | Rex, Neutral | — | Rex's tab capsule (rexlook's titlebar), in a titlebar band or in content: a press chooses, × on hover and a middle click close, a drag reorders live while the others slide, the strip scrolls when the tabs do not fit, ⌃Tab/⌃⇧Tab, ⌘⇧[ ], ⌘1…9 (keys or `TabCommands` menu commands). Tab-list and tab roles. Not AppKit's window tabs. |
| `BeginToolbar` … `End` | Plain 32, Compact 40, Unified 52, Rex 46 | — | Items (`Item`, `Nav`, `Picker`, `Menu`, groups, `Search`/`SearchButton`, `Title`, `Flexible`, `Space`, `BeginCustom`) in glass capsules laid out per pane (`Panes`, `NextPane`); the Rex band's items are borderless symbols, or glass capsules with `Glass`. The band's empty parts drag the window. `TitlebarFor(style)` gives `app.Config.Titlebar` and `TrafficLights`. |
| `Sidebar`, `SidebarSearch` | one-line and two-line (`Detail`) rows | medium rows; `RowH`/`HeaderH` | Source list on glass: headers, symbols, disclosure (13 per level), the selection inset 10 / radius 8 in the accent, grey or inactive grey. Up/Down, Left/Right, list and list-item roles. |
| `PathBar` | — | control sizes | NSPathControl's standard style: symbol and name per component, chevrons; the middle names drop out when it is narrow. |
| `BeginList` / `EndList` | Inset (default), FullWidth; striped | — | NSTableView's selection: selectedContentBackgroundColor with white text while the list has focus in the key window, unemphasised (label-coloured text) otherwise. Selected neighbours merge into one block (inset: rounded at its ends, a 1 pt divider between rows). No hover, no per-row focus ring. |
| `BeginTable` / `TableHeader` / `EndTable` | as `List` | — | 28 pt header with 1 × 16 pt column separators and sort chevrons; cells padded 10 pt, 8 pt at the outer sides. |
| `BeginOutline` / `EndOutline` | as `List` | — | NSOutlineView: disclosure chevrons, 13 pt per level, Left/Right collapse and expand (Option: the subtree). Virtualised over a pre-order `base.Tree`. |
| `BeginPopover` / `EndPopover` | below, above, right, left; transient, semitransient, application-defined | — | NSPopover's look, drawn (see D7a below): 20 pt radius, an 11 pt arrow 2 pt from the anchor that points at its centre, flips when the window has no room. Escape, a press outside and a window that stops being key close a transient one; a semitransient one closes on Escape and a press outside, not on a window that stops being key (measured: `tools/appkitref/popover.sh behavior`). |
| `Banner`, `Toast` | info, success, warning, error | — | The level's colour as a tint, its symbol, a title, a message, an action and a close button. A toast floats at the bottom centre of its parent, fades in, and goes after its time (the pointer over it holds it). |
| `Badge`, `BadgeCountN` | count, status dot, tag | — | A 16 pt count capsule ("99+" above 99), an 8 pt dot, an 18 pt tag. |
| `Avatar`, `AvatarStack` | image (`img.Handle`) or initials, presence dot | — | A circle; the initials (`Initials`: the first letter or digit of the first and last words, any script) on a colour picked by a hash of the name, or the app's (`OfRGBA`), until the picture is ready. A stack shows pictures (`Images`) and "+N". |
| `Skeleton` | line, block, avatar | — | A placeholder whose sheen sweeps only while it is on screen and the window shown; Reduce Motion shows the track alone. |
| `EmptyState` | — | — | Glyph, Title 3, message, action, centred in its parent. |
| `TokenField` | — | small … large | NSTokenField: tokens of the blue at 33 %, wrapped onto lines; Enter or a comma ends a token, Backspace selects and removes the last. |
| `Combobox` | — | small … large | NSComboBox: a text field and a 23.5 × 17.5 button holding a chevron; a `CompletionList` below it, Down opens it and Up/Down/Enter/Escape drive it. |
| `ColorWell` | the system's colour panel, or `Drawn` for its own picker | — | NSColorWell's 48 × 24 bezel and inset swatch; a popover of the system colours and a hex field. |
| `MessageBubble`, `BeginBubble` / `EndBubble` | incoming, outgoing, system; sent, sending, failed | — | C2 rich text in a bubble, emoji-only messages large and bubble-less, selectable across bubbles in a `VList` under one selection scope (C4). The app's fill or vertical gradient (`Fill`, `FillBottom`), its own `Look` or `FindMix`, and Messages' curled tail (`Curl`, a filled `gpu.Path`). |
| `Markdown`, `CodeBlock`, `CodeLine` | — | — | A parsed `markdown.Doc` (headings, lists with markers and task boxes, quotes, tables, rules, links as link spans) and code coloured by `highlight` tokens made on a worker (`MarkdownView`), with a language label, Copy, sideways scrolling, a line-number gutter, a highlighted line and diff bands; every text is a selection part (`MarkdownSelected`). Phase 8 C2/C3. |
| `ChatListRow`, `DateDivider`, `TypingIndicator` | compact; pill; inline | — | A 56 pt conversation row (avatar, title, two-line preview, time, unread count); a hairline-flanked date; three dots that pulse only while visible. |
| `CompletionList`, `CompletionState` | — | — | The completion popup of a text box: the contract the composer and the combo box share (its doc comment). |
| `Composer`, `NewComposer` | default (1 → 6 lines), expanded (3 → 8) | empty, typing, attachments, sending, disabled | The chat composer (D7b): a rounded field (the text field's fill and outline) holding an attachments row, the text and a 28 pt send button. The text is a hosted `NSTextView` on macOS (spell check, autocorrect, dictation, emoji palette, Writing Tools) that grows with its `Height` signal, and a drawn single-line field elsewhere and offscreen. The send button needs text or an attachment and no send in flight (Enter is reported as `SendKey` either way); the list is a `CompletionList` above the field, never over the hosted view. |

Every component is keyboard-operable and records its semantics. Its focus
ring sits outside the shape, shows for keyboard focus only (the text-entry
controls, text, search, combo box and token field, whenever focused, as AppKit
does: `base.Visual.TextEntry`), and never shows in an inactive window. Disabled is a
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

The 484 are the 476 of the first run and the 8 of the form-group sample (D3b);
the form samples are page 3 (`appkit-forms`: `form-group`, `form-grid`). After
the merge with the controls and lists, `TestRecipesMatchAppKit` makes 682
comparisons, all within tolerance.
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

Inactive windows (2026-10-20, Mac mini, macOS 27.2 26B5101f, blue accent):
AppKit greys what the accent fills when the window is not key, and the
accent returns with it. Measured offscreen (`*-inactive` samples; the
simulation agrees with real inactive windows, AppKitScenes' settings scene,
to 1 level) as labelColor at an opacity, over the control's own backdrop:
a checked or mixed box, a chosen radio button and an on switch 14.1 % (#DBDBDB,
#3E3E3E; the check and the dot labelColor), the slider's filled track 22.7 %
(#C5C5C5, #515151), the chosen segment 12.8 % over the track (#CDCDCD,
#4A4A4A), a determinate or indeterminate progress bar #AAAAAA in both modes,
the stepper's arrows secondaryLabelColor (#767676, #A1A1A1) with its fill
unchanged. Off boxes and switches and the level indicator do not change; the
default and toggle buttons were already measured. The MacBook's JSON carries
the same samples, taken from the Mac mini (`borrowed`), as that Mac was not
used for them.

Scroll knobs (same day, live, AppKitRef `-live -flash` and the AppKitScenes
Finder scene): a legacy knob over its slot reads #7A7A7A in light and #9F9F9F
in dark, an overlay knob #808080 and #9A9A9A over the view, both
labelColor-black at 49.8 % and white at 55.1 % (`ListRecipe.LegacyKnob`,
`OverlayKnob`). Ours were #747474 and #A5A5A5 for legacy, and an overlay knob
in the Rex palette's purple-grey. The legacy knob is 5 % longer in AppKit's
capture than ours (a knob-length difference not addressed here).

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

### Window chrome (D4)

`cmd/chromedemo` builds the Finder, Settings and Notes scenes of
`tools/appkitscenes` (same window sizes, panes, items and symbols) from
`Toolbar`, `Sidebar`, `Table`, `PathBar` and `Tabs`, plus a window of Rex's
tabs, with the scenes' contents (`tools/appkitscenes/main.m`: the 48 files,
the inspector, the settings form, the note) so that the pictures compare side
by side. `-plain` opens a striped backdrop window for glass captures and
`-drawn` turns the system's materials off. The layout audit
(`demo/audit_test.go`) runs on every scene, light and dark, at the top and
scrolled, and with a settings pop-up menu open.
`chromedemo -scene NAME -measure` prints the chrome's frames in points and
the toolbar's capsules beside AppKit's platters in the reference set for
this host (`internal/appkitref.Select`: the Mac mini's here);
`-o` renders the scene offscreen. Compared with the D0 dumps and captures
(`appkit-ref/scenes/captures`, macOS 27.2), 2026-10-19:

| Scene | Within ±0.5 pt | Off |
|---|---|---|
| finder | sidebar toggle 96, back/forward 188–274, title 284, collapsed picker 599.5–645.5, Group menu 653.5–701.5, share/tags group 705.5–789.5 (items 8 apart), Action 793.5–838, search 846–882, inspector toggle 898; sidebar rows and headers (52, 71 … 263, 276), row text ink 9 of 10 rows | Shared's text 2.5 right (AppKit puts it 1 after its 26-wide image, not 4) |
| settings | back/forward 223–309, title 319, sidebar search 10, 56, 195 × 24, rows 86, 105 … 425 | — |
| notes | sidebar toggle 91.5 (31 wide vs 31.5), Delete 137 (14.5 after the toggle, `SidebarGap`), title 250, format group 517, Media and Share one capsule 644.5–716 (`BeginShared`, 2 apart), search field 726–892, rows 40, 64 … 450 (52 tall), headers 24 | New Note 480 vs 477 (AppKit's viewer there is 40 wide around a 28.5 platter; the rule is not found) |

Colours at the same points, ΔE2000, light/dark × key/inactive: sidebar and
inspector glass ≤ 1.3, selections (emphasised, unemphasised, inactive) ≤ 0.7,
sidebar search fill 0, toolbar capsule ≤ 1.5, scroll-edge bar over the
header ≤ 1.2; ink of the title, headers, rows, details, selected rows, toolbar
symbols and the search placeholder ≤ 3 (2.9 at worst, most 0).

Live windows on the MacBook (`Chrome D4`, light and dark, key and not key)
give the same numbers: fills ΔE ≤ 1.5, ink ≤ 1.9, traffic lights at the
scenes' centres (26, 26 and 19, 20).

With the system's glass, live on the Mac mini (2026-10-20, 16 captures, the
same points): the drawn fills within ΔE 2; the toolbar capsules, now the
system's glass, 2.3–3.6 darker-than-AppKit greys in dark mode (the glass
adapts to what is behind the window, which differs between the machines).
Over a striped window (`-plain`) nothing shows through the content; the
sidebar's glass takes the tint of what is behind it at its edge, as AppKit's
does (wallpaper tinting).

Second pass (2026-10-20), offscreen against the same captures: the window
titles' ink within 0.5 (their text 2 into the title field, `TitleTextX`);
Finder's file list is `rexui.Table` (header in the pocket, `ClearHeader`;
Size right-aligned under its title; no separator after the filling Kind
column; secondary text on the emphasised selection white at 0.7, measured
#B3D1F6 on #0064E1); the settings tiles 20 × 20, radius 5, 11-pt white
symbols, half opaque in an inactive window; pop-ups as wide as their widest
option and 47.5, at the row's end; tags 16-pt circles (17 measured).

The glass is the system's in a window (`base.Kit.Materials`, set from
`Window.HasMaterials`): `GlassFill` leaves the box clear and `GlassRegion`
marks it (sidebar, inspector, each capsule; the band over the content is an
opaque `AreaTitlebar` region of the bar's or the content's colour, since the
capsules lie below the window's pixels). Offscreen, under Reduce
Transparency and off macOS the measured fallbacks are drawn. The rows of a
sidebar scroll below the band, clipped there; the content pane's run under it
(D4b, below).

On the theme's values from D3b forms (2026-10-20): toolbar symbols
`CapsuleSpec.Glyph`, items at least the style's `ItemW`, menus
`CapsuleSpec.MenuItemW`, and the roles `ToolbarSymbolInactive`,
`SelectedRowTextInactive`, `SidebarDetail`, `SidebarSearchFill(Inactive)`.
Against the Mac mini's Finder (`captures-macmini`, 27.2 26B5101f, AppKit's
frames from its tree) every capsule is within 0.5: WideGap next to a menu
or a group as well as a picker (8.5 there; 4.5 on 26B5091g), the inspector
pane's items half a point past its edge (`ToolbarRecipe.InspectorEdge`).
Notes' search field is 167 there (166 on 26B5091g). Since D4b Notes'
items are within 0.5 too (below).

With legacy scrollers (the system's style, followed live; a mouse on the
Mac mini) every scroll view's content leaves the bar's 17 pt beside it and
the bar is the legacy one: the sidebar's an opaque knob, 11 pt wide and 3 in,
in its slot (#A5A5A5 in #D8D8D8, #5A5A5A in #343434; #ABABAB in #DEDEDE,
#585858 in #323232 inactive); the settings form's boxes end 20 before it (its lines between rows lie
half a point up, on the rows' boundary, 111.5–112.5 as AppKit's).
In the frame the style changes the bar is at the view's edge already
(`base.ScrollBars`). `chromedemo -legacy` renders them offscreen, which is
overlay otherwise; against `captures-macmini` the bars, knobs and the boxes'
ends are within 0.5 pt, the content knob #747474 / #A5A5A5 against
#7A7A7A / #9F9F9F (`ListLook.LegacyKnob`, D3b's).

Without the pointer (review fixes, 2026-10-20): a sidebar group's Show or
Hide is a tab stop (Space toggles it; a click leaves the focus with the
list), and an assistive technology's press works on it and on a row's
disclosure; a row the keyboard selects scrolls into view. Every tab that
can close has its Close Tab button in the tree, shown on hover; disabled
tabs take no press and say so; tabs follow Reduce Motion and show no focus
ring in an inactive window; a tab the app chooses comes into view; Escape
during a drag puts the tab back where it started, as browsers do. The path
bar's components are tab stops. Toolbar items that do not fit their pane go,
with the rest of the pane, into a » overflow menu at the band's end, as
NSToolbar's do (`Toolbar.End` reports them; with native menus a choice
comes back as the item's Clicked); the collapsed picker opens for Show Menu
and Expand, and back and forward show the focus ring.

### Toolbar symbols, the scroll edge, the row disclosure (D4b)

Measured on the Mac mini (27.2, 26B5101f), 2026-10-20, with
`tools/appkitscenes/toolbarprobe.m` (any toolbar of symbols offscreen: 21
symbols alone, in 3-item and 2-item groups, compact and unified) and the
scene tool's `-width`, `-selectAll` and `-nested`:

- Toolbar symbols draw at AppKit's large scale (`CapsuleSpec.GlyphScale`,
  `ToolbarSymbol`: 13 pt; sidebar.left's image 24 × 18, not 18 × 14). An
  item is the symbol's exact alignment width at that scale
  (`icon.Atlas.AlignWidth`, measured 64× larger, since AppKit reports it
  rounded to half points) and 8.43 (`GroupPad`, the mean over the 21
  symbols; each is within half a point, no formula found fits all exactly),
  4 more unified, at least 36 unified; in a capsule of several items the
  first part is 4 wider and the last 5 (`EdgePad`). So Notes' Checklist is
  31 as the middle of its group, not a rounding (it is 35 first and 36
  last). Menus are their medium-scale image width and 27.5
  (`IconMediumWidth`); 8.5 next to a menu or a group (`MenuGap`; 4.5 on
  26B5091g); a flexible space keeps 3.5 after a compact item (`FlexAfter`).
  Finder's and Notes' capsules are within 0.5 of AppKit's tree, and the
  parts of Notes' groups within 0.5 wide (`TestNotesToolbarMatchesAppKitTree`,
  `TestToolbarItemWidthsMatchAppKit`; macOS 27 only, the symbols' widths
  differ elsewhere).
- The scroll edge (NSScrollPocket) is flat: over a selection the whole pocket
  deep the content shows through at 18% (light, 15% inactive) and 22% (dark,
  21% inactive) from the band's top to the pocket's end, no gradient, the
  same with legacy and overlay scrollers. A content pane's scroll view runs
  under the band (`base.Kit.NextScrollInset`; a table's
  `TableOpts.TopInset`, its header pinned below the band over the rows) and
  `ScrollBars` draws the bar (`ScrollEdgeLook`) while content is under it or
  the window is inactive, always above a table's header, with the hairline
  unless the header ends it. The toolbar leaves that band clear
  (`ToolbarOpts.ContentEdge`) and then draws its capsules, filled as the
  system's glass measured over the band (`EdgeFill`). Static at rest, no
  allocation per frame.
- While content is under it the scroll edge blurs it as AppKit does (D54,
  issue #25): `ui.Context.NextBackdrop` with AppKit's recipes read from its
  layers on the Mac mini's 27.2 (`docs/research/glass-over-metal.md`).
  A table's, whose header is in the pocket, is the Hard pocket
  (`ScrollEdgeLook.Hard`): the content at a quarter of the backing scale
  under a 6-pt Gaussian plus 0.03, and the control background plus 0.03 at
  82.45 % over it. A plain scroll view's is the thin film
  (`ScrollEdgeLook.Thin`): the control background mixed into the blur at
  50 % / 60 % at half scale, saturation 1.25, plus 0.03; AppKit's blur
  there falls off toward the hairline, which this does not model. An
  inactive window's recipes are the same (`HardInactive`, `ThinInactive`;
  AppKit's bands measured ΔE 0.00 apart). Drawn against AppKit's over
  GlassProbe's rows (ΔE2000, mean / p99): Hard 0.07 / 0.83 light and
  0.20 / 1.15 dark over text, 0.28 / 1.07 and 0.56 / 1.11 over a blue
  selection; thin film 0.36 / 1.66 and 0.95 / 2.38. Finder scrolled
  against `captures-macmini` finder-*-scrolled, per row of the pocket:
  0.13 (light) and 0.25 (dark) inactive, 0.84 and 1.12 key over its blue
  selection, which AppKit's dark blur saturates a little more. The blur
  costs 0.07–0.10 ms of GPU a frame, a 5K window at 2x included, and
  nothing at rest. At rest, nothing under it, the bar is as before.
- The toolbar's own band (`ContentUnder` without `ContentEdge`) is the Hard
  pocket too: a blur of whatever is drawn under it, built before the
  toolbar, its `gpu.Backdrop.Base` the content pane's colour (`BandFill`,
  else the control background), which the blur sees where nothing is drawn
  instead of the window's clear colour (white in dark mode would blur to
  #4C4C4C). With the content below the band, nothing is under it, and every
  renderer draws it flat without blurring: the pocket over that colour,
  #FFFFFF in light and #262626 over #1E1E1E in dark. A pane whose scroll
  view runs under the band (`ContentEdge`) draws the band's blur itself and
  the toolbar adds none: one blur per region.
- Where there is no blur, under Reduce Transparency and where the renderer
  has none (Direct3D 11, OpenGL: `gpu.Renderer.Backdrops`), the scroll edge
  over content is the colour AppKit's blurred pocket averages to, opaque
  (`ScrollEdgeLook.Under`, `UnderInactive`): #FCFCFC and #2B2B2B over rows
  of text, key and inactive, over the band and a header alike (Mac mini
  27.2: Settings, Notes, Finder, legacy and overlay scrollers,
  #FAFAFA–#FEFEFE and #2A2A2A–#2D2D2D). A list's selected rows under it
  tint it as the blur does, by their share of the inset at the share the
  bar lets through: Finder's two selected rows give #E0EBF9 against
  AppKit's #E4F0FF over the pocket (#273343 against #232E3D in dark).
  Without the blur the bar alone left the rows under the band and behind a
  header in the pocket legible; with this fill their text no longer shows.
- A sidebar row's disclosure: once any row has children every row's content
  moves 9 right (`DisclosureRoom`); the disclosure is a 13-wide box at 12 pt,
  13 more per level (`DisclosureX`), its chevron 1 below the middle,
  secondaryLabelColor (`RowChevron`); Finder's `-nested` sidebar within half
  a point (`chromedemo -nested`).
- The band covers what scrolls under it: it is hoverable and stops the wheel
  (`ui.Context.StopWheel`), so hover, secondary clicks and the wheel do not
  reach rows under it, while a press still moves the window. AppKit's
  titlebar views keep the wheel too: through NSWindow's routing on 27.2 the
  wheel over the band (empty, title, item, top edge) scrolled nothing,
  unified and compact, while over the content it scrolled 40 px a step.
- Reveals keep rows below the band: a scroll view's inset is obscured
  (`ui.Context.SetObscured`), so `ScrollIntoView`, the keyboard's moves and
  an assistive technology's `ActScrollIntoView` bring a row below it; a
  list's Page Up and Down count the rows below it.
- Under Reduce Transparency the scroll edge is opaque and not blurred:
  `MatScrollEdge`'s fill at rest, `ScrollEdgeLook.Under` with content
  under it (the toolbar's own band too).
- Back and forward's divider: 1 × 20 pt from the capsule's middle, the
  separator's colour at 21 % (light) and 26.5 % (dark): #C8C8C8 on
  #FCFCFC, #707070 on #3C3C3C.

Live captures (`Chrome D4`, the Mac mini, legacy and overlay scrollers,
light and dark, key and inactive, at the top and scrolled: 48) against
`captures-macmini` and `captures-macmini-overlay` at the probe points: fills
within ΔE 2, but the blurred text under the band (the sidebar search field
in dark, 2.1 key and 2.4 inactive then, now follows the Mac mini:
`SidebarRecipe.SearchFills`). The scrolled phases are
AppKit's offsets (Finder 130; Notes 114 with legacy bars, 98 overlay: our
note keeps NSTextView's empty line after its last newline).

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
and what the caller must do. The composer is that editor: the hosted view's
keys (`platform.EditorCommand`) become the same commands (`base.FieldCommandOf`)
and go to the same commander (`CompletionState`) as a drawn field's, so one list
serves both.

**The completion list and the combo box's list take no focus**
(`OverlayOpts.NoFocus`): the focus, a hosted native view's first responder
included, stays where it is when they open and close.

**The colour well drives the real colour panel** by default: pressing it opens
the shared `NSColorPanel` with its colour, the colour set there (continuous,
sRGB) is its colour, a colour the app sets moves the panel, and the panel closes
when the user closes it, when another well takes it, when the well is disabled
or no longer built. The path is `ui.Context.ColorPanel` (a request per frame) →
`app` (`syncColorPanel`) → `platform.Window.ShowColorPanel/SetColorPanelColor/
CloseColorPanel` and `platform.EvColor` back. The drawn picker is the fallback
where the platform has no panel (`platform.NativeColorPanel` is false, or
`ShowColorPanel` returns `ErrUnsupported`: the app tells the well with
`Input.SetColorFailed`) and what `ColorWellOpts{Drawn: true}` asks for.
Offscreen renders open no panel. `rexui/live` drives the panel, the combo box
and the token field with real AppKit events.

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
- Window chrome (D4):
  - Toolbar symbols are 13 pt at the large scale (`CapsuleSpec.GlyphScale`).
  - A one-symbol item is its symbol's alignment width and 8.43 compact,
    12.43 unified (`ToolbarRecipe.GroupPad`), at least 36 unified, the ends
    of a capsule of several 4 and 5 wider (`EdgePad`); `ToolbarSpec.ItemW`
    sets only the unified minimum.
  - A menu item is its symbol's medium image and 27.5, at least 44.5 (44.5
    and 47.5 measured); `CapsuleSpec.MenuW` is one 46.
  - Toolbar symbols are labelColor made opaque by the vibrancy (#000000,
    #FFFFFF; `SidebarLabel`'s value) and #A6A6A6 / #666666 inactive, lighter
    than `TitleTextInactive`.
  - The scroll edge's bar and hairline cover the content pane only (the
    sidebar's and inspector's glass run to the top), the hairline lies just
    below the band (y 52–52.5), and under a table header pinned below the
    toolbar the pocket's bar covers the header (`ToolbarOpts.Pocket`, or
    the table's own with `TableOpts.TopInset`); at rest the bar lets 18%
    (light) / 22% (dark) of the content through; over content it is
    AppKit's blur (`ScrollEdgeLook.Hard`, `Thin`), and without one opaque,
    AppKit's blurred mean (`ScrollEdgeLook.Under`).
  - Sidebar symbols are 16 pt; the title's field starts 4 after the
    symbol's own width (not a 20-pt column) and draws its text 2 in, as
    the headers' (`SidebarRecipe.FieldInset`).
  - A selected row in an inactive window is #676767 / #999999; a row's
    detail line black 43 % / white 36 % (0.7 white on the accent); a
    two-line row's title is semibold.
  - The sidebar's search field is a filled capsule: #E9E9E9 / #565656,
    #E3E3E3 / #4D4D4D inactive on the Mac mini (26B5101f,
    `SidebarRecipe.SearchFills`; #EAEAEA / #4E4E4E, #E5E5E5 / #464646 on the
    MacBook's 26B5091g, the `SidebarSearchFill` roles); in a key window a
    #FFFFFF / #727272 rim inside its edge and a #F2F2F2 / #5F5F5F glow
    (#F4F4F4 / #565656 on the MacBook) fading into the fill
    towards the middle, with a hairline just outside it (#D8D8D8 / #8C8C8C;
    #CDCDCD / #181818 inactive).
  - Single-line labels end in "…" (`ui.EllipsisOverflowX`: sidebar rows,
    table cells and headers, pop-ups, the path bar); tabs keep their fade.
    A row's text ends 22 before the sidebar's edge (`SidebarRecipe.TextEnd`).
  - A sidebar row's disclosure is at 12 pt, 13 more per level, and moves
    every row's content 9 right (`SidebarRecipe.DisclosureRoom`,
    `DisclosureX`, `DisclosureDY`, `RowChevron`; D4b).
  - A sidebar selection on 52-pt rows is the row's full height (64–116 pt in
    Notes, no inset) from 10 pt, on both builds; AppKit's corner is
    continuous, cutting the area of an 8.32-pt circle, ours a circle of 8
    (`Window.Selection.Radius`).
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
