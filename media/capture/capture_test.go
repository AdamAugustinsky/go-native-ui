package capture_test

import (
	"errors"
	"testing"

	"gonativeui/app"
	"gonativeui/media/capture"
	"gonativeui/platform"
)

// TestTypedErrors: the typed errors stay distinct, and none is
// ErrUnsupported.
func TestTypedErrors(t *testing.T) {
	typed := []error{capture.ErrPermissionDenied, capture.ErrPickerCancelled, capture.ErrNoEncoder, capture.ErrDiskFull,
		capture.ErrBusy, capture.ErrTargetGone, capture.ErrNotRecording}
	for i, a := range typed {
		if errors.Is(a, errors.ErrUnsupported) {
			t.Errorf("%v matches ErrUnsupported", a)
		}
		for j, b := range typed {
			if i != j && errors.Is(a, b) {
				t.Errorf("%v matches %v", a, b)
			}
		}
	}
}

func TestEventOf(t *testing.T) {
	ev, ok := capture.EventOf(&app.Event{Kind: app.EvService, Source: platform.SourceCapture, ID: 99, A: int64(capture.Ended), B: 4})
	if !ok || ev.Kind != capture.Ended || ev.Recording != 4 {
		t.Fatalf("EventOf = %+v %v", ev, ok)
	}
	if _, ok := capture.EventOf(&app.Event{Kind: app.EvService, Source: platform.SourceMedia}); ok {
		t.Fatal("a media event decoded as capture")
	}
}
