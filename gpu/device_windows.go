//go:build windows

package gpu

import (
	"errors"
	"sync/atomic"

	"gonativeui/internal/win32"
	"gonativeui/platform"
)

// Device is the Direct3D 11 device and immediate context an app's windows
// share (DECISIONS D30, ported to Windows by the phase 8 plan's D68): every
// window's swap chain is made on it (NewSurfaceOn), so one renderer, with
// one set of textures and one glyph atlas, draws into all of them. Every
// call on the immediate context is on the main thread, one window's frame
// after another; Renderer.Encode sets all the state it needs each time.
type Device struct {
	dev, ctx   uintptr // ID3D11Device, ID3D11DeviceContext (one reference each)
	levels     [4]uint32
	featureLvl uint32
}

// NewDevice creates a hardware device, falling back to WARP (what the CI
// runners have), and its immediate context. Release it once every surface
// and renderer on it is destroyed (each surface keeps its own references).
func NewDevice() (*Device, error) {
	d := &Device{}
	if err := createD3DDevice(&d.levels, &d.dev, &d.featureLvl, &d.ctx); err != nil {
		return nil, err
	}
	return d, nil
}

// Handles are the ID3D11Device and the immediate ID3D11DeviceContext, for
// NewRenderer.
func (d *Device) Handles() (device, queue uintptr) { return d.dev, d.ctx }

// Release releases the device's references to the device and the context.
func (d *Device) Release() {
	if d == nil || d.dev == 0 {
		return
	}
	win32.Release(d.ctx)
	win32.Release(d.dev)
	d.dev, d.ctx = 0, 0
}

// NewSurfaceOn creates a swap chain for the window on the shared device d.
// The surface holds its own references to the device and the context, so
// it may outlive d's Release until it is destroyed. w and h are physical
// pixels.
func NewSurfaceOn(d *Device, n platform.Native, w, h int32, scale float32) (*Surface, error) {
	if d == nil || d.dev == 0 {
		return nil, errors.New("gpu: no Direct3D 11 device")
	}
	s := &Surface{hwnd: n.HWND, w: max(w, 1), h: max(h, 1)}
	s.dev, s.ctx = d.dev, d.ctx
	win32.AddRef(s.dev)
	win32.AddRef(s.ctx)
	if err := s.initChain(); err != nil {
		s.Destroy()
		return nil, err
	}
	liveSurfaces.Add(1)
	s.counted = true
	return s, nil
}

// liveSurfaces counts the surfaces made and not destroyed, for the windows'
// open/close leak gate.
var liveSurfaces atomic.Int64

// LiveSurfaces is how many surfaces exist (made by NewSurface or
// NewSurfaceOn, not yet destroyed). Any goroutine.
func LiveSurfaces() int64 { return liveSurfaces.Load() }
