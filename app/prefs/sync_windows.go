package prefs

import (
	"os"
	"syscall"
	"unsafe"

	"gonativeui/internal/win32"
)

var pMoveFileExW = win32.Proc(win32.LoadSystem("kernel32.dll"), "MoveFileExW")

const (
	movefileReplaceExisting = 0x1
	movefileWriteThrough    = 0x8
)

// moveFile renames src over dst with MoveFileExW, replacing an existing dst and
// not returning until the move has been written to disk (MOVEFILE_WRITE_THROUGH).
// os.Rename replaces too, but gives no such promise. (ReplaceFileW would keep
// the old file's attributes and ACLs, which a preferences file does not need and
// which fails on more kinds of volumes.)
func moveFile(src, dst string) error {
	s, err := syscall.UTF16PtrFromString(src)
	if err != nil {
		return err
	}
	d, err := syscall.UTF16PtrFromString(dst)
	if err != nil {
		return err
	}
	r, _, e := syscall.SyscallN(pMoveFileExW, uintptr(unsafe.Pointer(s)), uintptr(unsafe.Pointer(d)),
		movefileReplaceExisting|movefileWriteThrough)
	if uint32(r) == 0 { // a BOOL: only the low 32 bits of the register
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: e}
	}
	return nil
}

// syncDir does nothing: Windows cannot flush a directory; moveFile's write
// through is what makes the rename durable.
func syncDir(string) error { return nil }

func syncFile(f *os.File, _ bool) error { return f.Sync() }
