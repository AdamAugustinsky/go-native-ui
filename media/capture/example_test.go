package capture_test

import (
	"errors"

	"gonativeui/app"
	"gonativeui/media/capture"
)

// bar is a recording bar: the system's picker, then the recording until
// the user stops it.
type bar struct {
	rec *capture.Recording
}

func (b *bar) record(a *app.App) {
	r, err := capture.Pick(a.Win, capture.Options{Microphone: true, SystemAudio: true})
	if errors.Is(err, errors.ErrUnsupported) {
		return // macOS 14, or no capture on this OS yet
	}
	b.rec = r
}

func (b *bar) event(a *app.App, e *app.Event) {
	ev, ok := capture.EventOf(e)
	if !ok || b.rec == nil || ev.Recording != b.rec.ID() || ev.Kind != capture.Ended {
		return
	}
	switch res := b.rec.Result(); {
	case errors.Is(res.Err, capture.ErrPickerCancelled):
	case errors.Is(res.Err, capture.ErrPermissionDenied):
	case res.Err == nil:
		_ = res.Path // upload it
	}
}

// Example records a window chosen in the system's picker.
func Example() {
	b := &bar{}
	_, _ = b.record, b.event
}
