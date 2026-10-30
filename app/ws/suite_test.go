package ws

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gonativeui/app/fetch"
)

func dial(t testing.TB, url string, o Options) *Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := Dial(ctx, url, o)
	if err != nil {
		t.Fatalf("Dial %s: %v", url, err)
	}
	t.Cleanup(func() { c.Close(CloseNormal, "") })
	return c
}

func TestTextAndBinary(t *testing.T) {
	s := newServer(t)
	c := dial(t, s.url+"/echo?x=1", Options{
		Headers:      map[string]string{"Authorization": "Bearer t0k3n"},
		Subprotocols: []string{"gnui.v1", "gnui.v2"},
	})
	if c.Subprotocol() != "gnui.v2" {
		t.Errorf("subprotocol %q, want gnui.v2", c.Subprotocol())
	}
	if h, _ := s.header.Load().(http.Header); h.Get("Authorization") != "Bearer t0k3n" {
		t.Errorf("the server saw Authorization %q", h.Get("Authorization"))
	}
	bin := make([]byte, 256)
	for i := range bin {
		bin[i] = byte(i)
	}
	cases := []struct {
		kind MessageKind
		data []byte
	}{
		{Text, []byte("héllo, wörld 👋")},
		{Binary, bin},
		{Text, []byte{}},
		{Binary, bytes.Repeat([]byte{0xAB}, 70000)}, // a 64-bit length
		{Text, bytes.Repeat([]byte("x"), 300)},      // a 16-bit length
	}
	var buf []byte
	for _, tc := range cases {
		if err := c.WriteMessage(tc.kind, tc.data); err != nil {
			t.Fatalf("write %d bytes: %v", len(tc.data), err)
		}
		kind, msg, err := c.ReadMessage(buf)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if kind != tc.kind || !bytes.Equal(msg, tc.data) {
			t.Errorf("echo of %d bytes kind %d: got %d bytes kind %d", len(tc.data), tc.kind, len(msg), kind)
		}
		buf = msg
	}
	// The buffer is reused once it is big enough.
	if err := c.WriteMessage(Binary, bin[:10]); err != nil {
		t.Fatal(err)
	}
	_, msg, err := c.ReadMessage(buf)
	if err != nil || &msg[0] != &buf[:1][0] {
		t.Errorf("a small message after a large one did not reuse the buffer (%v)", err)
	}
}

// bigTrips is the number of 32 MiB round trips (WS_BIG_TRIPS overrides).
func bigTrips() int {
	if n, err := strconv.Atoi(os.Getenv("WS_BIG_TRIPS")); err == nil && n > 0 {
		return n
	}
	return 6
}

// The agent's maxPayload is 32 MiB: messages of exactly that size go both
// ways intact, again and again (probe P8 saw one short read on arm64
// WinHTTP; every byte is compared).
func TestBigMessages(t *testing.T) {
	if testing.Short() {
		t.Skip("moves hundreds of MiB")
	}
	s := newServer(t)
	const size = 32 << 20
	var serverGot atomic.Int64
	s.handlers["/big"] = func(p *peer) {
		var buf []byte
		for {
			kind, msg, err := p.read(buf[:0])
			if err != nil {
				return
			}
			serverGot.Store(int64(len(msg)))
			buf = msg
			if p.write(kind, msg) != nil {
				return
			}
		}
	}
	c := dial(t, s.url+"/big", Options{MaxMessage: size})
	big := make([]byte, size)
	for i := range big {
		big[i] = byte(i*31 + i>>16)
	}
	var buf []byte
	for trip := range bigTrips() {
		big[trip] ^= 0xFF // each trip differs
		start := time.Now()
		if err := c.WriteMessage(Binary, big); err != nil {
			t.Fatalf("trip %d: write: %v", trip, err)
		}
		kind, msg, err := c.ReadMessage(buf)
		if err != nil {
			t.Fatalf("trip %d: read: %v (the server got %d bytes)", trip, err, serverGot.Load())
		}
		if kind != Binary || len(msg) != size || serverGot.Load() != size {
			t.Fatalf("trip %d: got %d bytes kind %d, the server got %d; want %d both ways", trip, len(msg), kind, serverGot.Load(), size)
		}
		if i := firstDiff(msg, big); i >= 0 {
			t.Fatalf("trip %d: byte %d differs", trip, i)
		}
		buf = msg
		t.Logf("trip %d: 32 MiB round trip in %v", trip, time.Since(start).Round(time.Millisecond))
	}
}

func firstDiff(a, b []byte) int {
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return min(len(a), len(b))
	}
	return -1
}

// A message the server sends in fragments, with a ping between them,
// arrives whole, and the ping is answered.
func TestFragmentedAndPing(t *testing.T) {
	s := newServer(t)
	s.handlers["/frag"] = func(p *peer) {
		var buf []byte
		for {
			kind, msg, err := p.read(buf[:0])
			if err != nil {
				return
			}
			buf = msg
			p.wmu.Lock()
			op := byte(kind)
			for off := 0; ; {
				end := min(off+7000, len(msg))
				fin := end == len(msg)
				if p.frame(fin, op, msg[off:end]) != nil {
					p.wmu.Unlock()
					return
				}
				if off == 0 {
					p.frame(true, opPing, []byte("mid-message"))
				}
				op, off = opCont, end
				if fin {
					break
				}
			}
			p.wmu.Unlock()
		}
	}
	c := dial(t, s.url+"/frag", Options{})
	for _, kind := range []MessageKind{Binary, Text} {
		data := bytes.Repeat([]byte("fragmented-"), 5000) // 55,000 bytes: 8 fragments
		if err := c.WriteMessage(kind, data); err != nil {
			t.Fatal(err)
		}
		k, msg, err := c.ReadMessage(nil)
		if err != nil || k != kind || !bytes.Equal(msg, data) {
			t.Fatalf("fragmented %d: kind %d, %d bytes, %v", kind, k, len(msg), err)
		}
	}
	if !waitFor(3*time.Second, func() bool { return s.pongs.Load() > 0 }) {
		t.Error("the server's ping got no pong")
	} else if got := s.lastPong.Load().(string); got != "mid-message" {
		t.Errorf("pong payload %q, want the ping's", got)
	}
}

// The server closes with a code and reason: ReadMessage returns them, the
// close is answered, and writes then fail with ErrClosed.
func TestServerClose(t *testing.T) {
	s := newServer(t)
	s.handlers["/close"] = func(p *peer) {
		if _, _, err := p.read(nil); err != nil {
			return
		}
		p.sendClose(4000, "done here")
		s.recordClose(p)
	}
	c := dial(t, s.url+"/close", Options{})
	c.WriteMessage(Text, []byte("hi"))
	_, _, err := c.ReadMessage(nil)
	var ce *CloseError
	if !errors.As(err, &ce) || ce.Code != 4000 || ce.Reason != "done here" {
		t.Fatalf("read: %v, want a CloseError 4000 \"done here\"", err)
	}
	if _, _, err2 := c.ReadMessage(nil); !errors.As(err2, &ce) {
		t.Errorf("a second read: %v, want the CloseError again", err2)
	}
	if !waitFor(3*time.Second, func() bool { return s.closeCode.Load() != 0 }) {
		t.Error("the client did not answer the close frame")
	}
	if !waitFor(3*time.Second, func() bool { return c.WriteMessage(Text, []byte("late")) != nil }) {
		t.Error("writes still succeed after the close")
	}
}

// The server drops the connection without a close frame.
func TestAbruptDrop(t *testing.T) {
	s := newServer(t)
	s.handlers["/drop"] = func(p *peer) {
		if _, _, err := p.read(nil); err != nil {
			return
		}
		p.conn.Close()
	}
	c := dial(t, s.url+"/drop", Options{})
	c.WriteMessage(Text, []byte("hi"))
	start := time.Now()
	_, _, err := c.ReadMessage(nil)
	var ce *CloseError
	if !errors.As(err, &ce) || ce.Code != CloseAbnormal {
		t.Fatalf("read: %v, want a CloseError %d", err, CloseAbnormal)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("the drop was seen after %v", d)
	}
}

// Close from another goroutine ends a pending read with ErrClosed, and the
// server gets the close frame with its code and reason; Close is
// idempotent.
func TestClientClose(t *testing.T) {
	s := newServer(t)
	s.handlers["/rec"] = s.recordClose
	c := dial(t, s.url+"/rec", Options{})
	done := make(chan error, 1)
	go func() {
		_, _, err := c.ReadMessage(nil)
		done <- err
	}()
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	c.Close(CloseGoingAway, "quitting")
	select {
	case err := <-done:
		if !errors.Is(err, ErrClosed) {
			t.Errorf("pending read: %v, want ErrClosed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not end the pending read")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("Close took %v", d)
	}
	if !waitFor(3*time.Second, func() bool { return s.closeCode.Load() != 0 }) {
		t.Fatal("the server saw no close frame")
	}
	if code, why := s.closeCode.Load(), s.closeWhy.Load().(string); code != int64(CloseGoingAway) || why != "quitting" {
		t.Errorf("the server saw close %d %q", code, why)
	}
	if err := c.Close(CloseNormal, ""); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if err := c.WriteMessage(Text, []byte("x")); !errors.Is(err, ErrClosed) {
		t.Errorf("write after Close: %v", err)
	}
	if _, _, err := c.ReadMessage(nil); !errors.Is(err, ErrClosed) {
		t.Errorf("read after Close: %v", err)
	}

	// Without a pending read, the close frame still goes out.
	s.closeCode.Store(0)
	c2 := dial(t, s.url+"/rec", Options{})
	c2.Close(CloseNormal, "bye")
	if !waitFor(3*time.Second, func() bool { return s.closeCode.Load() == int64(CloseNormal) }) {
		t.Errorf("no pending read: the server saw close %d", s.closeCode.Load())
	}
}

// Close ends a write held back by a server that stopped reading.
func TestCloseEndsBlockedWrite(t *testing.T) {
	s := newServer(t)
	stall := make(chan struct{})
	t.Cleanup(func() { close(stall) })
	s.handlers["/stall"] = func(p *peer) { <-stall }
	c := dial(t, s.url+"/stall", Options{})
	big := make([]byte, 8<<20)
	done := make(chan error, 1)
	var sent atomic.Int64
	go func() {
		for {
			if err := c.WriteMessage(Binary, big); err != nil {
				done <- err
				return
			}
			sent.Add(1)
		}
	}()
	// Wait until the writes stop moving: the socket buffers are full.
	last := int64(-1)
	waitFor(10*time.Second, func() bool {
		n := sent.Load()
		stalled := n == last
		last = n
		time.Sleep(300 * time.Millisecond)
		return stalled
	})
	start := time.Now()
	go c.Close(CloseNormal, "")
	select {
	case err := <-done:
		if !errors.Is(err, ErrClosed) {
			t.Logf("blocked write ended with %v (sent %d messages)", err, sent.Load())
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("Close did not end the blocked write (sent %d messages)", sent.Load())
	}
	t.Logf("the blocked write ended %v after Close, after %d messages", time.Since(start).Round(time.Millisecond), sent.Load())
}

// Cancelling the context ends a handshake the server never answers;
// Options.Timeout does too, and a cancelled context dials nothing.
func TestDialCancel(t *testing.T) {
	s := newServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Dial(ctx, s.url+"/echo", Options{}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled context: %v", err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	start := time.Now()
	if _, err := Dial(ctx, s.url+"/hang", Options{}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled handshake: %v", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("the cancelled handshake returned after %v", d)
	}
	start = time.Now()
	if _, err := Dial(context.Background(), s.url+"/hang", Options{Timeout: 300 * time.Millisecond}); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("handshake timeout: %v", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("the timed-out handshake returned after %v", d)
	}
}

func TestHandshakeStatus(t *testing.T) {
	s := newServer(t)
	_, err := Dial(context.Background(), s.url+"/status401", Options{Timeout: 10 * time.Second})
	var he *HandshakeError
	if !errors.As(err, &he) || he.Status != 401 {
		t.Fatalf("Dial: %v, want a HandshakeError 401", err)
	}
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()
	if _, err := Dial(context.Background(), "ws://"+addr+"/", Options{Timeout: 10 * time.Second}); err == nil || errors.As(err, &he) {
		t.Errorf("a closed port: %v, want a connection error", err)
	}
}

// A message over MaxMessage is ErrTooLarge.
func TestTooLarge(t *testing.T) {
	s := newServer(t)
	s.handlers["/huge"] = func(p *peer) {
		p.write(Binary, make([]byte, 1<<20+1))
		s.recordClose(p)
	}
	c := dial(t, s.url+"/huge", Options{MaxMessage: 1 << 20})
	_, _, err := c.ReadMessage(nil)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("read: %v, want ErrTooLarge", err)
	}
}

// A certificate the system does not trust is refused.
func TestUntrustedCertificate(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	_, err := Dial(context.Background(), "wss://"+srv.Listener.Addr().String()+"/", Options{Timeout: 10 * time.Second})
	var he *HandshakeError
	if err == nil || errors.As(err, &he) {
		t.Fatalf("Dial: %v, want a TLS failure", err)
	}
}

// Through an explicit HTTP proxy, and with ProxyNone around it.
func TestProxy(t *testing.T) {
	s := newServer(t)
	ps := newProxy(t)
	host, port, _ := net.SplitHostPort(ps.ln.Addr().String())
	n, _ := strconv.Atoi(port)
	// "origin" has no dots (no App Transport Security on macOS) and only
	// the proxy resolves it.
	_, sport, _ := net.SplitHostPort(s.ln.Addr().String())
	c := dial(t, "ws://origin:"+sport+"/echo", Options{Proxy: &fetch.Proxy{Kind: fetch.ProxyHTTP, Host: host, Port: n}})
	if err := c.WriteMessage(Text, []byte("via proxy")); err != nil {
		t.Fatal(err)
	}
	if _, msg, err := c.ReadMessage(nil); err != nil || string(msg) != "via proxy" {
		t.Fatalf("echo through the proxy: %q, %v", msg, err)
	}
	if seen := ps.lines(); len(seen) != 1 {
		t.Errorf("the proxy saw %q, want one request", seen)
	} else {
		t.Logf("the proxy saw %q", seen[0])
	}
	if _, err := Dial(context.Background(), "ws://origin:"+sport+"/echo", Options{Proxy: &fetch.Proxy{Kind: fetch.ProxyNone}, Timeout: 10 * time.Second}); err == nil {
		t.Error("ProxyNone reached a name only the proxy resolves")
	}
	d := dial(t, s.url+"/echo", Options{Proxy: &fetch.Proxy{Kind: fetch.ProxyNone}})
	d.WriteMessage(Text, []byte("direct"))
	if _, msg, err := d.ReadMessage(nil); err != nil || string(msg) != "direct" {
		t.Fatalf("direct: %q, %v", msg, err)
	}
	if seen := ps.lines(); len(seen) != 1 {
		t.Errorf("ProxyNone went through the proxy: %q", seen)
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		_, err := Dial(context.Background(), s.url, Options{Proxy: &fetch.Proxy{Kind: fetch.ProxyHTTPS, Host: host, Port: n}})
		if !errors.Is(err, errors.ErrUnsupported) {
			t.Errorf("HTTPS proxy: %v, want ErrUnsupported", err)
		}
	}
}

// floodSeconds is the length of the allocation run (WS_FLOOD_SECONDS
// overrides the plan's 10 s).
func floodSeconds() int {
	if n, err := strconv.Atoi(os.Getenv("WS_FLOOD_SECONDS")); err == nil && n > 0 {
		return n
	}
	return 10
}

// N2's gate: 1,000 messages a second for 10 s, with 0 Go allocations per
// message on the receive path. Two measurements: ReadMessage alone over a
// burst already in the socket (testing.AllocsPerRun; the server is idle),
// which must be exactly 0; then the paced run, counting the whole
// process's allocations (the server's writes allocate nothing either).
func TestReceiveAllocations(t *testing.T) {
	if testing.Short() {
		t.Skip("runs for 10 s")
	}
	secs := floodSeconds()
	total := 1000 * secs
	const burst = 100
	s := newServer(t)
	ready, paced := make(chan float64, 1), make(chan struct{})
	s.handlers["/flood"] = func(p *peer) {
		msg := make([]byte, 256)
		seq := uint64(0)
		// The burst: burst+1 messages (AllocsPerRun's warm-up included).
		ready <- testing.AllocsPerRun(burst, func() {
			binary.BigEndian.PutUint64(msg, seq)
			seq++
			p.write(Binary, msg)
		})
		<-paced
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		for i := range total {
			binary.BigEndian.PutUint64(msg, seq+uint64(i))
			if p.write(Binary, msg) != nil {
				return
			}
			<-tick.C
		}
		s.recordClose(p)
	}
	c := dial(t, s.url+"/flood", Options{})
	buf := make([]byte, 0, 4096)
	writeAllocs := <-ready
	next := uint64(0)
	read := func() {
		kind, msg, err := c.ReadMessage(buf)
		if err != nil || kind != Binary || len(msg) != 256 || binary.BigEndian.Uint64(msg) != next {
			t.Fatalf("message %d: kind %d, %d bytes, %v", next, kind, len(msg), err)
		}
		next++
		buf = msg
	}
	readAllocs := testing.AllocsPerRun(burst, read)
	t.Logf("burst: ReadMessage %.2f allocations per message; the test server's write %.2f", readAllocs, writeAllocs)
	if readAllocs != 0 {
		t.Errorf("ReadMessage allocates %.2f times per message", readAllocs)
	}

	close(paced)
	const warm = 200
	// The paced run counts allocations by cause: every allocation is
	// profiled, and those whose stack passes through the module's own code
	// (outside a _test.go file, so neither this loop nor the test server)
	// are the receive path's. The rest, the runtime's and the test server's,
	// come with time, not messages: on a loaded Windows runner the 1 ms
	// ticker paced 250-400 messages a second, the run took 25-42 s instead
	// of 10, and the process's 3 or so background allocations a second came
	// to 1.0-1.3 per hundred messages (p8-webview2 40797829935, 40816023392).
	oldRate := runtime.MemProfileRate
	runtime.MemProfileRate = 1
	defer func() { runtime.MemProfileRate = oldRate }()
	var before map[[32]uintptr]int64
	var msBefore, msAfter runtime.MemStats
	start := time.Now()
	for i := range total {
		if i == warm {
			before = allocProfile()
			runtime.ReadMemStats(&msBefore)
		}
		read()
	}
	runtime.ReadMemStats(&msAfter)
	took := time.Since(start)
	n := total - warm
	ours, others, sites := allocsByCause(before, allocProfile())
	process := msAfter.Mallocs - msBefore.Mallocs
	t.Logf("%d messages in %v (%.0f/s); %d Go allocations in the process over the last %d, %d of them through the module's code (%.4f per message), %d elsewhere (the runtime's, the test's)",
		total, took.Round(time.Millisecond), float64(total)/took.Seconds(), process, n, ours, float64(ours)/float64(n), others)
	if os.Getenv("WS_ALLOC_REPORT") != "" {
		fmt.Fprintf(os.Stderr, "WS_ALLOCS %s/%s burst_read=%.2f burst_server_write=%.2f paced_messages=%d paced_process_allocs=%d paced_module_allocs=%d per_message=%.4f seconds=%.2f\n",
			runtime.GOOS, runtime.GOARCH, readAllocs, writeAllocs, n, process, ours, float64(ours)/float64(n), took.Seconds())
	}
	// Zero per message; one per hundred leaves room for one-offs (a buffer
	// grown once, a thread's first callback).
	if float64(ours) > float64(n)/100 {
		t.Errorf("%d allocations through the module's code over %d messages; their stacks:\n%s", ours, n, sites)
	} else if ours > 0 {
		t.Logf("their stacks:\n%s", sites)
	}
}

// allocProfile is the process's allocation profile (objects allocated so
// far, by stack) after two GCs, which publish it.
func allocProfile() map[[32]uintptr]int64 {
	runtime.GC()
	runtime.GC()
	recs := make([]runtime.MemProfileRecord, 1024)
	n, ok := runtime.MemProfile(recs, true)
	for !ok {
		recs = make([]runtime.MemProfileRecord, n+256)
		n, ok = runtime.MemProfile(recs, true)
	}
	m := make(map[[32]uintptr]int64, n)
	for _, r := range recs[:n] {
		m[r.Stack0] += r.AllocObjects
	}
	return m
}

// allocsByCause sorts the allocations made between two profiles into those
// whose stack has a frame of the module outside a _test.go file (ours, whose
// stacks it describes in sites) and the rest.
func allocsByCause(before, after map[[32]uintptr]int64) (ours, others int64, sites string) {
	var sb strings.Builder
	for stk, n := range after {
		if n -= before[stk]; n <= 0 {
			continue
		}
		pcs := stk[:]
		for i, pc := range pcs {
			if pc == 0 {
				pcs = pcs[:i]
				break
			}
		}
		var stack strings.Builder
		mine := false
		frames := runtime.CallersFrames(pcs)
		for {
			fr, more := frames.Next()
			fmt.Fprintf(&stack, "    %s:%d\n", fr.Function, fr.Line)
			if strings.HasPrefix(fr.Function, "gonativeui/") && !strings.HasSuffix(fr.File, "_test.go") {
				mine = true
			}
			if !more {
				break
			}
		}
		if !mine {
			others += n
			continue
		}
		ours += n
		fmt.Fprintf(&sb, "  %d object(s):\n%s", n, stack.String())
	}
	return ours, others, sb.String()
}
