# Phase 3 plan: UI core and the interactive Rex look

You approved phase 3 on 2026-10-17. The design comes from ARCHITECTURE §8,
DECISIONS D10, and the "Phase 3" section of
[design/rex-benchmark.md](design/rex-benchmark.md).

## Work, in order

1. **`core`.** Shared plumbing with no OS code:
   - a byte arena (per-frame scratch, double-buffered for previous-frame hit testing);
   - an open-addressing `uint64 → uint32` table that never allocates after warm-up;
   - an index pool with a free list and generation bits;
   - a bounded MPSC ring for worker → main-thread messages, paired with `platform.Wake`.

2. **`ui`, the immediate-mode core** (ARCHITECTURE §8). `ui` imports `gpu` (only the OS-independent `DrawList`/`Quad` types) and `text`, never `platform`.
   - **Boxes:**
     - `Box(key, flags)` and its helpers;
     - 64-bit keys hashed from the parent key and a label, with RAD's `##`/`###` rules;
     - a pointer-free `[]box` pool with a key table, pruned when a box isn't built;
     - style stacks (colours, corner radii, border, shadow, font, padding, gap, child axis, size per axis).
   - **Layout:** RAD-style passes on each axis:
     1. standalone sizes (pixels, text);
     2. parent percentage;
     3. children sum;
     4. violations resolved by strictness;
     5. positioning, including scroll offsets and floating boxes.
   - **Input:** a `ui.Input` struct the app fills from platform events. Hit testing uses the previous frame's rects. Signals are hovering, pressed, released, clicked, double-clicked, dragging (with delta), scrolled and keyboard-activated. Hot, active and focus are stored as keys.
   - **Animation:** RAD's `1 - 2^(-k·dt)` approach for hot/active/focus and app values (`AnimateTo(key, target)`). `Animating()` is what keeps the loop running.
   - **Scrolling:** a target plus exponential approach, with precise trackpad deltas used as-is. Clipping uses the shader clip, not scissors.
   - **Virtualized lists:** fixed-height rows; only `[first−overscan, last+overscan]` is built, in O(1).
   - **Drawing:** boxes emit `gpu.Quad`s in tree order (shadow, fill, border, text, children, overlay), and text goes through `text.System`.
     - `AtlasSync` uploads dirty atlas pages through a small uploader interface that `gpu.Renderer` satisfies on every OS.
   - **Zero allocations:** steady-state frames allocate 0 objects and 0 bytes: build, layout, hit testing, animation and `DrawList` emission. Exact `runtime.ReadMemStats` deltas; strings come from the frame arena.

3. **Platform additions.**
   - **Live resize** (macOS first):
     - AppKit runs its own loop while the user drags a window edge, so the app's `Wait` doesn't return;
     - the platform calls an app-registered frame function from inside that loop on each display-link tick;
     - the first and last resize frames are presented in step with the window;
     - Windows: the same handler is called from `WM_SIZE`/`WM_PAINT` during the modal size loop (cross-compiled).
   - **Cursors:** arrow, pointing hand, I-beam and the resize cursors (macOS `NSCursor`; Windows `SetCursor`; Linux cursor shape on Wayland/X11 later).
   - Clipboard and IME preedit move to phase 4, when text editing needs them.

4. **`rexlook`, interactive** (rex-benchmark "Phase 3"), rebuilt on `ui`:
   - hover states;
   - focus switching with animated dimming;
   - tab switching;
   - split side by side, split stacked, maximise and close;
   - live pane resize by dragging the dividers.

   The static frame must still pass `tools/rexcompare` 40/40.

5. **List benchmark:** `cmd/listbench` scrolls a 1M-row virtualized list, with synthetic scroll input in bench mode.

6. **Startup worker preparation** (carried over from phase 2; worth ~15 ms):
   - per-thread call contexts in `internal/objc`, so a worker can create the Metal device, renderer, text system and first scene while AppKit opens the window;
   - optional if the rest of the phase runs long.

## Exit criteria (macOS on the MacBook; Windows and Linux in CI)

1. **UI core correctness:** unit tests for keys/pruning, every layout pass, hit testing and signals, animation convergence, scrolling and virtual-list ranges.
2. **Zero allocations:** 0 objects and 0 bytes over ≥ 600 warmed frames for these scripted sequences, using exact deltas:
   - `rexlook` hovering, switching focus and dragging a divider;
   - `listbench` scrolling 1M rows.
3. **1M-row scroll:** < 4 ms main-thread CPU per frame (ARCHITECTURE §11), paced at the display rate.
4. **Interactive `rexlook`:**
   - every listed interaction works, which you confirm on the MacBook;
   - animated frames are paced;
   - idle presents nothing;
   - live resize redraws every frame without stretched or stale content.
5. **Startup unchanged:**
   - content-first and half-bounce still pass (BounceRec);
   - exec → on glass is no slower than phase 2 (D21).
6. **Windows and Linux:**
   - `ui`, `core`, `listbench` and the `rexlook` scene build everywhere;
   - CI renders `rexlook` offscreen on Windows and Linux (artifact PNGs for review by eye) and runs the `ui`/`core` tests.
