//go:build darwin

package platform

import (
	"os"
	"runtime"
	"testing"
)

// AppKit creates windows on the main thread only. The test binary's main
// goroutine is locked to it here; the tests run on another goroutine and hand
// the AppKit work they need on the main thread to onMain.

var mainQ = make(chan func())

func init() { runtime.LockOSThread() }

func TestMain(m *testing.M) {
	done := make(chan int, 1)
	go func() { done <- m.Run() }()
	for {
		select {
		case f := <-mainQ:
			f()
		case code := <-done:
			os.Exit(code)
		}
	}
}

// onMain runs f on the main thread and waits for it. f must not call
// t.Skip or t.Fatal (they end the goroutine they run on).
func onMain(f func()) {
	d := make(chan struct{})
	mainQ <- func() { defer close(d); f() }
	<-d
}
