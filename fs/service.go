package fs

import (
	"errors"
	"slices"
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
// once the pool is warm. A Listing must not be used after Release. Spare
// buffers have an 8 MiB budget, raised to four times the recent working size
// (at most 256 MiB). An exceptional buffer over 2 MiB and twice the recent
// working size is dropped on release; repeated large listings still reuse.
// After 30 seconds without a release, oversized spare buffers are reclaimed.
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

	// Control-goroutine scratch.
	changes []Change

	// mu is a core.Mutex: a contended sync.Mutex parks its waiter on a runtime
	// sudog, which is heap-allocated when the P's cache and the central list
	// are empty (see core.Parker). Nothing under mu does I/O or blocks.
	mu       core.Mutex
	closed   bool
	nextReq  uint64
	ids      map[uint32]*idState
	dirtyIDs map[uint32]*idState
	queue    []uint32 // ids with a queued job
	qhead    int

	all         []*Listing // pool, indexed by slot
	busy        []bool     // slot is owned by a worker, the ring or the caller
	pins        []uint32   // read-only worker/control references after Release
	pooledBytes int
	poolDue     time.Time
	recentBytes [9]int
	recentPos   int
	lease       []uint32 // current hand-out number of each busy slot
	free        []int32
	slotOf      map[*Listing]int32
	leaseSeq    uint32
	ignored     uint64 // Release calls that did not match a lease

	seq    uint64 // orders list starts against watch activations
	wgens  uint64 // watch generations
	opens  int    // Open calls in flight
	openFn func(path string) error

	// The workers and the control goroutine sleep in core.Parkers, which take
	// no runtime sudog (a sync.Cond or channel wait does, and allocates one on
	// a P that has none). idle[i] says worker i is (about to be) parked and
	// has not been woken since; it is guarded by mu, and a wake that arrives
	// before the Park is kept by the Parker.
	parks    [numWorkers]*core.Parker
	idle     [numWorkers]bool
	ctlPark  *core.Parker
	ringWait [numWorkers]bool

	ops   []ctlOp
	w     watcher
	werrs []pendingResult // watch and open errors waiting for Poll
}

// resMsg is what travels on the ring: slots and numbers, no pointers.
type resMsg struct {
	id   uint32
	slot int32
	req  uint64
}

type pendingResult struct {
	result Result
	gen    uint64 // zero for Open; watch generation otherwise
}

type idState struct {
	id            uint32
	latest        int32
	latestLease   uint32
	latestPath    string
	syncReq       uint64
	syncGen       uint64
	running       bool
	resortRefresh bool // a refresh superseded by a resort still has to run
	resort        *Listing
	resortSlot    int32
	// current list request (also the job, until a worker takes it)
	path    string
	key     SortKey
	desc    bool
	req     uint64
	pending bool // an executable job for this id is queued (Cancel clears it; the stale queue entry is skipped)
	refresh bool // the pending job came from a watch event
	user    bool // a List result is still owed to the caller

	watched    bool
	watchPath  string
	wgen       uint64 // generation of the latest Watch/Unwatch; older ops, errors and events are obsolete
	wactive    bool   // the OS watch of generation wgen is established
	dirty      bool
	dirtyAt    time.Time
	dirtyDue   time.Time
	structural bool

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
	sync bool
	slot int32
	req  uint64
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
	// Kind says what the other fields mean.
	Kind ResultKind
	// ID is the caller's id of the directory.
	ID      uint32
	Req     uint64   // ResultListing: the List request it answers (or a watch refresh's own number)
	Refresh bool     // ResultListing: relisted because the directory changed, not because of a List call
	Listing *Listing // ResultListing
	Lease   uint32   // ResultListing: pass to Release with Listing
	Err     error    // ResultWatchError, ResultOpenError
}

const (
	numWorkers       = 2
	ringCapacity     = 1024
	defaultDebounce  = 50 * time.Millisecond
	contentQuiet     = 250 * time.Millisecond
	contentMaxWait   = time.Second
	listingPoolBytes = 8 << 20
	listingSlotBytes = 2 << 20
	listingPoolIdle  = 30 * time.Second
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
		t0:       time.Now(),
		ring:     core.NewRing[resMsg](ringCapacity),
		ids:      make(map[uint32]*idState),
		dirtyIDs: make(map[uint32]*idState),
		slotOf:   make(map[*Listing]int32),
	}
	for i := range s.parks {
		s.parks[i] = core.NewParker()
	}
	prewarmScratch(numWorkers + 1) // the workers and the watcher's stats may overlap
	s.ctlPark = core.NewParker()
	s.timer = time.AfterFunc(time.Hour, s.kick)
	s.timer.Stop()
	for i := 0; i < numWorkers; i++ {
		s.wg.Add(1)
		go s.worker(i)
	}
	s.wg.Add(1)
	go s.control()
	return s
}

// state returns id's state, creating it. s.mu must be held.
func (s *Service) state(id uint32) *idState {
	st := s.ids[id]
	if st == nil {
		st = &idState{id: id, latest: -1, resortSlot: -1}
		s.ids[id] = st
	}
	return st
}

// List requests a sorted listing of path for id and returns the request
// number. It supersedes id's previous request. The result arrives through
// Poll.
func (s *Service) List(id uint32, path string, key SortKey, desc bool) (req uint64) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0
	}
	st := s.state(id)
	st.user = true
	req = s.enqueueLocked(id, st, path, key, desc, false)
	s.mu.Unlock()
	return req
}

func (s *Service) enqueueLocked(id uint32, st *idState, path string, key SortKey, desc, refresh bool) uint64 {
	s.dropResortLocked(st)
	st.resortRefresh = false
	s.nextReq++
	st.req, st.path, st.key, st.desc, st.refresh = s.nextReq, path, key, desc, refresh
	if !st.pending {
		st.pending = true
		s.queue = append(s.queue, id)
		s.wakeWorkerLocked()
	}
	return st.req
}

// wakeWorkerLocked wakes one parked worker, if there is one: a worker that is
// busy looks at the queue again when it finishes. s.mu held.
func (s *Service) wakeWorkerLocked() {
	for i := range s.idle {
		if s.idle[i] {
			s.idle[i] = false
			s.parks[i].Unpark()
			return
		}
	}
}

// invalidateLocked drops id's list request: a queued job will not run (its
// queue entry is skipped), a running or already finished one is discarded
// when it reaches the ring or Poll.
func (s *Service) invalidateLocked(st *idState) {
	s.dropResortLocked(st)
	delete(s.dirtyIDs, st.id)
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
		delete(s.ids, id)
	}
	s.mu.Unlock()
	s.kick()
}

// Watch makes the Service relist id (with its last List parameters) when
// the directory path changes on disk. Structural changes coalesce for 50 ms;
// content changes wait for 250 ms quiet, bounded by one second.
// A previous watch of id is replaced. Setting up the OS watch happens off
// the calling thread.
func (s *Service) Watch(id uint32, path string) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	delete(s.dirtyIDs, id)
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
	delete(s.dirtyIDs, id)
	s.wgens++
	st.watched, st.wactive, st.dirty, st.wgen = false, false, false, s.wgens
	s.ops = append(s.ops, ctlOp{id: id, gen: st.wgen})
}

func (s *Service) kick() { s.ctlPark.Unpark() }

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
		s.werrs = append(s.werrs, pendingResult{result: Result{Kind: ResultOpenError, ID: id, Err: errOpenBusy}})
		s.mu.Unlock()
		s.signal()
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
			s.werrs = append(s.werrs, pendingResult{result: Result{Kind: ResultOpenError, ID: id, Err: err}})
		}
		s.mu.Unlock()
		if report {
			s.signal()
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
		s.pooledBytes -= l.bufferBytes()
		l.Reset()
		return l, slot
	}
	l := new(Listing)
	slot := int32(len(s.all))
	s.all = append(s.all, l)
	s.busy = append(s.busy, true)
	s.pins = append(s.pins, 0)
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
	if s.pins[slot] == 0 {
		s.recycleLocked(slot)
	}
}

func (s *Service) unpinLocked(slot int32) {
	s.pins[slot]--
	if s.pins[slot] == 0 && !s.busy[slot] {
		s.recycleLocked(slot)
	}
}

func (s *Service) recycleLocked(slot int32) {
	l := s.all[slot]
	s.recentBytes[s.recentPos] = len(l.Entries)*24 + len(l.Names) + len(l.Order)*4
	s.recentPos = (s.recentPos + 1) % len(s.recentBytes)
	recent := s.recentBytes
	slices.Sort(recent[:])
	working := recent[len(recent)*3/4]
	budget := min(256<<20, max(listingPoolBytes, 4*working))
	limit := min(budget, max(listingSlotBytes, 2*working))
	// A one-off large folder does not set the retained working set forever.
	// Repeated large listings warm normally, including the 1M-entry bench.
	for _, spare := range s.free {
		x := s.all[spare]
		if n := x.bufferBytes(); n > limit || s.pooledBytes+n > budget {
			s.pooledBytes -= n
			x.Entries, x.Names, x.Order = nil, nil, nil
		}
	}
	if n := l.bufferBytes(); n > limit || n > budget-s.pooledBytes {
		l.Entries, l.Names, l.Order = nil, nil, nil
	}
	l.Reset()
	s.pooledBytes += l.bufferBytes()
	s.free = append(s.free, slot)
	if s.pooledBytes > listingPoolBytes {
		s.poolDue = time.Now().Add(listingPoolIdle)
		s.kick()
	} else {
		s.poolDue = time.Time{}
	}
}

// trimPoolLocked returns an idle pool to the ordinary navigation budget.
// Live leases and pinned snapshots are never touched.
func (s *Service) trimPoolLocked() {
	for _, slot := range s.free {
		l := s.all[slot]
		if n := l.bufferBytes(); n > listingSlotBytes || s.pooledBytes > listingPoolBytes {
			s.pooledBytes -= n
			l.Entries, l.Names, l.Order = nil, nil, nil
		}
	}
	s.recentBytes = [9]int{}
	s.poolDue = time.Time{}
}

// SetWake changes the result notification target after a window is created.
// The callback must not block and may be called from any goroutine.
func (s *Service) SetWake(wake func()) {
	if wake == nil {
		wake = func() {}
	}
	s.mu.Lock()
	s.wake = wake
	s.mu.Unlock()
}

func (s *Service) signal() {
	s.mu.Lock()
	wake, closed := s.wake, s.closed
	s.mu.Unlock()
	if !closed {
		wake()
	}
}

// Resort requests a new order of a held listing without reading the directory.
// The caller keeps its read-only listing and lease until replacing it. A worker
// copies it into a pooled result, so the displayed order never changes mid-frame.
// A stale or foreign nonzero lease is rejected (request 0); lease 0 accepts an
// immutable caller-owned listing, which must stay unchanged until the result.
func (s *Service) Resort(id uint32, l *Listing, lease uint32, key SortKey, desc bool) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || l == nil {
		return 0
	}
	slot, own := s.slotOf[l]
	if lease != 0 && (!own || !s.busy[slot] || s.lease[slot] != lease) {
		return 0
	}
	if own && (lease == 0 || !s.busy[slot] || s.lease[slot] != lease) {
		return 0
	}
	if own {
		s.pins[slot]++
	} else {
		slot = -1
	}
	st := s.state(id)
	st.user = true
	if st.path == "" {
		st.path = st.watchPath
	}
	fresh := own && st.latest == slot && st.latestLease == lease
	refresh := st.watched && (st.dirty || st.pending || st.running || st.resortRefresh || !fresh && st.latest >= 0)
	req := s.enqueueLocked(id, st, st.path, key, desc, false)
	st.resort, st.resortSlot = l, slot
	st.resortRefresh = refresh
	return req
}

func (s *Service) dropResortLocked(st *idState) {
	if st.resort != nil && st.resortSlot >= 0 {
		s.unpinLocked(st.resortSlot)
	}
	st.resort, st.resortSlot = nil, -1
}

func (s *Service) syncListingLocked(id uint32, st *idState, slot int32) {
	if !st.watched || st.path != st.watchPath {
		return
	}
	if s.w != nil {
		if _, ok := s.w.(listingWatcher); !ok {
			return
		}
	}
	if st.syncReq == st.req && st.syncGen == st.wgen {
		return
	}
	st.syncReq, st.syncGen = st.req, st.wgen
	s.pins[slot]++
	s.ops = append(s.ops, ctlOp{sync: true, id: id, gen: st.wgen, slot: slot, req: st.req})
	s.kick()
}

func (s *Service) worker(i int) {
	defer s.wg.Done()
	park := s.parks[i]
	for {
		s.mu.Lock()
		for s.qhead == len(s.queue) && !s.closed {
			s.idle[i] = true
			s.mu.Unlock()
			ok := park.Park()
			s.mu.Lock()
			s.idle[i] = false // a wake from Close or a stale token leaves it set
			if !ok {          // the Parker was closed by Close
				s.mu.Unlock()
				return
			}
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
		if st == nil || !st.pending { // cancelled or invalidated while queued
			s.mu.Unlock()
			continue
		}
		st.pending, st.running = false, true
		source, sourceSlot := st.resort, st.resortSlot
		st.resort, st.resortSlot = nil, -1
		path, key, desc, req := st.path, st.key, st.desc, st.req
		s.seq++
		startSeq := s.seq
		l, slot := s.getListingLocked()
		s.mu.Unlock()

		// The directory's mtime before the listing begins, whatever the
		// watch state: Watch may be requested after the listing is shown.
		var mt int64
		var mtOK bool
		if source == nil {
			mt, mtOK = dirMTime(path)
			l.Err = s.listFn(path, l)
			Sort(l, key, desc)
		} else {
			l.Entries = append(l.Entries, source.Entries...)
			l.Names = append(l.Names, source.Names...)
			l.Err, l.sorted, l.sortKey, l.desc = source.Err, source.sorted, source.sortKey, source.desc
			if l.sorted && l.sortKey == key {
				l.Order = append(l.Order, source.Order...)
				l.reverse(desc)
			} else {
				Sort(l, key, desc)
			}
		}

		s.mu.Lock()
		if source != nil && sourceSlot >= 0 {
			s.unpinLocked(sourceSlot)
		}
		stale := s.closed || s.ids[id] != st || st.req != req
		if stale {
			s.freeSlotLocked(slot)
		} else {
			st.running = false
			st.latest, st.latestLease, st.latestPath = slot, s.lease[slot], path
			if source == nil {
				st.lastPath, st.lastMT, st.lastSeq, st.lastOK = path, mt, startSeq, mtOK && l.Err == nil
				s.gapLocked(st)
			}
			if source == nil || st.syncGen != st.wgen {
				s.syncListingLocked(id, st, slot)
			}
		}
		s.mu.Unlock()
		if stale {
			continue
		}
		msg := resMsg{id: id, slot: slot, req: req}
		s.mu.Lock()
		for !s.closed && !s.ring.TryPush(msg) {
			s.ringWait[i] = true
			s.mu.Unlock()
			park.Park()
			s.mu.Lock()
			s.ringWait[i] = false
		}
		closed := s.closed
		if closed {
			s.freeSlotLocked(slot)
		}
		s.mu.Unlock()
		if closed {
			return
		}
		s.signal()
	}
}

// control applies watches and synchronizes their file sets off the UI thread;
// opening handles and registering watches may block on the file system.
func (s *Service) control() {
	defer s.wg.Done()
	for {
		if !s.ctlPark.Park() {
			return
		}
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if closed {
			return
		}
		for {
			s.mu.Lock()
			if len(s.ops) == 0 {
				s.mu.Unlock()
				break
			}
			op := s.ops[0]
			copy(s.ops, s.ops[1:])
			s.ops[len(s.ops)-1] = ctlOp{}
			s.ops = s.ops[:len(s.ops)-1]
			if (op.add || op.sync) && !s.currentLocked(op.id, op.gen) { // replaced or removed meanwhile
				if op.sync {
					s.unpinLocked(op.slot)
				}
				s.mu.Unlock()
				continue
			}
			w, closed := s.w, s.closed
			var listing *Listing
			if op.sync {
				listing = s.all[op.slot]
			}
			s.mu.Unlock()
			if closed {
				if op.sync {
					s.mu.Lock()
					s.unpinLocked(op.slot)
					s.mu.Unlock()
				}
				return
			}
			if w == nil {
				if !op.add {
					if op.sync {
						s.mu.Lock()
						s.unpinLocked(op.slot)
						s.mu.Unlock()
					}
					continue
				}
				var err error
				if w, err = s.newWatcher(s.kick); err != nil {
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
			if op.sync {
				if lw, ok := w.(listingWatcher); ok {
					lw.syncListing(op.id, op.gen, op.req, listing)
				}
				s.mu.Lock()
				s.unpinLocked(op.slot)
				s.mu.Unlock()
				continue
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
				var err error
				if lw, ok := w.(listingWatcher); ok {
					err = lw.addListingWatch(op.id, op.path, op.gen)
				} else {
					err = w.add(op.id, op.path)
				}
				if err != nil {
					s.watchFailed(op.id, op.gen, err)
				} else {
					s.watchActive(op.id, op.gen, op.path)
					if _, ok := w.(listingWatcher); ok {
						s.mu.Lock()
						if s.currentLocked(op.id, op.gen) {
							st := s.ids[op.id]
							slot := st.latest
							if slot >= 0 && s.busy[slot] && s.lease[slot] == st.latestLease && st.latestPath == op.path {
								s.syncListingLocked(op.id, st, slot)
							} else if !st.pending && !st.running && (st.syncReq != st.req || st.syncGen != st.wgen) {
								path := st.path
								if path == "" {
									path = op.path
								}
								s.enqueueLocked(op.id, st, path, st.key, st.desc, !st.user)
							}
						}
						s.mu.Unlock()
					}
				}
			}
		}
		s.processChanges()
	}
}

// currentLocked reports whether gen is id's latest watch generation and the
// watch is wanted. s.mu held.
func (s *Service) currentLocked(id uint32, gen uint64) bool {
	st := s.ids[id]
	return !s.closed && st != nil && st.watched && st.wgen == gen
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
		s.kick()
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
	s.dirtyLocked(st, time.Now(), false)
	s.kick()
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
	delete(s.dirtyIDs, id)
	s.werrs = append(s.werrs, pendingResult{result: Result{Kind: ResultWatchError, ID: id, Err: err}, gen: gen})
	s.mu.Unlock()
	s.signal()
}

// processChanges owns the watch drain and debounce clock. Neither raw changes
// nor timer expirations wake the UI: only a result does.
func (s *Service) processChanges() {
	s.mu.Lock()
	w, closed := s.w, s.closed
	s.mu.Unlock()
	if closed {
		return
	}
	if w != nil {
		s.changes = w.drain(s.changes[:0])
	}
	now := time.Now()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	notify := false
	for _, c := range s.changes {
		switch c.Kind {
		case ChangeDir, ChangeContent:
			s.markDirtyLocked(c.ID, now, c.Kind == ChangeContent)
		case ChangeOverflow:
			for id := range s.ids {
				s.markDirtyLocked(id, now, false)
			}
		case ChangeGone:
			st := s.ids[c.ID]
			if st == nil || !st.watched || !st.wactive {
				continue
			}
			if st.path == st.watchPath || st.path == "" {
				s.invalidateLocked(st)
			}
			s.unwatchLocked(c.ID, st)
			s.werrs = append(s.werrs, pendingResult{result: Result{Kind: ResultGone, ID: c.ID}, gen: st.wgen})
			s.kick()
			notify = true
		}
	}
	s.changes = s.changes[:0]
	var next time.Duration
	for id, st := range s.dirtyIDs {
		if wait := st.dirtyDue.Sub(now); wait > 0 {
			if next == 0 || wait < next {
				next = wait
			}
			continue
		}
		delete(s.dirtyIDs, id)
		st.dirty = false
		if st.path == "" {
			st.path = st.watchPath
		}
		s.enqueueLocked(id, st, st.path, st.key, st.desc, !st.user)
	}
	if !s.poolDue.IsZero() {
		if wait := s.poolDue.Sub(now); wait <= 0 {
			s.trimPoolLocked()
		} else if next == 0 || wait < next {
			next = wait
		}
	}
	if next > 0 {
		s.timer.Reset(next)
	} else {
		s.timer.Stop()
	}
	s.mu.Unlock()
	if notify {
		s.signal()
	}
}

// Poll appends finished results to dst. Call it on the UI thread after a
// wake; pass dst[:0] of a reused slice to allocate nothing once warm.
func (s *Service) Poll(dst []Result) []Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.werrs {
		st := s.ids[p.result.ID]
		if p.gen == 0 || st != nil && st.wgen == p.gen {
			dst = append(dst, p.result)
		}
	}
	clear(s.werrs)
	s.werrs = s.werrs[:0]
	popped := false
	for {
		m, ok := s.ring.Pop()
		if !ok {
			break
		}
		popped = true
		st := s.ids[m.id]
		if st == nil || st.req != m.req {
			s.freeSlotLocked(m.slot)
			continue
		}
		refresh := st.refresh
		st.user = false
		dst = append(dst, Result{Kind: ResultListing, ID: m.id, Req: m.req, Refresh: refresh, Listing: s.all[m.slot], Lease: s.lease[m.slot]})
		if st.resortRefresh {
			st.resortRefresh = false
			s.markDirtyLocked(m.id, time.Now(), false)
			s.kick()
		}
	}
	if popped {
		for i, waiting := range s.ringWait {
			if waiting {
				s.parks[i].Unpark()
			}
		}
	}
	return dst
}

func (s *Service) markDirtyLocked(id uint32, now time.Time, content bool) {
	if st := s.ids[id]; st != nil && st.watched && st.wactive {
		s.dirtyLocked(st, now, content)
	}
}

func (s *Service) dirtyLocked(st *idState, now time.Time, content bool) {
	if !st.dirty {
		st.dirty, st.dirtyAt, st.structural = true, now, !content
		s.dirtyIDs[st.id] = st
		st.dirtyDue = now.Add(s.debounce)
		if content {
			st.dirtyDue = now.Add(contentQuiet)
		}
	} else if !content && !st.structural {
		st.structural = true
		st.dirtyDue = minTime(st.dirtyDue, now.Add(s.debounce))
	}
	if content && !st.structural {
		st.dirtyDue = minTime(now.Add(contentQuiet), st.dirtyAt.Add(contentMaxWait))
	}
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
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
	for _, st := range s.ids {
		s.dropResortLocked(st)
	}
	for _, op := range s.ops {
		if op.sync {
			s.unpinLocked(op.slot)
		}
	}
	clear(s.ops)
	s.ops = s.ops[:0]
	clear(s.dirtyIDs)
	for {
		m, ok := s.ring.Pop()
		if !ok {
			break
		}
		s.freeSlotLocked(m.slot)
	}
	for _, p := range s.parks {
		p.Unpark()
	}
	s.ctlPark.Unpark()
	s.mu.Unlock()
	stopped := make(chan struct{})
	go func() { s.wg.Wait(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(closeWait):
	}
	s.timer.Stop()
	for _, p := range s.parks { // a goroutine detached above returns from its Park
		p.Close()
	}
	s.ctlPark.Close()
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
