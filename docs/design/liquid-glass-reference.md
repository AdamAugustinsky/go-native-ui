# Liquid Glass reference: how a macOS 27.2 app window is laid out and lit

**Asked (user, 2026-10-19).** "All the apps that we did very much don't have a
macos native-feel to them, or rather, they look linux-like, or old versions of
macos (much before liquid glass). The only actually good looking and good
feeling to use app that we have is RexLook, the rest is quite mediocre."

**What this is.** It is item D0 of phase 6, the window-level counterpart of
`native-audit.md`. `tools/appkitref` measured single controls offscreen. This
document measures whole windows, live on screen, built from Apple's standard
idioms:

- `NSSplitViewController` with sidebar, content and inspector items;
- a unified or compact `NSToolbar` with item groups, a search item and tracking separators;
- source-list and inset tables;
- scroll views under the toolbar;
- `NSGlassEffectView` for a floating composer.

The window server composited the glass, and GnuiShot captured it at 2×.

**Method.** `tools/appkitscenes` built four scenes with fixed sample content:

- **finder** (Pilot's target): a sidebar, a file table, an inspector, and a unified toolbar.
- **notes** (`examples/notes`): a list of notes in the sidebar, an editor, and a compact toolbar.
- **settings** (gallery showcases): a sidebar with a search field, and grouped `NSBox` forms.
- **chat** (ZapFast): conversations, a message area, and a glass composer in a bottom split-item accessory.

Each scene ran on the MacBook (macOS 27.2, build 26B5091g, 2×, run 2026-10-19), in light and dark mode. Each was captured in four phases:

- inactive at the top;
- inactive and scrolled, so content sits under the toolbar;
- active at the top;
- active and scrolled.

The tool dumped its own view hierarchy at every phase. The pixels came from the captures. Two runs (`*_reorder-*`) took the inactive phases in the other order, to check whether a state came from the window being inactive or from the order of the phases.

The run used `-AppleAccentColor 4`, the default blue, in the tool's argument domain. The user's own accent is pink, and no setting was changed. On this machine:

- the Liquid Glass slider is at `NSGlassTintAmount = 0.645`;
- Reduce Transparency and Increase Contrast are off;
- the sidebar size is medium (`effectiveRowSizeStyle` 2).

Sources, all under `~/Projects/research/appkit-ref/scenes/`:

- **`cap:`** a capture in `captures/`, named `<scene>-<light|dark>-<active|inactive>-<top|scrolled>.png`.
- **`dump:`** the JSON beside the capture. Frames are in points from the window's top-left corner.
- **`meas:`** `captures/measured.json`, from `AppKitScenes -measure`.
- **`fit:`** `AppKitScenes -radius`, the body edges and corner radius of a rounded rectangle, fitted row by row.
- **`sheet:`** a file in `sheets/`.

Colours are sRGB `#RRGGBB`, averaged over the 2×2 pixels of a point.

## The surprise: macOS 27 is not Tahoe

macOS 27 ("Golden Gate") rolled back the parts of the macOS 26 (Tahoe) design
that people disliked. A target taken from Tahoe would already be out of date:

| | macOS 26 Tahoe (Apple, WWDC25 "Build an AppKit app with the new design") | macOS 27.2, measured |
|---|---|---|
| Sidebar | A floating glass pane, inset from the window edges, with its own corner radius | **Flush, edge to edge, full height, radius 0.** It is still an `NSGlassEffectView` (`dump: splitItems[0].superviews`, `cornerRadius 0`, `effectiveCornerRadii 0,0,0,0`). `-NSSplitViewItemSidebarDefaultsToFloatingAppearance YES` has no effect on 27.2 (tested; run removed). |
| Inspector | Edge-to-edge glass | The same: flush, radius 0 |
| Window corners | The radius depends on the window style, and toolbar windows are larger | **16 for every style** measured: unified, compact, and with or without a sidebar (`dump: window.cornerRadius(private _cornerRadius)`; the alpha mask agrees) |
| Toolbar | Glass items float over the content | **The items are still glass capsules.** Once content scrolls under them, a near-opaque bar with a 1-px hairline comes back ("hard" scroll edge). An inactive window always shows that bar |
| Sidebar icons | Monochrome | Accent-coloured symbols. Tag dots keep their colours |

Press reports of the change: Michael Tsai, "Golden Gate Sidebars and
Toolbars" and "Golden Gate Window Corners" (mjtsai.com, 2026-06-09); Six
Colors, "WWDC 2026: No, Tahoe—yes, Golden Gate" (2026-06-12). macOS 27 also
adds the Liquid Glass slider (System Settings → Appearance), the
`NSGlassTintAmount` default above. **Everything below is measured on 27.2.**
The Tahoe floating sidebar could not be reproduced on this machine, and it is
not measured.

What makes a window look current on 27, then, is not the floating sidebar. It is:

- glass capsules in a transparent toolbar;
- content that runs under the toolbar and gets a scroll-edge bar only when it is there;
- a flush, slightly grey glass sidebar with 10-pt-inset, 8-pt-radius selections;
- capsule-shaped controls (radius = height / 2);
- radius 16 window corners.

The old look: an opaque toolbar band, a drawn sidebar with full-width selection, square buttons and boxes of controls.

## 1. Window and toolbar geometry

| | Unified (finder, settings, chat) | Unified compact (notes) | Source |
|---|---|---|---|
| Titlebar plus toolbar height | **52** | **40** | `dump: window.titlebarPlusToolbarHeight` |
| Traffic lights | 14 × 14 at x 19 / 42 / 65, y 19 (centre 26, 26) | x 12 / 35 / 58, y 13 (centre 19, 20) | `dump: window.trafficLights` |
| Toolbar item capsule | **36** tall at y 8 (8 above and below), radius **18** | **24** tall at y 8, radius **12** | `dump: NSToolbarPlatterView`, `NSGlassEffectView.glass.cornerRadius`; `fit:` 16.2–17.3 for 36, 11.8 for 24 |
| One-icon capsule | 36 × 36 (glyph 15–18 pt) | 27–31.5 × 24 | `dump:` |
| Grouped items (`NSToolbarItemGroup` subitems) | one capsule. Items are 36–40 wide and 8 apart: 83.5 wide for 2 | one capsule, 124 wide for 3 items of 35.5–36.5, 8 apart | `dump: NSToolbarItemGroupView` |
| Back / forward (navigational, momentary) | 86 × 36, one capsule with a divider | — | finder, settings |
| Select-one group (view switcher) | Collapsed to a **46 × 36 pop-up capsule** when space is short (156 expanded) | — | finder `dump: NSToolbarItemGroupPickerView` |
| Menu item (`NSMenuToolbarItem`) | 44.5–47.5 × 36 (glyph and chevron) | — | finder |
| Search item | Collapsed to a 36 × 36 magnifier capsule | expanded field, 166 × 24, inside a capsule | finder / notes |
| Gap between capsules | **4–4.5** between adjacent items; 8–8.5 next to menus, flexible space or a tracking separator | 4.5; 10 before search | `dump:` frames |
| First item after the traffic lights | Sidebar toggle at x 96: **17** after the zoom light | x 91.5: 19.5 after | `dump:` |
| Title | **15 pt bold** (`.AppleSystemUIFont`, weight 0.40), 19 tall, at y 16.5 | **13 pt bold**, 16 tall, at y 12 | `dump: _NSToolbarTitleField` |
| Title x | 10 after the preceding capsule (finder 274 → 284, settings 309 → 319). With nothing before it, 18 from the content pane's edge (chat 280 → 298) | 10 from the pane's edge (240 → 250) | `dump:` |
| Title colour | `labelColor` resolves to `#000000B2` in the titlebar; the glyphs measure about `#4D4D4D` on white. Dark: about `#E9E9E9` | the same | `cap:`, `dump:` |
| Window corner radius | **16** | **16** | `dump: _cornerRadius` (private, read only); alpha-mask 45° crossing 4.5–5 pt in |
| Toolbar background, active, nothing under it | **None**: the content shows (white `#FFFFFF`, dark `#1E1E1E`), with no line | the same | `meas: areas[default].toolbarBand` |

All toolbar capsules sit in one `NSGlassContainerView` the width of the window,
with `spacing 4` (`dump:`). In this run the gaps were 4–4.5, and the capsules were not merged.

**Concentric radii on 27.2.** The radii are 16 for the window and h/2 for the
capsules: 18, 12, and 18 for the composer. Selections inside a pane are 8, and group boxes 12. The
sidebar and inspector are 0. The toolbar capsules are not concentric with the
window corner, because the traffic lights fill that corner, and the sidebar is flush.
What stays concentric is local:

- a capsule's inner pieces, such as the segmented and pop-up views, report `effectiveCornerRadii` 18 inside the 18 capsule;
- the search field's inner button is 14.5;
- the group box (12) contains 24-pt controls of radius 6 (`native-audit.md`).

The 27 API for this is `NSView.cornerConfiguration` / `effectiveCornerRadii`
(`NSViewCornerRadius containerConcentricRadius`), and the dump records it for
every view.

## 2. Sidebar and inspector

| | Value | Source |
|---|---|---|
| Frame | x 0, y 0, full window height. Under the toolbar, the safe area starts at top 52 (40 compact). The width is the split item's (180–280 in the scenes) | `dump: splitItems` |
| Material | `NSGlassEffectView`, style regular, radius 0, around the item's view. **No `NSVisualEffectView`** | `dump: splitItems[*].superviews` |
| Divider | a 0.5-pt `NSSplitDividerView`, **not visible**. The pixels step straight from the sidebar fill to the content (`#ECECEC` → `#FFFFFF`), with no line | `meas: areas[sidebar].pxRowAcrossDivider` |
| Fill, active | light **`#ECECEC`**, dark **`#272727`–`#2A2A2B`**. It is not flat: luminance 231–236 (light), because the glass refracts nearby content. Next to the table's blue selection the sidebar edge picks up `#E4ECF6` | `meas: areas[sidebar].fill` (median, min, max) |
| Fill, inactive | flat **`#F2F2F2`** / **`#252525`** (min = max) | `meas:` |
| Inspector | the same glass, flush and full height: `#ECECEC` / `#272727` active, `#F2F2F2` / `#252525` inactive; no divider | `meas: areas[inspector]` |
| Rows (medium) | **32**; group header rows **19**; **13** of extra space above each group after the first (Downloads ends at 263, iCloud's header starts at 276) | `dump: tables[NSOutlineView].rows` |
| Row layout | cell x 16; symbol at x 19 (20 × 20 medium); text at about x 43 (symbol + 4); text 16 tall, centred (y = row + 8) | `dump:` |
| Indentation | 13 per level | `dump: indentationPerLevel` |
| Group header type | **11 pt**, which AppKit swaps in for the cell's 13. The stems render about 1.5 pt wide, so it is drawn bold. Colour `#878787` on `#ECECEC` (light active), `#A6A6A6` inactive; x 14 | `dump:`, `cap: settings-light-*` |
| Row type | 13 pt regular, black (`labelColor` resolves to `#000000B2` on the glass, and the vibrancy darkens it to `#000000` in the pixels). Inactive `#727272`. **The selected row switches to 13 pt semibold** (weight 0.3) | `dump: rows[selected].text.font`, `cap:` |
| Search field in the sidebar (settings, chat) | 24 tall, **capsule-shaped**: `fit:` r ≈ 12, although the field reports `effectiveCornerRadii` 6. Fill `#EAEAEA` on `#ECECEC`. Its position (10 from each edge, 4 below the safe area) is this scene's constraint | `dump: NSSearchField`, `fit:` |

## 3. Selection styles

All are measured with `fit:` on the live captures. The colours are the solid fill.

| Where | Shape | Light | Dark | Source |
|---|---|---|---|---|
| Sidebar, emphasised (first responder, key window) | **inset 10 on each side, full row height (32), radius 8.0** | **`#0070F5`**, white semibold text | **`#007AFF`** | `meas: selections` (notes, settings) |
| Sidebar, unemphasised (another view focused, key window) | the same shape | **`#D7D7D7`**, black semibold text | **`#434343`** | finder, chat |
| Sidebar, window inactive (either case) | the same shape | **`#E7E7E7`** | **`#333333`** | all `*-inactive-*` |
| Inset table (`NSTableViewStyleInset`), emphasised | inset 10 from the content edges (x 190–880 in a 180–890 pane), **radius 8.0**. Contiguous rows **merge into one block**, rounded only at its ends, with a 1-pt lighter line between the rows (`#1973E4` light / `#196AD6` dark) | **`#0064E1`** (`selectedContentBackgroundColor`) | **`#0059D1`** | finder |
| Inset table, window inactive | the same shape | **`#DCDCDC`** | **`#464646`** | finder inactive |
| Striped rows (inset table) | the stripe is an inset rounded band like the selection, `#F4F5F5` / `#FFFFFF0C` over the window colour | | | `cap: finder-*`, `dump: colors` |

The sidebar's emphasised blue (`#0070F5`) is **lighter** than the table's
(`#0064E1`). It is close to `controlAccentColor` (`#007AFF`), drawn on the glass.
`native-audit.md` measured the sidebar radius as 8.5 offscreen; live it is 8.0.

## 4. Content under the toolbar: the scroll-edge effect

Every scroll view under the toolbar gets an `NSScrollPocket` (`dump:`). It
spans:

- **0 to the toolbar's bottom**: 52, or 40 compact;
- **0 to the bottom of the header** for a table with a header: 80 = 52 + the 28-pt header;
- **the bottom accessory's band** under a bottom split-item accessory: chat, 546 to 620. That is the accessory (56 tall, from this scene's content) plus 9 above and 9 below, which AppKit adds.

The scroll view's `contentInsets` are the same numbers (`top 52`, `bottom 74`), and AppKit's natural top for a table with a header is −80.

| | Active window | Inactive window | Source |
|---|---|---|---|
| Nothing scrolled under the toolbar | **No bar, no line.** The toolbar band is the content colour | **A bar and a hairline anyway**: light `#FFFFFF` with the line, dark `#262626` with a `#2E2E2E` line. The reordered run shows it is the inactive state, not the launch | `meas: areas[default].pxColumnAtToolbarEdge`, `*_reorder-*` |
| Content under the toolbar | a **"hard" edge**: a near-opaque bar over a blurred copy of the content, then a hairline at y = toolbar height | the same | `cap: notes-*-scrolled`, `chat-*` |
| The hairline | **1 device pixel** (0.5 pt) at y = 52 (40): `#E5E5E5` over white (≈ black 10 %); dark `#2E2E2E` over `#1E1E1E`. Over a group box: `#DEDEDE` / `#343434` | | `meas:` pixel columns |
| The bar over neutral content | light `#FFFFFF`. Dark **`#292929`**. A `#F7F7F7` group box under it reads `#FEFEFE`–`#FFFFFF`, so ≤ 5 % shows through | | settings scrolled |
| Over text | Blurred smudges. Bar luminance 235–250 (light), 46–63 (dark), median `#F3F4F4` / `#363636` | | notes scrolled |
| Over saturated colour | It shows through much more. A blue bubble (`#0088FF` light, `#0091FF` dark) under the bar reads `#ADD8FF` / `#244E6F`: about a third of it, while grey shows through at ≤ 5 %. The blurred backdrop is evidently saturation-boosted; a flat alpha cannot fit both | | `AppKitScenes -probe cap: chat-*-active-scrolled.png 760 40 470 30` |
| Below the line | The content is untouched. There is no gradient or soft fade below the bar | | the pixel columns |
| Bottom accessory band (chat) | the same treatment: a 1-pt `#F2F2F2` line (≈ black 5 %) at y 545, then the bar. Light `#FDFDFD` / dark `#202020` beside the composer | | `cap: chat-*` |
| Table header (finder) | the header and toolbar make one pocket, 0–80. The header's bottom line is 1 pt `#E5E5E5` at y 79. Rows scrolled under it show blurred, and the blue selection tints the band `#ECF5FF` | | `meas: finder-*-scrolled` |

The pocket's internal views are `NSHardPocketView` (a 1-pt layer `#0000000D` /
`#FFFFFF0D`) and a content-background `NSVisualEffectView` at alpha 0.6. They
report zero-size frames, so the visible geometry above comes from the pixels.

## 5. Glass capsules (toolbar items, composer)

| | Light, over white | Light, over the sidebar | Dark | Inactive | Source |
|---|---|---|---|---|---|
| Fill | **`#F8F8F8`–`#F9F9F9`** | `#F2F2F2` (the sidebar is `#ECECEC`) | **`#3A3A3A`** over `#262626` | flat: `#F2F2F2` light, `#333333` dark | `meas: glassCapsules.fillLeftPadding` |
| Rim (outermost pixel) | 1 px **`#BFBFBF`** (≈ black 25 %) | `#ABABAB` | 1 px **`#0D0D0D`**, a dark outline | `#C1C1C1` / `#131313` | `meas: pxRowAcrossLeftEdge` |
| Bottom edge and shadow | inner pixels `#FFFFFF`, a rim `#E8E8E8`, then a soft shadow: `#FCFCFC` on white (≈ black 1.5 %), fading out over about 10 pt | | **a bright inner rim**, `#444444` → `#787878` over the last 1–2 pt, then `#262626`, with no visible shadow | no highlight | `meas: pxColumnBelow` |
| Over coloured content | It takes the colour: over the blue bubble it is `#DFF2FF`, with the outside `#D2E9FF` | | `#304859` | | chat scrolled |
| Composer (`NSGlassEffectView`, r 18, interactive) | 568 × 36. AppKit places the accessory at 600 × 56, 10 in from each side of the 620 pane (`automaticallyAppliesContentInsets`). The glass's 16 / 8 / 16 / 12 inset inside it is this scene's choice. Fill `#F9F9F9`, rim `#BFBFBF` | | `#353535`, rim `#0A0A0A` | | chat |

## 6. Materials per area (light / dark, active)

| Area | What AppKit uses | Pixels | Native or drawn |
|---|---|---|---|
| Window background, content | `NSVisualEffectView` contentBackground, within the window (the theme frame and each scroll view) | `#FFFFFF` / `#1E1E1E` | drawable: a solid fill |
| Sidebar, inspector | `NSGlassEffectView` regular, radius 0 | `#ECECEC` / `#272727`–`#2A2A2B`, ±2 % refraction of nearby content; flat `#F2F2F2` / `#252525` inactive | **native for the exact look**; a flat fill comes within about 2 % (see §8) |
| Toolbar band | nothing (`NSTitlebarBackgroundView` per split section, transparent) until the pocket shows | the content colour | drawable |
| Toolbar items | `NSGlassEffectView` per capsule, in an `NSGlassContainerView` | §5 | **native for the specular rim and the content sampling**; drawable for the resting state |
| Scroll edge | `NSScrollPocket`: blur, saturation and a 60 % content background, plus the hard line | §4 | it samples only our own content, so **we can render it**: blur, saturate and tint our own pixels. The exact curve is native |
| Group box (`NSBox` primary, untitled) | a filled rounded rectangle, no border | `#F7F7F7` / `#252525`, `fit:` r **12.2**, body = the frame | drawable |
| Inset table | stripes and a selection inset 10, r 8 | §3 | drawable |
| Composer | `NSGlassEffectView` in a bottom split accessory | §5 | as the toolbar items |

## 7. What changes when the window is inactive

Compare `cap: *-active-*` with `*-inactive-*` (`sheet: sheet-<scene>.png`, rows two and four):

- **Traffic lights:** grey discs, `#EAEAEE` light and `#4A4A4A` dark. Active, they are `#F87B72` / `#FDC734` / `#7FD457` light and `#F35C52` / `#FDBF00` / `#2CC000` dark.
- **Title:** `#B8B8B8` light, `#5C5C5C` dark. Toolbar glyphs dim the same way.
- **Toolbar capsules:** a flat fill (`#F2F2F2` / `#333333`). The rim stays, and the bright bottom highlight goes.
- **Toolbar bar:** always on, with the hairline (§4).
- **Sidebar and inspector glass:** a flat `#F2F2F2` / `#252525`, with no refraction.
- **Sidebar selection:** `#E7E7E7` / `#333333`, whether it was emphasised or not. The text drops to `#727272`.
- **Sidebar symbols and tag dots:** desaturated (`sheet: sheets/crops/sidebar-*-inactive.png`).
- **Table selection:** `unemphasizedSelectedContentBackgroundColor`, `#DCDCDC` / `#464646`.
- **Unchanged:** the content area, the pocket geometry, and all frames.

## 8. What the framework must provide

**N** marks the pieces that need a native view or the window server to look right. **D** marks the pieces our renderer can draw to match, from the numbers above.

| Piece | N / D | What to build (feeds the native-shell decision, D1–D4) |
|---|---|---|
| Window: radius-16 mask, shadow, resize | **N** | A real `NSWindow` with a full-size content view. We never draw corners or shadows |
| Traffic lights | **N** | The real buttons, placed from the tokens: unified centre (26, 26), compact (19, 20), pitch 23 (D4) |
| Toolbar heights and layout | D | 52 unified, 40 compact. Capsules 36 / 24 tall at y 8, r = h/2. Items 36 square, 8 apart inside a group, groups 4–8.5 apart. Title 15 bold (13 compact), 10 after the previous capsule. **D4's "title at 13 bold" holds only for compact and plain titlebars; unified uses 15** |
| Toolbar capsules, resting | D | Fill `#F9F9F9` / `#3A3A3A`, a 1-px rim `#BFBFBF` / `#0D0D0D`, the dark-mode bright bottom rim, and the inactive flat fill. That reaches the static look |
| Toolbar capsules, live glass | **N** (or accept the drawn look) | The specular highlight that tracks the content, colour pickup over saturated content, the press response (`effectIsInteractive`), and the user's Liquid Glass slider (`NSGlassTintAmount`, private) only come from `NSGlassEffectView` or a real `NSToolbar`. If the native shell hosts an `NSToolbar`, this is free |
| Transparent toolbar band and the scroll edge | D, with a renderer feature | Content runs under the toolbar (insets = toolbar height, + header). No bar while nothing is under it (active). A hard edge when content is under it: blur + saturate + a near-opaque tint of **our own** pixels in the band, and a 1-device-pixel hairline at y = toolbar height (black 10 % / `#2E2E2E`). Always on when inactive. It needs a band blur in `gpu`, and no native view |
| Sidebar / inspector material | **N** for exact; D for close | Flush, full height, no divider line. Active `#ECECEC` / `#2A2A2A` (glass, ±2 % refraction); inactive flat `#F2F2F2` / `#252525`. A flat fill is within about 2 % except beside saturated content and does not follow the Liquid Glass slider. A native `NSGlassEffectView` behind a transparent region of our layer is the exact path (N3a) |
| Sidebar rows, headers, selection | D | Rows 32 (medium; follow `effectiveRowSizeStyle`), headers 19 with 11-pt bold text in `#878787`, 13 extra above later groups. Selection inset 10, full row height, **r 8**: emphasised `#0070F5` / `#007AFF` with semibold white text, unemphasised `#D7D7D7` / `#434343`, inactive `#E7E7E7` / `#333333`. D4 currently says r 8.5; the live value is 8 |
| Tables (inset) | D | Rows 24, header 28 with a 1-pt bottom line, stripes and selection inset 10 with r 8, merged runs with a 1-pt divider, `#0064E1` / `#0059D1`, inactive `#DCDCDC` / `#464646` |
| Group boxes and forms | D | Fill `#F7F7F7` / `#252525`, r 12, no border, hairline separators between rows |
| Composer and other floating glass controls | **N** (glass) or D (resting look) | Capsule r 18, 36 tall, in a bottom split accessory: the pane's pocket is the accessory's 56 + 9 + 9, with the same hard edge as the toolbar (a 1-pt black 5 % line) |
| Search fields | D | Capsule-shaped at 24 tall (r 12) in the sidebar. In the toolbar, collapsed to a 36-pt magnifier capsule when space is short |
| Inactive state | D | Everything in §7. Our kit already knows the window's focus (`ui.Input.Focused`), and every chrome component must read it |

**For the native-shell decision.** Every layout number here can be drawn. The
pieces that cannot be matched by drawing are the live glass ones:

- the sidebar and inspector glass, with refraction and the slider;
- the specular toolbar capsules and the composer.

All of them sit *behind* or *around* our content, never in its middle. So
there are two routes:

- **(a)** host a real `NSToolbar` and `NSGlassEffectView`-backed panes around one transparent content layer;
- **(b)** draw everything, with the resting-state numbers above.

Route (b) misses the glass highlights and the colour pickup, and it does not follow the Liquid Glass slider. Within the active, neutral case it is within about 2 %.

## 9. Limits

- **One machine, one setting.** The MacBook only: medium sidebar, glass slider at 0.645, blue accent forced in the tool. Other slider positions change the glass opacity and were not measured. Changing the setting was not allowed.
- **Single-window captures.** GnuiShot captures one window without its shadow or the desktop behind it. On 27 the glass here samples only within the window, so nothing was lost, but window shadows are not measured.
- **The settings form** is AppKit's `NSBox` group box with real controls. System Settings itself is SwiftUI and was not captured: it shows the user's account. Its row heights (40) are this scene's choice. The box radius and fill are AppKit's.
- **Chat bubbles** are drawn by the tool and are not an AppKit control. Only their colour under the pocket is used, as a probe.
- **The Tahoe (26) floating sidebar** is not measured, because 27.2 has no switch for it.
- **Hover and press states** of the capsules were not captured. The pointer was never moved on the user's machine.

## Reproduce

```sh
bash tools/appkitscenes/build.sh
scp -r tools/appkitscenes/bin/AppKitScenes.app tools/appkitscenes/run.sh mac:gnui-test/appkitscenes/
ssh mac 'cd ~/gnui-test/appkitscenes && sh run.sh /tmp/gnui-scenes/final'          # about 2 min; windows appear briefly
scp 'mac:/tmp/gnui-scenes/final/*' ~/Projects/research/appkit-ref/scenes/captures/
tools/appkitscenes/bin/AppKitScenes.app/Contents/MacOS/AppKitScenes -measure ~/Projects/research/appkit-ref/scenes/captures
sh tools/appkitscenes/sheets.sh ~/Projects/research/appkit-ref/scenes/captures ~/Projects/research/appkit-ref/scenes/sheets
```
