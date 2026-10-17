package platform

import "testing"

func TestTakePendingCopiesAndClears(t *testing.T) {
	q := newEventQueue()
	q.push(Event{Kind: EvResize, W: 10})
	q.pushText([]byte("hi"), 0)
	got := q.takePending()
	if len(got) != 2 || len(q.ev) != 0 {
		t.Fatalf("pending %d events, queue %d left", len(got), len(q.ev))
	}
	if string(q.textOf(got[1].Text)) != "hi" {
		t.Fatal("text of a pending event must stay readable")
	}
	// Events pushed after the call must not overwrite the slice handed out.
	q.push(Event{Kind: EvFocus})
	if got[0].Kind != EvResize {
		t.Fatal("pending slice aliases the queue")
	}
	if next := q.takePending(); len(next) != 1 || next[0].Kind != EvFocus {
		t.Fatalf("second pending = %v", next)
	}
	if next := q.takePending(); len(next) != 0 {
		t.Fatal("empty queue must give no events")
	}
}

func TestDropFirstKeepsLaterEvents(t *testing.T) {
	q := newEventQueue()
	q.push(Event{Kind: EvFocus})
	q.push(Event{Kind: EvVisible})
	q.dropFirst(1) // one was handed out, one arrived synchronously afterwards
	if len(q.ev) != 1 || q.ev[0].Kind != EvVisible {
		t.Fatalf("queue after dropFirst = %v", q.ev)
	}
	q.dropFirst(5)
	if len(q.ev) != 0 {
		t.Fatal("dropping everything must empty the queue")
	}
}

func TestSteadyStatePendingAllocatesNothing(t *testing.T) {
	q := newEventQueue()
	n := testing.AllocsPerRun(100, func() {
		for i := 0; i < 20; i++ {
			q.push(Event{Kind: EvResize})
		}
		q.takePending()
	})
	if n != 0 {
		t.Fatalf("%v allocations per run", n)
	}
}

// A Wake before the window exists is not lost: it leaves the coalescing flag
// set, which Open checks once the OS can be woken (darwin, windows and linux
// each do), and the flag is cleared exactly when the loop takes the wake.
func TestWakeBeforeTheWindowStaysPending(t *testing.T) {
	takeWake() // whatever an earlier test left
	Wake()     // no window: osWake finds nothing to post to
	Wake()     // coalesced
	if !wakePending.Load() {
		t.Fatal("a Wake with no window must stay pending for Open to find")
	}
	if !takeWake() || takeWake() {
		t.Error("the pending wake is taken once")
	}
	Wake()
	if !takeWake() {
		t.Error("a Wake after the pending one was taken is pending again")
	}
}
