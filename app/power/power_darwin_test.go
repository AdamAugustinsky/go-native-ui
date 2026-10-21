//go:build darwin

package power_test

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/app/power"
	"gonativeui/internal/childapp"
	"gonativeui/internal/objc"
)

func init() { runtime.LockOSThread() }

func TestMain(m *testing.M) {
	childapp.Register("events", scenarioEvents)
	if childapp.Serve() {
		return
	}
	os.Exit(m.Run())
}

// ourAssertions is the lines of pmset -g assertions that name this process's
// pid and the reason.
func ourAssertions(t *testing.T, reason string) []string {
	t.Helper()
	out, err := exec.Command("pmset", "-g", "assertions").Output()
	if err != nil {
		t.Fatalf("pmset: %v", err)
	}
	var got []string
	pid := fmt.Sprintf("pid %d(", os.Getpid())
	for _, l := range strings.Split(string(out), "\n") {
		if strings.Contains(l, pid) && strings.Contains(l, `named: "`+reason+`"`) {
			got = append(got, strings.TrimSpace(l))
		}
	}
	return got
}

func TestHoldShowsInPmset(t *testing.T) {
	for _, c := range []struct {
		kind  power.Kind
		label string
	}{{power.PreventIdleSleep, "PreventUserIdleSystemSleep"}, {power.PreventDisplaySleep, "PreventUserIdleDisplaySleep"}} {
		reason := fmt.Sprintf("gnui power test %d", c.kind)
		a, err := power.Hold(c.kind, reason)
		if err != nil {
			t.Fatal(err)
		}
		got := ourAssertions(t, reason)
		if len(got) != 1 || !strings.Contains(got[0], c.label) {
			t.Fatalf("held %s: pmset shows %q", c.label, got)
		}
		if err := a.Release(); err != nil {
			t.Fatal(err)
		}
		if got := ourAssertions(t, reason); len(got) != 0 {
			t.Fatalf("released, and pmset still shows %q", got)
		}
		if err := a.Release(); err != nil { // a second release is nothing
			t.Fatalf("second Release: %v", err)
		}
	}
}

type handler struct {
	steps []func()
	got   []string
}

func (h *handler) Start(a *app.App) error {
	time.AfterFunc(50*time.Millisecond, func() { app.Post(app.Event{ID: 1}, nil) })
	return nil
}

func postWorkspace(name string) {
	ws := objc.Send(objc.Cls("NSWorkspace"), objc.Sel("sharedWorkspace"))
	objc.Send2(objc.Send(ws, objc.Sel("notificationCenter")), objc.Sel("postNotificationName:object:"), uintptr(objc.Str(name)), 0)
}

func (h *handler) Event(a *app.App, e *app.Event) {
	if ev, ok := power.EventOf(e); ok {
		h.got = append(h.got, strings.ReplaceAll(ev.String(), " ", "_"))
		return
	}
	if e.Kind != app.EvPost {
		a.DefaultEvent(e)
		return
	}
	next := func(id uint64) {
		time.AfterFunc(150*time.Millisecond, func() { app.Post(app.Event{ID: id}, nil) })
	}
	switch e.ID {
	case 1:
		// What the application object delivers by itself, synchronously.
		postWorkspace("NSWorkspaceWillSleepNotification")
		postWorkspace("NSWorkspaceDidWakeNotification")
		// The screens' notifications, before Observe: nobody listens.
		postWorkspace("NSWorkspaceScreensDidSleepNotification")
		if err := power.Observe(); err != nil {
			childapp.Fail("Observe: %v", err)
		}
		if err := power.Observe(); err != nil {
			childapp.Fail("second Observe: %v", err)
		}
		next(2)
	case 2:
		postWorkspace("NSWorkspaceScreensDidSleepNotification")
		next(3)
	case 3:
		postWorkspace("NSWorkspaceScreensDidWakeNotification")
		next(4)
	case 4:
		childapp.Say("events=%s", strings.Join(h.got, ","))
		a.Quit()
	}
}

func scenarioEvents() {
	app.Main(app.AppConfig{KeepRunning: true}, &handler{})
}

func TestLiveEvents(t *testing.T) {
	r := childapp.Run(t, "events", 60*time.Second)
	want := "will_sleep,did_wake,screens_sleep,screens_wake"
	if got := r.KV["events"]; got != want {
		t.Fatalf("events = %q, want %q\n%s", got, want, r.Out)
	}
}
