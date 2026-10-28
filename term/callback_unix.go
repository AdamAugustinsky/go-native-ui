//go:build (darwin && arm64) || (linux && (arm64 || amd64))

package term

import "gonativeui/internal/ffi"

// newCallbacks makes the C entry points of the terminal callbacks, once per
// process, through internal/ffi's callback table (D3).
func newCallbacks(c *callbacks) error {
	c.writePty = ffi.NewCallback(func(cb *ffi.CB) { onWritePty(cb.Int(1), cb.Int(2), cb.Int(3)) })
	c.bell = ffi.NewCallback(func(cb *ffi.CB) { onBell(cb.Int(1)) })
	c.title = ffi.NewCallback(func(cb *ffi.CB) { onTitle(cb.Int(1)) })
	c.clipboard = ffi.NewCallback(func(cb *ffi.CB) { onClipboard(cb.Int(1), cb.Int(2)) })
	c.hold = ffi.NewCallback(func(cb *ffi.CB) { onRenderHold(cb.Int(1), cb.Int(2)&0xff != 0) })
	return nil
}
