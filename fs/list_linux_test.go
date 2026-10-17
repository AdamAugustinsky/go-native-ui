//go:build linux

package fs

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"
)

// These tests run on the real file systems of the CI runners. Behaviour
// that depends on the environment fails there (CI is set) instead of
// skipping, so a missing capability can't hide a regression.

func ostStrict() bool { return os.Getenv("CI") != "" }

// ostCannot reports that the environment lacks a capability: a skip locally,
// a failure on CI.
func ostCannot(t *testing.T, format string, args ...any) {
	t.Helper()
	if ostStrict() {
		t.Fatalf("CI requires this capability: "+format, args...)
	}
	t.Skipf(format, args...)
}

// lxListers are the listing variants under test.
var lxListers = []struct {
	name string
	fn   func(string, *Listing) error
}{
	{"getdents64+statx", lxListStatx},
	{"os.ReadDir", lxListReadDir},
	{"listDir", listDir},
}

type lxWant struct {
	kind  Kind
	size  int64
	has   uint8 // flags that must be set
	hasNo uint8 // flags that must not be set
}

var lxMtime = time.Date(2021, 3, 4, 5, 6, 7, 123456789, time.UTC)

func lxFixture(t *testing.T) (string, map[string]lxWant) {
	t.Helper()
	dir := t.TempDir()
	want := map[string]lxWant{}
	file := func(name string, size int, mode os.FileMode, w lxWant) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(strings.Repeat("z", size)), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, lxMtime, lxMtime); err != nil {
			t.Fatal(err)
		}
		w.kind, w.size = KindFile, int64(size)
		want[name] = w
	}
	const plain = FlagExecutable | FlagHidden | FlagSymlinkDir | FlagReadOnly
	file("plain.txt", 5, 0o644, lxWant{hasNo: plain})
	file("é.txt", 1, 0o644, lxWant{hasNo: plain})
	file("日本語.txt", 2, 0o644, lxWant{hasNo: plain})
	file("😀 emoji.md", 3, 0o644, lxWant{hasNo: plain})
	file(strings.Repeat("n", 251)+".txt", 4, 0o644, lxWant{hasNo: plain}) // 255 bytes: NAME_MAX
	file(".hidden", 6, 0o644, lxWant{has: FlagHidden, hasNo: FlagExecutable | FlagReadOnly})
	file("run.sh", 7, 0o755, lxWant{has: FlagExecutable, hasNo: FlagHidden | FlagReadOnly})
	file("only-group-x", 8, 0o610, lxWant{has: FlagExecutable})
	file("ro.txt", 9, 0o444, lxWant{has: FlagReadOnly, hasNo: FlagExecutable})
	file("empty", 0, 0o644, lxWant{})

	for _, d := range []struct {
		name string
		w    lxWant
	}{{"sub", lxWant{hasNo: FlagHidden}}, {".hdir", lxWant{has: FlagHidden}}} {
		if err := os.Mkdir(filepath.Join(dir, d.name), 0o755); err != nil {
			t.Fatal(err)
		}
		d.w.kind = KindDir
		want[d.name] = d.w
	}
	link := func(name, target string, w lxWant) {
		t.Helper()
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
		w.kind = KindSymlink
		want[name] = w
	}
	link("ln-file", "plain.txt", lxWant{size: int64(len("plain.txt")), hasNo: FlagSymlinkDir | FlagExecutable})
	link("ln-dir", "sub", lxWant{has: FlagSymlinkDir})
	link("ln-dead", "does-not-exist", lxWant{size: int64(len("does-not-exist")), hasNo: FlagSymlinkDir | FlagExecutable})
	link("ln-exe", "run.sh", lxWant{size: int64(len("run.sh")), has: FlagExecutable, hasNo: FlagSymlinkDir})
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o644); err != nil {
		t.Fatal(err)
	}
	want["pipe"] = lxWant{kind: KindOther}
	return dir, want
}

func TestLinuxListFixture(t *testing.T) {
	dir, want := lxFixture(t)
	for _, v := range lxListers {
		t.Run(v.name, func(t *testing.T) {
			l := new(Listing)
			if err := v.fn(dir, l); err != nil {
				t.Fatal(err)
			}
			got := entries(l)
			if len(l.Entries) != len(want) || len(got) != len(want) {
				t.Fatalf("listed %d entries (%d distinct), want %d", len(l.Entries), len(got), len(want))
			}
			total := 0
			for name, w := range want {
				e, ok := got[name]
				if !ok {
					t.Errorf("%q missing", name)
					continue
				}
				total += int(e.NameLen)
				if e.Kind != w.kind {
					t.Errorf("%q: kind %d, want %d", name, e.Kind, w.kind)
				}
				if w.kind != KindOther && e.Size != w.size {
					t.Errorf("%q: size %d, want %d", name, e.Size, w.size)
				}
				if e.Flags&w.has != w.has || e.Flags&w.hasNo != 0 {
					t.Errorf("%q: flags %04b, want %04b set and %04b clear", name, e.Flags, w.has, w.hasNo)
				}
				if w.kind == KindFile {
					if d := time.Duration(e.MTime - lxMtime.UnixNano()); d < -2*time.Second || d > 2*time.Second {
						t.Errorf("%q: mtime off by %v", name, d)
					}
				} else if time.Since(time.Unix(0, e.MTime)) > time.Hour || e.MTime == 0 {
					t.Errorf("%q: implausible mtime %v", name, time.Unix(0, e.MTime))
				}
			}
			if total != len(l.Names) {
				t.Errorf("names arena %d bytes, entries sum %d", len(l.Names), total)
			}
			for _, dot := range []string{".", ".."} {
				if _, ok := got[dot]; ok {
					t.Errorf("%q listed", dot)
				}
			}
		})
	}
}

// The two implementations must agree exactly, entry for entry.
func TestLinuxListVariantsAgree(t *testing.T) {
	dir, _ := lxFixture(t)
	for i := 0; i < 3000; i++ { // more than one getdents64 buffer of long names
		p := filepath.Join(dir, fmt.Sprintf("bulk-entry-with-a-longish-name-%05d.dat", i))
		if err := os.WriteFile(p, make([]byte, i%97), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a, b := new(Listing), new(Listing)
	if err := lxListStatx(dir, a); err != nil {
		t.Fatal(err)
	}
	if err := lxListReadDir(dir, b); err != nil {
		t.Fatal(err)
	}
	ea, eb := entries(a), entries(b)
	if len(a.Entries) != len(b.Entries) || len(ea) != len(eb) {
		t.Fatalf("statx listed %d, ReadDir %d", len(a.Entries), len(b.Entries))
	}
	for name, x := range ea {
		y, ok := eb[name]
		if !ok {
			t.Errorf("%q only in statx listing", name)
			continue
		}
		x.NameOff, y.NameOff = 0, 0
		if x != y {
			t.Errorf("%q: statx %+v, ReadDir %+v", name, x, y)
		}
	}
}

func TestLinuxListErrors(t *testing.T) {
	dir := t.TempDir()
	for _, v := range lxListers {
		t.Run(v.name, func(t *testing.T) {
			if err := v.fn(filepath.Join(dir, "missing"), new(Listing)); !os.IsNotExist(err) {
				t.Errorf("missing dir: %v", err)
			}
			f := filepath.Join(dir, "file")
			if err := os.WriteFile(f, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := v.fn(f, new(Listing)); err == nil {
				t.Error("listing a regular file succeeded")
			}
			if err := v.fn(dir+"\x00x", new(Listing)); err == nil {
				t.Error("path with NUL succeeded")
			}
			if err := v.fn("", new(Listing)); err == nil {
				t.Error("empty path succeeded")
			}
			l := new(Listing)
			if err := v.fn(t.TempDir(), l); err != nil || len(l.Entries) != 0 {
				t.Errorf("empty dir: %d entries, err %v", len(l.Entries), err)
			}
		})
	}
	if err := lxListStatx(strings.Repeat("a/", 3000), new(Listing)); err == nil {
		t.Error("over-long path succeeded")
	}
	// Unreadable directory: running as root defeats this check.
	if os.Geteuid() != 0 {
		p := filepath.Join(dir, "locked")
		if err := os.Mkdir(p, 0o000); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(p, 0o755)
		if err := listDir(p, new(Listing)); !os.IsPermission(err) {
			t.Errorf("unreadable dir: %v", err)
		}
	}
}

// A Listing reused after Reset lists the same thing every time.
func TestLinuxListReuse(t *testing.T) {
	dir, want := lxFixture(t)
	l := new(Listing)
	for i := 0; i < 3; i++ {
		l.Reset()
		if err := listDir(dir, l); err != nil {
			t.Fatal(err)
		}
		if len(l.Entries) != len(want) {
			t.Fatalf("round %d: %d entries, want %d", i, len(l.Entries), len(want))
		}
	}
}

// statx unavailable (old kernel, seccomp): listDir switches to the ReadDir
// variant and keeps working.
func TestLinuxListStatxFallback(t *testing.T) {
	dir, want := lxFixture(t)
	old := lxStatxState.Load()
	defer lxStatxState.Store(old)
	lxStatxState.Store(2)
	l := new(Listing)
	if err := listDir(dir, l); err != nil || len(l.Entries) != len(want) {
		t.Fatalf("fallback listing: %d entries, err %v", len(l.Entries), err)
	}
}

func TestLinuxListZeroAlloc(t *testing.T) {
	mk := func(n int) string {
		dir := t.TempDir()
		for i := 0; i < n; i++ {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("entry-%05d.txt", i)), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Symlink("entry-00000.txt", filepath.Join(dir, "link")); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	small, big := mk(10), mk(3000)
	measure := func(dir string) uint64 {
		l := new(Listing)
		for i := 0; i < 3; i++ { // grow the Listing and warm the scratch pool
			l.Reset()
			if err := lxListStatx(dir, l); err != nil {
				t.Fatal(err)
			}
		}
		const reps = 20
		runtime.GC()
		var a, b runtime.MemStats
		runtime.ReadMemStats(&a)
		for i := 0; i < reps; i++ {
			l.Reset()
			lxListStatx(dir, l)
		}
		runtime.ReadMemStats(&b)
		return b.Mallocs - a.Mallocs
	}
	ms, mb := measure(small), measure(big)
	t.Logf("20 warm listings: %d mallocs for 11 entries, %d for 3001", ms, mb)
	if mb > ms+5 { // background noise only; a per-entry allocation would add 60,000
		t.Errorf("allocations scale with entries: %d (11 entries) vs %d (3001 entries)", ms, mb)
	}
	if mb > 20 { // at most one per listing
		t.Errorf("%d mallocs over 20 listings", mb)
	}
}

// TestLinuxListBackendChoice times getdents64+statx against os.ReadDir on a
// 100k-entry directory and says which wins (DECISIONS D12). It does not
// assert a winner: CI runners are noisy; the numbers are the product.
func TestLinuxListBackendChoice(t *testing.T) {
	dir := bigDirPath(t)
	times := map[string]time.Duration{}
	counts := map[string]int{}
	for _, v := range lxListers[:2] {
		l := new(Listing)
		best := time.Hour
		for i := 0; i < 7; i++ {
			l.Reset()
			t0 := time.Now()
			if err := v.fn(dir, l); err != nil {
				t.Fatal(err)
			}
			if d := time.Since(t0); d < best {
				best = d
			}
		}
		times[v.name], counts[v.name] = best, len(l.Entries)
		t.Logf("%-18s best of 7: %v for %d entries (%.0f ns/entry)", v.name, best, len(l.Entries), float64(best.Nanoseconds())/float64(len(l.Entries)))
	}
	for _, n := range counts {
		if n != bigN {
			t.Fatalf("listed %d entries, want %d", n, bigN)
		}
	}
	names := []string{lxListers[0].name, lxListers[1].name}
	sort.Slice(names, func(i, j int) bool { return times[names[i]] < times[names[j]] })
	t.Logf("LIST BACKEND CHOICE (linux/%s): %s is faster: %v vs %v (%.2fx)",
		runtime.GOARCH, names[0], times[names[0]], times[names[1]], float64(times[names[1]])/float64(times[names[0]]))
}

func BenchmarkLinuxList100k(b *testing.B) {
	dir := bigDirPath(b)
	for _, v := range lxListers[:2] {
		b.Run(v.name, func(b *testing.B) {
			l := new(Listing)
			v.fn(dir, l)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				l.Reset()
				v.fn(dir, l)
			}
		})
	}
}
