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

// ErrUnsupported is returned by backends that are not implemented yet.
var ErrUnsupported = errors.New("fs: not supported on this OS yet")

// listDir lists path into l (appending to its reset buffers), without
// sorting. Implemented per OS: getattrlistbulk (darwin), FindFirstFileExW
// or GetFileInformationByHandleEx (windows, chosen by measurement),
// getdents64 + statx (linux). It must not allocate per entry once l's
// buffers have grown, and must be safe to call from any goroutine.
//
// var listDir func(path string, l *Listing) error

// watcher is the per-OS change notifier (FSEvents, ReadDirectoryChangesW,
// inotify). It runs its own goroutine or OS callback thread, calls wake
// when it has changes, and hands them over through drain on the UI
// thread. Implemented per OS:
//
//	newWatcher(wake func()) (watcher, error)
//
//	type watcher interface {
//		add(id uint32, path string) error  // id is the caller's handle for the directory
//		remove(id uint32)
//		drain(dst []Change) []Change       // UI thread; zero-alloc with a reused dst
//		close()
//	}

// Change is one coalesced watch notification for the directory with the
// caller's id.
type Change struct {
	ID   uint32
	Kind ChangeKind
}
