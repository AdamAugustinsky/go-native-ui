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

### Design (cursors, macOS)

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
