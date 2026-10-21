package inspectwire

import (
	"net"
	"os"
	"sync"
)

// Listener is the bridge's socket: a Unix domain socket that only its owner
// can connect to.
type Listener struct {
	ln   *net.UnixListener
	path string
	id   os.FileInfo // the socket's file as published, to know it again at Close
	lock *os.File    // the flock that says this bridge is alive, held until Close

	closeOnce sync.Once
	closeErr  error
}

// Path is the socket's path, with symbolic links in its directory resolved.
func (l *Listener) Path() string { return l.path }

// Close stops accepting and removes the socket file, if the path still names
// this socket: a file that something else put there since is left alone.
//
// The path is removed while the lock is still held, and only then is the lock
// released (its file stays), so a bridge waiting to take over never has its new
// socket removed by this one. Close may be called more than once.
func (l *Listener) Close() error {
	// Once: after the lock is let go a successor may have published its own
	// socket, which an inode number reused from this one's could pass for it.
	l.closeOnce.Do(func() {
		l.closeErr = l.ln.Close() // the listener does not unlink: Listen turned that off
		if fi, e := os.Lstat(l.path); e == nil && l.id != nil && os.SameFile(fi, l.id) {
			os.Remove(l.path)
		}
		if l.lock != nil {
			l.lock.Close()
		}
	})
	return l.closeErr
}
