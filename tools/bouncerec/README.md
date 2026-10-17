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
  -out /abs/path/result.json [-video /abs/path/frames-dir] [-dockvideo /abs/path/dock-dir] [-timeout 5]
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
  expected PNG), `-fps N`, `-v`, `-settle 1500` (ms of Dock stillness that ends
  the recording), the Dock thresholds (`-tile`, `-minrun`, `-mincols`,
  `-bouncetol`, `-commoncols`, `-resttop`, `-dockpad`, `-motionpx`) and the
  match thresholds below.

Launching follows the brief: capture starts, the first frame is the (continuously
refreshed) baseline, then `NSWorkspace openApplicationAtURL:configuration:` with
`activates = YES`, `createsNewApplicationInstance = YES`. The launch request is
stamped on the capture clock immediately before that call. The target is
terminated at the end unless `-keep`.

**When recording stops.** `-timeout` (default 5 s after the launch request) is the
maximum. If the Dock strip moved after the launch, recording ends earlier, as
soon as the strip has been still for `-settle` ms (1500). So a short app is
recorded for about (Dock motion + 1.5 s), and a Dock that keeps bouncing
extends the recording up to `-timeout` (then `bounce.settled` is false and the
numbers are unreliable: raise `-timeout`). The **last** frame is the Dock at
rest and is the reference for the bounce analysis.

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
scores 0. "Has pixels" means ≥ 50 % of sampled pixels in the window bounds differ
from the baseline by more than 12 per channel (`-firstfrac`, `-difftol`). A
window appearing changes most of its region; the first real run showed that a
2 % threshold counted unrelated screen activity (a system alert under the
window's future bounds) as the window's first pixels.

## Output (`-out`)

```
launchRequest {machNs, hostClockNs}   pid
tWindowListed  tFirstPixels  tComplete          ms after launch request (null = never)
contentFirst   true iff tFirstPixels == tComplete frame and partialFrames == 0
partialFrames, partialFrameTimes      frames with pixels but incomplete content, before tComplete
bounce        {available, unavailableReason, start, apex, apexDisplacementPt, end, periodMs, count,
               method, restTopMethod, restTopPt, restTopAssumedPt, iconColumnsPt, maxDisplacementPt,
               slideEndMs, slideFrames, settled, dockFrames, finalFrameMs}
halfBounce    {contentVsBounceStartMs, fractionOfFirstBounce = (tComplete-start)/(end-start),
               pass = tComplete <= apex   (null if no bounce was observed / Dock hidden)}
frames, framesAfterLaunch, framesInWindow [[t, matchedFraction, changedFraction]...]
dockSeries [[t, displacementPt]...]   screen {...}   clock {...}   params   notes
```

`-video dir` writes every captured frame inside the window bounds as PNG
`fNNNNN_t+MMMMM.MMms_{blank,partial,complete,later}.png`, to inspect partial
frames by eye.

`-dockvideo dir` writes the Dock strip of every frame from launch until the Dock
settled as 1× PNG `dNNNNN_t+MMMMM.MMms.png` (full width × Dock thickness + 40 pt,
`-dockpad`; screen edge at the bottom, rotated for a side Dock). Only that strip
is saved, never the rest of the screen (note it includes the 40 pt band above
the Dock, which can contain parts of other windows). Use it to check the
displacement curve by eye.

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
  `visibleFrame` vs `frame`; if the Dock is hidden/auto-hide the thickness is 0
  and `bounce.available` is false. On the Liquid-Glass Dock the bounce can stay
  *inside* that strip (peak below the boundary) and inserting the new icon
  re-lays out the whole centred Dock, so diffing against the pre-launch baseline
  is useless. The method (`bounce.method`):
  1. The Dock strip (Dock + `-dockpad` 40 pt above it, full width) is stored for
     every frame (about 0.7 MB each at 1512 pt; capped at about 1.5 GB).
  2. After the recording every frame is diffed against the **final, settled**
     frame. Per column, the topmost row of a run of ≥ `-minrun` (2) rows that
     differ by more than `-bouncetol` (16) is the column's top.
  3. **Common-mode rejection:** rows shared by ≥ `-commoncols` columns (default
     2 × icon width) are the Dock re-laying out (every icon sliding, the glass
     edge and highlight shifting), not the icon: those columns are re-scanned
     below that row, repeatedly. A single icon is narrower than that and is
     never peeled.
  4. Frames where more than 3 icon widths of columns changed are the insertion
     slide (`slideEndMs` is the last one). After it only the bouncing icon
     differs from the final frame, so its columns (`iconColumnsPt`) are the
     largest cluster of columns changed in those frames. (If there are no such
     frames the icon is the largest cluster rising more than 2 pt above the
     assumed rest: `excess-over-assumed-rest`.)
  5. **Rest top** (`restTopPt`, row index of the icons' resting top): the lowest
     icon top seen after the slide minus `-minrun`, because a lift is only
     visible once it spans `-minrun` rows. `restTopAssumedPt` is the same from
     the assumption of symmetric padding around the icons (`-tile` icon size,
     read from `com.apple.dock` `tilesize`); a note is added if they differ by
     more than 8 pt. Override with `-resttop N` (row index; e.g. 60 for a 74 pt
     Dock with 48 pt icons resting 13 pt below the boundary).
  6. `displacement(t)` = (top reached by ≥ `-mincols` (12) columns around the
     icon, ± half an icon because it slides too) − restTop, 0 at the final
     frame. The existing curve analysis then gives start/apex/end/period/count.
  * Limits: the icon top is hidden while another Dock-wide change covers its
    rows (the displacement can be off by about a point while the glass
    highlight overlaps the icon's peak); `start` (first displacement > 1 pt) is
    late by about `-minrun` rows of lift; the glass widening at the Dock's ends
    is a full-height change that is ignored by restricting to the icon's columns
    (it can fool the method only if the new icon sits at the very end of the Dock);
    a different bouncing icon, a notification banner or another window changing
    in the strip would read as bounce, so use a quiet desktop. Samples are
    interpolated only if neighbouring frames are < 40 ms apart; the apex is the
    middle of the plateau within 1 pt of the maximum (frames are change-driven, so
    a displacement can hold for tens of ms without a frame). A bounce ends at the
    first sample ≤ 1 pt after the apex, or at the valley minimum before the next
    bounce if the icon never reads as rest; two peaks are separate if the valley
    between is below 0.35 × the lower peak and each peak ≥ 4 pt; a series that
    starts above 1 pt and only decays is not a bounce.
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
shifted / colour-error frames), Dock strip copy and window-mask ranges for bottom,
left and right edges, the bounce-curve analysis (3 bounces, shallow valleys,
change-driven gaps, cut-off and decay-only series, no bounce) and the half-bounce
decision, a synthetic wide Dock (1512 pt, 23 icons: every icon slides half an icon
for 250 ms, the glass highlight changes along the whole Dock, one icon hops twice
below the Dock's visible boundary, change-driven frames; the slide alone must give
no bounce and start/apex/end must match the ground truth within 10 ms), plus a PNG
load + 2×→1× round trip. It
prints PASS/FAIL per section and exits non-zero on failure. It does not exercise
ScreenCaptureKit or the Dock; that needs a real run on the test Mac.
