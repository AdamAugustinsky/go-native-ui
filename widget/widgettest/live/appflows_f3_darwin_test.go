//go:build darwin

package live

// The app-level flows of phase 6's F3, driven through AppKit state: reopen,
// quit with no window, one and two (⌘Q through the menu, a quit Apple event
// the process sends itself, the app's own request), a refusal and a delayed
// reply, and a menu command with no window open. They need the application
// object (app.Main, Handler, RequestQuit, ReplyQuit).
//
// An allowed quit ends the process, so each flow is a Scenario: a child
// process of the test binary that runs app.Main and reports what it saw
// (widgettest.Say); the test reads the report and the exit status. F3's own
// tests (app/applive) go deeper into the quit policy (latching, deadlines,
// saturation, logout); these are the flows a user can name.

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/internal/objc"
	"gonativeui/menu"
	"gonativeui/platform"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// flat is a window's builder: one box that fills it. It can refuse to close.
type flatView struct{ refuse bool }

func (flatView) Build(c *ui.Context) {
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.Box(0, "bg")
}

func (v flatView) CloseRequested(*app.App) bool { return !v.refuse }

// flowHandler is an app.Handler made of two functions.
type flowHandler struct {
	start func(a *app.App) error
	event func(a *app.App, e *app.Event) bool
}

func (h *flowHandler) Start(a *app.App) error {
	if h.start != nil {
		return h.start(a)
	}
	return nil
}

func (h *flowHandler) Event(a *app.App, e *app.Event) {
	if e.Kind == app.EvWillTerminate {
		widgettest.Say("terminated=1 windows=%d", widgettest.VisibleWindows())
	}
	if h.event != nil && h.event(a, e) {
		return
	}
	a.DefaultEvent(e)
}

func envInt(k string) int { n, _ := strconv.Atoi(os.Getenv(k)); return n }

// openWindows opens n windows: the app's own first, plain AppKit windows for
// the others (two windows of the framework's own come with F4). It reports a
// skip when no window can be opened.
func openWindows(a *app.App, n int) (*app.Win, bool) {
	if n == 0 {
		return nil, true
	}
	w, err := a.OpenWindow(app.WindowConfig{Title: "flow", Width: 320, Height: 200}, flatView{})
	if err != nil {
		widgettest.Say("skip=no-window:%s", err.Error())
		return nil, false
	}
	for i := 1; i < n; i++ {
		widgettest.OpenPlainWindow(fmt.Sprintf("other %d", i), float64(60*i), 80)
	}
	return w, true
}

func ourNumber(a *app.App) int {
	if a.Win == nil {
		return 0
	}
	return int(objc.Send(objc.ID(a.Win.Native().NSWindow), objc.Sel("windowNumber")))
}

var quitID uint64

func init() {
	// f3-quit: the quit by ROUTE (dock, cmdq, request) with WINDOWS open.
	widgettest.RegisterScenario("f3-quit", func() {
		route, n := os.Getenv("ROUTE"), envInt("WINDOWS")
		h := &flowHandler{}
		h.start = func(a *app.App) error {
			if _, ok := openWindows(a, n); !ok {
				os.Exit(0)
			}
			widgettest.After(400*time.Millisecond, func() {
				widgettest.Say("open_windows=%d", widgettest.VisibleWindows())
				switch route {
				case "dock":
					widgettest.SendQuitEvent()
				case "cmdq":
					widgettest.PressCmdQ(ourNumber(a))
				case "request":
					a.RequestQuit()
				}
			})
			return nil
		}
		h.event = func(a *app.App, e *app.Event) bool {
			if e.Kind == app.EvQuitRequested {
				widgettest.Say("reason=%d", e.QuitReason())
			}
			return false
		}
		err := app.Main(app.AppConfig{KeepRunning: true}, h)
		widgettest.Say("main_returned=1 err=%v", err)
	})

	// f3-refuse-delay: the first quit is refused, the app goes on; the second
	// is answered 1.5 s later while a worker posts events, and allowed.
	widgettest.RegisterScenario("f3-refuse-delay", func() {
		var posted, postedDuring int
		var asked time.Time
		var requests int
		answerLater := false
		h := &flowHandler{}
		h.start = func(a *app.App) error {
			if _, ok := openWindows(a, 1); !ok {
				os.Exit(0)
			}
			widgettest.After(300*time.Millisecond, func() { widgettest.SendQuitEvent() })
			return nil
		}
		h.event = func(a *app.App, e *app.Event) bool {
			switch {
			case e.Kind == app.EvQuitRequested:
				requests++
				quitID = e.ID
				if requests == 1 {
					a.ReplyQuit(e.ID, false)
					widgettest.After(400*time.Millisecond, func() {
						widgettest.Say("alive_after_refusal=%v pending=%v", true, a.QuitPending())
						widgettest.SendQuitEvent()
					})
					return true
				}
				answerLater, asked = true, time.Now()
				go func() { // a worker posts for the whole wait
					for i := 0; i < 8; i++ {
						time.Sleep(150 * time.Millisecond)
						for !app.Post(app.Event{ID: uint64(100 + i)}, nil) {
							time.Sleep(time.Millisecond)
						}
					}
				}()
				widgettest.After(1500*time.Millisecond, func() {
					widgettest.Say("pending_before_reply=%v", a.QuitPending())
					a.ReplyQuit(quitID, true)
				})
				return true
			case e.Kind == app.EvPost && e.ID >= 100:
				posted++
				if answerLater && a.QuitPending() {
					postedDuring++
				}
				return true
			case e.Kind == app.EvWillTerminate:
				widgettest.Say("requests=%d posted=%d posted_during=%d waited_ms=%d", requests, posted, postedDuring, time.Since(asked).Milliseconds())
			}
			return false
		}
		app.Main(app.AppConfig{KeepRunning: true}, h)
	})

	// f3-reopen: the Dock icon is clicked while the window is hidden, then
	// while one is visible.
	widgettest.RegisterScenario("f3-reopen", func() {
		var reopens []int64
		h := &flowHandler{}
		var theApp *app.App
		h.start = func(a *app.App) error {
			theApp = a
			if _, ok := openWindows(a, 1); !ok {
				os.Exit(0)
			}
			widgettest.After(500*time.Millisecond, func() {
				ours := widgettest.PlainWindow{Window: objc.ID(a.Win.Native().NSWindow)}
				ours.Hide()
				widgettest.Say("visible_before=%d", widgettest.VisibleWindows())
				dockClick(false)
				widgettest.After(300*time.Millisecond, func() {
					widgettest.Say("visible_after_reopen=%d", widgettest.VisibleWindows())
					dockClick(true) // a visible window: nothing to show
					widgettest.After(300*time.Millisecond, func() {
						widgettest.Say("reopens=%d first_a=%d second_a=%d", len(reopens), reopens[0], reopens[1])
						a.Quit()
					})
				})
			})
			return nil
		}
		h.event = func(a *app.App, e *app.Event) bool {
			if e.Kind == app.EvReopen {
				reopens = append(reopens, e.A)
			}
			return false
		}
		_ = theApp
		app.Main(app.AppConfig{KeepRunning: true}, h)
	})

	// f3-menucmd: no window is open; File ▸ New in the menu bar is chosen
	// through AppKit and arrives as an app-wide command.
	widgettest.RegisterScenario("f3-menucmd", func() {
		const cmdNew menu.ID = 5
		m, err := menu.NewModel([]menu.Command{{ID: cmdNew, Title: "New"}},
			[]menu.Menu{{Title: "File", Items: []menu.Item{{Kind: menu.ItemCommand, Command: cmdNew}}}}, []int{0})
		if err != nil {
			widgettest.Fail("model: %v", err)
		}
		h := &flowHandler{}
		h.start = func(a *app.App) error {
			a.SetMenu(m)
			widgettest.After(500*time.Millisecond, func() {
				widgettest.Say("windows=%d", widgettest.VisibleWindows())
				it, ok := widgettest.FindMenuItem("File", "New")
				widgettest.Say("item=%v enabled=%v", ok, it.Enabled)
				if ok {
					it.Perform()
				}
				widgettest.After(400*time.Millisecond, func() { widgettest.Say("timeout=1"); a.Quit() })
			})
			return nil
		}
		h.event = func(a *app.App, e *app.Event) bool {
			if e.Kind == app.EvCommand {
				widgettest.Say("command=%d", e.ID)
				a.Quit()
				return true
			}
			return false
		}
		app.Main(app.AppConfig{KeepRunning: true}, h)
	})

	// f3-fakes: the app-event fakes reach a Handler as the real ones do.
	widgettest.RegisterScenario("f3-fakes", func() {
		var seen []string
		h := &flowHandler{}
		h.start = func(a *app.App) error {
			widgettest.After(300*time.Millisecond, func() {
				widgettest.FakeNotificationResponse(a, 9, 2, "hello")
				widgettest.FakeOpenURLs(a, "gnui-test://a", "gnui-test://b")
				widgettest.FakeReopen(a, false)
				widgettest.Say("seen=%v", len(seen))
				for i, s := range seen {
					widgettest.Say("e%d=%s", i, s)
				}
				a.Quit()
			})
			return nil
		}
		h.event = func(a *app.App, e *app.Event) bool {
			switch e.Kind {
			case app.EvService:
				seen = append(seen, fmt.Sprintf("service:%d:%d:%d:%s", e.Source, e.ID, e.A, e.Data()))
			case app.EvOpenURLs:
				seen = append(seen, fmt.Sprintf("urls:%d:%s", e.A, strings.Join(e.Strings(), "|")))
			case app.EvReopen:
				seen = append(seen, fmt.Sprintf("reopen:%d", e.A))
			default:
				return false
			}
			return true
		}
		app.Main(app.AppConfig{KeepRunning: true}, h)
	})
}

// dockClick calls the application delegate's reopen method as AppKit does
// when the Dock icon is clicked: applicationShouldHandleReopen:hasVisibleWindows:.
func dockClick(visible bool) {
	app := widgettest.NSApp()
	del := objc.Send(app, objc.Sel("delegate"))
	sel := objc.Sel("applicationShouldHandleReopen:hasVisibleWindows:")
	objc.F.Reset().Arg(uintptr(del)).Arg(uintptr(sel)).Arg(uintptr(app)).ArgBool(visible).Call(objc.MsgSend)
}

// Quitting by the Dock's Quit, ⌘Q or the app's own request ends the process
// after EvWillTerminate with no window open, one, and two: the default
// handler allows it at once, whatever else is open.
func TestF3QuitWithZeroOneTwoWindows(t *testing.T) {
	for _, route := range []string{"dock", "cmdq", "request"} {
		for n := 0; n <= 2; n++ {
			t.Run(fmt.Sprintf("%s/%dwindows", route, n), func(t *testing.T) {
				r := widgettest.Scenario(t, "f3-quit", 60*time.Second, "ROUTE="+route, fmt.Sprintf("WINDOWS=%d", n))
				want := map[string]int{"cmdq": int(app.QuitMenu), "dock": int(app.QuitDock), "request": int(app.QuitApp)}[route]
				if r.Code != 0 || r.Get("terminated") != "1" || r.Int("reason") != want || r.Get("main_returned") != "" || r.Int("open_windows") < n {
					t.Fatalf("exit %d, report %v\n%s", r.Code, r.KV, r.Out)
				}
			})
		}
	}
}

// A refusal leaves the app running; the next quit, answered 1.5 s late, is
// delivered while the answer is awaited (the worker's posts arrive) and then
// allowed.
func TestF3RefusalThenDelayedAllow(t *testing.T) {
	r := widgettest.Scenario(t, "f3-refuse-delay", 60*time.Second)
	if r.Code != 0 || r.Get("alive_after_refusal") != "true" || r.Get("terminated") != "1" || r.Int("requests") != 2 ||
		r.Get("pending_before_reply") != "true" || r.Int("posted_during") < 3 || r.Int("waited_ms") < 1500 {
		t.Fatalf("exit %d, report %v\n%s", r.Code, r.KV, r.Out)
	}
}

// The Dock icon's click with the window hidden shows it again (the default
// handler); with a window visible it is only reported.
func TestF3Reopen(t *testing.T) {
	r := widgettest.Scenario(t, "f3-reopen", 60*time.Second)
	if r.Code != 0 || r.Int("visible_before") != 0 || r.Int("visible_after_reopen") != 1 || r.Int("reopens") != 2 || r.Int("first_a") != 0 || r.Int("second_a") != 1 {
		t.Fatalf("exit %d, report %v\n%s", r.Code, r.KV, r.Out)
	}
}

// A menu command chosen with no window open is delivered to the handler.
func TestF3MenuCommandWithNoWindow(t *testing.T) {
	r := widgettest.Scenario(t, "f3-menucmd", 60*time.Second)
	if r.Code != 0 || r.Int("windows") != 0 || r.Get("item") != "true" || r.Get("enabled") != "true" || r.Int("command") != 5 || r.Get("timeout") != "" {
		t.Fatalf("exit %d, report %v\n%s", r.Code, r.KV, r.Out)
	}
}

// The fakes of app events reach the Handler with their data.
func TestF3FakeAppEvents(t *testing.T) {
	r := widgettest.Scenario(t, "f3-fakes", 60*time.Second)
	if r.Code != 0 || r.Int("seen") != 3 || r.Get("e0") != fmt.Sprintf("service:%d:9:2:hello", int(platform.SourceNotifyResponse)) ||
		r.Get("e1") != "urls:2:gnui-test://a|gnui-test://b" || r.Get("e2") != "reopen:0" {
		t.Fatalf("exit %d, report %v\n%s", r.Code, r.KV, r.Out)
	}
}
