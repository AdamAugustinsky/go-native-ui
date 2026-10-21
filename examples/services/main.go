// Command services is the example of the guide's chapter on services
// (docs/guide/services.md): a small window whose buttons use the services of
// package app/* — a notification with a reply action, the Dock badge, a status
// item, the Keychain, preferences, a login item — and which opens where it was
// closed.
//
//	services              open the window
//
// Notifications, the login item and the Dock need a signed bundle: run it with
//
//	cd examples/services && go run ../../cmd/gnui run
//
// A plain go run works for the rest and shows what each unavailable service
// answers.
package main

import (
	"fmt"
	"log"
	"os"

	"gonativeui/app"
	"gonativeui/app/notify"
)

func main() {
	// Before app.Main: the click that launched the app is not lost. Outside a
	// bundle, or off macOS, Setup says why and the app runs without
	// notifications.
	if err := notify.Setup(notify.Options{Foreground: true}); err != nil {
		log.Print("notifications: ", err)
	}
	p, err := newPings(appID, "")
	if err != nil {
		fmt.Fprintln(os.Stderr, "services:", err)
		os.Exit(1)
	}
	err = app.Main(app.AppConfig{ID: appID, KeepRunning: true}, p)
	// On macOS an allowed quit ends the process inside Main; the quit request
	// flushed both files. Elsewhere Main returns.
	p.prefs.Close()
	p.state.Close()
	if err != nil {
		fmt.Fprintln(os.Stderr, "services:", err)
		os.Exit(1)
	}
}
