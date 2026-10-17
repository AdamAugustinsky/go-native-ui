# Phase 3 measurements (macOS)

**Test machine:** MacBook Pro 14" (M3 Pro, ProMotion 120 Hz, macOS 27.2), driven over SSH. Someone was using it during these runs, so absolute startup numbers are noisy (±40 ms between runs, and the first of an A/B pair is slower).

## Step 3: platform additions (live resize and cursors)

### What was built

| API (same on every OS) | macOS | Windows | Linux |
|---|---|---|---|
| `Window.SetFrameHandler(fn)` | `fn` runs inside AppKit's live-resize loop | `fn` runs inside the size/move modal loop | accepted, never called (no modal loop) |
| `Window.Pending() []Event` | queued events, no OS pumping | same | always empty |
| `Window.SetCursor(Cursor)`, `CursorArrow/Pointer/Text/ResizeEW/ResizeNS` | `NSCursor` | `LoadCursorW` + `SetCursor` + `WM_SETCURSOR` | no-op (later: `wp_cursor_shape_v1` / `XDefineCursor`) |

macOS only: `platform.InLiveResize() bool` (read by `gpu.Surface`), and the test hook `Window.SimulateLiveResize` (`platform_darwin_debug.go`).

`Pending` copies the queue into a second reusable slice and clears the queue, so a handler that runs inside `Wait` never sees events twice and `Wait` still returns the events that arrive after the handler. Zero allocations in steady state (`platform/queue_test.go`).

### Experiment: does `nextDrawable` work after the `CAMetalDisplayLink` is invalidated?

**Yes.** A throwaway probe (deleted after the run) did this on the MacBook:
1. Open a window, create the surface, `SetFrameSource`, draw the first frame.
2. 20 paced frames through the `CAMetalDisplayLink` (`EvFrame` + `Present`).
3. `enterLiveResize` (invalidate and release the metal link, create a view `CADisplayLink`), then 40 × `setContentSize:` with a handler that calls `Present` (so `nextDrawable`).
4. `exitLiveResize` (new `CAMetalDisplayLink`), then 20 more paced frames.

Result: 39 of 39 handler presents succeeded (the 40th size was unchanged), nothing threw, and the 20 paced frames afterwards were delivered by the new link. `rexlook -live-resize-test` repeats it with the real renderer (`GNUI_DEBUG=1` prints `live resize begins; metal link detached: true`, then 150 `windowDidResize:` and the `metalDisplayLink:needsUpdate:` ticks resume after the end). The layer only refuses `nextDrawable` while a link is attached, so the alternative design (keep the link and draw in its ticks) was not needed.

### Design (macOS)

1. **Live-resize detection.** `windowWillStartLiveResize:` and `windowDidEndLiveResize:` on the window delegate (both are `NSWindowDelegate` methods). Two safeguards: `windowDidResize:` also enters the mode if `-[NSWindow inLiveResize]` is true, and `Wait` leaves it if `inLiveResize` is false. The mode is only entered when a handler is registered.
2. **Frame source switch.** Enter: release the tick's drawable, invalidate and release the `CAMetalDisplayLink`, create a plain view `CADisplayLink` (common run-loop modes, so it ticks inside AppKit's tracking loops). Exit: invalidate that link and attach a new `CAMetalDisplayLink` (`attachMetalLink`, the code the first-tick switch uses). If the first-tick switch has not happened yet, nothing is replaced and that switch is suppressed until the drag ends.
3. **When the handler runs.** (a) From `windowDidResize:` when `pushResize` queued an `EvResize`, i.e. once per size change, synchronously, so the content is drawn for the window size AppKit is about to show. (b) From a view-link tick when a frame is armed (`RequestFrame`), so animations keep running while the mouse is held still. The handler is never re-entered.
4. **Presenting.** `gpu.Surface.BeginFrame` asks `platform.InLiveResize()`. While it is true the surface sets `presentsWithTransaction = YES` on the layer, `TakeMetalDrawable` reports not linked, so the surface calls `nextDrawable`, and `EndFrame` uses Apple's sequence: `commit`, `waitUntilScheduled`, `[drawable present]` (no explicit `CATransaction`: the present joins the transaction that carries the window's new size). When the mode ends, the next `BeginFrame` switches `presentsWithTransaction` off again before the normal `presentDrawable:` path.
5. **No stretching.** `contentsGravity = topLeft` is set on the `CAMetalLayer`: a drawable that is not redrawn yet stays at its own size, anchored at the top-left, never scaled. The first resize frame is drawn inside the first `windowDidResize:` (before `-[NSWindow sendEvent:]` returns to the run loop), so no frame at the old size is presented after the window grew.
6. **Allocation.** `BeginFrame`..`EndFrame` in live resize allocates nothing (below). The mode switches (twice per drag) are not per frame.

### Design (cursors, macOS; superseded by step 5 below)

`SetCursor` does nothing when the shape is unchanged. Otherwise it stores the shape, calls `-[NSCursor set]` immediately if the pointer is over the view (tracked from the mouse events and the tracking area's enter/exit), and invalidates the window's cursor rects for the view. `GNUIView` implements `resetCursorRects`, which covers the whole bounds with the current cursor. Reasoning: AppKit resets the cursor on mouse moves for a view without cursor rects, so `set` alone does not stick; cursor rects alone lag by one mouse move because AppKit rebuilds them lazily. The combination (what GLFW and SDL do) changes the cursor at once and keeps it. `GNUI_DEBUG=1` shows `resetCursorRects` firing after `SetCursor`. A cursor rect applies while the window is key; `set` covers the first moments.

### Design (Windows, cross-compiled only)

`WM_ENTERSIZEMOVE` starts a 16 ms `WM_TIMER`, `WM_EXITSIZEMOVE` kills it. The handler runs from `WM_SIZE` while in the loop (after `emitResize`) and from the timer when a frame is requested (the timer pushes the `EvFrame`). The D3D11 `Present(1, 0)` path needs no change. Cursors: handles loaded once in `Open`; `WM_SETCURSOR` with `HTCLIENT` sets the current one and returns TRUE, so Windows does not reset it; `SetCursor` also applies it at once while the mouse is tracked inside the client area.

### Measurements

All with the build of this step on the MacBook.

**Live resize through the real path** (`rexlook -live-resize-test`: `enterLiveResize`, 150 × `setContentSize:` that AppKit answers with `windowDidResize:`, `exitLiveResize`; repeated 7 times, same result each time):

| | |
|---|---|
| handler calls | 150 / 150 |
| frames drawn and presented in the handler | 150 / 150, 0 failures |
| allocations per frame, `BeginFrame` → `EndFrame` (exact `MemStats` delta, 150 frames) | **0 objects, 0 bytes** |
| time per handler call (event drain + `scene.build` + `scene.frame` + draw; includes two `ReadMemStats` calls) | 8.4 ms mean; the scene rebuild dominates, rexlook is rewritten on `ui` later |
| paced frames after the drag (CAMetalDisplayLink re-attached) | 20 / 20 |
| final size | matches the window (the surface was resized from each `EvResize`) |

The 150 steps arrive one per `setContentSize:` call, not at display rate, so this exercises the code path and its allocations, not the visual smoothness of a real drag.

**Startup unchanged.** Interleaved ABBA, 16 runs each of `rexlook -exit-first` against a build of the previous commit (`git archive HEAD`):

| | exec → on glass, median (min) | `MainToOpenMs` median |
|---|---|---|
| previous commit | 161.8 ms (130.6) | 119.8 ms |
| this step | 161.6 ms (140.6) | 123.0 ms |

Indistinguishable within the noise. (An earlier, non-alternating run showed ±40 ms swings in either direction, so only the ABBA numbers count.)

**Spike gate** (`spike -bench -idle 3s -gate -quiet`, previous commit vs this step): idle presents 0 and 0, paced intervals p50 8.33 / 8.34 ms and p99 8.91 / 8.82 ms, `FrameAllocObjects` 0 and 0, `FrameAllocBytes` 0 and 0, 0 GCs, frame CPU 1729 / 1508 µs. Both pass the gate.

**rexlook `-bench`:** 600 frames paced (p50 8.33 ms, p99 9.30 ms), 0 allocations, 0 bytes, 0 GCs, 633 µs main-thread CPU per frame.

### What still needs a human test

A real drag could not be driven from SSH: synthetic `CGEvent` mouse events are dropped without Accessibility permission, and `screencapture` is denied. Please check by hand on the MacBook with `rexlook`:
1. Drag each edge and corner slowly and fast. The content must grow and shrink in step with the frame: no stretched content, no old-size flash at the first or last frame, no black or stale strip at the new edges, and no trailing frame after the mouse is released.
2. Drag the top and left edges (the window origin moves with the size). With `presentsWithTransaction` the content should not jitter against the frame.
3. After the drag, the idle window must still present nothing, and an animated app (`rexlook -bench`, spike) must still be paced.
4. Hover the tab capsule and the focused pane's header buttons (pointing hand) and the two dividers (resize arrows). The cursor should change on the first mouse move, stay while the mouse moves, and return to the arrow elsewhere and when leaving the window.
5. Maximise (green button) and fullscreen: these are not live resizes, so they take the normal `Wait` path.

### Open issues

- **Live resize without a handler** is unchanged (no redraw until the drag ends).
- **Zoom/fullscreen animations** deliver `windowDidResize:` to a loop blocked in `nextEventMatchingMask` without waking it, so the app may see the `EvResize` only at the next input event. This predates this step and was not changed (an attempted wake could not be shown to help in a probe); it matters once `rexlook` runs animated fullscreen transitions.
- **Events pushed between two `Wait`s** (a synchronous AppKit callback from app code, e.g. `Show`) used to be dropped by the next `Wait`'s reset; `Wait` and `Pending` now keep them (`eventQueue.dropFirst`).
- **Width off by one point** in the scripted resize: the last step asked for 807 pt and the window reports 808 pt (AppKit rounding or a minimum content size); not investigated.
- **Windows and Linux:** cross-compiled and vetted only. `WM_SIZE` during the size/move loop draws at the new size, but the D3D flip-model swap chain's behaviour while dragging (flicker, DWM stretching the old buffer) needs a Windows machine.
- `DECISIONS.md` D5/D18 still say frames come from the metal link; the live-resize exception (view link plus `nextDrawable` during a drag) should be added there by whoever owns that file.

## Step 5: interactive rexlook on `app`; native cursor regions; titlebar drag map

**Test machine:** the Mac mini (M4, macOS 27.2), driven over SSH. Its display is asleep, so windows open but no frame events arrive: every bench below is *unpaced* (`Paced: false`, `-allow-unpaced`) and the intervals show the `nextDrawable` wait (8.3 ms), not a paced display. Synthetic mouse events cannot be posted (the Accessibility restriction noted above) and the app cannot become active, so cursor rects and window drags could not be exercised with a pointer; what could be verified without one is listed as such.

### What was built

| | |
|---|---|
| `rexlook` | one OS-independent `main.go` on `app`; the scene is `rex.Rex` (an `app.Builder` and `app.Initer`, `rex.Config()` = 1057×639 pt, titlebar 46, lights (21,22), min 600×400). Flags: `-exit-first`, `-bench [-gate] [-allow-unpaced] [-max-cpu-us]`, `-live-resize-test`, `-probe-titlebar` (macOS), `-o out.png`, `-v`. `rexshot` calls the same `rex.RenderShot`. Esc quits (the wrapper in `main.go`). |
| `ui` | `ui.Cursor` (+ `NextCursor`/`PushCursor`/`PopCursor`), `ui.WindowDrag` flag, `Context.CursorRegions`, `Context.ActiveCursor`, `Context.DragRegions`, `Context.PointerRegions` (both maps in one walk), `Context.Reserve` now also sizes the box pool |
| `core` | `Pool.Reserve(n)` |
| `platform` | `SetCursorRegions([]CursorRegion)`, `SetCursorOverride(Cursor)`, `SetDragRegions([]DragRegion)`; `SetCursor` is gone. Pure-Go `cursorMap`/`dragMap` in `regions.go` (diffing, hit tests, overlap flattening), tested on every OS |
| `app` | after every frame `syncRegions` hands ui's regions, the active box's cursor and the drag map to the window (only changes reach the OS); `App.SetCursor` removed (listbench marks its thumb with `NextCursor`); `RunLiveResizeTest` / `LiveResizeReport` (macOS) |

### Cursors

*Root causes found on the way.* (1) `+arrowCursor` and `+IBeamCursor` return **nil before `NSApplication` exists**, and the NSCursor singletons were cached in `registerClasses`, before it: the arrow and I-beam objects were 0 (the pointing hand and the resize cursors were fine). They are cached in `loadCursors` after `sharedApplication` now. (2) The only region was one bounds-sized rect swapped on each change, and rex's own test for which cursor applied was the old static scene's geometry, so the hit zones were the old guess, not the boxes.

*Design.* Boxes carry a cursor (`c.NextCursor(ui.CursorResizeEW)`). After `End`, `PointerRegions` walks the tree in draw order (a box, its children, floating ones last, like `emit`) and returns the boxes with a non-arrow cursor as pixel rectangles clipped by the ancestors' clips and the window. The active box's cursor (`ActiveCursor`) goes to the platform as the override. macOS gives AppKit one cursor rect per region, as `NSSplitView` does (view not flipped: y from the bottom edge), or, while an override is set, a single rect over the bounds; it rebuilds (discard, reset, invalidate) only when the list or the override changed, and not at all for region changes during a drag. Windows looks the shape up in `WM_SETCURSOR` at the pointer position (override, then the topmost region, then the arrow). Linux: no-op for now (Wayland `wp_cursor_shape_v1`, X11 `XDefineCursor` later).

*Overlap.* The ui list is in draw order, the last region is the topmost, and the platform's lookup (`cursorMap.at`) takes the last match. AppKit does not specify which of two overlapping cursor rects of one view wins, and the pointer-driven behaviour could not be observed here (the app never became active on this machine, and `CGEvent`/`CGWarpMouseCursorPosition` moved the pointer but the window was never key, so `currentSystemCursor` stayed the arrow in a probe). So AppKit is never given an overlap: `cursorMap.flatten` cuts each region down to what no later region covers (up to 4 pieces per cut, capped at 512 pieces) and the rects are added disjoint. A randomized test checks that for every point the flat list gives the same cursor as the topmost-wins rule; the divider-over-header case is a fixed example. Regions with no overlap pass through unchanged.

*rex.* Tabs, `+`, `⌘` and the pane header buttons (enabled ones) have the pointer; each divider has a grab box centred on its gutter, at least 13 pt across (the 7 pt gutter plus 3 pt each side; test requires ≥ 8 pt at scales 1, 2 and 3) and spanning the whole split. The active divider keeps its resize cursor over everything while dragged.

### Titlebar drag map

`rex` marks the 46 pt band with a `ui.WindowDrag` box under the tabs and buttons. `DragRegions` returns the WindowDrag rects and, in draw order, the input-taking boxes (Clickable/Hoverable/Focusable) over them as `Block` rects; a press is a window drag when the *last* rect containing it is not a block, so no rectangle subtraction is needed (Go: `dragMap.at`).

macOS: `GNUIView` answers `mouseDownCanMoveWindow` NO and swallows `mouseDown:` (a press nobody took used to fall through to the theme frame's titlebar handling and start a drag under the tab clicks). In the event pump, a left press that lands in the drag map **and** that AppKit's own hit test (`-[NSThemeFrame hitTest:]`) routes to our view calls `-[NSWindow performWindowDragWithEvent:]` (or, for `clickCount` ≥ 2, the double-click action) and is not delivered to the app. The hit-test clause keeps the traffic lights and the window's resize border working: they are not our boxes, so the map alone would have taken them. Double click reads `AppleActionOnDoubleClick` (`Minimize` → miniaturize, `None` → nothing, anything else including `Maximize`, `Fill` and unset → zoom; `Fill`'s tiling is not implemented). On this Mac the setting reads `Maximize`; a probe call of `zoom:` changed the window from 1057×639 to 2560×1410 pt.

*Hit-test evidence* (`rexlook -probe-titlebar`: real window, 8 frames, then asks `[themeFrame hitTest:]` and the drag map; 1057×639 pt window):

| point (pt) | AppKit hit view | our drag map says |
|---|---|---|
| empty band (90, 23), (195, 23), (877, 23), (600, 3), host text (130, 22) | `GNUIView` | drag |
| tab 1 (330, 23), tab 2 (600, 23), `+` (1034, 23), `⌘` (1000, 23) | `GNUIView` | no drag (the app's press) |
| close (21, 22), zoom (69, 22) | `_NSThemeCloseWidget`, `_NSThemeZoomWidget` | no drag (system buttons) |
| far right edge (1055, 6) | `NSThemeFrame` (resize border) | no drag |
| just below the band (195, 47), pane body (300, 300) | `GNUIView` | no drag |

So every point of the band that is ours hits our view, not the titlebar container. What could **not** be checked without a pointer: that `performWindowDragWithEvent:` actually moves the window and that the double click zooms (see the hand test).

Windows keeps the system titlebar (no-op). Wayland without server-side decorations moved the window from a fixed 32 px top strip, which would swallow the top of rex's tabs; with a drag map set the map replaces the strip (`Window.dragsAt`). X11 and the Wayland server-side-decoration case are unchanged.

### Measurements (Mac mini, unpaced)

| | |
|---|---|
| `rexlook -o` + `tools/rexcompare` | **40 / 40 pass**; the PNG is pixel-identical to the previous step's `rexshot` render |
| `rexlook -bench -gate -allow-unpaced` (1000 warm-up, 900 measured frames, one full cycle of the pointer script: hover across the tabs, three focus clicks, both dividers dragged back and forth) | **0 mallocs, 0 bytes, 0 GCs**; 722 boxes (max 791), 1695 quads (max 1830), 18 draw calls; intervals p50 8.40 ms, p99 9.6 ms; main-thread CPU **570–930 µs** per frame over seven runs (build 370–640, encode+present 160–240); the gate passes (≤ 1000) but the spread is large |
| `rexlook -live-resize-test` | 150 steps, 150 handler calls, **150 draws, 0 failures, 0 mallocs, 0 bytes over the whole drag** (the build, regions, encode and present all included); 7.1 ms mean per handler call (dominated by `nextDrawable`/present, as the scene build is ~0.1 ms); 20 / 20 paced frames afterwards |
| `rexlook -exit-first` | exec → on glass 108–145 ms (3 runs), first frame 25 ms |
| `ui` Begin+Build+End on the 2114×1278 scene (`go test -bench`, no GPU) | idle 124 µs, pointer script 88 µs, layout script 83 µs, live resize 111 µs, 0 allocs |
| pointer map after each frame (`PointerRegions`, 11 cursor + 9 drag regions) | 4.3 µs, 0 allocs |

About the CPU figure: a CPU profile of the bench puts the UI build and region walk at about 0.1 ms per frame; the rest of the thread time is Metal (`EndFrame` ≈ 0.38 ms per frame) and the bench's own `thread_info` reads (four per measured frame, ≈ 17 % of the samples). The metric is therefore pessimistic and varies with the CPU cluster the thread runs on.

`listbench` no longer needs the two warm-up jumps (frames 30 and 60) that grew the box pool before the measured window: `Context.Reserve` now sizes the pool (`core.Pool.Reserve`). With the jumps removed, `listbench -bench -gate` is 0 allocations in 3 of 3 runs; with `Pool.Reserve` commented out and the jumps still removed it allocates 3 objects / 4864 bytes in 2 of 2 runs.

### What the user must test by hand on the MacBook (display on, real mouse)

1. **Cursor zones.** Hover the dividers of the first tab: the vertical one between the two columns and the horizontal one between agent and git. The resize cursor must appear over a band about 13 pt wide centred on the divider (move across it slowly, not just onto the line), and the arrow must return outside it, including over the pane header buttons and at the window's edges and outside the window. Check the vertical divider where it passes the agent pane's header (the header's buttons and the divider do not overlap, but the grab band runs the whole height).
2. Hover each tab, `+`, `⌘` and the focused pane's four header buttons: pointing hand over the whole box; the arrow over the empty capsule gaps, the host block and the pane text. A disabled `+` (6 tabs open) shows the arrow.
3. **Drag a divider** and move the pointer far from it (over the tabs, out of the window): the resize cursor stays until release, then returns to what is under the pointer without moving the mouse.
4. **Titlebar.** Press-drag on empty band: left of the host block, between the host block and the capsule, right of the capsule, above the capsule's top edge. The window must move. Press-drag on a tab, `+`, `⌘`: the window must **not** move, and a plain click must switch the tab / open a tab. The traffic lights must still work, and so must the window's edges for resizing (the top edge and corners, which are close to the band).
5. **Double click** on empty band: with System Settings > Desktop & Dock > "Double-click a window's title bar to" set to Zoom (or Fill), the window zooms; set to Minimize, it minimizes; set to Do Nothing, nothing happens. Double click on a tab must only select it.
6. Press and release quickly on a tab while the window is in the background (first click activates the window): the tab must still be selected and the window must not start to move.
7. Live resize (drag any edge) and the zoom button: cursors and the drag map follow the new layout; the first mouse move after a resize must show the right cursor.
8. Esc quits; Cmd+Q quits.

### Open issues

- **AppKit overlap behaviour** was avoided, not observed (see above); the flattening is correct by construction and by test, but the cursor rects were never seen applied with a pointer on this machine.
- **`performWindowDragWithEvent:`** is called on mouse-down in the event pump, before `sendEvent:`. It is the documented way (Chromium, Electron, GLFW use it) but is unverified here; if the window does not move, or the matching mouse-up reaches the app as a stray release, that is the place to look (`platform_darwin.go`, `handle`).
- **`Fill`** (Sequoia window tiling) is treated as zoom.
- The bench's CPU instrumentation is itself a visible cost (above); `FrameCPUus`/`MainThreadCPUus` read about 100–300 µs high.
- **Wayland:** with server-side decorations (`decoMode == 2`) the app's drag map is not used; X11 and Windows ignore it. Cursors on Linux are still no-ops.
- A `DECISIONS.md` entry for the cursor/drag-map design (the region lists as the only cursor mechanism, `App.SetCursor` removed) should be added by whoever owns that file.

## Phase-3 results (MacBook M3 Pro, 2026-10-17)

### Interactions
You tested by hand and everything works:
- hover;
- focus dimming;
- tab switching;
- split side by side, split stacked, maximise, close and `+`;
- divider drags;
- cursors: the resize cursor across the whole divider band, the pointing hand over tabs and buttons, the arrow restored elsewhere, and the cursor held during a drag;
- the titlebar: dragging empty space moves the window, tabs don't, double-click follows the system setting, and the traffic lights and edges work;
- live resize, and Esc / ⌘Q.

### Startup
**BounceRec**, 6 launches through LaunchServices, Dock auto-hide temporarily off:
- complete content at 267–304 ms after the launch request;
- 0 partial frames, content-first in 6 of 6;
- half-bounce passes 6 of 6, with content usually before the icon even starts to bounce (~285 ms).

**`rexlook -exit-first`,** exec → on glass: 177–234 ms, median ~190 ms, under your load. Phase 2 was a median of ~189 ms, so this is unchanged (D21 budget: ≤ 150 ms; not met on this MacBook).

### Animation
Paced 120 Hz, exact allocation deltas. Runs were started with `open`, so the window was in front.

| | Frames | Allocations | Main-thread CPU per frame | Build | Encode | Interval p50 / p99 |
|---|---|---|---|---|---|---|
| `rexlook -bench` (pointer script: hover, focus, divider drag) | 900 | 0 / 0 B / 0 GC | 1.27–1.30 ms | ~0.7 ms | ~0.24 ms | 8.33 / ~9.1 ms |
| `listbench -bench` (1M rows; budget < 4 ms) | 600 | 0 / 0 B / 0 GC | 1.10–1.30 ms | 0.57–0.75 ms | ~0.2 ms | 8.33 / 8.6–9.9 ms |

- **`rexlook` is over the 1 ms main-thread target** from phase 2, which phase 3 kept as an aspiration. Phase 2's static frame took 0.53–0.6 ms.
- **The same scene builds in ~0.09 ms in a hot loop on the Mac mini.** The 7× gap on the MacBook is unexplained so far. A likely cause is low clocks for bursty 1 ms work every 8 ms, plus the bench's own CPU-clock reads. To profile.
- **Runs started over SSH** stalled twice, because the frame source stops when your windows cover the bench window (D18). Runs with the window in front never stalled (5 of 5).

### Idle
`rexlook` in the background, so no pointer input:
- 0.0 % CPU;
- 10 ms of CPU over 30 s;
- about 0.2 package-idle wakeups per second (`top` IDLEW).

**Footprint:**
- 50 MB, of which 21 MB is IOSurface (two full-size drawables), the same as phase 2.
- 87 MB was seen right after interaction, before the drawables are reclaimed.

### CI
- Windows (amd64 and arm64) renders `rexlook` and `listbench` offscreen; the PNGs are artifacts, reviewed by eye.
- The windowed `listbench` bench passes the allocation gate, paced on WARP.
- Linux passes with the patched go-text (D23), including the exact zero-allocation scroll tests.
