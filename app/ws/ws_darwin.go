//go:build darwin

package ws

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"gonativeui/app/fetch"
	"gonativeui/internal/cf"
	"gonativeui/internal/ffi"
	"gonativeui/internal/nsurl"
	"gonativeui/internal/objc"
)

// macOS: NSURLSessionWebSocketTask (D73) in an ephemeral session of its
// own with a serial delegate queue. Receives and sends complete through
// per-request blocks (internal/objc's BlockKind: no allocation, any
// thread). A receive's completion copies the message into the reader's
// buffer on the delegate queue, inside the queue's autorelease pool, and
// hands it over by channel; the reader goroutine itself sends only
// messages that return nothing autoreleased. So a steady stream of
// messages allocates nothing in Go (N2's gate).

const (
	msgData   = 0 // NSURLSessionWebSocketMessageTypeData
	utf8      = 4 // NSUTF8StringEncoding
	idleLimit = 365 * 24 * time.Hour
	// maxConns is the size of the table blocks find their connection in.
	maxConns = 1024
)

var sys struct {
	once     sync.Once
	err      error
	delegate uintptr
	recvKind *objc.BlockKind
	sendKind *objc.BlockKind

	selReceive, selSend, selType, selData, selString, selDataEnc, selLength, selBytes     uintptr
	selAlloc, selInitData, selInitString, selInitBytesNoCopy, selInitStrBytes, selRelease uintptr
	selCloseCode, selCloseReason, selPing                                                 uintptr
}

// conns is the table a block's context (slot | generation<<32) indexes.
var (
	conns   [maxConns]atomic.Pointer[darwinConn]
	connsMu sync.Mutex
	connGen uint32
)

// delegates maps a delegate object to its connection (handshake and close
// callbacks only; they may allocate).
var delegates sync.Map

type recvResult struct {
	kind MessageKind
	err  error
}

type darwinConn struct {
	slot int
	gen  uint32
	max  int64

	delegate, queue, session, task uintptr

	recv chan recvResult
	sent chan error
	rbuf []byte // the reader's buffer, filled by the receive's completion

	opened    chan string   // the chosen subprotocol
	completed chan struct{} // didCompleteWithError
	doneErr   error         // set before completed closes
	status    int           // the handshake's HTTP status, set before completed closes
	compOnce  sync.Once

	closedCh  chan struct{} // Close called
	closing   atomic.Bool
	dead      atomic.Bool // the reader saw the end
	closeOnce sync.Once
	rerr      error
	wmu       sync.Mutex

	user, password string
}

func ctxOf(c *darwinConn) uintptr { return uintptr(c.slot) | uintptr(c.gen)<<32 }

func connOf(ctx uintptr) *darwinConn {
	slot, gen := int(ctx&0xFFFFFFFF), uint32(ctx>>32)
	if slot >= maxConns {
		return nil
	}
	c := conns[slot].Load()
	if c == nil || c.gen != gen {
		return nil
	}
	return c
}

func register(c *darwinConn) error {
	connsMu.Lock()
	defer connsMu.Unlock()
	for i := range conns {
		if conns[i].Load() == nil {
			connGen++
			c.slot, c.gen = i, connGen
			conns[i].Store(c)
			return nil
		}
	}
	return errors.New("ws: too many connections")
}

func unregister(c *darwinConn) {
	connsMu.Lock()
	defer connsMu.Unlock()
	conns[c.slot].CompareAndSwap(c, nil)
}

// sysError is an NSError.
type sysError struct {
	domain string
	code   int
	desc   string
}

func (e *sysError) Error() string { return fmt.Sprintf("ws: %s (%s %d)", e.desc, e.domain, e.code) }

func nsError(fr *ffi.Frame, ns uintptr) *sysError {
	return &sysError{
		code:   int(nsurl.Msg(fr, ns, "code")),
		domain: cf.GoString(fr, nsurl.Msg(fr, ns, "domain")),
		desc:   cf.GoString(fr, nsurl.Msg(fr, ns, "localizedDescription")),
	}
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
	for _, s := range []struct {
		p    *uintptr
		name string
	}{
		{&sys.selReceive, "receiveMessageWithCompletionHandler:"}, {&sys.selSend, "sendMessage:completionHandler:"},
		{&sys.selType, "type"}, {&sys.selData, "data"}, {&sys.selString, "string"},
		{&sys.selDataEnc, "dataUsingEncoding:"}, {&sys.selLength, "length"}, {&sys.selBytes, "bytes"},
		{&sys.selAlloc, "alloc"}, {&sys.selInitData, "initWithData:"}, {&sys.selInitString, "initWithString:"},
		{&sys.selInitBytesNoCopy, "initWithBytesNoCopy:length:freeWhenDone:"},
		{&sys.selInitStrBytes, "initWithBytes:length:encoding:"}, {&sys.selRelease, "release"},
		{&sys.selCloseCode, "closeCode"}, {&sys.selCloseReason, "closeReason"},
		{&sys.selPing, "sendPingWithPongReceiveHandler:"},
	} {
		*s.p = cf.Sel(fr, s.name)
	}
	cls, err := nsurl.NewClass("GNUIWebSocketDelegate", []nsurl.Method{
		{Sel: "URLSession:webSocketTask:didOpenWithProtocol:", Types: "v@:@@@", H: onOpen},
		{Sel: "URLSession:task:didCompleteWithError:", Types: "v@:@@@", H: onComplete},
		{Sel: "URLSession:task:didReceiveChallenge:completionHandler:", Types: "v@:@@@@?", H: onChallenge},
	})
	if err != nil {
		return err
	}
	sys.delegate = cls
	sys.recvKind = objc.NewBlockKind("v24@?0@8@16", true, onReceive, nil)
	sys.sendKind = objc.NewBlockKind("v16@?0@8", true, onSend, nil)
	return nil
}

// ---- Callbacks (on the session's delegate queue) ----

func delegateConn(self uintptr) *darwinConn {
	if v, ok := delegates.Load(self); ok {
		return v.(*darwinConn)
	}
	return nil
}

// URLSession:webSocketTask:didOpenWithProtocol:
func onOpen(cb *ffi.CB) {
	if c := delegateConn(cb.Int(0)); c != nil {
		select {
		case c.opened <- cf.GoString(cb.Frame(), cb.Int(4)):
		default:
		}
	}
}

// URLSession:task:didCompleteWithError:
func onComplete(cb *ffi.CB) {
	c := delegateConn(cb.Int(0))
	if c == nil {
		return
	}
	fr := cb.Frame()
	c.compOnce.Do(func() {
		if ns := cb.Int(4); ns != 0 {
			c.doneErr = nsError(fr, ns)
		}
		if resp := nsurl.Msg(fr, cb.Int(3), "response"); resp != 0 {
			c.status = int(nsurl.Msg(fr, resp, "statusCode"))
		}
		close(c.completed)
	})
}

// URLSession:task:didReceiveChallenge:completionHandler:
func onChallenge(cb *ffi.CB) {
	var user, pass string
	if c := delegateConn(cb.Int(0)); c != nil {
		user, pass = c.user, c.password
	}
	nsurl.ProxyCredential(cb.Frame(), cb.Int(4), objc.ID(cb.Int(5)), user, pass)
}

// The receive's completion: (NSURLSessionWebSocketMessage *, NSError *).
func onReceive(ctx uintptr, cb *ffi.CB) {
	c := connOf(ctx)
	if c == nil {
		return
	}
	fr := cb.Frame()
	m, ns := cb.Int(1), cb.Int(2)
	if m == 0 {
		var err error = errors.New("ws: receive failed")
		if ns != 0 {
			err = nsError(fr, ns)
		}
		c.recv <- recvResult{err: err}
		return
	}
	kind, d := Binary, uintptr(0)
	if fr.Reset().Arg(m).Arg(sys.selType).Call(cf.MsgAddr()).R0() == msgData {
		d = fr.Reset().Arg(m).Arg(sys.selData).Call(cf.MsgAddr()).R0()
	} else {
		kind = Text
		s := fr.Reset().Arg(m).Arg(sys.selString).Call(cf.MsgAddr()).R0()
		d = fr.Reset().Arg(s).Arg(sys.selDataEnc).Arg(utf8).Call(cf.MsgAddr()).R0() // autoreleased in the queue's pool
	}
	n := 0
	if d != 0 {
		n = int(fr.Reset().Arg(d).Arg(sys.selLength).Call(cf.MsgAddr()).R0())
	}
	if int64(n) > c.max {
		c.recv <- recvResult{err: ErrTooLarge}
		return
	}
	buf := c.rbuf
	if cap(buf) < n {
		buf = make([]byte, 0, n)
	}
	buf = buf[:n]
	if n > 0 {
		p := fr.Reset().Arg(d).Arg(sys.selBytes).Call(cf.MsgAddr()).R0()
		copy(buf, unsafe.Slice((*byte)(ffi.PtrFromC(p)), n))
	}
	c.rbuf = buf
	c.recv <- recvResult{kind: kind}
}

// The send's completion: (NSError *).
func onSend(ctx uintptr, cb *ffi.CB) {
	c := connOf(ctx)
	if c == nil {
		return
	}
	var err error
	if ns := cb.Int(1); ns != 0 {
		err = nsError(cb.Frame(), ns)
	}
	c.sent <- err
}

// ---- The connection ----

// settle waits, up to closeWait, for the pong of a ping, unless a write is in
// flight (it holds wmu, and its frames went out after the handshake). A task
// cancelled with a close code just after didOpenWithProtocol: may drop the
// close frame and end the TCP connection without it (the server sees 1006):
// measured on an M-series Mac under CPU load, about 1 % of Dial-then-Close,
// none once a ping had its pong, none 50 ms after the open.
func (c *darwinConn) settle(fr *ffi.Frame) {
	if !c.wmu.TryLock() {
		return
	}
	defer c.wmu.Unlock()
	req, err := sys.sendKind.New(ctxOf(c))
	if err != nil {
		return
	}
	fr.Reset().Arg(c.task).Arg(sys.selPing).Arg(uintptr(req.Block())).Call(cf.MsgAddr())
	req.Sent()
	t := time.NewTimer(closeWait)
	select {
	case <-c.sent: // the pong, or the error of a connection already gone
	case <-t.C:
	}
	t.Stop()
}

func osDial(ctx context.Context, rawURL string, o Options) (backend, string, error) {
	var px *nsurl.Proxy
	if p := o.Proxy; p != nil {
		kind := int(p.Kind)
		if p.Kind == 0 {
			kind = nsurl.ProxyHTTP
		}
		if p.Kind == fetch.ProxyHTTPS {
			return nil, "", fmt.Errorf("%w: HTTPS proxy on macOS", ErrUnsupported)
		}
		px = &nsurl.Proxy{Kind: kind, Host: p.Host, Port: p.Port, User: p.User, Password: p.Password}
	}
	if _, err := url.Parse(rawURL); err != nil {
		return nil, "", fmt.Errorf("%w: %q", ErrBadURL, rawURL)
	}
	if err := load(); err != nil {
		return nil, "", err
	}
	c := &darwinConn{
		max:       o.MaxMessage,
		recv:      make(chan recvResult, 1),
		sent:      make(chan error, 1),
		opened:    make(chan string, 1),
		completed: make(chan struct{}),
		closedCh:  make(chan struct{}),
	}
	if px != nil {
		c.user, c.password = px.User, px.Password
	}
	if err := register(c); err != nil {
		return nil, "", err
	}
	fr := cf.Acquire()
	defer cf.Release(fr)
	if err := c.start(fr, rawURL, o, px); err != nil {
		c.release(fr)
		return nil, "", err
	}
	select {
	case sub := <-c.opened:
		return c, sub, nil
	case <-c.completed:
		c.release(fr)
		if c.status != 0 && c.status != 101 {
			return nil, "", &HandshakeError{Status: c.status}
		}
		if c.doneErr != nil {
			return nil, "", c.doneErr
		}
		return nil, "", errors.New("ws: the connection closed during the handshake")
	case <-ctx.Done():
		c.release(fr)
		return nil, "", ctx.Err()
	}
}

func (c *darwinConn) start(fr *ffi.Frame, rawURL string, o Options, px *nsurl.Proxy) error {
	pop := cf.Pool(fr)
	defer pop()
	str := cf.String(fr, rawURL)
	u := nsurl.Msg(fr, nsurl.Classes.NSURL, "URLWithString:", str)
	cf.CFRelease(fr, str)
	if u == 0 {
		return fmt.Errorf("%w: %q", ErrBadURL, rawURL)
	}
	req := nsurl.Msg(fr, nsurl.Classes.Request, "requestWithURL:", u)
	for k, v := range o.Headers {
		nsurl.SetHeader(fr, req, k, v)
	}
	if len(o.Subprotocols) > 0 {
		nsurl.SetHeader(fr, req, "Sec-WebSocket-Protocol", strings.Join(o.Subprotocols, ", "))
	}
	cfg, err := nsurl.Config(fr, idleLimit, px)
	if err != nil {
		return err
	}
	c.delegate = nsurl.Msg(fr, nsurl.Msg(fr, sys.delegate, "alloc"), "init")
	c.queue = nsurl.Msg(fr, nsurl.Msg(fr, nsurl.Classes.Queue, "alloc"), "init")
	nsurl.Msg(fr, c.queue, "setMaxConcurrentOperationCount:", 1)
	delegates.Store(c.delegate, c)
	c.session = nsurl.Msg(fr, nsurl.Msg(fr, nsurl.Classes.Session, "sessionWithConfiguration:delegate:delegateQueue:", cfg, c.delegate, c.queue), "retain")
	c.task = nsurl.Msg(fr, nsurl.Msg(fr, c.session, "webSocketTaskWithRequest:", req), "retain")
	if c.task == 0 {
		return errors.New("ws: NSURLSession made no WebSocket task")
	}
	nsurl.Msg(fr, c.task, "setMaximumMessageSize:", uintptr(c.max))
	nsurl.Msg(fr, c.task, "resume")
	return nil
}

// release cancels the task and lets go of everything; the delegate's
// callbacks find nothing afterwards.
func (c *darwinConn) release(fr *ffi.Frame) {
	pop := cf.Pool(fr)
	if c.task != 0 {
		nsurl.Msg(fr, c.task, "cancel")
	}
	if c.session != 0 {
		nsurl.Msg(fr, c.session, "invalidateAndCancel")
	}
	delegates.Delete(c.delegate)
	unregister(c)
	for _, o := range []uintptr{c.task, c.session, c.queue, c.delegate} {
		cf.CFRelease(fr, o)
	}
	c.task, c.session, c.queue, c.delegate = 0, 0, 0, 0
	pop()
}

func (c *darwinConn) read(buf []byte) (MessageKind, []byte, error) {
	if c.rerr != nil {
		return 0, buf, c.rerr
	}
	if c.closing.Load() {
		return 0, buf, ErrClosed
	}
	c.rbuf = buf
	req, err := sys.recvKind.New(ctxOf(c))
	if err != nil {
		return 0, buf, fmt.Errorf("ws: %w", err)
	}
	fr := cf.Acquire()
	fr.Reset().Arg(c.task).Arg(sys.selReceive).Arg(uintptr(req.Block())).Call(cf.MsgAddr())
	req.Sent()
	cf.Release(fr)
	var r recvResult
	select {
	case r = <-c.recv:
	case <-c.closedCh:
		if req.Cancel() {
			c.rerr = ErrClosed
			c.dead.Store(true)
			return 0, buf, c.rerr
		}
		// Its completion is running: wait for it to hand the buffer back.
		t := time.NewTimer(closeWait)
		select {
		case r = <-c.recv:
		case <-t.C:
			r.err = ErrClosed
		}
		t.Stop()
	}
	if r.err != nil {
		c.rerr = c.readError(r.err)
		c.dead.Store(true)
		return 0, buf, c.rerr
	}
	return r.kind, c.rbuf, nil
}

// readError turns a failed receive into the read's error: ErrClosed after
// Close, the server's close frame as a *CloseError, ErrTooLarge, or a
// dropped connection.
func (c *darwinConn) readError(err error) error {
	if c.closing.Load() {
		return ErrClosed
	}
	if errors.Is(err, ErrTooLarge) {
		return err
	}
	code, reason := c.closeStatus()
	if code == 0 {
		// The close code may land just after the receive fails.
		t := time.NewTimer(200 * time.Millisecond)
		select {
		case <-c.completed:
		case <-t.C:
		}
		t.Stop()
		code, reason = c.closeStatus()
	}
	if code != 0 {
		return &CloseError{Code: CloseCode(code), Reason: reason}
	}
	var se *sysError
	if errors.As(err, &se) && se.domain == "NSPOSIXErrorDomain" && se.code == 40 { // EMSGSIZE: over maximumMessageSize
		return ErrTooLarge
	}
	return &CloseError{Code: CloseAbnormal}
}

// closeStatus is the task's closeCode and closeReason (0 before a close).
func (c *darwinConn) closeStatus() (int, string) {
	fr := cf.Acquire()
	defer cf.Release(fr)
	pop := cf.Pool(fr)
	defer pop()
	code := int(fr.Reset().Arg(c.task).Arg(sys.selCloseCode).Call(cf.MsgAddr()).R0())
	var reason string
	if d := fr.Reset().Arg(c.task).Arg(sys.selCloseReason).Call(cf.MsgAddr()).R0(); d != 0 {
		reason = string(cf.DataBytes(fr, d))
	}
	return code, reason
}

func (c *darwinConn) write(kind MessageKind, data []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.closing.Load() || c.dead.Load() {
		return ErrClosed
	}
	fr := cf.Acquire()
	defer cf.Release(fr)
	var obj, m uintptr
	ptr := uintptr(0)
	if len(data) > 0 {
		ptr = uintptr(unsafe.Pointer(&data[0]))
	}
	if kind == Binary {
		// The bytes stay ours: the send completes before WriteMessage
		// returns.
		obj = fr.Reset().Arg(nsurl.Classes.Data).Arg(sys.selAlloc).Call(cf.MsgAddr()).R0()
		obj = fr.Reset().Arg(obj).Arg(sys.selInitBytesNoCopy).Arg(ptr).Arg(uintptr(len(data))).Arg(0).Call(cf.MsgAddr()).R0()
		m = fr.Reset().Arg(nsurl.Classes.WSMessage).Arg(sys.selAlloc).Call(cf.MsgAddr()).R0()
		m = fr.Reset().Arg(m).Arg(sys.selInitData).Arg(obj).Call(cf.MsgAddr()).R0()
	} else {
		obj = fr.Reset().Arg(nsurl.Classes.String).Arg(sys.selAlloc).Call(cf.MsgAddr()).R0()
		obj = fr.Reset().Arg(obj).Arg(sys.selInitStrBytes).Arg(ptr).Arg(uintptr(len(data))).Arg(utf8).Call(cf.MsgAddr()).R0()
		if obj == 0 {
			return errors.New("ws: a Text message that is not UTF-8")
		}
		m = fr.Reset().Arg(nsurl.Classes.WSMessage).Arg(sys.selAlloc).Call(cf.MsgAddr()).R0()
		m = fr.Reset().Arg(m).Arg(sys.selInitString).Arg(obj).Call(cf.MsgAddr()).R0()
	}
	req, err := sys.sendKind.New(ctxOf(c))
	if err != nil {
		cf.CFRelease(fr, m)
		cf.CFRelease(fr, obj)
		return fmt.Errorf("ws: %w", err)
	}
	fr.Reset().Arg(c.task).Arg(sys.selSend).Arg(m).Arg(uintptr(req.Block())).Call(cf.MsgAddr())
	req.Sent()
	cf.CFRelease(fr, m)
	cf.CFRelease(fr, obj)
	// Wait for the completion: the system reads data until then. Close
	// cancels the task, which completes the send with an error.
	select {
	case err = <-c.sent:
	case <-c.closedCh:
		t := time.NewTimer(closeWait)
		select {
		case err = <-c.sent:
		case <-t.C:
			err = ErrClosed
		}
		t.Stop()
	}
	runtime.KeepAlive(data)
	if err != nil {
		if c.closing.Load() || c.dead.Load() {
			return ErrClosed
		}
		return err
	}
	return nil
}

func (c *darwinConn) close(code CloseCode, reason string) error {
	c.closeOnce.Do(func() {
		c.closing.Store(true)
		fr := cf.Acquire()
		defer cf.Release(fr)
		c.settle(fr)
		pop := cf.Pool(fr)
		d := uintptr(0)
		if reason != "" {
			d = cf.Data(fr, []byte(reason))
		}
		nsurl.Msg(fr, c.task, "cancelWithCloseCode:reason:", uintptr(code), d)
		cf.CFRelease(fr, d)
		pop()
		close(c.closedCh)
		t := time.NewTimer(closeWait)
		select {
		case <-c.completed:
		case <-t.C:
		}
		t.Stop()
		c.wmu.Lock() // a pending send has completed
		c.release(fr)
		c.wmu.Unlock()
	})
	return nil
}
