//go:build windows

package webview_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"gonativeui/app"
	"gonativeui/app/webview"
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procModuleHandle = kernel32.NewProc("GetModuleHandleW")
	runtimeDLL, _    = syscall.UTF16PtrFromString("EmbeddedBrowserWebView.dll")
)

// engineLoaded reports whether the WebView2 runtime's client DLL is loaded.
func engineLoaded() bool {
	h, _, _ := procModuleHandle.Call(uintptr(unsafe.Pointer(runtimeDLL)))
	return h != 0
}

// engineProcesses are the WebView2 processes running (msedgewebview2.exe):
// pid: memory, read with tasklist.
func engineProcesses() map[string]string {
	out, _ := exec.Command("tasklist", "/FI", "IMAGENAME eq msedgewebview2.exe", "/FO", "CSV", "/NH").Output()
	m := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Split(strings.TrimSpace(line), `","`)
		if len(f) >= 5 {
			m[f[1]] = strings.Trim(f[4], `"`)
		}
	}
	return m
}

// Until the window hosts child windows (W3), a view's window covers the
// window's client area.
func init() { webview.UseTestHost() }

// openReady opens a view and waits for its controller.
func (h *harness) openReady(o webview.Options) *webview.View {
	h.t.Helper()
	v := h.open(o)
	h.wait("the view's controller", 60*time.Second, func() bool { return webview.Ready(v) })
	return v
}

// H2's matrix: the runtime loads at the first New, not before; navigation
// with title, URL and load events; back and forward; a blocked scheme; a
// refused popup and one loaded in place; a 10 MB download with progress;
// location and notifications denied, and allowed; a non-persistent session
// shares nothing with another and leaves no cookie after Close; a hidden view
// is suspended within 1 s; an idle view draws no frames.
func TestViewMatrix(t *testing.T) {
	h := startApp(t)
	p := newPages(t)
	h.wait("the first frame", 10*time.Second, func() bool { return h.frames.Load() > 0 })
	if h.atStart || h.afterOpen || webview.LoadTime() != 0 {
		t.Errorf("the runtime was loaded before the first New: at Start %v, after the first frame %v", h.atStart, h.afterOpen)
	}
	before := engineProcesses()
	t0 := time.Now()
	v := h.open(webview.Options{DownloadDir: t.TempDir(), NewWindows: webview.NewWindowRefuse})
	first := time.Since(t0)
	h.wait("the view's controller", 60*time.Second, func() bool { return webview.Ready(v) })
	t.Logf("gate: New took %v (finding and loading the runtime %v); the controller came %v after it", first, webview.LoadTime(), time.Since(t0))
	if !engineLoaded() {
		t.Error("the runtime is not loaded after New")
	}

	// navigation, events and history
	h.navigate(v, p.url("/a"), "Page A")
	kinds := []webview.EventKind{webview.TitleChanged, webview.URLChanged, webview.LoadStarted, webview.LoadFinished, webview.Progress}
	h.wait("the first page's events", 5*time.Second, func() bool {
		for _, k := range kinds {
			if _, ok := h.saw(v, k); !ok {
				return false
			}
		}
		return true
	})
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
		if s := v.State(); s.Title != "Blocked" || s.Loading {
			t.Errorf("the blocked navigation left %+v", s)
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
	h.navigate(in, p.url("/popup"), "Popup") // Navigate before the controller: kept until it is ready
	h.wait("the popup loaded in place", 20*time.Second, func() bool { return in.State().Title == "Page B" })
	h.Do(func(*app.App) {
		if !webview.Rounded(in) {
			t.Error("CornerRadius 9.5: the view's window is not rounded")
		}
	})
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
	if _, failed := h.saw(v, webview.LoadFailed); failed {
		t.Error("the navigation that became a download reported LoadFailed")
	}

	// permissions: denied by default, granted when allowed
	h.clear()
	h.navigate(v, p.url("/perm?d-"), "Permissions")
	h.wait("the denied page's answers", 20*time.Second, func() bool {
		_, g := p.report("d-geo=")
		_, n := p.report("d-notify=")
		return g && n
	})
	geo, _ := p.report("d-geo=")
	notify, _ := p.report("d-notify=")
	t.Logf("denied: %s, %s", geo, notify)
	if geo != "d-geo=1" { // PERMISSION_DENIED
		t.Errorf("location without Allow: %s", geo)
	}
	if notify == "d-notify=granted" {
		t.Errorf("notifications without Allow: %s", notify)
	}
	h.wait("PermissionDenied for the location", 5*time.Second, func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		for _, e := range h.events {
			if e.View == v.ID() && e.Kind == webview.PermissionDenied && e.Permission == webview.PermLocation {
				return true
			}
		}
		return false
	})
	allow := h.open(webview.Options{Allow: webview.PermLocation | webview.PermNotifications})
	h.navigate(allow, p.url("/perm?a-"), "Permissions")
	h.wait("the allowed page's answers", 20*time.Second, func() bool {
		_, g := p.report("a-geo=")
		_, n := p.report("a-notify=")
		return g && n
	})
	geo, _ = p.report("a-geo=")
	notify, _ = p.report("a-notify=")
	t.Logf("allowed: %s, %s (no location provider on a runner: code 2 is not a denial)", geo, notify)
	if geo == "a-geo=1" || notify == "a-notify=denied" {
		t.Errorf("allowed, the page was denied: %s, %s", geo, notify)
	}
	if _, denied := h.saw(allow, webview.PermissionDenied); denied {
		t.Error("PermissionDenied from a view that allows what was asked")
	}
	h.close(allow)

	// non-persistent sessions: two at once share nothing; none outlives Close
	np := h.openReady(webview.Options{})
	h.navigate(np, p.url("/setcookie?v=np"), "set")
	h.navigate(np, p.url("/cookie"), "cookie:np")
	other := h.openReady(webview.Options{})
	h.navigate(other, p.url("/cookie"), "cookie:none")
	h.close(other)
	h.close(np)
	np2 := h.openReady(webview.Options{})
	h.navigate(np2, p.url("/cookie"), "cookie:none")
	h.close(np2)

	// hidden: suspended within 1 s; shown: resumed
	h.Do(func(*app.App) { webview.ShowView(v, false) })
	t1 := time.Now()
	h.wait("the hidden view suspended", 5*time.Second, func() bool { return webview.Suspended(v) })
	if d := time.Since(t1); d > time.Second {
		t.Errorf("gate: the hidden view was suspended after %v (budget 1 s)", d)
	} else {
		t.Logf("gate: the hidden view was suspended after %v", d)
	}
	h.Do(func(*app.App) { webview.ShowView(v, true) })
	h.wait("the shown view resumed", 5*time.Second, func() bool { return !webview.Suspended(v) })

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
	for pid, m := range engineProcesses() {
		if _, ok := before[pid]; !ok {
			t.Logf("reported, not budgeted: WebView2 process %s, %s", pid, m)
		}
	}
	if idle != 0 {
		t.Errorf("an idle view drew %d frames in 3 s", idle)
	}
}
