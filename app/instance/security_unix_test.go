//go:build unix

package instance

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func mustWrite(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func unchanged(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil || string(b) != content {
		t.Errorf("%s was changed: %q %v (want %q)", path, b, err, content)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != mode {
		t.Errorf("%s: mode %v %v, want %v", path, fi, err, mode)
	}
}

func notAcquired(t *testing.T, dir string, wantErr error) {
	t.Helper()
	p, s, err := AcquireWith("dev.gonativeui.test", Options{Dir: dir})
	if p != nil {
		p.Close()
	}
	if p != nil || s != nil || !errors.Is(err, wantErr) {
		t.Fatalf("Acquire = %v, %v, %v; want %v", p, s, err, wantErr)
	}
}

// A symbolic link planted as the lock file, pointing at a file the user owns,
// must not get that file locked, re-moded or truncated.
func TestLockFileSymlinkDoesNotTouchItsTarget(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(t.TempDir(), "precious.db")
	mustWrite(t, victim, "irreplaceable data", 0o644)
	if err := os.Symlink(victim, filepath.Join(dir, "instance.lock")); err != nil {
		t.Fatal(err)
	}
	notAcquired(t, dir, ErrUntrusted)
	unchanged(t, victim, "irreplaceable data", 0o644)
}

// A second name for another file (a hard link) is no lock file either.
func TestLockFileHardLinkDoesNotTouchItsTarget(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(t.TempDir(), "precious.db")
	mustWrite(t, victim, "irreplaceable data", 0o644)
	if err := os.Link(victim, filepath.Join(dir, "instance.lock")); err != nil {
		t.Skipf("no hard links here: %v", err)
	}
	notAcquired(t, dir, ErrUntrusted)
	unchanged(t, victim, "irreplaceable data", 0o644)
}

func TestLockFileThatIsNotARegularFile(t *testing.T) {
	t.Run("fifo", func(t *testing.T) {
		dir := t.TempDir()
		if err := syscall.Mkfifo(filepath.Join(dir, "instance.lock"), 0o600); err != nil {
			t.Skip(err)
		}
		done := make(chan struct{})
		go func() { defer close(done); notAcquired(t, dir, ErrUntrusted) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Acquire blocks on a FIFO planted as the lock file")
		}
	})
	t.Run("directory", func(t *testing.T) {
		dir := t.TempDir()
		os.Mkdir(filepath.Join(dir, "instance.lock"), 0o700)
		p, s, err := AcquireWith("x", Options{Dir: dir})
		if p != nil || s != nil || err == nil {
			t.Fatalf("Acquire = %v, %v, %v", p, s, err)
		}
	})
}

func TestLockFileOfAnotherUser(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "instance.lock"), "", 0o600)
	// From the point of view of a user one higher, every file here belongs to someone else.
	real := euid
	euid = func() int { return real() + 1 }
	t.Cleanup(func() { euid = real })
	if _, err := acquireOnly(dir); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("err = %v, want ErrUntrusted", err)
	}
}

func acquireOnly(dir string) (lock, error) {
	lk, _, err := acquireLock(filepath.Join(dir, "instance.lock"))
	return lk, err
}

// ---- the directory -----------------------------------------------------------------

func TestDirectoryThatIsASymlinkIsRefused(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "app")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	notAcquired(t, link, ErrUntrusted)
	if entries, _ := os.ReadDir(real); len(entries) != 0 {
		t.Errorf("files were made through the link: %v", entries)
	}
}

func TestDirectoryOfAnotherUserIsRefused(t *testing.T) {
	dir := t.TempDir()
	real := euid
	euid = func() int { return real() + 1 }
	t.Cleanup(func() { euid = real })
	notAcquired(t, dir, ErrUntrusted)
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("files were made in a directory of another user: %v", entries)
	}
}

func TestParentWritableByOthersIsRefused(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(parent, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o777); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(parent, "app")
	notAcquired(t, dir, ErrUntrusted)

	// The sticky bit (as /tmp has it) means others cannot rename or remove what
	// is not theirs: allowed.
	if err := os.Chmod(parent, 0o777|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	if p, s, err := AcquireWith("x", Options{Dir: dir}); err != nil || p == nil || s != nil {
		t.Fatalf("under a sticky parent: %v %v %v", p, s, err)
	} else {
		p.Close()
	}

	// A group-writable parent of a foreign group is refused; of the user's own group, allowed.
	other := filepath.Join(t.TempDir(), "grp")
	os.Mkdir(other, 0o770)
	os.Chmod(other, 0o770)
	os.Mkdir(filepath.Join(other, "app"), 0o700)
	if err := checkPrivate(filepath.Join(other, "app")); err != nil {
		t.Errorf("a parent writable by the user's own group: %v", err)
	}
}

func TestLooseDirectoryIsTightened(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "app")
	os.Mkdir(dir, 0o777)
	os.Chmod(dir, 0o777)
	p, _ := acquire(t, dir, Options{})
	if p == nil {
		t.Fatal("not primary")
	}
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("mode %v %v", fi, err)
	}
}

// ---- the socket's peer -------------------------------------------------------------

func TestPeerUIDIsTheCaller(t *testing.T) {
	dir := newDir(t)
	acquire(t, dir, Options{})
	got := make(chan uint32, 1)
	var supported atomic.Bool
	setPeer(t, func(c net.Conn) (uint32, bool, error) {
		uid, ok, err := peerUID(c)
		supported.Store(ok)
		if err != nil {
			t.Errorf("peerUID: %v", err)
		}
		select {
		case got <- uid:
		default:
		}
		return uid, ok, err
	})
	_, s := acquire(t, dir, Options{})
	if err := s.Send([]string{"x"}); err != nil {
		t.Fatal(err)
	}
	select {
	case uid := <-got:
		if !supported.Load() {
			t.Skip("this system cannot report the peer of a Unix socket")
		}
		if int(uid) != os.Geteuid() {
			t.Errorf("peer uid %d, want %d", uid, os.Geteuid())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the peer was not checked")
	}
}
