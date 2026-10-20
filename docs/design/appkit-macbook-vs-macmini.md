# AppKit reference: MacBook vs Mac mini

Written 2026-10-20 by `tools/appkitref/measure -diff macbook macmini` (the tables below the line) plus a reading of them. Offscreen parts only; the live pages and the `appkitscenes` captures wait for GnuiShot's Screen Recording grant on the Mac mini (see the end).

## Headline

- **The two machines agree almost everywhere.** 8,729 `metrics.json` values: 8,675 identical. NSScroller geometry: 84 of 84 identical. 190 of 202 sample renders are pixel-identical, including all the text in the single controls: fonts, text ink and the system colours match (the 12 that differ are listed below). The system colours differ in one entry, `separatorColor`.
- **The recipe test is not green against the mini's numbers**: `TestRecipesMatchAppKit` with `GNUI_APPKIT_REF=macmini` misses **7 of the checks** (4 spinner fills, 3 table-inset-fit heights, below). So CI stays on the MacBook set.
- The mini repeats itself exactly: two AppKitRef runs, same flags, gave identical JSON and identical PNGs. The differences are therefore the machine, the macOS build or the tool revision, not run-to-run noise.

## Two things to know before reading the numbers

1. **The build differs**: 26B5091g (MacBook) and 26B5101f (mini). The earlier note in `platform/appearance_values_darwin_test.go` already records the dark `separatorColor` change (`#FFFFFF19` → `#FFFFFF1F`). One machine each cannot separate build from hardware; only the menu font (below) is plausibly hardware.
2. **The MacBook renders are an older tool revision.** `macbook-blue/` predates D3b: it has no `toggle-*`, `link`, `outline-*` or `table-inset-fit-*`, and its tables were drawn by an older `main.m`. The committed MacBook recipe numbers for those samples came from a later run whose PNGs were not kept. Where the mini differs on a table, a sample's width the MacBook file still carries (`toggle-off` 65) and the mini file no longer writes, that is revision, not machine. The mini set is the first one produced entirely by the current tool.

## Biggest differences, in order

| What | MacBook | Mac mini | Effect |
|---|---|---|---|
| `table-inset-fit-*` selection height (two selected rows, a 1 pt hairline between them) | 48 pt | 23 pt | **A measuring artefact.** AppKit draws one 48 pt shape. On the mini the hairline in dark is `#1F6DD7` against the fill `#0059D1` (step 37), over the sample's colour tolerance of 30, so the run stops at it; the inactive samples have a tolerance of 15. Misses 3 recipe checks. Fix is `Tol: 40` on those two samples in `rexui/catalog/lists.go` (not changed here). |
| Menu and menu-bar font | 14 pt (line 17) | 13 pt (line 16) | Menus 6 pt narrower with a shortcut and image, 2 pt narrower otherwise. Plausibly the MacBook's taller notch menu bar; our menus are native, so no recipe uses it. |
| Spinner fill at the probe | `#6B6B6B` light, `#A1A1A1` dark | `#737373`, `#9A9A9A` | ΔE 3.1 and 2.2, over the 2.0 tolerance: 4 recipe checks. A thin anti-aliased spoke read at one pixel; I did not isolate why it differs. |
| Disclosure chevron, separator, toggle label ink | | | ΔE 0.6 to 2.7, all within or beside tolerance (the recipe test passes them). The toggle label also varied between runs on the MacBook (`rexui/README.md`); on the mini it does not vary. |
| `separatorColor` | `#FFFFFF19` dark, `#00000019` light | `#FFFFFF1F`, `#0000001A` | The build. Separator renders differ by one pixel row (5 steps in dark). |
| Stepper and date-picker chevron, dark | | | 267 pixels, up to 11 steps lighter on the MacBook. |
| Scrolling: `wheel-mid` | first offset change 22 ms after the event | 9 ms | `TestScrollTracksAppKit` with `macmini` fails this one scenario by 28.5 pt at t = 0: the fitted display latency (−10 ms) is at the edge of its range. The physics (30 pt per wheel event, the band constants c = 0.073, k = 12.9) match; the other 17 scenarios pass. |
| User settings, not hardware | `AppleShowScrollBars` unset, `NSTableViewDefaultSizeMode` 2 | `Automatic`, unset | Recorded in `behaviour`; the scroller style is overlay on both. |

## Text ink and colours

Text and colour barely differ in the offscreen renders: apart from the tables (older tool revision) the text renders are pixel-identical, and `colors` differs only in `separatorColor`. The display profile cannot enter offscreen renders (AppKitRef draws into sRGB bitmaps), so an LG panel against a built-in XDR panel shows up only in live captures, and GnuiShot records those in sRGB by default. That comparison is part (b)/(c) and has not run.

## The recipe test against the mini set

`GNUI_APPKIT_REF=macmini go test ./rexui -run TestRecipesMatchAppKit`: 7 misses (above). The MacBook set passes. `TestLegacyScrollersMatchAppKit` passes on both. rexcompare uses Rex's own screenshot and is unchanged: 40 of 40.

---

| | macbook | macmini |
|---|---|---|
| model | Mac15,6 | Mac16,10 |
| macOS | 27.2 (26B5091g) | 27.2 (26B5101f) |
| display | Built-in Liquid Retina XDR Display, 1512×982 pt @2x, 120 Hz | LG ULTRAGEAR+, 2560×1440 pt @2x, 120 Hz |
| appearance | dark | dark |
| accent | pink | pink |
| accentRendered | blue | blue |
| scrollerStyle | overlay | overlay |
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

## appkitscenes measured.json

skipped: open /Users/adamaxelaugustinsky/Projects/research/appkit-ref/scenes/captures-macmini/measured.json: no such file or directory

## Sample renders (pixels)

202 renders compared: 190 pixel-identical, 12 differ, 0 have a different size.

Most different (share of pixels, largest channel step):

| render | pixels differing | max step |
|---|---|---|
| light/separator | 3.0% (800) | 1 |
| dark/separator | 3.0% (800) | 5 |
| dark/stepper | 2.7% (267) | 11 |
| dark/table-fullwidth-unemphasized | 2.3% (11154) | 225 |
| dark/table-fullwidth-inactive | 2.3% (11154) | 225 |
| dark/table-fullwidth-emphasized | 2.3% (11152) | 255 |
| light/table-fullwidth-emphasized | 2.2% (10738) | 255 |
| light/table-fullwidth-inactive | 2.2% (10641) | 249 |

