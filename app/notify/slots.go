package notify

import (
	"cmp"
	"math/bits"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"gonativeui/platform"
)

// The response slots (D37 class 2). A note that wants a response gets a key,
// carried in its user info, and a slot holding the key and a ticket of
// slots.responses reserved at Post. The delegate finds the slot by the key
// with atomic loads only, claims it (the key goes to 0, once), and completes
// the ticket with the response before it answers the system: the record was
// reserved, so the completion needs no room anywhere. The record goes back to
// the table when the dispatcher has delivered it.
//
// The post side (Post, Remove, RemoveThread, the launch's restore, a failed
// post) keeps the rest of a slot's bookkeeping under svc.mu. It releases a
// slot by taking its key (the same compare-and-swap) and cancelling the
// ticket, so for every note exactly one of the delegate's completion and the
// post side's cancellation happens. A slot the delegate claimed is free for
// the post side at once; its record is not until delivered, and a slot is
// only taken once its record was reserved, so a free slot always exists for
// a reserved record.

const (
	nScratch   = 4  // responses being copied at once without waiting
	maxPending = 64 // native requests whose completions are outstanding (posts, listings)
)

type slot struct {
	key    atomic.Uint64 // the note's key while its response is awaited; 0 once claimed or released
	ticket atomic.Uint64 // its record in slots.responses (written before key)

	// The post side's (svc.mu):
	live   bool   // held for a note (its response may have been claimed since)
	k      uint64 // the key it was given
	id     string // the note's ID
	thread string
	seq    int64 // when the note was posted (ns since 1970): EvictOldest removes the smallest
}

var slots struct {
	once      sync.Once
	requests  *platform.Tickets // results: permission, status, post, restore
	responses *platform.Tickets // one record per slot
	s         [MaxResponses]slot

	withoutActions, evicted, stored, plain, lost atomic.Uint64

	// answered are the keys of the last responses stored: the launch's
	// listing may still show a note the user answered before it arrived (the
	// click that launched the app), which must not get a slot again.
	answered  [16]atomic.Uint64
	nAnswered atomic.Uint32
}

// initSlots makes the tables, once per process (they are never freed).
func initSlots() {
	slots.once.Do(func() {
		slots.requests = platform.NewTickets(platform.SourceNotify, platform.MaxTickets, 256)
		slots.responses = platform.NewTickets(platform.SourceNotifyResponse, MaxResponses, MaxReply+MaxUserData)
		for i := range scratch.buf {
			scratch.buf[i] = make([]byte, 0, MaxReply+MaxUserData)
		}
		pends.free.Store(^uint64(0))
	})
}

// takeSlot gives a free slot to a note: the record tk, its key, its ID and
// thread, and when it was posted. svc.mu is held. It returns -1, cancelling
// tk, if no slot is free (it cannot happen: see the overview).
func takeSlot(tk Ticket, key uint64, id, thread string, seq int64) int {
	for i := range slots.s {
		s := &slots.s[i]
		if s.live && s.key.Load() != 0 {
			continue
		}
		s.live, s.k, s.id, s.thread, s.seq = true, key, id, thread, seq
		s.ticket.Store(uint64(tk))
		s.key.Store(key) // published: the delegate may find it from here on
		return i
	}
	slots.responses.Cancel(tk)
	return -1
}

// releaseSlot frees slot i if it still holds the note with key: its record
// too, unless the delegate claimed it first (that response is delivered).
// svc.mu is held. It reports whether the slot was that note's.
func releaseSlot(i int, key uint64) bool {
	s := &slots.s[i]
	if !s.live || s.k != key {
		return false
	}
	if s.key.CompareAndSwap(key, 0) {
		slots.responses.Cancel(Ticket(s.ticket.Load()))
	}
	s.live, s.k, s.id, s.thread = false, 0, "", ""
	return true
}

// releaseID frees the slot of the note with ID id, if any. svc.mu is held.
func releaseID(id string) {
	for i := range slots.s {
		if s := &slots.s[i]; s.live && s.id == id {
			releaseSlot(i, s.k)
		}
	}
}

// slotOfKey is the slot given key, -1 for none. svc.mu is held.
func slotOfKey(key uint64) int {
	for i := range slots.s {
		if s := &slots.s[i]; s.live && s.k == key {
			return i
		}
	}
	return -1
}

// oldestSlot is the slot still awaiting a response whose note is the
// oldest, -1 for none. svc.mu is held.
func oldestSlot() int {
	best := -1
	for i := range slots.s {
		s := &slots.s[i]
		if s.live && s.key.Load() != 0 && (best < 0 || s.seq < slots.s[best].seq) {
			best = i
		}
	}
	return best
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

// respond stores the user's response to the note with key (0: a plain note)
// in its slot and queues its delivery: action, the reply text (cut to
// MaxReply at a character boundary) and the note's UserData. The delegate
// calls it before it tells the system the response was handled. A note of
// an earlier run that has no slot gets a record if one is free. It reports
// whether the response was stored.
func respond(key uint64, action int64, reply, data []byte) bool {
	if key == 0 {
		slots.plain.Add(1)
		return false
	}
	slots.answered[slots.nAnswered.Add(1)%uint32(len(slots.answered))].Store(key)
	tk, ok := claim(key)
	if !ok {
		var err error
		if tk, err = slots.responses.Reserve(0); err != nil {
			slots.lost.Add(1)
			return false
		}
	}
	reply = cutUTF8(reply, MaxReply)
	if len(data) > MaxUserData {
		data = data[:MaxUserData]
	}
	i := takeScratch()
	buf := append(append(scratch.buf[i][:0], reply...), data...)
	stored := slots.responses.Complete(tk, action, int64(len(data)), buf)
	scratch.buf[i] = buf[:0]
	scratch.busy.And(^uint32(1 << i))
	if !stored { // the app's last delivery has begun
		slots.responses.Cancel(tk)
		slots.lost.Add(1)
		return false
	}
	slots.stored.Add(1)
	return true
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

// scratch holds a response's reply and UserData together while the record is
// written: a few buffers, each taken by one response at a time.
var scratch struct {
	busy atomic.Uint32
	buf  [nScratch][]byte
}

func takeScratch() int {
	for {
		b := scratch.busy.Load()
		free := ^b & (1<<nScratch - 1)
		if free == 0 {
			runtime.Gosched() // another response is copying: microseconds
			continue
		}
		i := bits.TrailingZeros32(free)
		if scratch.busy.CompareAndSwap(b, b|1<<i) {
			return i
		}
	}
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
	case len(id) <= len(actionPrefix) || string(id[:len(actionPrefix)]) != actionPrefix || len(id)-len(actionPrefix) > 18:
		return ActionOther
	}
	var n int64
	for _, c := range id[len(actionPrefix):] {
		if c < '0' || c > '9' {
			return ActionOther
		}
		n = n*10 + int64(c-'0')
	}
	if n <= 0 {
		return ActionOther
	}
	return n
}

// ---- Native requests in flight ----

// The completions of a post and of a listing need more than a ticket: they
// find it in a pending entry, whose index is the native request's context.

const (
	pendPost = iota + 1
	pendRestore
	pendThread
)

type pending struct {
	kind   uint8
	ticket Ticket // pendPost, pendRestore: the result's ticket
	slot   int    // pendPost: the note's slot, -1 for none
	key    uint64 // pendPost: its key
	thread string // pendThread: the thread to remove
	before int64  // pendThread: notes delivered after this are kept
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
// the system refused frees its slot.
func added(ctx int, code int64, msg []byte) {
	p, ok := endPending(ctx)
	if !ok {
		return
	}
	if code != 0 && p.slot >= 0 {
		svc.mu.Lock()
		releaseSlot(p.slot, p.key)
		svc.mu.Unlock()
	}
	slots.requests.Complete(p.ticket, int64(ResultPost), code, msg)
}

// deliveredNote is one note in Notification Center.
type deliveredNote struct {
	id, thread string
	key        uint64 // its response key, 0 for a plain note
	date       int64  // when it was delivered (ns since 1970)
}

// listed takes the notes in Notification Center for the request in entry ctx.
func listed(ctx int, list []deliveredNote) {
	p, ok := endPending(ctx)
	if !ok {
		return
	}
	switch p.kind {
	case pendRestore:
		restore(p.ticket, list)
	case pendThread:
		removeListed(p.thread, p.before, list)
	}
}

// restore gives the notes of earlier runs their slots back, newest first,
// and reports how many did and which did not fit.
func restore(tk Ticket, list []deliveredNote) {
	slices.SortStableFunc(list, func(a, b deliveredNote) int { return cmp.Compare(b.date, a.date) })
	var over []byte
	n, nOver := 0, 0
	svc.mu.Lock()
	for _, d := range list {
		if d.key == 0 || slotOfKey(d.key) >= 0 || wasAnswered(d.key) {
			continue
		}
		if rtk, err := slots.responses.Reserve(0); err == nil && takeSlot(rtk, d.key, d.id, d.thread, d.date) >= 0 {
			n++
			continue
		}
		if nOver > 0 {
			over = append(over, 0)
		}
		over = append(over, d.id...)
		nOver++
	}
	svc.mu.Unlock()
	slots.requests.Complete(tk, int64(ResultRestored), int64(n)|int64(nOver)<<32, over)
}

// wasAnswered reports whether a response to the note with key was stored
// lately.
func wasAnswered(key uint64) bool {
	for i := range slots.answered {
		if slots.answered[i].Load() == key {
			return true
		}
	}
	return false
}

// removeListed removes the listed notes of thread delivered before the call.
func removeListed(thread string, before int64, list []deliveredNote) {
	var ids []string
	svc.mu.Lock()
	for _, d := range list {
		if d.thread != thread || d.date > before {
			continue
		}
		ids = append(ids, d.id)
		if i := slotOfKey(d.key); d.key != 0 && i >= 0 {
			releaseSlot(i, d.key)
		}
	}
	svc.mu.Unlock()
	if len(ids) > 0 {
		svc.b.remove(ids)
	}
}
