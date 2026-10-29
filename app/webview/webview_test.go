package webview_test

import (
	"errors"
	"runtime"
	"testing"

	"gonativeui/app"
	"gonativeui/app/webview"
	"gonativeui/platform"
)

func TestNewContract(t *testing.T) {
	if _, err := webview.New(nil, webview.Options{}); !errors.Is(err, webview.ErrNoWindow) {
		t.Fatalf("New(nil) = %v", err)
	}
	for _, p := range []string{"../x", "a/b", "..", string(make([]byte, 65))} {
		if _, err := webview.New(&platform.Window{}, webview.Options{Profile: p}); !errors.Is(err, webview.ErrProfile) {
			t.Errorf("profile %q: %v", p, err)
		}
	}
	if runtime.GOOS == "darwin" {
		return // H1: a view needs a real window (live_darwin_test.go)
	}
	_, err := webview.New(&platform.Window{}, webview.Options{Profile: "a3-browser"})
	if runtime.GOOS == "windows" { // H2: no window, or no runtime
		if !errors.Is(err, webview.ErrNoWindow) && !errors.Is(err, webview.ErrUnavailable) {
			t.Fatalf("New without a window = %v", err)
		}
		return
	}
	if !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("New = %v, want unsupported on Linux", err)
	}
}

func TestEventOf(t *testing.T) {
	e := app.Event{Kind: app.EvService, Source: platform.SourceWebView, ID: 3, A: int64(webview.PermissionDenied) | int64(webview.PermCamera)<<8, B: 9}
	ev, ok := webview.EventOf(&e)
	if !ok || ev.Kind != webview.PermissionDenied || ev.Permission != webview.PermCamera || ev.View != 3 || ev.Download != 9 {
		t.Fatalf("EventOf = %+v %v", ev, ok)
	}
	if _, ok := webview.EventOf(&app.Event{Kind: app.EvService, Source: platform.SourceMedia}); ok {
		t.Fatal("a media event decoded as a view event")
	}
}
