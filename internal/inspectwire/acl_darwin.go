//go:build darwin

package inspectwire

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
)

// Extended ACLs on macOS (<sys/acl.h>), through libSystem and internal/ffi: no
// cgo. An owned 0755 directory can carry an inheritable ACL that grants another
// user write access; what is created in it inherits the entry, and chmod 0600
// does not take it away.

const aclTypeExtended = 0x100 // ACL_TYPE_EXTENDED

var (
	aclMu                                             sync.Mutex
	aclFr                                             = new(ffi.Frame)
	fnGetLink, fnSetLink, fnInit, fnFree, fnErrnoAddr uintptr
	aclErr                                            error
)

func init() {
	lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		aclErr = err
		return
	}
	for name, p := range map[string]*uintptr{
		"acl_get_link_np": &fnGetLink, "acl_set_link_np": &fnSetLink, "acl_init": &fnInit, "acl_free": &fnFree, "__error": &fnErrnoAddr,
	} {
		if *p, err = purego.Dlsym(lib, name); err != nil {
			aclErr = fmt.Errorf("libSystem has no %s: %w", name, err)
			return
		}
	}
}

// errnoNow reads the calling thread's errno (the caller holds the thread).
func errnoNow() syscall.Errno {
	addr := aclFr.Call0(fnErrnoAddr) // __error(): the address of the thread's errno, in C memory
	return syscall.Errno(*(*int32)(*(*unsafe.Pointer)(unsafe.Pointer(&addr))))
}

func cpath(path string) []byte { return append([]byte(path), 0) }

// hasACL reports whether path (not followed, if it is a link) has an extended
// ACL. A failure to read it is an error, never "no".
func hasACL(path string) (bool, error) {
	if aclErr != nil {
		return false, aclErr
	}
	if _, err := os.Lstat(path); err != nil { // acl_get_* says ENOENT for a path that is missing as for one with no ACL
		return false, err
	}
	aclMu.Lock()
	defer aclMu.Unlock()
	runtime.LockOSThread() // errno is the thread's: both calls on one
	defer runtime.UnlockOSThread()
	p := cpath(path)
	acl := aclFr.Call2(fnGetLink, uintptr(unsafe.Pointer(&p[0])), aclTypeExtended)
	runtime.KeepAlive(p)
	if acl != 0 {
		aclFr.Call1(fnFree, acl)
		return true, nil
	}
	errno := errnoNow()
	if errno == syscall.ENOENT { // acl_get_*: no ACL
		return false, nil
	}
	return false, errno
}

// stripACL removes the extended ACL of path, if it has one, by setting an empty
// one.
func stripACL(path string) error {
	has, err := hasACL(path)
	if err != nil || !has {
		return err
	}
	aclMu.Lock()
	defer aclMu.Unlock()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	empty := aclFr.Call1(fnInit, 0)
	if empty == 0 {
		return errors.New("acl_init failed")
	}
	defer aclFr.Call1(fnFree, empty)
	p := cpath(path)
	if r := int32(aclFr.Call3(fnSetLink, uintptr(unsafe.Pointer(&p[0])), aclTypeExtended, empty)); r != 0 {
		return errnoNow()
	}
	runtime.KeepAlive(p)
	return nil
}

// dirHasACL is hasACL for the socket's directory: any extended ACL refuses it.
func dirHasACL(dir string) (bool, error) { return hasACL(dir) }
