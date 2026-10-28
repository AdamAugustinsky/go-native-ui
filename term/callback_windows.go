//go:build windows && (amd64 || arm64)

package term

import "syscall"

// newCallbacks makes the C entry points of the terminal callbacks, once per
// process, with the standard library's syscall.NewCallback (every argument
// is an integer or a pointer, as it requires).
func newCallbacks(c *callbacks) error {
	c.writePty = syscall.NewCallback(func(_, ud, data, n uintptr) uintptr { onWritePty(ud, data, n); return 0 })
	c.bell = syscall.NewCallback(func(_, ud uintptr) uintptr { onBell(ud); return 0 })
	c.title = syscall.NewCallback(func(_, ud uintptr) uintptr { onTitle(ud); return 0 })
	c.clipboard = syscall.NewCallback(func(_, ud, w uintptr) uintptr { onClipboard(ud, w); return 0 })
	c.hold = syscall.NewCallback(func(_, ud, held uintptr) uintptr { onRenderHold(ud, held&0xff != 0); return 0 })
	return nil
}
