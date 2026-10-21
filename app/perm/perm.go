// Package perm reports and requests the user's permission to use the
// microphone and the camera (DECISIONS D45, D57).
//
//	st, err := perm.Status(perm.Microphone)
//	switch {
//	case err != nil:
//		// not macOS yet (ErrUnsupported), or AVFoundation is missing
//	case st == perm.NotDetermined:
//		tk, err := perm.Request(perm.Microphone) // the system asks the user
//		...
//	case st == perm.Denied || st == perm.Restricted:
//		perm.OpenSettings(perm.Microphone) // only the user can change it now
//	}
//
//	func (c *chat) Event(a *app.App, e *app.Event) {
//		if r, ok := perm.ResultOf(e); ok && r.Kind == perm.Microphone && r.Granted {
//			// start recording
//		}
//		a.DefaultEvent(e)
//	}
//
// # Requests
//
// Request never waits for the user. It reserves a ticket from a table of
// MaxRequests (ErrBusy when all of them wait for delivery), asks the system,
// and returns; the answer arrives later as an app.EvService from
// platform.SourcePerm whose ID is the ticket, which ResultOf decodes. The
// system asks the user once per app: once the user has answered, a request is
// answered at once with that answer, and only System Settings changes it
// (OpenSettings opens the right page).
//
// The system calls the completion on a thread of its own; it only stores the
// answer in the reserved record, and the app object delivers it on the main
// thread between the app's event handler calls (DECISIONS D37, class 2).
//
// # Usage descriptions
//
// macOS ends a process that asks for the microphone or the camera without a
// usage string in its Info.plist (NSMicrophoneUsageDescription,
// NSCameraUsageDescription; gnui.json's usage strings write them). Request
// checks the main bundle first and returns ErrNoUsageDescription instead of
// asking, so a development run outside a bundle fails with an error rather
// than a crash. A build signed with the hardened runtime (gnui package) also
// needs the entitlement com.apple.security.device.audio-input (the camera:
// com.apple.security.device.camera) in gnui.json's entitlements; without it
// the system refuses access without asking.
//
// # Threads and cost
//
// Every function may be called from any goroutine. The first call opens
// AVFoundation (a few milliseconds); call it after the first frame. Status
// is then one Objective-C message to the system.
//
// # What each OS does
//
//   - macOS: AVCaptureDevice's authorizationStatusForMediaType: and
//     requestAccessForMediaType:completionHandler:.
//   - Windows and Linux: not yet; every function returns ErrUnsupported
//     (errors.Is errors.ErrUnsupported).
package perm

import (
	"errors"
	"fmt"
	"sync"

	"gonativeui/app"
	"gonativeui/platform"
)

// Kind is a protected device.
type Kind uint8

// The protected devices.
const (
	// Microphone is audio input.
	Microphone Kind = iota + 1
	// Camera is video input.
	Camera
)

// String names the kind.
func (k Kind) String() string {
	switch k {
	case Microphone:
		return "microphone"
	case Camera:
		return "camera"
	}
	return fmt.Sprintf("perm.Kind(%d)", uint8(k))
}

func (k Kind) valid() bool { return k == Microphone || k == Camera }

// State is the user's answer so far. The values are AVFoundation's
// AVAuthorizationStatus.
type State uint8

// The states.
const (
	// NotDetermined: the user has not been asked yet; Request asks.
	NotDetermined State = iota
	// Restricted: a policy (parental controls, a configuration profile)
	// forbids access; the user cannot grant it.
	Restricted
	// Denied: the user said no; only System Settings changes it.
	Denied
	// Authorized: the user said yes.
	Authorized
)

// String names the state.
func (s State) String() string {
	switch s {
	case NotDetermined:
		return "not determined"
	case Restricted:
		return "restricted"
	case Denied:
		return "denied"
	case Authorized:
		return "authorized"
	}
	return fmt.Sprintf("perm.State(%d)", uint8(s))
}

// Ticket identifies a request; it is the ID of the EvService that answers it.
type Ticket = platform.Ticket

// MaxRequests is how many requests can wait for their answers at once.
const MaxRequests = 16

var (
	// ErrUnsupported: this OS has no permission service here yet. It matches
	// errors.ErrUnsupported.
	ErrUnsupported = fmt.Errorf("perm: %w", errors.ErrUnsupported)
	// ErrUnavailable: the system framework could not be loaded.
	ErrUnavailable = errors.New("perm: AVFoundation is unavailable")
	// ErrInvalid: a Kind that is not Microphone or Camera.
	ErrInvalid = errors.New("perm: invalid kind")
	// ErrBusy: MaxRequests requests are waiting for delivery; nothing was
	// asked. Try again after results are delivered.
	ErrBusy = fmt.Errorf("perm: %w", platform.ErrBusy)
	// ErrNoUsageDescription: the app's Info.plist has no usage string for
	// this kind, and macOS would end the process if it asked; nothing was
	// asked.
	ErrNoUsageDescription = errors.New("perm: the app's Info.plist has no usage description for this device")
)

// Result is the answer to a Request.
type Result struct {
	// Ticket is the request's (Request's return value).
	Ticket Ticket
	// Kind is the device asked about.
	Kind Kind
	// Granted is the user's answer: true when access is authorized.
	Granted bool
	// State is the state after the answer.
	State State
}

// ResultOf decodes a request's answer (an app.EvService from
// platform.SourcePerm). It allocates nothing.
func ResultOf(e *app.Event) (Result, bool) {
	if e == nil || e.Kind != app.EvService || e.Source != platform.SourcePerm {
		return Result{}, false
	}
	return decode(e.ID, e.A, e.B), true
}

// encode packs a result into an event's A and B; decode unpacks it.
func encode(k Kind, granted bool, s State) (a, b int64) {
	a = int64(k)
	if granted {
		a |= 1 << 8
	}
	return a, int64(s)
}

func decode(id uint64, a, b int64) Result {
	return Result{Ticket: Ticket(id), Kind: Kind(a & 0xFF), Granted: a&(1<<8) != 0, State: State(b)}
}

// Status reports the user's answer so far for k.
func Status(k Kind) (State, error) {
	if !k.valid() {
		return NotDetermined, ErrInvalid
	}
	return osStatus(k)
}

// Request asks the user for access to k, unless the user has answered
// already, and returns the ticket its Result will carry. See the package
// comment.
func Request(k Kind) (Ticket, error) {
	if !k.valid() {
		return 0, ErrInvalid
	}
	if err := osPrepare(k); err != nil {
		return 0, err
	}
	tk, err := requests().Reserve(0)
	if err != nil {
		return 0, ErrBusy
	}
	if err := osRequest(tk, k); err != nil {
		requests().Cancel(tk)
		return 0, err
	}
	return tk, nil
}

// OpenSettings opens the System Settings page where the user grants or
// revokes access to k.
func OpenSettings(k Kind) error {
	if !k.valid() {
		return ErrInvalid
	}
	return osOpenSettings(k)
}

// requests is the completion table, made on first use.
var requests = sync.OnceValue(func() *platform.Tickets {
	return platform.NewTickets(platform.SourcePerm, MaxRequests, 0)
})
