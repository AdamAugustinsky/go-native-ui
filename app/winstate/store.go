package winstate

import (
	"fmt"
	"path/filepath"
	"time"

	"gonativeui/app/paths"
	"gonativeui/app/prefs"
)

// formatVersion is the version of the file; a file of another version is read as
// no state and replaced by the next Save.
const formatVersion = 1

// file is the JSON on disk.
type file struct {
	Version int   `json:"version"`
	State   State `json:"state"`
}

// Options configures LoadWith. The zero value is usable.
type Options struct {
	// Name selects the window's file when an application has several
	// windows: window-<Name>.json. "" means "main". Letters, digits, '-' and
	// '_', at most 64.
	Name string
	// Dir is the directory of the file; "" means paths.Config(id).
	Dir string
	// Debounce is how long after a Save the file is written; 0 means 500 ms
	// (see prefs.Options).
	Debounce time.Duration
}

// validName reports whether s is a window name: one to 64 of the letters and
// digits of ASCII, '-' and '_'.
func validName(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

// Store is a window's saved state. State, Save, Flush and Close may be called
// from any goroutine; Save never touches the disk.
type Store struct {
	p *prefs.Store[file]
}

// Load reads the saved state of the application id's main window. It does file
// I/O: call it at startup, before the window opens. See LoadWith.
func Load(id string) (*Store, error) { return LoadWith(id, Options{}) }

// LoadWith is Load with options. A missing file is not an error: State is then
// the zero State (Valid false). A file that cannot be read, or is corrupt (it is
// then moved aside to <file>.corrupt), is reported in err together with a Store
// that works, on no state, so the application can log it and go on. err is nil
// or wraps prefs.ErrCorrupt in the second case. The only error with no Store is
// a bad id or name.
func LoadWith(id string, o Options) (*Store, error) {
	name := o.Name
	if name == "" {
		name = "main"
	}
	if !validName(name) {
		return nil, fmt.Errorf("winstate: invalid window name %q", name)
	}
	dir := o.Dir
	if dir == "" {
		var err error
		if dir, err = paths.Config(id); err != nil {
			return nil, err
		}
	}
	p, err := prefs.Open(filepath.Join(dir, "window-"+name+".json"), file{}, prefs.Options{Debounce: o.Debounce})
	return &Store{p: p}, err
}

// State returns the saved state, or the zero State when there is none (or it is
// from another version of the file). The App slice is a copy.
func (s *Store) State() State {
	f := s.p.Get()
	if f.Version != formatVersion {
		return State{}
	}
	st := f.State
	st.App = append([]byte(nil), st.App...)
	if len(st.App) == 0 {
		st.App = nil
	}
	return st
}

// Save records st as the window's state and schedules the write, a moment later
// and on a worker; calls in between share one write. It does not touch the disk
// and may be called on every move and resize. It copies st.App. A state that is
// not Valid, or with too many application bytes, is refused with ErrInvalid or
// ErrTooLarge and the earlier state stays.
func (s *Store) Save(st State) error {
	if !st.Valid() {
		return ErrInvalid
	}
	if len(st.App) > MaxApp {
		return ErrTooLarge
	}
	st.App = append([]byte(nil), st.App...)
	s.p.Set(file{Version: formatVersion, State: st})
	return nil
}

// Flush writes a pending Save now. It blocks on the disk: call it from a worker
// or at quit.
func (s *Store) Flush() error { return s.p.Flush() }

// Close writes a pending Save and stops the store. Call it at quit. It blocks on
// the disk.
func (s *Store) Close() error { return s.p.Close() }

// Path is the file the store reads and writes.
func (s *Store) Path() string { return s.p.Path() }
