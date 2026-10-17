# Phase 1 measurements (macOS) and go/no-go

Measured 2026-10-16 with `cmd/spike` (commit after `9e3e634`).

## Environment

| | |
|---|---|
| Machine | Mac mini M4 (`Mac16,10`), one 5K display at 2× |
| OS | macOS 27.2 |
| Toolchain | Go 1.27.1, `CGO_ENABLED=0`, `-trimpath -ldflags="-s -w"` |
| purego | `d456b1b` (pseudo-version `v0.12.0-alpha.1.0.20261015130536-d456b1bb95e8`) |
| **Display state** | **Display asleep (`CGDisplayIsAsleep` = 1) and session locked (`CGSSessionScreenIsLocked` = 1) for every run.** The agent can't wake or unlock it. |

### What the display state invalidates

AppKit reports the window's occlusion state as `0x2000` (not visible), so
`CADisplayLink` never fires. As a result:

- **No vsync-paced frames:** the bench waits 1 s for a display-link tick, then falls back to unpaced frames (`FramesPaced: false`, no frame intervals).
- **No `EvVisible`:** `ExecToVisibleMs` is -1, so "window visible with first frame" is not measured.
- **Startup is the in-app timeline only** (exec → first `present` returns). The external timer (`probes/startwin`) only proves the window is in the window-server list, not that pixels reached the screen.
- **Idle wakeups may differ** with a live display and compositor.

**Correction to phase 0.** The phase-0 "window on screen" numbers (171 ms,
139 ms with `CATransaction flush`) came from the same window-list check.
They measured window-server registration, not visible pixels. With the
display likely asleep then too, they should be re-measured.

## Commands

```sh
go build -trimpath -ldflags="-s -w" -o bin/spike ./cmd/spike
./bin/spike -exit-first -timeout 20s     # first launch pays the Gatekeeper scan; discarded
./bin/spike -exit-first -timeout 20s     # ×7, startup timeline
./bin/spike -bench -idle 60s -gate -timeout 150s
go test -run '^$' -bench . -benchmem ./internal/ffi
```

## Results

### Startup (7 warm runs, in-app timeline)

| Segment | Median | Range |
|---|---|---|
| exec → `main` | 13.9 ms | 7.9–15.3 |
| `main` → window open (`NSApplication`, `NSWindow`, view, order front) | 92.4 ms | 83.3–98.6 |
| exec → first present returned | **134.9 ms** | 118.9–140.4 |

Window open → Metal surface takes about 1 ms. AppKit setup dominates, as
in phase 0. Runs on other days of this session ranged 121–155 ms (load
average ~1.5–1.9).

### Idle (60 s; the window has presented once and nothing is animating)

| Metric | Value |
|---|---|
| Presents | **0** |
| CPU time | 4.2 ms over 60 s (~0.07 ms/s) |
| Package-idle wakeups (Activity Monitor "Idle Wake Ups") | 60 (1.0/s). `top -stats idlew` read 0.2/s in a separate 20 s window; the probe from phase 0 also read 0.2/s under the same conditions |
| Interrupt wakeups (all) | 85 (1.4/s) |
| `phys_footprint` at end of idle | 30.6 MB (other runs: 34–52 MB after 2–3 s idle, 38.6 MB after 30 s) |
| RSS | 87 MB (includes shared framework pages; not gated) |

The wakeup counter now separates package-idle wakeups from all interrupt
wakeups. Earlier runs in this session summed them, which produced
"4.3/s".

### Worker → loop wake (200 wakes via `postEvent:atStart:`)

p50 **88.6 µs**, p99 **201 µs** (other runs: p99 165–237 µs).

### Animation (480 measured frames after 120 warm-up; unpaced, see above)

| Metric | Value |
|---|---|
| Heap allocations | **0 objects, 0 bytes, 0 GC cycles** (exact `runtime/metrics` deltas) |
| Process CPU per frame | 0.61 ms (all threads; a clear plus present through Metal) |
| Frame intervals | not measured (no display link) |
| `phys_footprint` 1 s after animating | **101–105 MB** (see "Memory" below) |
| Go heap | 0.27 MB |

### Memory over time

Sampled with `footprint -p` every ~0.5 s during a bench run:

- **~84 MB** right after startup, falling to **~34 MB** within about 2 s of idling.
- **~101–105 MB** about 1 s after the unpaced animation, measured in-app.

The 84 MB startup state breaks down as:
- 19 MB "owned physical footprint (graphics)", 14 MB of it reclaimable;
- 9 MB IOSurface, about one 1920×1200 BGRA drawable;
- 11 MB Malloc Small;
- small framework data.

So the excess is graphics memory that the system reclaims after a short
idle. Our code does not allocate it.

The ~100 MB after animating comes from presenting unpaced into an occluded
layer. It needs re-measuring with a live display before it counts against
the budget.

### FFI (darwin/arm64, `internal/ffi` benchmarks)

| Path | Time | Allocs |
|---|---|---|
| Call, integer arguments | 20.5 ns | 0 |
| Call, mixed int/float | 21.1 ns | 0 |
| Callback (C → Go) | 56.6 ns | 0 |

The tests also pass for darwin/amd64 under Rosetta.

### Binary size (stripped `cmd/spike`)

| Target | Bytes | MiB |
|---|---:|---:|
| darwin/arm64 | 2,818,754 | 2.69 |
| darwin/amd64 | 2,948,240 | 2.81 |
| windows/amd64 | 2,877,952 | 2.74 |
| windows/arm64 | 2,666,496 | 2.54 |
| linux/amd64 | 2,961,671 | 2.82 |
| linux/arm64 | 2,883,847 | 2.75 |

## Windows and Linux

**Not measured.** There are no machines and nothing has been pushed to CI.

- **Builds and vet:** all four targets cross-compile with `CGO_ENABLED=0` and pass `go vet`.
- **Checked against headers and XML:**
  - Windows: COM vtable indices and GUIDs against the mingw-w64 headers;
  - Wayland: interface tables generated from the protocol XML, plus a layout and signature test (`internal/wl`, runs on Linux CI).
- **Runtime:** no window has been opened, no frame presented and no number taken on either OS.
- **CI:** `.github/workflows/ci.yml` runs the spike with `-bench -gate` on:
  - Windows (WARP);
  - Xvfb (X11);
  - headless Weston (Wayland).

  It is written but not pushed.

## Exit criteria (docs/PHASE1-PLAN.md)

| # | Criterion | Result |
|---|---|---|
| 1 | Exec → window visible with first frame ≤ 150 ms warm, external + in-app | **Partial.** In-app exec → first present: 134.9 ms median. "Visible" and the external measurement are **blocked** by the display state |
| 2 | Idle: CPU unchanged over 60 s, 0 presents, wakeups reported | **Pass with a note.** 0 presents. CPU grows 4.2 ms per 60 s, so not strictly unchanged; the source (AppKit vs Go runtime) is unattributed. Wakeups reported: 1.0/s package-idle |
| 3 | Wake latency p99 < 1 ms | **Pass:** 201 µs |
| 4 | Frame intervals track vsync; 0 objects / 0 bytes over warmed-up frames | **Allocation part passes:** 0/0 over 480 frames. Vsync tracking is **blocked** (no display link while occluded) |
| 5 | `phys_footprint` ≤ 40 MB after settling; peak reported | **Pass with a note:** 30.6 MB after a 60 s idle. Peaks: 84 MB at startup, ~100 MB after unpaced animation; both are reclaimable graphics memory, to re-check with a live display |
| 6 | Stripped binary ≤ 5 MB | **Pass:** 2.69 MB |
| 7 | DPI change resizes the drawable | **Not exercised.** `viewDidChangeBackingProperties` / `windowDidChangeBackingProperties:` are handled, but there is only one display (2×) and the spike has no `-scale` flag |
| — | Windows/Linux: cross-build, vet, header/XML checks | **Pass.** Runtime **unverified** |

### Plan deliverables not done

- **`tools/startwin`:** not promoted; the probe stays in `probes/startwin`. Its window-list check can't see visible pixels, so it needs rework alongside the live-display measurement.
- **`-scale` flag:** not added to the spike (criterion 7).

### Known gaps carried into phase 2 (all OSes)

- No redraw during live resize.
- Not implemented yet:
  - IME preedit (`NSTextInputClient` is registered but only commits text);
  - clipboard;
  - cursors;
  - Wayland client-side decorations.

## Go/no-go

**Recommendation: conditional GO for phase 2.**

Everything this machine can measure passes:
- zero allocations per frame;
- no presents while idle;
- sub-millisecond wake latency;
- startup inside budget;
- idle footprint inside budget;
- a 2.7 MB binary;
- a 20 ns zero-alloc FFI.

None of the blocked items points to a design problem; each needs a
condition we lack. Three must be cleared early in phase 2, before the
renderer depends on them:

1. **Live display:** unlock the Mac with the display awake, then re-run:
   - `-bench` for vsync intervals, `EvVisible` and startup to visible;
   - `probes/startwin` for the external timing;
   - the post-animation footprint.
2. **CI:** push to a private repo and run the workflow. This gives the first Windows and Linux runtime evidence:
   - window, present, idle and the zero-alloc gate;
   - the Windows `syscall` allocation counts left open in D3.
3. **DPI:** add `-scale`, or move the window to a 1× display, to exercise the scale-change path.

If (1) showed paced frames failing to track the display, or (2) showed a
backend unable to present, that would be a no-go for the affected backend
until it is fixed.
