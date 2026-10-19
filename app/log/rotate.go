package log

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// rotator is a size-limited log file with numbered backups: name.log is the
// current file, name.1.log the previous one, up to name.<files-1>.log. A line is
// never split between two files. It is used by one goroutine at a time (the
// async writer's), so it has no lock.
type rotator struct {
	dir, name string
	max       int64
	files     int // including the current one; at least 1
	f         *os.File
	size      int64
}

func (r *rotator) path(n int) string {
	if n == 0 {
		return filepath.Join(r.dir, r.name+".log")
	}
	return filepath.Join(r.dir, fmt.Sprintf("%s.%d.log", r.name, n))
}

func (r *rotator) open() error {
	f, err := os.OpenFile(r.path(0), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	r.f, r.size = f, fi.Size()
	return nil
}

// rotate closes the current file, shifts the backups up (dropping the oldest)
// and starts a new file. Renames that fail are skipped: losing a backup is
// better than losing the log. If the current file could not be moved or removed
// (a backup name taken by a directory, a file another process holds), it is
// truncated instead: the log must make progress, and the oldest lines are the
// ones to lose.
func (r *rotator) rotate() error {
	if r.f != nil {
		r.f.Close()
		r.f = nil
	}
	if r.files <= 1 {
		os.Remove(r.path(0))
	} else {
		os.Remove(r.path(r.files - 1))
		for i := r.files - 2; i >= 0; i-- {
			os.Rename(r.path(i), r.path(i+1))
		}
	}
	if err := r.open(); err != nil {
		return err
	}
	if r.size > 0 { // still the old, full file
		if err := r.f.Truncate(0); err != nil {
			r.f.Close()
			r.f = nil
			return err
		}
		r.size = 0
	}
	return nil
}

// Write appends p, rotating between lines when the file would pass max. A line
// longer than max gets a file to itself.
func (r *rotator) Write(p []byte) (int, error) {
	total := len(p)
	if r.f == nil {
		if err := r.open(); err != nil {
			return 0, err
		}
	}
	rotated := false // rotated since the last write: whatever the file holds now, write one line
	for len(p) > 0 {
		room := r.max - r.size
		n := 0 // bytes of whole lines that fit
		for n < len(p) {
			end := len(p)
			if i := bytes.IndexByte(p[n:], '\n'); i >= 0 {
				end = n + i + 1
			}
			if int64(end) > room {
				break
			}
			n = end
		}
		if n == 0 {
			if r.size > 0 && !rotated {
				if err := r.rotate(); err != nil {
					return total - len(p), err
				}
				rotated = true
				continue
			}
			n = len(p) // the first line alone is too long: it gets the file
			if i := bytes.IndexByte(p, '\n'); i >= 0 {
				n = i + 1
			}
		}
		w, err := r.f.Write(p[:n])
		r.size += int64(w)
		if err != nil {
			return total - len(p) + w, err
		}
		p = p[n:]
		rotated = false
	}
	return total, nil
}

func (r *rotator) Close() error {
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}
