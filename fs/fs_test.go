package fs

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	code := m.Run()
	if bigDir.path != "" {
		os.RemoveAll(bigDir.path)
	}
	os.Exit(code)
}

// report logs a measurement and, when GNUI_FS_REPORT names a file, appends
// it there too, so CI can show the numbers without running the tests -v.
func report(t testing.TB, format string, args ...any) {
	t.Helper()
	t.Logf(format, args...)
	if p := os.Getenv("GNUI_FS_REPORT"); p != "" {
		if f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintf(f, "%s/%s %s: %s\n", runtime.GOOS, runtime.GOARCH, t.Name(), fmt.Sprintf(format, args...))
			f.Close()
		}
	}
}

// bigDir is a 100k-file directory created once and shared by the tests and
// benchmarks that need it.
var bigDir struct {
	once sync.Once
	path string
	err  error
}

const bigN = 100_000

func bigDirPath(tb testing.TB) string {
	if testing.Short() {
		tb.Skip("100k-entry directory skipped under -short")
	}
	bigDir.once.Do(func() {
		dir, err := os.MkdirTemp("", "fs100k")
		if err != nil {
			bigDir.err = err
			return
		}
		bigDir.path = dir
		for i := 0; i < bigN; i++ {
			name := fmt.Sprintf("IMG_%06d_%s.jpg", (i*7919)%bigN, []string{"holiday", "Family", "work", "scan", "Draft"}[i%5])
			if i%20 == 0 {
				name = fmt.Sprintf("folder %d", i)
				if err = os.Mkdir(filepath.Join(dir, name), 0o755); err != nil {
					break
				}
				continue
			}
			var f *os.File
			if f, err = os.Create(filepath.Join(dir, name)); err != nil {
				break
			}
			f.Close()
		}
		bigDir.err = err
	})
	if bigDir.err != nil {
		tb.Fatal(bigDir.err)
	}
	return bigDir.path
}

func listOf(tb testing.TB, path string) *Listing {
	tb.Helper()
	l := new(Listing)
	if err := listDir(path, l); err != nil {
		tb.Fatal(err)
	}
	return l
}

func entries(l *Listing) map[string]Entry {
	m := make(map[string]Entry, len(l.Entries))
	for i := range l.Entries {
		m[string(l.Name(i))] = l.Entries[i]
	}
	return m
}

func TestListDir(t *testing.T) {
	dir := t.TempDir()
	mtime := time.Date(2021, 3, 4, 5, 6, 7, 0, time.UTC)
	long := strings.Repeat("x", 200) + ".txt"
	files := map[string]int{
		"a.txt": 5, "B.txt": 0, "file2": 1, "file10": 2, "é.txt": 3, "日本語.txt": 4,
		"😀 emoji.md": 6, long: 7, ".hidden": 8, "no ext": 9, "script.sh": 10,
	}
	for name, n := range files {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(strings.Repeat("z", n)), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(dir, "script.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	links := os.Symlink("a.txt", filepath.Join(dir, "link")) == nil &&
		os.Symlink("sub", filepath.Join(dir, "dirlink")) == nil

	l := listOf(t, dir)
	got := entries(l)
	want := len(files) + 1
	if links {
		want += 2
	}
	if len(l.Entries) != want || len(got) != want {
		t.Fatalf("listed %d entries (%d distinct), want %d: %v", len(l.Entries), len(got), want, slices.Sorted(mapKeys(got)))
	}
	for name, n := range files {
		e, ok := got[name]
		if !ok {
			t.Errorf("%q missing", name)
			continue
		}
		if e.Kind != KindFile || e.Size != int64(n) {
			t.Errorf("%q: kind %d size %d, want file %d", name, e.Kind, e.Size, n)
		}
		if d := time.Duration(e.MTime - mtime.UnixNano()); d < -2*time.Second || d > 2*time.Second {
			t.Errorf("%q: mtime off by %v", name, d)
		}
		if int(e.NameLen) != len(name) {
			t.Errorf("%q: NameLen %d", name, e.NameLen)
		}
	}
	if e := got["sub"]; e.Kind != KindDir || e.Size != 0 {
		t.Errorf("sub: %+v", e)
	}
	if links {
		if e := got["link"]; e.Kind != KindSymlink {
			t.Errorf("link: %+v", e)
		}
		if e := got["dirlink"]; e.Kind != KindSymlink || e.Flags&FlagSymlinkDir == 0 {
			t.Errorf("dirlink: %+v", e)
		}
		if got["link"].Flags&FlagSymlinkDir != 0 {
			t.Error("file link marked as directory link")
		}
	}
	if runtime.GOOS != "windows" {
		if got[".hidden"].Flags&FlagHidden == 0 {
			t.Error(".hidden not hidden")
		}
		if got["a.txt"].Flags&FlagHidden != 0 {
			t.Error("a.txt hidden")
		}
		if got["script.sh"].Flags&FlagExecutable == 0 || got["a.txt"].Flags&FlagExecutable != 0 {
			t.Errorf("executable flags: script.sh %b a.txt %b", got["script.sh"].Flags, got["a.txt"].Flags)
		}
	}
	// Names are UTF-8 back to back and entries index them correctly.
	var total int
	for i := range l.Entries {
		total += int(l.Entries[i].NameLen)
	}
	if total != len(l.Names) {
		t.Errorf("names arena %d bytes, entries sum %d", len(l.Names), total)
	}
}

func mapKeys[M ~map[K]V, K comparable, V any](m M) func(yield func(K) bool) {
	return func(yield func(K) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

func TestListDirErrors(t *testing.T) {
	dir := t.TempDir()
	if err := listDir(filepath.Join(dir, "missing"), new(Listing)); !os.IsNotExist(err) {
		t.Errorf("missing dir: %v", err)
	}
	f := filepath.Join(dir, "file")
	os.WriteFile(f, nil, 0o644)
	if err := listDir(f, new(Listing)); err == nil {
		t.Error("listing a file succeeded")
	}
	// An empty directory lists as zero entries.
	l := listOf(t, t.TempDir())
	if len(l.Entries) != 0 {
		t.Errorf("empty dir: %d entries", len(l.Entries))
	}
}

// A directory bigger than one getattrlistbulk / getdents buffer.
func TestListDirManyEntries(t *testing.T) {
	n := 5000
	if testing.Short() {
		n = 1500
	}
	dir := t.TempDir()
	for i := 0; i < n; i++ {
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("file-with-a-reasonably-long-name-%05d.dat", i)), []byte("x"), 0o644)
	}
	l := listOf(t, dir)
	if len(l.Entries) != n {
		t.Fatalf("listed %d, want %d", len(l.Entries), n)
	}
	seen := make(map[string]bool, n)
	for i := range l.Entries {
		seen[string(l.Name(i))] = true
		if l.Entries[i].Size != 1 {
			t.Fatalf("entry %d size %d", i, l.Entries[i].Size)
		}
	}
	if len(seen) != n {
		t.Fatalf("%d distinct names, want %d", len(seen), n)
	}
}

func TestListDirZeroAlloc(t *testing.T) {
	dir := t.TempDir()
	const n = 10_000
	for i := 0; i < n; i++ {
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("entry-%05d.txt", i)), nil, 0o644)
	}
	l := new(Listing)
	for i := 0; i < 3; i++ { // warm the Listing and the backend's scratch
		l.Reset()
		if err := listDir(dir, l); err != nil {
			t.Fatal(err)
		}
	}
	const reps = 10
	runtime.GC()
	var a, b runtime.MemStats
	runtime.ReadMemStats(&a)
	for i := 0; i < reps; i++ {
		l.Reset()
		listDir(dir, l)
	}
	runtime.ReadMemStats(&b)
	mallocs, bytes := b.Mallocs-a.Mallocs, b.TotalAlloc-a.TotalAlloc
	t.Logf("%d listings of %d entries: %d mallocs, %d bytes", reps, n, mallocs, bytes)
	if len(l.Entries) != n {
		t.Fatalf("%d entries", len(l.Entries))
	}
	// Allow a constant (path strings, error values, runtime noise), never
	// anything that scales with the entries.
	if mallocs > 5*reps || bytes > 2048*reps {
		t.Errorf("listDir allocates: %d mallocs, %d bytes over %d listings of %d entries", mallocs, bytes, reps, n)
	}
}

func TestBig100k(t *testing.T) {
	dir := bigDirPath(t)
	l := new(Listing)
	best := func(f func()) time.Duration {
		b := time.Hour
		for i := 0; i < 7; i++ {
			t0 := time.Now()
			f()
			if d := time.Since(t0); d < b {
				b = d
			}
		}
		return b
	}
	list := best(func() { l.Reset(); listDir(dir, l) })
	if len(l.Entries) != bigN {
		t.Fatalf("listed %d, want %d", len(l.Entries), bigN)
	}
	listSort := best(func() { l.Reset(); listDir(dir, l); Sort(l, SortName, false) })
	sortOnly := best(func() { Sort(l, SortName, false) })
	sortSize := best(func() { Sort(l, SortSize, true) })
	sortMod := best(func() { Sort(l, SortModified, false) })
	Sort(l, SortName, false)
	var idx []uint32
	q := []byte("family")
	filter := best(func() { idx = Filter(l, q, idx[:0]) })
	perEntry := float64(len(l.Names)+len(l.Entries)*24+len(l.Order)*4) / float64(len(l.Entries))
	report(t, "100k: list %v, list+sort(name) %v, sort(name) %v, sort(size) %v, sort(mtime) %v, filter(%q → %d) %v; %.1f B/entry incl. names and order (names %.1f B avg)",
		list, listSort, sortOnly, sortSize, sortMod, q, len(idx), filter, perEntry, float64(len(l.Names))/float64(len(l.Entries)))
	if listSort > 600*time.Millisecond { // criterion is 150 ms on the target Mac; this only catches disasters on shared CI
		t.Errorf("list+sort of 100k took %v", listSort)
	}
}

func BenchmarkList100k(b *testing.B) {
	dir := bigDirPath(b)
	l := new(Listing)
	listDir(dir, l)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.Reset()
		listDir(dir, l)
	}
}

func BenchmarkListSort100k(b *testing.B) {
	dir := bigDirPath(b)
	l := new(Listing)
	listDir(dir, l)
	Sort(l, SortName, false)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.Reset()
		listDir(dir, l)
		Sort(l, SortName, false)
	}
}

func BenchmarkSort100k(b *testing.B) {
	l := listOf(b, bigDirPath(b))
	Sort(l, SortName, false)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Sort(l, SortName, false)
	}
}

func BenchmarkFilter100k(b *testing.B) {
	l := listOf(b, bigDirPath(b))
	Sort(l, SortName, false)
	idx := make([]uint32, 0, len(l.Entries))
	q := []byte("family")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx = Filter(l, q, idx[:0])
	}
}
