//go:build !darwin && !windows

package webview

import "gonativeui/platform"

// No hosted browser view on Linux: WebKitGTK would bring GTK (D64).

func osNew(*platform.Window, *Options) (backend, platform.HostID, uint32, error) {
	return nil, 0, 0, ErrUnsupported
}
