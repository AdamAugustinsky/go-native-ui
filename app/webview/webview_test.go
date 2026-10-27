package webview_test

import (
	"errors"
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
	_, err := webview.New(&platform.Window{}, webview.Options{Profile: "a3-browser"})
	if !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("New = %v, want unsupported until H1/H2", err)
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
