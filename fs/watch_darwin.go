//go:build darwin

package fs

import (
	"bytes"
	"errors"
	"os"
	"slices"
	"sync"
	"syscall"
	"unsafe"

	"gonativeui/core"
)

// kqueue watcher.
//
// FSEvents is recursive: a stream on ~ or / delivers a callback for any
// change anywhere below (~/Library churns all day), and every callback is
// a GCD thread wake plus Go runtime wakeups, measured at ~30 process
// wakeups/s for ~ while the exact-path filter dropped nearly all of the
// events. The pane only needs its own directory, so this watcher is
// strictly non-recursive and event-driven: one goroutine blocked in
// kevent, nothing at all happening while the directory is quiet.
//
// What is watched, per directory id:
//
//   - the directory vnode (EVFILT_VNODE, O_EVTONLY): NOTE_WRITE means an
//     entry was added, removed or renamed; NOTE_DELETE / NOTE_RENAME /
//     NOTE_REVOKE (removed, moved away, volume unmounted) mean ChangeGone;
//   - each regular file in it, while the directory has at most
//     maxDirFiles of them (and the process stays under maxTotalFiles open
//     watches): an in-place write, truncate or attribute change touches
//     only the file's vnode, never the directory's, and the listing shows
//     size and modification time. Beyond the caps the directory is
//     watched structurally only. The file set is re-synchronized after
//     every NOTE_WRITE on the directory.
//
// Known gaps:
//
//   - changes to files created and modified within the few milliseconds
//     between the directory event and the file's watch being registered;
//   - in-place changes in directories above the caps;
//   - subdirectories: entries added inside a child do not touch this
//     directory, so a child's displayed modification time can be stale
//     until the next relist;
//   - an ancestor of the directory being renamed (the directory's own vnode
//     does not change).
//
// File descriptors opened with O_EVTONLY do not keep volumes busy or
// files alive.

const (
	oEvtOnly     = 0x8000 // O_EVTONLY
	fcntlGetPath = 50     // F_GETPATH
	maxPathLen   = 1024

	maxDirFiles   = 2048 // regular files watched per directory
	maxTotalFiles = 4096 // across the whole watcher

	noteDirChange = syscall.NOTE_WRITE | syscall.NOTE_EXTEND | syscall.NOTE_ATTRIB | syscall.NOTE_LINK
	noteGone      = syscall.NOTE_DELETE | syscall.NOTE_RENAME | syscall.NOTE_REVOKE
)

type kqFile struct {
	fd   int
	off  uint32
	len  uint16
	next int32
}

type kqDir struct {
	id         uint32
	path       string // as given to add
	canon      string // real path, as reported by F_GETPATH
	fd         int
	files      []kqFile
	names      []byte
	lookup     map[uint64]int32
	spareFiles []kqFile
	spareNames []byte
	wgen       uint64
	lastReq    uint64
	external   bool
	nfiles     int // watches counted against the total budget
}

type kqRef struct {
	d     *kqDir
	index int // -1 for the directory itself
}

type kqWatcher struct {
	wake func()
	kq   int
	done chan struct{}

	smu           sync.Mutex // serializes standalone resync
	lst           Listing    // only standalone watcher users; the Service supplies its own listing
	path          []byte
	registrations []syscall.Kevent_t
	receipts      []syscall.Kevent_t

	mu     sync.Mutex // guards the structures below
	dirs   map[uint32]*kqDir
	byFD   map[int]kqRef
	nfiles int
	closed bool

	pmu     core.Mutex // guards pending (drain never waits for file system work)
	pending []Change

	scrChanged []Change // process's scratch (one caller at a time)
	scrResync  []*kqDir
}

func newWatcher(wake func()) (watcher, error) {
	w, err := newKq(wake)
	if err != nil {
		return nil, err
	}
	go w.loop()
	return w, nil
}

// newKq creates the watcher without its event loop (tests drive process
// themselves to control the order in which events are handled).
func newKq(wake func()) (*kqWatcher, error) {
	kq, err := syscall.Kqueue()
	if err != nil {
		return nil, os.NewSyscallError("kqueue", err)
	}
	syscall.CloseOnExec(kq)
	// The user event wakes the loop for close.
	ev := []syscall.Kevent_t{{Ident: 1, Filter: syscall.EVFILT_USER, Flags: syscall.EV_ADD | syscall.EV_CLEAR}}
	if _, err := kevent(kq, ev, nil); err != nil {
		syscall.Close(kq)
		return nil, os.NewSyscallError("kevent", err)
	}
	w := &kqWatcher{
		wake: wake, kq: kq, done: make(chan struct{}),
		dirs: make(map[uint32]*kqDir), byFD: make(map[int]kqRef),
		pending: make([]Change, 0, 16),
	}
	return w, nil
}

func kevent(kq int, changes, events []syscall.Kevent_t) (int, error) {
	for {
		n, err := syscall.Kevent(kq, changes, events, nil)
		if err != syscall.EINTR {
			return n, err
		}
	}
}

func openEvt(path string, dir bool) (int, error) {
	b := make([]byte, len(path)+1)
	copy(b, path)
	flags := oEvtOnly | syscall.O_CLOEXEC
	if dir {
		flags |= syscall.O_DIRECTORY
	} else {
		flags |= syscall.O_NOFOLLOW
	}
	for {
		fd, _, errno := syscall.Syscall(syscall.SYS_OPEN, uintptr(unsafe.Pointer(&b[0])), uintptr(flags), 0)
		if errno == syscall.EINTR {
			continue
		}
		if errno != 0 {
			return -1, &os.PathError{Op: "open", Path: path, Err: errno}
		}
		return int(fd), nil
	}
}

// fdPath returns the real path of an open descriptor (symlinks resolved,
// on-disk case); it fails once the file is gone.
func fdPath(fd int) (string, error) {
	buf := make([]byte, maxPathLen)
	if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), fcntlGetPath, uintptr(unsafe.Pointer(&buf[0]))); errno != 0 {
		return "", errno
	}
	n := 0
	for n < len(buf) && buf[n] != 0 {
		n++
	}
	return string(buf[:n]), nil
}

// stillAt reports whether d's descriptor still names the directory at its
// path: a moved directory reports a new path through F_GETPATH, a deleted
// one has no entry at the old path (F_GETPATH still answers for it).
func stillAt(d *kqDir) bool {
	if p, err := fdPath(d.fd); err != nil || p != d.canon {
		return false
	}
	var a, b syscall.Stat_t
	return syscall.Fstat(d.fd, &a) == nil && syscall.Stat(d.canon, &b) == nil && a.Dev == b.Dev && a.Ino == b.Ino
}

func (w *kqWatcher) register(fd int, fflags uint32) error {
	ev := []syscall.Kevent_t{{Ident: uint64(fd), Filter: syscall.EVFILT_VNODE, Flags: syscall.EV_ADD | syscall.EV_CLEAR, Fflags: fflags}}
	_, err := kevent(w.kq, ev, nil)
	return err
}

func (w *kqWatcher) add(id uint32, path string) error { return w.addWatch(id, path, 0, false) }

func (w *kqWatcher) addListingWatch(id uint32, path string, gen uint64) error {
	return w.addWatch(id, path, gen, true)
}

func (w *kqWatcher) addWatch(id uint32, path string, gen uint64, external bool) error {
	fd, err := openEvt(path, true)
	if err != nil {
		return err
	}
	canon, err := fdPath(fd)
	if err != nil {
		syscall.Close(fd)
		return &os.PathError{Op: "fcntl(F_GETPATH)", Path: path, Err: err}
	}
	d := &kqDir{id: id, path: path, canon: canon, fd: fd, lookup: make(map[uint64]int32), wgen: gen, external: external}

	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		syscall.Close(fd)
		return errors.New("fs: watcher closed")
	}
	if old := w.dirs[id]; old != nil {
		w.dropDirLocked(old)
	}
	w.dirs[id] = d
	w.byFD[fd] = kqRef{d: d, index: -1}
	err = w.register(fd, noteDirChange|noteGone)
	if err != nil {
		w.dropDirLocked(d)
		w.mu.Unlock()
		return os.NewSyscallError("kevent", err)
	}
	w.mu.Unlock()

	// Directory first, files second: a change in between shows up as a
	// directory event, and resync after it picks the new file up.
	if !external {
		w.resync(d)
	}
	return nil
}

func (w *kqWatcher) remove(id uint32) {
	w.mu.Lock()
	if d := w.dirs[id]; d != nil {
		w.dropDirLocked(d)
	}
	w.mu.Unlock()
	w.pmu.Lock()
	n := 0
	for _, c := range w.pending {
		if c.ID != id {
			w.pending[n] = c
			n++
		}
	}
	w.pending = w.pending[:n]
	w.pmu.Unlock()
}

// dropDirLocked closes d's descriptors (which also removes their kevents)
// and forgets it. w.mu held.
func (w *kqWatcher) dropDirLocked(d *kqDir) {
	for _, f := range d.files {
		if f.fd >= 0 {
			syscall.Close(f.fd)
			delete(w.byFD, f.fd)
		}
	}
	d.files, d.names = nil, nil
	clear(d.lookup)
	w.nfiles -= d.nfiles
	d.nfiles = 0
	if d.fd >= 0 {
		syscall.Close(d.fd)
		delete(w.byFD, d.fd)
		d.fd = -1
	}
	if w.dirs[d.id] == d {
		delete(w.dirs, d.id)
	}
}

// resync makes d's per-file watches match the regular files now in the
// directory.
func (w *kqWatcher) resync(d *kqDir) {
	w.smu.Lock()
	defer w.smu.Unlock()
	w.mu.Lock()
	path := d.path
	alive := d.fd >= 0 && !w.closed
	w.mu.Unlock()
	if !alive {
		return
	}
	l := &w.lst
	l.Reset()
	if listDir(path, l) != nil {
		return // the directory vanished or is unreadable: its own event says so
	}

	w.mu.Lock()
	w.syncLocked(d, l)
	w.mu.Unlock()
	if l.bufferBytes() > listingSlotBytes {
		*l = Listing{}
	}
}

func (w *kqWatcher) syncListing(id uint32, gen, req uint64, l *Listing) {
	w.mu.Lock()
	defer w.mu.Unlock()
	d := w.dirs[id]
	if d == nil || d.wgen != gen || req <= d.lastReq {
		return
	}
	d.lastReq = req
	if l.Err == nil {
		w.syncLocked(d, l)
	}
}

func fileHash(name []byte) uint64 {
	h := uint64(14695981039346656037)
	for _, b := range name {
		h = (h ^ uint64(b)) * 1099511628211
	}
	return h
}

func (d *kqDir) fileIndex(name []byte) int {
	for i := d.lookup[fileHash(name)]; i != 0; {
		f := &d.files[i-1]
		if bytes.Equal(name, d.names[f.off:f.off+uint32(f.len)]) {
			return int(i - 1)
		}
		i = f.next
	}
	return -1
}

// syncLocked rebuilds the bounded file set from one fresh listing. All bulk
// scratch and file records are pointer-free and reused; names are copied once
// into an arena, and new descriptors are registered in one kevent batch.
func (w *kqWatcher) syncLocked(d *kqDir, l *Listing) {
	if d.fd < 0 || w.closed {
		return
	}
	count, names := 0, 0
	for _, e := range l.Entries {
		if e.Kind == KindFile {
			count++
			names += int(e.NameLen)
		}
	}
	if count > maxDirFiles {
		count, names = 0, 0
	}
	files := slices.Grow(d.spareFiles[:0], count)
	arena := slices.Grow(d.spareNames[:0], names)
	w.registrations = w.registrations[:0]
	w.registrations = slices.Grow(w.registrations, count)
	for i, e := range l.Entries {
		if count == 0 {
			break
		}
		if e.Kind != KindFile {
			continue
		}
		name := l.Name(i)
		fd := -1
		if old := d.fileIndex(name); old >= 0 {
			fd = d.files[old].fd
			d.files[old].fd = -1 // transferred to the new records
		}
		if fd < 0 && w.nfiles < maxTotalFiles {
			w.path = append(append(append(append(w.path[:0], d.path...), '/'), name...), 0)
			for {
				r, _, errno := syscall.Syscall(syscall.SYS_OPEN, uintptr(unsafe.Pointer(&w.path[0])), oEvtOnly|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
				if errno == syscall.EINTR {
					continue
				}
				if errno == 0 {
					fd = int(r)
					w.nfiles++
					w.registrations = append(w.registrations, syscall.Kevent_t{Ident: uint64(fd), Filter: syscall.EVFILT_VNODE, Flags: syscall.EV_ADD | syscall.EV_CLEAR | syscall.EV_RECEIPT, Fflags: noteDirChange | noteGone})
				}
				break
			}
		}
		if fd < 0 {
			continue
		}
		files = append(files, kqFile{fd: fd, off: uint32(len(arena)), len: uint16(len(name))})
		arena = append(arena, name...)
	}
	for _, f := range d.files {
		if f.fd >= 0 {
			syscall.Close(f.fd)
			delete(w.byFD, f.fd)
			w.nfiles--
		}
	}
	d.files, d.spareFiles = files, d.files[:0]
	d.names, d.spareNames = arena, d.names[:0]
	clear(d.lookup)
	d.nfiles = len(files)
	for i := range d.files {
		f := &d.files[i]
		h := fileHash(d.names[f.off : f.off+uint32(f.len)])
		f.next = d.lookup[h]
		d.lookup[h] = int32(i + 1)
		w.byFD[f.fd] = kqRef{d: d, index: i}
	}
	if n := len(w.registrations); n > 0 {
		w.receipts = slices.Grow(w.receipts[:0], n)[:n]
		got, err := kevent(w.kq, w.registrations, w.receipts)
		for i, ev := range w.registrations {
			if err == nil && i < got && w.receipts[i].Flags&syscall.EV_ERROR != 0 && w.receipts[i].Data == 0 {
				continue
			}
			fd := int(ev.Ident)
			ref := w.byFD[fd]
			d.files[ref.index].fd = -1
			delete(w.byFD, fd)
			syscall.Close(fd)
			d.nfiles--
			w.nfiles--
		}
	}
}

// loop is the only goroutine that waits for events. While nothing changes
// it sits in kevent and the process has no wakeups at all.
func (w *kqWatcher) loop() {
	defer close(w.done)
	var evs [64]syscall.Kevent_t
	for {
		n, err := kevent(w.kq, nil, evs[:])
		if err != nil {
			return // the queue was closed under us
		}
		if !w.process(evs[:n]) {
			return
		}
	}
}

// process handles one batch of kevents; false once the watcher is closed.
func (w *kqWatcher) process(evs []syscall.Kevent_t) bool {
	changed, resync := w.scrChanged[:0], w.scrResync[:0]
	need := func(d *kqDir) {
		for _, x := range resync {
			if x == d {
				return
			}
		}
		resync = append(resync, d)
	}
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return false
	}
	for i := range evs {
		ev := &evs[i]
		if ev.Filter != syscall.EVFILT_VNODE {
			continue
		}
		ref, ok := w.byFD[int(ev.Ident)]
		if !ok {
			continue // a descriptor closed since the event was queued
		}
		d := ref.d
		fl := ev.Fflags
		switch {
		case ref.index >= 0: // a file in the directory
			if fl&noteGone != 0 {
				f := &d.files[ref.index]
				if f.fd == int(ev.Ident) {
					syscall.Close(f.fd)
					delete(w.byFD, f.fd)
					f.fd = -1
					d.nfiles--
					w.nfiles--
				}
				// The name may now belong to a replacement (an atomic save
				// renames a new file over it): a resync that ran before this
				// event kept the old watch under that name, so look again.
				need(d)
			}
			kind := ChangeContent
			if fl&noteGone != 0 || !d.external {
				kind = ChangeDir
			}
			changed = append(changed, Change{d.id, kind})
		case fl&noteGone != 0:
			// Confirm that the directory really is gone from its path.
			if !stillAt(d) {
				w.dropDirLocked(d)
				changed = append(changed, Change{d.id, ChangeGone})
				continue
			}
			fallthrough
		default:
			changed = append(changed, Change{d.id, ChangeDir})
			if fl&syscall.NOTE_WRITE != 0 {
				need(d)
			}
		}
	}
	w.pmu.Lock()
	for _, c := range changed {
		w.noteLocked(c.ID, c.Kind)
	}
	woke := len(w.pending) > 0
	w.pmu.Unlock()
	w.mu.Unlock() // remove cannot finish before this batch's changes are recorded
	for _, d := range resync {
		if !d.external {
			w.resync(d)
		}
	}
	clear(resync)
	w.scrChanged, w.scrResync = changed[:0], resync[:0]
	if woke {
		w.wake()
	}
	return true
}

// noteLocked records kind for id, keeping the strongest per id
// (gone > overflow > dir > content). w.pmu held.
func (w *kqWatcher) noteLocked(id uint32, kind ChangeKind) {
	rank := func(k ChangeKind) int {
		switch k {
		case ChangeGone:
			return 3
		case ChangeOverflow:
			return 2
		case ChangeDir:
			return 1
		}
		return 0
	}
	for i := range w.pending {
		if w.pending[i].ID == id {
			if rank(kind) > rank(w.pending[i].Kind) {
				w.pending[i].Kind = kind
			}
			return
		}
	}
	w.pending = append(w.pending, Change{ID: id, Kind: kind})
}

func (w *kqWatcher) drain(dst []Change) []Change {
	w.pmu.Lock()
	dst = append(dst, w.pending...)
	w.pending = w.pending[:0]
	w.pmu.Unlock()
	return dst
}

func (w *kqWatcher) close() {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	w.closed = true
	for _, d := range w.dirs {
		w.dropDirLocked(d)
	}
	w.mu.Unlock()
	trigger := []syscall.Kevent_t{{Ident: 1, Filter: syscall.EVFILT_USER, Fflags: syscall.NOTE_TRIGGER}}
	kevent(w.kq, trigger, nil)
	<-w.done
	syscall.Close(w.kq)
}
