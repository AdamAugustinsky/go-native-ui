package fetch

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// echoRequest answers with what the server saw: the method, the body's
// framing and the body's SHA-256.
func echoRequest(w http.ResponseWriter, r *http.Request) {
	h := sha256.New()
	n, err := io.Copy(h, r.Body)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	w.Header().Set("X-Method", r.Method)
	fmt.Fprintf(w, "%s %d %d %v %s", r.Method, n, r.ContentLength, r.TransferEncoding, hex.EncodeToString(h.Sum(nil)))
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func TestDoMethods(t *testing.T) {
	url := serve(t, http.HandlerFunc(echoRequest))
	ctx := context.Background()
	body := pattern(100 << 10)
	for _, m := range []string{"", "GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
		req := Request{Method: m, URL: url}
		want := fmt.Sprintf("%s 0 0 [] %s", cmp(m, "GET"), sum(nil))
		if m == "POST" || m == "PUT" || m == "PATCH" {
			req.Body, req.BodyLength = bytes.NewReader(body), int64(len(body))
			want = fmt.Sprintf("%s %d %d [] %s", m, len(body), len(body), sum(body))
		}
		var buf bytes.Buffer
		res, err := Do(ctx, req, &buf, Options{})
		if err != nil {
			t.Errorf("%q: %v", m, err)
			continue
		}
		if got := buf.String(); got != want {
			t.Errorf("%q: the server saw %q, want %q", m, got, want)
		}
		if res.Status != 200 || res.Headers["X-Method"] != cmp(m, "GET") || res.Headers["Content-Type"] == "" {
			t.Errorf("%q: response %+v", m, res)
		}
	}
	// HEAD: headers, no body.
	var buf bytes.Buffer
	res, err := Do(ctx, Request{Method: "HEAD", URL: url}, &buf, Options{MaxBytes: 1})
	if err != nil || buf.Len() != 0 || res.Headers["X-Method"] != "HEAD" {
		t.Errorf("HEAD: %v, %d bytes, %+v", err, buf.Len(), res)
	}
	// A POST with no body sends an empty one.
	buf.Reset()
	if _, err := Do(ctx, Request{Method: "POST", URL: url}, &buf, Options{}); err != nil || !strings.HasPrefix(buf.String(), "POST 0 0 ") {
		t.Errorf("POST without a body: %v, the server saw %q", err, buf.String())
	}
}

func cmp(a, b string) string {
	if a == "" {
		return b
	}
	return a
}

func TestDoRefusals(t *testing.T) {
	ctx := context.Background()
	for _, m := range []string{"GET", "HEAD"} {
		_, err := Do(ctx, Request{Method: m, URL: "http://127.0.0.1:1/", Body: strings.NewReader("x"), BodyLength: 1}, nil, Options{})
		if !errors.Is(err, ErrUnsupportedRequest) || !errors.Is(err, errors.ErrUnsupported) {
			t.Errorf("%s with a body: %v", m, err)
		}
	}
	if _, err := Do(ctx, Request{Method: "BAD METHOD", URL: "http://127.0.0.1:1/"}, nil, Options{}); !errors.Is(err, ErrBadMethod) {
		t.Errorf("bad method: %v", err)
	}
	if _, err := Do(ctx, Request{URL: "ftp://x"}, nil, Options{}); !errors.Is(err, ErrBadURL) {
		t.Errorf("bad URL: %v", err)
	}
}

// An API's error answer: a *StatusError, or with AnyStatus the body and
// headers (repeated ones joined).
func TestDoAnyStatus(t *testing.T) {
	url := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Add("x-multi", "a")
		w.Header().Add("x-multi", "b")
		w.WriteHeader(422)
		io.WriteString(w, `{"message":"nope"}`)
	}))
	ctx := context.Background()
	var buf bytes.Buffer
	_, err := Do(ctx, Request{Method: "POST", URL: url, Body: strings.NewReader("{}"), BodyLength: 2}, &buf, Options{})
	var se *StatusError
	if !errors.As(err, &se) || se.Code != 422 || buf.Len() != 0 {
		t.Fatalf("without AnyStatus: %v, %d bytes", err, buf.Len())
	}
	res, err := Do(ctx, Request{Method: "POST", URL: url, Body: strings.NewReader("{}"), BodyLength: 2}, &buf, Options{AnyStatus: true})
	if err != nil || res.Status != 422 || buf.String() != `{"message":"nope"}` {
		t.Fatalf("AnyStatus: %v, %+v, %q", err, res, buf.String())
	}
	if res.Headers["X-Multi"] != "a, b" || res.Headers["Content-Type"] != "application/json" {
		t.Errorf("headers %q", res.Headers)
	}
	// Get keeps its contract: no headers.
	if res, _ := Get(ctx, url, io.Discard, Options{}); res.Headers != nil {
		t.Errorf("Get filled the headers: %q", res.Headers)
	}
}

// chunky hands out its data in uneven pieces, as a pipe would.
type chunky struct {
	data []byte
	n    int
}

func (c *chunky) Read(p []byte) (int, error) {
	if len(c.data) == 0 {
		return 0, io.EOF
	}
	c.n++
	k := min(len(p), len(c.data), 1+c.n*977%70000)
	copy(p, c.data[:k])
	c.data = c.data[k:]
	return k, nil
}

// A body of unknown length goes chunked, with progress against -1.
func TestDoChunkedBody(t *testing.T) {
	url := serve(t, http.HandlerFunc(echoRequest))
	body := pattern(3<<20 + 123)
	var buf bytes.Buffer
	var calls, last int64
	res, err := Do(context.Background(), Request{Method: "POST", URL: url, Body: &chunky{data: body}, BodyLength: -1}, &buf, Options{
		UploadProgress: func(sent, total int64) {
			if sent < last || total != -1 {
				t.Errorf("progress %d of %d after %d", sent, total, last)
			}
			last = sent
			calls++
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("POST %d -1 [chunked] %s", len(body), sum(body))
	if buf.String() != want || res.Status != 200 {
		t.Errorf("the server saw %q, want %q", buf.String(), want)
	}
	// NSURLSession reports progress when it likes: on loopback a few MiB
	// may be one report.
	if last != int64(len(body)) || calls < 1 {
		t.Errorf("progress ended at %d after %d calls, want %d", last, calls, len(body))
	}
}

// A body shorter than its length fails; a body's own error is returned as
// it is.
func TestDoBodyErrors(t *testing.T) {
	url := serve(t, http.HandlerFunc(echoRequest))
	ctx := context.Background()
	_, err := Do(ctx, Request{Method: "PUT", URL: url, Body: strings.NewReader("short"), BodyLength: 100}, nil, Options{})
	if !errors.Is(err, ErrBodyLength) {
		t.Errorf("short body: %v, want ErrBodyLength", err)
	}
	boom := errors.New("disk on fire")
	r := io.MultiReader(strings.NewReader(strings.Repeat("x", 300<<10)), errReader{boom})
	if _, err := Do(ctx, Request{Method: "PUT", URL: url, Body: r, BodyLength: -1}, nil, Options{}); !errors.Is(err, boom) || err.Error() != boom.Error() {
		t.Errorf("failing body: %v, want the body's own error", err)
	}
	// A longer body is sent up to its length.
	var buf bytes.Buffer
	if _, err := Do(ctx, Request{Method: "PUT", URL: url, Body: strings.NewReader("abcdef"), BodyLength: 3}, &buf, Options{}); err != nil ||
		buf.String() != "PUT 3 3 [] "+sum([]byte("abc")) {
		t.Errorf("longer body: %v, the server saw %q", err, buf.String())
	}
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

// A streamed response is delivered as it arrives: the first line long
// before the last is written.
func TestDoStreamedLines(t *testing.T) {
	const lines, gap = 40, 25 * time.Millisecond
	url := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for i := range lines {
			fmt.Fprintf(w, "data: event %d\n", i)
			w.(http.Flusher).Flush()
			time.Sleep(gap)
		}
	}))
	pr, pw := io.Pipe()
	start := time.Now()
	var res Response
	var err error
	go func() {
		res, err = Do(context.Background(), Request{URL: url, Method: "POST"}, pw, Options{})
		pw.CloseWithError(err)
	}()
	sc := bufio.NewScanner(pr)
	var arrivals []time.Duration
	for sc.Scan() {
		if want := fmt.Sprintf("data: event %d", len(arrivals)); sc.Text() != want {
			t.Fatalf("line %q, want %q", sc.Text(), want)
		}
		arrivals = append(arrivals, time.Since(start))
	}
	if err != nil || len(arrivals) != lines || res.Headers["Content-Type"] != "text/event-stream" {
		t.Fatalf("%d lines, %v, %+v", len(arrivals), err, res)
	}
	total := arrivals[lines-1]
	// Streamed: the first line came within a fifth of the whole, and the
	// lines spread over the transfer rather than arriving at once.
	if arrivals[0] > total/5 || arrivals[lines/2]-arrivals[0] < total/4 {
		t.Errorf("not streamed: first line at %v, middle at %v, last at %v", arrivals[0], arrivals[lines/2], total)
	}
	t.Logf("first line after %v, last after %v", arrivals[0].Round(time.Millisecond), total.Round(time.Millisecond))
}

// gen is a generated body: a repeating pattern of period 251, so a lost,
// repeated or reordered piece shows.
type gen struct {
	n, off int64
}

var genBlock = func() []byte {
	b := make([]byte, 1<<20+251)
	for i := range b {
		b[i] = byte(i % 251)
	}
	return b
}()

func (g *gen) Read(p []byte) (int, error) {
	if g.off >= g.n {
		return 0, io.EOF
	}
	k := int(min(int64(len(p)), g.n-g.off, 1<<20))
	s := int(g.off % 251)
	copy(p[:k], genBlock[s:s+k])
	g.off += int64(k)
	return k, nil
}

// checkGen reads a generated body and reports its length, or -1 at the
// first wrong byte.
func checkGen(r io.Reader) int64 {
	buf := make([]byte, 256<<10)
	var off int64
	for {
		n, err := r.Read(buf)
		for i := 0; i < n; {
			s := int((off + int64(i)) % 251)
			k := min(n-i, 1<<20)
			if !bytes.Equal(buf[i:i+k], genBlock[s:s+k]) {
				return -1
			}
			i += k
		}
		off += int64(n)
		if err != nil {
			return off
		}
	}
}

// uploadSize is the streamed upload's size: 1 GiB, as N1 asks
// (FETCH_UPLOAD_MIB overrides).
func uploadSize() int64 {
	if n, err := strconv.Atoi(os.Getenv("FETCH_UPLOAD_MIB")); err == nil && n > 0 {
		return int64(n) << 20
	}
	return 1 << 30
}

// A 1 GiB generated body streams up with progress, in constant memory, and
// arrives intact.
func TestDoLargeUpload(t *testing.T) {
	if testing.Short() {
		t.Skip("uploads 1 GiB")
	}
	size := uploadSize()
	url := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, checkGen(r.Body), " ", r.ContentLength)
	}))
	for _, known := range []bool{true, false} {
		length := size
		if !known {
			length = -1
		}
		var calls, last int64
		var buf bytes.Buffer
		start := time.Now()
		res, err := Do(context.Background(), Request{Method: "PUT", URL: url, Body: &gen{n: size}, BodyLength: length}, &buf, Options{
			UploadProgress: func(sent, total int64) {
				if sent < last || total != length {
					t.Errorf("progress %d of %d after %d", sent, total, last)
				}
				last = sent
				calls++
			},
		})
		took := time.Since(start)
		if err != nil {
			t.Fatalf("known length %v: %v", known, err)
		}
		want := fmt.Sprintf("%d %d", size, length)
		if buf.String() != want || res.Status != 200 {
			t.Errorf("known length %v: the server saw %q, want %q", known, buf.String(), want)
		}
		if last != size || calls < 2 { // NSURLSession reports a few times a second
			t.Errorf("known length %v: progress ended at %d after %d calls", known, last, calls)
		}
		t.Logf("known length %v: %d MiB in %v (%.0f MB/s), %d progress calls", known, size>>20, took.Round(time.Millisecond),
			float64(size)/1e6/took.Seconds(), calls)
	}
}

// Cancelling while the body is held back by a server that does not read
// returns promptly with context.Canceled.
func TestDoCancelDuringUpload(t *testing.T) {
	gone, drain := make(chan struct{}), make(chan struct{})
	url := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(gone)
		// Reads nothing until the client has given up, then everything:
		// the copy ends only if the client closed the connection (the body
		// it announced is 1 TiB).
		<-drain
		io.Copy(io.Discard, r.Body)
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var sent atomic.Int64
	var cancelled atomic.Int64
	time.AfterFunc(700*time.Millisecond, func() { cancelled.Store(time.Now().UnixNano()); cancel() })
	_, err := Do(ctx, Request{Method: "PUT", URL: url, Body: &gen{n: 1 << 40}, BodyLength: 1 << 40}, nil, Options{
		UploadProgress: func(s, _ int64) { sent.Store(s) },
	})
	took := time.Duration(time.Now().UnixNano() - cancelled.Load())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error %v, want context.Canceled", err)
	}
	if took > 2*time.Second {
		t.Errorf("Do returned %v after the cancel", took)
	}
	t.Logf("cancelled after %d bytes sent; returned %v after the cancel", sent.Load(), took.Round(time.Millisecond))
	close(drain)
	select {
	case <-gone:
	case <-time.After(5 * time.Second):
		t.Error("the server never saw the connection close")
	}
}
