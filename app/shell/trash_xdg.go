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

// Tests replace these: a second file system cannot be assumed.
var (
	renameFn = os.Rename
	topdirFn = topdir
)

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

// trashXDG moves p (absolute, existing) to the trash. dataHome is the home
// data directory; now stamps the deletion date.
func trashXDG(p, dataHome string, uid int, now time.Time) error {
	err := trashInto(p, filepath.Join(dataHome, "Trash"), "", now)
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
	if admin := filepath.Join(top, ".Trash"); adminTrashOK(admin) {
		dir := filepath.Join(admin, strconv.Itoa(uid))
		if err := os.MkdirAll(dir, 0o700); err == nil {
			if err := trashInto(p, dir, rel, now); err == nil || !errors.Is(err, os.ErrPermission) {
				return err
			}
		}
	}
	return trashInto(p, filepath.Join(top, ".Trash-"+strconv.Itoa(uid)), rel, now)
}

// adminTrashOK reports whether dir is the administrator's shared trash: a
// directory (not a symbolic link) with the sticky bit.
func adminTrashOK(dir string) bool {
	fi, err := os.Lstat(dir)
	return err == nil && fi.IsDir() && fi.Mode()&os.ModeSticky != 0 && fi.Mode()&os.ModeSymlink == 0
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

// trashInto moves p into the trash directory trash (which has files/ and
// info/, made as needed). recorded is the path to write in the info file: ""
// means p itself (home trash), otherwise a path relative to the volume's top.
func trashInto(p, trash, recorded string, now time.Time) error {
	filesDir, infoDir := filepath.Join(trash, "files"), filepath.Join(trash, "info")
	for _, d := range []string{filesDir, infoDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	if recorded == "" {
		recorded = p
	}
	body := "[Trash Info]\nPath=" + (&url.URL{Path: recorded}).EscapedPath() + "\nDeletionDate=" + now.Format("2006-01-02T15:04:05") + "\n"

	base := filepath.Base(p)
	for n := 1; ; n++ {
		name := base
		if n > 1 {
			name = base + "." + strconv.Itoa(n)
		}
		if n > 10000 {
			return fmt.Errorf("no free name for %s in the trash", base)
		}
		if _, err := os.Lstat(filepath.Join(filesDir, name)); err == nil {
			continue // a file without an info file, left by something else
		}
		info := filepath.Join(infoDir, name+".trashinfo")
		f, err := os.OpenFile(info, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
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
			werr = renameFn(p, filepath.Join(filesDir, name))
		}
		if werr != nil {
			os.Remove(info)
			return werr
		}
		return nil
	}
}
