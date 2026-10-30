# Deterministic simulation tests

`app.NewSim` runs headless windows with the real text system, UI, event
translator, pacer, timeouts and frame arming. It replaces the native event
pump and surface. No visible window, key window, Accessibility permission or
Screen Recording permission is needed. `SimOptions.Render` also renders the
draw lists with the real offscreen renderer; the default builds them without
the pixel readback. Both modes use the real glyph atlas.
`FramePixels` borrows the latest rendered image; copy its pixels before
advancing to compare actual pictures across cache eviction or memory trims.

Create a builder as for `NewOffscreen`. Open more windows with
`sim.OpenWindow(cfg, widthPixels, heightPixels, scale, builder)`. They share
the renderer and text system, with separate UI contexts and pacers. Window
zero is the first window; integer window ids are never reused.

```go app/example_test.go
func ExampleSim() {
	s, err := app.NewSim(app.Config{}, 640, 400, 1, &editor{name: "Notes"}, app.SimOptions{Seed: 7})
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()
	if err = s.Advance(time.Second); err != nil {
		log.Fatal(err)
	}
	if err = s.Event(0, platform.Event{Kind: platform.EvVisible}); err != nil {
		log.Fatal(err)
	}
	if _, err = s.Idle(4*time.Hour, 0); err != nil {
		log.Fatal(err)
	}
	if err = s.Event(0, platform.Event{Kind: platform.EvVisible, Flags: platform.FlagDown}); err != nil {
		log.Fatal(err)
	}
	if err = s.Advance(time.Second); err != nil {
		log.Fatal(err)
	}
	log.Printf("replay %016x", s.TraceHash())
}
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

The clock is **process-wide and test-only**. The simulator also runs with
`GOMAXPROCS=1` from construction through close, restoring the previous value.
This keeps runtime timer heaps on the warmed P instead of letting scheduler
initialization on another P contaminate exact frame allocation counts.
Run simulations serially, on the
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
frame. Layout may use the previous frame's rectangles: audit findings have
at most two transitional frames to converge, only while a follow-up frame
is already owed. A static or persistent finding fails. Custom checks run
after every step, and failures include the seed,
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

The UI scenario suite in `app/simtests/ui` adds a million-row list with an
independent edit model, growing Markdown, composition/selection, expired AX
queries, fresh-build semantics, overlays, held input, cache eviction and
first-return input during box compaction. The Notes and Gallery builders
also cross hidden scale changes. Its default is one 24-hour seed replayed
twice; the focused lifecycle cases run once.

```sh
CGO_ENABLED=0 go test -short ./app/simtests/ui -count=1
GNUI_SIM_SEEDS=200 GNUI_SIM_HOURS=24 CGO_ENABLED=0 go test -short ./app/simtests/ui -count=1 -timeout=45m
GNUI_SIM_SEED=42 CGO_ENABLED=0 go test -short ./app/simtests/ui -run TestSimUILongReturn -count=1 -v
```

Pilot has an application-specific suite in `cmd/pilot/pilot/sim_test.go`.
It creates its synthetic filesystem service in Init, drives both panes on
the same folder, and checks source contents, filtered order, selection,
scroll anchors, focus, filter text/caret and bounded relists after each return.
Seeded changes include entry removal/rename/addition, `SynthSource.Bump`,
100k–1M-entry replacements, deletion and recreation, hidden scale changes
with queued prefetch, device rebuilding, and unchanged return hashes.
The default runs one 24-hour seed twice. Each replay checks exact warmed-frame
allocations, memory after a huge folder shrinks, and worker cleanup.

```sh
CGO_ENABLED=0 go test -short ./cmd/pilot/pilot -run '^TestPilotSim' -count=1 -v
GNUI_SIM_SEEDS=200 GNUI_SIM_HOURS=24 CGO_ENABLED=0 go test -short ./cmd/pilot/pilot -run '^TestPilotSimLifecycle$' -count=1 -failfast -timeout=30m -v
GNUI_SIM_SEED=42 CGO_ENABLED=0 go test -short ./cmd/pilot/pilot -run '^TestPilotSimLifecycle$' -count=1 -v
GNUI_SIM_SEEDS=200 CGO_ENABLED=0 go test -short ./fs -run '^TestSim' -count=1 -v
```

The fs suite separately injects watcher events into the real control worker,
advances across debounce deadlines in three-hour sleep-like jumps, and checks
30-second listing-pool/sorter expiry without UI wakes. Pilot's model work
uses fixed slices in simulation; production retains its 1 ms time budget.
Native app activation, watcher delivery, preferences and shell menus still
need native-platform testing. Icon style and sidebar discovery are pinned;
actual icon decoding and name-prefetch workers participate in the barrier.
