# A display link that stops calling back (macOS, CI): the evidence so far

Status (2026-10-20): not understood. A recovery is in place (below), and the
cause is to be chased. Written from F4's runs (branch `p6-f4`); the same
state looks like CI's most common flake, the spike gate's "frames were not
vsync-paced (no frame callbacks)", and the "bench stalled, drawable=0" stalls
on other branches.

## What it looks like

A window's `CAMetalDisplayLink` stops calling `metalDisplayLink:needsUpdate:`
for good, while the window waits for a frame. It is visible and running
(`paused` is 0 by our account and by the link's own), and the app's main
thread is alive. The pacer's fallback (app/pacer.go: draw without a frame
event after 1 s of silence) cannot draw: a layer that a metal link drives
gives no `nextDrawable`, so every attempt fails (`beginfail` climbs). Before
the recovery the window never drew again.

The two dumps, from `app.StateLine` and `platform.DebugState` (CI run
`39472466545`, job `118799787323`, macOS arm64; run `39467123874` failed the
same two tests the same way):

`TestWindowsPaceOnTheirOwn`, the first window, 1.8 s after it was asked to
animate:

    app{frames=7 needdraw=1 ready=1 paced=0 requested=0.0s-ago retry=11ms evframes=7 lastevframe=1.8s-ago lastdraw=1.8s-ago beginfail=15/consec=14 owed=A …}
    win{valid=1 occ=8194(visible) lastvis=visible@3.6s-ago key=0 active=1 mini=0 link=metal paused=0/0 armed=1@1.8s-ago drawable=0 live=0 relink=0 ticks=11 sinceframe=0 lasttick=1.8s-ago lastframe=1.8s-ago pauses=1 idle=0}

The other window had just animated for 1 s (60 frames, 96 ticks) and its
link was paused: `paused=1/1 ticks=96 pauses=2`.

`TestWorkerResultsReachTheirWindow`, the second window, after it had taken
3 of 5 results:

    app{frames=5 needdraw=1 ready=1 paced=0 requested=0.0s-ago retry=14ms evframes=6 lastevframe=4.9s-ago lastdraw=4.9s-ago beginfail=75/consec=73 owed=- …}
    win{valid=1 occ=8194(visible) lastvis=visible@5.4s-ago key=1 active=1 mini=0 link=metal paused=0/0 armed=1@4.9s-ago drawable=0 live=0 relink=0 ticks=11 sinceframe=0 lasttick=4.9s-ago lastframe=4.9s-ago pauses=1 idle=0}

How to read them:

- `link=metal paused=0/0 armed=1@Ns-ago`: the link drives the layer, is
  running, and a frame has been requested for N s.
- `lasttick` is as old as the request: no tick has come since.
- `drawable=0`: we hold no drawable of a tick, so the pool is not starved by
  us (that was the phase-2 stall, docs/measurements/phase2-macos.md).
- `occ=8194(visible) mini=0 active=1`: not covered, not in the Dock, and the
  app is active.
- `paced=0 beginfail` climbing: the fallback's attempts fail.
- `ticks = evframes + 4 idle ticks, pauses=1`: the link was paused once,
  after four idle ticks (linkIdleTicks). RequestFrame resumed it, it gave a
  few ticks, and then it stopped.

## When it happens

- **In the F4 tests, after a pause and a resume of the link.** In both dumps the link had been
  paused once (idle) and resumed. It ticked a few times after the resume,
  then stopped. A link resumed in the same test after its first pause worked
  (the other window's: `pauses=2`, 60 frames).
- **With a second window of the app.** At the moment of the stall, the other
  window had just drawn, or its link was paused, or both. Two windows
  animating together from the start never stalled (`TestTwoWindowsIdleAndAnimate`
  passed in every CI run).
- **Not occlusion.** Both windows are visible by the window server
  (`occlusionState` 8194), and the harness's covered-window evidence
  (`coveredSince`) found no visibility change.
- **CI only.** I could not make it happen on the MacBook (ProMotion,
  120 Hz): the two tests passed 8 runs out of 8, and 6 out of 6 with the
  link's rate range forced to 60 Hz. The CI runner is a macOS VM (paravirtual
  GPU, a virtual 60 Hz display).
- **The single-window spike** (`cmd/spike -bench -gate`, CI):
  - this branch: attempt 1 `FramesPaced:false PresentFailures:637`;
    attempt 2 `false`/189, with `FrameIntervalMs.Max` 1046 ms (one stall of
    about 1 s, then the restart below); attempt 3 passed;
  - about 20 recent runs of other branches: mostly `true` with 1–2 failures;
    p6-c6 `39454449193` was `false`/439, and p6-flakes was `true`/1830
    (`39475401244`) and `true`/63 (`39456711370`).

  So the stall happens with one window too. Two out of two on this branch
  may be chance, or something F4 changed.
- **The one-window benches, with no pause at all** (run `39507720375`, all
  green, which shows the restart log): `pilot -bench -rows 1000000`
  restarted twice (`ticks 163 pauses 0` and `ticks 574 pauses 0`), `notes
  -bench` and `imgbench -bench` once each (`ticks 1311` and `ticks 89`,
  `pauses 0`), all `windows open 1`. They pass with `-allow-unpaced`. So a
  pause and a resume are not needed, and the stall is common on CI. These
  benches run through the app package, so with F4 they use the shared device
  and queue (`gpu.Device`); the spike does not.

## Two leads

1. **The shared command queue.** With F4 every window's surface and the
   shared renderer commit to one `MTLCommandQueue` (`gpu.Device`). This
   includes the renderer's staged atlas blits, which commit ahead of the
   frame that needs them. A presented drawable stays in use until its
   command buffer completes and the compositor lets it go. With 2 drawables
   per layer (`setMaximumDrawableCount: 2`) and `preferredFrameLatency` 1, a
   link that has no free drawable does not call back. If one window's
   command buffers wait behind another's on the shared queue (in commit
   order, on a slow virtual GPU), its drawables come back late, or not
   before the link gives up.
   - Experiment: give each surface its own queue on the shared device. The
     renderer's blits then have to reach every window's queue, or be
     ordered with an `MTLSharedEvent`.
2. **The higher rate of spike stalls on this branch.** The spike has one
   window and its own device and queue (`gpu.NewSurface`), so lead 1 does not
   apply to it. What F4 changed on the one-window path:
   - `Wait` is the app-wide `Pump` plus `Events`, with `reap` of closed
     windows' objects;
   - window tabbing is off (`setTabbingMode:` and
     `allowsAutomaticWindowTabbing`);
   - the tracking area is released after `addTrackingArea:`;
   - `NativeLoop()` replaces the tick's own list.

   None of these touches the link or the drawables, as far as I can see.

## The recovery (platform/view_darwin.go)

`takeDrawable` → `linkStopped`: a frame armed for 1 s, the link running, no
tick since, and the window visible and not in the Dock. Then `restartLink`
replaces the metal link with a view `CADisplayLink`, as at startup. That
frame draws with the layer's `nextDrawable`, and the view link's first tick
attaches a new `CAMetalDisplayLink`. Each restart:

- counts in `DebugState.Restarts` (`restarts=N` in the state line);
- prints one line to stderr: `gnui: display link of window … stopped
  calling back …`.

`TestAStoppedDisplayLinkIsReplaced` stops a link behind the window's back
(`DebugStopLink`) and sees the window draw again on a new link. With the
restart, the two F4 tests above pass on CI. The spike still fails its gate
when a stall happens: it reports any 1 s silence as unpaced.

## What would tell more

- On CI: the presented handler (`addPresentedHandler:`) of every drawable.
  Do the drawables presented before the stall reach the glass, and when?
- Drawables in flight per layer at the stall (presented minus released).
- A run with `maximumDrawableCount` 3, one with per-surface queues, and one
  where the link is never paused: does any of them make the stall go away?
- The restart log lines in CI's output. In 4 failing runs of the live
  package (`39498360641`, `39500861219`, `39503358053`, `39505572655`, where
  `go test` shows the package's output), each run logged one unprompted
  restart: window 0 of an F4 test, after one pause (`armed 1002 ms ago, last
  tick 1002 ms ago, ticks 12 pauses 1 windows open 2`). The window-1 line
  next to it is `TestAStoppedDisplayLinkIsReplaced`'s own stop. Those runs
  failed in `TestEachWindowHasItsOwnComposer`, whose typing opened
  NSCorrectionPanel's nested loop. The restart may have come from that
  test, and from that loop. A passing package prints nothing, so the
  benches' lines above are the ones to watch.
