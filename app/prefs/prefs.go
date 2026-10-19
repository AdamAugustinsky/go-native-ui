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
	// failed save (a full disk, a read-only directory). The save is retried at
	// the next change or Flush; Err keeps the latest error until a save works.
	OnError func(error)
}

// Store is a typed preferences file. Get, Set, Update, Flush, Err and Close may
// be called from any goroutine. Get, Set and Update never touch the disk.
type Store[T any] struct {
	path string
	opt  Options

	mu     sync.Mutex // guards v, dirty, closed, armed, err, last
	v      T
	dirty  bool
	closed bool
	armed  bool // the timer is running
	err    error
	last   []byte // the bytes on disk, to skip saves that change nothing
	timer  *time.Timer

	saveMu sync.Mutex // one save at a time, in the order they were asked for
}

// Open reads the preferences file at path into a copy of defaults and returns a
// Store on it. It does file I/O: call it at startup, before the first frame,
// not from the UI loop.
//
// A missing file is not an error; the store holds defaults and the file is
// created by the first change. If the file cannot be read or is corrupt, Open
// returns a working Store on the defaults together with the error (ErrCorrupt
// for bad JSON): the caller may report it and carry on.
func Open[T any](path string, defaults T, opt Options) (*Store[T], error) {
	if opt.Debounce <= 0 {
		opt.Debounce = 500 * time.Millisecond
	}
	if opt.Perm == 0 {
		opt.Perm = 0o600
	}
	s := &Store[T]{path: path, opt: opt, v: defaults}
	s.timer = time.AfterFunc(time.Hour, s.fire)
	s.timer.Stop()
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return s, nil
	case err != nil:
		return s, fmt.Errorf("prefs: %w", err)
	}
	v := defaults
	if err := json.Unmarshal(data, &v); err != nil {
		aside := path + ".corrupt"
		os.Remove(aside)
		if rerr := os.Rename(path, aside); rerr != nil {
			return s, fmt.Errorf("%w: %s: %v (and could not move it aside: %v)", ErrCorrupt, path, err, rerr)
		}
		return s, fmt.Errorf("%w: %s: %v (kept as %s)", ErrCorrupt, path, err, aside)
	}
	s.v = v
	s.last = data
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
	s.mu.Unlock()
	s.save()
}

// save writes the current value if it changed since the last write.
func (s *Store[T]) save() error {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	s.mu.Lock()
	if !s.dirty {
		err := s.err
		s.mu.Unlock()
		return err
	}
	s.dirty = false
	data, err := json.MarshalIndent(&s.v, "", "  ")
	same := err == nil && bytes.Equal(data, s.last)
	s.mu.Unlock()
	if err == nil && !same {
		err = writeAtomic(s.path, append(data, '\n'), s.opt.Perm, s.opt.FullSync)
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
	if err != nil && s.opt.OnError != nil {
		s.opt.OnError(err)
	}
	return err
}

// Flush writes any pending change now and returns the error of the write. It
// blocks on the disk: call it at quit or from a worker, not from the UI loop.
func (s *Store[T]) Flush() error {
	s.mu.Lock()
	if s.armed {
		s.armed = false
		s.timer.Stop()
	}
	s.mu.Unlock()
	return s.save()
}

// Err is the error of the latest save, nil if it succeeded (or none happened).
func (s *Store[T]) Err() error {
	s.mu.Lock()
	err := s.err
	s.mu.Unlock()
	return err
}

// Close writes any pending change and stops saving. Later changes stay in
// memory. It blocks on the disk, like Flush.
func (s *Store[T]) Close() error {
	err := s.Flush()
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return err
}
