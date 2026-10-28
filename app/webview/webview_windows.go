//go:build windows

package webview

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"gonativeui/app/paths"
	"gonativeui/internal/com"
	"gonativeui/internal/win32"
	"gonativeui/platform"
)

// Windows (H2, D65): WebView2's Evergreen runtime, loaded without
// WebView2Loader.dll, as a child window of ours that the window hosts (D66).
//
//   - The loader: the runtime's folder is read from the registry as the
//     loader reads it (EdgeUpdate's ClientState for the runtime, per machine
//     then per user; the BrowserExecutableFolder policy first), its
//     EmbeddedBrowserWebView.dll is loaded from
//     <version folder>\EBWebView\<x64|arm64|x86>\ and its export
//     CreateWebViewEnvironmentWithOptionsInternal is called, with an
//     ICoreWebView2EnvironmentOptions implemented in Go (probe P3).
//   - One environment for the process, made by the first New, in the user
//     data folder (Options.DataDir, the UserDataFolder policy, or the cache
//     folder of the executable's name). It and each view's controller are
//     created asynchronously; Navigate before the controller is ready is
//     kept and done when it is. Nothing of this runs before the first New.
//   - Each view: a controller made with ICoreWebView2ControllerOptions
//     (ICoreWebView2Environment10): a named profile, or InPrivate in a
//     profile of its own for a non-persistent session (deleted with the
//     view, ICoreWebView2Profile8.Delete), so that two non-persistent views
//     share nothing, as on macOS.
//   - The policy is applied in the event handlers, in the call: the scheme
//     allow-list in NavigationStarting, new windows in NewWindowRequested
//     (loaded in place or refused, never a second window), permissions in
//     PermissionRequested, downloads in DownloadStarting (the path ours, the
//     download UI off).
//   - WebView2 reports no load progress; Progress is 0.1 at the start of a
//     navigation, 0.4 when the content loads, 0.7 at DOMContentLoaded and 1
//     when it completes.
//   - Handlers are COM objects of one internal/com kind, whose word is the
//     view's (or the download's) ID and the event, and run on the main
//     thread (the STA) during the window's message dispatch.
//
// Vtable slots and IIDs are read from WebView2.h of the Microsoft.Web.WebView2
// NuGet package 1.0.4314.35 (the C vtables, I…Vtbl), counting IUnknown's 0-2.

// Vtable slots.
const (
	// ICoreWebView2Environment / ICoreWebView2Environment10
	envCreateController            = 3
	envCreateControllerOptions     = 20
	envCreateControllerWithOptions = 21

	// ICoreWebView2ControllerOptions
	copPutProfileName = 4
	copPutInPrivate   = 6

	// ICoreWebView2Controller
	ctrlPutIsVisible         = 4
	ctrlPutBounds            = 6
	ctrlMoveFocus            = 12
	ctrlNotifyParentMoved    = 23
	ctrlClose                = 24
	ctrlGetCoreWebView2      = 25
	ctrlPutDefaultBackground = 27 // ICoreWebView2Controller2

	// ICoreWebView2 (and _2, _3, _4, _13, which extend it)
	wvGetSettings          = 3
	wvGetSource            = 4
	wvNavigate             = 5
	wvAddNavStarting       = 7
	wvRemoveNavStarting    = 8
	wvAddContentLoading    = 9
	wvRemoveContentLoading = 10
	wvAddSourceChanged     = 11
	wvRemoveSourceChanged  = 12
	wvAddHistoryChanged    = 13
	wvRemoveHistoryChanged = 14
	wvAddNavCompleted      = 15
	wvRemoveNavCompleted   = 16
	wvAddPermission        = 23
	wvRemovePermission     = 24
	wvAddProcessFailed     = 25
	wvRemoveProcessFailed  = 26
	wvReload               = 31
	wvGetCanGoBack         = 38
	wvGetCanGoForward      = 39
	wvGoBack               = 40
	wvGoForward            = 41
	wvStop                 = 43
	wvAddNewWindow         = 44
	wvRemoveNewWindow      = 45
	wvAddTitleChanged      = 46
	wvRemoveTitleChanged   = 47
	wvGetDocumentTitle     = 48
	wvAddDOMContentLoaded  = 64 // _2
	wvRemoveDOMContent     = 65
	wvTrySuspend           = 68 // _3
	wvResume               = 69
	wvGetIsSuspended       = 70
	wvAddDownloadStarting  = 75 // _4
	wvRemoveDownloadStart  = 76
	wvGetProfile           = 105 // _13

	// ICoreWebView2Settings / ICoreWebView2Settings2
	setPutDevTools     = 12
	setPutContextMenus = 14
	setGetUserAgent    = 21
	setPutUserAgent    = 22

	// ICoreWebView2Profile8
	profDelete = 24

	// event arguments
	navStartGetURI        = 3 // ICoreWebView2NavigationStartingEventArgs
	navStartPutCancel     = 8
	navStartGetID         = 9
	navDoneIsSuccess      = 3 // ICoreWebView2NavigationCompletedEventArgs
	navDoneErrorStatus    = 4
	navDoneGetID          = 5
	newWinGetURI          = 3 // ICoreWebView2NewWindowRequestedEventArgs
	newWinPutHandled      = 6
	permGetKind           = 4 // ICoreWebView2PermissionRequestedEventArgs
	permPutState          = 7
	dlStartGetOperation   = 3 // ICoreWebView2DownloadStartingEventArgs
	dlStartPutCancel      = 5
	dlStartGetResultPath  = 6
	dlStartPutResultPath  = 7
	dlStartPutHandled     = 9
	procFailedGetKind     = 3 // ICoreWebView2ProcessFailedEventArgs
	dlAddBytesReceived    = 3 // ICoreWebView2DownloadOperation
	dlRemoveBytesReceived = 4
	dlAddStateChanged     = 7
	dlRemoveStateChanged  = 8
	dlGetURI              = 9
	dlGetTotalBytes       = 12
	dlGetBytesReceived    = 13
	dlGetResultPath       = 15
	dlGetState            = 16
	dlCancel              = 18
)

// Enumerations.
const (
	permKindMicrophone    = 1 // COREWEBVIEW2_PERMISSION_KIND
	permKindCamera        = 2
	permKindGeolocation   = 3
	permKindNotifications = 4
	permKindClipboardRead = 6
	permKindAutoDownloads = 7
	permKindAutoplay      = 9

	permStateDefault = 0 // COREWEBVIEW2_PERMISSION_STATE
	permStateAllow   = 1
	permStateDeny    = 2

	dlStateInProgress  = 0 // COREWEBVIEW2_DOWNLOAD_STATE
	dlStateInterrupted = 1
	dlStateCompleted   = 2

	procBrowserExited = 0 // COREWEBVIEW2_PROCESS_FAILED_KIND
	procRenderExited  = 1

	webErrorCanceled = 14 // COREWEBVIEW2_WEB_ERROR_STATUS_OPERATION_CANCELED
	webErrorAborted  = 9  // COREWEBVIEW2_WEB_ERROR_STATUS_CONNECTION_ABORTED

	focusProgrammatic = 0 // COREWEBVIEW2_MOVE_FOCUS_REASON
)

// IIDs.
var (
	iidEnvironment10   = win32.GUID{Data1: 0xee0eb9df, Data2: 0x6f12, Data3: 0x46ce, Data4: [8]byte{0xb5, 0x3f, 0x3f, 0x47, 0xb9, 0xc9, 0x28, 0xe0}}
	iidController2     = win32.GUID{Data1: 0xc979903e, Data2: 0xd4ca, Data3: 0x4228, Data4: [8]byte{0x92, 0xeb, 0x47, 0xee, 0x3f, 0xa9, 0x6e, 0xab}}
	iidWebView2_2      = win32.GUID{Data1: 0x9E8F0CF8, Data2: 0xE670, Data3: 0x4B5E, Data4: [8]byte{0xB2, 0xBC, 0x73, 0xE0, 0x61, 0xE3, 0x18, 0x4C}}
	iidWebView2_3      = win32.GUID{Data1: 0xA0D6DF20, Data2: 0x3B92, Data3: 0x416D, Data4: [8]byte{0xAA, 0x0C, 0x43, 0x7A, 0x9C, 0x72, 0x78, 0x57}}
	iidWebView2_4      = win32.GUID{Data1: 0x20d02d59, Data2: 0x6df2, Data3: 0x42dc, Data4: [8]byte{0xbd, 0x06, 0xf9, 0x8a, 0x69, 0x4b, 0x13, 0x02}}
	iidWebView2_13     = win32.GUID{Data1: 0xf75f09a8, Data2: 0x667e, Data3: 0x4983, Data4: [8]byte{0x88, 0xd6, 0xc8, 0x77, 0x3f, 0x31, 0x5e, 0x84}}
	iidSettings2       = win32.GUID{Data1: 0xee9a0f68, Data2: 0xf46c, Data3: 0x4e32, Data4: [8]byte{0xac, 0x23, 0xef, 0x8c, 0xac, 0x22, 0x4d, 0x2a}}
	iidProfile8        = win32.GUID{Data1: 0xfbf70c2f, Data2: 0xeb1f, Data3: 0x4383, Data4: [8]byte{0x85, 0xa0, 0x16, 0x3e, 0x92, 0x04, 0x40, 0x11}}
	iidEnvOptions      = win32.GUID{Data1: 0x2fde08a8, Data2: 0x1e9a, Data3: 0x4766, Data4: [8]byte{0x8c, 0x05, 0x95, 0xa9, 0xce, 0xb9, 0xd1, 0xc5}}
	iidHandlerEnv      = win32.GUID{Data1: 0x4e8a3389, Data2: 0xc9d8, Data3: 0x4bd2, Data4: [8]byte{0xb6, 0xb5, 0x12, 0x4f, 0xee, 0x6c, 0xc1, 0x4d}}
	iidHandlerCtrl     = win32.GUID{Data1: 0x6c4819f3, Data2: 0xc9b7, Data3: 0x4260, Data4: [8]byte{0x81, 0x27, 0xc9, 0xf5, 0xbd, 0xe7, 0xf6, 0x8c}}
	iidHandlerNavStart = win32.GUID{Data1: 0x9adbe429, Data2: 0xf36d, Data3: 0x432b, Data4: [8]byte{0x9d, 0xdc, 0xf8, 0x88, 0x1f, 0xbd, 0x76, 0xe3}}
	iidHandlerContent  = win32.GUID{Data1: 0x364471e7, Data2: 0xf2be, Data3: 0x4910, Data4: [8]byte{0xbd, 0xba, 0xd7, 0x20, 0x77, 0xd5, 0x1c, 0x4b}}
	iidHandlerSource   = win32.GUID{Data1: 0x3c067f9f, Data2: 0x5388, Data3: 0x4772, Data4: [8]byte{0x8b, 0x48, 0x79, 0xf7, 0xef, 0x1a, 0xb3, 0x7c}}
	iidHandlerHistory  = win32.GUID{Data1: 0xc79a420c, Data2: 0xefd9, Data3: 0x4058, Data4: [8]byte{0x92, 0x95, 0x3e, 0x8b, 0x4b, 0xca, 0xb6, 0x45}}
	iidHandlerNavDone  = win32.GUID{Data1: 0xd33a35bf, Data2: 0x1c49, Data3: 0x4f98, Data4: [8]byte{0x93, 0xab, 0x00, 0x6e, 0x05, 0x33, 0xfe, 0x1c}}
	iidHandlerPerm     = win32.GUID{Data1: 0x15e1c6a3, Data2: 0xc72a, Data3: 0x4df3, Data4: [8]byte{0x91, 0xd7, 0xd0, 0x97, 0xfb, 0xec, 0x6b, 0xfd}}
	iidHandlerProc     = win32.GUID{Data1: 0x79e0aea4, Data2: 0x990b, Data3: 0x42d9, Data4: [8]byte{0xa1, 0xd1, 0x0f, 0xcc, 0x2e, 0x5b, 0xc7, 0xf1}}
	iidHandlerNewWin   = win32.GUID{Data1: 0xd4c185fe, Data2: 0xc81c, Data3: 0x4989, Data4: [8]byte{0x97, 0xaf, 0x2d, 0x3f, 0xa7, 0xab, 0x56, 0x51}}
	iidHandlerTitle    = win32.GUID{Data1: 0xf5f2b923, Data2: 0x953e, Data3: 0x4042, Data4: [8]byte{0x9f, 0x95, 0xf3, 0xa1, 0x18, 0xe1, 0xaf, 0xd4}}
	iidHandlerDOM      = win32.GUID{Data1: 0x4bac7e9c, Data2: 0x199e, Data3: 0x49ed, Data4: [8]byte{0x87, 0xed, 0x24, 0x93, 0x03, 0xac, 0xf0, 0x19}}
	iidHandlerDLStart  = win32.GUID{Data1: 0xefedc989, Data2: 0xc396, Data3: 0x41ca, Data4: [8]byte{0x83, 0xf7, 0x07, 0xf8, 0x45, 0xa5, 0x57, 0x24}}
	iidHandlerBytes    = win32.GUID{Data1: 0x828e8ab6, Data2: 0xd94c, Data3: 0x4264, Data4: [8]byte{0x9c, 0xef, 0x52, 0x17, 0x17, 0x0d, 0x62, 0x51}}
	iidHandlerDLState  = win32.GUID{Data1: 0x81336594, Data2: 0x7ede, Data3: 0x4ba9, Data4: [8]byte{0xbf, 0x71, 0xac, 0xf0, 0xa9, 0x5b, 0x58, 0xdd}}
	iidHandlerDevTools = win32.GUID{Data1: 0x5c4889f0, Data2: 0x5ef6, Data3: 0x4c5a, Data4: [8]byte{0x95, 0x2c, 0xd8, 0xf1, 0xb9, 0x2d, 0x05, 0x74}} // ICoreWebView2CallDevToolsProtocolMethodCompletedHandler (tests)
	iidHandlerSuspend  = win32.GUID{Data1: 0x00f206a7, Data2: 0x9d17, Data3: 0x4605, Data4: [8]byte{0x91, 0xf6, 0x4e, 0x8e, 0x4d, 0xe1, 0x92, 0xe3}}
)

// The handlers' events: the low byte of a handler object's word, the view's
// or the download's ID above it.
const (
	hEnv = iota + 1
	hCtrl
	hNavStart
	hContent
	hSource
	hHistory
	hNavDone
	hPerm
	hProc
	hNewWin
	hTitle
	hDOM
	hDLStart
	hBytes
	hDLState
	hSuspend
)

const runtimeGUID = `{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`

// targetVersion is what the options report as TargetCompatibleBrowserVersion:
// the oldest runtime WebView2 supports, so that no installed runtime is
// refused for being older than an SDK we do not have.
const targetVersion = "86.0.616.0"

var (
	user32   = win32.LoadSystem("user32.dll")
	kernel32 = win32.LoadSystem("kernel32.dll")
	ole32    = win32.LoadSystem("ole32.dll")
	advapi32 = win32.LoadSystem("advapi32.dll")
	gdi32    = win32.LoadSystem("gdi32.dll")

	pRegisterClassEx = win32.Proc(user32, "RegisterClassExW")
	pCreateWindowEx  = win32.Proc(user32, "CreateWindowExW")
	pDestroyWindow   = win32.Proc(user32, "DestroyWindow")
	pDefWindowProc   = win32.Proc(user32, "DefWindowProcW")
	pGetClientRect   = win32.Proc(user32, "GetClientRect")
	pIsWindowVisible = win32.Proc(user32, "IsWindowVisible")
	pGetDpiForWindow = win32.Proc(user32, "GetDpiForWindow")
	pSetWindowRgn    = win32.Proc(user32, "SetWindowRgn")
	pGetModuleHandle = win32.Proc(kernel32, "GetModuleHandleW")
	pLoadLibraryEx   = win32.Proc(kernel32, "LoadLibraryExW")
	pCoTaskMemFree   = win32.Proc(ole32, "CoTaskMemFree")
	pCoTaskMemAlloc  = win32.Proc(ole32, "CoTaskMemAlloc")
	pRegGetValue     = win32.Proc(advapi32, "RegGetValueW")
	pCreateRoundRgn  = win32.Proc(gdi32, "CreateRoundRectRgn")
)

// rt is the runtime and the process's environment (main thread).
var rt struct {
	loaded   bool
	err      error
	dll      string  // EmbeddedBrowserWebView.dll's path
	kind     uintptr // runtimeType: 0 the installed runtime, 1 a fixed folder (the policy)
	create   uintptr // CreateWebViewEnvironmentWithOptionsInternal
	udf      string  // the user data folder
	handlers *com.Kind
	options  *com.Kind
	class    *uint16 // the host window class
	ring     *platform.Ring

	envState int // 0 none, 1 creating, 2 ready, 3 failed
	envErr   error
	env      uintptr // ICoreWebView2Environment10 (+1)

	views   map[uint32]*view
	byHWND  map[uintptr]*view
	next    uint32
	nextDL  uint32
	private uint32 // non-persistent views' profiles made
	lastDL  int64  // the time of the last DownloadProgress posted, ns

	// loadTime is how long the first New took to find and load the runtime.
	loadTime time.Duration
}

// testHost, set by tests, hosts the view's window in place of
// platform.Window.NewHost.
var testHost func(w *platform.Window, hwnd uintptr) (platform.HostID, error)

// view is the Windows backend of a View.
type view struct {
	id   uint32
	opt  *Options
	w    *platform.Window
	host platform.HostID
	hwnd uintptr // our child window, the controller's parent

	ctrl    uintptr // ICoreWebView2Controller (+1)
	wv      uintptr // ICoreWebView2_4 (+1)
	wv3     uintptr // ICoreWebView2_3 (+1), 0 if the runtime has none
	profile string  // the controller's profile name
	private bool    // InPrivate, a profile of its own

	st       State
	pending  string // a URL to load when the controller is ready
	blockedU string
	navID    uint64 // the navigation in progress
	blockID  uint64 // the last navigation refused by the scheme list
	dls      []*download
	failed   bool // the browser process ended: Reload makes a new controller
	closed   bool
	visible  bool
	suspend  bool // a TrySuspend is wanted (hidden)

	tokens [16]int64 // event registration tokens, by handler event

	// out parameters: heap words the runtime writes
	outPtr  uintptr
	outI32  int32
	outI64  int64
	outU64  uint64
	scratch []byte
}

// download is one of a view's downloads.
type download struct {
	Download
	op       uintptr // ICoreWebView2DownloadOperation (+1)
	tokBytes int64
	tokState int64
	ended    bool
}

// comh calls method idx of obj, keeping the memory its pointer arguments point
// to alive through the call (Go strings converted for the call, out words).
//
//go:uintptrescapes
func comh(obj, idx uintptr, args ...uintptr) int32 { return com.HR(com.Call(obj, idx, args...)) }

// call is a Win32 call with comh's rule.
//
//go:uintptrescapes
func call(fn uintptr, args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(fn, args...)
	return r
}

func wide(s string) *uint16 {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		p, _ = syscall.UTF16PtrFromString(strings.ReplaceAll(s, "\x00", ""))
	}
	return p
}

// takeString reads and frees a string the runtime allocated (CoTaskMemAlloc).
func takeString(p uintptr) string {
	if p == 0 {
		return ""
	}
	n := 0
	for *(*uint16)(unsafe.Add(win32.Ptr(p), 2*n)) != 0 {
		n++
	}
	s := syscall.UTF16ToString(unsafe.Slice((*uint16)(win32.Ptr(p)), n))
	call(pCoTaskMemFree, p)
	return s
}

// coString is s in CoTaskMemAlloc memory, as getters return strings.
func coString(s string) uintptr {
	u, _ := syscall.UTF16FromString(s)
	p := call(pCoTaskMemAlloc, uintptr(2*len(u)))
	if p != 0 {
		copy(unsafe.Slice((*uint16)(win32.Ptr(p)), len(u)), u)
	}
	return p
}

// ---- The loader (D65) ----

var regBuf [1024]uint16

// regString reads a REG_SZ value; root is HKLM or HKCU.
func regString(root uintptr, key, value string) (string, bool) {
	n := uint32(len(regBuf) * 2)
	r := call(pRegGetValue, root, uintptr(unsafe.Pointer(wide(key))), uintptr(unsafe.Pointer(wide(value))),
		0x2, 0, uintptr(unsafe.Pointer(&regBuf[0])), uintptr(unsafe.Pointer(&n))) // RRF_RT_REG_SZ
	if r != 0 || n < 2 {
		return "", false
	}
	return syscall.UTF16ToString(regBuf[:n/2]), true
}

const hklm, hkcu = 0x80000002, 0x80000001

// policy reads a WebView2 policy for this executable (HKLM, then HKCU):
// …\Policies\Microsoft\Edge\WebView2\<name>, the value named for the
// executable's file name, or "*".
func policy(name string) string {
	exe, _ := os.Executable()
	base := filepath.Base(exe)
	for _, root := range []uintptr{hklm, hkcu} {
		for _, v := range []string{base, "*"} {
			if s, ok := regString(root, `SOFTWARE\Policies\Microsoft\Edge\WebView2\`+name, v); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// archDir is the folder of the runtime's client DLL for this process.
func archDir() string {
	switch runtime.GOARCH {
	case "arm64":
		return "arm64"
	case "386":
		return "x86"
	}
	return "x64"
}

// findRuntime returns the client DLL and the runtime type: the
// BrowserExecutableFolder policy's runtime (a fixed folder), else the
// Evergreen runtime per machine (EdgeUpdate is a 32-bit program: its keys are
// under WOW6432Node), then per user. The registry's EBWebView value is the
// version folder.
func findRuntime() (dll string, kind uintptr, err error) {
	if f := policy("BrowserExecutableFolder"); f != "" {
		dll = filepath.Join(f, "EBWebView", archDir(), "EmbeddedBrowserWebView.dll")
		if _, err := os.Stat(dll); err == nil {
			return dll, 1, nil
		}
		return "", 0, fmt.Errorf("%w: the BrowserExecutableFolder policy names %s, which has no runtime", ErrUnavailable, f)
	}
	for _, k := range []struct {
		root uintptr
		key  string
	}{
		{hklm, `SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\ClientState\` + runtimeGUID},
		{hklm, `SOFTWARE\Microsoft\EdgeUpdate\ClientState\` + runtimeGUID},
		{hkcu, `SOFTWARE\Microsoft\EdgeUpdate\ClientState\` + runtimeGUID},
	} {
		folder, ok := regString(k.root, k.key, "EBWebView")
		if !ok || folder == "" {
			continue
		}
		dll = filepath.Join(folder, "EBWebView", archDir(), "EmbeddedBrowserWebView.dll")
		if _, err := os.Stat(dll); err == nil {
			return dll, 0, nil
		}
	}
	return "", 0, fmt.Errorf("%w (WebView2 Evergreen; %s)", ErrUnavailable, RuntimeURL)
}

// userDataFolder is where the environment keeps its data: Options.DataDir,
// the UserDataFolder policy, or WebView2 in the cache folder of the
// executable's name (app/paths).
func userDataFolder(o *Options) (string, error) {
	if p := policy("UserDataFolder"); p != "" {
		return p, nil
	}
	if o.DataDir != "" {
		return o.DataDir, nil
	}
	exe, _ := os.Executable()
	id := strings.TrimSuffix(filepath.Base(exe), filepath.Ext(exe))
	if !paths.ValidID(id) {
		id = "gonativeui"
	}
	d, err := paths.Cache(id)
	if err != nil {
		return "", fmt.Errorf("%w: no folder for its data: %v", ErrRuntime, err)
	}
	return filepath.Join(d, "WebView2"), nil
}

// load finds and loads the runtime and makes the COM kinds and the window
// class, once (main thread).
func load(o *Options) error {
	if rt.loaded {
		return rt.err
	}
	rt.loaded = true
	t0 := time.Now()
	dll, kind, err := findRuntime()
	if err != nil {
		rt.err = err
		return err
	}
	h := call(pLoadLibraryEx, uintptr(unsafe.Pointer(wide(dll))), 0, 0x8) // LOAD_WITH_ALTERED_SEARCH_PATH
	if h == 0 {
		rt.err = fmt.Errorf("%w: cannot load %s", ErrRuntime, filepath.Base(dll))
		return rt.err
	}
	rt.create = win32.Proc(h, "CreateWebViewEnvironmentWithOptionsInternal")
	if rt.create == 0 {
		rt.err = fmt.Errorf("%w: the runtime has no CreateWebViewEnvironmentWithOptionsInternal (D65)", ErrRuntime)
		return rt.err
	}
	rt.dll, rt.kind = dll, kind
	if rt.udf, err = userDataFolder(o); err != nil {
		rt.err = err
		return err
	}
	rt.handlers = com.NewKind("webview2-handlers", 1024, []win32.GUID{
		iidHandlerEnv, iidHandlerCtrl, iidHandlerNavStart, iidHandlerContent, iidHandlerSource, iidHandlerHistory,
		iidHandlerNavDone, iidHandlerPerm, iidHandlerProc, iidHandlerNewWin, iidHandlerTitle, iidHandlerDOM,
		iidHandlerDLStart, iidHandlerBytes, iidHandlerDLState, iidHandlerSuspend, iidHandlerDevTools,
	}, []com.Method{{N: 2, Fn: invoke}})
	rt.options = newOptionsKind()
	rt.class = wide("GNUIWebView2Host")
	wc := &wndClass{proc: syscall.NewCallback(hostProc), name: rt.class}
	wc.inst = call(pGetModuleHandle, 0)
	wc.size = uint32(unsafe.Sizeof(*wc))
	if call(pRegisterClassEx, uintptr(unsafe.Pointer(wc))) == 0 {
		rt.err = fmt.Errorf("%w: cannot register the host window class", ErrRuntime)
		return rt.err
	}
	rt.ring = platform.NewRing(platform.SourceWebView, 256, 0)
	rt.views, rt.byHWND = map[uint32]*view{}, map[uintptr]*view{}
	rt.loadTime = time.Since(t0)
	return nil
}

// wndClass is WNDCLASSEXW.
type wndClass struct {
	size, style            uint32
	proc                   uintptr
	clsExtra, wndExtra     int32
	inst, icon, cur, brush uintptr
	menu, name             *uint16
	iconSm                 uintptr
}

var ePointer = uintptr(0x80004003) // E_POINTER

// newOptionsKind is ICoreWebView2EnvironmentOptions as the SDK's
// CoreWebView2EnvironmentOptions answers it with nothing set (strings NULL,
// S_OK: no extra browser arguments, the user's language), except
// TargetCompatibleBrowserVersion. The setters accept and drop.
func newOptionsKind() *com.Kind {
	str := func(v string) com.Method {
		return com.Method{N: 1, Fn: func(_ *com.Object, a com.Args) uintptr {
			if a[0] == 0 {
				return ePointer
			}
			out := uintptr(0)
			if v != "" {
				out = coString(v)
			}
			*(*uintptr)(win32.Ptr(a[0])) = out
			return 0
		}}
	}
	put := com.Method{N: 1, Fn: func(*com.Object, com.Args) uintptr { return 0 }}
	return com.NewKind("webview2-options", 2, []win32.GUID{iidEnvOptions}, []com.Method{
		str(""), put, // AdditionalBrowserArguments
		str(""), put, // Language
		str(targetVersion), put, // TargetCompatibleBrowserVersion
		{N: 1, Fn: func(_ *com.Object, a com.Args) uintptr { // AllowSingleSignOnUsingOSPrimaryAccount
			if a[0] == 0 {
				return ePointer
			}
			*(*int32)(win32.Ptr(a[0])) = 0
			return 0
		}}, put,
	})
}

// handler is a handler object for event ev of id (a view or a download),
// holding one reference, the caller's: the caller passes it to the runtime and
// releases it.
func handler(id uint32, ev uint8) (*com.Object, error) {
	o, _, err := rt.handlers.New(uintptr(id)<<8 | uintptr(ev))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRuntime, err)
	}
	return o, nil
}

// ---- The environment ----

// ensureEnv starts making the environment, once; views waiting for it get
// their controllers when it arrives.
func ensureEnv() {
	if rt.envState != 0 {
		return
	}
	rt.envState = 1
	h, err := handler(0, hEnv)
	if err != nil {
		envFailed(err)
		return
	}
	opts, _, err := rt.options.New(0)
	if err != nil {
		h.Release()
		envFailed(fmt.Errorf("%w: %v", ErrRuntime, err))
		return
	}
	if err := os.MkdirAll(rt.udf, 0o700); err != nil {
		h.Release()
		opts.Release()
		envFailed(fmt.Errorf("%w: %v", ErrRuntime, err))
		return
	}
	hr := int32(call(rt.create, 1, rt.kind, uintptr(unsafe.Pointer(wide(rt.udf))), opts.Ptr(), h.Ptr()))
	h.Release()
	opts.Release()
	if hr < 0 {
		envFailed(fmt.Errorf("%w: CreateWebViewEnvironmentWithOptionsInternal 0x%08X", ErrRuntime, uint32(hr)))
	}
}

func envFailed(err error) {
	rt.envState, rt.envErr = 3, err
	for _, v := range rt.views {
		if v.ctrl == 0 && !v.closed {
			v.failed = true
			v.post(ProcessFailed, 0, 0)
		}
	}
}

// envDone is the environment's completion: the environment (QueryInterface
// for Environment10, which controller options need), then every waiting
// view's controller.
func envDone(hr int32, env uintptr) {
	if hr < 0 || env == 0 {
		envFailed(fmt.Errorf("%w: the environment failed: 0x%08X", ErrRuntime, uint32(hr)))
		return
	}
	e10 := qi(env, &iidEnvironment10)
	if e10 == 0 {
		envFailed(fmt.Errorf("%w: the runtime is too old (no ICoreWebView2Environment10)", ErrRuntime))
		return
	}
	rt.env, rt.envState = e10, 2
	for _, v := range rt.views {
		if v.ctrl == 0 && !v.closed {
			v.createController()
		}
	}
}

// ---- A view ----

func osNew(w *platform.Window, o *Options) (backend, platform.HostID, uint32, error) {
	if err := load(o); err != nil {
		return nil, 0, 0, err
	}
	parent := w.Native().HWND
	if parent == 0 {
		return nil, 0, 0, ErrNoWindow
	}
	// WS_CHILD | WS_CLIPCHILDREN | WS_CLIPSIBLINGS, hidden until the window
	// places it
	hwnd := call(pCreateWindowEx, 0, uintptr(unsafe.Pointer(rt.class)), 0, 0x40000000|0x02000000|0x04000000,
		0, 0, 1, 1, parent, 0, call(pGetModuleHandle, 0), 0)
	if hwnd == 0 {
		return nil, 0, 0, fmt.Errorf("%w: cannot create the view's window", ErrRuntime)
	}
	rt.next++
	v := &view{id: rt.next, opt: o, w: w, hwnd: hwnd, profile: o.Profile}
	if o.Profile == "" {
		rt.private++
		v.private = true
		v.profile = fmt.Sprintf("gnui-private-%d-%d", os.Getpid(), rt.private)
	}
	var host platform.HostID
	var err error
	if testHost != nil {
		host, err = testHost(w, hwnd)
	} else {
		host, err = w.NewHost(platform.HostView, platform.HostOptions{View: hwnd, Focusable: true})
	}
	if err != nil {
		call(pDestroyWindow, hwnd)
		return nil, 0, 0, err
	}
	v.host = host
	rt.views[v.id] = v
	rt.byHWND[hwnd] = v
	switch rt.envState {
	case 0:
		ensureEnv()
	case 2:
		v.createController()
	case 3:
		delete(rt.views, v.id)
		delete(rt.byHWND, hwnd)
		w.ReleaseHost(host)
		call(pDestroyWindow, hwnd)
		return nil, 0, 0, rt.envErr
	}
	return v, host, v.id, nil
}

func (v *view) post(kind EventKind, perm Permission, dl uint32) {
	rt.ring.OfferID(uint64(v.id), int64(kind)|int64(perm)<<8, int64(dl), nil)
}

// createController asks the environment for the view's controller, with its
// profile (InPrivate for a non-persistent session).
func (v *view) createController() {
	v.outPtr = 0
	if hr := comh(rt.env, envCreateControllerOptions, uintptr(unsafe.Pointer(&v.outPtr))); hr < 0 || v.outPtr == 0 {
		v.fail()
		return
	}
	opts := v.outPtr
	defer win32.Release(opts)
	comh(opts, copPutProfileName, uintptr(unsafe.Pointer(wide(v.profile))))
	if v.private {
		comh(opts, copPutInPrivate, 1)
	}
	h, err := handler(v.id, hCtrl)
	if err != nil {
		v.fail()
		return
	}
	hr := comh(rt.env, envCreateControllerWithOptions, v.hwnd, opts, h.Ptr())
	h.Release()
	if hr < 0 {
		v.fail()
	}
}

// fail reports a view whose engine could not be made or ended.
func (v *view) fail() {
	v.failed = true
	v.st.Loading = false
	v.post(ProcessFailed, 0, 0)
}

// ready is the controller's completion: the web view, its settings and
// handlers, its bounds, then the URL Navigate kept.
func (v *view) ready(hr int32, ctrl uintptr) {
	if v.closed {
		if ctrl != 0 {
			com.Call(ctrl, ctrlClose)
		}
		return
	}
	if hr < 0 || ctrl == 0 {
		v.fail()
		return
	}
	win32.AddRef(ctrl)
	v.ctrl = ctrl
	v.outPtr = 0
	if comh(ctrl, ctrlGetCoreWebView2, uintptr(unsafe.Pointer(&v.outPtr))) < 0 || v.outPtr == 0 {
		v.fail()
		return
	}
	core := v.outPtr
	wv4 := qi(core, &iidWebView2_4)
	if wv4 == 0 {
		win32.Release(core)
		v.fail()
		return
	}
	v.wv = wv4
	v.wv3 = qi(core, &iidWebView2_3)
	wv2 := qi(core, &iidWebView2_2)
	win32.Release(core)
	v.failed = false

	// settings: the context menu, the inspector, the user agent
	v.outPtr = 0
	if comh(wv4, wvGetSettings, uintptr(unsafe.Pointer(&v.outPtr))) >= 0 && v.outPtr != 0 {
		set := v.outPtr
		comh(set, setPutContextMenus, b2u(!v.opt.NoContextMenu))
		comh(set, setPutDevTools, b2u(v.opt.Inspectable))
		if v.opt.ApplicationName != "" {
			if s2 := qi(set, &iidSettings2); s2 != 0 {
				v.outPtr = 0
				if comh(s2, setGetUserAgent, uintptr(unsafe.Pointer(&v.outPtr))) >= 0 {
					ua := takeString(v.outPtr) + " " + v.opt.ApplicationName
					comh(s2, setPutUserAgent, uintptr(unsafe.Pointer(wide(ua))))
				}
				win32.Release(s2)
			}
		}
		win32.Release(set)
	}

	// handlers: each object is held by the runtime until its remove_
	add := func(obj, slot uintptr, ev uint8) {
		h, err := handler(v.id, ev)
		if err != nil {
			return
		}
		comh(obj, slot, h.Ptr(), uintptr(unsafe.Pointer(&v.tokens[ev])))
		h.Release()
	}
	add(wv4, wvAddNavStarting, hNavStart)
	add(wv4, wvAddContentLoading, hContent)
	add(wv4, wvAddSourceChanged, hSource)
	add(wv4, wvAddHistoryChanged, hHistory)
	add(wv4, wvAddNavCompleted, hNavDone)
	add(wv4, wvAddPermission, hPerm)
	add(wv4, wvAddProcessFailed, hProc)
	add(wv4, wvAddNewWindow, hNewWin)
	add(wv4, wvAddTitleChanged, hTitle)
	add(wv4, wvAddDownloadStarting, hDLStart)
	if wv2 != 0 {
		add(wv2, wvAddDOMContentLoaded, hDOM)
		win32.Release(wv2)
	}

	v.resize()
	v.visible = false
	v.showIfVisible()
	if u := v.pending; u != "" {
		v.pending = ""
		comh(v.wv, wvNavigate, uintptr(unsafe.Pointer(wide(u))))
	}
}

func b2u(b bool) uintptr {
	if b {
		return 1
	}
	return 0
}

var clientRect [4]int32

// resize gives the controller the window's client area, and rounds the window
// (Options.CornerRadius) at its DPI.
func (v *view) resize() {
	call(pGetClientRect, v.hwnd, uintptr(unsafe.Pointer(&clientRect)))
	if v.ctrl != 0 {
		putBounds(v.ctrl, &clientRect)
	}
	if v.opt.CornerRadius > 0 {
		dpi := uint32(96)
		if pGetDpiForWindow != 0 {
			if d := uint32(call(pGetDpiForWindow, v.hwnd)); d != 0 {
				dpi = d
			}
		}
		d := int32(2*v.opt.CornerRadius*float32(dpi)/96 + 0.5)
		rgn := call(pCreateRoundRgn, 0, 0, uintptr(clientRect[2]+1), uintptr(clientRect[3]+1), uintptr(d), uintptr(d))
		if rgn != 0 {
			call(pSetWindowRgn, v.hwnd, rgn, 1) // the system owns the region now
		}
	}
}

// putBounds is ICoreWebView2Controller::put_Bounds(RECT), a 16-byte struct by
// value: in two registers on arm64 (AAPCS64), by reference to a copy on x64.
func putBounds(ctrl uintptr, r *[4]int32) {
	if runtime.GOARCH == "arm64" {
		lo := uintptr(uint32(r[0])) | uintptr(uint32(r[1]))<<32
		hi := uintptr(uint32(r[2])) | uintptr(uint32(r[3]))<<32
		com.Call(ctrl, ctrlPutBounds, lo, hi)
		return
	}
	comh(ctrl, ctrlPutBounds, uintptr(unsafe.Pointer(r)))
}

// showIfVisible follows the window's visibility: a shown view is visible (and
// resumed), a hidden one invisible and suspended (TrySuspend).
func (v *view) showIfVisible() {
	if vis := call(pIsWindowVisible, v.hwnd) != 0; v.ctrl != 0 && vis != v.visible {
		v.showState(vis)
	}
}

// hostProc is the view's window procedure: it keeps the controller's bounds,
// visibility, position and focus with the window.
func hostProc(hwnd, msg, wp, lp uintptr) uintptr {
	v := rt.byHWND[hwnd]
	switch msg {
	case 0x0005: // WM_SIZE
		if v != nil {
			v.resize()
		}
	case 0x0003, 0x0047: // WM_MOVE, WM_WINDOWPOSCHANGED
		if v != nil {
			if v.ctrl != 0 {
				com.Call(v.ctrl, ctrlNotifyParentMoved)
			}
			if msg == 0x0047 {
				v.showIfVisible()
			}
		}
	case 0x0018: // WM_SHOWWINDOW: wParam is the state the window is about to have
		if v != nil && v.ctrl != 0 && (wp != 0) != v.visible {
			v.showState(wp != 0)
		}
	case 0x0007: // WM_SETFOCUS: into the page
		if v != nil && v.ctrl != 0 {
			com.Call(v.ctrl, ctrlMoveFocus, focusProgrammatic)
			return 0
		}
	case 0x0014: // WM_ERASEBKGND: the page paints it
		return 1
	}
	r, _, _ := syscall.SyscallN(pDefWindowProc, hwnd, msg, wp, lp)
	return r
}

// showState makes the controller visible (and resumed) or invisible and
// suspended (TrySuspend, which needs it invisible).
func (v *view) showState(vis bool) {
	v.visible = vis
	com.Call(v.ctrl, ctrlPutIsVisible, b2u(vis))
	if vis {
		if v.wv3 != 0 {
			com.Call(v.wv3, wvResume)
		}
		return
	}
	if v.wv3 != 0 {
		if h, err := handler(v.id, hSuspend); err == nil {
			comh(v.wv3, wvTrySuspend, h.Ptr())
			h.Release()
		}
	}
}

// ---- The handlers ----

// invoke is every handler's Invoke(sender, args): the event is the low byte of
// the object's word, the view or download above it.
func invoke(o *com.Object, a com.Args) uintptr {
	w := o.Value()
	id, ev := uint32(w>>8), uint8(w)
	switch ev {
	case hEnv:
		envDone(int32(a[0]), a[1])
		return 0
	case hBytes, hDLState:
		if v, d := downloadByID(id); d != nil {
			v.downloadEvent(d, ev)
		}
		return 0
	}
	v := rt.views[id]
	if v == nil || v.closed {
		if ev == hCtrl && a[1] != 0 {
			com.Call(a[1], ctrlClose)
		}
		return 0
	}
	args := a[1]
	switch ev {
	case hCtrl:
		v.ready(int32(a[0]), a[1])
	case hNavStart:
		v.navStarting(args)
	case hContent:
		v.progress(0.4)
	case hDOM:
		v.progress(0.7)
	case hSource:
		v.outPtr = 0
		if comh(v.wv, wvGetSource, uintptr(unsafe.Pointer(&v.outPtr))) >= 0 {
			if s := takeString(v.outPtr); s != v.st.URL {
				v.st.URL = s
				v.post(URLChanged, 0, 0)
			}
		}
	case hHistory:
		v.outI32 = 0
		comh(v.wv, wvGetCanGoBack, uintptr(unsafe.Pointer(&v.outI32)))
		b := v.outI32 != 0
		v.outI32 = 0
		comh(v.wv, wvGetCanGoForward, uintptr(unsafe.Pointer(&v.outI32)))
		f := v.outI32 != 0
		if b != v.st.CanGoBack || f != v.st.CanGoForward {
			v.st.CanGoBack, v.st.CanGoForward = b, f
			v.post(URLChanged, 0, 0)
		}
	case hNavDone:
		v.navCompleted(args)
	case hTitle:
		v.outPtr = 0
		if comh(v.wv, wvGetDocumentTitle, uintptr(unsafe.Pointer(&v.outPtr))) >= 0 {
			if s := takeString(v.outPtr); s != v.st.Title {
				v.st.Title = s
				v.post(TitleChanged, 0, 0)
			}
		}
	case hNewWin:
		v.newWindow(args)
	case hPerm:
		v.permission(args)
	case hProc:
		v.outI32 = -1
		comh(args, procFailedGetKind, uintptr(unsafe.Pointer(&v.outI32)))
		switch v.outI32 {
		case procBrowserExited:
			v.dropController()
			v.fail()
		case procRenderExited:
			v.st.Loading = false
			v.post(ProcessFailed, 0, 0)
		}
	case hDLStart:
		v.downloadStarting(args)
	}
	return 0
}

// allowedURL reports whether u's scheme is in the allow-list.
func (v *view) allowedURL(u string) bool {
	scheme, _, ok := strings.Cut(u, ":")
	if !ok {
		return false
	}
	for _, s := range v.opt.Schemes {
		if strings.EqualFold(s, scheme) {
			return true
		}
	}
	return false
}

func (v *view) block(u string, kind EventKind) {
	v.blockedU = u
	v.post(kind, 0, 0)
}

func (v *view) navStarting(args uintptr) {
	v.outPtr = 0
	comh(args, navStartGetURI, uintptr(unsafe.Pointer(&v.outPtr)))
	u := takeString(v.outPtr)
	v.outU64 = 0
	comh(args, navStartGetID, uintptr(unsafe.Pointer(&v.outU64)))
	if !v.allowedURL(u) {
		comh(args, navStartPutCancel, 1)
		v.blockID = v.outU64
		v.block(u, NavigationBlocked)
		return
	}
	v.navID = v.outU64
	v.st.Loading, v.st.Progress = true, 0.1
	v.post(LoadStarted, 0, 0)
	v.post(Progress, 0, 0)
}

func (v *view) progress(p float32) {
	if v.st.Loading && p > v.st.Progress {
		v.st.Progress = p
		v.post(Progress, 0, 0)
	}
}

// navCompleted ends the navigation in progress: LoadFinished, or LoadFailed
// unless it was cancelled or aborted: a navigation stopped or replaced, and
// one that became a download, which completes as CONNECTION_ABORTED before
// DownloadStarting arrives (seen on both runners).
func (v *view) navCompleted(args uintptr) {
	v.outU64 = 0
	comh(args, navDoneGetID, uintptr(unsafe.Pointer(&v.outU64)))
	id := v.outU64
	if id == v.blockID && v.blockID != 0 || id != v.navID {
		return
	}
	v.outI32 = 0
	comh(args, navDoneIsSuccess, uintptr(unsafe.Pointer(&v.outI32)))
	ok := v.outI32 != 0
	v.outI32 = 0
	comh(args, navDoneErrorStatus, uintptr(unsafe.Pointer(&v.outI32)))
	v.st.Loading = false
	switch {
	case ok:
		v.st.Progress = 1
		v.post(Progress, 0, 0)
		v.post(LoadFinished, 0, 0)
	case v.outI32 != webErrorCanceled && v.outI32 != webErrorAborted:
		v.post(LoadFailed, 0, 0)
	}
}

// newWindow handles a page's new window: never a second window; loaded in
// place, or refused.
func (v *view) newWindow(args uintptr) {
	v.outPtr = 0
	comh(args, newWinGetURI, uintptr(unsafe.Pointer(&v.outPtr)))
	u := takeString(v.outPtr)
	comh(args, newWinPutHandled, 1)
	switch {
	case v.opt.NewWindows == NewWindowRefuse:
		v.block(u, NewWindowRefused)
	case !v.allowedURL(u):
		v.block(u, NavigationBlocked)
	default:
		comh(v.wv, wvNavigate, uintptr(unsafe.Pointer(wide(u))))
	}
}

// permission grants what Options.Allow names and denies the rest; autoplay
// and several downloads at once keep the runtime's default.
func (v *view) permission(args uintptr) {
	v.outI32 = 0
	comh(args, permGetKind, uintptr(unsafe.Pointer(&v.outI32)))
	var want Permission
	switch v.outI32 {
	case permKindMicrophone:
		want = PermMicrophone
	case permKindCamera:
		want = PermCamera
	case permKindGeolocation:
		want = PermLocation
	case permKindNotifications:
		want = PermNotifications
	case permKindClipboardRead:
		want = PermClipboardRead
	case permKindAutoplay, permKindAutoDownloads:
		comh(args, permPutState, permStateDefault)
		return
	}
	if want != 0 && v.opt.Allow&want == want {
		comh(args, permPutState, permStateAllow)
		return
	}
	comh(args, permPutState, permStateDeny)
	if want != 0 {
		v.post(PermissionDenied, want, 0)
	}
}

// downloadStarting puts the download in Options.DownloadDir under the
// server's name made unique, with the runtime's download UI off, or cancels
// it when there is no download folder.
func (v *view) downloadStarting(args uintptr) {
	if v.opt.DownloadDir == "" {
		comh(args, dlStartPutCancel, 1)
		return
	}
	v.outPtr = 0
	comh(args, dlStartGetResultPath, uintptr(unsafe.Pointer(&v.outPtr)))
	suggested := filepath.Base(takeString(v.outPtr))
	path, err := uniquePath(v.opt.DownloadDir, suggested, v.taken)
	if err != nil {
		comh(args, dlStartPutCancel, 1)
		return
	}
	v.outPtr = 0
	comh(args, dlStartGetOperation, uintptr(unsafe.Pointer(&v.outPtr)))
	op := v.outPtr
	if op == 0 {
		comh(args, dlStartPutCancel, 1)
		return
	}
	comh(args, dlStartPutResultPath, uintptr(unsafe.Pointer(wide(path))))
	comh(args, dlStartPutHandled, 1)
	rt.nextDL++
	d := &download{Download: Download{ID: rt.nextDL, Path: path, Total: -1, State: DownloadRunning}, op: op}
	v.outPtr = 0
	if comh(op, dlGetURI, uintptr(unsafe.Pointer(&v.outPtr))) >= 0 {
		d.URL = takeString(v.outPtr)
	}
	if h, err := handler(d.ID, hBytes); err == nil {
		comh(op, dlAddBytesReceived, h.Ptr(), uintptr(unsafe.Pointer(&d.tokBytes)))
		h.Release()
	}
	if h, err := handler(d.ID, hDLState); err == nil {
		comh(op, dlAddStateChanged, h.Ptr(), uintptr(unsafe.Pointer(&d.tokState)))
		h.Release()
	}
	v.dls = append(v.dls, d)
	v.post(DownloadStarted, 0, d.ID)
}

// taken reports whether one of the view's downloads is written to p.
func (v *view) taken(p string) bool {
	for _, d := range v.dls {
		if d.Path == p {
			return true
		}
	}
	return false
}

func downloadByID(id uint32) (*view, *download) {
	for _, v := range rt.views {
		for _, d := range v.dls {
			if d.ID == id {
				return v, d
			}
		}
	}
	return nil, nil
}

// counts reads a running download's byte counts from its operation.
func (v *view) counts(d *download) {
	v.outI64 = 0
	if comh(d.op, dlGetBytesReceived, uintptr(unsafe.Pointer(&v.outI64))) >= 0 {
		d.Received = v.outI64
	}
	v.outI64 = -1
	if comh(d.op, dlGetTotalBytes, uintptr(unsafe.Pointer(&v.outI64))) >= 0 && v.outI64 >= 0 {
		d.Total = v.outI64
	}
}

func (v *view) downloadEvent(d *download, ev uint8) {
	if d.ended {
		return
	}
	switch ev {
	case hBytes:
		now := time.Now().UnixNano()
		if now-rt.lastDL >= int64(50*time.Millisecond) {
			rt.lastDL = now
			v.post(DownloadProgress, 0, d.ID)
		}
	case hDLState:
		v.outI32 = -1
		comh(d.op, dlGetState, uintptr(unsafe.Pointer(&v.outI32)))
		switch v.outI32 {
		case dlStateCompleted:
			v.endDownload(d, DownloadDone)
		case dlStateInterrupted:
			v.endDownload(d, DownloadFailed)
		}
	}
}

// endDownload ends download d (once): its last counts, a failed download's
// file removed, its handlers removed, DownloadEnded posted.
func (v *view) endDownload(d *download, s DownloadState) {
	if d.ended {
		return
	}
	d.ended = true
	v.counts(d)
	if s == DownloadDone {
		if fi, err := os.Stat(d.Path); err == nil {
			d.Received = fi.Size()
		}
		if d.Total < 0 {
			d.Total = d.Received
		}
	}
	if s == DownloadFailed && d.Path != "" {
		os.Remove(d.Path)
	}
	d.State = s
	com.Call(d.op, dlRemoveBytesReceived, uintptr(d.tokBytes))
	com.Call(d.op, dlRemoveStateChanged, uintptr(d.tokState))
	v.post(DownloadEnded, 0, d.ID)
}

// ---- The backend's methods ----

func (v *view) navigate(u string) error {
	if v.ctrl == 0 {
		v.pending = u
		if v.failed && rt.envState == 2 {
			v.failed = false
			v.createController()
		}
		return nil
	}
	if hr := comh(v.wv, wvNavigate, uintptr(unsafe.Pointer(wide(u)))); hr < 0 {
		return fmt.Errorf("%w: %q (0x%08X)", ErrBadURL, u, uint32(hr))
	}
	return nil
}

// reload reloads the page; a view whose browser process ended gets a new
// controller, which loads the last URL.
func (v *view) reload() {
	if v.ctrl == 0 {
		if v.failed && rt.envState == 2 {
			v.failed = false
			if v.pending == "" {
				v.pending = v.st.URL
			}
			v.createController()
		}
		return
	}
	com.Call(v.wv, wvReload)
}

func (v *view) stop() {
	if v.wv != 0 {
		com.Call(v.wv, wvStop)
	}
}

func (v *view) back() {
	if v.wv != 0 {
		com.Call(v.wv, wvGoBack)
	}
}

func (v *view) forward() {
	if v.wv != 0 {
		com.Call(v.wv, wvGoForward)
	}
}

func (v *view) state() State    { return v.st }
func (v *view) blocked() string { return v.blockedU }

func (v *view) download(id uint32) (Download, bool) {
	for _, d := range v.dls {
		if d.ID == id {
			if !d.ended {
				v.counts(d)
			}
			return d.Download, true
		}
	}
	return Download{}, false
}

func (v *view) cancelDownload(id uint32) {
	for _, d := range v.dls {
		if d.ID == id && !d.ended {
			com.Call(d.op, dlCancel)
			v.endDownload(d, DownloadFailed)
		}
	}
}

// dropController removes the handlers and closes and releases the controller
// and its web view.
func (v *view) dropController() {
	if v.wv != 0 {
		for _, r := range [...]struct {
			slot uintptr
			ev   uint8
		}{
			{wvRemoveNavStarting, hNavStart}, {wvRemoveContentLoading, hContent}, {wvRemoveSourceChanged, hSource},
			{wvRemoveHistoryChanged, hHistory}, {wvRemoveNavCompleted, hNavDone}, {wvRemovePermission, hPerm},
			{wvRemoveProcessFailed, hProc}, {wvRemoveNewWindow, hNewWin}, {wvRemoveTitleChanged, hTitle},
			{wvRemoveDownloadStart, hDLStart},
		} {
			if v.tokens[r.ev] != 0 {
				com.Call(v.wv, r.slot, uintptr(v.tokens[r.ev]))
			}
		}
		if v.tokens[hDOM] != 0 {
			if wv2 := qi(v.wv, &iidWebView2_2); wv2 != 0 {
				com.Call(wv2, wvRemoveDOMContent, uintptr(v.tokens[hDOM]))
				win32.Release(wv2)
			}
		}
		v.tokens = [16]int64{}
		win32.Release(v.wv3)
		win32.Release(v.wv)
		v.wv, v.wv3 = 0, 0
	}
	if v.ctrl != 0 {
		com.Call(v.ctrl, ctrlClose)
		win32.Release(v.ctrl)
		v.ctrl = 0
	}
}

// close cancels the view's running downloads, closes its controller (a
// non-persistent view's profile is marked for deletion first), and releases
// its host and window.
func (v *view) close() {
	for _, d := range v.dls {
		if !d.ended {
			com.Call(d.op, dlCancel)
			v.endDownload(d, DownloadFailed)
		}
		win32.Release(d.op)
	}
	v.dls = nil
	v.closed = true
	if v.private && v.wv != 0 {
		v.deleteProfile()
	}
	v.dropController()
	v.w.ReleaseHost(v.host)
	delete(rt.byHWND, v.hwnd)
	call(pDestroyWindow, v.hwnd)
	delete(rt.views, v.id)
}

// deleteProfile marks the view's own InPrivate profile for deletion
// (ICoreWebView2Profile8.Delete, runtime 1.0.2365): the runtime removes its
// folder once no view uses it.
func (v *view) deleteProfile() {
	wv13 := qi(v.wv, &iidWebView2_13)
	if wv13 == 0 {
		return
	}
	defer win32.Release(wv13)
	v.outPtr = 0
	if comh(wv13, wvGetProfile, uintptr(unsafe.Pointer(&v.outPtr))) < 0 || v.outPtr == 0 {
		return
	}
	prof := v.outPtr
	defer win32.Release(prof)
	if p8 := qi(prof, &iidProfile8); p8 != 0 {
		com.Call(p8, profDelete)
		win32.Release(p8)
	}
}

// qiOut receives qi's interface (main thread).
var qiOut uintptr

// qi is obj's interface iid (+1), or 0.
func qi(obj uintptr, iid *win32.GUID) uintptr {
	qiOut = 0
	if obj == 0 || win32.QueryInterface(obj, iid, &qiOut) < 0 {
		return 0
	}
	return qiOut
}
