//go:build darwin

package fs

import (
	"errors"
	"os"
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
	fd  int
	gen uint32
}

type kqDir struct {
	id     uint32
	path   string // as given to add
	canon  string // real path, as reported by F_GETPATH
	fd     int
	files  map[string]*kqFile // by name
	gen    uint32
	nfiles int // watches counted against the total budget
}

type kqRef struct {
	d    *kqDir
	name string // "" for the directory itself
}

type kqWatcher struct {
	wake func()
	kq   int
	done chan struct{}

	smu sync.Mutex // serializes resync (owns lst)
	lst Listing

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

func (w *kqWatcher) add(id uint32, path string) error {
	fd, err := openEvt(path, true)
	if err != nil {
		return err
	}
	canon, err := fdPath(fd)
	if err != nil {
		syscall.Close(fd)
		return &os.PathError{Op: "fcntl(F_GETPATH)", Path: path, Err: err}
	}
	d := &kqDir{id: id, path: path, canon: canon, fd: fd, files: map[string]*kqFile{}}

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
	w.byFD[fd] = kqRef{d: d}
	err = w.register(fd, noteDirChange|noteGone)
	if err != nil {
		w.dropDirLocked(d)
		w.mu.Unlock()
		return os.NewSyscallError("kevent", err)
	}
	w.mu.Unlock()

	// Directory first, files second: a change in between shows up as a
	// directory event, and resync after it picks the new file up.
	w.resync(d)
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
	for name, f := range d.files {
		syscall.Close(f.fd)
		delete(w.byFD, f.fd)
		delete(d.files, name)
	}
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
	defer w.mu.Unlock()
	if d.fd < 0 || w.closed {
		return
	}
	files := 0
	for i := range l.Entries {
		if l.Entries[i].Kind == KindFile {
			files++
		}
	}
	d.gen++
	if files > maxDirFiles {
		files = 0 // structure only
	}
	if files > 0 {
		for i := range l.Entries {
			if l.Entries[i].Kind != KindFile {
				continue
			}
			name := l.Name(i)
			if f := d.files[string(name)]; f != nil {
				f.gen = d.gen
				continue
			}
			if w.nfiles >= maxTotalFiles {
				break
			}
			full := path + "/" + string(name)
			fd, err := openEvt(full, false)
			if err != nil {
				continue // raced with a delete, or unreadable
			}
			f := &kqFile{fd: fd, gen: d.gen}
			w.byFD[fd] = kqRef{d: d, name: string(name)}
			if w.register(fd, noteDirChange|noteGone) != nil {
				delete(w.byFD, fd)
				syscall.Close(fd)
				continue
			}
			d.files[string(name)] = f
			d.nfiles++
			w.nfiles++
		}
	}
	for name, f := range d.files {
		if f.gen != d.gen {
			syscall.Close(f.fd)
			delete(w.byFD, f.fd)
			delete(d.files, name)
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
		case ref.name != "": // a file in the directory
			if fl&noteGone != 0 {
				if f := d.files[ref.name]; f != nil && f.fd == int(ev.Ident) {
					syscall.Close(f.fd)
					delete(w.byFD, f.fd)
					delete(d.files, ref.name)
					d.nfiles--
					w.nfiles--
				}
				// The name may now belong to a replacement (an atomic save
				// renames a new file over it): a resync that ran before this
				// event kept the old watch under that name, so look again.
				need(d)
			}
			changed = append(changed, Change{d.id, ChangeDir})
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
	w.mu.Unlock()

	w.pmu.Lock()
	for _, c := range changed {
		w.noteLocked(c.ID, c.Kind)
	}
	woke := len(w.pending) > 0
	w.pmu.Unlock()
	for _, d := range resync {
		w.resync(d)
	}
	w.scrChanged, w.scrResync = changed, resync
	if woke {
		w.wake()
	}
	return true
}

// noteLocked records kind for id, keeping the strongest per id
// (gone > overflow > dir). w.pmu held.
func (w *kqWatcher) noteLocked(id uint32, kind ChangeKind) {
	rank := func(k ChangeKind) int {
		switch k {
		case ChangeGone:
			return 2
		case ChangeOverflow:
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
