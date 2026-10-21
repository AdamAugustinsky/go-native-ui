//go:build darwin

package fetch

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/cf"
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// macOS: NSURLSession. Each call makes an ephemeral session (no cookies, no
// cache, no stored credentials) with a serial delegate queue of its own, so a
// slow writer holds up only its own transfer, and one data task.
//
// The delegate is a small class made here, with the runtime's own functions
// and a call frame of our own: internal/objc's class builder shares the main
// thread's frame, and this package runs on any goroutine. NSURLSession calls
// the delegate on the queue's thread, a thread Go has never seen. The
// callbacks do no I/O of their own: they copy what arrives (a response head,
// a body chunk, the end) into a channel, and the calling goroutine writes to
// the writer and calls Progress. When the channel is full the callback waits,
// which holds back the delegate queue and, through TCP, the server: that is
// the transfer's flow control. Nothing here ever runs on the main thread.

const (
	dispositionCancel = 0 // NSURLSessionResponseCancel
	dispositionAllow  = 1 // NSURLSessionResponseAllow

	authUseCredential   = 0 // NSURLSessionAuthChallengeUseCredential
	authDefaultHandling = 1 // NSURLSessionAuthChallengePerformDefaultHandling

	policyReloadIgnoringCache = 1 // NSURLRequestReloadIgnoringLocalCacheData
	cookiesNever              = 1 // NSHTTPCookieAcceptPolicyNever

	errCancelled = -999  // NSURLErrorCancelled
	errTimedOut  = -1001 // NSURLErrorTimedOut
	errATS       = -1022 // NSURLErrorAppTransportSecurityRequiresSecureConnection

	chunkSize = 64 << 10
	idleLimit = 24 * time.Hour // NSURLSession's own 60 s idle limit would cut a stalled download short; the context is the limit
)

var sys struct {
	once sync.Once
	err  error

	cNSURL, cRequest, cSession, cConfig, cQueue, cNumber, cCredential, cObject uintptr
	selBytes, selLength                                                        uintptr
	cDelegate                                                                  uintptr

	cfnet uintptr // CFNetwork, for the proxy dictionary keys
}

// transfers maps a delegate object to the call it serves.
var transfers sync.Map // uintptr -> *transfer

var chunks = sync.Pool{New: func() any { b := make([]byte, 0, chunkSize); return &b }}

type eventKind uint8

const (
	evResponse eventKind = iota + 1
	evData
	evDone
)

type event struct {
	kind eventKind

	// evResponse
	status int
	length int64
	url    string

	// evData
	buf *[]byte

	// evDone
	err *sysError
}

// A transfer is the state the delegate callbacks share with the call.
type transfer struct {
	events chan event
	done   chan struct{} // closed when the call returns: callbacks stop waiting
	max    int64

	user, password string // proxy credentials
}

// send queues an event for the call; false if the call is gone.
func (t *transfer) send(e event) bool {
	select {
	case t.events <- e:
		return true
	case <-t.done:
		return false
	}
}

// allows reports whether a response's body is wanted.
func (t *transfer) allows(status int, length int64) bool {
	return ok(status) && (t.max <= 0 || length < 0 || length <= t.max)
}

// sysError is an NSError from the URL loading system.
type sysError struct {
	domain string
	code   int
	desc   string
}

func (e *sysError) Error() string { return fmt.Sprintf("%s (%s %d)", e.desc, e.domain, e.code) }

// Is maps the system's codes to the package's and the context's errors.
func (e *sysError) Is(target error) bool {
	if e.domain != "NSURLErrorDomain" {
		return false
	}
	switch target {
	case ErrInsecure:
		return e.code == errATS
	case context.DeadlineExceeded:
		return e.code == errTimedOut
	case context.Canceled:
		return e.code == errCancelled
	}
	return false
}

func msg(fr *ffi.Frame, recv uintptr, sel string, args ...uintptr) uintptr {
	return cf.Msg(fr, recv, cf.Sel(fr, sel), args...)
}

func msgF64(fr *ffi.Frame, recv uintptr, sel string, x float64) {
	s := cf.Sel(fr, sel) // before the builder starts: Sel uses the frame too
	fr.Reset().Arg(recv).Arg(s).ArgF64(x).Call(cf.MsgAddr())
}

// cstr is a NUL-terminated copy of s on the heap, for the runtime functions.
func cstr(s string) *byte {
	b := make([]byte, len(s)+1)
	copy(b, s)
	return &b[0]
}

func load() error {
	sys.once.Do(func() { sys.err = loadOnce() })
	return sys.err
}

func loadOnce() error {
	if err := cf.Init(); err != nil {
		return err
	}
	if _, err := cf.Framework("CFNetwork"); err != nil {
		return err
	}
	lib, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return err
	}
	fr := cf.Acquire()
	defer cf.Release(fr)
	sys.cfnet, _ = cf.Framework("CFNetwork")
	for _, c := range []struct {
		p    *uintptr
		name string
	}{
		{&sys.cNSURL, "NSURL"}, {&sys.cRequest, "NSMutableURLRequest"}, {&sys.cSession, "NSURLSession"},
		{&sys.cConfig, "NSURLSessionConfiguration"}, {&sys.cQueue, "NSOperationQueue"}, {&sys.cNumber, "NSNumber"},
		{&sys.cCredential, "NSURLCredential"}, {&sys.cObject, "NSObject"},
	} {
		if *c.p = cf.Cls(fr, c.name); *c.p == 0 {
			return cf.ErrUnavailable
		}
	}
	sys.selBytes, sys.selLength = cf.Sel(fr, "bytes"), cf.Sel(fr, "length")

	allocPair, registerPair, addMethod := cf.Sym(lib, "objc_allocateClassPair"), cf.Sym(lib, "objc_registerClassPair"), cf.Sym(lib, "class_addMethod")
	if allocPair == 0 || registerPair == 0 || addMethod == 0 {
		return cf.ErrUnavailable
	}
	name := cstr("GNUIFetchDelegate")
	cls := fr.Reset().Arg(sys.cObject).ArgPtr(unsafe.Pointer(name)).Arg(0).Call(allocPair).R0()
	if cls == 0 {
		return cf.ErrUnavailable
	}
	for _, m := range []struct {
		sel, types string
		h          func(*ffi.CB)
	}{
		{"URLSession:dataTask:didReceiveResponse:completionHandler:", "v@:@@@@?", onResponse},
		{"URLSession:dataTask:didReceiveData:", "v@:@@@", onData},
		{"URLSession:task:didCompleteWithError:", "v@:@@@", onComplete},
		{"URLSession:task:didReceiveChallenge:completionHandler:", "v@:@@@@?", onChallenge},
	} {
		sel, imp, types := cf.Sel(fr, m.sel), ffi.NewCallback(m.h), cstr(m.types)
		fr.Reset().Arg(cls).Arg(sel).Arg(imp).ArgPtr(unsafe.Pointer(types)).Call(addMethod)
	}
	fr.Call1(registerPair, cls)
	sys.cDelegate = cls
	return nil
}

// ---- Delegate callbacks (on the session's delegate queue) ----

func lookup(self uintptr) *transfer {
	if v, ok := transfers.Load(self); ok {
		return v.(*transfer)
	}
	return nil
}

// URLSession:dataTask:didReceiveResponse:completionHandler:
func onResponse(c *ffi.CB) {
	fr := c.Frame()
	t, resp, handler := lookup(c.Int(0)), c.Int(4), objc.ID(c.Int(5))
	disposition := uintptr(dispositionCancel)
	if t != nil {
		e := event{
			kind:   evResponse,
			status: int(msg(fr, resp, "statusCode")),
			length: int64(msg(fr, resp, "expectedContentLength")),
		}
		e.url = cf.GoString(fr, msg(fr, msg(fr, resp, "URL"), "absoluteString"))
		if t.send(e) && t.allows(e.status, e.length) {
			disposition = dispositionAllow
		}
	}
	objc.CallBlock(c.Frame(), handler, disposition)
}

// URLSession:dataTask:didReceiveData:
func onData(c *ffi.CB) {
	t := lookup(c.Int(0))
	if t == nil {
		return
	}
	fr := c.Frame()
	data := c.Int(4)
	n := int(fr.Reset().Arg(data).Arg(sys.selLength).Call(cf.MsgAddr()).R0())
	if n == 0 {
		return
	}
	p := fr.Reset().Arg(data).Arg(sys.selBytes).Call(cf.MsgAddr()).R0()
	src := unsafe.Slice((*byte)(ffi.PtrFromC(p)), n)
	for len(src) > 0 {
		bp := chunks.Get().(*[]byte)
		k := copy((*bp)[:cap(*bp)], src)
		*bp = (*bp)[:k]
		src = src[k:]
		if !t.send(event{kind: evData, buf: bp}) {
			return
		}
	}
}

// URLSession:task:didCompleteWithError:
func onComplete(c *ffi.CB) {
	t := lookup(c.Int(0))
	if t == nil {
		return
	}
	fr := c.Frame()
	e := event{kind: evDone}
	if ns := c.Int(4); ns != 0 {
		e.err = &sysError{
			code:   int(msg(fr, ns, "code")),
			domain: cf.GoString(fr, msg(fr, ns, "domain")),
			desc:   cf.GoString(fr, msg(fr, ns, "localizedDescription")),
		}
	}
	t.send(e)
}

// URLSession:task:didReceiveChallenge:completionHandler: answers a proxy's
// request for credentials with the ones in Options.Proxy, once; any other
// challenge (server trust, a web server's own authentication) gets the
// system's default handling.
func onChallenge(c *ffi.CB) {
	fr := c.Frame()
	t, challenge, handler := lookup(c.Int(0)), c.Int(4), objc.ID(c.Int(5))
	disposition, cred := uintptr(authDefaultHandling), uintptr(0)
	if t != nil && t.user != "" && msg(fr, challenge, "previousFailureCount") == 0 {
		if space := msg(fr, challenge, "protectionSpace"); space != 0 && cf.MsgBool(fr, space, cf.Sel(fr, "isProxy")) {
			user, pass := cf.String(fr, t.user), cf.String(fr, t.password)
			cred = msg(fr, sys.cCredential, "credentialWithUser:password:persistence:", user, pass, 0)
			cf.CFRelease(fr, user)
			cf.CFRelease(fr, pass)
			disposition = authUseCredential
		}
	}
	objc.CallBlock(c.Frame(), handler, disposition, cred)
}

// ---- The call ----

func get(ctx context.Context, u *url.URL, s *sink, opt Options) (Response, error) {
	if p := opt.Proxy; p != nil && p.kind() == ProxyHTTPS {
		return Response{}, fmt.Errorf("HTTPS proxy: %w on macOS", errors.ErrUnsupported)
	}
	if err := load(); err != nil {
		return Response{}, err
	}
	fr := cf.Acquire()
	defer cf.Release(fr)

	t := &transfer{events: make(chan event, 16), done: make(chan struct{}), max: s.max}
	if p := opt.Proxy; p != nil {
		t.user, t.password = p.User, p.Password
	}
	defer close(t.done)

	delegate, queue, session, task, err := start(fr, t, u, opt)
	if err != nil {
		return Response{}, err
	}
	defer func() {
		transfers.Delete(delegate)
		pop := cf.Pool(fr)
		msg(fr, task, "cancel")
		msg(fr, session, "invalidateAndCancel")
		for _, o := range []uintptr{task, session, queue, delegate} {
			msg(fr, o, "release")
		}
		pop()
	}()

	res := Response{ContentLength: -1}
	for {
		select {
		case <-ctx.Done():
			msg(fr, task, "cancel")
			return res, ctx.Err()
		case e := <-t.events:
			switch e.kind {
			case evResponse:
				res.Status, res.URL, res.ContentLength = e.status, e.url, e.length
				if !ok(e.status) {
					return res, &StatusError{Code: e.status, URL: e.url}
				}
				if err := s.admit(e.length); err != nil {
					return res, err
				}
			case evData:
				_, err := s.Write(*e.buf)
				chunks.Put(e.buf)
				if err != nil {
					return res, err
				}
			case evDone:
				if e.err != nil {
					return res, e.err
				}
				return res, nil
			}
		}
	}
}

// start builds the session and starts the task. The objects it returns are
// owned (+1); the caller releases them.
func start(fr *ffi.Frame, t *transfer, u *url.URL, opt Options) (delegate, queue, session, task uintptr, err error) {
	pop := cf.Pool(fr)
	defer pop()

	str := cf.String(fr, u.String())
	nsurl := msg(fr, sys.cNSURL, "URLWithString:", str)
	cf.CFRelease(fr, str)
	if nsurl == 0 {
		return 0, 0, 0, 0, fmt.Errorf("%w: %q", ErrBadURL, u.String())
	}

	req := msg(fr, sys.cRequest, "requestWithURL:", nsurl)
	msg(fr, req, "setCachePolicy:", policyReloadIgnoringCache)
	msg(fr, req, "setHTTPShouldHandleCookies:", 0)
	setHeader := func(k, v string) {
		ks, vs := cf.String(fr, k), cf.String(fr, v)
		msg(fr, req, "setValue:forHTTPHeaderField:", vs, ks)
		cf.CFRelease(fr, ks)
		cf.CFRelease(fr, vs)
	}
	setHeader("Accept-Encoding", "identity")
	for k, v := range opt.Headers {
		setHeader(k, v)
	}

	cfg := msg(fr, sys.cConfig, "ephemeralSessionConfiguration")
	msg(fr, cfg, "setHTTPCookieStorage:", 0)
	msg(fr, cfg, "setHTTPShouldSetCookies:", 0)
	msg(fr, cfg, "setHTTPCookieAcceptPolicy:", cookiesNever)
	msg(fr, cfg, "setURLCache:", 0)
	msg(fr, cfg, "setURLCredentialStorage:", 0)
	msg(fr, cfg, "setRequestCachePolicy:", policyReloadIgnoringCache)
	idle := idleLimit
	if opt.Timeout > 0 {
		idle = opt.Timeout
	}
	msgF64(fr, cfg, "setTimeoutIntervalForRequest:", idle.Seconds())
	if opt.Proxy != nil {
		dict, err := proxyDict(fr, opt.Proxy)
		if err != nil {
			return 0, 0, 0, 0, err
		}
		msg(fr, cfg, "setConnectionProxyDictionary:", dict)
		cf.CFRelease(fr, dict)
	}

	delegate = msg(fr, msg(fr, sys.cDelegate, "alloc"), "init")
	queue = msg(fr, msg(fr, sys.cQueue, "alloc"), "init")
	msg(fr, queue, "setMaxConcurrentOperationCount:", 1)
	transfers.Store(delegate, t)
	session = msg(fr, msg(fr, sys.cSession, "sessionWithConfiguration:delegate:delegateQueue:", cfg, delegate, queue), "retain")
	task = msg(fr, msg(fr, session, "dataTaskWithRequest:", req), "retain")
	msg(fr, task, "resume")
	return delegate, queue, session, task, nil
}

// proxyDict makes the connectionProxyDictionary for p (owned). An HTTP proxy
// carries both http and https URLs; its credentials go through onChallenge,
// since the dictionary has no place for them. ProxyNone's is empty: no proxy
// is enabled, where a nil dictionary would mean the system's settings.
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
	num := func(n int) uintptr { return msg(fr, sys.cNumber, "numberWithInt:", uintptr(n)) }
	host := cf.String(fr, p.Host)
	defer cf.CFRelease(fr, host)

	good := true
	switch p.kind() {
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
