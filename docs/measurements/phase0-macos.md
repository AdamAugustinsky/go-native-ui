# Phase 0 measurements — macOS

Machine: Mac mini M4 (`Mac16,10`), 10 cores, 16 GB, macOS 27.2 (26B5101f),
5K display (5120×2880). Toolchain: go1.27.1 darwin/arm64, `CGO_ENABLED=0`
unless stated. Date: 2026-10-16. All numbers are warm (OS file cache and dyld
shared cache warm) unless marked cold. Windows and Linux: **not measured**
(no machines available this session).

Probes live in `probes/` and are throwaway; commands are in each file header.

## 1. Foreign-call cost and allocations (`probes/ffi`, `probes/ffi2`, `probes/cgobase`)

`go test -bench . -benchmem`, darwin/arm64.

| Path | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| purego `SyscallN` (int args) | 55.1 | 8 | 1 |
| purego `SyscallN` → `objc_msgSend` | 56.2 | 16 | 1 |
| purego `RegisterFunc` (int) | 147.7 | 40 | 3 |
| purego `RegisterFunc` (float) | 144.8 | 40 | 3 |
| purego/objc `ID.Send` | 194.9 | 128 | 5 |
| purego/objc `ID.Send` + 1 arg | 208.1 | 152 | 7 |
| purego/objc `Send[T]` | 277.0 | 216 | 8 |
| purego `NewCallback` round trip (via `qsort`) | 232.6 | 108 | 4 |
| **own trampoline: C call, int** (`probes/ffi2`) | **15.8** | **0** | **0** |
| **own trampoline: `objc_msgSend`, mixed int/float args, float return** | **21.8** | **0** | **0** |
| **own trampoline: C→Go callback round trip** | **48.6** | **0** | **0** |
| cgo call, int (reference) | 14.9 | 0 | 0 |
| cgo `//export` callback round trip (reference) | 43.6 | 0 | 0 |

The own trampoline reaches cgo speed with zero allocations and needs no C
toolchain. It relies on purego's `internal/fakecgo` for runtime bootstrap
(`runtime.iscgo`, `crosscall2`, pthread-created Ms) and on
`runtime.cgocall` via linkname (kept stable by the Go team for purego; see
`$GOROOT/src/runtime/cgocall.go:126-132`).

## 2. AppKit + Metal window, first frame (`probes/macwin`)

Stripped binary (`-trimpath -ldflags="-s -w"`): **2,147,666 B**
(go hello world: 1,571,202 B).

Internal timeline, ms since `main` (median of 5 warm runs):

| Step | cumulative ms |
|---|---:|
| exec → `main` | 3.3 |
| dlopen AppKit/QuartzCore/Metal | 3.4 |
| `NSApplication sharedApplication` + activation policy | 29.0 |
| `NSWindow initWithContentRect:...` | 55.1 |
| `MTLCreateSystemDefaultDevice` (alone) | 0.17 |
| CAMetalLayer setup | 55.9 |
| `makeKeyAndOrderFront:` | 68.4 |
| `finishLaunching` | 78.2 |
| `activateIgnoringOtherApps:` | 78.8 |
| clear + present + `waitUntilCompleted` | **82.8** |

First launch after a fresh build: 415 ms (Gatekeeper scan + cold caches).

External timer (`probes/startwin`, exec → first layer-0 window of the child PID
listed on screen by `CGWindowListCopyWindowInfo`, 1 ms polling, 7 runs):
**median 171.5 ms** (133–175). The ~85 ms gap between "frame committed" and
"window on screen" is not yet explained (candidates: first CATransaction commit
only happens when the run loop turns; window open animation; WindowServer
round trips). Phase 1 investigates.

Startup variants (`probes/macstart`, same external timer, 7 runs each):

| Variant | exec → window on screen (median) |
|---|---:|
| baseline (as above) | 171 ms (repeated: 171 ms) |
| `[CATransaction flush]` after order-front and after present | **139–142 ms** (two sets) |
| `setAnimationBehavior: None` | 154 ms (one set); in later sets the timer never saw the window |
| flush + no animation | 98 ms in one run started from the shell; under the timer the window was never detected (20 s timeout) |

The no-animation variants are **inconclusive**: run by hand, the window is
listed on screen, but when the timer launches them it is not. That's either
flakiness in the tooling or an activation/ordering effect. Phase 1 replaces
window-list polling with an in-app visibility signal
(`NSWindowDidChangeOcclusionStateNotification`). Footprint 1 s after the
window appears was 84 MB (vs 34 MB after 5 s), so the startup memory
transient lasts more than a second.

Idle (window open, nothing animating, 20 s sample): CPU time unchanged
(0:00.09 → 0:00.09), `top`: 0.0 % CPU, 3 idle wakeups / 5 s.

Memory after first frame (960×600 pt window at 2×, 3 drawables):

| Metric | Value |
|---|---:|
| `phys_footprint` (Activity Monitor "Memory") | 34 MB |
| …of which owned graphics memory | 32 MB |
| `phys_footprint_peak` | 85 MB |
| RSS (`ps`) | 88 MB (includes shared framework pages) |

## 3. Baselines on this Mac (`probes/startwin`)

| App | Binary | exec → window on screen (median) | Footprint after settle | Idle CPU |
|---|---:|---:|---:|---|
| Probe (Go, AppKit+Metal) | 2.1 MB | 171 ms | 34 MB | 0 s / 10 s |
| Zed (Rust/GPUI, empty folder) | 260 MB | 302 ms (299–320) | 118 MB | 0.36 s / 10 s (8 s after start) |
| Finder (long-running) | — | n/a | 114 MB | — |
| Gio v0.10.3 hello (cgo) | 6.1 MB | **crashes at launch on macOS 27.2** (`runtime/cgo: misuse of an invalid Handle` in `gio_onDestroy`) | — | — |

## 4. GC cost of bulk entries (`probes/gcscan`)

1,000,000 entries, `runtime.GC()` ×20, `runtime/metrics`.

| Layout | Heap live | Heap objects | Scannable heap | GC CPU / cycle | GC wall / cycle |
|---|---:|---:|---:|---:|---:|
| `struct{Name string; ...}` | 72 MB | 1,000,259 | 40.2 MB | 8.94 ms | 1.86 ms |
| `struct{NameOff uint32; NameLen uint16; ...}` + one `[]byte` | 56 MB* | 241 | 0.2 MB | **0.16 ms** | **0.08 ms** |

*names buffer pre-sized at 32 B/entry; `sizeof(Entry)` = 24 B.

## 5. Directory listing (`probes/dirlist`)

Name + type + size + mtime for every entry; median of 9 warm runs.

| Directory | Entries | `getattrlistbulk` | `os.ReadDir` (name+type only) | `os.ReadDir` + `Info()` |
|---|---:|---:|---:|---:|
| synthetic | 10,000 | 7.7 ms | 4.5 ms | 15.7 ms |
| synthetic | 100,000 | 88.3 ms | 70.0 ms | 197.7 ms |
| /System/Library/Frameworks | 340 | 0.20 ms | 0.18 ms | 0.48 ms |
| ~/Library/Caches | 204 | 0.16 ms | 0.11 ms | 0.26 ms |

`getattrlistbulk` gives full metadata at ~2.2× the speed of
`ReadDir`+`Info` on large directories. Directories do not return
`ATTR_FILE_DATALENGTH` even with `FSOPT_PACK_INVAL_ATTRS`; parse by the
returned-attributes bitmap.

## 6. Text (`probes/coretext` + research subagent measurements)

CoreText through purego (allocating calls, ~8 FFI calls per string, so
≈1.5 µs of each figure is purego overhead):

| Metric | CoreText | go-text/typesetting v0.3.5 (pure Go) |
|---|---:|---:|
| Shape ASCII filename (~21 chars) | 10.6 µs | 4.5 µs (segment + fallback + shape) |
| Shape mixed CJK/Arabic/emoji | 38.5 µs | 5.9 µs (needs explicit "emoji" family) |
| Rasterize one glyph | 3.4 µs (26 px, CGBitmapContext) | 0.66 µs (13 px Latin, x/image/vector) |
| First use of fallback fonts | 27.5 ms (10 mixed strings) | 25 ms for Apple Color Emoji alone |
| Memory | 13.8 MB process peak footprint | **+190 MB Go heap** after loading Apple Color Emoji (sbix table read into heap) |
| Font discovery at startup | none (system cascade) | fontscan index: 156 ms cold, 43 ms warm (re-stats every font file) |
| Fallback quality | PingFang, SF Arabic, Apple SD Gothic, Apple Color Emoji chosen by the OS cascade | emoji → `.LastResort` unless queried; PingFang not found; Han switches font per character |
| Hinting / LCD AA | n/a on macOS (OS removed LCD AA in 10.14) | none / none |
| Binary size delta | 0 | +2.1–2.3 MB (shaping), +2.9–3.4 MB (full stack) |
