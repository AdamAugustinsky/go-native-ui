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

`TestBandTextContrastOverGlass` (`cmd/rexlook/rex/contrast_test.go`)
checks every glyph against its floor, at both ends of the measured glass
range. It samples three rows of each glyph (top, middle, bottom) and builds
the background from the frame's own quads as the Metal shader shades them:

- four-corner colours interpolated premultiplied;
- rounded-rect coverage, borders and shadows;
- clips and fades.

It covers these states, light and dark: rest, an unchosen tab hovered, a
tab held down, the window not key, and not key with the pointer over a
tab. `TestBandContrastSeesGradients` shows the check reads the gradients:
darkening only the bottom colours of the capsule and the pill breaks the
floors. The same holds when the pill's bottom colour alone is darkened in
`draw.go`. The first version of the test read each fill's top-left colour
only and passed both.

The live check is screen captures inside the window's frame: a light and a
dark window behind it, light and dark looks, key and not, all at rest
(`/tmp/gnui-br/contrast/after-*`, measured as the D0 titles were). Hover and
press were not captured for contrast; only the test covers them. The worst
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
- **Accessibility:** refusing first responder does not hide the buttons from
  VoiceOver. `TestButtonsAccessible` checks this in-process. Each button's
  element is its cell: an NSButton itself is ignored (`AXUnknown`) and its
  `NSButtonCell` is the `AXButton`, on 27.2. The test checks that:
  - each cell is an `AXButton` labelled "Commands" or "New Tab";
  - it offers `AXPress`, and an `AXPress` delivers the button's command;
  - it is among the window's accessibility children, next to the traffic
    lights' cells.

### Full screen from inside a frame

Calling `toggleFullScreen:` inside a frame, with the frame's autorelease
pool pushed (`gpu.Surface.BeginFrame`), crashes about a second later. That
is when the zoom into full screen finishes. The crash is a SIGSEGV at
address 0 in `objc_autoreleasePoolPop`, at the frame's pool pop in
`EndFrame`.

It reproduced on the local Mac mini (Mac16,10, macOS 27.2 26B5101f) on
2026-10-19. The build was rexlook with `DebugToggleFullScreen` sending
`toggleFullScreen:` directly; that change was not committed. The command
was `rexlook -pose fullscreen`:

```
  0.00s glass radius 16, full screen false
  1.11s glass radius 0, full screen true
SIGSEGV: segmentation violation
PC=0x18f0f27d0 m=0 sigcode=2 addr=0x0
signal arrived during cgo execution
gonativeui/internal/ffi.(*Frame).Call1(0xb93a1f3e008?, 0x18f0f278c, ...)   0x18f0f278c = objc_autoreleasePoolPop (dladdr); the PC is +68
gonativeui/gpu.(*Surface).EndFrame(...)  gpu/metal_darwin.go:271           objc.F.Call1(objc.PoolPop, f.pool)
gonativeui/app.(*App).drawFrame → present → loop → RunWith
```

**The cause** (`p6-framepool`, found on the same Mac mini). The cause was
a frame drawn inside a frame, not a pool that AppKit popped.

The evidence is the frame's pool token at every `BeginFrame` and `EndFrame`,
and `_objc_autoreleasePoolPrint` before and after the toggle:

1. The frame pushes its pool (token `0x78b5014038`) and calls Build, and
   Build calls `toggleFullScreen:`.
2. Inside that call, AppKit resizes the window synchronously, and
   `windowDidResize:` calls the app's frame handler. The handler draws a
   second frame into the same `gpu.Frame`. It pushes a pool above the first
   (`0x78b56e85c8`) and runs Build inside Build. Its `EndFrame` pops its own
   pool and clears the shared `Frame`, token included.
3. `toggleFullScreen:` returns after 76 ms. The pool print then still shows
   the first pool, intact, at the bottom of the stack: AppKit had popped
   nothing.
4. The outer `EndFrame` pops token 0: SIGSEGV at address 0 in
   `objc_autoreleasePoolPop`.

The outer frame had also lost its drawable (a leaked retain) and its
command buffer. Any AppKit call that resizes the window or runs a nested
loop from Build does the same; an animated `zoom:` inside a nested
`runUntilDate:` crashed alike.

**The fix:**

- `app.App.frameHandler` does nothing while a frame is in progress
  (`inFrame`). What it would have handled stays queued for the next `Wait`.
- `gpu.Surface.BeginFrame` and `EndFrame` each drain a pool of their own
  before returning. The frame holds its drawable and command buffer by
  explicit retains, so no pool token outlives either call.
- `drawFrame` drains the app code's autoreleases in a pool whose token is a
  local variable.

**The tests** run in a child process, so a crash fails the test:

- `TestNestedRunLoopInFrame` covers `toggleFullScreen:`, and an animated
  `zoom:` in a nested run loop, both from Build. Without the fix, both
  crash.
- `TestGreenButtonFullScreen` clicks the green button from the run loop,
  into full screen and back, with frames drawn during both animations.
- `TestFrameDrawablesBounded` checks over 600 frames that drawables are
  released, a frame makes no Go allocations, and the leak counters stay
  level.

The spike's first report called it an "out-of-order pop", which was wrong.
The deferred `DebugToggleFullScreen` hook is no longer needed, but it is
harmless.

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
