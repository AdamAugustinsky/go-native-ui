// Package notify posts local user notifications and delivers the user's
// responses to them (a click, an action button, a typed reply) as app events
// (DECISIONS D37, D45).
//
//	func main() {
//		// Before app.Main: a click that launched the app is not lost.
//		if err := notify.Setup(notify.Options{Foreground: true}); err != nil {
//			log.Print(err) // not a bundled app, or not macOS: run without notifications
//		}
//		app.Main(app.AppConfig{ID: "dev.example.chat"}, &chat{})
//	}
//
//	func (c *chat) Start(a *app.App) error {
//		notify.RegisterCategories(notify.Category{ID: "message", Actions: []notify.Action{
//			{ID: actReply, Title: "Reply", Reply: true},
//			{ID: actRead, Title: "Mark as Read"},
//		}})
//		notify.RequestPermission(notify.Alert | notify.Sound)
//		...
//	}
//
//	notify.Post(notify.Note{Thread: "chat-42", Title: "Ana", Body: "Lunch?",
//		Category: "message", UserData: []byte("chat-42/msg-7")})
//
//	func (c *chat) Event(a *app.App, e *app.Event) {
//		if r, ok := notify.ResponseOf(e); ok {
//			// r.Action: ActionClick, ActionDismiss or actReply/actRead;
//			// r.Reply the typed text, r.UserData the note's UserData
//		}
//		a.DefaultEvent(e)
//	}
//
// # Requests and results
//
// No call waits for the system. A call that the system answers later (Post,
// RequestPermission, Status) takes a ticket first, from a table of
// platform.MaxTickets; with all of them waiting for delivery it fails with
// ErrBusy and nothing is sent (so does a burst of more than that many posts
// from one event handler, whose results cannot be delivered before it
// returns). The answer arrives as an app.EvService from platform.SourceNotify
// whose ID is the ticket; ResultOf decodes it. One ResultRestored result also
// arrives after Setup (below).
//
// # Responses and their slots
//
// A note with UserData or a Category (actions, a reply field) reserves a
// response slot when it is posted, from a table of MaxResponses. When the
// user clicks it, picks an action or sends a reply, the delegate stores the
// action, the reply text (up to MaxReply bytes) and the note's UserData in
// that slot before it tells the system the response was handled, so a reply
// the system considers delivered cannot be lost on its way to the app. It
// arrives as an app.EvService from platform.SourceNotifyResponse whose ID is
// the slot's ticket (PostResult.Response); ResponseOf decodes it. Nothing the
// app does (app.Post, other requests) can take the room a response needs.
//
// A slot is freed when its response has been delivered, or when its note is
// removed (Remove, RemoveThread, a later note with the same ID replacing it,
// EvictOldest). A note the user clears from Notification Center without a
// response keeps its slot (unless its category has NotifyDismiss, which
// turns the clearing into an ActionDismiss response) until one of those, or
// until the app quits.
//
// When every slot is taken, the note is posted as a plain banner, without
// its actions, reply field and UserData, whose click only brings the app
// forward, and Post reports PostedWithoutActions. With Options.EvictOldest
// the oldest note holding a slot is removed from Notification Center instead,
// and the new note gets its slot (PostResult.Evicted names the one removed).
//
// A note without UserData or a Category is plain from the start
// (NoResponse): clicking it brings the app forward and delivers nothing.
//
// # Launches
//
// Setup installs the system delegate. Called before app.Main, it is in place
// before the application finishes launching, so the click that launched the
// app arrives (after Handler.Start returns). Notes posted by an earlier run
// and still in Notification Center get their slots back at Setup, newest
// first; the ResultRestored result says how many, and names the ones that
// did not fit, which the app may Remove. A response to one of those is still
// delivered if a slot is free when it arrives.
//
// # Threads and cost
//
// Setup must be called on the main thread (before app.Main, or from the
// Handler). It opens UserNotifications and connects to the system's center
// before the first frame, the one framework the app object loads early for
// an app that opts in: 3 to 10 ms on a Mac mini (macOS 27.2). The other
// functions may be called from any goroutine; Post with an Attachment copies
// the file. The delegate costs nothing while idle: the system calls it only
// for a note that is shown or answered, on a thread of its own, and it only
// copies into the reserved slot. Delivering a response allocates nothing
// once the slots are warm.
//
// # What each OS does
//
//   - macOS: UNUserNotificationCenter (UserNotifications, opened by Setup).
//     It works only in a bundled app (a .app with a bundle identifier, such
//     as gnui run and gnui package build); elsewhere Available is false and
//     Setup returns ErrUnavailable. The system asks the user once, at
//     RequestPermission, and remembers the answer per app; sign development
//     builds with a stable identity (gnui run does) so it survives rebuilds.
//   - Windows and Linux: not yet; Setup returns ErrUnsupported (errors.Is
//     errors.ErrUnsupported) and Available is false.
package notify

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"gonativeui/app"
	"gonativeui/platform"
)

// Ticket identifies a request (the ID of its result) or a response slot (the
// ID of its response). It never repeats in a process.
type Ticket = platform.Ticket

// Limits.
const (
	// MaxResponses is how many notes can wait for a response at once.
	MaxResponses = platform.MaxNotifyResponses
	// MaxReply is the longest reply text delivered, in bytes; a longer reply
	// is cut at a character boundary.
	MaxReply = platform.MaxNotifyReply
	// MaxUserData is the most UserData a note may carry.
	MaxUserData = 512
)

// The actions of a Response besides the app's own (Action.ID).
const (
	// ActionClick: the user clicked the note itself.
	ActionClick int64 = 0
	// ActionDismiss: the user dismissed the note (only for a category with
	// NotifyDismiss).
	ActionDismiss int64 = -1
	// ActionOther: an action the app did not register (none in practice).
	ActionOther int64 = -2
)

// SoundDefault is Note.Sound for the system's notification sound.
const SoundDefault = "default"

var (
	// ErrUnsupported: this OS has no notifications here yet. It matches
	// errors.ErrUnsupported.
	ErrUnsupported = fmt.Errorf("notify: %w", errors.ErrUnsupported)
	// ErrUnavailable: the system's notification center cannot be used by
	// this process, which is not a bundled app (a .app with a bundle
	// identifier).
	ErrUnavailable = errors.New("notify: unavailable (the app is not a bundle with an identifier)")
	// ErrNotSetUp: Setup has not succeeded yet.
	ErrNotSetUp = errors.New("notify: Setup has not been called")
	// ErrBusy: every request ticket is waiting for its result to be
	// delivered; nothing was sent. Try again after the results are
	// delivered.
	ErrBusy = fmt.Errorf("notify: %w", platform.ErrBusy)
	// ErrInvalid: a note, category or action the system cannot take (the
	// error says what).
	ErrInvalid = errors.New("notify: invalid")
)

// Options configure the service (Setup).
type Options struct {
	// EvictOldest: when every response slot is taken, Post removes the
	// oldest note holding one from Notification Center and gives its slot to
	// the new note, instead of posting the new note without actions.
	EvictOldest bool
	// Foreground shows notes while the app is the active app (Foreground
	// changes it later). Off, the system does not show them then.
	Foreground bool
}

// Permission is what RequestPermission asks for.
type Permission uint8

// The permissions.
const (
	// Badge: a number on the app's icon.
	Badge Permission = 1 << iota
	// Sound: notes may play a sound.
	Sound
	// Alert: notes are shown (banners and Notification Center).
	Alert
	// Provisional: notes go quietly to Notification Center without asking
	// the user first.
	Provisional
)

// AuthStatus is whether the app may post notes (ResultStatus).
type AuthStatus int64

// The authorization statuses (the system's values).
const (
	// StatusNotDetermined: the user has not been asked.
	StatusNotDetermined AuthStatus = iota
	// StatusDenied: the user said no; only System Settings changes it.
	StatusDenied
	// StatusAuthorized: notes are allowed.
	StatusAuthorized
	// StatusProvisional: notes go quietly to Notification Center.
	StatusProvisional
	// StatusEphemeral: allowed for a limited time (App Clips).
	StatusEphemeral
)

// Action is a button (or a reply field) under a note of its category.
type Action struct {
	// ID identifies the action in a Response; it must be greater than 0.
	ID int64
	// Title is the button's text.
	Title string
	// Reply makes the action a text field: the text arrives in
	// Response.Reply.
	Reply bool
	// ReplyButton is the send button of a Reply action ("" uses Title).
	ReplyButton string
	// ReplyPlaceholder is the empty text field's hint.
	ReplyPlaceholder string
	// Destructive shows the action as destructive.
	Destructive bool
	// Foreground brings the app to the front when the action is chosen.
	Foreground bool
	// AuthRequired: the action needs the Mac unlocked.
	AuthRequired bool
}

// Category is a kind of note with its actions (RegisterCategories); a note
// names it in Note.Category.
type Category struct {
	// ID names the category.
	ID string
	// Actions are its buttons, in order.
	Actions []Action
	// NotifyDismiss delivers the user's clearing of a note as a Response with
	// ActionDismiss (freeing its slot); otherwise clearing is not reported.
	NotifyDismiss bool
}

// Note is a notification to post.
type Note struct {
	// ID identifies the note in Notification Center (Remove); "" makes one.
	// A note posted with the ID of one still shown replaces it.
	ID string
	// Thread groups notes (a conversation); RemoveThread removes a group.
	Thread string
	// Title, Subtitle and Body are the note's text.
	Title, Subtitle, Body string
	// Sound is "" for none, SoundDefault, or the name of a sound file in the
	// app's bundle.
	Sound string
	// Attachment is the path of an image, audio or video file shown with the
	// note; Post copies it (the system takes the file it is given).
	Attachment string
	// Category names a registered category whose actions the note shows.
	Category string
	// UserData comes back in the note's Response (at most MaxUserData
	// bytes). A note with UserData or a Category reserves a response slot.
	UserData []byte
}

// Reservation says what Post reserved for the note's response.
type Reservation uint8

// The reservations.
const (
	// NoResponse: a plain note (no UserData, no Category); a click only
	// brings the app forward.
	NoResponse Reservation = iota
	// Reserved: a slot holds the note's response until it is delivered.
	Reserved
	// PostedWithoutActions: every slot was taken, so the note went out plain:
	// without its actions, reply field and UserData.
	PostedWithoutActions
)

// PostResult is what Post did.
type PostResult struct {
	// ID is the note's identifier (Note.ID, or the one made for it).
	ID string
	// Request is the ticket of the post's result (ResultPost).
	Request Ticket
	// Response is the ticket of the reserved slot, the ID of the note's
	// Response (0 unless Reserved).
	Response Ticket
	// Reservation says whether a slot was reserved.
	Reservation Reservation
	// Evicted is the ID of the note removed to free a slot (EvictOldest), ""
	// for none.
	Evicted string
}

// ResultKind says which request a Result answers.
type ResultKind uint8

// The result kinds.
const (
	// ResultPermission answers RequestPermission: Granted, Err.
	ResultPermission ResultKind = iota + 1
	// ResultStatus answers Status: Status.
	ResultStatus
	// ResultPost answers Post: Err and ErrCode, empty when the system took
	// the note.
	ResultPost
	// ResultRestored arrives once after Setup: Restored notes of earlier runs
	// got their slots back, Overflow did not (OverflowIDs).
	ResultRestored
)

// Result is a request's answer (ResultOf).
type Result struct {
	// Kind is the request answered.
	Kind ResultKind
	// Ticket is the request's ticket (the event's ID).
	Ticket Ticket
	// Granted: the user allowed the permission asked for (ResultPermission).
	Granted bool
	// Status is the app's authorization (ResultStatus).
	Status AuthStatus
	// ErrCode and Err are the system's error code and message, 0 and "" for
	// none.
	ErrCode int64
	// Err is the system's error message.
	Err string
	// Restored and Overflow count the notes of earlier runs that got their
	// slots back and that did not (ResultRestored).
	Restored, Overflow int
	// OverflowIDs are the IDs of the notes that did not fit.
	OverflowIDs []string
}

// Response is the user's response to a note (ResponseOf). Its byte slices
// are the event's data: valid during the Handler.Event call only.
type Response struct {
	// Ticket identifies the slot the response came through: the note's
	// PostResult.Response while the note held its slot; another ticket for a
	// note of an earlier run, or one removed just before the user answered.
	Ticket Ticket
	// Action is ActionClick, ActionDismiss, ActionOther or the chosen
	// Action.ID.
	Action int64
	// Reply is the text the user sent with a Reply action.
	Reply []byte
	// UserData is the note's UserData.
	UserData []byte
}

// ResponseOf decodes a response event (an app.EvService from
// platform.SourceNotifyResponse). Its slices are valid during the event.
func ResponseOf(e *app.Event) (Response, bool) {
	if e == nil || e.Kind != app.EvService || e.Source != platform.SourceNotifyResponse {
		return Response{}, false
	}
	return decodeResponse(e.ID, e.A, e.B, e.Data()), true
}

// decodeResponse splits a response's data: the reply, then the UserData,
// whose length is b.
func decodeResponse(id uint64, a, b int64, data []byte) Response {
	n := int(b)
	if n < 0 || n > len(data) {
		n = 0
	}
	r := Response{Ticket: Ticket(id), Action: a}
	if k := len(data) - n; k > 0 {
		r.Reply = data[:k:k]
	}
	if n > 0 {
		r.UserData = data[len(data)-n:]
	}
	return r
}

// ResultOf decodes a request's result (an app.EvService from
// platform.SourceNotify). It allocates the strings.
func ResultOf(e *app.Event) (Result, bool) {
	if e == nil || e.Kind != app.EvService || e.Source != platform.SourceNotify {
		return Result{}, false
	}
	return decodeResult(e.ID, e.A, e.B, e.Data()), true
}

func decodeResult(id uint64, a, b int64, data []byte) Result {
	r := Result{Kind: ResultKind(a), Ticket: Ticket(id)}
	switch r.Kind {
	case ResultPermission:
		r.Granted = b != 0
		r.Err = string(data)
	case ResultStatus:
		r.Status = AuthStatus(b)
	case ResultPost:
		r.ErrCode, r.Err = b, string(data)
	case ResultRestored:
		r.Restored, r.Overflow = int(b&0xFFFFFFFF), int(b>>32)
		for len(data) > 0 {
			i := 0
			for i < len(data) && data[i] != 0 {
				i++
			}
			r.OverflowIDs = append(r.OverflowIDs, string(data[:i]))
			data = data[min(i+1, len(data)):]
		}
	}
	return r
}

// ---- The service ----

// backend is the system's notification center. The core has checked the
// arguments; completions come back through the functions in slots.go, on
// any thread.
type backend interface {
	// supported: this OS has a notification center here.
	supported() bool
	// available: this process can use it.
	available() bool
	// start installs the delegate (main thread).
	start() error
	authorize(tk Ticket, p Permission) error
	settings(tk Ticket) error
	categories(cats []Category) error
	// add posts n as id; with key it carries the response key, its category
	// and UserData, without it is plain. Its result calls added(ctx, …).
	add(n *Note, id string, key uint64, ctx int) error
	// remove removes delivered and pending notes.
	remove(ids []string)
	// delivered lists the notes in Notification Center: listed(ctx, …).
	delivered(ctx int) error
}

var svc struct {
	setupMu sync.Mutex // Setup
	b       backend
	on      atomic.Bool // Setup succeeded

	foreground atomic.Bool

	mu   sync.Mutex // the post side: the slots' bookkeeping, the options
	opts Options

	catMu sync.Mutex // the registered categories, in order
	cats  []Category

	nonce uint32
	seq   atomic.Uint64 // generated IDs and keys
}

// osBackend is the system's center (tests replace svc.b).
var osBackend = func() backend { return nativeCenter{} }

// Available reports whether notifications can be used by this process: on
// macOS, an app run from its bundle (gnui run, gnui package).
func Available() bool {
	b := currentBackend()
	return b.supported() && b.available()
}

func currentBackend() backend {
	svc.setupMu.Lock()
	defer svc.setupMu.Unlock()
	if svc.b == nil {
		svc.b = osBackend()
	}
	return svc.b
}

// Setup opts the app in: it installs the system delegate that receives
// responses and shows notes, and gives the response slots back to the notes
// of earlier runs still in Notification Center (a ResultRestored result
// follows). Call it on the main thread before app.Main, so that a click that
// launched the app is delivered; called later it works the same, but such a
// click may be lost. A later call only changes the options. It returns
// ErrUnsupported on an OS without notifications here and ErrUnavailable
// when the process is not a bundled app.
func Setup(o Options) error {
	b := currentBackend()
	if !b.supported() {
		return ErrUnsupported
	}
	if !b.available() {
		return ErrUnavailable
	}
	svc.setupMu.Lock()
	defer svc.setupMu.Unlock()
	svc.mu.Lock()
	svc.opts = o
	svc.mu.Unlock()
	svc.foreground.Store(o.Foreground)
	if svc.on.Load() {
		return nil
	}
	initSlots()
	if svc.nonce == 0 {
		svc.nonce = rand.Uint32() | 1
	}
	if err := b.start(); err != nil {
		return err
	}
	svc.on.Store(true)
	restoreAtLaunch()
	return nil
}

// restoreAtLaunch asks for the notes in Notification Center: those of
// earlier runs get their slots back (listed), and the app hears how it went
// (ResultRestored).
func restoreAtLaunch() {
	tk, err := slots.requests.Reserve(0)
	if err != nil {
		return
	}
	pi, ok := takePending()
	if !ok {
		slots.requests.Cancel(tk)
		return
	}
	setPending(pi, pending{kind: pendRestore, ticket: tk, slot: -1})
	if err := svc.b.delivered(pi); err != nil {
		freePending(pi)
		slots.requests.Complete(tk, int64(ResultRestored), 0, nil)
	}
}

// ready returns the backend once Setup has succeeded.
func ready() (backend, error) {
	if !svc.on.Load() {
		if b := currentBackend(); !b.supported() {
			return nil, ErrUnsupported
		}
		return nil, ErrNotSetUp
	}
	return svc.b, nil
}

// Foreground sets whether notes are shown while the app is the active app.
func Foreground(show bool) { svc.foreground.Store(show) }

// RequestPermission asks the user to allow notes (the system asks once; later
// calls answer with the user's choice at once). The answer arrives as
// ResultPermission with the returned ticket.
func RequestPermission(p Permission) (Ticket, error) {
	b, err := ready()
	if err != nil {
		return 0, err
	}
	tk, err := slots.requests.Reserve(0)
	if err != nil {
		return 0, ErrBusy
	}
	if err := b.authorize(tk, p); err != nil {
		slots.requests.Cancel(tk)
		return 0, err
	}
	return tk, nil
}

// Status asks whether the app may post notes; the answer arrives as
// ResultStatus with the returned ticket. It never asks the user.
func Status() (Ticket, error) {
	b, err := ready()
	if err != nil {
		return 0, err
	}
	tk, err := slots.requests.Reserve(0)
	if err != nil {
		return 0, ErrBusy
	}
	if err := b.settings(tk); err != nil {
		slots.requests.Cancel(tk)
		return 0, err
	}
	return tk, nil
}

// RegisterCategories adds categories to the app's set (the system keeps one
// set; a category registered again with the same ID replaces the earlier
// one). Register them before posting notes that name them, at every launch.
func RegisterCategories(cats ...Category) error {
	b, err := ready()
	if err != nil {
		return err
	}
	for _, c := range cats {
		if err := c.validate(); err != nil {
			return err
		}
	}
	svc.catMu.Lock()
	defer svc.catMu.Unlock()
	all := slices.Clone(svc.cats)
	for _, c := range cats {
		c.Actions = slices.Clone(c.Actions)
		if i := slices.IndexFunc(all, func(x Category) bool { return x.ID == c.ID }); i >= 0 {
			all[i] = c
		} else {
			all = append(all, c)
		}
	}
	if err := b.categories(all); err != nil {
		return err
	}
	svc.cats = all
	return nil
}

func (c *Category) validate() error {
	if c.ID == "" || !utf8.ValidString(c.ID) {
		return fmt.Errorf("%w: a category needs an ID", ErrInvalid)
	}
	for i, a := range c.Actions {
		if a.ID <= 0 || a.Title == "" {
			return fmt.Errorf("%w: category %q: an action needs an ID above 0 and a title", ErrInvalid, c.ID)
		}
		for _, b := range c.Actions[:i] {
			if b.ID == a.ID {
				return fmt.Errorf("%w: category %q: action %d twice", ErrInvalid, c.ID, a.ID)
			}
		}
	}
	return nil
}

func registered(id string) bool {
	svc.catMu.Lock()
	defer svc.catMu.Unlock()
	return slices.ContainsFunc(svc.cats, func(c Category) bool { return c.ID == id })
}

// Post posts a note. A note with UserData or a Category reserves a response
// slot (see the package comment); PostResult says whether it got one. The
// system's acceptance arrives as ResultPost. It fails with ErrBusy when every
// request ticket is waiting for delivery, and with ErrInvalid for UserData
// over MaxUserData or a Category not registered.
func Post(n Note) (PostResult, error) {
	b, err := ready()
	if err != nil {
		return PostResult{}, err
	}
	if len(n.UserData) > MaxUserData {
		return PostResult{}, fmt.Errorf("%w: UserData is %d bytes, more than %d", ErrInvalid, len(n.UserData), MaxUserData)
	}
	if n.Category != "" && !registered(n.Category) {
		return PostResult{}, fmt.Errorf("%w: category %q is not registered", ErrInvalid, n.Category)
	}
	ptk, err := slots.requests.Reserve(0)
	if err != nil {
		return PostResult{}, ErrBusy
	}
	pi, ok := takePending()
	if !ok {
		slots.requests.Cancel(ptk)
		return PostResult{}, ErrBusy
	}
	id := n.ID
	if id == "" {
		id = "gnui-" + strconv.FormatUint(uint64(svc.nonce), 36) + "-" + strconv.FormatUint(svc.seq.Add(1), 36)
	}
	res := PostResult{ID: id, Request: ptk}
	si, key, evicted := reserveFor(&n, id, &res)
	if len(evicted) > 0 {
		res.Evicted = evicted[0]
		b.remove(evicted)
	}
	setPending(pi, pending{kind: pendPost, ticket: ptk, slot: si, key: key})
	if err := b.add(&n, id, key, pi); err != nil {
		freePending(pi)
		slots.requests.Cancel(ptk)
		if si >= 0 {
			svc.mu.Lock()
			releaseSlot(si, key)
			svc.mu.Unlock()
		}
		return PostResult{}, err
	}
	return res, nil
}

// reserveFor frees the slot of a note n replaces and reserves one for n if it
// wants a response: the slot and its key (-1, 0 for none) and the IDs of the
// notes evicted for it.
func reserveFor(n *Note, id string, res *PostResult) (si int, key uint64, evicted []string) {
	svc.mu.Lock()
	defer svc.mu.Unlock()
	releaseID(id) // the system replaces the note shown with this ID
	if n.Category == "" && len(n.UserData) == 0 {
		return -1, 0, nil
	}
	tk, err := slots.responses.Reserve(0)
	for err != nil && svc.opts.EvictOldest {
		v := oldestSlot()
		if v < 0 {
			break
		}
		s := &slots.s[v]
		if vid := s.id; releaseSlot(v, s.k) {
			evicted = append(evicted, vid)
			slots.evicted.Add(1)
		}
		tk, err = slots.responses.Reserve(0)
	}
	if err == nil {
		key = uint64(svc.nonce)<<32 | uint64(uint32(svc.seq.Add(1))) // never 0: the nonce is odd
		if si = takeSlot(tk, key, id, n.Thread, time.Now().UnixNano()); si >= 0 {
			res.Reservation, res.Response = Reserved, tk
			return si, key, evicted
		}
	}
	slots.withoutActions.Add(1)
	res.Reservation = PostedWithoutActions
	return -1, 0, evicted
}

// Remove removes notes from Notification Center (and those not shown yet),
// freeing their response slots.
func Remove(ids ...string) error {
	b, err := ready()
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	svc.mu.Lock()
	for _, id := range ids {
		releaseID(id)
	}
	svc.mu.Unlock()
	b.remove(ids)
	return nil
}

// RemoveThread removes the notes of a thread (Note.Thread) shown so far,
// freeing their response slots: those of this run at once, and every other
// note of the thread in Notification Center (plain notes, earlier runs')
// once the system has listed them. A note posted after the call is kept.
func RemoveThread(thread string) error {
	b, err := ready()
	if err != nil {
		return err
	}
	now := time.Now().UnixNano()
	var ids []string
	svc.mu.Lock()
	for i := range slots.s {
		if s := &slots.s[i]; s.live && s.thread == thread && s.seq <= now {
			id := s.id
			if releaseSlot(i, s.k) {
				ids = append(ids, id)
			}
		}
	}
	svc.mu.Unlock()
	if len(ids) > 0 {
		b.remove(ids)
	}
	pi, ok := takePending()
	if !ok {
		return ErrBusy
	}
	setPending(pi, pending{kind: pendThread, slot: -1, thread: thread, before: now})
	if err := b.delivered(pi); err != nil {
		freePending(pi)
		return err
	}
	return nil
}

// Stats counts what the service did, for tests and diagnostics.
type Stats struct {
	// Reserved is the response slots held now.
	Reserved int
	// WithoutActions counts notes posted plain because every slot was taken.
	WithoutActions uint64
	// Evicted counts notes removed to free a slot (EvictOldest).
	Evicted uint64
	// Stored counts responses stored in their slot.
	Stored uint64
	// Plain counts responses to plain notes (nothing is delivered).
	Plain uint64
	// Lost counts responses that found no slot and no free record (a note of
	// an earlier run that did not fit), or arrived after the app's last
	// delivery.
	Lost uint64
}

// ReadStats returns the counts.
func ReadStats() Stats {
	st := Stats{
		WithoutActions: slots.withoutActions.Load(),
		Evicted:        slots.evicted.Load(),
		Stored:         slots.stored.Load(),
		Plain:          slots.plain.Load(),
		Lost:           slots.lost.Load(),
	}
	if slots.responses != nil {
		st.Reserved = slots.responses.Outstanding()
	}
	return st
}
