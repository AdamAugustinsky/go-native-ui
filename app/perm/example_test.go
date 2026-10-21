package perm_test

import (
	"gonativeui/app"
	"gonativeui/app/perm"
)

// Asking for the microphone before recording a voice note.
func Example() {
	st, err := perm.Status(perm.Microphone)
	switch {
	case err != nil:
		// perm.ErrUnsupported outside macOS
	case st == perm.NotDetermined:
		if _, err := perm.Request(perm.Microphone); err != nil {
			// perm.ErrNoUsageDescription: Info.plist lacks NSMicrophoneUsageDescription
		}
	case st == perm.Denied:
		perm.OpenSettings(perm.Microphone)
	}
}

// event is the app's Handler.Event.
func event(a *app.App, e *app.Event) {
	if r, ok := perm.ResultOf(e); ok && r.Kind == perm.Microphone && r.Granted {
		// start recording
	}
	a.DefaultEvent(e)
}
