// Command probe-p2 is phase 8's probe P2 (docs/PHASE8-PLAN.md, D66): does a
// child window over a D3D11 flip-model swap chain sit above the swap chain's
// content, is it clipped by a container child window, and how many frames
// does a moved child trail a "scrolled list" drawn by the swap chain?
//
// It decides D66's fallback: if a child visibly trails a scroll (one frame or
// more, often), hosts are hidden during momentum scrolls and shown at rest.
//
// # What it does
//
// A top-level WS_POPUP window with WS_CLIPCHILDREN (400×300 physical
// pixels, topmost) gets a flip-model swap chain made as gpu/d3d11_windows.go
// makes one (hardware device, else WARP; FLIP_DISCARD, else FLIP_SEQUENTIAL;
// DXGI_ALPHA_MODE_IGNORE; frame-latency waitable), cleared blue. A grey
// container child window (the clip, client 220..380 × 50..250) holds a green
// child window (140×20). The checks:
//
//   - at rest, with the child half outside the container: the pixels of the
//     child inside the container are green, those outside it are the swap
//     chain's blue (clipping), and WindowFromPoint at the child's visible part
//     returns the child (z-order). Read twice: from the screen (BitBlt of the
//     desktop DC, CAPTUREBLT) and from PrintWindow(PW_RENDERFULLCONTENT);
//   - the scroll: for -frames frames the swap chain draws a red band at y(t)
//     (ID3D11DeviceContext1::ClearView with one rect) in the left column and
//     presents (Present(1, 0)); right after the present the child moves to the
//     same y(t) in one BeginDeferWindowPos/EndDeferWindowPos batch (D66's
//     order). DwmFlush then waits for the composition that takes the present
//     in, and the window's rectangle is read from the screen. The band's top
//     row and the child's top row give the offset, in frames at -speed pixels
//     per frame.
//
// The plan names Windows.Graphics.Capture frame timestamps for this. WGC
// needs a WinRT frame pool, a Direct3D11 interop device and a delegate, about
// as much code again as the rest of the probe; a BitBlt of the composed
// desktop after DwmFlush shows the same thing (what DWM composed, with the
// present and the window move in it or not) one composition at a time, which
// is the question. If BitBlt reads nothing (a locked or session-0 desktop),
// capture_ok is false and the trailing numbers are absent.
//
// # Running it on a CI runner
//
//	go run ./cmd/probe-p2                    # 120 frames at 4 px/frame
//	go run ./cmd/probe-p2 -frames 240 -speed 2 -timeout 90s
//
// It needs the runner's interactive desktop (GitHub's windows-2025 and
// windows-11-arm images run jobs in one).
//
// # Output
//
// One JSON object on stdout: driver ("hardware" or "warp"), adapter,
// swap_effect, feature_level, refresh (DwmGetCompositionTimingInfo's rate
// and period in ms, with its HRESULT), rest (the checks at rest, from the
// screen and from PrintWindow: child_visible, clipped, swapchain_beside,
// container_visible, window_from_point_is_child), capture_ok, frames,
// sampled, offsets (frames of offset → count; negative means the child shows
// an older position than the band, i.e. the child trails), trailing ("0",
// "1", "2+" by absolute offset), child_trails and band_trails counts,
// trailing_valid (false when no frame was sampled: the screen read did not
// show our window), covering (when the screen read missed our window: the
// top-level window at the sample point and the foreground window, by class,
// title, process and rectangle), and errors. Exit status 0 when it answered (whatever the answers), 1 when it
// could not run (no D3D11, no window, the timeout).
package main
