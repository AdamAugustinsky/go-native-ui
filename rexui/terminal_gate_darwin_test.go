//go:build darwin

package rexui_test

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"slices"
	"testing"
	"time"

	"gonativeui/gpu"
	"gonativeui/internal/fakeui"
	"gonativeui/rexui"
	"gonativeui/term"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// The phase 8 terminal budgets (docs/PHASE8-PLAN.md, C1), measured against
// CoreText on the machine running the test; timings are wall time on the
// UI thread, which is CPU bound here. A measurement, not a unit test:
//
//	GNUI_MEASURE=1 go test ./rexui -run TerminalGates -v
//
// Gates: a full 200×60 redraw ≤ 2 ms (median, shaped runs cached by
// content), and output of 50 MB keeps every frame within 16.7 ms. Reported:
// the same redraw with every run new (shaping cost), parse throughput
// (libghostty-vt's, not ours), and the memory of a 5,000 × 120 scrollback.
func TestTerminalGates(t *testing.T) {
	if os.Getenv("GNUI_MEASURE") == "" {
		t.Skip("a measurement, not a test: set GNUI_MEASURE=1")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ts, err := text.New()
	if err != nil {
		t.Skip("no text system:", err)
	}
	defer ts.Close()

	newView := func(o term.Options) (*rexui.TerminalState, func(), *int, *int) {
		s := newTermState(t, o)
		c := ui.New(ts)
		c.Reserve(1 << 14)
		c.AtlasSync(&fakeui.Uploader{})
		k := base.NewKit(c, ts, rex.Theme())
		k.SetMode(theme.Dark)
		k.ReduceMotion = true
		var in ui.Input
		in.Inside, in.Focused = true, true
		var list gpu.DrawList
		list.Quads = make([]gpu.Quad, 0, 1<<15)
		w, h := 1600, 1000
		frame := func() {
			c.Begin(&in, w, h, 2, 1.0/60)
			k.Begin(c)
			theme.At(c, 0, 0, float32(w), float32(h))
			c.Push(c.Box(theme.Floating, "root"))
			rexui.Terminal(k, "shell", s, rexui.TerminalOpts{})
			c.Pop()
			k.End()
			c.End(&list)
			in.ClearFrame()
		}
		return s, frame, &w, &h
	}

	// A full 200×60 redraw.
	s, frame, w, h := newView(term.Options{Scrollback: 100})
	frame()
	frame()
	cw, ch := s.CellSize()
	*w, *h = int(math.Ceil(float64(200*cw+24)))+1, int(math.Ceil(float64(60*ch+24)))+1
	frame() // the view's rectangle is the last frame's
	frame()
	if c, r := s.T.Size(); c != 200 || r != 60 {
		t.Fatalf("grid %d×%d, want 200×60", c, r)
	}
	screen := func(seed int) []byte {
		b := []byte("\x1b[H")
		for y := range 60 {
			for x := 0; x < 200; {
				n := min(4+(x*7+y*3+seed)%13, 200-x)
				b = fmt.Appendf(b, "\x1b[%d;%dm", 30+(x+y+seed)%8, 40+(x/20+seed)%8)
				for i := range n {
					b = append(b, byte('!'+(x+i+y*5+seed*11)%94))
				}
				x += n
			}
			b = append(b, "\x1b[0m"...)
			if y < 59 {
				b = append(b, "\r\n"...)
			}
		}
		return b
	}
	screens := [2][]byte{screen(0), screen(1)}
	measure := func(n int, next func(i int) []byte) (med, p95 time.Duration) {
		d := make([]time.Duration, n)
		for i := range n {
			s.T.Write(next(i))
			t0 := time.Now()
			frame()
			d[i] = time.Since(t0)
		}
		slices.Sort(d)
		return d[n/2], d[n*95/100]
	}
	for i := range 4 { // warm the run cache with both screens
		s.T.Write(screens[i%2])
		frame()
	}
	med, p95 := measure(200, func(i int) []byte { return screens[i%2] })
	t.Logf("full 200×60 redraw, runs cached: median %v, p95 %v (gate: median ≤ 2 ms)", med, p95)
	if med > 2*time.Millisecond {
		t.Errorf("full 200×60 redraw median %v > 2 ms", med)
	}
	cmed, cp95 := measure(50, func(i int) []byte { return screen(100 + i) })
	t.Logf("full 200×60 redraw, every run new (shaping): median %v, p95 %v (reported)", cmed, cp95)

	// 50 MB of output, as `cat` of a large file.
	s2, frame2, _, _ := newView(term.Options{Scrollback: 5000})
	frame2()
	frame2()
	line := []byte("func (t *Terminal) Write(p []byte) (int, error) { // héllo wörld ─ \x1b[32mok\x1b[0m 0123456789\r\n")
	data := make([]byte, 0, 50<<20+len(line))
	for len(data) < 50<<20 {
		data = append(data, line...)
	}
	s2.Feed(data)
	var frames []time.Duration
	t0 := time.Now()
	for s2.Pending() > 0 {
		f0 := time.Now()
		frame2()
		frames = append(frames, time.Since(f0))
	}
	total := time.Since(t0)
	slices.Sort(frames)
	worst := frames[len(frames)-1]
	t.Logf("50 MB cat: %d frames in %v (%.0f MB/s through the view), frame median %v, p99 %v, max %v (gate: max ≤ 16.7 ms)",
		len(frames), total.Round(time.Millisecond), float64(len(data))/(1<<20)/total.Seconds(),
		frames[len(frames)/2], frames[len(frames)*99/100], worst)
	if worst > 16700*time.Microsecond {
		t.Errorf("50 MB cat: a frame took %v > 16.7 ms", worst)
	}

	// Parse throughput alone (the library's).
	raw, err := term.New(term.Options{Cols: 200, Rows: 60, Scrollback: 5000})
	if err != nil {
		t.Fatal(err)
	}
	t0 = time.Now()
	for off := 0; off < len(data); off += 64 << 10 {
		raw.Write(data[off:min(off+64<<10, len(data))])
	}
	el := time.Since(t0)
	raw.Close()
	t.Logf("libghostty-vt parse: %.0f MB/s (reported; the library's)", float64(len(data))/(1<<20)/el.Seconds())

	// 5,000 × 120 scrollback.
	sb, err := term.New(term.Options{Cols: 120, Rows: 40, Scrollback: 5000})
	if err != nil {
		t.Fatal(err)
	}
	defer sb.Close()
	row := make([]byte, 0, 130)
	for i := range 120 {
		row = append(row, byte('a'+i%26))
	}
	row = append(row, "\r\n"...)
	for range 5100 {
		sb.Write(row)
	}
	m := sb.Memory()
	t.Logf("5,000 × 120 scrollback: %d rows kept, %.2f MB resident in %d pages (reported; plan budget 6 MB)",
		sb.ScrollbackRows(), float64(m.Primary)/(1<<20), m.PrimaryPages)
}
