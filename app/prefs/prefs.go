// Package prefs keeps an application's preferences in a JSON file: read once,
// synchronously, at startup; changed in memory by the UI thread with no I/O;
// written back, atomically, on a worker a moment after the last change.
//
//	type Settings struct {
//		Theme string
//		Zoom  float64
//		Recent []string
//	}
//	dir, _ := paths.Config(appID)
//	s, err := prefs.Open(filepath.Join(dir, "settings.json"), Settings{Theme: "system", Zoom: 1}, prefs.Options{})
//	// err reports a file that was unreadable or corrupt; s still works, on the defaults.
//	cur := s.Get()                      // any thread, per frame if you like: no I/O
//	s.Update(func(v *Settings) { v.Zoom = 1.25 }) // schedules a save
//	defer s.Close()                     // at quit: writes what is still pending
//
// Fields missing from the file keep their default, so adding a field to the
// struct needs no migration. Unknown fields in the file are ignored (and
// dropped by the next save).
package prefs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"time"
)

// ErrCorrupt wraps the error Open returns for a file that is not valid JSON for
// the type. The file is moved aside to <path>.corrupt (replacing an older such
// file), so the next save does not destroy it, and the store starts on the
// defaults.
var ErrCorrupt = errors.New("prefs: corrupt preferences file")

// Options configures a Store. The zero value is usable.
type Options struct {
	// Debounce is how long after a change the file is written; changes in
	// between share the write. 0 means 500 ms. The wait starts at the first
	// change and is not extended by later ones, so a stream of changes (a
	// slider being dragged) still saves every Debounce.
	Debounce time.Duration
	// Perm is the file mode of the written file. 0 means 0600.
	Perm os.FileMode
	// FullSync asks for the strongest flush the OS offers: F_FULLFSYNC on
	// macOS (slow, but the data is on the drive and not in its cache). Other
	// systems always use fsync.
	FullSync bool
	// OnError, when non-nil, is called from the saving goroutine with each
	// failed save (a full disk, a read-only directory). No lock of the store is
	// held, so it may call the store. The save is retried at the next change or
	// Flush; Err keeps the latest error until a save works.
	OnError func(error)
}

// Store is a typed preferences file. Get, Set, Update, Flush, Err and Close may
// be called from any goroutine. Get, Set and Update never touch the disk and
// never wait for a save: a save works on a private copy of the value.
type Store[T any] struct {
	path string
	opt  Options
	flat bool // T holds no pointers, maps or slices: a plain copy is a snapshot

	mu     sync.Mutex // guards v, dirty, closed, armed, err, last; never held across encoding or I/O
	v      T
	dirty  bool
	closed bool
	armed  bool // the timer is running
	err    error
	last   []byte // the bytes on disk, to skip saves that change nothing
	timer  *time.Timer

	saveMu sync.Mutex // one save at a time, in the order they were asked for
}

// encode is the file's canonical form of v.
func encode[T any](v *T) ([]byte, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// Open reads the preferences file at path into a copy of defaults and returns a
// Store on it. The store owns its copy: nothing Open or the store does changes
// the maps, slices or pointers inside defaults. Open does file I/O: call it at
// startup, before the first frame, not from the UI loop.
//
// A missing file is not an error; the store holds defaults and the file is
// created by the first change. If the file cannot be read or is corrupt, Open
// returns a working Store on the defaults together with the error (ErrCorrupt
// for bad JSON or a value of the wrong type): the caller may report it and carry
// on.
func Open[T any](path string, defaults T, opt Options) (*Store[T], error) {
	if opt.Debounce <= 0 {
		opt.Debounce = 500 * time.Millisecond
	}
	if opt.Perm == 0 {
		opt.Perm = 0o600
	}
	s := &Store[T]{path: path, opt: opt, v: clone(defaults), flat: !hasRefs(reflect.TypeFor[T]())}
	s.timer = time.AfterFunc(time.Hour, s.fire)
	s.timer.Stop()
	data, err := readFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return s, nil
	case err != nil:
		return s, fmt.Errorf("prefs: %w", err)
	}
	v := clone(defaults) // decoded into storage of its own; s.v stays untouched if this fails half way
	if err := json.Unmarshal(data, &v); err != nil {
		aside := path + ".corrupt"
		os.Remove(aside)
		if rerr := os.Rename(path, aside); rerr != nil {
			return s, fmt.Errorf("%w: %s: %v (and could not move it aside: %v)", ErrCorrupt, path, err, rerr)
		}
		return s, fmt.Errorf("%w: %s: %v (kept as %s)", ErrCorrupt, path, err, aside)
	}
	s.v = v
	// The baseline is what a save would write for this value, not the file's own
	// bytes (which may be formatted by hand or by an older version): an unchanged
	// value must not be rewritten after a reopen.
	if enc, err := encode(&s.v); err == nil {
		s.last = enc
	}
	return s, nil
}

// Path is the file the store reads and writes.
func (s *Store[T]) Path() string { return s.path }

// Get returns a copy of the current preferences. The copy is shallow: do not
// modify maps or slices reachable from it; change them with Update, which
// replaces them under the store's lock.
func (s *Store[T]) Get() T {
	s.mu.Lock()
	v := s.v
	s.mu.Unlock()
	return v
}

// Set replaces the preferences and schedules a save. It does not allocate or
// touch the disk. After Close the value changes in memory only.
func (s *Store[T]) Set(v T) {
	s.mu.Lock()
	s.v = v
	s.changedLocked()
	s.mu.Unlock()
}

// Update calls f on the current preferences, under the store's lock, and
// schedules a save. f must be quick and must not call the store.
func (s *Store[T]) Update(f func(*T)) {
	s.mu.Lock()
	f(&s.v)
	s.changedLocked()
	s.mu.Unlock()
}

// changedLocked marks the value changed and starts the debounce timer unless it
// is running or the store is closed. s.mu held.
func (s *Store[T]) changedLocked() {
	s.dirty = true
	if s.closed || s.armed {
		return
	}
	s.armed = true
	s.timer.Reset(s.opt.Debounce)
}

// fire is the timer's callback, on its own goroutine.
func (s *Store[T]) fire() {
	s.mu.Lock()
	s.armed = false
	closed := s.closed
	s.mu.Unlock()
	if !closed {
		s.save(false)
	}
}

// save writes the current value if it changed since the last write. A closed
// store writes only when force is set, which only Close does, once.
func (s *Store[T]) save(force bool) error {
	s.saveMu.Lock()
	err, report := s.saveLocked(force)
	s.saveMu.Unlock()
	if report && s.opt.OnError != nil { // after the unlock: the callback may call the store
		s.opt.OnError(err)
	}
	return err
}

// saveLocked is save under saveMu. report says whether err is a new failure.
func (s *Store[T]) saveLocked(force bool) (err error, report bool) {
	s.mu.Lock()
	if (s.closed && !force) || !s.dirty {
		err = s.err
		s.mu.Unlock()
		return err, false
	}
	s.dirty = false
	snap := s.v
	if !s.flat {
		snap = clone(s.v) // private from here on: the value may change while we encode
	}
	s.mu.Unlock()

	data, err := encode(&snap) // may run the type's own MarshalJSON: no lock held
	if err == nil && !bytes.Equal(data, s.lastBytes()) {
		err = writeAtomic(s.path, data, s.opt.Perm, s.opt.FullSync)
	}
	s.mu.Lock()
	if err != nil {
		s.dirty = true // try again at the next change or Flush
		s.err = err
	} else {
		s.err = nil
		s.last = data
	}
	s.mu.Unlock()
	return err, err != nil
}

func (s *Store[T]) lastBytes() []byte {
	s.mu.Lock()
	b := s.last
	s.mu.Unlock()
	return b
}

// Flush writes any pending change now and returns the error of the write. It
// blocks on the disk: call it at quit or from a worker, not from the UI loop. On
// a closed store it writes nothing.
func (s *Store[T]) Flush() error {
	s.mu.Lock()
	if s.armed {
		s.armed = false
		s.timer.Stop()
	}
	s.mu.Unlock()
	return s.save(false)
}

// Err is the error of the latest save, nil if it succeeded (or none happened).
func (s *Store[T]) Err() error {
	s.mu.Lock()
	err := s.err
	s.mu.Unlock()
	return err
}

// Close stops the store and writes any pending change: once it returns, no save
// is running or will start, and later changes stay in memory. A save that was
// already running finishes first. It blocks on the disk, like Flush. Calling it
// again waits for the first and returns the same result.
func (s *Store[T]) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		s.saveMu.Lock() // the first Close's final save may still be running
		s.saveMu.Unlock()
		return s.Err()
	}
	s.closed = true
	if s.armed {
		s.armed = false
		s.timer.Stop()
	}
	s.mu.Unlock()
	return s.save(true)
}
