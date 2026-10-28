//go:build windows

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"gonativeui/internal/com"
	"gonativeui/internal/win32"
)

func init() { runtime.LockOSThread() } // the main goroutine stays on the main thread: the STA

// Interface ids, WebView2.h (SDK 1.0.4314.35, checked 2026-10-27).
var (
	iidEnvCompleted  = win32.GUID{Data1: 0x4E8A3389, Data2: 0xC9D8, Data3: 0x4BD2, Data4: [8]byte{0xB6, 0xB5, 0x12, 0x4F, 0xEE, 0x6C, 0xC1, 0x4D}} // ICoreWebView2CreateCoreWebView2EnvironmentCompletedHandler
	iidCtrlCompleted = win32.GUID{Data1: 0x6C4819F3, Data2: 0xC9B7, Data3: 0x4260, Data4: [8]byte{0x81, 0x27, 0xC9, 0xF5, 0xBD, 0xE7, 0xF6, 0x8C}} // ICoreWebView2CreateCoreWebView2ControllerCompletedHandler
	iidNavCompleted  = win32.GUID{Data1: 0xD33A35BF, Data2: 0x1C49, Data3: 0x4F98, Data4: [8]byte{0x93, 0xAB, 0x00, 0x6E, 0x05, 0x33, 0xFE, 0x1C}} // ICoreWebView2NavigationCompletedEventHandler
	iidEnvOptions    = win32.GUID{Data1: 0x2FDE08A8, Data2: 0x1E9A, Data3: 0x4766, Data4: [8]byte{0x8C, 0x05, 0x95, 0xA9, 0xCE, 0xB9, 0xD1, 0xC5}} // ICoreWebView2EnvironmentOptions
)

// Vtable slots, counting IUnknown's 0-2, read from the C vtables
// (I…Vtbl structs) of WebView2.h in the Microsoft.Web.WebView2 NuGet
// package 1.0.4314.35 (2026-10-27): ICoreWebView2Environment at line 44467,
// ICoreWebView2Controller 39309, ICoreWebView2 3239,
// ICoreWebView2NavigationCompletedEventArgs and the handlers' Invoke at 3.
// The handlers' Invoke and ICoreWebView2EnvironmentOptions' eight accessors
// (get/put AdditionalBrowserArguments, Language,
// TargetCompatibleBrowserVersion, AllowSingleSignOnUsingOSPrimaryAccount,
// slots 3-10) are the methods of the kinds below, in that order.
const (
	envCreateController = 3 // ICoreWebView2Environment::CreateCoreWebView2Controller(HWND, handler)
	envBrowserVersion   = 5 // ICoreWebView2Environment::get_BrowserVersionString(LPWSTR*)

	ctrlPutIsVisible = 4  // ICoreWebView2Controller::put_IsVisible(BOOL)
	ctrlPutBounds    = 6  // ICoreWebView2Controller::put_Bounds(RECT): RECT by value
	ctrlClose        = 24 // ICoreWebView2Controller::Close()
	ctrlCoreWebView2 = 25 // ICoreWebView2Controller::get_CoreWebView2(ICoreWebView2**)

	wvNavigate              = 5  // ICoreWebView2::Navigate(LPCWSTR)
	wvAddNavigationComplete = 15 // ICoreWebView2::add_NavigationCompleted(handler, EventRegistrationToken*)
	wvDocumentTitle         = 48 // ICoreWebView2::get_DocumentTitle(LPWSTR*)

	navArgsIsSuccess   = 3 // ICoreWebView2NavigationCompletedEventArgs::get_IsSuccess(BOOL*)
	navArgsErrorStatus = 4 // ICoreWebView2NavigationCompletedEventArgs::get_WebErrorStatus(COREWEBVIEW2_WEB_ERROR_STATUS*)
)

var (
	user32   = win32.LoadSystem("user32.dll")
	kernel32 = win32.LoadSystem("kernel32.dll")
	ole32    = win32.LoadSystem("ole32.dll")
	advapi32 = win32.LoadSystem("advapi32.dll")

	pRegisterClassEx = win32.Proc(user32, "RegisterClassExW")
	pCreateWindowEx  = win32.Proc(user32, "CreateWindowExW")
	pDefWindowProc   = win32.Proc(user32, "DefWindowProcW")
	pShowWindow      = win32.Proc(user32, "ShowWindow")
	pPeekMessage     = win32.Proc(user32, "PeekMessageW")
	pTranslate       = win32.Proc(user32, "TranslateMessage")
	pDispatch        = win32.Proc(user32, "DispatchMessageW")
	pMsgWait         = win32.Proc(user32, "MsgWaitForMultipleObjectsEx")
	pGetClientRect   = win32.Proc(user32, "GetClientRect")
	pDestroyWindow   = win32.Proc(user32, "DestroyWindow")
	pGetModuleHandle = win32.Proc(kernel32, "GetModuleHandleW")
	pLoadLibraryEx   = win32.Proc(kernel32, "LoadLibraryExW")
	pOleInitialize   = win32.Proc(ole32, "OleInitialize")
	pCoTaskMemFree   = win32.Proc(ole32, "CoTaskMemFree")
	pCoTaskMemAlloc  = win32.Proc(ole32, "CoTaskMemAlloc")
	pRegGetValue     = win32.Proc(advapi32, "RegGetValueW")
)

// call is a Win32 call whose pointer arguments (converted in the call
// expression) are moved to the heap; it returns the last error too.
//
//go:uintptrescapes
func call(fn uintptr, args ...uintptr) (uintptr, syscall.Errno) {
	r, _, e := syscall.SyscallN(fn, args...)
	return r, e
}

// comh is com.Call with call's heap rule, for out parameters.
//
//go:uintptrescapes
func comh(obj, idx uintptr, args ...uintptr) int32 { return com.HR(com.Call(obj, idx, args...)) }

func wide(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }

// wstr reads a NUL-terminated UTF-16 string the system owns.
func wstr(p uintptr) string {
	if p == 0 {
		return ""
	}
	n := 0
	for *(*uint16)(unsafe.Add(win32.Ptr(p), 2*n)) != 0 {
		n++
	}
	return syscall.UTF16ToString(unsafe.Slice((*uint16)(win32.Ptr(p)), n))
}

func hrs(hr int32) string { return fmt.Sprintf("0x%08X", uint32(hr)) }

type step struct {
	Name   string  `json:"name"`
	HR     string  `json:"hr,omitempty"`
	Ms     float64 `json:"ms"`
	OK     bool    `json:"ok"`
	Detail string  `json:"detail,omitempty"`
}

type candidate struct {
	Key       string   `json:"key"`
	EBWebView string   `json:"ebwebview,omitempty"` // the version folder
	Version   string   `json:"version,omitempty"`   // Clients\{…} pv
	FolderVer string   `json:"folder_version,omitempty"`
	DLL       string   `json:"dll,omitempty"`
	DLLFound  bool     `json:"dll_found"`
	Arches    []string `json:"ebwebview_arches,omitempty"` // what EBWebView\ holds
	Err       string   `json:"err,omitempty"`
}

type runtimeInfo struct {
	Found      bool        `json:"found"`
	Folder     string      `json:"folder,omitempty"`
	Version    string      `json:"version,omitempty"`
	DLL        string      `json:"dll,omitempty"`
	Candidates []candidate `json:"candidates"`
}

type attempt struct {
	Options    string `json:"options,omitempty"` // "go_object" or "null"
	Steps      []step `json:"steps"`
	EnvVersion string `json:"env_version,omitempty"`
	NavSuccess bool   `json:"nav_success"`
	WebError   int32  `json:"web_error_status"`
	Title      string `json:"title,omitempty"`
	Worked     bool   `json:"worked"`
}

type report struct {
	Arch    string      `json:"arch"`
	Runtime runtimeInfo `json:"runtime"`
	attempt
	// NullOptions is a second try with options NULL, made only when the
	// first (with our options object) did not work.
	NullOptions *attempt       `json:"null_options,omitempty"`
	OptionCalls map[string]int `json:"option_calls"` // our options object's methods the runtime called
	Loader      *attempt       `json:"loader,omitempty"`
	TimedOut    bool           `json:"timed_out"`
	TotalMs     float64        `json:"total_ms"`
}

var (
	mu    sync.Mutex
	res   = report{Arch: runtime.GOARCH, OptionCalls: map[string]int{}}
	start = time.Now()
)

func emit() {
	mu.Lock()
	defer mu.Unlock()
	res.TotalMs = ms(start)
	b, _ := json.MarshalIndent(&res, "", "  ")
	os.Stdout.Write(append(b, '\n'))
}

func ms(t time.Time) float64 { return float64(time.Since(t).Microseconds()) / 1000 }

func (a *attempt) add(name string, hr int32, t time.Time, ok bool, detail string) {
	mu.Lock()
	a.Steps = append(a.Steps, step{Name: name, HR: hrs(hr), Ms: ms(t), OK: ok, Detail: detail})
	mu.Unlock()
}

// State the handlers set; everything runs on the STA thread.
var (
	envDone, ctrlDone, navDone bool
	envHR, ctrlHR              int32
	envObj, ctrlObj            uintptr
	navOK                      int32
	navStatus                  int32
	outStr                     uintptr
	outObj                     uintptr
	token                      int64
	msg                        [6]uintptr // MSG: 48 bytes on 64-bit
	clientRect                 [4]int32
	regBuf                     [2048]uint16
	regLen                     uint32
)

var (
	envKind = com.NewKind("p3-env", 4, []win32.GUID{iidEnvCompleted}, []com.Method{{N: 2, Fn: func(_ *com.Object, a com.Args) uintptr {
		envHR, envObj, envDone = int32(a[0]), a[1], true
		if envObj != 0 {
			win32.Com(envObj, 1) // AddRef: we keep it
		}
		return 0
	}}})
	ctrlKind = com.NewKind("p3-ctrl", 2, []win32.GUID{iidCtrlCompleted}, []com.Method{{N: 2, Fn: func(_ *com.Object, a com.Args) uintptr {
		ctrlHR, ctrlObj, ctrlDone = int32(a[0]), a[1], true
		if ctrlObj != 0 {
			win32.Com(ctrlObj, 1)
		}
		return 0
	}}})
	navKind = com.NewKind("p3-nav", 2, []win32.GUID{iidNavCompleted}, []com.Method{{N: 2, Fn: func(_ *com.Object, a com.Args) uintptr {
		args := a[1]
		comh(args, navArgsIsSuccess, uintptr(unsafe.Pointer(&navOK)))
		comh(args, navArgsErrorStatus, uintptr(unsafe.Pointer(&navStatus)))
		navDone = true
		return 0
	}}})
)

// targetVersion is what our options object reports as
// TargetCompatibleBrowserVersion: the oldest runtime WebView2 supports, so
// no installed runtime is refused for being older than our SDK.
const targetVersion = "86.0.616.0"

var ePointer = uintptr(0x80004003) // E_POINTER (win32.EPointer) as a raw method result

// coString is s in CoTaskMemAlloc memory, as COM getters return strings.
func coString(s string) uintptr {
	u, _ := syscall.UTF16FromString(s)
	p, _ := call(pCoTaskMemAlloc, uintptr(2*len(u)))
	if p != 0 {
		copy(unsafe.Slice((*uint16)(win32.Ptr(p)), len(u)), u)
	}
	return p
}

// optionsKind is ICoreWebView2EnvironmentOptions as the SDK's
// CoreWebView2EnvironmentOptions answers it with nothing set (strings NULL,
// S_OK), except TargetCompatibleBrowserVersion. Setters accept and drop.
var optionsKind = func() *com.Kind {
	str := func(name, v string) com.Method {
		return com.Method{N: 1, Fn: func(_ *com.Object, a com.Args) uintptr {
			mu.Lock()
			res.OptionCalls[name]++
			mu.Unlock()
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
	put := func(name string) com.Method {
		return com.Method{N: 1, Fn: func(_ *com.Object, _ com.Args) uintptr {
			mu.Lock()
			res.OptionCalls[name]++
			mu.Unlock()
			return 0
		}}
	}
	return com.NewKind("p3-options", 2, []win32.GUID{iidEnvOptions}, []com.Method{
		str("get_AdditionalBrowserArguments", ""), put("put_AdditionalBrowserArguments"),
		str("get_Language", ""), put("put_Language"),
		str("get_TargetCompatibleBrowserVersion", targetVersion), put("put_TargetCompatibleBrowserVersion"),
		{N: 1, Fn: func(_ *com.Object, a com.Args) uintptr {
			mu.Lock()
			res.OptionCalls["get_AllowSingleSignOnUsingOSPrimaryAccount"]++
			mu.Unlock()
			if a[0] == 0 {
				return ePointer
			}
			*(*int32)(win32.Ptr(a[0])) = 0
			return 0
		}},
		put("put_AllowSingleSignOnUsingOSPrimaryAccount"),
	})
}()

// pump runs the message loop until *done or d passes.
func pump(done *bool, d time.Duration) bool {
	end := time.Now().Add(d)
	for {
		for {
			r, _ := call(pPeekMessage, uintptr(unsafe.Pointer(&msg)), 0, 0, 0, 1) // PM_REMOVE
			if r == 0 {
				break
			}
			call(pTranslate, uintptr(unsafe.Pointer(&msg)))
			call(pDispatch, uintptr(unsafe.Pointer(&msg)))
		}
		if *done {
			return true
		}
		left := time.Until(end)
		if left <= 0 {
			return false
		}
		call(pMsgWait, 0, 0, uintptr(min(left, 50*time.Millisecond)/time.Millisecond), 0x04FF, 0x4) // QS_ALLINPUT, MWMO_INPUTAVAILABLE
	}
}

const runtimeGUID = `{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`

func regString(root uintptr, key, value string) (string, error) {
	regLen = uint32(len(regBuf) * 2)
	r, _ := call(pRegGetValue, root, uintptr(unsafe.Pointer(wide(key))), uintptr(unsafe.Pointer(wide(value))),
		0x2, 0, uintptr(unsafe.Pointer(&regBuf[0])), uintptr(unsafe.Pointer(&regLen))) // RRF_RT_REG_SZ
	if r != 0 {
		return "", syscall.Errno(r)
	}
	return syscall.UTF16ToString(regBuf[:regLen/2]), nil
}

// findRuntime reads the Evergreen runtime's location as WebView2Loader does:
// per machine (HKLM through the 32-bit view, KEY_WOW64_32KEY, which is
// SOFTWARE\WOW6432Node on 64-bit Windows: EdgeUpdate is a 32-bit program),
// then per user (HKCU). ClientState\{runtime}'s EBWebView value is the
// version folder (…\EdgeWebView\Application\<version>); the client DLL is
// <version folder>\EBWebView\<x64|arm64>\EmbeddedBrowserWebView.dll. The
// native HKLM view is read too, for the record.
func findRuntime() {
	const hklm, hkcu = 0x80000002, 0x80000001
	roots := []struct {
		name string
		root uintptr
		base string
	}{
		{`HKLM\SOFTWARE\WOW6432Node`, hklm, `SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\`},
		{`HKLM\SOFTWARE`, hklm, `SOFTWARE\Microsoft\EdgeUpdate\`},
		{`HKCU\SOFTWARE`, hkcu, `SOFTWARE\Microsoft\EdgeUpdate\`},
	}
	arch := "x64"
	if runtime.GOARCH == "arm64" {
		arch = "arm64"
	}
	for _, r := range roots {
		c := candidate{Key: r.name}
		folder, err := regString(r.root, r.base+`ClientState\`+runtimeGUID, "EBWebView")
		if err != nil {
			c.Err = "EBWebView: " + err.Error()
		}
		c.EBWebView = folder
		if v, err := regString(r.root, r.base+`Clients\`+runtimeGUID, "pv"); err == nil {
			c.Version = v
		}
		if folder != "" {
			c.FolderVer = filepath.Base(folder)
			c.DLL = filepath.Join(folder, "EBWebView", arch, "EmbeddedBrowserWebView.dll")
			_, serr := os.Stat(c.DLL)
			c.DLLFound = serr == nil
			if serr != nil {
				c.Err = serr.Error()
			}
			if ents, err := os.ReadDir(filepath.Join(folder, "EBWebView")); err == nil {
				for _, e := range ents {
					if e.IsDir() {
						c.Arches = append(c.Arches, e.Name())
					}
				}
			}
		}
		res.Runtime.Candidates = append(res.Runtime.Candidates, c)
		if c.DLLFound && !res.Runtime.Found {
			res.Runtime.Found, res.Runtime.Folder, res.Runtime.DLL = true, folder, c.DLL
			res.Runtime.Version = c.FolderVer
		}
	}
}

func openWindow() uintptr {
	hinst, _ := call(pGetModuleHandle, 0)
	proc := syscall.NewCallback(func(h, m, w, l uintptr) uintptr { r, _ := call(pDefWindowProc, h, m, w, l); return r })
	cls := wide("gnui-probe-p3")
	wc := &struct {
		size, style            uint32
		proc                   uintptr
		clsExtra, wndExtra     int32
		inst, icon, cur, brush uintptr
		menu, name             *uint16
		iconSm                 uintptr
	}{style: 0x3, proc: proc, inst: hinst, name: cls} // WNDCLASSEXW; CS_HREDRAW|CS_VREDRAW
	wc.size = uint32(unsafe.Sizeof(*wc))
	call(pRegisterClassEx, uintptr(unsafe.Pointer(wc)))
	h, _ := call(pCreateWindowEx, 0, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(wide("probe-p3"))),
		0x00CF0000, 100, 100, 800, 600, 0, 0, hinst, 0) // WS_OVERLAPPEDWINDOW, not visible
	return h
}

func putBounds(ctrl uintptr, r *[4]int32) int32 {
	if runtime.GOARCH == "arm64" { // a 16-byte struct in two registers (AAPCS64)
		lo := uintptr(uint32(r[0])) | uintptr(uint32(r[1]))<<32
		hi := uintptr(uint32(r[2])) | uintptr(uint32(r[3]))<<32
		return com.HR(com.Call(ctrl, ctrlPutBounds, lo, hi))
	}
	return comh(ctrl, ctrlPutBounds, uintptr(unsafe.Pointer(r))) // x64: by reference to a copy
}

// waitEnv waits for the environment handler and reports it.
func waitEnv(a *attempt, t time.Time) bool {
	if !pump(&envDone, 30*time.Second) {
		a.add("environment_completed", win32.EFail, t, false, "no completion in 30 s")
		return false
	}
	a.add("environment_completed", envHR, t, envHR == 0 && envObj != 0, "")
	if envHR != 0 || envObj == 0 {
		return false
	}
	if comh(envObj, envBrowserVersion, uintptr(unsafe.Pointer(&outStr))) == 0 {
		mu.Lock()
		a.EnvVersion = wstr(outStr)
		mu.Unlock()
		call(pCoTaskMemFree, outStr)
	}
	return true
}

// browse makes a controller in hwnd, navigates to url and reads the title.
func browse(a *attempt, hwnd uintptr, url string) {
	t := time.Now()
	ctrlDone, ctrlObj = false, 0
	h, _, _ := ctrlKind.New(0)
	hr := com.HR(com.Call(envObj, envCreateController, hwnd, h.Ptr()))
	h.Release()
	a.add("create_controller_call", hr, t, hr == 0, "")
	if hr != 0 {
		return
	}
	if !pump(&ctrlDone, 30*time.Second) {
		a.add("controller_completed", win32.EFail, t, false, "no completion in 30 s")
		return
	}
	a.add("controller_completed", ctrlHR, t, ctrlHR == 0 && ctrlObj != 0, "")
	if ctrlHR != 0 || ctrlObj == 0 {
		return
	}
	defer func() { com.Call(ctrlObj, ctrlClose); win32.Release(ctrlObj) }()

	t = time.Now()
	call(pShowWindow, hwnd, 5) // SW_SHOW: hidden until now
	call(pGetClientRect, hwnd, uintptr(unsafe.Pointer(&clientRect)))
	hr = putBounds(ctrlObj, &clientRect)
	a.add("put_bounds", hr, t, hr == 0, fmt.Sprint(clientRect))
	hr = com.HR(com.Call(ctrlObj, ctrlPutIsVisible, 1))
	a.add("put_is_visible", hr, t, hr == 0, "")

	t = time.Now()
	hr = comh(ctrlObj, ctrlCoreWebView2, uintptr(unsafe.Pointer(&outObj)))
	wv := outObj
	a.add("get_core_webview2", hr, t, hr == 0 && wv != 0, "")
	if hr != 0 || wv == 0 {
		return
	}
	defer win32.Release(wv)

	nav, _, _ := navKind.New(0)
	hr = comh(wv, wvAddNavigationComplete, nav.Ptr(), uintptr(unsafe.Pointer(&token)))
	nav.Release() // the webview holds its own reference
	a.add("add_navigation_completed", hr, t, hr == 0, "")

	t = time.Now()
	navDone = false
	hr = com.HR(com.Call(wv, wvNavigate, uintptr(unsafe.Pointer(wide(url)))))
	a.add("navigate_call", hr, t, hr == 0, url)
	if hr != 0 {
		return
	}
	if !pump(&navDone, 30*time.Second) {
		a.add("navigation_completed", win32.EFail, t, false, "no completion in 30 s")
		return
	}
	a.add("navigation_completed", 0, t, navOK != 0, fmt.Sprintf("web_error_status %d", navStatus))
	t = time.Now()
	hr = comh(wv, wvDocumentTitle, uintptr(unsafe.Pointer(&outStr)))
	title := ""
	if hr == 0 {
		title = wstr(outStr)
		call(pCoTaskMemFree, outStr)
	}
	a.add("get_document_title", hr, t, hr == 0, title)
	mu.Lock()
	a.NavSuccess, a.WebError, a.Title, a.Worked = navOK != 0, navStatus, title, title == "gnui-p3"
	mu.Unlock()
}

// internal calls CreateWebViewEnvironmentWithOptionsInternal(TRUE,
// 0 (the installed Evergreen runtime; 1 is a fixed-version folder),
// userDataFolder, options, handler) and, if the environment arrives, browses.
func internal(a *attempt, fn uintptr, folder string, options, hwnd uintptr, url string) {
	t := time.Now()
	envDone, envObj = false, 0
	h, _, _ := envKind.New(0)
	hr, _ := call(fn, 1, 0, uintptr(unsafe.Pointer(wide(folder))), options, h.Ptr())
	h.Release()
	a.add("create_environment_call", int32(hr), t, int32(hr) == 0, fmt.Sprintf("checkRunningInstance=TRUE runtimeType=0 options=%s", map[bool]string{true: "go_object", false: "NULL"}[options != 0]))
	if int32(hr) == 0 && waitEnv(a, t) {
		browse(a, hwnd, url)
		win32.Release(envObj)
	}
}

func loadDLL(path string) (uintptr, syscall.Errno) {
	return call(pLoadLibraryEx, uintptr(unsafe.Pointer(wide(path))), 0, 0x8) // LOAD_WITH_ALTERED_SEARCH_PATH
}

func errText(r uintptr, e syscall.Errno) string {
	if r != 0 {
		return ""
	}
	return e.Error()
}

func main() {
	timeout := flag.Duration("timeout", 120*time.Second, "give up after this long (exit status 3)")
	loader := flag.String("loader", "", "path of a WebView2Loader.dll to compare with (D65 option 2)")
	flag.Parse()
	time.AfterFunc(*timeout, func() {
		mu.Lock()
		res.TimedOut = true
		mu.Unlock()
		emit()
		os.Exit(3)
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, "probe-p3:", err)
		os.Exit(1)
	}
	go http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<!doctype html><html><head><title>gnui-p3</title></head><body>probe P3</body></html>`)
	}))
	url := "http://" + ln.Addr().String() + "/"

	findRuntime()
	t := time.Now()
	r, _ := call(pOleInitialize, 0)
	res.add("ole_initialize", int32(r), t, int32(r) >= 0, "")
	if int32(r) < 0 {
		emit()
		os.Exit(1)
	}
	t = time.Now()
	hwnd := openWindow()
	res.add("create_window", 0, t, hwnd != 0, "hidden")
	if hwnd == 0 {
		emit()
		os.Exit(1)
	}
	defer call(pDestroyWindow, hwnd)
	data, _ := os.MkdirTemp("", "gnui-p3-*")

	if res.Runtime.Found {
		t = time.Now()
		dll, e := loadDLL(res.Runtime.DLL)
		res.add("load_embedded_browser_dll", 0, t, dll != 0, errText(dll, e))
		fn := win32.Proc(dll, "CreateWebViewEnvironmentWithOptionsInternal")
		res.add("find_export", 0, t, fn != 0, "CreateWebViewEnvironmentWithOptionsInternal")
		if fn != 0 {
			// First with an options object implemented in Go, as the
			// loader passes one; then, only if that did not work, with NULL.
			res.Options = "go_object"
			opts, _, _ := optionsKind.New(0)
			internal(&res.attempt, fn, filepath.Join(data, "ours"), opts.Ptr(), hwnd, url)
			opts.Release()
			if !res.Worked {
				a := &attempt{Options: "null"}
				mu.Lock()
				res.NullOptions = a
				mu.Unlock()
				internal(a, fn, filepath.Join(data, "null"), 0, hwnd, url)
			}
		}
	}

	if *loader != "" {
		a := &attempt{}
		mu.Lock()
		res.Loader = a
		mu.Unlock()
		t = time.Now()
		dll, e := loadDLL(*loader)
		fn := win32.Proc(dll, "CreateCoreWebView2EnvironmentWithOptions")
		a.add("load_loader", 0, t, fn != 0, errText(dll, e))
		if fn != 0 {
			envDone, envObj = false, 0
			h, _, _ := envKind.New(0)
			hr, _ := call(fn, 0, uintptr(unsafe.Pointer(wide(filepath.Join(data, "loader")))), 0, h.Ptr())
			h.Release()
			a.add("create_environment_call", int32(hr), t, int32(hr) == 0, "CreateCoreWebView2EnvironmentWithOptions(NULL, folder, NULL, handler)")
			if int32(hr) == 0 && waitEnv(a, t) {
				browse(a, hwnd, url)
				win32.Release(envObj)
			}
		}
	}
	emit()
	os.RemoveAll(data) // may fail while the browser processes exit; it is a temporary folder
	os.Exit(0)
}
