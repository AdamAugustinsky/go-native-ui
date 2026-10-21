// Package dock controls the app's Dock tile: the badge (an unread count), a
// bounce that asks for attention, and whether the app is in the Dock at all.
//
//	dock.SetBadgeCount(unread)        // "3"; 0 clears it
//	id, _ := dock.Bounce(false)       // one bounce; true bounces until the app is active
//	dock.CancelBounce(id)
//
//	dock.SetPolicy(app.PolicyAccessory) // out of the Dock and the app switcher: a resident app
//	dock.SetPolicy(app.PolicyRegular)   // back in the Dock, before a window is shown again
//
// The Dock's menu is the app's: App.SetDockMenu takes a menu.Model, and its
// commands arrive as EvCommand.
//
// # Threads
//
// Every call is made on the main thread: from Handler.Start or Handler.Event,
// or at a safe point (App.AtSafePoint). Elsewhere a result that should reach
// the Dock is first sent to the app (app.Post).
//
// # What each OS does
//
//   - macOS: NSApplication's dockTile (badgeLabel), requestUserAttention: and
//     cancelUserAttentionRequest:, and setActivationPolicy:. The launch
//     policy is AppConfig.Policy; SetPolicy changes it while the app runs.
//     Switching to PolicyRegular does not bring the app forward: show or
//     activate the window that follows.
//   - Windows and Linux: not yet; every call returns ErrUnsupported (errors.Is
//     errors.ErrUnsupported).
package dock

import (
	"errors"
	"fmt"
	"strconv"

	"gonativeui/app"
)

var (
	// ErrUnsupported: this OS has no Dock here yet. It matches
	// errors.ErrUnsupported.
	ErrUnsupported = fmt.Errorf("dock: %w", errors.ErrUnsupported)
	// ErrNotStarted: the application object does not exist yet (the call came
	// before app.Main).
	ErrNotStarted = errors.New("dock: the app has not started (call from Handler.Start or later)")
	// ErrPolicy: the system refused the activation policy, or it is not one
	// the Dock offers (PolicyProhibited).
	ErrPolicy = errors.New("dock: the activation policy cannot be set")
)

// Attention names one request for the user's attention (a bounce). 0 is no
// request: the app was already active, so the system bounced nothing.
type Attention int

// MaxBadge is the largest count SetBadgeCount shows as itself; a larger one
// shows "999+".
const MaxBadge = 999

// SetBadge shows label on the Dock icon; "" removes the badge.
func SetBadge(label string) error { return osSetBadge(label) }

// SetBadgeCount shows n on the Dock icon; 0 or less removes the badge, and
// more than MaxBadge shows "999+".
func SetBadgeCount(n int) error { return osSetBadge(BadgeLabel(n)) }

// BadgeLabel is the text SetBadgeCount shows for n ("" for none).
func BadgeLabel(n int) string {
	switch {
	case n <= 0:
		return ""
	case n > MaxBadge:
		return strconv.Itoa(MaxBadge) + "+"
	}
	return strconv.Itoa(n)
}

// Badge is the label the Dock icon shows now, "" for none.
func Badge() string { return osBadge() }

// Bounce makes the Dock icon bounce. critical bounces until the user
// activates the app; otherwise it bounces once. The result names the request
// for CancelBounce, and is 0 (with no error) when the app is active, which
// the system does not bounce for.
func Bounce(critical bool) (Attention, error) { return osBounce(critical) }

// CancelBounce ends a request for attention (the icon stops bouncing). An
// id of 0, or one already over, does nothing.
func CancelBounce(id Attention) {
	if id != 0 {
		osCancelBounce(id)
	}
}

// SetPolicy changes the activation policy of the running app: PolicyRegular
// (a Dock icon and a menu bar) or PolicyAccessory (neither; the app may still
// show windows).
func SetPolicy(p app.Policy) error {
	if p != app.PolicyRegular && p != app.PolicyAccessory {
		return fmt.Errorf("%w: %d", ErrPolicy, p)
	}
	return osSetPolicy(p)
}

// Policy is the activation policy the app has now.
func Policy() (app.Policy, error) { return osPolicy() }
