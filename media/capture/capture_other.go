//go:build !darwin

package capture

import "gonativeui/platform"

// No capture here yet: M2 brings Windows.Graphics.Capture with Media
// Foundation; Linux waits (D63). When M2 lands this file becomes
// //go:build !darwin && !windows.

func osTargets(TargetKind) ([]Target, error)    { return nil, ErrUnsupported }
func osPick(*platform.Window, *Recording) error { return ErrUnsupported }
func osStart(Target, *Recording) error          { return ErrUnsupported }
