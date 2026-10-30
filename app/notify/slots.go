package notify

import (
	"cmp"
	"math"
	"math/bits"
	"slices"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"gonativeui/platform"
)

// The response slots (D37 class 2). A note that wants a response gets a key,
// carried in its user info, and a slot holding the key and a ticket of
// slots.responses reserved when the note is sent. The delegate finds the
// slot by the key with atomic loads only, claims it (the key goes to 0,
// once), and completes the ticket with the response before it answers the
// system: the record was reserved, so the completion needs no room anywhere.
// The record goes back to the table when the dispatcher has delivered it.
//
// A response that finds no slot (its note was evicted, removed or replaced
// just before, or an earlier run posted it and it got no slot back) takes a
// spare record (slots.spares, which posts never take) or any free record of
// the slots. With none free it is parked: the delegate keeps the system's
// completion handler and a copy of the response, and a goroutine, running
// only while something is parked, stores it once a record frees and only
// then calls the handler. The system never hears that a response was handled
// before it was stored.
//
// The post side keeps the rest of a slot's bookkeeping under svc.mu. It
// releases a slot by taking its key (the same compare-and-swap) and
// cancelling the ticket, so for every note exactly one of the delegate's
// completion and the post side's cancellation happens. A slot the delegate
// claimed is free for the post side at once; its record is not until
// delivered, and a slot is only taken once its record was reserved, so a free
// slot always exists for a reserved record.

const (
	nSpare      = 16  // records for responses that find no slot
	maxPending  = 64  // native requests whose completions are outstanding (posts, listings)
	maxAnswered = 256 // responses remembered while the launch's restore runs
)

type slot struct {
	key    atomic.Uint64 // the note's key while its response is awaited; 0 once claimed or released
	ticket atomic.Uint64 // its record in slots.responses (written before key)

	// The post side's (svc.mu):
	live     bool   // held for a note (its response may have been claimed since)
	k        uint64 // the key it was given
	id       string // the note's ID
	thread   string
	seq      int64  // when the note was posted (ns since 1970): EvictOldest removes the smallest
	gen      uint64 // the post it was given to
	accepted uint64 // svc.accepts when the system took the note; 0 until then
}

var slots struct {
	once      sync.Once
	requests  *platform.Tickets // results: permission, status, post, restore
	responses *platform.Tickets // one record per slot
	spares    *platform.Tickets // responses that find no slot
	s         [MaxResponses]slot

	withoutActions, evicted, stored, late, plain, reconciled, superseded, lostAtExit atomic.Uint64

	// answered are the keys of the responses that arrived while the
	// launch's restore runs (svc.restoring): its listing may still show a
	// note the user answered meanwhile (the click that launched the app),
	// which must not get a slot again. Past maxAnswered some were not kept.
	answered  [maxAnswered]atomic.Uint64
	nAnswered atomic.Uint32
}

// initSlots makes the tables, once per process (they are never freed).
func initSlots() {
	slots.once.Do(func() {
		slots.requests = platform.NewTickets(platform.SourceNotify, platform.MaxTickets, 256)
		slots.responses = platform.NewTickets(platform.SourceNotifyResponse, MaxResponses, MaxReply+MaxUserData)
		slots.spares = platform.NewTickets(platform.SourceNotifyResponse, nSpare, MaxReply+MaxUserData)
		slots.responses.OnFree(wakeParked)
		slots.spares.OnFree(wakeParked)
		pends.free.Store(^uint64(0))
		platform.OnLastDrain(lastDrain)
	})
}

// ---- The post side's bookkeeping (svc.mu) ----

// entry is what the post side knows of a note ID. Its posts (attempts) and
// the notes the system was sent are kept apart: only the latest post may be
// sent (latest, cut), and the note the system shows for the ID is the latest
// one sent and not refused (notes), whatever happened to later attempts. An
// idle entry is dropped (tidy), except while a listing whose snapshot may be
// older than a later post of the ID is outstanding (the launch's restore, a
// RemoveThread): it must see that post.
type entry struct {
	latest   uint64 // the generation of its latest Post, 0 for none this run
	cut      uint64 // its posts up to this generation were removed before they were sent
	thread   string // the latest Post's thread
	notes    []note // what the system has for the ID, or is being sent, oldest first
	removed  bool   // removed from the system this run (Remove, RemoveThread, EvictOldest)
	inflight int    // posts not yet sent or dropped
	slots    int    // live slots of its notes
}

// note is a post sent to the system, or (generation 0) a note of an earlier
// run that got its slot back.
type note struct {
	gen    uint64
	thread string
	taken  bool // the system took it
}

// current is the note the system shows for the ID (or will, once it takes
// it), as far as this run knows.
func (e *entry) current() (note, bool) {
	if len(e.notes) == 0 {
		return note{}, false
	}
	return e.notes[len(e.notes)-1], true
}

// took records that the system took note gen: it replaced the ones before.
func (e *entry) took(gen uint64) {
	if i := slices.IndexFunc(e.notes, func(n note) bool { return n.gen == gen }); i >= 0 {
		e.notes[i].taken = true
		e.notes = slices.Delete(e.notes, 0, i)
	}
}

// lost records that the system refused note gen, or no longer has it.
func (e *entry) lost(gen uint64) {
	e.notes = slices.DeleteFunc(e.notes, func(n note) bool { return n.gen == gen })
}

// gone records that the ID's notes were removed from the system.
func (e *entry) gone() { e.notes, e.removed = nil, true }

func (e *entry) idle() bool {
	return e.inflight == 0 && e.slots == 0 && !slices.ContainsFunc(e.notes, func(n note) bool { return !n.taken })
}

func entryOf(id string) *entry {
	e := svc.ids[id]
	if e == nil {
		e = &entry{}
		svc.ids[id] = e
	}
	return e
}

// tidy drops the entry of id if it is idle and no listing holds entries.
func tidy(id string) {
	if e := svc.ids[id]; e != nil && e.idle() && svc.listings == 0 {
		delete(svc.ids, id)
	}
}

// endListing ends a listing that holds the entries; after the last one the
// idle entries are dropped.
func endListing() {
	if svc.listings--; svc.listings > 0 {
		return
	}
	for id, e := range svc.ids {
		if e.idle() {
			delete(svc.ids, id)
		}
	}
}

// postClock is when a post is stamped (ns since 1970); tests replace it by a
// clock as coarse as Windows'.
var postClock = func() int64 { return time.Now().UnixNano() }

// takeSlot gives a free slot to post gen of id: the record tk and its key,
// the thread and when it was posted. It returns -1, cancelling tk, if no
// slot is free (it cannot happen: see the overview).
func takeSlot(tk Ticket, key uint64, id, thread string, seq int64, gen uint64) int {
	for i := range slots.s {
		s := &slots.s[i]
		if s.live && s.key.Load() != 0 {
			continue
		}
		if s.live { // claimed: the response is the delegate's
			dropSlot(s)
		}
		s.live, s.k, s.id, s.thread, s.seq, s.gen, s.accepted = true, key, id, thread, seq, gen, 0
		entryOf(id).slots++
		s.ticket.Store(uint64(tk))
		s.key.Store(key) // published: the delegate may find it from here on
		return i
	}
	slots.responses.Cancel(tk)
	return -1
}

// dropSlot ends the post side's bookkeeping of s.
func dropSlot(s *slot) {
	id := s.id
	s.live, s.k, s.id, s.thread = false, 0, "", ""
	if e := svc.ids[id]; e != nil {
		e.slots--
		tidy(id)
	}
}

// releaseSlot frees slot i if it still holds the note with key: its record
// too, unless the delegate claimed it first (that response is delivered). It
// reports whether the slot was that note's.
func releaseSlot(i int, key uint64) bool {
	s := &slots.s[i]
	if !s.live || s.k != key {
		return false
	}
	if s.key.CompareAndSwap(key, 0) {
		slots.responses.Cancel(Ticket(s.ticket.Load()))
	}
	dropSlot(s)
	return true
}

// releaseID frees the slots of the posts of id up to generation gen.
func releaseID(id string, gen uint64) {
	for i := range slots.s {
		if s := &slots.s[i]; s.live && s.id == id && s.gen <= gen {
			releaseSlot(i, s.k)
		}
	}
}

// slotOfKey is the slot given key, -1 for none.
func slotOfKey(key uint64) int {
	for i := range slots.s {
		if s := &slots.s[i]; s.live && s.k == key {
			return i
		}
	}
	return -1
}

// victimSlot is the slot EvictOldest takes for a post of id: the oldest
// still awaiting a response, of another ID, and holding the note the system
// shows for that ID (it removes by ID: removing a replaced note would remove
// its replacement too); -1 for none.
func victimSlot(id string) int {
	best := -1
	for i := range slots.s {
		s := &slots.s[i]
		if !s.live || s.key.Load() == 0 || s.id == id {
			continue
		}
		if e := svc.ids[s.id]; e == nil {
			continue
		} else if c, ok := e.current(); !ok || c.gen != s.gen {
			continue
		}
		// Equal times happen (Windows' clock ticks about every 0.5 ms or
		// more, and posts come faster): the earlier post is the one with the
		// smaller gen.
		if best < 0 || s.seq < slots.s[best].seq || s.seq == slots.s[best].seq && s.gen < slots.s[best].gen {
			best = i
		}
	}
	return best
}

// evictFor frees slots for a post of id by evicting the oldest notes until a
// record is free: the record (0 if none could be had) and the IDs to remove
// from Notification Center.
func evictFor(id string) (Ticket, []string) {
	var ids []string
	for {
		v := victimSlot(id)
		if v < 0 {
			return 0, ids
		}
		vid := slots.s[v].id
		svc.ids[vid].gone()
		releaseID(vid, math.MaxUint64)
		ids = append(ids, vid)
		slots.evicted.Add(1)
		if tk, err := slots.responses.Reserve(0); err == nil {
			return tk, ids
		}
	}
}

// keys makes the response keys: base+1, base+2, … (skipping 0), never one
// twice in a process; after 2^64 keys there are no more, and notes go out
// without actions. The base is random, so an earlier run's keys (still in
// Notification Center) are not this run's. svc.mu.
var keys struct {
	base, n uint64
	spent   bool
}

func nextKey() uint64 {
	for !keys.spent {
		if keys.n++; keys.n == 0 {
			keys.spent = true
			break
		}
		if k := keys.base + keys.n; k != 0 {
			return k
		}
	}
	return 0
}

// ---- The delegate's side: any thread, no locks, no allocation ----

// claim takes the slot holding key and returns its record; false when no
// slot holds it (claimed already, released, or never reserved in this run).
func claim(key uint64) (Ticket, bool) {
	for i := range slots.s {
		s := &slots.s[i]
		if s.key.Load() != key {
			continue
		}
		tk := s.ticket.Load() // written before the key: it is the key's
		if s.key.CompareAndSwap(key, 0) {
			return Ticket(tk), true
		}
		return 0, false
	}
	return 0, false
}

// testHookStoring runs while a response is being stored (tests: responses
// paused there).
var testHookStoring func()

// respond stores the user's response to the note with key (0: a plain note)
// and queues its delivery: action, the reply text (cut to MaxReply at a
// character boundary) and the note's UserData. The delegate calls it before
// it answers the system. A response whose note holds no slot takes a free
// record (store).
//
// It returns nil once the response is stored, or for a plain note: the
// delegate answers the system then. Otherwise the response is parked, with a
// copy of its data: the delegate sets its ack, which answers the system, and
// its drop, which releases what ack would have used, calls wait, and must
// not answer itself. It allocates only to park.
func respond(key uint64, action int64, reply, data []byte) *parked {
	if key == 0 {
		slots.plain.Add(1)
		return nil
	}
	reply = cutUTF8(reply, MaxReply)
	if len(data) > MaxUserData {
		data = data[:MaxUserData]
	}
	if svc.restoring.Load() {
		noteAnswered(key) // before the slots are searched (restore)
	}
	if tk, ok := claim(key); ok {
		if testHookStoring != nil {
			testHookStoring()
		}
		if slots.responses.CompleteParts(tk, action, int64(len(data)), reply, data) {
			slots.stored.Add(1)
			return nil
		}
		slots.responses.Cancel(tk) // the app's last delivery has begun: parked, then given up
	} else if store(action, reply, data) {
		slots.stored.Add(1)
		slots.late.Add(1)
		return nil
	}
	return park(action, reply, data)
}

// store stores a response without a slot in a free record: a spare, or one of
// the slots'. It reports false when none is free or the app's last delivery
// has begun.
func store(action int64, reply, data []byte) bool {
	for _, t := range [...]*platform.Tickets{slots.spares, slots.responses} {
		tk, err := t.Reserve(0)
		if err != nil {
			continue
		}
		if testHookStoring != nil {
			testHookStoring()
		}
		if t.CompleteParts(tk, action, int64(len(data)), reply, data) {
			return true
		}
		t.Cancel(tk)
		return false
	}
	return false
}

// cutUTF8 cuts b to at most n bytes without splitting a character.
func cutUTF8(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	c := n
	for c > 0 && c > n-utf8.UTFMax && !utf8.RuneStart(b[c]) {
		c--
	}
	return b[:c]
}

// noteAnswered remembers that a response to the note with key arrived.
func noteAnswered(key uint64) {
	if i := slots.nAnswered.Add(1) - 1; i < maxAnswered {
		slots.answered[i].Store(key)
	}
}

// wasAnswered reports whether a response to the note with key arrived while
// the restore runs; unknown when more arrived than were kept.
func wasAnswered(key uint64) (yes, unknown bool) {
	n := slots.nAnswered.Load()
	if n > maxAnswered {
		return false, true
	}
	for i := range n {
		if slots.answered[i].Load() == key {
			return true, false
		}
	}
	return false, false
}

// parked is a response waiting for a free record.
type parked struct {
	action int64
	n      int    // the UserData's length, at the end of data
	data   []byte // the reply, then the UserData
	ack    func() // answers the system (the delegate sets ack and drop before wait)
	drop   func() // gives the system's handler up unanswered (the app has ended)
}

// parking holds the parked responses. A goroutine retries them while any is
// held; the app's last drain stores them while records free and gives up
// those it cannot (lastDrain), and from then on a response that cannot be
// stored is given up at once (Stats.LostAtExit).
var parking = struct {
	mu      sync.Mutex
	list    []*parked
	running bool          // the retry goroutine runs
	wake    chan struct{} // makes it try at once
	ended   chan struct{} // closed when it has ended
}{wake: make(chan struct{}, 1)}

func park(action int64, reply, data []byte) *parked {
	buf := make([]byte, 0, len(reply)+len(data))
	return &parked{action: action, n: len(data), data: append(append(buf, reply...), data...)}
}

// wait queues p: it is stored once a record frees, and then answered. After
// the app's last drain has begun it can never be stored: it is given up.
func (p *parked) wait() {
	parking.mu.Lock()
	if platform.AppClosed() {
		parking.mu.Unlock()
		giveUp([]*parked{p})
		return
	}
	parking.list = append(parking.list, p)
	if !parking.running {
		parking.running = true
		parking.ended = make(chan struct{})
		go retryParked(parking.ended)
	}
	parking.mu.Unlock()
	wakeParked()
}

// storeParked stores the parked responses that fit, in order, and returns
// them; with final set, or once the last drain has begun, it also returns
// the others, which can never be stored. parking.mu is held.
func storeParked(final bool) (done, lost []*parked) {
	n := 0
	for _, p := range parking.list {
		k := len(p.data) - p.n
		if !store(p.action, p.data[:k], p.data[k:]) {
			break
		}
		n++
	}
	done = slices.Clone(parking.list[:n])
	parking.list = slices.Delete(parking.list, 0, n)
	if final || platform.AppClosed() {
		lost, parking.list = parking.list, nil
	}
	return done, lost
}

// answer answers the system for the responses stored, and gives up the lost.
func answer(done, lost []*parked) {
	for _, p := range done {
		slots.stored.Add(1)
		slots.late.Add(1)
		p.ack()
	}
	giveUp(lost)
}

func giveUp(lost []*parked) {
	for _, p := range lost {
		slots.lostAtExit.Add(1)
		p.drop()
	}
}

// wakeParked coalesces record releases and newly parked responses. It also
// covers a release between a failed reservation and parking the response.
func wakeParked() {
	select {
	case parking.wake <- struct{}{}:
	default:
	}
}

// retryParked stores responses when a record frees; no timer runs while the
// dispatcher is blocked and no record can become available.
func retryParked(ended chan struct{}) {
	defer close(ended)
	for range parking.wake {
		parking.mu.Lock()
		done, lost := storeParked(false)
		empty := len(parking.list) == 0
		if empty {
			parking.running = false
		}
		parking.mu.Unlock()
		answer(done, lost)
		if empty {
			return
		}
	}
}

// lastDrain is the parked responses' part of the app's last drain
// (platform.OnLastDrain): while the gate is open they are stored as the
// drain frees records, so they are delivered before EvWillTerminate; once it
// has closed (final) those left are given up, and the retry goroutine is
// stopped and waited for, answers included. It reports whether any is still parked.
func lastDrain(final bool) bool {
	parking.mu.Lock()
	done, lost := storeParked(final)
	left := len(parking.list) > 0
	running, ended := parking.running, parking.ended
	parking.mu.Unlock()
	answer(done, lost)
	if final && ended != nil {
		// Also when it has found the list empty (running is false then) and is
		// still answering what it stored: the drain ends after the answers.
		if running {
			select {
			case parking.wake <- struct{}{}:
			default:
			}
		}
		<-ended
	}
	return left
}

// presentInForeground is what the delegate tells the system for a note
// arriving while the app is active.
func presentInForeground() bool { return svc.foreground.Load() }

// actionPrefix starts the system identifier of an Action ("gnui.action.7").
const actionPrefix = "gnui.action."

// The system's identifiers of a click on the note and of its dismissal
// (macOS reads them from UserNotifications at Setup).
var (
	defaultActionID = "com.apple.UNNotificationDefaultActionIdentifier"
	dismissActionID = "com.apple.UNNotificationDismissActionIdentifier"
)

// actionOf is the Response.Action of a system action identifier, without
// allocating.
func actionOf(id []byte) int64 {
	switch {
	case len(id) == 0 || string(id) == defaultActionID:
		return ActionClick
	case string(id) == dismissActionID:
		return ActionDismiss
	case len(id) <= len(actionPrefix) || string(id[:len(actionPrefix)]) != actionPrefix || len(id)-len(actionPrefix) > 19:
		return ActionOther
	}
	var n uint64 // 19 digits fit
	for _, c := range id[len(actionPrefix):] {
		if c < '0' || c > '9' {
			return ActionOther
		}
		n = n*10 + uint64(c-'0')
	}
	if n == 0 || n > math.MaxInt64 {
		return ActionOther
	}
	return int64(n)
}

// ---- Native requests in flight ----

// The completions of a post and of a listing need more than a ticket: they
// find it in a pending entry, whose index is the native request's context.

const (
	pendPost = iota + 1
	pendRestore
	pendThread
	pendReconcile
)

type pending struct {
	kind   uint8
	ticket Ticket // pendPost, pendRestore: the result's ticket

	// pendPost: the post, its slot (-1 for none) and key, what it reserved
	id   string
	gen  uint64 // pendThread: the generation at the call
	slot int
	key  uint64
	res  PostResult

	// pendThread: the thread, and when the call was made
	thread string
	before int64

	// pendReconcile: the notes taken before the listing began (svc.accepts),
	// whether the delivered notes are listed next, the pending ones listed
	seq   uint64
	next  bool
	first []deliveredNote
}

var pends struct {
	free  atomic.Uint64 // a set bit is a free entry
	state [maxPending]atomic.Bool
	p     [maxPending]pending
}

func takePending() (int, bool) {
	for {
		v := pends.free.Load()
		if v == 0 {
			return -1, false
		}
		i := bits.TrailingZeros64(v)
		if pends.free.CompareAndSwap(v, v&^(1<<i)) {
			return i, true
		}
	}
}

// setPending fills entry i and publishes it to the completion.
func setPending(i int, p pending) {
	pends.p[i] = p
	pends.state[i].Store(true)
}

func freePending(i int) {
	pends.state[i].Store(false)
	pends.p[i] = pending{}
	pends.free.Or(1 << i)
}

// endPending takes entry i for its completion (once).
func endPending(i int) (pending, bool) {
	if i < 0 || i >= maxPending || !pends.state[i].CompareAndSwap(true, false) {
		return pending{}, false
	}
	p := pends.p[i]
	pends.p[i] = pending{}
	pends.free.Or(1 << i)
	return p, true
}

// ---- Completions from the backend (any thread) ----

func completeAuth(tk Ticket, granted bool, msg []byte) {
	var g int64
	if granted {
		g = 1
	}
	slots.requests.Complete(tk, int64(ResultPermission), g, msg)
}

func completeStatus(tk Ticket, status int64) {
	slots.requests.Complete(tk, int64(ResultStatus), status, nil)
}

// added is a post's result: code 0 when the system took the note. A note
// taken frees the slots of the posts of its ID before it (it replaced them);
// one refused frees its own and leaves those.
func added(ctx int, code int64, msg []byte) {
	p, ok := endPending(ctx)
	if !ok {
		return
	}
	svc.mu.Lock()
	e := svc.ids[p.id]
	if code == 0 {
		svc.accepts++
		if p.slot >= 0 {
			if s := &slots.s[p.slot]; s.live && s.k == p.key {
				s.accepted = svc.accepts
			}
		}
		if e != nil {
			e.took(p.gen)
		}
		releaseID(p.id, p.gen-1)
	} else {
		if e != nil {
			e.lost(p.gen)
		}
		if p.slot >= 0 {
			releaseSlot(p.slot, p.key)
		}
	}
	tidy(p.id)
	svc.mu.Unlock()
	completePost(p.ticket, p.res, code, msg)
}

// postFailed marks the result of a post that failed (ResultPost's A).
const postFailed = 1 << 16

// completePost delivers a post's result: what it reserved, and code and msg
// when it failed (code 0: the system took the note).
func completePost(tk Ticket, res PostResult, code int64, msg []byte) {
	a := int64(ResultPost) | int64(res.Reservation)<<8
	if code != 0 {
		slots.requests.Complete(tk, a|postFailed, code, msg)
		return
	}
	slots.requests.Complete(tk, a, int64(res.Response), []byte(res.Evicted))
}

// deliveredNote is one note the system has: in Notification Center, or not
// shown yet.
type deliveredNote struct {
	id, thread string
	key        uint64 // its response key, 0 for a plain note
	date       int64  // when it was delivered (ns since 1970), 0 if not yet
}

// listed takes the notes the system has for the request in entry ctx.
func listed(ctx int, list []deliveredNote) {
	p, ok := endPending(ctx)
	if !ok {
		return
	}
	switch p.kind {
	case pendRestore:
		restore(p.ticket, list)
	case pendThread:
		spawn(func() { removeListed(p, list) }) // it waits for svc.order: never on the system's thread
	case pendReconcile:
		if p.next {
			listDelivered(p.seq, list)
		} else {
			reconcile(p.seq, append(p.first, list...))
		}
	}
}

// testHookRestoring runs in restore before a note's slot is published
// (tests: a response arriving there).
var testHookRestoring func(key uint64)

// restore gives the notes of earlier runs their slots back, newest first,
// and reports how many did and which did not fit. It skips those this run
// replaced (a note of the ID the system took) or removed since Setup (the
// listing may be older), those of a thread removed since, and those answered
// since. One whose ID only has posts that failed or are still on their way
// is still shown: it gets its slot, older than those posts' (a post the
// system takes then frees it).
func restore(tk Ticket, list []deliveredNote) {
	slices.SortStableFunc(list, func(a, b deliveredNote) int { return cmp.Compare(b.date, a.date) })
	var over []byte
	n, nOver := 0, 0
	svc.mu.Lock()
	for _, d := range list {
		if d.key == 0 || slotOfKey(d.key) >= 0 || threadCut(d) {
			continue
		}
		if e := svc.ids[d.id]; e != nil && (e.removed || slices.ContainsFunc(e.notes, func(n note) bool { return n.taken })) {
			continue
		}
		yes, unknown := wasAnswered(d.key)
		if !unknown && !yes {
			yes = restoreOne(d)
		}
		switch {
		case yes:
			continue
		case slotOfKey(d.key) >= 0:
			n++
			continue
		}
		if nOver > 0 {
			over = append(over, 0)
		}
		over = append(over, d.id...)
		nOver++
	}
	svc.restoring.Store(false)
	svc.threadCuts = nil
	endListing()
	svc.mu.Unlock()
	slots.requests.Complete(tk, int64(ResultRestored), int64(n)|int64(nOver)<<32, over)
}

// restoreOne gives note d a slot, unless none is free. A response to it may
// have arrived before the slot was published, and found none: the slot is
// then taken back, and it reports answered (the answered keys are written
// before a response looks for its slot, and read here after the slot is
// published, so one of the two sees the other).
func restoreOne(d deliveredNote) (answered bool) {
	tk, err := slots.responses.Reserve(0)
	if err != nil {
		return false
	}
	if testHookRestoring != nil {
		testHookRestoring(d.key)
	}
	e := entryOf(d.id)
	e.notes = slices.Insert(e.notes, 0, note{thread: d.thread, taken: true})
	i := takeSlot(tk, d.key, d.id, d.thread, d.date, 0)
	if i < 0 {
		return false
	}
	svc.accepts++
	slots.s[i].accepted = svc.accepts
	yes, unknown := wasAnswered(d.key)
	if yes || unknown {
		e.lost(0)
		releaseSlot(i, d.key)
	}
	return yes
}

// threadCut reports whether d is in a thread removed (RemoveThread) after it
// was delivered.
func threadCut(d deliveredNote) bool {
	for _, c := range svc.threadCuts {
		if c.thread == d.thread && d.date <= c.before {
			return true
		}
	}
	return false
}

// removeListed removes the listed notes of RemoveThread's thread delivered
// before the call, except those whose ID the system was sent a note for
// since, or has a note of another thread for. The check and the removal are
// ordered with the posts (svc.order), so a post sent after the check is not
// removed.
func removeListed(p pending, list []deliveredNote) {
	var ids []string
	svc.order.Lock()
	defer svc.order.Unlock()
	svc.mu.Lock()
	for _, d := range list {
		if d.thread != p.thread || d.date > p.before {
			continue
		}
		if e := svc.ids[d.id]; e != nil {
			if c, ok := e.current(); ok && (c.gen > p.gen || c.thread != p.thread) {
				continue
			}
			e.gone()
			releaseID(d.id, p.gen)
		}
		ids = append(ids, d.id)
	}
	endListing()
	svc.mu.Unlock()
	if len(ids) > 0 {
		svc.b.remove(ids)
	}
}

// ---- Reconciliation ----

// reconcileSoon starts a reconciliation unless one is running: the system
// lists the notes it has (those not shown yet, then those shown), and the
// slots of the notes it took before the listing began and no longer has
// (the user cleared them) are freed. The posts waiting for it resume when it
// ends. It runs when the app becomes active and when a post finds every slot
// taken; never on a timer.
func reconcileSoon() {
	svc.mu.Lock()
	if svc.reconciling || !svc.on.Load() {
		svc.mu.Unlock()
		return
	}
	svc.reconciling = true
	seq := svc.accepts
	svc.mu.Unlock()
	if pi, ok := takePending(); ok {
		setPending(pi, pending{kind: pendReconcile, seq: seq, next: true})
		if svc.b.pending(pi) == nil {
			return
		}
		freePending(pi)
	}
	endReconcile()
}

// listDelivered lists the delivered notes after the pending ones.
func listDelivered(seq uint64, first []deliveredNote) {
	if pi, ok := takePending(); ok {
		setPending(pi, pending{kind: pendReconcile, seq: seq, first: first})
		if svc.b.delivered(pi) == nil {
			return
		}
		freePending(pi)
	}
	endReconcile()
}

// reconcile frees the slots of the notes taken by the system up to accept
// seq that it no longer has. A note taken later is kept: the listing may be
// older than it.
func reconcile(seq uint64, list []deliveredNote) {
	has := make(map[uint64]bool, len(list))
	for _, d := range list {
		has[d.key] = true
	}
	svc.mu.Lock()
	for i := range slots.s {
		s := &slots.s[i]
		if s.live && s.key.Load() != 0 && s.accepted != 0 && s.accepted <= seq && !has[s.k] {
			if e := svc.ids[s.id]; e != nil {
				e.lost(s.gen)
			}
			releaseSlot(i, s.k)
			slots.reconciled.Add(1)
		}
	}
	svc.mu.Unlock()
	endReconcile()
}

// endReconcile ends a reconciliation and resumes the posts waiting for it,
// in order, on a goroutine (they wait for svc.order: never on the system's
// thread).
func endReconcile() {
	svc.mu.Lock()
	waiting := svc.waiting
	svc.waiting, svc.reconciling = nil, false
	svc.mu.Unlock()
	if len(waiting) > 0 {
		spawn(func() {
			for _, p := range waiting {
				p.resume()
			}
		})
	}
}
