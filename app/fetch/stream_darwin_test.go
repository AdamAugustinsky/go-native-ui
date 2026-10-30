//go:build darwin

package fetch

import (
	"runtime"
	"testing"
	"time"
	"unsafe"

	"gonativeui/internal/cf"
	"gonativeui/internal/nsurl"
)

func TestUploadStreamSignalsSpace(t *testing.T) {
	if err := load(); err != nil {
		t.Fatal(err)
	}
	fr := cf.Acquire()
	defer cf.Release(fr)
	in, out := nsurl.BoundPair(fr, 16384)
	if in == 0 || out == 0 {
		t.Fatal("no bound stream")
	}
	defer cf.CFRelease(fr, in)
	defer cf.CFRelease(fr, out)
	u, err := watchUpload(fr, out)
	if err != nil {
		t.Fatal(err)
	}
	defer u.close(fr)

	core := cf.CoreFoundation()
	open := cf.Sym(core, "CFReadStreamOpen")
	read := cf.Sym(core, "CFReadStreamRead")
	closeRead := cf.Sym(core, "CFReadStreamClose")
	if fr.Call1(open, in) == 0 {
		t.Fatal("cannot open reader")
	}
	defer fr.Call1(closeRead, in)
	buf := make([]byte, 65536)
	u.submit(fr, buf)
	// Four reads must make room for the entire submission, and its completion
	// notification must arrive without a timer retry or a session delegate.
	received := 0
	readBuf := make([]byte, 16384)
	for received < len(buf) {
		n := int(fr.Reset().Arg(in).ArgPtr(unsafe.Pointer(&readBuf[0])).Arg(uintptr(16384)).Call(read).R0())
		if n <= 0 {
			t.Fatal("read failed")
		}
		received += n
	}
	select {
	case <-u.wake:
		if got := u.sent.Load(); got != int64(len(buf)) {
			t.Errorf("wrote %d bytes", got)
		}
	case <-time.After(time.Second):
		t.Fatal("draining the stream did not complete the submission")
	}

	runtime.KeepAlive(buf)
	runtime.KeepAlive(readBuf)
}
