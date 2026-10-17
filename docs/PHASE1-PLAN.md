# Phase 1 plan: platform spike

Scope (from the brief): on all three OSes, open a window, receive events,
handle DPI, wake the loop from a worker, and clear + present with vsync.
Measure startup, idle CPU, memory and binary size, then make a go/no-go
call.

## Deliverables

| Path | What |
|---|---|
| `internal/ffi` | Zero-alloc call + callback trampolines: darwin & linux, arm64 & amd64. Tests cover correctness (int/float/mixed/stack args, float/HFA returns, callbacks) and exact zero-allocation deltas. |
| `internal/objc` | Selector/class caching and typed `msgSend` helpers on `ffi`; one shared IMP for our NSView/NSWindow delegate methods. |
| `platform` | `Window`, `Event`, `Wait(deadline)`, `Wake()`, `LockOSThread` in `init`. Backends: AppKit; Win32; Wayland with X11 fallback. |
| `gpu` (spike subset) | Clear + present only: Metal (`CAMetalLayer`, 2 drawables), D3D11 (flip-discard + waitable), EGL + GL (Wayland `wl_egl_window`, X11). |
| `cmd/spike` | A window that logs events. A worker goroutine wakes the loop every 2 s and changes the clear colour. Space toggles a vsync-paced animation. `-bench` runs scripted sequences and writes a JSON report: startup timeline, presents, frame intervals, exact alloc deltas, memory, wake latency. |
| `tools/startwin` | The external exec → window-visible timer (darwin), promoted from the probe. |
| `.github/workflows/ci.yml` | The matrix from ARCHITECTURE §12. Written, **not pushed**. |
| `docs/measurements/phase1-macos.md` | macOS numbers, plus the go/no-go report. |

## Exit criteria

On macOS (measured here):

1. Exec → window visible with first frame: median ≤ 150 ms warm, measured both externally and in-app (occlusion state "visible").
2. Idle: CPU time unchanged over 60 s, 0 presents, wakeups reported.
3. Worker → loop wake latency measured (target < 1 ms at p99).
4. Animating: frame intervals track the display refresh (vsync). Exactly 0 heap objects and 0 bytes allocated over 600 warmed-up frames, measured with exact allocation deltas. (Phase 1 found that `runtime/metrics` deltas are not exact; `ReadMemStats` is used instead, see DECISIONS D11.)
5. Memory: `phys_footprint` ≤ 40 MB at 960×600 pt @2× after settling. Peak footprint reported.
6. Stripped binary ≤ 5 MB.
7. DPI: the window moved between scales (or a scale change simulated with `-scale`) resizes the drawable correctly.

On Windows and Linux (no machines this session):

- `CGO_ENABLED=0` cross-builds for amd64 and arm64.
- `go vet` clean for every target.
- Unit tests that don't need a display run in CI.
- Every COM vtable and Wayland interface table is checked against its header/XML.
- Runtime behaviour and numbers stay **not measured** until CI or hardware is available.

Go/no-go:

- **Go for phase 2** requires every macOS criterion to pass and Windows/Linux to build clean.
- Windows and Linux carry a "runtime unverified" flag into phase 2, until CI runs.
