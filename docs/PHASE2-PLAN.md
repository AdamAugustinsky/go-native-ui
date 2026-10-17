# Phase 2 plan: renderer, text, content-first startup, static Rex look

**Status: proposed, awaiting approval.** Starting phase 2 requires your
waiver of the open phase-1 criteria (see
[measurements/phase1-macos.md](measurements/phase1-macos.md)).

**Test machine:** the MacBook Pro (M3 Pro, ProMotion), driven over SSH from
the Mac mini with `tools/remote-run.sh`. Windows and Linux are
cross-compiled only until CI is approved.

## Work, in order

1. **Clear the phase-1 findings on the MacBook.**
   1. **Slow first frame:** break down the ~60–90 ms between `Open` and the first present (`nextDrawable`, commit, first `CATransaction`).
   2. **Pacing:**
      - stop pausing `CADisplayLink` after every tick (pause only after N idle ticks);
      - set `preferredFrameRateRange`;
      - gate on steady 120 Hz / 60 Hz intervals.
   3. **CPU per frame:** attribute the 2.2 ms (`sample`, link churn, Go runtime wakeups).
   4. **Graphics memory:** find the 88 MB while animating by comparing with a minimal native Metal clear app (swiftc from the Command Line Tools).
   5. **Remaining items:**
      - wake p99 (target < 1 ms);
      - add the `-scale` flag and exercise the DPI path;
      - attribute the idle CPU.
2. **Half-bounce harness.**
   - `tools/bundle`: a minimal `.app`.
   - Launch it with `open`, record the screen and Dock (ScreenCaptureKit or `screencapture -v`) and detect the first complete frame. Measure the Dock bounce period from the same recording.
   - Add `MTLDrawable` presented-time feedback in-app.
   - *Needs you once:* grant Screen Recording permission on the MacBook to whatever process records.
3. **Content-first window** (ARCHITECTURE §3).
   - Create the window ordered out and render the first frame.
   - Then: `presentsWithTransaction` → commit → `waitUntilScheduled` → `[drawable present]` and `orderFront` in one `CATransaction`.
   - Verify from recordings that no empty frame ever reaches the screen.
4. **Renderer (`gpu`).**
   - The instanced SDF quad pipeline from §6, plus the visual-bar additions: gradients, fade masks, hairlines, layered shadows.
   - Glyph and image atlases and the per-frame instance ring.
   - Metal first. D3D11 and GL implemented and cross-compiled, marked runtime-unverified.
5. **Text (`text`).**
   - macOS: CoreText shaping and rasterisation, run cache, font fallback.
   - Windows: DirectWrite. Linux: go-text plus fontconfig (D9 B). Both cross-compiled only.
6. **`cmd/rexlook`, static.**
   - Render the Rex reference layout ([design/rex-benchmark.md](design/rex-benchmark.md)), with system symbols for icons.
   - Add the comparison tool: window capture, per-region ΔE and geometry checks against the reference.

## Exit criteria (macOS, MacBook)

1. **Visual match:** a static `rexlook` frame matches the benchmark spec (flat-region ΔE ≤ 3, geometry ±1 px at 2×, text metrics ±0.5 pt), confirmed by your side-by-side review.
2. **Startup:** `rexlook` launched with `open` shows its complete first frame with no empty or partial frame on screen. Report time-to-content and the fraction of one Dock bounce; the target is ≤ half a bounce.
3. **Animation:** frame intervals track the display (120 Hz on ProMotion) with no misses at p99. 0 heap objects and bytes over 600 warmed-up frames (exact deltas). Report main-thread and process CPU per frame against a budget of ≤ 1 ms main-thread CPU for the `rexlook` frame.
4. **Idle:** 0 presents over 60 s.
5. **Memory:** idle footprint ≤ 40 MB. Footprint while animating is explained, and is no worse than a native Metal app doing the same.
6. **Windows and Linux:** renderer and text backends cross-compile and vet clean. Runtime unverified unless CI is approved.
