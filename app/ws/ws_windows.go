//go:build windows

package ws

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gonativeui/app/fetch"
	"gonativeui/internal/winhttp"
)

// Windows: WinHTTP's WebSocket API, synchronous, as probe P8 proved it on
// both runners. A message arrives in pieces (BinaryFragment ... then
// BinaryMessage, up to 32 KiB each in P8), received straight into the
// caller's buffer. WinHTTP answers pings and shows no frame lengths, so
// the length checks here are the message limit's; the suite's 32 MiB round
// trips compare every byte.

type winConn struct {
	mu     sync.Mutex // guards the handles' closing
	ses    uintptr
	conn   uintptr
	ws     uintptr
	closed bool

	max int64
	// WinHttpWebSocketReceive's out parameters: in the heap object, so a
	// receive allocates nothing.
	got, typ uint32
	rerr     error

	wmu sync.Mutex

	closing   atomic.Bool
	dead      atomic.Bool // the reader saw the end: a close frame, an error
	reading   atomic.Bool
	gotClose  chan struct{}
	gotOnce   sync.Once
	closeOnce sync.Once
}

// reqHandle is the handshake's request handle, closed by ctx or the dial.
type reqHandle struct {
	mu     sync.Mutex
	h      uintptr
	closed bool
}

func (r *reqHandle) close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed {
		r.closed = true
		winhttp.CloseHandle(r.h)
	}
}

func osDial(ctx context.Context, rawURL string, o Options) (backend, string, error) {
	if err := winhttp.Load(); err != nil {
		return nil, "", err
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return nil, "", fmt.Errorf("%w: %q", ErrBadURL, rawURL)
	}
	var px winhttp.Proxy
	if p := o.Proxy; p != nil {
		switch p.Kind {
		case fetch.ProxyNone:
			px.None = true
		case 0, fetch.ProxyHTTP:
			px = winhttp.Proxy{Host: p.Host, Port: p.Port, User: p.User, Password: p.Password}
		default:
			return nil, "", fmt.Errorf("%w: SOCKS5 and HTTPS proxies on Windows (WinHTTP has none)", ErrUnsupported)
		}
	}
	secure := u.Scheme == "wss"
	port := 80
	if secure {
		port = 443
	}
	if p := u.Port(); p != "" {
		if port, err = strconv.Atoi(p); err != nil {
			return nil, "", fmt.Errorf("%w: %q", ErrBadURL, rawURL)
		}
	}
	c := &winConn{max: o.MaxMessage, gotClose: make(chan struct{})}
	ok := false
	defer func() {
		if !ok {
			c.release()
		}
	}()
	if c.ses, err = winhttp.Session("gonativeui-ws/1", px); err != nil {
		return nil, "", err
	}
	if c.conn, err = winhttp.Connect(c.ses, u.Hostname(), port); err != nil {
		return nil, "", err
	}
	h, err := winhttp.OpenRequest(c.conn, "GET", u.RequestURI(), secure, false)
	if err != nil {
		return nil, "", err
	}
	rh := &reqHandle{h: h}
	defer rh.close()
	stop := context.AfterFunc(ctx, rh.close)
	defer stop()

	var lines strings.Builder
	for k, v := range o.Headers {
		lines.WriteString(k + ": " + v + "\r\n")
	}
	if len(o.Subprotocols) > 0 {
		lines.WriteString("Sec-WebSocket-Protocol: " + strings.Join(o.Subprotocols, ", ") + "\r\n")
	}
	if err := winhttp.AddHeaders(h, lines.String()); err != nil {
		return nil, "", err
	}
	if px.User != "" || px.Password != "" {
		winhttp.SetProxyCredentials(h, px.User, px.Password)
	}
	if err := winhttp.UpgradeToWebSocket(h); err != nil {
		return nil, "", err
	}
	status := 0
	for attempt := 0; ; attempt++ {
		if err := winhttp.Send(h, 0); err != nil {
			return nil, "", err
		}
		if err := winhttp.Receive(h); err != nil {
			return nil, "", err
		}
		if status, err = winhttp.Status(h); err != nil {
			return nil, "", err
		}
		if status == 407 && attempt == 0 && px.User != "" && winhttp.AnswerProxyChallenge(h, px.User, px.Password) == nil {
			continue
		}
		break
	}
	if status != 101 {
		return nil, "", &HandshakeError{Status: status}
	}
	sub := winhttp.Header(h, "Sec-WebSocket-Protocol")
	if c.ws, err = winhttp.CompleteUpgrade(h); err != nil {
		return nil, "", err
	}
	if !stop() {
		return nil, "", ctx.Err()
	}
	ok = true
	return c, sub, nil
}

// release closes the handles, once; a call blocked on them ends.
func (c *winConn) release() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	winhttp.CloseHandle(c.ws)
	winhttp.CloseHandle(c.conn)
	winhttp.CloseHandle(c.ses)
}

func (c *winConn) read(buf []byte) (MessageKind, []byte, error) {
	if c.rerr != nil {
		return 0, buf, c.rerr
	}
	if c.closing.Load() {
		return 0, buf, ErrClosed
	}
	c.reading.Store(true)
	defer c.reading.Store(false)
	var kind MessageKind
	for {
		if len(buf) == cap(buf) {
			if int64(len(buf)) > c.max {
				return 0, buf, c.tooBig()
			}
			buf = slices.Grow(buf, int(min(max(int64(len(buf)), 4<<10), c.max+1-int64(len(buf)))))
		}
		err := winhttp.WSReceive(c.ws, buf[len(buf):cap(buf)], &c.got, &c.typ)
		if err != nil {
			return 0, buf, c.fail(err)
		}
		buf = buf[:len(buf)+int(c.got)]
		var k MessageKind
		switch c.typ {
		case winhttp.BinaryMessage, winhttp.BinaryFragment:
			k = Binary
		case winhttp.UTF8Message, winhttp.UTF8Fragment:
			k = Text
		case winhttp.CloseBuffer:
			return 0, buf[:0], c.closedByServer()
		}
		if kind != 0 && k != kind {
			return 0, buf, c.fail(errProtocol)
		}
		kind = k
		if int64(len(buf)) > c.max {
			return 0, buf, c.tooBig()
		}
		if c.typ == winhttp.BinaryMessage || c.typ == winhttp.UTF8Message {
			return kind, buf, nil
		}
	}
}

func (c *winConn) tooBig() error {
	winhttp.WSShutdown(c.ws, uint16(CloseTooBig), nil)
	c.rerr = ErrTooLarge
	c.dead.Store(true)
	c.release()
	return c.rerr
}

// closedByServer reads the close frame's status. A close this end started
// is ErrClosed; one the server started is answered and is a *CloseError.
func (c *winConn) closedByServer() error {
	code, reason, _ := winhttp.WSCloseStatus(c.ws)
	c.dead.Store(true)
	c.gotOnce.Do(func() { close(c.gotClose) })
	if c.closing.Load() {
		c.rerr = ErrClosed
		return c.rerr
	}
	ce := &CloseError{Code: CloseCode(code), Reason: reason}
	if code == 0 {
		ce.Code = CloseNoStatus
	}
	answer := ce.Code
	if !sendable(answer) {
		answer = CloseNormal
	}
	winhttp.WSShutdown(c.ws, uint16(answer), nil)
	c.rerr = ce
	return ce
}

func (c *winConn) fail(err error) error {
	c.dead.Store(true)
	switch {
	case c.closing.Load():
		c.rerr = ErrClosed
	case errors.Is(err, errProtocol):
		winhttp.WSShutdown(c.ws, uint16(CloseProtocolError), nil)
		c.rerr = err
	case winhttp.Is(err, winhttp.ErrnoInvalidHandle), winhttp.Is(err, winhttp.ErrnoCancelled):
		c.rerr = ErrClosed
	default:
		c.rerr = &CloseError{Code: CloseAbnormal}
	}
	return c.rerr
}

func (c *winConn) write(kind MessageKind, data []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.closing.Load() || c.dead.Load() {
		return ErrClosed
	}
	typ := winhttp.BinaryMessage
	if kind == Text {
		typ = winhttp.UTF8Message
	}
	if err := winhttp.WSSend(c.ws, typ, data); err != nil {
		if c.closing.Load() || c.dead.Load() {
			return ErrClosed
		}
		return fmt.Errorf("ws: %w", err)
	}
	return nil
}

func (c *winConn) close(code CloseCode, reason string) error {
	c.closeOnce.Do(func() {
		c.closing.Store(true)
		if !c.dead.Load() {
			r := []byte(reason)
			if c.reading.Load() {
				// The pending read sees the server's close frame.
				winhttp.WSShutdown(c.ws, uint16(code), r)
				t := time.NewTimer(closeWait)
				select {
				case <-c.gotClose:
				case <-t.C:
				}
				t.Stop()
			} else {
				done := make(chan struct{})
				go func() {
					winhttp.WSClose(c.ws, uint16(code), r) // sends, waits for the server's
					close(done)
				}()
				t := time.NewTimer(closeWait)
				select {
				case <-done:
				case <-t.C:
				}
				t.Stop()
			}
		}
		c.release()
	})
	return nil
}
