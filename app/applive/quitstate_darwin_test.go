//go:build darwin

package applive

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/internal/objc"
	"gonativeui/platform"
	"gonativeui/ui"
)

// A logout that arrives while a frame is being built (a native loop entered
// from Build) is not refused: its event waits, the request reaches the
// handler after the frame, and the answer given later is the sender's reply
// (no error: the logout goes on).
func TestLogoutDuringBuildIsDeferred(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "sender.txt")
	r := child(t, "buildlogout", 60*time.Second, "OUT="+out)
	sent := readSender(out, 20*time.Second)
	if r.code != 0 || r.get("held_suspended") != "1" || r.get("reason") != "3" || r.get("inbuild") != "false" || r.get("terminated") != "1" ||
		sent["errn"] != "0" || sent["send_err"] != "0" {
		t.Fatalf("exit %d, results %v, sender %v\n%s", r.code, r.kv, sent, r.out)
	}
	t.Logf("%v sender %v", r.kv, sent)
}

// Leaving Main while a quit Apple event waits for its answer (App.Quit, or
// the last window closing) answers it (no error: the app is ending), and the
// open alert's cancelled result reaches the handler before EvWillTerminate.
func TestLeavingMainAnswersASuspendedQuit(t *testing.T) {
	for _, route := range []string{"quit", "close"} {
		t.Run(route, func(t *testing.T) {
			t.Parallel()
			out := filepath.Join(t.TempDir(), "sender.txt")
			r := child(t, "exitsusp", 60*time.Second, "ROUTE="+route, "OUT="+out)
			sent := readSender(out, 20*time.Second)
			if r.code != 0 || r.get("suspended") != "1" || r.get("order") != "shown,quit,dialog:cancelled,willterminate" ||
				r.get("main") != "returned" || r.get("after_suspended") != "0" || sent["errn"] != "0" || sent["send_err"] != "0" {
				t.Fatalf("exit %d, results %v, sender %v\n%s", r.code, r.kv, sent, r.out)
			}
			t.Logf("%v sender %v", r.kv, sent)
		})
	}
}

// Between a quit's approval and its termination, a second quit Apple event
// and a ⌘Q neither ask again nor replace the termination: the event is
// answered yes, ⌘Q does nothing, and the app ends once, as approved.
func TestApprovedQuitIsNotDisplaced(t *testing.T) {
	t.Parallel()
	r := child(t, "commitwin", 60*time.Second)
	if r.code != 0 || r.get("control") != "true" || r.get("requests") != "2" || r.get("after") != "1" || r.get("second_errn") != "0" ||
		r.get("second_send_err") != "0" || r.get("terminated") != "1" {
		t.Fatalf("exit %d, results %v\n%s", r.code, r.kv, r.out)
	}
}

// pumpNested runs an event loop of its own for d, as a modal loop does: it
// takes the events off the queue and sends them, so an Apple event from
// another process is handled in it (a run loop alone does not handle those).
func pumpNested(d time.Duration) {
	app := nsApp()
	selNext, selSend, selDate := objc.Sel("nextEventMatchingMask:untilDate:inMode:dequeue:"), objc.Sel("sendEvent:"), objc.Sel("dateWithTimeIntervalSinceNow:")
	mode, cls := objc.Str("kCFRunLoopDefaultMode"), objc.Cls("NSDate")
	for end := time.Now().Add(d); time.Now().Before(end); {
		date := objc.ID(objc.F.Reset().Arg(uintptr(cls)).Arg(uintptr(selDate)).ArgF64(0.01).Call(objc.MsgSend).R0())
		ev := objc.ID(objc.F.Reset().Arg(uintptr(app)).Arg(uintptr(selNext)).Arg(^uintptr(0)).Arg(uintptr(date)).Arg(uintptr(mode)).ArgBool(true).Call(objc.MsgSend).R0())
		if ev != 0 {
			objc.Send1(app, selSend, uintptr(ev))
		}
	}
}

// waitView runs a native event loop from inside Build once armed (pumpNested):
// during starts what is to arrive meanwhile, and the loop runs until until()
// holds.
type waitView struct {
	view
	arm, inBuild bool
	during       func()
	until        func() bool
}

func (v *waitView) Build(c *ui.Context) {
	v.view.Build(c)
	if !v.arm {
		return
	}
	v.arm = false
	v.inBuild = true
	v.during()
	for end := time.Now().Add(15 * time.Second); !v.until() && time.Now().Before(end); {
		pumpNested(50 * time.Millisecond)
	}
	v.inBuild = false
}

func init() {
	// buildlogout: a logout (kAEReallyLogOut, from another process) arrives
	// in a native loop inside Build; the request is allowed 300 ms after it
	// reaches the handler.
	scenarios["buildlogout"] = func() {
		v := &waitView{}
		v.during = func() { startQuitSender(os.Getpid(), "rlgo", os.Getenv("OUT")) }
		v.until = func() bool { return platform.SuspendedQuitEvents() > 0 }
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
			case e.Kind == app.EvQuitRequested:
				say("reason=%d inbuild=%v held_suspended=%d", e.A, v.inBuild, platform.SuspendedQuitEvents())
				after(300*time.Millisecond, 2)
			case e.Kind == app.EvPost && e.ID == 2:
				a.ReplyQuit(quitID, true)
			default:
				return false
			}
			return true
		}
		app.Main(app.AppConfig{KeepRunning: true}, h)
		say("main=returned")
	}

	// exitsusp: an alert is open and the Dock's Quit (from another process)
	// waits for its answer when the app leaves Main: ROUTE quit (App.Quit)
	// or close (its last window closes).
	scenarios["exitsusp"] = func() {
		var order []string
		var w *app.Win
		h := &handler{}
		h.start = func(a *app.App) error {
			var ok bool
			if w, ok = openWindows(a, 1, &view{}); !ok {
				os.Exit(0)
			}
			if _, err := a.Alert(platform.AlertSpec{Message: "Delete everything?", Buttons: []string{"Delete", "Cancel"}}); err != nil {
				fail("alert: %v", err)
			}
			after(600*time.Millisecond, 1)
			return nil
		}
		h.event = func(a *app.App, e *app.Event) bool {
			switch {
			case e.Kind == app.EvPost && e.ID == 1:
				order = append(order, "shown")
				startQuitSender(os.Getpid(), "", os.Getenv("OUT"))
			case e.Kind == app.EvQuitRequested:
				order = append(order, "quit")
				after(500*time.Millisecond, 2) // no answer
			case e.Kind == app.EvPost && e.ID == 2:
				say("suspended=%d", platform.SuspendedQuitEvents())
				if os.Getenv("ROUTE") == "close" {
					w.Close()
				} else {
					a.Quit()
				}
			case e.Kind == app.EvService && e.Source == platform.SourceDialog:
				r, _ := a.TakeDialog()
				out := "answered"
				if r.Outcome == platform.DialogCancelled {
					out = "cancelled"
				}
				order = append(order, "dialog:"+out)
			case e.Kind == app.EvWillTerminate:
				order = append(order, "willterminate")
				say("order=%s after_suspended=%d", strings.Join(order, ","), platform.SuspendedQuitEvents())
			default:
				return false
			}
			return true
		}
		app.Main(app.AppConfig{}, h)
		say("main=returned")
		time.Sleep(time.Second) // the sender reads its reply while the process lives
	}

	// commitwin: a ⌘Q (refused) shows the menu's Quit reaches the policy;
	// then the Dock's Quit is allowed 300 ms later, and in the same handler,
	// before the app's loop sends the termination, a second quit Apple event
	// and a ⌘Q arrive.
	scenarios["commitwin"] = func() {
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
				later(func() { say("control=%v", pressCmdQ(0)) })
			case e.Kind == app.EvQuitRequested && h.requests == 1: // the control
				a.ReplyQuit(quitID, false)
				after(300*time.Millisecond, 2)
			case e.Kind == app.EvPost && e.ID == 2:
				quitLater()
			case e.Kind == app.EvQuitRequested:
				after(300*time.Millisecond, 3)
			case e.Kind == app.EvPost && e.ID == 3:
				a.ReplyQuit(quitID, true)
				errn, sendErr := sendQuitWithReply()
				pressCmdQ(0)
				say("after=1 second_errn=%d second_send_err=%d", errn, sendErr)
			default:
				return false
			}
			return true
		}
		app.Main(app.AppConfig{KeepRunning: true}, h)
		say("main=returned")
	}
}
