//go:build windows

package fs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf16"
	"unicode/utf8"
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

// winListers are the listing variants under test.
var winListers = []struct {
	name string
	fn   func(string, *Listing) error
}{
	{"FindFirstFileExW", winListFind},
	{"ByHandle/FileIdExtdDirectoryInfo", winListExtd},
	{"ByHandle/FileFullDirectoryInfo", winListFull},
	{"listDir", listDir},
}

type winWant struct {
	kind  Kind
	size  int64
	has   uint8 // flags that must be set
	hasNo uint8 // flags that must not be set
}

var winMtime = time.Date(2021, 3, 4, 5, 6, 7, 123456700, time.UTC)

func winSetAttrs(t *testing.T, path string, attrs uint32) {
	t.Helper()
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.SetFileAttributes(p, attrs); err != nil {
		t.Fatal(err)
	}
}

func winFixture(t *testing.T) (string, map[string]winWant) {
	t.Helper()
	dir := t.TempDir()
	want := map[string]winWant{}
	const plain = FlagExecutable | FlagHidden | FlagSymlinkDir | FlagReadOnly
	file := func(name string, size int, w winWant) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(strings.Repeat("z", size)), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, winMtime, winMtime); err != nil {
			t.Fatal(err)
		}
		w.kind, w.size = KindFile, int64(size)
		want[name] = w
	}
	file("plain.txt", 5, winWant{hasNo: plain})
	file("é.txt", 1, winWant{hasNo: plain})
	file("日本語.txt", 2, winWant{hasNo: plain})
	file("😀 emoji.md", 3, winWant{hasNo: plain})                    // a surrogate pair
	file(strings.Repeat("😀", 100)+".txt", 4, winWant{hasNo: plain}) // 204 UTF-16 units, 404 UTF-8 bytes
	file(strings.Repeat("n", 251)+".txt", 4, winWant{hasNo: plain}) // 255 units: NAME_MAX
	file("with space and 'quotes' & ünï.txt", 1, winWant{hasNo: plain})
	file("empty", 0, winWant{hasNo: plain})
	file(".dotfile", 6, winWant{hasNo: FlagHidden}) // a leading dot means nothing on Windows

	file("hidden.txt", 7, winWant{has: FlagHidden, hasNo: FlagExecutable | FlagReadOnly})
	winSetAttrs(t, filepath.Join(dir, "hidden.txt"), syscall.FILE_ATTRIBUTE_HIDDEN)
	file("ro.txt", 9, winWant{has: FlagReadOnly, hasNo: FlagHidden | FlagExecutable})
	if err := os.Chmod(filepath.Join(dir, "ro.txt"), 0o444); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"app.exe", "RUN.BAT", "x.cmd", "y.com", "z.ps1", "Mixed.ExE"} {
		file(n, 1, winWant{has: FlagExecutable, hasNo: FlagHidden | FlagReadOnly})
	}
	for _, n := range []string{"notes.txt", "exe", "file.exe.txt", "a.exex", ".exe", "bat", "script.sh", "x.ps2"} {
		file(n, 1, winWant{hasNo: FlagExecutable})
	}

	for _, d := range []struct {
		name  string
		attrs uint32
		w     winWant
	}{
		{"sub", 0, winWant{hasNo: FlagHidden | FlagExecutable}},
		{"dir.exe", 0, winWant{hasNo: FlagExecutable}},
		{"hdir", syscall.FILE_ATTRIBUTE_HIDDEN, winWant{has: FlagHidden}},
	} {
		if err := os.Mkdir(filepath.Join(dir, d.name), 0o755); err != nil {
			t.Fatal(err)
		}
		if d.attrs != 0 {
			winSetAttrs(t, filepath.Join(dir, d.name), d.attrs)
		}
		d.w.kind = KindDir
		want[d.name] = d.w
	}

	// Symbolic links (need a privilege or developer mode) and a junction
	// (always creatable).
	if err := os.Symlink("plain.txt", filepath.Join(dir, "ln-file")); err != nil {
		ostCannot(t, "cannot create a file symlink: %v", err)
	} else {
		want["ln-file"] = winWant{kind: KindSymlink, size: -1, hasNo: FlagSymlinkDir}
	}
	if err := os.Symlink("sub", filepath.Join(dir, "ln-dir")); err != nil {
		ostCannot(t, "cannot create a directory symlink: %v", err)
	} else {
		want["ln-dir"] = winWant{kind: KindSymlink, has: FlagSymlinkDir}
	}
	out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(dir, "junction"), filepath.Join(dir, "sub")).CombinedOutput()
	if err != nil {
		ostCannot(t, "cannot create a junction: %v: %s", err, out)
	} else {
		want["junction"] = winWant{kind: KindSymlink, has: FlagSymlinkDir}
	}
	return dir, want
}

func TestWinListFixture(t *testing.T) {
	dir, want := winFixture(t)
	for _, v := range winListers {
		t.Run(v.name, func(t *testing.T) {
			l := new(Listing)
			if err := v.fn(dir, l); err != nil {
				t.Fatal(err)
			}
			got := entries(l)
			if len(l.Entries) != len(want) || len(got) != len(want) {
				var names []string
				for n := range got {
					names = append(names, n)
				}
				sort.Strings(names)
				t.Fatalf("listed %d entries (%d distinct), want %d: %q", len(l.Entries), len(got), len(want), names)
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
				if int(e.NameLen) != len(name) {
					t.Errorf("%q: NameLen %d, want %d", name, e.NameLen, len(name))
				}
				if w.size >= 0 && e.Size != w.size {
					t.Errorf("%q: size %d, want %d", name, e.Size, w.size)
				}
				if e.Flags&w.has != w.has || e.Flags&w.hasNo != 0 {
					t.Errorf("%q: flags %04b, want %04b set and %04b clear", name, e.Flags, w.has, w.hasNo)
				}
				if w.kind == KindFile {
					if d := time.Duration(e.MTime - winMtime.UnixNano()); d < -2*time.Second || d > 2*time.Second {
						t.Errorf("%q: mtime off by %v", name, d)
					}
				} else if e.MTime == 0 || time.Since(time.Unix(0, e.MTime)) > time.Hour {
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

// All variants must agree exactly, entry for entry.
func TestWinListVariantsAgree(t *testing.T) {
	dir, _ := winFixture(t)
	for i := 0; i < 3000; i++ { // more than one 64 KiB buffer of long names
		p := filepath.Join(dir, fmt.Sprintf("bulk-entry-with-a-longish-name-ünï-%05d.dat", i))
		if err := os.WriteFile(p, make([]byte, i%97), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ref := new(Listing)
	if err := winListFind(dir, ref); err != nil {
		t.Fatal(err)
	}
	er := entries(ref)
	for _, v := range winListers[1:] {
		l := new(Listing)
		if err := v.fn(dir, l); err != nil {
			t.Fatalf("%s: %v", v.name, err)
		}
		el := entries(l)
		if len(l.Entries) != len(ref.Entries) || len(el) != len(er) {
			t.Fatalf("%s listed %d entries, Find %d", v.name, len(l.Entries), len(ref.Entries))
		}
		for name, x := range er {
			y, ok := el[name]
			if !ok {
				t.Errorf("%s: %q missing", v.name, name)
				continue
			}
			x.NameOff, y.NameOff = 0, 0
			if x != y {
				t.Errorf("%s: %q: Find %+v, this %+v", v.name, name, x, y)
			}
		}
	}
}

// Paths in every spelling Windows accepts reach the same directory, and
// paths beyond MAX_PATH work without a long-path manifest.
func TestWinListPaths(t *testing.T) {
	base := t.TempDir()
	deep := base
	for len(deep) < 420 {
		deep = filepath.Join(deep, strings.Repeat("d", 40))
	}
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"one.txt", "två.txt", "三.txt"} {
		if err := os.WriteFile(filepath.Join(deep, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if len(deep) <= 260 {
		t.Fatalf("test path too short: %d", len(deep))
	}

	check := func(name, path string) {
		t.Helper()
		for _, v := range winListers {
			l := new(Listing)
			if err := v.fn(path, l); err != nil {
				t.Errorf("%s via %s: %v", name, v.name, err)
				continue
			}
			got := entries(l)
			if len(got) != 3 || got["one.txt"].Size != 1 || got["三.txt"].Size != 1 {
				t.Errorf("%s via %s: listed %v", name, v.name, got)
			}
		}
	}
	check("long plain path", deep)
	check("long, trailing backslash", deep+`\`)
	check("long, forward slashes", filepath.ToSlash(deep))
	check("long, verbatim prefix", `\\?\`+deep)
	check("long, dot-dot", deep+`\..\`+filepath.Base(deep))

	// Relative and drive-relative-looking spellings of a short directory.
	short := t.TempDir()
	for _, n := range []string{"one.txt", "två.txt", "三.txt"} {
		if err := os.WriteFile(filepath.Join(short, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	check("short", short)
	check("short, trailing backslash", short+`\`)
	check("short, forward slashes", filepath.ToSlash(short))
	t.Chdir(filepath.Dir(short))
	check("relative", filepath.Base(short))
	check("relative with dot", `.\`+filepath.Base(short))

	// A UNC spelling through the administrative share, when the OS offers it.
	if vol := filepath.VolumeName(short); len(vol) == 2 && vol[1] == ':' {
		unc := `\\localhost\` + vol[:1] + `$` + short[2:]
		if _, err := os.Stat(unc); err == nil {
			check("UNC admin share", unc)
		} else {
			t.Logf("UNC admin share not reachable here (%v); UNC spelling not exercised", err)
		}
	}
}

func TestWinListRoot(t *testing.T) {
	root := filepath.VolumeName(t.TempDir()) + `\`
	for _, v := range winListers {
		l := new(Listing)
		if err := v.fn(root, l); err != nil {
			t.Errorf("%s: listing %q: %v", v.name, root, err)
			continue
		}
		if len(l.Entries) == 0 {
			t.Errorf("%s: %q lists as empty", v.name, root)
		}
	}
}

func TestWinListErrors(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "file")
	if err := os.WriteFile(f, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, v := range winListers {
		t.Run(v.name, func(t *testing.T) {
			if err := v.fn(filepath.Join(dir, "missing"), new(Listing)); !os.IsNotExist(err) {
				t.Errorf("missing dir: %v", err)
			}
			if err := v.fn(filepath.Join(dir, "missing", "deeper"), new(Listing)); !os.IsNotExist(err) {
				t.Errorf("missing parent: %v", err)
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
}

// A Listing reused after Reset lists the same thing every time.
func TestWinListReuse(t *testing.T) {
	dir, want := winFixture(t)
	for _, v := range winListers {
		l := new(Listing)
		for i := 0; i < 3; i++ {
			l.Reset()
			if err := v.fn(dir, l); err != nil {
				t.Fatal(err)
			}
			if len(l.Entries) != len(want) {
				t.Fatalf("%s round %d: %d entries, want %d", v.name, i, len(l.Entries), len(want))
			}
		}
	}
}

func TestWinListZeroAlloc(t *testing.T) {
	mk := func(n int) string {
		dir := t.TempDir()
		for i := 0; i < n; i++ {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("entry-ü-%05d.txt", i)), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	small, big := mk(10), mk(3000)
	for _, v := range winListers {
		t.Run(v.name, func(t *testing.T) {
			measure := func(dir string) uint64 {
				l := new(Listing)
				for i := 0; i < 3; i++ { // grow the Listing and warm the scratch pool
					l.Reset()
					if err := v.fn(dir, l); err != nil {
						t.Fatal(err)
					}
				}
				const reps = 20
				runtime.GC()
				var a, b runtime.MemStats
				runtime.ReadMemStats(&a)
				for i := 0; i < reps; i++ {
					l.Reset()
					v.fn(dir, l)
				}
				runtime.ReadMemStats(&b)
				return b.Mallocs - a.Mallocs
			}
			ms, mb := measure(small), measure(big)
			t.Logf("20 warm listings: %d mallocs for 10 entries, %d for 3000", ms, mb)
			if mb > ms+5 { // background noise only; a per-entry allocation would add 60,000
				t.Errorf("allocations scale with entries: %d (10 entries) vs %d (3000 entries)", ms, mb)
			}
			if mb > 20 { // at most one per listing
				t.Errorf("%d mallocs over 20 listings", mb)
			}
		})
	}
}

// TestWinListBackendChoice times the three listing methods on a 100k-entry
// directory and says which wins (DECISIONS D12). It does not assert a
// winner: CI runners are noisy; the numbers are the product.
func TestWinListBackendChoice(t *testing.T) {
	dir := bigDirPath(t)
	times := map[string]time.Duration{}
	var names []string
	for _, v := range winListers[:3] {
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
		if len(l.Entries) != bigN {
			t.Fatalf("%s listed %d entries, want %d", v.name, len(l.Entries), bigN)
		}
		times[v.name] = best
		names = append(names, v.name)
		t.Logf("%-34s best of 7: %v for %d entries (%.0f ns/entry)", v.name, best, len(l.Entries), float64(best.Nanoseconds())/float64(len(l.Entries)))
	}
	sort.Slice(names, func(i, j int) bool { return times[names[i]] < times[names[j]] })
	t.Logf("LIST BACKEND CHOICE (windows/%s): %s is fastest: %v; then %s %v (%.2fx), %s %v (%.2fx)",
		runtime.GOARCH, names[0], times[names[0]],
		names[1], times[names[1]], float64(times[names[1]])/float64(times[names[0]]),
		names[2], times[names[2]], float64(times[names[2]])/float64(times[names[0]]))
}

func BenchmarkWinList100k(b *testing.B) {
	dir := bigDirPath(b)
	for _, v := range winListers[:3] {
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

// ---- pure helpers --------------------------------------------------------

func TestWinUTF16RoundTrip(t *testing.T) {
	for _, s := range []string{"", "a", "plain ascii.txt", "é", "日本語", "😀", "a😀b😀😀c", "\U0010FFFF", "\u0800\uFFFD", strings.Repeat("ü😀", 130)} {
		w, ok := winAppendUTF16(nil, s)
		if !ok {
			t.Errorf("%q: rejected", s)
			continue
		}
		if want := utf16.Encode([]rune(s)); !equalU16(w, want) {
			t.Errorf("%q: UTF-16 %x, want %x", s, w, want)
		}
		if back := winAppendUTF8([]byte("pre"), w); string(back) != "pre"+s {
			t.Errorf("%q: round trip %q", s, back)
		}
	}
	if _, ok := winAppendUTF16(nil, "a\x00b"); ok {
		t.Error("NUL accepted")
	}
	// Unpaired surrogates become U+FFFD instead of corrupting the arena.
	for _, w := range [][]uint16{{0xD800}, {0xDC00}, {'a', 0xD800, 'b'}, {0xD800, 0xD800, 0xDC00}} {
		got := winAppendUTF8(nil, w)
		if !utf8.Valid(got) || !strings.ContainsRune(string(got), utf8.RuneError) {
			t.Errorf("%x: %q", w, got)
		}
	}
}

func equalU16(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestWinFiletime(t *testing.T) {
	ft := func(tm time.Time) uint64 { return uint64(tm.UnixNano()/100) + winFiletimeEpochDiff }
	for _, tm := range []time.Time{
		time.Unix(0, 0), winMtime, time.Date(1970, 1, 1, 0, 0, 0, 100, time.UTC),
		time.Date(1969, 12, 31, 23, 59, 59, 0, time.UTC), time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC),
	} {
		if got, want := winUnixNanos(ft(tm)), tm.UnixNano()/100*100; got != want {
			t.Errorf("%v: %d, want %d", tm, got, want)
		}
	}
	if got := winUnixNanos(^uint64(0) >> 1); got != winMaxInt64 {
		t.Errorf("far future: %d", got)
	}
	if got := winUnixNanos(0); got != winMinInt64 { // 1601 is -1.16e19 ns: below int64, so it saturates
		t.Errorf("year 1601: %d", got)
	}
}

func TestWinIsExecName(t *testing.T) {
	for name, want := range map[string]bool{
		"a.exe": true, "A.EXE": true, "a.Bat": true, "a.cmd": true, "a.com": true, "a.ps1": true,
		"exe": false, ".exe": false, "a.exex": false, "a.ex": false, "a.ps2": false, "a.exe.txt": false,
		"": false, "é.exe": true, "日本.cmd": true,
	} {
		w, _ := winAppendUTF16(nil, name)
		if got := winIsExecName(w); got != want {
			t.Errorf("%q: %v, want %v", name, got, want)
		}
	}
}
