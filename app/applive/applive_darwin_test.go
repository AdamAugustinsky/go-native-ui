//go:build darwin

package applive

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The Dock's Quit and ⌘Q, with no window, one and two: each arrives as
// EvQuitRequested with its reason, the default handler allows it at once
// (NSTerminateNow, never replyToApplicationShouldTerminate:), and the
// process ends after EvWillTerminate without Main returning.
func TestQuitRoutes(t *testing.T) {
	for _, route := range []string{"dock", "cmdq"} {
		for n := 0; n <= 2; n++ {
			t.Run(fmt.Sprintf("%s/%dwindows", route, n), func(t *testing.T) {
				t.Parallel()
				r := child(t, "route", 60*time.Second, "ROUTE="+route, fmt.Sprintf("WINDOWS=%d", n))
				want := "2" // QuitDock
				if route == "cmdq" {
					want = "1" // QuitMenu
				}
				if r.code != 0 || r.get("terminated") != "1" || r.get("reason") != want || r.get("early") != "0" || r.get("ours") != "0" ||
					r.get("last") != "now" || r.get("asking") != "true" || r.get("main") != "" || r.int("windows") < n {
					t.Fatalf("exit %d, results %v\n%s", r.code, r.kv, r.out)
				}
				t.Logf("%v (%v)", r.kv, r.took.Round(time.Millisecond))
			})
		}
	}
}

// ⌘Q under a sheet asks the app without AppKit's terminate:, which would end
// the sheet even when the quit is refused: refused, the sheet stays; allowed
// (at once or later), the sheet is ended and the app terminates.
func TestQuitUnderASheet(t *testing.T) {
	for _, mode := range []string{"now", "later"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			r := child(t, "sheetquit", 60*time.Second, "MODE="+mode)
			if r.code != 0 || r.get("sheet_before") != "true" || r.get("reason1") != "1" || r.get("sheet_after_refuse") != "true" ||
				r.get("terminated") != "1" || r.get("requests") != "2" || r.get("last") != "now" || r.get("sheet_at_exit") != "false" ||
				mode == "later" && r.get("pending") != "true" {
				t.Fatalf("exit %d, results %v\n%s", r.code, r.kv, r.out)
			}
		})
	}
}

// ⌘Q is the native menu's Quit item, decided by the app's model like every
// system item: disabled, it asks nothing; enabled, it is a quit request.
func TestQuitItemFollowsTheModel(t *testing.T) {
	for n := 0; n <= 1; n++ {
		t.Run(fmt.Sprintf("%dwindows", n), func(t *testing.T) {
			t.Parallel()
			r := child(t, "quitmodel", 60*time.Second, fmt.Sprintf("WINDOWS=%d", n))
			if r.code != 0 || r.get("requests_disabled") != "0" || r.get("reason") != "1" || r.get("terminated") != "1" {
				t.Fatalf("exit %d, results %v\n%s", r.code, r.kv, r.out)
			}
		})
	}
}

// Answers given inside the quit request are latched. For a terminate: sent
// by someone else (VIA terminate), applicationShouldTerminate: returns
// NSTerminateNow for a synchronous allow, NSTerminateCancel for a refusal,
// and a reply from a native call nested in the event is latched too;
// replyToApplicationShouldTerminate: is never called. For the quit Apple
// event the delegate is not asked: a refusal is the event's userCanceledErr
// reply, an allowed quit sends terminate: answered "now".
func TestQuitAnswersLatched(t *testing.T) {
	for _, via := range []string{"aevt", "terminate"} {
		for _, mode := range []string{"allow", "refuse", "nested"} {
			t.Run(via+"/"+mode, func(t *testing.T) {
				t.Parallel()
				r := child(t, "latch", 60*time.Second, "MODE="+mode, "VIA="+via)
				if r.code != 0 || r.get("early") != "0" || r.get("ours") != "0" {
					t.Fatalf("exit %d, results %v\n%s", r.code, r.kv, r.out)
				}
				switch mode {
				case "refuse":
					last, reply := "cancel", "1" // no quit event answered
					if via == "aevt" {
						last, reply = "none", "-128"
					}
					if r.get("alive") != "1" || r.get("last") != last || r.get("quit_reply") != reply || r.get("pending") != "false" || r.get("main") != "returned" || r.get("terminated") != "1" {
						t.Fatalf("refused: %v\n%s", r.kv, r.out)
					}
				default:
					if r.get("terminated") != "1" || r.get("last") != "now" || r.get("main") != "" {
						t.Fatalf("allowed: %v\n%s", r.kv, r.out)
					}
				}
			})
		}
	}
}

// A refusal, then a second request that is allowed.
func TestQuitRefuseThenAllow(t *testing.T) {
	t.Parallel()
	r := child(t, "refuseallow", 60*time.Second)
	if r.code != 0 || r.get("first") != "refused" || r.get("second") != "allowed" || r.get("requests") != "2" || r.get("terminated") != "1" || r.get("early") != "0" || r.get("ours") != "0" {
		t.Fatalf("exit %d, results %v\n%s", r.code, r.kv, r.out)
	}
}

// A deferred answer: for the 2 s it takes, a worker's posts and an alert
// sheet's completion are delivered, a "Save changes?" sheet asked for then
// opens, and its answer allows the quit. For a terminate: sent by someone
// else (VIA terminate) the delegate answered NSTerminateLater: all of it
// happens inside AppKit's wait (the dispatcher runs the safe point), and the
// answer goes through one replyToApplicationShouldTerminate:. For the quit
// Apple event the app's loop runs on, the event is suspended and resumed
// with the answer, and terminate: follows, answered "now".
func TestQuitDeferredWithSheets(t *testing.T) {
	for _, via := range []string{"aevt", "terminate"} {
		t.Run(via, func(t *testing.T) {
			t.Parallel()
			r := child(t, "delayed", 60*time.Second, "VIA="+via)
			last, reply := "now", "0"
			if via == "terminate" {
				last, reply = "later", "1" // no quit event answered
			}
			if r.code != 0 || r.get("terminated") != "1" || r.get("worker") != "5/5" || r.get("report_during") != "true" ||
				r.get("save_open") != "true" || r.get("save_answer") != "1000" || r.get("last") != last || r.get("quit_reply") != reply ||
				r.get("ours") != "1" || r.get("replies") != "1" || r.int("waited_ms") < 2000 {
				t.Fatalf("exit %d, results %v\n%s", r.code, r.kv, r.out)
			}
			t.Logf("%v", r.kv)
		})
	}
}

// A handler that never answers: with no deadline the app still runs and
// answers 60 s later (GNUI_SHORT_QUIT_WAIT shortens that for local runs); a
// 1 s deadline cancels the quit and reports EvQuitDeadline; with
// QuitDeadlineAction Allow the app quits.
func TestQuitNoAnswer(t *testing.T) {
	wait := 60
	if s := os.Getenv("GNUI_SHORT_QUIT_WAIT"); s != "" {
		wait, _ = strconv.Atoi(s)
	}
	t.Run("no deadline", func(t *testing.T) {
		t.Parallel()
		r := child(t, "noreply", time.Duration(wait+90)*time.Second, fmt.Sprintf("WAIT=%d", wait))
		if w, _ := strconv.ParseFloat(r.get("waited_s"), 64); r.code != 0 || r.get("alive") != "1" || r.get("pending") != "false" && r.get("continued") != "1" ||
			w < float64(wait) || r.get("main") != "returned" || r.get("deadline") != "" {
			t.Fatalf("exit %d, results %v\n%s", r.code, r.kv, r.out)
		}
		if !strings.Contains(r.out, "alive=1 pending=true") {
			t.Fatalf("the request was not pending %d s later\n%s", wait, r.out)
		}
	})
	t.Run("deadline cancels", func(t *testing.T) {
		t.Parallel()
		r := child(t, "noreply", 60*time.Second, "DEADLINE=1000")
		if r.code != 0 || r.get("deadline") != "1" || r.get("action") != "0" || r.get("pending") != "false" || r.get("continued") != "1" ||
			r.get("main") != "returned" || r.int("after_ms") < 1000 || r.int("after_ms") > 5000 || r.get("ours") != "1" {
			t.Fatalf("exit %d, results %v\n%s", r.code, r.kv, r.out)
		}
	})
	t.Run("deadline allows", func(t *testing.T) {
		t.Parallel()
		r := child(t, "noreply", 60*time.Second, "DEADLINE=1000", "ACTION=allow")
		if r.code != 0 || r.get("deadline") != "1" || r.get("action") != "1" || r.get("terminated") != "1" || r.get("main") != "" || r.get("ours") != "1" {
			t.Fatalf("exit %d, results %v\n%s", r.code, r.kv, r.out)
		}
	})
}

// Saturation during a quit: everything accepted is delivered, every
// acknowledgement is made, the only losses are refused posts and coalesced
// observations (counted), and nothing deadlocks.
func TestSaturationDuringQuit(t *testing.T) {
	t.Parallel()
	r := child(t, "saturation", 90*time.Second)
	if r.code != 0 || r.get("terminated") != "1" || r.get("dialog65_busy") != "true" || r.get("notify257_without_actions") != "true" ||
		r.get("instance_first_accepted") != "32" || r.get("instance_busy") != "8" {
		t.Fatalf("exit %d, results %v\n%s", r.code, r.kv, r.out)
	}
	if r.get("posts_ok") != r.get("posts_delivered") || r.int("posts_ok") == 0 || r.get("dialogs") != "64" || r.get("responses") != "32" ||
		r.get("bad_replies") != "0" || r.get("acks") != "32" || r.get("instance") != "40" || r.get("retried") != "8" ||
		r.int("obs_delivered") != r.int("obs_set")-r.int("obs_coalesced") || r.get("ours") != "1" {
		t.Fatalf("losses: %v\n%s", r.kv, r.out)
	}
	t.Logf("posts %s accepted, %s refused; observations %s set, %s coalesced, %s delivered",
		r.get("posts_ok"), r.get("posts_refused"), r.get("obs_set"), r.get("obs_coalesced"), r.get("obs_delivered"))
}

// Open events: one handed over during Start waits in the startup list and
// comes first, after Start returned; then a get-URL Apple event and an
// open-documents one.
func TestOpenEvents(t *testing.T) {
	t.Parallel()
	r := child(t, "opens", 60*time.Second)
	want := "start-returns|urls:gnui-test://startup|urls:gnui-test://hello|files:" + r.get("doc")
	if r.code != 0 || r.get("order") != want {
		t.Fatalf("exit %d, order %q, want %q\n%s", r.code, r.get("order"), want, r.out)
	}
}

// No window: the accessory policy, the Dock menu's command, reopen, sleep,
// wake, session and screen events, and an idle app at 0 % CPU.
func TestAppServicesWithoutWindows(t *testing.T) {
	t.Parallel()
	r := child(t, "services", 60*time.Second)
	cpu, _ := strconv.ParseFloat(r.get("idle_cpu_pct"), 64)
	// Idle is about 0.03 % here; a CI runner's VM shows up to about 1.4 % for
	// an idle process (the windowed spike's idle there), so the bound catches
	// a pump that spins or polls, not that noise.
	//
	// The system may change the screens on its own (CI runners do): those
	// events are left out; the one the test posted must be there.
	var evs []string
	for _, e := range strings.Split(r.get("events"), ",") {
		if e != "screens" || len(evs) > 0 && evs[len(evs)-1] == "session1" {
			evs = append(evs, e)
		}
	}
	if r.code != 0 || r.get("policy") != "1" || strings.Join(evs, ",") != "post,command7,reopen0,sleep,wake,session0,session1,screens" || r.get("main") != "returned" || cpu > 2 {
		t.Fatalf("exit %d, results %v\n%s", r.code, r.kv, r.out)
	}
	t.Logf("idle with no window: %.3f %% CPU", cpu)
}

// Starting hidden: the window opens without showing until a reopen.
func TestStartHiddenThenReopen(t *testing.T) {
	t.Parallel()
	r := child(t, "hidden", 60*time.Second)
	if r.code != 0 || r.get("hidden_visible") != "0" || r.get("reopened_visible") != "1" {
		t.Fatalf("exit %d, results %v\n%s", r.code, r.kv, r.out)
	}
}
