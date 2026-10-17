//go:build linux

package fs

import (
	"errors"
	"os"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

// inotify watcher.
//
// One worker goroutine blocks in ppoll on two descriptors: the inotify
// instance and an eventfd that close() writes to. Directories are added and
// removed with inotify_add_watch / inotify_rm_watch directly from the
// caller's goroutine (both are thread-safe and non-blocking), so the worker
// never has to be woken for them. Idle cost: the thread sleeps in the
// kernel; there are no timers.
//
// Events are not decoded further than "something in this directory
// changed" (ChangeDir): IN_CREATE | IN_DELETE | IN_MOVED_FROM | IN_MOVED_TO
// | IN_MODIFY | IN_ATTRIB on the directory's entries. A queue overflow
// (IN_Q_OVERFLOW, wd = -1) is ChangeOverflow for every watched id.
// IN_DELETE_SELF, IN_MOVE_SELF, IN_UNMOUNT and IN_IGNORED end the watch with
// ChangeGone; the watch is forgotten (remove of that id is then a no-op).
//
// Two ids may watch the same directory (two panes on one folder): the kernel
// returns the same watch descriptor for the same inode, so the table maps
// descriptors to any number of ids and the descriptor is removed from the
// kernel only when its last id goes.
//
// Locking: tmu guards the table and closed; pmu guards the pending changes.
// The order is tmu then pmu. drain takes only pmu, so a slow add_watch (on a
// network mount) never blocks the UI thread.

const (
	lxInCreate     = 0x00000100
	lxInDelete     = 0x00000200
	lxInMovedFrom  = 0x00000040
	lxInMovedTo    = 0x00000080
	lxInModify     = 0x00000002
	lxInAttrib     = 0x00000004
	lxInDeleteSelf = 0x00000400
	lxInMoveSelf   = 0x00000800
	lxInUnmount    = 0x00002000
	lxInQOverflow  = 0x00004000
	lxInIgnored    = 0x00008000
	lxInOnlyDir    = 0x01000000

	// include/uapi/linux/inotify.h (linux master, fetched 2026-10-17).
	lxWatchMask = lxInCreate | lxInDelete | lxInMovedFrom | lxInMovedTo | lxInModify | lxInAttrib |
		lxInDeleteSelf | lxInMoveSelf | lxInOnlyDir

	lxEventHdr = 16 // sizeof(struct inotify_event): wd s32, mask u32, cookie u32, len u32, then name[len]
	lxReadBuf  = 64 << 10

	lxPollIn  = 0x1
	lxPollErr = 0x8
	lxPollHup = 0x10
	lxPollNvl = 0x20
)

// lxPollFd is struct pollfd (8 bytes).
type lxPollFd struct {
	fd      int32
	events  int16
	revents int16
}

var (
	_ [unsafe.Sizeof(lxPollFd{}) - 8]struct{}
	_ [8 - unsafe.Sizeof(lxPollFd{})]struct{}
)

type lxWatch struct {
	id uint32
	wd int32
}

type lxWatcher struct {
	wake func()
	ifd  int // inotify
	efd  int // eventfd: written by close
	done chan struct{}
	buf  []uint64 // worker-only read buffer

	tmu    sync.Mutex
	ws     []lxWatch
	closed bool

	pmu  sync.Mutex
	pend []Change
}

func newWatcher(wake func()) (watcher, error) {
	i, _, e := syscall.Syscall(syscall.SYS_INOTIFY_INIT1, syscall.IN_NONBLOCK|syscall.IN_CLOEXEC, 0, 0)
	if e != 0 {
		return nil, os.NewSyscallError("inotify_init1", e)
	}
	// EFD_NONBLOCK | EFD_CLOEXEC are O_NONBLOCK | O_CLOEXEC (eventfd.h).
	ev, _, e := syscall.Syscall(syscall.SYS_EVENTFD2, 0, syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if e != 0 {
		syscall.Syscall(syscall.SYS_CLOSE, i, 0, 0)
		return nil, os.NewSyscallError("eventfd2", e)
	}
	w := &lxWatcher{
		wake: wake,
		ifd:  int(i),
		efd:  int(ev),
		done: make(chan struct{}),
		buf:  make([]uint64, lxReadBuf/8),
	}
	go w.run()
	return w, nil
}

func (w *lxWatcher) add(id uint32, path string) error {
	if strings.IndexByte(path, 0) >= 0 {
		return lxPathErr("fs.watch", path, syscall.EINVAL)
	}
	cp, err := syscall.BytePtrFromString(path)
	if err != nil {
		return lxPathErr("fs.watch", path, syscall.EINVAL)
	}
	w.tmu.Lock()
	defer w.tmu.Unlock()
	if w.closed {
		return errors.New("fs: watcher closed")
	}
	w.removeLocked(id) // re-adding an id replaces its watch
	r, _, e := syscall.Syscall(syscall.SYS_INOTIFY_ADD_WATCH, uintptr(w.ifd), uintptr(unsafe.Pointer(cp)), lxWatchMask)
	if e != 0 {
		return lxPathErr("fs.watch", path, e)
	}
	w.ws = append(w.ws, lxWatch{id: id, wd: int32(r)})
	return nil
}

func (w *lxWatcher) remove(id uint32) {
	w.tmu.Lock()
	if !w.closed {
		w.removeLocked(id)
	}
	w.tmu.Unlock()
	w.pmu.Lock()
	w.dropPending(id)
	w.pmu.Unlock()
}

// removeLocked forgets id's watch and drops the kernel watch when no other
// id shares it; w.tmu is held.
func (w *lxWatcher) removeLocked(id uint32) {
	for i, x := range w.ws {
		if x.id != id {
			continue
		}
		w.ws = append(w.ws[:i], w.ws[i+1:]...)
		for _, o := range w.ws {
			if o.wd == x.wd {
				return
			}
		}
		syscall.Syscall(syscall.SYS_INOTIFY_RM_WATCH, uintptr(w.ifd), uintptr(x.wd), 0)
		return
	}
}

func (w *lxWatcher) drain(dst []Change) []Change {
	w.pmu.Lock()
	dst = append(dst, w.pend...)
	w.pend = w.pend[:0]
	w.pmu.Unlock()
	return dst
}

func (w *lxWatcher) close() {
	w.tmu.Lock()
	if w.closed {
		w.tmu.Unlock()
		return
	}
	w.closed = true
	w.tmu.Unlock()
	one := uint64(1)
	syscall.Syscall(syscall.SYS_WRITE, uintptr(w.efd), uintptr(unsafe.Pointer(&one)), 8)
	<-w.done
	syscall.Syscall(syscall.SYS_CLOSE, uintptr(w.ifd), 0, 0) // also drops every watch
	syscall.Syscall(syscall.SYS_CLOSE, uintptr(w.efd), 0, 0)
}

// dropPending removes id's queued change; w.pmu is held.
func (w *lxWatcher) dropPending(id uint32) {
	for i := range w.pend {
		if w.pend[i].ID == id {
			w.pend = append(w.pend[:i], w.pend[i+1:]...)
			return
		}
	}
}

// post queues a change for id, coalescing with an earlier one (Gone beats
// Overflow beats Dir); w.pmu is held. It reports whether the queue grew.
func (w *lxWatcher) post(id uint32, k ChangeKind) bool {
	for i := range w.pend {
		if w.pend[i].ID == id {
			if k > w.pend[i].Kind {
				w.pend[i].Kind = k
			}
			return false
		}
	}
	w.pend = append(w.pend, Change{ID: id, Kind: k})
	return true
}

func (w *lxWatcher) run() {
	defer close(w.done)
	var fds [2]lxPollFd
	for {
		fds[0] = lxPollFd{fd: int32(w.ifd), events: lxPollIn}
		fds[1] = lxPollFd{fd: int32(w.efd), events: lxPollIn}
		// ppoll(fds, 2, NULL timeout, NULL sigmask): sleeps until an event or close.
		_, _, e := syscall.Syscall6(syscall.SYS_PPOLL, uintptr(unsafe.Pointer(&fds[0])), 2, 0, 0, 0, 0)
		if e == syscall.EINTR {
			continue
		}
		if e != 0 || fds[1].revents != 0 {
			return
		}
		if fds[0].revents&(lxPollIn|lxPollErr|lxPollHup|lxPollNvl) != 0 && !w.readEvents() {
			return
		}
	}
}

// readEvents reads and handles every queued inotify event, then calls wake
// once if anything was queued. It returns false when the instance is dead.
func (w *lxWatcher) readEvents() bool {
	buf := unsafe.Slice((*byte)(unsafe.Pointer(&w.buf[0])), lxReadBuf)
	queued := false
	for {
		n, _, e := syscall.Syscall(syscall.SYS_READ, uintptr(w.ifd), uintptr(unsafe.Pointer(&buf[0])), lxReadBuf)
		if e == syscall.EINTR {
			continue
		}
		if e == syscall.EAGAIN {
			break
		}
		if e != 0 {
			return false
		}
		if n == 0 {
			break
		}
		if w.handle(buf[:n]) {
			queued = true
		}
	}
	if queued && w.wake != nil {
		w.wake()
	}
	return true
}

// handle decodes one read() worth of events and reports whether the queue
// grew.
func (w *lxWatcher) handle(b []byte) (queued bool) {
	w.tmu.Lock()
	defer w.tmu.Unlock()
	w.pmu.Lock()
	defer w.pmu.Unlock()
	for off := 0; off+lxEventHdr <= len(b); {
		wd := *(*int32)(unsafe.Pointer(&b[off]))
		mask := *(*uint32)(unsafe.Pointer(&b[off+4]))
		nameLen := int(*(*uint32)(unsafe.Pointer(&b[off+12])))
		off += lxEventHdr + nameLen
		switch {
		case mask&lxInQOverflow != 0:
			for _, x := range w.ws {
				if w.post(x.id, ChangeOverflow) {
					queued = true
				}
			}
		case mask&(lxInDeleteSelf|lxInMoveSelf|lxInUnmount|lxInIgnored) != 0:
			// The watch is over for every id on this descriptor.
			for i := 0; i < len(w.ws); {
				if w.ws[i].wd != wd {
					i++
					continue
				}
				if w.post(w.ws[i].id, ChangeGone) {
					queued = true
				}
				w.ws = append(w.ws[:i], w.ws[i+1:]...)
			}
			if mask&lxInIgnored == 0 { // still alive in the kernel (moved, unmounted)
				syscall.Syscall(syscall.SYS_INOTIFY_RM_WATCH, uintptr(w.ifd), uintptr(wd), 0)
			}
		default:
			for _, x := range w.ws {
				if x.wd == wd && w.post(x.id, ChangeDir) {
					queued = true
				}
			}
		}
	}
	return queued
}
