package platform

import (
	"errors"
	"math/bits"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// The application object's events and their delivery (DECISIONS D37). The
// app package turns them into app.Event for its Handler; services
// (app/notify, app/instance, the dialogs …) produce them through the delivery
// classes below.
//
// Delivery has four classes, and nothing accepted is lost:
//
//  1. Main-thread native events (reopen, quit requests, opens, activation,
//     sleep and wake, screens, will-terminate, app-wide menu commands) are
//     delivered synchronously from the delegate method that receives them
//     (appobj_darwin.go), except while the app draws a frame (HoldApp): then
//     they wait, in order, for the frame to end.
//  2. Request completions and responses go through Tickets: a record is
//     reserved when the request is made (Tickets.Reserve, ErrBusy when the
//     table is full), and the completion, on any thread, only fills it in
//     (Tickets.Complete). It never needs queue space.
//  3. Outside requests (a second instance's arguments) are offered to a Ring:
//     accepted only if a record and room for the data are free (Ring.Offer),
//     refused otherwise, so the sender can answer "busy".
//  4. Observations (levels, positions, thermal state, the locale) are one
//     latest value per source (Observation.Set): coalesced, never queued, the
//     only class where a value is replaced unseen (counted).
//
// The app's own path from its goroutines (app.Post) is a ring of its own
// (PostApp): allocation-free, never waiting, refusing when full.
//
// Producers mark their record ready, put a reference to it on one ready
// queue and signal the dispatcher: on macOS a run-loop source in the common
// modes, so it runs with no window, inside AppKit's modal and tracking loops
// and during a pending quit; elsewhere the main loop's Wake. The dispatcher
// (DispatchApp) runs on the main thread, delivers in the order the records
// became ready, and then runs the app's safe-point work. The ready queue can
// never be full: it has a cell for every record of every table, and a record
// is on it at most once.

// AppEventKind identifies an AppEvent.
type AppEventKind uint8

const (
	// AppEvNone is no event.
	AppEvNone AppEventKind = iota
	// AppEvReopen: the Dock icon was clicked (or the app opened again) while
	// running. A is 1 when the app has a visible window.
	AppEvReopen
	// AppEvActivated: the app became the active app.
	AppEvActivated
	// AppEvDeactivated: the app stopped being the active app.
	AppEvDeactivated
	// AppEvQuitRequested: something asks the app to quit; A is the QuitReason,
	// ID the request's serial. Answer with ReplyQuit.
	AppEvQuitRequested
	// AppEvQuitDeadline: a quit request stayed unanswered past
	// AppOptions.QuitDeadline; A is the QuitAction applied (an error event).
	AppEvQuitDeadline
	// AppEvOpenURLs: URLs to open; A is the count, the data the URLs,
	// NUL-separated.
	AppEvOpenURLs
	// AppEvOpenFiles: files to open; A is the count, the data the paths,
	// NUL-separated.
	AppEvOpenFiles
	// AppEvWillSleep: the system is going to sleep.
	AppEvWillSleep
	// AppEvDidWake: the system woke from sleep.
	AppEvDidWake
	// AppEvScreensChanged: screens were added, removed or rearranged, or
	// their resolution changed.
	AppEvScreensChanged
	// AppEvSessionActive: the user session became active (A = 1) or inactive
	// (A = 0, fast user switching).
	AppEvSessionActive
	// AppEvWillTerminate: the app is about to exit.
	AppEvWillTerminate
	// AppEvCommand: an app-wide menu command (no window to take it, or the Dock
	// menu); ID is the command id.
	AppEvCommand
	// AppEvService: a result, response, outside request or change from a
	// service; Source says which, ID is its ticket or sequence number.
	AppEvService
	// AppEvPost: an event the app posted from one of its goroutines (PostApp).
	AppEvPost
)

// AppEvent is one application event: a fixed-size, pointer-free record. Its
// data, if any, is passed beside it and valid during the delivery only.
type AppEvent struct {
	// Kind is the event's kind.
	Kind AppEventKind
	// Source is the service of an AppEvService (Source constants).
	Source Source
	// Window is the window the event concerns, 0 for none.
	Window uint32
	// ID is the ticket, request id, command id or serial, by kind.
	ID uint64
	// A and B are two integers whose meaning depends on the kind and source.
	A, B int64
}

// Source identifies the producer of an AppEvService. Services take a
// constant here; SourceApp and up are free for an app's own producers.
type Source uint16

const (
	// SourceNone is no source.
	SourceNone Source = iota
	// SourcePost is app.Post (the app's own events).
	SourcePost
	// SourceDialog is alert and file panel results (N2).
	SourceDialog
	// SourceNotify is notification requests' results (permission, post).
	SourceNotify
	// SourceNotifyResponse is a notification's click, action or reply.
	SourceNotifyResponse
	// SourceInstance is a second instance's request (app/instance).
	SourceInstance
	// SourceLocale is a locale change (app/locale).
	SourceLocale
	// SourcePower is power and screen-lock changes (app/power).
	SourcePower
	// SourcePerm is permission answers (app/perm).
	SourcePerm
	// SourceMedia is media events (playback position, end, levels).
	SourceMedia
	// SourceTask is task pools that report to the app object.
	SourceTask
	// SourceApp is the first source free for the app's own producers.
	SourceApp Source = 256
)

// QuitReason says where a quit request came from (AppEvQuitRequested's A).
type QuitReason int64

const (
	// QuitMenu is the Quit menu item or ⌘Q.
	QuitMenu QuitReason = iota + 1
	// QuitDock is Quit from the Dock or another app (an Apple event).
	QuitDock
	// QuitLogout is a logout, restart or shutdown: answer promptly.
	QuitLogout
	// QuitUpdate is an updater that needs the app to quit.
	QuitUpdate
	// QuitSignal is SIGTERM or SIGINT.
	QuitSignal
	// QuitApp is the app's own request (app.App.RequestQuit).
	QuitApp
	// QuitOther is any other caller of NSApplication terminate:.
	QuitOther
)

// QuitAction is what happens to a quit request still unanswered at its
// deadline (AppOptions.QuitDeadlineAction).
type QuitAction int64

const (
	// QuitCancel cancels the quit (the default).
	QuitCancel QuitAction = iota
	// QuitAllow lets the app quit (force-quit), only by the app's choice.
	QuitAllow
)

// Policy is the application's activation policy.
type Policy uint8

const (
	// PolicyRegular: a Dock icon and a menu bar (the default).
	PolicyRegular Policy = iota
	// PolicyAccessory: no Dock icon; the app may open windows (a menu bar
	// extra).
	PolicyAccessory
	// PolicyProhibited: no Dock icon and no windows of its own.
	PolicyProhibited
)

// AppOptions configure the application object (StartApp).
type AppOptions struct {
	// Policy is the activation policy.
	Policy Policy
	// QuitDeadline, when not 0, bounds how long a quit request may stay
	// unanswered: past it QuitDeadlineAction is applied and
	// AppEvQuitDeadline delivered.
	QuitDeadline       time.Duration
	QuitDeadlineAction QuitAction
	// Signals makes SIGTERM and SIGINT quit requests (QuitSignal) instead of
	// ending the process at once; a second one while the first is unanswered
	// ends it.
	Signals bool
	// WindowTabbing lets macOS group the app's windows into tabs (its
	// automatic window tabbing, the View menu's tab items); off by default.
	WindowTabbing bool
}

// AppSink receives the application object's events, on the main thread. The
// app package implements it.
type AppSink interface {
	// AppEvent delivers one event; data is valid during the call only.
	AppEvent(e *AppEvent, data []byte)
	// AppSafePoint runs the deferred safe-point work. The dispatcher calls it
	// after delivering events, also while the app's own loop is suspended in a
	// native modal or tracking loop. It must not start a native loop itself.
	AppSafePoint()
}

// ErrBusy is returned by Tickets.Reserve when the table is full: the request
// fails at once, and nothing that was accepted is lost.
var ErrBusy = errors.New("platform: busy (all completion records are reserved)")

// Delivery sizes.
const (
	// MaxTickets is the completion records a service's table has by default
	// (64: alerts, panels, permission answers, notification post results).
	MaxTickets = 64
	// MaxNotifyResponses is the response slots of actionable notifications.
	MaxNotifyResponses = 256
	// MaxNotifyReply is the reply text a response slot holds.
	MaxNotifyReply = 4 << 10
	// RingRecords and RingBytes are an outside-request ring's records and
	// data budget (app/instance).
	RingRecords = 32
	RingBytes   = 16 << 10
	// MaxPosted is how many app.Post events can wait at once, and
	// MaxPostData the data each may carry.
	MaxPosted   = 256
	MaxPostData = 512
	// readyCells bounds the records of all tables together: every record has
	// a cell on the ready queue.
	readyCells = 4096
	maxTables  = 256
)

// ---- Records and tables ----

// A record's state word: the generation (high 32 bits) and the state.
const (
	stFree     = 0
	stReserved = 1 // a ticket holds it
	stWriting  = 2 // a producer is filling it in
	stReady    = 3 // on the ready queue
	stDropped  = 4 // on the ready queue, cancelled: freed without delivery
)

type record struct {
	word atomic.Uint64
	ev   AppEvent
	data []byte
	cost int64 // ring: the bytes it holds of the budget
	kept int   // ring: the capacity of data counted in recTable.kept (main thread)
}

type tableKind uint8

const (
	kindTickets tableKind = iota
	kindRing
	kindPost
	kindObservation
)

type recTable struct {
	id      uint16
	kind    tableKind
	source  Source
	recs    []record
	free    []atomic.Uint64 // a set bit is a free record
	maxData int             // data a record may hold without growing (Post: a hard limit)
	budget  int64           // ring: bytes of data at once
	used    atomic.Int64
	kept    int           // ring: data capacity the records keep between uses, at most budget (main thread)
	fifo    atomic.Uint64 // ring: the next sequence number

	// observation
	queued    atomic.Bool
	seq       atomic.Uint64 // seqlock: odd while a writer writes a, b; seq/2 is the values set
	oa, ob    atomic.Int64
	lastVer   uint64 // the version last delivered (main thread)
	coalesced atomic.Uint64
	refused   atomic.Uint64
	accepted  atomic.Uint64
	retired   atomic.Uint64 // tickets: records retired at lastGen
}

var tables struct {
	mu    sync.Mutex
	all   [maxTables]atomic.Pointer[recTable]
	n     int
	cells int
}

func newTable(kind tableKind, src Source, n, maxData int) *recTable {
	t := &recTable{kind: kind, source: src, recs: make([]record, n), maxData: maxData}
	t.free = make([]atomic.Uint64, (n+63)/64)
	for i := 0; i < n; i++ {
		t.free[i/64].Or(1 << (i % 64))
		if maxData > 0 {
			t.recs[i].data = make([]byte, 0, maxData)
		}
	}
	tables.mu.Lock()
	defer tables.mu.Unlock()
	if tables.n == maxTables || tables.cells+n > readyCells {
		panic("platform: too many delivery records (raise readyCells)")
	}
	t.id = uint16(tables.n)
	tables.cells += n
	tables.all[tables.n].Store(t)
	tables.n++
	return t
}

// alloc takes a free record, or reports false.
func (t *recTable) alloc() (int, bool) {
	for w := range t.free {
		for {
			v := t.free[w].Load()
			if v == 0 {
				break
			}
			b := bits.TrailingZeros64(v)
			if t.free[w].CompareAndSwap(v, v&^(1<<b)) {
				return w*64 + b, true
			}
		}
	}
	return -1, false
}

func (t *recTable) release(i int) { t.free[i/64].Or(1 << (i % 64)) }

// fill copies the payload into record i (its state is stWriting) and makes
// it ready.
func (t *recTable) fill(i int, gen uint32, e AppEvent, data []byte) {
	r := &t.recs[i]
	r.ev = e
	r.data = append(r.data[:0], data...)
	r.word.Store(uint64(gen)<<32 | stReady)
	ready.push(uint32(t.id)<<16 | uint32(i))
	signalApp()
}

func nextGen(w uint64) uint32 {
	g := uint32(w>>32) + 1
	if g == 0 {
		g = 1
	}
	return g
}

// lastGen is a ticket record's last generation: a record that reaches it is
// retired instead of freed, so a Ticket (which carries the generation) never
// repeats; the table has one record fewer from then on.
const lastGen = ^uint32(0)

// freeOrRetire frees record i after its use with generation gen; a ticket
// record at lastGen is retired.
func (t *recTable) freeOrRetire(i int, gen uint32) {
	if t.kind == kindTickets && gen == lastGen {
		t.retired.Add(1)
		return
	}
	t.release(i)
}

// ---- Class 2: tickets ----

// Tickets is a service's table of completion records (class 2): every
// request reserves one before it is made, so its completion, on whatever
// thread, never needs room and cannot be lost.
type Tickets struct{ t *recTable }

// Ticket identifies one reservation; it is the ID of the AppEvService that
// completes it. The zero Ticket is not a reservation. A ticket never repeats
// for the life of the process: a record's generation advances on every use,
// and a record whose generations are spent is retired, not reused.
type Ticket uint64

// NewTickets makes a table of n records for src whose completions carry up to
// dataCap bytes without allocating (more grows the record once). Setup only.
func NewTickets(src Source, n, dataCap int) *Tickets {
	return &Tickets{newTable(kindTickets, src, n, dataCap)}
}

// Reserve takes a record for a request about to be made, for window win (0:
// none). It fails at once with ErrBusy when every record is taken. Any
// goroutine; it allocates nothing.
func (k *Tickets) Reserve(win uint32) (Ticket, error) {
	t := k.t
	i, ok := t.alloc()
	if !ok {
		t.refused.Add(1)
		return 0, ErrBusy
	}
	r := &t.recs[i]
	gen := nextGen(r.word.Load())
	r.ev.Window = win
	r.word.Store(uint64(gen)<<32 | stReserved)
	return Ticket(uint64(t.id)<<48 | uint64(gen)<<16 | uint64(i)), nil
}

func (k *Tickets) record(tk Ticket) (*record, int, uint32, bool) {
	t := k.t
	i := int(tk & 0xFFFF)
	if uint16(tk>>48) != t.id || i >= len(t.recs) {
		return nil, 0, 0, false
	}
	return &t.recs[i], i, uint32(tk >> 16), true
}

// Complete stores the result of ticket tk (a, b and a copy of data) and
// queues its delivery as an AppEvService with ID tk. Any thread, including
// a foreign one inside a callback; it allocates only when data outgrows the
// record. It reports false for a ticket that is not reserved any more
// (completed, or cancelled): nothing is stored then.
func (k *Tickets) Complete(tk Ticket, a, b int64, data []byte) bool {
	if !enterGate() {
		return false
	}
	defer leaveGate()
	r, i, gen, ok := k.record(tk)
	if !ok || !r.word.CompareAndSwap(uint64(gen)<<32|stReserved, uint64(gen)<<32|stWriting) {
		return false
	}
	k.t.accepted.Add(1)
	k.t.fill(i, gen, AppEvent{Kind: AppEvService, Source: k.t.source, Window: r.ev.Window, ID: uint64(tk), A: a, B: b}, data)
	return true
}

// Cancel withdraws ticket tk: no event is delivered for it, and its record
// is free again (at once if not completed, after the dispatcher skips it if
// already completed). It reports whether the ticket was outstanding.
func (k *Tickets) Cancel(tk Ticket) bool {
	r, i, gen, ok := k.record(tk)
	if !ok {
		return false
	}
	if r.word.CompareAndSwap(uint64(gen)<<32|stReserved, uint64(gen)<<32|stFree) {
		k.t.freeOrRetire(i, gen)
		return true
	}
	return r.word.CompareAndSwap(uint64(gen)<<32|stReady, uint64(gen)<<32|stDropped)
}

// Outstanding is how many records are reserved or waiting for delivery.
func (k *Tickets) Outstanding() int {
	n := len(k.t.recs)
	for w := range k.t.free {
		n -= bits.OnesCount64(k.t.free[w].Load())
	}
	return n
}

// Refused is how many Reserve calls failed with ErrBusy.
func (k *Tickets) Refused() uint64 { return k.t.refused.Load() }

// Retired is how many records were retired because their generations were
// spent (about 4·10⁹ uses each).
func (k *Tickets) Retired() uint64 { return k.t.retired.Load() }

// ---- Class 3: outside requests ----

// Ring takes requests from outside the process (class 3): one is accepted
// only if a record and room for its data are free, so a refusal can be
// answered "busy" to the sender instead of losing what was accepted.
type Ring struct{ t *recTable }

// NewRing makes a ring of n records sharing budget bytes of data for src.
// Setup only.
func NewRing(src Source, n, budget int) *Ring {
	t := newTable(kindRing, src, n, 0)
	t.budget = int64(budget)
	return &Ring{t}
}

// Offer accepts a request (a, b and a copy of data) and queues its delivery
// as an AppEvService whose ID is a sequence number, or refuses it (false)
// when the ring is full. Any goroutine. Accepted data allocates the first
// time a record holds that much; the records keep their buffers between uses
// up to the ring's byte budget in all, so a steady flow does not allocate.
func (r *Ring) Offer(a, b int64, data []byte) bool {
	t := r.t
	if !enterGate() {
		t.refused.Add(1)
		return false
	}
	defer leaveGate()
	n := int64(len(data))
	for {
		u := t.used.Load()
		if u+n > t.budget {
			t.refused.Add(1)
			return false
		}
		if t.used.CompareAndSwap(u, u+n) {
			break
		}
	}
	i, ok := t.alloc()
	if !ok {
		t.used.Add(-n)
		t.refused.Add(1)
		return false
	}
	rec := &t.recs[i]
	gen := nextGen(rec.word.Load())
	rec.word.Store(uint64(gen)<<32 | stWriting)
	rec.cost = n
	t.accepted.Add(1)
	t.fill(i, gen, AppEvent{Kind: AppEvService, Source: t.source, ID: t.fifo.Add(1), A: a, B: b}, data)
	return true
}

// Accepted and Refused count the offers.
func (r *Ring) Accepted() uint64 { return r.t.accepted.Load() }

// Refused counts the offers refused because the ring was full.
func (r *Ring) Refused() uint64 { return r.t.refused.Load() }

// ---- Class 4: observations ----

// Observation is one source's latest value (class 4): Set replaces it, and
// the dispatcher delivers the value it finds. A value replaced before it was
// delivered is lost by design and counted (Coalesced).
type Observation struct{ t *recTable }

// NewObservation makes an observation for src. Setup only.
func NewObservation(src Source) *Observation {
	return &Observation{newTable(kindObservation, src, 1, 0)}
}

// Set makes a, b the latest value and queues its delivery as an AppEvService
// unless one is queued already. Any goroutine; it allocates nothing.
func (o *Observation) Set(a, b int64) {
	t := o.t
	if !enterGate() {
		return
	}
	defer leaveGate()
	for { // seqlock writer: one writer at a time
		s := t.seq.Load()
		if s&1 == 0 && t.seq.CompareAndSwap(s, s+1) {
			break
		}
		runtime.Gosched()
	}
	t.oa.Store(a)
	t.ob.Store(b)
	t.seq.Add(1)
	t.accepted.Add(1)
	if testHookObservationSet != nil {
		testHookObservationSet()
	}
	// Queued already: the dispatcher reads the latest value when it gets to
	// it. A value is delivered once, by version, however many queue entries
	// find it, and the values it never saw are counted there (deliverRef).
	if t.queued.CompareAndSwap(false, true) {
		ready.push(uint32(t.id) << 16)
		signalApp()
	}
}

// testHookObservationSet runs inside Set between publishing the value and
// queueing it (tests: a controlled interleaving with the dispatcher).
var testHookObservationSet func()

// Coalesced counts the values replaced before they were delivered, as the
// dispatcher found them (a value not delivered yet is not counted yet).
func (o *Observation) Coalesced() uint64 { return o.t.coalesced.Load() }

// Sets counts the values set.
func (o *Observation) Sets() uint64 { return o.t.accepted.Load() }

// observed reads the latest value and its version (the values set so far).
func (t *recTable) observed() (a, b int64, ver uint64) {
	for {
		s := t.seq.Load()
		if s&1 != 0 {
			runtime.Gosched()
			continue
		}
		a, b = t.oa.Load(), t.ob.Load()
		if t.seq.Load() == s {
			return a, b, s / 2
		}
	}
}

// ---- The app's own posts ----

var posts struct {
	once sync.Once
	t    *recTable
}

func postTable() *recTable {
	posts.once.Do(func() { posts.t = newTable(kindPost, SourcePost, MaxPosted, MaxPostData) })
	return posts.t
}

// PreparePosts makes PostApp's storage, so that no post allocates or waits
// for it to be made. The app package calls it before posting is possible.
func PreparePosts() { postTable() }

// PostApp queues e (its Kind becomes AppEvPost) with a copy of data for
// delivery to the app, from any goroutine. It never waits and allocates
// nothing; it reports false, keeping nothing, when MaxPosted posts are
// waiting or data is longer than MaxPostData. The caller keeps the event and
// tries again later.
func PostApp(e AppEvent, data []byte) bool {
	t := postTable()
	if !enterGate() {
		t.refused.Add(1)
		return false
	}
	defer leaveGate()
	if len(data) > t.maxData {
		t.refused.Add(1)
		return false
	}
	i, ok := t.alloc()
	if !ok {
		t.refused.Add(1)
		return false
	}
	r := &t.recs[i]
	gen := nextGen(r.word.Load())
	r.word.Store(uint64(gen)<<32 | stWriting)
	e.Kind = AppEvPost
	t.accepted.Add(1)
	t.fill(i, gen, e, data)
	return true
}

// PostStats reports the posts accepted and refused so far.
func PostStats() (accepted, refused uint64) {
	t := postTable()
	return t.accepted.Load(), t.refused.Load()
}

// ---- The ready queue ----

// readyQueue is a bounded multi-producer queue of record references
// (table<<16 | record) with one consumer, the dispatcher (Vyukov's bounded
// queue). It cannot be full: it has a cell for every record.
type readyQueue struct {
	cells [readyCells]struct {
		seq atomic.Uint64
		ref atomic.Uint32
	}
	enq atomic.Uint64
	deq uint64 // the dispatcher's
}

var ready = func() *readyQueue {
	q := new(readyQueue)
	for i := range q.cells {
		q.cells[i].seq.Store(uint64(i))
	}
	return q
}()

func (q *readyQueue) push(ref uint32) {
	for {
		pos := q.enq.Load()
		c := &q.cells[pos%readyCells]
		switch seq := c.seq.Load(); {
		case seq == pos:
			if q.enq.CompareAndSwap(pos, pos+1) {
				if testHookReadyClaimed != nil {
					testHookReadyClaimed()
				}
				c.ref.Store(ref)
				c.seq.Store(pos + 1)
				return
			}
		case seq < pos:
			panic("platform: the ready queue is full") // a record queued twice
		}
	}
}

// pop takes the oldest reference; false when there is none (or the oldest is
// still being written: its producer signals again when it is done).
func (q *readyQueue) pop() (uint32, bool) {
	c := &q.cells[q.deq%readyCells]
	if c.seq.Load() != q.deq+1 {
		return 0, false
	}
	ref := c.ref.Load()
	c.seq.Store(q.deq + readyCells)
	q.deq++
	return ref, true
}

func (q *readyQueue) pending() uint64 { return q.enq.Load() - q.deq }

// headReady reports whether the oldest reference is published: a producer
// that has claimed its cell but not written it yet signals when it has.
func (q *readyQueue) headReady() bool {
	return q.cells[q.deq%readyCells].seq.Load() == q.deq+1
}

// testHookReadyClaimed runs inside push between claiming a cell and
// publishing it (tests: a producer paused there).
var testHookReadyClaimed func()

// gate is the producers' shutdown boundary (FlushApp). Every producer enters
// before it claims a record and leaves once its reference is published; once
// the gate is closed, producers refuse what they are given (PostApp and
// Offer report false, a completion is not stored), and the last drain waits
// for the producers inside to leave. Setting a sink opens it again.
var gate struct {
	closed   atomic.Bool
	inflight atomic.Int64
}

func enterGate() bool {
	gate.inflight.Add(1)
	if gate.closed.Load() {
		gate.inflight.Add(-1)
		return false
	}
	return true
}

func leaveGate() { gate.inflight.Add(-1) }

// flushWait bounds how long the last drain waits for a producer inside the
// gate (one preempted between its claim and its publication).
const flushWait = 2 * time.Second

// ---- The dispatcher ----

var dispatch struct {
	sink      AppSink
	signalled atomic.Bool
	running   bool   // DispatchApp is on the stack (main thread)
	held      bool   // HoldApp: the app is drawing a frame (main thread)
	missed    bool   // a nested or held DispatchApp returned early
	turns     uint64 // DispatchApp calls that got past the guards (tests)
	delivered uint64
	quitDue   atomic.Uint64 // a quit deadline fired for this request serial
	quitAsked atomic.Int64  // a QuitReason requested from a goroutine (RequestQuit, signals)
	ev        [8]AppEvent   // the events being delivered, by nesting depth
	depth     int
	dropped   uint64 // events found with no sink
}

// SetAppSink makes s the receiver of the application object's events. Main
// thread; nil detaches (events then wait on their records). A receiver opens
// the producers' gate again (a new run after an earlier one's last drain).
func SetAppSink(s AppSink) {
	dispatch.sink = s
	if s != nil {
		gate.closed.Store(false)
	}
}

// signalApp tells the dispatcher there is work, from any thread: the first
// signal after a dispatch reaches the OS, the others coalesce.
func signalApp() {
	if dispatch.signalled.CompareAndSwap(false, true) {
		osSignalApp()
	}
}

// DispatchApp delivers the ready events, in the order they became ready, then
// runs the app's safe-point work. Main thread. The run-loop source calls it
// on macOS; the app's loop calls it after every Wait on every OS. It is not
// re-entered: a nested call (a handler that spun a native loop) returns at
// once and the outer call signals again before it returns. While HoldApp
// holds it, it returns at once too, and the release signals again.
func DispatchApp() {
	if dispatch.running || dispatch.held {
		// The signal this call consumed is owed: cleared, so that the outer
		// call's or the release's signalApp sends it again.
		dispatch.signalled.Store(false)
		dispatch.missed = true
		return
	}
	dispatch.signalled.Store(false) // before draining: a later producer signals again
	s := dispatch.sink
	if s == nil {
		return
	}
	dispatch.turns++
	dispatch.running = true
	got := deliverReady(s)
	dispatchQuitWork()
	s.AppSafePoint()
	dispatch.running = false
	// Signalled again only for work this call can do: an unpublished head is
	// its producer's, which signals once it has written it (a signal now
	// would only spin on it).
	if dispatch.missed || len(natives.evs) > 0 || ready.headReady() {
		dispatch.missed = false
		signalApp()
	}
	if got {
		// The app's loop runs a turn: the events' sink asked the windows they
		// changed for their frames (a Wake would hand every window an EvWake,
		// and each would draw).
		WakeLoop()
	}
}

// deliverReady delivers the native events held during a frame, then the
// records ready now (later arrivals get the next turn). It reports whether
// it delivered anything.
func deliverReady(s AppSink) bool {
	got := len(natives.evs) > 0
	for i := 0; i < len(natives.evs); i++ {
		ne := natives.evs[i]
		deliverApp(s, &ne.ev, natives.data[ne.off:ne.off+ne.n])
	}
	natives.evs, natives.data = natives.evs[:0], natives.data[:0]
	for n := ready.pending(); n > 0; n-- {
		ref, ok := ready.pop()
		if !ok {
			break
		}
		got = deliverRef(s, ref) || got
	}
	return got
}

// natives are the main-thread native events that arrived while a frame held
// dispatch (HoldApp), in order, with their data. Main thread.
var natives struct {
	evs  []nativeEv
	data []byte
}

type nativeEv struct {
	ev     AppEvent
	off, n int
}

// FlushApp is the last delivery, before the process ends or Main returns:
// it closes the producers' gate (Post refuses from then on), waits for the
// producers inside it to publish (at most flushWait) and delivers everything
// they and the others queued, also while a frame holds dispatch, without the
// safe-point work. An unpublished head is not taken for an empty queue: the
// drain ends when no producer is inside and nothing is queued. It does
// nothing inside a delivery (what is queued could not be delivered in
// order). Main thread.
func FlushApp() {
	s := dispatch.sink
	if dispatch.running || s == nil {
		return
	}
	gate.closed.Store(true)
	dispatch.running = true
	deadline := time.Now().Add(flushWait)
	for {
		deliverReady(s)
		if gate.inflight.Load() == 0 && ready.pending() == 0 && len(natives.evs) == 0 {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Microsecond)
	}
	dispatch.running = false
}

// EndApp ends the application object's part before Main returns: the quit
// state is settled (on macOS every suspended quit Apple event is answered, a
// terminate: still waiting is refused, and the open dialogs and sheets end as
// cancelled), then the last drain delivers what was accepted (FlushApp).
// After it nothing more is delivered; the caller sends its final event.
// Main thread.
func EndApp() {
	osEndApp()
	resetQuit()
	FlushApp()
}

// HoldApp holds app-wide dispatch while on: the app is drawing a frame.
// The run-loop source fires in common modes, so it fires in a native loop
// the frame's own code runs (a modal session, an animated zoom), where a
// handler would run inside Build; held, nothing is delivered there, and the
// release dispatches what arrived meanwhile. Main thread.
func HoldApp(on bool) {
	dispatch.held = on
	if !on && (dispatch.missed || len(natives.evs) > 0) {
		dispatch.missed = false
		signalApp()
	}
}

// AppHeld reports whether a frame holds app-wide dispatch now. Main thread.
func AppHeld() bool { return dispatch.held }

// deliverRef delivers the record behind ref and frees it.
func deliverRef(s AppSink, ref uint32) bool {
	t := tables.all[ref>>16].Load()
	i := int(ref & 0xFFFF)
	if t.kind == kindObservation {
		t.queued.Store(false) // before reading: a later Set queues again
		a, b, ver := t.observed()
		if ver == t.lastVer {
			// Delivered already: a Set that published this value before the
			// last delivery read it, and queued it after.
			return false
		}
		if ver > t.lastVer+1 {
			t.coalesced.Add(ver - t.lastVer - 1)
		}
		t.lastVer = ver
		e := AppEvent{Kind: AppEvService, Source: t.source, A: a, B: b}
		deliverApp(s, &e, nil)
		return true
	}
	r := &t.recs[i]
	w := r.word.Load()
	gen := uint32(w >> 32)
	delivered := false
	if w&0xFFFFFFFF == stReady {
		e := r.ev
		deliverApp(s, &e, r.data)
		delivered = true
	}
	if t.kind == kindRing {
		t.used.Add(-r.cost)
		r.cost = 0
		// The buffer is kept while the ring's records keep no more than its
		// budget in all: a steady flow of large requests reuses them.
		if c := cap(r.data); c != r.kept {
			t.kept += c - r.kept
			r.kept = c
			if t.kept > int(t.budget) {
				t.kept -= c
				r.kept = 0
				r.data = nil
			}
		}
	} else if len(r.data) > 0 && cap(r.data) > 4*max(t.maxData, 256) {
		r.data = r.data[:0:0] // a rare large payload: do not keep it
		if t.maxData > 0 {
			r.data = make([]byte, 0, t.maxData)
		}
	}
	r.word.Store(uint64(gen)<<32 | stFree)
	t.freeOrRetire(i, gen)
	return delivered
}

// deliverApp hands one event to the sink, on a copy owned by this nesting
// depth (a handler may cause a synchronous event of its own).
func deliverApp(s AppSink, e *AppEvent, data []byte) {
	if s == nil {
		dispatch.dropped++
		return
	}
	d := dispatch.depth
	if d >= len(dispatch.ev) {
		d = len(dispatch.ev) - 1
	}
	dispatch.ev[d] = *e
	dispatch.depth++
	dispatch.delivered++
	s.AppEvent(&dispatch.ev[d], data)
	dispatch.depth--
}

// deliverNow delivers a main-thread native event synchronously (class 1),
// or, while a frame holds dispatch, keeps it for the frame's end: a native
// loop the frame's own code runs must not run the app's handler inside
// Build. EvWillTerminate is never held (the process ends after it).
func deliverNow(kind AppEventKind, id uint64, a, b int64, data []byte) {
	e := AppEvent{Kind: kind, ID: id, A: a, B: b}
	if dispatch.held && kind != AppEvWillTerminate {
		natives.evs = append(natives.evs, nativeEv{ev: e, off: len(natives.data), n: len(data)})
		natives.data = append(natives.data, data...)
		dispatch.missed = true
		return
	}
	deliverApp(dispatch.sink, &e, data)
}

// DeliveredApp counts the events delivered to the sink so far.
func DeliveredApp() uint64 { return dispatch.delivered }

// ---- The quit policy (shared part) ----

// quitState is the latch of the quit policy: an answer given while the
// request is being delivered is kept and returned by the delegate method;
// only without one is the answer deferred.
var quitState struct {
	phase    int // quitIdle, quitAsking, quitLater, quitCommitted
	answer   int // 0 none, 1 allow, 2 refuse
	serial   uint64
	reason   QuitReason
	opts     AppOptions
	timer    *time.Timer
	approved atomic.Bool // the quit was allowed and the app's loop ends (where the OS does not end the process itself)
}

// QuitApproved reports whether a quit request was allowed and the app's loop
// is to end. On macOS AppKit ends the process itself after an allowed quit
// (AppEvWillTerminate is the last event), so it stays false there.
func QuitApproved() bool { return quitState.approved.Load() }

const (
	quitIdle = iota
	quitAsking
	quitLater
	// quitCommitted: the quit was allowed and the native termination is on
	// its way (macOS): a later request neither asks again nor replaces it.
	quitCommitted
)

// QuitPending reports whether a quit request is waiting for its answer (the
// delegate returned "later"). Main thread.
func QuitPending() bool { return quitState.phase == quitLater }

// QuitAsking reports whether a quit request is being delivered now (an answer
// given now is latched). Main thread.
func QuitAsking() bool { return quitState.phase == quitAsking }

// ReplyQuit answers quit request serial (AppEvQuitRequested's ID), being
// delivered or waiting: allow lets the app quit, false cancels. An answer
// given while the request is being delivered (synchronously, or from a
// nested call) is latched and returned by the delegate method itself; a
// later one is sent to the system. It reports false, doing nothing, when
// that request is not waiting: answered already, or older than the one
// waiting now. Main thread.
func ReplyQuit(serial uint64, allow bool) bool {
	if serial == 0 || serial != quitState.serial {
		return false
	}
	switch quitState.phase {
	case quitAsking:
		if quitState.answer == 0 {
			quitState.answer = 2
			if allow {
				quitState.answer = 1
			}
		}
		return true
	case quitLater:
		quitState.phase = quitIdle
		if quitState.timer != nil {
			quitState.timer.Stop()
			quitState.timer = nil
		}
		if !allow {
			sigQuit.pending.Store(false)
		}
		replyQuitFn(allow)
		return true
	}
	return false
}

// replyQuitFn sends a deferred answer to the system (tests replace it).
var replyQuitFn = func(allow bool) {
	repliesSent.Add(1)
	osReplyQuit(allow)
}

var repliesSent atomic.Int64

// RepliesSent counts the deferred answers the framework sent to the system
// (replyToApplicationShouldTerminate: on macOS). A latched answer is
// returned by the delegate method itself and is not counted.
func RepliesSent() int64 { return repliesSent.Load() }

// RequestQuit asks the app to quit, from any goroutine, as reason: the
// request goes through the quit policy (AppEvQuitRequested) at the app
// loop's next turn.
func RequestQuit(reason QuitReason) {
	dispatch.quitAsked.Store(int64(reason))
	Wake()
}

// deferQuit leaves a quit request that arrived while a frame held dispatch
// to the app's loop (TakeQuitRequest), which asks it after the frame. A
// request already left there stays.
func deferQuit(reason QuitReason) {
	dispatch.quitAsked.CompareAndSwap(0, int64(reason))
	Wake()
}

// TakeQuitRequest returns a quit reason RequestQuit left for the app's loop,
// 0 for none. Main thread.
func TakeQuitRequest() QuitReason { return QuitReason(dispatch.quitAsked.Swap(0)) }

// resetQuit ends any quit state: nothing is being decided or committed.
func resetQuit() {
	quitState.phase, quitState.answer = quitIdle, 0
	if quitState.timer != nil {
		quitState.timer.Stop()
		quitState.timer = nil
	}
	dispatch.quitAsked.Store(0)
}

// beginQuit delivers the quit request (class 1) and returns the latched
// answer: 1 allow, 2 refuse, 0 none (deferred).
func beginQuit(reason QuitReason) int {
	quitState.phase, quitState.answer = quitAsking, 0
	quitState.serial++
	quitState.reason = reason
	deliverNow(AppEvQuitRequested, quitState.serial, int64(reason), 0, nil)
	ans := quitState.answer
	quitState.phase = quitIdle
	if ans == 2 {
		sigQuit.pending.Store(false)
	}
	if ans == 0 {
		quitState.phase = quitLater
		if d := quitState.opts.QuitDeadline; d > 0 {
			serial := quitState.serial
			quitState.timer = time.AfterFunc(d, func() {
				dispatch.quitDue.Store(serial)
				signalApp()
			})
		}
	}
	return ans
}

// dispatchQuitWork applies a quit deadline that fired.
func dispatchQuitWork() {
	s := dispatch.quitDue.Swap(0)
	if s == 0 || quitState.phase != quitLater || quitState.serial != s {
		return
	}
	act := quitState.opts.QuitDeadlineAction
	ReplyQuit(s, act == QuitAllow)
	deliverNow(AppEvQuitDeadline, s, int64(act), 0, nil)
}

// startCommon applies the options every OS shares.
func startCommon(o AppOptions) {
	quitState.opts = o
	if o.Signals {
		startSignals()
	}
}

// sigQuit turns SIGINT and SIGTERM into quit requests (AppOptions.Signals).
var sigQuit struct {
	once    sync.Once
	pending atomic.Bool // a signal's quit request is not answered yet
}

func startSignals() {
	sigQuit.once.Do(func() {
		c := make(chan os.Signal, 2)
		signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			for range c {
				if !sigQuit.pending.CompareAndSwap(false, true) {
					os.Exit(1) // a second signal while the first is still unanswered
				}
				RequestQuit(QuitSignal)
			}
		}()
	})
}
