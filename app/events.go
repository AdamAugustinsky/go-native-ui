package app

import (
	"bytes"

	"gonativeui/platform"
)

// App events (DECISIONS D37): what the application object delivers to the
// Handler, on the main thread, between frames, in order. The delivery
// classes are platform's (platform/appobj.go): main-thread native events
// synchronously from the system's delegate; completions and responses
// through records reserved with the request (platform.Tickets); outside
// requests accepted into a reserved ring or refused (platform.Ring); changes
// as coalesced observations (platform.Observation); and the app's own Post.

// EventKind identifies an Event.
type EventKind uint8

// The kinds of app events.
const (
	// EvNone is no event.
	EvNone = EventKind(platform.AppEvNone)
	// EvReopen: the Dock icon was clicked (or the app was opened again) while
	// it runs; A is 1 when a window is visible. DefaultHandler shows a hidden
	// or minimised window when none is visible.
	EvReopen = EventKind(platform.AppEvReopen)
	// EvActivated: the app became the active application.
	EvActivated = EventKind(platform.AppEvActivated)
	// EvDeactivated: another application became active.
	EvDeactivated = EventKind(platform.AppEvDeactivated)
	// EvQuitRequested asks the app to quit: e.QuitReason says who asks. Answer
	// with App.ReplyQuit, now or later (see App.ReplyQuit).
	EvQuitRequested = EventKind(platform.AppEvQuitRequested)
	// EvQuitDeadline: a quit request stayed unanswered past
	// AppConfig.QuitDeadline; A is the QuitAction that was applied. An error
	// event.
	EvQuitDeadline = EventKind(platform.AppEvQuitDeadline)
	// EvOpenURLs: URLs to open (the app's URL schemes); A is the count and
	// e.Strings the URLs.
	EvOpenURLs = EventKind(platform.AppEvOpenURLs)
	// EvOpenFiles: documents to open; A is the count and e.Strings the paths.
	EvOpenFiles = EventKind(platform.AppEvOpenFiles)
	// EvWillSleep: the system is going to sleep.
	EvWillSleep = EventKind(platform.AppEvWillSleep)
	// EvDidWake: the system woke up.
	EvDidWake = EventKind(platform.AppEvDidWake)
	// EvScreensChanged: screens were added, removed, rearranged or changed
	// resolution.
	EvScreensChanged = EventKind(platform.AppEvScreensChanged)
	// EvSessionActive: the user session became active (A 1) or inactive (A 0,
	// fast user switching).
	EvSessionActive = EventKind(platform.AppEvSessionActive)
	// EvWillTerminate is the last event: the app is about to end. Save what
	// must be saved here: after an allowed quit request macOS ends the
	// process when this returns, and Main does not return.
	EvWillTerminate = EventKind(platform.AppEvWillTerminate)
	// EvCommand is an app-wide menu command (ID): a command chosen while no
	// window is open, or from the Dock menu.
	EvCommand = EventKind(platform.AppEvCommand)
	// EvService is a result, response, outside request or change from a
	// service, tagged by Source; ID is its ticket or sequence number.
	EvService = EventKind(platform.AppEvService)
	// EvPost is an event the app posted with Post.
	EvPost = EventKind(platform.AppEvPost)
)

// Source identifies the producer of an EvService (platform.SourceDialog,
// platform.SourceNotifyResponse …); SourcePost marks the app's own posts.
type Source = platform.Source

// QuitReason says who asks the app to quit (Event.QuitReason).
type QuitReason = platform.QuitReason

// The quit reasons.
const (
	QuitMenu   = platform.QuitMenu   // the Quit menu item or ⌘Q
	QuitDock   = platform.QuitDock   // Quit in the Dock, or another app
	QuitLogout = platform.QuitLogout // logout, restart or shutdown: answer promptly
	QuitUpdate = platform.QuitUpdate // an updater
	QuitSignal = platform.QuitSignal // SIGTERM or SIGINT
	QuitApp    = platform.QuitApp    // App.RequestQuit
	QuitOther  = platform.QuitOther  // anything else that called terminate:
)

// QuitAction is what a quit request unanswered at its deadline gets.
type QuitAction = platform.QuitAction

// The quit deadline's actions.
const (
	QuitCancel = platform.QuitCancel // cancel the quit (the default)
	QuitAllow  = platform.QuitAllow  // quit anyway: only as the app's explicit choice
)

// Policy is the activation policy (AppConfig.Policy).
type Policy = platform.Policy

// The activation policies.
const (
	PolicyRegular    = platform.PolicyRegular    // a Dock icon and a menu bar
	PolicyAccessory  = platform.PolicyAccessory  // no Dock icon (a menu bar extra)
	PolicyProhibited = platform.PolicyProhibited // no Dock icon and no windows
)

// Event is one app event: a fixed-size, pointer-free record. Its data
// (Data, Strings) is valid during the Handler.Event call only.
type Event struct {
	// Kind is the kind of event.
	Kind EventKind
	// Source is the service of an EvService (SourcePost for EvPost).
	Source Source
	// Window is the window the event concerns, 0 for none.
	Window uint32
	// ID is the ticket of a completion, the sequence number of an outside
	// request, the command of an EvCommand, the serial of a quit request, or
	// what the app put in a Post.
	ID uint64
	// A and B are integers whose meaning depends on the kind and the source.
	A, B int64

	slot uint8  // the delivery depth whose data is the event's
	seq  uint32 // the delivery it belongs to: a copy kept past it has no data
}

// delivering is the data of the events being delivered, by nesting depth.
var delivering struct {
	data [8][]byte
	seq  [8]uint32
	next uint32
}

// Data returns the event's bytes (URLs, paths, a reply's text, a posted
// payload). It is valid during the Handler.Event call only; nil afterwards
// and for events without data.
func (e *Event) Data() []byte {
	if e.seq == 0 || int(e.slot) >= len(delivering.seq) || delivering.seq[e.slot] != e.seq {
		return nil
	}
	return delivering.data[e.slot]
}

// Strings splits the data at NUL bytes (EvOpenURLs, EvOpenFiles). It
// allocates; these events are rare.
func (e *Event) Strings() []string {
	d := e.Data()
	if len(d) == 0 {
		return nil
	}
	var out []string
	for _, p := range bytes.Split(d, []byte{0}) {
		out = append(out, string(p))
	}
	return out
}

// QuitReason is the reason of an EvQuitRequested.
func (e *Event) QuitReason() QuitReason { return QuitReason(e.A) }

// Post queues an event for the app's Handler from any goroutine: it arrives
// as EvPost with e's Source, Window, ID, A and B and a copy of data (up to
// platform.MaxPostData bytes). Post never blocks and allocates nothing; it
// returns false when platform.MaxPosted posts are waiting (or data is too
// long), and the caller keeps the event and tries again later. Framework
// services never use it.
func Post(e Event, data []byte) bool {
	return platform.PostApp(platform.AppEvent{Source: e.Source, Window: e.Window, ID: e.ID, A: e.A, B: e.B}, data)
}

// startupEvent is an event that arrived before Start returned, kept with a
// copy of its data (the one place app events allocate).
type startupEvent struct {
	ev   platform.AppEvent
	data []byte
}

// AppEvent is the application object's delivery (platform.AppSink); it is
// called by the platform on the main thread.
func (a *App) AppEvent(pe *platform.AppEvent, data []byte) {
	if pe.Kind == platform.AppEvWillTerminate {
		if a.terminated {
			return
		}
		a.terminated = true
	}
	if !a.started {
		a.startup = append(a.startup, startupEvent{*pe, append([]byte(nil), data...)})
		return
	}
	a.deliver(pe, data)
}

// AppSafePoint runs the deferred safe-point work (platform.AppSink).
func (a *App) AppSafePoint() { a.runSafePoints() }

// deliver hands one event to the Handler, in the record of its nesting depth.
func (a *App) deliver(pe *platform.AppEvent, data []byte) {
	if a.handler == nil {
		return
	}
	d := min(a.evDepth, len(a.evs)-1)
	a.evDepth++
	delivering.next++
	if delivering.next == 0 {
		delivering.next = 1
	}
	seq := delivering.next
	e := &a.evs[d]
	*e = Event{Kind: EventKind(pe.Kind), Source: pe.Source, Window: pe.Window, ID: pe.ID, A: pe.A, B: pe.B, slot: uint8(d), seq: seq}
	prevData, prevSeq := delivering.data[d], delivering.seq[d]
	delivering.data[d], delivering.seq[d] = data, seq
	a.handler.Event(a, e)
	delivering.data[d], delivering.seq[d] = prevData, prevSeq
	a.evDepth--
	if a.Win != nil { // what the event changed is drawn: by the loop, or by the frame handler inside a native loop
		a.p.invalidate()
		if platform.NativeLoop() {
			a.Win.RequestFrame()
		}
	}
}

// flushStartup delivers the events that arrived before Start returned, in
// order.
func (a *App) flushStartup() {
	for len(a.startup) > 0 {
		s := a.startup[0]
		a.startup = a.startup[1:]
		a.deliver(&s.ev, s.data)
	}
	a.startup = nil
}

// AtSafePoint runs f at the next safe point: between frames, outside Build
// and outside every event delivery. While the app's own loop is suspended in
// a native loop (AppKit's wait for a quit answer, menu tracking), the app
// object's dispatcher runs it, so a sheet asked for then still opens. f may
// begin sheets and alerts, open and close windows and rebuild menus; it must
// not start a native loop of its own (a popped-up menu, runModal). Main
// thread.
func (a *App) AtSafePoint(f func()) {
	a.safe = append(a.safe, f)
	platform.Wake()
}

// runSafePoints runs the queued safe-point work, including work queued by it
// (up to a bound; the rest waits for the next safe point).
func (a *App) runSafePoints() {
	if a.inSafe || a.evDepth > 0 {
		return
	}
	a.inSafe = true
	for round := 0; round < 16 && len(a.safe) > 0; round++ {
		q := a.safe
		a.safe = a.safeSpare[:0]
		for i, f := range q {
			q[i] = nil
			f()
		}
		a.safeSpare = q[:0]
	}
	a.inSafe = false
}
