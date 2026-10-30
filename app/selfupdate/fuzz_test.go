package selfupdate

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"path"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func fuzzArchive(t testing.TB, entries []entry) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name}
		h.SetMode(e.mode)
		h.UncompressedSize64, h.CompressedSize64 = e.size, uint64(len(e.body))
		var out io.Writer
		var err error
		if e.size != 0 {
			out, err = w.CreateRaw(h)
		} else {
			out, err = w.CreateHeader(h)
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(out, e.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func fuzzUpdateBound(t *testing.T, n int) func() {
	t.Helper()
	start := time.Now()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	return func() {
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		if time.Since(start) > 5*time.Second || after.TotalAlloc-before.TotalAlloc > uint64(16<<20+256*n) {
			t.Fatal("update input resource bound exceeded")
		}
	}
}

// Raw central directories and structured entries exercise both malformed ZIP
// framing and valid framing around attacker-controlled names/lengths/links.
// Input <=128 KiB, names/links <=2 KiB; 5 s and 16 MiB + 256*input bytes.
// No archive is extracted: every accepted destination must stay below stage.
func FuzzUpdateArchive(f *testing.F) {
	fixture := fuzzArchive(f, bundle())
	for _, name := range []string{"SUTest.app/ok", "../evil", "/tmp/evil", "SUTest.app/a\x00b", `SUTest.app\..\evil`, "SUTest.app/../evil", "SUTest.app/" + strings.Repeat("x", 256), "SUTest.app/\xed\xa0\x80"} {
		f.Add(fixture, name, "Resources", uint64(1), false)
	}
	f.Add([]byte("PK\x05\x06"), "SUTest.app/link", "../../..", uint64(1<<63), true)
	f.Add(fixture, "SUTest.app/Contents/link", "../Resources", uint64(maxTotalSize+1), true)
	f.Fuzz(func(t *testing.T, raw []byte, name, target string, size uint64, symlink bool) {
		if len(raw) > 128<<10 || len(name) > 2048 || len(target) > 2048 {
			t.Skip()
		}
		defer fuzzUpdateBound(t, len(raw)+len(name)+len(target))()
		check := func(data []byte) (bool, string) {
			top, err := checkArchive(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				if !errors.Is(err, ErrRejected) {
					t.Fatalf("unclassified archive failure: %v", err)
				}
				return false, ""
			}
			if top == ".app" || !strings.HasSuffix(top, ".app") || strings.Contains(top, "/") {
				t.Fatalf("invalid top %q", top)
			}
			zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				t.Fatal(err)
			}
			var total uint64
			for _, e := range zr.File {
				if !fuzzSafeArchiveName(e.Name) {
					t.Fatalf("unsafe accepted path %q", e.Name)
				}
				dest := path.Join("/stage", e.Name)
				if !strings.HasPrefix(dest, "/stage/"+top) {
					t.Fatalf("path escaped stage: %q", e.Name)
				}
				if e.UncompressedSize64 > maxTotalSize-total {
					t.Fatal("unpacked size exceeds bound")
				}
				total += e.UncompressedSize64
			}
			return true, top
		}
		check(raw)
		e := entry{name: name, mode: 0o644, body: "x", size: size}
		if symlink {
			e = link(name, target)
		}
		accepted, _ := check(fuzzArchive(t, append(bundle(), e)))
		if accepted && (!fuzzSafeArchiveName(name) || (!symlink && size > maxTotalSize)) {
			t.Fatal("unsafe structured entry accepted")
		}
		if symlink && accepted {
			resolved := path.Clean(path.Dir(name) + "/" + target)
			if target == "" || strings.HasPrefix(target, "/") || strings.ContainsRune(target, 0) || !utf8.ValidString(target) || (resolved != "SUTest.app" && !strings.HasPrefix(resolved, "SUTest.app/")) {
				t.Fatal("escaping link accepted")
			}
		}
	})
}

func fuzzSafeArchiveName(n string) bool {
	if len(n) == 0 || len(n) > 1024 || strings.HasPrefix(n, "/") || strings.ContainsAny(n, "\x00\\") || !utf8.ValidString(n) {
		return false
	}
	for _, c := range strings.Split(strings.TrimSuffix(n, "/"), "/") {
		if c == "" || c == "." || c == ".." || len(c) > 255 {
			return false
		}
		for _, r := range c {
			if r < 32 || r == 127 {
				return false
			}
		}
	}
	return true
}

// Versions use decimal components of at most 18 digits. The independent
// reference compares trimmed decimal strings, without integer conversion.
// Four strings <=4 KiB each; 5 s and 16 MiB + 256*input bytes.
func FuzzUpdateVersions(f *testing.F) {
	for _, v := range []string{"", "1", "1.2.0", "01.002", ".1", "1..2", "18446744073709551615", "1\x00", "-1", "1.é"} {
		f.Add(v, "2", "1.2", "1")
	}
	f.Fuzz(func(t *testing.T, a, ab, b, bb string) {
		if len(a)+len(ab)+len(b)+len(bb) > 16<<10 {
			t.Skip()
		}
		defer fuzzUpdateBound(t, len(a)+len(ab)+len(b)+len(bb))()
		valid := func(s string) bool {
			for _, p := range strings.Split(s, ".") {
				if len(p) < 1 || len(p) > 18 {
					return false
				}
				for _, c := range []byte(p) {
					if c < '0' || c > '9' {
						return false
					}
				}
			}
			return true
		}
		for _, v := range []string{a, ab, b, bb} {
			parts, err := parseVersion(v)
			if (err == nil) != valid(v) {
				t.Fatalf("version grammar: %q", v)
			}
			if err == nil {
				for i, p := range strings.Split(v, ".") {
					if strconv.FormatUint(parts[i], 10) != strings.TrimLeft(p, "0") && !(parts[i] == 0 && strings.Trim(p, "0") == "") {
						t.Fatal("parsed component differs")
					}
				}
			}
		}
		cmp := func(x, y string) int {
			xs, ys := strings.Split(x, "."), strings.Split(y, ".")
			for i := 0; i < max(len(xs), len(ys)); i++ {
				var xx, yy string
				if i < len(xs) {
					xx = strings.TrimLeft(xs[i], "0")
				}
				if i < len(ys) {
					yy = strings.TrimLeft(ys[i], "0")
				}
				if len(xx) < len(yy) {
					return -1
				}
				if len(xx) > len(yy) {
					return 1
				}
				if c := strings.Compare(xx, yy); c != 0 {
					return c
				}
			}
			return 0
		}
		want := valid(a) && valid(b) && (cmp(a, b) > 0 || cmp(a, b) == 0 && valid(ab) && valid(bb) && cmp(ab, bb) > 0)
		err := checkNewer(a, ab, b, bb)
		if (err == nil) != want || err != nil && !errors.Is(err, ErrRejected) {
			t.Fatalf("newer disagreement: %v", err)
		}
	})
}
