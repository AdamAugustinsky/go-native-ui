package img

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// blockingLease's Bytes waits until open is closed, holding its decode on a
// worker; started is closed when the worker reads it.
type blockingLease struct {
	b             []byte
	started, open chan struct{}
	released      atomic.Int32
}

func newBlockingLease(b []byte) *blockingLease {
	return &blockingLease{b: b, started: make(chan struct{}), open: make(chan struct{})}
}

func (l *blockingLease) Bytes() []byte {
	close(l.started)
	<-l.open
	return l.b
}

func (l *blockingLease) Release() { l.released.Add(1) }

// TestCloseReturnsEveryBuffer: Close finds decodes in all three states (one
// finished and not polled, two running past the wait, four queued behind
// them, three with an inline copy) and every pooled buffer, C memory on
// macOS, is freed once the running decoders return. A running decode's lease
// is released only after its decoder has stopped reading it; a queued one's
// at Close.
func TestCloseReturnsEveryBuffer(t *testing.T) {
	woke := make(chan struct{}, 64)
	c := NewCache(Options{Renderer: newFakeGPU(), Workers: 2, Wake: func() {
		select {
		case woke <- struct{}{}:
		default:
		}
	}})
	png := encPNG(t, gradient(32, 32))

	c.Request(Key{Hash: 1, W: 32, H: 32}, Bytes(png)) // finishes; never polled
	select {
	case <-woke:
	case <-time.After(10 * time.Second):
		t.Fatal("the first decode did not finish")
	}
	running := []*blockingLease{newBlockingLease(png), newBlockingLease(png)}
	for k, l := range running {
		c.Request(Key{Hash: uint64(2 + k), W: 32, H: 32}, Leased(l))
		<-l.started
	}
	for k := 0; k < 3; k++ {
		c.Request(Key{Hash: uint64(10 + k), W: 32, H: 32}, Bytes(png))
	}
	queued := &lease{b: png}
	c.Request(Key{Hash: 20, W: 32, H: 32}, Leased(queued))
	if c.bufs.held() < 4 {
		t.Fatalf("%d buffers before Close, want the output and three copies", c.bufs.held())
	}

	c.Close(10 * time.Millisecond) // the leased decodes outlive the wait
	if queued.released != 1 {
		t.Errorf("a queued decode's lease was released %d times by Close, want 1", queued.released)
	}
	for k, l := range running {
		if n := l.released.Load(); n != 0 {
			t.Fatalf("running decode %d: lease released %d times while its decoder still reads it", k, n)
		}
	}
	for _, l := range running {
		close(l.open)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		done := c.bufs.held() == 0
		for _, l := range running {
			done = done && l.released.Load() == 1
		}
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after the decoders returned: %d buffers still hold memory, leases released %d and %d times (want 0; 1, 1)",
				c.bufs.held(), running[0].released.Load(), running[1].released.Load())
		}
		time.Sleep(time.Millisecond)
	}
}

// TestEmptyInlineBytesFail: Bytes(nil) fails as undecodable through the
// cache, without a worker touching a buffer it does not have.
func TestEmptyInlineBytesFail(t *testing.T) {
	c, _ := newTestCache(t, Options{})
	for k, src := range []Source{Bytes(nil), Bytes([]byte{})} {
		h, _ := c.Request(Key{Hash: uint64(1 + k), W: 16, H: 16}, src)
		settle(t, c, nil)
		if st := c.State(h); st != Failed || !errors.Is(c.Err(h), ErrDecode) {
			t.Errorf("source %d: %v, %v; want Failed, ErrDecode", k, st, c.Err(h))
		}
	}
}
