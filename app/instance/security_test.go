package instance

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"testing"
	"time"
)

func rawSend(t *testing.T, sock, body string, waitReply bool) (byte, bool) {
	t.Helper()
	c, err := net.DialTimeout("unix", sock, time.Second)
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
	sock, _ := socketPath(dir)
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

func TestDedupTableIsBounded(t *testing.T) {
	dir := newDir(t)
	p, _ := acquire(t, dir, Options{Queue: 4})
	sock, _ := socketPath(dir)
	for i := 0; i < dedupSize+40; i++ {
		if r, _ := rawSend(t, sock, fmt.Sprintf(`{"id":"id-%d","args":["x"]}`, i), true); r != replyAccepted {
			t.Fatalf("request %d answered %q", i, r)
		}
		p.Poll(nil)
	}
	p.mu.Lock()
	n, order := len(p.seen), len(p.seenOrder)
	p.mu.Unlock()
	if n != dedupSize || order != dedupSize {
		t.Errorf("the table holds %d IDs (%d in order), want exactly %d", n, order, dedupSize)
	}
	// The latest are still remembered, the earliest are not.
	if r, _ := rawSend(t, sock, fmt.Sprintf(`{"id":"id-%d","args":["x"]}`, dedupSize+39), true); r != replyAccepted || len(p.Poll(nil)) != 0 {
		t.Error("a recent retry was stored again")
	}
}

// Only this user's processes are served: a peer of another user, or one the
// system cannot identify when it should, gets no answer and no request is stored.
func TestOtherPeersAreNotServed(t *testing.T) {
	dir := newDir(t)
	p, _ := acquire(t, dir, Options{})
	_, s := acquire(t, dir, Options{Timeout: 300 * time.Millisecond})
	setPeer(t, func(net.Conn) (uint32, bool, error) { return uint32(euid()) + 1, true, nil })
	if err := s.Send([]string{"from a stranger"}); !errors.Is(err, ErrNoAnswer) {
		t.Errorf("a peer of another user: Send = %v, want ErrNoAnswer", err)
	}
	setPeer(t, func(net.Conn) (uint32, bool, error) { return 0, true, errors.New("no credentials") })
	if err := s.Send([]string{"unknown"}); !errors.Is(err, ErrNoAnswer) {
		t.Errorf("a peer that cannot be identified: Send = %v, want ErrNoAnswer", err)
	}
	if got := p.Poll(nil); len(got) != 0 {
		t.Errorf("the primary stored %+v", got)
	}
	// Where the system cannot identify peers at all, the directory decides.
	setPeer(t, func(net.Conn) (uint32, bool, error) { return 0, false, nil })
	if err := s.Send([]string{"fine"}); err != nil {
		t.Errorf("unsupported peer check: %v", err)
	}
	want := 1
	if euid() >= 0 { // Windows has no user ids, and never reports a peer
		setPeer(t, func(net.Conn) (uint32, bool, error) { return uint32(euid()), true, nil })
		if err := s.Send([]string{"mine"}); err != nil {
			t.Errorf("own uid: %v", err)
		}
		want = 2
	}
	if got := p.Poll(nil); len(got) != want {
		t.Errorf("stored %+v, want %d requests", got, want)
	}
}

// setPeer replaces the question about a connection's peer for the test.
func setPeer(t *testing.T, f func(net.Conn) (uint32, bool, error)) {
	t.Helper()
	peerHook.Store(&f)
	t.Cleanup(func() { peerHook.Store(nil) })
}
