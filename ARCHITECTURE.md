# Architecture

Status: **phase 0 (research and decisions)**, 2026-10-16. Project name is a
placeholder (`go-native-ui`, module `gonativeui`) until approved.

This document describes the target design and the evidence behind it. The
choices themselves, with alternatives and costs, are in
[DECISIONS.md](DECISIONS.md). Raw numbers are in
[docs/measurements/phase0-macos.md](docs/measurements/phase0-macos.md).
Windows and Linux numbers are **not measured** yet: no machines were
available this session.

## 1. Goal and the bar

A native desktop UI stack in Go for macOS, Windows and Linux that meets
File Pilot's engineering bar: it starts instantly, uses little memory, burns
no CPU when idle, and responds to input immediately. The proof is a
reference app, a dual-pane file browser.

Two further bars (user, 2026-10-16):
- **Startup: half-bounce or better.** The first visible frame is complete content (§3).
- **Visuals: modern, in the original Rex style.** The benchmark is a recreation of Superlogical Rex's frontend in our framework (`cmd/rexlook`; [docs/design/rex-benchmark.md](docs/design/rex-benchmark.md)), held to the same performance gates. The same look becomes the default theme (D17).

What we know about File Pilot, and where it comes from:

| Fact | Source |
|---|---|
| C, custom platform layer, custom D3D11 + OpenGL renderers, own UI framework, memory primitives, containers, UTF-8 strings | [filepilot.handmade.network](https://filepilot.handmade.network/) |
| No external libraries beyond small single-headers (`stb_sprintf.h`) | same |
| 2.46 MB download (v0.8.5, 2026-08-31) | [filepilot.tech](https://filepilot.tech/) |
| Arenas, scratch arenas, pools, length-based strings | [handmade.network/snippet/1709](https://handmade.network/snippet/1709), [/1680](https://handmade.network/snippet/1680) |
| Multithreaded search/filtering; background metadata and thumbnail tasks | [/snippet/1675](https://handmade.network/snippet/1675), [v0.8.1 notes](https://filepilot.tech/starlog/2026-07-15-v0.8.1) |
| IMGUI interaction model, UI rewritten 2023 | [/snippet/1709](https://handmade.network/snippet/1709), [/snippet/2224](https://handmade.network/snippet/2224) |
| ~100k LOC, ~2 s full rebuild, DirectWrite only for glyph rasterization, Fleury-style UI, statically linked CRT | task brief (BSC 2025 talk, Wookash and Hanselminutes episodes). The talk and podcast transcripts could not be retrieved this session, so these are **unverified** here. |

What we take from it: own every layer that sits on the frame path; keep
the UI thread doing UI only; group data by lifetime; cache every OS result;
keep the product surface small.

## 2. Layers

```
cmd/pilot             reference app: dual-pane browser (phase 4)
cmd/rexlook           visual benchmark: Rex frontend recreation (static in phase 2, interactive in phase 3)
cmd/listbench         1M-row virtual-list scroll benchmark (phase 3)
app                   glue: window + surface + renderer + text + ui, the frame loop, input translation (D22)
ui                    immediate-mode builder, keyed box cache, layout, input, scroll, text edit
text                  shaping + fallback + rasterization per OS, run cache, glyph atlas
gpu                   Renderer interface; backends: metal (darwin), d3d11 (windows), gl (linux)
platform              Window, events, DPI, keyboard/IME, clipboard, cursors, timers, wake
fs                    directory listing, watching, icons/thumbnails (per OS)
core                  arenas, frame scratch, pointer-free tables, handle pools, MPSC queue
clib                  public: an app's own native library, dlopen + zero-alloc plain C calls on internal/ffi, no callbacks (D35; darwin, linux)
internal/ffi          zero-alloc C call + callback trampolines (darwin, linux; arm64, amd64)
internal/objc         Objective-C messaging on ffi (darwin)
internal/com          COM vtable call helpers (windows)
internal/wl, x11, egl Wayland/X11/EGL bindings via dlopen (linux)
```

Dependencies only point down. `ui` does not know about the OS. `gpu` and
`text` expose small interfaces with one backend per OS, picked at compile
time by build tags (no runtime plugin registry).

## 3. Threading and the frame loop

- `runtime.LockOSThread()` runs in the platform package's `init`, so the
  main goroutine owns the process main thread (AppKit requires this, and
  Win32/Wayland like it).
- The **main thread only** pumps OS events, builds the UI, lays it out,
  records draw data, and submits/presents.
- **Workers** are goroutines: directory listing, metadata, file watching,
  icons/thumbnails, text shaping prefetch, shell context menus (Windows,
  in an STA thread), search. They write results into memory they own
  (pointer-free blocks), then publish a small fixed-size message to a
  preallocated MPSC ring and call `platform.Wake()`.
- `Wake()` is coalesced with an atomic flag (at most one OS wake in
  flight). The mechanism per OS:
  - macOS: `[NSApp postEvent:atStart:]` with an `NSEventTypeApplicationDefined` event; it is safe to call from any thread (it wakes the main CFRunLoop).
  - Windows: `PostMessageW(hwnd, WM_APP_WAKE)`.
  - Linux: an `eventfd`, polled together with the Wayland display fd or the X11 connection fd.

```
loop:
    if !needFrame: platform.Wait()        // blocks; zero CPU, zero wakeups
    platform.DrainEvents(&input)          // non-blocking
    drain worker ring → apply results     // bounded work per frame
    if input || results || animating || resized:
        ui.Build(); ui.Layout(); r.Record(); r.Present()   // vsync-paced
    needFrame = ui.Animating()
```

**Idle is a hard invariant**: no timers, no frames, no presents unless
something changed. Caret blink and tooltip delays are deadlines passed to
`platform.Wait(deadline)`, not periodic timers. Caret blinking stops after
a few seconds without input (as on macOS) so a focused field doesn't keep
waking the app.

**Vsync pacing (only while animating or handling input):**

| OS | Mechanism |
|---|---|
| macOS 14+ | `CAMetalLayer` (`displaySyncEnabled`, 2 drawables, sRGB colour space). The first frame is drawn with `nextDrawable`; on the view `CADisplayLink`'s first tick the platform switches to a `CAMetalDisplayLink` (`preferredFrameLatency` 1, full-rate `preferredFrameRateRange`), which hands over the drawable with each tick, so the main thread never blocks in `nextDrawable`. Links run only while frames are requested (4-tick hysteresis), and callbacks go through our zero-alloc IMP trampoline |
| Windows | flip-model swap chain (`FLIP_DISCARD`, 2 buffers) with `FRAME_LATENCY_WAITABLE_OBJECT`, `SetMaximumFrameLatency(1)`; the loop waits on the waitable handle plus messages with `MsgWaitForMultipleObjectsEx` |
| Wayland | `wl_surface.frame` callbacks; `eglSwapInterval(0)` so a hidden surface never blocks |
| X11 | `eglSwapInterval(1)` (blocking swap) while animating; Present extension later |

**Latency:** handle input as soon as it arrives. On Windows, wait on the
waitable object *before* pumping messages and building, so input sampled
just before the frame lands in that frame. Live resize renders
synchronously from the OS resize callback (`windowDidResize:` with
`presentsWithTransaction` on macOS, `WM_SIZE`/`WM_PAINT` inside the modal
loop on Windows, configure events on Wayland).

### Content-first startup (half-bounce goal)

**Goal (user, 2026-10-16):** half-bounce or better on macOS. The window
must be on screen with its real content, and usable, before the Dock icon
finishes the first half of its first launch bounce. Windows and Linux use
the same wall-clock threshold. A blank or placeholder window followed by
content later does not count. See P1 "Half-bounce" for the definition and
sources.

**Rules:**

1. **The first visible frame is a complete frame.** The window reaches the screen together with its first content frame, never before it.
   - **macOS:** create the window without ordering it front and render the first frame into the `CAMetalLayer`. Present the first frame with `presentsWithTransaction`, then switch back to asynchronous presents. That needs a different present path from today's `presentDrawable:`; Apple's required sequence is:
     1. commit the command buffer;
     2. `waitUntilScheduled`;
     3. call the drawable's own `present` inside the `CATransaction` that also does `orderFront`.

     *Implemented in phase 2* (`Options.Hidden` + `Surface.RevealOnFirstFrame`). *Still to confirm from recordings (BounceRec): no empty frame reaches the screen.*
   - **Draw before the first event pump (all OSes).** On macOS the first `nextEventMatchingMask` runs 50–100 ms of AppKit's deferred launch work. Drawing the first frame before it cut exec → on glass from a 246 ms median to 159 ms (phase-2 measurements).
   - **Windows:** create the window cloaked (`DWMWA_CLOAK`), present the first frame, then uncloak and show it. No white flash.
   - **Wayland:** a surface maps only when its first buffer is committed, so commit only a complete frame.
   - **X11: unresolved.** Mapping happens before drawing, and X11 leaves a newly mapped window's contents undefined, so a compositor may show it before the first swap. A `None` background pixmap only avoids the background clear. *Phase-2 research:* compositor frame synchronisation (`_NET_WM_SYNC_REQUEST` / frame-drawn), validated visually.
2. **The main thread's critical path is OS window setup only.** On macOS that is ~92 ms of AppKit setup (P1). Workers start at the top of `main` and must finish before AppKit does:
   - read the initial directory (persisted last path), or render a persisted snapshot of the last listing validated by mtime, then revalidate;
   - load fonts, shape and rasterise only the glyphs of the visible rows;
   - create the GPU pipeline; precompile shaders if runtime compilation turns out to be on the critical path (D14).
3. **Persisted UI state** (window frame, panes, sort, scroll position) is read synchronously at startup. It is small, and it lets the first frame lay out without a second pass.
4. **Measure what the user sees.**
   - **External:** a `.app` bundle launched with `open` and recorded with ScreenCaptureKit at ≥ 60 fps. Measure launch → first frame with complete content, and the Dock bounce period from the same recording.
   - **In-app:** present feedback (`MTLDrawable` presented time, DXGI frame statistics, `wp_presentation`) on top of exec → first present.

## 4. Calling the OS without cgo, without allocating

Measured on darwin/arm64 (`probes/ffi*`): every purego call path allocates
(1–8 allocs, 55–277 ns), including C→Go callbacks (4 allocs). That breaks
the zero-allocation frame rule. Our own trampolines on top of purego's
`fakecgo` bootstrap run at cgo speed with **0 allocations** (15.8 ns call,
21.8 ns `objc_msgSend` with float args/return, 48.6 ns callback, vs cgo
14.9 / 43.6 ns).

- `internal/ffi.Frame` holds 8 integer and 8 float register slots, stack
  slots, the `x8` indirect-result pointer, and return slots (`x0`/`x1`,
  `d0`–`d3`). Frames live in memory that does not move, never on a
  goroutine stack (a callback could grow and move the stack while C holds
  the pointer): a global or heap frame used by one thread at a time (the
  main thread's `objc.F`), or, inside a callback, the invocation's own frame
  on the C stack (`CB.Frame()`, D36), which any thread and any nesting depth
  can use. The callback table has 1024 entries.
- The call path is `runtime.cgocall(trampoline, frame)`, so blocking calls
  (`nextEventMatchingMask`, `poll`) release the P and the GC is not
  stalled.
- Callbacks: a small assembly table of entry points → `crosscall2` →
  `runtime.cgocallback` → a non-reflective Go dispatcher. Objective-C
  methods all share **one** IMP that dispatches on `(self, _cmd)`. Wayland
  objects all share **one** dispatcher through `wl_proxy_add_dispatcher`. So
  the entry table stays tiny (32 slots).
- purego is still used for what it is good at: `Dlopen`/`Dlsym`, the
  `fakecgo` runtime bootstrap (sets `iscgo`, creates Ms with
  `pthread_create`, provides `crosscall2`), and one-time setup such as
  class registration.
- Windows needs none of this: `syscall.SyscallN` passes the first four
  arguments in both GPRs and XMM registers, which covers COM `FLOAT`
  parameters, and `syscall.NewCallback` handles `WndProc`. Their allocation
  behaviour still has to be verified on Windows (CI gate).
- Rules: pointer arguments must reference heap or global memory, not stack
  locals. Strings passed to C are copied into NUL-terminated frame-scratch
  bytes.

## 5. Platform layer

The common API is the same on every OS: `Window` (create, show, title,
size, scale, close), an event stream (pointer, wheel/trackpad with
precise deltas and phases, key with physical code + logical key +
modifiers, text input, IME preedit/commit, focus, resize, scale change,
close request), `Wait(deadline)`, `Wake()`, clipboard text, cursor shape.

| | macOS | Windows | Linux (Wayland first) | Linux (X11 fallback) |
|---|---|---|---|---|
| Binding | AppKit via `objc_msgSend` on `internal/ffi` | Win32 via stdlib `syscall` (`SyscallN`, `NewCallback`; DLLs loaded from System32 only) | `libwayland-client.so.0` via dlopen; `wl_proxy_marshal_array_flags` + `wl_proxy_add_dispatcher`; protocol tables generated from XML by our own generator | `libX11.so.6` via dlopen |
| Event loop | own loop: `nextEventMatchingMask:untilDate:` + `sendEvent:` (no `[NSApp run]`) | `MsgWaitForMultipleObjectsEx` + `PeekMessage` | `prepare_read`/`poll`/`read_events`/`dispatch_pending` on display fd + eventfd | `poll` on connection fd + eventfd, `XPending`/`XNextEvent` |
| DPI | `backingScaleFactor`, `viewDidChangeBackingProperties` | Per-Monitor v2 (manifest + `SetProcessDpiAwarenessContext`), `WM_DPICHANGED` suggested rect | `wp_fractional_scale_v1` + `wp_viewporter`; integer `wl_surface.preferred_buffer_scale` fallback | `Xft.dpi` resource, XSETTINGS later |
| Keyboard | `keyCode` + `charactersIgnoringModifiers` | scancode/VK + `WM_CHAR` | `wl_keyboard` keymap → libxkbcommon (dlopen) | core events + libxkbcommon-x11 or XKB ext |
| IME | `NSTextInputClient` on our NSView subclass | IMM32 (`WM_IME_*`) first, TSF later | `zwp_text_input_v3` | XIM later |
| Clipboard | `NSPasteboard` | `CF_UNICODETEXT` | `wl_data_device` | selections via `XConvertSelection` |
| Cursors | `NSCursor` | `LoadCursor`/`SetCursor` | `wp_cursor_shape_v1`, fallback libwayland-cursor theme | Xcursor (dlopen) |
| Decorations | native title bar (full-size content view later) | native, custom title bar later (`WM_NCCALCSIZE`/`WM_NCHITTEST`) | `zxdg_decoration_manager_v1` when the server decorates; otherwise **our own CSD** drawn by `ui` (GNOME never draws server-side decorations) | server-side (WM) |
| GPU surface | `CAMetalLayer` | DXGI swap chain on `HWND` | `wl_egl_window` (libwayland-egl) + EGL | EGL on `Window` |

Not used: libdecor (it loads a GTK plugin), GLFW/SDL, and any toolkit.

## 6. Renderer

```go
type Renderer interface {
    Begin(size PixelSize, scale float32) bool          // false: nothing to draw (minimized)
    Upload(tex TextureID, rect Rect, pixels []byte)    // atlas pages and images
    Draw(list *DrawList)                               // pointer-free instances + batches
    Present()                                          // vsync per section 3
}
```

- **One instanced quad pipeline** draws everything except text-specific
  blending: per-instance rect, UV rect, color (or 2 colors for a
  gradient), 4 corner radii, border width + color, edge softness (SDF
  shadows with an erf-based approximation), clip rect, and a mode (solid /
  alpha mask / RGBA texture / LCD mask). Rounded-rect SDF evaluation
  happens in the fragment shader (the approach of RAD Debugger and GPUI's
  quad/shadow primitives).
- **Glyph atlas:** an R8 page for grayscale coverage and an RGBA8 page for
  color glyphs. An RGB "LCD" page is used only where the OS gives subpixel
  masks (Windows ClearType), with dual-source blending. Our own
  shelf/skyline packer handles allocation; eviction is generation-based
  (a page is reset when full and repopulated lazily).
- **Images** (icons, thumbnails) go into RGBA atlas pages keyed by
  (path-hash, size). Package `img` (D44) decodes them at display size and
  packs those up to 256 px into 1024² pages; larger ones get their own
  textures.
- **Texture lifetime.** Texture ids carry a generation: `FreeTexture` makes an
  id stop resolving at once (quads still naming it draw nothing) and reuses
  its index, while the GPU object waits on a retiring list until every frame
  that may sample it has finished (ring-slot retirement: Metal command buffer
  status, D3D11 event queries, OpenGL sync objects, all read without waiting;
  a Metal frame not yet committed keeps what it samples). `Stage` fills part
  of a texture that frames in flight sample without waiting for them (Metal:
  a blit from a per-slot staging buffer, committed ahead of the next frame;
  D3D11 and OpenGL order uploads with draws already). See `gpu/textures.go`.
- **Batches** break only on texture or scissor changes; a typical frame is
  1–4 draw calls. Instance data goes into a persistently mapped ring buffer
  per frame in flight. Nothing allocates per frame.
- **Shaders:** MSL compiled at startup with `newLibraryWithSource:` on a
  worker thread, in parallel with AppKit window creation (no Xcode on the
  dev machine to precompile a metallib; revisit later). HLSL is
  precompiled to DXBC in CI (fxc), embedded as bytes, so we don't depend on
  `d3dcompiler_47.dll`. Until Windows CI is approved, the interim path is
`D3DCompile` at startup from the system copy of `d3dcompiler_47.dll` in
System32 (PHASE2-PLAN). GLSL 330 core / GLSL ES 300 text is compiled by
  the driver.
- **Additions for the visual bar** ([rex-benchmark.md](docs/design/rex-benchmark.md)):
  - backgrounds: three-or-more-stop and four-corner gradients;
  - text: an alpha fade mask for truncated runs;
  - edges: device-pixel hairlines and inner highlights, and two shadows per card;
  - icons: OS system symbols rasterised into the atlas;
  - focus dimming: an animated colour transform per subtree;
  - window chrome: custom on each OS.
- Partial presents (dirty rects) are a later optimisation. A full redraw
  of a file-browser frame is cheap; the point is that idle frames are
  never drawn.

## 7. Text

Text sits behind one interface with a native backend per OS (evidence in
DECISIONS D9):

```go
Shape(font FontKey, sizePx float32, s []byte, out *Run) // glyph ids, advances, offsets, clusters, face per glyph
Raster(face FaceID, glyph GlyphID, sizePx float32, subpixelX uint8, out *GlyphBitmap)
```

| OS | Shaping / bidi / fallback | Rasterization |
|---|---|---|
| macOS | CoreText (`CTLine`, system cascade list) | CoreText into a `CGBitmapContext` (grayscale; macOS has no LCD AA) |
| Windows | DirectWrite (`IDWriteTextAnalyzer`, `IDWriteFontFallback`) | `IDWriteGlyphRunAnalysis` (ClearType 3×1 or grayscale per system setting), color glyphs via `TranslateColorGlyphRun` |
| Linux | pure-Go go-text/typesetting (D9 option B, decided); fontconfig via dlopen for discovery and fallback | a Go rasterizer (grayscale AA, no hinting) |

On top, shared by every backend:

- **Run cache:** keyed by (hash of bytes, font, size). It stores
  pointer-free glyph arrays in a slab with clock eviction. File names are
  shaped once, mostly on workers, and prefetched for a listing as it loads.
  Shaping off the main thread assumes CoreText's immutable objects and the
  DirectWrite shared factory are thread-safe. Phase 2 verifies this per
  object type; the fallback is one shaping thread. The main thread
  only shapes cache misses for rows that just scrolled into view.
- **Glyph cache:** keyed by (face, glyph, size, subpixel-x variant 0–3),
  pointing into the atlas.
- **Ellipsis truncation, alignment and caret positions** are computed by us
  from advances and clusters. Grapheme and word boundaries come from the
  backend (CoreText/DirectWrite) or a UAX #29 table on Linux.

## 8. UI core (Fleury-style)

- **Boxes:** every widget is a box built each frame through a small
  builder API (`ui.Row`, `ui.Button`, `ui.Label`, `ui.ScrollList`, ...).
  These are thin helpers over `ui.Box(key, flags)` plus push/pop style
  stacks.
- **Identity:** a 64-bit key = hash(parent key, label-or-id bytes), with
  RAD-style `##`/`###` conventions for display-text vs identity. Keys are
  stable across frames and are the future accessibility element IDs
  (UIA `AutomationId`, `NSAccessibility` identifier, AT-SPI path).
- **Storage is pointer-free:** `[]Box` pool with a free list; links are
  `uint32` indices; key→index is our own open-addressing table (no Go map
  growth on the hot path); per-frame strings live in the frame arena as
  offset+length.
- **Persistent state** (hot_t/active_t animation, scroll offset, text-edit
  state, focus) lives in the same box record and survives as long as the
  key is built each frame. Boxes not touched in a frame are pruned (RAD
  behaviour); widgets that must survive temporary omission get an explicit
  keep-alive.
- **Layout:** per-axis passes as in RAD Debugger: standalone sizes (px,
  text) → parent-percentage → children-sum → violation resolution by
  strictness → positioning. Taffy/flexbox is out of scope.
- **Input:** hit-testing runs against the *previous* frame's rects, so a
  widget learns it was clicked while it is being built (signals: hovering,
  pressed, released, clicked, double-clicked, dragging, scrolled,
  keyboard-activated). Hot/active/focus are keys in UI state.
- **Scrolling:** smooth scroll offset with target + exponential approach.
  Precise trackpad deltas are used as-is, momentum comes from the OS.
- **Virtualized lists:** only rows in `[firstVisible-overscan,
  lastVisible+overscan]` are built. Row height is fixed per list, so the
  visible range is O(1) to compute; the cost per frame is independent of
  the 1M total.
- **Animation:** `rate = 1 - 2^(-k·dt)` approach (RAD). `ui.Animating()`
  returns true while any value is still moving; that is the only thing
  that keeps the loop running.

## 9. Memory and the GC

- **Bulk data is pointer-free:** a file entry is a struct of integers
  (`NameOff uint32, NameLen uint16, Kind, Flags, Size int64, ModNs int64,
  IconID uint32`) in one `[]Entry`. Names are in one `[]byte` per listing.
  1M entries with Go strings cost 8.9 ms GC CPU per cycle; with offsets,
  0.16 ms (56× less, `probes/gcscan`). Target ≤ 64 B per entry including
  the name.
- **Handles, not pointers:** `type ListingID uint32`, `EntryIdx uint32`,
  `TextureID uint16`, with generation bits where reuse is possible.
- **Frame scratch:** two byte arenas (current and previous frame, for
  prev-frame hit testing) that reset each frame. Typed slices are carved
  from them with `unsafe.Slice` only for pointer-free types. No dependency
  on `GOEXPERIMENT=arenas`.
- **Steady state allocates nothing.** A test harness runs warmed-up,
  isolated frame sequences (idle redraw, scroll a 1M list, type in a field,
  resize) and asserts the exact deltas of `/gc/heap/allocs:objects` and
  `/gc/heap/allocs:bytes` are **0**. `testing.AllocsPerRun` is only used
  for micro hot paths, because it truncates its average.
- **GC tuning** comes after measurement: default `GOGC`, plus a
  `GOMEMLIMIT` soft cap once we know the working set. Record GC CPU
  (`/cpu/classes/gc/total:cpu-seconds`) and pauses
  (`/sched/pauses/total/gc:seconds`) while scrolling, and check with
  `GODEBUG=gctrace=1`. Go 1.27 uses the Green Tea GC by default.

## 10. Reference-app data

| | Listing | Watching | Icons / thumbnails |
|---|---|---|---|
| macOS | `getattrlistbulk` (2.2× faster than `ReadDir`+`Info` at 100k entries) | kqueue `EVFILT_VNODE` on the directory and its files (non-recursive; D26 — FSEvents is recursive and woke the process 20–45×/s on `~`) | `NSWorkspace iconForFile:` / QuickLook thumbnails on workers |
| Windows | `FindFirstFileExW` (`FindExInfoBasic`, `FIND_FIRST_EX_LARGE_FETCH`) vs `GetFileInformationByHandleEx(FileIdExtdDirectoryInfo)`; measure both | `ReadDirectoryChangesW` (overlapped) on a worker | `SHGetFileInfoW` / `IShellItemImageFactory` on STA workers |
| Linux | `getdents64` + `statx` (batched; `io_uring` later if useful) vs `os.ReadDir` | inotify (fd in the worker's poll set) | freedesktop icon theme lookup (own, pure Go) + thumbnail cache spec (`~/.cache/thumbnails`) |

All results are cached by (path hash, mtime) with bounded memory.

## 11. Budgets

These are initial targets, revised after the macOS baselines. Windows and
Linux targets stay at the brief's values until we have machines.

| Metric | Brief | Revised (macOS) | Measured so far (macOS; phase 1 spike unless noted) | Baselines |
|---|---|---|---|---|
| Cold start → first interactive frame | < 150 ms | **Headline: half-bounce or better on macOS** (§3, content-first startup), the same wall-clock threshold on Windows/Linux. Operational budget (user, 2026-10-17): **≤ 150 ms from program start (exec) → first frame with complete content on screen (warm); stretch 120 ms**. Time before exec (LaunchServices, ~110–150 ms on the test MacBook) is outside our control and is judged by the half-bounce target instead, measured with `tools/bouncerec`. On macOS 27 the first launch bounce peaks ~455 ms after the launch request. "Cold after reboot" is reported separately | 134.9 ms median exec → first present (7 warm runs). Time to visible pixels is **not measured**: the display was asleep and the session locked (P1) | Zed 302 ms |
| Idle | 0 % CPU, no frames | 0 CPU-time growth over 60 s, 0 presents, ≤ 1 wakeup/s | 0 presents / 60 s; 3.9–4.2 ms CPU / 60 s (unattributed); 0.17–1.0 package-idle wakeups/s across runs | Zed 0.36 s CPU / 10 s just after start |
| Scroll 1M rows | < 4 ms CPU/frame, 0 allocs | same; also report GPU time | — | — |
| Memory, empty demo | < 40 MB RSS | **`phys_footprint` ≤ 40 MB at 960×600 pt @2×**; non-GPU footprint ≤ 12 MB; Go heap ≤ 4 MB. RSS reported but not gated: on macOS it counts shared framework pages (88 MB for a 34 MB-footprint process) | 30.6 MB after a 60 s idle. Peaks are reclaimable graphics memory: 84 MB at startup, ~100 MB after unpaced animation into an occluded layer (re-measure with a live display) | Zed 118 MB, Finder 114 MB |
| Bytes per file entry | minimize | ≤ 64 B incl. name (excl. icons) | 24 B + name bytes | — |
| Binary (stripped) | < 10 MB | < 10 MB hard, ≤ 5 MB target | spike 2.52–2.76 MiB on all six targets (darwin/arm64 2.64 MiB) | Zed 260 MB, Gio hello 6.1 MB, File Pilot 2.46 MB |
| Input-to-photon | measure | report event-timestamp → present time from OS present feedback (`MTLDrawable.presentedTime`, DXGI frame statistics, `wp_presentation`); camera measurement if hardware becomes available | — | — |

## 12. Testing and CI

- GitHub Actions matrix (`.github/workflows/ci.yml`; not pushed, since pushing needs approval):
  - `macos-15` (arm64; amd64 ffi tests under Rosetta)
  - `windows-2025` (D3D11, WARP when there is no GPU)
  - `ubuntu-24.04` + Xvfb + Mesa llvmpipe (X11)
  - `ubuntu-24.04` + headless Weston (Wayland; Weston lacks fractional-scale-v1, so integer scale only)
- The spike gate is `spike -bench -gate -timeout 120s` on every OS. It fails if:
  - anything is presented while idle;
  - any of the 600 warmed-up animated frames allocates (exact `ReadMemStats` deltas);
  - frames are not paced by the OS frame signal;
  - the run hangs.
- Gates: build + vet on all targets; cross-compile all targets from each
  host with `CGO_ENABLED=0`; unit tests; allocation-exactness tests; spike
  smoke run (open window, wake from worker, present N frames, exit)
  with startup/RSS/size recorded as artifacts; binary-size table per
  platform with a regression threshold.
- Golden-image renderer tests are few and tolerant (per-channel
  threshold, only after phase 2).

## 13. Risks

| Risk | Impact | Mitigation |
|---|---|---|
| Own ffi trampolines depend on runtime internals (`runtime.cgocall`, `crosscall2`) via purego's fakecgo | Breakage on a Go upgrade | The Go team keeps these linknames for purego ("hall of shame" list); pin Go minor; CI on tip-1; fallback = purego slow path (allocating) or cgo |
| purego v0.11.1 darwin signal-mask bug (fakecgo uses Linux `SIG_SETMASK`) | Crash under profiling signals | Pin to fix commit `d456b1b` until a tagged release (D4) |
| macOS time to visible pixels is unmeasured | Startup budget | Phase 1 measured only to first present (134.9 ms median): the display was asleep and the session locked, so the occlusion signal never reported "visible". Phase 0's "on screen" numbers measured window-server registration only. Re-measure with a live display early in phase 2 |
| Windows and Linux not runnable here | Untested code paths | Small, reference-checked bindings (vtable order checked against SDK headers); CI once push is approved; Wine/VM optional |
| Wayland via dlopen'd libwayland + EGL without cgo | ABI details (struct layouts, variadics) | Use the non-variadic `wl_proxy_marshal_array_flags`; generated interface tables; smoke tests under Weston in CI |
| Linux text quality vs binary size (D9) | No hinting/LCD AA on 1× Linux displays; +2–3 MB on Linux | Option B chosen. Tune gamma and stem darkening in the Go rasterizer; lazy emoji tables (the probe's +190 MB `sbix` load) |
| COLRv1 emoji (Windows Segoe UI Emoji, some Linux Noto builds) | Missing emoji | DirectWrite renders COLR; on Linux prefer CBDT emoji fonts, COLRv1 renderer later |
| Startup drawable memory scales with window size | Footprint budget | 2 drawables instead of 3; budget defined at a fixed window size |
| Live resize + own event loop on macOS | Content stretching | Render from `windowDidResize:` (callback path is zero-alloc) |
