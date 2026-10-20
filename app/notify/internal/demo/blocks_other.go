//go:build !darwin

package main

// blockCounts: no Objective-C blocks here.
func blockCounts() (live int64, stale uint64) { return 0, 0 }
