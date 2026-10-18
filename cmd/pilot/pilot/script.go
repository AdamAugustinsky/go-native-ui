package pilot

import (
	"fmt"
	"os"
	"time"

	"gonativeui/fs"
	"gonativeui/platform"
	"gonativeui/ui"
)

// The benchmark session: a fixed sequence of steps that drives a Pilot with
// the input a person would give it: scrolling, hovering, clicking, selecting
// with keys, typing a filter and clearing it, switching panes, sorting by each
// column, dragging the divider, toggling hidden files, and relists of the
// directory (what a watch event brings). It feeds ui.Input exactly as
// translated platform events would, through app.Scripter, and repeats.

type opKind uint8

const (
	opIdle    opKind = iota // n frames without input
	opWait                  // until nothing is loading (at most n frames, at least 2)
	opWheel                 // pane a: pointer over its list, b pixels of trackpad scroll per frame, n frames
	opHover                 // pane a: the pointer goes down the list one row per frame from visible row b, n frames
	opClick                 // pane a, visible row b: press, then release a frame later
	opKey                   // key a with modifiers m, once
	opKeys                  // key a with modifiers m on each of n frames
	opText                  // typed text scriptTexts[a]
	opHeader                // click the header of column a of pane b
	opRefresh               // pane a is listed again (a changed directory when synthetic)
	opDrag                  // drag the divider b pixels per frame for n frames and let go
	opToggle                // toggle hidden files in pane a (a key press)
)

// modifiers of a step; modPrim is ⌘ on macOS and Ctrl elsewhere.
const (
	modPrim = 1 << iota
	modShift
	modAlt
)

type step struct {
	op   opKind
	a, b int
	m    uint8
	n    int
}

var scriptTexts = [...]string{"e", "1", "re"}

var benchSteps = [...]step{
	// scroll the left list: down, then a jump to the end and back to the top
	{op: opWheel, a: 0, b: -36, n: 90},
	{op: opWheel, a: 0, b: 52, n: 40},
	{op: opKey, a: int(ui.KeyEnd)},
	{op: opIdle, n: 6},
	{op: opKey, a: int(ui.KeyHome)},
	{op: opIdle, n: 6},
	// hover
	{op: opHover, a: 0, b: 0, n: 30},
	{op: opHover, a: 1, b: 2, n: 20},
	// click, ⌘-click, shift-click
	{op: opClick, a: 0, b: 3},
	{op: opIdle, n: 2},
	{op: opClick, a: 0, b: 5, m: modPrim},
	{op: opIdle, n: 2},
	{op: opClick, a: 0, b: 9, m: modShift},
	{op: opIdle, n: 2},
	// keys: cursor, extend, pages, home/end
	{op: opKeys, a: int(ui.KeyDown), n: 10},
	{op: opKeys, a: int(ui.KeyDown), m: modShift, n: 10},
	{op: opKeys, a: int(ui.KeyUp), m: modShift, n: 4},
	{op: opKeys, a: int(ui.KeyPageDown), n: 3},
	{op: opKeys, a: int(ui.KeyPageUp), m: modShift, n: 2},
	{op: opKey, a: int(ui.KeyEnd)},
	{op: opIdle, n: 4},
	{op: opKey, a: int(ui.KeyHome)},
	{op: opIdle, n: 4},
	// type a filter, narrow it, clear it
	{op: opText, a: 0},
	{op: opWait, n: 200},
	{op: opText, a: 1},
	{op: opWait, n: 200},
	{op: opKeys, a: int(ui.KeyDown), n: 4},
	{op: opKeys, a: int(ui.KeyBackspace), n: 1},
	{op: opWait, n: 200},
	{op: opKey, a: int(ui.KeyEscape)},
	{op: opWait, n: 200},
	// the other pane
	{op: opKey, a: int(ui.KeyTab)},
	{op: opWheel, a: 1, b: -30, n: 30},
	{op: opClick, a: 1, b: 4},
	{op: opIdle, n: 2},
	{op: opKey, a: int(ui.KeyHome)}, // back to the top: the session must end where it began
	{op: opIdle, n: 8},
	{op: opKey, a: 'A', m: modPrim},
	{op: opIdle, n: 3},
	{op: opKey, a: int(ui.KeyEscape)},
	{op: opIdle, n: 3},
	{op: opKey, a: int(ui.KeyTab)},
	// sort by each column, then by name again
	{op: opHeader, a: 1, b: 0},
	{op: opWait, n: 400},
	{op: opHeader, a: 1, b: 0},
	{op: opWait, n: 400},
	{op: opHeader, a: 2, b: 0},
	{op: opWait, n: 400},
	{op: opHeader, a: 0, b: 0},
	{op: opWait, n: 400},
	// the divider
	{op: opDrag, b: 6, n: 30},
	{op: opDrag, b: -6, n: 30},
	// the directory changed on disk: relist with the selection and scroll kept
	{op: opClick, a: 0, b: 4},
	{op: opIdle, n: 2},
	{op: opRefresh, a: 0},
	{op: opWait, n: 600},
	{op: opRefresh, a: 1},
	{op: opWait, n: 600},
	// hidden files
	{op: opKey, a: hiddenKey, m: modPrim | modShift},
	{op: opWait, n: 400},
	{op: opKey, a: hiddenKey, m: modPrim | modShift},
	{op: opWait, n: 400},
	{op: opIdle, n: 4},
}

// hiddenKey is the key of the hidden-files shortcut (⌘⇧. / Ctrl+H); the script
// sends the right one for the OS in modifiers()/code().
const hiddenKey = -1

// script runs benchSteps against a Pilot.
type script struct {
	pl    *Pilot
	idx   int // current step
	t     int // frames into it
	synth *SynthSource

	// the list is scrolled over / the pointer parked at
	lastX, lastY float32
	divX         float32
	cycles       int
	err          error // the first step that did not complete
}

func (s *script) mods(m uint8) ui.Mods {
	var r ui.Mods
	if m&modPrim != 0 {
		r |= s.pl.primary()
	}
	if m&modShift != 0 {
		r |= ui.ModShift
	}
	if m&modAlt != 0 {
		r |= ui.ModAlt
	}
	return r
}

// busy reports whether anything the session asked for is still on its way: a
// navigation, a relist (sorting, a refresh) that has not been delivered, or the
// work on a delivered listing.
func (s *script) busy() bool {
	for _, p := range s.pl.panes {
		if p.loading() || p.relPending || p.jobBusy || p.rc.active {
			return true
		}
	}
	return false
}

// fail records that the session did not do what it says (the first such thing
// is kept): a measurement of a session that skipped work is not one.
func (s *script) fail(format string, args ...any) {
	if s.err == nil {
		s.err = fmt.Errorf("pilot: bench session step %d: "+format, append([]any{s.idx}, args...)...)
	}
}

// listPoint is a point inside visible row r of pane i's list.
func (s *script) listPoint(i, r int) (x, y float32) {
	g := &s.pl.geo[i]
	if g.rowPx == 0 {
		return g.rect[0] + 100, g.rect[1] + 100
	}
	return g.list[0] + 0.45*(g.list[2]-g.list[0]), g.list[1] + (float32(r)+0.5)*g.rowPx
}

// headerPoint is a point on the header of column col in pane i.
func (s *script) headerPoint(i, col int) (x, y float32) {
	pl := s.pl
	g := &pl.geo[i]
	L := pl.layoutPane(g.rect[2]-g.rect[0], g.rect[3]-g.rect[1], pl.panes[i].filterOpen)
	y = g.rect[1] + (L.hdr[1]+L.hdr[3])/2
	switch col {
	case 1:
		if L.showSize {
			return g.rect[0] + L.list[0] + L.sizeR - 4*pl.Adv, y
		}
	case 2:
		if L.showDate {
			return g.rect[0] + L.list[0] + L.dateX + 6*pl.Adv, y
		}
	}
	return g.rect[0] + L.list[0] + L.nameX + 2*pl.Adv, y
}

func (s *script) move(in *ui.Input, x, y float32) {
	in.MouseX, in.MouseY, in.Inside = x, y, true
	s.lastX, s.lastY = x, y
}

// frame feeds one frame of input.
func (s *script) frame(in *ui.Input) {
	pl := s.pl
	in.Inside, in.Focused = true, true
	if s.idx >= len(benchSteps) {
		s.idx, s.t = 0, 0
		s.cycles++
	}
	st := &benchSteps[s.idx]
	done := false
	switch st.op {
	case opIdle:
		done = s.t+1 >= st.n
	case opWait:
		done = s.t >= 1 && (!s.busy() || s.t+1 >= st.n)
		if done && s.busy() {
			s.fail("still loading after %d frames", st.n)
		}
	case opWheel:
		if s.t == 0 || s.lastX == 0 {
			x, y := s.listPoint(st.a, 4)
			s.move(in, x, y)
		}
		pl.active = st.a
		in.AddScroll(0, float32(st.b), true)
		done = s.t+1 >= st.n
	case opHover:
		x, y := s.listPoint(st.a, st.b+s.t%max(pl.geo[st.a].visRows, 1))
		s.move(in, x, y)
		done = s.t+1 >= st.n
	case opClick:
		if s.t == 0 {
			x, y := s.listPoint(st.a, st.b)
			s.move(in, x, y)
			in.Mods = s.mods(st.m)
			in.SetButton(ui.ButtonLeft, true, 1)
		} else {
			in.SetButton(ui.ButtonLeft, false, 0)
			in.Mods = 0
			done = true
		}
	case opKey, opKeys:
		code, m := ui.KeyCode(st.a), st.m
		if st.a == hiddenKey {
			code = 'H'
			if pl.mac {
				code = ui.KeyPeriod
			} else {
				m &^= modShift
			}
		}
		in.Mods = s.mods(m)
		in.PushKey(ui.KeyEvent{Code: code, Mods: in.Mods, Down: true})
		in.Mods = 0
		done = st.op == opKey || s.t+1 >= st.n
	case opText:
		in.AppendText(scriptTexts[st.a])
		done = true
	case opHeader:
		if s.t == 0 {
			x, y := s.headerPoint(st.b, st.a)
			s.move(in, x, y)
			in.SetButton(ui.ButtonLeft, true, 1)
		} else {
			in.SetButton(ui.ButtonLeft, false, 0)
			done = true
		}
	case opRefresh:
		pl.Refresh(st.a, s.synth)
		done = true
	case opDrag:
		_, g, _ := pl.paneRects()
		if s.t == 0 {
			s.divX = (g[0] + g[2]) / 2
			s.move(in, s.divX, (g[1]+g[3])/2)
			in.SetButton(ui.ButtonLeft, true, 1)
		} else if s.t < st.n {
			s.divX += float32(st.b) * pl.Scale
			s.move(in, s.divX, (g[1]+g[3])/2)
		} else {
			in.SetButton(ui.ButtonLeft, false, 0)
			done = true
		}
	}
	if done {
		s.idx++
		s.t = 0
	} else {
		s.t++
	}
}

// Refresh lists pane i's directory again, as a change on disk would. With a
// synthetic source the new listing differs a little from the last.
func (pl *Pilot) Refresh(i int, synth *SynthSource) {
	p := pl.panes[i]
	if p.src == nil || p.dir == "" || p.nav.pending {
		return
	}
	if synth != nil {
		synth.Bump()
	}
	p.relist()
}

// Bench is a Pilot that runs the benchmark session as its app.Scripter.
type Bench struct {
	*Pilot
	s script
}

// Script implements app.Scripter. It never ends; the bench stops after its
// measured frames.
func (b *Bench) Script(frame int, in *ui.Input) bool {
	b.s.frame(in)
	return true
}

// AtCycleStart implements app.CycleAligned: the next frame begins the session.
func (b *Bench) AtCycleStart() bool {
	return b.s.idx >= len(benchSteps) || (b.s.idx == 0 && b.s.t == 0)
}

// Err is the first step of the session whose wait ran out with work still
// outstanding, nil if every step completed. A bench with an Err did not measure
// the session it claims to.
func (b *Bench) Err() error { return b.s.err }

// Cycles is how many times the session has run through.
func (b *Bench) Cycles() int { return b.s.cycles }

// NewSynthBench is a Pilot on synthetic directories served through the real
// fs.Service: rows entries in the left pane and a tenth of that on the right.
// Both are listed and sorted before it returns.
func NewSynthBench(rows int) *Bench {
	synth := &SynthSource{}
	src := fs.NewServiceWith(platform.Wake, synth.List)
	left, right := synthDir(rows), synthDir(max(rows/10, 100))
	o := Options{Src: src, Home: "/", Loc: time.UTC}
	for i, dir := range [2]string{left, right} {
		l := new(fs.Listing)
		_ = synth.List(dir, l)
		fs.Sort(l, fs.SortName, false)
		o.Initial[i] = ready(dir, l)
	}
	if err := prewarm(src, synthDir(rows+64)); err != nil {
		panic(err)
	}
	pl := New(o)
	return &Bench{Pilot: pl, s: script{pl: pl, synth: synth}}
}

// NewDirBench is a Pilot on a real directory (both panes) through the real
// fs.Service with watching on. The setup lists the directory off the main
// thread and gives up after timeout: a listing can block indefinitely (a
// macOS privacy prompt for ~/Documents, a dead network mount), and the main
// thread must stay free to report it.
func NewDirBench(dir string, timeout time.Duration) (*Bench, error) {
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	dir = ResolvePath(dir, cwd, home) // as a path field would: absolute, native separators
	o := Options{Src: fs.NewService(platform.Wake), Home: home}
	done := make(chan error, 1)
	go func() {
		for i := range o.Initial {
			l := new(fs.Listing)
			if err := fs.ListDir(dir, l); err != nil {
				done <- fmt.Errorf("pilot: %s: %w", dir, err)
				return
			}
			fs.Sort(l, fs.SortName, false)
			o.Initial[i] = ready(dir, l)
		}
		done <- prewarm(o.Src.(*fs.Service), dir)
	}()
	select {
	case err := <-done:
		if err != nil {
			return nil, err
		}
	case <-time.After(timeout):
		return nil, fmt.Errorf("pilot: listing %s took longer than %v (a privacy prompt waiting for an answer, or a stuck volume?)", dir, timeout)
	}
	pl := New(o)
	return &Bench{Pilot: pl, s: script{pl: pl}}, nil
}

// StartCounting makes Build count the rows it builds (RowsBuilt).
func (pl *Pilot) StartCounting() { pl.counting = true }

// RowsBuilt is the mean and the largest number of list rows (both panes) built
// per frame since StartCounting.
func (pl *Pilot) RowsBuilt() (mean float64, max int) {
	if pl.buildCalls > 0 {
		mean = float64(pl.rowsBuilt) / float64(pl.buildCalls)
	}
	return mean, pl.rowsMax
}

// LeftEntries is the number of entries in the left pane's listing.
func (pl *Pilot) LeftEntries() int { return pl.panes[0].v.nEntries() }

// prewarmListings is how many listings prewarm grows: more than a session holds
// at once (two shown, two being reconciled, results in flight).
const prewarmListings = 10

// prewarm lists dir prewarmListings times at once, in every sort order, and
// gives the listings back: the Service's pool then holds listings (and the sort
// scratch) that have reached the size of dir, so that no listing in the measured
// frames grows a buffer. The sizes are those of the biggest directory.
func prewarm(src *fs.Service, dir string) error {
	const base = 1000
	for k := 0; k < prewarmListings; k++ {
		src.List(base+uint32(k), dir, fs.SortKey(k%4), k%2 == 1)
	}
	var res []fs.Result
	got := 0
	for deadline := time.Now().Add(120 * time.Second); got < prewarmListings; {
		if time.Now().After(deadline) {
			return fmt.Errorf("pilot: prewarm of %s timed out", dir)
		}
		res = src.Poll(res[:0])
		for i := range res {
			if res[i].Kind == fs.ResultListing {
				got++
			}
		}
		// hold every listing until all have arrived, then release them together
		if got < prewarmListings {
			held = append(held, res...)
			time.Sleep(time.Millisecond)
		}
	}
	held = append(held, res...)
	for i := range held {
		if held[i].Listing != nil {
			src.Release(held[i].Listing, held[i].Lease)
		}
	}
	held = nil
	for k := 0; k < prewarmListings; k++ {
		src.Unwatch(base + uint32(k))
	}
	return nil
}

var held []fs.Result
