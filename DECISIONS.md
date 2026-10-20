# Decisions

Each entry records the decision, the evidence, the alternatives and the
cost. "M:" points to measurements in
[docs/measurements/phase0-macos.md](docs/measurements/phase0-macos.md);
"P1:" points to
[docs/measurements/phase1-macos.md](docs/measurements/phase1-macos.md).

Status: phase 0 was approved, and the review answered D1 (macOS 14), D4
(pin purego), D5 (`CADisplayLink` only) and D9 (Linux option B). Phase-1
updates are marked "Phase 1:".

## D1. Toolchain and targets

- Go 1.27.x, module `gonativeui` (placeholder; final name needs your approval).
- `CGO_ENABLED=0` on every target. Every target cross-compiles from any host.
- Targets:
  - darwin/arm64 + amd64, **macOS 14+** (decided at the phase-0 review). Go 1.27's floor is 13; we raise it so `NSView` `CADisplayLink` is always available and no `CVDisplayLink` fallback is needed
  - windows/amd64 + arm64, Windows 10+ (Go's floor since 1.21)
  - linux/amd64 + arm64 with glibc. purego makes Linux binaries dynamically linked against `libc.so.6`/`libdl.so.2`, so musl is not supported out of the box.
- Release builds use `-trimpath -ldflags="-s -w"`. CI records the stripped size per target.

## D2. Dependency and cgo policy

A Go module dependency is allowed only if it is a solved, well-scoped piece
we would not do better ourselves, builds with `CGO_ENABLED=0`, and has a
permissive license. Every dependency gets a row here.

You must approve, before it happens:
- any cgo dependency on Windows;
- any bundled C library;
- dropping a platform.

On Linux, system libraries are loaded with `dlopen` at runtime and never
bundled. GTK, Qt, webviews, Electron/Wails, GLFW, SDL and libdecor are
excluded.

| Dependency | Why | Size cost (stripped) | cgo | License |
|---|---|---|---|---|
| `github.com/ebitengine/purego` | `Dlopen`/`Dlsym`; the `fakecgo` runtime bootstrap that makes C calls and callbacks legal without cgo (sets `runtime.iscgo`, creates threads with `pthread_create`, provides `crosscall2`); Objective-C class registration at startup. darwin + linux only. | +236 KB (Dlopen + 1 call), +509 KB with `objc` on darwin/arm64; +131–180 KB on linux | no | Apache-2.0 (some files BSD-3 from Go) |
| `github.com/go-text/typesetting` | Linux text shaping, bidi and segmentation (**D9 option B, chosen**). Linux builds only. To be added in phase 2. | +2.1–3.4 MB (probe) | no | Unlicense OR BSD-3; `harfbuzz/` is MIT |

**Phase 1: `golang.org/x/sys` dropped.** It was planned for Windows DLL
loading and Linux syscalls. The Windows and Linux backends ended up needing
only the standard library's `syscall` package:
- `LoadLibraryEx` with `LOAD_LIBRARY_SEARCH_SYSTEM32`, `SyscallN`, `NewCallback`;
- `eventfd2`/`poll` via `syscall.Syscall`.

`go.mod` now requires only purego. If phase 4 needs `inotify`/`statx`
helpers, x/sys comes back with its own row here.

System libraries we call (OS-provided, not bundled):

- **macOS:** AppKit, Foundation, CoreFoundation, QuartzCore, Metal, CoreText, CoreGraphics, libobjc, libSystem.
- **Windows:** kernel32, user32, gdi32, shcore, dwmapi, imm32, shell32, ole32, d3d11, dxgi, dwrite.
- **Linux:**
  - required: libc, libEGL, libGL or libOpenGL (libGLESv2 fallback), and libwayland-client + libwayland-egl + libxkbcommon (Wayland) or libX11 (X11)
  - optional: libwayland-cursor, libXcursor
  - phase 2 (D9 option B): libfontconfig, for font discovery and fallback only

Third-party code we read but did not copy: Gio (MIT/Unlicense), GPUI
(Apache-2.0), RAD Debugger (MIT), Ebitengine (Apache-2.0). If we adapt code
later, the file gets a header and NOTICE gets an entry.

## D3. Foreign calls: own zero-allocation trampolines on purego's bootstrap

**Decision.** Calls on the frame path (Objective-C messages, Wayland/EGL/GL
calls, callbacks) go through `internal/ffi`:
- an assembly trampoline called with `runtime.cgocall`;
- a fixed `Frame` holding integer, float and stack slots, in memory that does not move: a global or heap frame used by one thread at a time, or, inside a callback, the invocation's own frame on the C stack (D36);
- a table of callback entry points (1024 since phase 6, D36) that reaches a non-reflective Go dispatcher through `crosscall2`.

purego's convenience APIs (`RegisterFunc`, `objc.Send`, `NewCallback`)
are used only at setup.

**Evidence (M:1).** On darwin/arm64:
- purego allocates on every path: `SyscallN` 1 alloc / 55 ns, `RegisterFunc` 3 / 148 ns, `objc.ID.Send` 5–7 / 195–208 ns, `objc.Send[T]` 8 / 277 ns, callbacks 4 / 233 ns.
- `Send[T]` even calls `RegisterFunc` on every call (`objc_runtime_darwin.go:161-172`, v0.11.1).
- Our probe trampoline: 15.8 ns int call, 21.8 ns `objc_msgSend` with mixed int/float arguments and a float return, 48.6 ns callback, **0 allocations** in each case.
- cgo reference: 14.9 ns call, 43.6 ns callback.
- Correctness was tested for int, float, mixed-register, float-return and callback paths (`probes/ffi2`).

**Alternatives rejected:**
- **purego as-is:** violates the zero-allocation rule. It also can't mix int and float arguments in `SyscallN`, panics on float callback returns, and has no C-variadic support on Apple arm64.
- **cgo on macOS/Linux:** zero-alloc and well supported, but it needs a C toolchain and target SDK for every build. That loses cross-compilation (osxcross for darwin) and adds build time; you asked to avoid it.
- **Writing our own `fakecgo`:** high risk, no benefit, since purego maintains it.

**Cost and risk:**
- Roughly 150 lines of assembly per architecture (arm64 + amd64; darwin and linux share them).
- It depends on `runtime.cgocall` and `crosscall2` linknames. The Go runtime keeps `cgocall` linkable explicitly for purego (`runtime/cgocall.go:126-132`).
- Apple arm64 packs stack arguments by natural alignment, and variadic arguments go on the stack. The `Frame` builder handles both cases explicitly; Linux arm64 uses 8-byte slots.
- Windows doesn't use this path; it uses `syscall.SyscallN`/`NewCallback`. Their allocation counts are still to be verified on Windows CI.

**Phase 1 (P1).** Implemented in `internal/ffi`, with `ffi_arm64.s` and
`ffi_amd64.s` shared by darwin and linux.
- **Speed and allocations:** 20.5 ns integer call, 21.1 ns mixed int/float, 56.6 ns callback, 0 allocations on each path.
- **Tests:** pass on darwin/arm64 and on darwin/amd64 under Rosetta. Linux runs them in CI (not yet run).
- **C addresses:** `ffi.PtrFromC` converts C-owned addresses to `unsafe.Pointer` without tripping vet's `unsafeptr` check. It must never be used for Go heap addresses.
- **Windows gate:** the spike's `-gate` flag covers the `SyscallN` frame path through the exact allocation delta.

## D4. purego version

**Decision (approved at the phase-0 review).** Pin `github.com/ebitengine/purego` to commit
`d456b1b` (pseudo-version
`v0.12.0-alpha.1.0.20261015130536-d456b1bb95e8`), then move to the next
tagged release as soon as one exists.

**Evidence.**
- In v0.11.1, `fakecgo` on darwin/BSD uses Linux's `SIG_SETMASK=2` (`internal/fakecgo/libcgo.go:22-29`). On Darwin, 2 means `SIG_UNBLOCK`.
- Every runtime thread is created through `fakecgo`, so profiling signals can crash with "signal received on thread with no signal stack" (purego #552).
- The fix is PR #553, merged 2026-10-15 as `d456b1b`, and not yet released.

**Alternative.** Stay on v0.11.1 and never enable CPU profiling. That's
unacceptable, because we profile scrolling.

## D5. macOS platform

**Decision:**
- AppKit through the Objective-C runtime on `internal/ffi`.
- Our own event loop: `nextEventMatchingMask:untilDate:inMode:dequeue:` followed by `sendEvent:`, never `[NSApp run]`.
- Our own `NSView` subclass, registered at runtime and implementing `NSTextInputClient`. All its methods share one IMP.
- Worker wake: `postEvent:atStart:` with an application-defined event, coalesced through an atomic flag.
- `CAMetalLayer` with 2 drawables.
- Frame pacing: `CADisplayLink` from `NSView` only. No `CVDisplayLink` fallback, since the minimum is macOS 14 (D1).
- The window is shown with `orderFrontRegardless`. Under macOS 14 cooperative activation, a process started from a background shell can't activate itself, and `makeKeyAndOrderFront:` alone may leave the window behind others.

**Evidence (M:2):**
- The purego probe shows the approach works with no cgo: 2.1 MB binary, first frame committed 83 ms after `main`, 0 CPU while idle, 34 MB footprint.
- AppKit setup dominates startup: `NSApplication` ≈ 26 ms, the first `NSWindow` ≈ 26 ms, order-front ≈ 12 ms, `finishLaunching` ≈ 10 ms. Metal device creation is 0.17 ms, and the clear plus present is 4 ms.
- Ebitengine uses the same approach in production: `objc.RegisterClass` for `NSView` + `NSTextInputClient`, `PostEmptyEvent` via `postEvent`, and `CAMetalDisplayLink`/`CVDisplayLink`. That is according to the purego/Ebitengine research report; we did not copy their code.

**Phase 1 (P1).** Measured with the display asleep and the session locked,
so the window is occluded and `CADisplayLink` never fires.
- **Startup:** exec → first present has a median of 134.9 ms.
- **Idle:** 0 presents and 4.2 ms CPU per 60 s.
- **Wakes:** worker → loop wake p99 is 152–340 µs across runs.
- **Frames:** 0 allocations over 600 unpaced frames (exact `ReadMemStats` deltas, D11).
- **Memory:** idle footprint is 30.6 MB. The 84 MB startup peak is reclaimable graphics memory and drops to ~34 MB within about 2 s.

The phase-0 "on screen" numbers measured window-server registration, not
visible pixels. Vsync tracking, time to visible, and the footprint after
animating (101–105 MB, unpaced into an occluded layer) need a live display.

## D6. GPU API per OS

**Decision:**
- macOS: **Metal**.
- Windows: **Direct3D 11**, feature level 10_0+ with WARP fallback.
- Linux: **OpenGL 3.3 core through EGL**, accepting a GLES 3.0 context too, so shaders target the common subset.

A CPU fallback is not planned. Vulkan on Linux is deferred: we'll only
reconsider it if GL becomes a measured bottleneck.

**Evidence:**
- **Zed on Linux** requires Vulkan. Its docs list failure modes such as `NoSupportedDeviceFound`, `ERROR_INITIALIZATION_FAILED`, "GPU Crashed" and amdvlk launch failures, plus GPU selection workarounds ([zed.dev/docs/linux](https://zed.dev/docs/linux)).
- **Zed on Windows** moved from Vulkan to DirectX 11 because "users … didn't run on their machines due to the Vulkan dependency"; DX11 is "guaranteed to be available on Windows 7 or later, including Windows VMs", with lower memory ([blog, 2025-08-19](https://zed.dev/blog/windows-progress-report); [PR #34374](https://github.com/zed-industries/zed/pull/34374): better compatibility, vsync, device-loss handling, lower memory).
- **Gio** picks GLES via EGL first and Vulkan second on Linux, and D3D11 on Windows (research report: `app/os_wayland.go:2089-2108`, `app/d3d11_windows.go:27`).
- **Ebitengine** uses GL on Linux and D3D11 by default on Windows.
- Our workload (a few thousand instanced quads, two atlases, 1–4 draw calls) gains nothing from Vulkan's explicitness. GL via EGL runs on Mesa (including llvmpipe in CI and VMs), NVIDIA, and virgl in VMs.

**Alternatives:**
- **Vulkan on Linux:** more code, worse availability in VMs and on old GPUs.
- **wgpu/Dawn:** a large C++/Rust dependency that needs bundling or cgo.
- **D3D12 / Metal 3 features:** not needed for this workload.

## D7. Windows presentation

**Decision:**
- Swap chain: `DXGI_SWAP_EFFECT_FLIP_DISCARD`, 2 buffers, BGRA8.
- Latency: `DXGI_SWAP_CHAIN_FLAG_FRAME_LATENCY_WAITABLE_OBJECT` with `SetMaximumFrameLatency(1)`. The loop waits on the waitable handle together with the message queue (`MsgWaitForMultipleObjectsEx`).
- `Present(1, 0)` while animating; no present at all when idle.
- Tearing (`ALLOW_TEARING`) is off by default.
- Per-Monitor v2 DPI is set by the manifest, with `SetProcessDpiAwarenessContext` as a fallback.

**Evidence:**
- **Gio** uses the legacy blit model (`DXGI_SWAP_EFFECT_DISCARD`, 1 buffer) with no waitable object (`internal/d3d11/d3d11_windows.go:1646-1660`).
- **Ebitengine** uses `FLIP_SEQUENTIAL`, 2 buffers, no waitable object (`graphics_windows.go:305-320`; `dxgi_windows.go:659-661`).
- Microsoft recommends flip-model swap chains plus the waitable object for lowest latency ("Reduce latency with DXGI 1.3 swap chains"; that guidance is long-standing, not re-fetched this session).

**Phase 1.** Implemented in `gpu/d3d11_windows.go` and
`platform/platform_windows.go`, cross-compiled only; runtime unverified.
- **Swap effect:** `FLIP_DISCARD`, falling back to `FLIP_SEQUENTIAL` on Windows 8.x.
- **Waitable:** the frame-latency waitable is consumed once at setup, because it starts signalled (as in Microsoft's DirectXLatency sample). After that it is waited on together with the message queue.
- **DPI:** set with `SetProcessDpiAwarenessContext(PER_MONITOR_AWARE_V2)` at runtime. The embedded manifest (`.syso`) is deferred, so no manifest yet.
- **Occlusion:** `DXGI_STATUS_OCCLUDED` makes `Present` return false; the spike backs off 50 ms instead of spinning.
- **Not yet:** device-loss recovery beyond reporting it, and redraw during the live-resize modal loop.

**Cost.** We write the COM bindings by hand for the D3D11, DXGI 1.2–1.5 and DirectWrite interfaces we use. Vtable order is checked against the Windows SDK / mingw-w64 headers, and each interface gets a comment citing the header line ranges. Size: Gio's hand-written D3D11+DXGI binding is 1,694 lines; ours should be smaller (no compute, no generic GPU abstraction).

## D8. Linux windowing

**Decision:**
- **Wayland first.**
  - libwayland-client via `dlopen`, using `wl_proxy_marshal_array_flags` (non-variadic) and one `wl_proxy_add_dispatcher` callback for every object.
  - Protocol interface tables (`wl_interface`/`wl_message`) come from protocol XML through a small generator of our own, emitted as static Go data.
  - Protocols: `xdg-shell`, `xdg-decoration`, `fractional-scale-v1` + `viewporter`, `cursor-shape-v1`, `text-input-v3`, `wl_data_device`.
  - Keymaps through libxkbcommon.
  - Our own client-side decorations whenever the compositor doesn't decorate (GNOME).
- **X11 fallback** via `libX11` `dlopen` when there is no `WAYLAND_DISPLAY` or the connection fails.

**Evidence:**
- **EGL's Wayland platform needs a libwayland `wl_display*`.** So does `VK_KHR_wayland_surface`. A pure-Go wire implementation therefore can't be used with a GPU context, unless we render offscreen and pass dmabufs via `zwp_linux_dmabuf_v1`, which is much riskier.
- **fractional-scale-v1** is supported by Mutter (GNOME 44+), KWin, Sway 1.9+ and Hyprland, but **not Weston** ([wayland.app](https://wayland.app/protocols/fractional-scale-v1)). CI under Weston therefore exercises the integer path.
- **Gio** only supports integer Wayland scale (research report, `os_wayland.go:636-639`). Doing better than Gio here is a concrete win.
- **Ebitengine** has no native Wayland backend; it relies on XWayland.

**Phase 1.** Implemented, cross-compiled only; runtime unverified until CI
runs on Weston and Xvfb.

Done:
- **Wayland:** xdg-shell, xdg-decoration, `fractional-scale-v1` + `viewporter` (with an integer `preferred_buffer_scale` fallback), `wl_output`, and an xkbcommon keymap.
- **Interface tables:** generated by `internal/wl/gen.go` (a `go:build ignore` program) from protocol XML fetched 2026-10-16. The XML is not vendored; the generator header lists the source URLs.
- **X11 fallback:** libX11 plus EGL on the Xlib `Display*`.
- **Backend choice:** `GNUI_BACKEND=wayland|x11` forces one backend.

Deferred:
- cursor-shape-v1, text-input-v3, `wl_data_device`;
- client-side decorations: today, dragging the top 32 px moves the window;
- live-resize redraw.

**Alternatives:**
- **cgo + libwayland (Gio):** loses cross-compilation.
- **libdecor:** loads GTK, so it's excluded.
- **Pure-Go X11 wire protocol:** EGL/GLX need an Xlib `Display*`.

## D9. Text stack

**Decision:**
- macOS: **CoreText**.
- Windows: **DirectWrite**.
- Linux: **option B (chosen at the phase-0 review):** go-text/typesetting for shaping, bidi and segmentation; fontconfig via `dlopen` for discovery and fallback; a Go rasterizer.
  - Option A (system fontconfig + HarfBuzz + FreeType via `dlopen`) was the agent's recommendation, and is kept below for the record.
  - Consequences we accept: no hinting and no LCD AA on Linux.
  - Phase 2 must solve two problems the probe found:
    - load color-emoji tables lazily, never the whole table, given the +190 MB `sbix` result;
    - avoid re-statting every font at startup; use fontconfig's cache for discovery instead of fontscan's index.

Shaped-run and glyph caches are shared above the backend (ARCHITECTURE §7).

**Evidence (M:6, plus the Gio/go-text research report):**

| Criterion | CoreText (measured) | DirectWrite | FreeType+HarfBuzz+fontconfig | go-text + Go raster (measured) |
|---|---|---|---|---|
| Quality: hinting / LCD AA | n/a on macOS (no LCD AA since 10.14); native look | ClearType + hinting, matches the OS | hinting + LCD filter per user's fontconfig | **none / none**; x/image/vector is grayscale only, and the only Go hinter (golang/freetype) is archived since 2017 |
| Fallback, CJK, emoji | OS cascade: PingFang, SF Arabic, Apple SD Gothic, Apple Color Emoji, all correct | `IDWriteFontFallback`; color via `TranslateColorGlyphRun` (COLR) | fontconfig fallback per distro config; CBDT via FreeType; COLRv1 needs our own renderer | emoji fall back to `.LastResort` unless "emoji" is queried; PingFang not found; Han switches font per character; COLR not rendered |
| Bidi | yes | yes | needs a UAX #9 implementation (fribidi not guaranteed) | yes (UAX #9, Unicode 17 tests pass) |
| Speed | shape 10.6 µs ASCII / 38.5 µs mixed (incl. ~1.5 µs purego overhead), raster 3.4 µs per glyph at 26 px | not measured | not measured | shape 4.5 µs ASCII / 5.9 µs mixed; raster 0.66 µs per glyph at 13 px |
| Memory | 13.8 MB peak process footprint | not measured | not measured | **+190 MB Go heap** once Apple Color Emoji is loaded (whole `sbix` table read in) |
| Startup | no index | no index | fontconfig cache (system-maintained) | fontscan index: 156 ms cold, 43 ms warm (re-stats every font each start) |
| Binary | +0 | +0 | +0 | +2.1–3.4 MB |
| cgo | no (ffi) | no (COM) | no (dlopen) | no |

**Reasoning:**
- On macOS and Windows the native stacks win on every criterion except raw shaping speed.
- Shaping speed is not on the frame path: runs are cached and shaped on workers, so 10 µs per name × ~60 newly visible rows is at most ~0.6 ms, off the main thread when prefetched.
- On Linux, go-text is fast and pure Go, but it has no hinting or LCD AA. That is visible at 13 px on 1× displays, and Linux has the most 1× displays. Its fallback is also weaker than the desktop's own configuration.
- FreeType/HarfBuzz/fontconfig are present on every GNOME/KDE desktop. Strictly, though, they are desktop libraries, not "required by the OS". That's why this is your call.

**What we take from File Pilot.** File Pilot uses DirectWrite only for rasterization (per the task brief, unverified) because it targets one OS. We need real shaping and fallback on three OSes for CJK, Arabic and emoji file names, so we use each OS's full stack.

## D10. UI core

**Decision.** A Fleury/RAD-style immediate-mode builder over an ID-keyed,
pointer-free box cache, with our own per-axis layout passes, prev-frame
hit testing and virtualized lists (ARCHITECTURE §8).

**Evidence:**
- **RAD Debugger** (`src/ui/ui_core.h/.c`, MIT): box cache with per-build pruning; `UI_Size{kind, value, strictness}`; five layout passes per axis; `hot_t`/`active_t` animation with rate `1-2^(-60·dt)`; an `is_animating` signal; double-buffered build arenas.
- **GPUI** (Apache-2.0): rebuilds its element tree every frame, keeps state keyed by `(GlobalElementId, TypeId)`, and hit-tests hitboxes back to front; it uses Taffy for layout.
- **Gio:** reuses op buffers so a static label lays out with 0 allocations (3.4 µs per 100 runes); a dynamic label allocates 466 times per layout.

**Alternatives:**
- **A retained widget tree:** more state-synchronisation bugs, and harder to keep allocation-free.
- **Flexbox via a Taffy port:** too general, and costs per-node work and allocations.

## D11. Memory model and enforcement

**Decision.**
- Bulk data is pointer-free (offsets into a byte buffer).
- Index handles instead of pointers.
- Double-buffered frame arenas.
- No `GOEXPERIMENT=arenas`.
- Zero-allocation frames are a CI gate. The gate asserts exact allocation deltas over warmed-up, isolated frame sequences; `testing.AllocsPerRun` is used only for micro hot paths.
- **Phase 1 correction: the exact deltas come from `runtime.ReadMemStats`** (`Mallocs`, `TotalAlloc`, `NumGC`), not `runtime/metrics`.
  - The `/gc/heap/allocs:*` counters are credited a whole span at a time on per-P cache refill, so allocations within an already-credited span go unseen.
  - Measured: 600 small allocations after `runtime.GC()` read as 0 via `runtime/metrics`, and as 600 via `ReadMemStats`, which flushes the caches first.
  - A per-frame allocation planted in the spike reads 601 objects with the corrected gate (P1).
  - `runtime/metrics` stays fine for GC-cycle and live-heap measurements (M:4).

**Evidence (M:4).** For 1M entries, Go strings cost 8.94 ms of GC CPU per
cycle and leave 1,000,259 heap objects. Offsets cost 0.16 ms and leave 241
objects.

## D12. File-system APIs

**Decision:**
- macOS: `getattrlistbulk`.
- Windows: `GetFileInformationByHandleEx(FileFullDirectoryInfo)`, falling back to `FindFirstFileExW` (basic info, large fetch). Chosen by measurement in phase 4 (CI, 100k entries, 2026-10-17): 57.6 ms on amd64 and 55.0 ms on arm64; `FileIdExtdDirectoryInfo` was 1–6 % slower and `FindFirstFileExW` 10–34 % slower.
- Linux: `getdents64` + `statx`. Measured in phase 4 (CI, 100k entries): 1.4–2.0× faster than `os.ReadDir` + `Info` (amd64 236–295 ms vs 333–455 ms; arm64 147–148 ms vs 208–294 ms on the runners' disks).
- Watching: kqueue on macOS (D26 replaced FSEvents), `ReadDirectoryChangesW`, inotify.
- Results are cached by (path, mtime).

**Evidence (M:5).** `getattrlistbulk` is 88 ms against 198 ms for
`ReadDir`+`Info` on 100k entries, and 7.7 ms against 15.7 ms on 10k.
Directories do not return `ATTR_FILE_DATALENGTH`; parse by the
returned-attributes bitmap.

## D13. How we measure

- **Startup, headline: half-bounce or better** (user goal, 2026-10-16; ARCHITECTURE §3 "Content-first startup").
  - On macOS: from `open` of a `.app` bundle until the first frame with complete content is on screen, compared against the Dock bounce period. Both come from one ScreenCaptureKit recording at ≥ 60 fps, the same method as Bounce Lab.
  - On Windows/Linux: the same threshold in milliseconds, from launch request to first content frame, using present feedback (DXGI frame statistics, `wp_presentation`).
  - Phase 1 found that the window-list timer below only proves window-server registration, not pixels; it remains a secondary signal.
- **Startup, secondary signals:**
  - an external timer from exec until the child's first layer-0 window is listed by the window server (`probes/startwin`, `CGWindowListCopyWindowInfo`);
  - an internal timeline (process start → first present).
  - Warm runs are reported as a median of ≥ 5. "Cold after reboot" is reported separately when available. The first launch of a new binary is excluded, since it includes the Gatekeeper scan.
- **Memory:**
  - macOS `phys_footprint` (Activity Monitor's "Memory"), split into graphics and non-graphics;
  - RSS reported but not gated, since it includes shared framework pages;
  - Windows: Private Working Set + commit. Phase 1's `internal/sysinfo` reports only commit (`PrivateUsage`) as the footprint and the working set as RSS. The private working set (`PROCESS_MEMORY_COUNTERS_EX2` or `QueryWorkingSetEx`) is a phase-2 follow-up;
  - Linux: PSS (`smaps_rollup`).
- **Idle:** CPU-time delta over 60 s, presents counted in-app, and wakeups on macOS. The spike reports package-idle wakeups (`ri_pkg_idle_wkups`, the same counter as `top -stats idlew`) separately from all interrupt wakeups. Windows uses ETW/PresentMon; Linux uses `/proc/<pid>/stat` and `perf trace`.
- **Input-to-present:** the OS event timestamp until the OS-reported present time (`MTLDrawable.presentedTime`, DXGI frame statistics, `wp_presentation` feedback). This is reported as input-to-photon minus scan-out and device latency. A camera measurement is optional later.
- **Baselines on this Mac:**
  - Zed: 302 ms to window, 118 MB.
  - Finder: 114 MB.
  - The Gio v0.10.3 hello app crashes at launch on macOS 27.2, so it has no baseline.
- File Pilot, Explorer and Nautilus need Windows/Linux machines.

## D14. Shaders

- MSL is compiled at runtime from embedded source, on a worker in parallel with AppKit init. Phase 2 measures the cost.
- HLSL is precompiled to DXBC in Windows CI and embedded.
- GLSL source is compiled by the driver.
- One shader source per backend; there is no cross-compiler dependency.

## D15. CI

GitHub Actions matrix, as in ARCHITECTURE §12. It is written in phase 1 but
**not pushed**: pushing to GitHub needs your approval.

**Phase 1.** `.github/workflows/ci.yml` has four jobs:
- **cross:** gofmt; vet and stripped builds for all six targets; a binary-size table; a 10 MiB gate.
- **macOS 15 arm64:** tests (plus amd64 ffi tests under Rosetta), ffi benchmarks, and the spike.
- **Windows 2025 (amd64) and Windows 11 arm64:** the spike on D3D11, which falls back to WARP on runners.
- **Ubuntu 24.04, amd64 and arm64:** the spike on X11 (Xvfb) and on Wayland (headless Weston), both with Mesa llvmpipe.

Every spike job runs `spike -bench -gate -timeout 120s`. The run fails if:
- the idle phase presented anything;
- the warmed-up animated frames allocated any object or byte;
- the 600 warmed-up frames were not all produced, or were not paced by the OS frame signal;
- the run hangs.

Runner timings are uploaded as artifacts but are not gated.

The repository is public (user, 2026-10-16), so hosted arm64 runners are
available. That covers Linux arm64's 8-byte stack-slot ABI and Windows
arm64's `SyscallN` path at runtime.

## D16. Licensing

- Our code: license to be chosen with the name.
- NOTICE lists:
  - purego (Apache-2.0), which our ffi technique follows (runtime.cgocall + crosscall2).
  - x/sys was removed in phase 1 (D2).
- No third-party source is copied at present.

## D17. Visual quality bar

**Decision (user, 2026-10-16).** Interfaces must look modern. The
benchmark is a recreation of Superlogical Rex's frontend (`cmd/rexlook`).
The measured spec and acceptance method are in
[docs/design/rex-benchmark.md](docs/design/rex-benchmark.md).

- **Phase 2:** a static frame on macOS, compared against the public screenshot (flat-region ΔE ≤ 3, geometry ±1 px at 2×).
- **Phase 3:** interactive, under the same zero-allocation, idle and half-bounce gates as everything else.

**Product look (user, 2026-10-16): the original Rex style.** The file
browser and the framework's default theme use the Rex look, not a
derivative.

**Boundaries.**
- The reference image stays out of git.
- No Superlogical or third-party assets, logos, fonts or code: icons are system symbols or our own, and fonts are system fonts.
- **Risk (flagged, accepted by you):** shipping another product's distinctive look publicly can draw trade-dress objections. The public repo makes the resemblance visible early. If that becomes a concern, the theme is data (colours, radii, spacing) and can be changed without touching the renderer.

**Cost.** Renderer additions:
- multi-stop and four-corner gradients;
- fade masks for truncated text;
- device-pixel hairlines;
- layered shadows;
- system-symbol icons;
- custom window chrome per OS.

Linux needs an icon set of our own, with a licence to decide.

## Phase-0 review answers

1. **D9 Linux text:** option B, pure-Go go-text.
2. **D4:** pin purego to `d456b1b`.
3. **Testing without Windows/Linux machines:** cross-compile only for now. Numbers stay "not measured".
4. **Minimum macOS:** 14.

## Questions for the phase-1 review

See the go/no-go in [docs/measurements/phase1-macos.md](docs/measurements/phase1-macos.md).

1. **Live display.** Can you unlock the Mac and keep the display awake for a measurement run? That gives vsync intervals, time to visible, and the footprint after animating.
2. **CI.** May we create a private GitHub repo and push, so the workflow produces the first Windows and Linux runtime evidence?
3. **Key identity for shortcuts.** Should we match by physical key position (scancode: stable across layouts, the same Ctrl+Z spot on AZERTY) or by layout keysym (Ctrl+Z follows the printed letter)? The platform layer reports both; the question is the default for the UI layer's shortcut matcher.
4. **Wayland protocol XML.** Should we vendor the XML files next to `internal/wl/gen.go` (reproducible, under 100 KB for the four files, MIT-licensed) or keep fetching them from the URLs in the generator header?

## D18. macOS frame delivery: CAMetalDisplayLink (phase 2)

**Decision.** After the first frame, frames come from a `CAMetalDisplayLink`
on the surface's layer (`preferredFrameLatency` 1). The first frame uses
`nextDrawable`, because it is drawn before the run loop first spins.

**Evidence** (MacBook Pro M3 Pro, 120 Hz, `docs/measurements/phase2-macos.md`):
- **Before:** with the view's `CADisplayLink`, `sample` showed the main thread blocked in `nextDrawable` for ~96 % of each frame.
- **After:** process CPU per frame fell from 1.89–2.09 ms to 1.24–1.48 ms, below a native Objective-C app using the old approach (1.70–1.79 ms). Intervals: p50 8.33 ms, p99 ≤ 8.7 ms.
- 2 vs 3 drawables and latency 1 vs 2 made no measurable difference.

**Consequences:**
- A layer driven by the link throws on `nextDrawable`, so after the first frame `BeginFrame`/`Present` outside a tick return false; apps draw on `EvFrame`.
- With an occluded window or a sleeping display the link stops: nothing is drawn, and the bench times out instead of running unpaced.
- The transient footprint while animating is ~20 MB higher, which looks like two extra full-size drawables. Idle is unchanged.

**Live-resize exception (phase 3, 2026-10-17).** While the user drags a window edge, AppKit runs its own loop, so frames come from somewhere else:
- At `windowWillStartLiveResize:` the metal link is invalidated and released, and a plain view `CADisplayLink` takes over.
- The app's frame handler (`Window.SetFrameHandler`) is called from `windowDidResize:` and from that link's ticks.
- `gpu.Surface` sees `platform.InLiveResize()` and gets drawables with `nextDrawable`. It presents with `presentsWithTransaction`, so each frame commits together with the window's new size.
- At `windowDidEndLiveResize:` a new metal link is attached.
- **Evidence:** a MacBook probe showed `nextDrawable` works again after the link is invalidated (39/39 presents), and the new link paces again (20/20). The layer's `contentsGravity` is top-left, so a frame that isn't redrawn yet is never stretched.
- **Amendment (phase 4, 2026-10-17): flush after every live present.** Your hand resize froze for 1–2 s at a time, in rexlook and Pilot alike (and in the phase-3 build). A presented drawable stays in use until its transaction commits, and `windowDidResize:` plus a display-link tick could draw twice in one run-loop turn; the pool then ran dry and `nextDrawable` blocked for its full 1 s timeout (measured 1003 ms with `GNUI_TRACE_LIVE=1`). `gpu.Surface` now calls `[CATransaction flush]` right after a live present: the window's new size and the frame still commit together, and the implicit transaction never holds more than one present. (A flush cannot commit an explicit transaction AppKit may hold open around a callback; the traces and the hand test show none blocking on this MacBook, and `-live-resize-test` in CI would catch a starved pool on the runner's macOS.) Slowest step after the fix: 27 ms by hand, 19–24 ms in the simulation. `SimulateLiveResize` now also draws as a tick does after each step, so `-live-resize-test` reproduces the bug (1005 ms without the fix) and fails any step over 250 ms; the macOS CI job runs it for rexlook and Pilot.

## D19. Measurement tools may be Objective-C

**Decision.** Development and measurement tools that aren't part of the
framework or its apps may be Objective-C built with clang. Current tools:
- `probes/nativebaseline`: the native AppKit + Metal baseline;
- `tools/bouncerec`: the ScreenCaptureKit half-bounce recorder.

**Reasoning.** The no-cgo rule protects the framework's build and
distribution. ScreenCaptureKit is block- and delegate-heavy, and driving it
through our FFI would cost days for a tool nobody ships. The framework and
`cmd/*` apps stay pure Go.

## D20. Linux text dependencies (phase 2)

**Added:**
- `github.com/go-text/typesetting v0.3.5`, pinned: shaping (the HarfBuzz port), bidi and font parsing, as D9 chose;
- `golang.org/x/image v0.23.0`, indirect: go-text requires it.

The rasterizer is our own (`text/raster.go`, signed-area coverage), and so is the CBDT colour-emoji reader (`text/cbdt.go`).

**D9 problems, resolved:**
- **Colour-emoji tables load lazily.** `text/sfnt.go` hides CBDT, sbix and SVG from go-text, and one glyph's PNG is read on demand. In a test, a synthetic 24 MiB CBDT rasterized a glyph with 4.7 MiB allocated.
- **No font index:** fonts are found through fontconfig's own cache, via `dlopen`.

**Cost:** about +2.5 MB on a stripped Linux binary that uses `text`. It was measured with a minimal program at 4.2 MB, against a 1.7 MB purego baseline. Binaries that don't import `text` are unchanged.

**Limits:**
- COLRv1, SVG and sbix colour fonts aren't rendered.
- There is no hinting and no LCD antialiasing (D9).
- The UI font is fontconfig's `sans-serif`, not the desktop's GSettings/KDE setting.

**Windows note:** the DirectWrite backend needed `internal/com`, which holds hand-written COM vtable calls plus a small windows/arm64 trampoline. `syscall.SyscallN` doesn't load float registers on arm64, and three DirectWrite methods take `FLOAT` arguments.

## D21. Startup budget counts from program start (user, 2026-10-17)

**Decision.** The ≤ 150 ms startup budget (ARCHITECTURE §11) is measured from
program start (exec) to the first frame with complete content on screen.

**Reasoning.**
- On the test MacBook, LaunchServices spends ~110–150 ms before exec. We can't influence that time.
- The user-facing launch experience is covered by the half-bounce target, measured from the launch request with `tools/bouncerec`.

**Also decided (2026-10-17):**
- The static Rex frame is approved visually.
- Matching the reference's slightly bolder text is not needed.

## D22. An `app` package owns the frame loop (phase 3, 2026-10-17)

**Decision.** A new `app` package ties `platform`, `gpu`, `text` and `ui`
together:
- content-first startup;
- the event-driven, paced frame loop, including the occluded and asleep fallback;
- the live-resize frame handler;
- turning platform events into `ui.Input`;
- atlas sync;
- cursors from `ui` state;
- the bench and offscreen modes.

An app implements `Build(c *ui.Context)` on its own state and calls
`app.Run`.

**Reasoning:**
- The spike and `rexlook` each hand-wrote this loop.
- The phase-2 display-link stall was a bug in exactly that code: a frame was treated as ready after its tick's drawable had expired.
- One owned loop gets the frame contract right once, on every OS.

`cmd/spike` stays on the raw `platform` and `gpu` API as the
lowest-level benchmark.

**Constraints:**
- `ui` still never imports `platform`.
- Per-frame paths in `app` are zero-alloc. The `Build` callback is an interface method, not a closure created per frame.

## D23. Patched go-text kept in the repo (user, 2026-10-17)

**Decision.** `third_party/typesetting` holds the 12 packages of
`github.com/go-text/typesetting` v0.3.5 that the Linux text engine uses,
with our `zeroalloc.patch` applied. `go.mod` points the module there with a
`replace` directive. The user chose this over three alternatives: a fork plus
an upstream PR, accepting allocations on Linux, or system HarfBuzz.

**Why.** On Linux, shaping a string that isn't cached yet allocated inside
`harfbuzz.Buffer.Shape`:
- 3–770 objects per call, depending on the font;
- the causes were a closure per contextual lookup, the normalize context, and one slice per GPOS value record.

A list that scrolls through new text therefore allocated every frame. macOS
(CoreText) and Windows (DirectWrite) were already at zero.

**The patch** is about 35 lines in 4 files. It:
- turns the closures into small structs;
- keeps the normalize context in a field on `Buffer`;
- uses a stack array for value records.

With it, every test font shapes at 0 objects per call, about 1 µs per miss through the engine.

**Our own changes.** The engine also bypasses go-text's `Segmenter` and
`HarfbuzzShaper` for left-to-right text (`text/shape.go`):
- it does its own script itemization;
- it reuses one `harfbuzz.Buffer`;
- it caches one `harfbuzz.Font` per face.

Right-to-left and bidi text (phase 5, 2026-10-18): go-text's bidi package and its Arabic fallback plan, which upstream rebuilt on every call, now reuse their storage (patched in `third_party/typesetting`, in `zeroalloc.patch`), and `text/shape.go` no longer converts long text to a string. An RTL shaping miss went from 40 objects to 0 (`TestEngineRTLMissAllocatesNothing`).

**Licence.** go-text is Unlicense OR BSD-3-Clause. HarfBuzz's part is MIT.
Both licence files are kept in the copy.

**Verification:**
- `text/engine_alloc_test.go` asserts exactly 0 allocations per miss.
- `text/shape_test.go` checks the new path against go-text's reference pipeline, including a 6000-string fuzz.
- Update procedure: `third_party/typesetting/README.md`.

## D24. Cursors and titlebar drag come from UI regions (phase 3, 2026-10-17)

**Decision.**

- **Cursor.** A `ui` box carries a cursor (`NextCursor`/`PushCursor`). After layout, `ui` reports:
  - the cursor regions, as pixel rects in draw order, clipped by their ancestors;
  - the cursor of the pointer-captured box, as an override.
- **Titlebar drag.** Boxes flagged `ui.WindowDrag` (rex's titlebar background) become drag regions. Hit-testable boxes drawn above them are not.
- **Passing to the platform.** `app` hands the regions over with `platform.SetCursorRegions`, `SetCursorOverride` and `SetDragRegions`, only when they change.

**Per OS:**

- **macOS cursors:** one AppKit cursor rect per region, flattened so none overlap and the topmost wins, or a single bounds rect for the override. AppKit then shows the cursor on entering a rect and restores the arrow on leaving, as `NSSplitView` does.
- **macOS window drag:** the view refuses `mouseDownCanMoveWindow`. A left press in a drag region that the theme frame hit-tests to our view calls `performWindowDragWithEvent:`. A double click there does what `AppleActionOnDoubleClick` says.
- **Windows:** `WM_SETCURSOR` hit-tests the same regions.
- **Linux:** cursors not yet. On Wayland the drag map replaces the fixed 32 px move strip.

**Why.** Two earlier designs failed by hand on the MacBook:

- Setting the cursor directly with `[NSCursor set]`, plus one bounds-sized rect swapped on every change: the cursor flickered and the arrow never came back.
- Part of the cause was that `+arrowCursor` returns nil before `NSApplication` exists, so the arrow had been cached as nil. The cursors are now cached after the app object exists.
- Separate hit-test geometry in the app made the zones feel like 1-pixel lines. The regions now come from the same layout the clicks hit-test against.


## D25. Text editing: grapheme carets, caret blink, clipboard and IME scope (phase 4, 2026-10-17)

**Decision.**

- **Carets.** Every shaped run keeps one `uint32` per glyph with the byte offset of its cluster (`RunView.Clusters`, bit 31 = right-to-left). `CaretX`/`HitByte` refine shaper clusters to grapheme clusters at query time (our own UAX #29 on go-text's property tables, `text/unidata.go`), so caret stops are the same on CoreText, DirectWrite and go-text. Shaping itself stays cheap for lists that never need a caret.
- **Caret blink.** 500 ms half-period, only while a field has focus in the key window and for 10 s after the last interaction; then the caret is solid and the app is back to 0 wakeups. The field asks for its next frame through `Context.WakeAfter()`; it is not an animation, so the loop sleeps between transitions. The blink counts as activity, like any animation: exit criterion 6 (idle) is measured once it has stopped. `TextEdit.BlinkHalfNs`/`BlinkTimeoutNs` change it.
- **Clipboard.** Plain UTF-8 text. macOS `NSPasteboard`, Windows `CF_UNICODETEXT`. Linux has a process-local fallback for now; Wayland `wl_data_device` and the X11 `CLIPBOARD` selection come later.
- **IME.** macOS: the view implements `NSTextInputClient` (marked text → `EvPreedit`, commits → `EvText`, `firstRectForCharacterRange:` from `Window.SetIMERect`). Windows: imm32 (`WM_IME_COMPOSITION`), untested with a real IME. Linux: later (`zwp_text_input_v3`, XIM/IBus).

**Why.** A per-glyph offset is the smallest pointer-free record that supports carets on every backend. Grapheme refinement at query time keeps the cost on the one focused field, not on every shaped name. A 10 s blink window keeps the familiar caret while still letting an untouched app go fully idle.

**Known gaps.** No undo, no reconversion, `replacementRange` ignored (press-and-hold accents to be checked by hand), no bidi-aware arrow movement.

## D26. macOS directory watching uses kqueue, not FSEvents (phase 4, 2026-10-17)

**Decision.** The darwin watcher is one goroutine blocked in `kevent`:

- an `EVFILT_VNODE` watch on an `O_EVTONLY` fd for the directory (write, extend, attrib, link, delete, rename, revoke): a write means entries were added, removed or renamed;
- the same watch on every regular file in the directory, resynced after each directory write, so in-place writes (size, mtime) are seen. Cap: 2048 files per directory and 4096 in total, all-or-nothing per directory; above it, structure only;
- `ChangeGone` only after checking that the fd no longer names the directory at its path (`F_GETPATH` plus an inode comparison; `F_GETPATH` alone still answers for a deleted directory);
- an `EVFILT_USER` event wakes the loop for close.

**Why.** FSEvents is recursive. Watching `~` or `/` on the Mac mini gave about 2 callbacks/s at idle from churn elsewhere in the tree (e.g. `~/Library`), each costing 10–20 process wakeups: 20–45 wakeups/s against the ≤ 1/s idle budget. Its exclusion list holds only 8 paths. kqueue has no idle wakeups on any path measured (`~`, `/`, `~/Documents`, `~/Downloads`, a temp dir) and reports changes in about 1 ms. `~` needed 381 file watches (11 ms to add). It also removes the CoreServices dependency and the process-wide ffi callback.

**Known gaps.**

- A file created and written in the few milliseconds before its watch is registered.
- In-place writes in directories above the cap.
- A subdirectory's shown modification time can go stale (entries added inside a child don't touch the parent).
- A rename of an ancestor of the watched directory.
- Network volumes may not deliver vnode events.

A relist when the window becomes active would cover all of these cheaply.

## D27. Pilot's structure, startup and allocation gate (phase 4, 2026-10-17)

**Decision.**

- **Structure.** `cmd/pilot` is a thin `main.go` plus a `pilot` package (Builder, Initer, script, shot), like rexlook. The chrome both apps share (palette, backdrop field, glass panes, titlebar, traffic lights, icon atlas, dividers) lived in `cmd/internal/rexkit`; rexlook rendered byte-identically after the move. (Phase 5: it moved into the public `theme/rex` and `icon`, pixel-identical in light, and `rexkit` was deleted; Pilot now uses `widget`.)
- **Pane model.** Pointer-free: the listing, a row permutation (only when hidden files or a filter apply), a selection bitset by entry index, cursor and anchor. A refresh carries selection, cursor, anchor and scroll position across by name (hashes of the old names, matched in the new listing). Work that scales with the listing (filtering, matching, row building) runs in 1 ms slices per frame, bounded by the clock, not by an entry count.
- **Startup.** Both start listings begin on goroutines as the first thing in `main`, before the window system exists. The first frame waits for them up to 150 ms. Local folders are ready in 23–34 ms on the MacBook, so they are always in the first frame (exit criterion 5 is about local start folders). A slower volume (network share, sleeping disk) does not hold the window back beyond that cap: the window appears with that pane's status line saying "Loading…", and the listing fills in when it lands. A window that stays invisible for seconds is worse than an honest status.
- **Accepting results.** A pane takes only the answer to its latest request: a listing whose request is older than the pane's floor, a refresh while a navigation is pending, or an unrequested listing is released with its own lease and dropped. Changes to selection, cursor or filter during a multi-frame reconcile restart the affected phase, and a reconciled listing is drawn in the frame it finishes.
- **Watch gaps.** Pilot relists both panes when its window gains focus (`ui.Input.FocusGained`), which covers the kqueue watcher's documented gaps (D26) at no idle cost.
- **Allocation gate.** The bench measures whole script sessions as windows, back to back, after a GC and a post-GC warm-up, and passes on the first window with exactly 0 mallocs and 0 bytes (up to 12 windows; every window is reported). A real allocation in the session recurs in every window and fails; a one-off allocation elsewhere in the process does not. The counters are process-wide, and the runtime itself occasionally allocates once (a new OS thread, the scavenger's timer heap). The fs workers used to be such a source: parking on a `sync.Cond` or a contended `sync.Mutex` takes a runtime sudog, which allocates when the P's cache is empty. Since phase 5 they sleep in `core.Parker` (a pipe read in the netpoller on Unix, an event wait on Windows; no sudog) and share `core.Mutex` (TryLock and spin, never parks); `primeSudogs` is gone, and Pilot's gate passes on the first window on macOS and Windows CI.
- **Paths.** Path resolution is a pure function with a Unix and a Windows rule set, both tested on every OS. Windows: `\` and `/`, volumes, root-relative paths on the shown volume, drive letters, drive-relative paths, UNC shares, surrounding quotes; a root is its own parent.

**Why.** The model keeps per-frame work bounded and allocation-free at 1M entries. A time slice is the only bound that holds when entries are cache-cold. Whole-session windows make the gate exact without letting a real allocation through.

## D28. Public packages and the widget API (phase 5, 2026-10-18)

**Decision.** The framework's public surface is layered `core`/`gpu`/`text`/`platform` → `ui` → `menu` → `theme` → `theme/rex`, `icon` → `widget` → `task` → `app` (each imports only earlier ones; `fs` stays optional; `widget` never imports `app`). Widgets are immediate-mode calls on a per-window `widget.Kit` (made from the window's `ui.Context`, the text system and a theme) that build `ui` boxes and return signal structs by value; their persistent state is app-owned, pointer-free values (`TableState`, `Selection`, `ui.TextBuffer`) or `ui`'s box cache. Rex is the default theme (D17) and gets a dark variant. Every widget is keyboard-operable, shows a keyboard focus ring, has a disabled state, works in light and dark, and records its semantics (role, name, value, state) in `ui` for the phase-6 accessibility bridges.

**Reasoning:**
- Pilot and rexlook proved the bar on private code (`cmd/internal/rexkit`, hand-placed rows, columns and dividers); a framework has to offer that to every app.
- Immediate mode keeps `ui`'s zero-allocation, pointer-free contract (D10) without retained widget objects to synchronise.
- Recording semantics now costs little; retrofitting roles into finished widgets would mean revisiting every one.

**Constraints:**
- Steady-state 0 allocations, including widget interactions, once the working set is warm; every growable store has a `Reserve` and an overflow policy (undo history is bounded).
- No closures or interfaces per row; labels are `string` constants or `[]byte`.
- Flow layout by default, absolute placement still possible.
- A key event is handled at most once: input method, then the focused text field's editing chords, then commands, then widget keys (claimed bit). Paste is asynchronous (`platform.RequestPaste` → `EvPaste` for the requesting field); copy hands the platform bytes.
- `widget.Selection` is indexed by the app's stable item id, not the display row.
- Contract details: [docs/PHASE5-PLAN.md](docs/PHASE5-PLAN.md), "Contracts".

## D29. Menus: native on macOS, drawn on Windows and Linux (phase 5, 2026-10-18)

**Decision.** One OS-independent command and menu model in package `menu` (`Command`: id, title, shortcut, app-wide or per window; enabled and checked are mutable state set by the focused window's Build). Per-window commands reach that window's next Build exactly once, in order; app-wide ones (Quit, About, New Window) reach an `app.CommandHandler` even with no window open. On macOS it becomes a native NSMenu bar (app menu with About, Hide, Quit; an Edit menu wired to the focused text field; the app's menus) and native context menus. On Windows and Linux the same model is drawn by `widget.MenuBar` and `widget.ContextMenu` in a `ui` overlay inside the window.

**Reasoning:**
- macOS apps must have the global menu bar; ⌘Q, the Edit menu, Services and key equivalents are expected there, and only NSMenu provides them.
- Win32 menus cannot follow dark mode through a documented API and look unlike the rest of a drawn UI; Linux has no native toolkit we may use (no GTK/Qt).
- Drawn menus inside the window need no popup surfaces. Menus that extend past the window edge (popup windows, `xdg_popup`) are later.

## D30. More than one window (phase 5, 2026-10-18)

**Decision.** `app` opens and closes windows at runtime. One GPU device, renderer, text system and glyph atlas are shared; each window has its own platform window, surface, `ui.Context` and kit, is paced on its own, and presents only when it has something new. On all three OSes the platform layer's single-window globals become a window registry.

**What that requires:**
- `app` owns the GPU device and shared resources; a surface owns only its swapchain or layer. Linux shares one EGL display and context (`eglMakeCurrent` per surface; `eglTerminate` at exit only).
- Atlas textures are shared, and uploads are coordinated once for all contexts (today each `ui.Context` owns its textures and clears the text system's dirty regions).
- One app-wide event pump waits on every window (one run loop with a display link per window; `MsgWaitForMultipleObjects` over the swapchain waitables; one Wayland or X11 connection), with window-tagged events.
- Window changes are queued and applied at safe points between frames; close requests can be refused; the app quits on the last close (optional on macOS).
- The frame handler that keeps drawing during live resize generalises to every native modal or tracking loop (file dialogs, menu tracking, Windows size/move), without re-entering Build or leaving the main thread.

**Reasoning:** dialogs, inspectors and documents need windows; sharing the atlas keeps memory and startup flat as windows are added; per-window pacing keeps idle at 0 presents.

## D31. Linux D-Bus: an in-house minimal client (phase 5, 2026-10-18)

**Decision.** The xdg-desktop-portal calls (FileChooser, Settings for appearance and accent colour) and, if time allows, IBus use a small in-house D-Bus client: session bus over a Unix socket, EXTERNAL authentication, method calls, and signal matching.

**Alternatives:**
- `github.com/godbus/dbus/v5` (pure Go, BSD-2-Clause): complete and well tested, but reflection-based and larger than the handful of calls needed.
- No D-Bus: no native file dialog or system appearance on Linux.

**Reasoning:** we need about five calls and one signal; the project has two dependencies (purego, go-text) and keeps it that way unless a dependency clearly earns its place.

**Constraints:**
- Bus I/O runs on its own goroutine, never on the UI thread or the startup path; results reach the UI through a ring and a wake.
- Portal requests subscribe to the `Response` signal before the call (the documented race), correlate by request handle, and handle cancellation, the owner window closing, a missing or disconnected portal, and version differences in Settings. Messages are decoded with size bounds.
- Dialog parents: `x11:<XID>`, or a Wayland handle exported with xdg-foreign; an empty parent when neither is available.
- Tests run against a real `dbus-daemon` in CI with a fake portal, including immediate responses and an absent service.

## D32. System appearance: read at startup, followed without polling (phase 5, 2026-10-18)

**Decision.** `platform` reports the system's light or dark mode and accent colour (`SystemAppearance`, `AppearanceSerial`, contract in `platform/appearance.go`), and `(*Window).SetDarkChrome` makes the OS-drawn chrome match the app's mode. Apps follow the system by default, can force light or dark, and crossfade between modes. Moved from 5B into 5A at the user's request ("make sure that we have support for dark/light system mode").

**How, per OS:**
- **Startup:** the first frame is drawn in the system's mode. macOS (`effectiveAppearance`, `controlAccentColor`) and Windows (`AppsUseLightTheme`, DWM `AccentColor`) answer synchronously in microseconds. Linux asks the desktop portal over D-Bus (D31) on a goroutine started at launch; the first `SystemAppearance` call waits for the answer at most 25 ms, and a later answer crossfades. **So on Linux the first frame is in the system's mode when the portal answers within 25 ms, not unconditionally.** A running portal (the normal case on GNOME and KDE) answered in 0.23–1.17 ms in CI. A portal that D-Bus has to start on demand can take longer: the first frame is then light (`Known` false) and crossfades to dark when the answer arrives (tested with a fake portal that answers after 100 ms). The bound stays at 25 ms because a longer wait would cost every Linux user's startup for the sake of the rare slow portal (the 150 ms budget, D21).
- **Changes:** no polling. macOS observes the appearance and system-colour notifications; Windows waits in `RegNotifyChangeKeyValue` on the two registry keys on a blocked goroutine (so the window procedure needs no change); Linux listens for the portal's `SettingChanged`, read in place from the D-Bus client's receive buffer and decoded into fixed fields, so a switch allocates nothing once warm (tested with the exact allocation rule of D27). Each change bumps a serial and calls `Wake`; an idle app costs nothing until then.
- **Chrome:** Windows sets `DWMWA_USE_IMMERSIVE_DARK_MODE` (20 on Windows 11 build 22000+, 19 on Windows 10 builds that have it, nothing otherwise; Windows 10 stays supported). macOS sets the window's `NSAppearance`, which covers the traffic lights and menus. Linux has nothing to set: client-side decorations are drawn by the theme.

**Reasoning:** content-first startup (D21) means the first frame must already be in the right mode; following the system with notifications keeps the idle budget (0 wakeups) intact.

## D33. ZapFast on the framework: its Rust core, a Go UI (phase 6, 2026-10-18)

**Decision (user, 2026-10-18).** The first real app on the framework is a migration of ZapFast (crmne/zapfast, MIT), a WhatsApp client in Rust and egui. Its backend stays Rust:
- the `whatsapp-rust` protocol (MIT), the calls stack, the SQLCipher archive and the keyring;
- built as a library inside the app bundle, behind a small C ABI;
- driven from Go through the framework's cgo-free FFI.

The whole UI is rewritten in Go on the framework with the Rex design system, macOS first. Other OSes follow.

**Scope (user):** full parity with ZapFast before the user's first review of it.

**Where:** a private GitHub repo, `AdamAugustinsky/zapfast-gnui`. Upstream is kept as a remote so backend fixes can be merged.

**Alternatives:**
- **A full Go port on whatsmeow (MPL-2.0):** pure Go and no bundled library, but no voice or video calls, no SQLCipher without cgo, and about 150k lines of Rust to rewrite.

**Constraints:**
- **The exception is the app's, not the framework's.** It bundles a non-system native library. The framework's own rule is unchanged: OS system libraries only, no cgo, no third-party code.
- **The ABI.** Plain C calls, with no callbacks into Go where avoidable: commands in, events out, through a pollable fd plus a drain call. Memory ownership is explicit at the boundary. The Rust threads (tokio) never touch the UI.
- **Budgets.** The app keeps them: zero allocations per steady-state frame on the UI side, an idle loop with no wakeups, and content-first startup. Loading the library must not block the first frame.

## D34. Phase 6 is macOS-first (phase 6, 2026-10-18)

**Decision (user, 2026-10-18):** "it should feel macos native (we take care of other operating systems afterwards)". New APIs ship macOS implementations. On Windows and Linux they compile and return `ErrUnsupported`, or keep today's drawn equivalent (menus, dialogs); a second window is refused there. CI stays green on all 8 jobs, and everything phase 5 delivered on Windows and Linux keeps passing. Windows and Linux numbers come only from CI.

**Alternatives:** all three OSes per feature, as phases 1–5 did. That roughly triples the native work before ZapFast is possible.

**Cost:** a parity phase later, whose size grows with this one: hosted views need child HWNDs and Wayland subsurfaces, and the native pieces need drawn versions.

## D35. `clib`: C calls for apps (phase 6, 2026-10-18)

**Decision.** A public package for dlopen/dlsym and plain C calls on `internal/ffi`'s trampolines (D3): integer, pointer, `float32`/`float64` and `bool` arguments and results; no callbacks, no structs by value, no variadics; 0 allocations per call after `NewCall`. darwin and linux now; Windows `ErrUnsupported`.

**Reasoning:**
- D33's ZapFast core lives in another module and cannot import `internal/ffi`. A narrow public surface is safer than exporting `ffi.Frame`, and it keeps callbacks (the hard part) internal.
- D2 is unchanged: the framework bundles nothing and loads only OS libraries. What an app loads is that app's decision, recorded by the app (D33).

**Constraints:**
- Pointer arguments point to Go heap or C memory, never a goroutine stack; C keeps nothing after the call.
- **Errors come back in the same call.** Consecutive calls may run on different OS threads, so thread-local error state (`errno`, `dlerror`, a library's "last error") cannot be read after a call. Libraries designed for `clib` return errors through caller-owned storage in the call itself (ZapFast's `zf_err`, D49). `Open` and `Func` read `dlerror` with the thread pinned for that one call; a caller that must read a thread-local error from an existing library pins the thread around the call and the read.
- A library's own threads must coexist with Go's signal handling under fakecgo: tested with a fixture library whose threads spin and block while Go runs under `SIGPROF` and async preemption.

## D36. The Objective-C runtime grows (phase 6, 2026-10-18; revised by F1, 2026-10-19)

**Decision.**
- **Classes across files:** `objc.Define` collects methods and ivars from several files; `platform/platform_darwin.go` is split by concern onto it in wave 0. Every class gets one `uintptr` index ivar (`ClassDef.Index()`) into a Go-side table, replacing the global window; `Ivar` adds more. `SendSuper`/`SuperFrame` call `objc_msgSendSuper`, with the `objc_super` in the frame's scratch words.
- **Callback frames live on the C stack.** Every ffi callback reserves an `ffi.Frame` in its own trampoline frame on the calling thread's native stack (`CB.Frame()`), valid until the handler returns. Main-thread, foreign-thread and nested callbacks all use it, so there is no frame pool, no nested reserve, no exhaustion and no fallback: no callback waits for a frame. Nesting depth is bounded by the native stack (about 0.5 KiB of trampoline per level plus the callees' frames), not by ffi. The global `objc.F` stays main-thread only (checked in `gnuidebug` builds).
- **1024 callbacks** per process (`ffi.MaxCallbacks`, up from 32). A class takes one for all its methods (plus one per `MethodIMP`), a block kind one for all its requests.
- **Per-request blocks** (completion handlers) are stack-class literals: `_NSConcreteStackBlock`, `BLOCK_HAS_SIGNATURE`, the kind's ffi callback as invoke, and two captured integers, the slot and its generation. The literal lives in its slot of a fixed table of 256 (a full table fails the request with `ErrBlocksFull`). The issuing call holds a claim on the slot until `Request.Sent()`, which then poisons the literal, so an API that kept it uncopied gets a stale call, never another request's handler. An API that keeps the block copies it: `_Block_copy` makes a heap copy with its own captures, freed by `Block_release`; no copy or dispose helpers are needed.
- **Claims, not just generations.** Each slot has one atomic word: generation, a `retiring` bit and an active-claim count. A callback claims with a compare-and-swap that succeeds only on a matching generation with `retiring` clear (a one-shot kind completes in the same step); it releases its claim on return. Cancelling (or completing) moves the generation on and sets `retiring` in one step and never waits; whoever brings the claim count to 0 frees the slot. A failed claim is a stale call: it makes only the native acknowledgement the API requires, from its own arguments, never touching a request's context.
- **The generation rule:** a slot never issues a generation twice. Generations start at 1; 0 is never issued; a slot whose generation would wrap past 2^32−1 is retired for good when freed (`Leaks().RetiredSlots`).
- **Process-lifetime blocks** that carry no per-request state may be global (`objc.NewGlobalBlock`), with both `_NSConcreteGlobalBlock` and the `BLOCK_IS_GLOBAL` flag (`_Block_copy` tests the flag, not the isa).
- **Multi-shot callbacks** use selector-based observers where the API has them; the AVAudioEngine tap is a per-request block cancelled when it is removed.
- **Blocks given to us** are valid only during the call that passed them (`objc.CallBlock`). Any we keep goes through `objc.KeepBlock`, which `_Block_copy`s it before storing, and `objc.CallKeptBlock`, which calls it once and `Block_release`s it.
- **Cancellation is specified per API** (the phase-6 plan's table). **A window closing under a sheet calls the sheet's completion 0 times**, so the framework ends an attached sheet with `NSModalResponseAbort` before closing its window, which calls it exactly once.
- **Foreign-thread handlers** only copy into the app's event records, signal and wake. Leak counters (`objc.Leaks`): live, kept and global blocks, delegates, observers, stale calls, refusals, retired slots.

**Reasoning:**
- Every report found blocks the blocking prerequisite (`egui.md` §3, `electron-tauri.md` §1, `gpui-ghostty.md` §3.2).
- A recycled global block is unsafe: `_Block_copy` returns a global block unchanged, so AppKit keeps our literal's address, and a late callback would run whichever request reused the slot. A generation check alone is not enough either: a callback that passed the check could still be running when the slot is reused, so a claim count keeps the slot until it leaves.
- The literal lives in its slot, not in the caller's frame, because a nested request made during the passing call (on the same frame) would overwrite a literal the API had not yet copied.
- GCD promises nothing about how many threads it uses, so nothing may be keyed to a thread. The frame in the callback's own C frame is invocation-scoped by construction and never runs out, which the planned lease pool, nested reserve and per-API fallbacks only approximated.

**Evidence (F1 feasibility tests, MacBook arm64, Rosetta amd64, CI on darwin and Linux):**
- A foreign thread enters Go through fakecgo (`needm`, no M bound to the thread). A cold pthread's first callback allocates 9 objects / 6 KB; later fresh threads 0. Each new concurrency high costs about 8 objects / 6 KB (64 fresh threads at once); repeats, `dispatch_apply` 64-wide, 10,000 callbacks from 21 queues and queue and thread churn allocate 0 (exact `ReadMemStats` windows).
- Cost: about 60 ns per main-thread callback (unchanged), about 1.1 µs per GCD-thread callback including dispatch (attach and detach every time).
- A late call through an old heap copy after the slot's reuse is stale; a callback blocked in its handler keeps its slot through `Cancel` (which returns at once) until it returns; the generation-wrap test retires the slot.
- 32 nesting levels on a Go thread, a fresh pthread and a GCD thread; nested chains keep their frames intact while goroutine stacks grow and move, handlers are preempted and GCs run, under the CPU profiler.
- Block copy per API: NSAlert, NSSavePanel and NSOpenPanel copy the block and call it once on the main thread when the sheet ends, and 0 times when the window closes under it. AVAudioPlayerNode `scheduleBuffer:` copies and calls each completion once on AVFoundation's threads, and synchronously on the caller's thread for a buffer still pending at `stop`; the tap's block is copied, and no handler starts after `removeTapOnBus:` and `Cancel`. UserNotifications, AVCaptureDevice and the notification delegate's thread cannot be tested from an unbundled binary and remain assumptions until a bundled fixture checks them.

**Alternative rejected:** dispatching everything to the main queue. AVFoundation and UserNotifications still call us on their queues, and their completion handlers must be called.

## D37. The application object (phase 6, 2026-10-18)

**Decision.** An NSApplication delegate set before `finishLaunching`, one pump owned by the app (it runs with zero windows), and `app.Main`/`Handler`/`Post`, with `KeepRunning`, `StartHidden` and the Accessory policy. D30 is built for macOS in this phase.
- **App events are dispatched by a run-loop source,** not by the pump or a frame: a version-0 `CFRunLoopSource` on the main run loop in `kCFRunLoopCommonModes`, signalled from any thread. It fires in the default, modal-panel and event-tracking modes, so it keeps working with zero windows and inside native loops (`NSTerminateLater`'s wait, menu tracking, sheets). Native loops start only at safe points, so it never fires during Build.
- **Safe-point work runs from that dispatcher too.** While the outer pump is suspended in a native loop, the source's callback runs the deferred safe-point queue after delivering events (windows opened and closed, sheets and alerts begun, hosts, menu rebuilds), so a "Save changes?" sheet can open while a quit is pending.
- **Four delivery classes. Nothing the framework has accepted is lost:**
  1. main-thread native events (reopen, quit requests, opens, activation, sleep and wake, screens, menu commands) are delivered synchronously from the delegate method, with a startup list for events before `Start` returns;
  2. request completions and notification responses use records reserved before the action that produces them: a ticket per request (64 per service), and a response slot per actionable notification reserved when it is posted (256). A reply is stored in its slot before the system's completion handler is called. A completion arriving on a foreign thread writes its record and signals the source using its callback's own frame (D36), so no frame-free fallback path exists. A full table fails the request with `ErrBusy`, or posts the notification without actions and reports `PostedWithoutActions` (or, if the app chose `EvictOldest`, removes the oldest delivered notification to free a slot);
  3. requests from outside the process (a second instance's arguments) are accepted into a reserved ring only if they fit; otherwise the primary answers `busy`, and the secondary retries for 2 s and then reports the failure;
  4. observations (levels, positions, thermal state) are one coalesced latest value per source: the only class where a value is replaced unseen.
  - `app.Post` is the app's own path from its goroutines, allocation-free and non-blocking; it returns false when full, and the caller keeps the event. Native acknowledgements and resource release never depend on delivery.
- **Quit:** every route (⌘Q, Dock → Quit, logout, `a.Quit()`, signals) arrives as `EvQuitRequested`, synchronously. The default handler asks each window's `CloseRequested`; `a.ReplyQuit(id, allow)` answers. **An answer given inside the request** (synchronously, or a `ReplyQuit` arriving during it) is latched and applied at once.
  - *Revised in F3:* the app's own routes and the quit Apple event (Dock, another app, logout with `kAEQuitReason`) are decided **without `terminate:`**, which is sent only once the quit is allowed, after ending the sheets as cancelled. AppKit's `terminate:` ends every sheet before asking, an alert with its first button, so a Dock Quit or a logout would press a confirmation's default action. The quit Apple event, through a handler of ours, keeps AppKit's reply semantics: refused, its reply carries `userCanceledErr`, which cancels a logout; pending, it is suspended and resumed with the answer.
  - A `terminate:` someone else sends still asks `applicationShouldTerminate:`: `NSTerminateNow` or `NSTerminateCancel` when answered inside it, otherwise `NSTerminateLater`, after which `ReplyQuit` calls `replyToApplicationShouldTerminate:`, never early.
  - A quit arriving inside a native loop entered from Build is asked after the frame: the quit Apple event (a logout included) is suspended and resumed with that answer; a foreign `terminate:`, which blocks until answered and so cannot wait inside Build, is refused and asked again after the frame.
  - An allowed quit is committed: until the termination, later requests are answered yes without asking. Leaving `Main` while a quit waits answers its suspended events yes and ends the dialogs as cancelled before `EvWillTerminate`.
- **No automatic answer.** By default the app's quit policy owns the answer, with no watchdog. `AppConfig.QuitDeadline` is optional; when set, an unanswered quit gets `QuitDeadlineAction`, which defaults to `Cancel`, and the app receives `EvQuitDeadline`. Force-quitting (`Allow`) happens only as the app's explicit choice. Any timeout the system applies at logout is the system's; its length is not documented and is not ours to promise.

**Reasoning:**
- `electron-tauri.md` §1 ("No application object"), `go-and-others.md` §2a, `gpui-ghostty.md` §2: no app can run windowless, refuse a quit or receive an open-URL event without this.
- `NSTerminateLater` (for a foreign `terminate:`), menu tracking and sheets run the run loop in their own modes, so the pump cannot be the only dispatcher, nor the only place safe-point work runs; a common-modes source is both.
- Cancel-then-restart was rejected because cancelling during a logout stops the logout, and, by assumption, a later quit does not resume it; the suspended quit Apple event holds the logout instead, as `NSTerminateLater` does inside AppKit.
- An automatic "allow" would quit over a window that refused, losing the user's work; a deadline that cancels keeps the app's decision.
- Events, not callbacks: closures per event break the zero-allocation and main-thread rules (`go-and-others.md` §3.1). Gio's app-global event stream is the Go-idiomatic model.

**Assumption:** the logout behaviour (a suspended quit event keeps a logout pending; a refusal stops it; a later quit does not resume it) is checked by F3's feasibility test "logout behaviour", run on the MacBook with a dedicated test user, and listed as untested until it runs.

**Constraints:** tested in macOS CI through AppKit state: Dock Quit (a self-sent quit Apple event) and ⌘Q with zero, one and two windows; answers inside `applicationShouldTerminate:` latched with no early reply; delayed replies with app events and a sheet delivered meanwhile; refusals; no answer with and without `QuitDeadline`; and saturation in which every completion, notification response, accepted outside request and the quit arrive, and the only losses are coalesced observations and refused `app.Post` calls.

## D38. Text at the point size (phase 6, 2026-10-18)

**Decision.** Fonts are created at the point size, shaped and laid out there, and rasterised through a context scaled by the backing scale; metrics, pen positions and advances are CoreText's points times the scale. Faces carry their scale, so the glyph-cache key is a face at one scale, a glyph and a variant. Glyphs are rasterised with AppKit's font smoothing, which text can opt out of. rexlook's text metrics needed no refit: rexcompare stays 40/40 (its terminal text is monospace, whose advance does not change).

**Evidence:**
- **Point size.** The advance per em at 13 pt is 14.28 in AppKit and was 13.49 for us at 2× (`docs/design/native-audit.md`, problem 1): SF's 26 pt optical size and tracking. Now it equals `NSFont`'s within 0.05 % at 9–26 pt, at 1× and 2× (`text/opsz_darwin_test.go`). GPUI does it this way (`gpui_apple/src/text_system.rs:489-526`).
- **The cascade.** Pinning `opsz` (`kCTFontOpticalSizeAttribute`) on a pixel-sized font fixes SF but not the fallback fonts CoreText picks: at 13 pt and 2×, Arabic, Hebrew, CJK and emoji runs stayed 1.8–14 % narrower than AppKit's. Point-sized fonts get the point-sized fallbacks.
- **Smoothing.** The condition this entry set, a GnuiShot capture of AppKit text beside ours showing a weight difference, was met with an AppKit `NSTextField` label (the hosted `NSTextView` of N4 does not exist yet) on the MacBook, macOS 27.2: 13 pt SF at 2× has stems of 2.58 px for dark text and 2.91 px for light text in AppKit, and had 2.23 px for both in ours. They are now 2.57 and 2.91. This revisits D21's "slightly bolder text is not needed" on that evidence; rexlook's terminal panes keep their weight (the opt-out).

**How:**
- **Smoothing bands.** CoreGraphics picks one of four dilations from the fill colour's linear luminance Y: below 1/8, below 3/8, below 5/8, and above (found by scanning grey and RGB fills). `text.SmoothLevel(rgba)` returns the band as 1–4, or 0 when the user set `AppleFontSmoothing` to 0. A glyph is drawn into an alpha-only context in the grey at the middle of its band, so the bitmap is still coverage and the renderer still applies the colour.
- **`GlyphVariant` bits.** `Glyph`'s `sub` argument holds the quarter-pixel offset in bits 0–1 and the smoothing level in bits 2–4 (`text.GlyphVariant`). A glyph has at most four smoothed bitmaps, one or two per theme in practice. Colour glyphs ignore the level; the Windows and Linux backends mask it off.
- **The opt-out.** `ui.NoFontSmoothing` on a text box draws its glyphs at level 0. It is a box flag because `ui` is where the run's colour, and so its level, is known; a font role would split the shared faces and the glyph key for one rasterisation choice. `widget` and `rexui` pass it like any other `ui` flag. rexlook's terminal panes use it, as Ghostty draws without thickening by default (`font-thicken`); their pixels are those of d32d248.

## D39. Design tokens v2 (phase 6, 2026-10-18)

**Decision.** `docs/design/rex-design-system.md` §2: colour roles indexed by `Role`, each with a System or Rex source; macOS text styles; control sizes; space, radii, elevation, materials and motion. System values are read live from `NSColor` through the D32 path, without polling, and Reduce Motion, Reduce Transparency and Increase Contrast are followed. Rex keeps its identity roles; dark surfaces are neutral; white goes on the accent.
- **Native colours win over a fixed ratio.** System roles are exactly the system's colours, alpha included: light `secondaryLabelColor` (`#0000007F`, about 3.95:1 composited on white) is kept, though the proposal asked 4.5:1 of `Label2`.
- **Contrast is checked composited.** `theme.ContrastOn(fg, bg, under)` composites the foreground over its real background (itself composited over what is under it); `theme.Contrast` ignores alpha. Floors: primary text ≥ 4.5:1 on every background it is used on; secondary, tertiary and quaternary text and placeholders ≥ AppKit's own composited contrast for that role on `windowBackgroundColor` in the same appearance, computed from live system values. The secondary floor is AppKit's, and is documented as such. *Amended 2026-10-19 (D1 review, user's decision):* the secondary floor is AppKit's **weakest own pairing** for the role (window, group box, stripe, sidebar, toolbar capsule, text field), computed only from colours measured from AppKit (system roles, live or measured, and D0's measured chrome), never from Rex-only roles, because AppKit itself sets these roles below its window contrast (light `Label2`: 3.95:1 on the window, 3.84:1 on the sidebar; dark: 5.92:1 on the window, 4.67:1 on the capsule) and native fidelity is the goal; Rex's own pane, raised surface and glass are held to it (dark `Label2` on Raised is 5.42:1).
- **Traffic lights are stored as the close button's centre,** the unit `app.Config.TrafficLights`, `theme.TrafficCentrePt` and `layoutChrome` take, converted from AppKitRef's frame origins (x + 7): plain 16, 39, 62 at y 16; compact 19, 42, 65 at y 20; unified 26, 49, 72 at y 26; Rex 23, 46, 69 at y 23; spacing 23 pt. This supersedes the origins listed in `rex-design-system.md` §2.3.

**Evidence:** audit problems 3–8 and 11–20; `~/Projects/research/appkit-ref/macbook-blue/metrics.json` (`windows.*.trafficLights`).

**Alternative:** retuning `widget` in place (option A). Rejected as the end state: no variants and no sizes.

## D40. Component layering (phase 6, 2026-10-18)

**Decision.** `widget/base` holds the `Kit` and the headless primitives; `rexui` holds the styled components, one file each, copyable with `tools/rexui add`; `widget` keeps its API as wrappers (`widget.Kit` embeds `*base.Kit`; state types are aliases).

**Reasoning:** with the primitives inside `widget`, the wrappers would import `rexui` and `rexui` would import `widget`: a cycle (`rex-design-system.md` §4.2).

**Alternatives rejected:** painter interfaces (option C: interface calls per row, escaping arguments), and hosting NSButton and friends (option D).

**Amends D28's layering line:** `… → theme/rex, icon → widget/base → rexui → widget → task → app`.

## D41. Native AppKit where the OS adds behaviour (phase 6, 2026-10-18; refined by D52)

**Decision.** On macOS these are native: the menu bar, context and popup menus, NSAlert, the open and save panels, tooltips, materials (`NSVisualEffectView`), the chat composer (`NSTextView`) and accessibility. Everything else is drawn.

**Constraints:**
- Native views are wholly above or below the Metal layer, never interleaved; geometry changes are presented in the same transaction; a host is hidden while a drawn overlay covers it, so popups for a host are placed beside it.
- **The composer's contract:** the app sees UTF-8 byte ranges on scalar boundaries, mapped to AppKit's UTF-16 through a checkpoint table; marked text is visible state (`Marked`, a `Composing` flag) and blocks programmatic replacement; `Replace` runs as a user edit, so it is one undo step; while the app holds a completion active, `doCommandBySelector:` diverts Up, Down, Tab, Backtab, Enter and Escape to the app as host commands; mention styles are temporary attributes, outside the undo stack.
- **Acceptance of the composer** is an automated real-editor matrix run in-process on the MacBook (and in macOS CI where possible): keys through `[NSApp sendEvent:]`, IME input through the `NSTextInputClient` calls an input method makes; covering marked text, undo and redo, paste, autocorrect off and on, completion navigation, the Edit menu, and focus after a sheet. The user runs a short hand checklist before ZapFast's checkpoint: a real Japanese or Chinese input method, dictation, Writing Tools.

**Evidence:** `gpui-ghostty.md` §1 ("Ghostty feels native because the chrome is AppKit") and §3; `rex-design-system.md` §3 ("Draw / host").

**Alternative rejected:** hosting standard controls (option D: AppKit's heap per control, no gain in behaviour).

**Cost:** two text renderers side by side (D38 mitigates), synchronous presents on frames that move hosts (measured), and a z-order rule designers must follow.

## D52. Native shell, custom content; rexlook's language on measured macOS 27 windows (phase 6, 2026-10-19)

**The user's verdicts (2026-10-19):** "the macos-native feel of our programs is still miles behind the superlogical Rex program/demo"; after using the apps: "all the apps that we did very much don't have a macos native-feel to them … they look linux-like, or old versions of macos (much before liquid glass). the only actually good looking and good feeling to use app that we have is RexLook, the rest is quite mediocre"; and "we should not use the macos native component for tabs, we should instead build a custom component fast and native feeling, based on the original rex tabs."

**Decision.** This refines D41 from "native where the OS adds behaviour" to a native *shell*:
- **Native on macOS:** the window and its traffic lights; live materials (the window backdrop, and glass wherever the measured macOS 27 pattern uses it: sidebar, inspector, toolbar capsules, the composer); the menu bar, context and popup menus; alerts, sheets and panels; tooltips; overlay scrollers; a native button only where it matches better than a drawn one (rexlook's ⌘ and +).
- **Ours, drawn:** tabs (Rex's, made fast and native-feeling: select on press, instant switch, system tab shortcuts, key-window dimming) and every content control, in the Rex language.
- **rexlook is the source of truth for the look.** The Rex identity (panes, glass, typography, restraint) sits on the window-level metrics measured from macOS 27.2 (`docs/design/liquid-glass-reference.md`, tool `tools/appkitscenes`): unified toolbar 52 pt with a 15 pt bold title, compact 40 pt with 13 pt; toolbar items in 36 pt glass capsules; a flush, full-height glass sidebar (27.2 has no floating inset sidebar); selection inset 10 pt, radius 8; the toolbar's scroll-edge bar only once content is under it (always when the window is inactive); window radius 16. The gallery, notes, Pilot and ZapFast are rebuilt on it.

**Acceptance.** Live GnuiShot captures next to the `appkitscenes` and `appkitref` references, light and dark, active and inactive, top and scrolled; then the user's eye at checkpoint 1.

**Evidence:** the spike (`docs/design/native-shell-spike.md`, branch `p6-nativeshell`): glass backdrop under a non-opaque Metal layer, startup +7 ms, 0 allocations, idle 0 frames; `gpui-ghostty.md` §1; D0's finding that everything except the window, the traffic lights and live glass can be drawn to within about 2 %.

**Alternatives rejected:** native tabs (`NSToolbar` segmented or window tabbing: the user's call, and a toolbar's title area swallows clicks on drawn tabs); drawing the glass (it cannot follow the desktop or the user's Liquid Glass setting).

## D42. Rich text and selection (phase 6, 2026-10-18)

**Decision.** Pointer-free spans and a per-kit style table. On macOS, paragraphs are laid out by `CTTypesetter` (native breaking, per-line bidi, fallback), cached pointer-free and buildable on workers.
- **Selection is in document order, independent of built boxes.** A `SelectionScope` holds two endpoints, each (stable item id, text part, byte offset), with sentinels `PartEnd` (end of the item) and `OffEnd` (end of a part) so an endpoint can sit in an item that was never built. Built items receive the clipped range they cover; unbuilt rows in between are selected without being built. Dragging past the edge auto-scrolls and keeps extending; prepends leave endpoints alone; removing an endpoint's item moves the endpoint to the nearest survivor.
- **Enumeration:** the scope orders and walks items through an `Enumerator` (`IndexOf`, `First`, `Last`, `Next`). **In a `VList` the framework provides it** from `VListState`'s dense id array and index table, so ⌘A and walking the selected range need nothing from the app. Outside a `VList`, the app supplies its own `Enumerator`.
- **Copy text is the app's:** `Selected()` returns the normalised range and `ForEachSelected` calls back once per item in it with its part range, allocation-free; the app fetches each item's text from its model, so copying never depends on what was built.

**Evidence:** the P0 rich-text and selection rows in `zapfast.md` §2, `egui.md` §2.3 and `go-and-others.md` §2b. `ui/wrap.go` is one font per box and left-to-right only.

**Alternative:** our own UAX #9 and UAX #14 on every OS now. Left for the Windows and Linux phase.

**As built (C2, 2026-10-19):**
- `text.Layout(*Para)` over a reused `CFMutableAttributedString` with each paragraph's base direction set explicitly (DirAuto: rules P2/P3 over Unicode 17 Bidi_Class, skipping isolates, from a table generated by `tools/genbidi` and checked against CoreText's own resolution), `CTTypesetterSuggestLineBreak` and `CTTypesetterCreateLine`. Against an `NSLayoutManager` of the same width (`text/para_darwin_test.go`, 17 samples: Latin, styled spans, Thai, Japanese, Devanagari, Hebrew, Arabic, mixed bidi, emoji, URLs, Times, Menlo, SF 11–22 pt), line breaks are identical, and widths, tops, baselines and glyph positions agree to 0.01–0.05 pt. TextKit sets a line `round(ascent) + round(descent) + leading` points high, with the baseline `round(ascent)` below its top (16 pt for 13 pt SF, not CoreText's 15.31); RichText uses that. `WrapText` keeps its label line pitch, so existing renders are unchanged.
- AppKit's natural alignment follows the UI language, so it puts Hebrew flush left in an English UI. Ours follows the paragraph: right-to-left paragraphs are right-aligned, as chat apps do.
- The cache is pointer-free slabs keyed by text, spans, fonts, width, direction, line limit and ellipsis, evicted by second-chance compaction. It has a budget of layouts, glyphs, text, lines, spans and fonts (`text.ParaBudget`) that it never exceeds; a layout bigger than the whole budget is kept outside it, alone, in storage released once it leaves use. The app declares the budget with `ReserveParas`, which reserves the cache and the layout scratch (worst case of one paragraph of up to 16 KiB) up front, so misses and evictions within it allocate nothing; a miss beyond it may allocate (a longer paragraph grows the scratch once, an oversize one gets its own storage). Undeclared, the budget is `DefaultParaBudget` (1024 layouts, 64 Ki glyphs: 1.7 MiB), which `ReserveRuns` reserves, so an empty app pays about 3 MiB of address space for paragraphs instead of the 27.5 MiB of the limits; a chat app declares more (vlistbench: 4096 layouts, 256 Ki glyphs, about 7 MiB). Idle `phys_footprint` (medians of 3–5 runs, macOS 27.2, M-series) before C2 / with C2 / with the default budget: gallery 42.0 / 42.8 / 42.8 MB, notes 31.0 / 32.0 / 31.8, rexlook 34.2 / 35.0 / 34.7, Pilot 46–60 (bimodal, graphics) in all three, its Go heap 8.6 / 9.8 / 9.6. The 27.5 MiB reservation was address space that an idle app never touched, so the default budget changes what a busy app can grow to rather than the idle footprint.
- Selection geometry follows the caret model: a ligature is split at its caret stops, and the white space hanging left of a right-to-left line keeps its negative extent. An ellipsis takes the font of the last character kept.
- A fresh screen of 24 wrapped messages (`ui/freshscreen_darwin_test.go`, vlistbench's shape) takes 364/583/639 µs (P50/P99/max), against 1302/2789/3100 µs with the old wrap. vlistbench on p6-vlist with this merged: fresh-screen frames are P99 1.8–2.0 ms and max 1.9–3.4 ms, against 3.8–4.2 ms and 6.3–9.7 ms.
- On Windows and Linux, `text.BasicLayouter` applies to spans. Each style run is shaped on its own, runs are placed left to right, and right-to-left paragraphs are right-aligned.

## D43. Variable-height lists (phase 6, 2026-10-18)

**Decision.** `VList`: a position held as (anchor item id, offset), measured heights with estimates, and a Fenwick tree for totals and lookups. The state keeps the app's item ids with an id→index table, and changes only with the app's model: `Splice(at, removed, inserted)` (prepend and append are special cases), `Move(from, n, to)` with heights travelling with their items, and `Reset(ids)`. Edits above the anchor shift its index and keep the view still; deleting the anchor item moves the anchor to the next survivor at offset 0; a move keeps the anchor on its item; a reset keeps it if the id survives. Stick-to-bottom and jump-to-item as before. The fixed-pitch `List`/`Table` remain the fast path.
- **Cost of edits:** prepend and append of k items are O(k) (arrays with spare capacity at both ends, an index table relative to a base; an O(n) regrow when a reserve runs out). A middle `Splice` or a `Move` is O(distance): shifting ids and heights, re-indexing every shifted id, and rebuilding the Fenwick tree over the shifted range. A distant move on 1M items is O(n), benchmarked and gated at 2 ms within a frame.
- **Coherent publication:** an edit too long for a frame is rebuilt in a back buffer, 1 ms per frame, while every reader (layout, the anchor, selection, enumeration, semantics) uses the front buffer; the buffers swap at a frame boundary before Build, so no reader sees a half-updated index. Edits arriving meanwhile are queued onto the back buffer before the swap. The back buffer exists only for lists that have needed it.

**Evidence:** Gio's `layout.List` position model (`go-and-others.md` §3.4), egui's heterogeneous rows (`egui.md` §2.4), ZapFast's height cache (`zapfast/src/ui/conversation.rs:2289-2300`). ZapFast deletes messages for everyone, backfills history mid-list and reorders its chat list, so these rules are fixed before the interface freezes.

**Constraints:** 0 allocations, edits included within `Reserve`; under 4 ms of CPU per frame scrolling 1M mixed-height items.

## D44. Images (phase 6, 2026-10-18)

**Decision.**
- Decoding on workers through ImageIO at display size; a cache keyed by content and size, with GPU and CPU byte budgets and LRU eviction, never evicting what is in view; `gpu.FreeTexture` with generations, and RGBA atlas pages for small images; animated frames decoded lazily for visible images only; the standard library's decoders on Windows and Linux for now.
- **External textures** (`CVPixelBuffer` through `CVMetalTextureCache`) are leased: the lease retains the pixel buffer and both plane textures until the command buffer that used them has completed. Leases belong to the renderer's ring slot and are released when `Encode` reuses the slot after its existing wait (today's `waitUntilCompleted`, frame pacing, unchanged), at once for a frame that was never committed, and otherwise when the process-lifetime completion block reports the command buffer done and the dispatcher reads its status without waiting. At most 16 imports per frame (48 in the three-slot ring); beyond that an import returns `ErrBusy`.
- **Closing a window does not wait for the GPU and makes no latency promise.** The window is ordered out at once; its layer, textures and leases go on a retiring list released only when their command buffers have completed. Nothing the GPU may still read is ever freed on a timer.

**Evidence:** `egui.md` §2.5 (egui decodes every GIF frame up front; textures need freeing), `go-and-others.md` §5 gap 1; our textures are append-only (`gpu/renderer_darwin.go:257`). Metal textures from `CVMetalTextureCache` alias the pixel buffer's memory, which Core Video recycles once released.

## D45. App services (phase 6, 2026-10-18)

**Decision.** One package per service under `app/` (notify, dock, tray, secret, paths, prefs, log, winstate, instance, shell, login, power, perm, locale), imported only when used. Requests go in; completions, responses, outside requests and changes come out by D37's delivery classes.
- **Notifications** reserve a response slot when an actionable notification is posted, store the reply in it before the system's completion handler is called, and are posted without actions (`PostedWithoutActions`) when no slot is free, unless the app chose `EvictOldest`. Slots are re-reserved at launch for notifications delivered by an earlier run.
- **Single instance:** the primary accepts a second instance's request only if it fits its reserved ring and answers `accepted` or `busy`; the secondary retries for 2 s and then reports the failure. Frameworks are loaded on first use after the first frame; the notification delegate is the opt-in exception. Everything is keyed by `AppConfig.ID`. Window state uses our own file, read before the window exists.
- **Login items:** `app/login` covers the app itself (`SMAppService mainAppService`, which passes no arguments) and bundled launch agents (`agentServiceWithPlistName:`). `gnui package` generates each agent's plist from `gnui.json`, with the label `<id>.<name>` and the program `/usr/bin/open -g -b <id> --args …` taken from the bundle id being built, never a fixed string. An app that starts hidden at login uses an agent with `--start-hidden`.

**Evidence:** `electron-tauri.md` §3; `go-and-others.md` §3.2 and §3.12. Upstream ZapFast's `packaging/macos/autostart.plist` hard-codes its own bundle id.

**Alternatives:** Fyne- or Wails-style callback APIs, rejected for the allocation rule; `setFrameAutosaveName:`, rejected because it is macOS-only and cannot carry app state.

## D46. Media (phase 6, 2026-10-18)

**Decision.** AVAudioEngine for voice-note playback (with time-pitch speed) and recording taps; AVPlayer with `AVPlayerItemVideoOutput` → `CVMetalTextureCache` for inline video (D44's leases), never a hosted player layer; codecs the OS lacks or that are unverified (Opus, WebP encoding) stay with the app, and ZapFast's core already has them.

**Evidence:** `zapfast.md` §2 ("Media"), `gpui-ghostty.md` §2 ("Inline video").

**Risk:** Go code on AVAudio's queues. Handlers only copy into preallocated rings, using their callback's own frame (D36). A buffer still pending at `stop` has its completion called synchronously on the thread calling `stop`.

## D47. Tooling and the native-feel instruments (phase 6, 2026-10-18)

**Decision.**
- `cmd/gnui` (`package`, `run`, `doctor`) on `gnui.json`, shelling out only to tools that ship with macOS (`codesign`, `hdiutil`, `security`, `plutil`) plus `notarytool` and `stapler` from the Command Line Tools;
- a self-signed `gnui-dev` identity for development bundles (bundle id plus `.dev`), so TCC and Keychain grants survive rebuilds;
- GnuiShot (D19) on the MacBook as the acceptance instrument for live windows, with AppKitRef's live mode as the reference;
- a live harness that drives real AppKit input in-process (`NSEvent`s through `[NSApp sendEvent:]`, `NSTextInputClient` calls on hosted text views), used for gestures and the composer's real-editor matrix;
- a debug overlay with repaint causes, and a loopback agent bridge in development builds, both zero-cost when off.

**Evidence:** `electron-tauri.md` §4; `egui.md` §4; `go-and-others.md` §4.

**Alternative:** ad-hoc signing (today's `mkapp.sh`). Every rebuild voids the grants (GnuiShot README, "Rebuild caveat").

## D48. Our own QR encoder (phase 6, 2026-10-18)

**Decision.** `qr` is written here (byte mode, versions 1–40, mask selection, Reed–Solomon), with a darwin test that decodes our output with Vision and known-answer vectors elsewhere.

**Reasoning:** pairing needs it (`zapfast.md` §2, "UI shell"). An encoder is a few hundred lines; a dependency would need a D2 row and a licence review (`go-and-others.md` §2b, "QR code").

## D49. The ZapFast core ABI (phase 7, 2026-10-18)

**Decision.** Refines D33. A `zapfast_core` cdylib with C ABI v1 (`zapfast-gnui:docs/GNUI-MIGRATION-PLAN.md`, "The core ABI"):
- every command and event is a little-endian binary record generated from one schema (`zfcore/abi/zf.schema`) for both Rust and Go;
- **records are bounded:** at most `ZF_MAX_RECORD` (1 MiB), checked by the generator from per-field maxima and enforced in Rust before enqueueing. Large payloads (decoded pictures such as `Event::ClipboardImage`, pixels in commands, media) travel behind blob ids; history, chat-list and path batches are chunked. An oversized record becomes a small `EventDropped`; a malformed record is skipped; a bad length prefix fails the core rather than spinning. No record can stay pending and block the ones after it;
- **errors come back in the same call** through a caller-owned `zf_err` (code and a 256-byte message), because `clib` calls may change OS thread between calls (D35); there is no thread-local last error;
- a pipe fd plus a drain call, with lossless wakeups; blobs (read in ranges) and frames owned by Rust until freed or released;
- `catch_unwind` under a `panic = "unwind"` profile; an abort remains fatal;
- open (with the cached chat list) split from start (the startup permit, after the first frame);
- a demo core with injection for tests without an account.

**Reasoning:** the allocation gate is process-wide (D27) and receiving messages is steady state, so decoding must allocate nothing on any thread.

**Alternatives:** JSON off the main thread (allocates on every event); callbacks into Go (`clib` has none, D35).

## D50. ZapFast's fork layout and upstream merges (phase 7, 2026-10-18)

**Decision.**
- Upstream's crate gains a `headless` feature. Every seam from the core to egui, eframe, winit, glow, wgpu, rfd, the egui-dependent fastframe crates or a UI module is moved to a new file that the old module re-exports (the inventory is in the migration plan: call frames and `crate::video`; `animation::poster` with the MP4 reader, the openh264 decode loop and the raw-frame helpers it uses, so a sent video keeps its thumbnail, size and duration; `fastframe_shell::Waker`, `AttachmentId`/`ComposerMention`/`PAGE` from `app`, `eframe::storage_dir`, the settings, markup and wallpaper helpers, the UI's `Action` enum with `app_lock` and `notify`, the file dialogs, opener and updater). The target is under about 600 changed lines in upstream's files.
- **Gate:** `zapfast_core` builds and tests with `headless` only, a script over `cargo tree` fails on any UI crate, the dylib contains none of upstream's GPL-2.0 sounds (checked by searching it for a distinctive byte window from each WAV's `data` chunk; a fixture library that embeds a sound must fail the check), and upstream's own `cargo test` still passes. `--all-features` is not the gate.
- **Licences:** our own script over `cargo metadata` audits every transitive dependency of `zapfast_core` against an allow-list and writes a generated section of `THIRD-PARTY-NOTICES.md`; it runs at every merge.
- New code lives in `zfcore/` and `gnui/`; the framework is pinned as a git submodule; `upstream/main` is merged weekly and at releases with merge commits; exhaustive DTO matches catch ABI drift and a triaged summary of upstream UI commits catches UI drift.
- **The split stays private** (user default, 2026-10-18, revisable): nothing is offered to crmne/zapfast.

**Alternative:** a patch queue rebased on every upstream commit. Rejected: `gnui` is published, and upstream moves daily (`zapfast-gnui:AGENTS.md`, "Branches").

## D51. ZapFast's budgets and identity (phase 7, 2026-10-18)

**Decision.**
- **Budgets:** startup ≤ 150 ms warm to a first frame with complete content (the shell and the visible chat rows, no skeleton row), gated on a seeded archive with no migration pending. Skeleton rows have one trigger: the cached chat list is not ready when the first-frame cap expires (a slow archive, including one with a pending migration); that case is measured on fixtures and reported, not gated; 0 allocations in steady state over a scripted demo session; < 4 ms CPU per frame scrolling a 10k-message transcript; idle at 0 presents and ≤ 1 wakeup/s with the demo core; `phys_footprint` ≤ 150 MB idle (stretch 120 MB); the Go executable < 10 MB per architecture, and the dylib's ceiling set by the user after Z0 measures it.
- **The user's defaults, 2026-10-18 (revisable):**
  - a separate identity, `dev.gonativeui.zapfast`, with its own Application Support folder and Keychain items, never sharing upstream ZapFast's archive; its login agent is generated from that id (D45);
  - the updater is built against a local test feed until the user's Apple identity and a release feed exist;
  - full Rex: the Rex field and glass panes around the chat list and conversation;
  - no plaintext chat-list snapshot: names and previews never leave SQLCipher for startup.

**Reasoning:** a separate identity means this app can never open the real ZapFast's archive while it runs; the budgets are the framework's, applied to a real app.

## D53. Private AppKit API: a build tag and a runtime switch, both required (phase 6, 2026-10-19)

**Decision.**
- **Every use of a private system interface in the framework sits behind both switches:**
  - **the build tag `gnui_publicapi`.** The private selector names and their calls live in files tagged `//go:build !gnui_publicapi`; the tagged-in counterpart takes the public fallback. A Mac App Store build (guideline 2.5.1) uses the tag, and its binary does not contain the names (`TestPublicAPIBuildHasNoPrivateSelectors` builds rexlook both ways and scans it).
  - **the runtime option** `platform.Options.PublicAPIOnly` / `app.Config.PublicAPIOnly`, to run the public path without rebuilding. The platform's accessor `Window.privateAPI()` is false under either switch; each use checks it.
- **Current uses:**
  - **lights** (`platform/lights_private_darwin.go`): `NSWindow`'s `setTitlebarHeight:`, `titlebarHeight`, `setStandardWindowButtonGroupOffset:` and `standardWindowButtonGroupOffset`. AppKit places the window buttons of a custom titlebar at `TrafficLights`, the offset calibrated against where the close button lands. They are also used only when all four respond, their type encodings are the expected double and two-double `NSSize` forms (checked once per process), and calibration lands; a failed calibration puts AppKit's height and offset back.
  - **the native shell's materials and backdrop** (`platform/material_private_darwin.go`): `NSGlassEffectView`'s `set_variant:` and `set_adaptiveAppearance:` (the sidebar's and the inspector's glass, as `NSSplitViewController` configures its own), the `_variant` and `_adaptiveAppearance` probe that reads those values from a throwaway split view on a macOS version not listed, and `NSWindow`'s `_cornerRadius` (the backdrop glass follows the window's corners). Each selector is also used only where the view or window responds to it.
- **The public fallback, for lights:** the buttons stay at AppKit's default place (centre about (16, 16) on macOS 27), not at `TrafficLights`. `Window.SystemButtons` reports where they are, so ui keeps content clear of them.
- **The public fallback, for the materials:** the sidebar and the inspector are regular public `NSGlassEffectView` glass (it stays lighter when the window is inactive than AppKit's split-view glass), and the backdrop glass keeps square corners, which the window's own mask rounds.

**Reasoning:** moving the buttons from outside (Electron's `trafficLightPosition`) lasts only until AppKit's next titlebar layout (a title, key or appearance change, full screen), and the group's hover area follows AppKit's model, not the moved frames; holding it from outside needs the private `_mouseInGroup:` anyway (Electron, Godot, iTerm2). With AppKit's own metrics every layout pass places the buttons, their hover areas and the zoom button's tiling menu. The selectors exist on macOS 15 and 27, the systems tested. The tag keeps the names out of a store binary; the runtime switch lets tests and apps exercise the public path in an ordinary build.

**Alternatives:** an `NSToolbar` (fixed centres only: (26, 26) unified, (19, 20) unifiedCompact, and its title area takes the presses meant for tabs drawn in the band); a titlebar accessory (the buttons stay at (16, 16)); re-applied frames (hover areas stay at AppKit's default place); a runtime switch alone (the names stay in the binary).

## D54. Blur over our own content is drawn by our renderer (phase 6, 2026-10-20)

**Decision.** The scroll edge and the toolbar band blur what scrolls under them with a backdrop-blur primitive in our renderer, not with native views over the Metal layer. It follows AppKit's measured recipe: for the band and table headers the "Hard" edge (a blur of radius 6 at ¼ scale, the window colour mixed in at 0.8245 plus 0.03, and a 1-px hairline); for plain scroll views the softer "ThinFilm" recipe. Metal comes first, then D3D11 and GL. Until a backend has it, and under Reduce Transparency, the band is the opaque fill measured from AppKit's blurred band (`p6-pocketfill`: `#FCFCFC` light, `#2B2B2B` dark, tinted by selected rows under it). Native glass stays where it already works, below our layer: the sidebar, the inspector and the window backdrop (N3, D52).

**Evidence** (`docs/research/glass-over-metal.md`, `p6-glassprobe`, Mac mini, macOS 27.2 26B5101f):
- **Native glass sees our content.** Native backdrops placed over a CAMetalLayer sample it live, in the same frame (30 of 30 flash captures), and the titlebar pocket looks the same over Metal as over AppKit content (ΔE ≤ 0.1). So both paths are possible.
- **Cost.** The drawn blur adds 0.07–0.16 ms of GPU time per frame (GPU busy 18.5 % against 15 % with no blur), against 22 % for the native titlebar pocket and 33 % for native toolbar items. Idle stays at 0 presents.
- **Look.** The drawn Hard recipe is within mean ΔE 0.23 (light) and 0.24 (dark) of AppKit; over saturated colour it is ΔE 2.50 (light) and 0.61 (dark).
- **Native glass above our layer** needs a second, transparent foreground layer for the toolbar glyphs, hover and press. Keeping it in step costs about 1.4 ms of main-thread wait per frame (`presentsWithTransaction`: 0 out-of-step frames in 7,129); without that wait, frames get out of step (1 in 4,259, and 2 in 2,128 by another method). An `NSToolbar` also takes every click in the band, even where it is empty.

**Reasoning:**
- The drawn blur changes in the same frame as our scroll state and works in an inactive window.
- It appears in offscreen renders and golden tests.
- Windows and Linux share it.
- It costs a fraction of the native paths, and it needs no second drawable or main-thread wait.

**Risks:**
- The recipe's numbers come from AppKit's private layer names and may change in a later macOS; the probe re-reads them (`-phases inactive-dump`).
- The Liquid Glass slider, Increase Contrast and full screen were not measured, so the drawn band does not follow them yet.

**Alternatives:**
- **Native glass capsules over a drawn pocket,** with a foreground layer for the glyphs (effort L). Possible later, decided by eye once the drawn band ships, but it costs the second drawable, the ~1.4 ms wait, and AppKit's own press response.
- **Hosting an `NSToolbar`.** Rejected: it takes the band's clicks.
- **A transparent `NSScrollView` over our layer, to borrow its pocket.** Rejected: the colour is wrong, and it relies on behaviour AppKit doesn't promise.
