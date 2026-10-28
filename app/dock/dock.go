// Package dock controls the app's Dock tile, or its taskbar buttons on
// Windows: the badge (an unread count), a progress bar, a bounce that asks
// for attention, and whether the app is in the Dock at all.
//
//	dock.SetBadgeCount(unread)        // "3"; 0 clears it
//	dock.SetProgress(0.4)             // a long task 40 % done; dock.Indeterminate, dock.NoProgress
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
//     SetProgress puts a content view in the tile: the app's icon with an
//     NSProgressIndicator bar along its bottom (an indeterminate one does
//     not animate: the Dock draws the tile when told to, not per frame);
//     NoProgress gives the tile back to the Dock.
//   - Windows (phase 8 S1): every window has a taskbar button, and the
//     app's badge and progress are put on each open window's button
//     (ITaskbarList3) and on the button of a window opened later. The badge
//     is an overlay icon drawn at the small-icon size (a red disc with the
//     label in white; a long label is drawn smaller), described by the label
//     for screen readers. SetProgress is ITaskbarList3's progress bar.
//     Bounce flashes the buttons (FlashWindowEx): once, the button staying
//     highlighted until the app is activated, or, critical, until it is;
//     with no window open there is nothing to flash and the id is 0. The
//     policy cannot change (SetPolicy and Policy return ErrUnsupported).
//   - Linux: not yet; every call returns ErrUnsupported (errors.Is
//     errors.ErrUnsupported).
package dock

import (
	"errors"
	"fmt"
	"math"
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
	// ErrProgress: SetProgress was given NaN or a negative value other than
	// Indeterminate and NoProgress.
	ErrProgress = errors.New("dock: the progress is not a fraction, Indeterminate or NoProgress")
)

// The values of SetProgress besides a fraction from 0 to 1.
const (
	// NoProgress removes the progress bar.
	NoProgress = -1.0
	// Indeterminate shows a busy bar with no amount done.
	Indeterminate = -2.0
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

// SetProgress shows how much of a long task is done on the app's icon: a
// fraction from 0 to 1 (more is 1), Indeterminate, or NoProgress to remove
// the bar. It is the app's (one bar for the app), not a window's.
func SetProgress(fraction float64) error {
	switch {
	case math.IsNaN(fraction), fraction < 0 && fraction != NoProgress && fraction != Indeterminate:
		return fmt.Errorf("%w: %v", ErrProgress, fraction)
	case fraction > 1:
		fraction = 1
	}
	return osSetProgress(fraction)
}

// Progress is what SetProgress last set: a fraction, Indeterminate or
// NoProgress.
func Progress() float64 { return progress }

// progress is the last value SetProgress applied. Main thread.
var progress = NoProgress

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
