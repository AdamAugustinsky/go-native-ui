# BounceRec

Dev/measurement tool for the macOS half-bounce goal (ARCHITECTURE §3
"Content-first startup", PHASE2-PLAN step 2). It records the main display with
ScreenCaptureKit while it launches an app through LaunchServices, and reports:

1. when the app's window first shows **any** pixels (`tFirstPixels`),
2. when it first shows its **complete expected content** (`tComplete`),
3. the timing of the Dock icon's **first bounce** (`bounce.start/apex/end`),

and from those: whether complete content was on screen before the first bounce
was half done (`halfBounce.pass`, i.e. `tComplete <= apex`) and whether any
empty or partial window frame reached the screen (`partialFrames`,
`contentFirst`). All times are ms relative to the launch request.

It is written in Objective-C (like `probes/nativebaseline`), not framework code.

## Build

```sh
bash tools/bouncerec/build.sh      # -> tools/bouncerec/bin/BounceRec.app (clang -Wall -Wextra, macOS 14+)
tools/bouncerec/bin/BounceRec.app/Contents/MacOS/BounceRec -selftest   # pure-logic tests, no screen needed
```

`tools/bouncerec/bin/` is build output (the repo's `.gitignore` only ignores
the top-level `/bin/`).

The app is ad-hoc signed with the stable identifier `dev.gonativeui.bouncerec`
and has `LSUIElement` set, so it has no Dock icon of its own that could bounce
into the measurement.

**TCC caveat.** For an ad-hoc signature macOS ties the Screen Recording grant to
the code-directory hash as well as the bundle id. Rebuilding changes the hash,
so the grant may be reset (the toggle can look enabled but capture still fails;
remove BounceRec with "−" and add/enable it again). To keep a grant across
rebuilds sign with a real identity:
`SIGN_IDENTITY="Apple Development: Name (TEAMID)" bash tools/bouncerec/build.sh`.
Build once on the test Mac, grant once, and avoid rebuilding between runs.

## Grant Screen Recording (once, on the test Mac, at the keyboard)

System Settings → Privacy & Security → **Screen & System Audio Recording** →
enable **BounceRec**. The first run without the grant writes
`{"error": "screen recording permission needed"}` to `-out`, calls
`CGRequestScreenCaptureAccess()` (which adds BounceRec to the list / shows the
prompt) and exits non-zero. Re-run after enabling it (macOS may ask to quit and
reopen the app; that is fine, each run is a fresh `open`).

## Run (from SSH)

BounceRec must run inside the GUI session so it can capture the screen, hence
`open`. Over SSH direct processes cannot capture. The display must be awake and
the session unlocked.

```sh
open -W -n --stdout /tmp/bouncerec.log --stderr /tmp/bouncerec.err \
  tools/bouncerec/bin/BounceRec.app --args \
  -target /abs/path/Target.app -expect /abs/path/expected.png \
  -out /abs/path/result.json [-video /abs/path/frames-dir] [-timeout 5]
cat /abs/path/result.json
```

* Use **absolute paths**: a process started by `open` has `/` as its working directory.
* `open -W` returns when BounceRec exits but does not forward its exit status;
  check `result.json` (`"error"` key) instead. The JSON is also printed to the
  `--stdout` file.
* `-n` starts a fresh BounceRec instance for every run.
* Target: any `.app`; `tools/mkapp.sh <binary> <out.app> <bundle-id> [name]`
  wraps a bare executable into a minimal ad-hoc signed bundle.
* Other flags: `-keep` (do not quit the target afterwards), `-warm 500` (ms to
  wait after the first frame before launching), `-expectscale 2` (scale of the
  expected PNG), `-fps N`, `-v`, and the thresholds below.

Launching follows the brief: capture starts, the first frame is the (continuously
refreshed) baseline, then `NSWorkspace openApplicationAtURL:configuration:` with
`activates = YES`, `createsNewApplicationInstance = YES`. The launch request is
stamped on the capture clock immediately before that call. The target is
terminated at the end unless `-keep`. Recording stops `-timeout` seconds after
the launch request regardless.

## Expected PNG

`-expect` is the app's complete content rendered offscreen at **2×**
(window content size × 2, **excluding** the title bar). Our apps will offer
`-o path.png` for this. BounceRec box-downsamples it to 1×, places it centred
horizontally and **bottom-aligned** inside the window bounds from the window list
(the title bar sits above it; use `-offset x,y` to override), and compares a
grid of probe points (every 8 pt) against the captured pixels. Skipped:
top-left 90×46 pt (traffic lights), a 2 pt border band, a 24 pt square at each
window corner (rounded corners; `-corner`). A frame is *complete* when ≥ 98 %
of probes match (`-match`) within a per-channel tolerance of 24 (`-tol`); a
probe also matches if any pixel within ±1 pt does (`-slack`, absorbs sub-point
window origins and downsampling differences). Render the expected PNG in sRGB;
the stream is captured in sRGB too.

A window smaller than the expected content (still animating to its final size)
scores 0. "Has pixels" means ≥ 2 % of sampled pixels in the window bounds differ
from the baseline by more than 12 per channel (`-firstfrac`, `-difftol`).

## Output (`-out`)

```
launchRequest {machNs, hostClockNs}   pid
tWindowListed  tFirstPixels  tComplete          ms after launch request (null = never)
contentFirst   true iff tFirstPixels == tComplete frame and partialFrames == 0
partialFrames, partialFrameTimes      frames with pixels but incomplete content, before tComplete
bounce        {available, unavailableReason, start, apex, apexDisplacementPt, end, periodMs, count,
               iconColumnsPt, maxDisplacementPt}
halfBounce    {contentVsBounceStartMs, fractionOfFirstBounce = (tComplete-start)/(end-start),
               pass = tComplete <= apex   (null if no bounce was observed / Dock hidden)}
frames, framesAfterLaunch, framesInWindow [[t, matchedFraction, changedFraction]...]
dockSeries [[t, displacementPt]...]   screen {...}   clock {...}   params   notes
```

`-video dir` writes every captured frame inside the window bounds as PNG
`fNNNNN_t+MMMMM.MMms_{blank,partial,complete,later}.png`, to inspect partial
frames by eye.

## Heuristics and limits (read before trusting a number)

* **Frames are change-driven.** ScreenCaptureKit delivers a frame only when the
  screen changed. `tFirstPixels`/`tComplete` are the first frames that *show* the
  state; the true compose time is between the previous and that frame. Frame
  times are the sample buffer presentation timestamp (host-time clock, same
  timeline as `mach_absolute_time`). `clock.clockCheckMs` (now − first-frame
  timestamp) must be small and `clock.displayTimeMinusPtsMs` near 0; if not,
  the clock assumption is wrong.
* **Capture rate.** Frames are limited to the display's max rate (≤ 120 Hz,
  `-fps`), so timing resolution is 8.3 ms on ProMotion and 16.7 ms at 60 Hz.
  The capture is 1× (point-sized) to stay cheap; if frames are dropped (queue
  depth 8) a transient state can be missed, which is why the window list is
  consulted for every frame and `-video` is available for double checking.
* **Window list.** The window appears in `CGWindowListCopyWindowInfo`
  (on-screen, layer 0, target pid, largest window) when ordered in. If pixels
  were already on glass in one of the 3 frames before it was listed, they are
  recovered (note in `notes`). Bounds exclude the shadow, so the shadow never
  counts as pixels.
* **Window animations.** A fade or scale-in open animation shows as partial
  frames (correct for the goal). Set `animationBehavior = none` in apps under
  test or the check cannot pass.
* **Bounce measurement.** The Dock edge and thickness come from `NSScreen`
  `visibleFrame` vs `frame`. If the Dock is hidden/auto-hide the thickness is 0
  and `bounce.available` is false. Otherwise the area above the Dock
  (`-extra` × thickness, default 1.6×) is diffed against the baseline. For each
  column the topmost changed row is found; the icon's displacement is the height
  that at least 12 columns (`-mincols`) reach, relative to the Dock's top edge.
  That ignores thin spikes. The target window plus 48 pt shadow margin
  (`-shadow`) is masked out. The icon column range at maximum displacement is
  reported (`iconColumnsPt`) to check it is really the target icon.
  * The rest position of the icon is inside the Dock, so the first few points
    of lift are invisible: `start` (first displacement > 1 pt above the Dock top)
    is biased late by a few ms. `apex` and `end` are not. `-restgap N` also
    scans N rows inside the Dock below its top edge (try 4–6) at the risk of
    reading the glass shimmer as motion.
  * The Dock may slide icons to make room for the new one; this changes pixels
    inside the Dock but is only counted above its top edge, and is rejected if
    narrower than `-mincols`.
  * Another app's window changing in the area above the Dock, a notification
    banner, or a different bouncing icon (e.g. apps already starting) will read
    as bounce. Run on a quiet desktop.
  * Samples are interpolated (threshold crossings, parabolic apex refinement)
    only if neighbouring frames are < 40 ms apart; across longer gaps the time
    of the frame that showed the change is used. A bounce ends at the first sample
    ≤ 1 pt after the apex, or at the valley minimum before the next bounce if the
    icon never reads as rest. Two peaks are separate bounces if the valley between
    is below 0.35 × the lower peak and each peak ≥ 4 pt.
  * `count` and `periodMs` (mean apex-to-apex) depend on the Dock continuing to
    bounce while recording. The Dock stops bouncing when the app finishes
    launching, so a fast app can produce 0 or 1 bounce; then `halfBounce.pass` is
    null (no bounce to compare against) while `tComplete` is still reported.
* **Colours.** The expected PNG must be rendered the way the window shows it
  (same colour space, no transparency, same scale/theme). Wide-gamut or
  vibrancy/translucent content may need a larger `-tol`.
* **Multiple displays.** Only the menu-bar (main) display is captured. The
  target window must be on it.
* **Numbers are per machine.** Bounce duration, display rate and launch time
  depend on the Mac, macOS version, Dock size/magnification and system load.
  Report them with the machine, and repeat runs (discard the first after a
  build: cold file cache).
* BounceRec itself costs a little CPU (frame diffing on a utility queue) and
  macOS shows a screen-recording indicator while it runs.

## Self-test

`BounceRec -selftest` runs the probe matching (empty / partial / complete /
shifted / colour-error frames), Dock displacement on a synthetic Dock for bottom,
left and right edges (including the spike and window-mask rules), the bounce-curve
analysis (3 bounces, shallow valleys, change-driven gaps, cut-off series, no
bounce) and the half-bounce decision, plus a PNG load + 2×→1× round trip. It
prints PASS/FAIL per section and exits non-zero on failure. It does not exercise
ScreenCaptureKit or the Dock; that needs a real run on the test Mac.
