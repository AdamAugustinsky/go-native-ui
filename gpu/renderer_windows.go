//go:build windows

package gpu

import (
	"errors"
	"fmt"
	"runtime"
	"unsafe"

	"gonativeui/internal/win32"
)

// ---- COM vtable slots used by the renderer ----------------------------------
//
// Slot numbers are zero-based and include IUnknown (QueryInterface 0, AddRef
// 1, Release 2) and the inherited interfaces. Every one was extracted from the
// C `typedef struct <Interface>Vtbl` of the mingw-w64 headers
// (mingw-w64-headers/include/{d3d11,d3dcommon,d3dcompiler}.h, master, fetched
// 2026-10-17; line numbers are those of that revision) and its parameter list
// read from the same header.
//
// ID3D11Device (d3d11.h:10592-10831): CreateBuffer 3, CreateTexture2D 5,
// CreateShaderResourceView 7, CreateRenderTargetView 9 (vtDeviceCreate-
// RenderTargetView in d3d11_windows.go), CreateInputLayout 11,
// CreateVertexShader 12, CreatePixelShader 15, CreateBlendState 20,
// CreateRasterizerState 22, CreateSamplerState 23.
const (
	vtDeviceCreateBuffer             = 3
	vtDeviceCreateTexture2D          = 5
	vtDeviceCreateShaderResourceView = 7
	vtDeviceCreateInputLayout        = 11
	vtDeviceCreateVertexShader       = 12
	vtDeviceCreatePixelShader        = 15
	vtDeviceCreateBlendState         = 20
	vtDeviceCreateRasterizerState    = 22
	vtDeviceCreateSamplerState       = 23
	vtDeviceCreateQuery              = 24
)

// ID3D11DeviceContext (d3d11.h:6729-7379): VSSetConstantBuffers 7,
// PSSetShaderResources 8, PSSetShader 9, PSSetSamplers 10, VSSetShader 11,
// Map 14, Unmap 15, IASetInputLayout 17, IASetVertexBuffers 18,
// DrawInstanced 21, IASetPrimitiveTopology 24, End 28, GetData 29 (Begin 27
// sits between; an event query has no Begin), OMSetBlendState 35,
// RSSetState 43, RSSetViewports 44, CopyResource 47, UpdateSubresource 48
// (OMSetRenderTargets 33 and ClearRenderTargetView 50 are in d3d11_windows.go).
const (
	vtContextVSSetConstantBuffers   = 7
	vtContextPSSetShaderResources   = 8
	vtContextPSSetShader            = 9
	vtContextPSSetSamplers          = 10
	vtContextVSSetShader            = 11
	vtContextMap                    = 14
	vtContextUnmap                  = 15
	vtContextIASetInputLayout       = 17
	vtContextIASetVertexBuffers     = 18
	vtContextDrawInstanced          = 21
	vtContextIASetPrimitiveTopology = 24
	vtContextEnd                    = 28
	vtContextGetData                = 29
	vtContextOMSetBlendState        = 35
	vtContextRSSetState             = 43
	vtContextRSSetViewports         = 44
	vtContextCopyResource           = 47
	vtContextUpdateSubresource      = 48
)

// ID3D10Blob = ID3DBlob (d3dcommon.h:81-104): GetBufferPointer 3,
// GetBufferSize 4.
const (
	vtBlobGetBufferPointer = 3
	vtBlobGetBufferSize    = 4
)

// D3DCompile (d3dcompiler.h:88): (data, size, filename, defines, include,
// entrypoint, target, sflags, eflags, ID3DBlob** code, ID3DBlob** errors).

// ---- Enum values (same headers) ----------------------------------------------

const (
	fmtRGBA32Float = 0x02 // DXGI_FORMAT_R32G32B32A32_FLOAT (dxgiformat.h:33)
	fmtRGBA32UInt  = 0x03 // ..._R32G32B32A32_UINT (:34)
	fmtRG32Float   = 0x10 // ..._R32G32_FLOAT (:47)
	fmtRGBA8       = 0x1c // ..._R8G8B8A8_UNORM (:59)
	fmtR32Float    = 0x29 // ..._R32_FLOAT (:72)
	fmtR32UInt     = 0x2a // ..._R32_UINT (:73)
	fmtR8          = 0x3d // ..._R8_UNORM (:92)
	fmtBGRA8       = 0x57 // ..._B8G8R8A8_UNORM (:118)

	usageDefault = 0 // D3D11_USAGE_* (d3d11.h:1700-1703)
	usageDynamic = 2
	usageStaging = 3

	bindVertexBuffer   = 0x1 // D3D11_BIND_* (d3d11.h:1706-1711)
	bindConstantBuffer = 0x4
	bindShaderResource = 0x8
	bindRenderTarget   = 0x20

	cpuAccessWrite = 0x10000 // D3D11_CPU_ACCESS_* (d3d11.h:1718-1719)
	cpuAccessRead  = 0x20000

	mapRead          = 1 // D3D11_MAP_* (d3d11.h:1380-1384)
	mapWriteDiscard  = 4
	mapWriteNoOverwr = 5

	blendOne         = 2 // D3D11_BLEND_ONE, _INV_SRC_ALPHA (d3d11.h:840, 844)
	blendInvSrcAlpha = 6
	blendOpAdd       = 1 // D3D11_BLEND_OP_ADD (d3d11.h:858)
	colorWriteAll    = 0xf

	filterLinear     = 0x15 // D3D11_FILTER_MIN_MAG_MIP_LINEAR (d3d11.h:1322)
	addressClamp     = 3    // D3D11_TEXTURE_ADDRESS_CLAMP (d3d11.h:1674)
	comparisonNever  = 1    // D3D11_COMPARISON_NEVER (d3d11.h:1070)
	fillSolid        = 3    // D3D11_FILL_SOLID (d3d11.h:1262)
	cullNone         = 1    // D3D11_CULL_NONE (d3d11.h:1098)
	inputPerInstance = 1    // D3D11_INPUT_PER_INSTANCE_DATA (d3d11.h:1366)
	topologyTriStrip = 5    // D3D_PRIMITIVE_TOPOLOGY_TRIANGLESTRIP (d3dcommon.h:603)

	queryEvent      = 0 // D3D11_QUERY_EVENT (d3d11.h, enum D3D11_QUERY)
	asyncDoNotFlush = 1 // D3D11_ASYNC_GETDATA_DONOTFLUSH
	hrFalse         = 1 // S_FALSE: GetData's "not yet"

	compileStrict = 0x800  // D3DCOMPILE_ENABLE_STRICTNESS (d3dcompiler.h:54)
	compileOpt3   = 0x8000 // D3DCOMPILE_OPTIMIZATION_LEVEL3 (d3dcompiler.h:60)
)

// ---- Struct layouts ------------------------------------------------------------
//
// Field order and types from d3d11.h (D3D11_BUFFER_DESC :1823, _TEXTURE2D_DESC
// :2165, _SUBRESOURCE_DATA :1553, _MAPPED_SUBRESOURCE :1396, _BOX :1021,
// _INPUT_ELEMENT_DESC :1370, _BLEND_DESC :1797, _RENDER_TARGET_BLEND_DESC
// :1787, _SAMPLER_DESC :2006, _RASTERIZER_DESC :1464, _VIEWPORT :1721). The
// natural Go alignment matches the Windows C layout on amd64 and arm64
// (pointers are 8 bytes, everything else 4 or 1 with tail padding); the sizes
// are asserted below.

type bufferDesc struct {
	ByteWidth, Usage, BindFlags, CPUAccessFlags, MiscFlags, StructureByteStride uint32
}

type texture2DDesc struct {
	Width, Height, MipLevels, ArraySize, Format uint32
	SampleCount, SampleQuality                  uint32
	Usage, BindFlags, CPUAccessFlags, MiscFlags uint32
}

type mappedSubresource struct {
	Data       uintptr
	RowPitch   uint32
	DepthPitch uint32
}

type d3dBox struct{ Left, Top, Front, Right, Bottom, Back uint32 }

type inputElementDesc struct {
	SemanticName      uintptr // LPCSTR
	SemanticIndex     uint32
	Format            uint32
	InputSlot         uint32
	AlignedByteOffset uint32
	InputSlotClass    uint32
	StepRate          uint32
}

type rtBlendDesc struct {
	BlendEnable                                 int32
	SrcBlend, DestBlend, BlendOp                uint32
	SrcBlendAlpha, DestBlendAlpha, BlendOpAlpha uint32
	WriteMask                                   uint8
}

type blendDesc struct {
	AlphaToCoverage, IndependentBlend int32
	RT                                [8]rtBlendDesc
}

type samplerDesc struct {
	Filter, AddressU, AddressV, AddressW uint32
	MipLODBias                           float32
	MaxAnisotropy, ComparisonFunc        uint32
	BorderColor                          [4]float32
	MinLOD, MaxLOD                       float32
}

type rasterizerDesc struct {
	FillMode, CullMode                                        uint32
	FrontCounterClockwise                                     int32
	DepthBias                                                 int32
	DepthBiasClamp, SlopeScaledDepthBias                      float32
	DepthClipEnable, ScissorEnable, MultisampleEnable, AALine int32
}

type viewport struct{ X, Y, W, H, MinDepth, MaxDepth float32 }

type queryDesc struct{ Query, MiscFlags uint32 } // D3D11_QUERY_DESC

// Sizes of the C structs (verified by hand from the field lists above).
var (
	_ = [1]struct{}{}[unsafe.Sizeof(bufferDesc{})-24]
	_ = [1]struct{}{}[unsafe.Sizeof(texture2DDesc{})-44]
	_ = [1]struct{}{}[unsafe.Sizeof(mappedSubresource{})-16]
	_ = [1]struct{}{}[unsafe.Sizeof(d3dBox{})-24]
	_ = [1]struct{}{}[unsafe.Sizeof(inputElementDesc{})-32]
	_ = [1]struct{}{}[unsafe.Sizeof(rtBlendDesc{})-32]
	_ = [1]struct{}{}[unsafe.Sizeof(blendDesc{})-264]
	_ = [1]struct{}{}[unsafe.Sizeof(samplerDesc{})-52]
	_ = [1]struct{}{}[unsafe.Sizeof(rasterizerDesc{})-40]
	_ = [1]struct{}{}[unsafe.Sizeof(viewport{})-24]
	_ = [1]struct{}{}[unsafe.Sizeof(queryDesc{})-8]
)

// instanceElems describes the Quad record to the input assembler (per-
// instance data, step rate 1). Offsets are those of Quad, asserted below.
var instanceElems = [...]struct {
	name   string
	format uint32
	offset uintptr
}{
	{"QRECT", fmtRGBA32Float, unsafe.Offsetof(Quad{}.Rect)},
	{"QCLIP", fmtRGBA32Float, unsafe.Offsetof(Quad{}.Clip)},
	{"QUV", fmtRGBA32Float, unsafe.Offsetof(Quad{}.UV)},
	{"QCOLOR", fmtRGBA32UInt, unsafe.Offsetof(Quad{}.Color)},
	{"QRADII", fmtRGBA32Float, unsafe.Offsetof(Quad{}.Radii)},
	{"QBORDER", fmtR32Float, unsafe.Offsetof(Quad{}.Border)},
	{"QBORDERCOLOR", fmtR32UInt, unsafe.Offsetof(Quad{}.BorderColor)},
	{"QSOFT", fmtR32Float, unsafe.Offsetof(Quad{}.Soft)},
	{"QFADEX", fmtRG32Float, unsafe.Offsetof(Quad{}.FadeX)},
	{"QMODE", fmtR32UInt, unsafe.Offsetof(Quad{}.Mode)},
	{"QTEX", fmtR32UInt, unsafe.Offsetof(Quad{}.Tex)},
}

var (
	_ = [1]struct{}{}[unsafe.Offsetof(Quad{}.Rect)-0]
	_ = [1]struct{}{}[unsafe.Offsetof(Quad{}.Clip)-16]
	_ = [1]struct{}{}[unsafe.Offsetof(Quad{}.UV)-32]
	_ = [1]struct{}{}[unsafe.Offsetof(Quad{}.Color)-48]
	_ = [1]struct{}{}[unsafe.Offsetof(Quad{}.Radii)-64]
	_ = [1]struct{}{}[unsafe.Offsetof(Quad{}.Border)-80]
	_ = [1]struct{}{}[unsafe.Offsetof(Quad{}.BorderColor)-84]
	_ = [1]struct{}{}[unsafe.Offsetof(Quad{}.Soft)-88]
	_ = [1]struct{}{}[unsafe.Offsetof(Quad{}.FadeX)-92]
	_ = [1]struct{}{}[unsafe.Offsetof(Quad{}.Mode)-100]
	_ = [1]struct{}{}[unsafe.Offsetof(Quad{}.Tex)-104]
)

// Package-level byte strings handed to the OS as C strings.
var (
	semanticNames [len(instanceElems)][]byte
	cEntryVS      = []byte("vs_main\x00")
	cEntryPS      = []byte("ps_main\x00")
	cTargetVS     = []byte("vs_4_0\x00")
	cTargetPS     = []byte("ps_4_0\x00")
	hlslSource    = []byte(quadHLSL)
	compilerDLL   uintptr
	pD3DCompile   uintptr
)

func init() {
	for i, e := range instanceElems {
		semanticNames[i] = append([]byte(e.name), 0)
	}
}

// TextureKind is the pixel format of a renderer texture.
type TextureKind uint8

const (
	TexR8    TextureKind = iota // 1 byte per texel coverage
	TexRGBA8                    // 4 bytes per texel, premultiplied alpha
)

// ErrNoDevice is returned when no Direct3D 11 device can be created (not
// even WARP).
var ErrNoDevice = errors.New("gpu: no Direct3D 11 device")

// initialQuads is the starting capacity of the instance ring, in quads.
const initialQuads = 4096

// ringSlots is the number of frame fences in rotation (see textures.go).
const ringSlots = 3

// frameFence is the event query issued after one frame's draws.
type frameFence struct {
	query  uintptr // ID3D11Query (D3D11_QUERY_EVENT); 0 = none pending
	serial uint64
}

// retiredTex is a freed texture waiting for the frames that may sample it.
type retiredTex struct {
	tex, srv uintptr
	serial   uint64 // the last frame encoded when it was freed
	bytes    int64  // its memory, for TextureBytes
}

// Renderer draws DrawLists with Direct3D 11. It is single-threaded: every
// method must be called from the thread that owns the device context.
//
// Instances live in one dynamic vertex buffer used as a ring: each Encode
// appends its quads with Map(WRITE_NO_OVERWRITE) after the previous frames'
// data, and when the buffer is full it wraps with Map(WRITE_DISCARD), which
// makes the driver rename the buffer if the GPU is still reading it. The
// runtime therefore needs no CPU/GPU fence of ours (the Metal back end waits
// on its command buffers instead).
//
// Every pointer handed to the OS lives in a field of this heap object.
type Renderer struct {
	dev, ctx   uintptr // ID3D11Device, ID3D11DeviceContext
	ownsDevice bool

	vs, ps, layout, blend, rast, sampler, cbuf uintptr
	vb                                         uintptr // instance ring (ID3D11Buffer)
	capQuads, head                             int     // ring capacity and write position, in quads

	ids            texTable
	textures, srvs []uintptr // by index
	kinds          []TextureKind
	texW, texH     []int
	emptyTex       uintptr // 1×1 transparent texture bound for a freed id
	emptySRV       uintptr
	retiring       []retiredTex
	fences         [ringSlots]frameFence
	nextFence      int
	serial         uint64 // frames encoded so far: the serial of the last one
	done           uint64 // every frame up to this serial has finished on the GPU
	qd             queryDesc

	draws, totalDraws int

	// Offscreen target for RenderToRGBA.
	offTex, offRTV, staging uintptr
	offW, offH              int

	// Scratch the OS reads or writes through pointers (heap, never stack).
	out       uintptr // out-parameter of Create* calls
	blob      uintptr
	errBlob   uintptr
	target    uintptr
	srvArg    uintptr
	clear     [4]float32
	blendFac  [4]float32
	vp        viewport
	cbData    [4]float32
	cbW, cbH  int
	stride    uint32
	zero      uint32
	bd        bufferDesc
	td        texture2DDesc
	mapped    mappedSubresource
	box       d3dBox
	elems     [len(instanceElems)]inputElementDesc
	blendD    blendDesc
	sampD     samplerDesc
	rastD     rasterizerDesc
	levels    [4]uint32
	featureLv uint32
	pinned    *byte // keeps an Upload source on the heap for the duration of the call

	destroyed bool
}

// NewRenderer builds the pipeline and the instance ring on an existing
// device and immediate context (Surface.Device and Surface.Queue), which the
// caller keeps owning. Setup allocates.
func NewRenderer(device, queue uintptr) (*Renderer, error) {
	if device == 0 || queue == 0 {
		return nil, ErrNoDevice
	}
	r := &Renderer{dev: device, ctx: queue}
	if err := r.init(); err != nil {
		r.Destroy()
		return nil, err
	}
	return r, nil
}

// NewOffscreenRenderer creates a device (hardware, falling back to WARP) and
// a Renderer on it; Destroy releases both. It returns ErrNoDevice if no
// device can be created.
func NewOffscreenRenderer() (*Renderer, error) {
	r := &Renderer{ownsDevice: true}
	if err := createD3DDevice(&r.levels, &r.dev, &r.featureLv, &r.ctx); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoDevice, err)
	}
	if err := r.init(); err != nil {
		r.Destroy()
		return nil, err
	}
	return r, nil
}

func (r *Renderer) init() error {
	r.ids = newTexTable()
	r.textures = make([]uintptr, 0, initialTextures)
	r.srvs = make([]uintptr, 0, initialTextures)
	r.kinds = make([]TextureKind, 0, initialTextures)
	r.texW = make([]int, 0, initialTextures)
	r.texH = make([]int, 0, initialTextures)
	if compilerDLL == 0 {
		compilerDLL = win32.LoadSystem("d3dcompiler_47.dll")
		pD3DCompile = win32.Proc(compilerDLL, "D3DCompile")
	}
	if pD3DCompile == 0 {
		return errors.New("gpu: cannot load d3dcompiler_47.dll")
	}

	// Shaders and the input layout (which needs the vertex shader's input
	// signature).
	vsBlob, err := r.compile(cEntryVS, cTargetVS)
	if err != nil {
		return err
	}
	defer win32.Release(vsBlob)
	psBlob, err := r.compile(cEntryPS, cTargetPS)
	if err != nil {
		return err
	}
	defer win32.Release(psBlob)

	r.out = 0
	if hr := win32.HR(win32.Com(r.dev, vtDeviceCreateVertexShader, win32.Com(vsBlob, vtBlobGetBufferPointer), win32.Com(vsBlob, vtBlobGetBufferSize), 0, uintptr(unsafe.Pointer(&r.out)))); hr < 0 {
		return hresultError("CreateVertexShader", hr)
	}
	r.vs, r.out = r.out, 0
	if hr := win32.HR(win32.Com(r.dev, vtDeviceCreatePixelShader, win32.Com(psBlob, vtBlobGetBufferPointer), win32.Com(psBlob, vtBlobGetBufferSize), 0, uintptr(unsafe.Pointer(&r.out)))); hr < 0 {
		return hresultError("CreatePixelShader", hr)
	}
	r.ps, r.out = r.out, 0

	for i, e := range instanceElems {
		r.elems[i] = inputElementDesc{
			SemanticName:      uintptr(unsafe.Pointer(&semanticNames[i][0])),
			Format:            e.format,
			AlignedByteOffset: uint32(e.offset),
			InputSlotClass:    inputPerInstance,
			StepRate:          1,
		}
	}
	if hr := win32.HR(win32.Com(r.dev, vtDeviceCreateInputLayout, uintptr(unsafe.Pointer(&r.elems[0])), uintptr(len(r.elems)),
		win32.Com(vsBlob, vtBlobGetBufferPointer), win32.Com(vsBlob, vtBlobGetBufferSize), uintptr(unsafe.Pointer(&r.out)))); hr < 0 {
		return hresultError("CreateInputLayout", hr)
	}
	r.layout, r.out = r.out, 0

	// Premultiplied alpha: src + dst × (1 − src.a), for colour and alpha.
	r.blendD = blendDesc{}
	r.blendD.RT[0] = rtBlendDesc{
		BlendEnable: 1,
		SrcBlend:    blendOne, DestBlend: blendInvSrcAlpha, BlendOp: blendOpAdd,
		SrcBlendAlpha: blendOne, DestBlendAlpha: blendInvSrcAlpha, BlendOpAlpha: blendOpAdd,
		WriteMask: colorWriteAll,
	}
	if hr := win32.HR(win32.Com(r.dev, vtDeviceCreateBlendState, uintptr(unsafe.Pointer(&r.blendD)), uintptr(unsafe.Pointer(&r.out)))); hr < 0 {
		return hresultError("CreateBlendState", hr)
	}
	r.blend, r.out = r.out, 0

	r.rastD = rasterizerDesc{FillMode: fillSolid, CullMode: cullNone, DepthClipEnable: 1}
	if hr := win32.HR(win32.Com(r.dev, vtDeviceCreateRasterizerState, uintptr(unsafe.Pointer(&r.rastD)), uintptr(unsafe.Pointer(&r.out)))); hr < 0 {
		return hresultError("CreateRasterizerState", hr)
	}
	r.rast, r.out = r.out, 0

	r.sampD = samplerDesc{
		Filter:   filterLinear,
		AddressU: addressClamp, AddressV: addressClamp, AddressW: addressClamp,
		MaxAnisotropy: 1, ComparisonFunc: comparisonNever,
		MaxLOD: 3.402823466e38,
	}
	if hr := win32.HR(win32.Com(r.dev, vtDeviceCreateSamplerState, uintptr(unsafe.Pointer(&r.sampD)), uintptr(unsafe.Pointer(&r.out)))); hr < 0 {
		return hresultError("CreateSamplerState", hr)
	}
	r.sampler, r.out = r.out, 0

	r.bd = bufferDesc{ByteWidth: 16, Usage: usageDefault, BindFlags: bindConstantBuffer}
	if hr := win32.HR(win32.Com(r.dev, vtDeviceCreateBuffer, uintptr(unsafe.Pointer(&r.bd)), 0, uintptr(unsafe.Pointer(&r.out)))); hr < 0 {
		return hresultError("CreateBuffer(constants)", hr)
	}
	r.cbuf, r.out = r.out, 0

	r.stride = quadSize
	r.blendFac = [4]float32{}
	if err := r.growRing(initialQuads); err != nil {
		return err
	}

	// Texture 0: 1×1 white RGBA texel that shape quads bind.
	t0 := r.NewTexture(TexRGBA8, 1, 1)
	r.Upload(t0, 0, 0, 1, 1, []byte{255, 255, 255, 255}, 4)
	// The texture a quad naming a freed texture samples: one transparent texel.
	r.emptyTex, r.emptySRV = r.newD3DTexture(TexRGBA8, 1, 1)
	r.box = d3dBox{Right: 1, Bottom: 1, Back: 1}
	win32.Com(r.ctx, vtContextUpdateSubresource, r.emptyTex, 0, uintptr(unsafe.Pointer(&r.box)), uintptr(unsafe.Pointer(&r.zero)), 4, 0)
	return nil
}

// compile compiles quadHLSL's entry point with D3DCompile and returns the
// bytecode blob (the caller releases it).
func (r *Renderer) compile(entry, target []byte) (uintptr, error) {
	r.blob, r.errBlob = 0, 0
	hr := win32.HR(win32.Call(pD3DCompile,
		uintptr(unsafe.Pointer(&hlslSource[0])), uintptr(len(hlslSource)), 0, 0, 0,
		uintptr(unsafe.Pointer(&entry[0])), uintptr(unsafe.Pointer(&target[0])),
		compileStrict|compileOpt3, 0,
		uintptr(unsafe.Pointer(&r.blob)), uintptr(unsafe.Pointer(&r.errBlob))))
	runtime.KeepAlive(r)
	blob, msg := r.blob, ""
	if r.errBlob != 0 {
		p, n := win32.Com(r.errBlob, vtBlobGetBufferPointer), win32.Com(r.errBlob, vtBlobGetBufferSize)
		if p != 0 && n != 0 {
			msg = unsafe.String((*byte)(win32.Ptr(p)), int(n))
		}
		msg = string([]byte(msg)) // copy out of the blob before releasing it
		win32.Release(r.errBlob)
		r.errBlob = 0
	}
	if hr < 0 || blob == 0 {
		win32.Release(blob)
		return 0, fmt.Errorf("gpu: compiling %s (%s): HRESULT %#08x: %s", entry[:len(entry)-1], target[:len(target)-1], uint32(hr), msg)
	}
	r.blob = 0
	return blob, nil
}

// growRing (re)creates the instance ring with room for at least quads quads.
// The old buffer is released; D3D11 keeps it alive while queued commands
// still read it.
func (r *Renderer) growRing(quads int) error {
	n := r.capQuads
	if n == 0 {
		n = initialQuads
	}
	for n < quads {
		n *= 2
	}
	r.bd = bufferDesc{ByteWidth: uint32(n * quadSize), Usage: usageDynamic, BindFlags: bindVertexBuffer, CPUAccessFlags: cpuAccessWrite}
	r.out = 0
	if hr := win32.HR(win32.Com(r.dev, vtDeviceCreateBuffer, uintptr(unsafe.Pointer(&r.bd)), 0, uintptr(unsafe.Pointer(&r.out)))); hr < 0 {
		return hresultError("CreateBuffer(instances)", hr)
	}
	win32.Release(r.vb)
	r.vb, r.out = r.out, 0
	r.capQuads = n
	r.head = n // the next Encode starts a fresh buffer with WRITE_DISCARD
	return nil
}

// NewTexture creates a w×h texture. Contents are undefined until Upload. It
// reuses the index of a freed texture under a new generation (see
// FreeTexture); the table grows only past the most textures ever live at
// once.
func (r *Renderer) NewTexture(kind TextureKind, w, h int) TextureID {
	tex, srv := r.newD3DTexture(kind, w, h)
	i, id := r.ids.add()
	if i == len(r.textures) {
		r.textures = append(r.textures, 0)
		r.srvs = append(r.srvs, 0)
		r.kinds = append(r.kinds, 0)
		r.texW = append(r.texW, 0)
		r.texH = append(r.texH, 0)
	}
	r.textures[i], r.srvs[i], r.kinds[i], r.texW[i], r.texH[i] = tex, srv, kind, w, h
	r.ids.addBytes(i, texBytes(kind, w, h))
	return id
}

// FreeTexture frees texture t. Its id stops resolving at once: Upload and
// FreeTexture ignore it, and quads that still name it draw nothing. The
// texture and its view are released when the event query of the last frame
// encoded so far has signalled (see textures.go); FreeTexture itself never
// waits. Freeing texture 0, an unknown id or an id already freed does
// nothing.
// An index is reused under 4096 generations and then retired for good, so a
// renderer can create about 4.29 billion textures (2^20 indices × 4096) in its
// life; C7's external textures should revisit the id's width if they churn.
func (r *Renderer) FreeTexture(t TextureID) {
	i, ok := r.ids.lookup(t)
	if !ok || i == 0 {
		return
	}
	n := r.ids.remove(i)
	r.retiring = append(r.retiring, retiredTex{r.textures[i], r.srvs[i], r.serial, n})
	r.textures[i], r.srvs[i] = 0, 0
	r.collect()
}

// TextureCounts reports the live textures (texture 0 included) and the freed
// ones whose release still waits for the GPU.
func (r *Renderer) TextureCounts() (live, retiring int) { return r.ids.n, len(r.retiring) }

// TextureBytes reports the memory of the renderer's textures: the live ones
// (atlas pages and standalone textures) and the freed ones that wait for the
// GPU, as width × height × bytes per texel (no mip levels, no driver padding).
// Texture 0 (4 bytes) is counted; the renderer's own targets are not.
func (r *Renderer) TextureBytes() int64 {
	n := r.ids.used
	for _, x := range r.retiring {
		n += x.bytes
	}
	return n
}

// fenceDone reads an event query without waiting (and without flushing:
// Present flushes). A failed read (a removed device) counts as done.
func (r *Renderer) fenceDone(q uintptr) bool {
	return win32.HR(win32.Com(r.ctx, vtContextGetData, q, 0, 0, asyncDoNotFlush)) != hrFalse
}

// collect advances done from the frame fences that have signalled, without
// waiting, and releases the freed textures no unfinished frame may sample.
// The immediate context runs its commands in order, so a signalled fence
// completes every earlier frame too.
func (r *Renderer) collect() {
	for i := range r.fences {
		f := &r.fences[i]
		if f.query != 0 && r.fenceDone(f.query) {
			r.done = max(r.done, f.serial)
			win32.Release(f.query)
			f.query = 0
		}
	}
	n := 0
	for _, x := range r.retiring {
		if x.serial <= r.done {
			win32.Release(x.srv)
			win32.Release(x.tex)
			continue
		}
		r.retiring[n] = x
		n++
	}
	clear(r.retiring[n:])
	r.retiring = r.retiring[:n]
}

// fence issues the event query of the frame just encoded. A slot whose
// previous query has not signalled yet gives it up: the new one, later in
// the same command stream, signals after it.
func (r *Renderer) fence() {
	f := &r.fences[r.nextFence]
	if r.nextFence++; r.nextFence == ringSlots {
		r.nextFence = 0
	}
	if f.query == 0 {
		r.qd = queryDesc{Query: queryEvent}
		r.out = 0
		if hr := win32.HR(win32.Com(r.dev, vtDeviceCreateQuery, uintptr(unsafe.Pointer(&r.qd)), uintptr(unsafe.Pointer(&r.out)))); hr < 0 {
			return // no fence: what this frame frees waits for a later one
		}
		f.query, r.out = r.out, 0
	}
	f.serial = r.serial
	win32.Com(r.ctx, vtContextEnd, f.query)
}

func (r *Renderer) newD3DTexture(kind TextureKind, w, h int) (tex, srv uintptr) {
	format := uint32(fmtRGBA8)
	if kind == TexR8 {
		format = fmtR8
	}
	r.td = texture2DDesc{
		Width: uint32(w), Height: uint32(h), MipLevels: 1, ArraySize: 1, Format: format,
		SampleCount: 1, Usage: usageDefault, BindFlags: bindShaderResource,
	}
	r.out = 0
	if hr := win32.HR(win32.Com(r.dev, vtDeviceCreateTexture2D, uintptr(unsafe.Pointer(&r.td)), 0, uintptr(unsafe.Pointer(&r.out)))); hr < 0 {
		panic(hresultError("CreateTexture2D", hr))
	}
	tex = r.out
	r.out = 0
	if hr := win32.HR(win32.Com(r.dev, vtDeviceCreateShaderResourceView, tex, 0, uintptr(unsafe.Pointer(&r.out)))); hr < 0 {
		panic(hresultError("CreateShaderResourceView", hr))
	}
	srv, r.out = r.out, 0
	return tex, srv
}

// Upload copies a w×h block of pixels into texture t at (x, y). pixels[0] is
// the block's first texel and stride is the byte distance between rows. An
// id that was freed is ignored. UpdateSubresource is ordered with the
// context's draws, so frames already submitted read the old contents.
func (r *Renderer) Upload(t TextureID, x, y, w, h int, pixels []byte, stride int) {
	if w <= 0 || h <= 0 {
		return
	}
	i, ok := r.ids.lookup(t)
	if !ok {
		return
	}
	bpp := 1
	if r.kinds[i] == TexRGBA8 {
		bpp = 4
	}
	if stride < w*bpp || len(pixels) < (h-1)*stride+w*bpp {
		panic("gpu: Upload: pixels too short for region")
	}
	if x < 0 || y < 0 || x+w > r.texW[i] || y+h > r.texH[i] {
		panic("gpu: Upload: region outside the texture")
	}
	r.box = d3dBox{Left: uint32(x), Top: uint32(y), Right: uint32(x + w), Bottom: uint32(y + h), Back: 1}
	r.pinned = &pixels[0] // force the backing array onto the heap while D3D reads it
	win32.Com(r.ctx, vtContextUpdateSubresource, r.textures[i], 0, uintptr(unsafe.Pointer(&r.box)),
		uintptr(unsafe.Pointer(&pixels[0])), uintptr(stride), 0)
	r.pinned = nil
	runtime.KeepAlive(pixels)
}

// Stage is Upload: UpdateSubresource is already ordered with the frames in flight,
// which keep reading the old contents, and does not wait for them. (On Metal
// Stage defers the copy to the next Encode to get the same ordering.)
func (r *Renderer) Stage(t TextureID, x, y, w, h int, pixels []byte, stride int) {
	r.Upload(t, x, y, w, h, pixels, stride)
}

// Encode records one render pass on cmdBuf (the ID3D11DeviceContext): it
// binds target (an ID3D11RenderTargetView of a BGRA8 UNORM texture), clears
// it to clear and draws list. The caller presents afterwards. cmdBuf must be
// the context of the renderer's device (Surface.Queue), and Encode sets all
// the pipeline state it needs, so it can be called between other users of
// the context.
//
// clear is written to the target as is, so it should be premultiplied. The
// instance data is appended to the ring (see Renderer). Encode allocates
// nothing on the Go heap once the renderer and list are warm; it grows the
// ring only if the list is larger than any before.
func (r *Renderer) Encode(cmdBuf, target uintptr, width, height int, clear [4]float32, list *DrawList) {
	r.draws = 0
	if width <= 0 || height <= 0 {
		return
	}
	ctx := cmdBuf
	r.serial++
	if len(r.retiring) > 0 {
		r.collect()
	}
	var quads []Quad
	if list != nil {
		quads = list.Quads
	}
	if len(quads) > r.capQuads {
		if err := r.growRing(len(quads)); err != nil {
			panic(err)
		}
	}
	base := 0
	if n := len(quads); n > 0 {
		mapType := uintptr(mapWriteNoOverwr)
		if r.head+n > r.capQuads {
			mapType, r.head = mapWriteDiscard, 0
		}
		if hr := win32.HR(win32.Com(ctx, vtContextMap, r.vb, 0, mapType, 0, uintptr(unsafe.Pointer(&r.mapped)))); hr < 0 {
			panic(hresultError("Map(instances)", hr))
		}
		dst := unsafe.Slice((*Quad)(unsafe.Add(win32.Ptr(r.mapped.Data), r.head*quadSize)), n)
		copy(dst, quads)
		backdropsAsShapes(dst, list)
		win32.Com(ctx, vtContextUnmap, r.vb, 0)
		base = r.head
		r.head += n
	}

	// Viewport constants change only with the target size.
	if r.cbW != width || r.cbH != height {
		r.cbW, r.cbH = width, height
		r.cbData[0], r.cbData[1] = float32(width), float32(height)
		win32.Com(ctx, vtContextUpdateSubresource, r.cbuf, 0, 0, uintptr(unsafe.Pointer(&r.cbData[0])), 0, 0)
	}

	r.target = target
	r.clear = clear
	r.vp = viewport{W: float32(width), H: float32(height), MaxDepth: 1}
	win32.Com(ctx, vtContextOMSetRenderTargets, 1, uintptr(unsafe.Pointer(&r.target)), 0)
	win32.Com(ctx, vtContextClearRenderTargetView, target, uintptr(unsafe.Pointer(&r.clear[0])))
	win32.Com(ctx, vtContextRSSetViewports, 1, uintptr(unsafe.Pointer(&r.vp)))
	win32.Com(ctx, vtContextRSSetState, r.rast)
	win32.Com(ctx, vtContextOMSetBlendState, r.blend, uintptr(unsafe.Pointer(&r.blendFac[0])), 0xFFFFFFFF)
	win32.Com(ctx, vtContextIASetInputLayout, r.layout)
	win32.Com(ctx, vtContextIASetPrimitiveTopology, topologyTriStrip)
	win32.Com(ctx, vtContextIASetVertexBuffers, 0, 1, uintptr(unsafe.Pointer(&r.vb)), uintptr(unsafe.Pointer(&r.stride)), uintptr(unsafe.Pointer(&r.zero)))
	win32.Com(ctx, vtContextVSSetShader, r.vs, 0, 0)
	win32.Com(ctx, vtContextVSSetConstantBuffers, 0, 1, uintptr(unsafe.Pointer(&r.cbuf)))
	win32.Com(ctx, vtContextPSSetShader, r.ps, 0, 0)
	win32.Com(ctx, vtContextPSSetSamplers, 0, 1, uintptr(unsafe.Pointer(&r.sampler)))

	// Shape quads don't read the bound texture, so they join whatever run is
	// open; only a texture quad with a different texture ends it.
	bound := TextureID(0)
	r.bindTexture(ctx, bound)
	runStart := 0
	for i := range quads {
		q := &quads[i]
		if q.Mode == ModeShape || q.Mode == ModeBackdrop || q.Tex == bound {
			continue
		}
		if i > runStart {
			r.draw(ctx, base+runStart, i-runStart)
		}
		runStart = i
		bound = q.Tex
		r.bindTexture(ctx, bound)
	}
	if len(quads) > runStart {
		r.draw(ctx, base+runStart, len(quads)-runStart)
	}
	r.fence()
	runtime.KeepAlive(r)
}

// Backdrops reports whether the renderer blurs ModeBackdrop quads: Direct3D
// 11 does not yet, and draws their fallback colour.
func (r *Renderer) Backdrops() bool { return false }

// bindTexture binds texture t; a freed or unknown id binds the empty texture.
func (r *Renderer) bindTexture(ctx uintptr, t TextureID) {
	r.srvArg = r.emptySRV
	if i, ok := r.ids.lookup(t); ok {
		r.srvArg = r.srvs[i]
	}
	win32.Com(ctx, vtContextPSSetShaderResources, 0, 1, uintptr(unsafe.Pointer(&r.srvArg)))
}

// draw issues one instanced triangle-strip draw of count quads starting at
// instance first (StartInstanceLocation, which offsets the per-instance
// vertex fetch; SV_InstanceID is not used).
func (r *Renderer) draw(ctx uintptr, first, count int) {
	win32.Com(ctx, vtContextDrawInstanced, 4, uintptr(count), 0, uintptr(first))
	r.draws++
	r.totalDraws++
}

// DrawCalls is the number of draw calls the last Encode issued.
func (r *Renderer) DrawCalls() int { return r.draws }

// RenderToRGBA renders list into an offscreen BGRA8 target of the given
// size and writes the result to out as RGBA8 (premultiplied, row-major, top
// row first; len(out) must be at least width*height*4). It copies the target
// to a staging texture and maps it, which waits for the GPU: for tests and
// PNG output.
func (r *Renderer) RenderToRGBA(width, height int, clear [4]float32, list *DrawList, out []byte) {
	if width <= 0 || height <= 0 {
		return
	}
	n := width * height * 4
	if len(out) < n {
		panic("gpu: RenderToRGBA: out too short")
	}
	if r.offTex == 0 || r.offW != width || r.offH != height {
		r.newTarget(width, height)
	}
	r.Encode(r.ctx, r.offRTV, width, height, clear, list)
	win32.Com(r.ctx, vtContextCopyResource, r.staging, r.offTex)
	if hr := win32.HR(win32.Com(r.ctx, vtContextMap, r.staging, 0, mapRead, 0, uintptr(unsafe.Pointer(&r.mapped)))); hr < 0 {
		panic(hresultError("Map(staging)", hr))
	}
	src := unsafe.Slice((*byte)(win32.Ptr(r.mapped.Data)), int(r.mapped.RowPitch)*(height-1)+width*4)
	for y := 0; y < height; y++ {
		row := src[y*int(r.mapped.RowPitch) : y*int(r.mapped.RowPitch)+width*4]
		dst := out[y*width*4 : (y+1)*width*4]
		for i := 0; i < len(row); i += 4 { // BGRA → RGBA
			dst[i], dst[i+1], dst[i+2], dst[i+3] = row[i+2], row[i+1], row[i], row[i+3]
		}
	}
	win32.Com(r.ctx, vtContextUnmap, r.staging, 0)
	runtime.KeepAlive(r)
}

// newTarget (re)creates the offscreen BGRA8 render target, its view and the
// CPU-readable staging copy.
func (r *Renderer) newTarget(w, h int) {
	for _, p := range [...]*uintptr{&r.offRTV, &r.offTex, &r.staging} {
		win32.Release(*p)
		*p = 0
	}
	r.td = texture2DDesc{
		Width: uint32(w), Height: uint32(h), MipLevels: 1, ArraySize: 1, Format: fmtBGRA8,
		SampleCount: 1, Usage: usageDefault, BindFlags: bindRenderTarget | bindShaderResource,
	}
	r.out = 0
	if hr := win32.HR(win32.Com(r.dev, vtDeviceCreateTexture2D, uintptr(unsafe.Pointer(&r.td)), 0, uintptr(unsafe.Pointer(&r.out)))); hr < 0 {
		panic(hresultError("CreateTexture2D(offscreen)", hr))
	}
	r.offTex, r.out = r.out, 0
	if hr := win32.HR(win32.Com(r.dev, vtDeviceCreateRenderTargetView, r.offTex, 0, uintptr(unsafe.Pointer(&r.out)))); hr < 0 {
		panic(hresultError("CreateRenderTargetView(offscreen)", hr))
	}
	r.offRTV, r.out = r.out, 0
	r.td = texture2DDesc{
		Width: uint32(w), Height: uint32(h), MipLevels: 1, ArraySize: 1, Format: fmtBGRA8,
		SampleCount: 1, Usage: usageStaging, CPUAccessFlags: cpuAccessRead,
	}
	if hr := win32.HR(win32.Com(r.dev, vtDeviceCreateTexture2D, uintptr(unsafe.Pointer(&r.td)), 0, uintptr(unsafe.Pointer(&r.out)))); hr < 0 {
		panic(hresultError("CreateTexture2D(staging)", hr))
	}
	r.staging, r.out = r.out, 0
	r.offW, r.offH = w, h
}

// Destroy releases everything the renderer created. A device and context
// passed to NewRenderer are not released.
func (r *Renderer) Destroy() {
	if r.destroyed {
		return
	}
	r.destroyed = true
	if r.ctx != 0 {
		win32.Com(r.ctx, vtContextClearState)
		win32.Com(r.ctx, vtContextFlush)
	}
	for i := range r.textures {
		win32.Release(r.srvs[i])
		win32.Release(r.textures[i])
	}
	for _, x := range r.retiring {
		win32.Release(x.srv)
		win32.Release(x.tex)
	}
	for i := range r.fences {
		win32.Release(r.fences[i].query)
		r.fences[i].query = 0
	}
	r.textures, r.srvs, r.kinds, r.texW, r.texH, r.retiring = nil, nil, nil, nil, nil, nil
	r.ids = texTable{} // ids freed after Destroy resolve to nothing
	for _, p := range [...]*uintptr{&r.offRTV, &r.offTex, &r.staging, &r.vb, &r.cbuf, &r.sampler, &r.rast, &r.blend, &r.layout, &r.ps, &r.vs, &r.emptySRV, &r.emptyTex} {
		win32.Release(*p)
		*p = 0
	}
	if r.ownsDevice {
		win32.Release(r.ctx)
		win32.Release(r.dev)
		r.ctx, r.dev = 0, 0
	}
}
