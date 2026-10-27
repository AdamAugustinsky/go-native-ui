// Package winhttp is the WinHTTP plumbing app/fetch and app/ws share on
// Windows (the phase 8 plan's D73, N1 and N2): winhttp.dll's functions, a
// session with the system's proxy or a named one, and the calls the two
// packages make, every one synchronous. The calls were proven on both CI
// runners by probe P8 (cmd/probe-p8, docs/measurements/phase8-windows.md).
//
// Function signatures, constants and struct layouts are from the Windows
// SDK's winhttp.h. A blocked call blocks its goroutine's thread only; closing
// the handle it works on (CloseHandle, from any goroutine) ends it with
// ErrCancelled. The call helpers allocate nothing: pointer arguments must
// point at memory that stays reachable through the caller for the call.
package winhttp

import (
	"errors"
	"fmt"
	"strconv"
	"syscall"
	"unsafe"

	"gonativeui/internal/win32"
)

var (
	dll = win32.LoadSystem("winhttp.dll")

	pOpen               = win32.Proc(dll, "WinHttpOpen")
	pConnect            = win32.Proc(dll, "WinHttpConnect")
	pOpenRequest        = win32.Proc(dll, "WinHttpOpenRequest")
	pAddHeaders         = win32.Proc(dll, "WinHttpAddRequestHeaders")
	pSendRequest        = win32.Proc(dll, "WinHttpSendRequest")
	pWriteData          = win32.Proc(dll, "WinHttpWriteData")
	pReceiveResponse    = win32.Proc(dll, "WinHttpReceiveResponse")
	pQueryHeaders       = win32.Proc(dll, "WinHttpQueryHeaders")
	pQueryOption        = win32.Proc(dll, "WinHttpQueryOption")
	pReadData           = win32.Proc(dll, "WinHttpReadData")
	pQueryDataAvailable = win32.Proc(dll, "WinHttpQueryDataAvailable")
	pCloseHandle        = win32.Proc(dll, "WinHttpCloseHandle")
	pSetOption          = win32.Proc(dll, "WinHttpSetOption")
	pSetTimeouts        = win32.Proc(dll, "WinHttpSetTimeouts")
	pSetCredentials     = win32.Proc(dll, "WinHttpSetCredentials")
	pQueryAuthSchemes   = win32.Proc(dll, "WinHttpQueryAuthSchemes")
	pWSCompleteUpgrade  = win32.Proc(dll, "WinHttpWebSocketCompleteUpgrade")
	pWSSend             = win32.Proc(dll, "WinHttpWebSocketSend")
	pWSReceive          = win32.Proc(dll, "WinHttpWebSocketReceive")
	pWSShutdown         = win32.Proc(dll, "WinHttpWebSocketShutdown")
	pWSClose            = win32.Proc(dll, "WinHttpWebSocketClose")
	pWSQueryCloseStatus = win32.Proc(dll, "WinHttpWebSocketQueryCloseStatus")
)

// winhttp.h constants.
const (
	accessDefaultProxy   = 0 // WINHTTP_ACCESS_TYPE_DEFAULT_PROXY
	accessNoProxy        = 1 // WINHTTP_ACCESS_TYPE_NO_PROXY
	accessNamedProxy     = 3 // WINHTTP_ACCESS_TYPE_NAMED_PROXY
	accessAutomaticProxy = 4 // WINHTTP_ACCESS_TYPE_AUTOMATIC_PROXY (Windows 8.1+)

	flagSecure        = 0x00800000 // WINHTTP_FLAG_SECURE
	flagEscapeDisable = 0x00000040 // WINHTTP_FLAG_ESCAPE_DISABLE
	flagEscapeQuery   = 0x00000080 // WINHTTP_FLAG_ESCAPE_DISABLE_QUERY

	addReqFlagAdd     = 0x20000000 // WINHTTP_ADDREQ_FLAG_ADD
	addReqFlagReplace = 0x80000000 // WINHTTP_ADDREQ_FLAG_REPLACE

	queryContentLength = 5          // WINHTTP_QUERY_CONTENT_LENGTH
	queryStatusCode    = 19         // WINHTTP_QUERY_STATUS_CODE
	queryRawHeadersCRL = 22         // WINHTTP_QUERY_RAW_HEADERS_CRLF
	queryCustom        = 65535      // WINHTTP_QUERY_CUSTOM
	queryFlagNumber    = 0x20000000 // WINHTTP_QUERY_FLAG_NUMBER
	queryFlagNumber64  = 0x08000000 // WINHTTP_QUERY_FLAG_NUMBER64

	optionURL                = 34     // WINHTTP_OPTION_URL
	optionDisableFeature     = 63     // WINHTTP_OPTION_DISABLE_FEATURE
	optionUpgradeToWebSocket = 114    // WINHTTP_OPTION_UPGRADE_TO_WEB_SOCKET
	optionProxyUsername      = 0x1002 // WINHTTP_OPTION_PROXY_USERNAME
	optionProxyPassword      = 0x1003 // WINHTTP_OPTION_PROXY_PASSWORD

	disableCookies   = 0x1 // WINHTTP_DISABLE_COOKIES
	disableKeepAlive = 0x8 // WINHTTP_DISABLE_KEEP_ALIVE

	authTargetProxy = 1 // WINHTTP_AUTH_TARGET_PROXY
	authSchemeBasic = 1 // WINHTTP_AUTH_SCHEME_BASIC

	errInvalidParameter = 87  // ERROR_INVALID_PARAMETER
	errInsufficient     = 122 // ERROR_INSUFFICIENT_BUFFER
)

// The WinHTTP errors the packages test for.
const (
	ErrnoTimeout       syscall.Errno = 12002 // ERROR_WINHTTP_TIMEOUT
	ErrnoCancelled     syscall.Errno = 12017 // ERROR_WINHTTP_OPERATION_CANCELLED
	ErrnoCannotConnect syscall.Errno = 12029 // ERROR_WINHTTP_CANNOT_CONNECT
	ErrnoAborted       syscall.Errno = 12030 // ERROR_WINHTTP_CONNECTION_ERROR
	ErrnoResend        syscall.Errno = 12032 // ERROR_WINHTTP_RESEND_REQUEST
	ErrnoInvalidHandle syscall.Errno = 6     // ERROR_INVALID_HANDLE
)

// WebSocket buffer types (WINHTTP_WEB_SOCKET_BUFFER_TYPE).
const (
	BinaryMessage  = 0
	BinaryFragment = 1
	UTF8Message    = 2
	UTF8Fragment   = 3
	CloseBuffer    = 4
)

// ErrUnavailable means winhttp.dll or one of its functions is missing.
var ErrUnavailable = errors.New("winhttp: winhttp.dll or its WebSocket API is missing")

// Load reports whether WinHTTP is usable.
func Load() error {
	if pOpen == 0 || pWSCompleteUpgrade == 0 || pWSShutdown == 0 {
		return ErrUnavailable
	}
	return nil
}

// Error is a failed WinHTTP call.
type Error struct {
	Fn    string
	Errno syscall.Errno
}

var names = map[syscall.Errno]string{
	12002: "timed out",
	12005: "invalid URL",
	12007: "name not resolved",
	12017: "cancelled",
	12029: "cannot connect",
	12030: "connection error",
	12032: "resend request",
	12044: "client certificate needed",
	12152: "invalid server response",
	12175: "secure failure (TLS or certificate)",
	12178: "auto proxy service error",
	12180: "auto-detection failed",
	12185: "header not found",
}

func (e *Error) Error() string {
	if n, ok := names[e.Errno]; ok {
		return fmt.Sprintf("%s: %s (WinHTTP %d)", e.Fn, n, uint32(e.Errno))
	}
	return fmt.Sprintf("%s: %v (%d)", e.Fn, e.Errno, uint32(e.Errno))
}

// Unwrap gives the errno.
func (e *Error) Unwrap() error { return e.Errno }

// Is reports whether err is a WinHTTP error with errno no.
func Is(err error, no syscall.Errno) bool {
	var we *Error
	return errors.As(err, &we) && we.Errno == no
}

// call runs fn and returns its result and the thread's last error. The
// memory that pointers converted in its arguments point to escapes to the
// heap and stays alive for the call.
//
//go:uintptrescapes
func call(fn uintptr, args ...uintptr) (uintptr, syscall.Errno) {
	r, _, e := syscall.SyscallN(fn, args...)
	return r, e
}

// boolCall runs a BOOL-returning function and turns FALSE into an Error.
//
//go:uintptrescapes
func boolCall(name string, fn uintptr, args ...uintptr) error {
	if r, e := call(fn, args...); uint32(r) == 0 {
		return &Error{name, e}
	}
	return nil
}

// dwordCall runs a function that returns a Win32 error code (the WebSocket
// functions).
//
//go:uintptrescapes
func dwordCall(name string, fn uintptr, args ...uintptr) error {
	if r, _ := call(fn, args...); uint32(r) != 0 {
		return &Error{name, syscall.Errno(uint32(r))}
	}
	return nil
}

// Wide is a NUL-terminated UTF-16 copy of s (s must hold no NUL).
func Wide(s string) *uint16 {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		return &[]uint16{0}[0]
	}
	return p
}

// Proxy is how a session reaches servers.
type Proxy struct {
	// None connects directly; otherwise Host:Port is a named HTTP proxy,
	// and an empty Host means the system's settings.
	None bool
	Host string
	Port int
	// User and Password answer the proxy's Basic challenge.
	User, Password string
}

// Session opens a WinHTTP session (no WINHTTP_FLAG_ASYNC) with the system's
// proxy settings (automatic proxy, or the default configuration before
// Windows 8.1), no proxy, or a named one, and no timeouts but the caller's.
func Session(agent string, p Proxy) (uintptr, error) {
	a := Wide(agent)
	var h uintptr
	var e syscall.Errno
	switch {
	case p.None:
		h, e = call(pOpen, uintptr(unsafe.Pointer(a)), accessNoProxy, 0, 0, 0)
	case p.Host != "":
		host := p.Host
		for i := 0; i < len(host); i++ {
			if host[i] == ':' { // an IPv6 address
				host = "[" + host + "]"
				break
			}
		}
		// "<-loopback>" removes the loopback addresses from the implicit
		// bypass list: an explicit proxy carries them too, as on macOS and
		// Linux.
		name, bypass := Wide(host+":"+strconv.Itoa(p.Port)), Wide("<-loopback>")
		h, e = call(pOpen, uintptr(unsafe.Pointer(a)), accessNamedProxy, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(bypass)), 0)
	default:
		h, e = call(pOpen, uintptr(unsafe.Pointer(a)), accessAutomaticProxy, 0, 0, 0)
		if h == 0 && e == errInvalidParameter {
			h, e = call(pOpen, uintptr(unsafe.Pointer(a)), accessDefaultProxy, 0, 0, 0)
		}
	}
	if h == 0 {
		return 0, &Error{"WinHttpOpen", e}
	}
	// Resolve, connect, send, receive: 0 is no limit; the caller's context
	// closes the handle instead.
	call(pSetTimeouts, h, 0, 0, 0, 0)
	return h, nil
}

// Connect opens a connection handle to host:port (host without brackets).
func Connect(ses uintptr, host string, port int) (uintptr, error) {
	h, e := call(pConnect, ses, uintptr(unsafe.Pointer(Wide(host))), uintptr(port), 0)
	if h == 0 {
		return 0, &Error{"WinHttpConnect", e}
	}
	return h, nil
}

// OpenRequest opens a request handle for verb and target (the path and
// query, already escaped), over TLS when secure. Cookies are off, and so
// is keep-alive when noKeepAlive is set.
func OpenRequest(conn uintptr, verb, target string, secure, noKeepAlive bool) (uintptr, error) {
	flags := uintptr(flagEscapeDisable | flagEscapeQuery)
	if secure {
		flags |= flagSecure
	}
	h, e := call(pOpenRequest, conn, uintptr(unsafe.Pointer(Wide(verb))), uintptr(unsafe.Pointer(Wide(target))), 0, 0, 0, flags)
	if h == 0 {
		return 0, &Error{"WinHttpOpenRequest", e}
	}
	features := uint32(disableCookies)
	if noKeepAlive {
		features |= disableKeepAlive
	}
	SetOptionDWORD(h, optionDisableFeature, features)
	return h, nil
}

// SetOptionDWORD sets a DWORD option.
func SetOptionDWORD(h uintptr, option int, v uint32) error {
	return boolCall("WinHttpSetOption", pSetOption, h, uintptr(option), uintptr(unsafe.Pointer(&v)), 4)
}

// SetProxyCredentials gives the request the proxy's credentials, offered
// with Basic before any challenge.
func SetProxyCredentials(req uintptr, user, pass string) {
	u, p := Wide(user), Wide(pass)
	call(pSetOption, req, optionProxyUsername, uintptr(unsafe.Pointer(u)), uintptr(len(user)))
	call(pSetOption, req, optionProxyPassword, uintptr(unsafe.Pointer(p)), uintptr(len(pass)))
	call(pSetCredentials, req, authTargetProxy, authSchemeBasic, uintptr(unsafe.Pointer(u)), uintptr(unsafe.Pointer(p)), 0)
}

// AnswerProxyChallenge sets the credentials for the scheme a 407 asked for,
// before the request is sent again.
func AnswerProxyChallenge(req uintptr, user, pass string) error {
	var supported, first, target uint32
	if err := boolCall("WinHttpQueryAuthSchemes", pQueryAuthSchemes, req, uintptr(unsafe.Pointer(&supported)),
		uintptr(unsafe.Pointer(&first)), uintptr(unsafe.Pointer(&target))); err != nil {
		return err
	}
	scheme := first
	if supported&authSchemeBasic != 0 {
		scheme = authSchemeBasic
	}
	return boolCall("WinHttpSetCredentials", pSetCredentials, req, uintptr(target), uintptr(scheme),
		uintptr(unsafe.Pointer(Wide(user))), uintptr(unsafe.Pointer(Wide(pass))), 0)
}

// UpgradeToWebSocket marks the request as a WebSocket handshake.
func UpgradeToWebSocket(req uintptr) error {
	return boolCall("WinHttpSetOption(UPGRADE_TO_WEB_SOCKET)", pSetOption, req, optionUpgradeToWebSocket, 0, 0)
}

// AddHeaders adds header lines ("Name: value\r\n" each), replacing ones of
// the same name.
func AddHeaders(req uintptr, lines string) error {
	if lines == "" {
		return nil
	}
	return boolCall("WinHttpAddRequestHeaders", pAddHeaders, req, uintptr(unsafe.Pointer(Wide(lines))), uintptr(^uint32(0)), addReqFlagAdd|addReqFlagReplace)
}

// Send starts the request; total is the body's length when the body is
// written afterwards with Write (0: none, or framed by the caller).
func Send(req uintptr, total uint32) error {
	return boolCall("WinHttpSendRequest", pSendRequest, req, 0, 0, 0, 0, uintptr(total), 0)
}

// Write sends p of the request body; *n receives what was written.
func Write(req uintptr, p []byte, n *uint32) error {
	if len(p) == 0 {
		*n = 0
		return nil
	}
	return boolCall("WinHttpWriteData", pWriteData, req, uintptr(unsafe.Pointer(&p[0])), uintptr(len(p)), uintptr(unsafe.Pointer(n)))
}

// Receive waits for the response's headers.
func Receive(req uintptr) error {
	return boolCall("WinHttpReceiveResponse", pReceiveResponse, req, 0)
}

// Status is the response's status code.
func Status(req uintptr) (int, error) {
	var code, size uint32 = 0, 4
	err := boolCall("WinHttpQueryHeaders", pQueryHeaders, req, queryStatusCode|queryFlagNumber, 0,
		uintptr(unsafe.Pointer(&code)), uintptr(unsafe.Pointer(&size)), 0)
	return int(code), err
}

// ContentLength is the response's Content-Length, or -1 when it has none.
func ContentLength(req uintptr) int64 {
	var n int64
	size := uint32(8)
	if boolCall("WinHttpQueryHeaders", pQueryHeaders, req, queryContentLength|queryFlagNumber64, 0,
		uintptr(unsafe.Pointer(&n)), uintptr(unsafe.Pointer(&size)), 0) != nil {
		return -1
	}
	return n
}

// queryString reads a string the system copies into a buffer we size.
func queryString(get func(buf *uint16, size *uint32) error) string {
	var size uint32
	err := get(nil, &size)
	if err == nil || !Is(err, errInsufficient) || size == 0 {
		return ""
	}
	buf := make([]uint16, size/2+1)
	if get(&buf[0], &size) != nil {
		return ""
	}
	return syscall.UTF16ToString(buf[:size/2])
}

// RawHeaders is the response's status line and headers, CRLF-separated.
func RawHeaders(req uintptr) string {
	return queryString(func(buf *uint16, size *uint32) error {
		return boolCall("WinHttpQueryHeaders", pQueryHeaders, req, queryRawHeadersCRL, 0,
			uintptr(unsafe.Pointer(buf)), uintptr(unsafe.Pointer(size)), 0)
	})
}

// Header is one response header ("" when absent).
func Header(req uintptr, name string) string {
	n := Wide(name)
	return queryString(func(buf *uint16, size *uint32) error {
		return boolCall("WinHttpQueryHeaders", pQueryHeaders, req, queryCustom, uintptr(unsafe.Pointer(n)),
			uintptr(unsafe.Pointer(buf)), uintptr(unsafe.Pointer(size)), 0)
	})
}

// URL is the request's URL, after redirects.
func URL(req uintptr) string {
	return queryString(func(buf *uint16, size *uint32) error {
		return boolCall("WinHttpQueryOption", pQueryOption, req, optionURL, uintptr(unsafe.Pointer(buf)), uintptr(unsafe.Pointer(size)))
	})
}

// Available waits until some of the body has arrived and says how much can
// be read at once; 0 is the end. A synchronous WinHttpReadData waits until
// its buffer is full or the body ends, so a streamed body is read as
// Available, then Read of that many bytes (probe P8).
func Available(req uintptr, n *uint32) error {
	return boolCall("WinHttpQueryDataAvailable", pQueryDataAvailable, req, uintptr(unsafe.Pointer(n)))
}

// Read reads up to len(p) bytes of the body into p; *n is 0 at the end.
func Read(req uintptr, p []byte, n *uint32) error {
	return boolCall("WinHttpReadData", pReadData, req, uintptr(unsafe.Pointer(&p[0])), uintptr(len(p)), uintptr(unsafe.Pointer(n)))
}

// CloseHandle closes a WinHTTP handle, ending a call blocked on it.
func CloseHandle(h uintptr) {
	if h != 0 {
		call(pCloseHandle, h)
	}
}

// CompleteUpgrade turns a request that received 101 into a WebSocket
// handle; the request handle may then be closed.
func CompleteUpgrade(req uintptr) (uintptr, error) {
	h, e := call(pWSCompleteUpgrade, req, 0)
	if h == 0 {
		return 0, &Error{"WinHttpWebSocketCompleteUpgrade", e}
	}
	return h, nil
}

// WSSend sends p as one buffer of type typ.
func WSSend(ws uintptr, typ int, p []byte) error {
	var ptr uintptr
	if len(p) > 0 {
		ptr = uintptr(unsafe.Pointer(&p[0]))
	}
	return dwordCall("WinHttpWebSocketSend", pWSSend, ws, uintptr(typ), ptr, uintptr(len(p)))
}

// WSReceive reads the next piece of a message into p (len(p) > 0); *n and
// *typ receive its length and buffer type.
func WSReceive(ws uintptr, p []byte, n, typ *uint32) error {
	return dwordCall("WinHttpWebSocketReceive", pWSReceive, ws, uintptr(unsafe.Pointer(&p[0])), uintptr(len(p)),
		uintptr(unsafe.Pointer(n)), uintptr(unsafe.Pointer(typ)))
}

// WSShutdown sends a close frame without waiting for the server's.
func WSShutdown(ws uintptr, code uint16, reason []byte) error {
	var ptr uintptr
	if len(reason) > 0 {
		ptr = uintptr(unsafe.Pointer(&reason[0]))
	}
	return dwordCall("WinHttpWebSocketShutdown", pWSShutdown, ws, uintptr(code), ptr, uintptr(len(reason)))
}

// WSClose sends a close frame and waits for the server's.
func WSClose(ws uintptr, code uint16, reason []byte) error {
	var ptr uintptr
	if len(reason) > 0 {
		ptr = uintptr(unsafe.Pointer(&reason[0]))
	}
	return dwordCall("WinHttpWebSocketClose", pWSClose, ws, uintptr(code), ptr, uintptr(len(reason)))
}

// WSCloseStatus is the close frame the server sent.
func WSCloseStatus(ws uintptr) (code uint16, reason string, err error) {
	var buf [123]byte
	var used uint32
	err = dwordCall("WinHttpWebSocketQueryCloseStatus", pWSQueryCloseStatus, ws, uintptr(unsafe.Pointer(&code)),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(unsafe.Pointer(&used)))
	return code, string(buf[:min(int(used), len(buf))]), err
}
