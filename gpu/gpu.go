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
//	(*Surface).FrameWaitable() uintptr                Windows: DXGI frame-latency waitable; 0 elsewhere
//	(*Surface).Destroy()
//
// Backends: Metal (darwin), Direct3D 11 (windows), OpenGL 3.3 core / GLES 3.0
// via EGL (linux). Present must not allocate on the Go heap.
package gpu
