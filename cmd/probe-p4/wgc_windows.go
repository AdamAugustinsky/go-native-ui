package main

import (
	"time"
	"unsafe"

	"gonativeui/internal/win32"
)

// Windows.Graphics.Capture of our own window into a free-threaded frame
// pool on a D3D11 device, polled with TryGetNextFrame (no delegate).

// Interface ids (windows.graphics.capture.h, windows.graphics.capture.interop.h,
// windows.graphics.directx.direct3d11.h, windows.foundation.h, dxgi.h).
// Verify on first CI run.
var (
	iidGraphicsCaptureSessionStatics      = guid("2224A540-5974-49AA-B232-0882536F4CB5")
	iidGraphicsCaptureItemInterop         = guid("3628E81B-3CAC-4C60-B7F4-23CE0E0C3356")
	iidGraphicsCaptureItem                = guid("79C3F95B-31F7-4EC2-A464-632EF5D30760")
	iidDirect3D11CaptureFramePoolStatics2 = guid("589B103F-6BBC-5DF5-A991-02E28B3B66D5")
	iidDirect3DDevice                     = guid("A37624AB-8D5F-4650-9D3E-9EAE3D9BC670")
	iidClosable                           = guid("30D5A829-7FA4-4026-83BB-D75BAE4EA99E")
	iidDXGIDevice                         = guid("54ec77fa-1377-44e6-8c32-88fd5f44c84c")
)

// Vtable slots. WinRT interfaces start after IInspectable
// (win32.InspectableFirst = 6); the interop interface is a plain IUnknown.
const (
	vtSessionStaticsIsSupported  = win32.InspectableFirst + 0 // IGraphicsCaptureSessionStatics::IsSupported
	vtItemInteropCreateForWindow = 3                          // IGraphicsCaptureItemInterop::CreateForWindow
	vtItemGetSize                = win32.InspectableFirst + 1 // IGraphicsCaptureItem: get_DisplayName 6, get_Size 7
	vtPoolStatics2CreateFree     = win32.InspectableFirst + 0 // IDirect3D11CaptureFramePoolStatics2::CreateFreeThreaded
	vtPoolTryGetNextFrame        = win32.InspectableFirst + 1 // IDirect3D11CaptureFramePool: Recreate 6, TryGetNextFrame 7
	vtPoolCreateCaptureSession   = win32.InspectableFirst + 4 // … add_FrameArrived 8, remove_FrameArrived 9, CreateCaptureSession 10
	vtSessionStartCapture        = win32.InspectableFirst + 0 // IGraphicsCaptureSession::StartCapture
	vtFrameSystemRelativeTime    = win32.InspectableFirst + 1 // IDirect3D11CaptureFrame: get_Surface 6, get_SystemRelativeTime 7
	vtFrameContentSize           = win32.InspectableFirst + 2 // get_ContentSize 8
	vtClosableClose              = win32.InspectableFirst + 0 // IClosable::Close
	dxgiFormatB8G8R8A8           = 87                         // DirectXPixelFormat::B8G8R8A8UIntNormalized
)

type wgcResult struct {
	Supported      *bool             `json:"supported"`
	Steps          map[string]string `json:"steps"`
	Driver         string            `json:"d3dDriver,omitempty"` // hardware or warp
	ItemSize       [2]int32          `json:"itemSize"`
	Frames         int               `json:"frames"`
	Seconds        float64           `json:"seconds"`
	FirstFrameMs   float64           `json:"firstFrameMs"`            // from StartCapture
	FirstSysTime   int64             `json:"firstSystemRelativeTime"` // 100-ns units (QPC time base)
	LastSysTime    int64             `json:"lastSystemRelativeTime"`  // 100-ns units
	FirstContent   [2]int32          `json:"firstContentSize"`
	FrameInterval  float64           `json:"meanFrameIntervalMs"`
	CapturedWindow bool              `json:"capturedOwnWindow"`
}

func probeWGC(seconds float64) *wgcResult {
	r := &wgcResult{Steps: map[string]string{}, Seconds: seconds}
	if !win32.WinRTAvailable() {
		r.Steps["combase"] = "missing"
		return r
	}
	r.Steps["RoInitialize"] = hrText(win32.RoInitialize(win32.RoInitMultiThreaded))

	var statics uintptr
	hr := win32.RoGetActivationFactory("Windows.Graphics.Capture.GraphicsCaptureSession", &iidGraphicsCaptureSessionStatics, &statics)
	r.Steps["GraphicsCaptureSession statics"] = hrText(hr)
	if hr < 0 {
		return r
	}
	var ok byte
	hr = win32.HR(cm(statics, vtSessionStaticsIsSupported, uintptr(unsafe.Pointer(&ok))))
	win32.Release(statics)
	r.Steps["IsSupported"] = hrText(hr)
	if hr >= 0 {
		s := ok != 0
		r.Supported = &s
	}
	if hr < 0 || ok == 0 {
		return r
	}

	hwnd := newWindow("probe-p4 capture")
	if hwnd == 0 {
		r.Steps["CreateWindowExW"] = "failed"
		return r
	}
	defer call(pDestroyWindow, hwnd)
	for i := 0; i < 10; i++ { // let it show
		pump()
		time.Sleep(10 * time.Millisecond)
	}

	dev, driver, hr := d3dDevice()
	r.Steps["D3D11CreateDevice"], r.Driver = hrText(hr), driver
	if hr < 0 {
		return r
	}
	defer win32.Release(dev)
	var dxgi, insp, d3d uintptr
	hr = win32.QueryInterface(dev, &iidDXGIDevice, &dxgi)
	if hr >= 0 {
		hr = win32.HR(call(win32.Proc(win32.LoadSystem("d3d11.dll"), "CreateDirect3D11DeviceFromDXGIDevice"), dxgi, uintptr(unsafe.Pointer(&insp))))
		win32.Release(dxgi)
	}
	r.Steps["CreateDirect3D11DeviceFromDXGIDevice"] = hrText(hr)
	if hr < 0 {
		return r
	}
	hr = win32.QueryInterface(insp, &iidDirect3DDevice, &d3d)
	win32.Release(insp)
	r.Steps["IDirect3DDevice"] = hrText(hr)
	if hr < 0 {
		return r
	}
	defer win32.Release(d3d)

	var interop, item uintptr
	hr = win32.RoGetActivationFactory("Windows.Graphics.Capture.GraphicsCaptureItem", &iidGraphicsCaptureItemInterop, &interop)
	r.Steps["GraphicsCaptureItem interop"] = hrText(hr)
	if hr < 0 {
		return r
	}
	hr = win32.HR(cm(interop, vtItemInteropCreateForWindow, hwnd, uintptr(unsafe.Pointer(&iidGraphicsCaptureItem)), uintptr(unsafe.Pointer(&item))))
	win32.Release(interop)
	r.Steps["CreateForWindow"] = hrText(hr)
	if hr < 0 {
		return r
	}
	defer win32.Release(item)
	var size [2]int32 // SizeInt32 (windows.graphics.h)
	cm(item, vtItemGetSize, uintptr(unsafe.Pointer(&size)))
	r.ItemSize = size

	var poolStatics, pool, session uintptr
	hr = win32.RoGetActivationFactory("Windows.Graphics.Capture.Direct3D11CaptureFramePool", &iidDirect3D11CaptureFramePoolStatics2, &poolStatics)
	r.Steps["Direct3D11CaptureFramePool statics2"] = hrText(hr)
	if hr < 0 {
		return r
	}
	// SizeInt32 by value: 8 bytes, one integer register on x64 and arm64.
	sizeArg := uintptr(uint32(size[0])) | uintptr(uint32(size[1]))<<32
	hr = win32.HR(cm(poolStatics, vtPoolStatics2CreateFree, d3d, dxgiFormatB8G8R8A8, 2, sizeArg, uintptr(unsafe.Pointer(&pool))))
	win32.Release(poolStatics)
	r.Steps["CreateFreeThreaded"] = hrText(hr)
	if hr < 0 {
		return r
	}
	defer closeAndRelease(pool)
	hr = win32.HR(cm(pool, vtPoolCreateCaptureSession, item, uintptr(unsafe.Pointer(&session))))
	r.Steps["CreateCaptureSession"] = hrText(hr)
	if hr < 0 {
		return r
	}
	defer closeAndRelease(session)
	hr = win32.HR(cm(session, vtSessionStartCapture))
	r.Steps["StartCapture"] = hrText(hr)
	if hr < 0 {
		return r
	}

	start := time.Now()
	end := start.Add(time.Duration(seconds * float64(time.Second)))
	for i := 0; time.Now().Before(end); i++ {
		if i%3 == 0 {
			repaint(hwnd, i)
		}
		pump()
		for {
			var frame uintptr
			if win32.HR(cm(pool, vtPoolTryGetNextFrame, uintptr(unsafe.Pointer(&frame)))) < 0 || frame == 0 {
				break
			}
			var t int64 // TimeSpan (windows.foundation.h): 100-ns ticks
			cm(frame, vtFrameSystemRelativeTime, uintptr(unsafe.Pointer(&t)))
			if r.Frames == 0 {
				r.FirstFrameMs = float64(time.Since(start).Microseconds()) / 1000
				r.FirstSysTime = t
				var cs [2]int32
				cm(frame, vtFrameContentSize, uintptr(unsafe.Pointer(&cs)))
				r.FirstContent = cs
			}
			r.LastSysTime = t
			r.Frames++
			closeAndRelease(frame)
		}
		time.Sleep(5 * time.Millisecond)
	}
	r.CapturedWindow = r.Frames > 0
	if r.Frames > 1 {
		r.FrameInterval = float64(r.LastSysTime-r.FirstSysTime) / 10_000 / float64(r.Frames-1)
	}
	return r
}

// closeAndRelease closes a WinRT object (IClosable) and drops our
// reference.
func closeAndRelease(obj uintptr) {
	var c uintptr
	if win32.QueryInterface(obj, &iidClosable, &c) >= 0 {
		cm(c, vtClosableClose)
		win32.Release(c)
	}
	win32.Release(obj)
}

// d3dDevice makes a BGRA-capable D3D11 device, hardware first, then WARP
// (as gpu/d3d11_windows.go does).
func d3dDevice() (dev uintptr, driver string, hr int32) {
	create := win32.Proc(win32.LoadSystem("d3d11.dll"), "D3D11CreateDevice")
	if create == 0 {
		return 0, "", win32.EProcNotFound
	}
	levels := [...]uint32{0xb100, 0xb000, 0xa100, 0xa000} // D3D_FEATURE_LEVEL_11_1 … 10_0 (d3dcommon.h)
	const bgra, sdk = 0x20, 7                             // D3D11_CREATE_DEVICE_BGRA_SUPPORT, D3D11_SDK_VERSION (d3d11.h)
	for _, d := range []struct {
		name string
		typ  uintptr
	}{{"hardware", 1}, {"warp", 5}} { // D3D_DRIVER_TYPE_HARDWARE, _WARP
		var lvl uint32
		var ctx uintptr
		hr = win32.HR(call(create, 0, d.typ, 0, bgra, uintptr(unsafe.Pointer(&levels[0])), uintptr(len(levels)), sdk,
			uintptr(unsafe.Pointer(&dev)), uintptr(unsafe.Pointer(&lvl)), uintptr(unsafe.Pointer(&ctx))))
		if hr >= 0 {
			win32.Release(ctx)
			return dev, d.name, hr
		}
	}
	return 0, "", hr
}
