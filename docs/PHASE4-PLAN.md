# Phase 4 plan: Pilot, the dual-pane file browser

You approved phase 4 on 2026-10-17. Pilot is the reference app the framework
is built to prove (ARCHITECTURE §1, §10; D12; D17: the original Rex look).

## Scope

**In this phase:** browsing.
- **Panes:** two panes, Tab switches between them. Each pane has a path bar and a sortable file list.
- **Navigating:**
  - open a folder with Enter or a double-click;
  - go up with Backspace or ⌘↑;
  - back and forward.
- **Selecting:** single, ⌘/Ctrl and Shift.
- **Keyboard:** arrow keys and Page Up/Down.
- **Filtering:** type to filter.
- **Editing the path:** a text field with a caret, selection, clipboard and IME.
- **Opening files:** a file opens with the OS default app.
- **Live updates:** a pane updates when its directory changes on disk.

**Later (phase 5):**
- file operations (copy, move, delete, rename) with progress;
- OS icons and thumbnails;
- search across subdirectories.

## Work

1. **`fs`** (contract in `fs/fs.go`):
   - **listing:**
     - `getattrlistbulk` on macOS;
     - `FindFirstFileExW` vs `GetFileInformationByHandleEx` on Windows, chosen by measurement (D12);
     - `getdents64` + `statx` vs `os.ReadDir` on Linux.
   - **watching:** kqueue (D26; FSEvents was replaced because it is recursive), `ReadDirectoryChangesW`, inotify.
   - **worker service:** list, sort and watch on worker goroutines, then hand the results to the UI thread through `core.Ring` and a wake. Pointer-free 24-byte entries.
   - **sorting and filtering:** natural name order, case-insensitive.
   - **opening files:** with the OS default app.

   Split across two agents: the macOS backend plus the shared service, and the Windows and Linux backends.
2. **Text editing:**
   - per-glyph cluster boundaries in `text` (macOS, Windows, Linux), needed for carets;
   - a single-line `ui` text field: caret, selection, word moves, copy, cut and paste;
   - a platform clipboard: NSPasteboard and the Win32 clipboard now, Wayland and X11 at least for plain text;
   - macOS IME preedit through the existing `NSTextInputClient` view.
3. **`cmd/pilot`** on `app` + `ui` + `fs`, in the Rex look. Content-first: the start directory is listed on a worker while AppKit opens the window, and the first frame shows it.

## Exit criteria (macOS on the MacBook; Windows and Linux in CI on real file systems)

1. **Listing speed, 100k-entry directory.**
   - Listed and sorted on a worker in ≤ 150 ms on the MacBook (D12 measured 88 ms for listing alone).
   - Frames stay paced while it happens.
   - Windows and Linux numbers come from CI and are reported, not estimated.
2. **Memory:** ≤ 64 B per entry including its name. 1M synthetic entries scroll at < 4 ms main-thread CPU per frame with 0 allocations.
3. **Zero allocations:** steady-state frames allocate 0, including scrolling, hover, selection, typing a filter and watch-driven refreshes on the UI thread. Workers reuse their buffers.
4. **Watching:** creating, deleting or renaming a file in a shown directory appears in ≤ 250 ms on every OS.
5. **Startup:**
   - content-first, with the start directory's listing in the first frame;
   - half-bounce (BounceRec);
   - exec → on glass no slower than `rexlook`.
6. **Idle:** 0 presents, ≤ 1 wakeup/s, including while watching directories.
7. **Your hand test on the MacBook:** navigation, selection, keyboard, filter, path editing (including an IME, e.g. Japanese input), opening files, and live updates.
8. **Windows and Linux:**
   - `fs` listing and watching tests pass in CI;
   - CI renders Pilot offscreen;
   - text-field tests pass.

## Status (2026-10-17)

Built and pushed: `fs` (listing, sorting, filtering, watching and opening on all three OSes), text editing (carets, text field, clipboard, input methods), and Pilot. All 8 CI jobs pass, including the exact allocation gate for Pilot's bench on every OS. An independent review (two review-sol passes, then a scoped re-review of the high-severity fixes) found 34 issues; all are fixed with regression tests except two documented decisions (D27 startup cap) and the known gap below.

Results per exit criterion: [measurements/phase4-macos.md](measurements/phase4-macos.md). Met: 1 (132 ms on the MacBook; frames paced while listing real folders), 2 (58 B/entry; 1M at 1.40–1.48 ms/frame, 0 allocations), 3, 4, 5 (content-first, exec → on glass 161–184 ms, half-bounce 6 of 6), 6 (0 % CPU, 0 idle wakeups while watching `~`), 8. Remaining: your hand test (criterion 7).

Known gaps carried forward:
- Press-and-hold accents may produce "eé" (`replacementRange` ignored; design in phase4-text.md).
- Linux: clipboard is in-process only; no input method; no cursors.
- Windows: input method and clipboard untested with a real IME and other apps.
- kqueue watcher gaps (D26), covered by relisting when the window gains focus.
