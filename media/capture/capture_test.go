package capture_test

import (
	"errors"
	"testing"

	"gonativeui/app"
	"gonativeui/media/capture"
	"gonativeui/platform"
)

// TestContract is the wave-0 behaviour: every entry point is unsupported
// until M1 and M2, and the typed errors stay distinct.
func TestContract(t *testing.T) {
	if _, err := capture.Pick(nil, capture.Options{}); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("Pick = %v", err)
	}
	if _, err := capture.Start(capture.Target{Kind: capture.Display}, capture.Options{Microphone: true}); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("Start = %v", err)
	}
	if _, err := capture.Targets(capture.Window); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("Targets = %v", err)
	}
	typed := []error{capture.ErrPermissionDenied, capture.ErrPickerCancelled, capture.ErrNoEncoder, capture.ErrDiskFull, capture.ErrBusy, capture.ErrTargetGone}
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
	ev, ok := capture.EventOf(&app.Event{Kind: app.EvService, Source: platform.SourceCapture, ID: 4, A: int64(capture.Ended)})
	if !ok || ev.Kind != capture.Ended || ev.Recording != 4 {
		t.Fatalf("EventOf = %+v %v", ev, ok)
	}
	if _, ok := capture.EventOf(&app.Event{Kind: app.EvService, Source: platform.SourceMedia}); ok {
		t.Fatal("a media event decoded as capture")
	}
}
