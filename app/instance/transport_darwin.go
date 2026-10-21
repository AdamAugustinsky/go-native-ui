//go:build darwin

package instance

import (
	"fmt"
	"os"
	"syscall"
	"time"
)

// The Unix transport on macOS, on raw system calls: package net would add some
// 400 KB to every binary that links this package. The sockets are non-blocking
// and owned by an *os.File, so the runtime's poller gives them deadlines and a
// Close that wakes whoever waits on them, as it does for net. The protocol,
// the socket's mode and its location are those of transport_unix.go.

// newSocket makes a non-blocking, close-on-exec stream socket in the Unix
// domain that does not raise SIGPIPE.
func newSocket() (int, error) {
	syscall.ForkLock.RLock()
	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err == nil {
		syscall.CloseOnExec(fd)
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return -1, os.NewSyscallError("socket", err)
	}
	if err := syscall.SetNonblock(fd, true); err != nil {
		syscall.Close(fd)
		return -1, os.NewSyscallError("setnonblock", err)
	}
	syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_NOSIGPIPE, 1)
	return fd, nil
}

type unixListener struct {
	f  *os.File
	rc syscall.RawConn
}

func (l unixListener) Accept() (conn, error) {
	nfd := -1
	var aerr error
	err := l.rc.Read(func(fd uintptr) bool {
		for {
			syscall.ForkLock.RLock()
			nfd, _, aerr = syscall.Accept(int(fd))
			if aerr == nil {
				syscall.CloseOnExec(nfd)
			}
			syscall.ForkLock.RUnlock()
			switch aerr {
			case syscall.EINTR, syscall.ECONNABORTED:
				continue
			case syscall.EAGAIN:
				return false // wait until the listener is readable
			}
			return true
		}
	})
	if err != nil {
		return nil, err
	}
	if aerr != nil {
		return nil, os.NewSyscallError("accept", aerr)
	}
	if err := syscall.SetNonblock(nfd, true); err != nil {
		syscall.Close(nfd)
		return nil, os.NewSyscallError("setnonblock", err)
	}
	syscall.SetsockoptInt(nfd, syscall.SOL_SOCKET, syscall.SO_NOSIGPIPE, 1)
	return os.NewFile(uintptr(nfd), "instance"), nil
}

func (l unixListener) Close() error { return l.f.Close() }

// listen removes a stale socket (the caller holds the lock, so it is the leftover
// of a dead copy), listens, and gives the socket mode 0600 before anything can
// connect. The directory is private, so no one else could reach the socket while
// it had the mode the umask gave it.
func listen(addr string) (listener, error) {
	os.Remove(addr)
	fd, err := newSocket()
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), addr)
	fail := func(err error) (listener, error) {
		f.Close()
		os.Remove(addr)
		return nil, err
	}
	if err := syscall.Bind(fd, &syscall.SockaddrUnix{Name: addr}); err != nil {
		f.Close()
		return nil, os.NewSyscallError("bind", err)
	}
	if err := os.Chmod(addr, 0o600); err != nil {
		return fail(fmt.Errorf("setting the mode of %s: %w", addr, err))
	}
	if err := syscall.Listen(fd, syscall.SOMAXCONN); err != nil {
		return fail(os.NewSyscallError("listen", err))
	}
	rc, err := f.SyscallConn()
	if err != nil {
		return fail(err)
	}
	return unixListener{f, rc}, nil
}

// dial connects to the socket at addr, waiting at most timeout for the
// connection to complete.
func dial(addr string, timeout time.Duration) (conn, error) {
	fd, err := newSocket()
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), addr)
	fail := func(op string, err error) (conn, error) {
		f.Close()
		if _, isErrno := err.(syscall.Errno); isErrno {
			err = os.NewSyscallError(op, err)
		}
		return nil, err
	}
	rc, err := f.SyscallConn()
	if err != nil {
		return fail("dial", err)
	}
	f.SetWriteDeadline(time.Now().Add(timeout)) // bounds the wait below, where the runtime can poll
	err = syscall.Connect(fd, &syscall.SockaddrUnix{Name: addr})
	switch err {
	case nil, syscall.EISCONN:
	case syscall.EINPROGRESS, syscall.EALREADY, syscall.EINTR:
		var soErr error
		if werr := rc.Write(func(fd uintptr) bool {
			e, gerr := syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_ERROR)
			if gerr != nil {
				soErr = gerr
				return true
			}
			switch en := syscall.Errno(e); en {
			case syscall.EINPROGRESS, syscall.EALREADY, syscall.EINTR:
				return false
			case 0:
				// The poller can wake up early: only a peer name says it is connected.
				_, gerr := syscall.Getpeername(int(fd))
				return gerr == nil
			case syscall.EISCONN:
			default:
				soErr = en
			}
			return true
		}); werr != nil {
			return fail("connect", werr)
		}
		if soErr != nil {
			return fail("connect", soErr)
		}
	default:
		return fail("connect", err)
	}
	f.SetWriteDeadline(time.Time{})
	return f, nil
}
