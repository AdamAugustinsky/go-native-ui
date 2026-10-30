package rexui_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"gonativeui/gpu"
	"gonativeui/internal/fakeui"
	"gonativeui/rexui"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// layText is the fake text system with a paragraph layouter, as rich text
// needs.
type layText struct {
	*fakeui.Text
	*text.BasicLayouter
}

// chatRig is a list of message bubbles in a virtual list under one selection
// scope (C4): the transcript of a chat.
type chatRig struct {
	t     *testing.T
	c     *ui.Context
	k     *base.Kit
	in    ui.Input
	dl    gpu.DrawList
	st    base.VListState
	v     base.VList
	sc    ui.SelectionScope
	sig   ui.ScopeSignal
	msgs  map[uint64][]byte
	texts map[uint64]ui.ID
	clip  string
	time  int64
	style ui.RichStyles
}

func newChatRig(t *testing.T, n int) *chatRig {
	ft := fakeui.New()
	l := text.NewBasicLayouter(ft)
	l.ReserveParas(text.ParaBudget{Paras: 1024, Glyphs: 1 << 16, Text: 1 << 16})
	ts := layText{ft, l}
	g := &chatRig{t: t, c: ui.New(ts), msgs: map[uint64][]byte{}, texts: map[uint64]ui.ID{}}
	g.c.Reserve(4096)
	g.c.AtlasSync(&fakeui.Uploader{})
	g.k = base.NewKit(g.c, ts, rex.Theme())
	g.k.SetMode(theme.Light)
	g.k.ReduceMotion = true
	g.c.ClipboardSet = func(b []byte) { g.clip = string(b) }
	g.in.Inside, g.in.Focused = true, true
	g.in.MouseX, g.in.MouseY = -100, -100
	g.dl.Quads = make([]gpu.Quad, 0, 1<<13)
	ids := make([]uint64, n)
	for i := range ids {
		ids[i] = uint64(1000 + i)
		g.msgs[ids[i]] = []byte(fmt.Sprintf("message %d", i) + strings.Repeat(" lorem ipsum", i%5))
	}
	g.st.Reserve(n + 10)
	g.st.Append(ids)
	g.frames(4)
	return g
}

// AppendSelected is the model's text of a copy.
func (g *chatRig) AppendSelected(dst []byte, id uint64, fp uint16, fo uint32, tp uint16, to uint32) []byte {
	return append(dst, ui.SelectedBytes(g.msgs[id], 0, fp, fo, tp, to)...)
}

func (g *chatRig) frame() {
	g.time += 16_666_667
	g.in.Time = g.time
	c, k := g.c, g.k
	c.Begin(&g.in, 500, 400, 2, 1.0/60)
	k.Begin(c)
	clear(g.texts)
	g.v = base.BeginVList(c, "transcript", &g.st, base.VListOpts{Estimate: 40})
	g.sig = c.BeginSelection(&g.sc, ui.ScopeOpts{Items: &g.st, View: g.v.Outer, Text: g})
	for i := g.v.First; i < g.v.Last; i++ {
		id := g.v.Item(i)
		v := rexui.BubbleIncoming
		if i%2 == 1 {
			v = rexui.BubbleOutgoing
		}
		rexui.BubbleRichStyles(k, v, &g.style)
		sig := rexui.MessageBubble(k, ui.KeyN(c.ParentKey(), 0), g.msgs[id], nil, rexui.MessageOpts{
			BubbleOpts: rexui.BubbleOpts{Variant: v, ColumnW: 240}, Styles: &g.style, Item: id,
		})
		g.texts[id] = sig.Text.ID
	}
	c.EndSelection()
	base.EndVList(&g.v)
	k.End()
	c.End(&g.dl)
	g.in.ClearFrame()
}

func (g *chatRig) frames(n int) {
	for range n {
		g.frame()
	}
}

// A drag from one bubble's text to another's, in a virtual list, selects the
// texts between them and copies their plain text; a bubble keeps its own
// shape (the message is the rich text inside it).
func TestMessageBubblesSelectAcrossAVirtualList(t *testing.T) {
	g := newChatRig(t, 200)
	var ids []uint64
	for i := g.v.First; i < g.v.Last; i++ {
		if id := g.st.ID(i); g.c.Rect(g.texts[id])[1] >= 0 {
			ids = append(ids, id)
		}
	}
	if len(ids) < 4 {
		t.Fatalf("only %d bubbles in view", len(ids))
	}
	a, b := ids[1], ids[3] // consecutive ids: ids[2] lies between them
	ra, rb := g.c.Rect(g.texts[a]), g.c.Rect(g.texts[b])
	g.in.MouseX, g.in.MouseY = ra[0]+3, ra[1]+3
	g.frame()
	g.in.SetButton(ui.ButtonLeft, true, 1)
	g.frame()
	g.in.MouseX, g.in.MouseY = rb[0]+30, rb[1]+3
	g.frame()
	g.in.SetButton(ui.ButtonLeft, false, 0)
	g.frame()
	from, to, ok := g.sc.Selected()
	if !ok || from.Item != a || to.Item != b {
		t.Fatalf("selected %v to %v (%v), want items %d to %d", from, to, ok, a, b)
	}
	g.in.PushKey(ui.KeyEvent{Code: 'C', Mods: ui.PrimaryMod(), Down: true})
	g.frame()
	got := g.clip
	if want := string(g.msgs[a+1]); !strings.Contains(got, want) || !strings.HasPrefix(got, "message") {
		t.Fatalf("the copy %q lacks the bubble between them (%q)", got, want)
	}
}

// A selection that spans bubbles survives the list recycling its rows: scroll
// until the bubbles at both ends are out of the built range, and the selection
// is still those items and copies all of their text, from the model.
func TestBubbleSelectionSurvivesRecycling(t *testing.T) {
	g := newChatRig(t, 200)
	var ids []uint64
	for i := g.v.First; i < g.v.Last; i++ {
		if id := g.st.ID(i); g.c.Rect(g.texts[id])[1] >= 0 {
			ids = append(ids, id)
		}
	}
	a, b := ids[1], ids[3]
	ra, rb := g.c.Rect(g.texts[a]), g.c.Rect(g.texts[b])
	g.in.MouseX, g.in.MouseY = ra[0]+3, ra[1]+3
	g.frame()
	g.in.SetButton(ui.ButtonLeft, true, 1)
	g.frame()
	g.in.MouseX, g.in.MouseY = rb[0]+30, rb[1]+3
	g.frame()
	g.in.SetButton(ui.ButtonLeft, false, 0)
	g.frame()
	if from, to, ok := g.sc.Selected(); !ok || from.Item != a || to.Item != b {
		t.Fatalf("selected %v to %v (%v), want items %d to %d", from, to, ok, a, b)
	}
	// the wheel carries the transcript far away: the selected bubbles are recycled
	g.in.MouseX, g.in.MouseY = 250, 200
	g.frame()
	built := func(id uint64) bool {
		for i := g.v.First; i < g.v.Last; i++ {
			if g.st.ID(i) == id {
				return true
			}
		}
		return false
	}
	for i := 0; i < 80 && (built(a) || built(b)); i++ {
		g.in.ScrollY = -600
		g.frame()
	}
	g.frames(3)
	if built(a) || built(b) {
		t.Fatal("the selected bubbles were never recycled")
	}
	from, to, ok := g.sc.Selected()
	if !ok || from.Item != a || to.Item != b {
		t.Fatalf("after recycling: selected %v to %v (%v), want items %d to %d", from, to, ok, a, b)
	}
	g.in.PushKey(ui.KeyEvent{Code: 'C', Mods: ui.PrimaryMod(), Down: true})
	g.frame()
	if want := string(g.msgs[a+1]); !strings.Contains(g.clip, want) || strings.Count(g.clip, "message") < 2 {
		t.Fatalf("the copy after recycling %q lacks the text between the ends", g.clip)
	}
}

// An emoji-only message is drawn large with no bubble; a system message has
// none either; the failed one carries a badge and its status in the tree.
func TestMessageBubbleVariants(t *testing.T) {
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		k := g.k
		var st ui.RichStyles
		rexui.BubbleRichStyles(k, rexui.BubbleOutgoing, &st)
		bo := rexui.BubbleOpts{Variant: rexui.BubbleOutgoing, ColumnW: 300}
		rexui.MessageBubble(k, ui.KeyN(k.Context().ParentKey(), 1), []byte("Hello there"), nil, rexui.MessageOpts{BubbleOpts: bo, Styles: &st})
		bo.Status = rexui.BubbleFailed
		bo.Name = "Me, 10:42"
		rexui.MessageBubble(k, ui.KeyN(k.Context().ParentKey(), 2), []byte("Not sent"), nil, rexui.MessageOpts{BubbleOpts: bo, Styles: &st})
	}))
	g.h = 800
	n := g.node(ui.RoleGroup, "Me, 10:42")
	if string(g.sem.Text(n.Value)) != "Not delivered" {
		t.Fatalf("the failed message's value is %q", g.sem.Text(n.Value))
	}
	g.node(ui.RoleLabel, "Not delivered")
	g.node(ui.RoleGroup, "Sent message")
}

// fillRig is one bubble in a column with options o; id is its box.
type fillRig struct {
	*rig
	o  rexui.BubbleOpts
	id ui.ID
}

func newFillRig(t *testing.T) *fillRig {
	f := &fillRig{}
	f.rig = newRig(t, theme.Light, nil, column(func(g *rig) {
		g.k.NextWidth(g.k.Fixed(300))
		g.k.Column("chat")
		b := rexui.BeginBubble(g.k, ui.KeyN(g.c.ParentKey(), 1), f.o)
		rexui.Label(g.k, "Hello there", rexui.LabelOpts{})
		rexui.EndBubble(g.k, &b)
		f.id = b.ID
		g.k.EndColumn()
	}))
	return f
}

// fill is the colours (TL, TR, BL, BR) of the bubble's background quad.
func (f *fillRig) fill() [4]uint32 {
	f.t.Helper()
	f.settle()
	r := f.c.Rect(f.id)
	for _, q := range f.list.Quads {
		if q.Mode == gpu.ModeShape && q.Rect == r && q.Soft == 0 && q.Border == 0 {
			return q.Color
		}
	}
	f.t.Fatalf("no background quad at %v", r)
	return [4]uint32{}
}

// A bubble takes the app's fill, as a colour or a vertical gradient, and
// a highlight mixes into it by the look's FindMix or the call's.
func TestBubbleCustomFill(t *testing.T) {
	f := newFillRig(t)
	r := f.k.Theme()
	if got := f.fill(); got[0] != got[3] || got[0] == 0 {
		t.Fatalf("the incoming fill is %#v", got)
	}

	f.o.Fill = 0x25D366FF
	if got := f.fill(); got != [4]uint32{0x25D366FF, 0x25D366FF, 0x25D366FF, 0x25D366FF} {
		t.Errorf("a custom fill draws %#x", got)
	}
	f.o.FillBottom = 0x128C7EFF
	if got := f.fill(); got != [4]uint32{0x25D366FF, 0x25D366FF, 0x128C7EFF, 0x128C7EFF} {
		t.Errorf("a gradient draws %#x, want the top colour above the bottom one", got)
	}

	f.o.FillBottom = 0
	f.o.Highlighted = true
	find := rexui.BubbleLook.Find.RGBA(r)
	if got, want := f.fill()[0], theme.MixRGBA(0x25D366FF, find, rexui.BubbleLook.FindMix); got != want {
		t.Errorf("highlighted by the look: %#x, want %#x", got, want)
	}
	f.o.FindMix = 0.1
	if got, want := f.fill()[0], theme.MixRGBA(0x25D366FF, find, 0.1); got != want {
		t.Errorf("highlighted by the call's FindMix: %#x, want %#x", got, want)
	}
	if rexui.BubbleLook.FindMix == 0.1 {
		t.Error("the call changed the look")
	}

	// a look of the app's own
	own := rexui.BubbleLook
	own.FindMix = 1
	f.o.FindMix, f.o.Look = 0, &own
	if got := f.fill()[0]; got != find {
		t.Errorf("highlighted by the app's look (FindMix 1): %#x, want the find colour %#x", got, find)
	}
}

// With a mask, a tail bubble shows the curled tail (gpu.PathMask) at its
// bottom corner on the sender's side, in the bubble's colour, outside the
// bubble; the row leaves room for it.
func TestBubbleCurledTail(t *testing.T) {
	var masks [2]gpu.PathMask
	f := newFillRig(t)
	f.o = rexui.BubbleOpts{Variant: rexui.BubbleOutgoing, Tail: true, Curl: &masks[1], Fill: 0x25D366FF, FillBottom: 0x128C7EFF}
	f.settle()
	if f.c.Animating() {
		t.Fatal("the curled tail keeps the window animating")
	}
	b := f.c.Rect(f.id)
	var tail *gpu.Quad
	for i := range f.list.Quads {
		if q := &f.list.Quads[i]; q.Mode == gpu.ModeMaskR8 && q.Rect[0] > b[0] && q.Rect[3] == b[3] {
			tail = q
		}
	}
	if tail == nil {
		t.Fatalf("no tail quad at the bubble %v", b)
	}
	if tail.Rect[2] <= b[2] || tail.Rect[0] >= b[2] {
		t.Errorf("the tail %v does not cross the bubble's right side %v", tail.Rect, b[2])
	}
	if tail.Color[0] != 0x128C7EFF {
		t.Errorf("the tail is %#x, want the bubble's bottom colour", tail.Color[0])
	}

	// incoming: at the bottom left, mirrored
	f.o.Variant, f.o.Curl = rexui.BubbleIncoming, &masks[0]
	f.settle()
	b = f.c.Rect(f.id)
	tail = nil
	for i := range f.list.Quads {
		if q := &f.list.Quads[i]; q.Mode == gpu.ModeMaskR8 && q.Rect[3] == b[3] && q.Rect[0] < b[0] {
			tail = q
		}
	}
	if tail == nil {
		t.Fatalf("no tail quad at the incoming bubble's left %v", b)
	}
	if n := testing.AllocsPerRun(20, f.frame); n != 0 {
		t.Errorf("a frame with curled tails allocates %v times", n)
	}
}

// tailUpload records only widget uploads, so a cached tail can be compared
// with a fresh rasterization after switching sides or display scales.
type tailUpload struct {
	fakeui.Uploader
	pixels  []byte
	uploads int
}

func (u *tailUpload) Upload(_ gpu.TextureID, _, _, _, _ int, pixels []byte, _ int) {
	u.uploads++
	u.pixels = append(u.pixels[:0], pixels...)
}

func TestBubbleTailGeometryCache(t *testing.T) {
	f := newFillRig(t)
	var up, want tailUpload
	f.c.SetWidgetUploader(&up)
	var mask, fresh gpu.PathMask
	var path gpu.Path
	f.o.Tail, f.o.Curl = true, &mask
	for _, v := range []struct {
		scale float32
		side  rexui.BubbleVariant
	}{{2, rexui.BubbleOutgoing}, {2, rexui.BubbleIncoming}, {3, rexui.BubbleOutgoing}, {3, rexui.BubbleIncoming}, {2, rexui.BubbleOutgoing}} {
		f.scale, f.o.Variant = v.scale, v.side
		f.settle()
		rexui.BubbleTailPath(&path, v.scale, v.side == rexui.BubbleIncoming)
		fresh.Mask(&want, &path)
		if !bytes.Equal(up.pixels, want.pixels) {
			t.Fatalf("scale %v side %v: cached tail differs from a fresh path", v.scale, v.side)
		}
		uploads := up.uploads
		f.o.Fill = 0x112233FF
		f.frames(3)
		if up.uploads != uploads {
			t.Fatal("unchanged geometry or a colour change uploaded the tail again")
		}
	}
}
