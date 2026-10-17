// Package fs lists and watches directories for the reference app
// (ARCHITECTURE §10, DECISIONS D12). Listing and watching run on worker
// goroutines; results reach the UI thread through a bounded ring and a
// wake call, so the frame loop never blocks on the file system.
//
// Storage is pointer-free: a Listing is one []Entry plus one []byte of
// names, and an Entry refers to its name by offset. Workers reuse
// Listings (no per-entry allocation, no per-listing allocation once the
// buffers are warm).
package fs

import "errors"

// Kind is the type of a directory entry.
type Kind uint8

const (
	KindFile Kind = iota
	KindDir
	KindSymlink
	KindOther // devices, sockets, FIFOs, ...
)

// Entry flags.
const (
	FlagHidden     uint8 = 1 << iota // dot-file on Unix; FILE_ATTRIBUTE_HIDDEN / UF_HIDDEN
	FlagExecutable                   // any execute bit (Unix); .exe/.bat/.cmd/.com (Windows)
	FlagSymlinkDir                   // a symlink whose target is a directory (when cheaply known)
	FlagReadOnly
)

// Entry is one directory entry: 24 bytes, pointer-free. Size is 0 for
// directories. MTime is nanoseconds since the Unix epoch.
type Entry struct {
	Size    int64
	MTime   int64
	NameOff uint32 // into Listing.Names
	NameLen uint16
	Kind    Kind
	Flags   uint8
}

// Listing is the result of listing one directory. Names holds every
// entry's UTF-8 name back to back (Windows names are converted from
// UTF-16 once, while listing). Order is a permutation of the entry
// indices in the requested sort order.
type Listing struct {
	Entries []Entry
	Names   []byte
	Order   []uint32
	Err     error // non-nil: the listing failed or is partial
}

// Name returns entry i's name. It aliases l.Names.
func (l *Listing) Name(i int) []byte {
	e := &l.Entries[i]
	return l.Names[e.NameOff : e.NameOff+uint32(e.NameLen)]
}

// Reset empties l, keeping its capacity.
func (l *Listing) Reset() {
	l.Entries, l.Names, l.Order, l.Err = l.Entries[:0], l.Names[:0], l.Order[:0], nil
}

// SortKey selects the sort column. Directories always come first.
type SortKey uint8

const (
	SortName SortKey = iota // natural order: "file2" < "file10", case-insensitive
	SortSize
	SortModified
	SortKind // by extension, then name
)

// ChangeKind classifies a watch notification.
type ChangeKind uint8

const (
	ChangeDir      ChangeKind = iota // something in the directory changed: relist it
	ChangeOverflow                   // events were lost: relist everything watched
	ChangeGone                       // the watched directory was deleted or moved away
)

// ErrTooLarge is reported (in Listing.Err, with a partial listing) when a
// directory does not fit the 32-bit offsets of the name arena, the entry
// indices or the sort keys.
var ErrTooLarge = errors.New("fs: directory too large for 32-bit offsets")

// ErrUnsupported is returned by backends that are not implemented yet.
var ErrUnsupported = errors.New("fs: not supported on this OS yet")

// listDir lists path into l (appending to its reset buffers), without
// sorting. Implemented per OS: getattrlistbulk (darwin), FindFirstFileExW
// or GetFileInformationByHandleEx (windows, chosen by measurement),
// getdents64 + statx (linux). It must not allocate per entry once l's
// buffers have grown, and must be safe to call from any goroutine.
//
// var listDir func(path string, l *Listing) error

// watcher is the per-OS change notifier (kqueue, ReadDirectoryChangesW,
// inotify; all non-recursive, D26). It runs its own goroutine or OS callback thread, calls wake
// when it has changes, and hands them over through drain. Implemented per
// OS:
//
//	newWatcher(wake func()) (watcher, error)
//
// The Service calls add, remove and close from one control goroutine (they
// may be slow: they open handles and register watches) and drain from the UI thread,
// so a backend must make drain safe against concurrent add/remove and
// against its own notification thread. wake may be called from any thread
// and must not block.
//
// Paths passed to add are what the caller listed: a backend resolves
// symlinks or case itself if its notification API needs it.
//
// Once remove(id) has returned, no change of the removed watch is delivered
// any more (drain may still return ones recorded earlier for other ids):
// the Service relies on this to tell a replaced watch's events from the new
// watch's.
type watcher interface {
	add(id uint32, path string) error // id is the caller's handle for the directory
	remove(id uint32)
	drain(dst []Change) []Change // appends to dst; zero-alloc with a reused dst
	close()
}

// openPath opens path with the OS default application (a folder in the
// file manager). Implemented per OS and exposed as Open and, for the UI,
// as Service.Open:
//
//	openPath(path string) error
//
// It is called on a goroutine of its own, never on the UI thread, so it may
// block (checking the path on a dead network mount, waiting for the shell).
// It may be called from several goroutines at once. On macOS it launches
// through Launch Services, which is safe off the main thread.

// Change is one coalesced watch notification for the directory with the
// caller's id.
type Change struct {
	ID   uint32
	Kind ChangeKind
}
