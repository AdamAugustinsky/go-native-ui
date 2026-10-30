package app

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math/rand/v2"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"gonativeui/core"
	filesys "gonativeui/fs"
	"gonativeui/gpu"
	"gonativeui/img"
	"gonativeui/markdown"
	"gonativeui/platform"
	"gonativeui/rexui"
	"gonativeui/rexui/mdview"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// simScene exercises each owned worker family with real text and renderer,
// while synthetic directory contents avoid a host filesystem dependency.
type simScene struct {
	a                 *App
	k                 *base.Kit
	field             ui.TextBuffer
	fieldKey          ui.Key
	list              base.VListState
	ids               []uint64
	view              mdview.MarkdownView
	doc               markdown.Doc
	styles            ui.RichStyles
	font              text.FontKey
	prefetch          *text.Prefetcher
	files             *filesys.Service
	results           []filesys.Result
	images            *img.Cache
	png               []byte
	imageState        img.State
	burst             int
	listings, imports int
}

func (b *simScene) Init(a *App) error {
	b.a = a
	b.k = base.NewKit(a.UI, a.Text, rex.Theme())
	b.k.ReduceMotion = true
	b.font = a.Text.Font(text.FontDesc{SizePt: 13, Weight: 400, Scale: 1})
	b.styles.Set(0, ui.RichStyle{Font: b.font})
	b.styles.Set(1, ui.RichStyle{Font: b.font, Color: 0x3070d0ff, Deco: ui.DecoHoverUnderline})
	b.field.SetString("simulation")
	b.ids = make([]uint64, 10000)
	for i := range b.ids {
		b.ids[i] = uint64(i + 1)
	}
	b.list.Reserve(len(b.ids))
	b.list.Append(b.ids)
	var p markdown.Parser
	p.Parse([]byte("A **Markdown** view with [a link](https://example.com).\n\n```go\nfunc answer() int { return 42 }\n```\n")).CopyTo(&b.doc)
	var err error
	b.prefetch, err = text.NewPrefetcher(2, 16, platform.Wake)
	if err != nil {
		return err
	}
	b.prefetch.Submit(1, a.Text, b.font, []byte("worker prefetch fi fl"))
	b.files = filesys.NewServiceWith(platform.Wake, func(_ string, l *filesys.Listing) error {
		const n = 330000
		if cap(l.Entries) < n {
			l.Entries = make([]filesys.Entry, n)
		} else {
			l.Entries = l.Entries[:n]
		}
		l.Names = append(l.Names[:0], 'x')
		for i := range l.Entries {
			l.Entries[i] = filesys.Entry{NameLen: 1, Size: int64(i)}
		}
		return nil
	})
	b.results = make([]filesys.Result, 0, 8)
	var out bytes.Buffer
	if err := png.Encode(&out, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		return err
	}
	b.png = out.Bytes()
	b.images = img.NewCache(img.Options{Renderer: a.R, Workers: 2, Entries: 16, Wake: platform.Wake})
	return nil
}

func (b *simScene) Build(c *ui.Context) {
	b.imports += b.prefetch.Poll(b.a.Text, 16)
	b.results = b.files.Poll(b.results[:0])
	for _, r := range b.results {
		if r.Listing != nil {
			b.listings++
			b.files.Release(r.Listing, r.Lease)
		}
	}
	if b.images.Poll() {
		c.RequestFrame()
	}
	_, b.imageState = b.images.Request(img.Key{Hash: 1, W: 8, H: 8}, img.Bytes(b.png))
	b.k.Begin(c)
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextAxis(ui.AxisY)
	c.NextPad(12)
	c.NextGap(8)
	c.Open(ui.Clip, "root")
	b.k.NextWidth(ui.Pct(1))
	b.fieldKey = rexui.TextField(b.k, "Name", &b.field, "", rexui.FieldOpts{}).Key
	mdview.Markdown(b.k, ui.HashString(c.ParentKey(), "markdown"), &b.doc, mdview.MarkdownOpts{View: &b.view})
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.RichText(ui.HashString(c.ParentKey(), "rich"), []byte("Read the linked guide"), []ui.Span{{Off: 9, Len: 12, Style: 1, Flags: ui.SpanLink, Tag: 1}}, &b.styles, ui.RichOpts{})
	c.NextSize(ui.Pct(1), ui.Grow(1))
	v := base.BeginVList(c, "rows", &b.list, base.VListOpts{Estimate: 22})
	for i := v.First; i < v.Last; i++ {
		v.Item(i)
		c.NextSize(ui.Pct(1), ui.Px(22))
		c.NextFont(b.font)
		c.NextTextColor(b.k.Theme().Colors[theme.Label])
		c.Box(ui.DrawText, "row")
	}
	base.EndVList(&v)
	for i := 0; i < b.burst; i++ {
		c.NextPos(0, 0)
		c.NextSize(ui.Px(1), ui.Px(1))
		c.NextBG(0x30303080)
		c.BoxKeyed(ui.FloatingX|ui.FloatingY|ui.DrawBackground, ui.KeyN(c.ParentKey(), uint64(100000+i)), nil)
	}
	c.Close()
	b.k.End()
}

func (b *simScene) SimClose() {
	b.view.Close()
	if b.prefetch != nil {
		b.prefetch.Close()
	}
	if b.files != nil {
		b.files.Close()
	}
	if b.images != nil {
		b.images.Close(time.Second)
	}
}
func (b *simScene) SimRetained() map[string]int {
	return map[string]int{"listing-pool": b.files.RetainedPoolBytes(), "vlist-back": b.list.BackCapacity()}
}

func simMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func runSimSmoke(t *testing.T, seed uint64, hours time.Duration) uint64 {
	t.Helper()
	baseline := runtime.NumGoroutine()
	b := new(simScene)
	s, err := NewSim(Config{}, 900, 700, 1, b, SimOptions{Seed: seed, Render: true})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	finish := core.Now() + int64(hours)
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	simMust(t, s.Advance(time.Second))
	if b.imports != 1 || b.view.Pending() != 0 || b.imageState != img.Ready {
		t.Fatalf("workers: imports=%d highlight=%d image=%v", b.imports, b.view.Pending(), b.imageState)
	}
	// Grow the real GPU ring and box slab, and retain a VList rebuild back.
	b.burst = 10000
	simMust(t, s.Event(0, platform.Event{Kind: platform.EvWake}))
	simMust(t, s.Advance(100*time.Millisecond))
	b.list.Reset(b.ids)
	for range 4 {
		b.files.List(1, "/gnui-synthetic-not-on-disk", filesys.SortName, false)
		simMust(t, s.Advance(100*time.Millisecond))
	}
	peak, err := s.Memory()
	simMust(t, err)
	if peak.Boxes < 10000 || peak.Instances < 10000 || peak.Subsystems["0/listing-pool"] <= 8<<20 {
		t.Fatalf("stress did not grow capacities: %+v", peak)
	}
	b.burst = 0
	for range 210 {
		simMust(t, s.Event(0, platform.Event{Kind: platform.EvWake}))
		simMust(t, s.Advance(20*time.Millisecond))
	}
	// The VList and listing pool owe a cleanup after 30s; no busy frame loop.
	idle, err := s.Idle(time.Minute, SimDeadline|SimContinuation)
	simMust(t, err)
	floor, err := s.CheckMemory(SimMemory{Boxes: 2048, RunGlyphs: text.MaxCachedGlyphs, RunText: text.MaxCachedTextBytes, Instances: 8192, Subsystems: map[string]int{"0/listing-pool": 8 << 20, "0/vlist-back": 0}})
	simMust(t, err)
	if floor.Boxes >= peak.Boxes || floor.Instances >= peak.Instances || floor.HeapLive >= peak.HeapLive {
		t.Fatalf("retained memory did not fall: peak=%+v floor=%+v", peak, floor)
	}
	t.Logf("seed=%d peak heap=%d boxes=%d instances=%d pool=%d; idle floor heap=%d boxes=%d instances=%d; cleanup=%+v", seed, peak.HeapLive, peak.Boxes, peak.Instances, peak.Subsystems["0/listing-pool"], floor.HeapLive, floor.Boxes, floor.Instances, idle)
	// Isolate the exact D11 gate from scenario/trace/worker allocations.
	s.CheckAllocations(true)
	for range 64 {
		simMust(t, s.Event(0, platform.Event{Kind: platform.EvWake}))
		simMust(t, s.Advance(20*time.Millisecond))
	}
	s.CheckAllocations(false)
	for burst := 0; burst < 6; burst++ {
		b.k.Focus(b.fieldKey)
		for range 4 {
			x, y := float32(rng.IntN(500)), float32(240+rng.IntN(250))
			simMust(t, s.Event(0, platform.Event{Kind: platform.EvPointerMove, X: x, Y: y}))
			simMust(t, s.Event(0, platform.Event{Kind: platform.EvScroll, X: x, Y: y, DY: float32(rng.IntN(7) - 3)}))
			simMust(t, s.Event(0, platform.Event{Kind: platform.EvKey, Key: platform.KeyEnd, Flags: platform.FlagDown}))
			simMust(t, s.Event(0, platform.Event{Kind: platform.EvText}, []byte{byte('a' + rng.IntN(26))}))
			simMust(t, s.Advance(50*time.Millisecond))
		}
		// A real clipboard paste answer, targeted to the focused field.
		s.Clipboard([]byte(" paste"))
		simMust(t, s.Event(0, platform.Event{Kind: platform.EvPaste, Tag: uint64(b.fieldKey)}, s.clipboard))
		simMust(t, s.Event(0, platform.Event{Kind: platform.EvFocus}))
		simMust(t, s.Event(0, platform.Event{Kind: platform.EvVisible}))
		simMust(t, s.Advance(2*time.Second))
		quiet, err := s.Idle(max(hours-time.Hour-5*time.Minute, 0)/6, 0)
		simMust(t, err)
		if quiet.Frames != 0 {
			t.Fatal(quiet)
		}
		if burst == 1 {
			simMust(t, s.Appearance(true))
		}
		if burst == 2 {
			simMust(t, s.Event(0, platform.Event{Kind: platform.EvResize, W: 960, H: 720, Scale: 1.25}))
		}
		if burst == 3 {
			_, err = s.AXQuery(0)
			simMust(t, err)
			simMust(t, s.Advance(time.Minute))
			if s.windows[0].ax.Active() {
				t.Fatal("AX did not expire")
			}
			_, err = s.AXQuery(0)
			simMust(t, err)
		}
		if burst == 4 {
			simMust(t, s.DeviceLost())
		}
		simMust(t, s.Event(0, platform.Event{Kind: platform.EvVisible, Flags: platform.FlagDown}))
		simMust(t, s.Event(0, platform.Event{Kind: platform.EvFocus, Flags: platform.FlagDown}))
		b.k.Focus(0)
		simMust(t, s.Event(0, platform.Event{Kind: platform.EvPointerLeave}))
		simMust(t, s.Advance(2*time.Second))
	}
	// A completely unchanged return must reproduce the exact draw list.
	draw, _ := s.FrameHash(0)
	beforeQuads := append([]gpu.Quad(nil), s.windows[0].a.list.Quads...)
	checkReturn := SimReturnFrame(0, draw)
	s.AddCheck(func(s *Sim) error {
		if err := checkReturn(s); err != nil {
			after := s.windows[0].a.list.Quads
			for i := 0; i < min(len(beforeQuads), len(after)); i++ {
				if beforeQuads[i] != after[i] {
					return fmt.Errorf("%w; quads %d -> %d, first difference %d: %+v -> %+v", err, len(beforeQuads), len(after), i, beforeQuads[i], after[i])
				}
			}
			return fmt.Errorf("%w; quads %d -> %d", err, len(beforeQuads), len(after))
		}
		return nil
	})
	simMust(t, s.Event(0, platform.Event{Kind: platform.EvVisible}))
	simMust(t, s.Event(0, platform.Event{Kind: platform.EvFocus}))
	_, err = s.Idle(time.Duration(max(finish-core.Now()-int64(time.Second), 0)), 0)
	simMust(t, err)
	simMust(t, s.Event(0, platform.Event{Kind: platform.EvVisible, Flags: platform.FlagDown}))
	simMust(t, s.Event(0, platform.Event{Kind: platform.EvFocus, Flags: platform.FlagDown}))
	simMust(t, s.Advance(time.Second))
	hash := s.TraceHash()
	t.Logf("seed=%d trace=%016x frames=%d simulated=%s", seed, hash, s.windows[0].frames, time.Duration(core.Now()-int64(time.Second)))
	s.Close()
	simMust(t, s.CheckGoroutines(baseline))
	return hash
}

func TestSimFourHours(t *testing.T) {
	hours := 4 * time.Hour
	if v := os.Getenv("GNUI_SIM_HOURS"); v != "" {
		h, err := strconv.ParseFloat(v, 64)
		if err != nil || h <= 0 {
			t.Fatal("invalid GNUI_SIM_HOURS")
		}
		hours = time.Duration(h * float64(time.Hour))
	}
	seeds := []uint64{1}
	if v := os.Getenv("GNUI_SIM_SEEDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			t.Fatal("invalid GNUI_SIM_SEEDS")
		}
		seeds = make([]uint64, n)
		for i := range seeds {
			seeds[i] = uint64(i + 1)
		}
	}
	if v := os.Getenv("GNUI_SIM_SEED"); v != "" {
		n, err := strconv.ParseUint(v, 0, 64)
		simMust(t, err)
		seeds = []uint64{n}
	}
	for _, seed := range seeds {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			a := runSimSmoke(t, seed, hours)
			b := runSimSmoke(t, seed, hours)
			if a != b {
				t.Fatalf("seed=%d replay diverged: %016x != %016x", seed, a, b)
			}
		})
	}
}

type simCodeScene struct {
	k    *base.Kit
	view mdview.MarkdownView
}

func (b *simCodeScene) Init(a *App) error {
	b.k = base.NewKit(a.UI, a.Text, rex.Theme())
	b.k.ReduceMotion = true
	return nil
}
func (b *simCodeScene) Build(c *ui.Context) {
	b.k.Begin(c)
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextAxis(ui.AxisY)
	c.Open(ui.Clip, "root")
	mdview.CodeBlock(b.k, ui.Key(1), []byte("func answer() int { return 42 }"), mdview.CodeOpts{View: &b.view})
	c.Close()
	b.k.End()
}
func (b *simCodeScene) SimClose() { b.view.Close() }

func TestSimCodeScaleChange(t *testing.T) {
	s, err := NewSim(Config{}, 500, 300, 1, new(simCodeScene), SimOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	simMust(t, s.Advance(time.Second))
	simMust(t, s.Event(0, platform.Event{Kind: platform.EvVisible}))
	simMust(t, s.Advance(time.Hour))
	simMust(t, s.Event(0, platform.Event{Kind: platform.EvResize, W: 600, H: 400, Scale: 1.25}))
	simMust(t, s.Event(0, platform.Event{Kind: platform.EvVisible, Flags: platform.FlagDown}))
	simMust(t, s.Advance(time.Second))
}

type simFlat struct {
	due           int64
	ticks, closed int
	allocate      bool
	held          [][]byte
}

type simConverging struct{ overflow bool }

func (b simConverging) Build(c *ui.Context) {
	c.NextSize(ui.Pct(1), ui.Pct(1))
	p := c.Box(0, "parent")
	r := c.Rect(p)
	c.Push(p)
	w := float32(200)
	if !b.overflow && r[2] > r[0] {
		w = r[2] - r[0]
	}
	c.NextSize(ui.Px(w), ui.Px(20))
	c.NextBG(0xff0000ff)
	c.Box(ui.DrawBackground, "child")
	c.Pop()
}

func TestSimAuditConvergesAndRejectsSettledOverflow(t *testing.T) {
	for _, overflow := range []bool{false, true} {
		t.Run(fmt.Sprint(overflow), func(t *testing.T) {
			s, err := NewSim(Config{}, 100, 100, 1, simConverging{overflow}, SimOptions{})
			simMust(t, err)
			defer s.Close()
			err = s.Advance(time.Second)
			if overflow {
				if err == nil || !strings.Contains(err.Error(), "overflow") {
					t.Fatalf("settled overflow: %v", err)
				}
			} else {
				simMust(t, err)
			}
		})
	}
}

func (b *simFlat) Build(c *ui.Context) {
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextBG(0x202020ff)
	c.Box(ui.DrawBackground, "flat")
	if b.allocate {
		b.held = append(b.held, make([]byte, 64))
	}
	if b.due != 0 {
		if c.Input().Time >= b.due {
			b.ticks++
			b.due = 0
		} else {
			c.WakeIn(b.due - c.Input().Time)
		}
	}
}
func (b *simFlat) SimClose() { b.closed++ }

func TestSimIndependentWindowsAndPanel(t *testing.T) {
	b := new(simFlat)
	s, err := NewSim(Config{}, 200, 100, 1, b, SimOptions{})
	simMust(t, err)
	defer s.Close()
	other := &simFlat{due: core.Now() + int64(time.Hour)}
	id, err := s.OpenWindow(Config{}, 300, 200, 2, other)
	simMust(t, err)
	panel := new(simFlat)
	pid, err := s.OpenWindow(Config{Panel: &platform.Panel{}}, 100, 100, 1, panel)
	simMust(t, err)
	simMust(t, s.Advance(time.Second))
	if s.windows[pid].frames != 0 {
		t.Fatal("shelved panel built")
	}
	initial := s.windows[0].frames
	idle, err := s.Idle(time.Hour, SimDeadline)
	simMust(t, err)
	if idle.Frames != 1 || other.ticks != 1 || s.windows[0].frames != initial {
		t.Fatalf("windows shared pacing: %+v ticks=%d", idle, other.ticks)
	}
	simMust(t, s.Event(pid, platform.Event{Kind: platform.EvVisible, Flags: platform.FlagDown}))
	simMust(t, s.Advance(time.Second))
	if s.windows[pid].frames == 0 {
		t.Fatal("shown panel did not draw")
	}
	simMust(t, s.Event(id, platform.Event{Kind: platform.EvClose}))
	if other.closed != 1 || s.shared.done {
		t.Fatal("closing secondary ended app or leaked builder")
	}
	simMust(t, s.Event(0, platform.Event{Kind: platform.EvClose}))
	simMust(t, s.Event(pid, platform.Event{Kind: platform.EvClose}))
	if !s.shared.done || b.closed != 1 || panel.closed != 1 {
		t.Fatal("last window did not close")
	}
}

func TestSimAllocationGateDetectsOneAllocation(t *testing.T) {
	b := new(simFlat)
	s, err := NewSim(Config{}, 100, 100, 1, b, SimOptions{})
	simMust(t, err)
	defer s.Close()
	simMust(t, s.Advance(time.Second))
	s.CheckAllocations(true)
	b.allocate = true
	simMust(t, s.Event(0, platform.Event{Kind: platform.EvWake}))
	if err = s.Advance(time.Second); err == nil || !strings.Contains(err.Error(), "simFlat") {
		t.Fatalf("allocation gate missed planted allocation, or did not name its site: %v", err)
	}
}

func TestSimRestoresProcessors(t *testing.T) {
	previous := runtime.GOMAXPROCS(2)
	defer runtime.GOMAXPROCS(previous)
	s, err := NewSim(Config{}, 100, 100, 1, new(simFlat), SimOptions{})
	simMust(t, err)
	defer s.Close()
	if runtime.GOMAXPROCS(0) != 1 {
		t.Fatal("simulation is not isolated")
	}
	s.Close()
	if runtime.GOMAXPROCS(0) != 2 {
		t.Fatal("close did not restore processors")
	}
}

type simSettlingLayout struct {
	frames, bad int
	follow      bool
}

func (b *simSettlingLayout) Build(c *ui.Context) {
	b.frames++
	c.NextSize(ui.Px(50), ui.Px(50))
	c.Open(0, "parent")
	w := float32(40)
	if b.frames <= b.bad {
		w = 100
	}
	c.NextSize(ui.Px(w), ui.Px(40))
	c.NextBG(0x123456ff)
	c.Box(ui.DrawBackground, "child")
	c.Close()
	if b.follow {
		c.RequestFrame()
	}
}

func TestSimAuditBoundsLayoutSettling(t *testing.T) {
	for _, tc := range []struct {
		name         string
		bad          int
		follow, fail bool
	}{
		{"settles", 2, true, false},
		{"persistent", 3, true, true},
		{"static", 1, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := NewSim(Config{}, 100, 100, 1, &simSettlingLayout{bad: tc.bad, follow: tc.follow}, SimOptions{})
			simMust(t, err)
			defer s.Close()
			err = s.Advance(100 * time.Millisecond)
			if (err != nil) != tc.fail {
				t.Fatalf("failure=%v, want %v", err, tc.fail)
			}
			if err != nil && !strings.Contains(err.Error(), "layout: overflow") {
				t.Fatal(err)
			}
		})
	}
}

type simPanic struct{}

func (simPanic) Build(*ui.Context) { panic("planted simulation panic") }
func TestSimPanicContainsReplayContext(t *testing.T) {
	s, err := NewSim(Config{}, 100, 100, 1, simPanic{}, SimOptions{Seed: 99})
	simMust(t, err)
	defer s.Close()
	if err = s.Advance(0); err == nil || !strings.Contains(err.Error(), "seed=99 step=1") || !strings.Contains(err.Error(), "planted simulation panic") {
		t.Fatalf("missing replay context: %v", err)
	}
}

func TestSimClipboardAndAXActivation(t *testing.T) {
	b := new(simScene)
	s, err := NewSim(Config{}, 900, 700, 1, b, SimOptions{})
	simMust(t, err)
	defer s.Close()
	b.a.UI.SetSemanticsActive(false)
	simMust(t, s.Advance(time.Second))
	b.k.Focus(b.fieldKey)
	b.field.SelectAll()
	s.Clipboard([]byte("isolated paste"))
	mod := platform.ModCtrl
	if runtime.GOOS == "darwin" {
		mod = platform.ModSuper
	}
	simMust(t, s.Event(0, platform.Event{Kind: platform.EvPointerMove, X: 25, Y: 25}))
	simMust(t, s.Event(0, platform.Event{Kind: platform.EvKey, Key: platform.KeyA + ('V' - 'A'), Mods: mod, Flags: platform.FlagDown}))
	simMust(t, s.Advance(time.Second))
	if b.field.String() != "isolated paste" {
		t.Fatalf("paste response not delivered: %q", b.field.String())
	}
	tree, err := s.AXQuery(0)
	simMust(t, err)
	if len(tree.Nodes) != 0 {
		t.Fatal("AX published before the activating build")
	}
	simMust(t, s.Advance(time.Second))
	tree, err = s.AXQuery(0)
	simMust(t, err)
	if len(tree.Nodes) < 2 {
		t.Fatal("AX activation did not publish controls")
	}
	b.k.Focus(0)
	simMust(t, s.Event(0, platform.Event{Kind: platform.EvWake}))
	simMust(t, s.Advance(time.Minute))
	if s.windows[0].ax.Active() {
		t.Fatal("AX client did not expire")
	}
	tree, err = s.AXQuery(0)
	simMust(t, err)
	if len(tree.Nodes) < 2 || !s.windows[0].ax.Active() {
		t.Fatal("AX reactivation lost its tree")
	}
}
