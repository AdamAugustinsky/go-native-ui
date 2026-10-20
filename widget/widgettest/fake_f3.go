//go:build gnui_f3

package widgettest

import (
	"strings"

	"gonativeui/app"
	"gonativeui/platform"
)

// This file needs the application object (phase 6, F3: app.Main, Handler,
// app.Event), and is built with the gnui_f3 tag until F3 is merged; then the
// tag line goes and these are ordinary.
//
// The application object delivers what the system does to the app as app
// events on the Handler. These deliver the same events without the system,
// from the test's goroutine of an offscreen app or from a Handler's own
// code, on the main thread: for flows that react to a notification's reply, a
// Dock click or a URL, in a test that has no AppKit to cause them.

// DeliverAppEvent delivers pe (with its data) to the app's Handler as the
// application object does, on the main thread.
func DeliverAppEvent(a *app.App, pe platform.AppEvent, data []byte) { a.AppEvent(&pe, data) }

// FakeNotificationResponse delivers the user's response to a notification as
// an EvService from platform.SourceNotifyResponse: id names the notification
// (the ticket or sequence the service gave it), action the button chosen (0:
// the notification itself was clicked) and reply the text typed into a reply
// action ("" for none; it arrives as Event.Data).
func FakeNotificationResponse(a *app.App, id uint64, action int64, reply string) {
	var data []byte
	if reply != "" {
		data = []byte(reply)
	}
	DeliverAppEvent(a, platform.AppEvent{Kind: platform.AppEvService, Source: platform.SourceNotifyResponse, ID: id, A: action}, data)
}

// FakeReopen delivers the Dock icon's click (EvReopen); visible says whether
// a window was on screen (Event.A is 1).
func FakeReopen(a *app.App, visible bool) {
	var v int64
	if visible {
		v = 1
	}
	DeliverAppEvent(a, platform.AppEvent{Kind: platform.AppEvReopen, A: v}, nil)
}

// FakeOpenURLs delivers an open-URL request (EvOpenURLs): A is the count and
// Event.Strings the URLs.
func FakeOpenURLs(a *app.App, urls ...string) {
	DeliverAppEvent(a, platform.AppEvent{Kind: platform.AppEvOpenURLs, A: int64(len(urls))}, []byte(strings.Join(urls, "\x00")))
}
