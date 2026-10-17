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

**Next step:** move frame delivery to `CAMetalDisplayLink` (macOS 14+). It
hands over the drawable with the tick, so the main thread never blocks in
`nextDrawable`, and `preferredFrameLatency` controls latency.

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
