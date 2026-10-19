//go:build darwin

package platform

import (
	"runtime"
	"sync"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// The application's run loop: waiting for events and turning them into the
// platform's events (the event pump), and waking it from other threads.

var (
	selNextEvent    = objc.Sel("nextEventMatchingMask:untilDate:inMode:dequeue:")
	selSendEvent    = objc.Sel("sendEvent:")
	selDateSinceNow = objc.Sel("dateWithTimeIntervalSinceNow:")
	selType         = objc.Sel("type")
	selSubtype      = objc.Sel("subtype")
	selTimestamp    = objc.Sel("timestamp")
)

// Wait blocks until at least one event, a wake, a frame slot or the timeout.
func (w *Window) Wait(timeoutNs int64) []Event {
	if w.returned {
		w.q.dropFirst(w.nOut)
		w.returned = false
	}
	F := objc.F
	pool := F.Call0(objc.PoolPush)
	until := w.distantFuture
	switch {
	case len(w.q.ev) > 0 || timeoutNs == 0:
		until = w.distantPast
	case timeoutNs > 0:
		until = objc.ID(F.Reset().Arg(uintptr(clsNSDate)).Arg(uintptr(selDateSinceNow)).
			ArgF64(float64(timeoutNs) / 1e9).Call(objc.MsgSend).R0())
	}
	if w.live && !objc.SendBool(w.win, selInLiveResize) {
		w.exitLiveResize() // windowDidEndLiveResize: never arrived (or the resize ended inside a nested loop)
	}
	for ev := w.nextEvent(until); ev != 0; ev = w.nextEvent(w.distantPast) {
		w.handle(ev)
	}
	F.Call1(objc.PoolPop, pool)
	w.returned, w.nOut = true, len(w.q.ev)
	return w.q.ev
}

func (w *Window) nextEvent(until objc.ID) objc.ID {
	return objc.Send4(w.app, selNextEvent, ^uintptr(0), uintptr(until), uintptr(defaultMode), 1)
}

func (w *Window) handle(ev objc.ID) {
	keyIdx := -1 // index in the queue of the EvKey of this key-down event
	typ := uintptr(objc.Send(ev, selType))
	if typ == 15 && uintptr(objc.Send(ev, selSubtype))&0xFFFF == wakeSubtype {
		if takeWake() {
			w.q.push(Event{Kind: EvWake, Time: Now()})
		}
		return // also used to unblock the loop after a display-link tick
	}
	t := int64(objc.SendF64(ev, selTimestamp) * 1e9)
	switch typ {
	case 1, 2, 3, 4, 5, 6, 7, 25, 26, 27: // mouse down/up/moved/dragged
		if w.pointerEvent(ev, typ, t) {
			return
		}
	case 8: // mouse entered (the tracking area)
		w.pointerIn = true
	case 9: // mouse exited
		w.pointerIn = false
		w.q.push(Event{Kind: EvPointerLeave, Time: t})
	case 22: // scroll wheel
		w.scrollEvent(ev, t)
	case 10, 11: // key down / up
		keyIdx = w.keyEvent(ev, typ, t)
	}
	// The key reaches the view's keyDown: through interpretKeyEvents: the input
	// method sees it first. A key it consumed (composition keys, the key that
	// commits) is not a key press for the app; one it hands back as a command
	// (an arrow or Enter with nothing composing, doCommandBySelector:) is.
	markedBefore := len(w.marked) > 0
	w.imeUsed, w.cmdCalled = false, false
	objc.Send1(w.app, selSendEvent, uintptr(ev))
	w.dropConsumedKey(keyIdx, markedBefore)
}

// Text returns the UTF-8 bytes of an EvText event.
func (w *Window) Text(r TextRef) []byte { return w.q.textOf(r) }

// Pending returns and clears the events queued so far without pumping the
// OS, so it is safe to call from inside the frame handler. The slice is valid
// until the next Pending or Wait call.
func (w *Window) Pending() []Event {
	if w.returned {
		w.q.dropFirst(w.nOut)
		w.returned = false
	}
	return w.q.takePending()
}

var (
	wakeMu    sync.Mutex
	wakeFrame = new(ffi.Frame)
)

// osWake posts the reusable application-defined event; postEvent:atStart:
// may be called from any thread.
func osWake() {
	w := theWindow.Load()
	if w == nil {
		return
	}
	// Autorelease pools are per thread: stay on one OS thread from push to pop.
	runtime.LockOSThread()
	wakeMu.Lock()
	pool := wakeFrame.Call0(objc.PoolPush)
	wakeFrame.Call4(objc.MsgSend, uintptr(w.app), uintptr(selPostEvent), uintptr(w.wakeEv), 0)
	wakeFrame.Call1(objc.PoolPop, pool)
	wakeMu.Unlock()
	runtime.UnlockOSThread()
}
