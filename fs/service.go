package fs

import (
	"errors"
	"sync"
	"time"

	"gonativeui/core"
)

// Service lists, sorts and watches directories on worker goroutines and
// hands the results to the UI thread, which collects them with Poll after
// a wake. The UI thread never waits for the file system: List, Watch and
// Unwatch only record the request (a short mutex, no I/O), and Poll only
// pops finished work.
//
// The id is the caller's handle for a pane (any uint32). Each id has at
// most one outstanding list request: a newer List for the same id
// supersedes an older one that is still queued or running, and a result
// that arrives after it was superseded is recycled instead of delivered, so
// Poll only ever returns results that are current for their id.
//
// Ownership. Workers take a *Listing from the Service's pool, fill and
// sort it, and publish it by pushing its pool slot (a pointer-free
// message) on a core.Ring; Poll pops the slot and returns the Listing in a
// Result together with a lease number. From then on the caller owns it, and
// gives it back with Release(listing, lease) when it is replaced or no
// longer shown, so buffers are reused and nothing is allocated per listing
// once the pool is warm. A Listing must not be used after Release.
//
// The lease identifies one hand-out of a pooled Listing: the same *Listing
// is handed out again after a Release, and a stale duplicate Release of the
// earlier hand-out must not take it away from its new owner. Release
// ignores (and counts, see IgnoredReleases) a call whose lease is not the
// slot's current one.
type Service struct {
	wake       func()
	listFn     func(path string, l *Listing) error
	newWatcher func(wake func()) (watcher, error)
	debounce   time.Duration
	t0         time.Time

	ring  *core.Ring[resMsg]
	timer *time.Timer
	wg    sync.WaitGroup

	// UI-thread scratch.
	changes []Change

	mu      sync.Mutex
	cond    *sync.Cond // signalled when the job queue gains work or the service closes
	closed  bool
	nextReq uint64
	ids     map[uint32]*idState
	queue   []uint32 // ids with a queued job
	qhead   int

	all      []*Listing // pool, indexed by slot
	busy     []bool     // slot is owned by a worker, the ring or the caller
	lease    []uint32   // current hand-out number of each busy slot
	free     []int32
	slotOf   map[*Listing]int32
	leaseSeq uint32
	ignored  uint64 // Release calls that did not match a lease

	seq    uint64 // orders list starts against watch activations
	wgens  uint64 // watch generations
	opens  int    // Open calls in flight
	openFn func(path string) error

	ops   []ctlOp
	ctl   chan struct{}
	done  chan struct{} // closed by Close
	w     watcher
	werrs []Result // watch and open errors waiting for Poll
}

// resMsg is what travels on the ring: slots and numbers, no pointers.
type resMsg struct {
	id   uint32
	slot int32
	req  uint64
}

type idState struct {
	// current list request (also the job, until a worker takes it)
	path    string
	key     SortKey
	desc    bool
	req     uint64
	pending bool // an executable job for this id is queued (Cancel clears it; the stale queue entry is skipped)
	refresh bool // the pending job came from a watch event
	user    bool // a List result is still owed to the caller

	watched   bool
	watchPath string
	wgen      uint64 // generation of the latest Watch/Unwatch; older ops, errors and events are obsolete
	wactive   bool   // the OS watch of generation wgen is established
	dirty     bool
	dirtyAt   time.Time

	// A change between a listing and the moment the OS watch became active
	// is in neither the listing nor the event stream. The directory's mtime
	// when the latest listing began (lastMT, ordered by lastSeq) is compared
	// once with its mtime right after the watch was established (watchMT,
	// watchSeq), and only if the listing began first (see gapLocked).
	// Listings that begin after the watch are covered by its events and are
	// never compared.
	lastPath string
	lastMT   int64
	lastSeq  uint64
	lastOK   bool
	watchMT  int64
	watchSeq uint64
	watchMTK bool
}

type ctlOp struct {
	add  bool
	id   uint32
	path string
	gen  uint64
}

// ResultKind says which fields of a Result are set.
type ResultKind uint8

const (
	// ResultListing: Listing is the id's new listing (its Err may be set).
	// The caller owns it and must Release it.
	ResultListing ResultKind = iota
	// ResultGone: the watched directory was deleted or moved away. The
	// watch is dropped; navigate elsewhere.
	ResultGone
	// ResultWatchError: Watch could not be established (Err). The id keeps
	// working without live updates.
	ResultWatchError
	// ResultOpenError: an Open call failed (Err). A successful Open reports
	// nothing.
	ResultOpenError
)

// Result is one thing Poll reports.
type Result struct {
	Kind    ResultKind
	ID      uint32
	Req     uint64   // ResultListing: the List request it answers (or a watch refresh's own number)
	Refresh bool     // ResultListing: relisted because the directory changed, not because of a List call
	Listing *Listing // ResultListing
	Lease   uint32   // ResultListing: pass to Release with Listing
	Err     error    // ResultWatchError, ResultOpenError
}

const (
	numWorkers      = 2
	ringCapacity    = 1024
	defaultDebounce = 50 * time.Millisecond
)

// NewService starts a Service. wake is called, from any goroutine, when
// Poll has something to return; it must not block (platform.Wake).
func NewService(wake func()) *Service {
	return newService(wake, listDir, newWatcher, defaultDebounce)
}

// NewServiceWith is NewService with the caller's list function in place of the
// OS listing and without an OS watcher (Watch reports ResultWatchError with
// ErrUnsupported). It is for tools that serve listings that are not on disk:
// the synthetic directories of a benchmark go through the same pool, ring and
// Release path as real ones. list must be safe to call from any goroutine and
// should not allocate once l's buffers have grown.
func NewServiceWith(wake func(), list func(path string, l *Listing) error) *Service {
	return newService(wake, list, func(func()) (watcher, error) { return nil, ErrUnsupported }, defaultDebounce)
}

func newService(wake func(), list func(string, *Listing) error, nw func(func()) (watcher, error), debounce time.Duration) *Service {
	if wake == nil {
		wake = func() {}
	}
	s := &Service{
		wake: wake, listFn: list, newWatcher: nw, debounce: debounce, openFn: openPath,
		t0:     time.Now(),
		ring:   core.NewRing[resMsg](ringCapacity),
		ids:    make(map[uint32]*idState),
		slotOf: make(map[*Listing]int32),
		ctl:    make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
	s.cond = sync.NewCond(&s.mu)
	s.timer = time.AfterFunc(time.Hour, wake)
	s.timer.Stop()
	for i := 0; i < numWorkers; i++ {
		s.wg.Add(1)
		go s.worker()
	}
	s.wg.Add(1)
	go s.control()
	return s
}

// state returns id's state, creating it. s.mu must be held.
func (s *Service) state(id uint32) *idState {
	st := s.ids[id]
	if st == nil {
		st = new(idState)
		s.ids[id] = st
	}
	return st
}

// List requests a sorted listing of path for id and returns the request
// number. It supersedes id's previous request. The result arrives through
// Poll.
func (s *Service) List(id uint32, path string, key SortKey, desc bool) (req uint64) {
	s.mu.Lock()
	st := s.state(id)
	st.user = true
	req = s.enqueueLocked(id, st, path, key, desc, false)
	s.mu.Unlock()
	return req
}

func (s *Service) enqueueLocked(id uint32, st *idState, path string, key SortKey, desc, refresh bool) uint64 {
	s.nextReq++
	st.req, st.path, st.key, st.desc, st.refresh = s.nextReq, path, key, desc, refresh
	if !st.pending {
		st.pending = true
		s.queue = append(s.queue, id)
		s.cond.Signal()
	}
	return st.req
}

// invalidateLocked drops id's list request: a queued job will not run (its
// queue entry is skipped), a running or already finished one is discarded
// when it reaches the ring or Poll.
func (s *Service) invalidateLocked(st *idState) {
	s.nextReq++
	st.req, st.pending, st.user, st.dirty = s.nextReq, false, false, false
}

// Cancel drops id: its queued or running list is discarded and its watch
// is removed.
func (s *Service) Cancel(id uint32) {
	s.mu.Lock()
	if st := s.ids[id]; st != nil {
		s.invalidateLocked(st)
		s.unwatchLocked(id, st)
	}
	s.mu.Unlock()
	s.kick()
}

// Watch makes the Service relist id (with its last List parameters) when
// the directory path changes on disk, debounced to one relist per ~50 ms.
// A previous watch of id is replaced. Setting up the OS watch happens off
// the calling thread.
func (s *Service) Watch(id uint32, path string) {
	s.mu.Lock()
	st := s.state(id)
	s.wgens++
	st.watched, st.watchPath, st.dirty, st.wactive, st.watchMTK, st.wgen = true, path, false, false, false, s.wgens
	s.ops = append(s.ops, ctlOp{add: true, id: id, path: path, gen: st.wgen})
	s.mu.Unlock()
	s.kick()
}

// Unwatch stops watching id.
func (s *Service) Unwatch(id uint32) {
	s.mu.Lock()
	if st := s.ids[id]; st != nil && st.watched {
		s.unwatchLocked(id, st)
	}
	s.mu.Unlock()
	s.kick()
}

// unwatchLocked ends id's watch and makes everything still in flight for it
// (queued ops, errors, events) obsolete.
func (s *Service) unwatchLocked(id uint32, st *idState) {
	if !st.watched {
		return
	}
	s.wgens++
	st.watched, st.wactive, st.dirty, st.wgen = false, false, false, s.wgens
	s.ops = append(s.ops, ctlOp{id: id, gen: st.wgen})
}

func (s *Service) kick() {
	select {
	case s.ctl <- struct{}{}:
	default:
	}
}

// maxOpens bounds the Open calls in flight; opens beyond it fail at once.
const maxOpens = 8

var errOpenBusy = errors.New("fs: too many files are being opened")

// Open opens path with the OS default application (a folder in the file
// manager) without blocking the caller: the existence check and the launch
// run on a goroutine of their own, because either may stall on a dead
// network mount. Success is silent; a failure arrives through Poll as a
// ResultOpenError for id. Close does not wait for opens that are stuck.
func (s *Service) Open(id uint32, path string) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if s.opens >= maxOpens {
		s.werrs = append(s.werrs, Result{Kind: ResultOpenError, ID: id, Err: errOpenBusy})
		s.mu.Unlock()
		s.wake()
		return
	}
	s.opens++
	fn := s.openFn
	s.mu.Unlock()
	go func() {
		err := fn(path)
		s.mu.Lock()
		s.opens--
		report := err != nil && !s.closed
		if report {
			s.werrs = append(s.werrs, Result{Kind: ResultOpenError, ID: id, Err: err})
		}
		s.mu.Unlock()
		if report {
			s.wake()
		}
	}()
}

// Release gives a Listing obtained from Poll back to the pool; lease is the
// Result.Lease it came with. A call whose lease is not the Listing's
// current hand-out (a duplicate, or a stale release of an earlier hand-out
// of the same pooled Listing) is ignored, so it can never take the Listing
// from its present owner. Listings that did not come from this Service
// (lease 0) are ignored silently.
func (s *Service) Release(l *Listing, lease uint32) {
	if l == nil {
		return
	}
	s.mu.Lock()
	slot, ok := s.slotOf[l]
	switch {
	case ok && lease != 0 && s.busy[slot] && s.lease[slot] == lease:
		s.freeSlotLocked(slot)
	case ok || lease != 0:
		s.ignored++
	}
	s.mu.Unlock()
}

// IgnoredReleases is the number of Release calls that were ignored because
// their lease did not match (duplicates and stale releases).
func (s *Service) IgnoredReleases() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ignored
}

// getListingLocked takes a reset Listing from the pool and leases it.
// s.mu must be held.
func (s *Service) getListingLocked() (*Listing, int32) {
	s.leaseSeq++
	if s.leaseSeq == 0 {
		s.leaseSeq = 1
	}
	if n := len(s.free); n > 0 {
		slot := s.free[n-1]
		s.free = s.free[:n-1]
		s.busy[slot] = true
		s.lease[slot] = s.leaseSeq
		l := s.all[slot]
		l.Reset()
		return l, slot
	}
	l := new(Listing)
	slot := int32(len(s.all))
	s.all = append(s.all, l)
	s.busy = append(s.busy, true)
	s.lease = append(s.lease, s.leaseSeq)
	s.slotOf[l] = slot
	if cap(s.free) < len(s.all) {
		s.free = append(make([]int32, 0, 2*len(s.all)), s.free...)
	}
	return l, slot
}

func (s *Service) freeSlotLocked(slot int32) {
	s.busy[slot] = false
	s.lease[slot] = 0
	s.free = append(s.free, slot)
}

func (s *Service) worker() {
	defer s.wg.Done()
	for {
		s.mu.Lock()
		for s.qhead == len(s.queue) && !s.closed {
			s.cond.Wait()
		}
		if s.closed {
			s.mu.Unlock()
			return
		}
		id := s.queue[s.qhead]
		s.qhead++
		if s.qhead == len(s.queue) {
			s.queue, s.qhead = s.queue[:0], 0
		}
		st := s.ids[id]
		if !st.pending { // cancelled or invalidated while queued
			s.mu.Unlock()
			continue
		}
		st.pending = false
		path, key, desc, req := st.path, st.key, st.desc, st.req
		s.seq++
		startSeq := s.seq
		l, slot := s.getListingLocked()
		s.mu.Unlock()

		// The directory's mtime before the listing begins, whatever the
		// watch state: Watch may be requested after the listing is shown.
		mt, mtOK := dirMTime(path)
		l.Err = s.listFn(path, l)
		Sort(l, key, desc)

		s.mu.Lock()
		stale := st.req != req
		if stale {
			s.freeSlotLocked(slot)
		} else {
			st.lastPath, st.lastMT, st.lastSeq, st.lastOK = path, mt, startSeq, mtOK && l.Err == nil
			s.gapLocked(st)
		}
		s.mu.Unlock()
		if stale {
			continue
		}
		msg := resMsg{id: id, slot: slot, req: req}
		for !s.ring.TryPush(msg) { // the UI is behind: wait, never drop
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return
			}
			time.Sleep(200 * time.Microsecond)
		}
		s.wake()
	}
}

// control applies Watch and Unwatch to the OS watcher, which may take a
// while (FSEvents rebuilds its stream), off the UI thread.
func (s *Service) control() {
	defer s.wg.Done()
	for {
		select {
		case <-s.ctl:
		case <-s.done:
			return
		}
		for {
			s.mu.Lock()
			if len(s.ops) == 0 {
				s.mu.Unlock()
				break
			}
			op := s.ops[0]
			s.ops = append(s.ops[:0], s.ops[1:]...)
			if op.add && !s.currentLocked(op.id, op.gen) { // replaced or removed meanwhile
				s.mu.Unlock()
				continue
			}
			w, closed := s.w, s.closed
			s.mu.Unlock()
			if closed {
				return
			}
			if w == nil {
				if !op.add {
					continue
				}
				var err error
				if w, err = s.newWatcher(s.wake); err != nil {
					s.watchFailed(op.id, op.gen, err)
					continue
				}
				s.mu.Lock()
				if s.closed { // Close gave up waiting for us and has already run
					s.mu.Unlock()
					w.close()
					return
				}
				s.w = w
				s.mu.Unlock()
			}
			w.remove(op.id) // the removed watch delivers nothing from here on (watcher contract)
			if op.add {
				// Events of this generation are accepted from now on, so none
				// is lost between add registering and watchActive running.
				s.mu.Lock()
				if s.currentLocked(op.id, op.gen) {
					s.ids[op.id].wactive = true
				}
				s.mu.Unlock()
				if err := w.add(op.id, op.path); err != nil {
					s.watchFailed(op.id, op.gen, err)
				} else {
					s.watchActive(op.id, op.gen, op.path)
				}
			}
		}
	}
}

// currentLocked reports whether gen is id's latest watch generation and the
// watch is wanted. s.mu held.
func (s *Service) currentLocked(id uint32, gen uint64) bool {
	st := s.ids[id]
	return st != nil && st.watched && st.wgen == gen
}

// watchActive runs after the OS watch for id (generation gen) is established.
func (s *Service) watchActive(id uint32, gen uint64, path string) {
	s.mu.Lock()
	if !s.currentLocked(id, gen) {
		s.mu.Unlock()
		return
	}
	st := s.ids[id]
	s.seq++
	st.watchSeq = s.seq
	s.mu.Unlock()
	mt, ok := dirMTime(path) // after the watch is live: events cover everything later
	s.mu.Lock()
	woke := false
	if s.currentLocked(id, gen) {
		st.watchMT, st.watchMTK = mt, ok
		woke = s.gapLocked(st)
	}
	s.mu.Unlock()
	if woke {
		s.wake()
	}
}

// gapLocked closes the window between a listing and its watch. Events from
// before the watch was live are lost, so a listing that began before the
// watch was established is compared, once, with the directory's mtime right
// after establishment: if it moved, the listing may predate a change, and
// the directory is relisted. Listings that began after the watch are
// covered by its events and are not compared (comparing them against the
// establishment-time mtime would relist forever after the first change). It
// reports whether it marked id dirty. s.mu held.
func (s *Service) gapLocked(st *idState) bool {
	if !st.watched || !st.watchMTK || !st.lastOK || st.lastPath != st.watchPath || st.lastSeq > st.watchSeq {
		return false
	}
	st.lastOK = false // consumed
	if st.lastMT == st.watchMT || st.dirty {
		return false
	}
	st.dirty, st.dirtyAt = true, time.Now()
	return true
}

func (s *Service) watchFailed(id uint32, gen uint64, err error) {
	s.mu.Lock()
	if !s.currentLocked(id, gen) { // a newer Watch/Unwatch superseded this one: its failure is moot
		s.mu.Unlock()
		return
	}
	st := s.ids[id]
	st.watched, st.wactive, st.dirty = false, false, false
	s.werrs = append(s.werrs, Result{Kind: ResultWatchError, ID: id, Err: err})
	s.mu.Unlock()
	s.wake()
}

// Poll appends finished results to dst and returns it. Call it on the UI
// thread after a wake; pass dst[:0] of a reused slice and it allocates
// nothing once warm. Poll also drains the OS watcher, so it must be called
// after wakes caused by file system changes too (they use the same wake).
func (s *Service) Poll(dst []Result) []Result {
	now := time.Now()

	s.mu.Lock()
	w := s.w
	s.mu.Unlock()
	if w != nil {
		s.changes = w.drain(s.changes[:0])
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.changes {
		switch c.Kind {
		case ChangeDir:
			s.markDirtyLocked(c.ID, now)
		case ChangeOverflow:
			for id := range s.ids {
				s.markDirtyLocked(id, now)
			}
		case ChangeGone:
			st := s.ids[c.ID]
			if st == nil || !st.watched || !st.wactive { // from a watch that is no longer current
				continue
			}
			// Whatever is queued, running or finished for the vanished
			// directory is obsolete; a request for another path stands.
			if st.path == st.watchPath || st.path == "" {
				s.invalidateLocked(st)
			}
			s.unwatchLocked(c.ID, st)
			s.kick()
			dst = append(dst, Result{Kind: ResultGone, ID: c.ID})
		}
	}
	s.changes = s.changes[:0]

	// Relist the directories whose debounce window has passed; arm the
	// timer for the earliest of the rest.
	var next time.Duration
	for id, st := range s.ids {
		if !st.dirty {
			continue
		}
		if wait := st.dirtyAt.Add(s.debounce).Sub(now); wait > 0 {
			if next == 0 || wait < next {
				next = wait
			}
			continue
		}
		st.dirty = false
		if st.path == "" {
			st.path = st.watchPath
		}
		s.enqueueLocked(id, st, st.path, st.key, st.desc, !st.user)
	}
	if next > 0 {
		s.timer.Reset(next)
	}

	if len(s.werrs) > 0 {
		dst = append(dst, s.werrs...)
		s.werrs = s.werrs[:0]
	}

	for {
		m, ok := s.ring.Pop()
		if !ok {
			break
		}
		st := s.ids[m.id]
		if st == nil || st.req != m.req { // superseded after the worker finished
			s.freeSlotLocked(m.slot)
			continue
		}
		refresh := st.refresh
		st.user = false
		dst = append(dst, Result{Kind: ResultListing, ID: m.id, Req: m.req, Refresh: refresh, Listing: s.all[m.slot], Lease: s.lease[m.slot]})
	}
	return dst
}

func (s *Service) markDirtyLocked(id uint32, now time.Time) {
	if st := s.ids[id]; st != nil && st.watched && st.wactive && !st.dirty {
		st.dirty, st.dirtyAt = true, now
	}
}

// closeWait bounds how long Close waits for goroutines that are stuck in the
// file system (a listing or a watch setup on a dead network mount); they are
// detached after it.
var closeWait = 2 * time.Second

// Close stops the workers and the watcher. Results not yet polled are
// discarded. It does not wait forever for work stuck in the file system.
func (s *Service) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.cond.Broadcast()
	s.mu.Unlock()
	close(s.done)
	stopped := make(chan struct{})
	go func() { s.wg.Wait(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(closeWait):
	}
	s.timer.Stop()
	s.mu.Lock()
	w := s.w
	s.w = nil
	s.mu.Unlock()
	if w != nil {
		closed := make(chan struct{})
		go func() { w.close(); close(closed) }()
		select {
		case <-closed:
		case <-time.After(closeWait):
		}
	}
}

// ListDir lists path into l synchronously, unsorted (call Sort for Order).
// l is reset first and l.Err is set to the returned error. Use the Service
// for anything the UI thread triggers; this is for tools and tests.
func ListDir(path string, l *Listing) error {
	l.Reset()
	l.Err = listDir(path, l)
	return l.Err
}
