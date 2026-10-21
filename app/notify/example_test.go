package notify_test

import (
	"log"

	"gonativeui/app"
	"gonativeui/app/notify"
)

const (
	actReply int64 = iota + 1
	actRead
)

// chat is the application. It posts a note for a message, with a Reply
// action, and reads what the user did with it.
type chat struct{ app.DefaultHandler }

func (c *chat) Start(a *app.App) error {
	notify.RegisterCategories(notify.Category{ID: "message", Actions: []notify.Action{
		{ID: actReply, Title: "Reply", Reply: true},
		{ID: actRead, Title: "Mark as Read"},
	}})
	notify.RequestPermission(notify.Alert | notify.Sound) // the system asks the user once
	return nil
}

func (c *chat) Event(a *app.App, e *app.Event) {
	if r, ok := notify.ResponseOf(e); ok { // a click, an action or a typed reply
		switch r.Action {
		case notify.ActionClick: // bring the conversation forward: r.UserData names it
		case actReply:
			_ = r.Reply // the typed text, valid in this call only
		}
	}
	if r, ok := notify.ResultOf(e); ok && r.Kind == notify.ResultPermission && !r.Granted {
		// the user said no: keep the unread count in the app instead
	}
	a.DefaultEvent(e)
}

// Opting in to notifications. Setup comes before app.Main, so that a click
// that launched the app is not lost; it fails outside a bundled app and off
// macOS, and the app then runs without notifications. No call waits for the
// system: results and responses come back as app events (chat.Event above).
func Example() {
	if err := notify.Setup(notify.Options{Foreground: true}); err != nil {
		log.Print(err) // not a bundled app, or not macOS: run without notifications
	}
	app.Main(app.AppConfig{ID: "dev.example.chat"}, &chat{})
}

// Posting a note, from an event handler: it carries data that comes back with
// the user's response.
func ExamplePost() {
	res, err := notify.Post(notify.Note{
		Thread:   "chat-42",
		Title:    "Ana",
		Body:     "Lunch?",
		Category: "message",
		UserData: []byte("chat-42/msg-7"), // comes back in the Response
	})
	if err != nil { // notify.ErrBusy: keep the note and post it when a result arrives
		log.Print(err)
		return
	}
	_ = res.ID // Remove(res.ID) takes it off Notification Center
}
