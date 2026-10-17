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
| | Frames paced while listing | pending | MacBook, `pilot -bench -bench-dir` |
| 2 | ≤ 64 B per entry incl. name | **58 B** on the MacBook's long names (47.7 B in the synthetic CI set) | MacBook, CI |
| | 1M entries scroll < 4 ms main-thread CPU/frame, 0 allocations | **0.95 ms** paced (b07d6a8; 3 background mallocs before the exact gate existed); 1.24 ms offscreen, 0 allocations (Mac mini, 62b76e7) | MacBook; rerun with the exact gate pending |
| 3 | Steady-state frames allocate 0 | **0** on every CI job (best whole-session window), 100k and 1M; `-bench-dir` 0 | CI 7352d81, 41edb06; Mac mini |
| 4 | Watched changes appear in ≤ 250 ms | macOS ~1 ms raw, ~52 ms refreshed listing (debounce); Windows and Linux pass the 250 ms tests | Mac mini; CI |
| 5 | Content-first, start listings in the first frame | **9 of 9** runs, incl. `/usr/bin` and `/Applications`; listings ready 23–34 ms after exec | MacBook (b07d6a8) |
| | exec → on glass no slower than rexlook (~190 ms) | **161–184 ms**, median ~169 ms | MacBook (b07d6a8) |
| | Half-bounce (BounceRec) | pending | MacBook |
| 6 | Idle: 0 presents, ≤ 1 wakeup/s while watching | watcher: 0 wakeups/s on `~`, `/`, `~/Documents`, `~/Downloads` (kqueue, D26; FSEvents was 20–45/s); the app's idle run is pending | Mac mini; MacBook |
| 7 | Your hand test | pending | MacBook |
| 8 | Windows/Linux: fs tests, offscreen Pilot, text-field tests | **pass** on all 8 jobs | CI 7352d81, 41edb06 |

Binary size: `pilot` 3.8 MB (darwin/arm64, stripped).

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
| MacBook, 100k (b07d6a8) | 120 Hz, p50/p99 8.33/8.62 ms | 0 | 0.86 ms |
| MacBook, 1M (b07d6a8) | 120 Hz, p50/p99 8.33/8.63 ms | 3 objects / 240 B (background; before the exact gate) | 0.95 ms |
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

## Pending on the MacBook

These need the lid open, the screen unlocked and nothing else in front for about 10 minutes:

1. `pilot -bench -gate` at 100k and 1M with the exact gate, and `-bench-dir ~/Documents` (criterion 1's paced frames while listing).
2. Idle: Pilot on `~` in both panes, untouched for 60 s: CPU, idle wakeups, memory.
3. BounceRec half-bounce (needs Dock auto-hide off for the run, then restored).
4. The hand test (criterion 7; list below and in [phase4-text.md](phase4-text.md)).

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
