//go:build windows

package fs

import (
	"os"
	"sync"
	"syscall"
	"unicode/utf8"
	"unsafe"

	"gonativeui/internal/win32"
)

// Windows plumbing for the fs backends: entry points resolved once, path
// normalisation (long-path prefix) and UTF-16 <-> UTF-8 conversion that does
// not allocate. Only kernel32 is used here; it is a KnownDLL, loaded from
// System32 only (win32.LoadSystem).
//
// Every constant below was checked against the mingw-w64 headers
// (mingw-w64-headers/include/{winnt,minwinbase,winbase,fileapi}.h, master,
// fetched 2026-10-17) and the Win32 documentation.

var (
	winK32 = win32.LoadSystem("kernel32.dll")

	pFindFirstFileExW             = win32.Proc(winK32, "FindFirstFileExW")
	pFindNextFileW                = win32.Proc(winK32, "FindNextFileW")
	pFindClose                    = win32.Proc(winK32, "FindClose")
	pCreateFileW                  = win32.Proc(winK32, "CreateFileW")
	pCloseHandle                  = win32.Proc(winK32, "CloseHandle")
	pGetFileInformationByHandleEx = win32.Proc(winK32, "GetFileInformationByHandleEx")
	pGetFullPathNameW             = win32.Proc(winK32, "GetFullPathNameW")
	pGetFileAttributesW           = win32.Proc(winK32, "GetFileAttributesW")
	pCreateEventW                 = win32.Proc(winK32, "CreateEventW")
	pSetEvent                     = win32.Proc(winK32, "SetEvent")
	pResetEvent                   = win32.Proc(winK32, "ResetEvent")
	pWaitForMultipleObjects       = win32.Proc(winK32, "WaitForMultipleObjects")
	pReadDirectoryChangesW        = win32.Proc(winK32, "ReadDirectoryChangesW")
	pGetOverlappedResult          = win32.Proc(winK32, "GetOverlappedResult")
	pCancelIoEx                   = win32.Proc(winK32, "CancelIoEx")
)

const (
	winInvalidHandle = ^uintptr(0) // INVALID_HANDLE_VALUE

	// winnt.h:6111 FILE_LIST_DIRECTORY; 6166-6168 FILE_SHARE_*.
	winFileListDirectory = 0x0001
	winShareAll          = 0x1 | 0x2 | 0x4 // READ | WRITE | DELETE
	winOpenExisting      = 3
	// winbase.h:85,90.
	winFlagBackupSemantics = 0x02000000
	winFlagOverlapped      = 0x40000000

	// winnt.h:6170-6179.
	winAttrReadOnly     = 0x00000001
	winAttrHidden       = 0x00000002
	winAttrDirectory    = 0x00000010
	winAttrReparsePoint = 0x00000400
	// winnt.h:6413,6422.
	winReparseMountPoint = 0xA0000003
	winReparseSymlink    = 0xA000000C

	winInvalidFileAttributes = 0xFFFFFFFF

	// Errors (winerror.h).
	winErrFileNotFound  syscall.Errno = 2
	winErrAccessDenied  syscall.Errno = 5
	winErrNotSupported  syscall.Errno = 50
	winErrInvalidParam  syscall.Errno = 87
	winErrInvalidLevel  syscall.Errno = 124
	winErrNoMoreFiles   syscall.Errno = 18
	winErrAborted       syscall.Errno = 995
	winErrNotifyEnumDir syscall.Errno = 1022
	winErrInvalidName   syscall.Errno = 123
	winErrDirectory     syscall.Errno = 267 // ERROR_DIRECTORY
)

// winPathCap is the UTF-16 capacity of the pooled path buffers; longer paths
// (up to 32 767 units) grow them.
const winPathCap = 1024

// winScratch is the per-call working memory of a listing: pooled so that
// listing allocates nothing once warm. Everything lives on the heap, so the
// pointers handed to the kernel stay valid.
type winScratch struct {
	fd   winFindData
	in   []uint16 // the caller's path in UTF-16, then the final directory path
	full []uint16 // GetFullPathNameW output
	pat  []uint16 // FindFirstFileExW pattern
	big  []uint64 // GetFileInformationByHandleEx buffer (8-byte aligned), lazily allocated
}

// Scratch goes through a free list, not a sync.Pool: a Pool may hand a
// goroutine on another P a fresh object and the GC empties it, so a warm
// listing would still allocate.
var (
	winScratchMu   sync.Mutex
	winScratchFree []*winScratch
)

func winGetScratch() *winScratch {
	winScratchMu.Lock()
	defer winScratchMu.Unlock()
	if n := len(winScratchFree); n > 0 {
		s := winScratchFree[n-1]
		winScratchFree = winScratchFree[:n-1]
		return s
	}
	return &winScratch{
		in:   make([]uint16, 0, winPathCap),
		full: make([]uint16, winPathCap),
		pat:  make([]uint16, 0, winPathCap),
	}
}

func winPutScratch(s *winScratch) {
	winScratchMu.Lock()
	winScratchFree = append(winScratchFree, s)
	winScratchMu.Unlock()
}

// winAppendUTF16 appends s as UTF-16 (no terminator). ok is false when s
// contains a NUL, which no Windows path can.
func winAppendUTF16(dst []uint16, s string) (out []uint16, ok bool) {
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			if c == 0 {
				return dst, false
			}
			dst = append(dst, uint16(c))
			i++
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		i += n
		if r >= 0x10000 {
			r -= 0x10000
			dst = append(dst, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF)))
		} else {
			dst = append(dst, uint16(r)) // invalid UTF-8 became U+FFFD
		}
	}
	return dst, true
}

// winAppendUTF8 appends the UTF-16 text w as UTF-8; unpaired surrogates
// become U+FFFD. It does not allocate beyond growing dst.
func winAppendUTF8(dst []byte, w []uint16) []byte {
	for i := 0; i < len(w); i++ {
		c := w[i]
		switch {
		case c < 0x80:
			dst = append(dst, byte(c))
		case c < 0x800:
			dst = append(dst, 0xC0|byte(c>>6), 0x80|byte(c&0x3F))
		case c >= 0xD800 && c < 0xDC00 && i+1 < len(w) && w[i+1] >= 0xDC00 && w[i+1] < 0xE000:
			r := (rune(c)-0xD800)<<10 | (rune(w[i+1]) - 0xDC00) + 0x10000
			i++
			dst = append(dst, 0xF0|byte(r>>18), 0x80|byte(r>>12&0x3F), 0x80|byte(r>>6&0x3F), 0x80|byte(r&0x3F))
		case c >= 0xD800 && c < 0xE000: // unpaired surrogate
			dst = append(dst, 0xEF, 0xBF, 0xBD)
		default:
			dst = append(dst, 0xE0|byte(c>>12), 0x80|byte(c>>6&0x3F), 0x80|byte(c&0x3F))
		}
	}
	return dst
}

func winIsSep(c uint16) bool { return c == '\\' || c == '/' }

// winDirPath converts path to the absolute, \\?\-prefixed UTF-16 form used
// for every file-system call (so paths longer than MAX_PATH work without
// the long-path manifest), NUL-terminated. The result aliases s.in and is
// valid until the next winDirPath call on s.
func (s *winScratch) winDirPath(path string) (p []uint16, err syscall.Errno) {
	if path == "" {
		return nil, winErrFileNotFound
	}
	in, ok := winAppendUTF16(s.in[:0], path)
	if !ok {
		return nil, winErrInvalidName
	}
	in = append(in, 0)
	s.in = in
	// Already a verbatim (\\?\) or device (\\.\) path: no normalisation.
	if len(in) >= 5 && in[0] == '\\' && in[1] == '\\' && (in[2] == '?' || in[2] == '.') && in[3] == '\\' {
		return in, 0
	}
	// Make it absolute and canonical (forward slashes, ".", "..", drive-relative).
	var n uintptr
	for {
		r, _, e := syscall.SyscallN(pGetFullPathNameW,
			uintptr(unsafe.Pointer(&s.in[0])), uintptr(len(s.full)), uintptr(unsafe.Pointer(&s.full[0])), 0)
		n = r
		if n == 0 {
			if e == 0 {
				e = winErrInvalidName
			}
			return nil, e
		}
		if n < uintptr(len(s.full)) {
			break
		}
		s.full = make([]uint16, n+1) // n includes the NUL when the buffer was too small
	}
	full := s.full[:n]
	out := s.in[:0]
	switch {
	case len(full) >= 2 && full[0] == '\\' && full[1] == '\\':
		if len(full) >= 4 && (full[2] == '.' || full[2] == '?') && full[3] == '\\' {
			out = append(out, full...) // device path
		} else { // \\server\share\x -> \\?\UNC\server\share\x
			out = append(out, '\\', '\\', '?', '\\', 'U', 'N', 'C', '\\')
			out = append(out, full[2:]...)
		}
	case len(full) >= 3 && full[1] == ':' && full[2] == '\\':
		out = append(out, '\\', '\\', '?', '\\')
		out = append(out, full...)
	default:
		out = append(out, full...)
	}
	out = append(out, 0)
	s.in = out
	return out, 0
}

// winPattern returns dir + "\*" (NUL-terminated) in s.pat. dir is
// NUL-terminated.
func (s *winScratch) winPattern(dir []uint16) []uint16 {
	d := dir[:len(dir)-1]
	pat := append(s.pat[:0], d...)
	if len(pat) > 0 && !winIsSep(pat[len(pat)-1]) {
		pat = append(pat, '\\')
	}
	pat = append(pat, '*', 0)
	s.pat = pat
	return pat
}

func winCloseHandle(h uintptr) { syscall.SyscallN(pCloseHandle, h) }

func winFindClose(h uintptr) { syscall.SyscallN(pFindClose, h) }

// winPathErr wraps an errno the way package os does, so errors.Is(err,
// os.ErrNotExist) and friends work.
func winPathErr(op, path string, e syscall.Errno) error {
	return &os.PathError{Op: op, Path: path, Err: e}
}

// winIsExecName reports whether name (UTF-16, no NUL) has an executable
// extension: .exe .bat .cmd .com .ps1 (ASCII case-insensitive).
func winIsExecName(name []uint16) bool {
	n := len(name)
	if n < 5 || name[n-4] != '.' {
		return false
	}
	c1, c2, c3 := winLower(name[n-3]), winLower(name[n-2]), winLower(name[n-1])
	switch {
	case c1 == 'e' && c2 == 'x' && c3 == 'e':
	case c1 == 'b' && c2 == 'a' && c3 == 't':
	case c1 == 'c' && c2 == 'm' && c3 == 'd':
	case c1 == 'c' && c2 == 'o' && c3 == 'm':
	case c1 == 'p' && c2 == 's' && c3 == '1':
	default:
		return false
	}
	return true
}

func winLower(c uint16) uint16 {
	if c >= 'A' && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}

const (
	winFiletimeEpochDiff = 116444736000000000 // 100 ns ticks between 1601-01-01 and 1970-01-01
	winMaxInt64          = 1<<63 - 1
	winMinInt64          = -1 << 63
)

// winUnixNanos converts a FILETIME (100 ns ticks since 1601) to nanoseconds
// since the Unix epoch, saturating at the int64 range.
func winUnixNanos(ft uint64) int64 {
	t := int64(ft) - winFiletimeEpochDiff
	if t > winMaxInt64/100 {
		return winMaxInt64
	}
	if t < winMinInt64/100 {
		return winMinInt64
	}
	return t * 100
}

// winAddEntry appends one directory entry to l. name is UTF-16 without NUL.
// tag is the reparse tag (valid when attr has the reparse-point bit). It
// skips "." and "..".
func winAddEntry(l *Listing, name []uint16, attr, tag uint32, size int64, ftWrite uint64) {
	if len(name) <= 2 && len(name) > 0 && name[0] == '.' && (len(name) == 1 || name[1] == '.') {
		return
	}
	var kind Kind
	var flags uint8
	isDir := attr&winAttrDirectory != 0
	switch {
	case attr&winAttrReparsePoint != 0 && (tag == winReparseSymlink || tag == winReparseMountPoint):
		kind = KindSymlink
		if isDir {
			flags |= FlagSymlinkDir
		}
	case isDir:
		kind = KindDir
	default:
		kind = KindFile
	}
	if isDir {
		size = 0
	} else if winIsExecName(name) {
		flags |= FlagExecutable
	}
	if attr&winAttrHidden != 0 {
		flags |= FlagHidden
	}
	if attr&winAttrReadOnly != 0 {
		flags |= FlagReadOnly
	}
	off := len(l.Names)
	l.Names = winAppendUTF8(l.Names, name)
	l.Entries = append(l.Entries, Entry{
		Size:    size,
		MTime:   winUnixNanos(ftWrite),
		NameOff: uint32(off),
		NameLen: uint16(len(l.Names) - off),
		Kind:    kind,
		Flags:   flags,
	})
}
