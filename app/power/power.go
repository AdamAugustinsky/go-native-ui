// Package power keeps the machine awake while the app needs it, and tells the
// app when the machine sleeps and wakes.
//
//	a, err := power.Hold(power.PreventIdleSleep, "Call with Ana") // a call starts
//	defer a.Release()                                             // the call ends
//
//	func (c *chat) Start(a *app.App) error {
//		power.Observe() // also the screens' sleep and wake
//		...
//	}
//
//	func (c *chat) Event(a *app.App, e *app.Event) {
//		if ev, ok := power.EventOf(e); ok && ev == power.DidWake {
//			c.reconnect() // the network was cut while the machine slept
//		}
//		a.DefaultEvent(e)
//	}
//
// # Holds
//
// Hold takes a power assertion that stops the system from sleeping because
// the user is idle (or, with PreventDisplaySleep, the display from turning
// off) until it is released. It does not stop the user closing a laptop's
// lid, a sleep from the Apple menu, or the system sleeping on a low
// battery. A hold is cheap, safe from any goroutine, and ends with the
// process; the reason is what the system shows in Activity Monitor and
// pmset -g assertions. Release it as soon as the need is over.
//
// # Events
//
// The system's sleep and wake arrive as the app's own EvWillSleep and
// EvDidWake (the application object watches them from the first frame;
// nothing to set up). The screens' sleep and wake (the display going dark
// with the machine awake, or lighting again) arrive after Observe as an
// EvService from platform.SourcePower. EventOf decodes all four. A screens
// event that is not delivered before the next one is replaced by it: the
// app sees the state the screens are in, never a stale one.
//
// # What each OS does
//
//   - macOS: IOKit IOPMAssertionCreateWithName for Hold; NSWorkspace
//     notifications (willSleep, didWake, screensDidSleep, screensDidWake).
//   - Windows and Linux: not yet; Hold and Observe return ErrUnsupported
//     (errors.Is errors.ErrUnsupported).
package power

import (
	"errors"
	"fmt"
	"sync"

	"gonativeui/app"
	"gonativeui/platform"
)

var (
	// ErrUnsupported: this OS has no power management here yet. It matches
	// errors.ErrUnsupported.
	ErrUnsupported = fmt.Errorf("power: %w", errors.ErrUnsupported)
	// ErrInvalid: the kind is not one of the Kind constants.
	ErrInvalid = errors.New("power: invalid hold kind")
	// ErrRefused: the system refused the assertion; the error says its code.
	ErrRefused = errors.New("power: the system refused the assertion")
	// ErrNotStarted: Observe was called before the application object exists.
	ErrNotStarted = errors.New("power: the app has not started (call from Handler.Start or later)")
)

// Kind is what a hold keeps from happening.
type Kind uint8

const (
	// PreventIdleSleep keeps the system awake while the user is idle. The
	// display may still turn off.
	PreventIdleSleep Kind = iota + 1
	// PreventDisplaySleep keeps the display on, and so the system awake.
	PreventDisplaySleep
)

// An Assertion is one hold on the machine's sleep. Release it when the need
// is over.
type Assertion struct {
	mu   sync.Mutex
	id   uint32
	done bool
}

// Hold takes a power assertion of kind, with a reason the system shows ("Call
// with Ana"). Any goroutine.
func Hold(kind Kind, reason string) (*Assertion, error) {
	if kind != PreventIdleSleep && kind != PreventDisplaySleep {
		return nil, fmt.Errorf("%w: %d", ErrInvalid, kind)
	}
	id, err := osHold(kind, reason)
	if err != nil {
		return nil, err
	}
	return &Assertion{id: id}, nil
}

// Release gives the assertion back. A second call does nothing.
func (h *Assertion) Release() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.done {
		return nil
	}
	h.done = true
	return osRelease(h.id)
}

// Event is a change in the machine's power state.
type Event uint8

const (
	// WillSleep: the system is going to sleep (EvWillSleep).
	WillSleep Event = iota + 1
	// DidWake: the system woke from sleep (EvDidWake).
	DidWake
	// ScreensSleep: the displays went dark (after Observe).
	ScreensSleep
	// ScreensWake: the displays lit again (after Observe).
	ScreensWake
)

// String is the event in words: "did wake".
func (e Event) String() string {
	switch e {
	case WillSleep:
		return "will sleep"
	case DidWake:
		return "did wake"
	case ScreensSleep:
		return "screens sleep"
	case ScreensWake:
		return "screens wake"
	}
	return fmt.Sprintf("power.Event(%d)", uint8(e))
}

// EventOf decodes an app event: ok is false for one that is not a power
// event.
func EventOf(e *app.Event) (ev Event, ok bool) {
	switch {
	case e.Kind == app.EvWillSleep:
		return WillSleep, true
	case e.Kind == app.EvDidWake:
		return DidWake, true
	case e.Kind == app.EvService && e.Source == platform.SourcePower:
		if ev = Event(e.A); ev == ScreensSleep || ev == ScreensWake {
			return ev, true
		}
	}
	return 0, false
}

// Observe starts watching the screens' sleep and wake (ScreensSleep,
// ScreensWake); the system's own sleep and wake need no call. A second call
// does nothing. Main thread, from Handler.Start or later.
func Observe() error { return osObserve() }
