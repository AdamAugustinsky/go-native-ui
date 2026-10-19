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
// drops the lock when the process ends, however it ends. The file is created
// 0600; its descriptor is not inherited by child processes (Go opens files
// close-on-exec), so a child that outlives the primary cannot keep the lock.
func acquireLock(path string) (lk lock, got bool, err error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, false, err
	}
	f.Chmod(0o600)
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err != syscall.EINTR {
			break
		}
	}
	switch err {
	case nil:
		f.Truncate(0)
		fmt.Fprintf(f, "%d\n", os.Getpid()) // for a person looking: the lock is the flock, not this
		return flockLock{f}, true, nil
	case syscall.EWOULDBLOCK:
		f.Close()
		return nil, false, nil
	}
	f.Close()
	return nil, false, fmt.Errorf("instance: locking %s: %w", path, err)
}

func (l flockLock) release() { l.f.Close() } // closing drops the flock
