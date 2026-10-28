// Package nsurl is the NSURLSession plumbing app/fetch and app/ws share on
// macOS (DECISIONS D60, the phase 8 plan's D73): the classes, delegate
// classes made at run time, an ephemeral session configuration with an
// optional proxy, and the answer to a proxy's request for credentials.
//
// It runs on any goroutine and on the session's delegate queue, never on
// the main thread, so it builds classes with the runtime's own functions
// and a call frame of its own (internal/objc's class builder shares the
// main thread's frame).
package nsurl

import (
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/cf"
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

const (
	policyReloadIgnoringCache = 1 // NSURLRequestReloadIgnoringLocalCacheData
	cookiesNever              = 1 // NSHTTPCookieAcceptPolicyNever

	// Challenge dispositions (NSURLSessionAuthChallengeDisposition).
	authUseCredential   = 0
	authDefaultHandling = 1
)

// Classes are the Foundation classes the packages use, set by Load.
var Classes struct {
	NSURL, Request, Session, Config, Queue, Number, Credential, Object, Data, String, WSMessage uintptr
}

var sys struct {
	once                                    sync.Once
	err                                     error
	allocPair, registerPair, addMethod      uintptr
	cfnet                                   uintptr // CFNetwork, for the proxy dictionary keys
	streamPair, wOpen, wClose, wCan, wWrite uintptr
}

// Load loads Foundation and CFNetwork and looks the classes up, once.
func Load() error {
	sys.once.Do(func() { sys.err = load() })
	return sys.err
}

func load() error {
	if err := cf.Init(); err != nil {
		return err
	}
	var err error
	if sys.cfnet, err = cf.Framework("CFNetwork"); err != nil {
		return err
	}
	lib, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return err
	}
	fr := cf.Acquire()
	defer cf.Release(fr)
	for _, c := range []struct {
		p    *uintptr
		name string
	}{
		{&Classes.NSURL, "NSURL"}, {&Classes.Request, "NSMutableURLRequest"}, {&Classes.Session, "NSURLSession"},
		{&Classes.Config, "NSURLSessionConfiguration"}, {&Classes.Queue, "NSOperationQueue"}, {&Classes.Number, "NSNumber"},
		{&Classes.Credential, "NSURLCredential"}, {&Classes.Object, "NSObject"}, {&Classes.Data, "NSData"},
		{&Classes.String, "NSString"}, {&Classes.WSMessage, "NSURLSessionWebSocketMessage"},
	} {
		if *c.p = cf.Cls(fr, c.name); *c.p == 0 {
			return cf.ErrUnavailable
		}
	}
	sys.allocPair, sys.registerPair, sys.addMethod = cf.Sym(lib, "objc_allocateClassPair"), cf.Sym(lib, "objc_registerClassPair"), cf.Sym(lib, "class_addMethod")
	core := cf.CoreFoundation()
	sys.streamPair, sys.wOpen, sys.wClose = cf.Sym(core, "CFStreamCreateBoundPair"), cf.Sym(core, "CFWriteStreamOpen"), cf.Sym(core, "CFWriteStreamClose")
	sys.wCan, sys.wWrite = cf.Sym(core, "CFWriteStreamCanAcceptBytes"), cf.Sym(core, "CFWriteStreamWrite")
	if sys.allocPair == 0 || sys.registerPair == 0 || sys.addMethod == 0 || sys.streamPair == 0 || sys.wWrite == 0 {
		return cf.ErrUnavailable
	}
	return nil
}

// Method is one method of a class made by NewClass.
type Method struct {
	Sel, Types string
	H          func(*ffi.CB)
}

// NewClass makes and registers an NSObject subclass with the methods. Call
// it once per class, after Load.
func NewClass(name string, methods []Method) (uintptr, error) {
	fr := cf.Acquire()
	defer cf.Release(fr)
	n := cstr(name)
	cls := fr.Reset().Arg(Classes.Object).ArgPtr(unsafe.Pointer(n)).Arg(0).Call(sys.allocPair).R0()
	if cls == 0 {
		return 0, cf.ErrUnavailable
	}
	for _, m := range methods {
		sel, imp, types := cf.Sel(fr, m.Sel), ffi.NewCallback(m.H), cstr(m.Types)
		fr.Reset().Arg(cls).Arg(sel).Arg(imp).ArgPtr(unsafe.Pointer(types)).Call(sys.addMethod)
		keep = append(keep, types)
	}
	fr.Call1(sys.registerPair, cls)
	keep = append(keep, n)
	return cls, nil
}

var keep []*byte // class and type names, for the process lifetime

// cstr is a NUL-terminated copy of s on the heap.
func cstr(s string) *byte {
	b := make([]byte, len(s)+1)
	copy(b, s)
	return &b[0]
}

// Msg sends a message by selector name.
func Msg(fr *ffi.Frame, recv uintptr, sel string, args ...uintptr) uintptr {
	return cf.Msg(fr, recv, cf.Sel(fr, sel), args...)
}

// MsgF64 sends a message whose one argument is a double.
func MsgF64(fr *ffi.Frame, recv uintptr, sel string, x float64) {
	s := cf.Sel(fr, sel) // before the builder starts: Sel uses the frame too
	fr.Reset().Arg(recv).Arg(s).ArgF64(x).Call(cf.MsgAddr())
}

// Proxy kinds.
const (
	ProxyHTTP = iota + 1
	ProxyHTTPS
	ProxySOCKS5
	ProxyNone
)

// Proxy is a session's proxy (nil Proxy: the system's settings).
type Proxy struct {
	Kind           int
	Host           string
	Port           int
	User, Password string
}

// Config makes an ephemeral session configuration (autoreleased: call
// inside a pool) with no cookies, cache or stored credentials, idle as the
// request timeout, and the proxy p.
func Config(fr *ffi.Frame, idle time.Duration, p *Proxy) (uintptr, error) {
	cfg := Msg(fr, Classes.Config, "ephemeralSessionConfiguration")
	Msg(fr, cfg, "setHTTPCookieStorage:", 0)
	Msg(fr, cfg, "setHTTPShouldSetCookies:", 0)
	Msg(fr, cfg, "setHTTPCookieAcceptPolicy:", cookiesNever)
	Msg(fr, cfg, "setURLCache:", 0)
	Msg(fr, cfg, "setURLCredentialStorage:", 0)
	Msg(fr, cfg, "setRequestCachePolicy:", policyReloadIgnoringCache)
	MsgF64(fr, cfg, "setTimeoutIntervalForRequest:", idle.Seconds())
	if p != nil {
		dict, err := proxyDict(fr, p)
		if err != nil {
			return 0, err
		}
		Msg(fr, cfg, "setConnectionProxyDictionary:", dict)
		cf.CFRelease(fr, dict)
	}
	return cfg, nil
}

// proxyDict makes the connectionProxyDictionary for p (owned). An HTTP proxy
// carries both http and https URLs; its credentials go through
// ProxyCredential, since the dictionary has no place for them. ProxyNone's
// is empty: no proxy is enabled, where a nil dictionary would mean the
// system's settings.
func proxyDict(fr *ffi.Frame, p *Proxy) (uintptr, error) {
	key := func(name string) uintptr { return cf.Const(sys.cfnet, name) }
	d := cf.Dict(fr)
	set := func(k string, v uintptr) bool {
		kk := key(k)
		if kk == 0 {
			return false
		}
		cf.DictSet(fr, d, kk, v)
		return true
	}
	num := func(n int) uintptr { return Msg(fr, Classes.Number, "numberWithInt:", uintptr(n)) }
	host := cf.String(fr, p.Host)
	defer cf.CFRelease(fr, host)

	good := true
	switch p.Kind {
	case ProxyHTTP:
		good = set("kCFNetworkProxiesHTTPEnable", num(1)) &&
			set("kCFNetworkProxiesHTTPProxy", host) && set("kCFNetworkProxiesHTTPPort", num(p.Port)) &&
			set("kCFNetworkProxiesHTTPSEnable", num(1)) &&
			set("kCFNetworkProxiesHTTPSProxy", host) && set("kCFNetworkProxiesHTTPSPort", num(p.Port))
	case ProxySOCKS5:
		good = set("kCFNetworkProxiesSOCKSEnable", num(1)) &&
			set("kCFNetworkProxiesSOCKSProxy", host) && set("kCFNetworkProxiesSOCKSPort", num(p.Port))
		if good && p.User != "" {
			user, pass := cf.String(fr, p.User), cf.String(fr, p.Password)
			good = set("kCFStreamPropertySOCKSUser", user) && set("kCFStreamPropertySOCKSPassword", pass)
			cf.CFRelease(fr, user)
			cf.CFRelease(fr, pass)
		}
	}
	if !good {
		cf.CFRelease(fr, d)
		return 0, cf.ErrUnavailable
	}
	return d, nil
}

// ProxyCredential answers the challenge of URLSession:task:
// didReceiveChallenge:completionHandler: (handler is its block): a proxy's
// first request for credentials gets user and password, anything else
// (server trust, a web server's own authentication) the system's default
// handling.
func ProxyCredential(fr *ffi.Frame, challenge uintptr, handler objc.ID, user, pass string) {
	disposition, cred := uintptr(authDefaultHandling), uintptr(0)
	if user != "" && Msg(fr, challenge, "previousFailureCount") == 0 {
		if space := Msg(fr, challenge, "protectionSpace"); space != 0 && cf.MsgBool(fr, space, cf.Sel(fr, "isProxy")) {
			u, p := cf.String(fr, user), cf.String(fr, pass)
			cred = Msg(fr, Classes.Credential, "credentialWithUser:password:persistence:", u, p, 0)
			cf.CFRelease(fr, u)
			cf.CFRelease(fr, p)
			disposition = authUseCredential
		}
	}
	objc.CallBlock(fr, handler, disposition, cred)
}

// SetHeader sets one header of an NSMutableURLRequest.
func SetHeader(fr *ffi.Frame, req uintptr, k, v string) {
	ks, vs := cf.String(fr, k), cf.String(fr, v)
	Msg(fr, req, "setValue:forHTTPHeaderField:", vs, ks)
	cf.CFRelease(fr, ks)
	cf.CFRelease(fr, vs)
}

// Headers copies an NSHTTPURLResponse's header fields out, each passed to
// add (call inside a pool, or on a delegate queue).
func Headers(fr *ffi.Frame, resp uintptr, add func(name, value string)) {
	fields := Msg(fr, resp, "allHeaderFields")
	if fields == 0 {
		return
	}
	keys := Msg(fr, fields, "allKeys")
	for i, n := 0, int(Msg(fr, keys, "count")); i < n; i++ {
		k := Msg(fr, keys, "objectAtIndex:", uintptr(i))
		add(cf.GoString(fr, k), cf.GoString(fr, Msg(fr, fields, "objectForKey:", k)))
	}
}

// BoundPair makes a bound stream pair with a buffer of size bytes: an
// NSInputStream for NSURLSession to read a request body from and the
// write stream it reads, opened. Both are owned.
func BoundPair(fr *ffi.Frame, size int) (in, out uintptr) {
	pair := new([2]uintptr)
	fr.Reset().Arg(0).ArgPtr(unsafe.Pointer(&pair[0])).ArgPtr(unsafe.Pointer(&pair[1])).Arg(uintptr(size)).Call(sys.streamPair)
	in, out = pair[0], pair[1]
	if out != 0 {
		fr.Call1(sys.wOpen, out)
	}
	return in, out
}

// CanWrite reports whether a write to the stream would not block.
func CanWrite(fr *ffi.Frame, w uintptr) bool { return byte(fr.Call1(sys.wCan, w)) != 0 }

// WriteStream writes p (in Go memory the caller keeps alive) to the stream:
// the bytes written, -1 on the stream's error.
func WriteStream(fr *ffi.Frame, w uintptr, p []byte) int {
	return int(fr.Reset().Arg(w).ArgPtr(unsafe.Pointer(&p[0])).Arg(uintptr(len(p))).Call(sys.wWrite).R0())
}

// CloseStream closes the write stream: the reader sees the end.
func CloseStream(fr *ffi.Frame, w uintptr) { fr.Call1(sys.wClose, w) }
