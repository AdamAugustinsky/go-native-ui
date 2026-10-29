package login_test

import (
	"log"

	"gonativeui/app"
	"gonativeui/app/login"
)

// An app that opens at login should not open a window or jump in the Dock: a
// launch agent declared in gnui.json passes --start-hidden, and StartHidden
// tells the app so before app.Main.
func Example() {
	hidden := login.StartHidden()
	app.Main(app.AppConfig{ID: "dev.example.chat", KeepRunning: true, StartHidden: hidden}, &app.DefaultHandler{})
}

// Switching the login item on from Settings. The calls ask the system and
// can take a moment: call them from a task, not from a frame.
func ExampleItem_Enable() {
	st, err := login.Agent("hidden").Enable() // the agent gnui.json declares
	if err != nil {
		log.Print(err) // login.ErrUnsupported off macOS, ErrUnavailable outside a bundle
		return
	}
	if st == login.RequiresApproval {
		login.OpenSettings() // Login Items: the user switches the app on
	}
}

// On Windows the login item is a Run value. An app that had one keeps its
// name, so the user's choice in Task Manager carries over.
func ExampleRunValue() {
	st, err := login.RunValue("HyperPedia", login.StartHiddenArg).Enable()
	if err != nil {
		log.Print(err) // login.ErrUnsupported off Windows
		return
	}
	if st == login.RequiresApproval {
		login.OpenSettings() // Settings → Apps → Startup: the user switched it off
	}
}
