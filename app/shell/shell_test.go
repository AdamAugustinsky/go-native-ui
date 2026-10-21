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

// In a test binary the system in use refuses to launch; with GNUI_ALLOW_OPEN=1
// the calls reach the system behind the guard (a recorder here, so nothing
// opens either way).
func TestTestBinaryLaunchesNothing(t *testing.T) {
	if _, ok := sys.(guarded); !ok {
		t.Fatalf("the default system is %T, not guarded", sys)
	}
	dir := t.TempDir()
	r := &recorder{}
	old := sys
	sys = guarded{r}
	t.Cleanup(func() { sys = old })

	t.Setenv("GNUI_ALLOW_OPEN", "")
	if err := OpenURL("https://example.com"); !errors.Is(err, ErrTestBinary) {
		t.Errorf("OpenURL = %v, want ErrTestBinary", err)
	}
	if err := OpenPath(dir); !errors.Is(err, ErrTestBinary) {
		t.Errorf("OpenPath = %v, want ErrTestBinary", err)
	}
	if err := Reveal([]string{dir}); !errors.Is(err, ErrTestBinary) {
		t.Errorf("Reveal = %v, want ErrTestBinary", err)
	}
	if len(r.urls)+len(r.opened)+len(r.revealed) != 0 {
		t.Fatalf("the guard let calls through: %+v", r)
	}

	t.Setenv("GNUI_ALLOW_OPEN", "1")
	if err := OpenURL("https://example.com"); err != nil {
		t.Errorf("OpenURL with GNUI_ALLOW_OPEN=1 = %v", err)
	}
	if err := OpenPath(dir); err != nil {
		t.Errorf("OpenPath with GNUI_ALLOW_OPEN=1 = %v", err)
	}
	if err := Reveal([]string{dir}); err != nil {
		t.Errorf("Reveal with GNUI_ALLOW_OPEN=1 = %v", err)
	}
	if len(r.urls) != 1 || len(r.opened) != 1 || len(r.revealed) != 1 {
		t.Errorf("with GNUI_ALLOW_OPEN=1 the calls did not reach the system: %+v", r)
	}
}

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
	good := []string{"https://example.com/a?b=c#d", "http://localhost:8080", "mailto:a@example.com", "HTTPS://EXAMPLE.COM", "MailTo:a@example.com"}
	for _, u := range good {
		if err := OpenURL(u); err != nil {
			t.Errorf("OpenURL(%q) = %v", u, err)
		}
	}
	if !reflect.DeepEqual(r.urls, good) {
		t.Errorf("the system got %q, want %q", r.urls, good)
	}
}

// Nothing but the allowed schemes launches: drive paths parse as a one-letter
// scheme, and other schemes are other programs.
func TestOpenURLRefusals(t *testing.T) {
	r := fake(t)
	bad := []string{"", "example.com", "/etc/passwd", "file:///Applications/Calculator.app", "FILE:///etc/hosts",
		"C:/Windows/System32/calc.exe", "c:\\windows\\system32\\calc.exe", "C:calc.exe", "Z:/x", "\\\\server\\share\\x.exe",
		"javascript:alert(1)", "JavaScript:alert(1)", "data:text/html,<script>1</script>", "vbscript:msgbox(1)",
		"ms-msdt:/id PCWDiagnostic", "ms-msdt:x", "zapfast://chat/12", "whatsapp://send", "ssh://host", "x-man-page://ls", "tel:+15551234567",
		"https://exa mple.com", "https://example.com/\n", "https://example.com/\x00", "://nope",
		strings.Repeat("a", maxURL) + ":x"}
	for _, u := range bad {
		if err := OpenURL(u); !errors.Is(err, ErrInvalid) {
			t.Errorf("OpenURL(%q) = %v, want ErrInvalid", u, err)
		}
	}
	if len(r.urls) != 0 {
		t.Errorf("the system was asked to open %q", r.urls)
	}
}

func TestOpenURLWithSchemes(t *testing.T) {
	r := fake(t)
	o := URLOptions{Schemes: []string{"zapfast", "WhatsApp"}}
	for _, u := range []string{"zapfast://chat/12", "whatsapp://send?text=hi", "WHATSAPP://send", "https://example.com"} {
		if err := OpenURLWith(u, o); err != nil {
			t.Errorf("OpenURLWith(%q) = %v", u, err)
		}
	}
	if len(r.urls) != 4 {
		t.Errorf("opened %q", r.urls)
	}
	r.urls = nil
	// Allowing a scheme allows that scheme only.
	for _, u := range []string{"tel:+1", "ssh://host", "C:/Windows/System32/calc.exe", "file:///etc/hosts"} {
		if err := OpenURLWith(u, o); !errors.Is(err, ErrInvalid) {
			t.Errorf("OpenURLWith(%q) = %v, want ErrInvalid", u, err)
		}
	}
	// The never-schemes and one-letter ones cannot be allowed, even explicitly.
	for _, s := range []string{"file", "FILE", "javascript", "data", "vbscript", "c", "C", "", "1abc", "has space", "a_b", "ms:"} {
		if err := OpenURLWith("https://example.com", URLOptions{Schemes: []string{s}}); !errors.Is(err, ErrInvalid) {
			t.Errorf("allowing the scheme %q: %v, want ErrInvalid", s, err)
		}
		if err := OpenURLWith(s+"://x", URLOptions{Schemes: []string{s}}); !errors.Is(err, ErrInvalid) {
			t.Errorf("OpenURLWith(%q) with the scheme allowed = %v, want ErrInvalid", s+"://x", err)
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

// The home folder is protected by what it is, not by how it is spelled.
func TestTrashProtectsHomeByIdentity(t *testing.T) {
	r := fake(t)
	base := t.TempDir()
	home := filepath.Join(base, "real", "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.Symlink(filepath.Join(base, "real"), filepath.Join(base, "alias")); err != nil {
		t.Skipf("cannot make symbolic links here: %v", err)
	}
	spelled := filepath.Join(base, "alias", "home") // the same folder, through a linked parent
	if err := Trash(spelled); !errors.Is(err, ErrInvalid) {
		t.Errorf("Trash(%q) = %v, want ErrInvalid", spelled, err)
	}
	if err := Trash(home); !errors.Is(err, ErrInvalid) {
		t.Errorf("Trash(home) = %v, want ErrInvalid", err)
	}
	// A symbolic link that points at the home folder is only a link: it can be trashed.
	link := filepath.Join(base, "link-to-home")
	if err := os.Symlink(home, link); err != nil {
		t.Skip(err)
	}
	if err := Trash(link); err != nil {
		t.Errorf("Trash(link to home) = %v", err)
	}
	// Other folders are fine.
	other := filepath.Join(base, "real", "other")
	os.Mkdir(other, 0o700)
	if err := Trash(other); err != nil {
		t.Errorf("Trash(other) = %v", err)
	}
	if len(r.trashed) != 2 || r.trashed[0] != link || r.trashed[1] != other {
		t.Errorf("the system was asked to trash %q", r.trashed)
	}
}

func TestTrashProtectsTheRootByIdentity(t *testing.T) {
	r := fake(t)
	root := string(filepath.Separator)
	if v := filepath.VolumeName(t.TempDir()); v != "" {
		root = v + root
	}
	if err := Trash(root); !errors.Is(err, ErrInvalid) {
		t.Errorf("Trash(%q) = %v", root, err)
	}
	if len(r.trashed) != 0 {
		t.Error("the root was handed to the system")
	}
}
