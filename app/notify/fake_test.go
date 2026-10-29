package notify

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gonativeui/platform"
)

// The service against a fake notification center: everything above the
// native layer (the slots, the requests, the delivery through the
// application object's dispatcher, which the test runs itself) is the real
// code. The delegate's part is respond, which the native delegate calls with
// what it read from the system's response.
//
// The fake keeps what the system would have (shown: a note sent replaces the
// one with its ID, a removal deletes it) and answers listings from it at
// once, unless manual, when the test answers them (takeList).

type fakeNote struct {
	id, category, thread, att string
	key                       uint64
	userData                  []byte
	ctx                       int
	date                      int64
}

type fakeList struct {
	ctx     int
	pending bool // the notes not shown yet (else those shown)
}

type fakeCenter struct {
	mu          sync.Mutex
	adds        []fakeNote // sent, in order
	ops         []string   // the native changes in order: "add <id> <key>", "remove <id>"
	removed     []string
	shown       map[string]fakeNote
	cats        []Category
	lists       []fakeList // listings asked for and not answered (manual)
	nLists      int        // listings asked for
	manual      bool
	auths       []Ticket
	settings_   []Ticket
	addCode     int64                    // the result the system gives a post (0: taken)
	syncAdd     bool                     // the post's result is given inside send (else answerAdds)
	unanswered  []fakeNote               // sent without a result yet
	prepareErr  error                    // prepare fails with it
	prepareHook func(n *Note, id string) // runs in prepare (tests: a post paused there)
	discarded   int
	isLaunched  bool
}

func newFake(manual bool) *fakeCenter {
	return &fakeCenter{syncAdd: true, manual: manual, shown: map[string]fakeNote{}}
}

func (f *fakeCenter) supported() bool { return true }
func (f *fakeCenter) available() bool { return true }
func (f *fakeCenter) launched() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.isLaunched
}
func (f *fakeCenter) start() error { return nil }
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

func (f *fakeCenter) prepare(n *Note, id string, key uint64, att string, ctx int) (prepared, error) {
	f.mu.Lock()
	hook, err := f.prepareHook, f.prepareErr
	f.mu.Unlock()
	if hook != nil {
		hook(n, id)
	}
	if err != nil {
		return nil, err
	}
	a := &fakeNote{id: id, thread: n.Thread, key: key, att: att, ctx: ctx}
	if key != 0 { // what the native center sends: no category or UserData without a key
		a.category, a.userData = n.Category, append([]byte(nil), n.UserData...)
	}
	return a, nil
}

func (f *fakeCenter) send(p prepared) {
	a := *p.(*fakeNote)
	a.date = time.Now().UnixNano()
	f.mu.Lock()
	f.adds = append(f.adds, a)
	f.ops = append(f.ops, fmt.Sprintf("add %s %d", a.id, a.key))
	now := f.syncAdd
	if !now {
		f.unanswered = append(f.unanswered, a)
	}
	f.mu.Unlock()
	if now {
		f.answerAdd(a)
	}
}

func (f *fakeCenter) answerAdd(a fakeNote) {
	f.mu.Lock()
	code := f.addCode
	if code == 0 {
		f.shown[a.id] = a
	}
	f.mu.Unlock()
	var msg []byte
	if code != 0 {
		msg = []byte("not allowed")
	}
	added(a.ctx, code, msg)
}

// answerAdds gives the results of the posts sent with syncAdd off.
func (f *fakeCenter) answerAdds() {
	f.mu.Lock()
	adds := f.unanswered
	f.unanswered = nil
	f.mu.Unlock()
	for _, a := range adds {
		f.answerAdd(a)
	}
}

func (f *fakeCenter) discard(prepared) {
	f.mu.Lock()
	f.discarded++
	f.mu.Unlock()
}

func (f *fakeCenter) remove(ids []string) {
	f.mu.Lock()
	for _, id := range ids {
		f.ops = append(f.ops, "remove "+id)
		delete(f.shown, id)
	}
	f.removed = append(f.removed, ids...)
	f.mu.Unlock()
}

func (f *fakeCenter) pending(ctx int) error   { return f.list(ctx, true) }
func (f *fakeCenter) delivered(ctx int) error { return f.list(ctx, false) }

func (f *fakeCenter) list(ctx int, pending bool) error {
	f.mu.Lock()
	f.nLists++
	if f.manual {
		f.lists = append(f.lists, fakeList{ctx, pending})
		f.mu.Unlock()
		return nil
	}
	var list []deliveredNote
	if !pending {
		list = f.shownLocked()
	}
	f.mu.Unlock()
	listed(ctx, list)
	return nil
}

// shownList is what the fake system shows, oldest first.
func (f *fakeCenter) shownList() []deliveredNote {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.shownLocked()
}

func (f *fakeCenter) shownLocked() []deliveredNote {
	var list []deliveredNote
	for _, a := range f.shown {
		list = append(list, deliveredNote{id: a.id, thread: a.thread, key: a.key, date: a.date})
	}
	slices.SortFunc(list, func(a, b deliveredNote) int { return cmp.Compare(a.date, b.date) })
	return list
}

// clear is the user clearing a note from Notification Center.
func (f *fakeCenter) clear(ids ...string) {
	f.mu.Lock()
	for _, id := range ids {
		delete(f.shown, id)
	}
	f.mu.Unlock()
}

func (f *fakeCenter) lastAdd() fakeNote {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.adds[len(f.adds)-1]
}

func (f *fakeCenter) addsOf(id string) []fakeNote {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakeNote
	for _, a := range f.adds {
		if a.id == id {
			out = append(out, a)
		}
	}
	return out
}

func (f *fakeCenter) opsString() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.ops, ", ")
}

func (f *fakeCenter) set(fn func(f *fakeCenter)) {
	f.mu.Lock()
	fn(f)
	f.mu.Unlock()
}

// takeList takes the oldest listing asked for and not answered (manual).
func (f *fakeCenter) takeList(t *testing.T) fakeList {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.lists) == 0 {
		t.Fatal("no listing was asked for")
	}
	l := f.lists[0]
	f.lists = f.lists[1:]
	return l
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

// responses are the responses delivered, decoded (and copied).
func (s *sink) responses() []Response {
	var out []Response
	for i, e := range s.evs {
		if e.Kind == platform.AppEvService && e.Source == platform.SourceNotifyResponse {
			r := decodeResponse(e.ID, e.A, e.B, s.dataOf(i))
			r.Reply, r.UserData = slices.Clone(r.Reply), slices.Clone(r.UserData)
			out = append(out, r)
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

// resultOf is the delivered result with ticket tk.
func (s *sink) resultOf(t *testing.T, tk Ticket) Result {
	t.Helper()
	for _, r := range s.results() {
		if r.Ticket == tk {
			return r
		}
	}
	t.Fatalf("no result for ticket %d (results %+v)", tk, s.results())
	return Result{}
}

// waitResult waits for the result with ticket tk.
func waitResult(t *testing.T, s *sink, tk Ticket) Result {
	t.Helper()
	var res Result
	waitUntil(t, "a result", func() bool {
		for _, r := range s.results() {
			if r.Ticket == tk {
				res = r
				return true
			}
		}
		return false
	})
	return res
}

// shownKey is the key of the note the fake system shows with ID id.
func (f *fakeCenter) shownKey(id string) uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.shown[id].key
}

// removedList is what was removed so far.
func (f *fakeCenter) removedList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.removed)
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

// waitUntil runs the dispatcher until cond holds (or fails after 10 s).
func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		drain()
		time.Sleep(time.Millisecond)
	}
	drain()
}

// fresh sets the service up again over a fake center with options o (a new
// launch), whose listings are answered at once; the launch's has nothing.
func fresh(t *testing.T, o Options) (*fakeCenter, *sink) {
	t.Helper()
	f, s := relaunch(t, o, false)
	drain()
	if r := s.results(); len(r) != 1 || r[0].Kind != ResultRestored || r[0].Restored != 0 {
		t.Fatalf("after Setup: %+v, want one empty ResultRestored", r)
	}
	s.reset()
	if n := slots.responses.Outstanding() + slots.spares.Outstanding(); n != 0 {
		t.Fatalf("%d records held by a fresh service", n)
	}
	return f, s
}

// relaunch sets the service up again over a fake center with options o:
// what an earlier test left is released and delivered, and the counters
// start at 0. With manual set the launch's listing is left to the test.
func relaunch(t *testing.T, o Options, manual bool) (*fakeCenter, *sink) {
	t.Helper()
	f := newFake(manual)
	s := relaunchWith(t, o, f)
	return f, s
}

// relaunchWith is relaunch over the backend b.
func relaunchWith(t *testing.T, o Options, b backend) *sink {
	t.Helper()
	workers.Wait()
	s := &sink{}
	platform.SetAppSink(s)
	t.Cleanup(func() { platform.SetAppSink(nil) })
	parking.mu.Lock()
	left := len(parking.list)
	parking.mu.Unlock()
	if left != 0 {
		t.Fatalf("%d responses parked by an earlier test", left)
	}
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
	svc.setupMu.Lock()
	svc.b = b
	svc.on.Store(false)
	svc.setupMu.Unlock()
	svc.catMu.Lock()
	svc.cats = nil
	svc.catMu.Unlock()
	svc.mu.Lock()
	svc.ids = map[string]*entry{}
	svc.listings, svc.threadCuts, svc.waiting, svc.reconciling = 0, nil, nil, false
	keys.n, keys.spent = 0, false
	svc.mu.Unlock()
	slots.nAnswered.Store(0)
	stageFile, testHookStoring, testHookRestoring = stageAttachment, nil, nil
	for _, c := range []*atomic.Uint64{&slots.withoutActions, &slots.evicted, &slots.stored, &slots.late, &slots.plain, &slots.reconciled, &slots.superseded, &slots.lostAtExit} {
		c.Store(0)
	}
	if err := Setup(o); err != nil {
		t.Fatal(err)
	}
	return s
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

// retrying reports whether a goroutine retries parked responses.
func retrying() bool {
	parking.mu.Lock()
	defer parking.mu.Unlock()
	return parking.running
}

// parkOne parks a response with no record free (every slot's record holds
// a response, every spare too) and counts its answers and give-ups.
func parkOne(t *testing.T, f *fakeCenter, key uint64, ud string, acks, drops *atomic.Int32) {
	t.Helper()
	for i, k := range fillSlots(t, f, MaxResponses, "") {
		if !stored(k, ActionClick, nil, nil) {
			t.Fatal(i)
		}
	}
	for i := range nSpare {
		if !stored(uint64(1)<<40|uint64(i+1), ActionClick, nil, nil) {
			t.Fatal(i)
		}
	}
	p := respond(key, 1, []byte("reply"), []byte(ud))
	if p == nil {
		t.Fatal("stored with every record taken")
	}
	p.ack, p.drop = func() { acks.Add(1) }, func() { drops.Add(1) }
	p.wait()
}

// stored reports whether respond stored the response at once.
func stored(key uint64, action int64, reply, data []byte) bool {
	return respond(key, action, reply, data) == nil
}

// holding reports whether a slot awaits a response to the note with key.
func holding(key uint64) bool {
	svc.mu.Lock()
	defer svc.mu.Unlock()
	i := slotOfKey(key)
	return i >= 0 && slots.s[i].key.Load() == key
}

// slotsOf is the keys of the slots held for notes with ID id.
func slotsOf(id string) []uint64 {
	svc.mu.Lock()
	defer svc.mu.Unlock()
	var out []uint64
	for i := range slots.s {
		if s := &slots.s[i]; s.live && s.id == id && s.key.Load() != 0 {
			out = append(out, s.k)
		}
	}
	return out
}

// gate is a stageFile that waits until released, then copies nothing.
type gate struct {
	entered chan string
	release chan error
}

func newGate() *gate { return &gate{entered: make(chan string, 8), release: make(chan error)} }

func (g *gate) stage(path string) (string, error) {
	g.entered <- path
	if err := <-g.release; err != nil {
		return "", err
	}
	return path + ".copy", nil
}
