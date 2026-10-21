//go:build linux

package fs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// openPath starts xdg-open with the absolute path as its single argument.
// A stand-in xdg-open on PATH records what it was given.
func TestLinuxOpenPath(t *testing.T) {
	t.Setenv("GNUI_ALLOW_OPEN", "1") // past the test-binary guard to the stand-in xdg-open: nothing opens
	bin := t.TempDir()
	out := filepath.Join(bin, "args.txt")
	script := "#!/bin/sh\nprintf '%s\\n' \"$#\" \"$1\" > '" + out + ".tmp' && /bin/mv '" + out + ".tmp' '" + out + "'\n"
	if err := os.WriteFile(filepath.Join(bin, "xdg-open"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		ostCannot(t, "no /bin/sh: %v", err)
	}
	t.Setenv("PATH", bin)

	work := t.TempDir()
	t.Chdir(work)
	if err := os.WriteFile("-dash file", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(work, "-dash file"), "-dash file", work} {
		os.Remove(out)
		if err := openPath(path); err != nil {
			t.Fatalf("openPath(%q): %v", path, err)
		}
		var got []byte
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if got, _ = os.ReadFile(out); len(got) > 0 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		lines := strings.Split(strings.TrimSpace(string(got)), "\n")
		if len(lines) != 2 || lines[0] != "1" || !filepath.IsAbs(lines[1]) {
			t.Errorf("openPath(%q): xdg-open saw %q, want one absolute argument", path, got)
		}
	}
}

func TestLinuxOpenPathErrors(t *testing.T) {
	t.Setenv("GNUI_ALLOW_OPEN", "1") // past the guard: a missing path and a missing xdg-open launch nothing
	if err := openPath(filepath.Join(t.TempDir(), "missing")); !os.IsNotExist(err) {
		t.Errorf("missing path: %v", err)
	}
	f := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(f, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir()) // no xdg-open
	if err := openPath(f); err == nil {
		t.Error("openPath without xdg-open succeeded")
	}
}
