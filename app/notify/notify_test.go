package notify

import (
	"bytes"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"gonativeui/app"
	"gonativeui/platform"
)

// The service against a fake notification center: everything above the
// native layer (the slots, the requests, the delivery through the
// application object's dispatcher, which the test runs itself) is the real
// code. The delegate's part is respond, which the native delegate calls with
// what it read from the system's response.

type fakeAdd struct {
	id, category, thread string
	key                  uint64
	userData             []byte
	ctx                  int
}

type fakeCenter struct {
	mu        sync.Mutex
	adds      []fakeAdd
	removed   []string
	cats      []Category
	lists     []int // the contexts of the listings asked for
	auths     []Ticket
	settings_ []Ticket
	addCode   int64 // the result the system gives a post (0: taken)
	syncAdd   bool  // the post's result is given inside add (the system gives it later)
}

func (f *fakeCenter) supported() bool { return true }
func (f *fakeCenter) available() bool { return true }
func (f *fakeCenter) start() error    { return nil }
func (f *fakeCenter) authorize(tk Ticket, p Permission) error {
	f.mu.Lock()
	f.auths = append(f.auths, tk)
	f.mu.Unlock()
	return nil
}
func (f *fakeCenter) settings(tk Ticket) error {
	f.mu.Lock()
	f.settings_ = append(f.settings_, tk)
	f.mu.Unlock()
	return nil
}
func (f *fakeCenter) categories(cats []Category) error {
	f.mu.Lock()
	f.cats = cats
	f.mu.Unlock()
	return nil
}
func (f *fakeCenter) add(n *Note, id string, key uint64, ctx int) error {
	a := fakeAdd{id: id, thread: n.Thread, key: key, ctx: ctx}
	if key != 0 { // what the native center sends: no category or UserData without a key
		a.category, a.userData = n.Category, append([]byte(nil), n.UserData...)
	}
	f.mu.Lock()
	f.adds = append(f.adds, a)
	code, now := f.addCode, f.syncAdd
	f.mu.Unlock()
	if now {
		var msg []byte
		if code != 0 {
			msg = []byte("not allowed")
		}
		added(ctx, code, msg)
	}
	return nil
}
func (f *fakeCenter) remove(ids []string) {
	f.mu.Lock()
	f.removed = append(f.removed, ids...)
	f.mu.Unlock()
}
func (f *fakeCenter) delivered(ctx int) error {
	f.mu.Lock()
	f.lists = append(f.lists, ctx)
	f.mu.Unlock()
	return nil
}

func (f *fakeCenter) lastAdd() fakeAdd {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.adds[len(f.adds)-1]
}

func (f *fakeCenter) takeList(t *testing.T) int {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.lists) == 0 {
		t.Fatal("no listing was asked for")
	}
	ctx := f.lists[0]
	f.lists = f.lists[1:]
	return ctx
}

// sink records what the dispatcher delivers. It allocates nothing once its
// arrays have room (the allocation test).
type sink struct {
	evs   []platform.AppEvent
	data  []byte
	offs  []int
	nResp int // responses delivered
}

func (s *sink) AppEvent(e *platform.AppEvent, data []byte) {
	s.evs = append(s.evs, *e)
	s.offs = append(s.offs, len(s.data))
	s.data = append(s.data, data...)
	if e.Kind == platform.AppEvService && e.Source == platform.SourceNotifyResponse {
		s.nResp++
	}
}
func (s *sink) AppSafePoint() {}

func (s *sink) dataOf(i int) []byte {
	end := len(s.data)
	if i+1 < len(s.offs) {
		end = s.offs[i+1]
	}
	return s.data[s.offs[i]:end]
}

func (s *sink) reset() { s.evs, s.data, s.offs, s.nResp = s.evs[:0], s.data[:0], s.offs[:0], 0 }

// responses are the responses delivered, decoded.
func (s *sink) responses() []Response {
	var out []Response
	for i, e := range s.evs {
		if e.Kind == platform.AppEvService && e.Source == platform.SourceNotifyResponse {
			out = append(out, decodeResponse(e.ID, e.A, e.B, s.dataOf(i)))
		}
	}
	return out
}

// results are the request results delivered, decoded.
func (s *sink) results() []Result {
	var out []Result
	for i, e := range s.evs {
		if e.Kind == platform.AppEvService && e.Source == platform.SourceNotify {
			out = append(out, decodeResult(e.ID, e.A, e.B, s.dataOf(i)))
		}
	}
	return out
}

// drain runs the dispatcher until it delivers nothing more.
func drain() {
	for quiet := 0; quiet < 3; {
		n := platform.DeliveredApp()
		platform.DispatchApp()
		if platform.DeliveredApp() == n {
			quiet++
		} else {
			quiet = 0
		}
	}
}

// fresh sets the service up again over a fake center with options o (a new
// launch) and answers the launch's listing with nothing.
func fresh(t *testing.T, o Options) (*fakeCenter, *sink) {
	t.Helper()
	f, s := relaunch(t, o)
	listed(f.takeList(t), nil)
	drain()
	if r := s.results(); len(r) != 1 || r[0].Kind != ResultRestored || r[0].Restored != 0 {
		t.Fatalf("after Setup: %+v, want one empty ResultRestored", r)
	}
	s.reset()
	if n := slots.responses.Outstanding(); n != 0 {
		t.Fatalf("%d slots held by a fresh service", n)
	}
	return f, s
}

// relaunch sets the service up again over a fake center with options o:
// what an earlier test left is released and delivered, and the counters
// start at 0. The launch's listing is left to the test.
func relaunch(t *testing.T, o Options) (*fakeCenter, *sink) {
	t.Helper()
	s := &sink{}
	platform.SetAppSink(s)
	t.Cleanup(func() { platform.SetAppSink(nil) })
	if svc.on.Load() {
		svc.mu.Lock()
		for i := range slots.s {
			releaseSlot(i, slots.s[i].k)
		}
		svc.mu.Unlock()
		for i := 0; i < maxPending; i++ {
			if p, ok := endPending(i); ok && p.ticket != 0 {
				slots.requests.Cancel(p.ticket)
			}
		}
	}
	drain()
	s.reset()
	f := &fakeCenter{syncAdd: true}
	svc.setupMu.Lock()
	svc.b = f
	svc.on.Store(false)
	svc.setupMu.Unlock()
	svc.catMu.Lock()
	svc.cats = nil
	svc.catMu.Unlock()
	for _, c := range []*atomic.Uint64{&slots.withoutActions, &slots.evicted, &slots.stored, &slots.plain, &slots.lost} {
		c.Store(0)
	}
	if err := Setup(o); err != nil {
		t.Fatal(err)
	}
	return f, s
}

func mustPost(t *testing.T, n Note) PostResult {
	t.Helper()
	r, err := Post(n)
	if err != nil {
		t.Fatalf("Post(%q): %v", n.ID, err)
	}
	return r
}

var chat = Category{ID: "msg", Actions: []Action{{ID: 1, Title: "Reply", Reply: true}, {ID: 2, Title: "Mark as Read"}}}

// fillSlots posts n actionable notes "n0"… and returns their keys; the posts'
// results are delivered as it goes (each takes a request ticket until then).
func fillSlots(t *testing.T, f *fakeCenter, n int, thread string) []uint64 {
	t.Helper()
	keys := make([]uint64, n)
	for i := range n {
		r := mustPost(t, Note{ID: fmt.Sprintf("n%d", i), Thread: thread, Title: "t", UserData: []byte(fmt.Sprintf("u%d", i))})
		if r.Reservation != Reserved || r.Response == 0 {
			t.Fatalf("note %d: %+v, want Reserved", i, r)
		}
		keys[i] = f.lastAdd().key
		if i%16 == 15 {
			drain()
		}
	}
	drain()
	return keys
}

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
		stored                   atomic.Int64
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
			if respond(keys[i], 1, []byte(sent[i]), ud[i]) { // what the delegate read from the system
				stored.Add(1)
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

	if stored.Load() != nResp {
		t.Fatalf("%d of %d replies stored", stored.Load(), nResp)
	}
	byTicket := map[Ticket]Response{}
	for _, r := range s.responses() {
		if _, dup := byTicket[r.Ticket]; dup {
			t.Fatalf("response %d delivered twice", r.Ticket)
		}
		byTicket[r.Ticket] = Response{Ticket: r.Ticket, Action: r.Action, Reply: bytes.Clone(r.Reply), UserData: bytes.Clone(r.UserData)}
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

// With every slot taken a note goes out plain: no category, no UserData, no
// key, so its click only brings the app forward.
func TestPostWithTableFullHasNoActions(t *testing.T) {
	f, _ := fresh(t, Options{})
	if err := RegisterCategories(chat); err != nil {
		t.Fatal(err)
	}
	fillSlots(t, f, MaxResponses, "")
	r := mustPost(t, Note{ID: "late", Title: "late", Category: "msg", UserData: []byte("x")})
	a := f.lastAdd()
	if r.Reservation != PostedWithoutActions || r.Response != 0 || r.Evicted != "" || a.key != 0 || a.category != "" || a.userData != nil {
		t.Fatalf("257th note: %+v, sent as %+v", r, a)
	}
	if st := ReadStats(); st.WithoutActions != 1 || st.Reserved != MaxResponses {
		t.Fatalf("stats %+v", st)
	}
	if r := mustPost(t, Note{ID: "plain", Title: "plain"}); r.Reservation != NoResponse {
		t.Fatalf("a plain note: %+v", r)
	}
	drain()
}

// With EvictOldest the oldest note holding a slot is removed from
// Notification Center and the new note gets the slot; a response to the
// removed one that arrives anyway finds no record (counted, not stored).
func TestEvictOldest(t *testing.T) {
	f, s := fresh(t, Options{EvictOldest: true})
	keys := fillSlots(t, f, MaxResponses, "")
	r := mustPost(t, Note{ID: "new", Title: "new", UserData: []byte("new")})
	newKey := f.lastAdd().key
	if r.Reservation != Reserved || r.Evicted != "n0" || newKey == 0 {
		t.Fatalf("post with the table full: %+v", r)
	}
	f.mu.Lock()
	removed := strings.Join(f.removed, ",")
	f.mu.Unlock()
	if removed != "n0" {
		t.Fatalf("removed %q, want n0", removed)
	}
	if respond(keys[0], ActionClick, nil, []byte("u0")) {
		t.Fatal("a response to the evicted note was stored with the table full")
	}
	if !respond(newKey, ActionClick, nil, []byte("new")) || !respond(keys[1], 2, nil, []byte("u1")) {
		t.Fatal("responses to notes holding slots were not stored")
	}
	drain()
	got := s.responses()
	if len(got) != 2 || got[0].Ticket != r.Response || string(got[0].UserData) != "new" || string(got[1].UserData) != "u1" || got[1].Action != 2 {
		t.Fatalf("delivered %+v", got)
	}
	if st := ReadStats(); st.Evicted != 1 || st.Lost != 1 || st.Stored != 2 || st.Reserved != MaxResponses-2 {
		t.Fatalf("stats %+v", st)
	}
}

// A slot is freed when its response is delivered (not before), and at once
// by Remove, RemoveThread, a note replacing it, and a post the system
// refused.
func TestSlotsAreFreed(t *testing.T) {
	f, s := fresh(t, Options{})
	keys := fillSlots(t, f, MaxResponses, "")

	// Delivered: the record is the slot until the app has the response.
	if !respond(keys[5], ActionClick, nil, []byte("u5")) {
		t.Fatal("response not stored")
	}
	if r := mustPost(t, Note{Title: "x", UserData: []byte("x")}); r.Reservation != PostedWithoutActions {
		t.Fatalf("a slot was free before the response was delivered: %+v", r)
	}
	drain()
	if r := mustPost(t, Note{ID: "after", Title: "x", UserData: []byte("x")}); r.Reservation != Reserved {
		t.Fatalf("no slot after the response was delivered: %+v", r)
	}

	// Remove frees at once; a response that raced the removal is still
	// delivered when a record is free.
	before := slots.responses.Outstanding()
	if err := Remove("n7", "n8"); err != nil {
		t.Fatal(err)
	}
	if n := slots.responses.Outstanding(); n != before-2 {
		t.Fatalf("Remove: %d slots held, want %d", n, before-2)
	}
	if !respond(keys[7], ActionClick, nil, []byte("u7")) {
		t.Fatal("a response racing Remove was not stored with a record free")
	}
	drain()

	// A note posted with the ID of one shown replaces it: one slot.
	before = slots.responses.Outstanding()
	mustPost(t, Note{ID: "n9", Title: "again", UserData: []byte("u9b")})
	newKey := f.lastAdd().key
	if n := slots.responses.Outstanding(); n != before {
		t.Fatalf("replacing a note: %d slots held, want %d", n, before)
	}
	if !respond(keys[9], ActionClick, nil, []byte("u9")) { // late, for the replaced note: a free record takes it
		t.Fatal("a late response to the replaced note was not stored with a record free")
	}
	drain()
	if !respond(newKey, ActionClick, nil, []byte("u9b")) {
		t.Fatal("the replacing note's response was not stored")
	}
	drain()

	// A post the system refuses frees its slot and says why.
	f.mu.Lock()
	f.addCode = 1
	f.mu.Unlock()
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
	res := s.results()
	if len(res) != 1 || res[0].Kind != ResultPost || res[0].Ticket != r.Request || res[0].ErrCode != 1 || res[0].Err != "not allowed" {
		t.Fatalf("result of the refused post: %+v", res)
	}
	f.mu.Lock()
	f.addCode = 0
	f.mu.Unlock()
}

// RemoveThread frees the thread's slots at once and removes the thread's
// notes the system lists (plain ones, earlier runs') delivered before the
// call; a note delivered after it, or of another thread, stays.
func TestRemoveThread(t *testing.T) {
	f, _ := fresh(t, Options{})
	keys := fillSlots(t, f, 3, "t1")
	mustPost(t, Note{ID: "o", Thread: "t2", UserData: []byte("o")})
	held := slots.responses.Outstanding()
	t0 := time.Now().UnixNano()
	if err := RemoveThread("t1"); err != nil {
		t.Fatal(err)
	}
	if n := slots.responses.Outstanding(); n != held-3 {
		t.Fatalf("%d slots held after RemoveThread, want %d", n, held-3)
	}
	f.mu.Lock()
	early := strings.Join(f.removed, ",")
	f.removed = nil
	f.mu.Unlock()
	if early != "n0,n1,n2" {
		t.Fatalf("removed at once: %q", early)
	}
	listed(f.takeList(t), []deliveredNote{
		{id: "n0", thread: "t1", key: keys[0], date: t0 - 5e9},
		{id: "plain", thread: "t1", date: t0 - 1e9},
		{id: "earlier-run", thread: "t1", key: 42, date: t0 - 9e9},
		{id: "o", thread: "t2", key: f.lastAdd().key, date: t0 - 1e9},
		{id: "newer", thread: "t1", date: t0 + 60e9},
	})
	f.mu.Lock()
	late := strings.Join(f.removed, ",")
	f.mu.Unlock()
	if late != "n0,plain,earlier-run" {
		t.Fatalf("removed from the listing: %q", late)
	}
	if n := slots.responses.Outstanding(); n != held-3 {
		t.Fatalf("%d slots held, want %d", n, held-3)
	}
	drain()
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

	f, s := relaunch(t, Options{})
	// The click that launched the app, on a note of an earlier run, arrives
	// before the listing: delivered through a free record.
	clickedKey := uint64(7) << 40
	if !respond(clickedKey, ActionClick, nil, []byte("launch")) {
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
	listed(f.takeList(t), list)
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
	s.reset()
	// A restored note's response is delivered with its UserData (the
	// delegate reads it from the note); an overflowed one's finds no record.
	if !respond(keyOf(n-1), 1, []byte("hi"), []byte("old-ud")) {
		t.Fatal("response to a restored note not stored")
	}
	if respond(keyOf(0), ActionClick, nil, nil) {
		t.Fatal("response to an overflowed note stored with the table full")
	}
	if !respond(earlyKey, ActionClick, nil, []byte("e")) {
		t.Fatal("the early note lost its slot")
	}
	drain()
	got := s.responses()
	if len(got) != 2 || string(got[0].Reply) != "hi" || string(got[0].UserData) != "old-ud" || got[1].Ticket != early.Response {
		t.Fatalf("delivered %+v", got)
	}
	if st := ReadStats(); st.Lost != 1 || st.Reserved != MaxResponses-2 {
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
		if !respond(keys[i], 1, reply, ud) {
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
	if res := decodeResult(5, int64(ResultPost), 1, []byte("denied")); res.ErrCode != 1 || res.Err != "denied" {
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
		"gnui.action.9999999999999999999": ActionOther,
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
