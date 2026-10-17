package fs

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func tempDirWith(t *testing.T, n int) string {
	t.Helper()
	dir := t.TempDir()
	for i := 0; i < n; i++ {
		name := filepath.Join(dir, fmt.Sprintf("file-%03d.txt", i))
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDirMTime(t *testing.T) {
	dir := tempDirWith(t, 3)
	want := time.Date(2026, 3, 14, 9, 26, 53, 589_793_000, time.UTC)
	_ = want
	if err := os.Chtimes(dir, want, want); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := dirMTime(dir)
	if !ok || got != fi.ModTime().UnixNano() {
		t.Fatalf("dirMTime = %d, %v; os.Stat says %d", got, ok, fi.ModTime().UnixNano())
	}
	// a change moves it
	later := want.Add(90 * time.Minute)
	if err := os.Chtimes(dir, later, later); err != nil {
		t.Fatal(err)
	}
	if got2, ok := dirMTime(dir); !ok || got2 <= got {
		t.Errorf("after Chtimes: %d, %v (was %d)", got2, ok, got)
	}
	// the other cases of the old os.Stat: a missing path, a path with a NUL, a symlink's target
	if _, ok := dirMTime(filepath.Join(dir, "nope")); ok {
		t.Error("a missing directory has no mtime")
	}
	if _, ok := dirMTime(dir + "\x00x"); ok {
		t.Error("a path with a NUL has no mtime")
	}
	if _, ok := dirMTime(""); ok {
		t.Error("the empty path has no mtime")
	}
	if got3, ok := dirMTime(filepath.Join(dir, "sub")); !ok || got3 == 0 {
		t.Errorf("sub: %d, %v", got3, ok)
	}
}

// The directory's mtime is read on every relist of a watched directory: it
// must not allocate (os.Stat made a FileInfo and a path string each time).
func TestDirMTimeAllocatesNothing(t *testing.T) {
	dir := tempDirWith(t, 3)
	if n := testing.AllocsPerRun(200, func() {
		if _, ok := dirMTime(dir); !ok {
			panic("no mtime")
		}
	}); n != 0 {
		t.Errorf("dirMTime allocates %v times", n)
	}
}

// What a worker does for one relist of a watched directory, on a Listing that
// has been through the pool before: mtime, list, sort.
func TestRelistWorkAllocatesNothing(t *testing.T) {
	dir := tempDirWith(t, 400)
	l := new(Listing)
	one := func(key SortKey) {
		l.Reset()
		if _, ok := dirMTime(dir); !ok {
			panic("no mtime")
		}
		if err := listDir(dir, l); err != nil {
			panic(err)
		}
		Sort(l, key, false)
	}
	for k := SortName; k <= SortKind; k++ {
		one(k)
	}
	for k := SortName; k <= SortKind; k++ {
		if n := testing.AllocsPerRun(50, func() { one(k) }); n != 0 {
			t.Errorf("a relist sorted by key %d allocates %v times", k, n)
		}
	}
}

// The whole round trip through a Service, a watched directory included: List,
// the worker's mtime + list + sort, the ring, Poll, Release. With one P the
// runtime's background allocations (a sudog on a P without one) cannot happen,
// so the count is exact.
func TestServiceRelistAllocatesNothing(t *testing.T) {
	dir := tempDirWith(t, 400)
	s := NewService(func() {})
	defer s.Close()
	s.List(1, dir, SortName, false)
	s.Watch(1, dir)
	var res []Result
	round := func() {
		s.List(1, dir, SortName, false)
		deadline := time.Now().Add(10 * time.Second)
		for got := false; !got; {
			res = s.Poll(res[:0])
			for i := range res {
				if res[i].Kind == ResultListing {
					if res[i].Listing.Err != nil {
						panic(res[i].Listing.Err)
					}
					s.Release(res[i].Listing)
					got = true
				}
			}
			if !got {
				if time.Now().After(deadline) {
					panic("no listing")
				}
				time.Sleep(50 * time.Microsecond)
			}
		}
	}
	for i := 0; i < 8; i++ { // first listings, the watch coming up
		round()
	}
	time.Sleep(200 * time.Millisecond) // let the watcher settle: no stray refreshes in the window
	res = s.Poll(res[:0])
	for i := range res {
		s.Release(res[i].Listing)
	}
	old := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(old)
	for i := 0; i < 4; i++ {
		round()
	}
	if n := testing.AllocsPerRun(30, round); n != 0 {
		t.Errorf("a watched relist through the Service allocates %v times", n)
	}
}
