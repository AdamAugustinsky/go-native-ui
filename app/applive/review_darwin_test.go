//go:build darwin

package applive

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
	"gonativeui/menu"
	"gonativeui/platform"
	"gonativeui/ui"
)

// A quit and a reopen that arrive inside a native loop entered from Build
// reach the handler after the frame, never inside it.
func TestNativeEventsInsideBuildWaitForTheFrame(t *testing.T) {
	t.Parallel()
	r := child(t, "heldnative", 60*time.Second)
	if r.code != 0 || r.get("nested") != "done" || r.get("events") != "reopen:inbuild=false,quit:inbuild=false" {
		t.Fatalf("exit %d, results %v\n%s", r.code, r.kv, r.out)
	}
}

// An idle app answers a Dock Quit by asking "Save changes?" with the public
// App.Alert: the alert shows while the quit waits, its answer arrives as an
// event, and decides the quit.
func TestAlertAnswersADeferredQuit(t *testing.T) {
	for _, mode := range []string{"allow", "refuse"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			r := child(t, "dialogquit", 60*time.Second, "MODE="+mode)
			ok := r.code == 0 && r.get("alert_shown") == "true" && r.get("pending") == "true" && r.get("dialog_ticket_ok") == "true"
			if mode == "allow" {
				ok = ok && r.get("button") == "0" && r.get("terminated") == "1" && r.get("main") == ""
			} else {
				ok = ok && r.get("button") == "1" && r.get("alive") == "1" && r.get("pending_after") == "false" && r.get("main") == "returned"
			}
			if !ok {
				t.Fatalf("exit %d, results %v\n%s", r.code, r.kv, r.out)
			}
		})
	}
}

// An allowed quit with an alert open: the alert's completion runs and its
// result is delivered before EvWillTerminate (the Dock's Quit, which AppKit's
// terminate: takes, and ⌘Q, which ends the sheet itself).
func TestSheetResultsComeBeforeWillTerminate(t *testing.T) {
	for _, route := range []string{"dock", "cmdq"} {
		t.Run(route, func(t *testing.T) {
			t.Parallel()
			r := child(t, "dialogexit", 60*time.Second, "ROUTE="+route)
			// ⌘Q is decided without terminate: and ends the sheet itself, as
			// cancelled; the Dock's Quit goes through AppKit's terminate:,
			// which ends it with the default button (observed): the result
			// is delivered all the same.
			want := "shown,quit,dialog:cancelled,willterminate"
			if route == "dock" {
				want = "shown,quit,dialog:answered,willterminate"
			}
			if r.code != 0 || r.get("order") != want {
				t.Fatalf("exit %d, results %v\n%s", r.code, r.kv, r.out)
			}
		})
	}
}

// A URL handed over while NSApp finishes launching (a launch by URL) reaches
// the handler, after Start.
func TestLaunchURLDuringFinishLaunching(t *testing.T) {
	t.Parallel()
	r := child(t, "launchurl", 30*time.Second)
	if r.code != 0 || r.get("order") != "start-returns|urls:gnui-test://launch" {
		t.Fatalf("exit %d, results %v\n%s", r.code, r.kv, r.out)
	}
}

// A windowless app with nothing to do still gets the system's wake.
func TestIdleWindowlessAppGetsTheWake(t *testing.T) {
	t.Parallel()
	r := child(t, "idlewake", 30*time.Second)
	if r.code != 0 || r.get("wake") != "1" {
		t.Fatalf("exit %d, results %v\n%s", r.code, r.kv, r.out)
	}
}

// Win.Close while a quit answer is pending closes the window then, not after
// the quit is decided.
func TestCloseDuringAPendingQuit(t *testing.T) {
	t.Parallel()
	r := child(t, "closequit", 60*time.Second)
	if r.code != 0 || r.get("closed") != "true" || r.get("visible") != "0" || r.get("pending") != "true" || r.get("terminated") != "1" {
		t.Fatalf("exit %d, results %v\n%s", r.code, r.kv, r.out)
	}
}

// The Dock menu's commands are enabled and checked as their model says,
// whenever AppKit shows it.
func TestDockMenuFollowsTheModel(t *testing.T) {
	t.Parallel()
	r := child(t, "dockstate", 30*time.Second)
	if r.code != 0 || r.get("dock") != "7:1:0,8:0:0,9:1:1" || r.get("dock2") != "7:1:0,8:1:0,9:1:0" {
		t.Fatalf("exit %d, results %v\n%s", r.code, r.kv, r.out)
	}
}

// The first Post after Main started allocates nothing.
func TestColdPostAllocatesNothing(t *testing.T) {
	t.Parallel()
	r := child(t, "coldpost", 30*time.Second, "GOMAXPROCS=1")
	if r.code != 0 || r.get("posted") != "true" || r.get("post_allocs") != "0" {
		t.Fatalf("exit %d, results %v\n%s", r.code, r.kv, r.out)
	}
}

// nestView runs a native loop inside Build once armed, as AppKit does inside
// calls a frame makes (an animated zoom, a modal session); inBuild says when.
type nestView struct {
	view
	arm, inBuild bool
	during       func()
}

func (v *nestView) Build(c *ui.Context) {
	v.view.Build(c)
	if !v.arm {
		return
	}
	v.arm = false
	v.inBuild = true
	v.during()
	runNested(400 * time.Millisecond)
	v.inBuild = false
	say("nested=done")
}

func init() {
	// heldnative: inside a native loop entered from Build, AppKit is asked
	// to terminate (terminate:) and the Dock sends a reopen.
	scenarios["heldnative"] = func() {
		v := &nestView{}
		var got []string
		v.during = func() {
			laterAfter(50*time.Millisecond, func() { objc.Send1(nsApp(), objc.Sel("terminate:"), 0) })
			laterAfter(100*time.Millisecond, func() { sendAppleEvent(0x61657674, 0x72617070, nil, nil) }) // 'aevt' 'rapp'
		}
		h := &handler{}
		h.start = func(a *app.App) error {
			if _, err := a.OpenWindow(winCfg, v); err != nil {
				say("skip=no-window")
				os.Exit(0)
			}
			after(400*time.Millisecond, 1)
			return nil
		}
		h.event = func(a *app.App, e *app.Event) bool {
			switch {
			case e.Kind == app.EvPost && e.ID == 1:
				v.arm = true // the next frame builds it
			case e.Kind == app.EvReopen:
				got = append(got, fmt.Sprintf("reopen:inbuild=%v", v.inBuild))
			case e.Kind == app.EvQuitRequested:
				got = append(got, fmt.Sprintf("quit:inbuild=%v", v.inBuild))
				a.ReplyQuit(quitID, false)
				after(300*time.Millisecond, 2)
			case e.Kind == app.EvPost && e.ID == 2:
				say("events=%s", strings.Join(got, ","))
				a.Quit()
			default:
				return false
			}
			return true
		}
		app.Main(app.AppConfig{KeepRunning: true}, h)
	}

	// dialogquit: the Dock's Quit; the handler asks "Save changes?" with
	// App.Alert and answers the quit with the alert's answer (MODE=allow:
	// the first button, refuse: the second).
	scenarios["dialogquit"] = func() {
		mode := os.Getenv("MODE")
		var alert platform.DialogTicket
		h := &handler{}
		h.start = func(a *app.App) error {
			if _, ok := openWindows(a, 1, &view{}); !ok {
				os.Exit(0)
			}
			after(400*time.Millisecond, 1)
			return nil
		}
		h.event = func(a *app.App, e *app.Event) bool {
			switch {
			case e.Kind == app.EvPost && e.ID == 1:
				quitLater()
			case e.Kind == app.EvQuitRequested:
				tk, err := a.Alert(platform.AlertSpec{Message: "Save changes?", Buttons: []string{"Save", "Cancel"}})
				if err != nil {
					fail("alert: %v", err)
				}
				alert = tk
				after(700*time.Millisecond, 2)
			case e.Kind == app.EvPost && e.ID == 2:
				win := ourWindow(a)
				sheet := objc.Send(win, objc.Sel("attachedSheet"))
				say("alert_shown=%v pending=%v", sheet != 0, a.QuitPending())
				code := 1000 // NSAlertFirstButtonReturn: Save
				if mode == "refuse" {
					code = 1001
				}
				a.AtSafePoint(func() { objc.Send2(win, objc.Sel("endSheet:returnCode:"), uintptr(sheet), uintptr(code)) })
			case e.Kind == app.EvService && e.Source == platform.SourceDialog:
				r, ok := a.TakeDialog()
				say("dialog_ticket_ok=%v button=%d", ok && e.ID == uint64(alert) && r.Ticket == alert, r.Button)
				a.ReplyQuit(quitID, r.Button == 0)
				if r.Button != 0 {
					after(300*time.Millisecond, 3)
				}
			case e.Kind == app.EvPost && e.ID == 3:
				say("alive=1 pending_after=%v", a.QuitPending())
				a.Quit()
			default:
				return false
			}
			return true
		}
		app.Main(app.AppConfig{KeepRunning: true}, h)
		say("main=returned")
	}

	// dialogexit: an alert is open when the quit (ROUTE=dock or cmdq) is
	// allowed at once.
	scenarios["dialogexit"] = func() {
		route := os.Getenv("ROUTE")
		var order []string
		h := &handler{}
		h.start = func(a *app.App) error {
			if _, ok := openWindows(a, 1, &view{}); !ok {
				os.Exit(0)
			}
			if _, err := a.Alert(platform.AlertSpec{Message: "Open", Buttons: []string{"OK"}}); err != nil {
				fail("alert: %v", err)
			}
			after(600*time.Millisecond, 1)
			return nil
		}
		h.event = func(a *app.App, e *app.Event) bool {
			switch {
			case e.Kind == app.EvPost && e.ID == 1:
				if objc.Send(ourWindow(a), objc.Sel("attachedSheet")) != 0 {
					order = append(order, "shown")
				}
				if route == "dock" {
					quitLater()
				} else {
					postCmdQ(windowNumber(ourWindow(a)))
				}
			case e.Kind == app.EvQuitRequested:
				order = append(order, "quit")
				a.ReplyQuit(quitID, true)
			case e.Kind == app.EvService && e.Source == platform.SourceDialog:
				r, _ := a.TakeDialog()
				out := "answered"
				if r.Outcome == platform.DialogCancelled {
					out = "cancelled"
				}
				order = append(order, "dialog:"+out)
			case e.Kind == app.EvWillTerminate:
				order = append(order, "willterminate")
				say("order=%s", strings.Join(order, ","))
			default:
				return false
			}
			return true
		}
		app.Main(app.AppConfig{KeepRunning: true}, h)
		say("main=returned")
	}

	// launchurl: a URL is handed to the delegate while NSApp finishes
	// launching (application:openURLs:, as for a launch by URL).
	scenarios["launchurl"] = func() {
		def := objc.Define("GNUITestLaunch", objc.Cls("NSObject"), nil)
		def.Method(objc.Sel("gnuiWillFinish:"), "v@:@", func(*ffi.CB) {
			url := objc.Send1(objc.Cls("NSURL"), objc.Sel("URLWithString:"), uintptr(objc.Str("gnui-test://launch")))
			arr := objc.Send1(objc.Cls("NSArray"), objc.Sel("arrayWithObject:"), uintptr(url))
			objc.Send2(objc.ID(platform.AppDelegate()), objc.Sel("application:openURLs:"), uintptr(nsApp()), uintptr(arr))
		})
		obs := objc.Send(objc.Send(def.Register(), objc.Sel("alloc")), objc.Sel("init"))
		nc := objc.Send(objc.Cls("NSNotificationCenter"), objc.Sel("defaultCenter"))
		objc.AddObserver(nc, obs, objc.Sel("gnuiWillFinish:"), objc.Str("NSApplicationWillFinishLaunchingNotification"), 0)
		var order []string
		h := &handler{}
		h.start = func(a *app.App) error {
			order = append(order, "start-returns")
			after(500*time.Millisecond, 1)
			return nil
		}
		h.event = func(a *app.App, e *app.Event) bool {
			switch e.Kind {
			case app.EvOpenURLs:
				order = append(order, "urls:"+strings.Join(e.Strings(), ","))
			case app.EvPost:
				say("order=%s", strings.Join(order, "|"))
				a.Quit()
			default:
				return false
			}
			return true
		}
		app.Main(app.AppConfig{KeepRunning: true}, h)
	}

	// idlewake: no window and nothing to do; half a second in, the system
	// says it woke.
	scenarios["idlewake"] = func() {
		h := &handler{}
		h.start = func(a *app.App) error {
			laterAfter(500*time.Millisecond, func() {
				ws := objc.Send(objc.Cls("NSWorkspace"), objc.Sel("sharedWorkspace"))
				nc := objc.Send(ws, objc.Sel("notificationCenter"))
				objc.Send2(nc, objc.Sel("postNotificationName:object:"), uintptr(objc.Str("NSWorkspaceDidWakeNotification")), uintptr(ws))
			})
			return nil
		}
		h.event = func(a *app.App, e *app.Event) bool {
			if e.Kind == app.EvDidWake {
				say("wake=1")
				a.Quit()
				return true
			}
			return false
		}
		watchdog(20 * time.Second)
		app.Main(app.AppConfig{KeepRunning: true}, h)
	}

	// closequit: the window is closed (Win.Close) while the Dock's Quit
	// waits for its answer, which then allows it.
	scenarios["closequit"] = func() {
		var w *app.Win
		h := &handler{}
		h.start = func(a *app.App) error {
			var ok bool
			if w, ok = openWindows(a, 1, &view{}); !ok {
				os.Exit(0)
			}
			after(400*time.Millisecond, 1)
			return nil
		}
		h.event = func(a *app.App, e *app.Event) bool {
			switch {
			case e.Kind == app.EvPost && e.ID == 1:
				quitLater()
			case e.Kind == app.EvQuitRequested:
				w.Close()
				after(600*time.Millisecond, 2)
			case e.Kind == app.EvPost && e.ID == 2:
				say("closed=%v visible=%d pending=%v", !w.Open(), visibleWindows(), a.QuitPending())
				a.ReplyQuit(quitID, true)
			default:
				return false
			}
			return true
		}
		app.Main(app.AppConfig{KeepRunning: true}, h)
		say("main=returned")
	}

	// dockstate: a Dock menu of three commands, 8 disabled and 9 checked;
	// then 8 enabled and 9 unchecked. AppKit validates the menu as the Dock
	// shows it ([NSMenu update]).
	scenarios["dockstate"] = func() {
		m, err := menu.NewModel([]menu.Command{{ID: 7, Title: "New Note"}, {ID: 8, Title: "Sync"}, {ID: 9, Title: "Do Not Disturb"}},
			[]menu.Menu{{Title: "Dock", Items: []menu.Item{{Kind: menu.ItemCommand, Command: 7}, {Kind: menu.ItemCommand, Command: 8}, {Kind: menu.ItemCommand, Command: 9}}}}, nil)
		if err != nil {
			fail("model: %v", err)
		}
		m.SetEnabled(8, false)
		m.SetChecked(9, true)
		state := func() string {
			dm := objc.ID(platform.DockMenu())
			objc.Send(dm, objc.Sel("update"))
			var parts []string
			n := int(objc.Send(dm, objc.Sel("numberOfItems")))
			for i := 0; i < n; i++ {
				it := objc.Send1(dm, objc.Sel("itemAtIndex:"), uintptr(i))
				en := 0
				if objc.SendBool(it, objc.Sel("isEnabled")) {
					en = 1
				}
				parts = append(parts, fmt.Sprintf("%d:%d:%d", objc.Send(it, objc.Sel("tag")), en, objc.Send(it, objc.Sel("state"))))
			}
			return strings.Join(parts, ",")
		}
		h := &handler{}
		h.start = func(a *app.App) error {
			a.SetDockMenu(m, 0)
			after(300*time.Millisecond, 1)
			return nil
		}
		h.event = func(a *app.App, e *app.Event) bool {
			if e.Kind != app.EvPost {
				return false
			}
			say("dock=%s", state())
			m.SetEnabled(8, true)
			m.SetChecked(9, false)
			say("dock2=%s", state())
			a.Quit()
			return true
		}
		app.Main(app.AppConfig{KeepRunning: true}, h)
	}

	// coldpost: the first app.Post of the process, from Start.
	scenarios["coldpost"] = func() {
		h := &handler{}
		h.start = func(a *app.App) error {
			var ms runtime.MemStats
			runtime.ReadMemStats(&ms)
			m0 := ms.Mallocs
			ok := app.Post(app.Event{ID: 1}, []byte("first"))
			runtime.ReadMemStats(&ms)
			say("posted=%v post_allocs=%d", ok, ms.Mallocs-m0)
			return nil
		}
		h.event = func(a *app.App, e *app.Event) bool {
			if e.Kind == app.EvPost {
				a.Quit()
				return true
			}
			return false
		}
		app.Main(app.AppConfig{KeepRunning: true}, h)
	}
}
