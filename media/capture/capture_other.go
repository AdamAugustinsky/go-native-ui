package capture

import (
	"math"

	"gonativeui/platform"
)

// No capture here yet: M1 brings ScreenCaptureKit on macOS 15 and M2
// Windows.Graphics.Capture with Media Foundation; Linux waits (D63). When
// they land this file becomes //go:build !darwin && !windows.

func osTargets(TargetKind) ([]Target, error)    { return nil, ErrUnsupported }
func osPick(*platform.Window, *Recording) error { return ErrUnsupported }
func osStart(Target, *Recording) error          { return ErrUnsupported }
func float32frombits(b uint32) float32          { return math.Float32frombits(b) }
