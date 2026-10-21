//go:build !darwin

package main

// probe has nothing to ask outside macOS.
func probe() (windows, visible int, active bool, policy int) { return 0, 0, false, 0 }
