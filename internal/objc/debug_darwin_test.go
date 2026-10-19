//go:build darwin && gnuidebug

package objc

import (
	"runtime"
	"testing"
)

// go test -tags gnuidebug -run MainThreadCheck ./internal/objc (the other
// tests use F from a locked test goroutine, which this build refuses).
func TestMainThreadCheck(t *testing.T) {
	var onMainOK bool
	onMain(func() { onMainOK = Send(Cls("NSObject"), Sel("class")) != 0 })
	if !onMainOK {
		t.Error("a message on the main thread failed")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer func() {
		if recover() == nil {
			t.Error("F used off the main thread did not panic")
		}
	}()
	Send(Cls("NSObject"), Sel("class"))
}
