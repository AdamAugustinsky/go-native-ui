//go:build darwin

package app

import "gonativeui/internal/objc"

// framePoolPush opens the autorelease pool of one frame's app code (drawFrame).
func framePoolPush() uintptr { return objc.F.Call0(objc.PoolPush) }

// framePoolPop drains it.
func framePoolPop(p uintptr) { objc.F.Call1(objc.PoolPop, p) }
