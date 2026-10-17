//go:build windows

package fs

import (
	"errors"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

// ReadDirectoryChangesW watcher.
//
// One worker goroutine blocks in WaitForMultipleObjects on a control event
// plus one manual-reset event per outstanding overlapped
// ReadDirectoryChangesW (no completion port). The wait set is limited to
// MAXIMUM_WAIT_OBJECTS = 64, so at most 63 watches fit; a directory takes
// one, plus a shared one for its parent (below), so 31 to 62 directories
// can be watched at once and add reports an error beyond that. Pilot needs
// one per pane.
//
// Notification contents are not parsed: any completion of a directory's
// read means "this directory changed, relist it" (ChangeDir). A completion
// with zero bytes or ERROR_NOTIFY_ENUM_DIR means the kernel buffer
// overflowed and events were lost (ChangeOverflow).
//
// Gone. A watched directory that is deleted completes its own pending read
// (ERROR_ACCESS_DENIED, or a DeletePending handle). A directory that is
// renamed or moved away reports nothing on its own handle, so each watched
// directory also takes a ref on a watch of its parent for directory-name
// changes (FILE_NOTIFY_CHANGE_DIR_NAME only: it is quiet). Any completion
// of a parent watch re-checks that its watched children still exist
// (GetFileAttributesW) and reports ChangeGone for those that don't. The
// parent watch is best effort (the root has no parent, the parent may be
// unreadable or the wait set full); as a backstop the worker re-checks
// every watched path each winLivenessMs while otherwise idle, 0.2 wakeups
// per second, inside the idle budget. A directory renamed and replaced by
// another of the same name is not noticed.
//
// The directory handles are opened with FILE_SHARE_DELETE so a watch never
// stops the user from deleting or renaming the directory (renaming one of
// its ancestors is refused by Windows while any handle is open below it,
// for every watcher).
//
// Ownership and locking. The worker goroutine owns the watch table and
// every handle, event and OVERLAPPED buffer once a watch is handed to it:
// it alone issues reads, reaps them, cancels them and closes the handles,
// and only between waits, so a handle is never closed while a wait uses it.
// A cancelled read's buffer stays referenced until the kernel has completed
// it (it is Go heap memory, which never moves); if a cancellation does not
// complete within winCancelWait the watch and its handles are leaked on
// purpose instead of freeing memory the kernel may still write.
//
// add and remove are synchronous commands to the worker (open and first read
// happen on the caller, which may be slow on network shares; the worker only
// links the watch in). Two mutexes, both held only for in-memory work and
// never across a system call that can block: cmu guards the command queue
// and closed; pmu guards the pending changes. drain, which runs on the UI
// thread, takes only pmu.

const (
	winMaxWatches = 63
	winNotifyBuf  = 32 << 10 // bytes; DWORD-aligned by construction (uint64 array)
	winLivenessMs = 5000

	// winCancelWait bounds the wait for a cancelled read to complete.
	winCancelWait = 10000 // ms

	// winnt.h:6197-6201.
	winNotifyDir    = 0x1 | 0x2 | 0x4 | 0x8 | 0x10 // FILE_NAME | DIR_NAME | ATTRIBUTES | SIZE | LAST_WRITE
	winNotifyParent = 0x2                          // FILE_NOTIFY_CHANGE_DIR_NAME

	winStatusPending = 0x103
	winWaitObject0   = 0
	winWaitTimeout   = 0x102
	winWaitFailed    = 0xFFFFFFFF
	winInfinite      = 0xFFFFFFFF
)

// winWatch is one outstanding ReadDirectoryChangesW: a watched directory,
// or the shared watch of a parent. ov is first so the structure (and its
// buffer) stay 8-byte aligned; it lives on the heap for as long as the
// kernel may write to it.
type winWatch struct {
	ov     syscall.Overlapped
	buf    [winNotifyBuf / 8]uint64
	h      uintptr // directory handle
	filter uint32
	armed  bool     // a ReadDirectoryChangesW is outstanding or unreaped
	dead   bool     // to be swept: stopped and removed from the table
	path   []uint16 // NUL-terminated, \\?\-prefixed

	// A watched directory.
	id     uint32
	parent *winWatch // its parent watch; nil when it has none

	// A parent watch.
	isParent bool
	refs     int // watched directories that rely on it
}

// OVERLAPPED is {Internal, InternalHigh, Offset|Pointer, hEvent} (minwinbase.h:19-30).
var (
	_ [unsafe.Sizeof(syscall.Overlapped{}) - 32]struct{}
	_ [32 - unsafe.Sizeof(syscall.Overlapped{})]struct{}
	_ [unsafe.Offsetof(syscall.Overlapped{}.HEvent) - 24]struct{}
	_ [24 - unsafe.Offsetof(syscall.Overlapped{}.HEvent)]struct{}
	// FILE_STANDARD_INFO (winbase.h:3152-3158).
	_ [unsafe.Offsetof(winStandardInfo{}.DeletePending) - 20]struct{}
	_ [20 - unsafe.Offsetof(winStandardInfo{}.DeletePending)]struct{}
	_ [unsafe.Sizeof(winStandardInfo{}) - 24]struct{}
	_ [24 - unsafe.Sizeof(winStandardInfo{})]struct{}
)

// winStandardInfo mirrors FILE_STANDARD_INFO.
type winStandardInfo struct {
	AllocationSize int64
	EndOfFile      int64
	NumberOfLinks  uint32
	DeletePending  uint8
	Directory      uint8
}

// winCmd is an add or a remove, executed by the worker. For an add, x is the
// opened and armed directory watch and cand an optional opened parent watch
// (both already issued their first read); the worker owns them from the
// moment the command is queued.
type winCmd struct {
	id    uint32
	x     *winWatch // nil: remove id
	cand  *winWatch
	pp    []uint16 // the parent's path
	reply chan error
}

var errWinClosed = errors.New("fs: watcher closed")

type winWatcher struct {
	wake func()
	ctl  uintptr // auto-reset event: a command is queued, or close
	done chan struct{}

	cmu    sync.Mutex
	cmds   []*winCmd
	closed bool

	pmu  sync.Mutex
	pend []Change

	// Worker-only state.
	ws   []*winWatch // directory watches and parent watches
	std  winStandardInfo
	grew bool // a change was queued during this round

	// Published by the worker for tests and diagnostics.
	nDirs, nAll atomic.Int32
}

func newWatcher(wake func()) (watcher, error) {
	ctl, _, e := syscall.SyscallN(pCreateEventW, 0, 0, 0, 0)
	if ctl == 0 {
		return nil, winPathErr("fs.watch", "", e)
	}
	w := &winWatcher{wake: wake, ctl: ctl, done: make(chan struct{})}
	go w.run()
	return w, nil
}

// winOpenWatch opens the directory p (NUL-terminated, \\?\-prefixed) and
// issues its first read.
func winOpenWatch(p []uint16, filter uint32) (*winWatch, syscall.Errno) {
	h, _, e := syscall.SyscallN(pCreateFileW, uintptr(unsafe.Pointer(&p[0])),
		winFileListDirectory, winShareAll, 0, winOpenExisting, winFlagBackupSemantics|winFlagOverlapped, 0)
	if h == winInvalidHandle {
		return nil, e
	}
	var std winStandardInfo // CreateFile with BACKUP_SEMANTICS also opens plain files
	if r, _, _ := syscall.SyscallN(pGetFileInformationByHandleEx, h, winClassStandardInfo,
		uintptr(unsafe.Pointer(&std)), unsafe.Sizeof(std)); uint32(r) != 0 && std.Directory == 0 {
		winCloseHandle(h)
		return nil, winErrDirectory
	}
	ev, _, e := syscall.SyscallN(pCreateEventW, 0, 1, 0, 0) // manual-reset
	if ev == 0 {
		winCloseHandle(h)
		return nil, e
	}
	x := &winWatch{h: h, path: p, filter: filter}
	x.ov.HEvent = syscall.Handle(ev)
	if e := x.arm(); e != 0 {
		winCloseHandle(h)
		winCloseHandle(ev)
		return nil, e
	}
	return x, 0
}

// winParentPath returns the NUL-terminated parent of the directory path p
// (NUL-terminated, \\?\-prefixed), or false for a root.
func winParentPath(p []uint16) ([]uint16, bool) {
	n := len(p) - 1
	for n > 0 && winIsSep(p[n-1]) {
		n--
	}
	i := n - 1
	for i >= 0 && !winIsSep(p[i]) {
		i--
	}
	if i < 4 { // at or inside the \\?\ prefix: p is a root
		return nil, false
	}
	end := i
	if p[end-1] == ':' { // C:\ keeps its separator
		end++
	}
	out := make([]uint16, end+1)
	copy(out, p[:end])
	return out, true
}

// winSamePath compares two directory paths, folding ASCII case.
func winSamePath(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if winLower(a[i]) != winLower(b[i]) {
			return false
		}
	}
	return true
}

// submit queues c for the worker and waits for its answer.
func (w *winWatcher) submit(c *winCmd) error {
	c.reply = make(chan error, 1)
	w.cmu.Lock()
	if w.closed {
		w.cmu.Unlock()
		return errWinClosed
	}
	w.cmds = append(w.cmds, c)
	syscall.SyscallN(pSetEvent, w.ctl) // ctl stays open while !closed
	w.cmu.Unlock()
	select {
	case err := <-c.reply:
		return err
	case <-w.done:
		// The worker answers every queued command before it exits.
		select {
		case err := <-c.reply:
			return err
		default:
			return errWinClosed
		}
	}
}

func (w *winWatcher) add(id uint32, path string) error {
	s := winGetScratch()
	dir, e := s.winDirPath(path)
	var p []uint16
	if e == 0 {
		p = append([]uint16(nil), dir...)
	}
	winPutScratch(s)
	if e != 0 {
		return winPathErr("fs.watch", path, e)
	}
	x, e := winOpenWatch(p, winNotifyDir)
	if e != 0 {
		return winPathErr("fs.watch", path, e)
	}
	x.id = id
	c := &winCmd{id: id, x: x}
	// The parent watch is optional. It is opened here, off the worker (the
	// open can be slow); the worker uses it unless one already exists.
	if pp, ok := winParentPath(p); ok {
		if cand, e := winOpenWatch(pp, winNotifyParent); e == 0 {
			cand.isParent = true
			c.cand, c.pp = cand, pp
		}
	}
	if err := w.submit(c); err != nil {
		if err == errWinClosed {
			// Never queued, or answered by the exiting worker, which has
			// already stopped them; stop is idempotent.
			x.stop()
			if c.cand != nil {
				c.cand.stop()
			}
			return err
		}
		return err
	}
	return nil
}

func (w *winWatcher) remove(id uint32) {
	w.submit(&winCmd{id: id})
	w.pmu.Lock()
	w.dropPending(id)
	w.pmu.Unlock()
}

func (w *winWatcher) drain(dst []Change) []Change {
	w.pmu.Lock()
	dst = append(dst, w.pend...)
	w.pend = w.pend[:0]
	w.pmu.Unlock()
	return dst
}

func (w *winWatcher) close() {
	w.cmu.Lock()
	if w.closed {
		w.cmu.Unlock()
		<-w.done
		return
	}
	w.closed = true
	syscall.SyscallN(pSetEvent, w.ctl)
	w.cmu.Unlock()
	<-w.done // the worker stops every watch and closes ctl before it exits
}

// arm issues the (next) overlapped ReadDirectoryChangesW. It returns the
// error code of a synchronous failure.
func (x *winWatch) arm() syscall.Errno {
	r, _, e := syscall.SyscallN(pReadDirectoryChangesW, x.h, uintptr(unsafe.Pointer(&x.buf[0])), winNotifyBuf,
		0, // bWatchSubtree = FALSE
		uintptr(x.filter), 0, uintptr(unsafe.Pointer(&x.ov)), 0)
	if uint32(r) == 0 {
		if e == 0 {
			e = winErrInvalidParam
		}
		return e
	}
	x.armed = true
	return 0
}

// rearm resets the event and issues the next read. The kernel resets the
// event when a read starts; doing it explicitly as well means a stale signal
// can never spin the worker.
func (x *winWatch) rearm() syscall.Errno {
	syscall.SyscallN(pResetEvent, uintptr(x.ov.HEvent))
	return x.arm()
}

// pending reports whether the read is still outstanding (HasOverlappedIoCompleted).
func (x *winWatch) pending() bool {
	return x.armed && uint32(atomic.LoadUintptr(&x.ov.Internal)) == winStatusPending
}

// winLeaked keeps the memory of watches whose cancellation did not complete
// alive for the life of the process: the kernel may still write to them.
var (
	winLeakMu sync.Mutex
	winLeaked []*winWatch
)

// stop cancels the outstanding read, waits (bounded) until the kernel is
// done with the buffer, and closes the handles. It is idempotent. Only the
// goroutine that owns x calls it, and never while a wait uses x's event.
func (x *winWatch) stop() {
	if x.h == winInvalidHandle {
		return
	}
	if x.pending() {
		syscall.SyscallN(pCancelIoEx, x.h, uintptr(unsafe.Pointer(&x.ov)))
		r, _, _ := syscall.SyscallN(pWaitForSingleObject, uintptr(x.ov.HEvent), winCancelWait)
		if uint32(r) != winWaitObject0 && x.pending() {
			// Not completed: neither free the buffer nor close the handles.
			winLeakMu.Lock()
			winLeaked = append(winLeaked, x)
			winLeakMu.Unlock()
			x.h, x.armed = winInvalidHandle, false
			return
		}
	}
	x.armed = false
	winCloseHandle(x.h)
	winCloseHandle(uintptr(x.ov.HEvent))
	x.h = winInvalidHandle
}

// dropPending removes id's queued change; w.pmu is held.
func (w *winWatcher) dropPending(id uint32) {
	for i := range w.pend {
		if w.pend[i].ID == id {
			w.pend = append(w.pend[:i], w.pend[i+1:]...)
			return
		}
	}
}

// post queues a change for id, coalescing with an earlier one (Gone beats
// Overflow beats Dir). A change coalesced into an already queued one needs
// no new wake: the queued one was announced when it was posted.
func (w *winWatcher) post(id uint32, k ChangeKind) {
	w.pmu.Lock()
	defer w.pmu.Unlock()
	for i := range w.pend {
		if w.pend[i].ID == id {
			if k > w.pend[i].Kind {
				w.pend[i].Kind = k
			}
			return
		}
	}
	w.pend = append(w.pend, Change{ID: id, Kind: k})
	w.grew = true
}

// reap collects the result of x's finished read.
func (x *winWatch) reap() (ok bool, n uint32, e syscall.Errno) {
	r, _, e := syscall.SyscallN(pGetOverlappedResult, x.h, uintptr(unsafe.Pointer(&x.ov)), uintptr(unsafe.Pointer(&n)), 0)
	x.armed = false
	return uint32(r) != 0, n, e
}

// completeDir handles a finished read of a watched directory and re-arms
// it, or marks the watch dead (and queues ChangeGone) when the directory is
// gone. Worker only.
func (w *winWatcher) completeDir(x *winWatch) {
	ok, n, e := x.reap()
	switch {
	case ok && n > 0:
		w.post(x.id, ChangeDir)
	case ok || e == winErrNotifyEnumDir:
		w.post(x.id, ChangeOverflow) // zero bytes: the buffer overflowed
	default:
		// ERROR_ACCESS_DENIED: the directory is delete-pending or gone;
		// ERROR_OPERATION_ABORTED, ERROR_INVALID_HANDLE, ERROR_NETNAME_DELETED...:
		// the watch can't continue.
		w.gone(x)
		return
	}
	if w.deletePending(x) || x.rearm() != 0 {
		w.gone(x)
	}
}

// completeParent handles a finished read of a parent watch: a directory was
// created, deleted or renamed in the parent, so its watched children may be
// gone. Worker only.
func (w *winWatcher) completeParent(x *winWatch) {
	ok, _, e := x.reap()
	over := !ok && e != winErrNotifyEnumDir // the parent itself is gone or unreadable
	if !over && x.rearm() != 0 {
		over = true
	}
	for _, c := range w.ws {
		if c.parent == x && !c.dead {
			w.checkExists(c)
		}
	}
	if over {
		x.dead = true
	}
}

// checkExists queues ChangeGone for x when its path no longer exists.
func (w *winWatcher) checkExists(x *winWatch) {
	a, _, _ := syscall.SyscallN(pGetFileAttributesW, uintptr(unsafe.Pointer(&x.path[0])))
	if uint32(a) == winInvalidFileAttributes {
		w.gone(x)
	}
}

func (w *winWatcher) gone(x *winWatch) {
	w.post(x.id, ChangeGone)
	x.dead = true
}

// deletePending asks the directory handle whether the directory has been
// deleted while we hold it open.
func (w *winWatcher) deletePending(x *winWatch) bool {
	r, _, _ := syscall.SyscallN(pGetFileInformationByHandleEx, x.h, winClassStandardInfo,
		uintptr(unsafe.Pointer(&w.std)), unsafe.Sizeof(w.std))
	return uint32(r) != 0 && w.std.DeletePending != 0
}

// scan reaps every completed read; liveness additionally checks that each
// watched path still exists. Worker only.
func (w *winWatcher) scan(liveness bool) {
	for _, x := range w.ws {
		switch {
		case x.dead:
		case !x.pending():
			if x.isParent {
				w.completeParent(x)
			} else {
				w.completeDir(x)
			}
		case liveness && !x.isParent:
			w.checkExists(x)
		}
	}
	w.sweep()
}

// findParent returns the live parent watch of directory path pp. Worker only.
func (w *winWatcher) findParent(pp []uint16) *winWatch {
	for _, o := range w.ws {
		if o.isParent && !o.dead && winSamePath(o.path, pp) {
			return o
		}
	}
	return nil
}

// sweep stops and unlinks every dead watch, and parent watches nothing
// relies on any more. Worker only.
func (w *winWatcher) sweep() {
	for _, x := range w.ws {
		if x.dead && !x.isParent && x.parent != nil {
			x.parent.refs--
			x.parent = nil
		}
	}
	for _, x := range w.ws {
		if x.isParent && x.refs <= 0 {
			x.dead = true
		}
	}
	for _, x := range w.ws { // children of a parent that died on its own
		if !x.isParent && !x.dead && x.parent != nil && x.parent.dead {
			x.parent.refs--
			x.parent = nil
		}
	}
	j := 0
	for _, x := range w.ws {
		if x.dead {
			x.stop()
			continue
		}
		w.ws[j] = x
		j++
	}
	clear(w.ws[j:])
	w.ws = w.ws[:j]
	w.publishCounts()
}

func (w *winWatcher) publishCounts() {
	dirs := 0
	for _, x := range w.ws {
		if !x.isParent {
			dirs++
		}
	}
	w.nDirs.Store(int32(dirs))
	w.nAll.Store(int32(len(w.ws)))
}

// apply executes one command. Worker only.
func (w *winWatcher) apply(c *winCmd) {
	for _, x := range w.ws { // add replaces an existing id; remove drops it
		if !x.isParent && x.id == c.id {
			x.dead = true
		}
	}
	w.sweep()
	if c.x == nil {
		c.reply <- nil
		return
	}
	if len(w.ws)+1 > winMaxWatches {
		c.x.stop()
		if c.cand != nil {
			c.cand.stop()
		}
		c.reply <- errors.New("fs: too many watched directories (Windows waits on at most 63 watches)")
		return
	}
	var par *winWatch
	if c.cand != nil {
		par = w.findParent(c.pp)
		if par == nil && len(w.ws)+2 <= winMaxWatches {
			w.ws = append(w.ws, c.cand)
			par = c.cand
		} else {
			c.cand.stop() // one exists already, or no room
		}
	}
	if par != nil {
		par.refs++
		c.x.parent = par
	}
	w.ws = append(w.ws, c.x)
	w.publishCounts()
	c.reply <- nil
}

// reject stops the watches of unexecuted commands and answers them "closed".
func (w *winWatcher) reject(cmds []*winCmd) {
	for _, c := range cmds {
		if c.x != nil {
			c.x.stop()
		}
		if c.cand != nil {
			c.cand.stop()
		}
		c.reply <- errWinClosed
	}
}

// takeCmds hands the queued commands to the worker; closed reports close().
func (w *winWatcher) takeCmds(buf []*winCmd) (cmds []*winCmd, closed bool) {
	w.cmu.Lock()
	cmds = append(buf[:0], w.cmds...)
	clear(w.cmds)
	w.cmds = w.cmds[:0]
	closed = w.closed
	w.cmu.Unlock()
	return cmds, closed
}

func (w *winWatcher) run() {
	var hs [winMaxWatches + 1]uintptr
	var cmds []*winCmd
	defer func() {
		// Close: answer the commands that raced with it, stop everything,
		// and only then close the control event and report done.
		w.reject(cmds)
		more, _ := w.takeCmds(nil)
		w.reject(more)
		for _, x := range w.ws {
			x.stop()
		}
		w.ws = nil
		w.publishCounts()
		winCloseHandle(w.ctl)
		close(w.done)
	}()
	for {
		var closed bool
		cmds, closed = w.takeCmds(cmds)
		if closed {
			return
		}
		for _, c := range cmds {
			w.apply(c)
		}

		hs[0] = w.ctl
		n := 1
		for _, x := range w.ws {
			hs[n] = uintptr(x.ov.HEvent)
			n++
		}
		timeout := uintptr(winInfinite)
		if n > 1 {
			timeout = winLivenessMs
		}
		// Only this goroutine closes the events in hs, and only here, never
		// during the wait.
		r, _, _ := syscall.SyscallN(pWaitForMultipleObjects, uintptr(n), uintptr(unsafe.Pointer(&hs[0])), 0, timeout)
		if uint32(r) == winWaitFailed {
			time.Sleep(10 * time.Millisecond) // never a hot loop
		}
		w.grew = false
		w.scan(uint32(r) == winWaitTimeout)
		if w.grew && w.wake != nil {
			w.wake()
		}
	}
}
