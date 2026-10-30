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
		ReplyQuit(e.ID, s.reply == 1)
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
	drain() // what earlier tests left undelivered (a dialog's result) is not this one's
	if r, ok := s.(*recSink); ok {
		r.evs, r.data, r.safe = nil, nil, 0
	}
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
	// OfferID names the event's object instead of numbering it.
	s.evs = s.evs[:0]
	if !r.OfferID(7, 1, 2, nil) || !r.Offer(3, 0, nil) {
		t.Fatal("offers refused")
	}
	drain()
	if len(s.evs) != 2 || s.evs[0].ID != 7 || s.evs[0].A != 1 || s.evs[0].B != 2 || s.evs[1].ID == 7 || s.evs[1].ID == 0 {
		t.Fatalf("OfferID then Offer delivered %+v", s.evs)
	}
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
			func() { ReplyQuit(e.ID, true) }()
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
	if !ReplyQuit(quitState.serial, false) || QuitPending() || ReplyQuit(quitState.serial, true) || len(sent) != 1 || sent[0] {
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

// A writer that has published its value but not queued it yet when the
// dispatcher delivers that value must not get it delivered a second time:
// the interleaving is forced with the hook between publishing and queueing.
func TestObservationDeliveredOnceAcrossARequeue(t *testing.T) {
	s := &recSink{}
	withSink(t, s)
	o := NewObservation(SourceApp + 3)
	o.Set(1, 0) // queued, not delivered yet
	testHookObservationSet = func() {
		testHookObservationSet = nil
		drain() // the dispatcher runs between this Set's publish and its queueing
	}
	t.Cleanup(func() { testHookObservationSet = nil })
	o.Set(2, 0)
	drain()
	var got []int64
	for _, e := range s.evs {
		if e.Source == SourceApp+3 {
			got = append(got, e.A)
		}
	}
	if len(got) != 1 || got[0] != 2 {
		t.Fatalf("delivered %v, want [2]", got)
	}
	if o.Sets() != 2 || o.Coalesced() != 1 {
		t.Fatalf("sets %d, coalesced %d: want 2 and 1 (delivered = sets - coalesced)", o.Sets(), o.Coalesced())
	}
}

// A producer that has claimed the head of the ready queue but not published
// it yet leaves the dispatcher nothing to do: it must not signal itself (the
// run-loop source would fire again at once, a busy spin); the producer's own
// signal, once it has published, brings the event.
func TestUnpublishedHeadDoesNotSpin(t *testing.T) {
	s := &recSink{}
	withSink(t, s)
	paused, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	testHookReadyClaimed = func() {
		once.Do(func() {
			close(paused)
			<-release
		})
	}
	t.Cleanup(func() { testHookReadyClaimed = nil })
	go func() {
		PostApp(AppEvent{ID: 7}, nil)
		close(done)
	}()
	<-paused
	testHookReadyClaimed = nil
	// The run loop fires the source while it is signalled; count its turns.
	dispatch.signalled.Store(true)
	turns0 := dispatch.turns
	for i := 0; i < 100 && dispatch.signalled.Load(); i++ {
		DispatchApp()
	}
	if n := dispatch.turns - turns0; n != 1 || dispatch.signalled.Load() {
		t.Fatalf("%d dispatcher turns on an unpublished head (signalled %v), want 1 and no signal of its own", n, dispatch.signalled.Load())
	}
	close(release)
	<-done
	if !dispatch.signalled.Load() {
		t.Fatal("the producer did not signal once it had published")
	}
	drain()
	if len(s.evs) != 1 || s.evs[0].ID != 7 {
		t.Fatalf("delivered %+v", s.evs)
	}
}

// The last drain (FlushApp) does not take an unpublished head for an empty
// queue: a producer paused between its claim and its publication, with a
// later producer that published and returned, is waited for, and both are
// delivered in order. After it the gate is closed: a post is refused.
func TestLastDrainWaitsForAPausedProducer(t *testing.T) {
	s := &recSink{}
	withSink(t, s)
	t.Cleanup(func() { gate.closed.Store(false) })
	paused, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	testHookReadyClaimed = func() {
		once.Do(func() {
			close(paused)
			<-release
		})
	}
	t.Cleanup(func() { testHookReadyClaimed = nil })
	go func() {
		PostApp(AppEvent{ID: 7}, nil) // claims the head, then pauses
		close(done)
	}()
	<-paused
	testHookReadyClaimed = nil
	if !PostApp(AppEvent{ID: 8}, nil) { // published behind the paused one
		t.Fatal("the second post was refused")
	}
	time.AfterFunc(100*time.Millisecond, func() { close(release) })
	FlushApp()
	<-done
	if len(s.evs) != 2 || s.evs[0].ID != 7 || s.evs[1].ID != 8 {
		t.Fatalf("the last drain delivered %+v, want the paused post and the one behind it", s.evs)
	}
	if PostApp(AppEvent{ID: 9}, nil) {
		t.Fatal("a post after the last drain was accepted")
	}
}

// A ticket record whose generations are spent is retired, not reused: a
// Ticket never repeats.
func TestTicketsRetireSpentRecords(t *testing.T) {
	s := &recSink{}
	withSink(t, s)
	for _, cancel := range []bool{false, true} {
		k := NewTickets(SourceApp+4, 1, 0)
		k.t.recs[0].word.Store(uint64(lastGen-1) << 32) // used 2³²-2 times already
		tk, err := k.Reserve(0)
		if err != nil {
			t.Fatal(err)
		}
		if cancel {
			k.Cancel(tk)
		} else {
			k.Complete(tk, 1, 0, nil)
			drain()
		}
		if tk2, err := k.Reserve(0); !errors.Is(err, ErrBusy) {
			t.Fatalf("cancel %v: the spent record was reused (ticket %#x after %#x)", cancel, tk2, tk)
		}
		if k.Retired() != 1 || k.Outstanding() != 1 {
			t.Fatalf("cancel %v: retired %d, outstanding %d", cancel, k.Retired(), k.Outstanding())
		}
	}
}

// countSink counts what it is given and allocates nothing.
type countSink struct{ n int }

func (s *countSink) AppEvent(*AppEvent, []byte) { s.n++ }
func (s *countSink) AppSafePoint()              {}

// A steady flow of large outside requests (over 1 KiB) reuses the records'
// buffers: no allocation per request once they are grown.
func TestRingKeepsLargeBuffers(t *testing.T) {
	s := &countSink{}
	withSink(t, s)
	r := NewRing(SourceApp+5, 4, RingBytes)
	payload := bytes.Repeat([]byte("x"), 4<<10)
	for i := 0; i < 4; i++ { // the buffers grow once
		r.Offer(0, 0, payload)
		drain()
	}
	if n := testing.AllocsPerRun(50, func() {
		if !r.Offer(0, 0, payload) {
			t.Fatal("refused")
		}
		drain()
	}); n != 0 {
		t.Fatalf("%v allocations per 4 KiB request", n)
	}
	if s.n < 54 {
		t.Fatalf("%d delivered", s.n)
	}
}

// A late answer to an older quit request (a worker that took its time) does
// not settle the request waiting now.
func TestStaleQuitReplyIsIgnored(t *testing.T) {
	s := &recSink{}
	withSink(t, s)
	var sent []bool
	prevReply := replyQuitFn
	replyQuitFn = func(allow bool) { sent = append(sent, allow) }
	t.Cleanup(func() {
		replyQuitFn = prevReply
		quitState.phase = quitIdle
	})
	if beginQuit(QuitDock) != 0 {
		t.Fatal("answered")
	}
	first := s.evs[len(s.evs)-1].ID
	if !ReplyQuit(first, false) {
		t.Fatal("the first request was not waiting")
	}
	if beginQuit(QuitMenu) != 0 {
		t.Fatal("answered")
	}
	second := s.evs[len(s.evs)-1].ID
	if first == second || ReplyQuit(first, true) || len(sent) != 1 {
		t.Fatalf("a reply to request %d settled request %d: sent %v", first, second, sent)
	}
	if !ReplyQuit(second, true) || len(sent) != 2 || !sent[1] || ReplyQuit(second, false) {
		t.Fatalf("the second request's own reply: sent %v", sent)
	}
}

// While a frame holds dispatch, native events wait, in order and with their
// data, and are delivered once it ends; EvWillTerminate is never held.
func TestNativeEventsWaitForTheFrame(t *testing.T) {
	s := &recSink{}
	withSink(t, s)
	HoldApp(true)
	t.Cleanup(func() { HoldApp(false) })
	deliverNow(AppEvReopen, 0, 1, 0, nil)
	deliverNow(AppEvOpenURLs, 0, 1, 0, []byte("gnui-test://held"))
	deliverNow(AppEvActivated, 0, 0, 0, nil)
	if len(s.evs) != 0 {
		t.Fatalf("delivered inside the frame: %+v", s.evs)
	}
	deliverNow(AppEvWillTerminate, 0, 0, 0, nil)
	if len(s.evs) != 1 || s.evs[0].Kind != AppEvWillTerminate {
		t.Fatalf("will-terminate held: %+v", s.evs)
	}
	s.evs, s.data = nil, nil
	HoldApp(false)
	if !dispatch.signalled.Load() {
		t.Fatal("the frame's end did not signal")
	}
	DispatchApp()
	if len(s.evs) != 3 || s.evs[0].Kind != AppEvReopen || s.evs[1].Kind != AppEvOpenURLs || string(s.data[1]) != "gnui-test://held" || s.evs[2].Kind != AppEvActivated {
		t.Fatalf("after the frame: %+v %q", s.evs, s.data)
	}
}

// URLs handed over from outside the process (a second instance's command
// line, app/instance) arrive as AppEvOpenURLs, A the count and the data the
// URLs NUL-separated, as the URLs macOS opens with the app do.
func TestOfferedURLsArriveAsOpenURLs(t *testing.T) {
	s := &recSink{}
	withSink(t, s)
	if !OfferOpenURLs([]string{"notes-dev:x", "notes-dev://y?z"}) {
		t.Fatal("refused")
	}
	drain()
	if len(s.evs) != 1 || s.evs[0].Kind != AppEvOpenURLs || s.evs[0].A != 2 || s.evs[0].Source != SourceInstance ||
		string(s.data[0]) != "notes-dev:x\x00notes-dev://y?z" {
		t.Fatalf("delivered %+v %q", s.evs, s.data)
	}
}

func TestTicketFreeSignalsAvailability(t *testing.T) {
	k := NewTickets(SourcePost, 1, 0)
	frees := 0
	k.OnFree(func() { frees++ })
	tk, err := k.Reserve(0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := k.Reserve(0); err != ErrBusy {
		t.Fatalf("full reservation: %v", err)
	}
	if !k.Cancel(tk) || frees != 1 {
		t.Fatalf("cancel: %d availability signals", frees)
	}
	tk, err = k.Reserve(0)
	if err != nil {
		t.Fatal(err)
	}
	k.Cancel(tk)
	if frees != 2 {
		t.Fatalf("reuse: %d signals", frees)
	}
}

func TestReadyQueueIsLazy(t *testing.T) {
	var q readyQueue
	if q.cells.Load() != nil || q.pending() != 0 || q.headReady() {
		t.Fatal("unused queue has storage or work")
	}
	if _, ok := q.pop(); ok {
		t.Fatal("unused queue produced work")
	}
	q.init()
	q.push(42)
	if got, ok := q.pop(); !ok || got != 42 {
		t.Fatalf("initialized queue: %v, %v", got, ok)
	}
	if n := testing.AllocsPerRun(100, func() { q.push(7); q.pop() }); n != 0 {
		t.Errorf("warmed queue: %v allocations", n)
	}
}
