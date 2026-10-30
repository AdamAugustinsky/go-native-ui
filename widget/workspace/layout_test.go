package workspace

import (
	"encoding/json"
	"errors"
	"testing"

	"gonativeui/gpu"
	"gonativeui/internal/fakeui"
	"gonativeui/theme"
	"gonativeui/ui"
)

var testMetrics = Metrics{Scale: 1, Gutter: [2]float32{10, 10}, Min: [2]float32{100, 50}}

// three is a beside (b over c), the shape of Rex's first tab: a left
// column and a stacked right one.
func three() *Layout {
	var l Layout
	l.Reset(1)
	l.Split(1, 2, Right, 0.5)
	l.Split(2, 3, Down, 0.5)
	l.Arrange([4]float32{0, 0, 410, 210}, testMetrics)
	return &l
}

func TestSplitPlacesPanes(t *testing.T) {
	l := three()
	if l.Len() != 3 || l.Pane(0) != 1 || l.Pane(1) != 2 || l.Pane(2) != 3 || l.NumDividers() != 2 {
		t.Fatalf("panes %d (%d %d %d), dividers %d", l.Len(), l.Pane(0), l.Pane(1), l.Pane(2), l.NumDividers())
	}
	for id, want := range map[PaneID][4]float32{1: {0, 0, 200, 210}, 2: {210, 0, 410, 100}, 3: {210, 110, 410, 210}} {
		if got := l.Rect(id); got != want {
			t.Errorf("pane %d at %v, want %v", id, got, want)
		}
	}
	// A pane put before another reads first, and takes the share asked.
	l.Split(1, 4, Left, 0.25)
	l.Arrange([4]float32{0, 0, 410, 210}, testMetrics)
	if l.Pane(0) != 4 || l.Rect(4)[2] != 100 {
		t.Errorf("a pane put left: order %d…, at %v", l.Pane(0), l.Rect(4))
	}
	if l.Split(9, 5, Right, 0) != 0 || l.Split(1, 2, Right, 0) != 0 || l.Split(1, 0, Right, 0) != 0 {
		t.Error("a split of an unknown pane, or of a pane already in, or of no pane, took")
	}
	for id := PaneID(10); l.Len() < MaxPanes; id++ {
		if l.Split(1, id, Down, 0) == 0 {
			t.Fatalf("split %d refused at %d panes", id, l.Len())
		}
	}
	if l.Split(1, 99, Right, 0) != 0 {
		t.Error("a full layout took one more pane")
	}
}

// Slots stay with their panes while others come and go.
func TestSlotsAreStable(t *testing.T) {
	l := three()
	s3 := l.Slot(3)
	l.Close(2)
	l.Split(1, 7, Down, 0)
	if l.Slot(3) != s3 || l.Slot(2) != -1 || l.Slot(7) < 0 {
		t.Errorf("slot of 3: %d, was %d; of 2: %d; of 7: %d", l.Slot(3), s3, l.Slot(2), l.Slot(7))
	}
}

func TestLimitsAndFixedSizes(t *testing.T) {
	var l Layout
	l.Reset(1)
	l.Split(1, 2, Right, 0.5)
	l.SetLimits(1, Limits{Min: [2]float32{150, 0}, Max: [2]float32{180, 0}})
	l.Arrange([4]float32{0, 0, 1010, 100}, testMetrics)
	if w := l.Rect(1)[2]; w != 180 {
		t.Errorf("pane 1 is %v wide, its greatest is 180", w)
	}
	// A list kept at 120 pt (Zap's chat list) keeps it as the window grows,
	// and a drag changes it.
	l.SetLimits(1, Limits{})
	l.SetFixed(1, 120)
	m := testMetrics
	m.Scale = 2
	l.Arrange([4]float32{0, 0, 1010, 100}, m)
	l.Arrange([4]float32{0, 0, 2010, 100}, m)
	if w := l.Rect(1)[2]; w != 240 || l.Fixed(1) != 120 || l.Fixed(2) != 0 {
		t.Errorf("fixed pane %v px wide at scale 2, fixed %v", w, l.Fixed(1))
	}
	d := l.Divider(0)
	l.Press(d.ID)
	l.Drag(d.ID, 60)
	l.Arrange([4]float32{0, 0, 2010, 100}, m)
	if l.Fixed(1) != 150 || l.Rect(1)[2] != 300 {
		t.Errorf("after a drag of 60 px: fixed %v pt, %v px", l.Fixed(1), l.Rect(1)[2])
	}
	l.Drag(d.ID, -5000)
	l.Arrange([4]float32{0, 0, 2010, 100}, m)
	if l.Rect(1)[2] != 100 {
		t.Errorf("dragged past its least size: %v px", l.Rect(1)[2])
	}
}

func TestFocusNavigation(t *testing.T) {
	l := three()
	if l.Focus() != 1 {
		t.Fatalf("focus %d, want the first pane", l.Focus())
	}
	// Right from 1 is 2 or 3, equally near: 3 was used last.
	l.SetFocus(3)
	l.SetFocus(1)
	if !l.FocusDir(Right) || l.Focus() != 3 {
		t.Errorf("right from 1 went to %d, want 3 (used last)", l.Focus())
	}
	if !l.FocusDir(Up) || l.Focus() != 2 {
		t.Errorf("up from 3 went to %d", l.Focus())
	}
	if l.FocusDir(Up) || l.FocusDir(Right) || l.Focus() != 2 {
		t.Errorf("past the edge the focus moved to %d", l.Focus())
	}
	if !l.FocusDir(Left) || l.Focus() != 1 {
		t.Errorf("left from 2 went to %d", l.Focus())
	}
	// Cycling wraps both ways in reading order.
	l.FocusNext(-1)
	if l.Focus() != 3 {
		t.Errorf("before the first is %d, want the last", l.Focus())
	}
	l.FocusNext(1)
	if l.Focus() != 1 {
		t.Errorf("after the last is %d, want the first", l.Focus())
	}
	// Moving the focus restores a maximised layout.
	l.SetZoom(2)
	if l.Zoomed() != 2 || l.Focus() != 2 {
		t.Fatalf("zoom %d, focus %d", l.Zoomed(), l.Focus())
	}
	l.FocusNext(1)
	if l.Zoomed() != 0 {
		t.Error("the focus moved and the layout stayed maximised")
	}
}

func TestCloseRestoresFocus(t *testing.T) {
	l := three()
	l.SetFocus(3)
	l.SetFocus(2)
	l.SetZoom(2)
	if !l.Close(2) {
		t.Fatal("close refused")
	}
	if l.Focus() != 3 || l.Zoomed() != 0 {
		t.Errorf("after closing the focused, maximised pane: focus %d, zoom %d; want 3, the pane used before", l.Focus(), l.Zoomed())
	}
	l.Arrange([4]float32{0, 0, 410, 210}, testMetrics)
	if r := l.Rect(3); r != [4]float32{210, 0, 410, 210} {
		t.Errorf("the neighbour did not take the room: %v", r)
	}
	// With no pane used before, the focus goes to the neighbour.
	var m Layout
	m.Reset(1)
	m.Split(1, 2, Right, 0.5)
	m.Split(2, 3, Down, 0.5)
	if !m.Close(1) || m.Focus() != 2 {
		t.Errorf("focus %d after closing the only pane used, want 2", m.Focus())
	}
	m.Close(3)
	if m.Close(2) || m.Len() != 1 {
		t.Error("the last pane closed")
	}
}

func TestJSONRoundTrip(t *testing.T) {
	l := three()
	l.SetFixed(1, 160)
	l.SetFocus(3)
	l.SetZoom(3)
	l.SetLimits(2, Limits{Min: [2]float32{0, 70}})
	l.Split(3, 4, Down, 0)
	l.SetHidden(4, true)
	data, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	var r Layout
	r.Reset(2)
	r.SetLimits(2, Limits{Min: [2]float32{0, 70}})
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	l.Arrange([4]float32{0, 0, 810, 410}, testMetrics)
	r.Arrange([4]float32{0, 0, 810, 410}, testMetrics)
	for i := 0; i < l.Len(); i++ {
		id := l.Pane(i)
		if r.Pane(i) != id || r.Rect(id) != l.Rect(id) {
			t.Errorf("pane %d: %d at %v, saved %d at %v", i, r.Pane(i), r.Rect(id), id, l.Rect(id))
		}
	}
	if r.Focus() != 3 || r.Zoomed() != 3 || r.Fixed(1) != 160 || !r.Hidden(4) {
		t.Errorf("focus %d, zoom %d, fixed %v", r.Focus(), r.Zoomed(), r.Fixed(1))
	}
	// (a fixed split's share follows its fixed size once arranged)
	first, _ := json.Marshal(l)
	again, _ := json.Marshal(&r)
	if string(again) != string(first) {
		t.Errorf("saved again:\n%s\nfirst:\n%s", again, first)
	}
}

// A saved layout may name panes the app no longer has, repeat one, carry
// fields of a later minor change, or lack panes the app has now.
func TestRestoreToleratesUnknownPanes(t *testing.T) {
	data := []byte(`{"version":1,"focus":9,"future":true,"root":{"axis":"x","share":0.3,"a":{"pane":1},
		"b":{"axis":"y","share":7,"colour":"red","a":{"pane":9},"b":{"axis":"x","a":{"pane":2},"b":{"pane":1}}}}}`)
	var l Layout
	l.Reset(5)
	err := l.Restore(data, func(id PaneID) bool { return id != 9 })
	if err != nil {
		t.Fatal(err)
	}
	if l.Len() != 2 || l.Pane(0) != 1 || l.Pane(1) != 2 || l.Has(5) || l.Has(9) {
		t.Fatalf("restored %d panes: %d %d", l.Len(), l.Pane(0), l.Pane(1))
	}
	if l.Focus() != 1 {
		t.Errorf("the saved focus is gone: focus %d, want the first pane", l.Focus())
	}
	l.Arrange([4]float32{0, 0, 1010, 100}, testMetrics)
	if l.Rect(1)[2] != 300 {
		t.Errorf("pane 1 at %v, saved at a share of 0.3", l.Rect(1))
	}
	// The app adds the pane the file lacks.
	if l.Split(2, 5, Right, 0) == 0 || l.Len() != 3 {
		t.Error("could not add a missing pane")
	}

	for _, bad := range []struct {
		data string
		err  error
	}{
		{`{"version":2,"root":{"pane":1}}`, ErrVersion},
		{`{"version":1,"root":{"pane":9}}`, ErrEmpty},
		{`{"version":1}`, ErrEmpty},
	} {
		before := l
		if err := l.Restore([]byte(bad.data), func(id PaneID) bool { return id != 9 }); !errors.Is(err, bad.err) {
			t.Errorf("%s: %v, want %v", bad.data, err, bad.err)
		}
		if l != before {
			t.Errorf("%s changed the layout", bad.data)
		}
	}
	if err := l.Restore([]byte(`{"version":`), nil); err == nil {
		t.Error("malformed JSON restored")
	}
}

// frameRig builds a Layout's panes and dividers as plain boxes, each frame.
type frameRig struct {
	t    *testing.T
	c    *ui.Context
	in   ui.Input
	list gpu.DrawList
	sem  ui.SemTree
	l    Layout
	ev   Events
}

func newFrameRig(t *testing.T) *frameRig {
	g := &frameRig{t: t, c: ui.New(fakeui.New())}
	g.c.Reserve(1024)
	g.in.Inside, g.in.Focused = true, true
	g.list.Quads = make([]gpu.Quad, 0, 1024)
	g.sem.Reserve(1024, 1<<14)
	g.l.Key = ui.HashString(0, "test/workspace")
	g.l.Reset(1)
	g.l.Split(1, 2, Right, 0.5)
	g.l.Split(2, 3, Down, 0.5)
	g.frame()
	g.frame()
	return g
}

var paneNames = [...]string{1: "Shell", 2: "Git", 3: "Server"}

func (g *frameRig) frame() {
	c := g.c
	c.Begin(&g.in, 410, 210, 1, 1.0/60)
	g.ev = g.l.Update(c, Keys{Dir: ui.ModSuper | ui.ModAlt, Cycle: true})
	g.l.Arrange([4]float32{0, 0, 410, 210}, testMetrics)
	for i := 0; i < g.l.Len(); i++ {
		id := g.l.Pane(i)
		r := g.l.Rect(id)
		g.l.NextPane(c, id, paneNames[id])
		theme.At(c, r[0], r[1], r[2]-r[0], r[3]-r[1])
		c.BoxKeyed(ui.Clickable|theme.Floating, g.l.PaneKey(id), nil)
	}
	for i := 0; i < g.l.NumDividers(); i++ {
		d := g.l.Divider(i)
		g.l.NextDivider(c, &d)
		gt := d.Gutter
		theme.At(c, gt[0], gt[1], gt[2]-gt[0], gt[3]-gt[1])
		c.BoxKeyed(ui.Clickable|theme.Floating, d.Key, nil)
	}
	c.End(&g.list)
	g.in.ClearFrame()
}

func (g *frameRig) key(code ui.KeyCode, mods ui.Mods) {
	g.in.PushKey(ui.KeyEvent{Code: code, Mods: mods, Down: true})
	g.frame()
}

func (g *frameRig) node(role ui.Role, name string) (ui.SemNode, bool) {
	g.c.Semantics(&g.sem)
	for _, n := range g.sem.Nodes {
		if n.Role == role && string(g.sem.Text(n.Name)) == name {
			return n, true
		}
	}
	return ui.SemNode{}, false
}

// The frame: a press focuses a pane, the keys move the focus and say so to a
// screen reader, and a focused divider moves with the arrows; nothing
// allocates.
func TestUpdateAndSemantics(t *testing.T) {
	g := newFrameRig(t)
	g.in.MouseX, g.in.MouseY = 300, 150
	g.frame()
	g.in.SetButton(ui.ButtonLeft, true, 1)
	g.frame()
	g.in.SetButton(ui.ButtonLeft, false, 0)
	g.frame()
	if g.l.Focus() != 3 {
		t.Fatalf("a press in pane 3 focused %d", g.l.Focus())
	}
	if n, ok := g.node(ui.RoleGroup, "Server"); !ok || n.State&ui.SemSelected == 0 {
		t.Errorf("the focused pane's group: %+v (found %v)", n, ok)
	}
	if n, _ := g.node(ui.RoleGroup, "Shell"); n.State&ui.SemSelected != 0 {
		t.Error("an unfocused pane is selected")
	}

	g.key(ui.KeyLeft, ui.ModSuper|ui.ModAlt)
	if !g.ev.Focus || g.l.Focus() != 1 {
		t.Fatalf("⌘⌥← from 3 focused %d (event %v)", g.l.Focus(), g.ev.Focus)
	}
	g.c.Semantics(&g.sem)
	if a := string(g.sem.Text(g.sem.Announce)); a != "Shell" {
		t.Errorf("announced %q, want the pane's name", a)
	}
	g.key(ui.KeyF6, ui.ModShift)
	if g.l.Focus() != 3 {
		t.Errorf("⇧F6 from 1 focused %d, want the last", g.l.Focus())
	}

	d := g.l.Divider(0)
	if n, ok := g.node(ui.RoleSplitter, "Divider"); !ok || n.Range[2] != 0.5 {
		t.Errorf("the divider: %+v (found %v)", n, ok)
	}
	g.c.SetFocus(d.Key)
	g.key(ui.KeyRight, 0)
	if !g.ev.Moved || g.l.Divider(0).Share <= d.Share {
		t.Errorf("→ on the focused divider: share %v, was %v", g.l.Divider(0).Share, d.Share)
	}
	g.frames(2)
	if n := testing.AllocsPerRun(50, g.frame); n != 0 {
		t.Errorf("a frame allocates %v times", n)
	}
}

func (g *frameRig) frames(n int) {
	for i := 0; i < n; i++ {
		g.frame()
	}
}

// Zap's main window (docs/gnui-panes-inventory.md in Zap): the list, kept
// between 260 and 520 pt; the conversation, at least 320; a side pane that
// shows only with 240 pt beside the conversation, at the width it was left.
const (
	zList PaneID = 1 + iota
	zChat
	zSide
)

func zapLayout() *Layout {
	var l Layout
	l.Reset(zList)
	l.Split(zList, zChat, Right, 0.5)
	l.Split(zChat, zSide, Right, 0.3)
	l.SetLimits(zList, Limits{Min: [2]float32{260}, Max: [2]float32{520}})
	l.SetFixed(zList, 300)
	l.SetLimits(zChat, Limits{Min: [2]float32{320}})
	l.SetLimits(zSide, Limits{Min: [2]float32{240}, Max: [2]float32{640}, Yield: true})
	l.SetFixed(zSide, 380)
	return &l
}

var zapMetrics = Metrics{Scale: 1, Gutter: [2]float32{10, 10}, Min: [2]float32{100, 100}}

func TestZapRoomRules(t *testing.T) {
	l := zapLayout()
	arrange := func(w float32) { l.Arrange([4]float32{0, 0, w, 600}, zapMetrics) }
	arrange(1400)
	if l.Rect(zList)[2] != 300 || l.Rect(zSide)[0] != 1400-380 || l.NumDividers() != 2 {
		t.Errorf("wide: list %v, side %v, %d dividers", l.Rect(zList), l.Rect(zSide), l.NumDividers())
	}
	// The side pane takes only the room the conversation leaves it, and none
	// below its 240.
	arrange(1000) // 1000 − 300 − 10 − 10 = 680 for the two: 320 for the chat, 360 for the side
	if c, s := l.Rect(zChat), l.Rect(zSide); c[2]-c[0] != 320 || s[2]-s[0] != 360 {
		t.Errorf("1000 wide: chat %v, side %v", c, s)
	}
	l.SetFocus(zSide)
	arrange(860) // 540 for the two: too little
	if l.Shown(zSide) || l.Rect(zChat)[2] != 860 || l.NumDividers() != 1 {
		t.Errorf("860 wide: side shown %v, chat %v, %d dividers", l.Shown(zSide), l.Rect(zChat), l.NumDividers())
	}
	if l.Focus() == zSide {
		t.Error("the focus stayed on a side pane with no room")
	}
	arrange(1400)
	if !l.Shown(zSide) || l.Fixed(zSide) != 380 {
		t.Errorf("widened again: side shown %v at %v pt", l.Shown(zSide), l.Fixed(zSide))
	}
	// The list's drag stops at 520.
	d := l.Divider(0)
	l.Press(d.ID)
	l.Drag(d.ID, 400)
	arrange(1400)
	if l.Fixed(zList) != 520 {
		t.Errorf("list dragged to %v pt, its greatest is 520", l.Fixed(zList))
	}
	// ⌘B: the list hides, and the conversation starts at the left.
	l.SetHidden(zList, true)
	arrange(1400)
	if l.Shown(zList) || l.Rect(zChat)[0] != 0 || l.Fixed(zList) != 520 {
		t.Errorf("list hidden: shown %v, chat %v, list kept at %v", l.Shown(zList), l.Rect(zChat), l.Fixed(zList))
	}
	l.SetHidden(zList, false)
	arrange(1400)
	if l.Rect(zList)[2] != 520 {
		t.Errorf("list shown again at %v", l.Rect(zList))
	}
}

func TestZapFocusRules(t *testing.T) {
	l := zapLayout()
	arrange := func() { l.Arrange([4]float32{0, 0, 1400, 600}, zapMetrics) }
	l.SetHidden(zSide, true) // no side pane open
	l.SetEmpty(zChat, true)  // no chat open
	arrange()
	if l.SetFocus(zChat) || l.SetFocus(zSide) || l.Focus() != zList {
		t.Fatalf("an empty or hidden pane took the focus: %d", l.Focus())
	}
	if l.FocusNext(1) {
		t.Error("F6 moved to an empty pane")
	}
	// A chat opens: the conversation is in use.
	l.SetEmpty(zChat, false)
	l.SetFocus(zChat)
	// The side pane opens, and is used; it closes, and the chat is in use again.
	l.SetHidden(zSide, false)
	arrange()
	l.SetFocus(zSide)
	l.SetHidden(zSide, true)
	if l.Focus() != zChat {
		t.Errorf("the side pane closed: focus %d, want the chat", l.Focus())
	}
	// The chat closes: the pane stays, empty, and the list is in use.
	l.SetEmpty(zChat, true)
	arrange()
	if l.Focus() != zList || !l.Shown(zChat) {
		t.Errorf("the chat closed: focus %d, chat shown %v", l.Focus(), l.Shown(zChat))
	}
	// The list hides while it has the focus and no chat is open: the focus
	// stays, there is nowhere to go.
	l.SetHidden(zList, true)
	if l.Focus() != zList {
		t.Errorf("focus %d with nowhere to go", l.Focus())
	}
	l.SetHidden(zList, false)
}

// A narrow window shows one pane at a time: the list with no chat open, the
// chat, or a side pane over the whole window.
func TestSoloPane(t *testing.T) {
	l := zapLayout()
	work := [4]float32{52, 0, 700, 600} // beside the rail, which is the app's
	l.SetSolo(zList)
	l.Arrange(work, zapMetrics)
	if l.Rect(zList) != work || l.Shown(zChat) || l.Shown(zSide) || l.NumDividers() != 0 {
		t.Errorf("solo list: %v, chat shown %v", l.Rect(zList), l.Shown(zChat))
	}
	l.SetSolo(zSide)
	l.Arrange(work, zapMetrics)
	if l.Rect(zSide) != work || l.Focus() != zSide || l.FocusNext(1) || l.SetFocus(zList) {
		t.Errorf("solo side: %v, focus %d", l.Rect(zSide), l.Focus())
	}
	l.SetSolo(0)
	l.Arrange([4]float32{0, 0, 1400, 600}, zapMetrics)
	if !l.Shown(zList) || !l.Shown(zChat) || !l.Shown(zSide) || l.Fixed(zList) != 300 {
		t.Error("the panes did not come back as they were")
	}
}
