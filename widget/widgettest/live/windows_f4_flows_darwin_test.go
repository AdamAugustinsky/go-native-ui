//go:build darwin

package live

// The flows of more than one window that need the application object
// (app.Main): a refused close, the last window's close ending the app or not
// (AppConfig.KeepRunning), and the measurements of a second window (its
// opening time and memory, two idle windows, the allocations of frames in
// two windows). Each is a Scenario: a child process of the test binary.

import (
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/internal/objc"
	"gonativeui/internal/sysinfo"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/widgettest"
)

// closer is a window that may refuse to close (CloseRequested).
type closer struct {
	mwPane
	refuse atomic.Bool
}

func (c *closer) CloseRequested(*app.App) bool { return !c.refuse.Load() }

// pressClose clicks the window's close button as the user does
// (performClose:): the window is asked, through its CloseRequester.
func pressClose(w *app.Win) {
	if a := w.App(); a != nil {
		objc.Send1(objc.ID(a.Win.Native().NSWindow), objc.Sel("performClose:"), 0)
	}
}

func init() {
	// f4-close: two windows; the second refuses its close button, then
	// takes it; then the first, the last, is closed: the app ends, or with
	// MODE=keep (KeepRunning) runs on with no window until it quits.
	widgettest.RegisterScenario("f4-close", func() {
		keep := os.Getenv("MODE") == "keep"
		first, second := &closer{}, &closer{}
		h := &flowHandler{}
		h.start = func(a *app.App) error {
			w1, err := a.OpenWindow(mwConfig("first"), first)
			if err != nil {
				widgettest.Say("skip=no-window")
				os.Exit(0)
			}
			w2, err := a.OpenWindow(mwConfig("second"), second)
			if err != nil {
				widgettest.Fail("second window: %v", err)
			}
			w2.SetFrame(20, 60, 360, 268)
			second.refuse.Store(true)
			widgettest.After(400*time.Millisecond, func() {
				pressClose(w2)
				widgettest.After(300*time.Millisecond, func() {
					widgettest.Say("refused_open=%v windows_refused=%d", w2.Open(), len(a.Windows(nil)))
					second.refuse.Store(false)
					pressClose(w2)
					widgettest.After(300*time.Millisecond, func() {
						widgettest.Say("second_open=%v windows_after_second=%d first_open=%v", w2.Open(), len(a.Windows(nil)), w1.Open())
						pressClose(w1)
						widgettest.After(600*time.Millisecond, func() {
							widgettest.Say("alive_after_last=1 windows_after_last=%d", len(a.Windows(nil)))
							a.Quit()
						})
					})
				})
			})
			return nil
		}
		err := app.Main(app.AppConfig{KeepRunning: keep}, h)
		widgettest.Say("main_returned=1 err=%v", err)
	})

	// f4-measure: one window left idle for IDLE1 seconds (presents, wakeups,
	// CPU: the baseline), a second window's opening time and the memory it
	// adds, the two windows left idle for IDLE seconds (both 5 by default),
	// then, unless NOANIM is set, both animating (allocations per frame).
	widgettest.RegisterScenario("f4-measure", func() {
		seconds := func(k string) time.Duration {
			if s, err := strconv.Atoi(os.Getenv(k)); err == nil && s > 0 {
				return time.Duration(s) * time.Second
			}
			return 5 * time.Second
		}
		idle1, idle := seconds("IDLE1"), seconds("IDLE")
		first, second := &mwText{label: "The first window"}, &mwText{label: "The second window: Ωμ∂"}
		h := &flowHandler{}
		// idleFor measures the app left alone for d, then its footprint after
		// a GC, and calls next.
		footprint := map[string]uint64{}
		idleFor := func(key string, d time.Duration, next func()) {
			s0, b0 := sysinfo.Read(), first.builds.Load()+second.builds.Load()
			widgettest.After(d, func() {
				s1, b1 := sysinfo.Read(), first.builds.Load()+second.builds.Load()
				sec := d.Seconds()
				debug.FreeOSMemory() // a GC that also returns the freed memory now, not during the next idle
				footprint[key] = sysinfo.Read().Footprint
				widgettest.Say("%s_footprint_mb=%.2f", key, float64(footprint[key])/(1<<20))
				widgettest.Say("%s_s=%.0f %s_presents=%d %s_wakeups_per_s=%.2f %s_int_wakeups_per_s=%.2f %s_cpu_pct=%.3f",
					key, sec, key, b1-b0, key, float64(s1.Wakeups-s0.Wakeups)/sec, key, float64(s1.InterruptWakeups-s0.InterruptWakeups)/sec, key, float64(s1.CPUNs-s0.CPUNs)/1e9/sec*100)
				next()
			})
		}
		h.start = func(a *app.App) error {
			if _, err := a.OpenWindow(mwConfig("first"), first); err != nil {
				widgettest.Say("skip=no-window")
				os.Exit(0)
			}
			widgettest.After(1500*time.Millisecond, func() {
				idleFor("idle1", idle1, func() {
					debug.FreeOSMemory() // a GC that also returns the freed memory now, not during the next idle
					m0 := sysinfo.Read().Footprint
					t0 := time.Now()
					w2, err := a.OpenWindow(mwConfig("second"), second) // draws its first frame before it returns
					open := time.Since(t0)
					if err != nil {
						widgettest.Fail("second window: %v", err)
					}
					w2.SetFrame(20, 60, 360, 268)
					widgettest.After(2*time.Second, func() {
						debug.FreeOSMemory() // a GC that also returns the freed memory now, not during the next idle
						m1 := sysinfo.Read().Footprint
						widgettest.Say("open_ms=%.1f mem_added_mb=%.2f", float64(open.Microseconds())/1000, (float64(m1)-float64(m0))/(1<<20))
						idleFor("idle", idle, func() {
							widgettest.Say("mem_steady_added_mb=%.2f", (float64(footprint["idle"])-float64(footprint["idle1"]))/(1<<20))
							if os.Getenv("NOANIM") != "" { // a long idle measurement: the child's watchdog ends it at 90 s
								a.Quit()
								return
							}
							first.measure(second, func(mallocs, frames uint64) {
								widgettest.Say("anim_frames=%d anim_mallocs=%d", frames, mallocs)
								a.Quit()
							})
							first.animate.Store(true)
							second.animate.Store(true)
							a.Window().Wake()
							w2.Wake()
						})
					})
				})
			})
			return nil
		}
		if err := app.Main(app.AppConfig{}, h); err != nil {
			widgettest.Say("main_err=%v", err)
		}
	})
}

// mwText is a window of the measurement: a kit with a label and a box that
// moves while it animates. measure (on the first window) reads the
// allocations over frames of both windows.
type mwText struct {
	label   string
	k       *widget.Kit
	builds  atomic.Int64
	animate atomic.Bool
	x       float32

	other              *mwText
	done               func(mallocs, frames uint64)
	frame, from        int64
	m0                 runtime.MemStats
	m1                 runtime.MemStats
	others0, best      int64
	bestMallocs, tries uint64
}

func (m *mwText) Init(a *app.App) error {
	m.k = widget.NewKit(a.UI, a.Text, rex.Theme())
	return nil
}

func (m *mwText) Build(c *ui.Context) {
	m.builds.Add(1)
	k := m.k
	k.Begin(c)
	k.Label(m.label)
	if m.animate.Load() {
		m.x = float32(int(m.x+3) % 200)
		c.RequestFrame()
	}
	c.NextPos(c.Pt(20+m.x), c.Pt(120))
	c.NextSize(ui.Px(c.Pt(40)), ui.Px(c.Pt(40)))
	c.NextFlags(ui.FloatingX | ui.FloatingY)
	c.Box(0, "mover")
	k.End()
	if m.done != nil && m.animate.Load() {
		m.step()
	}
}

// measure arranges for the allocations of the frames of m and other to be
// read once both animate: after a warm-up, over windows of 300 of m's frames
// (the other's frames in between counted too), at most 5 of them, until one
// allocates nothing; done gets the best window's mallocs and frames.
func (m *mwText) measure(other *mwText, done func(mallocs, frames uint64)) {
	m.other, m.done = other, done
}

func (m *mwText) step() {
	const warm, span, tries = 240, 300, 5
	m.frame++
	switch {
	case m.frame == warm:
		runtime.GC()
		runtime.ReadMemStats(&m.m0)
		m.from, m.others0 = m.frame, m.other.builds.Load()
	case m.frame > warm && (m.frame-warm)%span == 0:
		runtime.ReadMemStats(&m.m1)
		mallocs := m.m1.Mallocs - m.m0.Mallocs
		frames := uint64(m.frame-m.from) + uint64(m.other.builds.Load()-m.others0)
		m.tries++
		if m.tries == 1 || mallocs < m.bestMallocs {
			m.bestMallocs, m.best = mallocs, int64(frames)
		}
		if mallocs == 0 || m.tries == tries {
			done := m.done
			m.done = nil
			m.animate.Store(false)
			m.other.animate.Store(false)
			done(m.bestMallocs, uint64(m.best))
			return
		}
		runtime.GC()
		runtime.ReadMemStats(&m.m0)
		m.from, m.others0 = m.frame, m.other.builds.Load()
	}
}

// A close button the window refuses leaves it open; taken, it closes the
// window and the app goes on with its other window; the last window's close
// ends the app, unless it keeps running (KeepRunning), where the app runs on
// with no window.
func TestLastWindowEndsTheAppUnlessItKeepsRunning(t *testing.T) {
	for _, mode := range []string{"quit", "keep"} {
		r := widgettest.Scenario(t, "f4-close", 60*time.Second, "MODE="+mode)
		if r.Get("refused_open") != "true" || r.Get("windows_refused") != "2" {
			t.Errorf("%s: a refused close: the window open %q, %s windows\n%s", mode, r.Get("refused_open"), r.Get("windows_refused"), r.Out)
		}
		if r.Get("second_open") != "false" || r.Get("first_open") != "true" || r.Get("windows_after_second") != "1" {
			t.Errorf("%s: after the second window's close: it is open %q, the first %q, %s windows\n%s", mode, r.Get("second_open"), r.Get("first_open"), r.Get("windows_after_second"), r.Out)
		}
		switch alive := r.Get("alive_after_last") == "1"; {
		case r.Code != 0 || r.Get("main_returned") != "1":
			t.Errorf("%s: exit %d, Main returned %q\n%s", mode, r.Code, r.Get("main_returned"), r.Out)
		case mode == "quit" && alive:
			t.Errorf("the last window's close did not end the app\n%s", r.Out)
		case mode == "keep" && (!alive || r.Get("windows_after_last") != "0"):
			t.Errorf("KeepRunning: after the last window's close the app ran on %v with %s windows\n%s", alive, r.Get("windows_after_last"), r.Out)
		}
	}
}

// Two windows: the second opens in a fraction of a second; left alone, the
// two draw nothing and the app sleeps; animating, their frames allocate
// nothing. Other test processes, which go test runs in parallel, take the
// focus and cover windows, which a window shows: a run whose idle windows
// drew is made once more, and only a second such run fails.
func TestTwoWindowsIdleAndAnimate(t *testing.T) {
	var r widgettest.ScenarioResult
	for try := 1; try <= 2; try++ {
		r = widgettest.Scenario(t, "f4-measure", 120*time.Second)
		if r.Code != 0 {
			t.Fatalf("exit %d\n%s", r.Code, r.Out)
		}
		if r.Get("idle_presents") == "0" && r.Get("idle1_presents") == "0" {
			break
		}
		t.Logf("run %d: the idle windows drew %s frames (one window) and %s (two)", try, r.Get("idle1_presents"), r.Get("idle_presents"))
	}
	num := func(k string) float64 { v, _ := strconv.ParseFloat(r.Get(k), 64); return v }
	for _, k := range []string{"idle1", "idle"} {
		t.Logf("%s: %s windows idle for %s s: %s presents, %s package-idle wakeups/s, %s interrupt wakeups/s, %s %% CPU", k, map[string]string{"idle1": "one", "idle": "two"}[k],
			r.Get(k+"_s"), r.Get(k+"_presents"), r.Get(k+"_wakeups_per_s"), r.Get(k+"_int_wakeups_per_s"), r.Get(k+"_cpu_pct"))
	}
	t.Logf("second window: opened in %s ms (its first frame drawn), %s MB added 2 s later, %s MB after the idle; animating both: %s mallocs over %s frames",
		r.Get("open_ms"), r.Get("mem_added_mb"), r.Get("mem_steady_added_mb"), r.Get("anim_mallocs"), r.Get("anim_frames"))
	if r.Get("idle_presents") != "0" || r.Get("idle1_presents") != "0" {
		t.Errorf("idle windows drew frames: %s with one window, %s with two", r.Get("idle1_presents"), r.Get("idle_presents"))
	}
	// Idle is near 0 % here, a few wakeups a second, as with one window (the
	// system's own: the spike's one idle window shows as many); a CI
	// runner's VM shows up to about 1.4 % for an idle process. The bounds
	// catch a loop that spins or polls (a 20 Hz poll: some 180 package-idle
	// and 430 interrupt wakeups a second).
	if num("idle_cpu_pct") > 2 || num("idle_wakeups_per_s") > 20 || num("idle_int_wakeups_per_s") > 60 {
		t.Errorf("two idle windows: %s %% CPU, %s package-idle and %s interrupt wakeups/s", r.Get("idle_cpu_pct"), r.Get("idle_wakeups_per_s"), r.Get("idle_int_wakeups_per_s"))
	}
	if r.Get("anim_mallocs") != "0" || num("anim_frames") < 300 {
		t.Errorf("frames of two animating windows: %s mallocs over %s frames, want 0", r.Get("anim_mallocs"), r.Get("anim_frames"))
	}
	if num("open_ms") <= 0 || num("open_ms") > 2000 {
		t.Errorf("the second window took %s ms to open", r.Get("open_ms"))
	}
}
