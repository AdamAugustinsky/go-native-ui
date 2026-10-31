package prefs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/synctest"
	"time"
)

// slowGate makes slow.MarshalJSON block, to hold a save in the middle of
// encoding.
var slowGate struct {
	on      atomic.Bool
	started chan struct{}
	release chan struct{}
}

type slow struct{ N int }

func (s slow) MarshalJSON() ([]byte, error) {
	if slowGate.on.Load() {
		select {
		case slowGate.started <- struct{}{}:
		default:
		}
		<-slowGate.release
	}
	return json.Marshal(s.N)
}

func (s *slow) UnmarshalJSON(b []byte) error { return json.Unmarshal(b, &s.N) }

type slowSettings struct {
	S     slow
	Theme string
	M     map[string]int
}

func armGate(t *testing.T) {
	t.Helper()
	slowGate.started = make(chan struct{}, 1)
	slowGate.release = make(chan struct{})
	slowGate.on.Store(true)
	t.Cleanup(func() {
		if slowGate.on.Swap(false) {
			close(slowGate.release)
		}
	})
}

func openGate() {
	if slowGate.on.Swap(false) {
		close(slowGate.release)
	}
}

func within(t *testing.T, d time.Duration, what string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { f(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s did not finish within %v", what, d)
	}
}

// 6. Encoding runs outside the lock Get, Set and Update take, on a private copy:
// the foreground neither waits for a slow encoder nor leaks into the snapshot.
func TestForegroundIsNotBlockedByASlowSave(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "p.json")
		s, _ := Open(p, slowSettings{Theme: "one", M: map[string]int{"a": 1}}, Options{Debounce: time.Hour})
		s.Set(slowSettings{S: slow{1}, Theme: "one", M: map[string]int{"a": 1}})
		armGate(t)
		flushed := make(chan error, 1)
		go func() { flushed <- s.Flush() }()
		select {
		case <-slowGate.started:
		case <-time.After(5 * time.Second):
			t.Fatal("the save never started encoding")
		}
		within(t, 2*time.Second, "foreground access during a save", func() {
			_ = s.Get()
			s.Update(func(v *slowSettings) { v.Theme = "two"; v.M["b"] = 2 }) // in place, on the live map
			s.Set(s.Get())
		})
		openGate()
		if err := <-flushed; err != nil {
			t.Fatal(err)
		}
		b := read(t, p)
		if strings.Contains(b, `"two"`) || strings.Contains(b, `"b"`) {
			t.Errorf("the snapshot saw changes made after it was taken:\n%s", b)
		}
		if err := s.Flush(); err != nil { // the changes made meanwhile are saved next
			t.Fatal(err)
		}
		if b := read(t, p); !strings.Contains(b, `"two"`) || !strings.Contains(b, `"b": 2`) {
			t.Errorf("the later changes were lost:\n%s", b)
		}
	})
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// 7. A type error after part of the file has been decoded into a map and a slice
// must leave the caller's defaults, and the store's fallback, as they were.
func TestFailedDecodeDoesNotTouchDefaults(t *testing.T) {
	type cfg struct {
		M    map[string]int
		S    []string
		P    *struct{ A int }
		N    int
		Deep map[string][]int
	}
	defaults := cfg{M: map[string]int{"a": 1}, S: []string{"x", "y"}, P: &struct{ A int }{1}, Deep: map[string][]int{"k": {1}}}
	p := filepath.Join(t.TempDir(), "p.json")
	os.WriteFile(p, []byte(`{"M":{"b":2,"a":9},"S":["z"],"P":{"A":7},"Deep":{"k":[5,6]},"N":"not a number"}`), 0o600)
	s, err := Open(p, defaults, Options{})
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("err %v, want ErrCorrupt", err)
	}
	defer s.Close() // its Update below schedules a save: not after this test
	if len(defaults.M) != 1 || defaults.M["a"] != 1 || defaults.S[0] != "x" || defaults.P.A != 1 || defaults.Deep["k"][0] != 1 || len(defaults.Deep["k"]) != 1 {
		t.Errorf("the caller's defaults were modified: %+v", defaults)
	}
	got := s.Get()
	if len(got.M) != 1 || got.M["a"] != 1 || len(got.S) != 2 || got.S[0] != "x" || got.P.A != 1 || len(got.Deep["k"]) != 1 {
		t.Errorf("the fallback is not the defaults: %+v", got)
	}
	// And the store's value is its own: changing it in place leaves defaults alone.
	s.Update(func(v *cfg) { v.M["c"] = 3; v.S[0] = "changed"; v.P.A = 50 })
	if len(defaults.M) != 1 || defaults.S[0] != "x" || defaults.P.A != 1 {
		t.Errorf("an Update reached the caller's defaults: %+v", defaults)
	}
}

// 8. Close waits for a save in flight and nothing writes after it returns.
func TestCloseQuiescesSaves(t *testing.T) {
	var writes atomic.Int32
	old := moveFileFn
	moveFileFn = func(a, b string) error { writes.Add(1); return old(a, b) }
	t.Cleanup(func() { moveFileFn = old })

	p := filepath.Join(t.TempDir(), "p.json")
	s, _ := Open(p, slowSettings{Theme: "x"}, Options{Debounce: time.Millisecond})
	armGate(t)
	s.Set(slowSettings{S: slow{1}, Theme: "a"}) // the timer's save starts and blocks in encoding
	select {
	case <-slowGate.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the timer save never started")
	}
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	waitClosed := time.Now().Add(5 * time.Second)
	for {
		s.mu.Lock()
		c := s.closed
		s.mu.Unlock()
		if c {
			break
		}
		if time.Now().After(waitClosed) {
			t.Fatal("Close never marked the store closed")
		}
		time.Sleep(time.Millisecond)
	}
	s.Set(slowSettings{S: slow{2}, Theme: "b"}) // a change while Close waits: must not start a timer save
	select {
	case <-closed:
		t.Fatal("Close returned while a save was still running")
	case <-time.After(50 * time.Millisecond):
	}
	openGate()
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	n := writes.Load()
	s.Set(slowSettings{Theme: "late"})
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	if writes.Load() != n {
		t.Errorf("%d writes after Close returned", writes.Load()-n)
	}
	if b := read(t, p); strings.Contains(b, "late") {
		t.Errorf("a change after Close reached the disk:\n%s", b)
	}
	if err := s.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if writes.Load() != n {
		t.Error("the second Close wrote")
	}
}

// 9. OnError runs without the save lock: it can call back into the store.
func TestOnErrorMayCallTheStore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		blocker := filepath.Join(dir, "blocker")
		var calls atomic.Int32
		var s *Store[settings]
		s, _ = Open(filepath.Join(blocker, "p.json"), defaults, Options{Debounce: time.Millisecond, OnError: func(err error) {
			if calls.Add(1) == 1 {
				s.Flush() // takes the save lock
				s.Err()
				s.Get()
			}
		}})
		os.WriteFile(blocker, []byte("a file"), 0o600) // fails only the save, not the initial read
		s.Update(func(v *settings) { v.Theme = "dark" })
		within(t, 5*time.Second, "a save whose OnError calls Flush", func() {
			for calls.Load() < 2 { // the save and the nested Flush both fail
				time.Sleep(time.Millisecond)
			}
		})
	})
}

// 10. Flush failures are failures.
func TestSyncFailuresAreReported(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "p.json")
	if err := WriteAtomic(p, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Run("file", func(t *testing.T) {
		old := syncFileFn
		syncFileFn = func(*os.File, bool) error { return syscall.EIO }
		defer func() { syncFileFn = old }()
		err := WriteAtomic(p, []byte("new"), 0o600)
		if !errors.Is(err, syscall.EIO) {
			t.Fatalf("err %v, want EIO", err)
		}
		if read(t, p) != "old" {
			t.Error("a failed flush replaced the file")
		}
		if ents, _ := os.ReadDir(dir); len(ents) != 1 {
			t.Errorf("temporary file left: %v", ents)
		}
	})
	t.Run("directory", func(t *testing.T) {
		old := syncDirFn
		syncDirFn = func(string) error { return syscall.EIO }
		defer func() { syncDirFn = old }()
		err := WriteAtomic(p, []byte("newer"), 0o600)
		if !errors.Is(err, syscall.EIO) || !strings.Contains(err.Error(), "durable") {
			t.Fatalf("err %v, want EIO saying durability is not known", err)
		}
	})
	t.Run("the store stays dirty and retries", func(t *testing.T) {
		old := syncDirFn
		fail := atomic.Bool{}
		fail.Store(true)
		syncDirFn = func(d string) error {
			if fail.Load() {
				return syscall.EIO
			}
			return old(d)
		}
		defer func() { syncDirFn = old }()
		s, _ := Open(filepath.Join(dir, "s.json"), defaults, Options{Debounce: time.Hour})
		s.Update(func(v *settings) { v.Theme = "durable" })
		if err := s.Flush(); err == nil {
			t.Fatal("Flush hid the failed durability")
		}
		fail.Store(false)
		if err := s.Flush(); err != nil || s.Err() != nil {
			t.Fatalf("retry: %v %v", err, s.Err())
		}
	})
}

func TestFsyncUnsupportedOnlyForUnsupportedErrors(t *testing.T) {
	for _, e := range []error{syscall.ENOTSUP, syscall.EOPNOTSUPP, syscall.EINVAL, fmt.Errorf("wrapped: %w", syscall.EINVAL)} {
		if !fsyncUnsupported(e) {
			t.Errorf("%v should count as unsupported", e)
		}
	}
	for _, e := range []error{syscall.EIO, syscall.ENOSPC, syscall.EBADF, errors.New("x"), nil} {
		if fsyncUnsupported(e) {
			t.Errorf("%v must not count as unsupported", e)
		}
	}
}

func TestSyncDirReportsErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no directory flush; MoveFileEx write-through is used instead")
	}
	dir := t.TempDir()
	if err := syncDir(dir); err != nil {
		t.Errorf("an existing directory: %v", err)
	}
	if err := syncDir(filepath.Join(dir, "missing")); err == nil {
		t.Error("a directory that cannot be opened was reported as flushed")
	}
}

// 12. A directory WriteAtomic creates is made durable in its parent.
func TestNewParentDirectoriesAreSynced(t *testing.T) {
	root := t.TempDir()
	// Only this test's directories: a store an earlier test left open may
	// still save in the background, and its directory is synced through
	// the same hook (CI saw TestFailedDecodeDoesNotTouchDefaults's here).
	var mu sync.Mutex
	var synced []string
	old := syncDirFn
	syncDirFn = func(d string) error {
		if d == root || strings.HasPrefix(d, root+string(filepath.Separator)) {
			mu.Lock()
			synced = append(synced, d)
			mu.Unlock()
		}
		return nil
	}
	defer func() { syncDirFn = old }()
	if err := WriteAtomic(filepath.Join(root, "a", "b", "c", "f.json"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{root, filepath.Join(root, "a"), filepath.Join(root, "a", "b"), filepath.Join(root, "a", "b", "c")} {
		found := false
		for _, d := range synced {
			found = found || d == want
		}
		if !found {
			t.Errorf("%s was not synced; synced %v", want, synced)
		}
	}
	// An existing directory costs one flush, not a walk up the tree.
	mu.Lock()
	synced = nil
	mu.Unlock()
	WriteAtomic(filepath.Join(root, "a", "b", "c", "f.json"), []byte("y"), 0o600)
	mu.Lock()
	defer mu.Unlock()
	if len(synced) != 1 {
		t.Errorf("rewriting in an existing directory synced %v", synced)
	}
}

// 13. A value that is unchanged after a reopen is not rewritten, whatever
// formatting the file on disk has.
func TestReopenedUnchangedValueIsNotRewritten(t *testing.T) {
	p := filepath.Join(t.TempDir(), "p.json")
	compact := `{"Theme":"light","Zoom":1.5}` // not the canonical (indented) form
	os.WriteFile(p, []byte(compact), 0o600)
	s, err := Open(p, defaults, Options{})
	if err != nil {
		t.Fatal(err)
	}
	s.Set(s.Get())
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := read(t, p); got != compact {
		t.Errorf("an unchanged value was rewritten after reopening:\n%s", got)
	}
	s.Update(func(v *settings) { v.Zoom = 2 })
	s.Flush()
	if got := read(t, p); !strings.Contains(got, `"Zoom": 2`) {
		t.Errorf("a changed value was not written:\n%s", got)
	}
}

// Open reads a file that another process holds for a moment (Windows refuses
// the open while a scanner looks at a file just renamed into place) by trying
// again, as rename does; a missing file is not retried, and where nothing
// refuses a read for a while (retryBusy off) an error is final. Before, Open
// returned the defaults and the error at the first refusal (winstate CI on
// Windows: a window's state read back as the zero state).
func TestOpenRetriesARefusedRead(t *testing.T) {
	type cfg struct{ N int }
	p := filepath.Join(t.TempDir(), "p.json")
	if err := os.WriteFile(p, []byte(`{"N":7}`), 0o600); err != nil {
		t.Fatal(err)
	}
	oldRead, oldRetry := readFileFn, retryBusy
	t.Cleanup(func() { readFileFn, retryBusy = oldRead, oldRetry })
	var calls int
	refuse := func(n int) {
		calls = 0
		readFileFn = func(path string) ([]byte, error) {
			if calls++; calls <= n {
				return nil, &os.PathError{Op: "open", Path: path, Err: syscall.Errno(32)} // ERROR_SHARING_VIOLATION
			}
			return os.ReadFile(path)
		}
	}
	retryBusy = true
	refuse(3)
	s, err := Open(p, cfg{}, Options{})
	if err != nil || s.Get().N != 7 || calls != 4 {
		t.Errorf("refused 3 times: err %v, N %d, %d reads; want the file's 7 after 4", err, s.Get().N, calls)
	}
	s.Close()
	refuse(0)
	if s, err := Open(filepath.Join(t.TempDir(), "missing.json"), cfg{N: 1}, Options{}); err != nil || s.Get().N != 1 || calls != 1 {
		t.Errorf("a missing file: err %v, %d reads; want the defaults after one", err, calls)
	}
	retryBusy = false
	refuse(1)
	if _, err := Open(p, cfg{}, Options{}); err == nil || calls != 1 {
		t.Errorf("without retries: err %v, %d reads; want the error after one", err, calls)
	}
}
