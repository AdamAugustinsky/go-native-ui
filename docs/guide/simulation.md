# Deterministic simulation tests

`app.NewSim` runs headless windows with the real text system, UI, event
translator, pacer, timeouts and frame arming. It replaces the native event
pump and surface. No visible window, key window, Accessibility permission or
Screen Recording permission is needed. `SimOptions.Render` also renders the
draw lists with the real offscreen renderer; the default builds them without
the pixel readback. Both modes use the real glyph atlas.

Create a builder as for `NewOffscreen`. Open more windows with
`sim.OpenWindow(cfg, widthPixels, heightPixels, scale, builder)`. They share
the renderer and text system, with separate UI contexts and pacers. Window
zero is the first window; integer window ids are never reused.

```go
s, err := app.NewSim(cfg, 900, 700, 1, builder, app.SimOptions{Seed: 42})
if err != nil { t.Fatal(err) }
defer s.Close()
must := func(err error) { t.Helper(); if err != nil { t.Fatal(err) } }
must(s.Advance(time.Second))
must(s.Event(0, platform.Event{Kind: platform.EvVisible}))
_, err = s.Idle(4*time.Hour, 0) // no frame is allowed during this interval
must(err)
must(s.Event(0, platform.Event{Kind: platform.EvVisible, Flags: platform.FlagDown}))
must(s.Event(0, platform.Event{Kind: platform.EvFocus, Flags: platform.FlagDown}))
must(s.Advance(time.Second))
t.Logf("trace %016x", s.TraceHash())
```

`Event` uses normal `platform.Event` fields, including physical resize size
and scale. Pass text as its optional third argument, for example
`s.Event(0, platform.Event{Kind: platform.EvText}, []byte("hello"))`.
`Clipboard` sets isolated clipboard text; UI copy and paste use it.
`Appearance`, `DeviceLost` and `AXQuery` inject those changes without native
clients. AX queries use the real tree/diff engine and the native bridge's
30-second expiry rule. Panels start shelved and build nothing until shown.

Seed the scenario's input generator with `SimOptions.Seed` (for example
`rand.New(rand.NewPCG(seed, seed))`). The simulator uses stable window, timer
and worker order; it does not invent input or random worker schedules.
`Advance` jumps to the next timeout, timer or requested display tick, waits
for owned workers, delivers their coalesced wakes and draws only when the
pacer says to draw. An idle hour without a deadline is one clock jump.

The clock is **process-wide and test-only**. Run simulations serially, on the
creating goroutine; no `t.Parallel`, native event pump, live service from a
previous run, or unrelated goroutine may share one. Construct services in
the builder's `Init` after the clock is installed. The fs listing/control
workers, text prefetch workers, image/task workers and Markdown highlighters
are gated automatically. Between turns they run to a parked state in stable
order. A full result queue is parked, not mistaken for a finished queue: the
UI drains its results and wakes its producer. `WorkerTimeout` bounds each
barrier in **real time**, so stuck native work fails rather than advancing
the simulated clock. Panics in gated jobs become replay failures.

Implement `SimClose()` on a builder to close its services when its window
closes. In particular, give Markdown an explicit `MarkdownView` and call its
`Close`; the default global view is not owned by a window. Close an image
cache, prefetcher and filesystem service before releasing the renderer.
`fs.NewServiceWith` supplies synthetic directory contents without a live OS
watcher. Real files, watchers, network responses, native dialogs/hosts,
audio/video capture and arbitrary app goroutines are not replay fixtures.

Each frame records a hash of every quad/backdrop and the semantic tree, plus
its causes. Events and injections are recorded too. `Trace` returns the
records; `TraceHash` excludes real-time measurements and heap observations.
Replay is comparable on the same host and font/OS environment; native text
versions and external data can change the result.

The default `SimCheck`s require a clean layout audit and a cause for every
frame. Custom checks run after every step, and failures include the seed,
step, virtual time and recent trace. Use these checkpoints as well:

- `CheckAllocations(true)` enables exact per-frame `runtime.ReadMemStats`
  deltas after warming; turn it off for changes that grow or trim storage.
  Trace, hashing, worker and scenario allocations are outside this interval.
  D11 explains why runtime/metrics allocation counters are not exact enough.
- `Idle(duration, allowedCauses)` returns frame counts and rates by cause.
  A static scene normally allows none; a VList cleanup allows `SimDeadline`.
- `Memory` performs a GC and reads `/gc/heap/live:bytes`, goroutine count,
  box, text-run and GPU instance capacities. Builders can implement
  `SimRetained` for listing pools and VList buffers. `CheckMemory` enforces
  explicit scenario bounds. Run-cache budgets do not shrink on elapsed time;
  box/GPU trims require 60 calm uses, whereas pool/VList expiry uses 30 seconds.
- `SimReturnFrame(window, hash)` compares a settled return with a captured
  draw hash. Install it only across an unchanged scene with matching focus,
  selection, dimensions and appearance. `CheckGoroutines` checks the count
  after `Close`, allowing a bounded real wait for worker returns.

The clock seam is `core.Now`, `WallNow`, `Since` and `AfterFunc`.
Production uses the original monotonic clocks (QPC on Windows), `time.Now`
and runtime timers. Native I/O waits, shutdown/watchdog bounds and calendar
timestamps remain real time. VList rebuilding uses fixed work slices under
simulation; terminal input uses one chunk per frame, since a paused virtual
clock cannot measure CPU budget consumption. This explores lifecycle and
state correctness, not native timing, thread races, GPU loss notifications or
energy use. `DeviceLost` tests real renderer rebuilding and cache recovery,
not an OS driver reset.

Run the smoke test (4 simulated hours, two identical replays):

```sh
CGO_ENABLED=0 go test -short ./app -run '^TestSimFourHours$' -count=1 -v
GNUI_SIM_SEED=42 CGO_ENABLED=0 go test -short ./app -run '^TestSimFourHours$' -count=1 -v
GNUI_SIM_HOURS=72 GNUI_SIM_SEEDS=10 CGO_ENABLED=0 go test -short ./app -run '^TestSimFourHours$' -count=1 -v
```

The fixture combines a TextField, 10,000-row VList, Markdown code highlighting,
RichText links, prefetching, image decoding and a large synthetic directory.
It exercises burst input, seconds-to-hours idle, hide/show, focus, appearance
and resize while hidden, expired AX activation, device rebuilding, retained
memory, allocation gates, exact return frames, worker cleanup and replay.
