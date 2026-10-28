package webview_test

import (
	"errors"

	"gonativeui/app"
	"gonativeui/app/webview"
)

// browser is a pane that shows the user's dev server, with a persistent
// profile of its own.
type browser struct {
	v *webview.View
}

func (b *browser) open(a *app.App) {
	v, err := webview.New(a.Win, webview.Options{
		Profile:     "a3-browser",
		DownloadDir: "/tmp",
		NewWindows:  webview.NewWindowInPlace,
	})
	if errors.Is(err, webview.ErrUnavailable) || errors.Is(err, errors.ErrUnsupported) {
		return // open pages in the system browser instead
	}
	if err != nil {
		return
	}
	b.v = v
	_ = v.Navigate("http://localhost:5173/")
}

func (b *browser) event(e *app.Event) {
	if ev, ok := webview.EventOf(e); ok && b.v != nil && ev.View == b.v.ID() {
		switch ev.Kind {
		case webview.TitleChanged:
			_ = b.v.State().Title
		case webview.DownloadEnded:
			_, _ = b.v.Download(ev.Download)
		}
	}
}

// Example hosts the system's browser view in a window and follows its
// events.
func Example() {
	b := &browser{}
	_ = b.open
	_ = b.event
}
