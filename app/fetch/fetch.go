// Package fetch downloads a web resource over http or https, streaming the
// body to a writer (DECISIONS D60). It is for an app's own small network
// jobs: a self-updater reading release metadata and downloading an archive, a
// feed, a manifest. It is not a general HTTP client: GET only, no cookies, no
// cache, no connection reuse between calls.
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
//   - Windows and Linux: net/http. The trust store is the system's (on Linux
//     the usual certificate directories); the proxy is the one the
//     HTTP_PROXY, HTTPS_PROXY and NO_PROXY environment variables name.
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
// Without Options.Proxy, macOS uses the system's proxy settings and the
// other systems the HTTP_PROXY, HTTPS_PROXY and NO_PROXY environment
// variables. Options.Proxy overrides that, also for loopback addresses, and a
// Proxy of Kind ProxyNone connects directly whatever those say. On
// macOS the credentials of an HTTP proxy (Proxy.User) answer the proxy's 407
// for https URLs, which go through CONNECT; the system does not turn a 407
// for a plain http URL into a request for credentials, so that transfer
// fails with a *StatusError with Code 407. SOCKS5 credentials work for both.
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
}

// ProxyKind is the protocol a proxy speaks.
type ProxyKind uint8

// The proxy protocols.
const (
	// ProxyHTTP is an HTTP proxy reached without encryption. It carries http
	// URLs and, through CONNECT, https URLs.
	ProxyHTTP ProxyKind = iota + 1
	// ProxyHTTPS is an HTTP proxy reached over TLS. macOS does not offer it
	// to NSURLSession's proxy settings: there the transfer fails with an error
	// that satisfies errors.Is(err, errors.ErrUnsupported).
	ProxyHTTPS
	// ProxySOCKS5 is a SOCKS5 proxy; names are resolved by the proxy.
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
}

// The errors Get and Bytes return can be matched with errors.Is and errors.As;
// each also carries the URL and, where there is one, the system's own message.
var (
	// ErrTooLarge means the body is longer than Options.MaxBytes.
	ErrTooLarge = errors.New("fetch: body larger than MaxBytes")
	// ErrBadURL means the URL is not an absolute http or https URL.
	ErrBadURL = errors.New("fetch: not an http or https URL")
	// ErrInsecure means macOS refused plain http to this host (see "Plain http
	// on macOS" in the package documentation).
	ErrInsecure = errors.New("fetch: plain http refused by App Transport Security")
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
	res, err := get(ctx, u, s, opt)
	if err != nil && ctx.Err() != nil && !errors.Is(err, ErrTooLarge) {
		err = ctx.Err() // the transfer failed because we cancelled it
	}
	var se *StatusError
	if err != nil && !errors.As(err, &se) && !errors.Is(err, ErrTooLarge) {
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
