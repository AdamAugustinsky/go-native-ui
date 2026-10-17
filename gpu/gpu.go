// Package gpu presents frames to a platform window.
//
// Phase 1 implements only what the platform spike needs: clear to a colour
// and present, paced by the platform's frame contract. The phase 2 renderer
// (instanced quads, glyph atlas, images) extends the same per-OS types.
//
// Every backend exposes:
//
//	NewSurface(n platform.Native, w, h int32, scale float32) (*Surface, error)
//	(*Surface).Resize(w, h int32, scale float32)      physical pixels
//	(*Surface).Present(r, g, b, a float32) bool       clear + present; false if nothing was presented
//	(*Surface).FrameSource() uintptr                  pass to Window.SetFrameSource: DXGI waitable (Windows), CAMetalLayer (macOS), 0 (Linux)
//	(*Surface).Destroy()
//
// Backends: Metal (darwin), Direct3D 11 (windows), OpenGL 3.3 core / GLES 3.0
// via EGL (linux). Present must not allocate on the Go heap.
package gpu

import "sync/atomic"

// FirstPresentTrace holds platform.Now timestamps of the stages of a
// surface's first Present, for startup analysis. Backends that don't record
// a stage leave it 0.
type FirstPresentTrace struct {
	Start     int64 // Present entered
	Acquired  int64 // drawable / back buffer acquired
	Committed int64 // commands committed and present scheduled
	Done      int64 // Present returning (after any first-frame flush)
	// OnGlass is when the first frame was shown on the display, from the OS
	// present feedback (Metal: presentedTime, or the presented handler's call
	// time where presentedTime reads 0). Written from another thread. 0 = not
	// (yet) known.
	OnGlass atomic.Int64
}
