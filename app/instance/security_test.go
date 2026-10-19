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
// has been read. It returns a secondary for it.
func fakePrimary(t *testing.T, dir string, handle func(n int, c conn)) *Secondary {
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
	t.Cleanup(func() { ln.Close(); wg.Wait(); lk.release() })
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 1; ; n++ {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.SetDeadline(time.Now().Add(3 * time.Second))
			readRequest(c)
			handle(n, c)
			c.Close()
		}
	}()
	_, s, err := AcquireWith(testID, Options{Dir: dir, Timeout: time.Second})
	if err != nil || s == nil {
		t.Fatalf("AcquireWith: %v %v", s, err)
	}
	return s
}

// ErrBusy means "not stored". If an earlier attempt reached the primary and its
// answer never came, "busy" afterwards cannot say that: ErrUncertain.
func TestSendReportsUncertainWhenAnEarlierAttemptMayHaveBeenStored(t *testing.T) {
	s := fakePrimary(t, newDir(t), func(n int, c conn) {
		if n > 1 {
			c.Write([]byte{replyBusy}) // the first one: the answer is lost
		}
	})
	if err := s.Send([]string{"x"}); !errors.Is(err, ErrUncertain) {
		t.Fatalf("Send = %v, want ErrUncertain", err)
	}
}

func TestSendReportsBusyWhenNothingWasStored(t *testing.T) {
	s := fakePrimary(t, newDir(t), func(n int, c conn) { c.Write([]byte{replyBusy}) })
	if err := s.Send([]string{"x"}); !errors.Is(err, ErrBusy) {
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
