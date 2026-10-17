package app

import "testing"

const sec = int64(1e9)

// idle: nothing owed means no draw and a blocking Wait.
func TestPacerIdleBlocksAndDrawsNothing(t *testing.T) {
	p := newPacer()
	if draw, _ := p.poll(0); !draw {
		t.Fatal("the first frame is owed at start")
	}
	p.requested(0)
	p.drawn(false)
	for now := int64(0); now < 100*sec; now += sec {
		if p.timeout(now) != -1 {
			t.Fatalf("idle Wait timeout %d, want to block", p.timeout(now))
		}
		if draw, req := p.poll(now); draw || req {
			t.Fatal("an idle app draws or requests")
		}
	}
	// The frame event of the last draw changes nothing while idle.
	p.frameEvent()
	if draw, _ := p.poll(101 * sec); draw {
		t.Fatal("a frame event alone draws")
	}
}

// Input after idle draws in the free slot at once and then spends it.
func TestPacerInputDrawsOnceAndSpendsTheSlot(t *testing.T) {
	p := newPacer()
	p.poll(0)
	p.requested(0)
	p.drawn(false)
	p.frameEvent() // the slot of the first frame's request arrives

	p.invalidate()
	if p.timeout(10*sec) != 0 {
		t.Fatal("a draw is possible now: poll, do not block")
	}
	if draw, _ := p.poll(10 * sec); !draw {
		t.Fatal("input did not draw")
	}
	p.requested(10 * sec)
	p.drawn(false)

	// More input before the frame event waits for the slot (with a timeout
	// that detects a silent frame source).
	p.invalidate()
	if draw, _ := p.poll(10*sec + 1); draw {
		t.Fatal("drew without a free slot")
	}
	if got := p.timeout(10*sec + 1); got != sec-1 {
		t.Fatalf("timeout %d, want the rest of the silence window %d", got, sec-1)
	}
	p.frameEvent()
	if draw, _ := p.poll(10*sec + 2); !draw {
		t.Fatal("did not draw on the frame event")
	}
}

// The phase-2 bug: a draw attempt that finds no frame must leave the repaint
// owed and ask for a new slot, and the next frame event must draw it.
func TestPacerFailedAttemptStaysOwed(t *testing.T) {
	p := newPacer()
	p.poll(0)
	p.requested(0)
	p.drawn(false)
	p.frameEvent()
	p.invalidate()

	draw, _ := p.poll(5 * sec)
	if !draw {
		t.Fatal("no draw attempt")
	}
	// BeginFrame returned false: the loop calls failed and requests a frame.
	p.failed(5 * sec)
	p.requested(5 * sec)
	if !p.needDraw {
		t.Fatal("a failed attempt forgot the repaint")
	}
	if draw, _ := p.poll(5*sec + 1); draw {
		t.Fatal("retried before a new frame slot (paced)")
	}
	p.frameEvent()
	if draw, _ := p.poll(5*sec + 2); !draw {
		t.Fatal("the frame event after a failed attempt did not redraw")
	}
	p.requested(5*sec + 2)
	p.drawn(false)
	if p.needDraw {
		t.Fatal("still owed after a successful draw")
	}
}

// A silent frame source (asleep display, occluded window) falls back to
// unpaced attempts, backs off when they fail, and a frame event restores
// paced mode.
func TestPacerSilenceFallsBackToUnpaced(t *testing.T) {
	p := newPacer()
	p.poll(1)
	p.requested(1) // times are never 0 on a real clock: 0 means "no request"
	p.drawn(true)  // animating
	if draw, _ := p.poll(sec); draw {
		t.Fatal("fell back before the silence window")
	}
	if p.timeout(101) != sec-100 {
		t.Fatalf("timeout %d, want to wake at the end of the silence window", p.timeout(101))
	}
	draw, _ := p.poll(sec + 1)
	if !draw || p.paced {
		t.Fatalf("after %d s of silence: draw %v paced %v, want an unpaced attempt", 1, draw, p.paced)
	}
	// The attempt finds nothing to draw into: back off, do not spin.
	p.failed(sec + 1)
	p.requested(sec + 1)
	if draw, _ := p.poll(sec + retryNs); draw {
		t.Fatal("retried before the backoff")
	}
	if got := p.timeout(sec + 2); got != retryNs-1 {
		t.Fatalf("backoff timeout %d, want %d", got, retryNs-1)
	}
	draw, _ = p.poll(sec + 1 + retryNs)
	if !draw {
		t.Fatal("no retry after the backoff")
	}
	// An attempt that works keeps drawing without waiting for frame events.
	p.requested(sec + retryNs)
	p.drawn(true)
	if draw, _ := p.poll(sec + retryNs + 1); !draw {
		t.Fatal("unpaced mode waited for a frame event")
	}
	// A frame event ends the fallback.
	p.drawn(true)
	p.frameEvent()
	if !p.paced {
		t.Fatal("a frame event did not restore paced mode")
	}
	p.requested(sec + retryNs + 2)
	p.drawn(true)
	if draw, _ := p.poll(sec + retryNs + 3); draw {
		t.Fatal("paced again, but drew without a slot")
	}
}

// After a draw that asked for no new frame (the OS modal loop's handler), the
// next owed repaint asks for one instead of waiting on a slot nobody armed.
func TestPacerRequestsWhenNoneIsOutstanding(t *testing.T) {
	p := newPacer()
	p.poll(0)
	p.drawn(false)
	p.requestedAt = 0 // the handler drew without requesting
	p.invalidate()
	draw, req := p.poll(7 * sec)
	if draw || !req {
		t.Fatalf("poll = draw %v request %v, want a request only", draw, req)
	}
	p.requested(7 * sec)
	if draw, req := p.poll(7*sec + 1); draw || req {
		t.Fatal("kept asking")
	}
	p.frameEvent()
	if draw, _ := p.poll(7*sec + 2); !draw {
		t.Fatal("no draw on the requested frame")
	}
}
