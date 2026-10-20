//go:build darwin

package live

import (
	"math"
	"testing"

	"gonativeui/app"
	"gonativeui/internal/objc"
	"gonativeui/platform"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// gestureApp is an application of three ui boxes and a hosted native view, to
// see where a trackpad gesture ends up. The window is 480×360 points:
//
//	root            the whole window, Hoverable (an ancestor of everything)
//	viewer          20,20 – 220,220     holds
//	  pad           20,20 – 140,120     the box the pinches begin over
//	other           300,20 – 420,170    a box a gesture crosses into
//	host            240,230 – 400,290   a native view that takes pinches itself
//
// Each frame it adds what ui told each box (Signal) to a running total.
type gestureApp struct {
	host     platform.HostID
	hostView objc.ID

	ids  map[string]ui.ID
	tot  map[string]*gestureTotal
	cent map[string][2]float32 // box centres in pixels, as of the last frame
}

// gestureTotal is what a box was told since the last reset.
type gestureTotal struct {
	mag, rot             float32
	smart                int
	began, ended, cancel int
}

func (g *gestureApp) Init(a *app.App) error {
	g.ids, g.tot, g.cent = map[string]ui.ID{}, map[string]*gestureTotal{}, map[string][2]float32{}
	v := newGestureView(160, 60)
	var err error
	g.host, err = a.Win.NewHost(platform.HostView, platform.HostOptions{View: uintptr(v)})
	g.hostView = v
	objc.Send(v, objc.Sel("release")) // the host holds it now
	return err
}

func (g *gestureApp) reset() {
	for _, t := range g.tot {
		*t = gestureTotal{}
	}
}

func (g *gestureApp) Build(c *ui.Context) {
	pt := c.Pt
	float := ui.FloatingX | ui.FloatingY
	place := func(x, y, w, h float32) {
		c.NextPos(pt(x), pt(y))
		c.NextSize(ui.Px(pt(w)), ui.Px(pt(h)))
	}
	c.NextSize(ui.Pct(1), ui.Pct(1))
	g.ids["root"] = c.Open(ui.Hoverable, "root")
	place(20, 20, 200, 200)
	g.ids["viewer"] = c.Open(ui.Hoverable|float, "viewer")
	c.NextSize(ui.Px(pt(120)), ui.Px(pt(100)))
	g.ids["pad"] = c.Box(ui.Hoverable, "pad")
	c.Close()
	place(300, 20, 120, 150)
	g.ids["other"] = c.Box(ui.Hoverable|float, "other")
	place(240, 230, 160, 60)
	c.NextFlags(float)
	g.ids["host"] = c.Host(uint32(g.host), 77, 0)
	c.Close()
	for name, id := range g.ids {
		r := c.Rect(id)
		g.cent[name] = [2]float32{(r[0] + r[2]) / 2, (r[1] + r[3]) / 2}
		s := c.Signal(id)
		t := g.tot[name]
		if t == nil {
			t = &gestureTotal{}
			g.tot[name] = t
		}
		t.mag += s.Magnify
		t.rot += s.Rotate
		if s.SmartMagnify {
			t.smart++
		}
		if s.GestureBegan {
			t.began++
		}
		if s.GestureEnded {
			t.ended++
		}
		if s.GestureCancelled {
			t.cancel++
		}
	}
}

// gstep is one event of a gesture: its phase, where it is (pixels) and its
// amount (a fraction for a pinch, degrees for a rotation).
type gstep struct {
	phase  widgettest.GesturePhase
	at     [2]float32
	amount float64
}

// route says how an event reaches the app.
type route int

const (
	// viaWindow is SendToWindow: [window sendEvent:], what Dispatch does for a
	// gesture. The view under the window's content gets magnifyWithEvent: and
	// the platform queues it for ui.
	viaWindow route = iota
	// viaQueue is Post: the event goes through the application's queue and
	// the platform's loop, which decides who owns the gesture (the host or ui).
	viaQueue
)

// gesture sends the steps as a magnify (or rotate) gesture, a frame or two
// after each.
func gesture(l *widgettest.LiveApp, rotate bool, how route, steps []gstep) {
	for _, s := range steps {
		l.Native(func(w widgettest.Win) {
			var ev objc.ID
			var err error
			if rotate {
				ev, err = w.RotateEvent(s.at[0], s.at[1], s.amount, s.phase)
			} else {
				ev, err = w.MagnifyEvent(s.at[0], s.at[1], s.amount, s.phase)
			}
			if err != nil {
				return // reported below: the frame count would not move
			}
			if how == viaQueue {
				widgettest.Post(ev)
			} else {
				widgettest.SendToWindow(ev)
			}
		})
		l.Frames(2)
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-4 }

// Phased pinches and rotations built for the window reach the ui box under
// their first event, and its ancestors, with the amounts summed and the sign
// kept, whatever the pointer does meanwhile, and Signal.GestureBegan and
// GestureEnded fire once each on every box that had the gesture.
func TestPhasedGesturesReachTheLatchedBox(t *testing.T) {
	g := &gestureApp{}
	l := widgettest.StartLive(t, app.Config{Title: "gestures", Width: 480, Height: 360}, g)
	l.Frames(4)
	var pad, other, host [2]float32
	l.Do(func(a *app.App) { pad, other, host = g.cent["pad"], g.cent["other"], g.cent["host"] })
	check := func(what string, name string, mag, rotDeg float64, began, ended int) {
		t.Helper()
		var got gestureTotal
		l.Do(func(a *app.App) { got = *g.tot[name] })
		if !near(float64(got.mag), mag) || !near(float64(got.rot), rotDeg*math.Pi/180) || got.began != began || got.ended != ended {
			t.Errorf("%s: %s got magnify %.4f rotate %.4f rad (%.2f°) began %d ended %d; want %.4f, %.4f rad, %d, %d",
				what, name, got.mag, got.rot, float64(got.rot)*180/math.Pi, got.began, got.ended, mag, rotDeg*math.Pi/180, began, ended)
		}
	}
	reset := func() { l.Do(func(a *app.App) { g.reset() }) }
	P, C, E := widgettest.GestureBegan, widgettest.GestureChanged, widgettest.GestureEnded

	// a pinch that begins over the pad and ends over the other box
	reset()
	gesture(l, false, viaWindow, []gstep{{P, pad, 0.1}, {C, pad, 0.2}, {C, other, -0.05}, {E, other, 0}})
	for _, name := range []string{"pad", "viewer", "root"} { // the box and its ancestors
		check("pinch from the pad", name, 0.25, 0, 1, 1)
	}
	for _, name := range []string{"other", "host"} {
		check("pinch from the pad", name, 0, 0, 0, 0)
	}

	// a rotation counterclockwise, then one clockwise: the sign survives
	reset()
	gesture(l, true, viaWindow, []gstep{{P, pad, 10}, {C, pad, 20}, {C, other, 0}, {E, other, 0}})
	for _, name := range []string{"pad", "viewer", "root"} {
		check("rotation +30°", name, 0, 30, 1, 1)
	}
	check("rotation +30°", "other", 0, 0, 0, 0)
	reset()
	gesture(l, true, viaWindow, []gstep{{P, other, -5}, {C, other, -10}, {E, other, 0}})
	check("rotation -15°", "other", 0, -15, 1, 1)
	check("rotation -15°", "root", 0, -15, 1, 1)
	check("rotation -15°", "pad", 0, 0, 0, 0)

	// a pinch and a rotation at once latch separately
	reset()
	gesture(l, false, viaWindow, []gstep{{P, pad, 0.1}})
	gesture(l, true, viaWindow, []gstep{{P, other, 5}})
	gesture(l, false, viaWindow, []gstep{{C, other, 0.1}, {E, other, 0}})
	gesture(l, true, viaWindow, []gstep{{E, pad, 0}})
	check("pinch with a rotation", "pad", 0.2, 0, 1, 1)
	check("pinch with a rotation", "other", 0, 5, 1, 1)
	check("pinch with a rotation", "root", 0.2, 5, 2, 2)

	// the smart zoom (two-finger double tap) goes to the box under it
	reset()
	l.Native(func(w widgettest.Win) {
		ev, err := w.SmartMagnifyEvent(pad[0], pad[1])
		if err != nil {
			t.Errorf("%v", err)
			return
		}
		widgettest.Dispatch(ev)
	})
	l.Frames(2)
	l.Do(func(a *app.App) {
		if g.tot["pad"].smart != 1 || g.tot["viewer"].smart != 1 || g.tot["other"].smart != 0 {
			t.Errorf("smart magnify: pad %d viewer %d other %d, want 1, 1, 0", g.tot["pad"].smart, g.tot["viewer"].smart, g.tot["other"].smart)
		}
	})

	// F5's ownership rule, through the platform's loop (the application's
	// queue): a pinch that began over ui stays with ui when it crosses the
	// host, and one that began over the host stays with the host (ui is told
	// nothing) when it crosses ui.
	reset()
	gesture(l, false, viaQueue, []gstep{{P, pad, 0.1}, {C, host, 0.2}, {C, host, -0.05}, {E, host, 0}})
	check("ui pinch crossing the host (queue)", "pad", 0.25, 0, 1, 1)
	check("ui pinch crossing the host (queue)", "root", 0.25, 0, 1, 1)
	reset()
	gesture(l, false, viaQueue, []gstep{{P, host, 0.1}, {C, pad, 0.2}, {C, other, -0.05}, {E, pad, 0}})
	for _, name := range []string{"pad", "viewer", "other", "root"} {
		check("host pinch crossing ui (queue)", name, 0, 0, 0, 0)
	}
	reset()
	gesture(l, true, viaQueue, []gstep{{P, host, 10}, {C, pad, 20}, {E, pad, 0}})
	check("host rotation crossing ui (queue)", "pad", 0, 0, 0, 0)
	reset()
	gesture(l, true, viaQueue, []gstep{{P, pad, 10}, {C, host, 20}, {E, host, 0}}) // the next gesture decides again
	check("ui rotation after a host one (queue)", "pad", 0, 30, 1, 1)

	// and through AppKit's own routing of a window event: a pinch over the
	// host reaches the native view (which uses it) and not ui.
	reset()
	l.Do(func(a *app.App) { gestureView.take() })
	gesture(l, false, viaWindow, []gstep{{P, host, 0.1}, {C, host, 0.2}, {E, host, 0}})
	var hostGot []gotEvent
	l.Do(func(a *app.App) { hostGot = gestureView.take() })
	sum := 0.0
	for _, e := range hostGot {
		if e.kind == "magnify" {
			sum += e.amount
		}
	}
	check("pinch over the host (window)", "root", 0, 0, 0, 0)
	if !near(sum, 0.3) {
		t.Errorf("pinch over the host (window): the native view got %v (%+v), want magnify events adding up to 0.3", sum, hostGot)
	}
}
