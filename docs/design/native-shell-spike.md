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
- **Key state:** the band dims (opacity 0.55, flatter pill shadow) while the window is not key.

Over a backdrop, the capsule becomes a translucent film, and the band's text
uses label roles (black or white at alpha) so it stays legible over whatever
shows through.

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
