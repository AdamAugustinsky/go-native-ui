//go:build darwin

package live

// The agent bridge (GNUI_INSPECT, phase 6 T4) on a real window: an agent
// drives the probe app through the socket (steps are applied at a frame safe
// point on the main thread, the answer comes after the next frame), reads its
// tree, and takes a shot of the live frame; and with the bridge on and no
// client the app is as idle as without it.

import (
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/internal/inspectwire"
	"gonativeui/internal/sysinfo"
	"gonativeui/widget/widgettest"
)

// socketDir is a directory of the user's with a path short enough for a socket.
func socketDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "gi")
	if err != nil {
		t.Fatal(err)
	}
	if d, err = filepath.EvalSymlinks(d); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

func TestAgentBridgeDrivesALiveWindow(t *testing.T) {
	sock := filepath.Join(socketDir(t), "s")
	t.Setenv("GNUI_INSPECT", sock)
	l, p := startProbe(t)
	c, err := inspectwire.Dial(sock, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	call := func(req inspectwire.Request) *inspectwire.Response {
		t.Helper()
		r, err := c.Call(&req, 30*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if !r.OK {
			t.Fatalf("%+v: %+v", req, r.Error)
		}
		return r
	}
	w := call(inspectwire.Request{Op: "windows"}).Windows
	if len(w) != 1 || w[0].Title != "probe" || w[0].ID == 0 {
		t.Fatalf("windows: %+v", w)
	}
	nodes := call(inspectwire.Request{Op: "tree"}).Trees[0].Nodes
	var press, name bool
	for _, n := range nodes {
		press = press || (n.Role == "Button" && n.Name == "Press")
		name = name || (n.Role == "TextField" && n.Name == "Name")
	}
	if !press || !name {
		t.Fatalf("the tree lacks the Press button or the Name field: %+v", nodes)
	}

	// steps by role and name: focus the field, type, click the button
	before := l.Built()
	r := call(inspectwire.Request{Op: "do", Steps: []inspectwire.Step{
		{Action: "focus", Role: "TextField", Name: "Name"},
		{Action: "type", Text: "agent"},
		{Action: "click", Role: "Button", Name: "Press"},
	}})
	if r.Steps != 3 || l.Built() <= before {
		t.Errorf("do: %+v, frames built %d → %d", r, before, l.Built())
	}
	got := 0
	l.Do(func(*app.App) { got = p.press })
	if got != 1 {
		t.Errorf("the button was pressed %d times", got)
	}
	f := call(inspectwire.Request{Op: "tree", Step: inspectwire.Step{Role: "TextField", Name: "Name"}}).Trees[0].Nodes
	if len(f) != 1 || f[0].Value != "agent" {
		t.Errorf("the field after typing: %+v", f)
	}

	// the answer to a do comes after the frame built from its steps: the list
	// scrolled under the wheel shows other rows at once
	r0 := call(inspectwire.Request{Op: "tree", Step: inspectwire.Step{Role: "ListItem"}}).Trees[0].Nodes
	call(inspectwire.Request{Op: "do", Step: inspectwire.Step{Action: "scroll", Role: "List", Dy: -300, N: 3}})
	r1 := call(inspectwire.Request{Op: "tree", Step: inspectwire.Step{Role: "ListItem"}}).Trees[0].Nodes
	if len(r0) == 0 || len(r1) == 0 || r0[0].Name == r1[0].Name {
		t.Errorf("the list did not scroll: first row %q, then %q", r0[0].Name, r1[0].Name)
	}

	// a failed step is a structured error, and the window carries on
	bad, err := c.Call(&inspectwire.Request{Op: "do", Step: inspectwire.Step{Action: "click", Role: "Button", Name: "Nope"}}, 30*time.Second)
	if err != nil || bad.OK || bad.Error.Code != inspectwire.ErrStepFailed {
		t.Errorf("a missing target: %+v %v", bad, err)
	}
	call(inspectwire.Request{Op: "do", Step: inspectwire.Step{Action: "click", Role: "Button", Name: "Press"}})

	// a shot of the live window
	out := filepath.Join(t.TempDir(), "live.png")
	sh := call(inspectwire.Request{Op: "shot", Path: out}).Shot
	fh, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	img, err := png.Decode(fh)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != sh.Width || b.Dy() != sh.Height || sh.Width < 400 {
		t.Errorf("the PNG is %v; the response says %dx%d", b, sh.Width, sh.Height)
	}
}

// bridgeGoroutines is how many goroutines of the process are in the bridge's
// code (the accept loop, a connection being served): the stacks of all of them
// are read and those that mention the package are counted.
func bridgeGoroutines() int {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	n := 0
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, "inspectwire.") {
			n++
		}
	}
	return n
}

// inspect-idle: the window left alone for IDLE seconds (5 by default): frames
// built, package-idle wakeups per second, CPU, and, with GNUI_INSPECT set, the
// proof that the bridge is up: it answers "windows" before the measurement
// begins, the socket is there when it ends, and the bridge's goroutines are the
// same at both ends.
func init() {
	widgettest.RegisterScenario("inspect-idle", func() {
		secs := 5
		if n, err := strconv.Atoi(os.Getenv("IDLE")); err == nil && n > 0 {
			secs = n
		}
		sock := os.Getenv("GNUI_INSPECT")
		var up atomic.Bool
		win := &mwText{label: "idle"}
		h := &flowHandler{}
		h.start = func(a *app.App) error {
			if _, err := a.OpenWindow(mwConfig("idle"), win); err != nil {
				widgettest.Say("skip=no-window")
				os.Exit(0)
			}
			if sock != "" {
				go func() { // a client: the request is served by the main thread's loop
					var c *inspectwire.Client
					var err error
					for i := 0; i < 100; i++ {
						if c, err = inspectwire.Dial(sock, time.Second); err == nil {
							break
						}
						time.Sleep(50 * time.Millisecond)
					}
					if err != nil {
						widgettest.Say("bridge_up=0 bridge_err=dial")
						up.Store(true)
						return
					}
					r, err := c.Call(&inspectwire.Request{Op: "windows"}, 20*time.Second)
					c.Close()
					if err != nil || !r.OK || len(r.Windows) != 1 {
						widgettest.Say("bridge_up=0 bridge_err=windows")
					} else {
						widgettest.Say("bridge_up=1")
					}
					up.Store(true)
				}()
			}
			var begin func(tries int)
			begin = func(tries int) {
				if sock != "" && !up.Load() && tries < 50 { // the check above comes first: it must not fall in the measurement
					widgettest.After(200*time.Millisecond, func() { begin(tries + 1) })
					return
				}
				s0, b0, g0 := sysinfo.Read(), win.builds.Load(), bridgeGoroutines()
				d := time.Duration(secs) * time.Second
				widgettest.After(d, func() {
					s1, b1, g1 := sysinfo.Read(), win.builds.Load(), bridgeGoroutines()
					debug.FreeOSMemory()
					widgettest.Say("presents=%d wakeups_per_s=%.2f int_wakeups_per_s=%.2f cpu_pct=%.3f goroutines0=%d goroutines1=%d",
						b1-b0, float64(s1.Wakeups-s0.Wakeups)/d.Seconds(), float64(s1.InterruptWakeups-s0.InterruptWakeups)/d.Seconds(),
						float64(s1.CPUNs-s0.CPUNs)/1e9/d.Seconds()*100, g0, g1)
					if fi, err := os.Lstat(sock); sock != "" && err == nil && fi.Mode()&os.ModeSocket != 0 {
						widgettest.Say("socket_up=1")
					}
					a.Quit()
				})
			}
			widgettest.After(1500*time.Millisecond, func() { begin(0) })
			return nil
		}
		if err := app.Main(app.AppConfig{}, h); err != nil {
			widgettest.Say("main_err=%v", err)
		}
	})
}

// With the bridge on and no client the window is as idle as without it. What
// this proves: the bridge really is running during the measurement (it answers
// a request first, its socket is there at the end), no frame is built, and the
// bridge's goroutines are the same at the start and the end (a timer or a poll
// would add or churn them). On a Mac at rest it also holds the wakeups to the
// budget of one a second (ARCHITECTURE §3) and to no more than the run without
// the bridge (a second more package-idle wakeups, fifteen more interrupt wakeups:
// those are the system's on a Mac in use, 6 to 23 a second were seen with the
// bridge off, so a tighter bound fails for no fault of the bridge). What it does not prove on a hosted runner: wakeups there are tens
// a second with the bridge off as well, so they are logged, not asserted; a
// ticker in the bridge would be caught there only by the goroutine count, and a
// goroutine that sleeps and wakes (time.Sleep in a loop) keeps the same count, so
// off CI the wakeup comparison is what catches it.
func TestIdleWithTheBridgeOn(t *testing.T) {
	run := func(env ...string) widgettest.ScenarioResult {
		var r widgettest.ScenarioResult
		for try := 1; try <= 2; try++ { // other processes' windows cover and uncover this one: a run that drew is made once more
			r = widgettest.Scenario(t, "inspect-idle", 90*time.Second, env...)
			if r.Code != 0 {
				t.Fatalf("exit %d\n%s", r.Code, r.Out)
			}
			if r.Get("presents") == "0" {
				break
			}
			t.Logf("try %d: %s frames built while idle", try, r.Get("presents"))
		}
		return r
	}
	num := func(r widgettest.ScenarioResult, k string) float64 {
		v, _ := strconv.ParseFloat(r.Get(k), 64)
		return v
	}
	off := run()
	sock := filepath.Join(socketDir(t), "s")
	on := run("GNUI_INSPECT=" + sock)
	if _, err := os.Lstat(sock); err == nil {
		t.Error("the socket outlived the app")
	}
	for _, c := range []struct {
		name string
		r    widgettest.ScenarioResult
	}{{"unset", off}, {"set", on}} {
		r := c.r
		t.Logf("GNUI_INSPECT %s: %s frames built, %s package-idle wakeups/s, %s interrupt wakeups/s, %s %% CPU, bridge goroutines %s → %s",
			c.name, r.Get("presents"), r.Get("wakeups_per_s"), r.Get("int_wakeups_per_s"), r.Get("cpu_pct"), r.Get("goroutines0"), r.Get("goroutines1"))
		if r.Get("presents") != "0" {
			t.Errorf("GNUI_INSPECT %s: %s frames built while idle", c.name, r.Get("presents"))
		}
	}
	if off.Get("goroutines0") != "0" || off.Get("goroutines1") != "0" {
		t.Errorf("with GNUI_INSPECT unset the bridge has goroutines: %s → %s", off.Get("goroutines0"), off.Get("goroutines1"))
	}
	if on.Get("bridge_up") != "1" || on.Get("socket_up") != "1" {
		t.Fatalf("the bridge was not up during the measurement: bridge_up=%q (%s) socket_up=%q\n%s", on.Get("bridge_up"), on.Get("bridge_err"), on.Get("socket_up"), on.Out)
	}
	if g := on.Get("goroutines0"); g == "0" || g != on.Get("goroutines1") {
		t.Errorf("the bridge's goroutines were %s at the start and %s at the end of the idle (want the same, not 0)", g, on.Get("goroutines1"))
	}
	if os.Getenv("CI") != "" {
		return // the runner's wakeups are noise; see above
	}
	for name, v := range map[string]float64{"unset": num(off, "wakeups_per_s"), "set": num(on, "wakeups_per_s")} {
		if v > 1.5 {
			t.Errorf("GNUI_INSPECT %s: %.2f package-idle wakeups/s, budget 1", name, v)
		}
	}
	if num(on, "wakeups_per_s") > num(off, "wakeups_per_s")+1 || num(on, "int_wakeups_per_s") > num(off, "int_wakeups_per_s")+15 {
		t.Errorf("the bridge adds wakeups: %s/s (%s interrupt) against %s/s (%s)",
			on.Get("wakeups_per_s"), on.Get("int_wakeups_per_s"), off.Get("wakeups_per_s"), off.Get("int_wakeups_per_s"))
	}
}
