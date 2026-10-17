//go:build darwin

package fs

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
)

// FSEvents watcher.
//
// One FSEventStream watches the whole set of directories, scheduled on a
// shared serial dispatch queue; its callback runs on a GCD thread. Without
// kFSEventStreamCreateFlagFileEvents the stream reports the directory in
// which something changed (recursively, so events for deeper directories
// arrive too and are ignored), which is exactly "relist this directory".
// Paths are matched after canonicalization (F_GETPATH: symlinks resolved,
// on-disk case), because FSEvents reports real paths.
//
// A stream's path list is fixed, so add and remove build a new stream,
// start it, and only then stop the old one: events can be duplicated across
// the switch, never lost. That work runs on the Service's control
// goroutine.
//
// The ffi callback table is a process-wide resource, so exactly one C
// callback is registered, for all watchers; its info argument selects the
// watcher.

const (
	fseNoDefer   = 0x2 // kFSEventStreamCreateFlagNoDefer
	fseWatchRoot = 0x4 // kFSEventStreamCreateFlagWatchRoot

	fseMustScanSubDirs = 0x1
	fseUserDropped     = 0x2
	fseKernelDropped   = 0x4
	fseRootChanged     = 0x20
	fseUnmount         = 0x80

	fseSinceNow = ^uint64(0) // kFSEventStreamEventIdSinceNow

	fseLatency = 0.03 // seconds

	fcntlGetPath = 50
	oEvtOnly     = 0x8000
	maxPathLen   = 1024

	maxWatchers = 16
)

var fse struct {
	once sync.Once
	err  error

	create, setQueue, start, stop, invalidate, release uintptr
	cfStringCreate, cfArrayCreate, cfRelease           uintptr
	arrayCallBacks, queueCreate                        uintptr
	queue, cb                                          uintptr
	fr                                                 ffi.Frame // setup only; heap/global, never on a stack

	watchers [maxWatchers]atomic.Pointer[fseWatcher]
}

func loadFSE() error {
	fse.once.Do(func() {
		open := func(p string) uintptr {
			h, err := purego.Dlopen(p, purego.RTLD_NOW|purego.RTLD_GLOBAL)
			if err != nil && fse.err == nil {
				fse.err = fmt.Errorf("fs: %w", err)
			}
			return h
		}
		sym := func(h uintptr, n string) uintptr {
			p, err := purego.Dlsym(h, n)
			if err != nil && fse.err == nil {
				fse.err = fmt.Errorf("fs: %w", err)
			}
			return p
		}
		cf := open("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation")
		cs := open("/System/Library/Frameworks/CoreServices.framework/CoreServices")
		sys := open("/usr/lib/libSystem.B.dylib")
		if fse.err != nil {
			return
		}
		fse.cfStringCreate, fse.cfArrayCreate, fse.cfRelease = sym(cf, "CFStringCreateWithBytes"), sym(cf, "CFArrayCreate"), sym(cf, "CFRelease")
		fse.arrayCallBacks = sym(cf, "kCFTypeArrayCallBacks")
		fse.create, fse.setQueue = sym(cs, "FSEventStreamCreate"), sym(cs, "FSEventStreamSetDispatchQueue")
		fse.start, fse.stop = sym(cs, "FSEventStreamStart"), sym(cs, "FSEventStreamStop")
		fse.invalidate, fse.release = sym(cs, "FSEventStreamInvalidate"), sym(cs, "FSEventStreamRelease")
		fse.queueCreate = sym(sys, "dispatch_queue_create")
		if fse.err != nil {
			return
		}
		label := append([]byte("gonativeui.fs.events"), 0)
		fse.queue = fse.fr.Call2(fse.queueCreate, uintptr(unsafe.Pointer(&label[0])), 0)
		if fse.queue == 0 {
			fse.err = errors.New("fs: dispatch_queue_create failed")
			return
		}
		fse.cb = ffi.NewCallback(fseCallback)
	})
	return fse.err
}

// fseCallback is FSEventStreamCallback:
// (stream, info, numEvents, char **eventPaths, flags[], ids[]).
func fseCallback(c *ffi.CB) {
	slot := int(c.Int(1)) - 1
	if slot < 0 || slot >= maxWatchers {
		return
	}
	w := fse.watchers[slot].Load()
	if w == nil {
		return
	}
	n := int(c.Int(2))
	paths := unsafe.Slice((*uintptr)(c.Ptr(3)), n)
	flags := unsafe.Slice((*uint32)(c.Ptr(4)), n)
	w.onEvents(paths, flags)
}

type fseEntry struct {
	id   uint32
	path string // canonical
}

type fseWatcher struct {
	wake func()
	slot int

	cmu    sync.Mutex // serializes add/remove/close (stream rebuilds)
	fr     ffi.Frame  // only used with cmu held
	ctx    [5]uintptr // FSEventStreamContext, only info is set
	stream uintptr
	live   []string // paths the current stream watches, sorted
	closed bool

	mu      sync.Mutex // guards entries, byPath and pending against the callback thread
	entries []fseEntry
	byPath  map[string][]uint32
	pending []Change
}

func newWatcher(wake func()) (watcher, error) {
	if err := loadFSE(); err != nil {
		return nil, err
	}
	w := &fseWatcher{wake: wake, byPath: make(map[string][]uint32), pending: make([]Change, 0, 64)}
	for i := range fse.watchers {
		if fse.watchers[i].CompareAndSwap(nil, w) {
			w.slot = i
			w.ctx[1] = uintptr(i + 1)
			return w, nil
		}
	}
	return nil, errors.New("fs: too many watchers")
}

// canonPath resolves path to the real path FSEvents will report.
func canonPath(path string) (string, error) {
	b := make([]byte, len(path)+1)
	copy(b, path)
	var fd uintptr
	for {
		r, _, errno := syscall.Syscall(syscall.SYS_OPEN, uintptr(unsafe.Pointer(&b[0])), syscall.O_DIRECTORY|oEvtOnly|syscall.O_CLOEXEC, 0)
		if errno == syscall.EINTR {
			continue
		}
		if errno != 0 {
			return "", &os.PathError{Op: "open", Path: path, Err: errno}
		}
		fd = r
		break
	}
	defer syscall.Close(int(fd))
	buf := make([]byte, maxPathLen)
	if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, fd, fcntlGetPath, uintptr(unsafe.Pointer(&buf[0]))); errno != 0 {
		return "", &os.PathError{Op: "fcntl(F_GETPATH)", Path: path, Err: errno}
	}
	n := 0
	for n < len(buf) && buf[n] != 0 {
		n++
	}
	return string(buf[:n]), nil
}

func (w *fseWatcher) add(id uint32, path string) error {
	canon, err := canonPath(path)
	if err != nil {
		return err
	}
	w.cmu.Lock()
	defer w.cmu.Unlock()
	if w.closed {
		return errors.New("fs: watcher closed")
	}
	w.setEntry(id, canon, true)
	if err := w.restart(); err != nil {
		w.setEntry(id, "", false)
		return err
	}
	return nil
}

func (w *fseWatcher) remove(id uint32) {
	w.cmu.Lock()
	defer w.cmu.Unlock()
	if w.closed || !w.setEntry(id, "", false) {
		return
	}
	w.restart()
}

// setEntry replaces id's entry (add) or deletes it, and reports whether
// anything was removed or changed. cmu held.
func (w *fseWatcher) setEntry(id uint32, canon string, add bool) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	had := false
	for i, e := range w.entries {
		if e.id == id {
			w.entries = slices.Delete(w.entries, i, i+1)
			had = true
			break
		}
	}
	if add {
		w.entries = append(w.entries, fseEntry{id, canon})
	}
	clear(w.byPath)
	for _, e := range w.entries {
		w.byPath[e.path] = append(w.byPath[e.path], e.id)
	}
	// Drop pending changes of a removed id.
	if !add {
		w.pending = slices.DeleteFunc(w.pending, func(c Change) bool { return c.ID == id })
	}
	return had || add
}

// restart makes the running stream watch exactly the entries' paths. cmu held.
func (w *fseWatcher) restart() error {
	w.mu.Lock()
	paths := make([]string, 0, len(w.byPath))
	for p := range w.byPath {
		paths = append(paths, p)
	}
	w.mu.Unlock()
	slices.Sort(paths)
	if slices.Equal(paths, w.live) {
		return nil
	}
	var next uintptr
	if len(paths) > 0 {
		var err error
		if next, err = w.newStream(paths); err != nil {
			return err
		}
	}
	old := w.stream
	w.stream, w.live = next, paths
	if old != 0 {
		w.fr.Call1(fse.stop, old)
		w.fr.Call1(fse.invalidate, old)
		w.fr.Call1(fse.release, old)
	}
	return nil
}

func (w *fseWatcher) newStream(paths []string) (uintptr, error) {
	fr := &w.fr
	vals := make([]uintptr, len(paths))
	for i, p := range paths {
		b := []byte(p)
		var bp unsafe.Pointer
		if len(b) > 0 {
			bp = unsafe.Pointer(&b[0])
		}
		vals[i] = fr.Reset().Arg(0).ArgPtr(bp).Arg(uintptr(len(b))).Arg32(0x08000100 /* UTF-8 */).Arg(0).Call(fse.cfStringCreate).R0()
		if vals[i] == 0 {
			return 0, errors.New("fs: CFStringCreateWithBytes failed")
		}
	}
	arr := fr.Call4(fse.cfArrayCreate, 0, uintptr(unsafe.Pointer(&vals[0])), uintptr(len(vals)), fse.arrayCallBacks)
	for _, v := range vals {
		fr.Call1(fse.cfRelease, v)
	}
	if arr == 0 {
		return 0, errors.New("fs: CFArrayCreate failed")
	}
	defer fr.Call1(fse.cfRelease, arr)

	stream := fr.Reset().Arg(0).Arg(fse.cb).ArgPtr(unsafe.Pointer(&w.ctx)).Arg(arr).Arg(uintptr(fseSinceNow)).
		Arg32(fseNoDefer | fseWatchRoot).ArgF64(fseLatency).Call(fse.create).R0()
	if stream == 0 {
		return 0, errors.New("fs: FSEventStreamCreate failed")
	}
	fr.Call2(fse.setQueue, stream, fse.queue)
	if !fr.Reset().Arg(stream).Call(fse.start).Bool() {
		fr.Call1(fse.invalidate, stream)
		fr.Call1(fse.release, stream)
		return 0, errors.New("fs: FSEventStreamStart failed")
	}
	return stream, nil
}

// onEvents runs on a GCD thread. It must not touch UI state: it only
// records changes under w.mu and wakes the consumer.
func (w *fseWatcher) onEvents(paths []uintptr, flags []uint32) {
	woke := false
	w.mu.Lock()
	for i, pp := range paths {
		if i >= len(flags) {
			break
		}
		f := flags[i]
		if f&(fseMustScanSubDirs|fseUserDropped|fseKernelDropped) != 0 {
			for _, e := range w.entries {
				w.noteLocked(e.id, ChangeOverflow)
			}
			woke = woke || len(w.entries) > 0
			continue
		}
		p := cstring(pp)
		if len(p) > 1 && p[len(p)-1] == '/' {
			p = p[:len(p)-1]
		}
		ids := w.byPath[string(p)] // no allocation: map lookup keyed by a converted []byte
		kind := ChangeDir
		if f&(fseRootChanged|fseUnmount) != 0 {
			kind = ChangeGone
		}
		for _, id := range ids {
			w.noteLocked(id, kind)
			woke = true
		}
	}
	w.mu.Unlock()
	if woke {
		w.wake()
	}
}

func cstring(p uintptr) []byte {
	b := (*byte)(ffi.PtrFromC(p))
	n := 0
	for *(*byte)(unsafe.Add(unsafe.Pointer(b), n)) != 0 {
		n++
	}
	return unsafe.Slice(b, n)
}

// noteLocked records kind for id, keeping the strongest per id
// (gone > overflow > dir). w.mu held.
func (w *fseWatcher) noteLocked(id uint32, kind ChangeKind) {
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

func (w *fseWatcher) drain(dst []Change) []Change {
	w.mu.Lock()
	dst = append(dst, w.pending...)
	w.pending = w.pending[:0]
	w.mu.Unlock()
	return dst
}

func (w *fseWatcher) close() {
	w.cmu.Lock()
	defer w.cmu.Unlock()
	if w.closed {
		return
	}
	w.closed = true
	fse.watchers[w.slot].Store(nil)
	if w.stream != 0 {
		w.fr.Call1(fse.stop, w.stream)
		w.fr.Call1(fse.invalidate, w.stream)
		w.fr.Call1(fse.release, w.stream)
		w.stream, w.live = 0, nil
	}
}
