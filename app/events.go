package app

import (
	"bytes"
	"slices"

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
	// it runs; A is 1 when a window is visible. DefaultHandler shows the most
	// recently opened hidden or minimised window when none is visible; with
	// no window open it does nothing, and the app's Handler opens one if it
	// wants (OpenWindow).
	EvReopen = EventKind(platform.AppEvReopen)
	// EvActivated: the app became the active application.
	EvActivated = EventKind(platform.AppEvActivated)
	// EvDeactivated: another application became active.
	EvDeactivated = EventKind(platform.AppEvDeactivated)
	// EvQuitRequested asks the app to quit: e.QuitReason says who asks, and
	// ID identifies the request. Answer with App.ReplyQuit(e.ID, allow), now
	// or later (see App.ReplyQuit). The quit is decided with the app's sheets
	// and alerts still up; allowed, they end as cancelled (never with their
	// first button) before EvWillTerminate. A logout's request (QuitLogout)
	// refused cancels the logout; left unanswered, the logout waits.
	//
	// A request asked while a frame is drawn (a native loop inside Build)
	// arrives after the frame. A quit Apple event (the Dock, a logout) waits
	// for that request's answer meanwhile, so a logout then goes on or stops
	// as the app answers. Only a terminate: someone else sends during a frame
	// is refused at once (it cannot wait inside the frame) and asked again
	// after it.
	//
	// Once a quit is allowed it is not asked again: a quit request that
	// arrives before the app ends is answered yes and does not ask the
	// handler. Leaving Main (App.Quit, the last window closing) while a
	// request waits answers its quit event yes, ends the open dialogs as
	// cancelled (their results arrive first) and then sends EvWillTerminate.
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
	// EvWillTerminate is the last event: the app is about to end, and on
	// macOS the process exits when it returns (Main does not return). It is
	// for final, quick, synchronous cleanup that does no I/O worth waiting
	// for. Durable work, saving above all, must be finished before the quit
	// is allowed: answer EvQuitRequested with ReplyQuit(id, true) only once
	// the save is done (defer the answer while it runs). Before this event
	// every sheet has ended and every result and event already accepted has
	// been delivered.
	EvWillTerminate = EventKind(platform.AppEvWillTerminate)
	// EvCommand is an app-wide menu command (ID): a command chosen while no
	// window is open, or from the Dock menu.
	EvCommand = EventKind(platform.AppEvCommand)
	// EvService is a result, response, outside request or change from a
	// service, tagged by Source; ID is its ticket or sequence number. A
	// native dialog's answer (Alert, OpenPanel, SavePanel) is an EvService
	// from platform.SourceDialog with the call's ticket as ID; TakeDialog
	// returns its result, with the paths.
	EvService = EventKind(platform.AppEvService)
	// EvPost is an event the app posted with Post.
	EvPost = EventKind(platform.AppEvPost)
	// EvWindowClosed: a window has closed, whichever way (its close button,
	// Win.Close, the app quitting); Window is its id (Win.ID). It arrives
	// once per window, when the window is gone (its Win.Open is false), so an
	// app need not poll Win.Open. When the app ends it arrives for each
	// window still open just before EvWillTerminate, which stays the last
	// event.
	EvWindowClosed = EventKind(platform.AppEvWindowClosed)
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
// long), and the caller keeps the event and tries again later. Once the app
// is ending (the last delivery before EvWillTerminate has begun) it returns
// false for good. Framework services never use it.
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
	if pe.Kind == platform.AppEvService && pe.Source == platform.SourceDialog {
		if w := a.dialogWindow(pe); w != nil {
			w.onDialog(pe, data)
		} else {
			a.closedDialog(pe, data)
		}
		return
	}
	a.deliver(pe, data)
}

// dialogWindow is the window whose dialog a SourceDialog event answers (the
// one showing it under the platform's ticket), else a if it shows it (an
// App with no window of the loop's: offscreen), else nil.
func (a *App) dialogWindow(pe *platform.AppEvent) *App {
	t := platform.DialogTicket(pe.ID)
	for _, w := range a.wins {
		for _, d := range w.dlgShown {
			if d.plat == t {
				return w
			}
		}
	}
	for _, d := range a.dlgShown {
		if d.plat == t {
			return a
		}
	}
	return nil
}

// closedDialog is a dialog that was on screen when its window closed: its
// sheet ends as cancelled after the window is gone.
type closedDialog struct {
	shownDialog
	a   *App   // the window's App, which showed it
	win uint32 // the closed window's id
}

// keepClosedDialogs keeps the dialogs on screen as the window closes for
// closedDialog: their sheets end as cancelled after the window is gone.
func (a *App) keepClosedDialogs() {
	for _, d := range a.dlgShown {
		a.closedDlg = append(a.closedDlg, closedDialog{shownDialog: d, a: a, win: a.winID})
	}
	a.dlgShown = a.dlgShown[:0]
}

// closedDialog delivers the answer of a dialog whose window closed while it
// was on screen: to the Handler, as an EvService with the app's ticket and the
// closed window's id (Event.Window), and to TakeDialog of the window's App,
// unless that App holds another window by now (the Handler's, opened again:
// the answer is not the new window's). An answer of no such dialog (a
// withdrawn one) is dropped.
func (a *App) closedDialog(pe *platform.AppEvent, data []byte) {
	t := platform.DialogTicket(pe.ID)
	for i, d := range a.closedDlg {
		if d.plat == t {
			a.closedDlg = slices.Delete(a.closedDlg, i, i+1)
			if d.a.winID == d.win {
				r := platform.DialogResultOf(pe, data)
				r.Ticket = d.app
				d.a.pushDialog(r)
			}
			e := *pe
			e.ID, e.Window = uint64(d.app), d.win
			a.deliver(&e, data)
			return
		}
	}
}

// AppSafePoint runs the deferred safe-point work (platform.AppSink): also
// while the app's own loop is suspended in a native loop (a pending quit),
// so a dialog asked for then is shown and a window closed.
func (a *App) AppSafePoint() { a.safePoint() }

// deliver hands one event to the Handler, in the record of its nesting depth.
func (a *App) deliver(pe *platform.AppEvent, data []byte) {
	if a.handler == nil {
		return
	}
	if pe.Kind == platform.AppEvWillTerminate {
		a.announceOpenClosed()
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
	h := a.root
	if h == nil {
		h = a
	}
	a.handler.Event(h, e)
	delivering.data[d], delivering.seq[d] = prevData, prevSeq
	a.evDepth--
	// What the event changed is drawn, by the loop, or by the frame handler
	// inside a native loop: in the window the event is for (Event.Window),
	// else in every window.
	native := len(a.wins) > 0 && platform.NativeLoop()
	only := a.windowOf(pe.Window)
	if only == nil && pe.Window != 0 {
		return // for a window that has closed: none draws
	}
	for _, w := range a.wins {
		if only != nil && w != only {
			continue
		}
		w.p.invalidate()
		if native {
			w.Win.RequestFrame()
		}
	}
}

// announceOpenClosed delivers EvWindowClosed for the windows still open as
// the app ends, the last opened first, before EvWillTerminate.
func (a *App) announceOpenClosed() {
	open := slices.Clone(a.wins)
	slices.Reverse(open)
	for _, w := range open {
		if id := w.winID; id != 0 && w.told != id {
			w.told = id
			a.deliver(&platform.AppEvent{Kind: platform.AppEvWindowClosed, Window: id}, nil)
		}
	}
}

// windowOf is the open window with id, nil for 0 or none.
func (a *App) windowOf(id uint32) *App {
	if id == 0 {
		return nil
	}
	for _, w := range a.wins {
		if w.winID == id {
			return w
		}
	}
	return nil
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
// a native loop (menu tracking, AppKit's wait for the answer to a terminate:
// sent by someone else), the app
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
