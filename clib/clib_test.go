package clib

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestOpenRejects(t *testing.T) {
	_, err := Open("libfixture.dylib")
	if err == nil {
		t.Fatal("Open accepted a relative path")
	}
	if !supported {
		if !errors.Is(err, ErrUnsupported) || !errors.Is(err, errors.ErrUnsupported) {
			t.Fatalf("Open on %s/%s = %v, want ErrUnsupported", runtime.GOOS, runtime.GOARCH, err)
		}
		return
	}
	if !strings.Contains(err.Error(), "not an absolute path") {
		t.Fatalf("Open(relative) = %v", err)
	}
	if _, err := Open("/usr/lib/libc.so\x00.missing"); err == nil || !strings.Contains(err.Error(), "NUL") {
		t.Fatalf("Open(path with NUL) = %v, want the NUL error", err)
	}
	missing := filepath.Join(t.TempDir(), "libmissing.so")
	if _, err := Open(missing); err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("Open(missing) = %v, want an error naming the path", err)
	}
}

// TestArgumentLimits checks that a call that cannot fit is refused while it
// is built, before anything reaches C, on every system.
func TestArgumentLimits(t *testing.T) {
	refused := func(name string, build func(c *Call)) {
		t.Helper()
		defer func() {
			if r := recover(); r == nil || !strings.Contains(r.(string), "too many arguments") {
				t.Errorf("%s: recovered %v, want the too-many-arguments panic", name, r)
			}
		}()
		build(NewCall())
	}
	full := func(c *Call, ints, floats int) {
		for range ints {
			c.Int(1)
		}
		for range floats {
			c.Float64(1)
		}
	}
	// The limits themselves are accepted.
	full(NewCall(), intArgRegs+stackArgWords, 0)
	full(NewCall(), 0, floatArgRegs+stackArgWords)
	full(NewCall(), intArgRegs, floatArgRegs+stackArgWords)
	refused("ints", func(c *Call) { full(c, intArgRegs+stackArgWords, 0); c.Uint8(1) })
	refused("floats", func(c *Call) { full(c, 0, floatArgRegs+stackArgWords); c.Float32(1) })
	refused("mixed", func(c *Call) { full(c, intArgRegs+6, floatArgRegs+6); c.Ptr(nil) })

	// Reset after an abandoned build starts from empty registers again.
	c := NewCall()
	full(c, intArgRegs+stackArgWords, 0)
	full(c.Reset(), intArgRegs+stackArgWords, 0)
}

func TestCStr(t *testing.T) {
	buf := make([]byte, 0, 16)
	got := CStr(buf, "hello")
	if string(got) != "hello\x00" || &got[0] != &buf[:1][0] {
		t.Fatalf("CStr = %q (reused buffer: %v)", got, &got[0] == &buf[:1][0])
	}
	if got := CStr(got, ""); string(got) != "\x00" {
		t.Fatalf("CStr(empty) = %q", got)
	}
	if n := testing.AllocsPerRun(100, func() { buf = CStr(buf, "a string of 15!") }); n != 0 {
		t.Fatalf("CStr into a large enough buffer: %v allocations", n)
	}
}

func TestBundleFramework(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skip(err)
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	dir := filepath.Dir(exe)
	want := map[string]string{
		"darwin":  filepath.Join(filepath.Dir(dir), "Frameworks", "libx"),
		"windows": filepath.Join(dir, "libx"),
	}[runtime.GOOS]
	if want == "" {
		want = filepath.Join(filepath.Dir(dir), "lib", "libx")
	}
	if got := BundleFramework("libx"); got != want || !filepath.IsAbs(got) {
		t.Fatalf("BundleFramework = %q, want %q", got, want)
	}
	// A NUL in the name comes back unchanged, for Open to reject, rather
	// than as a bundle path that C would truncate.
	if got := BundleFramework("libx\x00/../../evil"); got != "libx\x00/../../evil" {
		t.Fatalf("BundleFramework(name with NUL) = %q", got)
	}
}
