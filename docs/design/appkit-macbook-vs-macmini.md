# AppKit reference: MacBook vs Mac mini

Written 2026-10-20 by `tools/appkitref/measure -diff macbook macmini` (the tables after the line) plus a reading of them. The Mac mini set is complete: offscreen runs, `scroller.m`, the scroll recordings, the live AppKitRef pages and D0's `appkitscenes`.

## Headline

- **The layout of AppKit is identical on both machines; the differences are small and have three causes.** NSScroller geometry: 84 of 84 values identical. `metrics.json`: 8,675 of 8,729 identical. The scenes' window, toolbar, split-view and table geometry: identical (27,974 of 28,040 values, with overlay scrollers on both). 189 of 202 sample renders are pixel-identical, including the text of the single controls.
- **Cause 1, the input device: scroll bars.** The Mac mini has a mouse attached, so the system's scroller style is **legacy**; the MacBook's trackpad gives **overlay**. In D0's live windows that makes every sidebar and list 17 pt narrower (a legacy bar takes 17 pt). The mini's scenes were therefore captured twice: `captures-macmini` with the system's own legacy bars, and `captures-macmini-overlay` with `-AppleShowScrollBars WhenScrolling` in the argument domain of that process only, which is the like-for-like set against the MacBook. A fresh process reports overlay until its run loop has heard from the devices (`machine.h` reads it after that).
- **Cause 2, the macOS build** (26B5091g against 26B5101f; one machine each cannot separate build from hardware):
  - **the selected sidebar row is 13 pt regular on the mini and 13 pt semibold (weight 0.3) on the MacBook**, in all 16 Finder and Settings captures and in `metrics.json` (`sidebar-*` row 4). `theme.StyleBodySemibold` and `liquid-glass-reference.md` say semibold, from the MacBook. This is the one finding that may change a token on this build;
  - the dark `separatorColor` is `#FFFFFF1F` (MacBook `#FFFFFF19`), light `#0000001A` (`#00000019`): the only system colour that differs;
  - the stepper and date-picker chevrons are up to 11 steps darker in dark mode.
- **Cause 3, the display or the menu bar: the menu font** is 13 pt (line 16) on the mini and 14 pt (line 17) on the MacBook, so a menu with a shortcut and an image is 147 pt wide against 153. Our menus are native, so no recipe uses it.
- **The recipe test is not green against the mini's numbers**: `TestRecipesMatchAppKit` with `GNUI_APPKIT_REF=macmini` misses 7 checks (below), so CI stays on the MacBook set.
- The mini repeats itself exactly: three AppKitRef runs with the same flags gave identical JSON and identical PNGs. Differences are therefore the machine, the build or the tool revision, not run-to-run noise.

## Before reading the numbers

The MacBook renders are an older tool revision. `macbook-blue/` predates D3b: it has no `toggle-*`, `link`, `outline-*` or `table-inset-fit-*`, and its tables came from an older `main.m` (the Size column sits 5 px further left and has an extra divider). The committed MacBook recipe numbers for those samples came from a later run whose PNGs were not kept. Where the mini differs on a table, or on a width the MacBook file carries and the mini file no longer writes (`toggle-off` 65 pt), that is revision, not machine. The mini set is the first one produced entirely by the current tool.

## Biggest differences, in order

| What | MacBook | Mac mini | Effect |
|---|---|---|---|
| Sidebar and list width in live windows | overlay bars | legacy bars | Tables 17 pt narrower (280 → 263, 180 → 163): 2,268 numbers in the scene dumps, all the same 17 pt. Gone in the overlay run. |
| `table-inset-fit-*` selection height (two selected rows with a 1 pt hairline between them) | 48 pt | 23 pt | **A measuring artefact.** AppKit draws one 48 pt shape. On the mini the hairline in dark is `#1F6DD7` against the fill `#0059D1` (step 37), over the sample's colour tolerance of 30, so the run stops at it; the inactive samples have a tolerance of 15. Misses 3 recipe checks. The fix is `Tol: 40` on those two samples in `rexui/catalog/lists.go` (not changed here). |
| Menu and menu-bar font | 14 pt (line 17) | 13 pt (line 16) | Menus 6 pt narrower with a shortcut and image, 2 pt narrower otherwise. |
| Selected sidebar row font | semibold | regular | See cause 2. |
| Spinner fill at the probe | `#6B6B6B` light, `#A1A1A1` dark | `#737373`, `#9A9A9A` | ΔE 3.1 and 2.2, over the 2.0 tolerance: 4 recipe checks. A thin anti-aliased spoke read at one pixel; I did not isolate why it differs. |
| Disclosure chevron, separator, toggle label ink | | | ΔE 0.6 to 2.7, within or beside tolerance (the recipe test passes them). The toggle label also varied between runs on the MacBook (`rexui/README.md`); on the mini it does not vary. |
| `separatorColor` | `#FFFFFF19` dark, `#00000019` light | `#FFFFFF1F`, `#0000001A` | The build. Separator renders differ by one pixel row (5 steps in dark). The light alert differs in a 128 px square at its top left (its icon). |
| Scrolling: `wheel-mid` | first offset change 22 ms after the event | 9 ms | `TestScrollTracksAppKit` with `macmini` fails this one scenario by 28.5 pt at t = 0: the fitted display latency (−10 ms) sits at the edge of its range. The physics (30 pt per wheel event, c = 0.073, k = 12.9) match; the other 17 scenarios pass. |
| User settings | `AppleShowScrollBars` unset, `NSTableViewDefaultSizeMode` 2 | `Automatic`, unset | Recorded in `behaviour`. |

## Text ink and colours

Text and colour barely differ in the offscreen renders: apart from the tables (older tool revision) the text renders are pixel-identical, and `colors` differs only in `separatorColor`. The display profile cannot enter offscreen renders (AppKitRef draws into sRGB bitmaps, GnuiShot captures in sRGB by default), so the LG panel against the built-in XDR panel does not show in any of these numbers. The live pages and scenes were captured at 2× on both; the dumps' system colours differ only in `separatorColor`.

## Our recipes against the mini set (2026-10-20)

- `GNUI_APPKIT_REF=macmini go test ./rexui -run TestRecipesMatchAppKit`: 7 misses (4 spinner fills, 3 table-inset-fit heights). The MacBook set passes.
- Offscreen, `tools/appkitref/ours` against `macmini-blue`: 647 of 654 within tolerance (the same 7); against `macmini-system` (pink): 648 of 654.
- Live, the gallery's appkit pages against AppKitRef's own live pages on this Mac, light and dark, pages 0 to 2: 533 of 552. The misses are the checkbox and search-field radii of 0.6 pt that the MacBook run had, AppKit's mini switch, which is 2.2 pt wider than ours in light, and two animated samples (`spinner`, `progress-indeterminate`) read at different phases. Both apps ran with this Mac's legacy scroll bars.
- `TestLegacyScrollersMatchAppKit` passes on both sets. rexcompare uses Rex's own screenshot and is unchanged: 40 of 40.

---

| | macbook | macmini |
|---|---|---|
| model | Mac15,6 | Mac16,10 |
| macOS | 27.2 (26B5091g) | 27.2 (26B5101f) |
| display | Built-in Liquid Retina XDR Display, 1512×982 pt @2x, 120 Hz | LG ULTRAGEAR+, 2560×1440 pt @2x, 120 Hz |
| appearance | dark | dark |
| accent | pink | pink |
| accentRendered | blue | blue |
| scrollerStyle | overlay | legacy |
| date | 2026-10-18 | 2026-10-20 |

## Recipe measurements (rexui/testdata/appkit-27.2.json)

753 values compared, 726 identical, 18 colours, 9 numbers and 10 other differ.

Colours (largest 8 of 18):

| path | A | B | ΔE2000 |
|---|---|---|---|
| `/samples/spinner/light/fill` | #6B6B6B | #737373 | 3.13 |
| `/samples/disclosure-closed/dark/fill` | #797979 | #808080 | 2.72 |
| `/samples/disclosure-open/dark/fill` | #797979 | #808080 | 2.72 |
| `/samples/toggle-off/light/label` | #2C2C2C | #242424 | 2.53 |
| `/samples/toggle-on-inactive/light/label` | #2A2A2A | #222222 | 2.52 |
| `/samples/spinner-small/light/fill` | #6D6D6D | #737373 | 2.36 |
| `/samples/spinner/dark/fill` | #A1A1A1 | #9A9A9A | 2.18 |
| `/samples/separator/dark/fill` | #343434 | #393939 | 1.62 |

Numbers (largest 8 of 9):

| path | A | B | |Δ| |
|---|---|---|---|
| `/samples/table-inset-fit-emphasized/dark/h` | 48 | 23 | 25.00 |
| `/samples/table-inset-fit-inactive/dark/h` | 48 | 23 | 25.00 |
| `/samples/table-inset-fit-inactive/light/h` | 48 | 23 | 25.00 |
| `/samples/spinner-small/dark/w` | 2.14 | 2 | 0.14 |
| `/samples/spinner-small/light/w` | 2.14 | 2 | 0.14 |
| `/samples/spinner/dark/w` | 4.14 | 4 | 0.14 |
| `/samples/spinner/light/w` | 4.14 | 4 | 0.14 |
| `/samples/level/dark/h` | 16.07 | 16 | 0.07 |

Other (first 6 of 10):

- `/samples/toggle-off/dark/w`: present vs missing
- `/samples/toggle-off/light/w`: present vs missing
- `/samples/toggle-on-disabled/dark/label`: present vs missing
- `/samples/toggle-on-disabled/dark/w`: present vs missing
- `/samples/toggle-on-disabled/light/label`: present vs missing
- `/samples/toggle-on-disabled/light/w`: present vs missing

## NSScroller geometry (theme/testdata/appkit-scroller-27.2.json)

84 values compared, 84 identical, 0 colours, 0 numbers and 0 other differ.

## AppKitRef metrics.json (default accent)

8729 values compared, 8675 identical, 4 colours, 43 numbers and 131 other differ.

Colours (largest 4 of 4):

| path | A | B | ΔE2000 |
|---|---|---|---|
| `/colors/NSAppearanceNameAccessibilityAqua/separatorColor` | #00000019 | #0000001A | 0.00 |
| `/colors/NSAppearanceNameAccessibilityDarkAqua/separatorColor` | #FFFFFF19 | #FFFFFF1F | 0.00 |
| `/colors/NSAppearanceNameAqua/separatorColor` | #00000019 | #0000001A | 0.00 |
| `/colors/NSAppearanceNameDarkAqua/separatorColor` | #FFFFFF19 | #FFFFFF1F | 0.00 |

Numbers (largest 8 of 43):

| path | A | B | |Δ| |
|---|---|---|---|
| `/menus/menuWithShortcutAndImageSize/w` | 153 | 147 | 6.00 |
| `/menus/fiveItemMenuSize/w` | 73 | 71 | 2.00 |
| `/menus/oneItemMenuSize/w` | 73 | 71 | 2.00 |
| `/controls/dark/sidebar-emphasized/scrollers/style` | 1 | 0 | 1.00 |
| `/controls/dark/sidebar-unemphasized/scrollers/style` | 1 | 0 | 1.00 |
| `/controls/light/sidebar-emphasized/scrollers/style` | 1 | 0 | 1.00 |
| `/controls/light/sidebar-unemphasized/scrollers/style` | 1 | 0 | 1.00 |
| `/fonts/menuBarFont/lineHeight` | 17 | 16 | 1.00 |

Other (first 6 of 131):

- `/behaviour/AppleShowScrollBars`: (unset) vs Automatic
- `/behaviour/NSTableViewDefaultSizeMode`: 2 vs (unset)
- `/controls/dark/link`: missing vs present
- `/controls/dark/outline-emphasized`: missing vs present
- `/controls/dark/outline-inset-emphasized`: missing vs present
- `/controls/dark/outline-unemphasized`: missing vs present

## appkitscenes dumps (40 captures)

33136 values compared, 30808 identical, 0 colours, 2268 numbers and 60 other differ.

Numbers (largest 8 of 2268):

| path | A | B | |Δ| |
|---|---|---|---|
| `/chat-dark-active-scrolled/tables[0]/frame/w` | 280 | 263 | 17.00 |
| `/chat-dark-active-scrolled/tables[0]/rows[0]/cell/w` | 248 | 231 | 17.00 |
| `/chat-dark-active-scrolled/tables[0]/rows[0]/rect/w` | 280 | 263 | 17.00 |
| `/chat-dark-active-scrolled/tables[0]/rows[0]/rowView/w` | 280 | 263 | 17.00 |
| `/chat-dark-active-scrolled/tables[0]/rows[1]/cell/w` | 248 | 231 | 17.00 |
| `/chat-dark-active-scrolled/tables[0]/rows[1]/rect/w` | 280 | 263 | 17.00 |
| `/chat-dark-active-scrolled/tables[0]/rows[1]/rowView/w` | 280 | 263 | 17.00 |
| `/chat-dark-active-scrolled/tables[0]/rows[2]/cell/w` | 248 | 231 | 17.00 |

Other (first 6 of 60):

- `/chat-dark-active-scrolled/colors/separatorColor`: separatorColor #FFFFFF19 vs separatorColor #FFFFFF1F
- `/chat-dark-active-top/colors/separatorColor`: separatorColor #FFFFFF19 vs separatorColor #FFFFFF1F
- `/chat-dark-inactive-scrolled/colors/separatorColor`: separatorColor #FFFFFF19 vs separatorColor #FFFFFF1F
- `/chat-dark-inactive-top/colors/separatorColor`: separatorColor #FFFFFF19 vs separatorColor #FFFFFF1F
- `/chat-light-active-scrolled/colors/separatorColor`: separatorColor #00000019 vs separatorColor #0000001A
- `/chat-light-active-top/colors/separatorColor`: separatorColor #00000019 vs separatorColor #0000001A

## appkitscenes dumps, macmini with overlay scrollers (32 captures)

28040 values compared, 27974 identical, 0 colours, 18 numbers and 48 other differ.

Numbers (largest 8 of 18):

| path | A | B | |Δ| |
|---|---|---|---|
| `/notes-light-inactive-scrolled/window/occlusionVisible` | 1 | 0 | 1.00 |
| `/notes-light-inactive-top/window/occlusionVisible` | 1 | 0 | 1.00 |
| `/finder-dark-active-scrolled/tables[1]/rows[5]/text/font/weight` | 0.3 | 0 | 0.30 |
| `/finder-dark-active-top/tables[1]/rows[5]/text/font/weight` | 0.3 | 0 | 0.30 |
| `/finder-dark-inactive-scrolled/tables[1]/rows[5]/text/font/weight` | 0.3 | 0 | 0.30 |
| `/finder-dark-inactive-top/tables[1]/rows[5]/text/font/weight` | 0.3 | 0 | 0.30 |
| `/finder-light-active-scrolled/tables[1]/rows[5]/text/font/weight` | 0.3 | 0 | 0.30 |
| `/finder-light-active-top/tables[1]/rows[5]/text/font/weight` | 0.3 | 0 | 0.30 |

Other (first 6 of 48):

- `/chat-dark-active-scrolled/colors/separatorColor`: separatorColor #FFFFFF19 vs separatorColor #FFFFFF1F
- `/chat-dark-active-top/colors/separatorColor`: separatorColor #FFFFFF19 vs separatorColor #FFFFFF1F
- `/chat-dark-inactive-scrolled/colors/separatorColor`: separatorColor #FFFFFF19 vs separatorColor #FFFFFF1F
- `/chat-dark-inactive-top/colors/separatorColor`: separatorColor #FFFFFF19 vs separatorColor #FFFFFF1F
- `/chat-light-active-scrolled/colors/separatorColor`: separatorColor #00000019 vs separatorColor #0000001A
- `/chat-light-active-top/colors/separatorColor`: separatorColor #00000019 vs separatorColor #0000001A

## Sample renders (pixels)

202 renders compared: 189 pixel-identical, 13 differ, 0 have a different size.

Most different (share of pixels, largest channel step):

| render | pixels differing | max step |
|---|---|---|
| light/alert | 6.7% (16200) | 43 |
| light/separator | 3.0% (800) | 1 |
| dark/separator | 3.0% (800) | 5 |
| dark/stepper | 2.7% (267) | 11 |
| dark/table-fullwidth-unemphasized | 2.3% (11154) | 225 |
| dark/table-fullwidth-inactive | 2.3% (11154) | 225 |
| dark/table-fullwidth-emphasized | 2.3% (11152) | 255 |
| light/table-fullwidth-emphasized | 2.2% (10738) | 255 |

