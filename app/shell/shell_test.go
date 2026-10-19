package shell

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// recorder is a system that launches nothing.
type recorder struct {
	urls, opened, trashed []string
	revealed              [][]string
}

func (r *recorder) openURL(u string) error  { r.urls = append(r.urls, u); return nil }
func (r *recorder) openPath(p string) error { r.opened = append(r.opened, p); return nil }
func (r *recorder) reveal(p []string) error { r.revealed = append(r.revealed, p); return nil }
func (r *recorder) trash(p string) error    { r.trashed = append(r.trashed, p); return nil }

func fake(t *testing.T) *recorder {
	t.Helper()
	r := &recorder{}
	old := sys
	sys = r
	t.Cleanup(func() { sys = old })
	return r
}

func TestOpenURL(t *testing.T) {
	r := fake(t)
	good := []string{"https://example.com/a?b=c#d", "http://localhost:8080", "mailto:a@example.com", "zapfast://chat/12", "HTTPS://EXAMPLE.COM"}
	for _, u := range good {
		if err := OpenURL(u); err != nil {
			t.Errorf("OpenURL(%q) = %v", u, err)
		}
	}
	if !reflect.DeepEqual(r.urls, good) {
		t.Errorf("the system got %q, want %q", r.urls, good)
	}
	r.urls = nil
	bad := []string{"", "example.com", "/etc/passwd", "file:///Applications/Calculator.app", "FILE:///etc/hosts",
		"https://exa mple.com", "https://example.com/\n", "https://example.com/\x00", "://nope", strings.Repeat("a", maxURL) + ":x"}
	for _, u := range bad {
		if err := OpenURL(u); !errors.Is(err, ErrInvalid) {
			t.Errorf("OpenURL(%q) = %v, want ErrInvalid", u, err)
		}
	}
	if len(r.urls) != 0 {
		t.Errorf("the system was asked to open %q", r.urls)
	}
}

func TestOpenPathChecksAndAbsolutises(t *testing.T) {
	r := fake(t)
	dir := t.TempDir()
	f := filepath.Join(dir, "doc.txt")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := OpenPath(f); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	if err := OpenPath("doc.txt"); err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.Abs("doc.txt")
	if len(r.opened) != 2 || r.opened[1] != want || !filepath.IsAbs(r.opened[0]) {
		t.Errorf("the system got %q", r.opened)
	}
	if err := OpenPath(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a missing path: %v", err)
	}
	for _, bad := range []string{"", "a\x00b"} {
		if err := OpenPath(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("OpenPath(%q) = %v", bad, err)
		}
	}
	if len(r.opened) != 2 {
		t.Errorf("the system was asked to open a path that does not exist: %q", r.opened)
	}
}

func TestReveal(t *testing.T) {
	r := fake(t)
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	for _, f := range []string{a, b} {
		if err := os.WriteFile(f, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := Reveal(nil); err != nil || len(r.revealed) != 0 {
		t.Errorf("no paths: %v, %v", err, r.revealed)
	}
	if err := Reveal([]string{a, b}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.revealed, [][]string{{a, b}}) {
		t.Errorf("revealed %q", r.revealed)
	}
	// One missing path stops the whole request before anything is shown.
	if err := Reveal([]string{a, filepath.Join(dir, "nope")}); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a missing path: %v", err)
	}
	if len(r.revealed) != 1 {
		t.Errorf("something was revealed despite the error: %q", r.revealed)
	}
}

func TestTrashRefusals(t *testing.T) {
	r := fake(t)
	root := string(filepath.Separator)
	if v := os.Getenv("SystemDrive"); v != "" {
		root = v + `\`
	}
	home, _ := os.UserHomeDir()
	for _, p := range []string{root, home, home + string(filepath.Separator)} {
		if p == "" {
			continue
		}
		if err := Trash(p); !errors.Is(err, ErrInvalid) {
			t.Errorf("Trash(%q) = %v, want ErrInvalid", p, err)
		}
	}
	if err := Trash(filepath.Join(t.TempDir(), "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a missing file: %v", err)
	}
	if len(r.trashed) != 0 {
		t.Errorf("the system was asked to trash %q", r.trashed)
	}
	f := filepath.Join(t.TempDir(), "x")
	os.WriteFile(f, nil, 0o600)
	if err := Trash(f); err != nil || len(r.trashed) != 1 || r.trashed[0] != f {
		t.Errorf("Trash(%q) = %v, system got %q", f, err, r.trashed)
	}
}
