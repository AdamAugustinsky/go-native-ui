//go:build !darwin

package capture_test

import (
	"errors"
	"testing"

	"gonativeui/media/capture"
)

// TestUnsupported: no capture here yet (Windows until M2, Linux, D63).
func TestUnsupported(t *testing.T) {
	if _, err := capture.Pick(nil, capture.Options{}); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("Pick = %v", err)
	}
	if _, err := capture.Start(capture.Target{Kind: capture.Display}, capture.Options{Microphone: true}); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("Start = %v", err)
	}
	if _, err := capture.Targets(capture.Window); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("Targets = %v", err)
	}
}
