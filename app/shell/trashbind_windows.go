//go:build windows

package shell

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"gonativeui/internal/win32"
)

// Binding the identity check to the move. SHFileOperationW moves an item given
// by its path, and no shell call moves one given by a handle, so the check cannot
// be made atomic with the move. What the package does instead:
//
//  1. The entry is opened itself (FILE_FLAG_OPEN_REPARSE_POINT: a link or
//     junction is not followed) for attributes only, with every share mode, so it
//     blocks nothing, and the check works on that handle: volume serial number
//     and file index compared with the home folder and the volume root. The handle
//     keeps the file from being deleted for good and its index from being reused
//     while the call runs.
//  2. SHFileOperationW is given the path the handle has now (the final path:
//     real, with no link or junction on the way), not the path the caller wrote.
//  3. After it, the handle is asked where its file is. The Recycle Bin holds it
//     when the path names $Recycle.Bin. If it does not, the bin took another
//     file: a folder on the path was swapped in the window left between steps 2
//     and 3. Trash returns an error saying so (the file that went to the bin can
//     be restored from it; it is not put back here, because the shell does not
//     return where it put it).
//
// The window that remains is between the path being read in step 2 and the move.

const (
	fileReadAttributes = 0x80
	fileShareAll       = 0x1 | 0x2 | 0x4 // read, write, delete
	flagBackup         = 0x02000000      // FILE_FLAG_BACKUP_SEMANTICS (opens directories)
	flagReparse        = 0x00200000      // FILE_FLAG_OPEN_REPARSE_POINT
	maxShellPath       = 259             // MAX_PATH - 1: SHFileOperationW does not take longer
)

var pGetFinalPath = func() uintptr { return win32.Proc(win32.LoadSystem("kernel32.dll"), "GetFinalPathNameByHandleW") }()

type fileID struct{ vol, hi, lo uint32 }

// heldFile is an entry opened for the length of a Trash call.
type heldFile struct {
	h    syscall.Handle
	id   fileID
	path string // the final path of the entry, in the form the shell takes
}

func openHandle(p string, noFollow bool) (*heldFile, error) {
	w, err := syscall.UTF16PtrFromString(p)
	if err != nil {
		return nil, ErrInvalid
	}
	attrs := uint32(flagBackup)
	if noFollow {
		attrs |= flagReparse
	}
	h, err := syscall.CreateFile(w, fileReadAttributes, fileShareAll, nil, syscall.OPEN_EXISTING, attrs, 0)
	if err != nil {
		return nil, err
	}
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(h, &info); err != nil {
		syscall.CloseHandle(h)
		return nil, err
	}
	f := &heldFile{h: h, id: fileID{info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow}, path: p}
	if q := f.finalPath(); q != "" && len(q) <= maxShellPath {
		f.path = q
	}
	return f, nil
}

func (f *heldFile) close() { syscall.CloseHandle(f.h) }

// finalPath is the path the handle's file has now, "" when Windows cannot say.
// The \\?\ prefix is removed (the shell does not take it).
func (f *heldFile) finalPath() string {
	if pGetFinalPath == 0 {
		return ""
	}
	var buf [1024]uint16
	r := win32.Call(pGetFinalPath, uintptr(f.h), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0)
	if n := int(uint32(r)); n == 0 || n >= len(buf) {
		return ""
	}
	p := syscall.UTF16ToString(buf[:])
	switch {
	case strings.HasPrefix(p, `\\?\UNC\`):
		return `\\` + p[len(`\\?\UNC\`):]
	case strings.HasPrefix(p, `\\?\`):
		return p[len(`\\?\`):]
	}
	return p
}

// protected reports whether the entry is the user's home folder or the root of
// its volume, by what it is.
func (f *heldFile) protected() bool {
	targets := []string{filepath.VolumeName(f.path) + `\`}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		targets = append(targets, home)
	}
	for _, t := range targets {
		if o, err := openHandle(t, false); err == nil {
			same := o.id == f.id
			o.close()
			if same {
				return true
			}
		}
	}
	return false
}

// inBin reports whether the entry is now in a Recycle Bin.
func (f *heldFile) inBin() (known, in bool) {
	p := f.finalPath()
	if p == "" {
		return false, false
	}
	return true, strings.Contains(strings.ToLower(p), `\$recycle.bin\`)
}

// beforeMove is a test seam: it runs after the entry is held and before the
// shell is asked to move it, where a swap would have to happen.
var beforeMove = func() {}
