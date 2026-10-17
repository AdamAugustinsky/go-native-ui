package fs

import (
	"os"
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
// Result. From then on the caller owns it, and gives it back with Release
// when it is replaced or no longer shown, so buffers are reused and
// nothing is allocated per listing once the pool is warm. A Listing must
// not be used after Release.
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

	all    []*Listing // pool, indexed by slot
	busy   []bool     // slot is owned by a worker, the ring or the caller
	free   []int32
	slotOf map[*Listing]int32

	ops   []ctlOp
	ctl   chan struct{}
	done  chan struct{} // closed by Close
	w     watcher
	werrs []Result
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
	pending bool // a job for this id is in s.queue
	refresh bool // the pending job came from a watch event
	user    bool // a List result is still owed to the caller

	watched   bool
	watchPath string
	dirty     bool
	dirtyAt   time.Time

	// The directory's mtime when the delivered listing began and when the
	// OS watch became active. A change in between is in neither the listing
	// nor the event stream; a differing mtime catches it (see gapLocked).
	listMT, watchMT     int64
	listMTOK, watchMTOK bool
}

type ctlOp struct {
	add  bool
	id   uint32
	path string
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
)

// Result is one thing Poll reports.
type Result struct {
	Kind    ResultKind
	ID      uint32
	Req     uint64   // ResultListing: the List request it answers (or a watch refresh's own number)
	Refresh bool     // ResultListing: relisted because the directory changed, not because of a List call
	Listing *Listing // ResultListing
	Err     error    // ResultWatchError
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
		wake: wake, listFn: list, newWatcher: nw, debounce: debounce,
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
	st.user, st.listMTOK = true, false
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

// Cancel drops id: its queued or running list is discarded and its watch
// is removed.
func (s *Service) Cancel(id uint32) {
	s.mu.Lock()
	if st := s.ids[id]; st != nil {
		s.nextReq++
		st.req, st.user, st.dirty = s.nextReq, false, false
		if st.watched {
			st.watched = false
			s.ops = append(s.ops, ctlOp{id: id})
		}
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
	st.watched, st.watchPath, st.dirty, st.watchMTOK = true, path, false, false
	s.ops = append(s.ops, ctlOp{add: true, id: id, path: path})
	s.mu.Unlock()
	s.kick()
}

// Unwatch stops watching id.
func (s *Service) Unwatch(id uint32) {
	s.mu.Lock()
	if st := s.ids[id]; st != nil && st.watched {
		st.watched, st.dirty = false, false
		s.ops = append(s.ops, ctlOp{id: id})
	}
	s.mu.Unlock()
	s.kick()
}

func (s *Service) kick() {
	select {
	case s.ctl <- struct{}{}:
	default:
	}
}

// Release gives a Listing obtained from Poll back to the pool. Releasing a
// Listing twice, or one that did not come from this Service, is ignored.
func (s *Service) Release(l *Listing) {
	if l == nil {
		return
	}
	s.mu.Lock()
	if slot, ok := s.slotOf[l]; ok && s.busy[slot] {
		s.busy[slot] = false
		s.free = append(s.free, slot)
	}
	s.mu.Unlock()
}

// getListingLocked takes a reset Listing from the pool. s.mu must be held.
func (s *Service) getListingLocked() (*Listing, int32) {
	if n := len(s.free); n > 0 {
		slot := s.free[n-1]
		s.free = s.free[:n-1]
		s.busy[slot] = true
		l := s.all[slot]
		l.Reset()
		return l, slot
	}
	l := new(Listing)
	slot := int32(len(s.all))
	s.all = append(s.all, l)
	s.busy = append(s.busy, true)
	s.slotOf[l] = slot
	if cap(s.free) < len(s.all) {
		s.free = append(make([]int32, 0, 2*len(s.all)), s.free...)
	}
	return l, slot
}

func (s *Service) freeSlotLocked(slot int32) {
	s.busy[slot] = false
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
		st.pending = false
		path, key, desc, req := st.path, st.key, st.desc, st.req
		watching := st.watched && st.watchPath == path
		l, slot := s.getListingLocked()
		s.mu.Unlock()

		var mt int64
		var mtOK bool
		if watching {
			mt, mtOK = dirMTime(path)
		}
		l.Err = s.listFn(path, l)
		Sort(l, key, desc)

		s.mu.Lock()
		stale := st.req != req
		if stale {
			s.freeSlotLocked(slot)
		} else if mtOK && l.Err == nil {
			st.listMT, st.listMTOK = mt, true
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
			if op.add { // skip a watch that was replaced or removed meanwhile
				if st := s.ids[op.id]; st == nil || !st.watched || st.watchPath != op.path {
					s.mu.Unlock()
					continue
				}
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
					s.watchFailed(op.id, err)
					continue
				}
				s.mu.Lock()
				s.w = w
				s.mu.Unlock()
			}
			w.remove(op.id)
			if op.add {
				if err := w.add(op.id, op.path); err != nil {
					s.watchFailed(op.id, err)
				} else {
					s.watchActive(op.id, op.path)
				}
			}
		}
	}
}

// dirMTime is the directory's own modification time.
func dirMTime(path string) (int64, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	return fi.ModTime().UnixNano(), true
}

// watchActive runs after the OS watch for id is established.
func (s *Service) watchActive(id uint32, path string) {
	mt, ok := dirMTime(path)
	s.mu.Lock()
	st := s.ids[id]
	woke := false
	if st != nil && st.watched && st.watchPath == path && ok {
		st.watchMT, st.watchMTOK = mt, true
		woke = s.gapLocked(st)
	}
	s.mu.Unlock()
	if woke {
		s.wake()
	}
}

// gapLocked closes the window between a listing and its watch: events from
// before the watch was active are lost, so if the directory's mtime moved
// between the listing's start and the watch's start, relist (once). It
// reports whether it marked id dirty. s.mu held.
func (s *Service) gapLocked(st *idState) bool {
	if !st.watched || !st.listMTOK || !st.watchMTOK || (st.path != st.watchPath && st.path != "") {
		return false
	}
	st.listMTOK = false
	if st.listMT == st.watchMT || st.dirty {
		return false
	}
	st.dirty, st.dirtyAt = true, time.Now()
	return true
}

func (s *Service) watchFailed(id uint32, err error) {
	s.mu.Lock()
	if st := s.ids[id]; st != nil {
		st.watched, st.dirty = false, false
	}
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
			if st := s.ids[c.ID]; st != nil && st.watched {
				st.watched, st.dirty = false, false
				s.ops = append(s.ops, ctlOp{id: c.ID})
				s.kick()
				dst = append(dst, Result{Kind: ResultGone, ID: c.ID})
			}
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
		dst = append(dst, Result{Kind: ResultListing, ID: m.id, Req: m.req, Refresh: refresh, Listing: s.all[m.slot]})
	}
	return dst
}

func (s *Service) markDirtyLocked(id uint32, now time.Time) {
	if st := s.ids[id]; st != nil && st.watched && !st.dirty {
		st.dirty, st.dirtyAt = true, now
	}
}

// Close stops the workers and the watcher. Results not yet polled are
// discarded.
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
	s.wg.Wait()
	s.timer.Stop()
	s.mu.Lock()
	w := s.w
	s.w = nil
	s.mu.Unlock()
	if w != nil {
		w.close()
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
