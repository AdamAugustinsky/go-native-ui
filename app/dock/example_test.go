package dock_test

import (
	"fmt"

	"gonativeui/app"
	"gonativeui/app/dock"
)

// The Dock tile: a badge with an unread count, a bounce that asks for
// attention, and whether the app is in the Dock at all. Every call is made on
// the main thread, from Handler.Start or Handler.Event; where there is no
// Dock it returns an error that matches errors.ErrUnsupported.
func Example() {
	dock.SetBadgeCount(3) // "3"; 0 clears it
	id, err := dock.Bounce(false)
	if err == nil {
		dock.CancelBounce(id) // e.g. when the app became active
	}

	dock.SetPolicy(app.PolicyAccessory) // out of the Dock and the app switcher: a resident app
	dock.SetPolicy(app.PolicyRegular)   // back in the Dock, before a window is shown again
}

// A count that does not fit the badge is capped, as the Dock does.
func ExampleBadgeLabel() {
	fmt.Println(dock.BadgeLabel(0) == "", dock.BadgeLabel(7), dock.BadgeLabel(1500))
	// Output: true 7 999+
}
