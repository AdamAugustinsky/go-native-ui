//go:build darwin

package shell

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/cf"
	"gonativeui/internal/ffi"
)

// Binding the identity check to the move. NSFileManager moves a file given by
// its path, and there is no descriptor-relative form of it, so the check cannot
// be made atomic with the move. What the package does instead:
//
//  1. The entry is opened itself (O_SYMLINK: a link is not followed), and the
//     check works on that descriptor: fstat compared with the home folder and
//     the volume root. A descriptor keeps the object alive, so its identity
//     cannot be reused by another file while the call runs.
//  2. NSFileManager is given the path the descriptor has now (F_GETPATH: real,
//     with no link on the way), not the path the caller wrote. A link or a
//     component renamed before this point changes nothing.
//  3. The move returns the URL the item now has. After it, that item is
//     compared with the descriptor's identity: the same device and inode (when
//     the Trash is on another volume the item is a copy and this is not
//     checked). If it is another file, a folder was swapped in the window left
//     between steps 2 and 3, and the swapped-in file is put back with
//     renamex_np(RENAME_EXCL), which replaces nothing, and Trash returns an
//     error saying so.
//
// The window that remains is between the path being read in step 2 and the move:
// it can still send the wrong file to the Trash, but never unnoticed, and it is
// undone unless someone has taken the old place again.

const (
	oSymlink   = 0x200000 // O_SYMLINK: open a symbolic link itself
	renameExcl = 0x4      // RENAME_EXCL
	maxPath    = 1024     // MAXPATHLEN
)

// libc symbols for the undo, loaded with the shell's.
var (
	libcOnce     sync.Once
	renamexNP    uintptr
	errnoLocator uintptr
)

func loadLibc() { libcOnce.Do(loadLibcSyms) }

func loadLibcSyms() {
	if h, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL); err == nil {
		renamexNP, errnoLocator = cf.Sym(h, "renamex_np"), cf.Sym(h, "__error")
	}
}

// held is an entry opened for the length of a Trash call.
type held struct {
	fd   int // -1 when the entry could not be opened (no read permission, a socket)
	st   syscall.Stat_t
	path string // the real path of the entry
}

// hold opens p without following a link and records what it is. When the entry
// cannot be opened the identity is taken from lstat of the path, which is the
// weaker form (the inode could be reused), and the path is used as given.
func hold(p string) (*held, error) {
	h := &held{fd: -1, path: p}
	fd, err := syscall.Open(p, syscall.O_RDONLY|oSymlink|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err == nil {
		h.fd = fd
		if err := syscall.Fstat(fd, &h.st); err != nil {
			h.close()
			return nil, err
		}
		var buf [maxPath]byte
		if _, _, e := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), 50 /* F_GETPATH */, uintptr(unsafe.Pointer(&buf[0]))); e == 0 {
			n := 0
			for n < len(buf) && buf[n] != 0 {
				n++
			}
			if n > 0 {
				h.path = string(buf[:n])
			}
		}
		runtime.KeepAlive(&buf)
		return h, nil
	}
	if err := syscall.Lstat(p, &h.st); err != nil {
		return nil, err
	}
	return h, nil
}

func (h *held) close() {
	if h.fd >= 0 {
		syscall.Close(h.fd)
		h.fd = -1
	}
}

// protected reports whether the held entry is the user's home folder or the
// root of its volume, by what it is.
func (h *held) protected() bool {
	targets := []string{"/"}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		targets = append(targets, home)
	}
	for _, t := range targets {
		var st syscall.Stat_t
		if syscall.Stat(t, &st) == nil && st.Dev == h.st.Dev && st.Ino == h.st.Ino {
			return true
		}
	}
	return false
}

// moved reports whether the held entry is what is now at the Trash's URL.
func (h *held) moved(result string) (bool, error) {
	var st syscall.Stat_t
	if err := syscall.Lstat(result, &st); err != nil {
		return false, err
	}
	if st.Dev == h.st.Dev {
		return st.Ino == h.st.Ino, nil
	}
	// Another volume: the system copied the item and removed the original, so
	// the inode differs and nothing on the descriptor tells the two apart (a
	// removed folder keeps its link count). Not verified.
	return true, nil
}

// renameExclusive renames from to to and fails if to exists (renamex_np with
// RENAME_EXCL).
func renameExclusive(from, to string) error {
	loadLibc()
	if renamexNP == 0 || errnoLocator == 0 {
		return cf.ErrUnavailable
	}
	fr := cf.Acquire()
	defer cf.Release(fr)
	a, err := syscall.BytePtrFromString(from)
	if err != nil {
		return err
	}
	b, err := syscall.BytePtrFromString(to)
	if err != nil {
		return err
	}
	runtime.LockOSThread()
	r := int32(fr.Reset().ArgPtr(unsafe.Pointer(a)).ArgPtr(unsafe.Pointer(b)).Arg32(renameExcl).Call(renamexNP).R0())
	var e syscall.Errno
	if r != 0 {
		e = syscall.Errno(*(*int32)(ffi.PtrFromC(fr.Call0(errnoLocator))))
	}
	runtime.UnlockOSThread()
	runtime.KeepAlive(a)
	runtime.KeepAlive(b)
	if r == 0 {
		return nil
	}
	return &os.LinkError{Op: "renamex_np", Old: from, New: to, Err: e}
}

// beforeMove is a test seam: it runs after the entry is held and before the
// system is asked to move it, where a swap would have to happen.
var beforeMove = func() {}

func (osSystem) trash(p string) error {
	if err := ready(); err != nil {
		return err
	}
	h, err := hold(p)
	if err != nil {
		return &os.PathError{Op: "trash", Path: p, Err: err}
	}
	defer h.close()
	if h.protected() {
		return &os.PathError{Op: "trash", Path: p, Err: ErrInvalid}
	}
	beforeMove()

	result, err := trashURL(h.path)
	if err != nil {
		return &os.PathError{Op: "trash", Path: p, Err: err}
	}
	same, err := h.moved(result)
	if same && err == nil {
		return nil
	}
	// Something else went to the Trash. Put it back where it was, if that place
	// is still free.
	if rerr := renameExclusive(result, h.path); rerr != nil {
		return &os.PathError{Op: "trash", Path: p, Err: fmt.Errorf("the path was changed while it was moved, and another file went to the Trash as %s (%v)", filepath.Base(result), rerr)}
	}
	return &os.PathError{Op: "trash", Path: p, Err: fmt.Errorf("the path was changed while it was moved; the file that went to the Trash was put back")}
}
