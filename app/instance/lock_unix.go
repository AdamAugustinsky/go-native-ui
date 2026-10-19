//go:build unix

package instance

import (
	"fmt"
	"os"
	"syscall"
)

// lock is a held instance lock.
type lock interface{ release() }

type flockLock struct{ f *os.File }

// acquireLock takes an exclusive flock on the file at path. got is false when
// another process (or another open of the file in this one) holds it. The OS
// drops the lock when the process ends, however it ends.
//
// The file is opened without following a symbolic link, and nothing is changed
// until what was opened has been checked on its descriptor: a regular file with
// one name, owned by this user. A link planted at the path (to a file the user
// owns elsewhere) is refused, not truncated or re-moded. The descriptor is not
// inherited by child processes (close-on-exec), so a child that outlives the
// primary cannot keep the lock.
func acquireLock(path string) (lk lock, got bool, err error) {
	var fd int
	for {
		// O_NONBLOCK so that a FIFO planted there does not block the open.
		fd, err = syscall.Open(path, syscall.O_RDWR|syscall.O_CREAT|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0o600)
		if err != syscall.EINTR {
			break
		}
	}
	if err == syscall.ELOOP { // O_NOFOLLOW met a symbolic link
		return nil, false, fmt.Errorf("%w: the lock file %s is a symbolic link", ErrUntrusted, path)
	}
	if err != nil {
		return nil, false, fmt.Errorf("instance: opening the lock file %s: %w", path, err)
	}
	fail := func(err error) (lock, bool, error) {
		syscall.Close(fd)
		return nil, false, err
	}
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		return fail(fmt.Errorf("instance: %s: %w", path, err))
	}
	switch {
	case uint32(st.Mode)&syscall.S_IFMT != syscall.S_IFREG:
		return fail(fmt.Errorf("%w: the lock file %s is not a regular file", ErrUntrusted, path))
	case int(st.Uid) != euid():
		return fail(fmt.Errorf("%w: the lock file %s belongs to another user", ErrUntrusted, path))
	case uint64(st.Nlink) != 1:
		return fail(fmt.Errorf("%w: the lock file %s has other names", ErrUntrusted, path))
	}
	if err := syscall.Fchmod(fd, 0o600); err != nil {
		return fail(fmt.Errorf("instance: setting the mode of %s: %w", path, err))
	}
	if err := syscall.SetNonblock(fd, false); err != nil {
		return fail(err)
	}
	for {
		err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if err != syscall.EINTR {
			break
		}
	}
	switch err {
	case nil:
	case syscall.EWOULDBLOCK:
		syscall.Close(fd)
		return nil, false, nil
	default:
		return fail(fmt.Errorf("instance: locking %s: %w", path, err))
	}
	f := os.NewFile(uintptr(fd), path)
	// For a person looking: the lock is the flock, not this. Only the holder
	// writes it, so this never truncates another primary's.
	if err := f.Truncate(0); err != nil {
		f.Close()
		return nil, false, fmt.Errorf("instance: %s: %w", path, err)
	}
	if _, err := fmt.Fprintf(f, "%d\n", os.Getpid()); err != nil {
		f.Close()
		return nil, false, fmt.Errorf("instance: %s: %w", path, err)
	}
	return flockLock{f}, true, nil
}

func (l flockLock) release() { l.f.Close() } // closing drops the flock
