//go:build darwin

package platform

import (
	"gonativeui/internal/objc"
)

// The application's run loop: waiting for events and turning them into the
// platform's events (the event pump), and waking it from other threads.

var (
	selNextEvent    = objc.SelZ("nextEventMatchingMask:untilDate:inMode:dequeue:\x00")
	selSendEvent    = objc.SelZ("sendEvent:\x00")
	selDateSinceNow = objc.SelZ("dateWithTimeIntervalSinceNow:\x00")
	selType         = objc.SelZ("type\x00")
	selSubtype      = objc.SelZ("subtype\x00")
	selTimestamp    = objc.SelZ("timestamp\x00")
)

// Wait blocks until at least one event, a wake, a frame slot or the timeout,
// and returns the window's events. It is the app-wide Pump followed by
// Events: the events of the app's other windows wait in their own queues.
// The slice is valid until the next Wait, Pump, Events or Pending.
func (w *Window) Wait(timeoutNs int64) []Event {
	Pump(timeoutNs)
	return w.Events()
}

// handle is one event of this window (route): queued as the app's input, and
// sent on to AppKit.
func (w *Window) handle(ev objc.ID) {
	keyIdx := -1 // index in the queue of the EvKey of this key-down event
	typ := uintptr(objc.Send(ev, selType))
	t := int64(objc.SendF64(ev, selTimestamp) * 1e9)
	if w.dialogOpen() {
		// A sheet owns the window's input (dialog.go): its keys and clicks go
		// to AppKit only, not to the app behind it.
		switch typ {
		case 1, 2, 3, 4, 5, 6, 7, 10, 11, 18, 22, 25, 26, 27, 30, 32:
			prev := sending
			sending = w
			objc.Send1(w.app, selSendEvent, uintptr(ev))
			sending = prev
			return
		}
	}
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
	case 18, 30, 32: // rotate, magnify, smart magnify (the trackpad's gestures)
		if w.gestureEvent(ev, typ, t) {
			return
		}
	case 12: // flagsChanged: a modifier key on its own
		w.modifiersEvent(ev, t)
	}
	// The key reaches the view's keyDown: through interpretKeyEvents: the input
	// method sees it first. A key it consumed (composition keys, the key that
	// commits) is not a key press for the app; one it hands back as a command
	// (an arrow or Enter with nothing composing, doCommandBySelector:) is.
	markedBefore := len(w.marked) > 0
	w.imeUsed, w.cmdCalled, w.menuActed, w.editTaken = false, false, false, false
	// A key the focused text field takes is not a key equivalent: the menu
	// items validate as disabled while AppKit matches it (validate).
	w.fieldKey = keyIdx >= 0 && w.fieldKeys != nil && w.fieldKeys(&w.q.ev[keyIdx])
	prev := sending
	sending = w
	objc.Send1(w.app, selSendEvent, uintptr(ev))
	sending = prev
	w.hosts.scrollDelivered()
	w.fieldKey = false
	if w.menuActed && keyIdx >= 0 && keyIdx < len(w.q.ev) && w.q.ev[keyIdx].Kind == EvKey && w.q.ev[keyIdx].Time == t {
		// The key was a menu item's key equivalent (performKeyEquivalent:): the
		// item delivered its command, so the key is not the app's as well. (A
		// frame handler that ran inside sendEvent, during a menu's tracking,
		// may have taken the queue: then the key is gone already.)
		w.q.ev = append(w.q.ev[:keyIdx], w.q.ev[keyIdx+1:]...)
		return
	}
	if w.editTaken && keyIdx >= 0 && keyIdx < len(w.q.ev) && w.q.ev[keyIdx].Kind == EvKey && w.q.ev[keyIdx].Time == t {
		// The key bindings made the key an edit command (EvEdit): the command
		// is the app's, the key is not as well.
		w.q.ev = append(w.q.ev[:keyIdx], w.q.ev[keyIdx+1:]...)
		return
	}
	w.dropConsumedKey(keyIdx, markedBefore)
}

// sending is the window whose event handle is sending to AppKit, nil outside
// one: a menu item that acts or validates meanwhile does so for that event's
// key (menuActed, fieldKey).
var sending *Window

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
	w.frameOut = false
	return w.q.takePending()
}
