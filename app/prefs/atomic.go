package prefs

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// WriteAtomic replaces the file at path with data so that a reader, or a crash
// or power loss at any moment, sees the old content or the new and never a
// mixture. It writes a temporary file in the same directory, flushes it to
// stable storage (fsync), renames it over path, and on Unix flushes the
// directory. Missing parent directories are created (0700). perm is the new
// file's mode (before umask); an existing file's mode is not preserved.
//
// It blocks on the disk: call it from a worker, never from the UI thread.
// Options.FullSync makes the flush the strongest one the OS offers (see there);
// WriteAtomic uses the ordinary fsync.
func WriteAtomic(path string, data []byte, perm os.FileMode) error {
	return writeAtomic(path, data, perm, false)
}

func writeAtomic(path string, data []byte, perm os.FileMode, full bool) (err error) {
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, 0o700); err != nil {
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
	if err = syncFile(f, full); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = rename(tmp, path); err != nil {
		return err
	}
	syncDir(dir) // best effort: the rename is already visible; this makes it durable
	return nil
}

// rename moves tmp over path. Windows refuses while another process (a virus
// scanner, a backup tool, a reader that did not share delete access) holds path
// open, usually for a few milliseconds, so it retries briefly.
func rename(tmp, path string) error {
	var err error
	for i := 0; i < 20; i++ {
		if err = os.Rename(tmp, path); err == nil || runtime.GOOS != "windows" {
			break
		}
		time.Sleep(time.Duration(min(i+1, 10)) * 5 * time.Millisecond)
	}
	if err != nil {
		return fmt.Errorf("prefs: replacing %s: %w", filepath.Base(path), err)
	}
	return nil
}
