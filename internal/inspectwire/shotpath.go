package inspectwire

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// ShotDest is a place a shot may be written, validated when the request
// arrived and held by an open directory (os.Root): what is written goes into
// that directory whatever the path names by the time of the write.
type ShotDest struct {
	// Path is the file's path as asked for (the directory resolved), Name the
	// file's name in the directory.
	Path, Name string
	root       *os.Root
}

// OpenShotDest checks the path a client asked a shot to be written to and
// opens its directory. The path must be absolute and name a file (not a
// directory) in an existing directory that is as private as the socket's
// (CheckDir: owned by the user, closed to the group and others, no ACL, every
// ancestor safe), so nobody else can swap it between the check and the write.
// The directory must also not be the socket's directory or inside it: the
// client could otherwise replace the socket. That is checked by identity
// (os.SameFile) on the open directory and each of its ancestors, so a link or a
// case-insensitive alias of the socket's directory does not get past it, and
// any failure to look at one refuses. The caller closes the ShotDest.
func OpenShotDest(socket, path string) (*ShotDest, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("the path %q is not absolute", path)
	}
	path = filepath.Clean(path)
	name := filepath.Base(path)
	if name == "." || name == ".." || name == string(filepath.Separator) {
		return nil, fmt.Errorf("%q does not name a file", path)
	}
	dir, err := CheckDir(filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("the directory of %q is not fit for a shot: %w", path, err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("the directory of %q: %w", path, err)
	}
	ok := false
	defer func() {
		if !ok {
			root.Close()
		}
	}()
	// The directory opened is the one the path names (it was not swapped
	// between the resolution and the open).
	rfi, err := root.Stat(".")
	if err != nil {
		return nil, err
	}
	if pfi, err := statDir(dir); err != nil || !os.SameFile(rfi, pfi) {
		return nil, fmt.Errorf("%q changed while it was being checked", dir)
	}
	if fi, err := root.Lstat(name); err == nil && fi.IsDir() {
		return nil, fmt.Errorf("%q is a directory", path)
	}
	sfi, err := statDir(filepath.Dir(socket))
	if err != nil {
		return nil, fmt.Errorf("the socket's directory: %w", err)
	}
	if os.SameFile(rfi, sfi) { // the directory held open, not a path that could name another by now
		return nil, fmt.Errorf("%q is in the socket's directory %q; write elsewhere", path, filepath.Dir(socket))
	}
	for d := dir; ; d = filepath.Dir(d) {
		fi, err := statDir(d)
		if err != nil {
			return nil, fmt.Errorf("cannot look at %q to see whether it is the socket's directory: %w", d, err)
		}
		if os.SameFile(fi, sfi) {
			return nil, fmt.Errorf("%q is in the socket's directory %q; write elsewhere", path, filepath.Dir(socket))
		}
		if d == filepath.Dir(d) {
			break
		}
	}
	ok = true
	return &ShotDest{Path: filepath.Join(dir, name), Name: name, root: root}, nil
}

// statDir is os.Stat (a variable for the tests).
var statDir = os.Stat

// Close closes the directory. It may be called more than once.
func (d *ShotDest) Close() { d.root.Close() }

// testHookAfterTemp runs in Write between making the temporary file and
// finishing it (for the tests).
var testHookAfterTemp func(name string)

// Write writes data to the file: through the held directory, into a temporary
// file made there (never following a link), set to mode 0644 on the open file
// and renamed over the name, so that a link at the name is replaced and not
// followed and a reader never sees half a file.
func (d *ShotDest) Write(data []byte) error {
	tmp := ".gnui-shot-" + randomName(6) + ".png"
	f, err := d.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	done := false
	defer func() {
		if !done {
			f.Close()
			d.root.Remove(tmp)
		}
	}()
	if testHookAfterTemp != nil {
		testHookAfterTemp(tmp)
	}
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Chmod(0o644); err != nil { // on the open file, never by path
		return err
	}
	mine, err := f.Stat()
	if err != nil {
		return err
	}
	if cur, err := d.root.Lstat(tmp); err != nil || !os.SameFile(cur, mine) {
		return fmt.Errorf("the temporary file %s was replaced while it was written", tmp)
	}
	if err = f.Close(); err != nil {
		return err
	}
	done = true
	if err = d.root.Rename(tmp, d.Name); err != nil {
		d.root.Remove(tmp)
		return err
	}
	return nil
}

// randomName returns n random bytes as hex.
func randomName(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
