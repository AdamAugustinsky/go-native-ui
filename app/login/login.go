// Package login makes the app, or a launch agent bundled with it, start when
// the user logs in.
//
//	func main() {
//		hidden := login.StartHidden() // before app.Main: pick the launch policy
//		...
//	}
//
//	st, err := login.Agent("hidden").Enable() // the agent gnui.json declares: starts the app with --start-hidden
//	if st == login.RequiresApproval {
//		login.OpenSettings() // Login Items: the user switches the app on
//	}
//
// # Start hidden
//
// An app that opens at login should not open a window or jump in the Dock.
// The main app's login item (App) launches it with no arguments, so it cannot
// start hidden. A launch agent can pass arguments: declare one in gnui.json,
//
//	"loginAgents": [{"name": "hidden", "arguments": ["--start-hidden"]}]
//
// and gnui package (and gnui run) writes its launchd property list into the
// bundle, with the label "<bundle id>.hidden" and the bundle's own executable
// as the program, both from the bundle id in use (so a development bundle,
// whose id ends in ".dev", registers its own agent). Agent("hidden") is that
// agent. The app learns it was started that way from StartHidden (and
// app.App.StartHidden, which also reports AppConfig.StartHidden): windows
// opened in Start stay hidden until EvReopen or Win.Show.
//
// # Calls
//
// Status, Enable and Disable ask the system's background-items service and
// may take a moment: call them from a task, not from a frame. They are safe
// from any goroutine.
//
// # Approval
//
// macOS lets the user decide which login items run. The first Enable of an
// item registers it and returns RequiresApproval: the system tells the user,
// who switches the item on in System Settings → General → Login Items
// (OpenSettings opens the pane). Enable again, or Status, tells when that
// happened. Registration needs a signed bundle in a place the system can
// see it; gnui run and gnui package make one.
//
// # What each OS does
//
//   - macOS 13 and later: ServiceManagement's SMAppService (mainAppService for
//     App, agentServiceWithPlistName: for Agent). It works only in a bundled
//     app; elsewhere every call returns ErrUnavailable.
//   - Windows and Linux: not yet; every call returns ErrUnsupported (errors.Is
//     errors.ErrUnsupported). StartHidden works everywhere.
package login

import (
	"errors"
	"fmt"
	"os"
)

var (
	// ErrUnsupported: this OS has no login items here yet. It matches
	// errors.ErrUnsupported.
	ErrUnsupported = fmt.Errorf("login: %w", errors.ErrUnsupported)
	// ErrUnavailable: the system cannot register this process: it is not a
	// bundled app with an identifier, or the system is older than macOS 13.
	ErrUnavailable = errors.New("login: unavailable (not a bundled app, or macOS older than 13)")
	// ErrNotFound: the bundle has no launch agent of that name (its gnui.json
	// has no loginAgents entry with it, or the bundle was built before it
	// had).
	ErrNotFound = errors.New("login: the bundle has no such launch agent")
	// ErrInvalid: an agent name that gnui.json could not have declared.
	ErrInvalid = errors.New("login: invalid launch agent name")
)

// State is the registration state of a login item.
type State uint8

// The states, in the order of SMAppServiceStatus.
const (
	// NotRegistered: the item is not a login item (the user can still add it
	// by hand in System Settings).
	NotRegistered State = iota
	// Enabled: the item starts at login.
	Enabled
	// RequiresApproval: registered, but the user has not switched it on in
	// System Settings → Login Items (or switched it off).
	RequiresApproval
	// NotFound: the system cannot find the item: the bundle does not hold the
	// agent's property list, or the app is not where the system sees it. It
	// is also what a bundle the system has not registered anything of yet
	// reports for its agents, so a first Status before any Enable may say
	// NotFound; Enable tells the two apart (ErrNotFound).
	NotFound
)

// String is the state in words: "enabled", "requires approval".
func (s State) String() string {
	switch s {
	case NotRegistered:
		return "not registered"
	case Enabled:
		return "enabled"
	case RequiresApproval:
		return "requires approval"
	case NotFound:
		return "not found"
	}
	return fmt.Sprintf("login.State(%d)", uint8(s))
}

// StartHiddenArg is the argument of a launch agent that starts the app
// hidden; app.Main reads it too.
const StartHiddenArg = "--start-hidden"

// StartHidden reports whether this process was started with --start-hidden,
// that is, by a login agent that asks the app to open no window. It reads
// only the arguments, so it can be called before app.Main (to choose
// AppConfig.Policy, say).
func StartHidden() bool {
	for _, a := range os.Args[1:] {
		if a == StartHiddenArg {
			return true
		}
	}
	return false
}

// Item is one login item: the app itself (App) or a bundled launch agent
// (Agent).
type Item struct {
	agent string // "" for the app
}

// App is the app's own login item: it starts the app at login with no
// arguments.
func App() Item { return Item{} }

// Agent is the bundled launch agent that gnui.json's loginAgents declares
// under name. An invalid name gives an Item whose calls fail with ErrInvalid.
func Agent(name string) Item {
	if !validAgent(name) {
		return Item{agent: "\x00" + name}
	}
	return Item{agent: name}
}

// validAgent reports whether name is ASCII letters, digits and hyphens, not
// starting with a hyphen (and not empty). Plain code: regexp would add its
// weight to every binary.
func validAgent(name string) bool {
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' && i > 0 {
			continue
		}
		return false
	}
	return name != ""
}

func (i Item) check() error {
	if len(i.agent) > 0 && i.agent[0] == 0 {
		return fmt.Errorf("%w: %q", ErrInvalid, i.agent[1:])
	}
	return nil
}

// Status is the item's registration state.
func (i Item) Status() (State, error) {
	if err := i.check(); err != nil {
		return NotFound, err
	}
	return osStatus(i.agent)
}

// Enable registers the item as a login item and returns the state it is in:
// Enabled, or RequiresApproval when the user has still to switch it on. An
// item already registered is left as it is. ErrNotFound means there is
// nothing to register.
func (i Item) Enable() (State, error) {
	if err := i.check(); err != nil {
		return NotFound, err
	}
	return osEnable(i.agent)
}

// Disable removes the item from the login items. An item that is not
// registered is not an error. It returns the state afterwards.
func (i Item) Disable() (State, error) {
	if err := i.check(); err != nil {
		return NotFound, err
	}
	return osDisable(i.agent)
}

// Status is App().Status().
func Status() (State, error) { return App().Status() }

// Enable is App().Enable().
func Enable() (State, error) { return App().Enable() }

// Disable is App().Disable().
func Disable() (State, error) { return App().Disable() }

// OpenSettings opens System Settings at the Login Items pane, where the user
// approves a login item.
func OpenSettings() error { return osOpenSettings() }
