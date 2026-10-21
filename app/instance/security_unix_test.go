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
	euidShift.Store(1) // every file here now belongs to someone else
	t.Cleanup(func() { euidShift.Store(0) })
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
	euidShift.Store(1) // every file here now belongs to someone else
	t.Cleanup(func() { euidShift.Store(0) })
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

	// A parent that a group can write is refused whatever the group: on macOS
	// every user is in staff, and any member could replace the directory.
	grp := filepath.Join(t.TempDir(), "grp")
	os.Mkdir(grp, 0o770)
	os.Chmod(grp, 0o770)
	notAcquired(t, filepath.Join(grp, "app"), ErrUntrusted)
	if _, err := os.Stat(filepath.Join(grp, "app", "instance.lock")); err == nil {
		t.Error("a lock file was made under a group-writable parent")
	}
	// With the sticky bit it is fine.
	os.Chmod(grp, 0o770|os.ModeSticky)
	if p, s, err := AcquireWith("x", Options{Dir: filepath.Join(grp, "app")}); err != nil || p == nil || s != nil {
		t.Fatalf("under a sticky group-writable parent: %v %v %v", p, s, err)
	} else {
		p.Close()
	}
}

// ancestorOK, on the stat of every ancestor: no exemption for root's own
// directories, none for a group, none for another user's.
func TestAncestorOK(t *testing.T) {
	me := uint32(euid())
	for _, c := range []struct {
		name string
		uid  uint32
		mode uint32
		ok   bool
	}{
		{"root 0755", 0, syscall.S_IFDIR | 0o755, true},
		{"root 0775", 0, syscall.S_IFDIR | 0o775, false},
		{"root 0777", 0, syscall.S_IFDIR | 0o777, false},
		{"root 0757", 0, syscall.S_IFDIR | 0o757, false},
		{"root 1777 (/tmp)", 0, syscall.S_IFDIR | syscall.S_ISVTX | 0o777, true},
		{"root 1775 sticky group", 0, syscall.S_IFDIR | syscall.S_ISVTX | 0o775, true},
		{"mine 0700", me, syscall.S_IFDIR | 0o700, true},
		{"mine 0770", me, syscall.S_IFDIR | 0o770, false},
		{"mine 0775", me, syscall.S_IFDIR | 0o775, false},
		{"another user's 0755", me + 7, syscall.S_IFDIR | 0o755, false},
		{"another user's 1777", me + 7, syscall.S_IFDIR | syscall.S_ISVTX | 0o777, false},
		{"not a directory", me, syscall.S_IFREG | 0o600, false},
		{"a symbolic link", me, syscall.S_IFLNK | 0o777, false},
	} {
		st := syscall.Stat_t{Uid: c.uid}
		st.Mode = uint16OrUint32(c.mode)
		if err := ancestorOK(&st); (err == nil) != c.ok {
			t.Errorf("%s: %v, want ok=%v", c.name, err, c.ok)
		}
	}
}

// The path given may go through a directory others can write, and may be an alias
// they retarget after Acquire. Everything uses the resolved path, so a secondary
// that was handed the alias still reaches the real primary, and never the
// attacker's socket.
func TestAliasRetargetedAfterAcquire(t *testing.T) {
	base, err := os.MkdirTemp("/tmp", "gn") // short: the socket's path must fit
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	real := filepath.Join(base, "real", "app")
	if err := os.MkdirAll(real, 0o700); err != nil {
		t.Fatal(err)
	}
	evil := filepath.Join(base, "evil") // a directory the attacker can write
	os.Mkdir(evil, 0o777)
	os.Chmod(evil, 0o777)
	alias := filepath.Join(evil, "link")
	if err := os.Symlink(filepath.Join(base, "real"), alias); err != nil {
		t.Fatal(err)
	}
	viaAlias := filepath.Join(alias, "app")

	p, _ := acquire(t, viaAlias, Options{})
	_, s := acquire(t, viaAlias, Options{Timeout: time.Second})

	// The attacker retargets the alias at a directory of their own, with a socket
	// at the very path the old code would have dialled.
	attacker := filepath.Join(base, "attacker")
	os.MkdirAll(filepath.Join(attacker, "app"), 0o700)
	atkSock := filepath.Join(attacker, "app", "instance.sock")
	atk, err := net.Listen("unix", atkSock)
	if err != nil {
		t.Fatal(err)
	}
	defer atk.Close()
	stolen := make(chan struct{}, 1)
	go func() {
		if c, err := atk.Accept(); err == nil {
			stolen <- struct{}{}
			c.Close()
		}
	}()
	os.Remove(alias)
	if err := os.Symlink(attacker, alias); err != nil {
		t.Fatal(err)
	}

	if err := s.Send([]string{"for the real primary"}); err != nil {
		t.Fatalf("Send after the alias was retargeted: %v", err)
	}
	select {
	case <-stolen:
		t.Fatal("the request went to the attacker's socket")
	default:
	}
	if got := argsOf(p.Poll(nil)); len(got) != 1 || got[0][0] != "for the real primary" {
		t.Fatalf("the real primary saw %q", got)
	}
	// And closing the primary does not remove the attacker's socket file.
	p.Close()
	if _, err := os.Lstat(atkSock); err != nil {
		t.Errorf("Close removed a file through the retargeted alias: %v", err)
	}
	// A new Acquire through the alias now resolves to the attacker's directory: it
	// is a different instance (the attacker's directory is private and ours here),
	// not the old one's, and nothing in the old directory changed.
	if _, err := os.Stat(filepath.Join(real, "instance.lock")); err != nil {
		t.Errorf("the real directory's lock file: %v", err)
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
	setPeer(t, func(c conn) error {
		uid, ok, err := peerUID(c)
		supported.Store(ok)
		if err != nil {
			t.Errorf("peerUID: %v", err)
		}
		select {
		case got <- uid:
		default:
		}
		return nil
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

// With the real check (no hook), a peer of another user is refused: here "another
// user" is this process taken for a user one higher.
func TestPeerOfAnotherUserIsRefusedByTheRealCheck(t *testing.T) {
	dir := newDir(t)
	p, _ := acquire(t, dir, Options{})
	_, s := acquire(t, dir, Options{Timeout: 300 * time.Millisecond})
	if _, ok, _ := peerUID(mustDialUnix(t, dir)); !ok {
		t.Skip("this system cannot report the peer of a Unix socket")
	}
	euidShift.Store(1)
	t.Cleanup(func() { euidShift.Store(0) })
	if err := s.Send([]string{"x"}); !errors.Is(err, ErrNoAnswer) {
		t.Errorf("Send = %v, want ErrNoAnswer", err)
	}
	if got := p.Poll(nil); len(got) != 0 {
		t.Errorf("stored %+v", got)
	}
}

func mustDialUnix(t *testing.T, dir string) conn {
	t.Helper()
	lk, addr, err := setup("x", Options{Dir: dir})
	_ = lk
	if err != nil {
		t.Fatal(err)
	}
	c, err := dial(addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func uint16OrUint32(m uint32) (v statMode) { return statMode(m) }
