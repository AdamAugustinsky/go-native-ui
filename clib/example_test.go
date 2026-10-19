package clib_test

import (
	"errors"
	"unsafe"

	"gonativeui/clib"
)

// zfErr is ZapFast's zf_err, laid out as in C:
//
//	typedef struct { int32_t code; uint32_t len; uint8_t msg[256]; } zf_err;
type zfErr struct {
	Code int32
	Len  uint32
	Msg  [256]byte
}

func (e *zfErr) toError() error { return errors.New(string(e.Msg[:e.Len])) }

// sender is one call site: its Call and the error storage it passes live
// together on the heap and are used by one goroutine at a time.
type sender struct {
	call *clib.Call
	err  *zfErr
	fn   clib.Func
	core unsafe.Pointer // zf_core *, from zf_core_open
}

// send calls int32_t zf_send(zf_core *c, const uint8_t *cmd, size_t len,
// zf_err *err) without allocating. If it fails, the error is already in
// s.err when Do returns, whichever thread the call ran on. Ptr keeps buf
// reachable until then, so no runtime.KeepAlive is needed.
func (s *sender) send(buf []byte) error {
	st := s.call.Reset().Ptr(s.core).Ptr(unsafe.Pointer(&buf[0])).Uint(uint64(len(buf))).
		Ptr(unsafe.Pointer(s.err)).Do(s.fn).Int32()
	if st != 0 {
		return s.err.toError() // allocates only on failure
	}
	return nil
}

// The call-site pattern for a library that returns errors through
// caller-owned storage.
func Example() {
	lib, err := clib.Open(clib.BundleFramework("libzapfast_core.dylib"))
	if err != nil {
		return // not bundled, or not supported on this OS
	}
	fn, err := lib.Func("zf_send")
	if err != nil {
		return
	}
	s := &sender{call: clib.NewCall(), err: new(zfErr), fn: fn}
	_ = s // s.core comes from zf_core_open; then s.send(cmd) per command
}
