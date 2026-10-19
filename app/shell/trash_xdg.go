//go:build unix

package shell

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
	"unicode/utf8"
)

// The freedesktop.org Trash specification (1.0), without helper programs.
//
// A file goes to the home trash, $XDG_DATA_HOME/Trash, when it is on the same
// file system (a rename moves it). Otherwise it goes to a trash at the top of
// its own file system, the mount point: $topdir/.Trash/$uid when the
// administrator made $topdir/.Trash (a real directory with the sticky bit),
// else $topdir/.Trash-$uid, which is created (0700). Each trash has files/ and
// info/; moving a file X creates info/X.trashinfo (claimed with O_EXCL, so two
// programs trashing a file of the same name get different names) and then
// renames the file into files/X. A trash in the home directory records the
// original absolute path; one at the top of another volume records the path
// relative to that volume's top directory.
//
// The top of another volume is a place other users can write to, so nothing
// found there is trusted. $topdir/.Trash must not be a symbolic link and must
// have the sticky bit; $topdir/.Trash/$uid, $topdir/.Trash-$uid and their files
// and info directories must be real directories, owned by this user, with mode
// 0700 exactly. A shared trash that fails the checks is skipped for
// $topdir/.Trash-$uid; that one failing refuses the request (the file stays where
// it is). Every check is made on a descriptor that is then used for the work
// (trashfd.go), so a directory swapped for a link after the check is not
// followed. The home trash is the user's own space: it is checked for owner, for
// not being a link and for not being writable by others, not for mode 0700.

// Tests replace these: a second file system, another owner and the failure of a
// rename cannot be assumed.
var (
	renameFn = func(dst *tdir, src *tdir, srcName, dstName string) error {
		return dst.renameInto(src, srcName, dstName)
	}
	topdirFn = topdir
	euid     = os.Geteuid
)

// errUntrusted marks a trash directory that failed the checks.
var errUntrusted = errors.New("untrusted trash directory")

// maxStem bounds the name a file gets in the trash, in bytes, so that the
// suffix for a name already taken and ".trashinfo" fit in the 255 bytes a name
// can have. The original name stays in the info file.
const maxStem = 200

// dataHome is $XDG_DATA_HOME, or ~/.local/share when it is unset or relative.
func dataHome() (string, error) {
	if d := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(d) {
		return d, nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", errors.New("no home directory")
	}
	return filepath.Join(h, ".local", "share"), nil
}

// A trashDirs is a trash's open files and info directories.
type trashDirs struct{ files, info *tdir }

func (t *trashDirs) close() {
	t.files.close()
	t.info.close()
}

// secureDir opens the subdirectory name of parent (making it, mode 0700, when
// create is set and it is missing) and checks it: a real directory, owned by
// this user, and with mode 0700 (strict) or not writable by group and others.
// A missing directory without create is reported as os.ErrNotExist; every
// other failure wraps errUntrusted.
func secureDir(parent *tdir, name string, create, strict bool) (*tdir, error) {
	d, err := parent.child(name)
	made := false
	if errors.Is(err, os.ErrNotExist) && create {
		if merr := parent.mkdir(name, 0o700); merr == nil {
			made = true
		} else if !errors.Is(merr, os.ErrExist) {
			return nil, merr
		}
		d, err = parent.child(name)
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	where := filepath.Join(parent.path, name)
	if err != nil { // a symbolic link, a file, or no access
		return nil, fmt.Errorf("%w: %s: %v", errUntrusted, where, err)
	}
	if made && d.chmod(0o700) != nil { // the umask may have taken bits away
		d.close()
		return nil, fmt.Errorf("%w: %s: cannot set its mode", errUntrusted, where)
	}
	st, err := d.stat()
	perm := st.Mode & 0o777
	switch {
	case err != nil:
	case int(st.Uid) != euid():
		err = errors.New("owned by another user")
	case strict && perm != 0o700:
		err = fmt.Errorf("mode %04o, want 0700", perm)
	case !strict && perm&0o022 != 0:
		err = fmt.Errorf("mode %04o is writable by others", perm)
	}
	if err != nil {
		d.close()
		return nil, fmt.Errorf("%w: %s: %v", errUntrusted, where, err)
	}
	return d, nil
}

// openTrash opens the files and info directories of the trash at root.
func openTrash(root *tdir, strict bool) (*trashDirs, error) {
	files, err := secureDir(root, "files", true, strict)
	if err != nil {
		return nil, err
	}
	info, err := secureDir(root, "info", true, strict)
	if err != nil {
		files.close()
		return nil, err
	}
	return &trashDirs{files, info}, nil
}

// trashXDG moves p (absolute, existing) to the trash. dataHome is the home
// data directory; now stamps the deletion date.
func trashXDG(p, dataHome string, uid int, now time.Time) error {
	err := trashHome(p, dataHome, now)
	if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	// Another file system: a trash of its own, at its top.
	top, err := topdirFn(p)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(top, p)
	if err != nil {
		return err
	}
	topd, err := openDir(top, true)
	if err != nil {
		return err
	}
	defer topd.close()
	if t, aerr := openSharedTrash(topd, uid); aerr == nil {
		err := trashInto(p, t, rel, now)
		t.close()
		if err == nil {
			return nil
		}
	}
	root, err := secureDir(topd, ".Trash-"+strconv.Itoa(uid), true, true)
	if err != nil {
		return err
	}
	defer root.close()
	t, err := openTrash(root, true)
	if err != nil {
		return err
	}
	defer t.close()
	return trashInto(p, t, rel, now)
}

func trashHome(p, dataHome string, now time.Time) error {
	if err := os.MkdirAll(dataHome, 0o700); err != nil {
		return err
	}
	dh, err := openDir(dataHome, true)
	if err != nil {
		return err
	}
	defer dh.close()
	root, err := secureDir(dh, "Trash", true, false)
	if err != nil {
		return err
	}
	defer root.close()
	t, err := openTrash(root, false)
	if err != nil {
		return err
	}
	defer t.close()
	return trashInto(p, t, "", now)
}

// openSharedTrash opens $topdir/.Trash/$uid when the administrator's
// $topdir/.Trash is usable: a real directory (not a link) with the sticky bit.
func openSharedTrash(top *tdir, uid int) (*trashDirs, error) {
	shared, err := top.child(".Trash")
	if err != nil {
		return nil, err
	}
	defer shared.close()
	st, err := shared.stat()
	if err != nil {
		return nil, err
	}
	if st.Mode&syscall.S_ISVTX == 0 {
		return nil, fmt.Errorf("%w: %s has no sticky bit", errUntrusted, shared.path)
	}
	root, err := secureDir(shared, strconv.Itoa(uid), true, true)
	if err != nil {
		return nil, err
	}
	defer root.close()
	return openTrash(root, true)
}

// topdir finds the top of p's file system: the highest ancestor on the same
// device.
func topdir(p string) (string, error) {
	dev, err := deviceOf(p)
	if err != nil {
		return "", err
	}
	top := filepath.Dir(p)
	for {
		parent := filepath.Dir(top)
		if parent == top {
			return top, nil
		}
		d, err := deviceOf(parent)
		if err != nil || d != dev {
			return top, nil
		}
		top = parent
	}
}

func deviceOf(p string) (uint64, error) {
	fi, err := os.Lstat(p)
	if err != nil {
		return 0, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, errors.New("no device number")
	}
	return uint64(st.Dev), nil
}

// trashName is the name the file gets in the trash on the n-th try: its own name
// cut to maxStem bytes (at a character boundary, keeping a short extension), and
// ".n" from the second try on.
func trashName(base string, n int) string {
	if len(base) > maxStem {
		ext := filepath.Ext(base)
		if len(ext) > 16 || len(ext) == len(base) {
			ext = ""
		}
		cut := maxStem - len(ext)
		for cut > 0 && !utf8.RuneStart(base[cut]) {
			cut--
		}
		base = base[:cut] + ext
	}
	if n > 1 {
		base += "." + strconv.Itoa(n)
	}
	return base
}

// trashInto moves p into the trash t. recorded is the path to write in the info
// file: "" means p itself (home trash), otherwise a path relative to the volume's
// top. The original name is kept there whatever the file is called in the trash.
func trashInto(p string, t *trashDirs, recorded string, now time.Time) error {
	if recorded == "" {
		recorded = p
	}
	body := "[Trash Info]\nPath=" + (&url.URL{Path: recorded}).EscapedPath() + "\nDeletionDate=" + now.Format("2006-01-02T15:04:05") + "\n"
	src, err := openDir(filepath.Dir(p), true)
	if err != nil {
		return err
	}
	defer src.close()
	base := filepath.Base(p)

	for n := 1; n <= 10000; n++ {
		name := trashName(base, n)
		infoName := name + ".trashinfo"
		f, err := t.info.createExcl(infoName, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return err
		}
		_, werr := f.WriteString(body)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr == nil {
			werr = renameFn(t.files, src, base, name)
		}
		if werr == nil {
			return nil
		}
		t.info.remove(infoName)
		if errors.Is(werr, os.ErrExist) { // a file of that name without an info file
			continue
		}
		return werr
	}
	return fmt.Errorf("no free name for %s in the trash", base)
}
