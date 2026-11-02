//go:build darwin || windows

package webview_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/app/webview"
	"gonativeui/platform"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// H1's and H2's matrix on a real window (the phase 8 plan, H1, H2 and exit
// criterion 5): each test runs in a process of its own (RunMainIsolated),
// with app.Main on the main thread, a loopback HTTP server for the pages, and
// the test driving the app through app.Post. The matrix itself is per OS
// (live_darwin_test.go, live_windows_test.go); the harness, the pages and the
// profile test are here.

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
	gone    atomic.Bool // app.Main returned (a child's quit)

	mu     sync.Mutex
	events []webview.Event
	kinds  []app.EventKind // every app event's kind, in order (the idle gate)
	views  []*webview.View
	hidden map[*webview.View]bool // views not built: their hosts hide
	frames atomic.Int64
	debugN atomic.Int64 // the debug overlay's number of the last frame built (the idle gate)
	// atStart and afterOpen: the engine was loaded when Start began, and
	// once the window had drawn its first frame
	atStart, afterOpen bool
}

func (h *harness) Start(a *app.App) error {
	h.atStart = engineLoaded()
	w, err := a.OpenWindow(app.Config{Title: "webview test", Width: 800, Height: 500}, h)
	if err != nil {
		return err
	}
	h.win = w
	h.afterOpen = engineLoaded()
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
	if h.win != nil { // nil in the frame OpenWindow draws
		h.debugN.Store(int64(h.win.App().DebugFrames()))
	}
	c.NextAxis(ui.AxisX)
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.Push(c.Box(0, "views"))
	n := len(h.views)
	for i, v := range h.views {
		if h.hidden[v] {
			continue
		}
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
		if h.gone.Load() {
			return
		}
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

// show builds v's host or leaves it out, from the next frame (main thread).
func (h *harness) show(v *webview.View, shown bool) {
	h.Do(func(*app.App) {
		if h.hidden == nil {
			h.hidden = map[*webview.View]bool{}
		}
		h.hidden[v] = !shown
		h.win.App().Win.RequestFrame()
	})
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
// openReady opens a view and waits for it to be ready: the first in a
// process creates the WebView2 environment, slow on a busy runner.
func (h *harness) openReady(o webview.Options) *webview.View {
	h.t.Helper()
	v := h.open(o)
	h.wait("the view's controller", 60*time.Second, func() bool { return webview.Ready(v) })
	return v
}

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
	mux.HandleFunc("/green", page("Green", `<style>html,body{margin:0;height:100%;background:#00c000}</style>`))
	mux.HandleFunc("/blocked", page("Blocked", `<script>setTimeout(function(){location.href="gnuitest:hello"},50)</script>`))
	mux.HandleFunc("/popup", page("Popup", `<script>setTimeout(function(){window.open("/b")},50)</script>`))
	mux.HandleFunc("/camera", page("Camera", `<script>
if (!navigator.mediaDevices) { fetch("/report?camera=nomediadevices&secure="+window.isSecureContext) } else
navigator.mediaDevices.getUserMedia({video: true}).then(function(){fetch("/report?camera=granted")}, function(e){fetch("/report?camera="+e.name)})
</script>`))
	mux.HandleFunc("/perm", page("Permissions", `<script>
var tag = location.search.slice(1);
navigator.geolocation.getCurrentPosition(function(){fetch("/report?"+tag+"geo=granted")}, function(e){fetch("/report?"+tag+"geo="+e.code)}, {timeout: 5000});
if (window.Notification) Notification.requestPermission().then(function(p){fetch("/report?"+tag+"notify="+p)}); else fetch("/report?"+tag+"notify=none");
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
			time.Sleep(stepDelay(r))
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

// stepDelay is the pause between /big.bin's 1 MB writes: 40 ms, or the
// request's ms parameter.
func stepDelay(r *http.Request) time.Duration {
	if ms, err := strconv.Atoi(r.URL.Query().Get("ms")); err == nil && ms > 0 {
		return time.Duration(ms) * time.Millisecond
	}
	return 40 * time.Millisecond
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
		v := h.openReady(o)
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
		v := h.openReady(one)
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
	// macOS: terminate: ends the process here. Windows: app.Main returns.
	select {
	case err := <-h.ended:
		h.gone.Store(true)
		if err != nil {
			t.Fatalf("app.Main: %v", err)
		}
		return
	case <-time.After(30 * time.Second):
	}
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

// Eval and Capture (D94): a script's value as JSON, awaited; undefined as
// null; a throw and a rejection as ErrScript with the page's message; a click
// that navigates; the page's own globals; and a capture of a green page.
func TestEvalAndCapture(t *testing.T) {
	h := startApp(t)
	p := newPages(t)
	v := h.openReady(webview.Options{})
	h.navigate(v, p.url("/a"), "Page A")

	run := func(script string) webview.Result {
		t.Helper()
		var id uint32
		var err error
		h.Do(func(*app.App) { id, err = v.Eval(script) })
		if err != nil {
			t.Fatalf("Eval(%q): %v", script, err)
		}
		var r webview.Result
		h.wait("the script's result", 20*time.Second, func() bool {
			var ok bool
			r, ok = v.Result(id)
			return ok
		})
		if e, ok := h.saw(v, webview.EvalEnded); !ok || e.Request == 0 {
			t.Errorf("no EvalEnded event with a request (%+v)", e)
		}
		return r
	}
	cases := []struct{ script, want string }{
		{"return document.title", `"Page A"`},
		{"await new Promise(r => setTimeout(r, 20)); return {n: 1 + 1, links: document.querySelectorAll('a').length}", `{"n":2,"links":1}`},
		{"document.body.dataset.x = 'y'", "null"},
		{"window.gnuiCount = (window.gnuiCount || 0) + 1; return window.gnuiCount", "1"},
		{"return window.gnuiCount", "1"},
		{"return () => 1", "null"},
	}
	for _, c := range cases {
		if r := run(c.script); r.Err != nil || r.JSON != c.want {
			t.Errorf("Eval(%q) = %q, %v; want %q", c.script, r.JSON, r.Err, c.want)
		}
	}
	for _, script := range []string{"throw new Error('boom ' + 42)", "await Promise.reject(new Error('boom 42'))"} {
		r := run(script)
		if !errors.Is(r.Err, webview.ErrScript) || !strings.Contains(r.Err.Error(), "boom 42") {
			t.Errorf("Eval(%q): err %v, want ErrScript with the message", script, r.Err)
		}
	}
	if r := run("document.querySelector('a').click()"); r.Err != nil {
		t.Errorf("click: %v", r.Err)
	}
	h.wait("Page B after the click", 20*time.Second, func() bool { return v.State().Title == "Page B" && !v.State().Loading })
	if _, ok := v.Result(1 << 30); ok {
		t.Error("Result of an unknown request reported a result")
	}

	h.navigate(v, p.url("/green"), "Green")
	var id uint32
	var err error
	h.Do(func(*app.App) { id, err = v.Capture() })
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	var r webview.Result
	h.wait("the capture", 20*time.Second, func() bool {
		var ok bool
		r, ok = v.Result(id)
		return ok
	})
	if _, ok := h.saw(v, webview.CaptureEnded); !ok {
		t.Error("no CaptureEnded event")
	}
	if r.Err != nil {
		t.Fatalf("Capture: %v", r.Err)
	}
	img, err := png.Decode(bytes.NewReader(r.PNG))
	if err != nil {
		t.Fatalf("the capture is not a PNG: %v", err)
	}
	b := img.Bounds()
	cr, cg, cb, _ := img.At(b.Dx()/2, b.Dy()/2).RGBA()
	t.Logf("capture %dx%d, %d bytes, centre %02x%02x%02x", b.Dx(), b.Dy(), len(r.PNG), cr>>8, cg>>8, cb>>8)
	if b.Dx() < 100 || b.Dy() < 100 || cg>>8 < 0x90 || cr>>8 > 0x40 || cb>>8 > 0x40 {
		t.Errorf("the capture is %v with centre %02x%02x%02x, want the green page", b, cr>>8, cg>>8, cb>>8)
	}
	h.Do(func(*app.App) {
		if _, ok := v.Result(id); ok {
			t.Error("a result was taken twice")
		}
	})
}
