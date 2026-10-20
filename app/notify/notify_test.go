package notify

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"gonativeui/app"
	"gonativeui/platform"
)

// A reply accepted while the event paths are flooded (app.Post from 8
// goroutines, another service's tickets completing as fast as they can) is
// delivered with its whole text and the note's UserData; nothing the app or
// the other service does can take its record.
func TestReplyDeliveredUnderSaturation(t *testing.T) {
	f, s := fresh(t, Options{})
	if err := RegisterCategories(chat); err != nil {
		t.Fatal(err)
	}
	const nResp = 32
	keys := make([]uint64, nResp)
	ud := make([][]byte, nResp)
	tks := make([]Ticket, nResp)
	for i := range nResp {
		ud[i] = []byte(fmt.Sprintf("chat-%d/msg-%d", i, i*7))
		r := mustPost(t, Note{Title: "Ana", Body: "Lunch?", Category: "msg", UserData: ud[i]})
		if r.Reservation != Reserved {
			t.Fatalf("note %d not reserved: %+v", i, r)
		}
		keys[i], tks[i] = f.lastAdd().key, r.Response
		if a := f.lastAdd(); a.category != "msg" || !bytes.Equal(a.userData, ud[i]) {
			t.Fatalf("note %d sent as %+v", i, a)
		}
	}
	drain()
	s.reset()
	// Replies: short, multi-byte, exactly MaxReply, and longer (cut).
	want := make([]string, nResp)
	sent := make([]string, nResp)
	for i := range nResp {
		switch i % 4 {
		case 0:
			sent[i] = fmt.Sprintf("ok %d", i)
		case 1:
			sent[i] = strings.Repeat("ção 🙂 ", 40) + fmt.Sprint(i)
		case 2:
			sent[i] = strings.Repeat("é", MaxReply/2) // exactly MaxReply bytes
		case 3:
			sent[i] = strings.Repeat("a", MaxReply-1) + "é" // one byte over: the é is dropped whole
		}
		want[i] = sent[i]
		if len(want[i]) > MaxReply {
			want[i] = sent[i][:MaxReply-1]
		}
	}
	other := otherTickets()
	var (
		stop                     atomic.Bool
		posted, refused, otherOK atomic.Int64
		floods                   sync.WaitGroup
		nStored                  atomic.Int64
	)
	for g := 0; g < 8; g++ {
		floods.Add(1)
		go func() {
			defer floods.Done()
			for !stop.Load() {
				if app.Post(app.Event{ID: 100, A: int64(g)}, []byte("flood")) {
					posted.Add(1)
				} else {
					refused.Add(1)
					runtime.Gosched()
				}
			}
		}()
	}
	for g := 0; g < 4; g++ {
		floods.Add(1)
		go func() {
			defer floods.Done()
			for !stop.Load() {
				tk, err := other.Reserve(0)
				if err != nil {
					runtime.Gosched()
					continue
				}
				if other.Complete(tk, int64(g), 0, []byte("other")) {
					otherOK.Add(1)
				}
			}
		}()
	}
	var replies sync.WaitGroup
	for i := range nResp {
		replies.Add(1)
		go func() {
			defer replies.Done()
			time.Sleep(time.Duration(i%8) * time.Millisecond)
			if stored(keys[i], 1, []byte(sent[i]), ud[i]) { // what the delegate read from the system
				nStored.Add(1)
			}
		}()
	}
	repliesDone := make(chan struct{})
	go func() { replies.Wait(); close(repliesDone) }()
	deadline := time.Now().Add(20 * time.Second)
	for s.nResp < nResp && time.Now().Before(deadline) {
		platform.DispatchApp() // the main thread's dispatcher, racing the producers
	}
	<-repliesDone
	time.Sleep(20 * time.Millisecond) // the floods run on a while after the last reply
	stop.Store(true)
	floods.Wait()
	drain()

	if nStored.Load() != nResp {
		t.Fatalf("%d of %d replies stored", nStored.Load(), nResp)
	}
	byTicket := map[Ticket]Response{}
	for _, r := range s.responses() {
		if _, dup := byTicket[r.Ticket]; dup {
			t.Fatalf("response %d delivered twice", r.Ticket)
		}
		byTicket[r.Ticket] = r
	}
	if len(byTicket) != nResp {
		t.Fatalf("%d responses delivered, want %d", len(byTicket), nResp)
	}
	for i, tk := range tks {
		r, ok := byTicket[tk]
		if !ok || r.Action != 1 || string(r.Reply) != want[i] || !bytes.Equal(r.UserData, ud[i]) || !utf8.Valid(r.Reply) {
			t.Fatalf("response %d: action %d, reply of %d bytes (equal %v), UserData %q; want action 1, reply of %d bytes, UserData %q",
				i, r.Action, len(r.Reply), string(r.Reply) == want[i], r.UserData, len(want[i]), ud[i])
		}
	}
	var gotPosts, gotOther int64
	for _, e := range s.evs {
		switch {
		case e.Kind == platform.AppEvPost && e.ID == 100:
			gotPosts++
		case e.Kind == platform.AppEvService && e.Source == otherSource:
			gotOther++
		}
	}
	if gotPosts != posted.Load() || gotOther != otherOK.Load() {
		t.Fatalf("posts %d delivered of %d accepted; other completions %d of %d", gotPosts, posted.Load(), gotOther, otherOK.Load())
	}
	if n := slots.responses.Outstanding(); n != 0 {
		t.Fatalf("%d response records held after delivery", n)
	}
	t.Logf("app.Post: %d accepted, %d refused; other tickets: %d", posted.Load(), refused.Load(), otherOK.Load())
}

const otherSource = platform.SourceApp + 7

var other struct {
	once sync.Once
	k    *platform.Tickets
}

// otherTickets is another service's table (made once: tables last for the
// process).
func otherTickets() *platform.Tickets {
	other.once.Do(func() { other.k = platform.NewTickets(otherSource, platform.MaxTickets, 16) })
	return other.k
}

// Responses being written never wait for each other: with 8 of them paused
// in the middle of their copy (twice the scratch buffers the delegate once
// shared), another is stored at once.
func TestRespondNeverWaits(t *testing.T) {
	f, s := fresh(t, Options{})
	keys := fillSlots(t, f, 9, "")
	const paused = 8
	var entered atomic.Int32
	release := make(chan struct{})
	testHookStoring = func() {
		if entered.Add(1) <= paused {
			<-release
		}
	}
	defer func() { testHookStoring = nil }()
	var wg sync.WaitGroup
	for i := range paused {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stored(keys[i], 1, []byte(fmt.Sprintf("reply %d", i)), []byte(fmt.Sprintf("u%d", i)))
		}()
	}
	for deadline := time.Now().Add(5 * time.Second); entered.Load() < paused; runtime.Gosched() {
		if time.Now().After(deadline) {
			n := entered.Load()
			close(release)
			wg.Wait()
			t.Fatalf("%d of %d responses began copying: the others waited", n, paused)
		}
	}
	done := make(chan bool)
	go func() { done <- stored(keys[paused], 2, []byte("the ninth"), []byte("u8")) }()
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("the ninth response was not stored")
		}
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("a response waited for the responses being copied")
	}
	close(release)
	wg.Wait()
	drain()
	got := map[string]string{}
	for _, r := range s.responses() {
		got[string(r.UserData)] = string(r.Reply)
	}
	for i := range paused + 1 {
		want := fmt.Sprintf("reply %d", i)
		if i == paused {
			want = "the ninth"
		}
		if got[fmt.Sprintf("u%d", i)] != want {
			t.Fatalf("responses delivered %v", got)
		}
	}
}

// With every slot taken a note goes out plain (no category, no UserData, no
// key, so its click only brings the app forward), after the service asked
// the system which notes it still has.
func TestPostWithTableFullHasNoActions(t *testing.T) {
	f, s := fresh(t, Options{})
	if err := RegisterCategories(chat); err != nil {
		t.Fatal(err)
	}
	fillSlots(t, f, MaxResponses, "")
	lists := f.nLists
	r := mustPost(t, Note{ID: "late", Title: "late", Category: "msg", UserData: []byte("x")})
	if r.Reservation != Pending || r.Response != 0 {
		t.Fatalf("257th note: %+v, want Pending", r)
	}
	if f.nLists != lists+2 { // the pending notes, then the shown ones
		t.Fatalf("%d listings for a full table, want 2", f.nLists-lists)
	}
	waitUntil(t, "the post", func() bool { return f.lastAdd().id == "late" })
	a := f.lastAdd()
	if a.id != "late" || a.key != 0 || a.category != "" || a.userData != nil {
		t.Fatalf("257th note sent as %+v", a)
	}
	if res := s.resultOf(t, r.Request); res.Kind != ResultPost || res.Reservation != PostedWithoutActions || res.Response != 0 || res.ErrCode != 0 {
		t.Fatalf("its result %+v", res)
	}
	if st := ReadStats(); st.WithoutActions != 1 || st.Reserved != MaxResponses || st.Reconciled != 0 {
		t.Fatalf("stats %+v", st)
	}
	if r := mustPost(t, Note{ID: "plain", Title: "plain"}); r.Reservation != NoResponse {
		t.Fatalf("a plain note: %+v", r)
	}
	drain()
}

// With EvictOldest the oldest note holding a slot is removed from
// Notification Center (before the new note is sent) and the new note gets
// the slot; a response to the removed one that arrives anyway is stored in a
// spare record, not lost.
func TestEvictOldest(t *testing.T) {
	f, s := fresh(t, Options{EvictOldest: true})
	keys := fillSlots(t, f, MaxResponses, "")
	r := mustPost(t, Note{ID: "new", Title: "new", UserData: []byte("new")})
	res := waitResult(t, s, r.Request)
	newKey := f.lastAdd().key
	if r.Reservation != Pending || res.Reservation != Reserved || res.Evicted != "n0" || res.Response == 0 || newKey == 0 {
		t.Fatalf("post with the table full: %+v, result %+v", r, res)
	}
	if ops := f.opsString(); !strings.HasSuffix(ops, fmt.Sprintf("remove n0, add new %d", newKey)) {
		t.Fatalf("native changes %s", ops)
	}
	s.reset()
	if !stored(keys[0], ActionClick, nil, []byte("u0")) {
		t.Fatal("a response to the evicted note was not stored")
	}
	if !stored(newKey, ActionClick, nil, []byte("new")) || !stored(keys[1], 2, nil, []byte("u1")) {
		t.Fatal("responses to notes holding slots were not stored")
	}
	drain()
	got := s.responses()
	if len(got) != 3 || string(got[0].UserData) != "u0" || got[1].Ticket != res.Response || string(got[1].UserData) != "new" ||
		string(got[2].UserData) != "u1" || got[2].Action != 2 {
		t.Fatalf("delivered %+v", got)
	}
	if st := ReadStats(); st.Evicted != 1 || st.Late != 1 || st.Stored != 3 || st.Parked != 0 || st.Reserved != MaxResponses-2 {
		t.Fatalf("stats %+v", st)
	}
}

// A response that finds no slot and no free record is not answered until it
// is stored: it waits for a record to free (the dispatcher delivering), is
// stored, and only then is the system told.
func TestResponseWaitsForARecord(t *testing.T) {
	f, s := fresh(t, Options{})
	keys := fillSlots(t, f, MaxResponses, "")
	for i, k := range keys { // every slot's record holds an undelivered response
		if !stored(k, ActionClick, nil, []byte(fmt.Sprintf("u%d", i))) {
			t.Fatal(i)
		}
	}
	for i := range nSpare { // and every spare: responses to notes of an earlier run
		if !stored(uint64(1)<<40|uint64(i+1), ActionClick, nil, []byte("spare")) {
			t.Fatal(i)
		}
	}
	p := respond(uint64(1)<<41, 1, []byte("waits"), []byte("parked-ud"))
	if p == nil {
		t.Fatal("stored with every record taken")
	}
	var acks, storedAtAck atomic.Uint64
	p.ack = func() {
		storedAtAck.Store(slots.stored.Load())
		acks.Add(1)
	}
	p.drop = func() { t.Error("given up") }
	p.wait()
	time.Sleep(20 * time.Millisecond)
	if acks.Load() != 0 || ReadStats().Parked != 1 {
		t.Fatalf("answered (%d) before it was stored; %+v", acks.Load(), ReadStats())
	}
	s.reset()
	waitUntil(t, "the parked response", func() bool { return acks.Load() == 1 })
	want := uint64(MaxResponses + nSpare + 1)
	if storedAtAck.Load() != want {
		t.Fatalf("answered with %d responses stored, want %d", storedAtAck.Load(), want)
	}
	var found bool
	for _, r := range s.responses() {
		if string(r.UserData) == "parked-ud" {
			found = string(r.Reply) == "waits" && r.Action == 1
		}
	}
	if !found {
		t.Fatalf("the parked response was not delivered: %d delivered", len(s.responses()))
	}
	if st := ReadStats(); st.Parked != 0 || st.Stored != want || st.Late != nSpare+1 {
		t.Fatalf("stats %+v", st)
	}
}

// A slot is freed when its response is delivered (not before), and at once
// by Remove and RemoveThread; a note replacing one frees the replaced note's
// slot once the system took it; a post the system refused frees its own.
func TestSlotsAreFreed(t *testing.T) {
	f, s := fresh(t, Options{})
	keys := fillSlots(t, f, MaxResponses, "")

	// Delivered: the record is the slot until the app has the response.
	if !stored(keys[5], ActionClick, nil, []byte("u5")) {
		t.Fatal("response not stored")
	}
	f.set(func(f *fakeCenter) { f.manual = true })
	if r := mustPost(t, Note{Title: "x", UserData: []byte("x")}); r.Reservation != Pending {
		t.Fatalf("a slot was free before the response was delivered: %+v", r)
	}
	drain() // the response; the post waits for the listing
	l := f.takeList(t)
	listed(l.ctx, nil)
	listed(f.takeList(t).ctx, f.shownList())
	waitUntil(t, "the post", func() bool { return string(f.lastAdd().userData) == "x" })
	if a := f.lastAdd(); a.key == 0 {
		t.Fatalf("no slot after the response was delivered: %+v", a)
	}
	f.set(func(f *fakeCenter) { f.manual = false })
	drain()

	// Remove frees at once; a response that raced the removal is still
	// delivered.
	before := slots.responses.Outstanding()
	if err := Remove("n7", "n8"); err != nil {
		t.Fatal(err)
	}
	if n := slots.responses.Outstanding(); n != before-2 {
		t.Fatalf("Remove: %d slots held, want %d", n, before-2)
	}
	if !stored(keys[7], ActionClick, nil, []byte("u7")) {
		t.Fatal("a response racing Remove was not stored")
	}
	drain()

	// A note posted with the ID of one shown replaces it: the replaced
	// note's slot goes once the system has taken the new one.
	before = slots.responses.Outstanding()
	mustPost(t, Note{ID: "n9", Title: "again", UserData: []byte("u9b")})
	newKey := f.lastAdd().key
	if n := slots.responses.Outstanding(); n != before || holding(keys[9]) || !holding(newKey) {
		t.Fatalf("replacing a note: %d slots held, want %d", n, before)
	}
	if !stored(keys[9], ActionClick, nil, []byte("u9")) { // late, for the replaced note
		t.Fatal("a late response to the replaced note was not stored")
	}
	drain()
	if !stored(newKey, ActionClick, nil, []byte("u9b")) {
		t.Fatal("the replacing note's response was not stored")
	}
	drain()

	// A post the system refuses frees its slot and says why.
	f.set(func(f *fakeCenter) { f.addCode = 1 })
	before = slots.responses.Outstanding()
	r := mustPost(t, Note{ID: "refused", Title: "x", UserData: []byte("x")})
	if r.Reservation != Reserved {
		t.Fatalf("%+v", r)
	}
	if n := slots.responses.Outstanding(); n != before {
		t.Fatalf("a refused post holds a slot: %d, want %d", n, before)
	}
	s.reset()
	drain()
	if res := s.resultOf(t, r.Request); res.Kind != ResultPost || res.ErrCode != 1 || res.Err != "not allowed" || res.Reservation != Reserved || res.Response != 0 {
		t.Fatalf("result of the refused post: %+v", res)
	}
	f.set(func(f *fakeCenter) { f.addCode = 0 })
}

// A replacement that fails, before it is sent (its attachment, the system
// refusing to make it) or after (the system refusing it), leaves the note it
// would have replaced with its slot; and nothing is evicted for a post that
// fails before it is sent.
func TestFailedReplacementKeepsTheSlot(t *testing.T) {
	f, s := fresh(t, Options{EvictOldest: true})
	first := mustPost(t, Note{ID: "x", Title: "first", UserData: []byte("x1")})
	k1 := f.lastAdd().key
	drain()
	s.reset()

	stageFile = func(string) (string, error) { return "", errors.New("no such file") }
	r := mustPost(t, Note{ID: "x", Title: "with a picture", UserData: []byte("x2"), Attachment: "/no/such.png"})
	waitUntil(t, "the attachment's result", func() bool { return len(s.results()) == 1 })
	if res := s.resultOf(t, r.Request); res.ErrCode != CodeAttachment || res.Reservation != NoResponse {
		t.Fatalf("a failed attachment: %+v", res)
	}
	stageFile = stageAttachment
	if !holding(k1) {
		t.Fatal("a replacement whose attachment failed freed the note's slot")
	}

	f.set(func(f *fakeCenter) { f.prepareErr = fmt.Errorf("%w: the system refused the note", ErrInvalid) })
	if _, err := Post(Note{ID: "x", Title: "refused", UserData: []byte("x3")}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("the system refusing to make a note: %v", err)
	}
	f.set(func(f *fakeCenter) { f.prepareErr = nil; f.addCode = 1 })
	mustPost(t, Note{ID: "x", Title: "not allowed", UserData: []byte("x4")})
	f.set(func(f *fakeCenter) { f.addCode = 0 })
	drain()
	if !holding(k1) || len(slotsOf("x")) != 1 {
		t.Fatalf("a refused replacement: slots of x %v, want only %d", slotsOf("x"), k1)
	}
	s.reset()
	if !stored(k1, ActionClick, nil, []byte("x1")) {
		t.Fatal("not stored")
	}
	drain()
	if got := s.responses(); len(got) != 1 || got[0].Ticket != first.Response || ReadStats().Late != 0 {
		t.Fatalf("the response came through %+v, want its slot %d", got, first.Response)
	}

	// With the table full and EvictOldest, a post whose attachment the
	// system refuses evicts nothing.
	keys := fillSlots(t, f, MaxResponses, "")
	f.set(func(f *fakeCenter) {
		f.prepareErr = fmt.Errorf("%w: the system refused it: unsupported type", errAttachment)
		f.removed = nil
	})
	stageFile = func(p string) (string, error) { return p + ".copy", nil }
	s.reset()
	r = mustPost(t, Note{ID: "pic", Title: "a picture", UserData: []byte("p"), Attachment: "/tmp/a.txt"})
	waitUntil(t, "the refused attachment's result", func() bool { return len(s.results()) == 1 })
	f.set(func(f *fakeCenter) { f.prepareErr = nil })
	if res := s.resultOf(t, r.Request); res.ErrCode != CodeAttachment {
		t.Fatalf("result %+v", res)
	}
	if len(f.removed) != 0 || ReadStats().Evicted != 0 {
		t.Fatalf("evicted %v for a post that failed", f.removed)
	}
	for i, k := range keys {
		if !holding(k) {
			t.Fatalf("note %d lost its slot", i)
		}
	}
}

// A post superseded while it was being prepared (its attachment copying, its
// request being made) is never sent after what superseded it: a later Post
// of its ID, or a Remove of it, wins; the system ends up showing the latest
// note, whose key holds the slot.
func TestSupersededPostIsNotSent(t *testing.T) {
	f, s := fresh(t, Options{})
	g := newGate()
	stageFile = g.stage

	// A, with an attachment, stalls copying; B replaces it.
	a := mustPost(t, Note{ID: "x", Title: "A", UserData: []byte("a"), Attachment: "/tmp/a.png"})
	<-g.entered
	b := mustPost(t, Note{ID: "x", Title: "B", UserData: []byte("b")})
	kb := f.lastAdd().key
	g.release <- nil
	waitUntil(t, "A's result", func() bool { return len(s.results()) == 2 })
	if res := s.resultOf(t, a.Request); res.ErrCode != CodeSuperseded {
		t.Fatalf("A: %+v", res)
	}
	if adds := f.addsOf("x"); len(adds) != 1 || adds[0].key != kb || f.shownKey("x") != kb || !slices.Equal(slotsOf("x"), []uint64{kb}) {
		t.Fatalf("sent %+v; shown %d; slots %v", adds, f.shownKey("x"), slotsOf("x"))
	}
	if res := s.resultOf(t, b.Request); res.Reservation != Reserved || res.ErrCode != 0 {
		t.Fatalf("B: %+v", res)
	}

	// C stalls copying; Remove of its ID comes first.
	s.reset()
	c := mustPost(t, Note{ID: "y", Title: "C", UserData: []byte("c"), Attachment: "/tmp/c.png"})
	<-g.entered
	if err := Remove("y"); err != nil {
		t.Fatal(err)
	}
	g.release <- nil
	waitUntil(t, "C's result", func() bool { return len(s.results()) == 1 })
	if res := s.resultOf(t, c.Request); res.ErrCode != CodeSuperseded || len(f.addsOf("y")) != 0 || len(slotsOf("y")) != 0 {
		t.Fatalf("C: %+v, sent %+v", res, f.addsOf("y"))
	}
	stageFile = stageAttachment

	// D stalls while its request is made (after it was checked); E replaces it.
	s.reset()
	inPrepare, resume := make(chan struct{}), make(chan struct{})
	f.set(func(f *fakeCenter) {
		f.prepareHook = func(n *Note, id string) {
			if n.Title == "D" {
				close(inPrepare)
				<-resume
			}
		}
	})
	dDone := make(chan PostResult)
	go func() {
		r, err := Post(Note{ID: "z", Title: "D", UserData: []byte("d")})
		if err != nil {
			t.Error(err)
		}
		dDone <- r
	}()
	<-inPrepare
	mustPost(t, Note{ID: "z", Title: "E", UserData: []byte("e")})
	ke := f.lastAdd().key
	close(resume)
	d := <-dDone
	drain()
	if res := s.resultOf(t, d.Request); res.ErrCode != CodeSuperseded || f.discarded != 1 {
		t.Fatalf("D: %+v (discarded %d)", res, f.discarded)
	}
	if adds := f.addsOf("z"); len(adds) != 1 || adds[0].key != ke || !slices.Equal(slotsOf("z"), []uint64{ke}) {
		t.Fatalf("sent %+v; slots %v", adds, slotsOf("z"))
	}
	if st := ReadStats(); st.Superseded != 3 {
		t.Fatalf("stats %+v", st)
	}
}

// A note with an Attachment is copied off the caller's goroutine: Post
// returns while the copy is still running, and the result says what it got.
func TestAttachmentIsCopiedOffTheCaller(t *testing.T) {
	f, s := fresh(t, Options{})
	g := newGate()
	stageFile = g.stage
	done := make(chan PostResult, 1)
	go func() {
		r, err := Post(Note{ID: "pic", Title: "a picture", UserData: []byte("p"), Attachment: "/tmp/big.mov"})
		if err != nil {
			t.Error(err)
		}
		done <- r
	}()
	<-g.entered
	var r PostResult
	select {
	case r = <-done:
	case <-time.After(5 * time.Second):
		g.release <- nil
		t.Fatal("Post waited for the attachment's copy")
	}
	if r.Reservation != Pending || len(f.addsOf("pic")) != 0 {
		t.Fatalf("%+v", r)
	}
	g.release <- nil
	waitUntil(t, "the post's result", func() bool { return len(s.results()) == 1 })
	res := s.resultOf(t, r.Request)
	a := f.lastAdd()
	if res.Reservation != Reserved || res.Response == 0 || res.ErrCode != 0 || a.att != "/tmp/big.mov.copy" || a.key == 0 || !holding(a.key) {
		t.Fatalf("result %+v, sent %+v", res, a)
	}

	// The copy itself: the content, in a file of its own.
	src := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(src, []byte("not really a png"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst, err := stageAttachment(src)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(dst)
	if b, err := os.ReadFile(dst); err != nil || string(b) != "not really a png" || filepath.Ext(dst) != ".png" || dst == src {
		t.Fatalf("copied to %s: %q, %v", dst, b, err)
	}
	if _, err := stageAttachment(filepath.Join(t.TempDir(), "missing.png")); err == nil {
		t.Fatal("a missing file was copied")
	}
}

// RemoveThread frees the thread's slots at once and removes the thread's
// notes the system lists (plain ones, earlier runs') delivered before the
// call; a note delivered after it, or of another thread, stays.
func TestRemoveThread(t *testing.T) {
	f, _ := fresh(t, Options{})
	keys := fillSlots(t, f, 3, "t1")
	mustPost(t, Note{ID: "o", Thread: "t2", UserData: []byte("o")})
	ko := f.lastAdd().key
	held := slots.responses.Outstanding()
	t0 := time.Now().UnixNano()
	f.set(func(f *fakeCenter) { f.manual = true })
	if err := RemoveThread("t1"); err != nil {
		t.Fatal(err)
	}
	if n := slots.responses.Outstanding(); n != held-3 {
		t.Fatalf("%d slots held after RemoveThread, want %d", n, held-3)
	}
	f.set(func(f *fakeCenter) {
		if early := strings.Join(f.removed, ","); early != "n0,n1,n2" {
			t.Fatalf("removed at once: %q", early)
		}
		f.removed = nil
	})
	listed(f.takeList(t).ctx, []deliveredNote{
		{id: "n0", thread: "t1", key: keys[0], date: t0 - 5e9},
		{id: "plain", thread: "t1", date: t0 - 1e9},
		{id: "earlier-run", thread: "t1", key: 42, date: t0 - 9e9},
		{id: "o", thread: "t2", key: ko, date: t0 - 1e9},
		{id: "newer", thread: "t1", date: t0 + 60e9},
	})
	workers.Wait()
	if late := strings.Join(f.removedList(), ","); late != "n0,plain,earlier-run" {
		t.Fatalf("removed from the listing: %q", late)
	}
	if n := slots.responses.Outstanding(); n != held-3 || !holding(ko) {
		t.Fatalf("%d slots held, want %d", n, held-3)
	}
	drain()
}

// RemoveThread's listing may be older than a note posted after the call
// with the ID of one it lists: that note is not removed.
func TestRemoveThreadKeepsLaterPosts(t *testing.T) {
	f, _ := fresh(t, Options{})
	mustPost(t, Note{ID: "x", Thread: "t", Title: "plain"})
	mustPost(t, Note{ID: "y", Thread: "t", UserData: []byte("y")})
	drain()
	snapshot := f.shownList() // what the system will list: from before the call
	f.set(func(f *fakeCenter) { f.manual = true })
	if err := RemoveThread("t"); err != nil {
		t.Fatal(err)
	}
	mustPost(t, Note{ID: "x", Thread: "t", Title: "posted after RemoveThread", UserData: []byte("x2")})
	kx := f.lastAdd().key
	drain()
	f.set(func(f *fakeCenter) { f.removed = nil })
	listed(f.takeList(t).ctx, append(snapshot, deliveredNote{id: "earlier", thread: "t", key: 77, date: 1}))
	workers.Wait()
	if removed := f.removedList(); slices.Contains(removed, "x") || !slices.Contains(removed, "earlier") {
		t.Fatalf("removed from the listing: %v", removed)
	}
	if f.shownKey("x") != kx {
		t.Fatal("the later x is gone")
	}
	if !holding(kx) {
		t.Fatal("the later x lost its slot")
	}
}

// A note the user cleared keeps its slot until the service asks the system
// which notes it has: when the app becomes active, the slots of the notes
// the system took before the listing began and no longer has are freed. A
// note taken after it began, or not taken yet, keeps its slot.
func TestReconcileFreesClearedNotes(t *testing.T) {
	f, s := fresh(t, Options{})
	keys := fillSlots(t, f, 4, "") // n0…n3, taken
	f.clear("n0", "n1")
	f.set(func(f *fakeCenter) { f.manual = true })
	appActive()
	pend := f.takeList(t)
	if !pend.pending {
		t.Fatal("the pending notes are not listed first")
	}
	mustPost(t, Note{ID: "after", Title: "posted while listing", UserData: []byte("a")})
	kAfter := f.lastAdd().key
	f.set(func(f *fakeCenter) { f.syncAdd = false })
	mustPost(t, Note{ID: "untaken", Title: "not taken yet", UserData: []byte("u")})
	kUntaken := f.lastAdd().key
	appActive() // one reconciliation at a time
	listed(pend.ctx, nil)
	deliv := f.takeList(t)
	if deliv.pending {
		t.Fatal("the shown notes are not listed second")
	}
	if len(f.lists) != 0 {
		t.Fatalf("listings %+v while one was running", f.lists)
	}
	listed(deliv.ctx, []deliveredNote{{id: "n2", key: keys[2]}, {id: "n3", key: keys[3]}}) // from before "after"
	if holding(keys[0]) || holding(keys[1]) || !holding(keys[2]) || !holding(keys[3]) || !holding(kAfter) || !holding(kUntaken) {
		t.Fatalf("held: n0 %v n1 %v n2 %v n3 %v after %v untaken %v", holding(keys[0]), holding(keys[1]),
			holding(keys[2]), holding(keys[3]), holding(kAfter), holding(kUntaken))
	}
	if st := ReadStats(); st.Reconciled != 2 {
		t.Fatalf("stats %+v", st)
	}
	f.answerAdds()
	// A response to a cleared note (one shown again, say) is still stored.
	s.reset()
	if !stored(keys[0], ActionClick, nil, []byte("u0")) {
		t.Fatal("not stored")
	}
	drain()
	if got := s.responses(); len(got) != 1 || string(got[0].UserData) != "u0" {
		t.Fatalf("delivered %+v", got)
	}
}

// A post that finds every slot taken waits for the reconciliation first: a
// slot freed by a note the user cleared goes to it, and nothing is evicted.
func TestFullTableReconcilesBeforeEvicting(t *testing.T) {
	f, s := fresh(t, Options{EvictOldest: true})
	fillSlots(t, f, MaxResponses, "")
	f.clear("n3", "n200")
	f.set(func(f *fakeCenter) { f.removed = nil })
	r := mustPost(t, Note{ID: "new", Title: "new", UserData: []byte("n")})
	res := waitResult(t, s, r.Request)
	if r.Reservation != Pending || res.Reservation != Reserved || res.Evicted != "" || len(f.removedList()) != 0 {
		t.Fatalf("post %+v, result %+v, removed %v", r, res, f.removedList())
	}
	if st := ReadStats(); st.Reconciled != 2 || st.Evicted != 0 || st.Reserved != MaxResponses-1 {
		t.Fatalf("stats %+v", st)
	}
}

// EvictOldest never takes the slot of a note replaced by one not taken yet:
// the system removes by ID, which would remove the replacement too.
func TestEvictionSkipsReplacedNotes(t *testing.T) {
	f, _ := fresh(t, Options{EvictOldest: true})
	mustPost(t, Note{ID: "x", Title: "x0", UserData: []byte("x0")}) // the oldest
	k0 := f.lastAdd().key
	for i := range MaxResponses - 1 {
		mustPost(t, Note{ID: fmt.Sprintf("n%d", i), Title: "t", UserData: []byte("u")})
		if i%16 == 15 {
			drain()
		}
	}
	drain()
	f.set(func(f *fakeCenter) { f.syncAdd = false; f.removed = nil })
	mustPost(t, Note{ID: "x", Title: "x1", UserData: []byte("x1")}) // evicts n0, not x
	waitUntil(t, "x1", func() bool { return len(f.addsOf("x")) == 2 })
	k1 := f.lastAdd().key
	mustPost(t, Note{ID: "q", Title: "q", UserData: []byte("q")}) // x0 is the oldest, but replaced
	waitUntil(t, "q", func() bool { return len(f.addsOf("q")) == 1 })
	if removed := strings.Join(f.removedList(), ","); removed != "n0,n1" {
		t.Fatalf("evicted %v, want n0,n1", removed)
	}
	if !holding(k0) || !holding(k1) {
		t.Fatal("x's slots")
	}
	f.answerAdds()
	drain()
	if holding(k0) || !holding(k1) || f.shownKey("x") != k1 {
		t.Fatalf("after x1 was taken: x0 held %v, x1 held %v", holding(k0), holding(k1))
	}
	if st := ReadStats(); st.Evicted != 2 {
		t.Fatalf("stats %+v", st)
	}
}

// At launch the notes of earlier runs still in Notification Center get their
// slots back, newest first; those that do not fit are reported by ID, and a
// note posted before the listing arrived (during Start) keeps its slot.
func TestRestoreAtLaunch(t *testing.T) {
	const n = MaxResponses + 44
	keyOf := func(i int) uint64 { return uint64(1)<<40 | uint64(i+1) } // an earlier run's keys
	now := time.Now().UnixNano()
	var list []deliveredNote
	for i := range n { // old0 is the oldest
		list = append(list, deliveredNote{id: fmt.Sprintf("old%d", i), thread: "t", key: keyOf(i), date: now - int64(n-i)*1e9})
	}
	list = append(list, deliveredNote{id: "plain", date: now})

	f, s := relaunch(t, Options{}, true)
	launch := f.takeList(t)
	// The click that launched the app, on a note of an earlier run, arrives
	// before the listing: delivered through a spare record.
	clickedKey := uint64(7) << 40
	if !stored(clickedKey, ActionClick, nil, []byte("launch")) {
		t.Fatal("the launching click was not stored")
	}
	drain()
	if got := s.responses(); len(got) != 1 || string(got[0].UserData) != "launch" {
		t.Fatalf("the launching click: %+v", got)
	}
	early := mustPost(t, Note{ID: "early", Title: "posted during Start", UserData: []byte("e")})
	earlyKey := f.lastAdd().key
	list = append(list,
		deliveredNote{id: "early", key: earlyKey, date: now},         // the listing may show it: it keeps its one slot
		deliveredNote{id: "clicked", key: clickedKey, date: now + 1}, // answered already: no slot again
	)
	listed(launch.ctx, list)
	drain()
	var res Result
	for _, r := range s.results() {
		if r.Kind == ResultRestored {
			res = r
		}
	}
	// 255 slots were free: old299 … old45 get them, old44 … old0 do not.
	if res.Restored != MaxResponses-1 || res.Overflow != n-(MaxResponses-1) || len(res.OverflowIDs) != res.Overflow {
		t.Fatalf("restored %d, overflow %d (%d ids)", res.Restored, res.Overflow, len(res.OverflowIDs))
	}
	if res.OverflowIDs[0] != fmt.Sprintf("old%d", res.Overflow-1) || res.OverflowIDs[len(res.OverflowIDs)-1] != "old0" {
		t.Fatalf("overflow %v, want the oldest notes", res.OverflowIDs)
	}
	if holding(clickedKey) {
		t.Fatal("the answered note got a slot")
	}
	s.reset()
	// A restored note's response is delivered with its UserData (the
	// delegate reads it from the note); an overflowed one's through a spare.
	if !stored(keyOf(n-1), 1, []byte("hi"), []byte("old-ud")) {
		t.Fatal("response to a restored note not stored")
	}
	if !stored(keyOf(0), ActionClick, nil, []byte("old0")) {
		t.Fatal("response to an overflowed note not stored")
	}
	if !stored(earlyKey, ActionClick, nil, []byte("e")) {
		t.Fatal("the early note lost its slot")
	}
	drain()
	got := s.responses()
	if len(got) != 3 || string(got[0].Reply) != "hi" || string(got[0].UserData) != "old-ud" || string(got[1].UserData) != "old0" || got[2].Ticket != early.Response {
		t.Fatalf("delivered %+v", got)
	}
	if st := ReadStats(); st.Late != 2 || st.Reserved != MaxResponses-2 {
		t.Fatalf("stats %+v", st)
	}
	// The app removes the overflow (no slots); restored notes' slots go
	// with Remove like any other.
	if err := Remove(res.OverflowIDs...); err != nil {
		t.Fatal(err)
	}
	if err := Remove("old298", "old297"); err != nil {
		t.Fatal(err)
	}
	if held := slots.responses.Outstanding(); held != MaxResponses-2-2 {
		t.Fatalf("%d slots held, want %d", held, MaxResponses-4)
	}
}

// The launch's listing may be older than what the app did during Start: a
// note it replaced, one it removed, a thread it removed, are not given their
// old slots back.
func TestRestoreSkipsWhatStartChanged(t *testing.T) {
	f, s := relaunch(t, Options{}, true)
	launch := f.takeList(t)
	now := time.Now().UnixNano()
	mustPost(t, Note{ID: "x", Title: "x again", UserData: []byte("x")})
	kx := f.lastAdd().key
	if err := Remove("y"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveThread("tz"); err != nil {
		t.Fatal(err)
	}
	listed(launch.ctx, []deliveredNote{
		{id: "x", key: 1<<40 | 1, date: now - 3e9},
		{id: "y", key: 1<<40 | 2, date: now - 2e9},
		{id: "z", thread: "tz", key: 1<<40 | 3, date: now - 1e9},
		{id: "w", key: 1<<40 | 4, date: now - 1e9},
	})
	drain()
	var res Result
	for _, r := range s.results() {
		if r.Kind == ResultRestored {
			res = r
		}
	}
	if res.Restored != 1 || holding(1<<40|1) || holding(1<<40|2) || holding(1<<40|3) || !holding(1<<40|4) {
		t.Fatalf("restored %+v", res)
	}
	if !slices.Equal(slotsOf("x"), []uint64{kx}) {
		t.Fatalf("x's slots %v, want only %d", slotsOf("x"), kx)
	}
	listed(f.takeList(t).ctx, nil) // RemoveThread's
	workers.Wait()
}

// A response arriving while the launch's restore runs, for a note it is
// about to give a slot, is never left with a slot of its own afterwards: it
// is delivered once, and the note gets no slot. Any number of responses
// arriving before the listing are remembered (or, past what is kept, the
// restore gives no slot back at all).
func TestRestoreRacesAResponse(t *testing.T) {
	now := time.Now().UnixNano()
	race := uint64(1)<<40 | 9
	f, s := relaunch(t, Options{}, true)
	launch := f.takeList(t)
	testHookRestoring = func(key uint64) {
		if key == race && !stored(race, ActionClick, nil, []byte("raced")) {
			t.Error("the racing response was not stored")
		}
	}
	early := make([]uint64, 20) // more than a ring of 16 would keep
	for i := range early {
		early[i] = uint64(2)<<40 | uint64(i+1)
		if !stored(early[i], ActionClick, nil, nil) {
			t.Fatal(i)
		}
	}
	drain()
	listed(launch.ctx, []deliveredNote{
		{id: "raced", key: race, date: now},
		{id: "first-answered", key: early[0], date: now - 1},
		{id: "kept", key: 1<<40 | 10, date: now - 2},
	})
	drain()
	testHookRestoring = nil
	var res Result
	for _, r := range s.results() {
		if r.Kind == ResultRestored {
			res = r
		}
	}
	if res.Restored != 1 || holding(race) || holding(early[0]) || !holding(1<<40|10) {
		t.Fatalf("restored %+v; raced held %v, answered held %v", res, holding(race), holding(early[0]))
	}
	var raced int
	for _, r := range s.responses() {
		if string(r.UserData) == "raced" {
			raced++
		}
	}
	if raced != 1 {
		t.Fatalf("the racing response delivered %d times", raced)
	}

	// More responses than are kept: nothing is given back.
	f, s = relaunch(t, Options{}, true)
	launch = f.takeList(t)
	for i := range maxAnswered + 1 {
		if !stored(uint64(3)<<40|uint64(i+1), ActionClick, nil, nil) {
			t.Fatal(i)
		}
		if i%64 == 63 {
			drain()
		}
	}
	drain()
	listed(launch.ctx, []deliveredNote{{id: "a", key: 1<<40 | 20, date: now}})
	drain()
	for _, r := range s.results() {
		if r.Kind == ResultRestored && (r.Restored != 0 || !slices.Equal(r.OverflowIDs, []string{"a"})) {
			t.Fatalf("restored %+v with the answered keys not all kept", r)
		}
	}
}

// Delivering a response allocates nothing once the slots are warm: the
// delegate's lookup, the copy into the reserved record and the dispatcher's
// delivery.
func TestResponseDeliveryAllocs(t *testing.T) {
	f, s := fresh(t, Options{})
	keys := fillSlots(t, f, 120, "")
	s.evs = make([]platform.AppEvent, 0, 256)
	s.offs = make([]int, 0, 256)
	s.data = make([]byte, 0, 1<<20)
	reply := []byte(strings.Repeat("reply ", 200))
	ud := []byte("chat-1/msg-2")
	i := 0
	allocs := testing.AllocsPerRun(100, func() {
		if !stored(keys[i], 1, reply, ud) {
			t.Fatal("not stored")
		}
		i++
		platform.DispatchApp()
	})
	if allocs != 0 {
		t.Fatalf("%v allocations per response", allocs)
	}
	if got := len(s.responses()); got != i {
		t.Fatalf("%d responses delivered of %d", got, i)
	}
	drain()
}

// Keys never repeat: not when their low 32 bits wrap, and not at all once
// 2^64 have been made (notes go out without actions from then on).
func TestKeysNeverRepeat(t *testing.T) {
	f, _ := fresh(t, Options{})
	mustPost(t, Note{ID: "a", UserData: []byte("a")})
	ka := f.lastAdd().key
	svc.mu.Lock()
	keys.n += 1<<32 - 1 // the next key's low 32 bits are a's
	svc.mu.Unlock()
	mustPost(t, Note{ID: "b", UserData: []byte("b")})
	kb := f.lastAdd().key
	if ka == kb || ka == 0 || kb == 0 {
		t.Fatalf("keys %#x and %#x", ka, kb)
	}
	drain()
	svc.mu.Lock()
	keys.n = math.MaxUint64 - 1
	svc.mu.Unlock()
	mustPost(t, Note{ID: "last", UserData: []byte("l")})
	kl := f.lastAdd().key
	r := mustPost(t, Note{ID: "after", UserData: []byte("x")})
	r2 := mustPost(t, Note{ID: "after2", UserData: []byte("x")})
	if kl == 0 || r.Reservation != PostedWithoutActions || r2.Reservation != PostedWithoutActions || f.lastAdd().key != 0 {
		t.Fatalf("after the last key: %+v %+v, sent %+v", r, r2, f.lastAdd())
	}
	drain()
	if n := slots.responses.Outstanding(); n != 3 {
		t.Fatalf("%d slots held, want 3", n)
	}
}

// Setup after the application finished launching fails (the launching click
// would be lost); a later Setup after a successful one changes the options.
func TestSetupTooLate(t *testing.T) {
	f, _ := fresh(t, Options{})
	f.set(func(f *fakeCenter) { f.isLaunched = true })
	if err := Setup(Options{EvictOldest: true}); err != nil {
		t.Fatalf("Setup again: %v", err)
	}
	svc.mu.Lock()
	evict := svc.opts.EvictOldest
	svc.mu.Unlock()
	if !evict {
		t.Fatal("the options did not change")
	}
	svc.setupMu.Lock()
	svc.on.Store(false)
	svc.setupMu.Unlock()
	defer svc.on.Store(true)
	if err := Setup(Options{}); !errors.Is(err, ErrTooLate) {
		t.Fatalf("Setup after launching: %v, want ErrTooLate", err)
	}
	if _, err := Post(Note{Title: "x"}); !errors.Is(err, ErrNotSetUp) {
		t.Fatalf("Post after a late Setup: %v", err)
	}
}

// The requests take a ticket each and report through it; a full table
// refuses the request at once.
func TestRequests(t *testing.T) {
	f, s := fresh(t, Options{})
	tk, err := RequestPermission(Alert | Sound)
	if err != nil {
		t.Fatal(err)
	}
	st, err := Status()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.auths) != 1 || f.auths[0] != tk || len(f.settings_) != 1 || f.settings_[0] != st {
		t.Fatalf("auths %v settings %v", f.auths, f.settings_)
	}
	completeAuth(tk, true, nil)
	completeStatus(st, int64(StatusAuthorized))
	drain()
	res := s.results()
	if len(res) != 2 || res[0].Kind != ResultPermission || !res[0].Granted || res[0].Ticket != tk ||
		res[1].Kind != ResultStatus || res[1].Status != StatusAuthorized || res[1].Ticket != st {
		t.Fatalf("results %+v", res)
	}
	var held []Ticket
	for {
		tk, err := Status()
		if err != nil {
			if !errors.Is(err, ErrBusy) || !errors.Is(err, platform.ErrBusy) {
				t.Fatalf("a full table: %v", err)
			}
			break
		}
		held = append(held, tk)
	}
	if len(held) != platform.MaxTickets {
		t.Fatalf("%d tickets before ErrBusy", len(held))
	}
	if _, err := Post(Note{Title: "x"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("Post with the request table full: %v", err)
	}
	for _, tk := range held {
		completeStatus(tk, 0)
	}
	drain()
	if _, err := Post(Note{Category: "nope"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an unregistered category: %v", err)
	}
	if _, err := Post(Note{UserData: make([]byte, MaxUserData+1)}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("UserData too long: %v", err)
	}
	if err := RegisterCategories(Category{ID: "c", Actions: []Action{{ID: 0, Title: "x"}}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an action without an ID: %v", err)
	}
	// Categories are a union: registering again replaces by ID and keeps the others.
	if err := RegisterCategories(chat, Category{ID: "b"}); err != nil {
		t.Fatal(err)
	}
	if err := RegisterCategories(Category{ID: "msg", NotifyDismiss: true}); err != nil {
		t.Fatal(err)
	}
	if len(f.cats) != 2 || f.cats[0].ID != "msg" || !f.cats[0].NotifyDismiss || len(f.cats[0].Actions) != 0 || f.cats[1].ID != "b" {
		t.Fatalf("categories sent %+v", f.cats)
	}
	Foreground(true)
	if !presentInForeground() {
		t.Fatal("Foreground(true)")
	}
	Foreground(false)
}

func TestDecode(t *testing.T) {
	data := []byte("hello thereUD")
	r := decodeResponse(9, 3, 2, data)
	if r.Ticket != 9 || r.Action != 3 || string(r.Reply) != "hello there" || string(r.UserData) != "UD" {
		t.Fatalf("%+v", r)
	}
	// What widgettest.FakeNotificationResponse sends: the reply alone.
	if r := decodeResponse(9, 2, 0, []byte("hello")); string(r.Reply) != "hello" || r.UserData != nil {
		t.Fatalf("%+v", r)
	}
	if r := decodeResponse(1, 0, 99, []byte("x")); string(r.Reply) != "x" || r.UserData != nil {
		t.Fatalf("a bad length: %+v", r)
	}
	if r := decodeResponse(1, 0, 3, []byte("abc")); r.Reply != nil || string(r.UserData) != "abc" {
		t.Fatalf("UserData only: %+v", r)
	}
	res := decodeResult(5, int64(ResultRestored), 3|2<<32, []byte("a\x00b"))
	if res.Restored != 3 || res.Overflow != 2 || strings.Join(res.OverflowIDs, ",") != "a,b" {
		t.Fatalf("%+v", res)
	}
	big := Ticket(0xFFFF<<48 | 0xFFFFFFFF<<16 | 0xFFFF)
	if res := decodeResult(5, int64(ResultPost)|int64(Reserved)<<8, int64(big), []byte("old")); res.Kind != ResultPost ||
		res.Reservation != Reserved || res.Response != big || res.Evicted != "old" || res.ErrCode != 0 {
		t.Fatalf("%+v", res)
	}
	if res := decodeResult(5, int64(ResultPost)|int64(Reserved)<<8|postFailed, 1, []byte("denied")); res.Kind != ResultPost ||
		res.Reservation != Reserved || res.ErrCode != 1 || res.Err != "denied" || res.Response != 0 {
		t.Fatalf("%+v", res)
	}
	if _, ok := ResponseOf(nil); ok {
		t.Fatal("ResponseOf(nil)")
	}
	if _, ok := ResponseOf(&app.Event{Kind: app.EvService, Source: platform.SourceDialog}); ok {
		t.Fatal("ResponseOf of a dialog's result")
	}
	if _, ok := ResultOf(&app.Event{Kind: app.EvPost, Source: platform.SourceNotify}); ok {
		t.Fatal("ResultOf of a post")
	}
}

func TestActionOf(t *testing.T) {
	for id, want := range map[string]int64{
		"": ActionClick, defaultActionID: ActionClick, dismissActionID: ActionDismiss,
		"gnui.action.7": 7, "gnui.action.123456789": 123456789, "gnui.action.0": ActionOther,
		"gnui.action.": ActionOther, "gnui.action.7x": ActionOther, "other": ActionOther,
		"gnui.action.1000000000000000000":  1e18,               // 19 digits
		"gnui.action.9223372036854775807":  math.MaxInt64,      // the largest
		"gnui.action.9223372036854775808":  ActionOther,        // one past it
		"gnui.action.9999999999999999999":  ActionOther,        // 19 digits, too large
		"gnui.action.10000000000000000000": ActionOther,        // 20 digits
		"gnui.action.0000000000000000042":  42,                 // 19 digits with zeros
		"gnui.action.-5":                   ActionOther,        // not a digit
		"gnui.action.18446744073709551617": ActionOther,        // past 2^64
		"gnui.action.922337203685477580":   922337203685477580, // 18 digits
	} {
		if got := actionOf([]byte(id)); got != want {
			t.Errorf("actionOf(%q) = %d, want %d", id, got, want)
		}
	}
}

func TestCutUTF8(t *testing.T) {
	s := []byte("aé🙂")
	for n, want := range map[int]string{0: "", 1: "a", 2: "a", 3: "aé", 4: "aé", 6: "aé", 7: "aé🙂", 9: "aé🙂"} {
		if got := string(cutUTF8(s, n)); got != want {
			t.Errorf("cutUTF8(%d) = %q, want %q", n, got, want)
		}
	}
}

// Without a notification center the calls fail cleanly: ErrUnsupported
// where the OS has none here, ErrUnavailable on macOS outside a bundle (a
// test binary), never a crash.
func TestWithoutACenter(t *testing.T) {
	svc.setupMu.Lock()
	prevOn := svc.on.Load()
	svc.b = nativeCenter{}
	svc.on.Store(false)
	svc.setupMu.Unlock()
	t.Cleanup(func() {
		svc.setupMu.Lock()
		svc.b = nil
		svc.on.Store(prevOn)
		svc.setupMu.Unlock()
	})
	if Available() {
		t.Fatal("Available in a test binary")
	}
	if (nativeCenter{}).launched() {
		t.Fatal("launched without an application object")
	}
	err := Setup(Options{})
	switch runtime.GOOS {
	case "darwin":
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("Setup: %v, want ErrUnavailable", err)
		}
		if _, err := Post(Note{Title: "x"}); !errors.Is(err, ErrNotSetUp) {
			t.Fatalf("Post: %v", err)
		}
	default:
		if !errors.Is(err, ErrUnsupported) || !errors.Is(err, errors.ErrUnsupported) {
			t.Fatalf("Setup: %v, want ErrUnsupported", err)
		}
		if _, err := Post(Note{Title: "x"}); !errors.Is(err, errors.ErrUnsupported) {
			t.Fatalf("Post: %v", err)
		}
	}
}

// A post that fails leaves the note the system still shows for its ID as it
// was: the launch's restore gives an earlier run's note its slot back when
// this run's posts of the ID failed (before they were sent, or refused), and
// when one is still on its way (the slot goes if the system takes it).
func TestFailedPostKeepsRestore(t *testing.T) {
	f, s := relaunch(t, Options{}, true)
	launch := f.takeList(t)
	stageFile = func(string) (string, error) { return "", errors.New("no such file") }
	mustPost(t, Note{ID: "x", Thread: "t2", Title: "a picture", UserData: []byte("x"), Attachment: "/no/such.png"})
	f.set(func(f *fakeCenter) { f.prepareErr = fmt.Errorf("%w: refused", ErrInvalid) })
	if _, err := Post(Note{ID: "y", Title: "refused", UserData: []byte("y")}); err == nil {
		t.Fatal("posted")
	}
	f.set(func(f *fakeCenter) { f.prepareErr = nil; f.addCode = 1 })
	mustPost(t, Note{ID: "z", Title: "not allowed", UserData: []byte("z")})
	f.set(func(f *fakeCenter) { f.addCode = 0; f.syncAdd = false })
	mustPost(t, Note{ID: "w", Title: "on its way", UserData: []byte("w")}) // answered after the listing
	mustPost(t, Note{ID: "v", Title: "on its way, refused", UserData: []byte("v")})
	waitUntil(t, "x's and z's results", func() bool { return len(s.results()) == 2 })
	old := func(i uint64) uint64 { return 1<<40 | i }
	now := time.Now().UnixNano()
	listed(launch.ctx, []deliveredNote{
		{id: "x", thread: "t1", key: old(1), date: now - 5}, {id: "y", key: old(2), date: now - 4},
		{id: "z", key: old(3), date: now - 3}, {id: "w", key: old(4), date: now - 2}, {id: "v", key: old(5), date: now - 1},
	})
	drain()
	for i := uint64(1); i <= 5; i++ {
		if !holding(old(i)) {
			t.Fatalf("the earlier run's note %d got no slot back", i)
		}
	}
	f.mu.Lock()
	sent := f.unanswered
	f.unanswered = nil
	f.mu.Unlock()
	for _, a := range sent {
		if a.id == "v" {
			f.set(func(f *fakeCenter) { f.addCode = 1 })
		}
		f.answerAdd(a)
		f.set(func(f *fakeCenter) { f.addCode = 0 })
	}
	if holding(old(4)) || !holding(old(5)) {
		t.Fatalf("after w was taken and v refused: w's old note held %v, v's %v", holding(old(4)), holding(old(5)))
	}
}

// After a note in thread t1 was replaced by a post (in t2) that failed,
// RemoveThread(t1) removes the note still shown.
func TestRemoveThreadAfterFailedReplacement(t *testing.T) {
	f, _ := fresh(t, Options{})
	for _, fail := range []string{"refused", "attachment"} {
		id := "x-" + fail
		mustPost(t, Note{ID: id, Thread: "t1", Title: "shown", UserData: []byte("1")})
		k := f.lastAdd().key
		drain()
		switch fail {
		case "refused":
			f.set(func(f *fakeCenter) { f.addCode = 1 })
			mustPost(t, Note{ID: id, Thread: "t2", Title: "not allowed", UserData: []byte("2")})
			f.set(func(f *fakeCenter) { f.addCode = 0 })
		case "attachment":
			stageFile = func(string) (string, error) { return "", errors.New("no such file") }
			mustPost(t, Note{ID: id, Thread: "t2", Title: "a picture", UserData: []byte("2"), Attachment: "/no/such.png"})
			workers.Wait()
			stageFile = stageAttachment
		}
		drain()
		f.set(func(f *fakeCenter) { f.removed = nil; f.manual = true })
		if err := RemoveThread("t1"); err != nil {
			t.Fatal(err)
		}
		if removed := f.removedList(); !slices.Equal(removed, []string{id}) || holding(k) {
			t.Fatalf("%s: removed %v at once, slot held %v", fail, removed, holding(k))
		}
		listed(f.takeList(t).ctx, nil)
		workers.Wait()
		f.set(func(f *fakeCenter) { f.manual = false })
	}
}

// After a post replacing the oldest note failed, EvictOldest still takes
// that note's slot.
func TestEvictionAfterFailedReplacement(t *testing.T) {
	f, s := fresh(t, Options{EvictOldest: true})
	mustPost(t, Note{ID: "x", Title: "the oldest", UserData: []byte("x")})
	for i := range MaxResponses - 1 {
		mustPost(t, Note{ID: fmt.Sprintf("n%d", i), Title: "t", UserData: []byte("u")})
		if i%16 == 15 {
			drain()
		}
	}
	drain()
	f.set(func(f *fakeCenter) { f.prepareErr = fmt.Errorf("%w: refused", ErrInvalid) })
	r := mustPost(t, Note{ID: "x", Title: "refused", UserData: []byte("x2")}) // waits for the reconciliation: fails then
	waitResult(t, s, r.Request)
	f.set(func(f *fakeCenter) { f.prepareErr = nil; f.removed = nil })
	r = mustPost(t, Note{ID: "q", Title: "q", UserData: []byte("q")})
	if res := waitResult(t, s, r.Request); res.Evicted != "x" {
		t.Fatalf("evicted %q (removed %v), want x", res.Evicted, f.removedList())
	}
}

// A post that waits for the reconciliation has its own copy of the UserData:
// the caller may reuse the buffer once Post returns.
func TestDeferredPostOwnsItsUserData(t *testing.T) {
	f, s := fresh(t, Options{})
	fillSlots(t, f, MaxResponses, "")
	f.clear("n0")
	f.set(func(f *fakeCenter) { f.manual = true })
	buf := []byte("original")
	r := mustPost(t, Note{ID: "late", Title: "late", UserData: buf})
	if r.Reservation != Pending {
		t.Fatalf("%+v, want Pending", r)
	}
	copy(buf, "MUTATED!")
	listed(f.takeList(t).ctx, nil)
	listed(f.takeList(t).ctx, f.shownList())
	res := waitResult(t, s, r.Request)
	if a := f.lastAdd(); a.id != "late" || string(a.userData) != "original" || res.Reservation != Reserved {
		t.Fatalf("sent %q with UserData %q (result %+v), want %q", a.id, a.userData, res, "original")
	}
}

// The app's last drain (platform.FlushApp, before EvWillTerminate) stores a
// parked response while it frees records, delivers it and answers the
// system; a response arriving after it can never be stored, and is given up
// at once (its handler released, counted). No retry goroutine is left.
func TestLastDrainEndsParking(t *testing.T) {
	f, s := fresh(t, Options{})
	var acks, drops atomic.Int32
	parkOne(t, f, uint64(1)<<41, "at the end", &acks, &drops)
	s.reset()
	platform.FlushApp()
	var found bool
	for _, r := range s.responses() {
		found = found || string(r.UserData) == "at the end"
	}
	if st := ReadStats(); !found || acks.Load() != 1 || drops.Load() != 0 || st.Parked != 0 || st.LostAtExit != 0 || retrying() {
		t.Fatalf("after the last drain: delivered %v, answered %d, given up %d, retrying %v, stats %+v", found, acks.Load(), drops.Load(), retrying(), st)
	}
	p := respond(uint64(1)<<41|1, 1, nil, []byte("too late"))
	if p == nil {
		t.Fatal("stored after the last drain")
	}
	p.ack, p.drop = func() { acks.Add(1) }, func() { drops.Add(1) }
	p.wait()
	if st := ReadStats(); acks.Load() != 1 || drops.Load() != 1 || st.LostAtExit != 1 || st.Parked != 0 || retrying() {
		t.Fatalf("after it: answered %d, given up %d, retrying %v, stats %+v", acks.Load(), drops.Load(), retrying(), st)
	}
}

// A parked response that cannot be stored even in the last drain (no record
// frees before its time is up) is given up when the gate closes, once; the
// retry goroutine is stopped.
func TestLastDrainGivesUpWhatCannotBeStored(t *testing.T) {
	f, _ := fresh(t, Options{})
	fillSlots(t, f, MaxResponses, "") // every slot's record reserved for a note
	var held []Ticket
	for range nSpare { // and every spare, by something that never completes
		tk, err := slots.spares.Reserve(0)
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, tk)
	}
	defer func() {
		for _, tk := range held {
			slots.spares.Cancel(tk)
		}
	}()
	p := respond(uint64(1)<<41, 1, nil, []byte("never"))
	if p == nil {
		t.Fatal("stored with every record taken")
	}
	var acks, drops atomic.Int32
	p.ack, p.drop = func() { acks.Add(1) }, func() { drops.Add(1) }
	p.wait()
	if !retrying() {
		t.Fatal("no retries")
	}
	platform.FlushApp()
	if st := ReadStats(); acks.Load() != 0 || drops.Load() != 1 || st.LostAtExit != 1 || st.Parked != 0 || retrying() {
		t.Fatalf("answered %d, given up %d, retrying %v, stats %+v", acks.Load(), drops.Load(), retrying(), st)
	}
}
