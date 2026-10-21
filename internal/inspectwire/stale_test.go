//go:build !windows

package inspectwire

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// Only a refused connection proves a socket stale: a timeout, a permission
// error, any other failure, or a file that changed meanwhile leaves it alone.
func TestClearStaleNeedsProofAndTheSameFile(t *testing.T) {
	old := dialStale
	defer func() { dialStale = old }()
	d := tmpDir(t)
	sock := filepath.Join(d, "s")
	mk := func() {
		os.Remove(sock)
		l, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
		if err != nil {
			t.Fatal(err)
		}
		l.SetUnlinkOnClose(false)
		l.Close()
	}
	exists := func() bool { _, err := os.Lstat(sock); return err == nil }
	for name, err := range map[string]error{
		"timeout":    &net.OpError{Op: "dial", Err: os.ErrDeadlineExceeded},
		"permission": &net.OpError{Op: "dial", Err: syscall.EACCES},
		"other":      &net.OpError{Op: "dial", Err: syscall.ECONNRESET},
		"plain":      errors.New("something"),
	} {
		mk()
		dialStale = func(string) (net.Conn, error) { return nil, err }
		if clearStale(sock) == nil || !exists() {
			t.Errorf("%s: the socket was taken for stale", name)
		}
	}
	mk()
	dialStale = func(string) (net.Conn, error) { return nil, &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED} }
	if err := clearStale(sock); err != nil || exists() {
		t.Errorf("a refused connection: %v, exists %v", err, exists())
	}
	// refused, but the file was replaced while the question was asked
	mk()
	dialStale = func(p string) (net.Conn, error) {
		os.Remove(p)
		os.WriteFile(p, []byte("precious"), 0o600)
		return nil, &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}
	}
	if clearStale(sock) == nil {
		t.Error("a file that replaced the socket meanwhile was removed")
	}
	if b, _ := os.ReadFile(sock); string(b) != "precious" {
		t.Errorf("the replacement is gone: %q", b)
	}
}
