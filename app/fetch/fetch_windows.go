//go:build windows

package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"gonativeui/internal/winhttp"
)

// Windows: WinHTTP (D73), synchronous, on the calling goroutine. Each call
// opens a session of its own with keep-alive off, as macOS makes a session
// per call, so nothing is left behind when it returns. Cancelling closes the
// request handle, which ends the WinHTTP call in progress with
// ERROR_WINHTTP_OPERATION_CANCELLED (probe P8: at once, also for a
// WinHttpWriteData blocked on a server that stopped reading).

const (
	agent      = "gonativeui-fetch/1"
	readChunk  = 64 << 10
	writeChunk = 256 << 10
	// chunkHead is the room kept before each piece of a chunked body for
	// its size line (up to 8 hex digits and CRLF).
	chunkHead = 10
)

// handle is a request handle that the context's watcher or the call
// closes, whichever comes first, exactly once.
type handle struct {
	mu     sync.Mutex
	h      uintptr
	closed bool
}

func (h *handle) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.closed {
		h.closed = true
		winhttp.CloseHandle(h.h)
	}
}

// sessionProxy maps Options.Proxy to the session's; WinHTTP speaks only to
// HTTP proxies reached without TLS.
func sessionProxy(p *Proxy) (winhttp.Proxy, error) {
	switch {
	case p == nil:
		return winhttp.Proxy{}, nil
	case p.kind() == ProxyNone:
		return winhttp.Proxy{None: true}, nil
	case p.kind() == ProxySOCKS5:
		return winhttp.Proxy{}, fmt.Errorf("SOCKS5 proxy: %w on Windows (WinHTTP has none)", errors.ErrUnsupported)
	case p.kind() == ProxyHTTPS:
		return winhttp.Proxy{}, fmt.Errorf("HTTPS proxy: %w on Windows (WinHTTP has none)", errors.ErrUnsupported)
	}
	return winhttp.Proxy{Host: p.Host, Port: p.Port, User: p.User, Password: p.Password}, nil
}

func do(ctx context.Context, u *url.URL, c *call, s *sink, opt Options) (Response, error) {
	if err := winhttp.Load(); err != nil {
		return Response{}, err
	}
	px, err := sessionProxy(opt.Proxy)
	if err != nil {
		return Response{}, err
	}
	ses, err := winhttp.Session(agent, px)
	if err != nil {
		return Response{}, err
	}
	defer winhttp.CloseHandle(ses)
	port := 80
	if u.Scheme == "https" {
		port = 443
	}
	if p := u.Port(); p != "" {
		if port, err = strconv.Atoi(p); err != nil {
			return Response{}, fmt.Errorf("%w: port %q", ErrBadURL, p)
		}
	}
	conn, err := winhttp.Connect(ses, u.Hostname(), port)
	if err != nil {
		return Response{}, err
	}
	defer winhttp.CloseHandle(conn)
	h, err := winhttp.OpenRequest(conn, c.method, u.RequestURI(), u.Scheme == "https", true)
	if err != nil {
		return Response{}, err
	}
	rh := &handle{h: h}
	defer rh.close()
	stop := context.AfterFunc(ctx, rh.close)
	defer stop()
	req := rh.h

	if px.User != "" || px.Password != "" {
		winhttp.SetProxyCredentials(req, px.User, px.Password)
	}
	if err := winhttp.AddHeaders(req, headerLines(c, opt.Headers)); err != nil {
		return Response{}, err
	}

	// Send, write the body, wait for the response; again once when a proxy
	// asks for the credentials and no body has gone yet.
	for attempt := 0; ; attempt++ {
		if err := send(req, c); err != nil {
			return Response{}, err
		}
		status, err := winhttp.Status(req)
		if err != nil {
			return Response{}, err
		}
		if status == 407 && attempt == 0 && px.User != "" && c.sent == 0 && !c.hasBody() {
			if winhttp.AnswerProxyChallenge(req, px.User, px.Password) == nil {
				continue
			}
		}
		break
	}
	status, _ := winhttp.Status(req)
	res := Response{Status: status, URL: winhttp.URL(req), ContentLength: winhttp.ContentLength(req)}
	if res.URL == "" {
		res.URL = u.String()
	}
	if c.headers {
		res.Headers = parseHeaders(winhttp.RawHeaders(req))
	}
	if !c.accepts(status) {
		return res, &StatusError{Code: status, URL: res.URL}
	}
	if c.method == "HEAD" {
		return res, nil
	}
	if err := s.admit(res.ContentLength); err != nil {
		return res, err
	}
	// The response is read as it streams: WinHttpQueryDataAvailable, then
	// WinHttpReadData of that much (a synchronous ReadData waits for a full
	// buffer; probe P8).
	buf := make([]byte, readChunk)
	var n uint32
	for {
		if err := winhttp.Available(req, &n); err != nil {
			return res, err
		}
		if n == 0 {
			return res, nil
		}
		if err := winhttp.Read(req, buf[:min(int(n), len(buf))], &n); err != nil {
			return res, err
		}
		if n == 0 {
			return res, nil
		}
		if _, err := s.Write(buf[:n]); err != nil {
			return res, err
		}
	}
}

// headerLines are the request's header lines: identity encoding unless the
// caller's headers say otherwise, the caller's, and the body's framing.
func headerLines(c *call, extra map[string]string) string {
	var b strings.Builder
	if !hasHeader(extra, "Accept-Encoding") {
		b.WriteString("Accept-Encoding: identity\r\n")
	}
	for k, v := range extra {
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(v)
		b.WriteString("\r\n")
	}
	switch {
	case c.hasBody() && c.length < 0:
		b.WriteString("Transfer-Encoding: chunked\r\n")
	case c.hasBody() && c.length > 0xFFFFFFFE:
		// Over 4 GB WinHTTP takes the length from the header
		// (WINHTTP_IGNORE_REQUEST_TOTAL_LENGTH as the total).
		b.WriteString("Content-Length: " + strconv.FormatInt(c.length, 10) + "\r\n")
	}
	return b.String()
}

func hasHeader(h map[string]string, name string) bool {
	for k := range h {
		if strings.EqualFold(k, name) {
			return true
		}
	}
	return false
}

// send sends the request with its body and waits for the response's head.
func send(req uintptr, c *call) error {
	var total uint32
	if c.hasBody() && c.length >= 0 && c.length <= 0xFFFFFFFE {
		total = uint32(c.length)
	}
	if err := winhttp.Send(req, total); err != nil {
		return err
	}
	if c.hasBody() {
		if err := writeBody(req, c); err != nil {
			return err
		}
	}
	err := winhttp.Receive(req)
	if winhttp.Is(err, winhttp.ErrnoResend) {
		return fmt.Errorf("the server asked for the request again (a 307 or 308 redirect, or authentication), which a streamed body cannot be: %w", err)
	}
	return err
}

// writeBody writes the body with WinHttpWriteData, chunked by us when its
// length is unknown.
func writeBody(req uintptr, c *call) error {
	buf := make([]byte, chunkHead+writeChunk+2)
	var n uint32
	write := func(p []byte) error {
		for len(p) > 0 {
			if err := winhttp.Write(req, p, &n); err != nil {
				return err
			}
			p = p[n:]
		}
		return nil
	}
	chunked := c.length < 0
	for {
		k, err := c.fill(buf[chunkHead : chunkHead+writeChunk])
		if err == io.EOF {
			if chunked {
				return write([]byte("0\r\n\r\n"))
			}
			return nil
		}
		if err != nil {
			return err
		}
		p := buf[chunkHead : chunkHead+k]
		if chunked {
			head := strconv.FormatInt(int64(k), 16) + "\r\n"
			start := chunkHead - len(head)
			copy(buf[start:], head)
			p = append(buf[start:chunkHead+k], '\r', '\n')
		}
		if err := write(p); err != nil {
			return err
		}
		c.advance(k)
	}
}

// parseHeaders turns WINHTTP_QUERY_RAW_HEADERS_CRLF's text into canonical
// names and values, skipping the status line.
func parseHeaders(raw string) map[string]string {
	h := map[string]string{}
	lines := strings.Split(raw, "\r\n")
	for i, line := range lines {
		if i == 0 || line == "" {
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		addHeader(h, strings.TrimSpace(name), strings.TrimSpace(value))
	}
	return h
}
