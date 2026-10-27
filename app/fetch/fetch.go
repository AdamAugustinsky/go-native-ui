// Package fetch makes HTTP requests over http or https through the system's
// client, streaming the response body to a writer (DECISIONS D60; the phase
// 8 plan's N1 and D73). Get downloads a resource: a self-updater reading
// release metadata and downloading an archive, a feed, a manifest. Do makes
// any request: a method, headers, a streamed request body with upload
// progress, and a streamed response an app can read as it arrives (server-
// sent events are the app's: a body it reads line by line). There are no
// cookies, no cache and no connection reuse between calls.
//
//	res, err := fetch.Do(ctx, fetch.Request{
//		Method: "PUT", URL: "https://agent.example/api/upload",
//		Body: f, BodyLength: size, // -1: unknown, sent chunked
//	}, io.Discard, fetch.Options{
//		Headers:        map[string]string{"Authorization": "Bearer " + token},
//		UploadProgress: func(sent, total int64) { ... },
//		AnyStatus:      true, // a 4xx's JSON body is the app's to read
//	})
//
// The body is read on the calling goroutine as the system takes it, so an
// upload of any size (a file through io.SectionReader, a generated stream)
// runs in constant memory.
//
//	var buf bytes.Buffer
//	res, err := fetch.Get(ctx, "https://example.com/app-1.2.zip", f, fetch.Options{
//		Timeout:  5 * time.Minute,
//		MaxBytes: 200 << 20,
//		Progress: func(got, total int64) { ... }, // total is -1 when unknown
//	})
//
//	manifest, _, err := fetch.Bytes(ctx, "https://example.com/latest.json", 1<<20, fetch.Options{})
//
//	var se *fetch.StatusError
//	if errors.As(err, &se) && se.Code == 404 { ... }
//
// # What each OS does
//
//   - macOS: NSURLSession, an ephemeral session on its own delegate queue. The
//     program links neither net/http nor crypto/tls (about 2.8 MB), and the
//     transfer uses the system's trust store, its proxy settings (System
//     Settings, PAC files, per-network rules) and its TLS fixes.
//   - Windows: WinHTTP (winhttp.dll), synchronous, one session per call. The
//     program links neither net/http nor crypto/tls; TLS is SChannel with
//     the system's trust store, and the proxy is the system's
//     (WINHTTP_ACCESS_TYPE_AUTOMATIC_PROXY: auto-detection, PAC files and the
//     user's settings), not the environment's.
//   - Linux: net/http. The trust store is the usual certificate
//     directories; the proxy is the one the HTTP_PROXY, HTTPS_PROXY and
//     NO_PROXY environment variables name.
//
// Both behave the same where the API reaches: the same redirects (followed),
// the same errors, the same limits, the body delivered as the server sent it
// (no Accept-Encoding other than identity unless Options.Headers sets one,
// and then decompressing is the caller's business).
//
// # Plain http on macOS
//
// NSURLSession applies App Transport Security to an app bundle, and so does
// this package inside one: plain http to some hosts fails with ErrInsecure
// (an https URL that redirects to one fails the same way). Measured on
// macOS 27:
//
//   - A bare executable (no Info.plist, as under go run and go test) is not
//     subject to it: plain http works to every host.
//   - In a bundle, plain http works to loopback addresses (127.0.0.1, [::1]),
//     "localhost", private and link-local addresses (192.168.x.x, 10.x.x.x,
//     169.254.x.x), host names with no dots, and .local names.
//   - In a bundle, any other plain http host (a name such as
//     feeds.example.com, or a public IP address) is refused unless the
//     Info.plist allows it: NSAppTransportSecurity with NSAllowsArbitraryLoads,
//     or an NSExceptionDomains entry for the host with
//     NSExceptionAllowsInsecureHTTPLoads. NSAllowsLocalNetworking adds nothing
//     for the hosts listed above.
//
// Windows and Linux allow plain http everywhere. Use https for anything real;
// plain http to a local address is for tests and local feeds, and works in a
// bundle without any Info.plist key.
//
// # Proxies
//
// Without Options.Proxy, macOS and Windows use the system's proxy settings
// and Linux the HTTP_PROXY, HTTPS_PROXY and NO_PROXY environment variables.
// Options.Proxy overrides that, also for loopback addresses (except on
// Windows, where WinHTTP always connects to those directly), and a Proxy of
// Kind ProxyNone connects directly whatever those say. On macOS the
// credentials of an HTTP proxy (Proxy.User) answer the proxy's 407 for https
// URLs, which go through CONNECT; the system does not turn a 407 for a plain
// http URL into a request for credentials, so that transfer fails with a
// *StatusError with Code 407. SOCKS5 credentials work for both. Windows
// offers the credentials of an HTTP proxy with Basic before it asks, and
// answers its 407 once for a request without a body. WinHTTP has no SOCKS
// and no TLS-secured proxy: ProxySOCKS5 and ProxyHTTPS fail there with an
// error that satisfies errors.Is(err, errors.ErrUnsupported).
//
// # Threads
//
// Every function may be called from any goroutine and blocks until the
// transfer ends: call it from a task worker (package task), never from the UI
// thread. The writer and Progress run on the calling goroutine. Cancelling
// the context stops the transfer promptly.
package fetch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
)

// Options tunes one transfer. The zero value is a plain GET with no limits.
type Options struct {
	// Headers are extra request headers, by name ("Accept", "User-Agent",
	// "Authorization"). They are kept across redirects. Setting Accept-Encoding
	// asks the server for a compressed body, which then arrives compressed.
	Headers map[string]string
	// Timeout bounds the whole transfer, headers and body, redirects included
	// (0: no limit but the context's). Expiry returns an error that satisfies
	// errors.Is(err, context.DeadlineExceeded).
	Timeout time.Duration
	// MaxBytes caps the body (0: no cap). A body longer than that fails with
	// ErrTooLarge: when the server announces a length over the cap, before
	// anything is written; otherwise after exactly MaxBytes bytes have been.
	MaxBytes int64
	// Progress, if set, is called on the calling goroutine after every write
	// to the writer with the bytes written so far and the body's length, or -1
	// when the server did not say.
	Progress func(received, total int64)
	// Proxy, if set, routes the transfer through that proxy instead of the
	// system's, or with Kind ProxyNone, through none.
	Proxy *Proxy
	// UploadProgress, if set, is called on the calling goroutine as Do sends
	// a request body, with the bytes sent so far and Request.BodyLength (-1
	// when unknown).
	UploadProgress func(sent, total int64)
	// AnyStatus makes Do deliver the response body whatever the status, with
	// no *StatusError: an API's error responses carry a body the app reads.
	// Get ignores it.
	AnyStatus bool
}

// Request is one request for Do.
type Request struct {
	// Method is the HTTP method ("GET", "POST", "PUT", "PATCH", "DELETE",
	// "HEAD", "OPTIONS"); "" is GET.
	Method string
	// URL is an absolute http or https URL.
	URL string
	// Body is the request body, read on the calling goroutine as it is sent;
	// nil sends none. Do never closes it.
	Body io.Reader
	// BodyLength is the body's length in bytes, sent as Content-Length; -1
	// when unknown, and the body is then sent chunked. A Body that ends
	// before BodyLength bytes fails the request.
	BodyLength int64
}

// ProxyKind is the protocol a proxy speaks.
type ProxyKind uint8

// The proxy protocols.
const (
	// ProxyHTTP is an HTTP proxy reached without encryption. It carries http
	// URLs and, through CONNECT, https URLs.
	ProxyHTTP ProxyKind = iota + 1
	// ProxyHTTPS is an HTTP proxy reached over TLS. Neither NSURLSession's
	// proxy settings nor WinHTTP offer it: on macOS and Windows the transfer
	// fails with an error that satisfies errors.Is(err, errors.ErrUnsupported).
	ProxyHTTPS
	// ProxySOCKS5 is a SOCKS5 proxy; names are resolved by the proxy.
	// WinHTTP has none: on Windows it is errors.ErrUnsupported.
	ProxySOCKS5
	// ProxyNone is no proxy: the transfer connects to the server directly,
	// whatever the system's settings or the environment name (a host on the
	// app's own no-proxy list). It takes no Host, Port or credentials.
	ProxyNone
)

// Proxy names one proxy server.
type Proxy struct {
	// Kind is the protocol (the zero value means ProxyHTTP).
	Kind ProxyKind
	// Host and Port locate the proxy.
	Host string
	Port int
	// User and Password are the proxy's credentials, if it wants any (HTTP
	// basic authentication, or SOCKS5 username and password). They are not
	// sent to the web server.
	User, Password string
}

// Response describes what came back. Get fills it as far as the server got,
// also when it returns an error.
type Response struct {
	// Status is the HTTP status code of the final response, after redirects
	// (0 if none arrived).
	Status int
	// URL is the final URL, after redirects.
	URL string
	// ContentLength is the length the server announced for the body, -1 when
	// it did not (the length of the body written may differ then, and also when
	// the server's announcement was wrong).
	ContentLength int64
	// Headers are the final response's headers by canonical name
	// ("Content-Type"), repeated ones joined with ", ". Do fills them; Get
	// leaves them nil.
	Headers map[string]string
}

// The errors Get, Bytes and Do return can be matched with errors.Is and errors.As;
// each also carries the URL and, where there is one, the system's own message.
var (
	// ErrTooLarge means the body is longer than Options.MaxBytes.
	ErrTooLarge = errors.New("fetch: body larger than MaxBytes")
	// ErrBadURL means the URL is not an absolute http or https URL.
	ErrBadURL = errors.New("fetch: not an http or https URL")
	// ErrInsecure means macOS refused plain http to this host (see "Plain http
	// on macOS" in the package documentation).
	ErrInsecure = errors.New("fetch: plain http refused by App Transport Security")
	// ErrUnsupportedRequest is Do's error for a request the systems' clients
	// do not all make: a GET or HEAD with a body (NSURLSession refuses it).
	// It matches errors.ErrUnsupported.
	ErrUnsupportedRequest = fmt.Errorf("fetch: request not supported: %w", errors.ErrUnsupported)
	// ErrBodyLength means Request.Body ended before Request.BodyLength
	// bytes.
	ErrBodyLength = errors.New("fetch: request body shorter than BodyLength")
	// ErrBadMethod means Request.Method is not an HTTP method token.
	ErrBadMethod = errors.New("fetch: not an HTTP method")
)

// StatusError is returned when the final response's status is not 2xx. The
// body is not read.
type StatusError struct {
	// Code is the HTTP status code.
	Code int
	// URL is the final URL.
	URL string
}

// Error describes the status and the URL.
func (e *StatusError) Error() string { return fmt.Sprintf("fetch: %s: status %d", e.URL, e.Code) }

// Get fetches rawURL with GET and writes the body to w as it arrives. It
// returns when the body is complete, the transfer fails, or ctx ends. A
// status outside 2xx is a *StatusError (the body is discarded, w gets
// nothing). On any error w may hold a partial body.
func Get(ctx context.Context, rawURL string, w io.Writer, opt Options) (Response, error) {
	u, err := parseURL(rawURL)
	if err != nil {
		return Response{}, err
	}
	return run(ctx, u, &call{method: "GET", length: -1}, w, opt)
}

// run makes the call c to u: the checks and limits Get and Do share, then
// the system's client.
func run(ctx context.Context, u *url.URL, c *call, w io.Writer, opt Options) (Response, error) {
	if opt.Proxy != nil {
		if err := opt.Proxy.check(); err != nil {
			return Response{}, err
		}
	}
	if opt.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opt.Timeout)
		defer cancel()
	}
	if err := ctx.Err(); err != nil {
		return Response{}, wrap(u, err)
	}
	s := &sink{w: w, max: opt.MaxBytes, total: -1, progress: opt.Progress}
	res, err := do(ctx, u, c, s, opt)
	if err != nil && ctx.Err() != nil && !errors.Is(err, ErrTooLarge) && !errors.Is(err, ErrBodyLength) && !c.bodyErr {
		err = ctx.Err() // the transfer failed because we cancelled it
	}
	var se *StatusError
	if err != nil && !errors.As(err, &se) && !errors.Is(err, ErrTooLarge) && !c.bodyErr {
		err = wrap(u, err)
	}
	return res, err
}

// Bytes is Get into memory: the body, which can be at most limit bytes
// (limit must be positive; it replaces opt.MaxBytes). Use it for small
// documents such as a JSON manifest; it returns nil bytes on any error.
func Bytes(ctx context.Context, rawURL string, limit int64, opt Options) ([]byte, Response, error) {
	if limit <= 0 {
		return nil, Response{}, errors.New("fetch: Bytes needs a positive limit")
	}
	opt.MaxBytes = limit
	var buf bytes.Buffer
	res, err := Get(ctx, rawURL, &buf, opt)
	if err != nil {
		return nil, res, err
	}
	return buf.Bytes(), res, nil
}

// Do makes the request req and writes the response body to w as it
// arrives (nil w discards it). It returns when the body is complete, the
// request fails, or ctx ends. A status outside 2xx is a *StatusError unless
// opt.AnyStatus is set. Options.MaxBytes, Timeout, Progress, Headers and
// Proxy mean what they mean for Get; Response.Headers is filled.
//
// The request body is read on the calling goroutine, in pieces, as the
// system takes them, so a file or a generated stream of any size is sent
// in constant memory; UploadProgress follows it. An error from the body's
// Read ends the request and is returned as it is.
//
// Redirects are followed: 301, 302 and 303 turn the request into a GET
// without a body, as browsers do. A request with a body is not sent again,
// so a 307 or 308 answer to one fails the request (a *StatusError when the
// system hands the answer back, an error of the system's otherwise).
func Do(ctx context.Context, req Request, w io.Writer, opt Options) (Response, error) {
	m := req.Method
	if m == "" {
		m = "GET"
	}
	if !methodToken(m) {
		return Response{}, fmt.Errorf("%w: %q", ErrBadMethod, req.Method)
	}
	u, err := parseURL(req.URL)
	if err != nil {
		return Response{}, err
	}
	if w == nil {
		w = io.Discard
	}
	c := &call{method: m, headers: true, anyStatus: opt.AnyStatus, upload: opt.UploadProgress, length: -1}
	if req.Body != nil {
		if m == "GET" || m == "HEAD" {
			return Response{}, fmt.Errorf("%w: a %s with a body", ErrUnsupportedRequest, m)
		}
		if req.BodyLength < -1 {
			return Response{}, fmt.Errorf("fetch: BodyLength %d", req.BodyLength)
		}
		c.body, c.length = req.Body, req.BodyLength
	}
	return run(ctx, u, c, w, opt)
}

// A call is what one request asks of the system's client beyond a URL.
type call struct {
	method    string
	body      io.Reader // nil: none
	length    int64     // the body's length, -1 unknown (sent chunked)
	headers   bool      // fill Response.Headers
	anyStatus bool
	upload    func(sent, total int64)

	sent    int64 // body bytes handed to the system
	bodyErr bool  // the request failed because the body's Read did
}

// hasBody reports whether the request carries a body (an empty one with a
// known length of 0 included).
func (c *call) hasBody() bool { return c.body != nil }

// fill reads the next piece of the body into p: n > 0, or io.EOF once the
// body is complete. A body that ends before its length is ErrBodyLength;
// one longer than its length is read only up to it.
func (c *call) fill(p []byte) (int, error) {
	if c.length >= 0 {
		rem := c.length - c.sent
		if rem <= 0 {
			return 0, io.EOF
		}
		if int64(len(p)) > rem {
			p = p[:rem]
		}
	}
	for empty := 0; ; empty++ {
		n, err := c.body.Read(p)
		switch {
		case n > 0:
			return n, nil
		case err == io.EOF && c.length >= 0:
			return 0, fmt.Errorf("%w: %d of %d bytes", ErrBodyLength, c.sent, c.length)
		case err == io.EOF:
			return 0, io.EOF
		case err != nil:
			c.bodyErr = true
			return 0, err
		case empty == 100:
			c.bodyErr = true
			return 0, io.ErrNoProgress
		}
	}
}

// advance records n body bytes taken by the system and reports progress.
func (c *call) advance(n int) {
	c.sent += int64(n)
	if c.upload != nil {
		c.upload(c.sent, c.length)
	}
}

// accepts reports whether a response's body is delivered.
func (c *call) accepts(status int) bool { return c.anyStatus || ok(status) }

// addHeader adds a response header to h under its canonical name, joining
// repeated ones with ", ".
func addHeader(h map[string]string, name, value string) {
	name = canonicalKey(name)
	if old, ok := h[name]; ok {
		value = old + ", " + value
	}
	h[name] = value
}

// canonicalKey is name with its first letter and each letter after a
// hyphen upper case and the rest lower case ("content-type" is
// "Content-Type"), as net/http writes header names.
func canonicalKey(name string) string {
	b := []byte(name)
	upper := true
	for i, c := range b {
		switch {
		case upper && 'a' <= c && c <= 'z':
			b[i] = c - 'a' + 'A'
		case !upper && 'A' <= c && c <= 'Z':
			b[i] = c - 'A' + 'a'
		}
		upper = c == '-'
	}
	return string(b)
}

// methodToken reports whether m is an HTTP token (RFC 9110 §5.6.2).
func methodToken(m string) bool {
	if m == "" {
		return false
	}
	for i := 0; i < len(m); i++ {
		c := m[i]
		if c <= ' ' || c >= 0x7F || strings.IndexByte("\"(),/:;<=>?@[\\]{}", c) >= 0 {
			return false
		}
	}
	return true
}

func parseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("%w: %q", ErrBadURL, raw)
	}
	return u, nil
}

func (p *Proxy) check() error {
	if p.Kind == ProxyNone {
		if p.Host != "" || p.Port != 0 || p.User != "" || p.Password != "" {
			return errors.New("fetch: ProxyNone takes no host, port or credentials")
		}
		return nil
	}
	if p.Host == "" || p.Port <= 0 || p.Port > 65535 {
		return errors.New("fetch: proxy needs a host and a port")
	}
	if strings.ContainsAny(p.Host, "/@ ") {
		return errors.New("fetch: proxy host is not a bare host name or address")
	}
	switch p.Kind {
	case 0, ProxyHTTP, ProxyHTTPS, ProxySOCKS5:
		return nil
	}
	return errors.New("fetch: unknown proxy kind")
}

// kind returns the proxy's kind with the default applied.
func (p *Proxy) kind() ProxyKind {
	if p.Kind == 0 {
		return ProxyHTTP
	}
	return p.Kind
}

// wrap names the URL in err, keeping the error matchable.
func wrap(u *url.URL, err error) error {
	return fmt.Errorf("fetch: %s: %w", u.Redacted(), err)
}

// A sink is the writer a transfer feeds: it applies MaxBytes and reports
// progress. The transfer sets total once the response is known.
type sink struct {
	w        io.Writer
	max      int64
	got      int64
	total    int64
	progress func(received, total int64)
}

// admit reports whether a body of the announced length may be written at all.
func (s *sink) admit(length int64) error {
	s.total = length
	if s.max > 0 && length > s.max {
		return ErrTooLarge
	}
	return nil
}

// Write passes p on, up to the cap; reaching it with more to come is
// ErrTooLarge.
func (s *sink) Write(p []byte) (int, error) {
	var over bool
	if s.max > 0 && s.got+int64(len(p)) > s.max {
		p, over = p[:s.max-s.got], true
	}
	n, err := s.w.Write(p)
	s.got += int64(n)
	if n > 0 && s.progress != nil {
		s.progress(s.got, s.total)
	}
	if err == nil && over {
		err = ErrTooLarge
	}
	return n, err
}

// ok reports whether a status is a success.
func ok(status int) bool { return status >= 200 && status < 300 }
