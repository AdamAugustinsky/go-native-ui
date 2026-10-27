package fetch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// pattern returns n bytes that differ from block to block, so a lost or
// repeated chunk changes the hash.
func pattern(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7 + i>>8 + i>>16)
	}
	return b
}

func serve(t *testing.T, h http.Handler) string {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return s.URL
}

func TestStreamWithProgress(t *testing.T) {
	body := pattern(12 << 20)
	url := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.Write(body)
	}))
	h := sha256.New()
	var calls, last int64
	var total int64 = -2
	res, err := Get(context.Background(), url+"/big.bin", h, Options{Progress: func(got, tot int64) {
		if got <= last {
			t.Errorf("progress went from %d to %d", last, got)
		}
		last, total = got, tot
		calls++
	}})
	if err != nil {
		t.Fatal(err)
	}
	if want := sha256.Sum256(body); !bytes.Equal(h.Sum(nil), want[:]) {
		t.Error("body differs from what the server sent")
	}
	if last != int64(len(body)) || total != int64(len(body)) || calls < 2 {
		t.Errorf("progress ended at %d of %d after %d calls, want %d of %[4]d in several calls", last, total, calls, len(body))
	}
	if res.Status != 200 || res.ContentLength != int64(len(body)) || res.URL != url+"/big.bin" {
		t.Errorf("response %+v", res)
	}
}

// A body of unknown length (chunked) reports -1 and still arrives whole.
func TestUnknownLength(t *testing.T) {
	body := pattern(3 << 20)
	url := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for off := 0; off < len(body); off += 100 << 10 {
			w.Write(body[off:min(off+100<<10, len(body))])
			w.(http.Flusher).Flush()
		}
	}))
	var buf bytes.Buffer
	var total int64
	res, err := Get(context.Background(), url, &buf, Options{Progress: func(_, tot int64) { total = tot }})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), body) {
		t.Errorf("got %d bytes, want %d", buf.Len(), len(body))
	}
	if total != -1 || res.ContentLength != -1 {
		t.Errorf("length %d, progress total %d, want -1", res.ContentLength, total)
	}
}

func TestCancelMidTransfer(t *testing.T) {
	gone := make(chan struct{})
	url := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(gone)
		w.Header().Set("Content-Length", strconv.Itoa(1<<30))
		chunk := pattern(32 << 10)
		for {
			if _, err := w.Write(chunk); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(2 * time.Millisecond):
			}
		}
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var once sync.Once
	var cancelled time.Time
	w := writerFunc(func(p []byte) (int, error) {
		if r := cancelled; r.IsZero() {
			once.Do(func() { cancelled = time.Now(); cancel() })
		}
		return len(p), nil
	})
	_, err := Get(ctx, url, w, Options{})
	took := time.Since(cancelled)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error %v, want context.Canceled", err)
	}
	if took > time.Second {
		t.Errorf("Get returned %v after the cancel", took)
	}
	select {
	case <-gone:
	case <-time.After(5 * time.Second):
		t.Error("the server never saw the connection close")
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

func TestCancelBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Get(ctx, "http://127.0.0.1:1/", io.Discard, Options{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("error %v, want context.Canceled", err)
	}
}

func TestStatusError(t *testing.T) {
	url := serve(t, http.NotFoundHandler())
	var buf bytes.Buffer
	res, err := Get(context.Background(), url+"/missing", &buf, Options{})
	var se *StatusError
	if !errors.As(err, &se) || se.Code != 404 || se.URL != url+"/missing" {
		t.Fatalf("error %v, want a 404 StatusError", err)
	}
	if buf.Len() != 0 || res.Status != 404 {
		t.Errorf("wrote %d bytes, response %+v", buf.Len(), res)
	}
	if _, _, err := Bytes(context.Background(), url, 100, Options{}); !errors.As(err, &se) {
		t.Errorf("Bytes: %v", err)
	}
}

func TestRedirect(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/old", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/mid", http.StatusFound)
	})
	mux.HandleFunc("/mid", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/new?x=1", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/new", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "arrived %s %s", r.URL.RawQuery, r.Header.Get("X-Keep"))
	})
	url := serve(t, mux)
	got, res, err := Bytes(context.Background(), url+"/old", 1<<10, Options{Headers: map[string]string{"X-Keep": "yes"}})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "arrived x=1 yes" {
		t.Errorf("body %q", got)
	}
	if res.Status != 200 || res.URL != url+"/new?x=1" {
		t.Errorf("response %+v, want the final URL", res)
	}
}

func TestMaxBytes(t *testing.T) {
	body := pattern(1 << 20)
	mux := http.NewServeMux()
	mux.HandleFunc("/known", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.Write(body)
	})
	mux.HandleFunc("/chunked", func(w http.ResponseWriter, r *http.Request) {
		for off := 0; off < len(body); off += 64 << 10 {
			w.Write(body[off : off+64<<10])
			w.(http.Flusher).Flush()
		}
	})
	url := serve(t, mux)
	ctx := context.Background()

	var buf bytes.Buffer
	_, err := Get(ctx, url+"/known", &buf, Options{MaxBytes: 1000})
	if !errors.Is(err, ErrTooLarge) || buf.Len() != 0 {
		t.Errorf("announced length over the cap: error %v, %d bytes written; want ErrTooLarge and none", err, buf.Len())
	}

	buf.Reset()
	cut := 300<<10 + 17
	_, err = Get(ctx, url+"/chunked", &buf, Options{MaxBytes: int64(cut)})
	if !errors.Is(err, ErrTooLarge) || !bytes.Equal(buf.Bytes(), body[:cut]) {
		t.Errorf("unannounced length over the cap: error %v, %d bytes written; want ErrTooLarge and exactly %d", err, buf.Len(), cut)
	}

	buf.Reset()
	if _, err = Get(ctx, url+"/known", &buf, Options{MaxBytes: int64(len(body))}); err != nil || buf.Len() != len(body) {
		t.Errorf("a body of exactly MaxBytes: error %v, %d bytes", err, buf.Len())
	}
	buf.Reset()
	if _, err = Get(ctx, url+"/chunked", &buf, Options{MaxBytes: int64(len(body))}); err != nil || buf.Len() != len(body) {
		t.Errorf("a chunked body of exactly MaxBytes: error %v, %d bytes", err, buf.Len())
	}
}

func TestTimeout(t *testing.T) {
	url := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}))
	start := time.Now()
	_, err := Get(context.Background(), url, io.Discard, Options{Timeout: 300 * time.Millisecond})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error %v, want context.DeadlineExceeded", err)
	}
	if d := time.Since(start); d < 250*time.Millisecond || d > 3*time.Second {
		t.Errorf("returned after %v", d)
	}
}

// The timeout covers the body as well as the headers.
func TestTimeoutDuringBody(t *testing.T) {
	url := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(bytes.Repeat([]byte("first"), 1000))
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}))
	var buf bytes.Buffer
	_, err := Get(context.Background(), url, &buf, Options{Timeout: 400 * time.Millisecond})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error %v, want context.DeadlineExceeded", err)
	}
	if buf.Len() != 5000 {
		t.Errorf("wrote %d bytes before the timeout, want 5000", buf.Len())
	}
}

func TestHeaders(t *testing.T) {
	url := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s|%s|%s|%s|%d", r.Method, r.Header.Get("X-Token"), r.Header.Get("Accept"),
			r.Header.Get("Accept-Encoding"), len(r.Cookies()))
	}))
	got, _, err := Bytes(context.Background(), url, 1<<10, Options{Headers: map[string]string{"X-Token": "abc", "Accept": "application/json"}})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "GET|abc|application/json|identity|0" {
		t.Errorf("server saw %q", got)
	}
}

// The body is delivered as the server stored it, even when it looks
// compressed.
func TestNoTransparentDecompression(t *testing.T) {
	gz := []byte{0x1f, 0x8b, 8, 0, 0, 0, 0, 0, 0, 3, 1, 2, 3}
	url := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		w.Write(gz)
	}))
	got, _, err := Bytes(context.Background(), url, 1<<10, Options{})
	if err != nil || !bytes.Equal(got, gz) {
		t.Errorf("got %x, %v; want %x", got, err, gz)
	}
}

func TestBytes(t *testing.T) {
	url := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"version":"1.2.3"}`))
	}))
	ctx := context.Background()
	got, res, err := Bytes(ctx, url, 1<<10, Options{})
	if err != nil || string(got) != `{"version":"1.2.3"}` || res.Status != 200 {
		t.Fatalf("Bytes = %q, %+v, %v", got, res, err)
	}
	got, _, err = Bytes(ctx, url, 5, Options{})
	if !errors.Is(err, ErrTooLarge) || got != nil {
		t.Errorf("Bytes over its limit = %q, %v; want nil and ErrTooLarge", got, err)
	}
	if _, _, err = Bytes(ctx, url, 0, Options{}); err == nil {
		t.Error("Bytes without a limit succeeded")
	}
}

func TestBadURL(t *testing.T) {
	for _, u := range []string{"", "file:///etc/passwd", "ftp://example.com/x", "example.com/x", "http://", "/relative"} {
		if _, err := Get(context.Background(), u, io.Discard, Options{}); !errors.Is(err, ErrBadURL) {
			t.Errorf("Get(%q) = %v, want ErrBadURL", u, err)
		}
	}
}

// The systems' clients reach HTTP proxies without TLS only (Linux's
// net/http has the TLS kind).
func TestHTTPSProxyUnsupported(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("net/http has HTTPS proxies")
	}
	p := Proxy{Kind: ProxyHTTPS, Host: "127.0.0.1", Port: 8443}
	if _, err := Get(context.Background(), "https://example.com/", io.Discard, Options{Proxy: &p}); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("error %v, want errors.ErrUnsupported", err)
	}
}

func TestBadProxy(t *testing.T) {
	for _, p := range []Proxy{{}, {Host: "h"}, {Host: "h", Port: 70000}, {Host: "h", Port: 1, Kind: 9}, {Host: "http://h", Port: 1}} {
		if _, err := Get(context.Background(), "http://127.0.0.1:1/", io.Discard, Options{Proxy: &p}); err == nil {
			t.Errorf("proxy %+v accepted", p)
		}
	}
}

func TestConnectionRefused(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	_, err = Get(context.Background(), "http://"+addr+"/", io.Discard, Options{Timeout: 5 * time.Second})
	var se *StatusError
	if err == nil || errors.As(err, &se) || errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error %v, want a connection failure", err)
	}
}

// A server whose certificate the system does not trust is refused, not
// skipped over.
func TestUntrustedCertificate(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("x")) }))
	defer s.Close()
	var buf bytes.Buffer
	_, err := Get(context.Background(), s.URL, &buf, Options{Timeout: 10 * time.Second})
	var se *StatusError
	if err == nil || errors.As(err, &se) || buf.Len() != 0 {
		t.Errorf("error %v, %d bytes written; want a certificate failure", err, buf.Len())
	}
}

// Many transfers at once, from many goroutines, each its own session.
func TestConcurrent(t *testing.T) {
	body := pattern(512 << 10)
	url := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body) }))
	var wg sync.WaitGroup
	for i := range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 3 {
				got, _, err := Bytes(context.Background(), url, 1<<20, Options{})
				if err != nil || !bytes.Equal(got, body) {
					t.Errorf("goroutine %d: %d bytes, %v", i, len(got), err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// A slow writer holds the transfer back without losing or reordering data.
func TestSlowWriter(t *testing.T) {
	body := pattern(4 << 20)
	url := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body) }))
	h := sha256.New()
	n := 0
	w := writerFunc(func(p []byte) (int, error) {
		if n++; n%20 == 0 {
			time.Sleep(10 * time.Millisecond)
		}
		return h.Write(p)
	})
	if _, err := Get(context.Background(), url, w, Options{}); err != nil {
		t.Fatal(err)
	}
	if want := sha256.Sum256(body); !bytes.Equal(h.Sum(nil), want[:]) {
		t.Error("body differs")
	}
}

func TestWriterError(t *testing.T) {
	url := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(pattern(2 << 20)) }))
	boom := errors.New("disk full")
	_, err := Get(context.Background(), url, writerFunc(func([]byte) (int, error) { return 0, boom }), Options{})
	if !errors.Is(err, boom) {
		t.Errorf("error %v, want the writer's", err)
	}
}

// ---- Proxies ----

// An origin that does not exist: only a proxy can answer for it. The name has
// no dots, so macOS applies no App Transport Security to plain http.
const phantom = "http://origin"

type seen struct {
	mu   sync.Mutex
	reqs []string // "METHOD absolute-URL"
	auth []string
}

func (s *seen) add(r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reqs = append(s.reqs, r.Method+" "+r.URL.String())
	s.auth = append(s.auth, r.Header.Get("Proxy-Authorization"))
}

func (s *seen) snapshot() (reqs, auth []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.reqs...), append([]string(nil), s.auth...)
}

func hostPort(t *testing.T, rawURL string) (string, int) {
	t.Helper()
	h, p, err := net.SplitHostPort(strings.TrimPrefix(rawURL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	n, _ := strconv.Atoi(p)
	return h, n
}

func TestHTTPProxy(t *testing.T) {
	var s seen
	proxy := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.add(r)
		fmt.Fprintf(w, "via proxy: %s", r.Host)
	}))
	host, port := hostPort(t, proxy)
	got, res, err := Bytes(context.Background(), phantom+"/feed.json?v=2", 1<<10, Options{Proxy: &Proxy{Kind: ProxyHTTP, Host: host, Port: port}})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "via proxy: origin" || res.Status != 200 {
		t.Errorf("body %q, response %+v", got, res)
	}
	reqs, auth := s.snapshot()
	if len(reqs) != 1 || reqs[0] != "GET "+phantom+"/feed.json?v=2" || auth[0] != "" {
		t.Errorf("the proxy saw %q (auth %q), want one GET of %s/feed.json?v=2", reqs, auth, phantom)
	}
	// An explicit proxy carries loopback addresses too.
	if _, _, err := Bytes(context.Background(), "http://127.0.0.1:9/loop", 1<<10, Options{Proxy: &Proxy{Kind: ProxyHTTP, Host: host, Port: port}}); err != nil {
		t.Errorf("loopback address through the proxy: %v", err)
	}
	if reqs, _ := s.snapshot(); len(reqs) != 2 || reqs[1] != "GET http://127.0.0.1:9/loop" {
		t.Errorf("the proxy saw %q, want the loopback request second", reqs)
	}
	// The zero Kind means HTTP.
	if _, _, err := Bytes(context.Background(), phantom+"/", 1<<10, Options{Proxy: &Proxy{Host: host, Port: port}}); err != nil {
		t.Errorf("zero Kind: %v", err)
	}
}

// An https URL goes through an HTTP proxy with CONNECT, and the proxy's
// credentials answer its 407. (The test proxy then hangs up, so the transfer
// fails after the tunnel request; what is tested is what the proxy saw.)
func TestHTTPProxyCredentials(t *testing.T) {
	var s seen
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:p@ss word"))
	proxy := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.reqs = append(s.reqs, r.Method+" "+r.Host)
		s.auth = append(s.auth, r.Header.Get("Proxy-Authorization"))
		s.mu.Unlock()
		if r.Header.Get("Proxy-Authorization") != want {
			w.Header().Set("Proxy-Authenticate", `Basic realm="fetch"`)
			http.Error(w, "proxy auth", http.StatusProxyAuthRequired)
			return
		}
		c, _, _ := w.(http.Hijacker).Hijack()
		c.Close()
	}))
	host, port := hostPort(t, proxy)
	p := Proxy{Host: host, Port: port, User: "user", Password: "p@ss word"}
	_, err := Get(context.Background(), "https://origin/feed", io.Discard, Options{Proxy: &p, Timeout: 10 * time.Second})
	if err == nil {
		t.Fatal("a tunnel to nowhere succeeded")
	}
	reqs, auth := s.snapshot()
	if len(reqs) == 0 || reqs[len(reqs)-1] != "CONNECT origin:443" || auth[len(auth)-1] != want {
		t.Errorf("the proxy saw %q with credentials %q (%v); want a CONNECT to origin:443 with %q", reqs, auth, err, want)
	}
	for _, a := range auth {
		if a != "" && a != want {
			t.Errorf("the proxy was sent %q", a)
		}
	}
}

// socks5 is a SOCKS5 server for tests: it answers every CONNECT by serving a
// fixed HTTP response itself, and records the destination and credentials.
type socks5 struct {
	l          net.Listener
	user, pass string // required when user != ""

	mu    sync.Mutex
	dests []string
	creds []string
}

func newSocks5(t *testing.T, user, pass string) *socks5 {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &socks5{l: l, user: user, pass: pass}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s
}

func (s *socks5) serve(c net.Conn) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(c, hdr); err != nil || hdr[0] != 5 {
		return
	}
	methods := make([]byte, hdr[1])
	io.ReadFull(c, methods)
	if s.user != "" {
		c.Write([]byte{5, 2}) // username and password
		var b [2]byte
		io.ReadFull(c, b[:])
		u := make([]byte, b[1])
		io.ReadFull(c, u)
		io.ReadFull(c, b[:1])
		p := make([]byte, b[0])
		io.ReadFull(c, p)
		s.mu.Lock()
		s.creds = append(s.creds, string(u)+":"+string(p))
		s.mu.Unlock()
		if string(u) != s.user || string(p) != s.pass {
			c.Write([]byte{1, 1})
			return
		}
		c.Write([]byte{1, 0})
	} else {
		c.Write([]byte{5, 0})
	}
	req := make([]byte, 4)
	if _, err := io.ReadFull(c, req); err != nil || req[1] != 1 {
		return
	}
	var host string
	switch req[3] {
	case 1:
		a := make([]byte, 4)
		io.ReadFull(c, a)
		host = net.IP(a).String()
	case 3:
		var n [1]byte
		io.ReadFull(c, n[:])
		a := make([]byte, n[0])
		io.ReadFull(c, a)
		host = string(a)
	case 4:
		a := make([]byte, 16)
		io.ReadFull(c, a)
		host = net.IP(a).String()
	}
	var pb [2]byte
	io.ReadFull(c, pb[:])
	s.mu.Lock()
	s.dests = append(s.dests, fmt.Sprintf("%s:%d", host, int(pb[0])<<8|int(pb[1])))
	s.mu.Unlock()
	c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	// The client now speaks HTTP to "the origin": read its request, answer.
	buf := make([]byte, 4096)
	n, _ := c.Read(buf)
	if !strings.HasPrefix(string(buf[:n]), "GET /socks ") {
		return
	}
	body := "via socks"
	fmt.Fprintf(c, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
}

func (s *socks5) addr(t *testing.T) (string, int) { return hostPort(t, "http://"+s.l.Addr().String()) }

// skipSOCKS5 skips on Windows, where WinHTTP has no SOCKS proxy, after
// checking that the transfer says so.
func skipSOCKS5(t *testing.T) {
	if runtime.GOOS != "windows" {
		return
	}
	p := Proxy{Kind: ProxySOCKS5, Host: "127.0.0.1", Port: 1080}
	if _, err := Get(context.Background(), phantom, io.Discard, Options{Proxy: &p}); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("SOCKS5 on Windows: %v, want errors.ErrUnsupported", err)
	}
	t.Skip("WinHTTP has no SOCKS proxy")
}

func TestSOCKS5Proxy(t *testing.T) {
	skipSOCKS5(t)
	srv := newSocks5(t, "", "")
	host, port := srv.addr(t)
	got, _, err := Bytes(context.Background(), phantom+":8080/socks", 1<<10, Options{Proxy: &Proxy{Kind: ProxySOCKS5, Host: host, Port: port}, Timeout: 10 * time.Second})
	if err != nil || string(got) != "via socks" {
		t.Fatalf("%q, %v", got, err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.dests) != 1 || srv.dests[0] != "origin:8080" {
		t.Errorf("the SOCKS server was asked to connect to %q, want origin:8080", srv.dests)
	}
}

func TestSOCKS5Credentials(t *testing.T) {
	skipSOCKS5(t)
	srv := newSocks5(t, "alice", "s3cret")
	host, port := srv.addr(t)
	p := Proxy{Kind: ProxySOCKS5, Host: host, Port: port, User: "alice", Password: "s3cret"}
	got, _, err := Bytes(context.Background(), phantom+"/socks", 1<<10, Options{Proxy: &p, Timeout: 10 * time.Second})
	if err != nil || string(got) != "via socks" {
		srv.mu.Lock()
		defer srv.mu.Unlock()
		t.Fatalf("%q, %v (the server saw credentials %q)", got, err, srv.creds)
	}
	p.Password = "nope"
	if _, _, err = Bytes(context.Background(), phantom+"/socks", 1<<10, Options{Proxy: &p, Timeout: 10 * time.Second}); err == nil {
		t.Error("wrong SOCKS credentials succeeded")
	}
}
