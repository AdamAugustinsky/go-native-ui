//go:build !darwin && !windows

package main

// newTextField has no native text field to give outside macOS.
func newTextField(string) uintptr { return 0 }
