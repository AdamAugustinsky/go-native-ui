//go:build !darwin && !windows

package capture_test

import (
	"errors"
	"testing"

	"gonativeui/media/capture"
)

// TestUnsupported: no capture on Linux yet (D63).
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
