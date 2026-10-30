package rexui

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"gonativeui/core"
	"gonativeui/gpu"
	"gonativeui/internal/fakeui"
	"gonativeui/term"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// A simulated parse spends deterministic byte work, even when a real frame
// previously measured a different parse rate. The shared budget and each
// pane's cap still hold with a clock that does not advance during parsing.
func TestTerminalSimulationParseBudget(t *testing.T) {
	clock, err := core.InstallSimClockForTesting()
	if err != nil {
		t.Fatal(err)
	}
	defer clock.Restore()
	newPane := func() *TerminalState {
		s, err := NewTerminal(term.Options{})
		if err != nil {
			if os.Getenv("GNUI_REQUIRE_LIBGHOSTTY") != "" {
				t.Fatal(err)
			}
			t.Skip(err)
		}
		return s
	}
	const work = 4096
	const budget = work * feedSimByte
	a, b := newPane(), newPane()
	defer a.Close()
	defer b.Close()
	data := bytes.Repeat([]byte("x"), 8*work)
	a.Feed(data)
	a.rate.nsPerByte = 1e9
	if !a.feedFrame(budget, &a.rate) || a.parsedFrame != work || a.parseTime != budget {
		t.Fatalf("standalone simulated slice: %+v", a.Stats())
	}
	b.OpenInbox(TerminalInboxOpts{HighWater: len(data)})
	if _, err := b.Inbox().Write(data); err != nil {
		t.Fatal(err)
	}
	q := NewTerminalScheduler(TerminalSchedulerOpts{Budget: budget})
	q.rate.nsPerByte = 1e9
	q.Add(a)
	q.Add(b)
	a.feedCap = budget / 4
	c := ui.New(fakeui.New())
	var in ui.Input
	var list gpu.DrawList
	for frame := range 3 {
		c.Begin(&in, 100, 100, 1, 1.0/60)
		a.seenOK, a.seen, a.wantFocus = true, c.Frame()-1, true
		q.Run(c)
		st := q.Stats()
		if st.Parsed != work || st.Spent != budget || st.Behind != 2 {
			t.Fatalf("frame %d: shared simulated budget: %+v", frame, st)
		}
		if a.parsedFrame != work/4 || b.parsedFrame != work*3/4 {
			t.Fatalf("frame %d: capped/focused pane %d, other %d", frame, a.parsedFrame, b.parsedFrame)
		}
		q.Run(c)
		if q.Stats() != st {
			t.Fatal("scheduler parsed twice in one frame")
		}
		c.End(&list)
		in.ClearFrame()
	}
}

// A writer blocks at the high-water mark, resumes at the low one, and a
// Close unblocks it; nothing written before the Close is lost.
func TestTerminalInboxBackpressure(t *testing.T) {
	woke := 0
	in := newTerminalInbox(TerminalInboxOpts{HighWater: 1024, LowWater: 256, Wake: func() { woke++ }})
	done := make(chan error, 1)
	var wrote int
	go func() {
		n, err := in.Write(make([]byte, 4096))
		wrote = n
		done <- err
	}()
	queued := func() int { in.mu.Lock(); defer in.mu.Unlock(); return in.queued }
	waitFor := func(what string, f func() bool) {
		t.Helper()
		for end := time.Now().Add(5 * time.Second); !f(); time.Sleep(time.Millisecond) {
			if time.Now().After(end) {
				t.Fatal("timed out waiting for", what)
			}
		}
	}
	waitFor("the writer to block", func() bool { in.mu.Lock(); defer in.mu.Unlock(); return in.wait == 1 })
	if q := queued(); q != 1024 {
		t.Fatalf("queued %d at the high-water mark, want 1024", q)
	}
	// The UI parses down to just above the low-water mark: still blocked.
	var buf []byte
	buf = in.take(buf)
	in.parsed(700)
	time.Sleep(10 * time.Millisecond)
	if q := queued(); q != 324 {
		t.Fatalf("queued %d, want 324: the writer resumed above the low-water mark", q)
	}
	in.parsed(100) // 224 ≤ 256: resume, fill to the mark again, block
	waitFor("the writer to refill", func() bool { return queued() == 1024 })
	in.Close()
	if err := <-done; !errors.Is(err, ErrTerminalInboxClosed) {
		t.Fatalf("blocked Write after Close returned %v", err)
	}
	if wrote != 1024+800 {
		t.Errorf("Write reported %d bytes, want %d queued before the Close", wrote, 1024+800)
	}
	if n := len(buf) + len(in.take(nil)); n != wrote {
		t.Errorf("%d bytes reached the UI, %d were written", n, wrote)
	}
	if _, err := in.Write([]byte("x")); !errors.Is(err, ErrTerminalInboxClosed) {
		t.Errorf("Write after Close returned %v", err)
	}
	if woke < 2 || in.hits != 2 || in.blocked <= 0 {
		t.Errorf("wakes %d, high-water hits %d, blocked %v", woke, in.hits, in.blocked)
	}
}

// ptyReader hands out data in reads of 1 byte to 16 KiB, as a pty does.
type ptyReader struct {
	data []byte
	rng  *rand.Rand
}

func (r *ptyReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.data[:min(len(r.data), 1+r.rng.IntN(16<<10))])
	r.data = r.data[n:]
	return n, nil
}

// flood is n bytes of coloured, numbered lines (a build log, `yes`).
func flood(seed, n int) []byte {
	b := make([]byte, 0, n+128)
	for i := 0; len(b) < n; i++ {
		b = fmt.Appendf(b, "\x1b[3%dm%6d\x1b[0m flood %d: the quick brown fox jumps over the lazy dog %x\r\n", i%8, i, seed, i*seed)
	}
	return b
}

// feedPane is one terminal of the scheduler test.
type feedPane struct {
	name    string
	s       *TerminalState
	data    []byte
	visible bool
	focused bool // fed by the test, a chunk a frame, through its inbox
	err     error
	// starved counts the frames in a row it began with output queued and
	// parsed none; worst is the longest such run.
	starved, worst int
	queued         int
	parsed         int64
}

// Several busy terminals share one budget: two floods and recorded sessions
// written by reader goroutines through bounded inboxes, some views hidden,
// and a focused pane fed a little each frame. The focused pane parses what
// it gets in the frame it gets it, the hidden ones keep draining, queues
// stay under the high-water mark, every byte is parsed, and each screen
// ends as the old path (Feed, parsed whole) leaves it.
//
//	GNUI_MEASURE=1 go test ./rexui -run TerminalSchedulerPanes -v
//
// logs the per-frame parse times as well.
func TestTerminalSchedulerPanes(t *testing.T) {
	// A small high-water mark, where the readers block; the default one,
	// where the budget is what holds the panes back.
	for i, set := range [][]string{
		{"nvim", "btop", "opencode", "gitlog", "less", "vim"},
		{"vim", "claude", "utf8", "gitlog-pager", "nvim", "opencode"},
	} {
		high, low := 64<<10, 16<<10
		if i == 1 {
			high, low = DefaultInboxHighWater, DefaultInboxLowWater
		}
		t.Run(fmt.Sprint(high), func(t *testing.T) { schedulerPanes(t, set, high, low) })
	}
}

func schedulerPanes(t *testing.T, sessions []string, high, low int) {
	const budget = 4 * time.Millisecond
	ts := fakeui.New()
	c := ui.New(ts)
	c.Reserve(1 << 14)
	c.AtlasSync(&fakeui.Uploader{})
	k := base.NewKit(c, ts, rex.Theme())
	var in ui.Input
	var list gpu.DrawList
	q := NewTerminalScheduler(TerminalSchedulerOpts{Budget: budget})
	wake := make(chan struct{}, 1)
	opts := term.Options{Scrollback: 500}

	// The focused pane and three more are drawn; the rest are hidden.
	var panes []*feedPane
	add := func(name string, data []byte, visible bool) {
		s, err := NewTerminal(opts)
		if err != nil {
			if os.Getenv("GNUI_REQUIRE_LIBGHOSTTY") != "" {
				t.Fatal(err)
			}
			t.Skip(err)
		}
		t.Cleanup(s.Close)
		s.OpenInbox(TerminalInboxOpts{HighWater: high, LowWater: low, Wake: func() {
			select {
			case wake <- struct{}{}:
			default:
			}
		}})
		q.Add(s)
		panes = append(panes, &feedPane{name: name, s: s, data: data, visible: visible})
	}
	for i, name := range sessions {
		data, err := os.ReadFile(filepath.Join("..", "term", "testdata", "sessions", name+".vt"))
		if err != nil {
			t.Fatal(err)
		}
		add(name, data, i < 3)
		if i == 0 {
			panes[0].focused = true
		}
	}
	add("flood-a", flood(1, 16<<20), true)
	add("flood-b", flood(2, 16<<20), false)

	frame := func() {
		c.Begin(&in, 1600, 600, 2, 1.0/60)
		k.Begin(c)
		theme.At(c, 0, 0, 1600, 600)
		c.NextAxis(ui.AxisX)
		c.Push(c.Box(theme.Floating, "root"))
		for i, p := range panes {
			if !p.visible {
				continue
			}
			c.NextSize(ui.Pct(0.25), ui.Pct(1))
			c.Push(c.BoxKeyed(0, ui.KeyN(ui.HashString(0, "pane"), uint64(i)), nil))
			o := TerminalOpts{Scheduler: q}
			if p.focused {
				o.Focus = 1
			}
			Terminal(k, p.name, p.s, o)
			c.Pop()
		}
		c.Pop()
		k.End()
		c.End(&list)
		in.ClearFrame()
	}
	frame()
	frame() // the views have their sizes

	var wg sync.WaitGroup
	start := time.Now()
	for i, p := range panes {
		if p.focused {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, p.err = p.s.Inbox().ReadFrom(&ptyReader{data: p.data, rng: rand.New(rand.NewPCG(uint64(i), 7))})
			p.s.Inbox().Close()
		}()
	}
	focus := panes[0]
	off, lateFocus := 0, 0
	var spent, focusTime []time.Duration
	maxQueued := 0
	for {
		if time.Since(start) > 60*time.Second {
			t.Fatal("the panes did not drain in 60 s")
		}
		if off < len(focus.data) {
			n := min(300, len(focus.data)-off)
			focus.s.Inbox().Write(focus.data[off : off+n])
			off += n
		}
		for _, p := range panes {
			st := p.s.Stats()
			p.queued, p.parsed = st.Queued, st.Parsed
		}
		frame()
		st := q.Stats()
		if st.Parsed > 0 {
			spent = append(spent, st.Spent)
		}
		if fs := focus.s.Stats(); fs.Queued > 0 {
			lateFocus++
		} else if fs.ParsedFrame > 0 {
			focusTime = append(focusTime, fs.ParseTime)
		}
		busy := false
		for _, p := range panes {
			s := p.s.Stats()
			maxQueued = max(maxQueued, s.Queued)
			if p.queued > 0 && s.Parsed == p.parsed {
				p.starved++
				p.worst = max(p.worst, p.starved)
			} else {
				p.starved = 0
			}
			busy = busy || s.Queued > 0 || !p.s.Inbox().Closed() && !p.focused
		}
		if !busy && off == len(focus.data) {
			break
		}
		if st.Behind == 0 && off == len(focus.data) {
			select { // idle until a reader writes: the Wake path
			case <-wake:
			case <-time.After(time.Second):
			}
		}
	}
	wg.Wait()
	elapsed := time.Since(start)

	if lateFocus > 0 {
		t.Errorf("the focused pane had output left after %d frames", lateFocus)
	}
	if maxQueued > high {
		t.Errorf("a pane queued %d bytes, over the high-water mark %d", maxQueued, high)
	}
	var total int64
	hits, worst := uint64(0), 0
	for _, p := range panes {
		st := p.s.Stats()
		total += st.Parsed
		hits += st.HighWaterHits
		if p.err != nil {
			t.Errorf("%s: ReadFrom: %v", p.name, p.err)
		}
		if st.Written != int64(len(p.data)) || st.Parsed != int64(len(p.data)) || st.Fed != 0 {
			t.Errorf("%s: %d bytes, %d written, %d parsed, %d fed", p.name, len(p.data), st.Written, st.Parsed, st.Fed)
		}
		if st.QueuedPeak > high {
			t.Errorf("%s: queue peaked at %d bytes", p.name, st.QueuedPeak)
		}
		if p.worst > len(panes) {
			t.Errorf("%s: waited %d frames in a row with output queued", p.name, p.worst)
		}
		worst = max(worst, p.worst)
		// The old path: everything fed, parsed in one go, at the same size.
		ref, err := NewTerminal(opts)
		if err != nil {
			t.Fatal(err)
		}
		cols, rows := p.s.T.Size()
		cw, ch := p.s.T.CellSize()
		if c0, r0 := ref.T.Size(); c0 != cols || r0 != rows {
			ref.T.Resize(cols, rows, cw, ch)
		}
		ref.Feed(p.data)
		ref.feedFrame(1<<40, &ref.rate)
		// The views set the palette, which the dump includes: one for both.
		var cs term.Colors
		cs.FG, cs.BG = 0xEEEEEE, 0x111111
		p.s.T.SetColors(cs)
		ref.T.SetColors(cs)
		if got, want := p.s.T.AppendScreen(nil, term.VT), ref.T.AppendScreen(nil, term.VT); !bytes.Equal(got, want) {
			i := 0
			for i < len(got) && got[i] == want[i] {
				i++
			}
			t.Errorf("%s: the screen differs from the old path's at byte %d: %q vs %q", p.name, i, got[max(0, i-40):min(len(got), i+40)], want[max(0, i-40):min(len(want), i+40)])
		}
		ref.Close()
	}
	if hits == 0 {
		t.Error("no writer ever reached the high-water mark: backpressure untested")
	}
	slices.Sort(spent)
	slices.Sort(focusTime)
	pct := func(d []time.Duration, p float64) time.Duration {
		if len(d) == 0 {
			return 0
		}
		return d[min(len(d)-1, int(float64(len(d))*p))]
	}
	if med := pct(spent, 0.5); med > budget+budget/10 {
		t.Errorf("median parse time per frame %v over the %v budget", med, budget)
	}
	msg := fmt.Sprintf("%d panes, %.1f MB in %d parsing frames, %v: per-frame parse p50 %v p99 %v max %v (budget %v); focused pane p50 %v max %v, 0 frames late; max queue %d B (high water %d); %d high-water hits; longest wait of a queued pane %d frames",
		len(panes), float64(total)/1e6, len(spent), elapsed.Round(time.Millisecond), pct(spent, 0.5), pct(spent, 0.99), pct(spent, 1),
		budget, pct(focusTime, 0.5), pct(focusTime, 1), maxQueued, high, hits, worst)
	if os.Getenv("GNUI_MEASURE") != "" {
		t.Log(msg)
	}
}

// With a scheduler and an inbox, a frame of output allocates nothing.
func TestTerminalSchedulerAllocations(t *testing.T) {
	s, err := NewTerminal(term.Options{Scrollback: 1000})
	if err != nil {
		if os.Getenv("GNUI_REQUIRE_LIBGHOSTTY") != "" {
			t.Fatal(err)
		}
		t.Skip(err)
	}
	defer s.Close()
	ts := fakeui.New()
	c := ui.New(ts)
	c.Reserve(4096)
	c.AtlasSync(&fakeui.Uploader{})
	k := base.NewKit(c, ts, rex.Theme())
	var in ui.Input
	var list gpu.DrawList
	q := NewTerminalScheduler(TerminalSchedulerOpts{})
	ib := s.OpenInbox(TerminalInboxOpts{Wake: func() {}})
	frame := func() {
		c.Begin(&in, 800, 400, 2, 1.0/60)
		k.Begin(c)
		theme.At(c, 0, 0, 800, 400)
		c.Push(c.Box(theme.Floating, "root"))
		Terminal(k, "t", s, TerminalOpts{Scheduler: q, Focus: 1})
		c.Pop()
		k.End()
		c.End(&list)
		in.ClearFrame()
	}
	line := []byte("\x1b[32mok\x1b[0m 0123456789\r\n")
	for range 200 {
		ib.Write(line)
		frame()
	}
	if n := testing.AllocsPerRun(50, func() { ib.Write(line); frame() }); n != 0 {
		t.Errorf("%v allocations per frame of output", n)
	}
}
