package instance

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func rawSend(t *testing.T, sock, body string, waitReply bool) (byte, bool) {
	t.Helper()
	c, err := rawDial(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	c.Write(append(binary.BigEndian.AppendUint32(nil, uint32(len(body))), body...))
	if !waitReply {
		return 0, false // the connection drops: the sender never learns the answer
	}
	var r [1]byte
	if _, err := io.ReadFull(c, r[:]); err != nil {
		return 0, false
	}
	return r[0], true
}

// An answer that is lost makes the sender retry the same request; the primary
// must not store it twice.
func TestRetryOfAnAcceptedRequestIsStoredOnce(t *testing.T) {
	dir := newDir(t)
	p, _ := acquire(t, dir, Options{})
	sock := endpoint(t, dir)
	body := `{"id":"req-1","args":["open","a.txt"],"dir":"/w"}`
	rawSend(t, sock, body, false) // accepted, answer lost
	deadline := time.Now().Add(3 * time.Second)
	for p.Serial() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if r, ok := rawSend(t, sock, body, true); !ok || r != replyAccepted {
		t.Fatalf("the retry was answered %q (%v), want accepted", r, ok)
	}
	if r, ok := rawSend(t, sock, body, true); !ok || r != replyAccepted {
		t.Fatalf("the second retry was answered %q (%v), want accepted", r, ok)
	}
	if got := p.Poll(nil); len(got) != 1 {
		t.Fatalf("the primary saw %d copies of one request: %+v", len(got), got)
	}
	// Even after it has been collected, a late retry is not a new request.
	if r, _ := rawSend(t, sock, body, true); r != replyAccepted {
		t.Errorf("answered %q", r)
	}
	if got := p.Poll(nil); len(got) != 0 {
		t.Errorf("a retry after Poll was delivered again: %+v", got)
	}
	// A different ID is a different request, even with the same content.
	if r, _ := rawSend(t, sock, `{"id":"req-2","args":["open","a.txt"],"dir":"/w"}`, true); r != replyAccepted {
		t.Fatalf("answered %q", r)
	}
	if got := p.Poll(nil); len(got) != 1 {
		t.Errorf("the second request: %+v", got)
	}
	// A request with no ID cannot be told from its retries: refused.
	if r, _ := rawSend(t, sock, `{"args":["x"]}`, true); r != replyRejected {
		t.Errorf("no ID: answered %q, want rejected", r)
	}
}

// The same through Send: the first answer is dropped, Send retries within its
// timeout, and the primary has the request once.
func TestSendWithALostAnswerDeliversOnce(t *testing.T) {
	dir := newDir(t)
	p, _ := acquire(t, dir, Options{})
	_, s := acquire(t, dir, Options{Timeout: 3 * time.Second})
	dropAcks.Store(2) // two answers lost in a row
	t.Cleanup(func() { dropAcks.Store(0) })
	if err := s.Send([]string{"once"}); err != nil {
		t.Fatal(err)
	}
	if got := argsOf(p.Poll(nil)); !slices.EqualFunc(got, [][]string{{"once"}}, slices.Equal) {
		t.Fatalf("the primary saw %q, want one request", got)
	}
}

// setDedup shrinks the primary's memory and gives the test a clock it can move.
// Call it before Acquire.
func setDedup(t *testing.T, size int) (advance func(time.Duration)) {
	t.Helper()
	var skew atomic.Int64
	oldSize, oldNow := dedupSize, nowFn
	dedupSize = size
	nowFn = func() time.Time { return time.Now().Add(time.Duration(skew.Load())) }
	t.Cleanup(func() { dedupSize, nowFn = oldSize, oldNow })
	return func(d time.Duration) { skew.Add(int64(d)) }
}

func idBody(i int) string { return fmt.Sprintf(`{"id":"id-%d","args":["x"]}`, i) }

// The memory is bounded, and forgets an ID only when it is old and its request has
// been collected.
func TestDedupTableIsBounded(t *testing.T) {
	advance := setDedup(t, 8)
	dir := newDir(t)
	p, _ := acquire(t, dir, Options{Queue: 4})
	sock := endpoint(t, dir)
	for i := 0; i < 30; i++ {
		if r, _ := rawSend(t, sock, idBody(i), true); r != replyAccepted {
			t.Fatalf("request %d answered %q", i, r)
		}
		p.Poll(nil)
		advance(dedupHorizon + time.Second)
	}
	p.mu.Lock()
	n, order := len(p.seen), len(p.order)
	p.mu.Unlock()
	if n != 8 || order != 8 {
		t.Errorf("the table holds %d IDs (%d in order), want exactly 8", n, order)
	}
	if r, _ := rawSend(t, sock, idBody(29), true); r != replyAccepted || len(p.Poll(nil)) != 0 {
		t.Error("a recent retry was stored again")
	}
}

// An ID is not forgotten while a retry can still come: not while its request is
// queued, and not within the horizon. When the table is full of such IDs the
// primary says "busy" instead of forgetting one.
func TestDedupKeepsLiveIDs(t *testing.T) {
	advance := setDedup(t, 4)
	dir := newDir(t)
	p, _ := acquire(t, dir, Options{Queue: 16})
	sock := endpoint(t, dir)
	for i := 0; i < 4; i++ {
		if r, _ := rawSend(t, sock, idBody(i), true); r != replyAccepted {
			t.Fatalf("request %d answered %q", i, r)
		}
	}
	// Full, nothing collected: a new request waits; it does not push out id-0.
	if r, _ := rawSend(t, sock, idBody(4), true); r != replyBusy {
		t.Fatalf("a request that would evict a queued ID was answered %q, want busy", r)
	}
	if r, _ := rawSend(t, sock, idBody(0), true); r != replyAccepted {
		t.Fatalf("the retry of id-0 was answered %q", r)
	}
	if got := p.Poll(nil); len(got) != 4 {
		t.Fatalf("the primary saw %d requests, want 4 (id-0 once)", len(got))
	}
	// Collected, but within the horizon: a retry may still arrive.
	advance(dedupHorizon / 2)
	if r, _ := rawSend(t, sock, idBody(4), true); r != replyBusy {
		t.Fatalf("a request that would evict an ID within the horizon was answered %q, want busy", r)
	}
	if r, _ := rawSend(t, sock, idBody(0), true); r != replyAccepted || len(p.Poll(nil)) != 0 {
		t.Fatal("the retry of id-0 within the horizon was stored again")
	}
	// Past the horizon the oldest goes, and the new request fits.
	advance(dedupHorizon)
	if r, _ := rawSend(t, sock, idBody(4), true); r != replyAccepted {
		t.Fatalf("after the horizon: answered %q", r)
	}
	if got := p.Poll(nil); len(got) != 1 {
		t.Fatalf("after the horizon: %+v", got)
	}
}

// fakePrimary holds the lock and listens like a primary; handle is called with
// the number of the connection (from 1) and the connection, after the request
// has been read. It returns a secondary for it, and the log of the
// connections it handles.
func fakePrimary(t *testing.T, dir string, handle func(n int, c conn)) (*Secondary, *fakeLog) {
	t.Helper()
	lockKey, addr, err := setup(testID, Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	lk, got, err := acquireLock(lockKey)
	if err != nil || !got {
		t.Fatalf("acquireLock: %v %v", got, err)
	}
	ln, err := listen(addr)
	if err != nil {
		lk.release()
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	fl := &fakeLog{t0: time.Now()}
	t.Cleanup(func() { ln.Close(); wg.Wait(); lk.release() })
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 1; ; n++ {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			at := fl.since()
			c.SetDeadline(time.Now().Add(3 * time.Second))
			readRequest(c)
			handle(n, c)
			c.Close()
			fl.add(fakeConn{at, fl.since()})
		}
	}()
	_, s, err := AcquireWith(testID, Options{Dir: dir, Timeout: time.Second})
	if err != nil || s == nil {
		t.Fatalf("AcquireWith: %v %v", s, err)
	}
	return s, fl
}

// A fakeLog is when the fake primary accepted and finished each connection,
// from the last start.
type fakeLog struct {
	mu    sync.Mutex
	t0    time.Time
	conns []fakeConn
}

type fakeConn struct{ accepted, done time.Duration }

func (l *fakeLog) start() { l.mu.Lock(); l.t0, l.conns = time.Now(), nil; l.mu.Unlock() }

func (l *fakeLog) since() time.Duration { l.mu.Lock(); defer l.mu.Unlock(); return time.Since(l.t0) }

func (l *fakeLog) add(c fakeConn) { l.mu.Lock(); l.conns = append(l.conns, c); l.mu.Unlock() }

// starvedAfter is the fake primary's evidence of a starved process, or "": it
// answers at once, in this process, so a connection it accepted more than
// starvedGap after the start or after it finished the previous one, or took
// longer than that over, is time its goroutine did not get. Then a Send
// attempt can run out of its time with the answer still to come (CI, macOS
// and Windows under the whole suite: "did not answer" at exactly 1.00 s), and
// the answers prove nothing about Send. by is how long the log covers.
func (l *fakeLog) starvedAfter(by time.Duration) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return starved(l.conns, by)
}

const starvedGap = 500 * time.Millisecond

func starved(conns []fakeConn, by time.Duration) string {
	prev := time.Duration(0)
	for i, c := range conns {
		switch {
		case c.accepted-prev > starvedGap:
			return fmt.Sprintf("the fake primary accepted connection %d %v after it was free (log %v)", i+1, c.accepted-prev, conns)
		case c.done-c.accepted > starvedGap:
			return fmt.Sprintf("the fake primary took %v over connection %d (log %v)", c.done-c.accepted, i+1, conns)
		}
		prev = c.done
	}
	if by-prev > starvedGap+250*time.Millisecond { // Send's backoff is 200 ms at most
		return fmt.Sprintf("the fake primary accepted nothing in the last %v of %v (log %v)", by-prev, by, conns)
	}
	return ""
}

// The starvation evidence: a fake primary that kept up says nothing; one that
// was late to a connection, slow over one, or idle while Send waited does.
func TestStarvedEvidence(t *testing.T) {
	ms := time.Millisecond
	for _, c := range []struct {
		name  string
		conns []fakeConn
		by    time.Duration
		want  bool
	}{
		{"kept up", []fakeConn{{1 * ms, 2 * ms}, {30 * ms, 31 * ms}, {80 * ms, 81 * ms}, {180 * ms, 181 * ms}, {390 * ms, 391 * ms}, {600 * ms, 601 * ms}, {810 * ms, 811 * ms}}, 1000 * ms, false},
		{"late to the first", []fakeConn{{1002 * ms, 1003 * ms}}, 1003 * ms, true},
		{"slow over one", []fakeConn{{1 * ms, 1200 * ms}}, 1200 * ms, true},
		{"nothing accepted", nil, 1000 * ms, true},
		{"idle at the end", []fakeConn{{1 * ms, 2 * ms}}, 1000 * ms, true},
	} {
		if got := starved(c.conns, c.by) != ""; got != c.want {
			t.Errorf("%s: starved %v, want %v", c.name, got, c.want)
		}
	}
}

// ErrBusy means "not stored". If an earlier attempt reached the primary and its
// answer never came, "busy" afterwards cannot say that: ErrUncertain.
func TestSendReportsUncertainWhenAnEarlierAttemptMayHaveBeenStored(t *testing.T) {
	s, fl := fakePrimary(t, newDir(t), func(n int, c conn) {
		if n > 1 {
			c.Write([]byte{replyBusy}) // the first one: the answer is lost
		}
	})
	fl.start()
	if err := s.Send([]string{"x"}); !errors.Is(err, ErrUncertain) {
		if why := fl.starvedAfter(fl.since()); why != "" {
			t.Skipf("Send = %v, but the process was starved: %s", err, why)
		}
		t.Fatalf("Send = %v, want ErrUncertain", err)
	}
}

func TestSendReportsBusyWhenNothingWasStored(t *testing.T) {
	s, fl := fakePrimary(t, newDir(t), func(n int, c conn) { c.Write([]byte{replyBusy}) })
	fl.start()
	if err := s.Send([]string{"x"}); !errors.Is(err, ErrBusy) {
		if why := fl.starvedAfter(fl.since()); why != "" {
			t.Skipf("Send = %v, but the process was starved: %s", err, why)
		}
		t.Fatalf("Send = %v, want ErrBusy", err)
	}
	// A real primary whose queue is full: busy, and nothing stored.
	dir := newDir(t)
	p, _ := acquire(t, dir, Options{Queue: 1})
	_, s2 := acquire(t, dir, Options{Timeout: 300 * time.Millisecond})
	if err := s2.Send([]string{"a"}); err != nil {
		t.Fatal(err)
	}
	if err := s2.Send([]string{"b"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("Send to a full queue = %v, want ErrBusy", err)
	}
	if got := p.Poll(nil); len(got) != 1 {
		t.Fatalf("stored %+v", got)
	}
}

// Only this user's processes are served: a peer that fails the check gets no
// answer and no request is stored.
func TestOtherPeersAreNotServed(t *testing.T) {
	dir := newDir(t)
	p, _ := acquire(t, dir, Options{})
	_, s := acquire(t, dir, Options{Timeout: 300 * time.Millisecond})
	setPeer(t, func(conn) error { return errors.New("a stranger") })
	if err := s.Send([]string{"from a stranger"}); !errors.Is(err, ErrNoAnswer) {
		t.Errorf("a peer that fails the check: Send = %v, want ErrNoAnswer", err)
	}
	if got := p.Poll(nil); len(got) != 0 {
		t.Errorf("the primary stored %+v", got)
	}
	setPeer(t, func(conn) error { return nil })
	if err := s.Send([]string{"mine"}); err != nil {
		t.Errorf("a peer that passes: %v", err)
	}
	if got := p.Poll(nil); len(got) != 1 {
		t.Errorf("stored %+v, want 1 request", got)
	}
}

// setPeer replaces the question about a connection's peer for the test.
func setPeer(t *testing.T, f func(conn) error) {
	t.Helper()
	peerHook.Store(&f)
	t.Cleanup(func() { peerHook.Store(nil) })
}
