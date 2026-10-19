//go:build !darwin

package app

// framePoolPush is the frame's autorelease pool: none here (drawFrame).
func framePoolPush() uintptr { return 0 }

// framePoolPop is a no-op here.
func framePoolPop(uintptr) {}
