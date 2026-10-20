//go:build darwin

package live

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/internal/objc"
	"gonativeui/widget/widgettest"
)

// quitProbe is the probe with the steps of a quit scenario: once it is up it
// opens the other windows, quits by the route, and reports whether it was
// still there a few seconds later.
type quitProbe struct {
	probe
	route   string
	windows int
}

func (q *quitProbe) Init(a *app.App) error {
	if err := q.probe.Init(a); err != nil {
		return err
	}
	widgettest.After(500*time.Millisecond, func() {
		for i := 1; i < q.windows; i++ {
			widgettest.OpenPlainWindow(fmt.Sprintf("other %d", i), float64(60*i), 80)
		}
		widgettest.Say("windows=%d", widgettest.VisibleWindows())
		switch q.route {
		case "dock": // the Dock's Quit: a quit Apple event the process sends itself
			widgettest.Say("sending=quit-event")
			replyErr, sendErr := widgettest.SendQuitEvent()
			widgettest.Say("reply_errn=%d send_err=%d", replyErr, sendErr)
		case "cmdq":
			widgettest.Say("sending=cmd-q")
			name := filepath.Base(os.Args[0])
			it, ok := widgettest.FindMenuItem("Application", "Quit "+name)
			widgettest.Say("quit_item=%v enabled=%v key=%s", ok, it.Enabled, it.KeyEquivalent)
			widgettest.PressCmdQ(int(objc.Send(objc.ID(a.Win.Native().NSWindow), objc.Sel("windowNumber"))))
		}
	})
	widgettest.After(8*time.Second, func() {
		widgettest.Say("survived=1")
		a.Quit()
	})
	return nil
}

func init() {
	widgettest.RegisterScenario("main-quit", func() {
		n, _ := strconv.Atoi(os.Getenv("WINDOWS"))
		q := &quitProbe{route: os.Getenv("ROUTE"), windows: n}
		err := app.Run(probeConfig(), q)
		widgettest.Say("run_returned=1 err=%v", err)
	})
}

// The Dock's Quit (a quit Apple event the process sends itself; the process
// ends) and ⌘Q through the main menu (the app's run ends) end the application, with the app's window alone and with
// another window open: a quit is a quit, whatever else is open. The event is
// addressed to the own process, which needs no Automation grant; the test
// says if that did not hold.
func TestQuitRoutes(t *testing.T) {
	for _, route := range []string{"dock", "cmdq"} {
		for _, windows := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s/%dwindows", route, windows), func(t *testing.T) {
				r := widgettest.Scenario(t, "main-quit", 60*time.Second, "ROUTE="+route, fmt.Sprintf("WINDOWS=%d", windows))
				t.Logf("%v (%v)\n%s", r.KV, r.Took.Round(time.Millisecond), r.Out)
				if r.Get("send_err") != "" && r.Get("send_err") != "0" {
					t.Fatalf("the process could not send itself the quit event (Automation grant?): error %s", r.Get("send_err"))
				}
				if r.Get("survived") == "1" {
					t.Fatalf("the app was still running after the quit: %v\n%s", r.KV, r.Out)
				}
				if route == "cmdq" && (r.Get("quit_item") != "true" || r.Get("enabled") != "true" || r.Get("key") != "q") {
					t.Errorf("the Quit item of the application menu: %v", r.KV)
				}
				if r.Code != 0 {
					t.Fatalf("exit status %d\n%s", r.Code, r.Out)
				}
				if r.Int("windows") < windows {
					t.Errorf("%d windows were open at the quit, want at least %d", r.Int("windows"), windows)
				}
			})
		}
	}
}
