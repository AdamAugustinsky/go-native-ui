package inspectwire

import (
	"net"
	"os"
)

// Listener is the bridge's socket: a Unix domain socket that only its owner
// can connect to.
type Listener struct {
	ln   *net.UnixListener
	path string
	id   os.FileInfo // the socket's file as published, to know it again at Close
}

// Path is the socket's path, with symbolic links in its directory resolved.
func (l *Listener) Path() string { return l.path }

// Close stops accepting and removes the socket file, if the path still names
// this socket: a file that something else put there since is left alone.
func (l *Listener) Close() error {
	err := l.ln.Close() // the listener does not unlink: Listen turned that off
	if fi, e := os.Lstat(l.path); e == nil && l.id != nil && os.SameFile(fi, l.id) {
		os.Remove(l.path)
	}
	return err
}
