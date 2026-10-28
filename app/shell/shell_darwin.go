//go:build darwin

package shell

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"unsafe"

	"gonativeui/fs"
	"gonativeui/internal/cf"
)

// macOS, from whatever worker goroutine calls: every call has a frame of its
// own (internal/cf) and, where it sends Objective-C messages, an autorelease
// pool. Launch Services and NSFileManager's shared instance are thread-safe;
// NSWorkspace's activateFileViewerSelectingURLs: is documented without a
// thread restriction and is used here the same way.

type osSystem struct{}

type macSyms struct {
	once sync.Once
	err  error

	lsOpen uintptr
}

var mac macSyms

func (m *macSyms) load() {
	if m.err = cf.Init(); m.err != nil {
		return
	}
	cs, err := cf.Framework("CoreServices")
	if err != nil {
		m.err = err
		return
	}
	if _, err := cf.Framework("AppKit"); err != nil { // NSWorkspace
		m.err = err
		return
	}
	if m.lsOpen = cf.Sym(cs, "LSOpenCFURLRef"); m.lsOpen == 0 {
		m.err = cf.ErrUnavailable
	}
}

func ready() error {
	mac.once.Do(mac.load)
	if mac.err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, mac.err)
	}
	return nil
}

func (osSystem) openURL(u string) error {
	if err := ready(); err != nil {
		return err
	}
	fr := cf.Acquire()
	defer cf.Release(fr)
	url := cf.URL(fr, u)
	if url == 0 {
		return fmt.Errorf("%w: the system cannot make a URL of %q", ErrInvalid, u)
	}
	defer cf.CFRelease(fr, url)
	// OSStatus: the low 32 bits of the result, signed.
	if st := int32(fr.Reset().Arg(url).Arg(0).Call(mac.lsOpen).R0()); st != 0 {
		return fmt.Errorf("shell: the system could not open %s (Launch Services error %d)", u, st)
	}
	return nil
}

// openPath is fs.Open: Launch Services on a file URL, shared with the file
// browser's own Open.
func (osSystem) openPath(p string) error { return fs.Open(p) }

func (osSystem) reveal(paths []string) error {
	if err := ready(); err != nil {
		return err
	}
	fr := cf.Acquire()
	defer cf.Release(fr)
	defer cf.Pool(fr)()

	nsurl, fileURL := cf.Cls(fr, "NSURL"), cf.Sel(fr, "fileURLWithPath:")
	urls := make([]uintptr, 0, len(paths))
	for _, p := range paths {
		s := cf.String(fr, p)
		u := cf.Msg(fr, nsurl, fileURL, s) // autoreleased
		cf.CFRelease(fr, s)
		if u == 0 {
			return fmt.Errorf("%w: the system cannot make a URL of %q", ErrInvalid, p)
		}
		urls = append(urls, u)
	}
	arr := cf.Array(fr, urls...) // does not retain: the pool owns the URLs
	defer cf.CFRelease(fr, arr)
	ws := cf.Msg(fr, cf.Cls(fr, "NSWorkspace"), cf.Sel(fr, "sharedWorkspace"))
	if ws == 0 {
		return ErrUnavailable
	}
	cf.Msg(fr, ws, cf.Sel(fr, "activateFileViewerSelectingURLs:"), arr)
	return nil
}

// NSError codes (Foundation/FoundationErrors.h) that have a Go equivalent.
const (
	nsFileNoSuchFileError         = 4
	nsFileReadNoSuchFileError     = 260
	nsFileWriteNoPermissionError  = 513
	nsFileWriteVolumeReadOnlyCode = 642
)

// trashURL asks NSFileManager to move the item at p to the Trash and returns
// the path it has there.
func trashURL(p string) (string, error) {
	fr := cf.Acquire()
	defer cf.Release(fr)
	defer cf.Pool(fr)()

	s := cf.String(fr, p)
	url := cf.Msg(fr, cf.Cls(fr, "NSURL"), cf.Sel(fr, "fileURLWithPath:"), s)
	cf.CFRelease(fr, s)
	fm := cf.Msg(fr, cf.Cls(fr, "NSFileManager"), cf.Sel(fr, "defaultManager"))
	if url == 0 || fm == 0 {
		return "", ErrUnavailable
	}
	// The resulting URL and the error are two words of the frame's scratch.
	scratch := fr.Scratch()
	scratch[0], scratch[1] = 0, 0
	resulting, out := unsafe.Pointer(&scratch[0]), unsafe.Pointer(&scratch[1])
	if cf.MsgBool(fr, fm, cf.Sel(fr, "trashItemAtURL:resultingItemURL:error:"), url, uintptr(resulting), uintptr(out)) {
		if u := scratch[0]; u != 0 { // autoreleased, valid until the pool pops
			if path := cf.GoString(fr, cf.Msg(fr, u, cf.Sel(fr, "path"))); path != "" {
				return path, nil
			}
		}
		return "", errors.New("the system did not say where the file went")
	}
	nserr := scratch[1]
	if nserr == 0 {
		return "", fmt.Errorf("the system refused without saying why")
	}
	code := int(cf.Msg(fr, nserr, cf.Sel(fr, "code")))
	desc := cf.GoString(fr, cf.Msg(fr, nserr, cf.Sel(fr, "localizedDescription")))
	switch code {
	case nsFileNoSuchFileError, nsFileReadNoSuchFileError:
		return "", os.ErrNotExist
	case nsFileWriteNoPermissionError, nsFileWriteVolumeReadOnlyCode:
		return "", os.ErrPermission
	}
	return "", fmt.Errorf("%s (Cocoa error %d)", desc, code)
}
