//go:build darwin

package live

import (
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/internal/objc"
	"gonativeui/platform"
	"gonativeui/widget/widgettest"
)

// scriptedNestedLoop runs a nested event loop on the main thread for d, as
// AppKit's own do (NSCorrectionPanel's runs until the next key): it takes
// events with nextEventMatchingMask and sends them, and never returns to the
// app's loop. every is called between events with the time since it began.
func scriptedNestedLoop(d time.Duration, every func(el time.Duration)) {
	app_ := widgettest.NSApp()
	selNext := objc.Sel("nextEventMatchingMask:untilDate:inMode:dequeue:")
	selSend, selDate := objc.Sel("sendEvent:"), objc.Sel("dateWithTimeIntervalSinceNow:")
	clsDate, mode := objc.Cls("NSDate"), objc.Str("kCFRunLoopDefaultMode")
	start := time.Now()
	for el := time.Duration(0); el < d; el = time.Since(start) {
		every(el)
		until := objc.ID(objc.F.Reset().Arg(uintptr(clsDate)).Arg(uintptr(selDate)).ArgF64(0.02).Call(objc.MsgSend).R0())
		if ev := objc.Send4(app_, selNext, ^uintptr(0), uintptr(until), uintptr(mode), 1); ev != 0 {
			objc.Send1(app_, selSend, uintptr(ev))
		}
	}
}

// A worker's result that arrives while AppKit runs a nested loop of its own
// (the correction panel, a tracking loop) is taken by a frame drawn there,
// not when the loop ends: the wake the worker posts is one the nested loop
// may swallow, and the display link's ticks collect the owed work. The loop
// here is scripted; the result comes from another goroutine, with
// platform.Wake as a task pool's does.
func TestWorkerResultInANestedLoopIsTakenThere(t *testing.T) {
	p := &mwPane{results: make(chan int, 16)}
	l := widgettest.StartLive(t, mwConfig("nested wake"), p)
	l.Frames(5)
	settle()
	const sendAt, loopFor = 300 * time.Millisecond, 2500 * time.Millisecond
	var sentAt, tookAt time.Time
	var got0, got1, builds0, builds1 int64
	var visible bool
	l.Do(func(a *app.App) {
		if visible = a.Win.DebugState().Visible; !visible {
			return
		}
		got0, builds0 = p.got.Load(), p.builds.Load()
		sent := false
		scriptedNestedLoop(loopFor, func(el time.Duration) {
			switch {
			case !sent && el >= sendAt:
				sent = true
				sentAt = time.Now()
				go func() {
					p.results <- 1
					platform.Wake()
				}()
			case sent && tookAt.IsZero() && p.got.Load() > got0:
				tookAt = time.Now()
			}
		})
		got1, builds1 = p.got.Load(), p.builds.Load()
	})
	if !visible {
		t.Skip("the window is not visible (a locked screen): its display link does not tick")
	}
	if tookAt.IsZero() {
		t.Fatalf("the result was not taken during the %v nested loop (taken after it: %d of 1; %d frames built inside)", loopFor, got1-got0, builds1-builds0)
	}
	if d := tookAt.Sub(sentAt); d > 600*time.Millisecond {
		t.Errorf("the result was taken %v after it arrived in the nested loop, want within 600 ms", d)
	}
	if nestedBuilds.Load() != 0 {
		t.Errorf("%d Builds ran inside a Build", nestedBuilds.Load())
	}
}
