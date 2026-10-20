//go:build darwin

package instance

import (
	"errors"
	"net"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
)

var peerSym struct {
	once sync.Once
	fn   uintptr
	pool sync.Pool
}

// peerUID returns the user id of the process on the other end of a Unix-domain
// connection (getpeereid). ok is false where the system cannot say.
func peerUID(c net.Conn) (uid uint32, ok bool, err error) {
	uc, isUnix := c.(*net.UnixConn)
	if !isUnix {
		return 0, false, nil
	}
	peerSym.once.Do(func() {
		if h, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL); err == nil {
			peerSym.fn, _ = purego.Dlsym(h, "getpeereid")
		}
		peerSym.pool.New = func() any { return new(ffi.Frame) }
	})
	if peerSym.fn == 0 {
		return 0, true, errors.New("getpeereid is missing")
	}
	rc, err := uc.SyscallConn()
	if err != nil {
		return 0, true, err
	}
	fr := peerSym.pool.Get().(*ffi.Frame) // a Frame lives on the heap
	defer peerSym.pool.Put(fr)
	rv := int32(-1)
	if err := rc.Control(func(fd uintptr) {
		sc := fr.Scratch() // two words that live with the frame: uid and gid come back in them
		sc[0], sc[1] = 0, 0
		rv = int32(fr.Reset().Arg(fd).ArgPtr(unsafe.Pointer(&sc[0])).ArgPtr(unsafe.Pointer(&sc[1])).Call(peerSym.fn).R0())
		uid = uint32(sc[0])
	}); err != nil {
		return 0, true, err
	}
	if rv != 0 {
		return 0, true, errors.New("getpeereid failed")
	}
	return uid, true, nil
}
