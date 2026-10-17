//go:build darwin

package fs

import (
	"errors"
	"os"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/objc"
)

var openSel struct {
	once                                       sync.Once
	err                                        error
	nsString, nsURL, workspace                 objc.ID
	stringUTF8, fileURL, sharedSel, openURLSel objc.SEL
}

// openPath asks NSWorkspace to open path. It must run on the main thread
// (internal/objc's shared call frame).
func openPath(path string) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	o := &openSel
	o.once.Do(func() {
		if _, err := purego.Dlopen("/System/Library/Frameworks/AppKit.framework/AppKit", purego.RTLD_NOW|purego.RTLD_GLOBAL); err != nil {
			o.err = err
			return
		}
		o.nsString, o.nsURL, o.workspace = objc.Cls("NSString"), objc.Cls("NSURL"), objc.Cls("NSWorkspace")
		o.stringUTF8, o.fileURL = objc.Sel("stringWithUTF8String:"), objc.Sel("fileURLWithPath:")
		o.sharedSel, o.openURLSel = objc.Sel("sharedWorkspace"), objc.Sel("openURL:")
	})
	if o.err != nil {
		return o.err
	}
	b := make([]byte, len(path)+1)
	copy(b, path)
	str := objc.Send1(o.nsString, o.stringUTF8, uintptr(unsafe.Pointer(&b[0])))
	runtime.KeepAlive(b)
	url := objc.Send1(o.nsURL, o.fileURL, uintptr(str))
	if url == 0 {
		return errors.New("fs: cannot make a file URL for " + path)
	}
	if !objc.SendBool1(objc.Send(o.workspace, o.sharedSel), o.openURLSel, uintptr(url)) {
		return errors.New("fs: the system could not open " + path)
	}
	return nil
}
