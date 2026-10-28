//go:build windows

package main

import (
	"fmt"
	"syscall"
	"unsafe"

	"gonativeui/internal/win32"
)

// The swap chain, made as gpu/d3d11_windows.go makes it (the slot numbers
// and their header lines are the ones cited there).

const (
	vtDXGIObjectGetParent                     = 6  // dxgi.h:312-341
	vtDXGIDeviceGetAdapter                    = 7  // dxgi.h:2016-2073
	vtDXGIAdapterGetDesc                      = 8  // dxgi.h IDXGIAdapterVtbl: EnumOutputs 7, GetDesc 8
	vtDXGIFactory2CreateSwapChainForHwnd      = 15 // dxgi1_2.h:1483-1608
	vtSwapChainPresent                        = 8  // dxgi.h:1607-1693
	vtSwapChainGetBuffer                      = 9
	vtSwapChain2SetMaximumFrameLatency        = 31 // dxgi1_3.h:387-550
	vtSwapChain2GetFrameLatencyWaitableObject = 33
	vtDeviceCreateRenderTargetView            = 9   // d3d11.h:10592-10823
	vtContextClearRenderTargetView            = 50  // d3d11.h:7018
	vtContextFlush                            = 111 // d3d11.h:7370
	// ID3D11DeviceContext1 (d3d11_1.h ID3D11DeviceContext1Vtbl): the 115
	// slots of ID3D11DeviceContext (0-114: ... ClearState 110, Flush 111,
	// GetType 112, GetContextFlags 113, FinishCommandList 114), then
	// CopySubresourceRegion1 115, UpdateSubresource1 116, DiscardResource 117,
	// DiscardView 118, {VS,HS,DS,GS,PS,CS}SetConstantBuffers1 119-124,
	// {VS,HS,DS,GS,PS,CS}GetConstantBuffers1 125-130,
	// SwapDeviceContextState 131, ClearView 132, DiscardView1 133.
	// Verify on first CI run.
	vtContext1ClearView = 132

	d3dDriverTypeHardware = 1    // d3dcommon.h:322
	d3dDriverTypeWarp     = 5    // d3dcommon.h:326
	d3d11SDKVersion       = 7    // d3d11.h:11043
	d3d11CreateBGRA       = 0x20 // d3d11.h:11033
	dxgiFormatB8G8R8A8    = 0x57 // dxgiformat.h:118
	dxgiUsageRenderTarget = 0x20 // dxgi.h:168
	dxgiAlphaModeIgnore   = 3    // dxgi1_2.h:118
	dxgiEffectFlipSeq     = 3    // dxgi.h:203
	dxgiEffectFlipDiscard = 4    // dxgi.h:204
	dxgiFlagFrameLatency  = 0x40 // dxgi.h:253
)

var (
	iidDXGIDevice      = win32.GUID{Data1: 0x54ec77fa, Data2: 0x1377, Data3: 0x44e6, Data4: [8]byte{0x8c, 0x32, 0x88, 0xfd, 0x5f, 0x44, 0xc8, 0x4c}} // dxgi.h:1983
	iidDXGIFactory2    = win32.GUID{Data1: 0x50c83a1c, Data2: 0xe072, Data3: 0x4c48, Data4: [8]byte{0x87, 0xb0, 0x36, 0x30, 0xfa, 0x36, 0xa6, 0xd0}} // dxgi1_2.h:1419
	iidDXGISwapChain2  = win32.GUID{Data1: 0xa8be2ac4, Data2: 0x199f, Data3: 0x4946, Data4: [8]byte{0xb3, 0x31, 0x79, 0x59, 0x9f, 0xb9, 0x8d, 0xe7}} // dxgi1_3.h:352
	iidD3D11Texture2D  = win32.GUID{Data1: 0x6f15aaf2, Data2: 0xd208, Data3: 0x4e89, Data4: [8]byte{0x9a, 0xb4, 0x48, 0x95, 0x35, 0xd3, 0x4f, 0x9c}} // d3d11.h:5682
	iidD3D11DeviceCtx1 = win32.GUID{Data1: 0xbb2c6faa, Data2: 0xb5fb, Data3: 0x4082, Data4: [8]byte{0x8e, 0x6b, 0x38, 0x8b, 0x8c, 0xfa, 0x90, 0xe1}} // d3d11_1.h MIDL_INTERFACE("bb2c6faa-...")
)

// swapChainDesc1 is DXGI_SWAP_CHAIN_DESC1 (dxgi1_2.h).
type swapChainDesc1 struct {
	Width, Height, Format      uint32
	Stereo                     int32
	SampleCount, SampleQuality uint32
	BufferUsage, BufferCount   uint32
	Scaling, SwapEffect        uint32
	AlphaMode, Flags           uint32
}

// adapterDesc is the head of DXGI_ADAPTER_DESC (dxgi.h): Description is
// its first field; the rest is room.
type adapterDesc struct {
	Description [128]uint16
	rest        [64]byte
}

// gfx holds every interface and out-parameter (heap memory, a package
// variable).
var gfx struct {
	dev, ctx, ctx1, sc, sc1, rtv, buf uintptr
	dxgiDev, adapter, factory         uintptr
	waitable                          uintptr
	levels                            [4]uint32
	level                             uint32
	desc                              swapChainDesc1
	adesc                             adapterDesc
	clear, band                       [4]float32
	rect                              [4]int32
	driver                            string
	effect                            string
}

// makeSwapChain makes the device (hardware, else WARP) and the swap chain.
func makeSwapChain() error {
	dll := win32.LoadSystem("d3d11.dll")
	create := win32.Proc(dll, "D3D11CreateDevice")
	if create == 0 {
		return fmt.Errorf("no D3D11CreateDevice")
	}
	gfx.levels = [4]uint32{0xb100, 0xb000, 0xa100, 0xa000} // d3dcommon.h:333-336
	var hr int32
	for _, d := range [...]uintptr{d3dDriverTypeHardware, d3dDriverTypeWarp} {
		hr = win32.HR(call(create, 0, d, 0, d3d11CreateBGRA, uintptr(unsafe.Pointer(&gfx.levels[0])), 4, d3d11SDKVersion,
			uintptr(unsafe.Pointer(&gfx.dev)), uintptr(unsafe.Pointer(&gfx.level)), uintptr(unsafe.Pointer(&gfx.ctx))))
		if hr >= 0 {
			gfx.driver = map[uintptr]string{d3dDriverTypeHardware: "hardware", d3dDriverTypeWarp: "warp"}[d]
			break
		}
	}
	if hr < 0 {
		return fmt.Errorf("D3D11CreateDevice: %#x", uint32(hr))
	}
	if hr := win32.QueryInterface(gfx.dev, &iidDXGIDevice, &gfx.dxgiDev); hr < 0 {
		return fmt.Errorf("QueryInterface(IDXGIDevice): %#x", uint32(hr))
	}
	com(gfx.dxgiDev, vtDXGIDeviceGetAdapter, uintptr(unsafe.Pointer(&gfx.adapter)))
	com(gfx.adapter, vtDXGIAdapterGetDesc, uintptr(unsafe.Pointer(&gfx.adesc)))
	if hr := win32.HR(com(gfx.adapter, vtDXGIObjectGetParent, uintptr(unsafe.Pointer(&iidDXGIFactory2)), uintptr(unsafe.Pointer(&gfx.factory)))); hr < 0 {
		return fmt.Errorf("GetParent(IDXGIFactory2): %#x", uint32(hr))
	}
	gfx.desc = swapChainDesc1{Width: topW, Height: topH, Format: dxgiFormatB8G8R8A8, SampleCount: 1,
		BufferUsage: dxgiUsageRenderTarget, BufferCount: 2, SwapEffect: dxgiEffectFlipDiscard,
		AlphaMode: dxgiAlphaModeIgnore, Flags: dxgiFlagFrameLatency}
	gfx.effect = "flip_discard"
	hr = createSwapChain()
	if hr < 0 {
		gfx.desc.SwapEffect, gfx.effect = dxgiEffectFlipSeq, "flip_sequential"
		hr = createSwapChain()
	}
	if hr < 0 {
		return fmt.Errorf("CreateSwapChainForHwnd: %#x", uint32(hr))
	}
	if win32.QueryInterface(gfx.sc1, &iidDXGISwapChain2, &gfx.sc) >= 0 {
		com(gfx.sc, vtSwapChain2SetMaximumFrameLatency, 1)
		gfx.waitable = com(gfx.sc, vtSwapChain2GetFrameLatencyWaitableObject)
	} else {
		gfx.sc = gfx.sc1
	}
	if hr := win32.HR(com(gfx.sc, vtSwapChainGetBuffer, 0, uintptr(unsafe.Pointer(&iidD3D11Texture2D)), uintptr(unsafe.Pointer(&gfx.buf)))); hr < 0 {
		return fmt.Errorf("GetBuffer: %#x", uint32(hr))
	}
	if hr := win32.HR(com(gfx.dev, vtDeviceCreateRenderTargetView, gfx.buf, 0, uintptr(unsafe.Pointer(&gfx.rtv)))); hr < 0 {
		return fmt.Errorf("CreateRenderTargetView: %#x", uint32(hr))
	}
	if hr := win32.QueryInterface(gfx.ctx, &iidD3D11DeviceCtx1, &gfx.ctx1); hr < 0 {
		return fmt.Errorf("QueryInterface(ID3D11DeviceContext1) for ClearView: %#x", uint32(hr))
	}
	gfx.clear = [4]float32{0, 0, 1, 1}
	gfx.band = [4]float32{1, 0, 0, 1}
	return nil
}

func createSwapChain() int32 {
	return win32.HR(com(gfx.factory, vtDXGIFactory2CreateSwapChainForHwnd,
		gfx.dev, wins.top, uintptr(unsafe.Pointer(&gfx.desc)), 0, 0, uintptr(unsafe.Pointer(&gfx.sc1))))
}

func adapterName() string { return syscall.UTF16ToString(gfx.adesc.Description[:]) }

// draw clears the buffer blue with a red band whose top is at y (bandY < 0:
// no band) and presents with sync interval 1.
func draw(bandY int) int32 {
	if gfx.waitable != 0 {
		call(pWaitForSingleObjectEx, gfx.waitable, 1000, 1)
	}
	com(gfx.ctx, vtContextClearRenderTargetView, gfx.rtv, uintptr(unsafe.Pointer(&gfx.clear[0])))
	if bandY >= 0 {
		gfx.rect = [4]int32{bandX0, int32(bandY), bandX1, int32(bandY + childH)} // D3D11_RECT: left, top, right, bottom
		com(gfx.ctx1, vtContext1ClearView, gfx.rtv, uintptr(unsafe.Pointer(&gfx.band[0])), uintptr(unsafe.Pointer(&gfx.rect)), 1)
	}
	return win32.HR(com(gfx.sc, vtSwapChainPresent, 1, 0))
}

var pWaitForSingleObjectEx = win32.Proc(k32, "WaitForSingleObjectEx")

//go:uintptrescapes
func com(obj, idx uintptr, args ...uintptr) uintptr { return win32.Com(obj, idx, args...) }
