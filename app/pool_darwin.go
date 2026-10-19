//go:build darwin

package app

import (
	"gonativeui/gpu"
	"gonativeui/internal/objc"
)

// framePoolPush opens the autorelease pool of one frame's app code (drawFrame).
func framePoolPush() uintptr { return objc.F.Call0(objc.PoolPush) }

// framePoolPop drains it, and any pool pushed above it and left open.
func framePoolPop(p uintptr) { objc.F.Call1(objc.PoolPop, p) }

// abandonSurfaceFrame lets go of a frame that will not be presented.
func abandonSurfaceFrame(s *gpu.Surface, f *gpu.Frame) { s.AbandonFrame(f) }
