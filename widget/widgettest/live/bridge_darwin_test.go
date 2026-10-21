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
	"runtime/debug"
	"strconv"
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

// inspect-idle: the window left alone for IDLE seconds (5 by default): frames
// built, package-idle wakeups per second, CPU.
func init() {
	widgettest.RegisterScenario("inspect-idle", func() {
		secs := 5
		if n, err := strconv.Atoi(os.Getenv("IDLE")); err == nil && n > 0 {
			secs = n
		}
		win := &mwText{label: "idle"}
		h := &flowHandler{}
		h.start = func(a *app.App) error {
			if _, err := a.OpenWindow(mwConfig("idle"), win); err != nil {
				widgettest.Say("skip=no-window")
				os.Exit(0)
			}
			widgettest.After(1500*time.Millisecond, func() {
				s0, b0 := sysinfo.Read(), win.builds.Load()
				d := time.Duration(secs) * time.Second
				widgettest.After(d, func() {
					s1, b1 := sysinfo.Read(), win.builds.Load()
					debug.FreeOSMemory()
					widgettest.Say("presents=%d wakeups_per_s=%.2f int_wakeups_per_s=%.2f cpu_pct=%.3f",
						b1-b0, float64(s1.Wakeups-s0.Wakeups)/d.Seconds(), float64(s1.InterruptWakeups-s0.InterruptWakeups)/d.Seconds(),
						float64(s1.CPUNs-s0.CPUNs)/1e9/d.Seconds()*100)
					a.Quit()
				})
			})
			return nil
		}
		if err := app.Main(app.AppConfig{}, h); err != nil {
			widgettest.Say("main_err=%v", err)
		}
	})
}

// With the bridge on and no client the window is as idle as without it: no
// frame is built, and the wakeups stay near what the system alone causes.
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
	for name, r := range map[string]widgettest.ScenarioResult{"unset": off, "set": on} {
		t.Logf("GNUI_INSPECT %s: %s frames built, %s package-idle wakeups/s, %s interrupt wakeups/s, %s %% CPU",
			name, r.Get("presents"), r.Get("wakeups_per_s"), r.Get("int_wakeups_per_s"), r.Get("cpu_pct"))
		if r.Get("presents") != "0" {
			t.Errorf("GNUI_INSPECT %s: %s frames built while idle", name, r.Get("presents"))
		}
		// The budget is one wakeup a second (ARCHITECTURE §3); a poll or a
		// ticker would show tens.
		if num(r, "wakeups_per_s") > 1.5 {
			t.Errorf("GNUI_INSPECT %s: %s package-idle wakeups/s", name, r.Get("wakeups_per_s"))
		}
	}
	if num(on, "wakeups_per_s") > num(off, "wakeups_per_s")+1 || num(on, "int_wakeups_per_s") > num(off, "int_wakeups_per_s")+5 {
		t.Errorf("the bridge adds wakeups: %s/s (%s interrupt) against %s/s (%s)",
			on.Get("wakeups_per_s"), on.Get("int_wakeups_per_s"), off.Get("wakeups_per_s"), off.Get("int_wakeups_per_s"))
	}
}
