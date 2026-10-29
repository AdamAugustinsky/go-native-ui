//go:build windows

package platform

import (
	"errors"
	"fmt"
	"math"
	"unsafe"

	"gonativeui/internal/com"
	"gonativeui/internal/win32"
)

// The editor's drawing (W4, D67's windowless refinement): each editor's host
// view is a child window with its own flip-model swap chain, on one D3D11
// device the editors share (made with the first editor, not before: D21),
// with a Direct2D render target on its back buffer. RichEdit draws into it
// (ITextServices2::TxDrawD2D, which draws colour emoji; the windowed control
// cannot, P5), then the editor draws its caret and placeholder. A swap chain
// child shows whether or not the top-level window has a GDI surface
// (Options.ChildWindows), so the composer needs nothing of the app.
//
// Vtable slots: the DXGI and D3D11 ones as package gpu has them (checked
// against the mingw-w64 headers there); d2d1.h's and dwrite.h's in order
// below.

// ID2D1Factory (d2d1.h): IUnknown 0-2, ReloadSystemMetrics 3, GetDesktopDpi 4,
// CreateRectangleGeometry 5, CreateRoundedRectangleGeometry 6,
// CreateEllipseGeometry 7, CreateGeometryGroup 8, CreateTransformedGeometry 9,
// CreatePathGeometry 10, CreateStrokeStyle 11, CreateDrawingStateBlock 12,
// CreateWicBitmapRenderTarget 13, CreateHwndRenderTarget 14,
// CreateDxgiSurfaceRenderTarget 15, CreateDCRenderTarget 16.
//
// ID2D1RenderTarget (d2d1.h): ID2D1Resource GetFactory 3, CreateBitmap 4,
// CreateBitmapFromWicBitmap 5, CreateSharedBitmap 6, CreateBitmapBrush 7,
// CreateSolidColorBrush 8, … FillRectangle 17, … DrawText 27, …
// SetTextAntialiasMode 34, … Clear 47, BeginDraw 48, EndDraw 49.
//
// ID2D1SolidColorBrush: ID2D1Brush SetOpacity 4 … GetTransform 7, SetColor 8.
//
// IDWriteFactory (dwrite.h): GetSystemFontCollection 3 …
// CreateTextFormat 15. IDWriteTextFormat: SetTextAlignment 3,
// SetParagraphAlignment 4, SetWordWrapping 5.
const (
	vtD2DFactoryCreateDxgiSurfaceRT = 15
	vtRTCreateSolidColorBrush       = 8
	vtRTFillRectangle               = 17
	vtRTDrawText                    = 27
	vtRTSetTextAntialiasMode        = 34
	vtRTClear                       = 47
	vtRTBeginDraw                   = 48
	vtRTEndDraw                     = 49
	vtBrushSetColor                 = 8
	vtDWFactoryCreateTextFormat     = 15
	vtTextFormatSetWordWrapping     = 5

	vtDXGIObjectGetParent              = 6
	vtDXGIDeviceGetAdapter             = 7
	vtDXGIFactory2CreateSwapChainForHw = 15
	vtSwapChainPresent                 = 8
	vtSwapChainGetBuffer               = 9
	vtSwapChainResizeBuffers           = 13
)

var (
	// d2d1.h: 06152247-6f50-465a-9245-118bfd3b6007
	iidD2D1Factory = win32.GUID{Data1: 0x06152247, Data2: 0x6f50, Data3: 0x465a, Data4: [8]byte{0x92, 0x45, 0x11, 0x8b, 0xfd, 0x3b, 0x60, 0x07}}
	// dxgi.h: cafcb56c-6ac3-4889-bf47-9e23bbd260ec
	iidDXGISurface = win32.GUID{Data1: 0xcafcb56c, Data2: 0x6ac3, Data3: 0x4889, Data4: [8]byte{0xbf, 0x47, 0x9e, 0x23, 0xbb, 0xd2, 0x60, 0xec}}
	// dxgi.h: 54ec77fa-1377-44e6-8c32-88fd5f44c84c
	iidDXGIDevice = win32.GUID{Data1: 0x54ec77fa, Data2: 0x1377, Data3: 0x44e6, Data4: [8]byte{0x8c, 0x32, 0x88, 0xfd, 0x5f, 0x44, 0xc8, 0x4c}}
	// dxgi1_2.h: 50c83a1c-e072-4c48-87b0-3630fa36a6d0
	iidDXGIFactory2 = win32.GUID{Data1: 0x50c83a1c, Data2: 0xe072, Data3: 0x4c48, Data4: [8]byte{0x87, 0xb0, 0x36, 0x30, 0xfa, 0x36, 0xa6, 0xd0}}
	// dwrite.h: b859ee5a-d838-4b5b-a2e8-1adc7d93db48
	iidDWriteFactory = win32.GUID{Data1: 0xb859ee5a, Data2: 0xd838, Data3: 0x4b5b, Data4: [8]byte{0xa2, 0xe8, 0x1a, 0xdc, 0x7d, 0x93, 0xdb, 0x48}}
)

// edGfx is the D3D11 device, the DXGI factory, the Direct2D and DirectWrite
// factories the editors share (made once, with the first editor; kept for
// the process's life).
var edGfx struct {
	made       bool
	err        error
	dev, ctx   uintptr
	dxgiDev    uintptr
	adapter    uintptr
	factory    uintptr // IDXGIFactory2
	d2d        uintptr // ID2D1Factory
	dw         uintptr // IDWriteFactory
	levels     [4]uint32
	featureLvl uint32
	frame      com.Frame // CreateTextFormat's float argument
}

// swapDesc1 is DXGI_SWAP_CHAIN_DESC1.
type swapDesc1 struct {
	Width, Height              uint32
	Format                     uint32
	Stereo                     int32
	SampleCount, SampleQuality uint32
	BufferUsage, BufferCount   uint32
	Scaling, SwapEffect        uint32
	AlphaMode, Flags           uint32
}

// rtProps is D2D1_RENDER_TARGET_PROPERTIES.
type rtProps struct {
	typ             uint32
	format, alpha   uint32
	dpiX, dpiY      float32
	usage, minLevel uint32
}

// colorF is D2D1_COLOR_F; rectF D2D1_RECT_F.
type colorF struct{ r, g, b, a float32 }
type rectF struct{ l, t, r, b float32 }

const (
	d3dDriverHardware = 1
	d3dDriverWarp     = 5
	d3d11BGRA         = 0x20
	d3d11SDK          = 7
	fmtBGRA           = 87
	dxgiRenderTarget  = 0x20
	flipDiscard       = 4
	flipSequential    = 3
	alphaIgnore       = 3
)

// editorGraphics makes the shared device and factories once.
func editorGraphics() error {
	g := &edGfx
	if g.made {
		return g.err
	}
	g.made = true
	g.err = makeEditorGraphics()
	return g.err
}

func makeEditorGraphics() error {
	g := &edGfx
	d3d := win32.LoadSystem("d3d11.dll")
	create := win32.Proc(d3d, "D3D11CreateDevice")
	if create == 0 {
		return errors.New("platform: editor: cannot load d3d11.dll")
	}
	g.levels = [4]uint32{0xb100, 0xb000, 0xa100, 0xa000}
	var hr int32 = -1
	for _, drv := range [...]uintptr{d3dDriverHardware, d3dDriverWarp} {
		hr = win32.HR(win32.Call(create, 0, drv, 0, d3d11BGRA, uintptr(unsafe.Pointer(&g.levels[0])), 4, d3d11SDK,
			uintptr(unsafe.Pointer(&g.dev)), uintptr(unsafe.Pointer(&g.featureLvl)), uintptr(unsafe.Pointer(&g.ctx))))
		if hr < 0 { // without 11_1 (Windows 7's runtime)
			hr = win32.HR(win32.Call(create, 0, drv, 0, d3d11BGRA, uintptr(unsafe.Pointer(&g.levels[1])), 3, d3d11SDK,
				uintptr(unsafe.Pointer(&g.dev)), uintptr(unsafe.Pointer(&g.featureLvl)), uintptr(unsafe.Pointer(&g.ctx))))
		}
		if hr >= 0 {
			break
		}
	}
	if hr < 0 {
		return fmt.Errorf("platform: editor: D3D11CreateDevice: %#x", uint32(hr))
	}
	if hr := win32.QueryInterface(g.dev, &iidDXGIDevice, &g.dxgiDev); hr < 0 {
		return fmt.Errorf("platform: editor: IDXGIDevice: %#x", uint32(hr))
	}
	if hr := win32.HR(win32.Com(g.dxgiDev, vtDXGIDeviceGetAdapter, uintptr(unsafe.Pointer(&g.adapter)))); hr < 0 {
		return fmt.Errorf("platform: editor: GetAdapter: %#x", uint32(hr))
	}
	if hr := win32.HR(win32.Com(g.adapter, vtDXGIObjectGetParent, uintptr(unsafe.Pointer(&iidDXGIFactory2)), uintptr(unsafe.Pointer(&g.factory)))); hr < 0 {
		return fmt.Errorf("platform: editor: IDXGIFactory2: %#x", uint32(hr))
	}
	d2d := win32.Proc(win32.LoadSystem("d2d1.dll"), "D2D1CreateFactory")
	if d2d == 0 {
		return errors.New("platform: editor: cannot load d2d1.dll")
	}
	if hr := win32.HR(win32.Call(d2d, 0 /* single-threaded */, uintptr(unsafe.Pointer(&iidD2D1Factory)), 0, uintptr(unsafe.Pointer(&g.d2d)))); hr < 0 {
		return fmt.Errorf("platform: editor: D2D1CreateFactory: %#x", uint32(hr))
	}
	dw := win32.Proc(win32.LoadSystem("dwrite.dll"), "DWriteCreateFactory")
	if dw == 0 {
		return errors.New("platform: editor: cannot load dwrite.dll")
	}
	if hr := win32.HR(win32.Call(dw, 0 /* shared */, uintptr(unsafe.Pointer(&iidDWriteFactory)), uintptr(unsafe.Pointer(&g.dw)))); hr < 0 {
		return fmt.Errorf("platform: editor: DWriteCreateFactory: %#x", uint32(hr))
	}
	return nil
}

// edSurface is one editor's swap chain and render target.
type edSurface struct {
	desc   swapDesc1
	sc     uintptr // IDXGISwapChain1
	surf   uintptr // IDXGISurface of buffer 0 (scratch)
	rt     uintptr // ID2D1RenderTarget on the back buffer
	brush  uintptr // ID2D1SolidColorBrush (caret, placeholder)
	format uintptr // IDWriteTextFormat of the placeholder (0: none yet)
	props  rtProps
	w, h   int32
	col    colorF
	rect   rectF
	bounds rect
	fmtPt  float32 // the placeholder format's size
	face   [16]uint16
	locale [6]uint16
	frames int64 // presents (tests, the idle measurement)
}

// open makes the swap chain on hwnd.
func (s *edSurface) open(hwnd uintptr, w, h int32) error {
	s.w, s.h = max(w, 1), max(h, 1)
	s.desc = swapDesc1{Width: uint32(s.w), Height: uint32(s.h), Format: fmtBGRA, SampleCount: 1,
		BufferUsage: dxgiRenderTarget, BufferCount: 2, SwapEffect: flipDiscard, AlphaMode: alphaIgnore}
	hr := win32.HR(win32.Com(edGfx.factory, vtDXGIFactory2CreateSwapChainForHw, edGfx.dev, hwnd, uintptr(unsafe.Pointer(&s.desc)), 0, 0, uintptr(unsafe.Pointer(&s.sc))))
	if hr < 0 {
		s.desc.SwapEffect = flipSequential
		hr = win32.HR(win32.Com(edGfx.factory, vtDXGIFactory2CreateSwapChainForHw, edGfx.dev, hwnd, uintptr(unsafe.Pointer(&s.desc)), 0, 0, uintptr(unsafe.Pointer(&s.sc))))
	}
	if hr < 0 {
		return fmt.Errorf("platform: editor: CreateSwapChainForHwnd: %#x", uint32(hr))
	}
	return s.target()
}

// target makes the render target on buffer 0 and the brush.
func (s *edSurface) target() error {
	if hr := win32.HR(win32.Com(s.sc, vtSwapChainGetBuffer, 0, uintptr(unsafe.Pointer(&iidDXGISurface)), uintptr(unsafe.Pointer(&s.surf)))); hr < 0 {
		return fmt.Errorf("platform: editor: GetBuffer: %#x", uint32(hr))
	}
	// 96 DPI: a DIP is a pixel, as RichEdit lays out in pixels.
	s.props = rtProps{format: fmtBGRA, alpha: alphaIgnore, dpiX: 96, dpiY: 96}
	hr := win32.HR(win32.Com(edGfx.d2d, vtD2DFactoryCreateDxgiSurfaceRT, s.surf, uintptr(unsafe.Pointer(&s.props)), uintptr(unsafe.Pointer(&s.rt))))
	win32.Release(s.surf)
	s.surf = 0
	if hr < 0 {
		return fmt.Errorf("platform: editor: CreateDxgiSurfaceRenderTarget: %#x", uint32(hr))
	}
	win32.Com(s.rt, vtRTSetTextAntialiasMode, 2 /* D2D1_TEXT_ANTIALIAS_MODE_GRAYSCALE: ClearType needs an opaque known background */)
	s.col = colorF{0, 0, 0, 1}
	if hr := win32.HR(win32.Com(s.rt, vtRTCreateSolidColorBrush, uintptr(unsafe.Pointer(&s.col)), 0, uintptr(unsafe.Pointer(&s.brush)))); hr < 0 {
		return fmt.Errorf("platform: editor: CreateSolidColorBrush: %#x", uint32(hr))
	}
	return nil
}

// resize follows the window's size.
func (s *edSurface) resize(w, h int32) {
	w, h = max(w, 1), max(h, 1)
	if s.sc == 0 || w == s.w && h == s.h {
		return
	}
	win32.Release(s.brush)
	win32.Release(s.rt)
	s.brush, s.rt = 0, 0
	if hr := win32.HR(win32.Com(s.sc, vtSwapChainResizeBuffers, 0, uintptr(w), uintptr(h), 0, 0)); hr < 0 {
		return
	}
	s.w, s.h = w, h
	s.target()
}

// close releases everything.
func (s *edSurface) close() {
	for _, p := range [...]*uintptr{&s.format, &s.brush, &s.rt, &s.sc} {
		win32.Release(*p)
		*p = 0
	}
}

// colorOf is 0xRRGGBBAA as a D2D colour (alpha forced opaque when 0).
func colorOf(c uint32) colorF {
	f := func(sh uint) float32 { return float32(c>>sh&0xFF) / 255 }
	return colorF{f(24), f(16), f(8), f(0)}
}

func (s *edSurface) setColor(c colorF) {
	s.col = c
	win32.Com(s.brush, vtBrushSetColor, uintptr(unsafe.Pointer(&s.col)))
}

// fillRect fills r (pixels) with colour c.
func (s *edSurface) fillRect(l, t, r, b float32, c colorF) {
	s.setColor(c)
	s.rect = rectF{l, t, r, b}
	win32.Com(s.rt, vtRTFillRectangle, uintptr(unsafe.Pointer(&s.rect)), s.brush)
}

// placeholderFormat makes (or remakes, for a new size) the placeholder's
// text format: Segoe UI at px pixels, no wrapping.
func (s *edSurface) placeholderFormat(px float32) uintptr {
	if s.format != 0 && s.fmtPt == px {
		return s.format
	}
	win32.Release(s.format)
	s.format = 0
	copy(s.face[:], []uint16{'S', 'e', 'g', 'o', 'e', ' ', 'U', 'I', 0})
	s.locale = [6]uint16{'e', 'n', '-', 'u', 's', 0}
	r := edGfx.frame.CallF(edGfx.dw, vtDWFactoryCreateTextFormat, 1<<5,
		uintptr(unsafe.Pointer(&s.face[0])), 0, 400, 0, 5, uintptr(math.Float32bits(px)), uintptr(unsafe.Pointer(&s.locale[0])), uintptr(unsafe.Pointer(&s.format)))
	if win32.HR(r) < 0 {
		s.format = 0
		return 0
	}
	s.fmtPt = px
	win32.Com(s.format, vtTextFormatSetWordWrapping, 1 /* DWRITE_WORD_WRAPPING_NO_WRAP */)
	return s.format
}

// drawText draws UTF-16 text (no NUL) in rect r with colour c.
func (s *edSurface) drawText(text []uint16, format uintptr, l, t, r, b float32, c colorF) {
	if len(text) == 0 || format == 0 {
		return
	}
	s.setColor(c)
	s.rect = rectF{l, t, r, b}
	win32.Com(s.rt, vtRTDrawText, uintptr(unsafe.Pointer(&text[0])), uintptr(len(text)), format, uintptr(unsafe.Pointer(&s.rect)), s.brush, 0, 0)
}
