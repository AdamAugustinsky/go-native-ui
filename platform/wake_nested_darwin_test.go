//go:build darwin

package platform

import (
	"testing"
	"time"

	"gonativeui/internal/objc"
)

// A wake that the run loop finds outside the app's own wait is one found in a
// loop of AppKit's (a correction panel, a control's tracking), which the
// app's loop is suspended in: the window is marked as in a nested loop at
// once, so that the next tick draws what the wake brought, where a wake found
// in the app's pump leaves the window to the pump.
func TestWakeInANestedLoopMarksTheWindow(t *testing.T) {
	var opened bool
	var inPump, inLoop bool
	onMain(func() {
		w, err := Open(Options{Title: "nested wake", Width: 300, Height: 200, Hidden: true})
		if err != nil {
			return
		}
		defer w.Close()
		opened = true
		pumpFor(w, 100*time.Millisecond)

		// The wake arrives while the app's pump waits.
		w.Events()
		Wake()
		for end := time.Now().Add(time.Second); time.Now().Before(end) && len(w.q.ev) == 0; {
			Pump(int64(20 * time.Millisecond))
		}
		inPump = w.nested
		w.Events()
		pumpFor(w, 50*time.Millisecond)

		// The wake arrives while the main thread runs a nested loop of its own.
		Wake()
		app, mode := appObj.app, objc.Str("kCFRunLoopDefaultMode")
		selDate, clsDate := objc.Sel("dateWithTimeIntervalSinceNow:"), objc.Cls("NSDate")
		for end := time.Now().Add(time.Second); time.Now().Before(end) && !w.nested; {
			until := objc.ID(objc.F.Reset().Arg(uintptr(clsDate)).Arg(uintptr(selDate)).ArgF64(0.02).Call(objc.MsgSend).R0())
			if ev := objc.Send4(app, selNextEvent, ^uintptr(0), uintptr(until), uintptr(mode), 1); ev != 0 {
				objc.Send1(app, selSendEvent, uintptr(ev))
			}
		}
		inLoop = w.nested
		w.Events()
	})
	if !opened {
		t.Skip("no window (a locked screen)")
	}
	if inPump {
		t.Error("a wake taken by the app's own pump marked the window as in a nested loop")
	}
	if !inLoop {
		t.Error("a wake taken in a nested loop did not mark the window as in one")
	}
}
