# Visual benchmark: the Rex look

**Goal (user, 2026-10-16).** Our interfaces must look modern. As the
visual benchmark, recreate the frontend of Superlogical's **Rex** terminal
multiplexer in our framework. This covers visuals only: no terminal
emulation, no PTYs, no networking.

**Reference.** The screenshot in "Public testing is beginning"
(<https://www.superlogical.com/updates/public-testing-beginning>,
2026-10-05): `https://www.superlogical.com/rex-screenshot@2x.webp`, 2114 × 1278 px at 2×.

```sh
curl -sSLo /tmp/rex.webp https://www.superlogical.com/rex-screenshot@2x.webp &&
  sips -s format png /tmp/rex.webp --out docs/design/ref/rex-screenshot@2x.png
```

## Boundaries

This is an internal benchmark, not our product's identity.

- **The reference image:** kept in the gitignored `docs/design/ref/`, never committed.
- **No assets copied:** none of Superlogical's or third parties' logos, app icons, fonts or code. Icons are our own placeholders or OS system symbols.
- **What we reproduce:** layout, proportions, colour, depth and motion, to prove the renderer can reach this quality.
- **Our own product look:** chosen separately (open question).

## Measured spec

All values come from the reference screenshot and are given in points
(px ÷ 2). Colours are sampled sRGB. Text colours are the darkest core
pixel of 2× glyph strokes, so they are approximate.

### Window and background

| Element | Value |
|---|---|
| Window | 1057 × 639 pt; macOS traffic lights at x ≈ 21 / 43 / 65 pt, centred at y ≈ 22 pt |
| Titlebar band | 46 pt (panes start at y = 46) |
| Background | Soft vertical gradient: pink-lavender at the top (≈ `#F5E8F5`–`#F7EBF7`), neutral in the middle (≈ `#E6E5E5`, sampled between panes), warm sand at the bottom (≈ `#EADFC3`). The sides run slightly warmer (`#E8D7C9`). Fit the final stops in the comparison loop |
| Gutters | 8 pt from the window edges and 8 pt between panes, everywhere |

### Panes (cards)

| Element | Value |
|---|---|
| Corner radius | 9 pt |
| Fill, focused | opaque white `#FFFFFF` |
| Fill, unfocused | translucent white that picks up the background (`#F4F4F4` mid, `#F4F4E3` near the bottom); ≈ 60–70 % white |
| Edge | 0.5 pt darker hairline (`#C6BCB2` at the outer edge) plus a 0.5 pt light inner highlight on the bottom edge (`#FFFFF1`) |
| Shadow | soft drop shadow, ~3–5 pt, visible under each card |
| Header | 16 pt process icon and title, centred ~18 pt below the card top. Content starts ~46 pt below the top; bullets at 11 pt inset, text at 25 pt |
| Header controls | split side by side, split stacked, maximise, close. Glyphs `#96999B`, ~22 pt apart, shown **only on the focused pane** |

### Tabs (in the titlebar)

| Element | Value |
|---|---|
| Group | one translucent capsule (`#E8DDE8` over the `#F5E8F5` background), ~28 pt tall |
| Active tab | white pill (`#FFFAFF`), full capsule radius, soft shadow |
| Tab content | a stacked "cards" icon (process icon in front, coloured cards behind) and the title |
| Inactive titles | fade out with an alpha gradient when truncated; hairline separators between inactive tabs |
| Host block | to the left of the tabs: device glyph, host name (`#5F5D63`, semibold) over the machine name (`#86848B`, smaller) |
| Right edge | ⌘ and + glyphs (`#5F5D62`) |

### Type

| Use | Font | Colour |
|---|---|---|
| Tab titles | system UI (SF Pro) ~13–14 pt regular | active `#1E2227`, inactive `#5A5A60` |
| Pane titles | SF Pro ~13 pt semibold | focused `#202228`, unfocused `#606064` |
| Terminal text | monospace ~13 pt; cell ≈ 7.6 × 16 pt (line height ≈ 1.23). The face is unidentified (not SF Mono), so we use a system monospace font and match the metrics | — |

### Terminal palette

The palette is Catppuccin-Latte-like (blue matches Latte exactly). Unfocused panes use a dimmed palette, faded toward the fill:

| Role | Focused | Unfocused |
|---|---|---|
| Foreground | `#1E2228` | `#5E6161` |
| Blue | `#1E66F5` | `#4F92D9` |
| Teal | `#218A91` | `#5CA0A3` / `#67A3B3` |
| Red | `#CF3E4B` | `#DA6066` |
| Green | `#378925` (bullet `#106329`) | `#558E62` |
| Dim / secondary | `#52566F`, `#8F9194` | `#9B9B97` |
| Purple, orange | — | `#A481DF`, `#B78942` |

## What the renderer and platform must add

ARCHITECTURE §6 already plans instanced SDF rounded rects with per-corner
radii, borders, erf soft shadows, two-colour gradients, and glyph and image
atlases. The Rex look also needs:

1. **Richer gradients:** at least three stops, or four-corner (bilinear) colour, for the window background.
2. **Fade masks:** an alpha ramp on a clipped run, for truncated tab titles.
3. **Exact hairlines:** 0.5 pt (one device pixel at 2×) borders and inner highlights snapped to device pixels.
4. **Layered shadows:** two per card, a tight one plus an ambient one.
5. **System symbols:**
   - macOS: SF Symbols via `NSImage imageWithSystemSymbolName:`, rasterised into the atlas;
   - Windows: the Segoe Fluent Icons font;
   - Linux: a small icon set of our own (licence to decide).
6. **Custom window chrome:**
   - macOS: a transparent full-size titlebar of custom height, with the traffic lights repositioned;
   - Windows: our own caption area (`WM_NCCALCSIZE` plus DWM frame);
   - Wayland: client-side decorations (already planned).
7. **Focus dimming:** an animated colour transform per pane (palette and fill toward the background).
8. **Liquid Glass not needed:** the background is smooth, so a blur is invisible here. `NSGlassEffectView` stays an optional macOS extra.

## Benchmark app and acceptance

`cmd/rexlook` renders the reference layout: three panes holding static
ANSI-coloured text matching the screenshot, the tab bar, and the host
block.

**Phase 2 (renderer + text): a static frame on macOS.**
- **Capture:** render at 1057 × 639 pt on a 2× display (the test MacBook) and capture the window with `screencapture -l <windowid>`.
- **Side-by-side review:** reference and capture next to each other, plus a flicker overlay.
- **Flat regions** (background, pane fills, tab capsule and pills): CIEDE2000 ΔE ≤ 3 against the reference.
- **Geometry** (pane edges, radii, gutters, tab pills): within ±1 px at 2×.
- **Text:** metrics within ±0.5 pt (cell size, line height, baseline). The glyph shapes differ wherever the font differs, so judge them by eye.

**Phase 3 (UI core): interactive.**
- **Interactions:** hover states; focus switching with animated dimming; tab switching; split, close and maximise; live pane resize.
- **Gates:** every animated frame is paced and allocates nothing; idle presents nothing.

**Startup, every phase.** `rexlook` must reach half-bounce with the full
frame visible (ARCHITECTURE §3, "Content-first startup").

**Windows and Linux.** The same app with native window controls. Judge by
eye against the macOS capture once CI or hardware can run it.
