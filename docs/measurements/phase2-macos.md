# Phase 2 measurements (macOS)

**Test machine:** MacBook Pro 14" (M3 Pro, ProMotion 120 Hz, macOS 27.2).
Driven over SSH with `tools/remote-run.sh`. It was on battery while you
were using it, so treat absolute numbers as noisy and pessimistic.

**Native baseline:** `probes/nativebaseline/main.m`, a minimal Objective-C
AppKit + Metal app doing exactly what the spike does, measured the same
way and run interleaved with the spike.

## Step 1: phase-1 findings

### Startup

**In-app instrumentation:**
- `platform.OpenTimeline()`: the duration of each `Open` stage.
- `gpu` first-present trace: acquire, encode + commit, flush.
- **On-glass time:** when the first frame reached the display.
  - It comes from an `MTLDrawable` presented handler, a hand-built global Objective-C block calling an ffi callback.
  - `presentedTime` reads 0 on this machine, so the handler's own call time is used. Metal calls the handler "immediately after the drawable is presented".

**Finding 1: the first event pump cost a frame and a half.** The first
`Wait` runs AppKit's deferred launch work and took 50–100 ms with no event
to deliver. The spike used to pump events before drawing, so the first
frame waited for all of it. It now draws first.

| exec → first frame on glass | median of 5 (range) |
|---|---|
| pump first (old order, `-wait-first`) | 246 ms (212–250) |
| **draw first (new default)** | **159 ms (138–182)** |

**Finding 2: what remains is AppKit, and we match native.** Stage
breakdown with draw-first, from 6 runs:

| Stage | Spike | Native Objective-C |
|---|---|---|
| exec → `main` | 16–22 ms | 6–24 ms (the pre-main cost is dyld/exec, not Go: Go's own init is 6.5 ms per `GODEBUG=inittrace`) |
| `NSApplication` shared + activation policy | 41–52 ms | 34–60 ms |
| `NSWindow` init | 37–45 ms | 33–39 ms |
| our view, delegate, tracking area, event | 2–5 ms | 2 ms |
| `finishLaunching` | 12–19 ms | 12–17 ms |
| order front + activate | 4–18 ms | 8–14 ms |
| first frame (acquire + commit + flush) | ~5 ms | 15–21 ms |
| **exec → on glass** | **median 152 ms** (147–194, interleaved) | **median 149 ms** (106–165) |

**What this means for half-bounce.** The Go stack adds nothing measurable.
Most of the ~150 ms is AppKit's own `NSApplication` and `NSWindow` setup
(~85 ms), which every AppKit app pays. Getting further below needs:
- launch measured from the Dock, not exec;
- work that overlaps AppKit setup;
- possibly prewarming what AppKit loads lazily.

The bounce period is still unmeasured; the half-bounce recorder is step 2.

### Pacing

**Before:** the display link was paused after every tick and re-armed by
`RequestFrame`. On ProMotion, intervals mixed ~8 ms and ~16 ms (mean
14.3 ms, p50 16.2 ms).

**Now:**
- the link keeps running through gaps of up to 4 ticks before pausing (idle still pauses it);
- it asks for the screen's full rate through `preferredFrameRateRange` (`ffi.ArgFloat3` passes the three-float struct).

| 4 runs, 600 frames each | p50 | mean | p99 | max |
|---|---|---|---|---|
| after | 8.33 ms | 8.33–8.39 ms | 8.5–9.5 ms | 8.8–34 ms (one hitch in one run) |

That is a steady 120 Hz. One mixed run happened during the first
measurement, before both changes were in.

### CPU per frame

The cost per frame is mostly the platform's.

| Clear + present at 120 Hz, process CPU per frame | |
|---|---|
| Native Objective-C (view `CADisplayLink`, render in the callback) | 1.70–1.79 ms |
| Spike | 1.89–2.09 ms |

**Where it goes.** Per-thread CPU (`ps -M`) puts ~6 % on the main thread
and ~5 % on each of three system threads at priority 37: Core Animation,
Metal and FramePacing.

**Our overhead** is ~0.2 ms per frame: the event pump and the app-defined
event posted on each tick.

**Main thread blocked in `nextDrawable`.** `sample` (with a
`get-task-allow` re-signed binary) shows the main thread spending ~96 % of
each frame waiting there.
- With 2 drawables and the window server compositing, the next drawable is free only about one frame later.
- Raising to 3 drawables doesn't change CPU or intervals.
- Blocking costs input responsiveness, not CPU.

**Done: frames now come from `CAMetalDisplayLink`** (macOS 14+,
`preferredFrameLatency` 1). It hands over the drawable with the tick, so
the main thread never waits in `nextDrawable`.
- **Constraint:** a layer driven by the link throws on `nextDrawable`. The draw-first frame is presented before the run loop first spins, so the platform swaps the view's `CADisplayLink` for the metal link on the view link's first tick, which always comes after the first present.
- **Contract:** after that, `Present` outside a frame tick returns false, so apps draw on `EvFrame`.
- **Occluded window or sleeping display:** the link stops ticking and nothing is drawn. That is correct for an app; a bench run in that state times out instead of falling back to unpaced frames.

| 9 runs, 600 frames each | CPU per frame | intervals |
|---|---|---|
| View `CADisplayLink` + `nextDrawable` (before) | 1.89–2.09 ms | p50 8.33 ms |
| Native Objective-C, same old approach | 1.70–1.79 ms | 8.33 ms mean |
| **`CAMetalDisplayLink` (now)** | **1.24–1.48 ms** | p50 8.33 ms, p99 8.6–8.7 ms, max ≤ 11 ms |

- **Drawables:** 2 vs 3 drawables and latency 1 vs 2 made no measurable difference, so we keep 2 drawables and latency 1.
- **Cost:** the footprint just after animating rose to ~161 MB, from 134–143 MB. The link seems to keep about two extra full-size drawables (9.2 MB each at this size). Idle settles at the same 33 MB.

### Graphics memory

The ~88 MB of graphics memory while animating is the platform's.

| `phys_footprint` | Spike | Native Objective-C |
|---|---|---|
| Just after the first frame | ~126 MB | 121 MB |
| Just after animating | 134–143 MB | 130–131 MB |
| Idle, settled | 33 MB | — |

The transient graphics allocation is the same for a plain AppKit + Metal
app on this machine at this window size (1920 × 1200 px). Our overhead is
~5–10 MB. The system reclaims it after a short idle.

### Windows (CI, WARP)

The spike runs on Windows amd64 and arm64 runners.

| Metric | Result |
|---|---|
| exec → first present | 56–70 ms |
| Footprint | ~14.5 MB |
| Idle | 0 presents |
| Pacing | paced frames |

**Fixed:**
- **Allocation gate:** it failed on 2 objects / 56 bytes. `-allocsites` traced them to the measurement itself: `syscall.GetCurrentProcess`'s lazy DLL lookup in `sysinfo.CPUNs`. Fixed by using the pseudo-handle.
- **Clock:** the runtime's Windows `nanotime` only advances once per timer tick, so wake latencies read exactly 0. `platform.Now` on Windows now uses `QueryPerformanceCounter`, and wake latency reads p50 48 µs, p99 126 µs.

### Linux (CI, Mesa llvmpipe)

The spike passes the strict gate on amd64 and arm64, on both X11 (Xvfb)
and Wayland (headless Weston). This is the first runtime evidence for the
Linux backends and for the Linux arm64 ffi ABI.

## Steps 2–6 (2026-10-17)

### Display-link stall (fixed)

**Symptom:** an intermittent stall of several seconds. It hit about 2 of 3 MacBook runs at first, and once in CI on macOS.

**Cause:** a frame tick's drawable was kept until it was used, possibly through a whole idle period. With 2 drawables in the pool, the `CAMetalDisplayLink` had nothing left to hand out and stopped ticking.

**Fixes:**
- A tick's drawable now lives one tick.
- `BeginFrame` with no drawable requests the next frame.
- A fresh `EvFrame` cancels the spike's 50 ms retry timer.

**Result:** 11 of 11 MacBook runs paced afterwards; CI is green.

### Content-first startup and half-bounce (BounceRec)

`tools/bouncerec` records the screen with ScreenCaptureKit while it starts the app through LaunchServices. It checks every captured window frame against the app's offscreen render, and it measures the Dock icon's bounce from a strip of the Dock.

On macOS 27 the launch bounce is subtle: the icon grows in, rises about 14 pt and settles. It was verified by eye from the recorded strips. Dock auto-hide was turned off for these runs and then restored.

All times are milliseconds after the launch request:

| App (5 runs each) | complete content | first bounce start / apex / end | partial frames | half-bounce |
|---|---|---|---|---|
| spike | 281–324 | ~265 / ~455 / ~760 | 0 (content-first in every run) | 5 of 5 pass |
| rexlook | 281–336 | ~270 / ~455 / ~760 | 0 (content-first in every run) | 5 of 5 pass, about 8 % into the first bounce |

The window never reaches the screen without its content. Complete content
arrives about 150 ms before the first bounce's apex.

**Exec → on glass** (in-app, `rexlook -exit-first`):

| Machine | Result |
|---|---|
| MacBook, under your load | 129–212 ms, median ~189 |
| Mac mini | 94–97 ms |

On the MacBook:
- AppKit setup takes 98–162 ms.
- Our work after `Open` takes ~35–50 ms: scene and text 13 ms; the first frame 18–34 ms, mostly the window server commit when the window appears.
- A trivial native AppKit app takes a median 149 ms on the same machine.

So the ≤150 ms budget is met on the Mac mini but not on the MacBook.
- **Next lever:** building the scene and text on a worker while AppKit sets up the window (ARCHITECTURE §3 rule 2). It is worth about 15 ms.
- **Prerequisite:** per-thread call contexts in `internal/objc`, because today it has one shared call frame.

### rexlook (MacBook, 120 Hz)

**Animation:** 600 paced frames re-rendering the full scene (1791 quads, 13 draw calls), 3 runs.

| Metric | Result |
|---|---|
| Frame interval | p50 8.33 ms, p99 8.60–8.68 ms |
| Allocations | 0 objects, 0 bytes, 0 GCs |
| Main-thread CPU | 0.59–0.61 ms per frame (budget ≤ 1 ms) |
| Process CPU | 1.64 ms per frame |

**Idle:**
- CPU is 0.0 %.
- Footprint is 37–51 MB. It depends on whether the system still holds one full-size drawable: IOSurface is 11 MB, and each drawable is 10.8 MB at 2114 × 1278.
- The empty spike's 60 s idle: 0 presents, 0 package-idle wakeups, 29 ms of CPU in total, mostly in the first second, and 32 MB.

**Visual match:**
- `tools/rexcompare` passes 40 of 40 checks against the reference: 11 colour regions, 24 edge probes, and text metrics. Region ΔE00 medians are 0–2.2, and the worst p95 is 3.6.
- The terminal cell measures 7.5 pt in the reference (15.00 px period), not 7.6 pt.
- The largest visible difference is glyph weight: the reference's text is a little bolder.

### Windows and Linux (CI)

Both backends now run their tests in CI, against the same pixel and zero-allocation tests as Metal:

| OS | Renderer | Text | Runners |
|---|---|---|---|
| Windows | D3D11 on WARP | DirectWrite, with fallback (Yu Gothic UI for CJK) | amd64 and arm64 |
| Linux | GL 3.3 on Mesa llvmpipe, surfaceless EGL | go-text + fontconfig | amd64 and arm64 |

All 8 CI jobs pass.
