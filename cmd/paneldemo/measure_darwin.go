//go:build darwin

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"time"

	"gonativeui/app"
	"gonativeui/app/hotkey"
	"gonativeui/platform"
)

// The posts that drive -measure and -shot, from timers.
const (
	postPress = iota + 1
	postCheck
	postShow
)

func later(d time.Duration, id uint64) {
	time.AfterFunc(d, func() { app.Post(app.Event{ID: id}, nil) })
}

// measurement times hotkey presses to the palette's first frame on glass.
// The first press is the warm-up (the palette's display link starts with
// it) and is not counted.
type measurement struct {
	n, done      int
	t0, built    int64  // the press; the first build after it
	ticks0, tick uint64 // display-link ticks at the press and at that build
	glassMs      []float64
	buildMs      []float64
	slots        []int // frame slots from the press to the build
	missed       int   // presses whose frame never reported its present
	onScreen     int   // presses after which the palette was visible on screen (occlusion state)
}

func (d *demo) startMeasure(a *app.App) {
	if d.hotkeyErr != nil {
		fmt.Fprintln(os.Stderr, "paneldemo: no hotkey to measure:", d.hotkeyErr)
		a.Quit()
		return
	}
	m := &measurement{n: *measure}
	d.m = m
	d.pal.built = func() {
		if m.t0 != 0 && m.built == 0 {
			m.built, m.tick = platform.Now(), d.palette.App().Win.DebugState().Ticks
		}
	}
	later(500*time.Millisecond, postPress)
}

// pressed is the palette's hotkey while measuring: show it.
func (m *measurement) pressed(d *demo) { d.palette.Show() }

func (d *demo) post(a *app.App, e *app.Event) bool {
	m := d.m
	switch e.ID {
	case postPress:
		pa := d.palette.App()
		m.t0, m.built, m.ticks0 = platform.Now(), 0, pa.Win.DebugState().Ticks
		pa.Surf.TraceNextPresent()
		if err := hotkey.Simulate(paletteKey); err != nil {
			fmt.Fprintln(os.Stderr, "paneldemo: Simulate:", err)
			a.Quit()
		}
		later(200*time.Millisecond, postCheck)
	case postCheck:
		pa := d.palette.App()
		g := pa.Surf.Trace.OnGlass.Load()
		if st := pa.Win.DebugState(); m.done > 0 && pa.Win.Visible() && st.Occlusion&2 != 0 {
			m.onScreen++
		}
		if m.done > 0 { // the first is the warm-up
			if g == 0 || m.built == 0 {
				m.missed++
				if os.Getenv("PANELDEMO_DEBUG") != "" {
					fmt.Fprintf(os.Stderr, "missed press %d: glass %d, built %d\n%s\n", m.done, g, m.built, pa.StateLine(nil, platform.Now()))
				}
			} else {
				m.glassMs = append(m.glassMs, float64(g-m.t0)/1e6)
				m.buildMs = append(m.buildMs, float64(m.built-m.t0)/1e6)
				m.slots = append(m.slots, int(m.tick-m.ticks0))
			}
		}
		d.palette.Hide()
		m.t0 = 0
		if m.done++; m.done <= m.n {
			later(150*time.Millisecond, postPress)
			return true
		}
		m.report()
		a.Quit()
	case postShow:
		d.bar.Show()
		d.palette.Show()
	default:
		return false
	}
	return true
}

func stats(v []float64) map[string]float64 {
	if len(v) == 0 {
		return nil
	}
	s := slices.Clone(v)
	slices.Sort(s)
	q := func(p float64) float64 { return s[min(len(s)-1, int(p*float64(len(s))))] }
	return map[string]float64{"min": s[0], "median": q(0.5), "p95": q(0.95), "max": s[len(s)-1]}
}

func (m *measurement) report() {
	slots := map[int]int{}
	for _, s := range m.slots {
		slots[s]++
	}
	out := map[string]any{
		"what":    "hotkey.Simulate (a synthetic kEventHotKeyPressed through Carbon's handler) to the palette's first frame on the display, warm",
		"presses": len(m.glassMs),
		"missed":  m.missed, "visible_after_press": m.onScreen, "hide_on_deactivate": !*keep,
		"hotkey_to_glass_ms":   stats(m.glassMs),
		"hotkey_to_build_ms":   stats(m.buildMs),
		"frame_slots_to_build": slots,
		"backdrop_glass":       !*noGlass,
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
}
