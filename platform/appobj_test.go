package platform

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The delivery classes of the application object, without a system: a sink
// records what the dispatcher delivers, and the test runs the dispatcher
// itself.

type recSink struct {
	evs   []AppEvent
	data  [][]byte
	safe  int
	reply int // 1: allow, 2: refuse the quit request when it arrives, -1 nested
	onEv  func(e *AppEvent)
}

func (s *recSink) AppEvent(e *AppEvent, data []byte) {
	s.evs = append(s.evs, *e)
	s.data = append(s.data, append([]byte(nil), data...))
	if e.Kind == AppEvQuitRequested && s.reply != 0 {
		ReplyQuit(s.reply == 1)
	}
	if s.onEv != nil {
		s.onEv(e)
	}
}

func (s *recSink) AppSafePoint() { s.safe++ }

func withSink(t *testing.T, s AppSink) {
	t.Helper()
	prev := dispatch.sink
	SetAppSink(s)
	t.Cleanup(func() { SetAppSink(prev) })
}

func drain() {
	for i := 0; i < 64 && ready.pending() > 0; i++ {
		DispatchApp()
	}
}

func TestTicketsReserveCompleteCancel(t *testing.T) {
	s := &recSink{}
	withSink(t, s)
	k := NewTickets(SourceDialog, MaxTickets, 8)
	var tks []Ticket
	for i := 0; i < MaxTickets; i++ {
		tk, err := k.Reserve(7)
		if err != nil {
			t.Fatalf("reserve %d: %v", i, err)
		}
		tks = append(tks, tk)
	}
	if _, err := k.Reserve(7); !errors.Is(err, ErrBusy) {
		t.Fatalf("reserve %d: %v, want ErrBusy", MaxTickets+1, err)
	}
	if k.Refused() != 1 || k.Outstanding() != MaxTickets {
		t.Fatalf("refused %d, outstanding %d", k.Refused(), k.Outstanding())
	}
	// Complete in reverse: delivered in completion order, with their data
	// (one longer than the record grows it).
	for i := len(tks) - 1; i >= 0; i-- {
		d := []byte(fmt.Sprintf("r%d", i))
		if i == 0 {
			d = bytes.Repeat([]byte("x"), 100)
		}
		if !k.Complete(tks[i], int64(i), -int64(i), d) {
			t.Fatalf("complete %d failed", i)
		}
	}
	if k.Complete(tks[3], 0, 0, nil) {
		t.Error("a ticket completed twice")
	}
	drain()
	if len(s.evs) != MaxTickets {
		t.Fatalf("%d events, want %d", len(s.evs), MaxTickets)
	}
	for j, e := range s.evs {
		i := MaxTickets - 1 - j
		want := fmt.Sprintf("r%d", i)
		if i == 0 {
			want = string(bytes.Repeat([]byte("x"), 100))
		}
		if e.Kind != AppEvService || e.Source != SourceDialog || e.ID != uint64(tks[i]) || e.A != int64(i) || e.B != -int64(i) || e.Window != 7 || string(s.data[j]) != want {
			t.Fatalf("event %d = %+v %q, want ticket %d", j, e, s.data[j], i)
		}
	}
	if k.Outstanding() != 0 {
		t.Fatalf("outstanding %d after delivery", k.Outstanding())
	}
	// Cancelled before and after completion: nothing is delivered, the
	// records come back, and a reused record's ticket is new.
	a, _ := k.Reserve(0)
	b, _ := k.Reserve(0)
	if !k.Cancel(a) || k.Complete(a, 1, 1, nil) {
		t.Error("cancel then complete")
	}
	k.Complete(b, 2, 2, nil)
	if !k.Cancel(b) {
		t.Error("cancel of a completed, undelivered ticket")
	}
	s.evs = s.evs[:0]
	drain()
	if len(s.evs) != 0 || k.Outstanding() != 0 {
		t.Fatalf("cancelled tickets delivered %d events, %d outstanding", len(s.evs), k.Outstanding())
	}
	c, _ := k.Reserve(0)
	if c == a || c == b {
		t.Error("a ticket repeated")
	}
	k.Cancel(c)
}

func TestRingAcceptsOrRefuses(t *testing.T) {
	s := &recSink{}
	withSink(t, s)
	r := NewRing(SourceInstance, RingRecords, 64*RingRecords)
	for i := 0; i < RingRecords; i++ {
		if !r.Offer(int64(i), 0, []byte("args")) {
			t.Fatalf("offer %d refused", i)
		}
	}
	if r.Offer(99, 0, nil) {
		t.Fatal("an offer past the ring's records accepted")
	}
	drain()
	if len(s.evs) != RingRecords || r.Refused() != 1 {
		t.Fatalf("%d delivered, %d refused", len(s.evs), r.Refused())
	}
	for i, e := range s.evs {
		if e.A != int64(i) || e.ID != uint64(i+1) || string(s.data[i]) != "args" {
			t.Fatalf("event %d = %+v", i, e)
		}
	}
	// The data budget: one request bigger than what is left is refused.
	if r.Offer(0, 0, make([]byte, 64*RingRecords+1)) {
		t.Fatal("an offer over the data budget accepted")
	}
	if !r.Offer(0, 0, make([]byte, 64*RingRecords)) || r.Offer(0, 0, []byte{1}) {
		t.Fatal("the budget is not shared")
	}
	drain()
	if !r.Offer(0, 0, []byte{1}) {
		t.Fatal("the budget was not given back on delivery")
	}
	drain()
}

func TestObservationCoalesces(t *testing.T) {
	s := &recSink{}
	withSink(t, s)
	o := NewObservation(SourceLocale)
	o.Set(1, 10)
	o.Set(2, 20)
	o.Set(3, 30)
	drain()
	if len(s.evs) != 1 || s.evs[0].A != 3 || s.evs[0].B != 30 || o.Coalesced() != 2 || o.Sets() != 3 {
		t.Fatalf("events %+v, coalesced %d", s.evs, o.Coalesced())
	}
	o.Set(4, 40)
	drain()
	if len(s.evs) != 2 || s.evs[1].A != 4 {
		t.Fatalf("a set after delivery: %+v", s.evs)
	}
}

func TestPostRefusesWhenFull(t *testing.T) {
	s := &recSink{}
	withSink(t, s)
	drain()
	acc0, ref0 := PostStats()
	for i := 0; i < MaxPosted; i++ {
		if !PostApp(AppEvent{ID: uint64(i), Kind: AppEvReopen}, []byte{byte(i)}) {
			t.Fatalf("post %d refused", i)
		}
	}
	if PostApp(AppEvent{}, nil) {
		t.Fatal("a post past MaxPosted accepted")
	}
	drain()
	if PostApp(AppEvent{}, make([]byte, MaxPostData+1)) {
		t.Fatal("a post with too much data accepted")
	}
	acc, ref := PostStats()
	if acc-acc0 != MaxPosted || ref-ref0 != 2 || len(s.evs) != MaxPosted {
		t.Fatalf("accepted %d refused %d delivered %d", acc-acc0, ref-ref0, len(s.evs))
	}
	for i, e := range s.evs {
		if e.Kind != AppEvPost || e.ID != uint64(i) || s.data[i][0] != byte(i) {
			t.Fatalf("post %d delivered as %+v", i, e)
		}
	}
}

// Many producers at once against one dispatcher: everything accepted is
// delivered once; the only losses are refused posts and coalesced values,
// both counted.
func TestDeliveryUnderContention(t *testing.T) {
	s := &recSink{}
	withSink(t, s)
	drain()
	k := NewTickets(SourceNotifyResponse, MaxNotifyResponses, 64)
	r := NewRing(SourceInstance, RingRecords, RingBytes)
	o := NewObservation(SourceMedia)
	tks := make(chan Ticket, MaxNotifyResponses)
	for i := 0; i < MaxNotifyResponses; i++ {
		tk, err := k.Reserve(0)
		if err != nil {
			t.Fatal(err)
		}
		tks <- tk
	}
	close(tks)
	acc0, ref0 := PostStats()
	var posted, offered, refusedOffers atomic.Int64
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				if PostApp(AppEvent{ID: uint64(g)}, []byte("p")) {
					posted.Add(1)
				}
				o.Set(int64(i), int64(g))
				if i%50 == 0 {
					if r.Offer(int64(g), 0, []byte("second instance")) {
						offered.Add(1)
					} else {
						refusedOffers.Add(1)
					}
				}
				if tk, ok := <-tks; ok {
					k.Complete(tk, int64(g), 0, []byte("reply text"))
				}
			}
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
loop:
	for {
		select {
		case <-done:
			break loop
		default:
			DispatchApp()
		}
	}
	close(stop)
	drain()
	var gotPosts, gotResp, gotOffers, gotObs int64
	for i, e := range s.evs {
		switch {
		case e.Kind == AppEvPost:
			gotPosts++
		case e.Source == SourceNotifyResponse:
			gotResp++
			if string(s.data[i]) != "reply text" {
				t.Fatalf("a response's text: %q", s.data[i])
			}
		case e.Source == SourceInstance:
			gotOffers++
		case e.Source == SourceMedia:
			gotObs++
		}
	}
	acc, ref := PostStats()
	if gotPosts != int64(acc-acc0) || gotPosts != posted.Load() {
		t.Errorf("posts: %d delivered, %d accepted (%d counted)", gotPosts, posted.Load(), acc-acc0)
	}
	if gotResp != MaxNotifyResponses || k.Outstanding() != 0 {
		t.Errorf("responses: %d delivered, %d outstanding", gotResp, k.Outstanding())
	}
	if gotOffers != offered.Load() {
		t.Errorf("outside requests: %d delivered, %d accepted", gotOffers, offered.Load())
	}
	if gotObs != int64(o.Sets()-o.Coalesced()) {
		t.Errorf("observations: %d delivered, %d set, %d coalesced", gotObs, o.Sets(), o.Coalesced())
	}
	t.Logf("posts %d (refused %d), offers %d (refused %d), observations %d of %d", gotPosts, ref-ref0, offered.Load(), refusedOffers.Load(), gotObs, o.Sets())
}

// The quit latch: an answer given while the request is delivered is
// returned directly; without one the answer is deferred, and a deadline
// applies its action and reports it.
func TestQuitLatch(t *testing.T) {
	s := &recSink{}
	withSink(t, s)
	var sent []bool
	prevReply := replyQuitFn
	replyQuitFn = func(allow bool) { sent = append(sent, allow) }
	t.Cleanup(func() {
		replyQuitFn = prevReply
		quitState.phase, quitState.opts = quitIdle, AppOptions{}
		quitState.approved.Store(false)
	})

	s.reply = 1
	if got := beginQuit(QuitMenu); got != 1 || QuitPending() {
		t.Fatalf("synchronous allow: %d, pending %v", got, QuitPending())
	}
	s.reply = 2
	if got := beginQuit(QuitDock); got != 2 || QuitPending() {
		t.Fatalf("synchronous refusal: %d", got)
	}
	s.reply = 0
	s.onEv = func(e *AppEvent) { // a reply from a nested call is latched as well
		if e.Kind == AppEvQuitRequested {
			func() { ReplyQuit(true) }()
		}
	}
	if got := beginQuit(QuitApp); got != 1 {
		t.Fatalf("nested reply: %d", got)
	}
	s.onEv = nil
	if got := beginQuit(QuitSignal); got != 0 || !QuitPending() {
		t.Fatalf("no answer: %d, pending %v", got, QuitPending())
	}
	if len(sent) != 0 {
		t.Fatalf("answers sent to the system for latched replies: %v", sent)
	}
	if !ReplyQuit(false) || QuitPending() || ReplyQuit(true) || len(sent) != 1 || sent[0] {
		t.Fatalf("a deferred reply: sent %v", sent)
	}

	quitState.opts = AppOptions{QuitDeadline: 20 * time.Millisecond}
	s.evs = s.evs[:0]
	if beginQuit(QuitDock) != 0 {
		t.Fatal("answered")
	}
	deadline := time.Now().Add(2 * time.Second)
	for QuitPending() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
		DispatchApp()
	}
	if QuitPending() || len(s.evs) != 2 || s.evs[1].Kind != AppEvQuitDeadline || QuitAction(s.evs[1].A) != QuitCancel || len(sent) != 2 || sent[1] {
		t.Fatalf("after the deadline: pending %v, events %+v", QuitPending(), s.evs)
	}
}

// While a frame holds dispatch nothing is delivered into it, and nothing is
// stranded: the signal a held or nested call consumed is sent again by the
// release or the outer call, so producers keep signalling.
func TestHeldAndNestedDispatchOweTheirSignal(t *testing.T) {
	s := &recSink{}
	withSink(t, s)
	drain()
	HoldApp(true)
	t.Cleanup(func() { HoldApp(false) })
	PostApp(AppEvent{ID: 1}, nil)
	safe := s.safe
	DispatchApp()
	if len(s.evs) != 0 || s.safe != safe {
		t.Fatalf("held: delivered %+v, %d safe points", s.evs, s.safe-safe)
	}
	if dispatch.signalled.Load() {
		t.Fatal("a held dispatch left the signal set: no producer would signal again")
	}
	HoldApp(false)
	if !dispatch.signalled.Load() {
		t.Fatal("the release did not signal")
	}
	DispatchApp()
	if len(s.evs) != 1 || s.evs[0].ID != 1 {
		t.Fatalf("after the release: %+v", s.evs)
	}

	s.evs = s.evs[:0]
	s.onEv = func(e *AppEvent) {
		if e.ID == 2 {
			PostApp(AppEvent{ID: 3}, nil)
			DispatchApp() // a handler that spun a native loop
		}
	}
	PostApp(AppEvent{ID: 2}, nil)
	DispatchApp()
	if !dispatch.signalled.Load() {
		t.Fatal("the outer dispatch did not signal again after a nested one")
	}
	drain()
	if len(s.evs) != 2 || s.evs[0].ID != 2 || s.evs[1].ID != 3 {
		t.Fatalf("nested: %+v", s.evs)
	}
}
