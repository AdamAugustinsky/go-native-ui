//go:build unix

package instance

import (
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// rawDial connects without any check on the other end.
func rawDial(addr string) (conn, error) { return net.DialTimeout("unix", addr, time.Second) }

// Only the user can reach the socket, the lock and their directory.
func TestPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "app")
	os.MkdirAll(dir, 0o755) // an existing, too open directory is tightened
	acquire(t, dir, Options{})
	sock := endpoint(t, dir)
	for path, want := range map[string]os.FileMode{dir: 0o700, filepath.Join(dir, "instance.lock"): 0o600, sock: 0o600} {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != want {
			t.Errorf("%s: mode %o, want %o", path, got, want)
		}
	}
}

// A primary that is killed leaves its socket file but not its lock.
func TestDeadPrimaryLeavesAStaleSocket(t *testing.T) {
	dir := newDir(t)
	c := startChild(t, "hold", dir)
	if line := c.line(t); line != "primary" {
		t.Fatalf("helper said %q", line)
	}
	sock := endpoint(t, dir)
	if _, err := os.Lstat(sock); err != nil {
		t.Fatalf("no socket while the primary runs: %v", err)
	}
	c.cmd.Process.Kill()
	c.cmd.Wait()
	if _, err := os.Lstat(sock); err != nil {
		t.Skipf("the killed primary left no socket file (%v): nothing stale to test", err)
	}
	if _, err := net.DialTimeout("unix", sock, time.Second); err == nil {
		t.Fatal("the stale socket accepts connections")
	}

	p, s := acquire(t, dir, Options{})
	if p == nil || s != nil {
		t.Fatal("the lock of a dead primary should be free: the next copy becomes the primary")
	}
	_, s2 := acquire(t, dir, Options{})
	if s2 == nil {
		t.Fatal("expected a secondary")
	}
	if err := s2.Send([]string{"after", "crash"}); err != nil {
		t.Fatal(err)
	}
	if got := p.Poll(nil); len(got) != 1 || !slices.Equal(got[0].Args, []string{"after", "crash"}) {
		t.Fatalf("requests %+v", got)
	}
}

// An application directory whose socket path would be too long for a
// Unix-domain socket still works: the socket goes to a short private directory.
func TestLongDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), strings.Repeat("d", 60), strings.Repeat("e", 60))
	p, _ := acquire(t, dir, Options{})
	sock := endpoint(t, dir)
	if len(sock) > maxSocketPath {
		t.Fatalf("socket path of %d bytes", len(sock))
	}
	if filepath.Dir(sock) == dir {
		t.Errorf("the socket is in the long directory: %s", sock)
	}
	t.Cleanup(func() { os.RemoveAll(filepath.Dir(sock)) })
	_, s := acquire(t, dir, Options{})
	if err := s.Send([]string{"long"}); err != nil {
		t.Fatal(err)
	}
	if got := p.Poll(nil); len(got) != 1 {
		t.Errorf("requests %+v", got)
	}
}
