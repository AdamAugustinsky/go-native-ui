# Native shell, custom content: the rexlook spike (2026-10-19)

The question was whether AppKit chrome closes rexlook's "not native" gap
against Rex. The answer: yes, for the material and the window's own
controls. Phase 6's D41 tracks (materials, hosted views) are meant to absorb
the provisional API below.

## What is native, what is ours

| Part | Now | Before |
|---|---|---|
| Window background | `NSGlassEffectView` (macOS 26+), else an `NSVisualEffectView` (under-window material, blurring what is behind the window), under a non-opaque `CAMetalLayer` | painted pink-to-sand gradient |
| Traffic lights, drag, double-click zoom | AppKit's lights; the custom titlebar band's drag map | same |
| ⌘ and + | `NSButton`s above the Metal layer (borderless symbols; `-glass-buttons`: Liquid Glass circles) | drawn |
| Tabs | **ours**: Rex's tab capsule, drawn | ours |
| Host block, panes, text, shadows, hairlines | ours | ours |

## Tabs

Following the user's 2026-10-19 correction, the tabs are not native.
`NSWindow` tabbing, `NSSegmentedControl` and toolbar tab items are out. A
first cut put them in an `NSToolbar`, and that cut is how we know a toolbar
fights custom tabs: its title container covers the band from x = 300 to
1040 pt, and AppKit's hit test hands it every press there. So the toolbar is
gone. The band is the custom titlebar again, with its drag map.

Rex's tabs now behave like AppKit's:

- **Selection on the press**, as in `NSTabBar` window tabs. A segmented control selects on the release instead.
- **Instant switching:** the panes change in the press frame, and the pill glides (rate 26).
- **A press darkening** on the held tab.
- **The arrow cursor**, not the pointing hand.
- **Shortcuts:** ⌘1–⌘8 and ⌘9; ⌃Tab and ⌃⇧Tab; ⌘⇧] and ⌘⇧[; ⌘T.
- **Key state:** the band dims while the window is not key: a flatter pill shadow, the icons at opacity 0.55, and the text by its colour, down to the floors below.

## Text over the glass

Black or white text at a fixed alpha is not legible over whatever shows
through. The glass shows the windows behind it, and the first cut fell to
1.68:1 (an inactive tab's path, dark, a white window behind, window not
key). The band now keeps AppKit's own floors, measured on 27.2:

- **In the key window,** the title block and the chosen tab keep at least
  `secondaryLabelColor`'s contrast on the window background: 3.95:1 light
  (black at 0.498 over white), 5.84:1 dark (white at 0.549 over `#1E1E1E`).
- **Elsewhere** (the other tabs, and everything while the window is not key),
  the text keeps at least AppKit's own inactive window title:
  1.98:1 light (`#B8B8B8` on white) and 2.25:1 dark (`#5C5C5C` on `#262626`;
  the D0 captures, finder and chat).

How:

- **A scrim:** Rex's theme lays one scrim of its field's tone over the
  material (light `#F5EEF5` at 0.30, dark `#1E1F2A` at 0.45). It narrows the
  range of what shows.
- **A backing:** the tab capsule is its own tint at 0.5 and 0.55.
- **Alpha floors:** each text role has a label alpha in the key window and
  one when it is not, and the key state animates between them by colour, not
  by opacity.

`TestBandTextContrastOverGlass` checks every glyph against its floor. It
composes the frame's fills over both ends of the measured glass range.

The live check is screen captures inside the window's frame: a light and a
dark window behind it, light and dark looks, key and not
(`/tmp/gnui-br/contrast/after-*`, measured as the D0 titles were). The worst
cases are 5.16, 2.84, 6.91 and 3.32:1 against floors of 3.95, 1.98, 5.84 and
2.25. Before this, they were 3.17, 1.72, 2.60 and 1.68.

These are claims about the measured range, not a guarantee:

- a saturated or patterned window behind can show through differently;
- Reduce Transparency and Increase Contrast were not measured.

**Window-only captures misstate the glass.** A GnuiShot window capture
composites the glass with nothing behind it. It reads `#969696` where the
screen shows `#FCFCFC` over a white window. The earlier "light mode looks
grey" finding came from such captures and was wrong: over a light window,
the light glass is near white. Measure the glass from a display capture of
the window's frame.

## Provisional API (experimental)

- `platform.Options`:
  - `Backdrop` (`BackdropNone`, `BackdropWindow`, `BackdropGlass`, `BackdropGlassClear`);
  - `Buttons []NativeButton{ID, Symbol, Label, X, Y, Size, Style, Disabled}` (`ButtonPlain`, `ButtonGlass`).
- `platform.EvCommand` (Tag: the button's id). `app` turns it into `ui` commands (`TakeCommand`).
- `(*Window)`: `Backdrop()`, `HasButtons()`, `SetButtonEnabled(id, on)`. On Windows and Linux these are no-ops, and nothing changes there.
- `platform.Native.Transparent`: the Metal layer is non-opaque.
- `app.Config.Backdrop` and `Buttons`. A window that gets a backdrop clears to transparent.
- `theme.Resolved.Backdrop`: Rex's chrome paints no field, and its unfocused panes are really translucent.
- `rex.NativeConfig(glass)`.

The content view becomes a layer-backed container: the effect view, then
`GNUIView`, then the buttons. This is the plan's GNUIContainer, from
`gpui-ghostty.md` §3.1.

The shell's lifecycle:

- **Ownership:** each view goes to its superview once inserted, and
  `Window.Close` takes the shell apart. The buttons lose their target and
  every view is freed; `TestShellFreedOnClose` checks this over open and
  close cycles with zeroing weak references.
- **Full screen:** the glass's corner radius follows the window's, 0 in full
  screen, through `windowWillEnterFullScreen:` and `windowDidExitFullScreen:`
  (or `windowDidFailToEnterFullScreen:`) and every resize.
- **Focus:** the buttons refuse first responder, as titlebar controls do. Even
  with full keyboard access on, Tab never reaches them, and a click leaves
  the focus and an input method's composition in the content.

## Numbers (MacBook, macOS 27.2, under the user's load)

- **ffi callbacks:** 5 of 32 in a rexlook process, unchanged. The buttons' action shares `GNUIWindowDelegate`'s dispatcher.
- **Startup:**
  - backdrop: 1.3 ms, and buttons: 3.2 ms, inside `Open`;
  - first frame: +7 ms;
  - exec to on glass: about 139 ms, against about 133 ms for main (budget 150).
- **`rexlook -bench -gate` (native):** 0 allocations, 777 µs per frame, paced.
- **`-drawn`:** 0 allocations. The CPU gate fails at about 1090 µs per frame, and so does main (1115–1127) at load 3.5.
- **Gallery gate:** 0 allocations, passed.
- **Idle (`rexlook -idle 3s`):** 0 frames.
- **`rexcompare`:**
  - drawn: 40/40;
  - `-o -native`: 13/40, since the field is transparent and the edge crossings need the painted background.

  The pane and tab geometry is identical by test (`TestNativeShellScene`).
