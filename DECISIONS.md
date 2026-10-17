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

- **macOS:** AppKit, Foundation, CoreFoundation, QuartzCore, Metal, CoreText, CoreGraphics, CoreServices (FSEvents), libobjc, libSystem.
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
- a fixed `Frame` holding integer, float and stack slots, kept per OS thread in long-lived memory;
- a small table of callback entry points that reaches a non-reflective Go dispatcher through `crosscall2`.

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
- Windows: `FindFirstFileExW` (basic info, large fetch) vs `GetFileInformationByHandleEx`; choose by measurement in phase 4.
- Linux: `getdents64` + `statx` vs `os.ReadDir`; measure in phase 4.
- Watching: FSEvents, `ReadDirectoryChangesW`, inotify.
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

