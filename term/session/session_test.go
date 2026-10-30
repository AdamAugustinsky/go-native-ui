//go:build darwin || linux

package session

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const daemonEnv = "GNUI_SESSION_TEST_DAEMON"

// TestMain doubles as the daemon that TestShutdownLeavesNoProcesses
// launches.
func TestMain(m *testing.M) {
	if dir := os.Getenv(daemonEnv); dir != "" {
		if err := RunDaemon(dir, ServeOptions{Config: Config{KillGrace: 300 * time.Millisecond}}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// sink is a view's sink that records what it gets; gate, if set, holds
// each Write until it returns.
type sink struct {
	mu   sync.Mutex
	b    bytes.Buffer
	gate func()
}

func (s *sink) Write(p []byte) (int, error) {
	if s.gate != nil {
		s.gate()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *sink) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func waitFor(t *testing.T, what string, timeout time.Duration, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(timeout); !ok(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("timed out after %v waiting for %s", timeout, what)
		}
	}
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func tempDir(t *testing.T) string {
	// Short: a Unix socket's path has room for about 100 bytes.
	dir, err := os.MkdirTemp("", "gs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "d")
}

var testConfig = Config{KillGrace: 300 * time.Millisecond}

// startDaemon serves a daemon in this process and returns a client of it.
func startDaemon(t *testing.T, cfg Config) (dir string, d *daemon, c Service) {
	dir = tempDir(t)
	stop := make(chan struct{})
	served := make(chan struct{})
	ready := make(chan *daemon, 1)
	serveHook = func(d *daemon) { ready <- d }
	t.Cleanup(func() { serveHook = nil })
	go func() {
		defer close(served)
		if err := Serve(dir, ServeOptions{Config: cfg, Stop: stop}); err != nil {
			t.Error(err)
		}
	}()
	select {
	case d = <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("the daemon did not start")
	}
	c, err := Dial(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c.Close()
		close(stop)
		select {
		case <-served:
		case <-time.After(10 * time.Second):
			t.Error("the daemon did not stop")
		}
	})
	return dir, d, c
}

// ticker counts lines, tick 1, tick 2 …, every 20 ms.
var ticker = []string{"/bin/sh", "-c", `i=0; while :; do i=$((i+1)); echo "tick $i"; sleep 0.02; done`}

var tickRE = regexp.MustCompile(`tick (\d+)\r\n`)

// ticks returns the tick numbers in s and fails unless they run on with no
// gap and no repeat.
func ticks(t *testing.T, s string) []int {
	t.Helper()
	var n []int
	for _, m := range tickRE.FindAllStringSubmatch(s, -1) {
		v, _ := strconv.Atoi(m[1])
		if len(n) > 0 && v != n[len(n)-1]+1 {
			t.Fatalf("tick %d after tick %d", v, n[len(n)-1])
		}
		n = append(n, v)
	}
	return n
}

func TestLocalAttachOutputExit(t *testing.T) {
	l := NewLocal(testConfig)
	defer l.Close()
	in, err := l.Create(Options{Argv: []string{"/bin/sh", "-c", "read x; echo got $x; exit 3"}, Size: Size{Cols: 80, Rows: 24}})
	if err != nil {
		t.Fatal(err)
	}
	var s sink
	a, err := l.Attach(in.ID, &s, Size{})
	if err != nil {
		t.Fatal(err)
	}
	a.Write([]byte("hello\n"))
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("no exit")
	}
	if got := s.String(); !strings.HasPrefix(got, ResetSequence) || !strings.Contains(got, "got hello\r\n") {
		t.Fatalf("output %q", got)
	}
	if code, ok := a.Exit(); !ok || code != 3 {
		t.Fatalf("exit %d %v, want 3", code, ok)
	}
	a.Close()
	if l, _ := l.List(); len(l) != 0 {
		t.Fatalf("an exited, closed session is still listed: %+v", l)
	}
}

// TestDetachReattach detaches from a persistent session, which goes on
// ticking into its ring, and attaches again: the reset and the replay come
// first, then the live ticks, with no tick lost or repeated between them.
func TestDetachReattach(t *testing.T) {
	_, _, c := startDaemon(t, testConfig)
	in, err := c.Create(Options{Argv: ticker, Persist: true})
	if err != nil {
		t.Fatal(err)
	}
	var s1 sink
	a, err := c.Attach(in.ID, &s1, Size{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "tick 3", 5*time.Second, func() bool { return strings.Contains(s1.String(), "tick 3\r\n") })
	if err := a.Detach(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a detached session", 5*time.Second, func() bool {
		l, _ := c.List()
		return len(l) == 1 && !l[0].Attached
	})
	seen := len(ticks(t, s1.String()))
	time.Sleep(300 * time.Millisecond)
	if !alive(in.Pid) {
		t.Fatal("the program died with its view")
	}
	var s2 sink
	a, err = c.Attach(in.ID, &s2, Size{Cols: 100, Rows: 30})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "live ticks after the replay", 5*time.Second, func() bool { return len(ticks(t, s2.String())) > seen+20 })
	got := s2.String()
	if !strings.HasPrefix(got, ResetSequence) {
		t.Fatalf("the replay did not start with a reset: %q", got[:min(len(got), 20)])
	}
	if n := ticks(t, got); n[0] != 1 {
		t.Fatalf("the replay starts at tick %d; the ring has not wrapped, so it should start at 1", n[0])
	}
	if l, _ := c.List(); l[0].Size.Cols != 100 || !l[0].Attached {
		t.Fatalf("after the attach: %+v", l[0])
	}
	a.Close() // persistent: a detach
	time.Sleep(100 * time.Millisecond)
	if !alive(in.Pid) {
		t.Fatal("closing the view of a persistent session killed it")
	}
}

// TestCrashReconnect drops a client's connections with no goodbye: its
// persistent session lives on for the next client and its other one is
// hung up.
func TestCrashReconnect(t *testing.T) {
	dir, _, c := startDaemon(t, testConfig)
	c1, err := Dial(dir)
	if err != nil {
		t.Fatal(err)
	}
	keep, _ := c1.Create(Options{Argv: ticker, Persist: true})
	drop, _ := c1.Create(Options{Argv: ticker})
	var s1, s2 sink
	if _, err := c1.Attach(keep.ID, &s1, Size{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c1.Attach(drop.ID, &s2, Size{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "output", 5*time.Second, func() bool { return strings.Contains(s1.String(), "tick 2") && strings.Contains(s2.String(), "tick 2") })
	c1.(*client).Close() // the crash: sockets close, nothing is said
	waitFor(t, "the non-persistent program's end", 5*time.Second, func() bool { return !alive(drop.Pid) })
	if !alive(keep.Pid) {
		t.Fatal("the persistent program died with its client")
	}
	l, _ := c.List()
	if len(l) != 1 || l[0].ID != keep.ID || l[0].Attached {
		t.Fatalf("after the crash: %+v", l)
	}
	var s3 sink
	a, err := c.Attach(keep.ID, &s3, Size{})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the replay", 5*time.Second, func() bool { return len(ticks(t, s3.String())) > len(ticks(t, s1.String())) })
	a.Detach()
}

// TestCloseAndKill runs the same lifecycle on a local service and a daemon.
func TestCloseAndKill(t *testing.T) {
	for _, mode := range []string{"local", "daemon"} {
		t.Run(mode, func(t *testing.T) {
			var svc Service
			if mode == "local" {
				svc = NewLocal(testConfig)
				defer svc.Close()
			} else {
				_, _, svc = startDaemon(t, testConfig)
			}
			np, _ := svc.Create(Options{Argv: ticker})
			p, _ := svc.Create(Options{Argv: ticker, Persist: true})
			var s1, s2 sink
			a1, err := svc.Attach(np.ID, &s1, Size{})
			if err != nil {
				t.Fatal(err)
			}
			a2, err := svc.Attach(p.ID, &s2, Size{})
			if err != nil {
				t.Fatal(err)
			}
			waitFor(t, "output", 5*time.Second, func() bool { return strings.Contains(s1.String(), "tick 1") && strings.Contains(s2.String(), "tick 1") })
			a1.Close()
			a2.Close()
			waitFor(t, "the non-persistent program's end", 5*time.Second, func() bool { return !alive(np.Pid) })
			time.Sleep(100 * time.Millisecond)
			if !alive(p.Pid) {
				t.Fatal("closing a persistent session's view killed it")
			}
			var s3 sink
			a3, err := svc.Attach(p.ID, &s3, Size{})
			if err != nil {
				t.Fatal(err)
			}
			if err := svc.Kill(p.ID); err != nil {
				t.Fatal(err)
			}
			if alive(p.Pid) {
				t.Fatal("alive after Kill")
			}
			select {
			case <-a3.Done():
			case <-time.After(5 * time.Second):
				t.Fatal("the view did not learn of the kill")
			}
			if code, ok := a3.Exit(); !ok || code != -1 {
				t.Fatalf("exit %d %v after a kill, want -1 (a signal)", code, ok)
			}
			if l, _ := svc.List(); len(l) != 0 {
				t.Fatalf("sessions left: %+v", l)
			}
			if err := svc.Kill(p.ID); err != ErrNotFound {
				t.Fatalf("a second Kill: %v", err)
			}
		})
	}
}

// TestSlowView holds a view's sink still, then lets it drain slowly, while
// the program writes far more than the ring holds: the daemon queues no more
// than its limit and a read's worth, and the view still gets every line.
func TestSlowView(t *testing.T) {
	for _, mode := range []string{"local", "daemon"} {
		t.Run(mode, func(t *testing.T) {
			cfg := Config{RingSize: 64 << 10, LiveLimit: 32 << 10, KillGrace: 300 * time.Millisecond}
			var svc Service
			var m *manager
			if mode == "local" {
				svc = NewLocal(cfg)
				m = svc.(*local).m
				defer svc.Close()
			} else {
				var d *daemon
				_, d, svc = startDaemon(t, cfg)
				m = d.m
			}
			const lines = 200000
			in, err := svc.Create(Options{Argv: []string{"/bin/sh", "-c", fmt.Sprintf("stty -echo; echo ready; read x; seq 1 %d; echo done", lines)}})
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			hold := 500 * time.Millisecond
			s := &sink{gate: func() {
				if d := hold - time.Since(start); d > 0 {
					time.Sleep(d)
				} else {
					time.Sleep(time.Millisecond)
				}
			}}
			a, err := svc.Attach(in.ID, s, Size{})
			if err != nil {
				t.Fatal(err)
			}
			waitFor(t, "ready", 5*time.Second, func() bool { return strings.Contains(s.String(), "ready") })
			a.Write([]byte("go\n"))
			select {
			case <-a.Done():
			case <-time.After(60 * time.Second):
				t.Fatal("the output did not finish")
			}
			got := s.String()
			body := got[strings.Index(got, "ready\r\n")+len("ready\r\n"):]
			var want strings.Builder
			for i := 1; i <= lines; i++ {
				fmt.Fprintf(&want, "%d\r\n", i)
			}
			want.WriteString("done\r\n")
			if body != want.String() {
				t.Fatalf("the live stream lost or changed bytes: got %d bytes, want %d", len(body), want.Len())
			}
			ss, _ := m.get(in.ID)
			ss.mu.Lock()
			q, wrapped := ss.maxQueued, ss.ring.start > 0
			ss.mu.Unlock()
			if !wrapped {
				t.Fatal("the ring did not wrap; the test writes too little")
			}
			if limit := cfg.LiveLimit + 32<<10; q > limit {
				t.Fatalf("queued %d bytes for the view, the bound is %d", q, limit)
			}
			t.Logf("%d bytes through a held view; at most %d queued (bound %d)", len(body), q, cfg.LiveLimit+32<<10)
			a.Close()
		})
	}
}

// TestShutdownLeavesNoProcesses launches a daemon process on demand, gives
// it a program that ignores SIGHUP and has a child of its own, and shuts the
// daemon down: no process of it is left, and neither is its directory.
func TestShutdownLeavesNoProcesses(t *testing.T) {
	dir := tempDir(t)
	t.Setenv(daemonEnv, dir)
	c, err := DialOrLaunch(dir, []string{os.Args[0], "-test.run=^$"}, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	dpid := c.(*client).pid
	if dpid == os.Getpid() {
		t.Fatal("the daemon is this process")
	}
	stubborn, _ := c.Create(Options{Argv: []string{"/bin/sh", "-c", `trap "" HUP; sleep 100 & echo "child $!"; wait`}})
	plain, _ := c.Create(Options{Argv: []string{"/bin/sleep", "100"}, Persist: true})
	var s sink
	if _, err := c.Attach(stubborn.ID, &s, Size{}); err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`child (\d+)`)
	waitFor(t, "the grandchild's pid", 5*time.Second, func() bool { return re.MatchString(s.String()) })
	grandchild, _ := strconv.Atoi(re.FindStringSubmatch(s.String())[1])
	pids := []int{dpid, stubborn.Pid, grandchild, plain.Pid}
	for _, p := range pids {
		if !alive(p) {
			t.Fatalf("pid %d is not running before the shutdown", p)
		}
	}
	if err := c.Shutdown(); err != nil {
		t.Fatal(err)
	}
	// DialOrLaunch reaps the daemon; launchd or init reaps any orphan.
	waitFor(t, "no processes", 5*time.Second, func() bool {
		for _, p := range pids {
			if alive(p) {
				return false
			}
		}
		return true
	})
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		e, _ := os.ReadDir(dir)
		t.Fatalf("the daemon's directory is still there: %v %v", err, e)
	}
	if _, err := Dial(dir); err == nil {
		t.Fatal("a daemon still answers")
	}
}

func TestRingReplayStartsAtASafeBoundary(t *testing.T) {
	r := newRing(4096)
	var w bytes.Buffer
	for i := 0; w.Len() < 3*4096; i++ {
		// A title (an OSC string with a newline in it), colours, a CJK
		// character, a line.
		fmt.Fprintf(&w, "\x1b]0;title\n%d\x07\x1b[3%dm%d 漢字\x1b[0m\r\n", i, i%8, i)
	}
	b := w.Bytes()
	for len(b) > 0 { // in uneven pieces, as a terminal reads them
		k := min(len(b), 777)
		r.write(b[:k])
		b = b[k:]
	}
	snap, lost := r.snapshot(nil)
	if !lost || len(snap) == 0 || len(snap) > 4096 {
		t.Fatalf("snapshot of %d bytes, lost %v", len(snap), lost)
	}
	if !bytes.HasSuffix(w.Bytes(), snap) {
		t.Fatal("the snapshot is not the stream's tail")
	}
	if !bytes.HasPrefix(snap, []byte("\x1b]0;title\n")) {
		t.Fatalf("the replay starts mid-sequence: %q", snap[:min(len(snap), 24)])
	}
	r = newRing(4096)
	r.write([]byte("short"))
	if snap, lost := r.snapshot(nil); string(snap) != "short" || lost {
		t.Fatalf("an unwrapped ring replays %q, lost %v", snap, lost)
	}
}
