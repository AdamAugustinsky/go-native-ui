//go:build windows

package capture

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"gonativeui/internal/com"
	"gonativeui/internal/win32"
	"gonativeui/platform"
)

// Windows 10 2004 and later (M2, D72): Windows.Graphics.Capture, the D3D11
// video processor, WASAPI and a Media Foundation sink writer, through
// internal/win32 and internal/com, no cgo.
//
// The main thread runs the session (session.go) as on macOS. The system's
// picker completes on the main thread's apartment; the capture item's
// Closed event and the audio activation complete on threads of their own.
// Each handler only stores what it got and posts to the live session's
// inbox (or signals the recorder); posts wake the main thread with a
// message to a message-only window, whose procedure pumps the session
// there.
//
// The recording itself runs on a capture thread of its own (a locked
// goroutine in the multithreaded apartment, recorder_windows.go): it polls
// the free-threaded frame pool, converts each new frame to NV12 with the
// video processor, pulls the microphone and the system's audio from WASAPI
// and writes both to one MP4 with the sink writer. A pause stops feeding the
// file and offsets the later timestamps, so a session is oneFile: no
// segments, no join.

// guid parses "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx" (package setup only).
func guid(s string) win32.GUID {
	p := strings.Split(s, "-")
	if len(p) != 5 || len(p[0]) != 8 || len(p[1]) != 4 || len(p[2]) != 4 || len(p[3]) != 4 || len(p[4]) != 12 {
		panic("capture: bad GUID " + s)
	}
	u := func(h string, bits int) uint64 {
		v, err := strconv.ParseUint(h, 16, bits)
		if err != nil {
			panic("capture: bad GUID " + s)
		}
		return v
	}
	g := win32.GUID{Data1: uint32(u(p[0], 32)), Data2: uint16(u(p[1], 16)), Data3: uint16(u(p[2], 16))}
	rest := p[3] + p[4]
	for i := range g.Data4 {
		g.Data4[i] = byte(u(rest[2*i:2*i+2], 8))
	}
	return g
}

// Interface ids: windows.graphics.capture.h, windows.graphics.capture.interop.h,
// windows.graphics.directx.direct3d11.interop.h, shobjidl.h, d3d11.h,
// d3d10.h, dxgi.h (the Windows SDK; mingw-w64's copies), and windows-rs for
// IGraphicsCapturePicker, which mingw-w64 lacks.
var (
	iidSessionStatics      = guid("2224a540-5974-49aa-b232-0882536f4cb5") // IGraphicsCaptureSessionStatics
	iidItemInterop         = guid("3628e81b-3cac-4c60-b7f4-23ce0e0c3356") // IGraphicsCaptureItemInterop
	iidItem                = guid("79c3f95b-31f7-4ec2-a464-632ef5d30760") // IGraphicsCaptureItem
	iidPicker              = guid("5a1711b3-ad79-4b4a-9336-1318fdde3539") // IGraphicsCapturePicker
	iidInitializeWithWin   = guid("3e68d4bd-7135-4d10-8018-9fb6d9f33fa1") // IInitializeWithWindow
	iidPickCompleted       = guid("43004a3c-ffe7-5352-85a6-7bc41b782fca") // AsyncOperationCompletedHandler<GraphicsCaptureItem>
	iidItemClosed          = guid("e9c610c0-a68c-5bd9-8021-8589346eeee2") // TypedEventHandler<GraphicsCaptureItem, IInspectable>
	iidAsyncInfo           = guid("00000036-0000-0000-c000-000000000046") // IAsyncInfo
	iidPoolStatics2        = guid("589b103f-6bbc-5df5-a991-02e28b3b66d5") // IDirect3D11CaptureFramePoolStatics2
	iidSession2            = guid("2c39ae40-7d2e-5044-804e-8b6799d4cf9e") // IGraphicsCaptureSession2
	iidClosable            = guid("30d5a829-7fa4-4026-83bb-d75bae4ea99e") // IClosable
	iidDirect3DDevice      = guid("a37624ab-8d5f-4650-9d3e-9eae3d9bc670") // IDirect3DDevice
	iidDxgiInterfaceAccess = guid("a9b3d012-3df2-4ee3-b8d1-8695f457d3c1") // IDirect3DDxgiInterfaceAccess
	iidDXGIDevice          = guid("54ec77fa-1377-44e6-8c32-88fd5f44c84c") // IDXGIDevice
	iidTexture2D           = guid("6f15aaf2-d208-4e89-9ab4-489535d34f9c") // ID3D11Texture2D
	iidVideoDevice         = guid("10ec4d5b-975a-4689-b9e4-d0aac30fe333") // ID3D11VideoDevice
	iidVideoContext        = guid("61f21c45-3c0e-4a74-9cea-67100d9ad5e4") // ID3D11VideoContext
	iidMultithread         = guid("9b7e4e00-342c-4106-a19f-4f2704f689f0") // ID3D10Multithread
)

// Vtable slots (zero-based, IUnknown included; WinRT interfaces start after
// IInspectable, win32.InspectableFirst = 6). From the same headers.
const (
	vtSessionStaticsIsSupported = 6  // IGraphicsCaptureSessionStatics::IsSupported
	vtInteropCreateForWindow    = 3  // IGraphicsCaptureItemInterop::CreateForWindow
	vtInteropCreateForMonitor   = 4  // ::CreateForMonitor
	vtItemGetSize               = 7  // IGraphicsCaptureItem: get_DisplayName 6, get_Size 7
	vtItemAddClosed             = 8  // add_Closed 8, remove_Closed 9
	vtItemRemoveClosed          = 9  //
	vtPickerPickSingle          = 6  // IGraphicsCapturePicker::PickSingleItemAsync
	vtInitWithWindow            = 3  // IInitializeWithWindow::Initialize
	vtAsyncOpPutCompleted       = 6  // IAsyncOperation<T>: put_Completed 6, get_Completed 7, GetResults 8
	vtAsyncOpGetResults         = 8  //
	vtAsyncInfoCancel           = 9  // IAsyncInfo: Id 6, Status 7, ErrorCode 8, Cancel 9, Close 10
	vtAsyncInfoClose            = 10 //
	vtClosableClose             = 6  // IClosable::Close
)

// HRESULTs we classify (winerror.h, mferror.h).
const (
	hrAccessDenied    = -2147024891 // E_ACCESSDENIED 0x80070005
	hrInvalidArg      = -2147024809 // E_INVALIDARG 0x80070057
	hrDiskFull        = -2147024784 // HRESULT_FROM_WIN32(ERROR_DISK_FULL) 0x80070070
	hrHandleDiskFull  = -2147024857 // HRESULT_FROM_WIN32(ERROR_HANDLE_DISK_FULL) 0x80070027
	hrNotFound        = -2147023728 // HRESULT_FROM_WIN32(ERROR_NOT_FOUND) 0x80070490
	hrInvalidWindow   = -2147023496 // HRESULT_FROM_WIN32(ERROR_INVALID_WINDOW_HANDLE) 0x80070578
	hrCodecNotFound   = -1072868846 // MF_E_TOPO_CODEC_NOT_FOUND 0xC00D5212
	hrInvalidMediaTyp = -1072875852 // MF_E_INVALIDMEDIATYPE 0xC00D36B4
	hrDeviceInvalid   = -2004287484 // AUDCLNT_E_DEVICE_INVALIDATED 0x88890004
)

// hrError maps a failed HRESULT of step to a typed error when it is one of
// ours, keeping the step and the code.
func hrError(step string, hr int32) error {
	var kind error
	switch hr {
	case hrAccessDenied:
		kind = ErrPermissionDenied
	case hrDiskFull, hrHandleDiskFull:
		kind = ErrDiskFull
	case hrCodecNotFound:
		kind = ErrNoEncoder
	}
	if kind != nil {
		return fmt.Errorf("%w: %s (0x%08X)", kind, step, uint32(hr))
	}
	return fmt.Errorf("capture: %s failed (0x%08X)", step, uint32(hr))
}

// cm calls vtable slot idx of obj, and call calls fn, moving what a pointer
// converted in the call expression points to onto the heap (the
// internal/win32 rule for out parameters). For setup; the capture loop uses
// com.Call with heap fields, which allocates nothing.
//
//go:uintptrescapes
func cm(obj, idx uintptr, args ...uintptr) uintptr { return com.Call(obj, idx, args...) }

//go:uintptrescapes
func call(fn uintptr, args ...uintptr) uintptr { return win32.Call(fn, args...) }

// closeAndRelease closes a WinRT object (IClosable) and drops our
// reference.
func closeAndRelease(obj uintptr) {
	if obj == 0 {
		return
	}
	var c uintptr
	if win32.QueryInterface(obj, &iidClosable, &c) >= 0 && c != 0 {
		com.Call(c, vtClosableClose)
		win32.Release(c)
	}
	win32.Release(obj)
}

// Entry points, resolved once (load).
var sys struct {
	once sync.Once
	err  error

	kernel32, user32, d3d11, mmdevapi, ole32, mfplat, mfrw, dwmapi uintptr

	createEvent, setEvent, waitOne, qpc, qpf, diskFree         uintptr
	openProcess, queryImage                                    uintptr
	registerClass, createWindow, defWindowProc, postMessage    uintptr
	enumWindows, isVisible, isIconic, getWindow, windowTextLen uintptr
	windowText, windowThread, windowLong, windowRect, isWindow uintptr
	dpiForWindow, enumMonitors, monitorInfo, dpiForMonitor     uintptr
	activeWindow, dwmAttr                                      uintptr
	d3d11Create, deviceFromDXGI                                uintptr
	coCreate, coTaskMemFree, activateAudioAsync                uintptr
	mfStartup, mfCreateAttributes, mfCreateMediaType           uintptr
	mfCreateMemoryBuffer, mfCreateSample                       uintptr
	mfCreateSinkWriter, mfCreateSourceReader                   uintptr

	qpcFreq int64
}

// load resolves the entry points and starts Media Foundation. Any thread.
func load() error {
	sys.once.Do(func() { sys.err = loadWindows() })
	return sys.err
}

func loadWindows() error {
	k, u := win32.LoadSystem("kernel32.dll"), win32.LoadSystem("user32.dll")
	sys.kernel32, sys.user32 = k, u
	sys.d3d11 = win32.LoadSystem("d3d11.dll")
	sys.mmdevapi = win32.LoadSystem("mmdevapi.dll")
	sys.ole32 = win32.LoadSystem("ole32.dll")
	sys.dwmapi = win32.LoadSystem("dwmapi.dll")
	for _, p := range []struct {
		dst  *uintptr
		dll  uintptr
		name string
	}{
		{&sys.createEvent, k, "CreateEventW"}, {&sys.setEvent, k, "SetEvent"}, {&sys.waitOne, k, "WaitForSingleObject"},
		{&sys.qpc, k, "QueryPerformanceCounter"}, {&sys.qpf, k, "QueryPerformanceFrequency"},
		{&sys.diskFree, k, "GetDiskFreeSpaceExW"}, {&sys.openProcess, k, "OpenProcess"},
		{&sys.queryImage, k, "QueryFullProcessImageNameW"},
		{&sys.registerClass, u, "RegisterClassExW"}, {&sys.createWindow, u, "CreateWindowExW"},
		{&sys.defWindowProc, u, "DefWindowProcW"}, {&sys.postMessage, u, "PostMessageW"},
		{&sys.enumWindows, u, "EnumWindows"}, {&sys.isVisible, u, "IsWindowVisible"}, {&sys.isIconic, u, "IsIconic"},
		{&sys.getWindow, u, "GetWindow"}, {&sys.windowTextLen, u, "GetWindowTextLengthW"},
		{&sys.windowText, u, "GetWindowTextW"}, {&sys.windowThread, u, "GetWindowThreadProcessId"},
		{&sys.windowLong, u, "GetWindowLongPtrW"}, {&sys.windowRect, u, "GetWindowRect"}, {&sys.isWindow, u, "IsWindow"},
		{&sys.dpiForWindow, u, "GetDpiForWindow"}, {&sys.enumMonitors, u, "EnumDisplayMonitors"},
		{&sys.monitorInfo, u, "GetMonitorInfoW"}, {&sys.activeWindow, u, "GetActiveWindow"},
		{&sys.dpiForMonitor, win32.LoadSystem("shcore.dll"), "GetDpiForMonitor"},
		{&sys.dwmAttr, sys.dwmapi, "DwmGetWindowAttribute"},
		{&sys.d3d11Create, sys.d3d11, "D3D11CreateDevice"},
		{&sys.deviceFromDXGI, sys.d3d11, "CreateDirect3D11DeviceFromDXGIDevice"},
		{&sys.coCreate, sys.ole32, "CoCreateInstance"}, {&sys.coTaskMemFree, sys.ole32, "CoTaskMemFree"},
		{&sys.activateAudioAsync, sys.mmdevapi, "ActivateAudioInterfaceAsync"},
	} {
		*p.dst = win32.Proc(p.dll, p.name)
	}
	call(sys.qpf, uintptr(unsafe.Pointer(&sys.qpcFreq)))
	if sys.qpcFreq <= 0 {
		sys.qpcFreq = 10_000_000
	}
	// Windows.Graphics.Capture: Windows 10 1803 for the API, 2004 for
	// CreateFreeThreaded. The factories need an apartment: asked on a
	// thread of the multithreaded one, whatever the caller's thread.
	if !win32.WinRTAvailable() || sys.d3d11Create == 0 || sys.deviceFromDXGI == 0 {
		return fmt.Errorf("%w: no WinRT or D3D11", ErrUnsupported)
	}
	var err error
	inMTA(func() { err = checkWGC() })
	if err != nil {
		return err
	}
	if err := loadMF(); err != nil {
		return err
	}
	makeKinds()
	return nil
}

func checkWGC() error {
	var statics uintptr
	if hr := win32.RoGetActivationFactory("Windows.Graphics.Capture.GraphicsCaptureSession", &iidSessionStatics, &statics); hr < 0 {
		return fmt.Errorf("%w: no Windows.Graphics.Capture (0x%08X)", ErrUnsupported, uint32(hr))
	}
	var ok uint8
	hr := win32.HR(cm(statics, vtSessionStaticsIsSupported, uintptr(unsafe.Pointer(&ok))))
	win32.Release(statics)
	if hr < 0 || ok == 0 {
		return fmt.Errorf("%w: GraphicsCaptureSession.IsSupported is false (0x%08X)", ErrUnsupported, uint32(hr))
	}
	var ps uintptr
	if hr := win32.RoGetActivationFactory("Windows.Graphics.Capture.Direct3D11CaptureFramePool", &iidPoolStatics2, &ps); hr < 0 {
		return fmt.Errorf("%w: no free-threaded frame pool before Windows 10 2004 (0x%08X)", ErrUnsupported, uint32(hr))
	}
	win32.Release(ps)
	return nil
}

// The COM objects we implement: the picker's completion, the item's Closed
// handler and WASAPI's activation completion. Value is the recording's id
// (the picker, Closed) or an event handle (activation).
var kinds struct {
	pick, closed, activated *com.Kind
}

func makeKinds() {
	kinds.pick = com.NewKind("capture.PickCompleted", 4, []win32.GUID{iidPickCompleted}, []com.Method{
		{N: 2, Fn: func(o *com.Object, a com.Args) uintptr {
			pickCompleted(uint64(o.Value()), a[0], uint32(a[1]))
			return 0
		}},
	})
	kinds.closed = com.NewKind("capture.ItemClosed", 4, []win32.GUID{iidItemClosed}, []com.Method{
		{N: 2, Fn: func(o *com.Object, a com.Args) uintptr {
			if s := live.Load(); s != nil && uint64(o.Value()) == s.r.id {
				if n, ok := s.n.(*wgcNative); ok && n.rec != nil {
					n.rec.targetGone()
				}
			}
			return 0
		}},
	})
	kinds.activated = com.NewKind("capture.AudioActivated", 4, []win32.GUID{iidActivateCompleted}, []com.Method{
		{N: 1, Fn: func(o *com.Object, a com.Args) uintptr { win32.Call(sys.setEvent, o.Value()); return 0 }},
	})
}

// live is the running recording's session (one at a time); written on the
// main thread, read by the handlers.
var live atomic.Pointer[session]

// The main thread's waker: a message-only window made on the first
// recording's thread (the app's main thread), whose procedure pumps the
// live session.
var waker struct {
	hwnd    uintptr
	thread  uint32
	pending atomic.Bool
	class   []uint16
	wc      wndClassEx
}

// wndClassEx is WNDCLASSEXW (winuser.h).
type wndClassEx struct {
	size, style            uint32
	wndProc                uintptr
	clsExtra, wndExtra     int32
	instance, icon, cursor uintptr
	background             uintptr
	menuName, className    *uint16
	iconSm                 uintptr
}

const wmCaptureWake = 0x8000 + 0x47 // WM_APP + 'G'

func makeWaker() error {
	if waker.hwnd != 0 {
		return nil
	}
	waker.class, _ = syscall.UTF16FromString("GnuiCaptureWaker")
	waker.wc = wndClassEx{wndProc: syscall.NewCallback(wakerProc), className: &waker.class[0]}
	waker.wc.size = uint32(unsafe.Sizeof(waker.wc))
	call(sys.registerClass, uintptr(unsafe.Pointer(&waker.wc)))
	const hwndMessage = ^uintptr(2) // HWND_MESSAGE (-3)
	waker.hwnd = call(sys.createWindow, 0, uintptr(unsafe.Pointer(&waker.class[0])), 0, 0, 0, 0, 0, 0, hwndMessage, 0, 0, 0)
	if waker.hwnd == 0 {
		return errors.New("capture: no message window for the main thread")
	}
	return nil
}

func wakerProc(hwnd, m, wp, lp uintptr) uintptr {
	if m == wmCaptureWake {
		waker.pending.Store(false)
		if s := live.Load(); s != nil {
			s.pump()
		}
		return 0
	}
	return win32.Call(sys.defWindowProc, hwnd, m, wp, lp)
}

// wakeMain has the main thread pump the live session. Any thread; it
// allocates nothing.
func wakeMain() {
	if waker.pending.CompareAndSwap(false, true) {
		if !win32.Bool(win32.Call(sys.postMessage, waker.hwnd, wmCaptureWake, 0, 0)) {
			waker.pending.Store(false)
		}
	}
}

// minFree is the free space below which a recording does not start.
const minFree = 64 << 20

func osPick(w *platform.Window, r *Recording) error {
	var owner uintptr
	if w != nil {
		owner = w.Native().HWND
	}
	return begin(r, nil, owner)
}

func osStart(t Target, r *Recording) error {
	if t.Kind != Display && t.Kind != Window {
		return fmt.Errorf("capture: target kind %d", t.Kind)
	}
	return begin(r, &t, 0)
}

func begin(r *Recording, t *Target, owner uintptr) error {
	if err := load(); err != nil {
		return err
	}
	if live.Load() != nil {
		return ErrBusy
	}
	if err := makeWaker(); err != nil {
		return err
	}
	// The picker's apartment: the main thread is the app's OLE STA; a
	// thread in none joins one (S_FALSE and RPC_E_CHANGED_MODE are fine).
	win32.RoInitialize(win32.RoInitSingleThreaded)
	out := r.opt.Path
	if out == "" {
		out = filepath.Join(os.TempDir(), fmt.Sprintf("gnui-capture-%d-%d.mp4", os.Getpid(), r.id))
	}
	if abs, err := filepath.Abs(out); err == nil {
		out = abs
		dir, _ := syscall.UTF16PtrFromString(filepath.Dir(out))
		var free uint64
		if win32.Bool(call(sys.diskFree, uintptr(unsafe.Pointer(dir)), uintptr(unsafe.Pointer(&free)), 0, 0)) && free < minFree {
			return ErrDiskFull
		}
	}
	if t == nil && owner == 0 {
		owner = win32.Call(sys.activeWindow)
	}
	n := &wgcNative{r: r, o: r.opt, target: t, owner: owner}
	s := newSession(r, n, out, wakeMain)
	s.oneFile = true
	n.s = s
	s.done = func() { live.CompareAndSwap(s, nil) }
	if err := s.reserve(0); err != nil {
		return err
	}
	live.Store(s)
	if err := s.begin(); err != nil {
		live.Store(nil)
		s.unreserve()
		n.release()
		return err
	}
	r.ownAudio = r.opt.SystemAudio && !r.opt.IncludeOwnAudio // until the recorder knows
	return nil
}

// wgcNative is one recording's half on the main thread: the picker or the
// target's capture item, and the recorder once started.
type wgcNative struct {
	r      *Recording
	s      *session
	o      Options
	target *Target
	owner  uintptr

	item    uintptr        // IGraphicsCaptureItem, ours
	picked  atomic.Uintptr // the picker's item, stored by its completion
	op      uintptr        // the picker's IAsyncOperation
	handler *com.Object    // the picker's completion handler
	pickOut uintptr        // GetResults' out parameter (the completion's)
	rec     *recorder
	size    [2]int32 // get_Size's out parameter
}

func (n *wgcNative) choose() error {
	if n.target != nil {
		var interop uintptr
		if hr := win32.RoGetActivationFactory("Windows.Graphics.Capture.GraphicsCaptureItem", &iidItemInterop, &interop); hr < 0 {
			return hrError("GraphicsCaptureItem interop", hr)
		}
		slot := uintptr(vtInteropCreateForWindow)
		if n.target.Kind == Display {
			slot = vtInteropCreateForMonitor
		} else if !win32.Bool(win32.Call(sys.isWindow, uintptr(n.target.ID))) {
			slot = 0
		}
		if slot != 0 {
			var item uintptr
			hr := win32.HR(cm(interop, slot, uintptr(n.target.ID), uintptr(unsafe.Pointer(&iidItem)), uintptr(unsafe.Pointer(&item))))
			if hr >= 0 {
				n.item = item
			} else if hr != hrInvalidArg && hr != hrInvalidWindow {
				win32.Release(interop)
				return hrError("CreateForWindow/Monitor", hr)
			}
		}
		win32.Release(interop)
		n.s.post(msg{kind: mChosen}) // chosen reports a missing target
		return nil
	}
	if n.owner == 0 {
		return errors.New("capture: the picker needs a window (Pick's window, or an active one)")
	}
	var insp, init, picker uintptr
	if hr := win32.RoActivateInstance("Windows.Graphics.Capture.GraphicsCapturePicker", &insp); hr < 0 {
		return hrError("GraphicsCapturePicker", hr)
	}
	defer win32.Release(insp)
	if hr := win32.QueryInterface(insp, &iidInitializeWithWin, &init); hr < 0 {
		return hrError("IInitializeWithWindow", hr)
	}
	hr := win32.HR(com.Call(init, vtInitWithWindow, n.owner))
	win32.Release(init)
	if hr < 0 {
		return hrError("IInitializeWithWindow::Initialize", hr)
	}
	if hr := win32.QueryInterface(insp, &iidPicker, &picker); hr < 0 {
		return hrError("IGraphicsCapturePicker", hr)
	}
	defer win32.Release(picker)
	o, _, err := kinds.pick.New(uintptr(n.r.id))
	if err != nil {
		return ErrBusy
	}
	if hr := win32.HR(cm(picker, vtPickerPickSingle, uintptr(unsafe.Pointer(&n.op)))); hr < 0 {
		o.Release()
		return hrError("PickSingleItemAsync", hr)
	}
	n.handler = o
	if hr := win32.HR(com.Call(n.op, vtAsyncOpPutCompleted, o.Ptr())); hr < 0 {
		n.unchoose()
		return hrError("the picker's put_Completed", hr)
	}
	return nil
}

// pickCompleted is the picker's completion (the main thread's apartment):
// the item the user chose, or none.
func pickCompleted(id uint64, op uintptr, status uint32) {
	s := live.Load()
	if s == nil || s.r.id != id {
		return
	}
	n, ok := s.n.(*wgcNative)
	if !ok {
		return
	}
	const asyncCompleted, asyncCanceled = 1, 2 // AsyncStatus (windows.foundation.h)
	switch status {
	case asyncCompleted:
		n.pickOut = 0
		hr := win32.HR(com.Call(op, vtAsyncOpGetResults, uintptr(unsafe.Pointer(&n.pickOut))))
		if hr < 0 {
			s.post(msg{kind: mFailed, err: hrError("the picker's GetResults", hr)})
			return
		}
		if n.pickOut == 0 {
			s.post(msg{kind: mCancelled})
			return
		}
		if old := n.picked.Swap(n.pickOut); old != 0 {
			win32.Release(old)
		}
		s.post(msg{kind: mChosen})
	case asyncCanceled:
		s.post(msg{kind: mCancelled})
	default:
		s.post(msg{kind: mFailed, err: errors.New("capture: the system's picker failed")})
	}
}

func (n *wgcNative) unchoose() {
	if n.op != 0 {
		var info uintptr
		if win32.QueryInterface(n.op, &iidAsyncInfo, &info) >= 0 && info != 0 {
			com.Call(info, vtAsyncInfoCancel)
			com.Call(info, vtAsyncInfoClose)
			win32.Release(info)
		}
		win32.Release(n.op)
		n.op = 0
	}
	if n.handler != nil {
		n.handler.Release()
		n.handler = nil
	}
}

func (n *wgcNative) chosen() error {
	if n.target == nil {
		n.unchoose()
		n.item = n.picked.Swap(0)
	}
	if n.item == 0 {
		return ErrTargetGone
	}
	return nil
}

func (n *wgcNative) start(path string) error {
	n.size = [2]int32{}
	com.Call(n.item, vtItemGetSize, uintptr(unsafe.Pointer(&n.size)))
	if n.size[0] <= 0 || n.size[1] <= 0 {
		return ErrTargetGone // a minimized window
	}
	win32.AddRef(n.item)
	n.rec = newRecorder(n, n.item, path, n.size)
	go n.rec.run()
	return nil
}

func (n *wgcNative) attach(int, string) error { n.rec.resume(); return nil }
func (n *wgcNative) detach() error            { n.rec.pause(); return nil }
func (n *wgcNative) stopCapture()             { n.rec.stop() }
func (n *wgcNative) errorOf(uintptr) error    { return nil }
func (n *wgcNative) drop(uintptr)             {}

func (n *wgcNative) release() {
	n.unchoose()
	win32.Release(n.item)
	n.item = 0
	if p := n.picked.Swap(0); p != 0 {
		win32.Release(p)
	}
}

func (n *wgcNative) join([]string, string) (time.Duration, error) {
	return 0, errors.New("capture: a Windows recording is one file")
}

func (n *wgcNative) duration(path string) time.Duration {
	return readFile(path, false).duration
}

// inMTA runs f on a new OS thread in the multithreaded apartment, and waits.
// The thread ends with f (it stays locked), so the apartment goes with it.
func inMTA(f func()) {
	done := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer close(done)
		win32.RoInitialize(win32.RoInitMultiThreaded)
		f()
	}()
	<-done
}

// osTargets lists the displays (EnumDisplayMonitors) or the top-level
// windows a user would pick (EnumWindows: visible, uncloaked, titled, not
// owned, not tool windows, not minimized).
func osTargets(kind TargetKind) ([]Target, error) {
	if err := load(); err != nil {
		return nil, err
	}
	targets.mu.Lock()
	defer targets.mu.Unlock()
	targets.out = nil
	if kind == Display {
		if targets.monCB == 0 {
			targets.monCB = syscall.NewCallback(monitorFound)
		}
		win32.Call(sys.enumMonitors, 0, 0, targets.monCB, 0)
	} else {
		if targets.winCB == 0 {
			targets.winCB = syscall.NewCallback(windowFound)
		}
		win32.Call(sys.enumWindows, targets.winCB, 0)
	}
	out := targets.out
	targets.out = nil
	return out, nil
}

var targets struct {
	mu           sync.Mutex
	out          []Target
	monCB, winCB uintptr
	mi           monitorInfoEx
	rect         [4]int32
	dpiX, dpiY   uint32
	cloaked      uint32
	pid          uint32
	text         [512]uint16
	textLen      uint32
}

// monitorInfoEx is MONITORINFOEXW (winuser.h).
type monitorInfoEx struct {
	size    uint32
	monitor [4]int32
	work    [4]int32
	flags   uint32
	device  [32]uint16
}

// points converts pixels at dpi to points (96 per inch).
func points(px int32, dpi uint32) int {
	if dpi == 0 {
		return int(px)
	}
	return int((int64(px)*96 + int64(dpi)/2) / int64(dpi))
}

func monitorFound(mon, hdc, rc, data uintptr) uintptr {
	t := &targets
	t.mi = monitorInfoEx{size: uint32(unsafe.Sizeof(monitorInfoEx{}))}
	if !win32.Bool(win32.Call(sys.monitorInfo, mon, uintptr(unsafe.Pointer(&t.mi)))) {
		return 1
	}
	t.dpiX = 96
	if sys.dpiForMonitor != 0 && win32.HR(win32.Call(sys.dpiForMonitor, mon, 0, uintptr(unsafe.Pointer(&t.dpiX)), uintptr(unsafe.Pointer(&t.dpiY)))) < 0 {
		t.dpiX = 96
	}
	m := t.mi.monitor
	d := Target{Kind: Display, ID: uint64(mon), Title: syscall.UTF16ToString(t.mi.device[:]),
		Width: points(m[2]-m[0], t.dpiX), Height: points(m[3]-m[1], t.dpiX)}
	if t.mi.flags&1 != 0 { // MONITORINFOF_PRIMARY first
		t.out = append([]Target{d}, t.out...)
	} else {
		t.out = append(t.out, d)
	}
	return 1
}

func windowFound(hwnd, _ uintptr) uintptr {
	t := &targets
	const (
		gwOwner       = 4
		gwlExStyle    = ^uintptr(19) // -20
		wsExTool      = 0x80
		wsExAppWindow = 0x40000
		dwmCloaked    = 14
		dwmFrame      = 9 // DWMWA_EXTENDED_FRAME_BOUNDS
	)
	if !win32.Bool(win32.Call(sys.isVisible, hwnd)) || win32.Bool(win32.Call(sys.isIconic, hwnd)) ||
		win32.Call(sys.getWindow, hwnd, gwOwner) != 0 {
		return 1
	}
	if ex := win32.Call(sys.windowLong, hwnd, gwlExStyle); ex&wsExTool != 0 && ex&wsExAppWindow == 0 {
		return 1
	}
	t.cloaked = 0
	if sys.dwmAttr != 0 {
		win32.Call(sys.dwmAttr, hwnd, dwmCloaked, uintptr(unsafe.Pointer(&t.cloaked)), 4)
	}
	if t.cloaked != 0 {
		return 1
	}
	n := int(win32.Call(sys.windowText, hwnd, uintptr(unsafe.Pointer(&t.text[0])), uintptr(len(t.text))))
	if n <= 0 {
		return 1
	}
	w := Target{Kind: Window, ID: uint64(hwnd), Title: syscall.UTF16ToString(t.text[:n])}
	if sys.dwmAttr == 0 || win32.HR(win32.Call(sys.dwmAttr, hwnd, dwmFrame, uintptr(unsafe.Pointer(&t.rect)), 16)) < 0 {
		win32.Call(sys.windowRect, hwnd, uintptr(unsafe.Pointer(&t.rect)))
	}
	dpi := uint32(96)
	if sys.dpiForWindow != 0 {
		dpi = uint32(win32.Call(sys.dpiForWindow, hwnd))
	}
	w.Width, w.Height = points(t.rect[2]-t.rect[0], dpi), points(t.rect[3]-t.rect[1], dpi)
	t.pid = 0
	win32.Call(sys.windowThread, hwnd, uintptr(unsafe.Pointer(&t.pid)))
	const queryLimited = 0x1000 // PROCESS_QUERY_LIMITED_INFORMATION
	if h := win32.Call(sys.openProcess, queryLimited, 0, uintptr(t.pid)); h != 0 {
		t.textLen = uint32(len(t.text))
		if win32.Bool(win32.Call(sys.queryImage, h, 0, uintptr(unsafe.Pointer(&t.text[0])), uintptr(unsafe.Pointer(&t.textLen)))) {
			w.App = strings.TrimSuffix(filepath.Base(syscall.UTF16ToString(t.text[:t.textLen])), ".exe")
		}
		win32.CloseHandle(h)
	}
	t.out = append(t.out, w)
	return 1
}
