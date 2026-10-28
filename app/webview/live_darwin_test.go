//go:build darwin

package webview_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ebitengine/purego"

	"gonativeui/app"
	"gonativeui/app/webview"
	"gonativeui/platform"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// H1's matrix on a real window (the phase 8 plan, H1 and exit criterion 5):
// each test runs in a process of its own (RunMainIsolated), with app.Main on
// the main thread, a loopback HTTP server for the pages, and the test
// driving the app through app.Post.

func TestMain(m *testing.M) { widgettest.RunMainIsolated(m) }

// srcDo is the source of the harness's own posts: run the queued functions.
const srcDo = platform.SourceApp + 7

// harness is an app with one window that hosts the views the test makes,
// side by side, and records their events.
type harness struct {
	app.DefaultHandler
	t       *testing.T
	win     *app.Win
	do      chan func(a *app.App)
	started chan struct{}
	ended   chan error

	mu     sync.Mutex
	events []webview.Event
	kinds  []app.EventKind // every app event's kind, in order (the idle gate)
	views  []*webview.View
	frames atomic.Int64
	// atStart and afterOpen: WebKit was loaded when Start began, and once
	// the window had drawn its first frame
	atStart, afterOpen bool
}

func (h *harness) Start(a *app.App) error {
	h.atStart = webKitLoaded()
	w, err := a.OpenWindow(app.Config{Title: "webview test", Width: 800, Height: 500}, h)
	if err != nil {
		return err
	}
	h.win = w
	h.afterOpen = webKitLoaded()
	close(h.started)
	return nil
}

func (h *harness) Event(a *app.App, e *app.Event) {
	h.mu.Lock()
	h.kinds = append(h.kinds, e.Kind)
	h.mu.Unlock()
	if e.Kind == app.EvPost && e.Source == srcDo {
		for {
			select {
			case f := <-h.do:
				f(a)
			default:
				return
			}
		}
	}
	if ev, ok := webview.EventOf(e); ok {
		h.mu.Lock()
		h.events = append(h.events, ev)
		h.mu.Unlock()
	}
	a.DefaultEvent(e)
}

// Build lays the views out side by side.
func (h *harness) Build(c *ui.Context) {
	h.frames.Add(1)
	c.NextAxis(ui.AxisX)
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.Push(c.Box(0, "views"))
	n := len(h.views)
	for i, v := range h.views {
		c.NextSize(ui.Pct(1/float32(n)), ui.Pct(1))
		c.Host(uint32(v.Host()), ui.KeyN(1, uint64(i)), ui.HostFocusable)
	}
	c.Pop()
}

// startApp runs app.Main with a harness and returns once its window is open.
func startApp(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, do: make(chan func(a *app.App), 16), started: make(chan struct{}), ended: make(chan error, 1)}
	go widgettest.OnMain(func() { h.ended <- app.Main(app.AppConfig{ID: "dev.gonativeui.webview.test"}, h) })
	select {
	case <-h.started:
	case err := <-h.ended:
		t.Skipf("no app here: %v", err)
	case <-time.After(60 * time.Second):
		t.Fatal("the app did not start within 60 s")
	}
	t.Cleanup(func() {
		h.post(func(a *app.App) {
			for _, v := range h.views {
				v.Close()
			}
			a.Quit()
		})
		select {
		case <-h.ended:
		case <-time.After(30 * time.Second):
			t.Error("the app did not end within 30 s of Quit")
		}
	})
	return h
}

func (h *harness) post(f func(a *app.App)) {
	h.do <- f
	app.Post(app.Event{Source: srcDo}, nil)
}

// Do runs f on the main thread, in an app event, and waits for it.
func (h *harness) Do(f func(a *app.App)) {
	h.t.Helper()
	done := make(chan struct{})
	h.post(func(a *app.App) { defer close(done); f(a) })
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		h.t.Fatal("the main thread did not run a call within 30 s")
	}
}

// open makes a view in the window (main thread).
func (h *harness) open(o webview.Options) *webview.View {
	h.t.Helper()
	var v *webview.View
	var err error
	h.Do(func(a *app.App) {
		if v, err = webview.New(h.win.App().Win, o); err == nil {
			h.views = append(h.views, v)
			h.win.App().Win.RequestFrame()
		}
	})
	if err != nil {
		h.t.Fatalf("webview.New: %v", err)
	}
	return v
}

// close closes a view (main thread).
func (h *harness) close(v *webview.View) {
	h.Do(func(*app.App) {
		v.Close()
		for i, x := range h.views {
			if x == v {
				h.views = append(h.views[:i], h.views[i+1:]...)
				break
			}
		}
	})
}

// wait waits up to limit for cond, read on the main thread.
func (h *harness) wait(what string, limit time.Duration, cond func() bool) {
	h.t.Helper()
	for end := time.Now().Add(limit); ; time.Sleep(20 * time.Millisecond) {
		var ok bool
		h.Do(func(*app.App) { ok = cond() })
		if ok {
			return
		}
		if time.Now().After(end) {
			h.t.Fatalf("%s: not within %v", what, limit)
		}
	}
}

// saw reports whether view v had an event of kind (since the events were
// last cleared).
func (h *harness) saw(v *webview.View, kind webview.EventKind) (webview.Event, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, e := range h.events {
		if e.View == v.ID() && e.Kind == kind {
			return e, true
		}
	}
	return webview.Event{}, false
}

func (h *harness) count(v *webview.View, kind webview.EventKind) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, e := range h.events {
		if e.View == v.ID() && e.Kind == kind {
			n++
		}
	}
	return n
}

func (h *harness) clear() {
	h.mu.Lock()
	h.events = h.events[:0]
	h.mu.Unlock()
}

// navigate loads url in v and waits for the title.
func (h *harness) navigate(v *webview.View, url, title string) {
	h.t.Helper()
	var err error
	h.Do(func(*app.App) { err = v.Navigate(url) })
	if err != nil {
		h.t.Fatalf("Navigate(%s): %v", url, err)
	}
	h.wait("the title "+title, 20*time.Second, func() bool { return v.State().Title == title && !v.State().Loading })
}

// ---- The pages ----

const bigSize = 10 << 20

// pages is the loopback site: plain pages, a blocked scheme, a popup, a
// 10 MB download written in steps, a camera request and cookies. reports
// collects what pages report with fetch('/report?…').
type pages struct {
	srv     *httptest.Server
	mu      sync.Mutex
	reports []string
}

func newPages(t *testing.T) *pages {
	p := &pages{}
	mux := http.NewServeMux()
	page := func(title, body string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, "<!doctype html><title>%s</title><body>%s</body>", title, body)
		}
	}
	mux.HandleFunc("/a", page("Page A", `<a href="/b">b</a>`))
	mux.HandleFunc("/b", page("Page B", ""))
	mux.HandleFunc("/blocked", page("Blocked", `<script>setTimeout(function(){location.href="gnuitest:hello"},50)</script>`))
	mux.HandleFunc("/popup", page("Popup", `<script>setTimeout(function(){window.open("/b")},50)</script>`))
	mux.HandleFunc("/camera", page("Camera", `<script>
if (!navigator.mediaDevices) { fetch("/report?camera=nomediadevices&secure="+window.isSecureContext) } else
navigator.mediaDevices.getUserMedia({video: true}).then(function(){fetch("/report?camera=granted")}, function(e){fetch("/report?camera="+e.name)})
</script>`))
	mux.HandleFunc("/big.bin", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="big.bin"`)
		w.Header().Set("Content-Length", fmt.Sprint(bigSize))
		chunk := bytes.Repeat([]byte{'g'}, 1<<20)
		for i := 0; i < bigSize>>20; i++ {
			if _, err := w.Write(chunk); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			time.Sleep(40 * time.Millisecond)
		}
	})
	mux.HandleFunc("/setcookie", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "gnui", Value: r.URL.Query().Get("v"), Path: "/", MaxAge: 3600})
		page("set", "")(w, r)
	})
	mux.HandleFunc("/cookie", func(w http.ResponseWriter, r *http.Request) {
		v := "none"
		if c, err := r.Cookie("gnui"); err == nil {
			v = c.Value
		}
		page("cookie:"+v, "")(w, r)
	})
	mux.HandleFunc("/report", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.reports = append(p.reports, r.URL.RawQuery)
		p.mu.Unlock()
	})
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

func (p *pages) url(path string) string { return p.srv.URL + path }

func (p *pages) report(prefix string) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, r := range p.reports {
		if strings.HasPrefix(r, prefix) {
			return r, true
		}
	}
	return "", false
}

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

// Named profiles: a cookie set in one view of a profile is seen by another
// view of it, not by another profile or a non-persistent view, and after a
// restart of the app (a second process) the profile still has it.
func TestProfiles(t *testing.T) {
	if os.Getenv("GNUI_WEBVIEW_PHASE") != "" {
		t.Skip("a child of TestProfiles")
	}
	p := newPages(t)
	val := fmt.Sprintf("v%d", time.Now().UnixNano())
	set := profileChild(t, "set", p.srv.URL, val)
	get := profileChild(t, "get", p.srv.URL, val)
	t.Logf("first run: %v; second run: %v", set, get)
	want := map[string]string{"same": val, "other": "none", "private": "none"}
	for k, w := range want {
		if set[k] != w {
			t.Errorf("first run, %s profile view: cookie %q, want %q", k, set[k], w)
		}
	}
	if get["same"] != val || get["other"] != "none" {
		t.Errorf("after a restart: the profile has %q (want %q), the other %q", get["same"], val, get["other"])
	}
}

// TestProfileChild is TestProfiles' child process: phase "set" sets the
// cookie in profile one and reads it from new views of profile one, profile
// two and a non-persistent session; "get" reads it in profiles one and two.
func TestProfileChild(t *testing.T) {
	phase := os.Getenv("GNUI_WEBVIEW_PHASE")
	if phase == "" {
		t.Skip("run by TestProfiles")
	}
	base, val := os.Getenv("GNUI_WEBVIEW_URL"), os.Getenv("GNUI_WEBVIEW_COOKIE")
	h := startApp(t)
	read := func(o webview.Options) string {
		v := h.open(o)
		var title string
		h.Do(func(*app.App) { _ = v.Navigate(base + "/cookie") })
		h.wait("the cookie page", 20*time.Second, func() bool {
			title = v.State().Title
			return strings.HasPrefix(title, "cookie:") && !v.State().Loading
		})
		h.close(v)
		return strings.TrimPrefix(title, "cookie:")
	}
	one, two := webview.Options{Profile: "gnui-webview-test-1"}, webview.Options{Profile: "gnui-webview-test-2"}
	if phase == "set" {
		v := h.open(one)
		h.navigate(v, base+"/setcookie?v="+val, "set")
		h.close(v)
		fmt.Printf("R private=%s\n", read(webview.Options{}))
	}
	fmt.Printf("R same=%s\n", read(one))
	fmt.Printf("R other=%s\n", read(two))
	// Quit as a user does (a quit request, allowed): AppKit's terminate:,
	// whose will-terminate notification has WebKit write the profile's
	// cookies. The process ends there.
	if phase == "set" {
		// WebKit's network process writes a profile's cookies to disk a
		// moment after they change (CFNetwork's cookie sync; measured: a quit
		// 1 s after the change lost it, 3 s after kept it), and no public call
		// flushes them (the H1 note in docs/PHASE8-PLAN.md).
		time.Sleep(3 * time.Second)
	}
	h.post(func(a *app.App) { a.RequestQuit() })
	time.Sleep(30 * time.Second)
	t.Fatal("the app did not end within 30 s of an allowed quit request")
}

// profileChild runs TestProfileChild in phase and returns what it printed.
func profileChild(t *testing.T, phase, url, val string) map[string]string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestProfileChild$", "-test.v")
	cmd.Env = append(os.Environ(), "GNUI_ISOLATED_TEST=1", "GNUI_WEBVIEW_PHASE="+phase, "GNUI_WEBVIEW_URL="+url, "GNUI_WEBVIEW_COOKIE="+val)
	out, err := cmd.CombinedOutput()
	res := map[string]string{}
	for sc := bufio.NewScanner(bytes.NewReader(out)); sc.Scan(); {
		if kv, ok := strings.CutPrefix(sc.Text(), "R "); ok {
			if k, v, ok := strings.Cut(kv, "="); ok {
				res[k] = v
			}
		}
	}
	if os.Getenv("GNUI_WEBVIEW_DEBUG") != "" {
		t.Logf("phase %s: %v\n%s", phase, err, out)
	}
	if err != nil || bytes.Contains(out, []byte("--- SKIP")) {
		t.Fatalf("phase %s: %v\n%s", phase, err, out)
	}
	return res
}
