//go:build windows

package gpu

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"gonativeui/internal/win32"
	"gonativeui/platform"
)

// ---- COM vtable slots -------------------------------------------------------
//
// Every slot below was checked against the mingw-w64 headers
// (mingw-w64-headers/include/*.h, master, fetched 2026-10-16) by extracting
// the method order of each C `typedef struct <Interface>Vtbl`. Slot numbers
// are zero-based and include IUnknown (QueryInterface 0, AddRef 1, Release 2)
// and all inherited interfaces.

// IDXGIObject (dxgi.h:312-341): QueryInterface 0, AddRef 1, Release 2,
// SetPrivateData 3, SetPrivateDataInterface 4, GetPrivateData 5, GetParent 6.
// IDXGIFactory/1/2 and IDXGIDevice and IDXGISwapChain all derive from it.
const vtDXGIObjectGetParent = 6

// IDXGIDevice (dxgi.h:2016-2073): IDXGIObject 0-6, GetAdapter 7.
const vtDXGIDeviceGetAdapter = 7

// IDXGIFactory (dxgi.h:1839-1894): IDXGIObject 0-6, EnumAdapters 7,
// MakeWindowAssociation 8, GetWindowAssociation 9, CreateSwapChain 10,
// CreateSoftwareAdapter 11. IDXGIFactory1 (dxgi.h:2523-2589) adds 12-13.
// IDXGIFactory2 (dxgi1_2.h:1483-1608) adds IsWindowedStereoEnabled 14,
// CreateSwapChainForHwnd 15.
const (
	vtDXGIFactoryMakeWindowAssociation   = 8
	vtDXGIFactory2CreateSwapChainForHwnd = 15
)

// IDXGISwapChain (dxgi.h:1607-1693): IDXGIObject 0-6, IDXGIDeviceSubObject
// GetDevice 7, Present 8, GetBuffer 9, SetFullscreenState 10,
// GetFullscreenState 11, GetDesc 12, ResizeBuffers 13, ResizeTarget 14,
// GetContainingOutput 15, GetFrameStatistics 16, GetLastPresentCount 17.
// IDXGISwapChain1 (dxgi1_2.h:1130-1263) continues to 28 (SetRotation 27,
// GetRotation 28). IDXGISwapChain2 (dxgi1_3.h:387-550) adds SetSourceSize 29,
// GetSourceSize 30, SetMaximumFrameLatency 31, GetMaximumFrameLatency 32,
// GetFrameLatencyWaitableObject 33, SetMatrixTransform 34, GetMatrixTransform 35.
const (
	vtSwapChainPresent                        = 8
	vtSwapChainGetBuffer                      = 9
	vtSwapChainResizeBuffers                  = 13
	vtSwapChain2SetMaximumFrameLatency        = 31
	vtSwapChain2GetFrameLatencyWaitableObject = 33
)

// ID3D11Device (d3d11.h:10592-10823): IUnknown 0-2, CreateBuffer 3,
// CreateTexture1D 4, CreateTexture2D 5, CreateTexture3D 6,
// CreateShaderResourceView 7, CreateUnorderedAccessView 8,
// CreateRenderTargetView 9, ... GetDeviceRemovedReason 39,
// GetImmediateContext 40.
const (
	vtDeviceCreateRenderTargetView = 9
	vtDeviceGetDeviceRemovedReason = 39
)

// ID3D11DeviceContext (d3d11.h:6729-7370): IUnknown 0-2, then
// ID3D11DeviceChild GetDevice 3, GetPrivateData 4, SetPrivateData 5,
// SetPrivateDataInterface 6 (d3d11.h:2674-2702), then VSSetConstantBuffers 7
// ... OMSetRenderTargets 33 (line 6916), ClearRenderTargetView 50 (line 7018),
// ClearState 110 (line 7367), Flush 111 (line 7370).
const (
	vtContextOMSetRenderTargets    = 33
	vtContextClearRenderTargetView = 50
	vtContextClearState            = 110
	vtContextFlush                 = 111
)

// The renderer's slots (renderer_windows.go) are listed and sourced there.

// ---- GUIDs (checked against the MIDL_INTERFACE lines of the same headers) --

var (
	// dxgi.h:1983
	iidDXGIDevice = win32.GUID{Data1: 0x54ec77fa, Data2: 0x1377, Data3: 0x44e6, Data4: [8]byte{0x8c, 0x32, 0x88, 0xfd, 0x5f, 0x44, 0xc8, 0x4c}}
	// dxgi1_2.h:1419
	iidDXGIFactory2 = win32.GUID{Data1: 0x50c83a1c, Data2: 0xe072, Data3: 0x4c48, Data4: [8]byte{0x87, 0xb0, 0x36, 0x30, 0xfa, 0x36, 0xa6, 0xd0}}
	// dxgi1_3.h:352
	iidDXGISwapChain2 = win32.GUID{Data1: 0xa8be2ac4, Data2: 0x199f, Data3: 0x4946, Data4: [8]byte{0xb3, 0x31, 0x79, 0x59, 0x9f, 0xb9, 0x8d, 0xe7}}
	// d3d11.h:5682
	iidD3D11Texture2D = win32.GUID{Data1: 0x6f15aaf2, Data2: 0xd208, Data3: 0x4e89, Data4: [8]byte{0x9a, 0xb4, 0x48, 0x95, 0x35, 0xd3, 0x4f, 0x9c}}
)

// ---- Constants ---------------------------------------------------------------

const (
	d3dDriverTypeHardware = 1           // d3dcommon.h:322
	d3dDriverTypeWarp     = 5           // d3dcommon.h:326
	d3dFeatureLevel11_1   = 0xb100      // d3dcommon.h:336
	d3dFeatureLevel11_0   = 0xb000      // d3dcommon.h:335
	d3dFeatureLevel10_1   = 0xa100      // d3dcommon.h:334
	d3dFeatureLevel10_0   = 0xa000      // d3dcommon.h:333
	d3d11SDKVersion       = 7           // d3d11.h:11043
	d3d11CreateBGRA       = 0x20        // D3D11_CREATE_DEVICE_BGRA_SUPPORT, d3d11.h:11033
	dxgiFormatUnknown     = 0           // dxgiformat.h:31
	dxgiFormatB8G8R8A8    = 0x57        // dxgiformat.h:118
	dxgiUsageRenderTarget = 0x20        // dxgi.h:168
	dxgiScalingStretch    = 0           // dxgi1_2.h:1045
	dxgiAlphaModeIgnore   = 3           // dxgi1_2.h:118
	dxgiEffectFlipSeq     = 3           // dxgi.h:203
	dxgiEffectFlipDiscard = 4           // dxgi.h:204
	dxgiFlagFrameLatency  = 0x40        // DXGI_SWAP_CHAIN_FLAG_FRAME_LATENCY_WAITABLE_OBJECT, dxgi.h:253
	dxgiMWANoAltEnter     = 0x2         // dxgi.h:1796
	dxgiStatusOccluded    = 0x087A0001  // winerror.h:4639
	eInvalidArg           = -2147024809 // 0x80070057
)

// swapChainDesc1 is DXGI_SWAP_CHAIN_DESC1 (dxgi1_2.h).
type swapChainDesc1 struct {
	Width, Height uint32
	Format        uint32
	Stereo        int32
	SampleCount   uint32
	SampleQuality uint32
	BufferUsage   uint32
	BufferCount   uint32
	Scaling       uint32
	SwapEffect    uint32
	AlphaMode     uint32
	Flags         uint32
}

var (
	d3d11DLL           uintptr
	pD3D11CreateDevice uintptr

	// DwmFlush waits for the desktop compositor's next composition pass that
	// takes in the DirectX presents made before the call (the reveal).
	pDwmFlush = win32.Proc(win32.LoadSystem("dwmapi.dll"), "DwmFlush")
)

// Surface is a flip-model swap chain on a window.
//
// Every pointer handed to the OS lives in a field of the heap-allocated
// Surface (or in a package variable), never in a stack local.
type Surface struct {
	dev, ctx uintptr // ID3D11Device, ID3D11DeviceContext
	sc       uintptr // IDXGISwapChain2 (IDXGISwapChain1 if SwapChain2 is unavailable)
	rtv      uintptr // ID3D11RenderTargetView of back buffer 0
	buf      uintptr // scratch out-pointer for GetBuffer
	waitable uintptr // frame-latency waitable (owned: closed in Destroy)
	hwnd     uintptr

	Trace FirstPresentTrace // Start, Acquired, Committed and Done of the first frame; OnGlass is not recorded on this backend yet

	frame     Frame  // Present's frame (kept here so Present doesn't allocate)
	presented int    // frames handed to the swap chain
	lastShown bool   // whether the last EndFrame presented
	reveal    func() // shows the window right after the first frame's present (RevealOnFirstFrame)

	dxgiDev, adapter, factory, sc1 uintptr // creation-time interfaces

	w, h       int32
	color      [4]float32
	levels     [4]uint32
	featureLvl uint32
	desc       swapChainDesc1
	loggedLost bool
	counted    bool // in liveSurfaces (device_windows.go) until Destroy
}

// NewSurface creates a D3D11 device and a swap chain for the window.
// w and h are physical pixels.
func NewSurface(n platform.Native, w, h int32, scale float32) (*Surface, error) {
	w, h = max(w, 1), max(h, 1)
	s := &Surface{hwnd: n.HWND, w: w, h: h}
	if err := s.createDevice(); err != nil {
		s.Destroy()
		return nil, err
	}
	if err := s.initChain(); err != nil {
		s.Destroy()
		return nil, err
	}
	liveSurfaces.Add(1)
	s.counted = true
	return s, nil
}

// initChain makes the swap chain and its render target view on the surface's
// device (its own, or the app's: NewSurfaceOn).
func (s *Surface) initChain() error {
	// Device → adapter → factory (IDXGIFactory2).
	if hr := win32.QueryInterface(s.dev, &iidDXGIDevice, &s.dxgiDev); hr < 0 {
		return hresultError("QueryInterface(IDXGIDevice)", hr)
	}
	if hr := win32.HR(win32.Com(s.dxgiDev, vtDXGIDeviceGetAdapter, uintptr(unsafe.Pointer(&s.adapter)))); hr < 0 {
		return hresultError("IDXGIDevice.GetAdapter", hr)
	}
	if hr := win32.HR(win32.Com(s.adapter, vtDXGIObjectGetParent, uintptr(unsafe.Pointer(&iidDXGIFactory2)), uintptr(unsafe.Pointer(&s.factory)))); hr < 0 {
		return hresultError("IDXGIAdapter.GetParent(IDXGIFactory2)", hr)
	}

	// Flip model swap chain; FLIP_DISCARD needs Windows 10, so fall back to
	// FLIP_SEQUENTIAL (Windows 8+).
	s.desc = swapChainDesc1{
		Width: uint32(s.w), Height: uint32(s.h),
		Format:      dxgiFormatB8G8R8A8,
		SampleCount: 1,
		BufferUsage: dxgiUsageRenderTarget,
		BufferCount: 2,
		Scaling:     dxgiScalingStretch,
		SwapEffect:  dxgiEffectFlipDiscard,
		AlphaMode:   dxgiAlphaModeIgnore,
		Flags:       dxgiFlagFrameLatency,
	}
	hr := s.createSwapChain()
	if hr < 0 {
		s.desc.SwapEffect = dxgiEffectFlipSeq
		hr = s.createSwapChain()
	}
	if hr < 0 {
		return hresultError("IDXGIFactory2.CreateSwapChainForHwnd", hr)
	}
	win32.Com(s.factory, vtDXGIFactoryMakeWindowAssociation, s.hwnd, dxgiMWANoAltEnter)

	// IDXGISwapChain2 provides the frame-latency waitable.
	if win32.QueryInterface(s.sc1, &iidDXGISwapChain2, &s.sc) >= 0 {
		win32.Release(s.sc1)
		s.sc1 = 0
		win32.Com(s.sc, vtSwapChain2SetMaximumFrameLatency, 1)
		s.waitable = win32.Com(s.sc, vtSwapChain2GetFrameLatencyWaitableObject)
	} else {
		s.sc, s.sc1 = s.sc1, 0 // no waitable: the platform paces frames without it
	}

	if err := s.createRTV(); err != nil {
		return err
	}
	win32.Release(s.dxgiDev)
	win32.Release(s.adapter)
	win32.Release(s.factory)
	s.dxgiDev, s.adapter, s.factory = 0, 0, 0
	return nil
}

// createDevice creates a hardware device, falling back to WARP.
func (s *Surface) createDevice() error {
	return createD3DDevice(&s.levels, &s.dev, &s.featureLvl, &s.ctx)
}

// createD3DDevice creates a BGRA-capable device and its immediate context on
// the hardware, falling back to WARP (the software rasterizer, which is what
// headless CI machines have). The pointers must point into heap memory (a
// field of a heap object or a package variable), never at stack locals: the
// OS writes through them.
func createD3DDevice(levels *[4]uint32, dev *uintptr, featureLvl *uint32, ctx *uintptr) error {
	*levels = [4]uint32{d3dFeatureLevel11_1, d3dFeatureLevel11_0, d3dFeatureLevel10_1, d3dFeatureLevel10_0}
	if d3d11DLL == 0 {
		d3d11DLL = win32.LoadSystem("d3d11.dll")
		pD3D11CreateDevice = win32.Proc(d3d11DLL, "D3D11CreateDevice")
	}
	if pD3D11CreateDevice == 0 {
		return errors.New("gpu: cannot load d3d11.dll")
	}
	var hr int32
	for _, driver := range [...]uintptr{d3dDriverTypeHardware, d3dDriverTypeWarp} {
		hr = tryCreateDevice(levels, driver, 0, 4, dev, featureLvl, ctx)
		if hr == eInvalidArg { // Windows 7 without the platform update rejects 11_1
			hr = tryCreateDevice(levels, driver, 1, 3, dev, featureLvl, ctx)
		}
		if hr >= 0 {
			return nil
		}
	}
	return hresultError("D3D11CreateDevice", hr)
}

func tryCreateDevice(levels *[4]uint32, driver, firstLevel, nLevels uintptr, dev *uintptr, featureLvl *uint32, ctx *uintptr) int32 {
	return win32.HR(win32.Call(pD3D11CreateDevice,
		0, driver, 0, d3d11CreateBGRA,
		uintptr(unsafe.Pointer(&levels[firstLevel])), nLevels, d3d11SDKVersion,
		uintptr(unsafe.Pointer(dev)), uintptr(unsafe.Pointer(featureLvl)), uintptr(unsafe.Pointer(ctx))))
}

func (s *Surface) createSwapChain() int32 {
	hr := win32.HR(win32.Com(s.factory, vtDXGIFactory2CreateSwapChainForHwnd,
		s.dev, s.hwnd, uintptr(unsafe.Pointer(&s.desc)), 0, 0, uintptr(unsafe.Pointer(&s.sc1))))
	runtime.KeepAlive(s)
	return hr
}

// createRTV creates the render target view of back buffer 0. For D3D11 flip
// swap chains buffer 0 stays the current back buffer across presents.
func (s *Surface) createRTV() error {
	if hr := win32.HR(win32.Com(s.sc, vtSwapChainGetBuffer, 0, uintptr(unsafe.Pointer(&iidD3D11Texture2D)), uintptr(unsafe.Pointer(&s.buf)))); hr < 0 {
		return hresultError("IDXGISwapChain.GetBuffer", hr)
	}
	hr := win32.HR(win32.Com(s.dev, vtDeviceCreateRenderTargetView, s.buf, 0, uintptr(unsafe.Pointer(&s.rtv))))
	win32.Release(s.buf)
	s.buf = 0
	runtime.KeepAlive(s)
	if hr < 0 {
		return hresultError("ID3D11Device.CreateRenderTargetView", hr)
	}
	return nil
}

// Resize resizes the swap chain buffers (physical pixels). 0×0 is ignored.
func (s *Surface) Resize(w, h int32, scale float32) {
	if s.sc == 0 || w <= 0 || h <= 0 || (w == s.w && h == s.h && s.rtv != 0) {
		return
	}
	// All references to the buffers must be gone before ResizeBuffers.
	win32.Com(s.ctx, vtContextClearState)
	win32.Release(s.rtv)
	s.rtv = 0
	win32.Com(s.ctx, vtContextFlush)
	hr := win32.HR(win32.Com(s.sc, vtSwapChainResizeBuffers, 0, uintptr(w), uintptr(h), dxgiFormatUnknown, dxgiFlagFrameLatency))
	if hr < 0 {
		s.reportLost("IDXGISwapChain.ResizeBuffers", hr)
		return
	}
	s.w, s.h = w, h
	if err := s.createRTV(); err != nil {
		s.reportLost("createRTV", 0)
	}
}

// Device and Queue return the ID3D11Device and the immediate
// ID3D11DeviceContext (the Metal back end's device and command queue), for
// gpu.NewRenderer. The surface keeps owning them.
func (s *Surface) Device() uintptr { return s.dev }

// Queue returns the immediate ID3D11DeviceContext; see Device.
func (s *Surface) Queue() uintptr { return s.ctx }

// Frame is one frame being drawn: render into Target with commands issued on
// CmdBuf, then call EndFrame.
type Frame struct {
	CmdBuf uintptr // ID3D11DeviceContext (the immediate context)
	Target uintptr // ID3D11RenderTargetView of the back buffer, BGRA8 UNORM
	W, H   int     // target size in pixels

	begun bool
}

// BeginFrame returns the frame's context and back-buffer view. It returns
// false if the swap chain has no usable back buffer (a failed resize). The
// back buffer is not cleared; the renderer's Encode (or the caller) does
// that. Every successful BeginFrame must be followed by EndFrame on the same
// thread. It does not allocate.
func (s *Surface) BeginFrame(f *Frame) bool {
	if s.rtv == 0 || s.ctx == 0 {
		return false
	}
	if s.presented == 0 {
		s.Trace.Start = platform.Now()
		s.Trace.Acquired = s.Trace.Start // D3D11 flip chains hand out buffer 0 without waiting
	}
	f.CmdBuf, f.Target = s.ctx, s.rtv
	f.W, f.H = int(s.w), int(s.h)
	f.begun = true
	return true
}

// EndFrame presents the back buffer with vsync (the platform paces frames
// with the swap chain's frame-latency waitable, FrameSource). A frame whose
// presentation was refused (window occluded, device lost) is dropped; Present
// reports it. The Frame is reset.
func (s *Surface) EndFrame(f *Frame) {
	if !f.begun {
		return
	}
	first := s.presented == 0
	if first {
		s.Trace.Committed = platform.Now()
	}
	hr := win32.HR(win32.Com(s.sc, vtSwapChainPresent, 1, 0))
	runtime.KeepAlive(s)
	s.lastShown = true
	switch {
	case hr == dxgiStatusOccluded:
		s.lastShown = false
	case hr < 0:
		s.reportLost("IDXGISwapChain.Present", hr)
		s.lastShown = false
	} // other success codes (e.g. DXGI_STATUS_MODE_CHANGED) count as shown
	s.presented++
	if first {
		if s.reveal != nil && s.lastShown && pDwmFlush != 0 {
			// Present only queues the frame for the compositor. Uncloaked now,
			// the window can be composed once before DWM has taken the frame
			// in: its frame around an empty client area (the window has no
			// surface of its own, WS_EX_NOREDIRECTIONBITMAP; with one, that
			// pass showed it white). It happens on the runners, whose WARP
			// device renders on the CPU. DwmFlush returns after the
			// composition pass that has taken in this present, so the reveal
			// shows the frame.
			win32.Call(pDwmFlush)
		}
		s.Trace.Done = platform.Now()
		if s.reveal != nil {
			reveal := s.reveal
			s.reveal = nil
			reveal()
		}
	}
	*f = Frame{}
}

// Present clears the back buffer to the colour and presents it with vsync.
// It returns false if nothing was presented (occluded or device error).
func (s *Surface) Present(r, g, b, a float32) bool {
	f := &s.frame
	if !s.BeginFrame(f) {
		return false
	}
	s.color = [4]float32{r, g, b, a}
	win32.Com(f.CmdBuf, vtContextOMSetRenderTargets, 1, uintptr(unsafe.Pointer(&f.Target)), 0)
	win32.Com(f.CmdBuf, vtContextClearRenderTargetView, f.Target, uintptr(unsafe.Pointer(&s.color[0])))
	s.EndFrame(f)
	return s.lastShown
}

// RevealOnFirstFrame makes the first EndFrame call show (typically the
// platform Window's Show, for a window opened with Options.Hidden) once the
// desktop compositor has taken in its present: the window is shown but
// cloaked until then (DWM), so no empty frame reaches the screen.
func (s *Surface) RevealOnFirstFrame(show func()) { s.reveal = show }

// FrameSource returns the DXGI frame-latency waitable handle, or 0.
func (s *Surface) FrameSource() uintptr { return s.waitable }

// Destroy releases all resources. The platform must not wait on the
// FrameSource handle afterwards.
func (s *Surface) Destroy() {
	if s.counted {
		s.counted = false
		liveSurfaces.Add(-1)
	}
	if s.ctx != 0 {
		win32.Com(s.ctx, vtContextClearState)
		win32.Com(s.ctx, vtContextFlush)
	}
	if s.waitable != 0 {
		win32.CloseHandle(s.waitable)
		s.waitable = 0
	}
	for _, p := range [...]*uintptr{&s.rtv, &s.buf, &s.sc, &s.sc1, &s.factory, &s.adapter, &s.dxgiDev, &s.ctx, &s.dev} {
		win32.Release(*p)
		*p = 0
	}
}

// reportLost logs a device error once (device removed/reset is fatal in the spike).
func (s *Surface) reportLost(what string, hr int32) {
	if s.loggedLost {
		return
	}
	s.loggedLost = true
	reason := win32.HR(win32.Com(s.dev, vtDeviceGetDeviceRemovedReason))
	fmt.Fprintf(os.Stderr, "gpu: %s failed: HRESULT %#08x (device removed reason %#08x)\n", what, uint32(hr), uint32(reason))
}

func hresultError(what string, hr int32) error {
	return fmt.Errorf("gpu: %s failed: HRESULT %#08x", what, uint32(hr))
}
