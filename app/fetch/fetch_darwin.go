//go:build darwin

package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"sync"
	"time"
	"unsafe"

	"gonativeui/internal/cf"
	"gonativeui/internal/ffi"
	"gonativeui/internal/nsurl"
	"gonativeui/internal/objc"
)

// macOS: NSURLSession. Each call makes an ephemeral session (no cookies, no
// cache, no stored credentials) with a serial delegate queue of its own, so a
// slow writer holds up only its own transfer, and one task: a data task, or
// for a request body an upload task that reads the body from a bound stream
// pair the calling goroutine writes into.
//
// NSURLSession calls the delegate on the queue's thread, a thread Go has
// never seen. The callbacks do no I/O of their own: they copy what arrives
// (a response head, a body chunk, upload progress, the end) into a channel,
// and the calling goroutine writes to the writer and calls Progress and
// UploadProgress. When the channel is full the callback waits, which holds
// back the delegate queue and, through TCP, the server: that is the
// transfer's flow control. Nothing here ever runs on the main thread.

const (
	dispositionCancel = 0 // NSURLSessionResponseCancel
	dispositionAllow  = 1 // NSURLSessionResponseAllow

	errCancelled = -999  // NSURLErrorCancelled
	errTimedOut  = -1001 // NSURLErrorTimedOut
	errATS       = -1022 // NSURLErrorAppTransportSecurityRequiresSecureConnection

	chunkSize = 64 << 10
	// bodyBuffer is the bound stream pair's buffer, and the most the
	// calling goroutine reads from the body ahead of what was sent.
	bodyBuffer = 256 << 10
	idleLimit  = 24 * time.Hour // NSURLSession's own 60 s idle limit would cut a stalled download short; the context is the limit
)

var sys struct {
	once      sync.Once
	err       error
	selBytes  uintptr
	selLength uintptr
	cDelegate uintptr
}

// transfers maps a delegate object to the call it serves.
var transfers sync.Map // uintptr -> *transfer

var chunks = sync.Pool{New: func() any { b := make([]byte, 0, chunkSize); return &b }}

type eventKind uint8

const (
	evResponse eventKind = iota + 1
	evData
	evDone
	evSent
)

type event struct {
	kind eventKind

	// evResponse
	status  int
	length  int64
	url     string
	headers map[string]string

	// evData
	buf *[]byte

	// evSent: the body bytes sent so far
	sent int64

	// evDone
	err *sysError
}

// A transfer is the state the delegate callbacks share with the call.
type transfer struct {
	events chan event
	done   chan struct{} // closed when the call returns: callbacks stop waiting
	max    int64
	call   *call

	user, password string // proxy credentials

	bodyStream uintptr   // the bound pair's input stream, owned; 0 when none
	streamOnce sync.Once // the stream is given once: a second ask fails the task
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
	return t.call.accepts(status) && (t.max <= 0 || length < 0 || length <= t.max)
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
	return nsurl.Msg(fr, recv, sel, args...)
}

func load() error {
	sys.once.Do(func() { sys.err = loadOnce() })
	return sys.err
}

func loadOnce() error {
	if err := nsurl.Load(); err != nil {
		return err
	}
	fr := cf.Acquire()
	defer cf.Release(fr)
	sys.selBytes, sys.selLength = cf.Sel(fr, "bytes"), cf.Sel(fr, "length")
	cls, err := nsurl.NewClass("GNUIFetchDelegate", []nsurl.Method{
		{Sel: "URLSession:dataTask:didReceiveResponse:completionHandler:", Types: "v@:@@@@?", H: onResponse},
		{Sel: "URLSession:dataTask:didReceiveData:", Types: "v@:@@@", H: onData},
		{Sel: "URLSession:task:didCompleteWithError:", Types: "v@:@@@", H: onComplete},
		{Sel: "URLSession:task:didReceiveChallenge:completionHandler:", Types: "v@:@@@@?", H: onChallenge},
		{Sel: "URLSession:task:needNewBodyStream:", Types: "v@:@@@?", H: onNeedBody},
		{Sel: "URLSession:task:didSendBodyData:totalBytesSent:totalBytesExpectedToSend:", Types: "v@:@@qqq", H: onSent},
	})
	sys.cDelegate = cls
	return err
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
		if t.call.headers {
			e.headers = map[string]string{}
			nsurl.Headers(fr, resp, func(k, v string) { addHeader(e.headers, k, v) })
		}
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
// request for credentials with the ones in Options.Proxy, once.
func onChallenge(c *ffi.CB) {
	t := lookup(c.Int(0))
	var user, pass string
	if t != nil {
		user, pass = t.user, t.password
	}
	nsurl.ProxyCredential(c.Frame(), c.Int(4), objc.ID(c.Int(5)), user, pass)
}

// URLSession:task:needNewBodyStream: gives the upload task the body's
// stream, once: a body read once cannot be sent again (a 307 redirect, a
// server's authentication), so a second ask gets nil and fails the task.
func onNeedBody(c *ffi.CB) {
	t := lookup(c.Int(0))
	stream := uintptr(0)
	if t != nil {
		t.streamOnce.Do(func() { stream = t.bodyStream })
	}
	objc.CallBlock(c.Frame(), objc.ID(c.Int(4)), stream)
}

// URLSession:task:didSendBodyData:totalBytesSent:totalBytesExpectedToSend:
func onSent(c *ffi.CB) {
	if t := lookup(c.Int(0)); t != nil {
		t.send(event{kind: evSent, sent: int64(c.Int(5))})
	}
}

// ---- The call ----

func do(ctx context.Context, u *url.URL, c *call, s *sink, opt Options) (Response, error) {
	if p := opt.Proxy; p != nil && p.kind() == ProxyHTTPS {
		return Response{}, fmt.Errorf("HTTPS proxy: %w on macOS", errors.ErrUnsupported)
	}
	if err := load(); err != nil {
		return Response{}, err
	}
	fr := cf.Acquire()
	defer cf.Release(fr)

	t := &transfer{events: make(chan event, 16), done: make(chan struct{}), max: s.max, call: c}
	if p := opt.Proxy; p != nil {
		t.user, t.password = p.User, p.Password
	}
	defer close(t.done)

	var out uintptr // the body's write stream
	var upload *uploadStream
	if c.hasBody() && c.length != 0 {
		t.bodyStream, out = nsurl.BoundPair(fr, bodyBuffer)
		if t.bodyStream == 0 || out == 0 {
			cf.CFRelease(fr, t.bodyStream)
			cf.CFRelease(fr, out)
			return Response{}, cf.ErrUnavailable
		}
		var err error
		upload, err = watchUpload(fr, out)
		if err != nil {
			nsurl.CloseStream(fr, out)
			cf.CFRelease(fr, t.bodyStream)
			cf.CFRelease(fr, out)
			return Response{}, err
		}
	}
	delegate, queue, session, task, err := start(fr, t, u, opt)
	if err != nil {
		if upload != nil {
			upload.close(fr)
		}
		cf.CFRelease(fr, t.bodyStream)
		cf.CFRelease(fr, out)
		return Response{}, err
	}
	defer func() {
		transfers.Delete(delegate)
		pop := cf.Pool(fr)
		msg(fr, task, "cancel")
		msg(fr, session, "invalidateAndCancel")
		if out != 0 {
			upload.close(fr)
		}
		for _, o := range []uintptr{task, session, queue, delegate, t.bodyStream, out} {
			cf.CFRelease(fr, o)
		}
		pop()
	}()

	res := Response{ContentLength: -1}
	var reported int64 // the last count UploadProgress was given
	handle := func(e event) (bool, error) {
		switch e.kind {
		case evResponse:
			res.Status, res.URL, res.ContentLength, res.Headers = e.status, e.url, e.length, e.headers
			if !c.accepts(e.status) {
				return true, &StatusError{Code: e.status, URL: e.url}
			}
			if c.method == "HEAD" {
				return true, nil
			}
			if err := s.admit(e.length); err != nil {
				return true, err
			}
		case evData:
			_, err := s.Write(*e.buf)
			chunks.Put(e.buf)
			if err != nil {
				return true, err
			}
		case evSent:
			if c.upload != nil {
				c.upload(e.sent, c.length)
				reported = e.sent
			}
		case evDone:
			if upload != nil {
				c.sent = upload.sent.Load()
			}
			if e.err != nil {
				return true, e.err
			}
			// NSURLSession may end the task before it reports the last of
			// the body (seen on a loaded runner at 93 % of 1 GiB): the
			// bytes all went, so the progress ends where the body did.
			if c.upload != nil && c.sent > reported {
				c.upload(c.sent, c.length)
			}
			return true, nil
		}
		return false, nil
	}

	// Keep one body buffer in flight. Native callouts write it without blocking
	// and signal when it is consumed; this goroutine keeps delivering session
	// events and cancellation while the reader holds the stream back.
	var buf []byte
	writing, submitted := out != 0, false
	if writing {
		buf = make([]byte, bodyBuffer)
	}
	for {
		if writing && !submitted {
			n, err := c.fill(buf)
			if err == io.EOF {
				upload.close(fr)
				writing = false
			} else if err != nil {
				return res, err
			} else {
				upload.submit(fr, buf[:n])
				submitted = true
			}
		}
		var writable <-chan struct{}
		if writing {
			writable = upload.wake
		}
		select {
		case <-ctx.Done():
			msg(fr, task, "cancel")
			return res, ctx.Err()
		case e := <-t.events:
			if end, err := handle(e); end {
				return res, err
			}
		case <-writable:
			c.sent = upload.sent.Load()
			writing = !upload.finished()
			submitted = false
		}
	}

}

// start builds the session and starts the task. The objects it returns are
// owned (+1); the caller releases them.
func start(fr *ffi.Frame, t *transfer, u *url.URL, opt Options) (delegate, queue, session, task uintptr, err error) {
	pop := cf.Pool(fr)
	defer pop()

	str := cf.String(fr, u.String())
	nsurlObj := msg(fr, nsurl.Classes.NSURL, "URLWithString:", str)
	cf.CFRelease(fr, str)
	if nsurlObj == 0 {
		return 0, 0, 0, 0, fmt.Errorf("%w: %q", ErrBadURL, u.String())
	}

	c := t.call
	req := msg(fr, nsurl.Classes.Request, "requestWithURL:", nsurlObj)
	msg(fr, req, "setCachePolicy:", 1) // NSURLRequestReloadIgnoringLocalCacheData
	msg(fr, req, "setHTTPShouldHandleCookies:", 0)
	if c.method != "GET" {
		m := cf.String(fr, c.method)
		msg(fr, req, "setHTTPMethod:", m)
		cf.CFRelease(fr, m)
	}
	nsurl.SetHeader(fr, req, "Accept-Encoding", "identity")
	for k, v := range opt.Headers {
		nsurl.SetHeader(fr, req, k, v)
	}
	if c.hasBody() && c.length >= 0 {
		nsurl.SetHeader(fr, req, "Content-Length", strconv.FormatInt(c.length, 10))
	}

	idle := idleLimit
	if opt.Timeout > 0 {
		idle = opt.Timeout
	}
	var px *nsurl.Proxy
	if p := opt.Proxy; p != nil {
		px = &nsurl.Proxy{Kind: int(p.kind()), Host: p.Host, Port: p.Port, User: p.User, Password: p.Password}
	}
	cfg, err := nsurl.Config(fr, idle, px)
	if err != nil {
		return 0, 0, 0, 0, err
	}

	delegate = msg(fr, msg(fr, sys.cDelegate, "alloc"), "init")
	queue = msg(fr, msg(fr, nsurl.Classes.Queue, "alloc"), "init")
	msg(fr, queue, "setMaxConcurrentOperationCount:", 1)
	transfers.Store(delegate, t)
	session = msg(fr, msg(fr, nsurl.Classes.Session, "sessionWithConfiguration:delegate:delegateQueue:", cfg, delegate, queue), "retain")
	if t.bodyStream != 0 {
		task = msg(fr, msg(fr, session, "uploadTaskWithStreamedRequest:", req), "retain")
	} else {
		task = msg(fr, msg(fr, session, "dataTaskWithRequest:", req), "retain")
	}
	msg(fr, task, "resume")
	return delegate, queue, session, task, nil
}
