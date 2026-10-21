package inspectwire

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"
)

// Handler answers requests. Handle is called from the goroutine of the
// connection the request arrived on, one request at a time per connection,
// and may be called from several connections at once; it must not touch the
// application's state from there.
type Handler interface {
	Handle(req *Request) *Response
}

const (
	maxConns    = 8                // connections served at once
	idleTimeout = 10 * time.Minute // a connection that sends nothing for this long is closed
	writeLimit  = 10 * time.Second
)

// Serve accepts connections until the listener is closed and answers their
// requests with h. It returns when the listener is closed, after the
// connections it accepted have ended.
func (l *Listener) Serve(h Handler) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	conns := map[net.Conn]struct{}{}
	for {
		c, err := l.ln.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				slog.Error("inspect: accept", "err", err)
			}
			break
		}
		mu.Lock()
		if len(conns) >= maxConns {
			mu.Unlock()
			c.SetWriteDeadline(time.Now().Add(writeLimit))
			c.Write(Fail("", ErrBusy, "too many connections to the bridge").Marshal())
			c.Close()
			continue
		}
		conns[c] = struct{}{}
		mu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			serveConn(c, h)
			mu.Lock()
			delete(conns, c)
			mu.Unlock()
		}()
	}
	mu.Lock()
	for c := range conns {
		c.Close() // a client that waits for a reply does not keep the app's exit waiting
	}
	mu.Unlock()
	wg.Wait()
}

// serveConn serves one connection until the client leaves, sends a line that
// is too long, or the connection is closed.
func serveConn(c net.Conn, h Handler) {
	defer c.Close()
	r := bufio.NewReaderSize(c, 4096)
	for {
		c.SetReadDeadline(time.Now().Add(idleTimeout))
		line, err := ReadLine(r, MaxLine)
		resp := (*Response)(nil)
		switch {
		case errors.Is(err, ErrLineTooLong):
			resp = Fail("", ErrTooLarge, "the request is longer than 65536 bytes; send less (a shorter text, fewer steps)")
		case err != nil:
			return // the client left, or the connection timed out
		default:
			resp = Dispatch(h, line)
		}
		c.SetWriteDeadline(time.Now().Add(writeLimit))
		if _, werr := c.Write(resp.Marshal()); werr != nil || (resp.Error != nil && resp.Error.Code == ErrTooLarge) {
			return
		}
	}
}

// Dispatch decodes one request line (no newline needed) and gives it to h; a
// line that is not a request is answered with ErrBadRequest, and a panic in h
// with ErrInternal. Unknown fields are refused, so that a misspelt one is
// reported instead of ignored.
func Dispatch(h Handler, line []byte) (resp *Response) {
	var req Request
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return Fail("", ErrBadRequest, "not a request: "+err.Error())
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF { // anything after the request, a second value or junk
		return Fail(req.ID, ErrBadRequest, "something follows the request on the line; send one JSON object per line and nothing else")
	}
	defer func() {
		if p := recover(); p != nil {
			slog.Error("inspect: panic in a request", "op", req.Op, "panic", p)
			resp = Fail(req.ID, ErrInternal, "panic: "+toString(p))
		}
	}()
	resp = h.Handle(&req)
	if resp == nil {
		resp = Fail(req.ID, ErrInternal, "no response")
	}
	resp.ID = req.ID
	return resp
}

func toString(p any) string {
	if e, ok := p.(error); ok {
		return e.Error()
	}
	if s, ok := p.(string); ok {
		return s
	}
	return "unknown"
}

// ErrLineTooLong is ReadLine's error for a line over the limit.
var ErrLineTooLong = errors.New("inspect: line too long")

// ReadLine reads one line of at most max bytes, newline included, without the
// newline. A longer line is not read to its end: it returns ErrLineTooLong
// as soon as the limit is passed, with at most max bytes held.
func ReadLine(r *bufio.Reader, max int) ([]byte, error) {
	var line []byte
	for {
		part, err := r.ReadSlice('\n')
		if len(line)+len(part) > max {
			return nil, ErrLineTooLong
		}
		line = append(line, part...)
		switch {
		case err == nil:
			return bytes.TrimRight(line[:len(line)-1], "\r"), nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && len(line) > 0:
			return bytes.TrimRight(line, "\r"), nil // a last request with no newline
		default:
			return nil, err
		}
	}
}
