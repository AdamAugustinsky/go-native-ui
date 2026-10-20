//go:build darwin

package main

import (
	"sync"

	"gonativeui/platform"
)

var appKitOnce sync.Once

// offscreenOnMain runs f on the main thread after AppKit has set up its
// fonts: a CoreText system font created before the process's first window
// makes AppKit's later NSFont lookups crash (in the window opened by
// TestShellPresentsInStep), so a hidden window opens first, as app.Run does.
func offscreenOnMain(f func()) {
	onMain(func() {
		appKitOnce.Do(func() {
			if w, err := platform.Open(platform.Options{Title: "audit", Width: 100, Height: 100, Hidden: true}); err == nil {
				w.Close()
			}
		})
		f()
	})
}
