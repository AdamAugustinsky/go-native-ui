package app

import (
	"testing"

	"gonativeui/platform"
	"gonativeui/ui"
)

// evBuilder is a Run's Builder that takes the app events (EventHandler).
type evBuilder struct{ got []Event }

func (b *evBuilder) Build(*ui.Context) {}

func (b *evBuilder) Event(a *App, e *Event) {
	b.got = append(b.got, *e)
	a.DefaultEvent(e)
}

// Under Run, the services' events reach the Builder's Event as they reach
// Main's Handler: a web view's, a hotkey's and screen capture's, offered
// into their rings off the main thread and delivered by the application
// object's dispatch. A Builder without Event gets DefaultEvent, which
// still answers a quit request.
func TestRunDeliversServiceEventsToTheBuilder(t *testing.T) {
	b := &evBuilder{}
	a := newApp()
	a.handler = runHandler(b) // what Run (openWindowed) gives the app
	a.started = true
	platform.SetAppSink(a)
	defer platform.SetAppSink(nil)

	srcs := []platform.Source{platform.SourceWebView, platform.SourceHotkey, platform.SourceCapture}
	for i, src := range srcs {
		r := platform.NewRing(src, 8, 0)
		done := make(chan bool)
		go func() { done <- r.OfferID(uint64(10+i), 3, 4, nil) }() // a service's thread
		if !<-done {
			t.Fatalf("source %d: the ring refused the event", src)
		}
	}
	for range 10 {
		platform.DispatchApp()
		if len(b.got) == len(srcs) {
			break
		}
	}
	if len(b.got) != len(srcs) {
		t.Fatalf("the Builder got %d events, want %d", len(b.got), len(srcs))
	}
	for i, e := range b.got {
		if e.Kind != EvService || e.Source != srcs[i] || e.ID != uint64(10+i) || e.A != 3 || e.B != 4 {
			t.Errorf("event %d: %+v, want EvService from source %d, ID %d, A 3, B 4", i, e, srcs[i], 10+i)
		}
	}

	if _, ok := runHandler(struct{ Builder }{b}).(DefaultHandler); !ok {
		t.Error("a Builder without Event does not get DefaultEvent")
	}
}
