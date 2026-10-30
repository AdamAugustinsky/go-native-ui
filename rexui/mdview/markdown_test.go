package mdview_test

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"gonativeui/gpu"
	"gonativeui/highlight"
	"gonativeui/internal/fakeui"
	"gonativeui/markdown"
	"gonativeui/rexui/mdview"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// agentReply is an assistant message of the kind a3-app's transcript
// shows: a heading, a paragraph with code and a link, a nested and a task
// list, a quote, a table and two code blocks.
func agentReply(i int) string {
	return fmt.Sprintf("## Step %d\n\nI changed `ui/richtext.go` and **two** tests; see [the PR](https://example.com/pr/%d).\n\n"+
		"1. Read the failing test\n2. Patch `layout()`\n   - keep the cache\n\n- [x] parse\n- [ ] render\n\n"+
		"> The cache is per window.\n\n| File | Lines |\n|:--|--:|\n| a.go | 12 |\n| b.go | 7 |\n\n"+
		"```Go\nfunc step%d() error {\n\treturn nil // done\n}\n```\n\n```diff\n@@ -1 +1 @@\n-old()\n+new()\n```\n", i, i, i)
}

// layText is the fake text system with a paragraph layouter, as rich text
// needs.
type layText struct {
	*fakeui.Text
	*text.BasicLayouter
}

// mdRig is a transcript of n Markdown messages in a virtual list under one
// selection scope, on the fake text system with a paragraph layouter.
type mdRig struct {
	t     *testing.T
	c     *ui.Context
	k     *base.Kit
	in    ui.Input
	dl    gpu.DrawList
	st    base.VListState
	v     base.VList
	sc    ui.SelectionScope
	view  mdview.MarkdownView
	docs  []markdown.Doc
	ids   []uint64
	sigs  map[uint64]mdview.MarkdownSignal
	clip  string
	time  int64
	w, h  int
	links []uint32
}

func newMDRig(t *testing.T, n int) *mdRig {
	t.Helper()
	ft := fakeui.New()
	l := text.NewBasicLayouter(ft)
	l.ReserveParas(text.ParaBudget{Paras: 4096, Glyphs: 1 << 18, Text: 1 << 18})
	ts := layText{ft, l}
	g := &mdRig{t: t, c: ui.New(ts), sigs: map[uint64]mdview.MarkdownSignal{}, w: 900, h: 700}
	t.Cleanup(g.view.Close)
	g.c.Reserve(1 << 14)
	g.c.AtlasSync(&fakeui.Uploader{})
	g.k = base.NewKit(g.c, ts, rex.Theme())
	g.k.SetMode(theme.Light)
	g.k.ReduceMotion = true
	g.c.ClipboardSet = func(b []byte) { g.clip = string(b) }
	g.in.Inside, g.in.Focused = true, true
	g.in.MouseX, g.in.MouseY = -100, -100
	g.dl.Quads = make([]gpu.Quad, 0, 1<<15)
	g.docs = make([]markdown.Doc, n)
	g.ids = make([]uint64, n)
	var p markdown.Parser
	for i := range g.docs {
		p.Parse([]byte(agentReply(i))).CopyTo(&g.docs[i]) // parsed once, as a worker would
		g.ids[i] = uint64(i)
	}
	g.st.Reserve(n + 10)
	g.st.Append(g.ids)
	g.frames(4)
	return g
}

// AppendSelected is the model's text of a copy (ui.SelectionText).
func (g *mdRig) AppendSelected(dst []byte, id uint64, fp uint16, fo uint32, tp uint16, to uint32) []byte {
	return mdview.MarkdownSelected(dst, &g.docs[id], 0, fp, fo, tp, to)
}

func (g *mdRig) frame() {
	g.time += 16_666_667
	g.in.Time = g.time
	c, k := g.c, g.k
	c.Begin(&g.in, g.w, g.h, 2, 1.0/60)
	k.Begin(c)
	clear(g.sigs)
	g.v = base.BeginVList(c, "transcript", &g.st, base.VListOpts{Estimate: 300})
	c.BeginSelection(&g.sc, ui.ScopeOpts{Items: &g.st, View: g.v.Outer, Text: g})
	for i := g.v.First; i < g.v.Last; i++ {
		id := g.v.Item(i)
		c.NextPadding(24, 12, 24, 12)
		c.NextSize(ui.Pct(1), ui.Children(1))
		c.Push(c.BoxKeyed(0, ui.KeyN(c.ParentKey(), 1), nil))
		sig := mdview.Markdown(k, ui.KeyN(c.ParentKey(), 0), &g.docs[id], mdview.MarkdownOpts{Item: id, View: &g.view})
		c.Pop()
		g.sigs[id] = sig
		if sig.Link != 0 {
			g.links = append(g.links, sig.Link)
		}
	}
	c.EndSelection()
	base.EndVList(&g.v)
	k.End()
	c.End(&g.dl)
	g.in.ClearFrame()
}

func (g *mdRig) frames(n int) {
	for range n {
		g.frame()
	}
}

// settle runs frames until the list builds the same messages three frames
// running with the highlighting worker idle after each (bounded). After a
// resize or a scroll the list's range moves for a few frames; a message it
// builds for the first time hands its code to the worker, and an idle worker
// before that proves nothing: under load (CI runs every package at once) the
// worker would still be lexing in the measured frames, and AllocsPerRun
// counts the whole process's allocations.
func (g *mdRig) settle() {
	steady := 0
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
		first, last := g.v.First, g.v.Last
		g.frame()
		switch {
		case g.view.Pending() > 0:
			steady = 0
			time.Sleep(2 * time.Millisecond)
		case g.v.First != first || g.v.Last != last:
			steady = 0
		default:
			if steady++; steady == 3 {
				return
			}
		}
	}
	g.t.Fatal("the transcript never settled")
}

// node is the first node of the exported tree with the role whose name
// holds s.
func (g *mdRig) node(role ui.Role, s string) (ui.SemNode, bool) {
	var sem ui.SemTree
	g.c.Semantics(&sem)
	for _, n := range sem.Nodes {
		if n.Role == role && strings.Contains(string(sem.Text(n.Name)), s) {
			return n, true
		}
	}
	return ui.SemNode{}, false
}

func (g *mdRig) click(x, y float32) {
	g.in.MouseX, g.in.MouseY = x, y
	g.frame()
	g.in.SetButton(ui.ButtonLeft, true, 1)
	g.frame()
	g.in.SetButton(ui.ButtonLeft, false, 0)
	g.frames(2)
}

// A transcript of 1,000 Markdown messages in a virtual list draws its
// steady frames with no allocation: the documents are parsed and the code
// highlighted before, off the frame.
func TestMarkdownTranscriptAllocatesNothing(t *testing.T) {
	g := newMDRig(t, 1000)
	g.h = 4000
	g.settle()
	if g.v.Last-g.v.First < 2 {
		t.Fatalf("only %d messages built", g.v.Last-g.v.First)
	}
	if n := testing.AllocsPerRun(30, g.frame); n != 0 {
		t.Fatalf("a steady frame of the transcript allocates %v times", n)
	}
	// scrolled to the middle and settled there, still none
	g.in.MouseX, g.in.MouseY = 450, 350
	for range 40 {
		g.in.ScrollY = -2000
		g.frame()
	}
	g.settle()
	if g.v.First == 0 {
		t.Fatal("the wheel did not move the transcript")
	}
	if n := testing.AllocsPerRun(30, g.frame); n != 0 {
		t.Fatalf("a steady frame after scrolling allocates %v times", n)
	}
}

// The blocks are what a reader sees: a heading at its level, list items
// with markers, task boxes with their state, the table's cells, and the code
// blocks with their language, a Copy button and highlighted lines.
func TestMarkdownBlocks(t *testing.T) {
	g := newMDRig(t, 3)
	g.h = 2400
	g.settle()
	if n, ok := g.node(ui.RoleHeading, "Step 0"); !ok || n.Level != 2 {
		t.Errorf("heading: %+v %v", n, ok)
	}
	for _, s := range []string{"Read the failing test", "keep the cache", "The cache is per window.", "a.go", "Lines"} {
		if _, ok := g.node(ui.RoleLabel, s); !ok {
			t.Errorf("no text %q", s)
		}
	}
	if n, ok := g.node(ui.RoleCheckbox, "Task"); !ok || n.State&ui.SemChecked == 0 {
		t.Errorf("the first task box is not checked: %+v %v", n, ok)
	}
	if _, ok := g.node(ui.RoleTable, ""); !ok {
		t.Error("no table")
	}
	for _, s := range []string{"go", "diff"} {
		if _, ok := g.node(ui.RoleLabel, s); !ok {
			t.Errorf("no code label %q", s)
		}
	}
	// a highlighted code line: its keyword in the keyword colour
	kw := mdview.CodeColor(highlight.Keyword, 0)
	found := false
	for _, q := range g.dl.Quads {
		if q.Color[0] == kw {
			found = true
			break
		}
	}
	if !found {
		t.Error("no glyph drawn in the keyword colour: the code is not highlighted")
	}
	// the diff's bands
	added := mdview.CodeLook.Added.RGBA(g.k.Theme())
	found = false
	for _, q := range g.dl.Quads {
		if q.Color[0] == added {
			found = true
		}
	}
	if !found {
		t.Error("no added-line band")
	}
}

// A click on a link reports its tag, whose destination the document gives;
// nothing is opened.
func TestMarkdownLinkClick(t *testing.T) {
	g := newMDRig(t, 3)
	g.settle()
	n, ok := g.node(ui.RoleLink, "the PR")
	if !ok {
		t.Fatal("no link node")
	}
	g.click((n.Bounds[0]+n.Bounds[2])/2, (n.Bounds[1]+n.Bounds[3])/2)
	if len(g.links) == 0 {
		t.Fatal("the click on the link was not reported")
	}
	var dest string
	for id := range g.docs {
		if d := g.docs[id].LinkDest(g.links[0]); d != nil {
			dest = string(d)
			break
		}
	}
	if !strings.HasPrefix(dest, "https://example.com/pr/") {
		t.Errorf("the link's destination is %q", dest)
	}
}

// The Copy button of a code block puts its code on the clipboard and says
// which block it was.
func TestCodeBlockCopy(t *testing.T) {
	g := newMDRig(t, 2)
	g.settle()
	n, ok := g.node(ui.RoleButton, "Copy")
	if !ok {
		t.Fatal("no Copy button")
	}
	copied := -1
	g.in.MouseX, g.in.MouseY = (n.Bounds[0]+n.Bounds[2])/2, (n.Bounds[1]+n.Bounds[3])/2
	g.frame()
	g.in.SetButton(ui.ButtonLeft, true, 1)
	g.frame()
	g.in.SetButton(ui.ButtonLeft, false, 0)
	g.frame()
	for _, s := range g.sigs {
		if s.Copied >= 0 {
			copied = s.Copied
		}
	}
	if g.clip != "func step0() error {\n\treturn nil // done\n}" {
		t.Errorf("the clipboard holds %q", g.clip)
	}
	if copied < 0 || g.docs[0].Blocks[copied].Kind != markdown.Code {
		t.Errorf("Copied = %d", copied)
	}
}

// ⌘A and ⌘C in the transcript copy the text of the messages in order:
// paragraphs, list items, cells and code lines, from the model.
func TestMarkdownSelectionCopies(t *testing.T) {
	g := newMDRig(t, 2)
	g.settle()
	n, ok := g.node(ui.RoleLabel, "I changed")
	if !ok {
		t.Fatal("no paragraph")
	}
	g.click(n.Bounds[0]+4, n.Bounds[1]+4) // a press in a text focuses the scope
	g.in.PushKey(ui.KeyEvent{Code: 'A', Mods: ui.PrimaryMod(), Down: true})
	g.frame()
	g.in.PushKey(ui.KeyEvent{Code: 'C', Mods: ui.PrimaryMod(), Down: true})
	g.frame()
	for _, want := range []string{"Step 0\nI changed ui/richtext.go", "Read the failing test\nPatch layout()", "File\tLines\na.go\t12", "func step0() error {\n\treturn nil // done\n}", "Step 1"} {
		if !strings.Contains(g.clip, want) {
			t.Errorf("the copy lacks %q:\n%s", want, g.clip)
		}
	}
}

// A drag from a paragraph into the code block below it selects across the
// blocks: the copy holds the end of the one and the start of the other.
func TestMarkdownSelectAcrossBlocks(t *testing.T) {
	g := newMDRig(t, 1)
	g.settle()
	a, ok1 := g.node(ui.RoleLabel, "The cache is per window.")
	b, ok2 := g.node(ui.RoleLabel, "func step0")
	if !ok1 || !ok2 {
		t.Fatal("texts not found")
	}
	g.in.MouseX, g.in.MouseY = a.Bounds[0]+2, (a.Bounds[1]+a.Bounds[3])/2
	g.frame()
	g.in.SetButton(ui.ButtonLeft, true, 1)
	g.frame()
	g.in.MouseX, g.in.MouseY = b.Bounds[0]+40, (b.Bounds[1]+b.Bounds[3])/2
	g.frame()
	g.in.SetButton(ui.ButtonLeft, false, 0)
	g.frame()
	g.in.PushKey(ui.KeyEvent{Code: 'C', Mods: ui.PrimaryMod(), Down: true})
	g.frame()
	if !strings.HasPrefix(g.clip, "The cache is per window.") || !strings.Contains(g.clip, "a.go\t12") || !strings.Contains(g.clip, "\nfun") {
		t.Errorf("the drag copied %q", g.clip)
	}
}

// Precise vertical deltas (a trackpad without a gesture) over a code block
// scroll the transcript, not the block, which scrolls sideways only. A
// mouse wheel's notches there scroll the block sideways (ui's rule).
func TestCodeBlockPassesVerticalScroll(t *testing.T) {
	g := newMDRig(t, 50)
	g.settle()
	n, ok := g.node(ui.RoleLabel, "func step0")
	if !ok {
		t.Fatal("no code line")
	}
	g.in.MouseX, g.in.MouseY = n.Bounds[0]+10, (n.Bounds[1]+n.Bounds[3])/2
	for range 10 {
		g.in.ScrollY = -300
		g.in.Precise = true
		g.frame()
	}
	g.frames(10)
	if m, ok := g.node(ui.RoleLabel, "func step0"); ok && m.Bounds[1] >= n.Bounds[1]-50 {
		t.Errorf("precise vertical deltas over a code block did not scroll the transcript: the line went from %v to %v", n.Bounds[1], m.Bounds[1])
	}
}

// Streaming: a message that grows by chunks, parsed on change, draws each
// step; the code block keeps its colours from the last highlighting while
// the new tokens are made.
func TestMarkdownStreams(t *testing.T) {
	g := newMDRig(t, 1)
	g.h = 2400 // the last line must be visible to be built
	msg := agentReply(0)
	var p markdown.Parser
	for i := 0; i < len(msg); i += 40 {
		p.Append([]byte(msg[i:min(i+40, len(msg))])).CopyTo(&g.docs[0])
		g.frames(2)
	}
	g.settle()
	if _, ok := g.node(ui.RoleLabel, "+new()"); !ok {
		t.Error("the streamed message lacks its last line")
	}
}

// Large gutters, mixed-case fences, and visible-line construction are all
// on the warmed frame path, including callers that draw individual rows.
func TestLargeCodeFramesAllocateNothing(t *testing.T) {
	g := newMDRig(t, 0)
	src := []byte(strings.Repeat("var value = 123\n", 1000))
	key := ui.HashString(0, "large-code")
	lang := highlight.LangOf("TyPeScRiPt")
	frame := func() {
		g.time += 16_666_667
		g.in.Time = g.time
		g.c.Begin(&g.in, 900, 700, 2, 1.0/60)
		g.k.Begin(g.c)
		lang = highlight.LangOf("TyPeScRiPt")
		mdview.CodeBlock(g.k, key, src, mdview.CodeOpts{Lang: lang, Gutter: true, FirstLine: 1000, View: &g.view})
		mdview.CodeWidth(g.k, 30, mdview.CodeLineOpts{Number: 1234, View: &g.view})
		mdview.CodeLine(g.k, ui.KeyN(key, 10000), []byte("value"), nil, mdview.CodeLineOpts{Number: 1234, View: &g.view})
		g.k.End()
		g.c.End(&g.dl)
		g.in.ClearFrame()
	}
	for range 20 {
		frame()
	}
	if n := g.c.Boxes(); n > 150 {
		t.Fatalf("1000 lines built %d boxes; invisible lines were not culled", n)
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range 100 {
		frame()
	}
	runtime.ReadMemStats(&after)
	if n := after.Mallocs - before.Mallocs; n != 0 {
		t.Fatalf("large code frames allocated %d objects / %d bytes", n, after.TotalAlloc-before.TotalAlloc)
	}
}
