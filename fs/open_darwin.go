//go:build darwin

package fs

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
)

// openPath opens path through Launch Services (LSOpenCFURLRef), on whatever
// goroutine calls it. NSWorkspace through internal/objc would tie it to the
// main thread: objc's helpers share one call frame and Cls/Sel are not
// goroutine-safe, and an off-thread message send also needs an autorelease
// pool. Launch Services is a plain C API on CoreFoundation objects that we
// create and release ourselves, thread-safe by Apple's documentation, with
// a call Frame of our own per call. (LSOpenCFURLRef is deprecated in favour
// of NSWorkspace but present on every supported system; it opens a folder
// in Finder and a document in its default app, like openURL:.)
func openPath(path string) error {
	fi, err := os.Stat(path) // may block on a dead mount: we are on a worker
	if err != nil {
		return err
	}
	ls := &lsym
	ls.once.Do(ls.load)
	if ls.err != nil {
		return ls.err
	}
	b := make([]byte, len(path)+1)
	copy(b, path)
	fr := new(ffi.Frame) // heap: a Frame must not live on a stack
	url := fr.Reset().Arg(0).ArgPtr(unsafe.Pointer(&b[0])).Arg(uintptr(len(path))).ArgBool(fi.IsDir()).Call(ls.urlFromFS).R0()
	runtime.KeepAlive(b) // the frame holds only the address; CF copies the bytes during the call
	if url == 0 {
		return errors.New("fs: cannot make a file URL for " + path)
	}
	// OSStatus is a 32-bit result in w0/eax; take its low 32 bits as signed.
	status := int32(fr.Reset().Arg(url).Arg(0).Call(ls.open).R0())
	fr.Call1(ls.release, url)
	if status != 0 {
		return fmt.Errorf("fs: the system could not open %s (Launch Services error %d)", path, status)
	}
	return nil
}

type lsSyms struct {
	once                     sync.Once
	err                      error
	urlFromFS, release, open uintptr
}

var lsym lsSyms

func (ls *lsSyms) load() {
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		ls.err = err
		return
	}
	cs, err := purego.Dlopen("/System/Library/Frameworks/CoreServices.framework/CoreServices", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		ls.err = err
		return
	}
	sym := func(h uintptr, n string) uintptr {
		p, err := purego.Dlsym(h, n)
		if err != nil && ls.err == nil {
			ls.err = err
		}
		return p
	}
	ls.urlFromFS = sym(cf, "CFURLCreateFromFileSystemRepresentation")
	ls.release = sym(cf, "CFRelease")
	ls.open = sym(cs, "LSOpenCFURLRef")
}
