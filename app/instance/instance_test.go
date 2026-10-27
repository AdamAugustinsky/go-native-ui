package instance

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---- a second process ----------------------------------------------------------
//
// The test binary runs itself as the other copy of the application:
// GNUI_INSTANCE_HELPER names what it does, GNUI_INSTANCE_DIR the directory.

func TestHelperProcess(t *testing.T) {
	mode := os.Getenv("GNUI_INSTANCE_HELPER")
	if mode == "" {
		t.Skip("only run as a helper process")
	}
	dir := os.Getenv("GNUI_INSTANCE_DIR")
	p, s, err := AcquireWith("dev.gonativeui.test", Options{Dir: dir, Timeout: 3 * time.Second})
	if err != nil {
		fmt.Println("error", err)
		os.Exit(2)
	}
	switch mode {
	case "send": // acts as a second copy: send the arguments after "--" and report
		if p != nil {
			fmt.Println("primary")
			os.Exit(4)
		}
		args := os.Args[slices.Index(os.Args, "--")+1:]
		if err := s.Send(args); err != nil {
			fmt.Println("send failed:", err)
			os.Exit(3)
		}
		fmt.Println("sent")
		os.Exit(0)
	case "hold": // acts as the first copy: print each request, exit when stdin closes
		if p == nil {
			fmt.Println("secondary")
			os.Exit(0)
		}
		fmt.Println("primary")
		go func() { io.Copy(io.Discard, os.Stdin); p.Close(); os.Exit(0) }()
		for {
			for _, r := range p.Poll(nil) {
				b, _ := json.Marshal(r)
				fmt.Println("request", string(b))
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	os.Exit(2)
}

type child struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	lines chan string
}

func startChild(t *testing.T, mode, dir string, args ...string) *child {
	t.Helper()
	cmd := exec.Command(os.Args[0], append([]string{"-test.run=^TestHelperProcess$", "--"}, args...)...)
	cmd.Env = append(os.Environ(), "GNUI_INSTANCE_HELPER="+mode, "GNUI_INSTANCE_DIR="+dir)
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c := &child{cmd: cmd, stdin: in, lines: make(chan string, 64)}
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			l := sc.Text()
			if l == "PASS" || strings.HasPrefix(l, "=== ") || strings.HasPrefix(l, "--- ") {
				continue
			}
			c.lines <- l
		}
		close(c.lines)
	}()
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	return c
}

func (c *child) line(t *testing.T) string {
	t.Helper()
	select {
	case l, ok := <-c.lines:
		if !ok {
			t.Fatal("the helper process ended without the expected output")
		}
		return l
	case <-time.After(15 * time.Second):
		t.Fatal("the helper process printed nothing")
	}
	return ""
}

func (c *child) wait(t *testing.T) (code int) {
	t.Helper()
	c.stdin.Close()
	err := c.cmd.Wait()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	if err != nil {
		t.Fatal(err)
	}
	return 0
}

// ---- helpers ---------------------------------------------------------------------

// testID is the application id the tests use.
const testID = "dev.gonativeui.test"

func newDir(t *testing.T) string { return t.TempDir() }

// endpoint is the address of the socket or pipe for dir.
func endpoint(t *testing.T, dir string) string {
	t.Helper()
	_, addr, err := setup(testID, Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	return addr
}

func acquire(t *testing.T, dir string, o Options) (*Primary, *Secondary) {
	t.Helper()
	o.Dir = dir
	p, s, err := AcquireWith(testID, o)
	if err != nil {
		t.Fatal(err)
	}
	if (p == nil) == (s == nil) {
		t.Fatalf("Acquire returned primary %v and secondary %v", p, s)
	}
	if p != nil {
		t.Cleanup(func() { p.Close() })
	}
	return p, s
}

func argsOf(rs []Request) [][]string {
	var out [][]string
	for _, r := range rs {
		out = append(out, r.Args)
	}
	return out
}

// ---- tests -----------------------------------------------------------------------

func TestPrimaryThenSecondary(t *testing.T) {
	dir := newDir(t)
	p, s := acquire(t, dir, Options{})
	if p == nil {
		t.Fatal("the first Acquire should be the primary")
	}
	if _, s2 := acquire(t, dir, Options{}); s2 == nil {
		t.Fatal("the second Acquire should be a secondary")
	} else {
		s = s2
	}
	wd, _ := os.Getwd()
	var woke atomic.Int32
	old := wake
	wake = func() { woke.Add(1) }
	defer func() { wake = old }()
	if err := s.Send([]string{"--open", "a b.txt", "ünï"}); err != nil {
		t.Fatal(err)
	}
	got := p.Poll(nil)
	if len(got) != 1 || !slices.Equal(got[0].Args, []string{"--open", "a b.txt", "ünï"}) || got[0].Dir != wd {
		t.Fatalf("requests %+v (working directory %q)", got, wd)
	}
	if again := p.Poll(nil); len(again) != 0 {
		t.Errorf("a request was delivered twice: %+v", again)
	}
	if woke.Load() != 1 || p.Serial() != 1 {
		t.Errorf("wakes %d, serial %d, want 1 and 1", woke.Load(), p.Serial())
	}
	if err := s.Send(nil); err != nil { // no arguments is a request too ("bring me to the front")
		t.Fatal(err)
	}
	if got := p.Poll(nil); len(got) != 1 || len(got[0].Args) != 0 {
		t.Errorf("empty request: %+v", got)
	}
}

func TestSecondProcessSendsToPrimary(t *testing.T) {
	dir := newDir(t)
	p, _ := acquire(t, dir, Options{})
	c := startChild(t, "send", dir, "one", "two words")
	if line := c.line(t); line != "sent" {
		t.Fatalf("helper said %q", line)
	}
	if code := c.wait(t); code != 0 {
		t.Fatalf("helper exited %d", code)
	}
	got := p.Poll(nil)
	if len(got) != 1 || !slices.Equal(got[0].Args, []string{"one", "two words"}) {
		t.Fatalf("requests %+v", got)
	}
}

func TestPrimaryProcessReceivesFromThisOne(t *testing.T) {
	dir := newDir(t)
	c := startChild(t, "hold", dir)
	if line := c.line(t); line != "primary" {
		t.Fatalf("helper said %q", line)
	}
	p, s := acquire(t, dir, Options{})
	if p != nil || s == nil {
		t.Fatal("this process should be the secondary")
	}
	if err := s.Send([]string{"from", "the test"}); err != nil {
		t.Fatal(err)
	}
	line := c.line(t)
	var r Request
	if !strings.HasPrefix(line, "request ") || json.Unmarshal([]byte(strings.TrimPrefix(line, "request ")), &r) != nil || !slices.Equal(r.Args, []string{"from", "the test"}) {
		t.Fatalf("helper printed %q", line)
	}
	// The primary leaves cleanly: the lock is free for the next copy.
	if code := c.wait(t); code != 0 {
		t.Fatalf("helper exited %d", code)
	}
	if p2, s2 := acquire(t, dir, Options{}); p2 == nil || s2 != nil {
		t.Fatal("after the primary quit, the next copy should be the primary")
	}
}

// Options.Notify runs when a second process's request has been accepted, with
// the request already there to poll: the app can post an event from it.
func TestNotifyOnRequestFromSecondProcess(t *testing.T) {
	dir := newDir(t)
	notified := make(chan int, 4)
	var pp atomic.Pointer[Primary]
	p, _ := acquire(t, dir, Options{Notify: func() { notified <- len(pp.Load().Poll(nil)) }})
	pp.Store(p)
	c := startChild(t, "send", dir, "open", "x")
	if line := c.line(t); line != "sent" {
		t.Fatalf("helper said %q", line)
	}
	select {
	case n := <-notified:
		if n != 1 {
			t.Errorf("the request was not stored when Notify ran: %d polled", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Notify was not called")
	}
}

func TestBusyAndRejectedRequestsAreNeverSeen(t *testing.T) {
	dir := newDir(t)
	p, _ := acquire(t, dir, Options{Queue: 2, Timeout: 300 * time.Millisecond})
	_, s := acquire(t, dir, Options{Queue: 2, Timeout: 300 * time.Millisecond})
	for _, a := range []string{"1", "2"} {
		if err := s.Send([]string{a}); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	err := s.Send([]string{"3"})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("a third request into a ring of two = %v, want ErrBusy", err)
	}
	if d := time.Since(start); d < 250*time.Millisecond || d > 3*time.Second {
		t.Errorf("Send retried for %v, want about the timeout (300ms)", d)
	}
	if got := argsOf(p.Poll(nil)); !slices.EqualFunc(got, [][]string{{"1"}, {"2"}}, slices.Equal) {
		t.Fatalf("the primary saw %q, want only the two accepted", got)
	}
	// Room again: a later request is accepted, and the refused one never shows up.
	if err := s.Send([]string{"4"}); err != nil {
		t.Fatal(err)
	}
	if got := argsOf(p.Poll(nil)); !slices.EqualFunc(got, [][]string{{"4"}}, slices.Equal) {
		t.Fatalf("the primary saw %q", got)
	}
}

// A busy answer is retried: a request refused at first is accepted as soon as
// the primary has room, within the timeout.
func TestBusyRequestIsRetriedUntilThereIsRoom(t *testing.T) {
	dir := newDir(t)
	p, _ := acquire(t, dir, Options{Queue: 1})
	_, s := acquire(t, dir, Options{Queue: 1, Timeout: 5 * time.Second})
	if err := s.Send([]string{"first"}); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		p.Poll(nil)
	}()
	start := time.Now()
	if err := s.Send([]string{"second"}); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 250*time.Millisecond {
		t.Errorf("accepted after %v: it should have waited for room", d)
	}
	if got := argsOf(p.Poll(nil)); !slices.EqualFunc(got, [][]string{{"second"}}, slices.Equal) {
		t.Fatalf("the primary saw %q", got)
	}
}

func TestManySendersAreEitherAcceptedOrToldSo(t *testing.T) {
	dir := newDir(t)
	p, _ := acquire(t, dir, Options{Queue: 8})
	_, s := acquire(t, dir, Options{Timeout: 400 * time.Millisecond})
	var accepted, refused atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch err := s.Send([]string{fmt.Sprint(i)}); {
			case err == nil:
				accepted.Add(1)
			case errors.Is(err, ErrBusy), errors.Is(err, ErrNoAnswer):
				refused.Add(1)
			default:
				t.Errorf("Send(%d) = %v", i, err)
			}
		}()
	}
	var seen []Request
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
loop:
	for {
		select {
		case <-done:
			break loop
		case <-time.After(15 * time.Millisecond): // drain in bursts, so that the ring fills and empties
			seen = p.Poll(seen)
		}
	}
	seen = p.Poll(seen)
	t.Logf("%d accepted, %d refused", accepted.Load(), refused.Load())
	if int32(len(seen)) != accepted.Load() {
		t.Errorf("the primary saw %d requests, %d senders were told accepted (%d refused)", len(seen), accepted.Load(), refused.Load())
	}
	uniq := map[string]bool{}
	for _, r := range seen {
		uniq[r.Args[0]] = true
	}
	if len(uniq) != len(seen) {
		t.Errorf("a request was delivered twice: %v", argsOf(seen))
	}
}

// The lock is held but nothing listens: a primary that is not up yet, or is
// stuck.
func TestNoAnswerWhenNothingListens(t *testing.T) {
	dir := newDir(t)
	lockKey, _, err := setup(testID, Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	lk, got, err := acquireLock(lockKey)
	if err != nil || !got {
		t.Fatalf("acquireLock: %v %v", got, err)
	}
	defer lk.release()
	p, s, err := AcquireWith(testID, Options{Dir: dir, Timeout: 300 * time.Millisecond})
	if err != nil || p != nil || s == nil {
		t.Fatalf("with the lock held elsewhere Acquire = %v, %v, %v", p, s, err)
	}
	start := time.Now()
	if err := s.Send([]string{"x"}); !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("Send = %v, want ErrNoAnswer", err)
	}
	if d := time.Since(start); d < 250*time.Millisecond || d > 3*time.Second {
		t.Errorf("Send retried for %v", d)
	}
}

// A primary that takes the connection and says nothing.
func TestNoAnswerFromAHungPrimary(t *testing.T) {
	dir := newDir(t)
	lockKey, _, err := setup(testID, Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	lk, _, _ := acquireLock(lockKey)
	defer lk.release()
	ln, err := listen(endpoint(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var conns []conn
	var mu sync.Mutex
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c) // held open, never answered
			mu.Unlock()
		}
	}()
	defer func() {
		mu.Lock()
		for _, c := range conns {
			c.Close()
		}
		mu.Unlock()
	}()
	_, s, err := AcquireWith(testID, Options{Dir: dir, Timeout: 500 * time.Millisecond})
	if err != nil || s == nil {
		t.Fatalf("Acquire = %v, %v", s, err)
	}
	start := time.Now()
	if err := s.Send([]string{"x"}); !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("Send = %v, want ErrNoAnswer", err)
	}
	if d := time.Since(start); d > 4*time.Second {
		t.Errorf("Send took %v against a hung primary", d)
	}
}

func TestMalformedRequestsAreRejectedAndHarmless(t *testing.T) {
	dir := newDir(t)
	p, _ := acquire(t, dir, Options{})
	sock := endpoint(t, dir)
	raw := func(b []byte) byte {
		c, err := rawDial(sock)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(3 * time.Second))
		c.Write(b)
		var r [1]byte
		if _, err := io.ReadFull(c, r[:]); err != nil {
			return 0
		}
		return r[0]
	}
	frame := func(body string) []byte {
		return append(binary.BigEndian.AppendUint32(nil, uint32(len(body))), body...)
	}
	for name, b := range map[string][]byte{
		"not JSON":        frame("hello"),
		"wrong JSON type": frame(`[1,2,3]`),
		"zero length":     {0, 0, 0, 0},
		"too large":       binary.BigEndian.AppendUint32(nil, maxFrame+1),
		"args not text":   frame(`{"args":[1,2]}`),
	} {
		if got := raw(b); got != replyRejected {
			t.Errorf("%s: answered %q, want rejected", name, got)
		}
	}
	// A half-sent request does not hold the primary up for others.
	slow, err := rawDial(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Close()
	slow.Write([]byte{0, 0, 1}) // 3 bytes of a 4-byte length, then silence
	_, s := acquire(t, dir, Options{})
	if err := s.Send([]string{"still", "works"}); err != nil {
		t.Fatal(err)
	}
	if got := p.Poll(nil); len(got) != 1 || !slices.Equal(got[0].Args, []string{"still", "works"}) {
		t.Errorf("requests %+v", got)
	}
	big := make([]string, 0, 4)
	for i := 0; i < 4; i++ {
		big = append(big, strings.Repeat("x", maxFrame/2))
	}
	if err := s.Send(big); !errors.Is(err, ErrRejected) {
		t.Errorf("Send of %d KiB = %v, want ErrRejected", len(big)*maxFrame/2/1024, err)
	}
}

func TestCloseFreesTheLockAndTheSocket(t *testing.T) {
	dir := newDir(t)
	p, _ := acquire(t, dir, Options{})
	sock := endpoint(t, dir)
	if runtime.GOOS != "windows" {
		if _, err := os.Lstat(sock); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal("a second Close:", err)
	}
	if runtime.GOOS != "windows" {
		if _, err := os.Lstat(sock); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the socket file is still there: %v", err)
		}
	} else if c, err := rawDial(sock); err == nil {
		c.Close()
		t.Error("the pipe is still there after Close")
	}
	p2, s2 := acquire(t, dir, Options{})
	if p2 == nil || s2 != nil {
		t.Fatal("after Close the next Acquire should be the primary")
	}
}

// Many copies start at once, in this process and in others: exactly one wins.
func TestRacingPrimaries(t *testing.T) {
	dir := newDir(t)
	const n = 24
	var primaries atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	var held []*Primary
	var mu sync.Mutex
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			p, s, err := AcquireWith(testID, Options{Dir: dir})
			switch {
			case err != nil:
				t.Error(err)
			case p != nil && s == nil:
				primaries.Add(1)
				mu.Lock()
				held = append(held, p)
				mu.Unlock()
			case p == nil && s != nil:
			default:
				t.Errorf("both or neither: %v %v", p, s)
			}
		}()
	}
	close(start)
	wg.Wait()
	if primaries.Load() != 1 {
		t.Errorf("%d primaries among %d racing Acquires, want 1", primaries.Load(), n)
	}
	for _, p := range held {
		p.Close()
	}
}

func TestRacingProcesses(t *testing.T) {
	dir := newDir(t)
	const n = 6
	var cs []*child
	for i := 0; i < n; i++ {
		cs = append(cs, startChild(t, "hold", dir))
	}
	primaries := 0
	for _, c := range cs {
		switch line := c.line(t); line {
		case "primary":
			primaries++
		case "secondary":
		default:
			t.Fatalf("helper said %q", line)
		}
	}
	if primaries != 1 {
		t.Errorf("%d primaries among %d racing processes, want 1", primaries, n)
	}
	for _, c := range cs {
		c.wait(t)
	}
}

// A second instance's arguments of the app's schemes are taken out of the
// request and handed to the application object as URLs (EvOpenURLs); the
// rest is polled as before, and a request of URLs only is not polled at all.
// A full ring of URLs answers "busy" and stores nothing.
func TestSchemeArgumentsBecomeOpenURLs(t *testing.T) {
	var offered [][]string
	refuse := false
	old := offerURLs
	offerURLs = func(u []string) bool {
		if refuse {
			return false
		}
		offered = append(offered, slices.Clone(u))
		return true
	}
	defer func() { offerURLs = old }()
	dir := newDir(t)
	p, _ := acquire(t, dir, Options{Schemes: []string{"notes-dev"}, Timeout: 200 * time.Millisecond})
	_, s := acquire(t, dir, Options{Timeout: 200 * time.Millisecond})
	if err := s.Send([]string{"notes-dev:x"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Send([]string{"--new", "NOTES-DEV://y?z=1", "notes-devx:no", "notes-dev"}); err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"notes-dev:x"}, {"NOTES-DEV://y?z=1"}}; !slices.EqualFunc(offered, want, slices.Equal) {
		t.Errorf("offered %q, want %q", offered, want)
	}
	if got := argsOf(p.Poll(nil)); len(got) != 1 || !slices.Equal(got[0], []string{"--new", "notes-devx:no", "notes-dev"}) {
		t.Errorf("polled %q, want the second request without its URL", got)
	}
	refuse = true
	if err := s.Send([]string{"notes-dev:full"}); !errors.Is(err, ErrBusy) {
		t.Errorf("a refused URL: %v, want ErrBusy", err)
	}
	if got := p.Poll(nil); len(got) != 0 {
		t.Errorf("a refused request was stored: %+v", got)
	}
}
