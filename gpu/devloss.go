package gpu

// Device loss and recovery.
//
// A GPU device can go away under a running app: a Direct3D 11 timeout
// detection and recovery (TDR), a driver update or a GPU switch removes the
// device (DXGI_ERROR_DEVICE_REMOVED, _RESET or _HUNG from Present or
// ResizeBuffers); an OpenGL context is lost (EGL_CONTEXT_LOST from the swap
// or from making the context current); a Metal command buffer reports
// MTLCommandBufferErrorDeviceRemoved. Every object made on the device is
// gone with it, textures and their contents included.
//
// The three backends report and recover in the same steps:
//
//   - Surface.DeviceLost and Renderer.DeviceLost say a loss was detected (or
//     simulated, SimulateDeviceLoss). Frames drawn after it are not shown.
//   - Surface.Recover builds the surface's device-level objects again on the
//     same window: the device and swap chain (Direct3D), the display,
//     context and window surface (EGL), the device and queue the layer draws
//     with (Metal).
//   - Renderer.Rebuild builds the pipeline again on the new device (the
//     arguments are those of NewRenderer) and creates every live texture
//     again under the same id, size and kind. Their contents are blank: a
//     draw that names one draws nothing until its owner uploads it again.
//     The renderer's Epoch counts the rebuilds, so an owner that keeps its
//     pixels in memory (the text atlas, the icon atlas) compares the epoch it
//     last uploaded at with the renderer's, and uploads in full when they
//     differ. Textures an owner cannot refill (image pages whose pixels were
//     dropped, a video's poster) are the owner's to make again.
//
// The app package does all of this between frames (App.recoverDevice).

// lossState is what a Surface and a Renderer remember of device loss.
type lossState struct {
	epoch uint32 // recoveries completed
	lost  bool
}

// Epoch counts the times the object was recovered (Surface.Recover,
// Renderer.Rebuild): 0 until a device loss. An owner of textures compares it
// with the value it last uploaded at, to upload again when the renderer
// rebuilt.
func (l *lossState) Epoch() uint32 { return l.epoch }

// DeviceLost reports whether a device loss was detected, or simulated, and
// not recovered from yet.
func (l *lossState) DeviceLost() bool { return l.lost }

// SimulateDeviceLoss makes DeviceLost report true, as if the OS had reported
// the device lost, so that the recovery path can be tested without a GPU
// reset. The real device is untouched until Surface.Recover or
// Renderer.Rebuild replaces it.
func (l *lossState) SimulateDeviceLoss() { l.lost = true }
