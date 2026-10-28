package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"gonativeui/app"
	"gonativeui/app/webview"
)

// cameraSelfTest loads a loopback page that asks for the camera in a
// non-persistent view and prints, as one JSON line, what the page saw and
// whether the policy denied it (a PermissionDenied event). WebKit asks the
// policy only in an app whose Info.plist has NSCameraUsageDescription: run it
// bundled (cd cmd/webdemo && go run ../gnui run -- -selftest camera).
func (d *demo) cameraSelfTest() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	page := make(chan string, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/camera", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><title>Camera</title><script>
if (!navigator.mediaDevices) { fetch("/report?r=nomediadevices") } else
navigator.mediaDevices.getUserMedia({video: true}).then(function(){fetch("/report?r=granted")}, function(e){fetch("/report?r="+e.name)})
</script>`)
	})
	mux.HandleFunc("/report", func(w http.ResponseWriter, r *http.Request) {
		select {
		case page <- r.URL.Query().Get("r"):
		default:
		}
	})
	go http.Serve(ln, mux)
	d.start, d.profile = "http://"+ln.Addr().String()+"/camera", ""
	denied := make(chan webview.Permission, 1)
	d.onEvent = func(ev webview.Event) {
		if ev.Kind == webview.PermissionDenied {
			select {
			case denied <- ev.Permission:
			default:
			}
		}
	}
	go func() {
		var saw string
		var perm webview.Permission
		deadline := time.After(20 * time.Second)
		for saw == "" {
			select {
			case saw = <-page:
			case perm = <-denied:
			case <-deadline:
				saw = "timeout"
			}
		}
		select {
		case perm = <-denied:
		case <-time.After(500 * time.Millisecond):
		}
		fmt.Printf("{\"Page\": %q, \"PermissionDenied\": %v, \"Camera\": %v}\n", saw, perm != 0, perm&webview.PermCamera != 0)
		os.Exit(0)
	}()
	return app.Main(app.AppConfig{ID: "dev.gonativeui.webdemo"}, d)
}
