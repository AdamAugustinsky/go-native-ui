//go:build windows

package applive

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/app/instance"
	"gonativeui/gpu"
	"gonativeui/internal/testlock"
	"gonativeui/platform"
	"gonativeui/ui"
)

// The application object and its windows on Windows (the phase 8 plan's W1,
// D68; exit criterion 1), in child processes of the test binary on their
// main thread, as on macOS: each scenario prints "R key=value" lines and the
// test reads them. They run on both CI runners (windows-2025 and
// windows-11-arm); nothing here needs the foreground (messages are posted to
// the windows, not typed).

func init() { runtime.LockOSThread() }

// TestMain runs a scenario when the binary is a scenario's child, the tests
// otherwise.
func TestMain(m *testing.M) {
	if name := os.Getenv("GNUI_APPLIVE"); name != "" {
		runScenario(name)
		return
	}
	os.Exit(m.Run())
}

// result is what a child printed and how it ended.
type result struct {
	kv   map[string]string
	out  string
	code int
}

func (r result) get(k string) string { return r.kv[k] }

func (r result) int(k string) int {
	n, err := strconv.Atoi(r.kv[k])
	if err != nil {
		return -1
	}
	return n
}

// child runs scenario name with env in a child process and waits for it at
// most limit (a deadlock fails the test).
func child(t *testing.T, name string, limit time.Duration, env ...string) result {
	t.Helper()
	testlock.Hold(t, "activation") // other packages' tests open windows too (in parallel processes)
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^$")
	cmd.Env = append(append(os.Environ(), "GNUI_APPLIVE="+name), env...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	r := result{kv: map[string]string{}, out: out.String()}
	if ctx.Err() != nil {
		t.Fatalf("%s %v: did not end within %v (deadlock?)\n%s", name, env, limit, r.out)
	}
	if ee, ok := err.(*exec.ExitError); ok {
		r.code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	sc := bufio.NewScanner(strings.NewReader(r.out))
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "R ") {
			continue
		}
		for _, f := range strings.Fields(line[2:]) {
			if k, v, ok := strings.Cut(f, "="); ok {
				r.kv[k] = v
			}
		}
	}
	if r.code != 0 {
		t.Fatalf("%s %v: exit status %d\n%s", name, env, r.code, r.out)
	}
	return r
}

// ---- The child's side ----

func say(format string, args ...any) { fmt.Printf("R "+format+"\n", args...) }

func fail(format string, args ...any) {
	fmt.Printf("FAIL "+format+"\n", args...)
	os.Exit(2)
}

var scenarios = map[string]func(){}

func runScenario(name string) {
	f := scenarios[name]
	if f == nil {
		fail("no scenario %q", name)
	}
	time.AfterFunc(90*time.Second, func() { fmt.Println("FAIL watchdog"); os.Exit(3) })
	f()
	os.Exit(0)
}

// counter is a window's builder: it counts its builds and, while animate is
// set, asks for the next frame.
type counter struct {
	builds  atomic.Int64
	animate atomic.Bool
}

func (c *counter) Build(ctx *ui.Context) {
	c.builds.Add(1)
	if c.animate.Load() {
		ctx.RequestFrame()
	}
}

// handler runs a scenario's steps on the main thread: start at Start, the
// others as EvPost events posted by a timer (app.Post), in order.
type handler struct {
	app.DefaultHandler
	start func(a *app.App) error
	steps []func(a *app.App)
	quit  func(a *app.App, e *app.Event) // EvQuitRequested, when set
	urls  func(a *app.App, e *app.Event) // EvOpenURLs, when set
	next  int
}

func (h *handler) Start(a *app.App) error {
	if h.start != nil {
		return h.start(a)
	}
	return nil
}

func (h *handler) Event(a *app.App, e *app.Event) {
	switch {
	case e.Kind == app.EvPost && e.Source == platform.SourceApp:
		if h.next < len(h.steps) {
			h.next++
			h.steps[h.next-1](a)
		}
	case e.Kind == app.EvQuitRequested && h.quit != nil:
		h.quit(a, e)
	case e.Kind == app.EvOpenURLs && h.urls != nil:
		h.urls(a, e)
	default:
		a.DefaultEvent(e)
	}
}

// after posts the next step d from now, from the poster's thread.
func after(d time.Duration) { afterFor(d, 0) }

// afterFor is after with the post for window win: only that window draws for
// it (0: every window).
func afterFor(d time.Duration, win uint32) {
	posterOnce.Do(func() { go poster() })
	time.AfterFunc(d, func() { posts <- win })
}

// posts carries the steps' posts to poster, a goroutine on an OS thread of
// its own. app.Post calls PostMessageW, and the first user32 call of a
// thread makes it a GUI thread with a default input context: a USER object
// it keeps while it lives. Posted from the timers' goroutines, which run on
// whichever of the runtime's threads is free, the posts made more threads
// GUI threads as the scenario went on, and the process's USER count grew
// with them (cycles: one input context per thread that had posted).
var (
	posts      = make(chan uint32, 16)
	posterOnce sync.Once
)

func poster() {
	runtime.LockOSThread()
	for win := range posts {
		for !app.Post(app.Event{Source: platform.SourceApp, Window: win}, nil) {
			time.Sleep(time.Millisecond)
		}
	}
}

var cfg = app.WindowConfig{Title: "applive", Width: 320, Height: 220}

var (
	user32          = syscall.NewLazyDLL("user32.dll")
	getGuiResources = user32.NewProc("GetGuiResources")
	sendMessageW    = user32.NewProc("SendMessageW")
	getCurrentProc  = syscall.NewLazyDLL("kernel32.dll").NewProc("GetCurrentProcess")
)

// guiObjects is the process's GDI and USER object counts.
func guiObjects() (gdi, user int) {
	p, _, _ := getCurrentProc.Call()
	g, _, _ := getGuiResources.Call(p, 0)
	u, _, _ := getGuiResources.Call(p, 1)
	return int(g), int(u)
}

// ---- Two windows ----

// twowin: A animates while B idles, so B builds nothing meanwhile; then A
// closes and B keeps drawing when it is woken.
func init() {
	scenarios["twowin"] = func() {
		a1, b1 := &counter{}, &counter{}
		var wa, wb *app.Win
		var bIdle0, aBuilds0 int64
		h := &handler{}
		h.start = func(a *app.App) error {
			var err error
			if wa, err = a.OpenWindow(cfg, a1); err != nil {
				return err
			}
			if wb, err = a.OpenWindow(cfg, b1); err != nil {
				return err
			}
			afterFor(500*time.Millisecond, wa.ID()) // both settle; the steps' posts draw only A
			return nil
		}
		h.steps = []func(*app.App){
			func(a *app.App) { // A starts animating
				bIdle0, aBuilds0 = b1.builds.Load(), a1.builds.Load()
				a1.animate.Store(true)
				wa.Wake()
				afterFor(1500*time.Millisecond, wa.ID())
			},
			func(a *app.App) {
				say("a_frames=%d b_frames_while_a_animated=%d", a1.builds.Load()-aBuilds0, b1.builds.Load()-bIdle0)
				a1.animate.Store(false)
				wa.Close()
				after(300 * time.Millisecond)
			},
			func(a *app.App) { // B, alone, is woken three times
				say("a_open=%v windows=%d", wa.Open(), len(a.Windows(nil)))
				bIdle0 = b1.builds.Load()
				wb.Wake()
				after(200 * time.Millisecond)
			},
			func(a *app.App) { wb.Wake(); after(200 * time.Millisecond) },
			func(a *app.App) { wb.Wake(); after(300 * time.Millisecond) },
			func(a *app.App) {
				say("b_frames_after_close=%d live_surfaces=%d", b1.builds.Load()-bIdle0, gpu.LiveSurfaces())
				a.Quit()
			},
		}
		if err := app.Main(app.AppConfig{ID: "dev.gonativeui.applive"}, h); err != nil {
			fail("main: %v", err)
		}
		say("end_surfaces=%d", gpu.LiveSurfaces())
	}
}

func TestTwoWindowsPacedIndependentlyAndClosingOneKeepsTheOther(t *testing.T) {
	r := child(t, "twowin", 60*time.Second)
	if n := r.int("a_frames"); n < 10 {
		t.Errorf("the animating window drew %d frames in 1.5 s, want many\n%s", n, r.out)
	}
	if n := r.int("b_frames_while_a_animated"); n != 0 {
		t.Errorf("the idle window drew %d frames while the other animated, want 0", n)
	}
	if r.get("a_open") != "false" || r.int("windows") != 1 {
		t.Errorf("after the close: open %s, windows %s", r.get("a_open"), r.get("windows"))
	}
	if n := r.int("b_frames_after_close"); n < 3 {
		t.Errorf("the window left open drew %d frames for 3 wakes, want 3 or more", n)
	}
	if r.int("live_surfaces") != 1 || r.int("end_surfaces") != 0 {
		t.Errorf("surfaces %s with one window, %s at the end; want 1 and 0", r.get("live_surfaces"), r.get("end_surfaces"))
	}
}

// ---- Open and close ----

// cycles: with one window open throughout (the shared renderer stays), a
// second is opened and closed 20 times; the windows, surfaces, textures,
// hosts and GDI and USER objects come back to where they were.
//
// Both counts are read a second after a close. While windows open and close
// the process has a USER timer more, which goes 300-450 ms after the last
// close on the runners: the first count, read 350 ms after one, had it, and
// the last, 330 ms after, did not.
func init() {
	scenarios["cycles"] = func() {
		const settle = time.Second
		anchor := &counter{}
		var gdi0, user0, tex0 int
		var surf0 int64
		counted := false
		n := 0
		h := &handler{}
		h.start = func(a *app.App) error {
			_, err := a.OpenWindow(cfg, anchor)
			after(300 * time.Millisecond)
			return err
		}
		var cycle func(a *app.App)
		var w *app.Win
		cycle = func(a *app.App) {
			if w != nil {
				w.Close()
				w = nil
				h.steps = append(h.steps, cycle)
				after(30 * time.Millisecond)
				return
			}
			if n == 20 {
				h.steps = append(h.steps, func(a *app.App) {
					root := a.Windows(nil)[0].App()
					live, _ := root.R.TextureCounts()
					g, u := guiObjects()
					sc := platform.ShellObjects()
					say("gdi=%d,%d user=%d,%d textures=%d,%d surfaces=%d,%d windows=%d hosts=%d",
						gdi0, g, user0, u, tex0, live, surf0, gpu.LiveSurfaces(), platform.OpenCount(), sc.Hosts)
					a.Quit()
				})
				after(settle)
				return
			}
			if n == 1 && !counted { // after a first cycle: the class and what the first window loads once
				counted = true
				h.steps = append(h.steps, func(a *app.App) {
					root := a.Windows(nil)[0].App()
					tex0, _ = root.R.TextureCounts()
					gdi0, user0 = guiObjects()
					surf0 = gpu.LiveSurfaces()
					cycle(a)
				})
				after(settle)
				return
			}
			n++
			var err error
			if w, err = a.OpenWindow(cfg, &counter{}); err != nil {
				fail("open %d: %v", n, err)
			}
			h.steps = append(h.steps, cycle)
			after(30 * time.Millisecond)
		}
		h.steps = []func(*app.App){cycle}
		if err := app.Main(app.AppConfig{ID: "dev.gonativeui.applive"}, h); err != nil {
			fail("main: %v", err)
		}
	}
}

func TestOpenCloseCyclesLeakNothing(t *testing.T) {
	r := child(t, "cycles", 60*time.Second)
	pair := func(k string) (int, int) {
		a, b, _ := strings.Cut(r.get(k), ",")
		x, _ := strconv.Atoi(a)
		y, _ := strconv.Atoi(b)
		return x, y
	}
	for _, k := range []string{"gdi", "user", "textures", "surfaces"} {
		if before, after := pair(k); before != after || r.get(k) == "" {
			t.Errorf("%s: %d before 19 more cycles, %d after\n%s", k, before, after, r.out)
		}
	}
	if r.int("windows") != 1 || r.int("hosts") != 0 {
		t.Errorf("windows %s, hosts %s; want 1 and 0", r.get("windows"), r.get("hosts"))
	}
}

// ---- No window ----

// idle0: the app's only window closes and it runs on (KeepRunning) for 10 s
// with no window: no surface, no frame, and the pump sleeps.
func init() {
	scenarios["idle0"] = func() {
		c := &counter{}
		var p0, w0 uint64
		var built int64
		h := &handler{}
		h.start = func(a *app.App) error {
			w, err := a.OpenWindow(cfg, c)
			if err != nil {
				return err
			}
			w.Close()
			after(300 * time.Millisecond)
			return nil
		}
		h.steps = []func(*app.App){
			func(a *app.App) {
				say("windows=%d surfaces=%d", len(a.Windows(nil)), gpu.LiveSurfaces())
				built = c.builds.Load()
				p0, w0 = platform.PumpStats()
				after(10 * time.Second)
			},
			func(a *app.App) {
				p1, w1 := platform.PumpStats()
				say("pumps=%d wakes=%d builds=%d", p1-p0, w1-w0, c.builds.Load()-built)
				a.Quit()
			},
		}
		if err := app.Main(app.AppConfig{ID: "dev.gonativeui.applive", KeepRunning: true}, h); err != nil {
			fail("main: %v", err)
		}
	}
}

func TestTenSecondsWithNoWindow(t *testing.T) {
	r := child(t, "idle0", 60*time.Second)
	if r.int("windows") != 0 || r.int("surfaces") != 0 {
		t.Errorf("after the close: %s windows, %s surfaces; want 0 and 0\n%s", r.get("windows"), r.get("surfaces"), r.out)
	}
	if r.int("builds") != 0 {
		t.Errorf("%s frames with no window", r.get("builds"))
	}
	// The 10 s end with the timer's post (one wake) and the dispatcher's turn.
	if n := r.int("pumps"); n < 0 || n > 10 {
		t.Errorf("%s pump turns in 10 s with no window, want at most 10 (≤ 1 wakeup/s)\n%s", r.get("pumps"), r.out)
	}
}

// ---- A second instance's URL ----

// urls: the primary runs with no window and takes a second instance's
// notes-dev:x as EvOpenURLs.
func init() {
	scenarios["urls"] = func() {
		dir := os.Getenv("DIR")
		p, _, err := instance.AcquireWith("dev.gonativeui.applive", instance.Options{Dir: dir, Schemes: []string{"notes-dev"}})
		if err != nil || p == nil {
			fail("acquire: primary %v, %v", p != nil, err)
		}
		defer p.Close()
		h := &handler{}
		h.start = func(a *app.App) error {
			cmd := exec.Command(os.Args[0], "-test.run=^$")
			cmd.Env = append(os.Environ(), "GNUI_APPLIVE=urlsend", "DIR="+dir)
			cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
			if err := cmd.Start(); err != nil {
				return err
			}
			go cmd.Wait()
			return nil
		}
		h.urls = func(a *app.App, e *app.Event) {
			say("urls=%s source=%d polled=%d", strings.Join(e.Strings(), ","), e.Source, len(p.Poll(nil)))
			a.Quit()
		}
		if err := app.Main(app.AppConfig{ID: "dev.gonativeui.applive", KeepRunning: true}, h); err != nil {
			fail("main: %v", err)
		}
	}
	scenarios["urlsend"] = func() {
		_, s, err := instance.AcquireWith("dev.gonativeui.applive", instance.Options{Dir: os.Getenv("DIR"), Timeout: 10 * time.Second})
		if err != nil || s == nil {
			fail("acquire: secondary %v, %v", s != nil, err)
		}
		if err := s.Send([]string{"notes-dev:x"}); err != nil {
			fail("send: %v", err)
		}
	}
}

func TestSecondInstanceURLArrivesAsOpenURLs(t *testing.T) {
	r := child(t, "urls", 60*time.Second, "DIR="+t.TempDir())
	if r.get("urls") != "notes-dev:x" || r.int("source") != int(platform.SourceInstance) || r.int("polled") != 0 {
		t.Errorf("urls %q source %s polled %s, want notes-dev:x from the instance and nothing to poll\n%s",
			r.get("urls"), r.get("source"), r.get("polled"), r.out)
	}
}

// ---- The session's end ----

// logout: WM_QUERYENDSESSION, sent to the app window from another thread as
// Windows sends it, is a QuitLogout request. MODE=inside answers it during
// the request (the message returns TRUE); MODE=after answers 300 ms later
// (the message returns FALSE and the session is told to wait), and the
// allowed quit ends Main.
func init() {
	scenarios["logout"] = func() {
		mode := os.Getenv("MODE")
		got := make(chan uintptr, 1)
		h := &handler{}
		h.start = func(a *app.App) error {
			if _, err := a.OpenWindow(cfg, &counter{}); err != nil {
				return err
			}
			hwnd := platform.AppWindow()
			go func() {
				time.Sleep(300 * time.Millisecond)
				r, _, _ := sendMessageW.Call(hwnd, 0x0011, 0, 0x80000000) // WM_QUERYENDSESSION, ENDSESSION_LOGOFF
				got <- r
			}()
			return nil
		}
		h.quit = func(a *app.App, e *app.Event) {
			say("reason=%d", e.QuitReason())
			if mode == "inside" {
				a.ReplyQuit(e.ID, true)
				return
			}
			id := e.ID
			after(300 * time.Millisecond)
			h.steps = append(h.steps, func(a *app.App) {
				say("pending=%v", a.QuitPending())
				say("replied=%v", a.ReplyQuit(id, true))
			})
		}
		start := time.Now()
		if err := app.Main(app.AppConfig{ID: "dev.gonativeui.applive", QuitBlockReason: "applive: saving"}, h); err != nil {
			fail("main: %v", err)
		}
		say("result=%d ended_after_ms=%d", <-got, time.Since(start).Milliseconds())
	}
}

func TestQuitRepliedInsideAndAfterQueryEndSession(t *testing.T) {
	for _, mode := range []string{"inside", "after"} {
		r := child(t, "logout", 60*time.Second, "MODE="+mode)
		if r.int("reason") != int(platform.QuitLogout) {
			t.Errorf("%s: reason %s, want QuitLogout\n%s", mode, r.get("reason"), r.out)
		}
		want := 1
		if mode == "after" {
			want = 0
			if r.get("pending") != "true" || r.get("replied") != "true" {
				t.Errorf("after: pending %s, replied %s", r.get("pending"), r.get("replied"))
			}
		}
		if r.int("result") != want {
			t.Errorf("%s: WM_QUERYENDSESSION returned %s, want %d", mode, r.get("result"), want)
		}
	}
}
