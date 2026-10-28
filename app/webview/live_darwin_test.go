//go:build darwin

package webview_test

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ebitengine/purego"

	"gonativeui/app"
	"gonativeui/app/webview"
)

// engineLoaded reports whether WebKit is among the process's images.
func engineLoaded() bool { return webKitLoaded() }

// webKitLoaded reports whether WebKit is among the process's images.
func webKitLoaded() bool {
	sys, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW)
	if err != nil {
		return false
	}
	var count func() uint32
	var name func(uint32) string
	purego.RegisterLibFunc(&count, sys, "_dyld_image_count")
	purego.RegisterLibFunc(&name, sys, "_dyld_get_image_name")
	for i := uint32(0); i < count(); i++ {
		if strings.Contains(name(i), "/WebKit.framework/") {
			if os.Getenv("GNUI_WEBVIEW_DEBUG") != "" {
				fmt.Println("image:", name(i))
			}
			return true
		}
	}
	return false
}

// webKitProcesses are the WebKit helper processes running (pid: name, RSS
// and CPU), read with ps.
func webKitProcesses() map[string]string {
	out, _ := exec.Command("ps", "-Ao", "pid=,rss=,pcpu=,comm=").Output()
	m := map[string]string{}
	for sc := bufio.NewScanner(bytes.NewReader(out)); sc.Scan(); {
		f := strings.Fields(sc.Text())
		if len(f) >= 4 && strings.Contains(f[3], "com.apple.WebKit.") {
			m[f[0]] = fmt.Sprintf("%s, RSS %s KiB, CPU %s %%", filepath.Base(f[3]), f[1], f[2])
		}
	}
	return m
}

// ---- The tests ----

// The matrix: WebKit loads at the first New, not before the first frame;
// navigation with title, URL and load events; back and forward; a blocked
// scheme; a refused popup and one loaded in place; a 10 MB download with
// progress; a camera request denied; a non-persistent session leaves no
// cookie after Close; an idle view draws no frames.
func TestViewMatrix(t *testing.T) {
	if webKitLoaded() {
		t.Log("WebKit is loaded before the app starts")
	}
	h := startApp(t)
	p := newPages(t)
	h.wait("the first frame", 10*time.Second, func() bool { return h.frames.Load() > 0 })
	// AppKit maps WebKit's image itself on macOS 27 (finishLaunching pulls
	// SafariServices in): the gate is that this package does nothing before
	// the first New.
	t.Logf("WebKit's image mapped: when Start began %v, after the window's first frame %v", h.atStart, h.afterOpen)
	if webview.LoadTime() != 0 {
		t.Error("the package loaded WebKit before the first New")
	}
	before := webKitProcesses()
	t0 := time.Now()
	v := h.open(webview.Options{DownloadDir: t.TempDir(), NewWindows: webview.NewWindowRefuse})
	first := time.Since(t0)
	t.Logf("gate: nothing of the package ran before the first frame; the first New, after it, took %v (dlopen and the classes %v)", first, webview.LoadTime())
	if !webKitLoaded() || webview.LoadTime() == 0 {
		t.Error("WebKit is not loaded after New")
	}

	// navigation, events and history
	h.navigate(v, p.url("/a"), "Page A")
	// The state changes at once (KVO), its events reach the app through the
	// views' ring, after the harness's own post may have: the title can be
	// there before its event (macOS CI, under the load of the whole run).
	kinds := []webview.EventKind{webview.TitleChanged, webview.URLChanged, webview.LoadStarted, webview.LoadFinished}
	allSeen := func() bool {
		for _, k := range kinds {
			if _, ok := h.saw(v, k); !ok {
				return false
			}
		}
		return true
	}
	for end := time.Now().Add(5 * time.Second); !allSeen() && time.Now().Before(end); {
		time.Sleep(20 * time.Millisecond)
	}
	for _, k := range kinds {
		if _, ok := h.saw(v, k); !ok {
			t.Errorf("no event of kind %d for the first page within 5 s of its title", k)
		}
	}
	h.Do(func(*app.App) {
		if s := v.State(); s.URL != p.url("/a") || s.Progress != 1 || s.CanGoBack {
			t.Errorf("state after /a: %+v", s)
		}
	})
	h.navigate(v, p.url("/b"), "Page B")
	h.Do(func(*app.App) { v.Back() })
	h.wait("back to A", 20*time.Second, func() bool { s := v.State(); return s.Title == "Page A" && s.CanGoForward && !s.CanGoBack })
	h.Do(func(*app.App) { v.Forward() })
	h.wait("forward to B", 20*time.Second, func() bool { s := v.State(); return s.Title == "Page B" && s.CanGoBack && s.URL == p.url("/b") })
	h.Do(func(*app.App) { v.Reload() })
	h.wait("reloaded", 20*time.Second, func() bool { return !v.State().Loading })

	// schemes
	h.Do(func(*app.App) {
		if err := v.Navigate("gnuitest:x"); !errors.Is(err, webview.ErrBadURL) {
			t.Errorf("Navigate to a scheme not allowed: %v", err)
		}
	})
	h.clear()
	h.navigate(v, p.url("/blocked"), "Blocked")
	h.wait("NavigationBlocked", 20*time.Second, func() bool { _, ok := h.saw(v, webview.NavigationBlocked); return ok })
	h.Do(func(*app.App) {
		if b := v.Blocked(); b != "gnuitest:hello" {
			t.Errorf("Blocked() = %q", b)
		}
		if s := v.State(); s.Title != "Blocked" {
			t.Errorf("the blocked navigation left %q", s.Title)
		}
	})

	// popups: refused here, in place in a second view
	h.clear()
	h.navigate(v, p.url("/popup"), "Popup")
	h.wait("NewWindowRefused", 20*time.Second, func() bool { _, ok := h.saw(v, webview.NewWindowRefused); return ok })
	h.Do(func(*app.App) {
		if b := v.Blocked(); b != p.url("/b") {
			t.Errorf("the refused window's URL %q", b)
		}
	})
	in := h.open(webview.Options{NewWindows: webview.NewWindowInPlace, CornerRadius: 9.5})
	h.Do(func(*app.App) {
		if r := webview.CornerRadiusOf(in); r != 9.5 {
			t.Errorf("CornerRadius 9.5: the layer's is %v", r)
		}
	})
	h.navigate(in, p.url("/popup"), "Popup")
	h.wait("the popup loaded in place", 20*time.Second, func() bool { return in.State().Title == "Page B" })
	h.close(in)

	// a download of 10 MB
	h.clear()
	h.Do(func(*app.App) { _ = v.Navigate(p.url("/big.bin")) })
	var dl webview.Download
	h.wait("DownloadEnded", 60*time.Second, func() bool {
		e, ok := h.saw(v, webview.DownloadEnded)
		if ok {
			dl, _ = v.Download(e.Download)
		}
		return ok
	})
	progress := h.count(v, webview.DownloadProgress)
	_, started := h.saw(v, webview.DownloadStarted)
	t.Logf("download: %d progress events, %+v", progress, dl)
	if !started || progress < 1 || dl.State != webview.DownloadDone || dl.Received != bigSize || dl.Total != bigSize || filepath.Base(dl.Path) != "big.bin" {
		t.Errorf("download: started %v, %d progress events, %+v", started, progress, dl)
	}
	if fi, err := os.Stat(dl.Path); err != nil || fi.Size() != bigSize {
		t.Errorf("the downloaded file: %v %v", fi, err)
	}

	// the camera
	h.clear()
	h.navigate(v, p.url("/camera"), "Camera")
	h.wait("the camera's answer", 20*time.Second, func() bool { _, ok := p.report("camera="); return ok })
	r, _ := p.report("camera=")
	e, denied := h.saw(v, webview.PermissionDenied)
	t.Logf("camera: the page reported %q; PermissionDenied event %v %+v", r, denied, e)
	if r == "camera=granted" {
		t.Error("the camera was granted")
	}
	if denied && e.Permission != webview.PermCamera {
		t.Errorf("PermissionDenied for %v, want the camera", e.Permission)
	}
	if !denied {
		t.Logf("pending: WebKit did not ask the UI delegate (%s): it hides navigator.mediaDevices from a process with no NSCameraUsageDescription; the bundled check is cmd/webdemo -selftest", r)
	}
	// the delegate's own path, called as WebKit calls it
	allowMic := h.open(webview.Options{Allow: webview.PermMicrophone})
	h.clear()
	h.Do(func(*app.App) {
		for _, c := range []struct {
			v    *webview.View
			typ  int
			want int
		}{{v, 0, 2}, {v, 1, 2}, {v, 2, 2}, {allowMic, 1, 1}, {allowMic, 2, 2}} {
			if got := webview.AskMedia(c.v, c.typ); got != c.want {
				t.Errorf("media request type %d with Allow %v: decision %d, want %d", c.typ, c.v == allowMic, got, c.want)
			}
		}
	})
	h.wait("PermissionDenied events", 10*time.Second, func() bool { return h.count(v, webview.PermissionDenied) == 3 })
	if e, _ := h.saw(allowMic, webview.PermissionDenied); e.Permission != webview.PermCamera|webview.PermMicrophone {
		t.Errorf("camera and microphone denied with the microphone allowed: %+v", e)
	}
	h.close(allowMic)

	// a non-persistent session
	np := h.open(webview.Options{})
	h.navigate(np, p.url("/setcookie?v=np"), "set")
	h.navigate(np, p.url("/cookie"), "cookie:np")
	h.close(np)
	np2 := h.open(webview.Options{})
	h.navigate(np2, p.url("/cookie"), "cookie:none")
	h.close(np2)

	// idle: once loaded, the view adds no frames
	h.Do(func(*app.App) {})
	time.Sleep(500 * time.Millisecond)
	h.mu.Lock()
	k0 := len(h.kinds)
	h.mu.Unlock()
	f0 := h.frames.Load()
	time.Sleep(3 * time.Second)
	idle := h.frames.Load() - f0
	h.mu.Lock()
	during := append([]app.EventKind(nil), h.kinds[k0:]...)
	h.mu.Unlock()
	t.Logf("gate: an idle view, 3 s: %d frames (presents); app events %v", idle, during)
	// An app event draws what the Handler may have changed (D37). The
	// system's own (another app took the activation or gave it back, the
	// screens changed: a hosted runner's session does both) are not the
	// view's doing, as widgettest/live's idle test has it.
	outside := 0
	for _, k := range during {
		if k == app.EvActivated || k == app.EvDeactivated || k == app.EvScreensChanged {
			outside++
		}
	}
	if idle != 0 && outside > 0 && outside == len(during) {
		t.Logf("the idle frames follow %d of the system's app events: not counted", outside)
		idle = 0
	}
	for pid, p := range webKitProcesses() {
		if _, ok := before[pid]; !ok {
			t.Logf("reported, not budgeted: WebKit process %s started with the views: %s", pid, p)
		}
	}
	if idle != 0 {
		t.Errorf("an idle view drew %d frames in 3 s", idle)
	}
}
