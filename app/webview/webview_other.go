package webview

import "gonativeui/platform"

// No hosted browser view here yet: H1 brings WKWebView on macOS and H2
// WebView2 on Windows; Linux stays unsupported (D64). When they land this
// file becomes //go:build !darwin && !windows.

func osNew(*platform.Window, *Options) (backend, platform.HostID, uint32, error) {
	return nil, 0, 0, ErrUnsupported
}
