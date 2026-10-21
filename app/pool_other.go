//go:build !darwin

package app

import "gonativeui/gpu"

// framePoolPush is the frame's autorelease pool: none here (drawFrame).
func framePoolPush() uintptr { return 0 }

// framePoolPop is a no-op here.
func framePoolPop(uintptr) {}

// abandonSurfaceFrame lets go of a frame that will not be presented: the
// frame holds no references here.
func abandonSurfaceFrame(_ *gpu.Surface, _ *gpu.Renderer, f *gpu.Frame) { *f = gpu.Frame{} }
