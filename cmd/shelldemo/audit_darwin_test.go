//go:build darwin

package main

// offscreenOnMain runs f on the main thread. The offscreen rigs create
// CoreText fonts before TestShellPresentsInStep opens the process's first
// window; text loads AppKit before its first CTFont (loadSyms), so that
// ordering is safe and no window needs to open first (issue #6).
func offscreenOnMain(f func()) { onMain(f) }
