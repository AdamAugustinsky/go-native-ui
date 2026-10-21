//go:build !windows

package inspectwire_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gonativeui/internal/inspectwire"
)

func TestCheckDir(t *testing.T) {
	d := shortDir(t)
	n := 0
	mk := func(name string, mode os.FileMode) string {
		n++
		p := filepath.Join(d, name+strconv.Itoa(n))
		if err := os.Mkdir(p, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for _, mode := range []os.FileMode{0o700, 0o755, 0o750, 0o500} {
		if _, err := inspectwire.CheckDir(mk(mode.String(), mode)); err != nil {
			t.Errorf("mode %v refused: %v", mode, err)
		}
	}
	for _, mode := range []os.FileMode{0o770, 0o707, 0o777, 0o720, 0o702, os.ModeSticky | 0o777} {
		_, err := inspectwire.CheckDir(mk("bad"+mode.String(), mode))
		if err == nil || !strings.Contains(err.Error(), "writable") {
			t.Errorf("mode %v: %v", mode, err)
		}
	}
	f := filepath.Join(d, "file")
	os.WriteFile(f, nil, 0o600)
	if _, err := inspectwire.CheckDir(f); err == nil {
		t.Error("a file was accepted as a directory")
	}
	if _, err := inspectwire.CheckDir(filepath.Join(d, "missing")); err == nil {
		t.Error("a missing directory was accepted")
	}
	if os.Geteuid() != 0 {
		// Somebody else's directory, whatever its mode: the system's own.
		for _, other := range []string{"/", "/usr", "/etc"} {
			if _, err := inspectwire.CheckDir(other); err == nil || !strings.Contains(err.Error(), "not owned") {
				t.Errorf("%s: %v", other, err)
			}
		}
	}
}

// An owned 0700 directory under a directory others can write to can be renamed
// away and replaced: every ancestor must be the user's or root's, and closed to
// the group and others unless it has the sticky bit.
func TestCheckDirRefusesAWritableAncestor(t *testing.T) {
	d := shortDir(t)
	mkchain := func(name string, parent os.FileMode) string {
		p := filepath.Join(d, name)
		if err := os.Mkdir(p, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, parent); err != nil {
			t.Fatal(err)
		}
		c := filepath.Join(p, "c")
		if err := os.Mkdir(c, 0o700); err != nil {
			t.Fatal(err)
		}
		return c
	}
	for _, mode := range []os.FileMode{0o777, 0o770, 0o707, 0o722, 0o702} {
		c := mkchain("open"+mode.String(), mode)
		if _, err := inspectwire.CheckDir(c); err == nil || !strings.Contains(err.Error(), "above the socket directory") {
			t.Errorf("a parent of mode %v: %v", mode, err)
		}
		sock := filepath.Join(c, "s")
		if l, err := inspectwire.Listen(sock); err == nil {
			l.Close()
			t.Errorf("Listen made a socket under a parent of mode %v", mode)
		}
	}
	// the sticky bit is what makes /tmp usable: others cannot rename what is not theirs
	if _, err := inspectwire.CheckDir(mkchain("sticky", os.ModeSticky|0o777)); err != nil {
		t.Errorf("a sticky parent: %v", err)
	}
	if _, err := inspectwire.CheckDir(mkchain("closed", 0o755)); err != nil {
		t.Errorf("a closed parent: %v", err)
	}
	// a link in the way is resolved once, and the resolved chain is checked
	open := mkchain("viaopen", 0o777)
	link := filepath.Join(d, "link")
	if err := os.Symlink(filepath.Dir(open), link); err == nil {
		if _, err := inspectwire.CheckDir(filepath.Join(link, "c")); err == nil {
			t.Error("a link to a directory under a writable parent was accepted")
		}
	}
}

// The directory a shot goes to is held to the rule of the socket's: one under a
// directory others can write and rename in, with no sticky bit, is refused.
func TestOpenShotDestRefusesAnUnsafeDirectory(t *testing.T) {
	d := shortDir(t)
	sockDir := filepath.Join(d, "run")
	os.Mkdir(sockDir, 0o700)
	p := filepath.Join(d, "open")
	os.Mkdir(p, 0o700)
	os.Chmod(p, 0o777)
	out := filepath.Join(p, "out")
	os.Mkdir(out, 0o700)
	if dest, err := inspectwire.OpenShotDest(filepath.Join(sockDir, "s"), filepath.Join(out, "a.png")); err == nil {
		dest.Close()
		t.Error("an output directory under a world-writable non-sticky parent was accepted")
	} else if !strings.Contains(err.Error(), "not fit for a shot") {
		t.Errorf("the refusal: %v", err)
	}
	loose := filepath.Join(d, "loose")
	os.Mkdir(loose, 0o700)
	os.Chmod(loose, 0o777)
	if dest, err := inspectwire.OpenShotDest(filepath.Join(sockDir, "s"), filepath.Join(loose, "a.png")); err == nil {
		dest.Close()
		t.Error("a world-writable output directory was accepted")
	}
}
