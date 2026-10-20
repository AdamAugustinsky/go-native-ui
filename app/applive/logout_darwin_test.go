//go:build darwin

package applive

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/platform"
)

// The feasibility test "logout behaviour" (phase 6 plan, F3; DECISIONS D37's
// assumption). It needs a real logout, so it never runs by itself: it logs
// out the user it runs as. Run it only in the GUI session of a dedicated test
// user, never in yours.
//
// Steps (pending the user's approval to create the test user):
//
//  1. As an admin: sudo sysadminctl -addUser gnuitest -fullName "GNUI Test"
//     -password <pw>; log in to it once with Fast User Switching so it has a
//     GUI session; copy the test binary there:
//     go test -c -o /Users/Shared/applive.test ./app/applive
//  2. In gnuitest's session (its Terminal), hold mode:
//     GNUI_LOGOUT_TEST=hold GNUI_LOGOUT_LOG=/Users/Shared/logout-hold.log \
//     /Users/Shared/applive.test -test.run TestLogoutFeasibility -test.v
//     The app leaves the logout's quit request unanswered (its quit Apple
//     event is suspended) for 10 s, then allows it. The session must end about 10 s later. (The
//     first run may ask to allow Terminal to control "loginwindow": allow it,
//     then run again.)
//  3. Back as the admin: GNUI_LOGOUT_VERIFY=hold GNUI_LOGOUT_LOG=/Users/Shared/logout-hold.log \
//     go test -run TestLogoutVerify ./app/applive
//     It checks the log: the request came as QuitLogout, the answer was
//     deferred, allowed 10 s on (the suspended event resumed without an
//     error), and the app terminated.
//  4. Log in to gnuitest again; cancel mode:
//     GNUI_LOGOUT_TEST=cancel GNUI_LOGOUT_LOG=/Users/Shared/logout-cancel.log \
//     /Users/Shared/applive.test -test.run TestLogoutFeasibility -test.v
//     The app refuses the logout (userCanceledErr): the session must stay
//     (the test sees it survive 20 s and passes); 10 s after the refusal the
//     app quits by itself (App.RequestQuit), and the logout must not resume
//     (the session is still there when the test ends).
//  5. Remove the user: sudo sysadminctl -deleteUser gnuitest.
func TestLogoutFeasibility(t *testing.T) {
	mode, log := os.Getenv("GNUI_LOGOUT_TEST"), os.Getenv("GNUI_LOGOUT_LOG")
	if mode == "" || log == "" {
		t.Skip("logs out the current user: run only as a dedicated test user (GNUI_LOGOUT_TEST=hold|cancel, GNUI_LOGOUT_LOG=path; see the steps above)")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "GNUI_APPLIVE=logout", "MODE="+mode, "LOG="+log)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second) // the app is up
	// "Really log out", without the confirmation dialog, sent to loginwindow.
	if out, err := exec.Command("osascript", "-e", `tell application "loginwindow" to «event aevtrlgo»`).CombinedOutput(); err != nil {
		t.Fatalf("asking loginwindow to log out: %v %s", err, out)
	}
	if mode == "hold" {
		time.Sleep(60 * time.Second) // the session ends before this returns
		t.Fatal("the session did not end within 60 s of an allowed logout")
	}
	cmd.Wait() // the app quits 10 s after refusing
	time.Sleep(10 * time.Second)
	b, _ := os.ReadFile(log)
	t.Logf("session alive 10 s after the app quit by itself; log:\n%s", b)
	if !strings.Contains(string(b), "reason=3") || !strings.Contains(string(b), "answer=cancel") || !strings.Contains(string(b), "terminated") {
		t.Fatalf("log:\n%s", b)
	}
}

// TestLogoutVerify checks the log a hold-mode logout left (step 3).
func TestLogoutVerify(t *testing.T) {
	if os.Getenv("GNUI_LOGOUT_VERIFY") != "hold" {
		t.Skip("verifies a logout test's log (GNUI_LOGOUT_VERIFY=hold, GNUI_LOGOUT_LOG=path)")
	}
	b, err := os.ReadFile(os.Getenv("GNUI_LOGOUT_LOG"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"reason=3", "allowed after_s=10 reply=0", "terminated"} {
		if !strings.Contains(s, want) {
			t.Fatalf("no %q in the log:\n%s", want, s)
		}
	}
}

func init() {
	// logout: the app of the logout test. It appends what happens to LOG.
	scenarios["logout"] = func() {
		mode := os.Getenv("MODE")
		f, err := os.OpenFile(os.Getenv("LOG"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o666)
		if err != nil {
			fail("log: %v", err)
		}
		logf := func(format string, args ...any) {
			fmt.Fprintf(f, time.Now().Format("15:04:05.000 ")+format+"\n", args...)
			f.Sync()
		}
		var t0 time.Time
		h := &handler{}
		h.start = func(a *app.App) error { logf("start mode=%s", mode); return nil }
		h.event = func(a *app.App, e *app.Event) bool {
			switch {
			case e.Kind == app.EvQuitRequested && h.requests == 1:
				t0 = time.Now()
				logf("request reason=%d", e.A)
				if mode == "cancel" {
					a.ReplyQuit(quitID, false)
					logf("answer=cancel reply=%d", platform.LastQuitEventReply())
					after(10*time.Second, 1)
				} else {
					after(10*time.Second, 2)
				}
			case e.Kind == app.EvQuitRequested:
				logf("request reason=%d", e.A)
				a.ReplyQuit(quitID, true)
			case e.Kind == app.EvPost && e.ID == 1:
				logf("quit again")
				a.RequestQuit()
			case e.Kind == app.EvPost && e.ID == 2:
				a.ReplyQuit(quitID, true)
				logf("allowed after_s=%.0f reply=%d", time.Since(t0).Seconds(), platform.LastQuitEventReply())
			case e.Kind == app.EvWillTerminate:
				logf("terminated")
			default:
				return false
			}
			return true
		}
		app.Main(app.AppConfig{KeepRunning: true}, h)
		logf("main returned")
	}
}
