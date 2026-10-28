//go:build !darwin && !windows

package capture

import "gonativeui/platform"

// No capture on Linux yet (D63).

func osTargets(TargetKind) ([]Target, error)    { return nil, ErrUnsupported }
func osPick(*platform.Window, *Recording) error { return ErrUnsupported }
func osStart(Target, *Recording) error          { return ErrUnsupported }
