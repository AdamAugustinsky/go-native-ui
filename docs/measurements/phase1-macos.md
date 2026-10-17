# Phase 1 measurements (macOS) and go/no-go

Measured 2026-10-16 with `cmd/spike`.

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

- **No vsync-paced frames.** The bench waits 1 s for a display-link tick, then falls back to unpaced frames (`FramesPaced: false`, no frame intervals). These runs need `-allow-unpaced` to pass `-gate`.
- **No visibility.** `EvVisible` never arrives, so `ExecToVisibleMs` is -1: "window visible with first frame" is not measured.
- **In-app timeline only.** Startup is measured from exec until the first `present` returns. The external timer (`probes/startwin`) only proves the window is in the window-server list, not that pixels reached the screen.
- **Wakeups.** Idle wakeups may differ with a live display and compositor.

**Correction to phase 0.** The phase-0 "window on screen" numbers (171 ms,
139 ms with `CATransaction flush`) came from the same window-list check.
They measured window-server registration, not visible pixels.

### Correction: allocation measurement method

Phase 1 first asserted zero allocations with `runtime/metrics` deltas
(`/gc/heap/allocs:objects` and `:bytes`). **Those deltas are not exact.**
The runtime credits small allocations to those counters a whole span at a
time, when a per-P cache refills. Allocations that land in an
already-credited span don't show up until a flush.

A standalone check made 600 eight-byte allocations after `runtime.GC()`:

| Method | Reported |
|---|---|
| `runtime/metrics` | **0 objects / 0 bytes** |
| `runtime.ReadMemStats` | 600 mallocs / 4,800 bytes (it flushes the caches first) |

The spike and the ffi tests now use `ReadMemStats` (`Mallocs`, `TotalAlloc`,
`NumGC`) deltas.

**Negative control.** A build with one deliberate small allocation per
frame reports 601 objects / 17,856 bytes and fails `-gate`. The fixed build
reports 0 / 0.

Every zero-allocation result below uses the exact method.

## Commands

```sh
go build -trimpath -ldflags="-s -w" -o bin/spike ./cmd/spike
./bin/spike -exit-first -timeout 20s     # first launch pays the Gatekeeper scan; discarded
./bin/spike -exit-first -timeout 20s     # ×7, startup timeline
./bin/spike -bench -idle 60s -gate -allow-unpaced -timeout 150s
go test -run '^$' -bench . -benchmem ./internal/ffi
```

## Results

### Startup (7 warm runs, in-app timeline)

| Segment | Median | Range |
|---|---|---|
| exec → `main` | 13.9 ms | 7.9–15.3 |
| `main` → window open (`NSApplication`, `NSWindow`, view, order front) | 92.4 ms | 83.3–98.6 |
| exec → first present returned | **134.9 ms** | 118.9–140.4 |

- **Where the time goes:** window open → Metal surface takes about 1 ms. AppKit setup on the main thread dominates, as in phase 0.
- **Variance:** single bench runs during the session ranged 121–155 ms (load average ~1.5–1.9).
- **Not content:** the spike only clears the window to a colour.
- **Not the user-visible span:** it starts at exec, not at the Dock click, and stops at the present call, not at pixels on screen (see "Half-bounce" below).

### Idle (60 s; the window has presented once and nothing is animating)

| Metric | Value |
|---|---|
| Presents | **0** (every run) |
| CPU time | 3.9–4.2 ms per 60 s (~0.07 ms/s) |
| Package-idle wakeups (Activity Monitor "Idle Wake Ups") | 10–60 per 60 s across runs (0.17–1.0/s) |
| Interrupt wakeups (all) | 28–85 per 60 s |
| `phys_footprint` at end of idle | 30.6–30.8 MB after 60 s; 34–52 MB after 2–3 s; 38.6 MB after 30 s |
| RSS | 87 MB (includes shared framework pages; not gated) |

`top -stats idlew` read 0.2/s over a separate 20 s window, as did the
phase-0 window probe under the same conditions. The wakeup counter now
separates package-idle wakeups from all interrupt wakeups. Earlier runs in
this session summed the two, which produced "4.3/s".

### Worker → loop wake (200 wakes via `postEvent:atStart:`, one in flight at a time)

p50 **83–89 µs**; p99 **152–340 µs** across runs. The p99 is the
second-largest of 200 samples, so it is noisy.

### Animation (120 warm-up + 600 measured frames; unpaced, see above)

| Metric | Value |
|---|---|
| Heap allocations | **0 objects, 0 bytes, 0 GC cycles** (exact `ReadMemStats` deltas) |
| Wait path | Unpaced frames use a finite 1 ns `Wait` deadline, the `NSDate` path that paced frames take, so the gate covers it. The review found that path allocated on every frame (`objc.Cls` per call); fixed, and caught by the negative control above |
| Process CPU per frame | 0.61 ms (all threads; a clear plus present through Metal) |
| Frame intervals | not measured (no display link) |
| `phys_footprint` 1 s after animating | **101–105 MB** (see "Memory" below) |
| Go heap | 0.25 MB |

### Memory over time

Sampled with `footprint -p` every ~0.5 s during a bench run:

- **~84 MB** right after startup, falling to **~34 MB** within about 2 s of idling.
- **~101–105 MB** about 1 s after the unpaced animation, measured in-app.

A 52 MB snapshot taken about 2.5 s into a run breaks down as:
- 19 MB "owned physical footprint (graphics)", 14 MB of it reclaimable;
- 9 MB IOSurface, about one 1920×1200 BGRA drawable;
- 11 MB Malloc Small;
- small framework data.

The transient excess is reclaimable graphics memory. The ~100 MB after
animating comes from presenting unpaced into an occluded layer; it needs
re-measuring with a live display before it counts against the budget.

### FFI (darwin/arm64, `internal/ffi`)

| Path | Time | Allocs |
|---|---|---|
| Call, integer arguments | 20.5 ns | 0 |
| Call, mixed int/float | 21.1 ns | 0 |
| Callback (C → Go) | 56.6 ns | 0 |

- **Source:** `-benchmem`, which uses `MemStats` and is exact.
- **`TestZeroAlloc`:** 6,000 calls + 2,000 callbacks with exact deltas. It passes on darwin/arm64 and on darwin/amd64 under Rosetta.
- **`TestAggregateSpill`:** pins how a by-value struct spills to the stack once float registers run out:
  - AAPCS64 closes the remaining float registers (NSRN = 8);
  - SysV leaves them open.

### Binary size (stripped `cmd/spike`)

| Target | Bytes | MiB |
|---|---:|---:|
| darwin/arm64 | 2,765,954 | 2.64 |
| darwin/amd64 | 2,887,648 | 2.75 |
| windows/amd64 | 2,853,376 | 2.72 |
| windows/arm64 | 2,642,432 | 2.52 |
| linux/amd64 | 2,896,135 | 2.76 |
| linux/arm64 | 2,818,311 | 2.69 |

## Live-display results: MacBook Pro (added 2026-10-16)

**Setup.** This session runs on the Mac mini. The spike is cross-built
there and run over SSH (Tailscale) on a MacBook Pro 14" (M3 Pro,
`Mac15,6`, macOS 27.2, built-in ProMotion XDR display). A process started
over SSH opens its window in the logged-in GUI session.

```sh
GNUI_TEST_HOST=user@host tools/remote-run.sh -bench -idle 60s -gate -quiet
```

**Conditions.** The machine was on battery (54%, discharging) and you
were actively using it, so the numbers are noisy and pessimistic. The
first launch of each newly copied binary pays the XProtect scan
(326 ms / 421 ms visible) and is excluded.

### Startup (7 warm runs)

| Segment | Median | Range |
|---|---|---|
| exec → `main` | 16.9 ms | 12.6–29.1 |
| `main` → window open | 98.2 ms | 84.7–155.4 |
| exec → first present returned | **203.2 ms** | 159.9–273.2 |
| exec → first present **and** occlusion-visible | **214.0 ms** | 161.5–302.0 |

**Over the 150 ms budget.** Open → first present takes ~60–90 ms on this
machine. On the Mac mini, with its display asleep, the same step took
~30 ms; surface creation is about 1 ms on both. The missing time sits in
the first frame's present path (`nextDrawable`, commit, or the first
`CATransaction` flush). Breaking that step down is the first half-bounce
task.

### Strict bench (`-gate`, passes)

| Metric | Value |
|---|---|
| Frames | 600, **paced by `CADisplayLink`** |
| Heap allocations | **0 objects / 0 bytes / 0 GC** (exact) |
| Frame interval | p50 16.17 ms, mean 14.26 ms, p99 23.2 ms, max 25.7 ms |
| Process CPU per frame | **2.20 ms** (all threads) |
| Idle 60 s | **0 presents**; 54.6 ms CPU; 1 package-idle wakeup, 109 interrupt wakeups |
| Wake latency | p50 157 µs, **p99 1.02 ms**, max 1.57 ms |
| Footprint, idle | 29.6 MB |
| Footprint, animating / 1 s after | **134–141 MB** |

### Findings

1. **Pacing is not steady.** A mean (14.3 ms) below the p50 (16.2 ms) means the intervals mix ~8 ms and ~16 ms ticks on the 120 Hz ProMotion panel, with misses up to 25 ms.
   - Suspects: the link is paused after every tick and unpaused by `RequestFrame`, and no `preferredFrameRateRange` is set.
2. **CPU per frame is high for a clear:** 2.2 ms, against 0.6 ms unpaced on the mini. Not yet attributed. Candidates:
   - per-frame link pause/unpause;
   - Go runtime thread wakeups around each callback;
   - CoreAnimation work.
3. **Graphics memory while animating:** 88 MB of "owned physical footprint (graphics)", none of it reclaimable, plus 18 MB of IOSurface (2 drawables).
   - It is ~126 MB at startup, drops to 32 MB when idle, and returns to ~134 MB while animating.
   - The occluded-layer explanation from the Mac mini runs does not apply here.
   - Next step: compare with a minimal native Metal clear app on the same machine.
4. **Wake latency p99 just over 1 ms** on battery under user load. The target is < 1 ms.

Criterion 4's vsync part now has data, and the data says "not yet". The
other blocked criteria still are: visibility timing is in-app only, and
DPI is not exercised.

## Windows and Linux

**Not measured.** There are no machines and nothing has been pushed to CI.

- **Builds and vet:** all four targets cross-compile with `CGO_ENABLED=0` and pass `go vet`.
- **Checked against headers and XML:**
  - Windows: COM vtable indices and GUIDs against the mingw-w64 headers;
  - Wayland: interface tables generated from the protocol XML, plus a layout and signature test (`internal/wl`, runs on Linux CI).
- **Runtime:** no window has been opened, no frame presented and no number taken on either OS.
- **CI:** `.github/workflows/ci.yml` runs `spike -bench -gate` (paced frames required) on:
  - Windows (WARP);
  - Xvfb (X11);
  - headless Weston (Wayland).

  It is written but not pushed.
- **X11 caveat:** "paced" means swap-interval pacing, and Xvfb does not throttle swaps.

## Independent review (review-sol) and fixes

**Fixed and re-verified on macOS where possible:**
- **Wake thread:** the macOS worker wake now stays on one OS thread from autorelease-pool push to pop.
- **Wait allocation:** `Wait` caches the `NSDate` class; it allocated on every finite-deadline wait.
- **Early wake:** a `Wake` before `Open` is now delivered.
- **amd64 IME rect:** `firstRectForCharacterRange:` has its own IMP. Its hidden struct-return pointer shifts `self`/`_cmd`, so the shared dispatcher never matched it.
- **Struct arguments:** `ArgPoint`/`ArgRect` follow the AAPCS64 and SysV rules for aggregates that no longer fit in registers (new test).
- **Spike bench:**
  - failed presents stay owed and back off 50 ms;
  - worker wakes are acknowledged one at a time (coalescing could hang the bench);
  - the measured count is now 600 frames after warm-up;
  - "visible" requires the first present too;
  - `-gate` requires paced frames unless `-allow-unpaced`;
  - allocation deltas use `ReadMemStats`.
- **Linux, cross-compiled only:**
  - X11 no longer starves worker wakes while animating;
  - Wayland honours a configure that fixes only one dimension;
  - EGL rejects configs that don't match the X11 visual, so the GLES fallback runs;
  - X11 text uses `xkb_keysym_to_utf8` when libxkbcommon is present, so non-Latin-1 layouts produce text.
- **Windows, cross-compiled only:** letters and digits get their `Key` from the scancode (physical position), matching the platform contract and macOS/Linux.

**Deferred and recorded:** `Close` doesn't release AppKit objects. Phase 1
assumes one window per process, and `Close` is followed by exit.

## Exit criteria (docs/PHASE1-PLAN.md)

| # | Criterion | Result |
|---|---|---|
| 1 | Exec → window visible with first frame ≤ 150 ms warm, external + in-app | **Not met as written.** In-app exec → first present: 134.9 ms median. "Visible" and the external measurement are **blocked** by the display state |
| 2 | Idle: CPU unchanged over 60 s, 0 presents, wakeups reported | **Deviation.** 0 presents and wakeups reported, but CPU grows ~4 ms per 60 s. The source (AppKit vs the Go runtime) is unattributed |
| 3 | Wake latency p99 < 1 ms | **Pass:** 152–340 µs |
| 4 | Frame intervals track vsync; 0 objects / 0 bytes over 600 warmed-up frames | **Allocation part passes** (exact, with a negative control). Vsync tracking is **blocked** |
| 5 | `phys_footprint` ≤ 40 MB after settling; peak reported | **Pass with a note:** 30.6–30.8 MB after 60 s. Peaks: 84 MB at startup, ~100 MB after unpaced animation; both are reclaimable graphics memory, to re-check with a live display |
| 6 | Stripped binary ≤ 5 MB | **Pass:** 2.64 MB |
| 7 | DPI change resizes the drawable | **Not exercised.** The scale-change notifications are handled, but there is one 2× display and no `-scale` flag |
| — | Windows/Linux: cross-build, vet, header/XML checks | **Pass.** Runtime **unverified** |

## Half-bounce startup goal (added 2026-10-16)

The user set **half-bounce on macOS, or better** as a headline goal, with
equivalents on Windows and Linux.

**Definition.** The term comes from Mitchell Hashimoto's posts (August–September
2026). The app's window must be on screen *with its real content* and usable
before the Dock icon finishes the first half of its first launch bounce.

It is a perceptual, unofficial metric, analysed from screen recordings
(e.g. [Bounce Lab](https://bounce-lab.miho.dev/), which compares icon
motion with the first render). Sources disagree on whether the cut-off is
the apex or the landing ([zenn.dev summary](https://zenn.dev/ocat/articles/fd84fe810de02e)).

**How we read it:**
- the clock starts at the Dock click or `open`;
- it stops at the first frame on screen that shows complete content;
- a blank or placeholder frame followed by content doesn't count.

**What phase 1 does and doesn't show.** It is not a half-bounce measurement:
- it starts at exec (LaunchServices/launchd time not included);
- it stops when `present` returns, not at pixels on screen;
- it draws a clear colour, not content;
- the display was asleep.

The one relevant finding: AppKit's main-thread setup (~92 ms) is the floor
of the critical path. Everything else the first frame needs must be ready
by the time AppKit is.

**The duration of one Dock bounce on this Mac has not been measured.** It
must be measured from a recording before we can express a result as a
fraction of a bounce.

The plan (ARCHITECTURE §3 "Content-first startup", §11) is carried into
phase 2:
1. **A `.app` bundle and an external launch harness.**
   - Launch with `open`.
   - Record the screen (ScreenCaptureKit, ≥ 60 fps) and find the first frame with complete content.
   - From the same recording, measure the Dock bounce period.
   - In-app, add `MTLDrawable` presented-time feedback for when the frame actually hit the display.
2. **Never show an empty window.** Order the window front only together with its first complete frame.
3. **Parallel critical path.** While AppKit initialises on the main thread, workers read the initial directory, load fonts, shape and rasterise the glyphs of the visible rows, and build the Metal pipeline.

## Go/no-go

**The phase-1 exit gate is not fully met.**

Criteria 1, 4 (vsync) and 7 are blocked by conditions we lack, not by
failures. Criterion 2 has a small deviation.

What this machine could measure supports the design:
- exact zero allocations per frame, with a negative control;
- no presents while idle;
- sub-millisecond wake latency;
- an in-app startup of 135 ms to first present, with AppKit as the floor;
- an idle footprint of 31 MB;
- a 2.6 MB binary;
- a 20 ns zero-alloc FFI.

**Recommendation: proceed to phase 2 only if you waive criteria 1, 4
(vsync part), 7 and the CPU deviation in 2 for now.** These would then be
cleared as the first phase-2 work, before the renderer depends on them:

1. **Live display:** with the Mac unlocked and the display awake, re-run:
   - `-bench` (strict gate), for vsync intervals and visibility;
   - the half-bounce harness above, which replaces criterion 1's external timing;
   - the post-animation footprint.
2. **CI:** push to a private repo and run the workflow. This gives the first Windows and Linux runtime evidence, including the strict paced gate.
3. **DPI:** add `-scale`, or move the window to a 1× display.
4. **Idle CPU:** attribute the ~4 ms per 60 s (sample the idle process; compare with the bare phase-0 probe).

If (1) showed paced frames failing to track the display, or (2) showed a
backend unable to present, that backend would be a no-go until it is fixed.
