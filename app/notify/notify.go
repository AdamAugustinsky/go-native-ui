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
// ErrBusy and nothing is sent. Results are delivered between the app's event
// handler calls, so a handler that posts more notes than that at once gets
// ErrBusy too: it should keep the rest and post them as results arrive, never
// retry in a loop inside the handler (nothing is delivered until it
// returns). The answer arrives as an app.EvService from platform.SourceNotify
// whose ID is the ticket; ResultOf decodes it. One ResultRestored result also
// arrives after Setup (below).
//
// # Responses and their slots
//
// A note with UserData or a Category reserves a response slot when it is
// sent, from a table of MaxResponses. When the user clicks it, picks an
// action or sends a reply, the delegate stores the action, the reply text (up
// to MaxReply bytes) and the note's UserData in that slot before it tells the
// system the response was handled, so a reply the system considers delivered
// cannot be lost on its way to the app. Nothing the app does (app.Post, other
// requests) can take the room a response needs. A response to a note that
// holds no slot (one removed or evicted just before the user answered, one
// of an earlier run that got none back) takes a spare record, or any free
// one; with none free, the delegate keeps the system's completion handler and
// answers the system only once a record has freed and the response is
// stored.
//
// A response arrives as an app.EvService from platform.SourceNotifyResponse,
// which ResponseOf decodes: its ID is the ticket of the slot it came through
// (PostResult.Response), A is the action, an int64 (ActionClick,
// ActionDismiss, ActionOther or the chosen Action.ID, which the system knows
// as "gnui.action.<ID>"), B is the length of the UserData, and the data is
// the reply text followed by the UserData.
//
// A slot is freed when its response has been delivered, or when its note is
// gone: removed (Remove, RemoveThread, EvictOldest), replaced by a later note
// with the same ID (once the system has taken that one), or cleared by the
// user from Notification Center. The system does not report a clearing
// (unless the note's category has NotifyDismiss, which makes it an
// ActionDismiss response); the service asks the system which notes it still
// has when the app becomes active and when a post finds every slot taken, and
// frees the slots of the notes it took before asking and no longer has
// (Stats.Reconciled). It never polls.
//
// When every slot is taken even so, the note is posted as a plain banner,
// without its actions, reply field and UserData, whose click only brings the
// app forward: PostedWithoutActions. With Options.EvictOldest the oldest
// note holding a slot is removed from Notification Center instead, and the
// new note gets its slot (PostResult.Evicted names the one removed). A post
// that fails before it is sent (its attachment, say) evicts nothing; one the
// system refuses later does not bring the evicted note back.
//
// A note without UserData or a Category is plain from the start
// (NoResponse): clicking it brings the app forward and delivers nothing.
//
// Post knows the reservation when it returns, except for a note with an
// Attachment (the file is copied first, off the caller's goroutine) and one
// posted with every slot taken (the service first asks the system which
// notes it still has). Its Reservation is Pending then, and the ResultPost
// says what the note got; every ResultPost says it. A post superseded before
// it could be sent, by a later Post or a Remove of its ID, is not sent: its
// ResultPost has CodeSuperseded.
//
// # Launches
//
// Setup installs the system delegate, and it must be called before app.Main:
// the click that launched the app reaches the delegate while the application
// finishes launching (and the app after Handler.Start returns). Called once
// launching has finished (from Handler.Start, say), Setup returns ErrTooLate
// and does nothing; after a successful Setup, a later call only changes the
// options. Notes posted by an earlier run and still in Notification Center
// get their slots back when Setup's listing arrives, newest first; the
// ResultRestored result says how many, and names the ones that did not get
// one, which the app may Remove. A response to one of those is still
// delivered.
//
// # Threads and cost
//
// Setup must be called on the main thread. It opens UserNotifications and
// connects to the system's center before the first frame, the one framework
// the app object loads early for an app that opts in: 3 to 10 ms on a Mac
// mini (macOS 27.2). It also makes the response tables, kept for the
// process: MaxResponses records of MaxReply+MaxUserData bytes and 16 spare
// ones, about 1.2 MiB of buffers without pointers, which nothing writes
// until a response is stored there (0.2 to 0.45 MiB of it resident after
// Setup, on that Mac). The other functions may be called from any
// goroutine. The delegate costs nothing while idle: the system calls it only
// for a note that is shown or answered, on a thread of its own, and it only
// copies into the reserved record. Delivering a response allocates nothing
// once the slots are warm, unless it has to wait for a record.
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
	"sync"
	"sync/atomic"
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
	// ErrTooLate: Setup was called after the application finished launching
	// (call it before app.Main); nothing was set up.
	ErrTooLate = errors.New("notify: Setup after the application finished launching (call it before app.Main)")
	// ErrNotSetUp: Setup has not succeeded yet.
	ErrNotSetUp = errors.New("notify: Setup has not been called")
	// ErrBusy: every request ticket is waiting for its result to be
	// delivered; nothing was sent. Try again after results are delivered,
	// not in a loop inside an event handler.
	ErrBusy = fmt.Errorf("notify: %w", platform.ErrBusy)
	// ErrInvalid: a note, category or action the system cannot take (the
	// error says what).
	ErrInvalid = errors.New("notify: invalid")
)

// The ErrCode of a ResultPost for a note that failed before the system had
// it. The system's own codes are positive.
const (
	// CodeSuperseded: a later Post of the note's ID, or a Remove of it, came
	// before the note could be sent; it was not sent.
	CodeSuperseded int64 = -2
	// CodeAttachment: the Attachment could not be copied, or the system
	// refused it.
	CodeAttachment int64 = -3
	// CodeInvalid: the system could not make the note (Err says why).
	CodeInvalid int64 = -4
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
	// note; it is copied (the system takes the file it is given), off the
	// caller's goroutine.
	Attachment string
	// Category names a registered category whose actions the note shows.
	Category string
	// UserData comes back in the note's Response (at most MaxUserData
	// bytes). A note with UserData or a Category reserves a response slot.
	UserData []byte
}

// Reservation says what a post reserved for the note's response.
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
	// Pending: decided when the note is sent, after its Attachment is copied
	// or the system has said which notes it still has; the ResultPost says.
	Pending
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
	// ResultPost answers Post: Reservation, Response and Evicted (what the
	// note got), and ErrCode and Err, 0 and "" when the system took the
	// note. A note that failed holds no slot.
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
	// Reservation, Response and Evicted are what a post reserved, as in
	// PostResult (ResultPost); Response and Evicted are empty for a post
	// that failed.
	Reservation Reservation
	// Response is the ticket of the note's slot.
	Response Ticket
	// Evicted is the ID of the note EvictOldest removed for it.
	Evicted string
	// ErrCode and Err are the error's code and message, 0 and "" for none:
	// the system's code, or one of the Code constants.
	ErrCode int64
	// Err is the error's message.
	Err string
	// Restored and Overflow count the notes of earlier runs that got their
	// slots back and that did not (ResultRestored).
	Restored, Overflow int
	// OverflowIDs are the IDs of the notes that did not get one.
	OverflowIDs []string
}

// Response is the user's response to a note (ResponseOf). Its byte slices
// are the event's data: valid during the Handler.Event call only.
type Response struct {
	// Ticket identifies the record the response came through: the note's
	// PostResult.Response while the note held its slot; another ticket for a
	// note that held none (of an earlier run, or removed just before the
	// user answered).
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

// decodeResult decodes a result: a is the kind (ResultPost: the reservation
// in bits 8 to 15, postFailed), b and the data depend on it.
func decodeResult(id uint64, a, b int64, data []byte) Result {
	r := Result{Kind: ResultKind(a), Ticket: Ticket(id)}
	switch r.Kind {
	case ResultPermission:
		r.Granted = b != 0
		r.Err = string(data)
	case ResultStatus:
		r.Status = AuthStatus(b)
	case ResultPost:
		r.Reservation = Reservation(a >> 8)
		if a&postFailed != 0 {
			r.ErrCode, r.Err = b, string(data)
		} else {
			r.Response, r.Evicted = Ticket(b), string(data)
		}
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
	// launched: the application has finished launching (Setup is too late).
	launched() bool
	// start installs the delegate (main thread).
	start() error
	authorize(tk Ticket, p Permission) error
	settings(tk Ticket) error
	categories(cats []Category) error
	// prepare makes the request posting n as id, with the attachment file
	// att ("" for none): with key it carries the key, n's category and
	// UserData, without it is plain. Nothing is sent: send sends it (its
	// result calls added(ctx, …)), discard drops it.
	prepare(n *Note, id string, key uint64, att string, ctx int) (prepared, error)
	send(r prepared)
	discard(r prepared)
	// remove removes delivered and pending notes.
	remove(ids []string)
	// pending lists the notes not shown yet, delivered those in
	// Notification Center: listed(ctx, …).
	pending(ctx int) error
	delivered(ctx int) error
}

// prepared is a backend's request, made and not sent.
type prepared any

var svc struct {
	setupMu sync.Mutex // Setup
	b       backend
	on      atomic.Bool // Setup succeeded

	foreground atomic.Bool
	restoring  atomic.Bool // the launch's restore has not run yet

	// order: a native change (sending, removing) with the bookkeeping it
	// follows from. Taken before mu.
	order sync.Mutex

	mu          sync.Mutex // the post side: the slots' bookkeeping, the options
	opts        Options
	ids         map[string]*entry
	gen         uint64 // the last post's generation
	accepts     uint64 // the notes the system took
	listings    int    // listings that hold the entries (restore, RemoveThread)
	threadCuts  []threadCutAt
	reconciling bool
	waiting     []*post // posts waiting for the reconciliation

	catMu sync.Mutex // the registered categories, in order
	cats  []Category

	nonce uint32        // generated IDs: "gnui-<nonce>-<n>"
	idSeq atomic.Uint64 // their n
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
// launched the app is delivered: once the application has finished
// launching it returns ErrTooLate and sets nothing up. After a successful
// Setup a later call only changes the options. It returns ErrUnsupported on
// an OS without notifications here and ErrUnavailable when the process is
// not a bundled app.
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
	if !svc.on.Load() && b.launched() {
		return ErrTooLate
	}
	svc.mu.Lock()
	svc.opts = o
	svc.mu.Unlock()
	svc.foreground.Store(o.Foreground)
	if svc.on.Load() {
		return nil
	}
	initSlots()
	svc.mu.Lock()
	if svc.ids == nil {
		svc.ids = map[string]*entry{}
	}
	if svc.nonce == 0 {
		svc.nonce, keys.base = rand.Uint32()|1, rand.Uint64()
	}
	svc.listings++ // the restore's
	svc.mu.Unlock()
	svc.restoring.Store(true) // before the delegate can be called
	if err := b.start(); err != nil {
		endRestore()
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
		endRestore()
		return
	}
	pi, ok := takePending()
	if !ok {
		slots.requests.Cancel(tk)
		endRestore()
		return
	}
	setPending(pi, pending{kind: pendRestore, ticket: tk, slot: -1})
	if err := svc.b.delivered(pi); err != nil {
		if _, ok := endPending(pi); ok {
			endRestore()
			slots.requests.Complete(tk, int64(ResultRestored), 0, nil)
		}
	}
}

// endRestore ends a restore that will not run.
func endRestore() {
	svc.mu.Lock()
	svc.restoring.Store(false)
	svc.threadCuts = nil
	endListing()
	svc.mu.Unlock()
}

// appActive is the app becoming active: the user may have cleared notes.
func appActive() { reconcileSoon() }

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

// Stats counts what the service did, for tests and diagnostics.
type Stats struct {
	// Reserved is the slot records held now: for notes, and for responses
	// not delivered yet.
	Reserved int
	// Parked is the responses waiting for a free record now.
	Parked int
	// WithoutActions counts notes posted plain because every slot was taken.
	WithoutActions uint64
	// Evicted counts notes removed to free a slot (EvictOldest).
	Evicted uint64
	// Reconciled counts slots freed because the system no longer had their
	// note (the user cleared it).
	Reconciled uint64
	// Superseded counts posts not sent because a later Post or a Remove of
	// their ID came first.
	Superseded uint64
	// Stored counts responses stored.
	Stored uint64
	// Late counts the responses stored without their note's slot (a spare
	// or another free record), parked ones included.
	Late uint64
	// Plain counts responses to plain notes (nothing is delivered).
	Plain uint64
}

// ReadStats returns the counts.
func ReadStats() Stats {
	st := Stats{
		WithoutActions: slots.withoutActions.Load(),
		Evicted:        slots.evicted.Load(),
		Reconciled:     slots.reconciled.Load(),
		Superseded:     slots.superseded.Load(),
		Stored:         slots.stored.Load(),
		Late:           slots.late.Load(),
		Plain:          slots.plain.Load(),
	}
	if slots.responses != nil {
		st.Reserved = slots.responses.Outstanding()
	}
	parking.mu.Lock()
	st.Parked = len(parking.list)
	parking.mu.Unlock()
	return st
}
