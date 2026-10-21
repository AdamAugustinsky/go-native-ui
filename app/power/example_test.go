package power_test

import (
	"gonativeui/app"
	"gonativeui/app/power"
)

type chat struct{ app.DefaultHandler }

func (c *chat) Start(a *app.App) error {
	power.Observe() // also the screens' sleep and wake
	return nil
}

func (c *chat) Event(a *app.App, e *app.Event) {
	if ev, ok := power.EventOf(e); ok && ev == power.DidWake {
		// reconnect: the network was cut while the machine slept
	}
	a.DefaultEvent(e)
}

// Keeping the machine awake for a call, and learning when it sleeps and wakes.
// A hold stops idle sleep (it does not stop a closed lid or a sleep the user
// asks for) until it is released; the reason is what the system shows in
// Activity Monitor.
func Example() {
	a, err := power.Hold(power.PreventIdleSleep, "Call with Ana") // a call starts
	if err == nil {
		defer a.Release() // the call ends
	}
	app.Main(app.AppConfig{ID: "dev.example.chat"}, &chat{})
}
