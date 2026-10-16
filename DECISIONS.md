# Decisions

Each entry records the decision, the evidence, the alternatives and the
cost. "M:" points to measurements in
[docs/measurements/phase0-macos.md](docs/measurements/phase0-macos.md).
Status: **proposed**, awaiting review at the end of phase 0.

## D1. Toolchain and targets

- Go 1.27.x, module `gonativeui` (placeholder; final name needs your approval).
- `CGO_ENABLED=0` on every target. Every target cross-compiles from any host.
- Targets:
  - darwin/arm64 + amd64, macOS 13+ (Go 1.27's floor)
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
| `golang.org/x/sys` | `windows`: `NewLazySystemDLL` (System32-only loading), handles, constants. `unix`: `eventfd`, `poll`, `inotify`, `getdents64`, `statx`. | Only the referenced symbols get linked; tens of KB | no | BSD-3-Clause |
| `github.com/go-text/typesetting` | **Proposed for Linux text only if D9 option B is chosen.** Not added yet. | +2.1–3.4 MB | no | Unlicense OR BSD-3; `harfbuzz/` is MIT |

System libraries we call (OS-provided, not bundled):

- **macOS:** AppKit, Foundation, CoreFoundation, QuartzCore, Metal, CoreText, CoreGraphics, CoreServices (FSEvents), libobjc, libSystem.
- **Windows:** kernel32, user32, gdi32, shcore, dwmapi, imm32, shell32, ole32, d3d11, dxgi, dwrite.
- **Linux:**
  - required: libc, libwayland-client, libwayland-egl, libEGL, libxkbcommon, libX11
  - optional: libwayland-cursor, libXcursor
  - D9 option A: libfontconfig, libfreetype, libharfbuzz

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

## D4. purego version

**Decision.** In phase 1, pin `github.com/ebitengine/purego` to commit
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
- Frame pacing: `CADisplayLink` from `NSView` on macOS 14+, `CVDisplayLink` on 13.

**Evidence (M:2):**
- The purego probe shows the approach works with no cgo: 2.1 MB binary, first frame committed 83 ms after `main`, 0 CPU while idle, 34 MB footprint.
- AppKit setup dominates startup: `NSApplication` ≈ 26 ms, the first `NSWindow` ≈ 26 ms, order-front ≈ 12 ms, `finishLaunching` ≈ 10 ms. Metal device creation is 0.17 ms, and the clear plus present is 4 ms.
- Ebitengine uses the same approach in production: `objc.RegisterClass` for `NSView` + `NSTextInputClient`, `PostEmptyEvent` via `postEvent`, and `CAMetalDisplayLink`/`CVDisplayLink`. That is according to the purego/Ebitengine research report; we did not copy their code.

**Open in phase 1.**
- Exec → window on screen is 171 ms measured externally, against 83 ms internally; it drops to 139 ms with an explicit `[CATransaction flush]` (M:2). Disabling the window animation is inconclusive so far.
- The 85 MB peak footprint at startup; it is still 84 MB one second after the window appears.

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

**Alternatives:**
- **cgo + libwayland (Gio):** loses cross-compilation.
- **libdecor:** loads GTK, so it's excluded.
- **Pure-Go X11 wire protocol:** EGL/GLX need an Xlib `Display*`.

## D9. Text stack

**Decision:**
- macOS: **CoreText**.
- Windows: **DirectWrite**.
- Linux: **open, you choose:**
  - **Option A (recommended):** system fontconfig + HarfBuzz + FreeType via `dlopen`, plus a small UAX #9/#29 table for bidi/graphemes.
  - **Option B:** go-text/typesetting for shaping, bidi and segmentation; fontconfig via `dlopen` for discovery and fallback; a Go rasterizer.

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
- Zero-allocation frames are a CI gate. The gate asserts exact `runtime/metrics` allocation deltas over warmed-up, isolated frame sequences; `testing.AllocsPerRun` is used only for micro hot paths.

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

- **Startup:**
  - an external timer from exec until the child's first layer-0 window is on screen (`probes/startwin`, `CGWindowListCopyWindowInfo`);
  - an internal timeline (process start → first present).
  - Warm runs are reported as a median of ≥ 5. "Cold after reboot" is reported separately when available. The first launch of a new binary is excluded, since it includes the Gatekeeper scan.
- **Memory:**
  - macOS `phys_footprint` (Activity Monitor's "Memory"), split into graphics and non-graphics;
  - RSS reported but not gated, since it includes shared framework pages;
  - Windows: Private Working Set + commit;
  - Linux: PSS (`smaps_rollup`).
- **Idle:** CPU-time delta over 60 s, presents counted in-app, and wakeups from `top -stats idlew` on macOS. Windows uses ETW/PresentMon; Linux uses `/proc/<pid>/stat` and `perf trace`.
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

## D16. Licensing

- Our code: license to be chosen with the name.
- NOTICE lists:
  - purego (Apache-2.0), which our ffi technique follows (runtime.cgocall + crosscall2);
  - x/sys (BSD-3).
- No third-party source is copied at present.

## Questions for review

1. **D9 Linux text:** do system FreeType/HarfBuzz/fontconfig via `dlopen` count as "system libraries" for this project (option A), or do you prefer pure-Go go-text, at +2–3 MB and no hinting (option B)?
2. **D4:** OK to pin purego to an unreleased commit until the next tag?
3. **Testing without Windows/Linux machines:**
   - (a) a private GitHub repo with CI (needs your OK to push);
   - (b) Wine via Homebrew on this Mac for Windows smoke tests (functional only, ~1.5 GB);
   - (c) wait for hardware.
4. **Minimum macOS:** stay at 13 (Go's floor, needs a `CVDisplayLink` fallback) or raise to 14 (`CADisplayLink` only)?
