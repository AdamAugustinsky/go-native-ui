# Native-feel audit: why rexlook feels like a Mac app and the widget apps do not

**Question (user, 2026-10-18).** "The RexLook app has a very Mac OS native
feel, but the other apps we did with the same theme do not … specially on
macos, apps should feel native and fast."

**Method.** `tools/appkitref` rendered 94 real AppKit controls offscreen at
2×, in Aqua and DarkAqua, on the MacBook (macOS 27.2, build 26B5091g) and
measured them (`metrics.json`). `tools/appkitref/ours` rendered our widget-kit
counterparts with the same sample names, and `tools/appkitref/sheets.sh` put
the two side by side. The four apps (gallery scenes, Pilot, notes and
rexlook) were rendered offscreen on the Mac mini.

All results are in `~/Projects/research/appkit-ref/`; its `README.md` lists
the paths. In this document:

- **`ref:`** a field of `macbook-blue/metrics.json`, in points; colours are sRGB `#RRGGBBAA`.
- **`sheet:`** a file in `sheets/`.
- **`ours:`** a file in `ours/`.

Our values come from `theme/rex` and `widget`, with file references.

The offscreen limits are listed under [What could not be measured](#what-could-not-be-measured).

## The short answer

rexlook does not use the widget kit at all. `cmd/rexlook/rex` draws
everything itself. Each length was fitted to a pixel reference, and checked
by `tools/rexcompare` (ΔE and edge positions). Its content is monospace
terminal text, and its few UI elements are restrained:

- titles;
- borderless glyph buttons that show only on the focused pane;
- a tab capsule set into the titlebar.

The Rex *theme* (`theme/rex`) captured only that chrome: the window
background, the panes, the dividers and the titlebar.

Every control in the gallery, Pilot and notes comes from `widget`. Its look was
designed, not measured, from a handful of generic tokens (`theme.Metrics`:
one `ControlH`, one `Pad`, three radii; a 26-role `Palette`; five fonts).
Against AppKit 27.2, that look is a web/shadcn look:

- it is about 17 % taller and 8 % larger in type;
- it uses white bordered buttons;
- selection is translucent;
- the focus ring sits inside the control;
- everything highlights on hover;
- the menu bar is drawn in the window.

Our text system also renders every UI string at the wrong optical size on Retina
displays (problem 1). rexlook escapes most of that, because its text is
monospace.

So the theme is fine as chrome and missing as a design system. The fix is to
make the macOS measurements the token scale and to rebuild the components on
it. That proposal is `rex-design-system.md`.

## Top problems, ranked by how much they give us away

| # | Problem | Evidence | Fix |
|---|---|---|---|
| 1 | **UI text is set at the optical size and tracking of twice its size.** `text/coretext_darwin.go` creates the font at `SizePt × Scale` pixels (`CTFontCreateUIFontForLanguage(…, px)`). On a 2× display, 13 pt text is therefore SF at 26 pt: the Display design, with display tracking. | AppKit's advance per em of a probe string: 13 pt **14.28**, 26 pt **13.49** (`ref:fonts.systemFontBySize`). Ours at 13 pt and 2×: **13.49**; at 1×: 14.28 (measured with `text.System`). Our text is 5.5 % tighter and thinner than every native label next to it. Visible in `sheet:type-light.png` | Shape and lay out at the point size and scale the positions by `Scale`. Rasterise with the `opsz` axis pinned to the point size (`kCTFontOpticalSizeAttribute`, or a variation dictionary on the px-sized font). Add a test that compares advances with `NSFont` at 1× and 2× |
| 2 | **A drawn menu bar in the window** (gallery, notes) and drawn menus. A Mac app's menus live in the system menu bar. | `ours:gallery-main-light.png` (File Edit View beside the traffic lights), `ours:notes-light.png`. `widget/menus.go` says that on macOS the model becomes the native bar in 5B; `platform` has no NSMenu yet | Build the macOS NSMenu bar and native context menus from `menu.Model` (5B, D29). On macOS, `Kit.MenuBar` draws nothing |
| 3 | **Controls are too big, and in the wrong type size.** One `ControlH` = 28 pt and 14 pt type for everything. | AppKit regular controls are 24 pt tall: push button, popup, text field, search field, segmented control, combo box (`ref:controls.light.*.frame.h`). Small is 20, mini 16, large 28 (a capsule) and extra-large 36. Body and control text is 13 pt; small controls use 11 and mini 9 (`ref:fonts.controlSizes`) | Control-size tokens (mini to xlarge) with height, font, padding and radius per size. Regular is the default: 24 pt and 13 pt |
| 4 | **Push buttons look like web buttons.** They have a white 70 % fill, a 0.5 pt border, 8 pt side padding, a semibold primary label, and are dimmed to 45 % when disabled. | AppKit fills a push button with `secondarySystemFill` (`#00000014` / `#FFFFFF14`) and draws no border. Radius is 6 at regular. "Button" is 64.5 pt wide, so about 12 pt each side. The default button is accent with a regular-weight label. Pressed is `#0000002B`. Disabled is fill `#0000000A` with `disabledControlTextColor`, not an opacity (`sheet:buttons-*.png`) | A Button recipe on fill roles, without a border, padded per size. Disabled is a colour, not opacity |
| 5 | **Selection is translucent and unfocused-looking.** Rows are tinted `Accent` at 17 % with rounded corners per row, and the text stays black. Focus does not change the selection. A hover tint is drawn on every row, and a focus border on the cursor row. | AppKit: the emphasised selection is solid `selectedContentBackgroundColor` (`#0064E1`, dark `#0059D1`) with white text, and contiguous rows merge into one block. Unemphasised (the table is not first responder, or the window is inactive) is `#DCDCDC` / `#464646` with label text. There is no row hover and no per-row ring (`sheet:tables-*.png`, `widget/list.go:540–590`) | List and Table selection: emphasised when focused in a key window, unemphasised otherwise. Merge runs; no hover; no cursor ring (the focus shows through the emphasis) |
| 6 | **The focus ring is a 2 pt border inside the control, at 90 % alpha.** | AppKit draws the ring *outside* the shape: 3 pt outset, a band about 3.5 pt wide, `keyboardFocusIndicatorColor` = accent at 50 % (`#0067F47F`, dark `#1AA9FF7F`) (`ref:controls.light.push-focused.focusRing`) | A ring outside the bounds, following the control's radius plus 3. It needs room in layout: the ring is drawn outside the box and not clipped by the parent |
| 7 | **Hover effects everywhere.** Buttons, fields, checkboxes, switches, rows and menu-bar titles all tint on hover (`widget/style.go:decorate`, `Palette.HoverOverlay`). | AppKit push buttons, popups, checkboxes, switches and table rows do not react to hover. Only toolbar, borderless and recessed buttons, menus, scrollers and sidebar disclosure do | Hover off by default on macOS, per component. Keep it for toolbar and icon buttons and for menu items |
| 8 | **The colours are a palette, not the system's.** The palette has a lavender background field and purple-ink dark surfaces (`#2A2C3A`). The dark accent is a pastel `#7AA2FF` with dark-ink text on it, and `Line` is `#5C5E6040`. The text selection ignores the user's highlight colour. | Measured semantic colours (§ Colours): `windowBackgroundColor` is `#FFFFFF` / `#1E1E1E`, `separatorColor` `#00000019` / `#FFFFFF19`, `labelColor` `#000000D8` / `#FFFFFFD8`. Text on the accent is always white. On the MacBook, `selectedTextBackgroundColor` = `#E0E0E0`, because the user set a graphite highlight. Dark mode is neutral grey | Map our roles to the system's semantic colours: read them live through `platform` like the accent (D32), and keep Rex's own colours for the background field, the pane glass and the terminal palette. Dark surfaces go neutral |
| 9 | **Window integration differs per app.** notes has no full-size titlebar, so its drawn bar sits under the system's 32 pt titlebar. Gallery and Pilot use a 46 pt band with the traffic lights at (21, 22): the Rex reference, matching no AppKit style. The gallery title is centred across the band; Pilot's is a bold name over a path. | AppKit 27.2 (`ref:windows`): plain titled window 32 pt, lights at x 9 / 32 / 55, centred at y 16. Unified toolbar 52 pt, lights at x 19 / 42 / 65, y 26. Compact 40 pt, x 12, y 20. The lights are 14 pt; the title is 13 pt bold (`ref:fonts.titleBarFont`); the window radius is 16. Dialogs are 260 pt alerts or sheets, without a scrim (`ref:controls.*.alert`) | One `Toolbar` window-chrome component with three heights (32 / 40 / 52, plus Rex's 46 as its own variant). It places the real traffic lights from the same token, and the title or toolbar items after them. On macOS, Dialog becomes a sheet or an `NSAlert` |
| 10 | **Pilot's lists are set in Menlo, like a terminal.** Its filenames, sizes and dates are in Menlo 12.46 pt, its folders bold blue, and its path field monospace (`cmd/pilot/pilot/look.go`). | A Mac file list uses 13 pt SF with symbol icons. Kind and date are secondary, and monospaced *digits* (not a monospace font) align the sizes (`ref:fonts.monospacedDigitSystemFont13`). The header is 11 pt regular and 28 pt tall; rows are 24 (`ref:…table-fullwidth-emphasized.table`) | Table cells in body type, `secondaryLabel` for metadata, a tabular-digits font feature for numbers, and a path bar or a breadcrumb instead of a mono field |

Ten more are in the sections below.

## 1. Typography

| | AppKit 27.2 (ref) | Ours (`theme/rex/fonts.go`, `widget`) |
|---|---|---|
| Body and controls (regular) | 13 pt, line 16 | 14 pt (`Fonts.UI`) |
| Small controls / mini | 11 / 9 pt | none |
| Emphasised | **Headline** 13 bold (weight 0.40, `wght` 700). Semibold is 0.30, `wght` 590. The title-bar font is 13 bold | `UISemi` 12.75 pt semibold, used for primary-button labels, headings and titles |
| Secondary and caption | callout 12, subheadline 11, footnote 10, caption1 10, caption2 10 medium (510) | `Small` 10.8 pt |
| Titles | title3 15, title2 17, title1 22, largeTitle 26 (line 20 / 22 / 26 / 32) | none: headings are `UISemi`, smaller than body |
| Table header | 11 pt regular, `headerTextColor` | 14 pt muted |
| Sidebar | items 13 pt; group headers 11 pt, bold as drawn, secondary | none |
| Menu | `menuFontOfSize:0` = **14 pt** on 27.2 | 14 pt `UI` (right by accident) |
| Monospace | SF Mono 13 (`.AppleSystemUIFontMonospaced`, advance 16.07/em) | `Mono` 12.5 pt; Pilot uses Menlo |
| Optical size | `opsz` 17 for everything ≤ 17 pt, then equal to the size (19, 20, 22 …) | the pixel size at 2×: problem 1 |
| Tracking | advance per em falls from 14.91 at 9 pt to 13.45 at 22 pt and up | that of twice the size |

**Hierarchy.** AppKit separates levels mostly by size (13, 15, 17, 22) and
uses bold sparingly. Our headings ("States", "Text", "Table") are 12.75 pt
semibold under 14 pt body text, so they read *smaller* than the body. That
inversion stands out in `ours:gallery-main-light.png`.

**Fix.**

- **Rendering:** fix the optical size and tracking (problem 1).
- **Type scale:** replace `theme.Fonts` with the eleven text styles plus control sizes (`rex-design-system.md` § Type).
- **Default size:** body is 13.
- **Headings:** headline, title3 or title2.
- **Numbers:** tabular digits (`kNumberSpacingType` monospaced) through a `FontDesc` feature flag.

## 2. Control metrics and shapes

Measured from pixels (`shape` in `metrics.json`: the 50 % edges along the
centre lines; the radius from the 45° crossing at the top corners).

| Control | AppKit 27.2 regular | Ours |
|---|---|---|
| Push button | 24 h, radius 6, fill `secondarySystemFill`, no border, about 12 pt side padding | 28 h, r 6, white 70 % with a 0.5 pt border, 8 pt padding |
| Push button by size | mini 16 / r 4, small 20 / r 5, large 28 / **capsule** r 14, xlarge 36 / capsule r 18 | one size |
| Default button | accent `#007BFF` fill, white 13 regular; pressed `#006FE6` | accent `#1E66F5`, 14 semibold |
| Popup / pull-down | 24, r 6, fill 8 %, an up-down chevron at the right | 28, white bordered, a down chevron |
| Text field | 24, r 6 (small 22 / r 5, large r 7), `textBackgroundColor` at 98 % plus a hairline | 28, r 6, `ControlFill` plus a border |
| Search field | 24, **capsule** (r 12), magnifier and clear button | 28, r 6, an × glyph to clear |
| Checkbox | 14 pt box, r ≈ 3.5, off fill `#00000019`, about 6 pt to the label | 16 pt box, r 4, 8 pt gap (`widget/choice.go`) |
| Radio | about 13–14 pt circle | 16 pt |
| Switch | **54 × 24** (small 44 × 20, mini 36 × 16, large 64 × 28); off track translucent; white knob | 35 × 19 (`2·IconSize·1.1 × 1.2·IconSize`) |
| Slider | 16 pt tall frame, track about 6 pt, white capsule knob | 4 pt track, an 18 pt circular knob with a ring |
| Segmented | 24, fill 8 % track, the selected segment an accent pill (white text); small 20 | no segmented control: `Tabs` draws underline tabs (`sheet:selects-*.png`) |
| Stepper, help, disclosure, color well, date picker, token field, combo box | 20 × 24; 24 circle; 13 pt triangle; 48 × 24; 24; 24; 24 | none |
| Progress | track 6, accent fill, capsule | 6 (`ScrollThumbW × 1.5`) |
| Alert | 260 wide; centred icon, bold 13 message and 13 informative text; two 28 pt capsule buttons side by side, filling the width | 380 wide card, left-aligned, buttons at the right (`ours:gallery-dialog-*.png`) |

Every size is a whole point and every radius a whole or half point. The kit
derives many of its sizes from other tokens instead (`IconSize × 1.1`,
`BorderW × 3`, `ScrollThumbW × 1.5`). Native sizes are a scale, not ratios of
one another.

## 3. Colours against the semantic colours

The measured values for light / dark are in `ref:colors`. The ones that matter:

| Role | AppKit light | AppKit dark | Rex light | Rex dark |
|---|---|---|---|---|
| Window background | `windowBackgroundColor` `#FFFFFFFF` | `#1E1E1EFF` | field around `#E7E3E7` | `#18191F` |
| Content surface | `controlBackgroundColor` `#FFFFFF` | `#1E1E1E` | `Surface` `#FFFFFF` | `#2A2C3A` (violet) |
| Under page | `#F6F6F6` | `#282828` | — | — |
| Text | `labelColor` `#000000D8` | `#FFFFFFD8` | `#1E2228` | `#ECEDF3` |
| Secondary | `secondaryLabelColor` `#0000007F` | `#FFFFFF8C` | `TextMuted` `#5A585E` | `#A9ACBB` |
| Tertiary / quaternary | `#00000042` / `#00000019` | `#FFFFFF3F` / `#FFFFFF19` | — | — |
| Placeholder | `#0000007F` | `#FFFFFF8C` | `TextMuted` | |
| Separator | `separatorColor` `#00000019` | `#FFFFFF19` | `Line` `#5C5E6040` | `#FFFFFF1F` |
| Control fill | `secondarySystemFill` `#00000014`; system fill `#00000019`; tertiary `#0000000C`; quaternary `#00000007` | the same in white | `ControlFill` `#FFFFFFB3` plus a border | `#FFFFFF14` plus a border |
| Accent | `controlAccentColor` `#007AFF` (user's: pink `#F74F9E`) | `#007AFF` | `#1E66F5`, follows the system | `#7AA2FF` (pastel), follows |
| On accent | white | white | white | `#0B1226` (dark ink) |
| Selected content | `selectedContentBackgroundColor` `#0064E1` | `#0059D1` | `Selection` `#1E66F52B` | `#7AA2FF40` |
| Unemphasised selection | `#DCDCDC` | `#464646` | (same as emphasised) | |
| Text selection | `selectedTextBackgroundColor`: follows the highlight setting (`#E0E0E0` on the MacBook, whose highlight is graphite) | `#FFFFFF3F` | `Selection` (accent tint) | |
| Focus ring | `keyboardFocusIndicatorColor` `#0067F47F` | `#1AA9FF7F` | `FocusRing` `#1E66F5E6` | `#7AA2FFE6` |
| Alternating rows | `#FFFFFF`, `#F4F5F5` | `#1E1E1E`, `#FFFFFF0C` | none | |
| Grid | `#E6E6E6` | `#1A1A1A` | `Line` | |
| Link | `#0068DA` | `#419CFF` | `Accent` | |
| System red / green / orange | `#FF383C` / `#34C759` / `#FF8D28` | `#FF4245` / `#30D158` / `#FF9230` | `Danger` `#CF3E4B`, `Success` `#378925`, `Warning` `#B87B2E` | pastels |

Notes:

- **Following the user.** The system colours change with the user's accent *and* highlight settings, and with Increase Contrast (`ref:colors.NSAppearanceNameAccessibilityHighContrast*`). Today we follow only the accent.
- **Dark mode.** AppKit's dark is neutral grey, with translucent white fills over it. Rex dark tints every surface violet, so every control on it looks off-system (`sheet:choices-dark.png`, `sheet:fields-dark.png`).
- **Status colours.** Rex's are Catppuccin-like terminal colours. They are right for terminal content and wrong for status UI.

## 4. Selection and focus

- **Lists and tables.** See problem 5. AppKit also distinguishes an *inactive window*: the selection goes to `unemphasizedSelectedContentBackgroundColor`, and the default button loses its accent fill (`macbook-blue/*/push-default-inactive.png`). The kit knows the window's focus (`ui.Input.Focused`, which the titlebar uses) but no component consults it.
- **Sidebar.** Rows are 32 pt tall (`effectiveRowSizeStyle` medium, macOS 27). The selection is a rounded block (r ≈ 8.5) inset about 10 pt, in accent with white bold text and icon when emphasised. Group headers are 19 pt rows of 11 pt text, and the indent is 13 pt per level. We have no sidebar component; Pilot and the gallery have none either.
- **Focus.** See problem 6. AppKit shows a ring only on text fields, lists, and controls reached with Full Keyboard Access (off on the MacBook: `ref:behaviour.fullKeyboardAccess` = false). `ui.FocusVisible` gives the same rule; keep it.
- **Text selection colour** follows `AppleHighlightColor`, not the accent (problem 8).

## 5. Scrollbars, menus, window and titlebar

- **Scrollers.** The scroller is 17 pt wide in both styles at regular size, and 15 (overlay) or 13 (legacy) at small (`ref:scrollers`). The preferred style follows `AppleShowScrollBars` and the mouse, and is overlay on the MacBook. Ours is a 12 pt hit area with a 4 pt thumb that grows to 8, the same in every case. Knob pixels could not be captured offscreen. The fix: follow `NSScroller.preferredScrollerStyle` (legacy reserves the width and shows a track) and match the widths.
- **Menus.** See problem 2. When they are drawn (Windows, Linux, popup lists): items 24 pt, 5 pt vertical padding, separators 11 pt, font 14 pt (`ref:menus`). The menu's material, shadow and radius need the window server and were not measured.
- **Titlebar.** See problem 9. Also, gallery and Pilot draw the title in `UISemi` 12.75; AppKit's title is 13 pt bold (weight 0.40). The known bug of the gallery's menu bar under the real traffic lights is fixed on another branch. With native menus the bar leaves the titlebar entirely.
- **Window corners.** AppKit windows have a radius of 16 (`_cornerRadius`, private). Our offscreen renders draw corners of their own. Live windows get the system's.

## 6. Density

AppKit 27.2 regular: controls 24, rows 24, sidebar rows 32, body 13, header
28 at 11 pt. Ours: controls 28, rows 24 (header rows `rowPt + 4`), body 14,
headers 14.

- **The gallery's state matrix:** 30 pt rows of 28 pt controls at a 34 pt pitch (`cmd/gallery/gallery/panes.go`). AppKit stacks such rows 32 pt apart (24 + 8).
- **Notes:** the composer row is 28 pt high (`ours:notes-light.png`), where AppKit would use 24.

Our gaps (8) are right; the elements inside them are too big. Pilot's 13 pt
Menlo rows are dense but read as a terminal (problem 10).

## 7. Motion

| | AppKit | Ours |
|---|---|---|
| Hover | none on standard controls | exponential fade at rate 50 (`theme/rex/fonts.go: motion`) on all of them |
| Press | instant highlight | fades in at rate 50 (about 20 ms half-life): close to instant, fine |
| Switch | the knob slides about 0.2 s | `AnimateValue` at rate 40: fine |
| Default animation | `NSAnimationContext` 0.25 s (`ref:behaviour.defaultAnimationDuration`) | Fade 18, Dim 20 |
| Appearance change | instant | 0.3 s crossfade (`Motion.Mode`): pleasant; keep it short |
| Reduce Motion / Transparency / Contrast | read from `NSWorkspace` (`ref:behaviour`) | not read: `Kit.ReduceMotion` is set only by tests and screenshots |

The fix: no hover animation where AppKit has no hover; read the three
accessibility settings through `platform` (beside the appearance, D32). Also
use `doubleClickInterval` (0.5 s here) and the key-repeat rates from the
system, if the input layer does not already.

## 8. What rexlook does that the widget kit doesn't

1. **It was measured against a reference and gated.** Every length is in reference pixels, and `tools/rexcompare` checks ΔE and edges. The kit has no reference. This audit's renders and metrics give it one; the proposal makes them the gate.
2. **Its chrome is the whole app.** Tabs live in the titlebar capsule, panes are cards with layered shadows and hairlines, and dividers show a slim handle only on hover. That is how modern Mac apps look: content in materials, not boxes of controls.
3. **It is restrained.** Pane controls are borderless glyphs in `#96999B`, shown only on the focused pane. The accent appears almost nowhere, and nothing reacts to hover except what you would click.
4. **Its focus is like a key window.** Unfocused panes dim and turn to glass, and the focused one is opaque with stronger shadows. That reads like the active and inactive window treatment.
5. **Its type escapes problem 1.** The content is monospace (SF Mono has no optical-size axis). Only tab and pane titles use SF, at 14 and 12.75 pt.
6. **Its icons are system symbols** (SF Symbols through `icon`), at symbol weights that match the text.

What it does *not* show: any standard control. The Rex reference contains
none. That is why making a theme from it produced chrome tokens and no
control tokens.

## Further problems (11–20)

11. **Disabled state is an opacity** (`disabledOpacity` 0.45 over the whole widget). AppKit changes the fill (to 4 %) and the text (`disabledControlTextColor` 25 %); the shape stays crisp. *Fix:* per-state colour tokens.
12. **The primary button's label is semibold.** AppKit's default button is regular weight. *Fix:* the token.
13. **No control sizes** (`NSControlSize`). *Fix:* Size on every component.
14. **Tabs are underline tabs.** macOS uses a segmented control, or the tab view's top segments, for both. *Fix:* SegmentedControl, and Tabs built on it.
15. **The search field is not a capsule**, and its clear button is a bare ×, not the filled circle. *Fix:* the recipe.
16. **The dialog** is a centred card over a scrim. *Fix:* on macOS, a sheet from the titlebar, or an NSAlert-style panel: 260 pt, centred, with capsule buttons and no scrim.
17. **No alternating row colours** in tables (`alternatingContentBackgroundColors`). *Fix:* a Table variant.
18. **Hover tints rows** in lists and tables. *Fix:* remove it on macOS.
19. **The headings are semibold 12.75 under 14 pt body.** *Fix:* text styles (§ 1).
20. **Dark accent and on-accent.** The pastel accent with dark text breaks the "white on accent" rule of every native control. *Fix:* use the system accent in dark too.

## What could not be measured

- **Live windows.** The MacBook is in use, so no window was shown and nothing was captured on screen. The traffic-light *positions* in our live windows (and the known menu-bar bug) are not verified here. AppKit's are in `ref:windows`.
- **Window-server compositing.** Vibrancy and materials (sidebars, menus, popovers, toolbars, Liquid Glass `NSBezelStyleGlass`), window shadows and the window mask are missing. The sidebar renders on flat grey; its unemphasised selection renders black (a vibrancy blend mode that offscreen cannot do).
- **Key-window state** is simulated (`ARWindow`/`ARApp` override the key, main and active queries). The inactive samples are honest.
- **Focus rings** were drawn with AppKit's own focus-ring API from each view's mask, because the system layer is not captured. Its geometry and colour are AppKit's, but it is not a screenshot of a live ring.
- **Layers that are not captured:** scroller knobs (legacy and overlay), `NSTabView` tabs, spinners (one frame only), and the indeterminate bar's motion.
- **Menus, popovers and tooltips** (their own windows). Only menu metrics are available.
- **Motion curves** (durations and easing of switches, focus-ring "pop", menu fade, sheets). Only the default duration and the settings were read; the rest is from experience and marked as such.
- **Rex itself.** The only Rex reference is one marketing screenshot of the chrome and panes. Rex's own controls, menus, dialogs and dark mode are unknown. Rex dark is our design.
