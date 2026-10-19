package prefs

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type settings struct {
	Theme  string
	Zoom   float64
	Recent []string
	Nested struct{ A, B int }
}

var defaults = settings{Theme: "system", Zoom: 1, Recent: []string{"x"}}

func waitFile(t *testing.T, path string, cond func([]byte) bool) []byte {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, err := os.ReadFile(path)
		if err == nil && cond(b) {
			return b
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s (last read: %q, %v)", path, b, err)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestWriteAtomicReplacesAndLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "dir", "f.json") // parents are created
	if err := WriteAtomic(p, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(p, []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "two" {
		t.Errorf("content %q", b)
	}
	ents, _ := os.ReadDir(filepath.Dir(p))
	if len(ents) != 1 {
		t.Errorf("directory holds %d entries after two writes, want 1", len(ents))
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
			t.Errorf("mode %v", fi.Mode().Perm())
		}
	}
}

func TestWriteAtomicFailureKeepsOldFileAndLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "target")
	if err := os.Mkdir(p, 0o700); err != nil { // a directory cannot be replaced by a file
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(p, "keep"), []byte("k"), 0o600)
	if err := WriteAtomic(p, []byte("x"), 0o600); err == nil {
		t.Fatal("replaced a non-empty directory with a file")
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Errorf("temporary file left behind: %v", ents)
	}
}

// A reader that opens the file at any moment during a stream of writes sees one
// complete version, never a prefix or a mixture.
func TestWriteAtomicReadersNeverSeeAPartialFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	a := bytes.Repeat([]byte("A"), 300_000)
	b := bytes.Repeat([]byte("B"), 150_000)
	if err := WriteAtomic(p, a, 0o600); err != nil {
		t.Fatal(err)
	}
	var stop atomic.Bool
	var bad atomic.Int32
	var reads atomic.Int64
	var wg sync.WaitGroup
	for r := 0; r < 3; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				time.Sleep(100 * time.Microsecond) // Windows: an open file blocks the rename, so readers must leave gaps
				got, err := os.ReadFile(p)
				if err != nil {
					continue // Windows may refuse an open during a rename
				}
				reads.Add(1)
				if !bytes.Equal(got, a) && !bytes.Equal(got, b) {
					bad.Add(1)
				}
			}
		}()
	}
	for i := 0; i < 60; i++ {
		d := a
		if i%2 == 0 {
			d = b
		}
		if err := WriteAtomic(p, d, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stop.Store(true)
	wg.Wait()
	if bad.Load() != 0 {
		t.Errorf("%d of %d reads saw a partial or mixed file", bad.Load(), reads.Load())
	}
}

func TestFullSync(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	if err := writeAtomic(p, []byte("x"), 0o600, true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "x" {
		t.Errorf("content %q", b)
	}
}

func TestOpenMissingGivesDefaultsAndSetIsNotSynchronous(t *testing.T) {
	p := filepath.Join(t.TempDir(), "p.json")
	s, err := Open(p, defaults, Options{Debounce: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Get(); got.Theme != "system" || got.Zoom != 1 {
		t.Fatalf("defaults %+v", got)
	}
	s.Update(func(v *settings) { v.Theme = "dark" })
	if _, err := os.Stat(p); err == nil {
		t.Error("Update wrote the file before the debounce")
	}
	if got := s.Get(); got.Theme != "dark" {
		t.Errorf("Get after Update: %+v", got)
	}
	if err := s.Close(); err != nil { // writes what is pending
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil || !strings.Contains(string(b), `"Theme": "dark"`) {
		t.Fatalf("file %q, %v", b, err)
	}
}

func TestMissingFieldsKeepDefaultsAndRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "p.json")
	os.WriteFile(p, []byte(`{"Theme":"light","Unknown":1,"Nested":{"A":3}}`), 0o600)
	s, err := Open(p, defaults, Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := s.Get()
	if got.Theme != "light" || got.Zoom != 1 || len(got.Recent) != 1 || got.Nested.A != 3 {
		t.Fatalf("loaded %+v", got)
	}
	s.Set(settings{Theme: "x", Zoom: 2, Recent: []string{"a", "b"}})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(p, defaults, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if g := s2.Get(); g.Theme != "x" || g.Zoom != 2 || len(g.Recent) != 2 || g.Recent[1] != "b" {
		t.Errorf("reloaded %+v", g)
	}
}

func TestDebounceSavesInTheBackgroundAndDoesNotStarve(t *testing.T) {
	p := filepath.Join(t.TempDir(), "p.json")
	s, _ := Open(p, defaults, Options{Debounce: 40 * time.Millisecond})
	defer s.Close()
	// A change every 5 ms for 400 ms: the wait does not restart, so the file
	// appears while changes are still arriving.
	end := time.Now().Add(400 * time.Millisecond)
	var seen bool
	for i := 0; time.Now().Before(end); i++ {
		s.Update(func(v *settings) { v.Zoom = float64(i + 2) })
		if _, err := os.Stat(p); err == nil {
			seen = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !seen {
		t.Fatal("no save during a stream of changes")
	}
	s.Set(settings{Theme: "final"})
	waitFile(t, p, func(b []byte) bool { return strings.Contains(string(b), `"final"`) })
}

func TestCorruptFileIsKeptAsideAndStoreStillWorks(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "p.json")
	os.WriteFile(p, []byte(`{"Theme": `), 0o600)
	s, err := Open(p, defaults, Options{})
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("err %v, want ErrCorrupt", err)
	}
	if s == nil || s.Get().Theme != "system" {
		t.Fatalf("store %v", s)
	}
	if b, _ := os.ReadFile(p + ".corrupt"); string(b) != `{"Theme": ` {
		t.Errorf("corrupt file not kept: %q", b)
	}
	s.Update(func(v *settings) { v.Theme = "dark" })
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(p, defaults, Options{})
	if err != nil || s2.Get().Theme != "dark" {
		t.Errorf("after recovery: %v %+v", err, s2.Get())
	}
}

func TestUnreadableFileReportsErrorAndWorks(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "p.json")
	os.Mkdir(p, 0o700) // reading a directory fails
	s, err := Open(p, defaults, Options{})
	if err == nil || errors.Is(err, ErrCorrupt) {
		t.Fatalf("err %v", err)
	}
	if s == nil || s.Get().Theme != "system" {
		t.Fatal("no usable store")
	}
}

func TestSaveErrorIsReportedAndRetried(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	os.WriteFile(blocker, []byte("a file"), 0o600)
	p := filepath.Join(blocker, "p.json") // its parent is a file: cannot be written
	var calls atomic.Int32
	s, _ := Open(p, defaults, Options{Debounce: time.Millisecond, OnError: func(error) { calls.Add(1) }})
	s.Update(func(v *settings) { v.Theme = "dark" })
	deadline := time.Now().Add(5 * time.Second)
	for s.Err() == nil {
		if time.Now().After(deadline) {
			t.Fatal("the failed save was not reported")
		}
		time.Sleep(time.Millisecond)
	}
	if calls.Load() == 0 {
		t.Error("OnError not called")
	}
	if err := s.Flush(); err == nil {
		t.Error("Flush hid the error")
	}
	os.Remove(blocker) // the disk problem goes away
	if err := s.Flush(); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if s.Err() != nil {
		t.Errorf("Err %v after a good save", s.Err())
	}
	if b, _ := os.ReadFile(p); !strings.Contains(string(b), "dark") {
		t.Errorf("file %q", b)
	}
}

func TestUnchangedValueIsNotRewritten(t *testing.T) {
	p := filepath.Join(t.TempDir(), "p.json")
	s, _ := Open(p, defaults, Options{})
	s.Set(settings{Theme: "a"})
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(p, []byte("marker"), 0o600)
	s.Set(settings{Theme: "a"})
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "marker" {
		t.Errorf("an unchanged value was written again: %q", b)
	}
	s.Set(settings{Theme: "b"})
	s.Flush()
	if b, _ := os.ReadFile(p); !strings.Contains(string(b), `"b"`) {
		t.Errorf("a changed value was not written: %q", b)
	}
}

func TestAfterCloseChangesStayInMemory(t *testing.T) {
	p := filepath.Join(t.TempDir(), "p.json")
	s, _ := Open(p, defaults, Options{Debounce: time.Millisecond})
	s.Close()
	s.Set(settings{Theme: "late"})
	time.Sleep(30 * time.Millisecond)
	if _, err := os.Stat(p); err == nil {
		t.Error("a closed store wrote")
	}
	if s.Get().Theme != "late" {
		t.Error("value lost")
	}
}

func TestConcurrentUse(t *testing.T) {
	p := filepath.Join(t.TempDir(), "p.json")
	s, _ := Open(p, defaults, Options{Debounce: time.Millisecond})
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				s.Update(func(v *settings) { v.Nested.A++ })
				_ = s.Get()
				if i%100 == 0 {
					s.Flush()
				}
			}
		}()
	}
	wg.Wait()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, _ := Open(p, defaults, Options{})
	if s2.Get().Nested.A != 1200 {
		t.Errorf("counter %d, want 1200", s2.Get().Nested.A)
	}
}

// What the UI thread calls allocates nothing.
func TestUIThreadCallsAllocateNothing(t *testing.T) {
	type small struct {
		Zoom  float64
		Theme int
	}
	p := filepath.Join(t.TempDir(), "p.json")
	s, _ := Open(p, small{}, Options{Debounce: time.Hour})
	z := 0.0
	if a := testing.AllocsPerRun(200, func() {
		z++
		s.Set(small{Zoom: z})
		_ = s.Get()
		s.Update(func(v *small) { v.Theme++ })
	}); a != 0 {
		t.Errorf("Get/Set/Update allocate %v", a)
	}
}
