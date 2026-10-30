package ws

import (
	"errors"
	"io"
	"net"
	"testing"
	"testing/synctest"
	"time"
)

// The framing's close handshake (frame.go: the Linux client's, and the test
// server's on every OS) on a simulated clock: net.Pipe inside a
// testing/synctest bubble, so closeWait passes in no real time and the
// elapsed times below are exact. The peer reads no core clock, so it needs
// no core.UseSynctestForTesting.

// pipePeers is a client peer and the raw server end of a net.Pipe.
func pipePeers(t *testing.T) (*peer, net.Conn) {
	cc, sc := net.Pipe()
	t.Cleanup(func() { cc.Close(); sc.Close() })
	c := newPeer(cc, nil, true, 1<<20)
	c.newKey = func(k *[4]byte) { *k = [4]byte{1, 2, 3, 4} }
	return c, sc
}

// pendingRead starts the client's one reader and waits until it is blocked.
func pendingRead(c *peer) chan error {
	done := make(chan error, 1)
	go func() {
		_, _, err := c.read(nil)
		done <- err
	}()
	synctest.Wait()
	return done
}

// serve runs a server peer on sc that reads until the connection ends and
// returns the client's close frame (nil if none came).
func serve(sc net.Conn) chan *CloseError {
	got := make(chan *CloseError, 1)
	go func() {
		s := newPeer(sc, nil, false, 1<<20)
		for {
			if _, _, err := s.read(nil); err != nil {
				got <- s.peerClose.Load()
				return
			}
		}
	}()
	return got
}

// Close with a read pending waits for the server's answering close frame,
// which arrives at once, and ends the read with ErrClosed.
func TestPeerCloseWithAPendingRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, sc := pipePeers(t)
		got := serve(sc)
		done := pendingRead(c)
		start := time.Now()
		c.close(CloseGoingAway, "quitting")
		if d := time.Since(start); d != 0 {
			t.Errorf("Close took %v with a server that answers at once", d)
		}
		if err := <-done; !errors.Is(err, ErrClosed) {
			t.Errorf("pending read: %v, want ErrClosed", err)
		}
		if ce := <-got; ce == nil || ce.Code != CloseGoingAway || ce.Reason != "quitting" {
			t.Errorf("the server saw close %+v, want 1001 \"quitting\"", ce)
		}
		if err := c.write(Text, []byte("x")); !errors.Is(err, ErrClosed) {
			t.Errorf("write after Close: %v", err)
		}
		if _, _, err := c.read(nil); !errors.Is(err, ErrClosed) {
			t.Errorf("read after Close: %v", err)
		}
	})
}

// A server that never answers the close frame holds Close for closeWait
// exactly; then the connection is released and the read ends.
func TestPeerCloseGivesUpAfterCloseWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, sc := pipePeers(t)
		go io.Copy(io.Discard, sc) // reads everything, answers nothing
		done := pendingRead(c)
		start := time.Now()
		c.close(CloseNormal, "")
		if d := time.Since(start); d != closeWait {
			t.Errorf("Close took %v, want closeWait (%v)", d, closeWait)
		}
		if err := <-done; !errors.Is(err, ErrClosed) {
			t.Errorf("pending read: %v, want ErrClosed", err)
		}
	})
}

// Without a pending read Close does not wait, and the close frame still
// reaches the server.
func TestPeerCloseWithoutARead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, sc := pipePeers(t)
		got := serve(sc)
		start := time.Now()
		c.close(CloseNormal, "bye")
		if d := time.Since(start); d != 0 {
			t.Errorf("Close without a read took %v", d)
		}
		if ce := <-got; ce == nil || ce.Code != CloseNormal || ce.Reason != "bye" {
			t.Errorf("the server saw close %+v, want 1000 \"bye\"", ce)
		}
	})
}

// Close ends a write that a server which stopped reading holds back, at once
// and with ErrClosed (the close frame cannot go out behind it).
func TestPeerCloseEndsABlockedWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, _ := pipePeers(t) // the server end is never read
		done := make(chan error, 1)
		go func() { done <- c.write(Binary, make([]byte, 1<<20)) }()
		synctest.Wait()
		select {
		case err := <-done:
			t.Fatalf("the write returned %v with nobody reading", err)
		default:
		}
		start := time.Now()
		c.close(CloseNormal, "")
		if err := <-done; !errors.Is(err, ErrClosed) {
			t.Errorf("blocked write: %v, want ErrClosed", err)
		}
		if d := time.Since(start); d != 0 {
			t.Errorf("Close took %v to end a blocked write", d)
		}
	})
}

// A server that closes first gets its close answered with its code, the read
// ends with its *CloseError, and the client's later Close does not wait.
func TestPeerServerCloses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, sc := pipePeers(t)
		s := newPeer(sc, nil, false, 1<<20)
		answered := make(chan *CloseError, 1)
		go func() {
			s.sendClose(4000, "done here")
			_, _, err := s.read(nil)
			if !errors.Is(err, ErrClosed) {
				t.Errorf("the server's read after its close: %v, want ErrClosed", err)
			}
			answered <- s.peerClose.Load()
		}()
		_, _, err := c.read(nil)
		var ce *CloseError
		if !errors.As(err, &ce) || ce.Code != 4000 || ce.Reason != "done here" {
			t.Fatalf("read: %v, want a CloseError 4000 \"done here\"", err)
		}
		if a := <-answered; a == nil || a.Code != 4000 {
			t.Errorf("the client answered with %+v, want the server's code", a)
		}
		start := time.Now()
		c.close(CloseNormal, "")
		if d := time.Since(start); d != 0 {
			t.Errorf("Close after the server's close took %v", d)
		}
	})
}
