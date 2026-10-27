//go:build windows

package main

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"gonativeui/internal/win32"
)

// WinHTTP, from the Windows SDK's winhttp.h. Every call is synchronous (no
// WINHTTP_FLAG_ASYNC), made from a goroutine; a blocked call blocks its
// thread only.

var (
	winhttp  = win32.LoadSystem("winhttp.dll")
	kernel32 = win32.LoadSystem("kernel32.dll")

	pOpen               = win32.Proc(winhttp, "WinHttpOpen")
	pConnect            = win32.Proc(winhttp, "WinHttpConnect")
	pOpenRequest        = win32.Proc(winhttp, "WinHttpOpenRequest")
	pAddHeaders         = win32.Proc(winhttp, "WinHttpAddRequestHeaders")
	pSendRequest        = win32.Proc(winhttp, "WinHttpSendRequest")
	pWriteData          = win32.Proc(winhttp, "WinHttpWriteData")
	pReceiveResponse    = win32.Proc(winhttp, "WinHttpReceiveResponse")
	pQueryHeaders       = win32.Proc(winhttp, "WinHttpQueryHeaders")
	pReadData           = win32.Proc(winhttp, "WinHttpReadData")
	pQueryDataAvailable = win32.Proc(winhttp, "WinHttpQueryDataAvailable")
	pCloseHandle        = win32.Proc(winhttp, "WinHttpCloseHandle")
	pSetOption          = win32.Proc(winhttp, "WinHttpSetOption")
	pSetTimeouts        = win32.Proc(winhttp, "WinHttpSetTimeouts")
	pIEProxy            = win32.Proc(winhttp, "WinHttpGetIEProxyConfigForCurrentUser")
	pDefaultProxy       = win32.Proc(winhttp, "WinHttpGetDefaultProxyConfiguration")
	pWSCompleteUpgrade  = win32.Proc(winhttp, "WinHttpWebSocketCompleteUpgrade")
	pWSSend             = win32.Proc(winhttp, "WinHttpWebSocketSend")
	pWSReceive          = win32.Proc(winhttp, "WinHttpWebSocketReceive")
	pWSClose            = win32.Proc(winhttp, "WinHttpWebSocketClose")
	pWSQueryCloseStatus = win32.Proc(winhttp, "WinHttpWebSocketQueryCloseStatus")
	pGlobalFree         = win32.Proc(kernel32, "GlobalFree")
)

// winhttp.h constants.
const (
	accessDefaultProxy   = 0 // WINHTTP_ACCESS_TYPE_DEFAULT_PROXY
	accessAutomaticProxy = 4 // WINHTTP_ACCESS_TYPE_AUTOMATIC_PROXY (Windows 8.1+)

	addReqFlagAdd = 0x20000000 // WINHTTP_ADDREQ_FLAG_ADD

	queryStatusCode = 19         // WINHTTP_QUERY_STATUS_CODE
	queryFlagNumber = 0x20000000 // WINHTTP_QUERY_FLAG_NUMBER

	optionUpgradeToWebSocket = 114 // WINHTTP_OPTION_UPGRADE_TO_WEB_SOCKET

	// WINHTTP_WEB_SOCKET_BUFFER_TYPE.
	wsBinaryMessage  = 0
	wsBinaryFragment = 1
	wsUTF8Message    = 2
	wsUTF8Fragment   = 3
	wsClose          = 4

	errOperationCancelled = 12017 // ERROR_WINHTTP_OPERATION_CANCELLED
	errInvalidParameter   = 87    // ERROR_INVALID_PARAMETER
)

// call is a WinHTTP call whose pointer arguments (converted in the call
// expression) are moved to the heap; it returns the last error too.
//
//go:uintptrescapes
func call(fn uintptr, args ...uintptr) (uintptr, syscall.Errno) {
	r, _, e := syscall.SyscallN(fn, args...)
	return r, e
}

func wide(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }

func wptr(s string) uintptr { return uintptr(unsafe.Pointer(wide(s))) }

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

// winErr is a failed BOOL call's error.
type winErr struct {
	fn string
	no syscall.Errno
}

func (e *winErr) Error() string { return fmt.Sprintf("%s: %d %v", e.fn, uint32(e.no), e.no) }

func errno(err error) uint32 {
	var we *winErr
	if errors.As(err, &we) {
		return uint32(we.no)
	}
	return 0
}

// boolCall runs a BOOL-returning function and turns FALSE into a winErr.
func boolCall(name string, fn uintptr, args ...uintptr) error {
	r, e := call(fn, args...)
	if uint32(r) == 0 {
		return &winErr{name, e}
	}
	return nil
}

// dwordCall runs a function that returns a Win32 error code (the
// WebSocket functions) and turns non-zero into a winErr.
func dwordCall(name string, fn uintptr, args ...uintptr) error {
	r, _ := call(fn, args...)
	if uint32(r) != 0 {
		return &winErr{name, syscall.Errno(uint32(r))}
	}
	return nil
}

// ieProxyConfig is WINHTTP_CURRENT_USER_IE_PROXY_CONFIG (winhttp.h): BOOL
// fAutoDetect, then three LPWSTRs freed with GlobalFree.
type ieProxyConfig struct {
	autoDetect    int32
	autoConfigURL uintptr
	proxy, bypass uintptr
}

// proxyInfo is WINHTTP_PROXY_INFO (winhttp.h): DWORD dwAccessType, LPWSTR
// lpszProxy, LPWSTR lpszProxyBypass, freed with GlobalFree.
type proxyInfo struct {
	accessType    uint32
	proxy, bypass uintptr
}

// session opens a WinHTTP session with the automatic proxy, or the default
// one where the system predates it.
func session() (h uintptr, kind string, err error) {
	h, e := call(pOpen, wptr("gnui-probe-p8/1"), accessAutomaticProxy, 0, 0, 0)
	if h != 0 {
		return h, "automatic", nil
	}
	if e != errInvalidParameter {
		return 0, "", &winErr{"WinHttpOpen(automatic)", e}
	}
	h, e = call(pOpen, wptr("gnui-probe-p8/1"), accessDefaultProxy, 0, 0, 0)
	if h == 0 {
		return 0, "", &winErr{"WinHttpOpen(default)", e}
	}
	return h, "default", nil
}

// request opens a connection and a request handle to 127.0.0.1:port.
func request(ses uintptr, port int, verb, path string) (conn, req uintptr, err error) {
	conn, e := call(pConnect, ses, wptr("127.0.0.1"), uintptr(port), 0)
	if conn == 0 {
		return 0, 0, &winErr{"WinHttpConnect", e}
	}
	req, e = call(pOpenRequest, conn, wptr(verb), wptr(path), 0, 0, 0, 0)
	if req == 0 {
		call(pCloseHandle, conn)
		return 0, 0, &winErr{"WinHttpOpenRequest", e}
	}
	return conn, req, nil
}

func closeAll(hs ...uintptr) {
	for _, h := range hs {
		if h != 0 {
			call(pCloseHandle, h)
		}
	}
}

func addHeader(req uintptr, line string) error {
	return boolCall("WinHttpAddRequestHeaders", pAddHeaders, req, wptr(line), uintptr(^uint32(0)), addReqFlagAdd)
}

// send starts a request with a body of total bytes (0 for none or for a
// chunked body), written afterwards with write.
func send(req uintptr, total uint32) error {
	return boolCall("WinHttpSendRequest", pSendRequest, req, 0, 0, 0, 0, uintptr(total), 0)
}

func write(req uintptr, p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	var n uint32
	err := boolCall("WinHttpWriteData", pWriteData, req, uintptr(unsafe.Pointer(&p[0])), uintptr(len(p)), uintptr(unsafe.Pointer(&n)))
	return int(n), err
}

func receive(req uintptr) error {
	return boolCall("WinHttpReceiveResponse", pReceiveResponse, req, 0)
}

func status(req uintptr) (int, error) {
	var code, size uint32 = 0, 4
	err := boolCall("WinHttpQueryHeaders", pQueryHeaders, req, queryStatusCode|queryFlagNumber, 0,
		uintptr(unsafe.Pointer(&code)), uintptr(unsafe.Pointer(&size)), 0)
	return int(code), err
}

// header returns one response header, by name for the ones the probe reads
// (WINHTTP_QUERY_* indices, winhttp.h), or "" when absent.
func header(req uintptr, name string) string {
	idx := map[string]uintptr{"Transfer-Encoding": 63, "Content-Encoding": 29}[name] // WINHTTP_QUERY_TRANSFER_ENCODING, _CONTENT_ENCODING
	var buf [256]uint16
	size := uint32(len(buf) * 2)
	if boolCall("WinHttpQueryHeaders", pQueryHeaders, req, idx, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0) != nil {
		return ""
	}
	return syscall.UTF16ToString(buf[:size/2])
}

// read reads up to len(p) bytes of the response; 0 is the end.
func read(req uintptr, p []byte) (int, error) {
	var n uint32
	err := boolCall("WinHttpReadData", pReadData, req, uintptr(unsafe.Pointer(&p[0])), uintptr(len(p)), uintptr(unsafe.Pointer(&n)))
	return int(n), err
}

// available waits until some of the response has arrived (or the end) and
// returns how many bytes can be read without blocking; 0 is the end.
// Synchronous WinHttpReadData waits until it has filled its buffer or the
// response ended, so a streamed body is read as available then read(n).
func available(req uintptr) (int, error) {
	var n uint32
	err := boolCall("WinHttpQueryDataAvailable", pQueryDataAvailable, req, uintptr(unsafe.Pointer(&n)))
	return int(n), err
}

// readAll reads the whole response body.
func readAll(req uintptr) ([]byte, error) {
	var out []byte
	buf := make([]byte, 64<<10)
	for {
		n, err := read(req, buf)
		out = append(out, buf[:n]...)
		if err != nil || n == 0 {
			return out, err
		}
	}
}
