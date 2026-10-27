// Package ws is a WebSocket client (RFC 6455) through the system's network
// stack: the phase 8 plan's N2 and D73. a3-app's window keeps one socket to
// its agent for events, terminal bytes and the port tunnel.
//
//	c, err := ws.Dial(ctx, "wss://agent.example/api/ws", ws.Options{
//		Headers:    map[string]string{"Authorization": "Bearer " + token},
//		MaxMessage: 32 << 20,
//	})
//	defer c.Close(ws.CloseNormal, "")
//
//	go func() { // the reader: one goroutine
//		var buf []byte
//		for {
//			kind, msg, err := c.ReadMessage(buf[:0]) // grows buf once, then reuses it
//			if err != nil {
//				return // a *CloseError after a close frame
//			}
//			buf = msg
//			handle(kind, msg) // copy what outlives the next read
//		}
//	}()
//	err = c.WriteMessage(ws.Text, []byte(`{"type":"ping"}`)) // blocks while the socket's buffers are full
//
// Pings are answered by the system; there is no compression extension.
// Messages arrive whole: fragments are joined before ReadMessage returns.
//
// # Threads
//
// One goroutine reads (ReadMessage) and one writes (WriteMessage) at a
// time; Close may be called from any goroutine and ends a pending read or
// write. Never call them on the UI thread: post what arrives to the app
// (app.Post) or into a task.
//
// # Allocation
//
// ReadMessage reads into the caller's buffer, growing it only when a
// message is larger than its capacity, so a steady stream of messages
// allocates nothing on the receive path (N2's gate).
//
// # What each OS does
//
//   - macOS: NSURLSessionWebSocketTask on the D60 session machinery.
//   - Windows: WinHTTP's WebSocket API (WinHttpWebSocketCompleteUpgrade,
//     Send, Receive, Close), with the system's proxy and TLS.
//   - Linux: net/http's upgrade with our own framing.
//   - Until N2 lands, Dial returns ErrUnsupported everywhere (errors.Is
//     errors.ErrUnsupported). This package never links net/http or
//     crypto/tls on darwin or windows.
package ws

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	// ErrUnsupported: no WebSocket client here yet. It matches
	// errors.ErrUnsupported.
	ErrUnsupported = fmt.Errorf("ws: %w", errors.ErrUnsupported)
	// ErrBadURL means the URL is not an absolute ws or wss URL.
	ErrBadURL = errors.New("ws: not a ws or wss URL")
	// ErrClosed means the connection is closed (by Close, or after the close
	// handshake); a close frame from the server is a *CloseError instead.
	ErrClosed = errors.New("ws: connection closed")
	// ErrTooLarge means a message exceeds Options.MaxMessage; the
	// connection is closed with CloseTooBig.
	ErrTooLarge = errors.New("ws: message larger than MaxMessage")
)

// MessageKind is a data message's type.
type MessageKind uint8

// The message kinds.
const (
	// Text is a UTF-8 text message.
	Text MessageKind = iota + 1
	// Binary is a binary message.
	Binary
)

// CloseCode is a close frame's status code (RFC 6455 §7.4).
type CloseCode uint16

// Close codes an app sends or meets.
const (
	// CloseNormal is a normal closure (1000).
	CloseNormal CloseCode = 1000
	// CloseGoingAway is an endpoint going away: the app quitting, the server
	// restarting (1001).
	CloseGoingAway CloseCode = 1001
	// CloseProtocolError is a protocol error (1002).
	CloseProtocolError CloseCode = 1002
	// CloseNoStatus is a close frame without a code (1005; never sent).
	CloseNoStatus CloseCode = 1005
	// CloseAbnormal is a connection lost without a close frame (1006; never
	// sent).
	CloseAbnormal CloseCode = 1006
	// CloseTooBig is a message too big to process (1009).
	CloseTooBig CloseCode = 1009
)

// CloseError is ReadMessage's error once the server closed the connection:
// its code and reason.
type CloseError struct {
	// Code is the server's status code (CloseNoStatus when it sent none,
	// CloseAbnormal when the connection dropped).
	Code CloseCode
	// Reason is the server's text.
	Reason string
}

// Error describes the close.
func (e *CloseError) Error() string { return fmt.Sprintf("ws: closed: %d %s", e.Code, e.Reason) }

// HandshakeError is Dial's error when the server answered the upgrade with
// something other than 101.
type HandshakeError struct {
	// Status is the HTTP status code.
	Status int
}

// Error describes the status.
func (e *HandshakeError) Error() string { return fmt.Sprintf("ws: handshake: status %d", e.Status) }

// DefaultMaxMessage is the message size limit when Options.MaxMessage is 0.
const DefaultMaxMessage = 16 << 20

// Options configure Dial.
type Options struct {
	// Headers are extra handshake headers ("Authorization", "Origin").
	Headers map[string]string
	// Subprotocols are offered in Sec-WebSocket-Protocol; Conn.Subprotocol
	// is the one chosen.
	Subprotocols []string
	// Timeout bounds the handshake (0: the context's only).
	Timeout time.Duration
	// MaxMessage is the largest message accepted, in bytes (0:
	// DefaultMaxMessage).
	MaxMessage int64
}

// Conn is an open WebSocket connection.
type Conn struct {
	sub string
	be  backend
}

// backend is the OS's connection.
type backend interface {
	read(buf []byte) (MessageKind, []byte, error)
	write(kind MessageKind, data []byte) error
	close(code CloseCode, reason string) error
}

// Dial opens a WebSocket connection to rawURL (ws: or wss:) and completes
// the handshake. It blocks; ctx ends the attempt.
func Dial(ctx context.Context, rawURL string, o Options) (*Conn, error) {
	if !strings.HasPrefix(rawURL, "ws://") && !strings.HasPrefix(rawURL, "wss://") {
		return nil, fmt.Errorf("%w: %q", ErrBadURL, rawURL)
	}
	if o.MaxMessage <= 0 {
		o.MaxMessage = DefaultMaxMessage
	}
	if o.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.Timeout)
		defer cancel()
	}
	be, sub, err := osDial(ctx, rawURL, o)
	if err != nil {
		return nil, err
	}
	return &Conn{sub: sub, be: be}, nil
}

// Subprotocol is the subprotocol the server chose ("" none).
func (c *Conn) Subprotocol() string { return c.sub }

// ReadMessage reads the next whole message, appending it to buf[:0] and
// returning the result (buf itself when it had the capacity). Pass the
// slice back in to reuse it.
func (c *Conn) ReadMessage(buf []byte) (MessageKind, []byte, error) { return c.be.read(buf[:0]) }

// WriteMessage sends one message, blocking until the system has taken it
// (backpressure).
func (c *Conn) WriteMessage(kind MessageKind, data []byte) error {
	if kind != Text && kind != Binary {
		return errors.New("ws: unknown message kind")
	}
	return c.be.write(kind, data)
}

// Close sends a close frame with code and reason (at most 123 bytes), waits
// briefly for the server's, and releases the connection. A second Close
// does nothing.
func (c *Conn) Close(code CloseCode, reason string) error {
	if len(reason) > 123 {
		reason = reason[:123]
	}
	return c.be.close(code, reason)
}
