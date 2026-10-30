package uisim_test

import (
	"fmt"
	"math/rand/v2"
	"os"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/core"
	"gonativeui/highlight"
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

// scene keeps a separate row model so publication is checked against the
// edits supplied while the window was asleep, not against VList's own index.
type scene struct {
	a                            *app.App
	k                            *base.Kit
	field                        ui.TextBuffer
	fieldKey, buttonKey, stepKey ui.Key
	value                        float32
	list                         base.VListState
	ids                          []uint64
	doc                          markdown.Doc
	parser                       markdown.Parser
	view                         mdview.MarkdownView
	source                       string
	burst                        int
	builds                       int
	popover                      bool
	behavior                     rexui.PopoverBehavior
}

func (b *scene) Init(a *app.App) error {
	b.a = a
	b.k = base.NewKit(a.UI, a.Text, rex.Theme())
	b.k.ReduceMotion = true
	if b.ids == nil {
		b.ids = make([]uint64, 1_000_000)
		for i := range b.ids {
			b.ids[i] = uint64(i + 1)
		}
	}
	b.list.Reserve(len(b.ids))
	b.list.Append(b.ids)
	if b.field.Len() == 0 {
		b.field.SetString("Draft café 👩‍💻")
	}
	if b.source == "" {
		b.source = "A **streaming** reply.\n\n```go\nvar answer = 42\n```\n"
	}
	b.parse()
	return nil
}

func (b *scene) parse() { b.parser.Parse([]byte(b.source)).CopyTo(&b.doc) }

func (b *scene) Build(c *ui.Context) {
	b.builds++
	b.k.Begin(c)
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextAxis(ui.AxisY)
	c.NextPad(c.Pt(12))
	c.NextGap(c.Pt(8))
	c.Open(ui.Clip, "root")
	b.k.NextWidth(ui.Pct(1))
	b.fieldKey = rexui.TextField(b.k, "Draft", &b.field, "", rexui.FieldOpts{}).Key
	c.NextSize(ui.Pct(1), ui.Px(c.Pt(32)))
	c.NextAxis(ui.AxisX)
	c.NextGap(c.Pt(8))
	c.Open(0, "tools")
	button := rexui.Button(b.k, "Details", rexui.ButtonOpts{})
	b.buttonKey = c.KeyOf(button.ID)
	rexui.Tooltip(b.k, button.ID, "Draft details")
	rexui.ContextMenu(b.k, button.ID, func(items *[]ui.MenuItem) {
		*items = append(*items, ui.MenuItem{ID: 1, Title: "Copy"}, ui.MenuItem{ID: 2, Title: "Archive"})
	})
	b.stepKey = c.KeyOf(rexui.Stepper(b.k, "Count", &b.value, 0, 1000000, 1, rexui.StepperOpts{}).ID)
	c.Close()
	c.NextSize(ui.Pct(1), ui.Grow(1))
	c.NextAxis(ui.AxisX)
	c.NextGap(c.Pt(8))
	c.Open(ui.Clip, "content")
	c.NextSize(ui.Pct(.5), ui.Pct(1))
	c.NextAxis(ui.AxisY)
	c.Open(ui.Clip, "message")
	mdview.Markdown(b.k, ui.HashString(c.ParentKey(), "markdown"), &b.doc, mdview.MarkdownOpts{View: &b.view})
	c.Close()
	c.NextSize(ui.Grow(1), ui.Pct(1))
	v := base.BeginVList(c, "rows", &b.list, base.VListOpts{Estimate: 22})
	for i := v.First; i < v.Last; i++ {
		v.Item(i)
		var buf [32]byte
		label := strconv.AppendUint(append(buf[:0], "Row "...), b.list.ID(i), 10)
		c.NextSize(ui.Pct(1), ui.Px(c.Pt(22)))
		c.NextFont(b.k.Theme().Styles[theme.StyleBody].Font)
		c.NextTextColor(b.k.Theme().Colors[theme.Label])
		c.NextRole(ui.RoleListItem)
		c.NextSemNameBytes(label)
		c.NextSemPos(i, b.list.Len())
		c.BoxKeyed(ui.DrawText, ui.KeyN(c.ParentKey(), 1), label)
	}
	base.EndVList(&v)
	c.Close()
	for i := 0; i < b.burst; i++ {
		c.NextPos(0, 0)
		c.NextSize(ui.Px(1), ui.Px(1))
		c.NextBG(0x30303080)
		c.BoxKeyed(ui.FloatingX|ui.FloatingY|ui.DrawBackground, ui.KeyN(c.ParentKey(), uint64(100000+i)), nil)
	}
	if b.popover {
		p := rexui.BeginPopover(b.k, "Details", c.RectOf(b.buttonKey), rexui.PopoverOpts{Behavior: b.behavior})
		rexui.Label(b.k, "Saved draft", rexui.LabelOpts{})
		rexui.EndPopover(b.k, &p)
		if p.Dismissed {
			b.popover = false
			c.RequestFrame()
		}
	}
	c.Close()
	b.k.End()
}

func (b *scene) SimClose() { b.view.Close() }
func (b *scene) SimRetained() map[string]int {
	return map[string]int{"vlist-back": b.list.BackCapacity()}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func event(t *testing.T, s *app.Sim, e platform.Event, data ...[]byte) {
	t.Helper()
	must(t, s.Event(0, e, data...))
}
func wake(t *testing.T, s *app.Sim, d time.Duration) {
	t.Helper()
	event(t, s, platform.Event{Kind: platform.EvWake})
	must(t, s.Advance(d))
}
func hide(t *testing.T, s *app.Sim) {
	t.Helper()
	event(t, s, platform.Event{Kind: platform.EvFocus})
	event(t, s, platform.Event{Kind: platform.EvPointerLeave})
	event(t, s, platform.Event{Kind: platform.EvVisible})
}
func show(t *testing.T, s *app.Sim) {
	t.Helper()
	event(t, s, platform.Event{Kind: platform.EvVisible, Flags: platform.FlagDown})
	event(t, s, platform.Event{Kind: platform.EvFocus, Flags: platform.FlagDown})
	must(t, s.Advance(12*time.Second))
}
func quiet(t *testing.T, s *app.Sim, d time.Duration) {
	t.Helper()
	r, err := s.Idle(d, 0)
	must(t, err)
	if r.Frames != 0 {
		t.Fatalf("hidden frames: %+v", r)
	}
}

func options(t *testing.T) ([]uint64, time.Duration) {
	t.Helper()
	n, hours := 1, 24.0
	if v := os.Getenv("GNUI_SIM_SEEDS"); v != "" {
		var err error
		n, err = strconv.Atoi(v)
		must(t, err)
	}
	if v := os.Getenv("GNUI_SIM_HOURS"); v != "" {
		var err error
		hours, err = strconv.ParseFloat(v, 64)
		must(t, err)
	}
	if n < 1 || hours < 1 || hours > 24*365 {
		t.Fatal("invalid GNUI_SIM_SEEDS or GNUI_SIM_HOURS")
	}
	seeds := make([]uint64, n)
	for i := range seeds {
		seeds[i] = uint64(i + 1)
	}
	if v := os.Getenv("GNUI_SIM_SEED"); v != "" {
		seed, err := strconv.ParseUint(v, 0, 64)
		must(t, err)
		seeds = []uint64{seed}
	}
	return seeds, time.Duration(hours * float64(time.Hour))
}

func checkModel(t *testing.T, b *scene) {
	t.Helper()
	if b.list.Pending() || b.list.Len() != len(b.ids) {
		t.Fatalf("list publication: pending=%v len=%d want=%d", b.list.Pending(), b.list.Len(), len(b.ids))
	}
	for i, id := range b.ids {
		if got := b.list.ID(i); got != id {
			t.Fatalf("row %d=%d, want %d", i, got, id)
		}
	}
}

func tree(c *ui.Context) ui.SemTree { var out ui.SemTree; c.Semantics(&out); return out }
func equalTrees(t *testing.T, got, want ui.SemTree) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		for i := 0; i < min(len(got.Nodes), len(want.Nodes)); i++ {
			if got.Nodes[i] != want.Nodes[i] {
				t.Fatalf("semantic node %d: got %+v; fresh %+v", i, got.Nodes[i], want.Nodes[i])
			}
		}
		t.Fatalf("semantic tree differs: nodes=%d/%d bytes=%q/%q", len(got.Nodes), len(want.Nodes), got.Bytes, want.Bytes)
	}
}

func run(t *testing.T, seed uint64, hours time.Duration) uint64 {
	t.Helper()
	baseline := runtime.NumGoroutine()
	b := new(scene)
	s, err := app.NewSim(app.Config{}, 900, 700, 1, b, app.SimOptions{Seed: seed})
	must(t, err)
	defer s.Close()
	finish := core.Now() + int64(hours)
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	must(t, s.Advance(time.Second))
	checkModel(t, b)
	_, err = s.AXQuery(0)
	must(t, err)
	b.burst = 6000
	wake(t, s, 100*time.Millisecond)
	b.list.Reset(b.ids)
	wake(t, s, 5*time.Second)
	for i := 0; b.list.Pending() && i < 60; i++ {
		must(t, s.Advance(time.Second))
	}
	if b.list.Pending() {
		t.Fatal("million-row rebuild did not converge")
	}
	peak, err := s.Memory()
	must(t, err)
	if peak.Boxes < 6000 || b.list.BackCapacity() == 0 {
		t.Fatalf("stress missing: %+v", peak)
	}
	b.burst = 0
	for range 150 {
		wake(t, s, 20*time.Millisecond)
	}
	r, err := s.Idle(time.Minute, app.SimDeadline|app.SimContinuation)
	must(t, err)
	if r.Frames > 2 {
		t.Fatalf("idle cleanup kept drawing: %+v", r)
	}
	floor, err := s.CheckMemory(app.SimMemory{Boxes: 2048, RunGlyphs: text.MaxCachedGlyphs, RunText: text.MaxCachedTextBytes, Subsystems: map[string]int{"0/vlist-back": 0}})
	must(t, err)
	if floor.HeapLive >= peak.HeapLive || floor.Boxes >= peak.Boxes {
		t.Fatalf("memory did not shrink: peak=%+v floor=%+v", peak, floor)
	}
	s.CheckAllocations(true)
	for range 16 {
		wake(t, s, 20*time.Millisecond)
	}
	s.CheckAllocations(false)
	for cycle := 0; cycle < 8; cycle++ {
		b.k.Focus(b.fieldKey)
		b.field.SetCaret(b.field.Len())
		for range 1 + rng.IntN(5) {
			event(t, s, platform.Event{Kind: platform.EvText}, []byte{byte('a' + rng.IntN(26))})
			must(t, s.Advance(40*time.Millisecond))
		}
		b.field.SetSelection(0, min(5, b.field.Len()))
		b.list.ScrollTo(rng.IntN(len(b.ids)/2), base.AlignStart)
		wake(t, s, time.Second)
		before, caret := b.field.String(), b.field.Caret()
		lo, hi := b.field.Selection()
		anchor, offset := b.list.Anchor()
		// Composition is transient; deactivation must neither commit it nor
		// erase the selected text. Clear it as the native IME does on blur.
		event(t, s, platform.Event{Kind: platform.EvPreedit, SelStart: 3}, []byte("かな"))
		must(t, s.Advance(20*time.Millisecond))
		hide(t, s)
		event(t, s, platform.Event{Kind: platform.EvPreedit})
		quiet(t, s, time.Duration(1+rng.IntN(60))*time.Second)
		for chunk := 0; chunk < 3; chunk++ {
			b.source = fmt.Sprintf("Reply **%d** arrived while away. %s\n\n```go\nvar answer = %d\n```\n", cycle, strings.Repeat("More streamed text. ", cycle*3+chunk+1), rng.IntN(9999))
			b.parse()
			// Queue actual highlights while the source grows. Completion may
			// wake the loop, but cannot draw through the visibility gate.
			b.view.Tokens(b.a.UI, ui.Key(0xace), highlight.Go, []byte(b.source))
			event(t, s, platform.Event{Kind: platform.EvWake})
			quiet(t, s, time.Duration(1+rng.IntN(10))*time.Second)
		}
		insert := []uint64{uint64(2_000_000 + cycle*2), uint64(2_000_001 + cycle*2)}
		b.list.Splice(0, 1, insert)
		b.ids = slices.Replace(b.ids, 0, 1, insert...)
		must(t, s.Appearance(cycle%2 == 0))
		b.k.SetAccent(0xb06040ff + uint32(cycle)*0x10000)
		b.a.UI.SetSystemValues(uint32(cycle+1), &ui.SystemValues{Known: true, IncreaseContrast: cycle%2 == 0})
		scale := []float32{1, 2, 1.5}[cycle%3]
		event(t, s, platform.Event{Kind: platform.EvResize, W: int32(900 * scale), H: int32(700 * scale), Scale: scale})
		quiet(t, s, (hours-10*time.Minute)/9)
		show(t, s)
		wantT := float32(0)
		if cycle%2 == 0 {
			wantT = 1
		}
		if th := b.k.Theme(); th.T != wantT || th.Colors[theme.Accent] != 0xb06040ff+uint32(cycle)*0x10000 || th.Settings.IncreaseContrast != (cycle%2 == 0) {
			t.Fatalf("stale appearance: t=%g accent=%x contrast=%v", th.T, th.Colors[theme.Accent], th.Settings.IncreaseContrast)
		}
		checkModel(t, b)
		if b.view.Pending() != 0 {
			t.Fatal("highlight did not finish")
		}
		gotLo, gotHi := b.field.Selection()
		if b.field.String() != before || b.field.Caret() != caret || gotLo != lo || gotHi != hi || b.a.UI.FocusKey() != b.fieldKey {
			t.Fatalf("draft changed across return: %q caret=%d selection=%d,%d focus=%x", b.field.String(), b.field.Caret(), gotLo, gotHi, b.a.UI.FocusKey())
		}
		if id, off := b.list.Anchor(); id != anchor || off != offset {
			t.Fatalf("scroll anchor %d/%g -> %d/%g", anchor, offset, id, off)
		}
		ax, err := s.AXQuery(0)
		must(t, err)
		current := tree(b.a.UI)
		if !slices.Equal(ax.Nodes, current.Nodes) || !slices.Equal(ax.Bytes, current.Bytes) {
			t.Fatal("expired AX client returned a stale tree")
		}
		if b.a.UI.ActiveKey() != 0 {
			t.Fatal("stuck press after return")
		}
	}
	// Compare the returned tree with a clean build of the same application
	// state, including the selection and virtual-list anchor.
	anchor, offset := b.list.Anchor()
	fresh := &scene{ids: slices.Clone(b.ids), source: b.source, value: b.value}
	fresh.field.SetString(b.field.String())
	lo, hi := b.field.Selection()
	fresh.field.SetSelection(lo, hi)
	id, err := s.OpenWindow(app.Config{}, 1800, 1400, 2, fresh)
	must(t, err)
	fresh.k.SetAccent(0xb76040ff)
	fresh.a.UI.SetSystemValues(8, &ui.SystemValues{Known: true})
	must(t, s.Advance(time.Second))
	fresh.list.SetAnchor(anchor, offset)
	fresh.k.Focus(fresh.fieldKey)
	must(t, s.Event(id, platform.Event{Kind: platform.EvWake}))
	must(t, s.Advance(12*time.Second))
	equalTrees(t, tree(b.a.UI), tree(fresh.a.UI))
	must(t, s.Event(id, platform.Event{Kind: platform.EvClose}))
	b.k.Focus(0)
	wake(t, s, time.Second)
	draw, _ := s.FrameHash(0)
	s.AddCheck(app.SimReturnFrame(0, draw))
	hide(t, s)
	quiet(t, s, time.Duration(max(finish-core.Now()-int64(12*time.Second), 0)))
	show(t, s)
	if elapsed := time.Duration(core.Now() - (finish - int64(hours))); elapsed != hours {
		t.Fatalf("simulated %s, want %s", elapsed, hours)
	}
	hash := s.TraceHash()
	t.Logf("seed=%d hours=%g trace=%016x builds=%d peakHeap=%d floorHeap=%d boxes=%d->%d cleanup=%+v", seed, hours.Hours(), hash, b.builds, peak.HeapLive, floor.HeapLive, peak.Boxes, floor.Boxes, r)
	s.Close()
	must(t, s.CheckGoroutines(baseline))
	return hash
}

func TestSimUILongReturn(t *testing.T) {
	seeds, hours := options(t)
	for _, seed := range seeds {
		t.Run(strconv.FormatUint(seed, 10), func(t *testing.T) {
			a, b := run(t, seed, hours), run(t, seed, hours)
			if a != b {
				t.Fatalf("seed=%d replay diverged: %016x != %016x", seed, a, b)
			}
		})
	}
}
