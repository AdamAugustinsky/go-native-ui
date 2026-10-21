package prefs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

// WriteAtomic replaces the file at path with data so that a reader, or a crash
// or power loss at any moment, sees the old content or the new and never a
// mixture. It writes a temporary file in the same directory, flushes it to
// stable storage (fsync), renames it over path, and then makes the rename
// durable: on Unix by fsyncing the directory (and the parent of every directory
// it had to create), on Windows by renaming with MOVEFILE_WRITE_THROUGH, which
// returns once the move is on disk. Missing parent directories are created
// (0700). perm is the new file's mode (before umask); an existing file's mode is
// not preserved.
//
// A flush that fails is an error: the file may already be in place, but it is
// not known to survive a power loss, so the caller should treat the write as
// failed and try again. (A file system that does not support fsync on a
// directory, which reports EINVAL or ENOTSUP, is not an error.)
//
// It blocks on the disk: call it from a worker, never from the UI thread.
// Options.FullSync makes the flush the strongest one the OS offers (see there);
// WriteAtomic uses the ordinary fsync.
func WriteAtomic(path string, data []byte, perm os.FileMode) error {
	return writeAtomic(path, data, perm, false)
}

// The flushes, the move and the read are variables so that tests can fail
// them.
var (
	syncFileFn = syncFile
	syncDirFn  = syncDir
	moveFileFn = moveFile
	readFileFn = os.ReadFile
)

// retryBusy says whether a refused move or read is tried again: on Windows,
// where another process holding the file refuses it for a while (a variable
// so that tests can have it elsewhere).
var retryBusy = runtime.GOOS == "windows"

// busyTries and busyWait pace those retries: about 0.8 s in all.
const busyTries = 20

func busyWait(i int) time.Duration { return time.Duration(min(i+1, 10)) * 5 * time.Millisecond }

// fsyncUnsupported reports whether err says the file system cannot do this
// flush at all (as opposed to failing to do it): the one case where falling
// back to a weaker flush, or to none, is right.
func fsyncUnsupported(err error) bool {
	return errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.EOPNOTSUPP) || errors.Is(err, syscall.EINVAL)
}

// mkdirAll creates dir and its parents and returns the directories it created.
func mkdirAll(dir string) (created []string, err error) {
	for d := dir; ; {
		if _, serr := os.Stat(d); serr == nil {
			break
		}
		created = append(created, d)
		p := filepath.Dir(d)
		if p == d {
			break
		}
		d = p
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return created, nil
}

func writeAtomic(path string, data []byte, perm os.FileMode, full bool) (err error) {
	dir := filepath.Dir(path)
	created, err := mkdirAll(dir)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			f.Close() // already closed on the paths that get here after Close; harmless
			os.Remove(tmp)
		}
	}()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if cerr := f.Chmod(perm); cerr != nil && runtime.GOOS != "windows" { // Windows has no Unix modes
		err = cerr
		return err
	}
	if err = syncFileFn(f, full); err != nil {
		return fmt.Errorf("prefs: flushing %s: %w", filepath.Base(tmp), err)
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = rename(tmp, path); err != nil {
		return err
	}
	// The file is in place. Make the directory entries durable: the file's, and
	// those of any directories this call created (their parents).
	var serr error
	if e := syncDirFn(dir); e != nil {
		serr = e
	}
	for _, c := range created {
		if e := syncDirFn(filepath.Dir(c)); e != nil && serr == nil {
			serr = e
		}
	}
	if serr != nil {
		// Not an undo: the new file is in place and readable. Returning the
		// error keeps the store dirty, so the next save tries again.
		return fmt.Errorf("prefs: %s is replaced but not known to be durable: %w", filepath.Base(path), serr)
	}
	return nil
}

// rename moves tmp over path. Windows refuses while another process (a virus
// scanner, a backup tool, a reader that did not share delete access) holds path
// open, usually for a few milliseconds, so it retries briefly.
func rename(tmp, path string) error {
	var err error
	for i := 0; i < busyTries; i++ {
		if err = moveFileFn(tmp, path); err == nil || !retryBusy {
			break
		}
		time.Sleep(busyWait(i))
	}
	if err != nil {
		return fmt.Errorf("prefs: replacing %s: %w", filepath.Base(path), err)
	}
	return nil
}

// readFile reads path. On Windows a process that holds the file (a virus
// scanner looking at the one just renamed into place) can refuse the open for
// a few milliseconds, as it refuses rename: readFile tries again as rename
// does. A missing file is not retried.
func readFile(path string) ([]byte, error) {
	var data []byte
	var err error
	for i := 0; i < busyTries; i++ {
		if data, err = readFileFn(path); err == nil || !retryBusy || errors.Is(err, os.ErrNotExist) {
			break
		}
		time.Sleep(busyWait(i))
	}
	return data, err
}
