//go:build darwin

package main

import "gonativeui/internal/objc"

// blockCounts are the per-request blocks still live and the calls that
// found their request gone (objc.Leaks).
func blockCounts() (live int64, stale uint64) {
	c := objc.Leaks()
	return c.Blocks, c.StaleCalls
}
