// Package dbus is a small D-Bus client (D31): a session-bus connection over a
// Unix socket with EXTERNAL authentication, method calls with replies and
// errors, signal subscriptions, and enough server side (owning a name,
// answering method calls, emitting signals) to write a fake service in a
// test. It speaks little-endian messages without file descriptors and uses
// only the standard library.
//
// A connection reads on its own goroutine. Calls block the calling goroutine,
// never the reader. Signal callbacks run on the reader goroutine in the order
// the bus sent the signals, so they must not block and must not call
// [Conn.Call]; method handlers run on a goroutine of their own.
//
// The portal requests of phase 5B reuse this: subscribe to the Response signal
// of the request handle with [Conn.Subscribe] (it returns after the bus has
// installed the match), then make the call.
package dbus

import (
	"bufio"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ErrNoBus means no session bus address is known.
var ErrNoBus = errors.New("dbus: no session bus (DBUS_SESSION_BUS_ADDRESS is not set)")

// Well-known names.
const (
	busName   = "org.freedesktop.DBus"
	busPath   = "/org/freedesktop/DBus"
	handshake = 5 * time.Second
)

// Conn is a connection to a bus.
type Conn struct {
	nc   net.Conn
	wmu  sync.Mutex // serialises writes
	last atomic.Uint32

	name string // the unique name the bus gave us

	mu      sync.Mutex
	pending map[uint32]chan *Message
	objects map[objKey]Handler

	subs atomic.Pointer[[]*Subscription] // copy on write

	done chan struct{}
	err  error // set before done is closed
	once sync.Once
}

type objKey struct {
	path  ObjectPath
	iface string
}

// SessionAddress returns the session bus address: DBUS_SESSION_BUS_ADDRESS,
// or $XDG_RUNTIME_DIR/bus when that variable is unset and the socket exists
// (what libdbus does on systemd systems).
func SessionAddress() (string, error) {
	if a := os.Getenv("DBUS_SESSION_BUS_ADDRESS"); a != "" {
		return a, nil
	}
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		p := d + "/bus"
		if fi, err := os.Stat(p); err == nil && fi.Mode()&os.ModeSocket != 0 {
			return "unix:path=" + p, nil
		}
	}
	return "", ErrNoBus
}

// DialSession connects to the session bus.
func DialSession(ctx context.Context) (*Conn, error) {
	a, err := SessionAddress()
	if err != nil {
		return nil, err
	}
	return Dial(ctx, a)
}

// Dial connects to the bus at address (a ';'-separated list of
// "unix:path=...", "unix:abstract=..." entries; the first that works is used),
// authenticates and says Hello. ctx bounds the whole setup.
func Dial(ctx context.Context, address string) (*Conn, error) {
	var last error = errors.New("dbus: empty address")
	for _, entry := range strings.Split(address, ";") {
		network, addr, err := parseAddress(entry)
		if err != nil {
			last = err
			continue
		}
		var d net.Dialer
		nc, err := d.DialContext(ctx, network, addr)
		if err != nil {
			last = err
			continue
		}
		c, err := newConn(ctx, nc)
		if err != nil {
			nc.Close()
			last = err
			continue
		}
		return c, nil
	}
	return nil, last
}

// parseAddress turns one address entry into a network and address for net.Dial.
func parseAddress(entry string) (network, addr string, err error) {
	transport, rest, ok := strings.Cut(entry, ":")
	if !ok || transport != "unix" {
		return "", "", fmt.Errorf("dbus: unsupported transport in %q", entry)
	}
	for _, kv := range strings.Split(rest, ",") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if v, err = unescape(v); err != nil {
			return "", "", err
		}
		switch k {
		case "path":
			return "unix", v, nil
		case "abstract":
			return "unix", "@" + v, nil // Go's spelling of an abstract socket
		}
	}
	return "", "", fmt.Errorf("dbus: no path or abstract name in %q", entry)
}

// unescape decodes the %xx escapes of a D-Bus address value.
func unescape(s string) (string, error) {
	if !strings.Contains(s, "%") {
		return s, nil
	}
	var b []byte
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			b = append(b, s[i])
			continue
		}
		if i+3 > len(s) {
			return "", errors.New("dbus: bad escape in address")
		}
		x, err := hex.DecodeString(s[i+1 : i+3])
		if err != nil {
			return "", errors.New("dbus: bad escape in address")
		}
		b = append(b, x[0])
		i += 2
	}
	return string(b), nil
}

func newConn(ctx context.Context, nc net.Conn) (*Conn, error) {
	deadline := time.Now().Add(handshake)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	nc.SetDeadline(deadline)
	// EXTERNAL authentication with our uid, and BEGIN in the same write: the
	// bus handles its input in order, so there is no round trip to wait for.
	uid := hex.EncodeToString([]byte(strconv.Itoa(os.Getuid())))
	if _, err := nc.Write([]byte("\x00AUTH EXTERNAL " + uid + "\r\nBEGIN\r\n")); err != nil {
		return nil, err
	}
	br := bufio.NewReaderSize(nc, 4096)
	line, err := br.ReadSlice('\n')
	if err != nil {
		return nil, fmt.Errorf("dbus: authentication: %w", err)
	}
	if !strings.HasPrefix(string(line), "OK ") {
		return nil, fmt.Errorf("dbus: authentication refused: %q", strings.TrimSpace(string(line)))
	}
	c := &Conn{nc: nc, pending: map[uint32]chan *Message{}, objects: map[objKey]Handler{}, done: make(chan struct{})}
	go c.readLoop(br)
	r, err := c.CallContext(ctx, busName, busPath, busName, "Hello", "")
	if err != nil {
		c.Close()
		return nil, err
	}
	if len(r.Body) != 1 {
		c.Close()
		return nil, errors.New("dbus: bad Hello reply")
	}
	c.name, _ = r.Body[0].(string)
	nc.SetDeadline(time.Time{})
	return c, nil
}

// Name is the unique name the bus gave this connection (":1.42").
func (c *Conn) Name() string { return c.name }

// Done is closed when the connection ends.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err is why the connection ended (nil while it is open).
func (c *Conn) Err() error {
	select {
	case <-c.done:
		return c.err
	default:
		return nil
	}
}

// Close ends the connection; calls in flight fail.
func (c *Conn) Close() error {
	c.fail(errors.New("dbus: connection closed"))
	return nil
}

func (c *Conn) fail(err error) {
	c.once.Do(func() {
		c.err = err
		c.nc.Close()
		close(c.done)
	})
}

func (c *Conn) readLoop(br *bufio.Reader) {
	for {
		m, err := readMessage(br)
		if err != nil {
			c.fail(err)
			return
		}
		c.dispatch(m)
	}
}

func (c *Conn) dispatch(m *Message) {
	switch m.Type {
	case TypeMethodReturn, TypeError:
		c.mu.Lock()
		ch := c.pending[m.ReplySerial]
		delete(c.pending, m.ReplySerial)
		c.mu.Unlock()
		if ch != nil {
			ch <- m // buffered
		}
	case TypeSignal:
		if p := c.subs.Load(); p != nil {
			for _, s := range *p {
				if s.matches(m) {
					s.fn(m)
				}
			}
		}
	case TypeMethodCall:
		c.mu.Lock()
		h := c.objects[objKey{m.Path, m.Interface}]
		c.mu.Unlock()
		go c.serve(h, m)
	}
}

// send marshals m with a fresh serial (when it has none) and writes it.
func (c *Conn) send(m *Message) error {
	if m.Serial == 0 {
		m.Serial = c.last.Add(1)
	}
	b, err := m.marshal()
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.nc.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := c.nc.Write(b); err != nil {
		c.fail(err)
		return err
	}
	return nil
}

// Call calls a method and waits for the reply (a *[Error] for an error reply).
func (c *Conn) Call(dest string, path ObjectPath, iface, member, sig string, args ...any) (*Message, error) {
	return c.CallContext(context.Background(), dest, path, iface, member, sig, args...)
}

// CallContext is Call that gives up when ctx ends.
func (c *Conn) CallContext(ctx context.Context, dest string, path ObjectPath, iface, member, sig string, args ...any) (*Message, error) {
	m := &Message{Type: TypeMethodCall, Destination: dest, Path: path, Interface: iface, Member: member, Signature: sig, Body: args}
	m.Serial = c.last.Add(1)
	ch := make(chan *Message, 1)
	c.mu.Lock()
	select {
	case <-c.done:
		c.mu.Unlock()
		return nil, c.err
	default:
	}
	c.pending[m.Serial] = ch
	c.mu.Unlock()
	forget := func() {
		c.mu.Lock()
		delete(c.pending, m.Serial)
		c.mu.Unlock()
	}
	if err := c.send(m); err != nil {
		forget()
		return nil, err
	}
	select {
	case r := <-ch:
		if r.Type == TypeError {
			return nil, &Error{Name: r.ErrorName, Body: r.Body}
		}
		return r, nil
	case <-ctx.Done():
		forget()
		return nil, ctx.Err()
	case <-c.done:
		return nil, c.err
	}
}

// ---- signals ------------------------------------------------------------------

// Match selects signals. Empty fields match anything. Sender is a bus name
// (well-known or unique) that the bus itself filters on; Path, Interface,
// Member and Arg0 (the first argument, a string) are also checked here.
// Because a signal carries its sender's unique name, two subscriptions that
// differ only in Sender see each other's signals.
type Match struct {
	Sender    string
	Path      ObjectPath
	Interface string
	Member    string
	Arg0      string
}

func (m Match) rule() (string, error) {
	var b strings.Builder
	b.WriteString("type='signal'")
	for _, kv := range [][2]string{{"sender", m.Sender}, {"path", string(m.Path)}, {"interface", m.Interface}, {"member", m.Member}, {"arg0", m.Arg0}} {
		if kv[1] == "" {
			continue
		}
		if strings.ContainsAny(kv[1], "'\\") {
			return "", fmt.Errorf("dbus: %s %q cannot be matched", kv[0], kv[1])
		}
		b.WriteString("," + kv[0] + "='" + kv[1] + "'")
	}
	return b.String(), nil
}

// Subscription is an installed signal match.
type Subscription struct {
	c    *Conn
	m    Match
	rule string
	fn   func(*Message)
}

func (s *Subscription) matches(m *Message) bool {
	if s.m.Path != "" && m.Path != s.m.Path || s.m.Interface != "" && m.Interface != s.m.Interface || s.m.Member != "" && m.Member != s.m.Member {
		return false
	}
	if s.m.Arg0 != "" {
		a, ok := m.firstString()
		if !ok || a != s.m.Arg0 {
			return false
		}
	}
	return true
}

func (m *Message) firstString() (string, bool) {
	if len(m.Body) == 0 {
		return "", false
	}
	s, ok := m.Body[0].(string)
	return s, ok
}

// Subscribe delivers matching signals to fn (see the package comment for what
// fn may do). It returns once the bus has installed the match, so a call made
// afterwards cannot lose the signal it provokes.
func (c *Conn) Subscribe(ctx context.Context, m Match, fn func(*Message)) (*Subscription, error) {
	rule, err := m.rule()
	if err != nil {
		return nil, err
	}
	s := &Subscription{c: c, m: m, rule: rule, fn: fn}
	c.mu.Lock()
	old := c.subs.Load()
	var next []*Subscription
	if old != nil {
		next = append(next, *old...)
	}
	next = append(next, s)
	c.subs.Store(&next)
	c.mu.Unlock()
	if _, err := c.CallContext(ctx, busName, busPath, busName, "AddMatch", "s", rule); err != nil {
		s.remove()
		return nil, err
	}
	return s, nil
}

func (s *Subscription) remove() {
	c := s.c
	c.mu.Lock()
	defer c.mu.Unlock()
	old := c.subs.Load()
	if old == nil {
		return
	}
	next := make([]*Subscription, 0, len(*old))
	for _, x := range *old {
		if x != s {
			next = append(next, x)
		}
	}
	c.subs.Store(&next)
}

// Close removes the subscription.
func (s *Subscription) Close() {
	s.remove()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s.c.CallContext(ctx, busName, busPath, busName, "RemoveMatch", "s", s.rule)
}

// ---- serving ------------------------------------------------------------------

// Handler answers a method call on an exported object. It returns the reply's
// signature and body, or an error: an *[Error] is sent as that D-Bus error,
// any other error as org.freedesktop.DBus.Error.Failed.
type Handler func(m *Message) (sig string, body []any, err error)

// Export makes h answer method calls to path and iface.
func (c *Conn) Export(path ObjectPath, iface string, h Handler) {
	c.mu.Lock()
	c.objects[objKey{path, iface}] = h
	c.mu.Unlock()
}

// Request-name flags and replies.
const (
	NameDoNotQueue   uint32 = 4
	NameReplyPrimary uint32 = 1
)

// RequestName asks the bus for a well-known name.
func (c *Conn) RequestName(ctx context.Context, name string, flags uint32) (uint32, error) {
	r, err := c.CallContext(ctx, busName, busPath, busName, "RequestName", "su", name, flags)
	if err != nil {
		return 0, err
	}
	if len(r.Body) != 1 {
		return 0, errors.New("dbus: bad RequestName reply")
	}
	v, _ := r.Body[0].(uint32)
	return v, nil
}

// Emit broadcasts a signal.
func (c *Conn) Emit(path ObjectPath, iface, member, sig string, args ...any) error {
	return c.send(&Message{Type: TypeSignal, Flags: FlagNoReplyExpected, Path: path, Interface: iface, Member: member, Signature: sig, Body: args})
}

func (c *Conn) serve(h Handler, m *Message) {
	reply := &Message{Destination: m.Sender, ReplySerial: m.Serial, Flags: FlagNoReplyExpected}
	switch {
	case h == nil && m.Interface == "org.freedesktop.DBus.Peer" && m.Member == "Ping":
		reply.Type = TypeMethodReturn
	case h == nil:
		reply.Type, reply.ErrorName = TypeError, "org.freedesktop.DBus.Error.UnknownMethod"
		reply.Signature, reply.Body = "s", []any{"no such method " + m.Interface + "." + m.Member + " at " + string(m.Path)}
	default:
		sig, body, err := h(m)
		switch e := err.(type) {
		case nil:
			reply.Type, reply.Signature, reply.Body = TypeMethodReturn, sig, body
		case *Error:
			reply.Type, reply.ErrorName, reply.Body = TypeError, e.Name, e.Body
			reply.Signature = "s"
			if len(e.Body) == 0 {
				reply.Body = nil
				reply.Signature = ""
			}
		default:
			reply.Type, reply.ErrorName = TypeError, "org.freedesktop.DBus.Error.Failed"
			reply.Signature, reply.Body = "s", []any{err.Error()}
		}
	}
	if m.Flags&FlagNoReplyExpected != 0 {
		return
	}
	if err := c.send(reply); err != nil && c.Err() == nil {
		// The reply could not be encoded (too large, or a value that does not
		// fit its signature): the caller gets an error instead of silence.
		c.send(&Message{Type: TypeError, Flags: FlagNoReplyExpected, Destination: m.Sender, ReplySerial: m.Serial,
			ErrorName: "org.freedesktop.DBus.Error.Failed", Signature: "s", Body: []any{err.Error()}})
	}
}
