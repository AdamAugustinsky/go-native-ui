//go:build darwin

package dock_test

import (
	"os"
	"runtime"
	"strconv"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/app/dock"
	"gonativeui/internal/childapp"
	"gonativeui/internal/objc"
)

func init() { runtime.LockOSThread() }

func TestMain(m *testing.M) {
	childapp.Register("dock", scenarioDock)
	if childapp.Serve() {
		return
	}
	os.Exit(m.Run())
}

// handler runs f at the first app event, on the main thread with the app
// object started, and quits.
type handler struct{ f func(a *app.App) }

func (h handler) Start(a *app.App) error {
	time.AfterFunc(50*time.Millisecond, func() { app.Post(app.Event{ID: 1}, nil) })
	return nil
}

func (h handler) Event(a *app.App, e *app.Event) {
	if e.Kind == app.EvPost {
		h.f(a)
		a.Quit()
		return
	}
	a.DefaultEvent(e)
}

func nsApp() objc.ID { return objc.Send(objc.Cls("NSApplication"), objc.Sel("sharedApplication")) }

// tileBadge reads the badge straight from AppKit: NSApp.dockTile.badgeLabel.
func tileBadge() string {
	tile := objc.Send(nsApp(), objc.Sel("dockTile"))
	return string(objc.AppendUTF8(nil, objc.Send(tile, objc.Sel("badgeLabel"))))
}

func scenarioDock() {
	h := handler{f: func(a *app.App) {
		check := func(name string, got, want string) {
			if got != want {
				childapp.Fail("%s = %q, want %q", name, got, want)
			}
		}
		check("badge before", tileBadge(), "")
		if err := dock.SetBadgeCount(7); err != nil {
			childapp.Fail("SetBadgeCount: %v", err)
		}
		check("tile after SetBadgeCount(7)", tileBadge(), "7")
		check("dock.Badge", dock.Badge(), "7")
		dock.SetBadge("•")
		check("tile after SetBadge", tileBadge(), "•")
		dock.SetBadgeCount(1234)
		check("tile after a large count", tileBadge(), "999+")
		dock.SetBadgeCount(0)
		tile := objc.Send(nsApp(), objc.Sel("dockTile"))
		if objc.Send(tile, objc.Sel("badgeLabel")) != 0 {
			childapp.Fail("the badge label is still set after SetBadgeCount(0)")
		}
		dock.SetBadge("x")
		dock.SetBadge("")
		check("tile after SetBadge(\"\")", tileBadge(), "")

		// A bounce: the system makes one only for an inactive app, and then
		// the request has an id; for an active one the id is 0.
		active := objc.SendBool(nsApp(), objc.Sel("isActive"))
		id, err := dock.Bounce(false)
		if err != nil {
			childapp.Fail("Bounce: %v", err)
		}
		cid, err := dock.Bounce(true)
		if err != nil {
			childapp.Fail("Bounce(critical): %v", err)
		}
		dock.CancelBounce(id)
		dock.CancelBounce(cid)
		dock.CancelBounce(cid) // already over
		childapp.Say("active=%v bounce=%d critical=%d", active, id, cid)

		p0, err := dock.Policy()
		childapp.Say("policy0=%d policy0_err=%v", p0, err)
		if err := dock.SetPolicy(app.PolicyAccessory); err != nil {
			childapp.Fail("SetPolicy(Accessory): %v", err)
		}
		childapp.Say("policy_accessory=%d", objc.Send(nsApp(), objc.Sel("activationPolicy")))
		if err := dock.SetPolicy(app.PolicyRegular); err != nil {
			childapp.Fail("SetPolicy(Regular): %v", err)
		}
		p, _ := dock.Policy()
		childapp.Say("policy_regular=%d policy_api=%d done=1", objc.Send(nsApp(), objc.Sel("activationPolicy")), p)
	}}
	app.Main(app.AppConfig{KeepRunning: true}, h)
}

func TestLiveDock(t *testing.T) {
	r := childapp.Run(t, "dock", 60*time.Second)
	if r.KV["done"] != "1" {
		t.Fatalf("the scenario did not finish\n%s", r.Out)
	}
	if r.KV["policy0"] != "0" || r.KV["policy_accessory"] != strconv.Itoa(int(app.PolicyAccessory)) ||
		r.KV["policy_regular"] != "0" || r.KV["policy_api"] != "0" {
		t.Errorf("activation policy did not round-trip: %v", r.KV)
	}
	// The app of this test was not activated by anyone: it is bounced for,
	// unless the user happened to make it active (a bounce then is no bounce).
	if r.KV["active"] == "false" && (r.KV["bounce"] == "0" || r.KV["critical"] == "0") {
		t.Errorf("an inactive app got no bounce request: %v", r.KV)
	}
	if r.KV["active"] == "true" && (r.KV["bounce"] != "0" || r.KV["critical"] != "0") {
		t.Errorf("an active app got a bounce request: %v", r.KV)
	}
}
