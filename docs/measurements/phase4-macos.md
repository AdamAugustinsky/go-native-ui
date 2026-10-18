# Phase 4 measurements (Pilot, fs, text editing)

**Machines:**
- **MacBook Pro 14"** (M3 Pro, ProMotion 120 Hz, macOS 27.2), driven over SSH. Live-window runs need its lid open, the screen unlocked and nothing else frontmost: a window that opens behind another app gets no frame events, and the bench reports `UNPACED`.
- **Mac mini** (M4, display asleep): offscreen and file-system runs.
- **CI:** GitHub runners (windows-2025, windows-11-arm, ubuntu-24.04 amd64 and arm64, macos arm64).

Text editing has its own document: [phase4-text.md](phase4-text.md).

## Exit criteria

| # | Criterion | Result | Where |
|---|---|---|---|
| 1 | 100k entries listed + sorted on a worker in ≤ 150 ms | **132 ms** (list 107 ms) | MacBook, `fsbench` |
| | Windows / Linux, reported | Windows 64–65 ms (amd64/arm64); Linux 198–401 ms (runner disks) | CI `TestBig100k` |
| | Frames paced while listing | **paced at 120 Hz, 0 allocations** on real folders with watching on: `/usr/bin` 1.46 ms/frame, `~/Documents` 1.55–1.60 ms | MacBook, `pilot -bench -bench-dir` (b040421) |
| 2 | ≤ 64 B per entry incl. name | **58 B** on the MacBook's long names (47.7 B in the synthetic CI set) | MacBook, CI |
| | 1M entries scroll < 4 ms main-thread CPU/frame, 0 allocations | **1.40–1.48 ms** paced at 120 Hz, frame-CPU p99 2.5–2.6 ms, **0 allocations** (exact gate, first window) | MacBook (a48b3e9) |
| 3 | Steady-state frames allocate 0 | **0** on every CI job (best whole-session window), 100k and 1M; `-bench-dir` 0 | CI 7352d81, 41edb06; Mac mini |
| 4 | Watched changes appear in ≤ 250 ms | macOS ~1 ms raw, ~52 ms refreshed listing (debounce); Windows and Linux pass the 250 ms tests | Mac mini; CI |
| 5 | Content-first, start listings in the first frame | **9 of 9** runs, incl. `/usr/bin` and `/Applications`; listings ready 23–34 ms after exec | MacBook (b07d6a8) |
| | exec → on glass no slower than rexlook (~190 ms) | **161–184 ms**, median ~169 ms | MacBook (b07d6a8) |
| | Half-bounce (BounceRec) | **6 of 6**, content-first 6 of 6 (0 partial frames): complete content 260–342 ms after the launch request, bounce apex 424–445 ms | MacBook (b040421) |
| 6 | Idle: 0 presents, ≤ 1 wakeup/s while watching | **0.0 % CPU, 0 idle wakeups, 0 CPU time** over 60 s with both panes on `~` and watching it (kqueue, D26; FSEvents was 20–45 wakeups/s) | MacBook (a48b3e9) |
| 7 | Your hand test | pending | MacBook |
| 8 | Windows/Linux: fs tests, offscreen Pilot, text-field tests | **pass** on all 8 jobs | CI 7352d81, 41edb06 |

Binary size: `pilot` 3.8 MB (darwin/arm64, stripped). Idle memory footprint 73 MB with two panes on `~`; most of it is the window's three 2400×1520 drawables (~44 MB). The ≤ 40 MB budget is the empty demo's.

## File system (fs)

### 100k entries

| Machine | List | List + sort (name) | Sort only | Filter | B/entry |
|---|---|---|---|---|---|
| MacBook, `fsbench` (names 34 B avg) | 107 ms | 132 ms | 26 ms | 2.8 ms (query "a", all match) | 58 |
| Mac mini, `TestBig100k` (names 19.7 B) | 80–87 ms | 100–105 ms | 20 ms | 1.3 ms ("family", 20k match) | 47.7 |
| Windows amd64, CI | 59 ms | 65 ms | 7 ms | ~0 ms | 47.7 |
| Windows arm64, CI | 61 ms | 64 ms | 5 ms | 1.5 ms | 47.7 |
| Linux amd64, CI | 252–319 ms | 281–401 ms | 35–64 ms | 3.3–5.0 ms | 47.7 |
| Linux arm64, CI | 156–200 ms | 198–243 ms | 43 ms | 7.2–8.6 ms | 47.7 |

`listDir` into a warm Listing allocates 0 on every OS.

### Backend choices (D12)

| OS | Variants (100k, best of 7) | Chosen |
|---|---|---|
| Windows amd64 | `FileFullDirectoryInfo` 57.6 ms, `FileIdExtdDirectoryInfo` 58.0 ms, `FindFirstFileExW` 63.1 ms | `FileFullDirectoryInfo` |
| Windows arm64 | 55.0 / 58.1 / 73.8 ms | `FileFullDirectoryInfo` |
| Linux amd64 | `getdents64`+`statx` 236–295 ms, `os.ReadDir`+`Info` 333–455 ms | `getdents64`+`statx` |
| Linux arm64 | 147–148 ms vs 208–294 ms | `getdents64`+`statx` |

### Watching

| | macOS (kqueue) | Windows | Linux |
|---|---|---|---|
| Raw change → notification | ~1 ms | < 250 ms (CI test) | < 250 ms (CI test) |
| Watched directory removed/renamed → Gone | ~1 ms | < 1 s (CI test) | < 1 s (CI test) |
| Idle wakeups | 0 on every path tried | parent-watch path check every 5 s (0.2/s) | 0 |

FSEvents measurement that led to D26 (Mac mini, 40–65 s per path, idle): `~` 1.7–2.0 callbacks/s, 20–45 process wakeups/s; `/` 2.2 callbacks/s; quiet directories 0.

## Pilot

### Bench (scripted session: scroll, hover, clicks, keys, filter, pane switch, sort by every column, divider drag, refresh swaps, hidden toggle)

| Run | Paced | Allocations (best window) | Main-thread CPU/frame |
|---|---|---|---|
| MacBook, 100k (a48b3e9, 2 runs) | 120 Hz, p50/p99 8.33/8.79 ms | 0 (first window; second run: third window, 2 background mallocs before) | 1.38–1.72 ms |
| MacBook, 1M (a48b3e9, 2 runs) | 120 Hz, p50/p99 8.33/8.53 ms | 0 (first window) | 1.40–1.48 ms |
| MacBook, `-bench-dir /usr/bin`, `~/Documents` | 120 Hz | 0 (first window) | 1.46–1.60 ms |
| MacBook, 100k / 1M (b07d6a8, before the review fixes) | 120 Hz | 0 / 3 objects | 0.86 / 0.95 ms |
| Mac mini offscreen, 100k (62b76e7) | — | 0 | 0.89 ms |
| Mac mini offscreen, 1M (62b76e7) | — | 0 | 1.24 ms |
| CI macOS 100k / 1M | yes | 0 / 0 | 1.9 / 2.1 ms |
| CI Windows amd64 / arm64 | yes | 0 / 0 | 1.0 / 0.5 ms |
| CI Linux (4 jobs) | yes | 0 | 2.7–3.6 ms (llvmpipe renders on the CPU) |

On Linux CI the earlier windows of a run show one-off 48–2,400 B allocations from the runtime; the final window is clean, and a real per-session allocation would be in every window (D27). The software renderers are slow on the arm64 runners (llvmpipe ~20 fps, WARP ~14 fps), so their bench limit is 420 s.

### Startup (MacBook, b07d6a8, `pilot -exit-first -v`)

| Start folders | exec → listings ready | exec → first frame on glass | In the first frame |
|---|---|---|---|
| `~`, `~` (6 runs) | 27–34 ms | 162–184 ms | both, 6 of 6 |
| `/usr/bin`, `/Applications` (3 runs) | 23–68 ms | 165–183 ms | both, 3 of 3 |

The review fixes made each bench session wait for its refreshes and sorts to be delivered and reconciled, so a session now does more work per frame than at b07d6a8.

### Half-bounce (BounceRec, MacBook, b040421)

The expected content is `pilot -o expect.png ~ ~` rendered on the MacBook (its home folder and fonts). Six launches through LaunchServices, Dock auto-hide temporarily off and restored afterwards:

| Run | Complete content | Bounce start / apex / end | Partial frames | Half-bounce |
|---|---|---|---|---|
| 1 | 332 ms | 257 / 445 / 753 ms | 0 | pass |
| 2 | 284 ms | 238 / 438 / 746 ms | 0 | pass |
| 3 | 291 ms | 237 / 432 / 745 ms | 0 | pass |
| 4 | 342 ms | 245 / 430 / 734 ms | 0 | pass |
| 5 | 274 ms | 228 / 424 / 736 ms | 0 | pass |
| 6 | 260 ms | 254 / 435 / 739 ms | 0 | pass |

Two things spoiled the first attempts. A macOS privacy prompt for `~/Documents`, left from a bench run, covered the window. And writing results into `~/gnui-test` changed a row's modified time between the expected render and the runs; results now go to `/tmp`.

### Privacy prompts and the main thread

The first `-bench-dir ~/Documents` run never finished: the bench setup listed the folder synchronously on the main thread before the window existed, and `open()` waited on the privacy prompt. The setup now lists on a goroutine and gives up after the bench timeout (b040421). Normal startup was never affected: start listings run on goroutines and the first frame waits at most 150 ms (D27). macOS ties the permission to the exact signature, and Pilot is ad-hoc signed, so every new build asks again.

## Live resize (found in your hand test)

**Symptom:** resizing by hand froze for 1–2 s, then the window jumped to its new size. It happened in Pilot, in rexlook, and in a rexlook built from the end of phase 3.

**Cause:** a trace of a real drag (`GNUI_TRACE_LIVE=1`) showed `nextDrawable` blocked for 1003 ms in a draw from a display-link tick. During a live resize each frame is presented inside the window's Core Animation transaction, and its drawable stays in use until that transaction commits. A resize draw and a tick draw could land in the same run-loop turn, leaving no free drawable; the commit `nextDrawable` waited for could not happen while it blocked, so only the 1 s timeout ended it.

**Fix:** `[CATransaction flush]` right after each live present (D18 amendment).

| | Before | After |
|---|---|---|
| Hand resize, Pilot | 1003 ms block | slowest `nextDrawable` 27 ms, slowest step 29 ms; you: "Smooth now" |
| Hand resize, rexlook | freezes | slowest step 23 ms |
| `-live-resize-test` (now with a tick draw after each step), rexlook | slowest step 1005 ms, fails | 23.8 ms, 300 of 300 draws, 0 allocations |
| Same, Pilot | — | 18.8 ms, 300 of 300 draws, 1 allocation over the drag |

## Pending on the MacBook

Nothing: your hand test passed after the live-resize fix ("all working", then "Smooth now").

## Hand-test checklist

- **Navigation:** double-click and Enter open folders; Backspace and ⌘↑ go up and select the folder you came from; ⌘[ and ⌘] go back and forward; the back/forward/up buttons in the titlebar.
- **Selection:** click, ⌘-click, Shift-click, ⌘A, arrow keys with and without Shift, Page Up/Down, Home/End.
- **Panes:** Tab switches; clicking a pane activates it; the divider drags; sorting by each column header and again for descending.
- **Filter:** type in a list; Esc clears; ↑/↓/Enter still drive the list; Japanese input starts the filter.
- **Path field:** ⌘L or a click selects all; type a path, `~`, a relative path, a missing path (error shown, text kept); Esc restores.
- **Open:** Enter or a double-click on a file opens it in its default app; a failure appears in the status line. Note what happens with an `.app` bundle (it is a folder).
- **Live updates:** create, rename and delete a file in Finder or Terminal in a shown folder; it appears within a moment, selection kept; delete the shown folder itself: the pane moves to its parent.
- **Hidden files:** ⌘⇧. per pane.
- **Window:** titlebar drag and double-click, resize down to the minimum size, focus another app and come back (the panes relist).
