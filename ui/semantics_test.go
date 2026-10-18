package ui

import (
	"testing"
)

// find returns the node with the given name.
func (t *SemTree) find(name string) int {
	for i := range t.Nodes {
		if string(t.Text(t.Nodes[i].Name)) == name {
			return i
		}
	}
	return -1
}

func (t *SemTree) children(i int) []int {
	var out []int
	for c := t.Nodes[i].FirstChild; c >= 0; c = t.Nodes[c].NextSibling {
		out = append(out, int(c))
	}
	return out
}

func TestSemanticsTree(t *testing.T) {
	h := newHarness(t, 300, 200)
	var tree SemTree
	h.frame(func(c *Context) {
		c.NextSize(Px(300), Px(100))
		c.NextRole(RoleGroup)
		c.NextSemName("toolbar")
		bar := c.Open(0, "bar")
		c.NextSize(Px(100), Px(100)) // a box without a role between
		plain := c.Open(0, "plain")
		c.NextSize(Px(40), Px(20))
		c.NextRole(RoleButton)
		c.NextSemName("Open")
		c.Box(Clickable|Focusable, "open")
		c.NextSize(Px(40), Px(20))
		c.NextRole(RoleButton)
		c.NextSemName(c.Fmt().Str("Save ").Int(2).String())
		c.NextSemState(SemChecked)
		c.Box(Clickable|Focusable, "save")
		c.Close()
		_ = plain
		c.NextSize(Px(40), Px(20))
		c.NextRole(RoleLabel)
		c.NextSemNameBytes([]byte("status"))
		c.NextSemValueBytes([]byte("ready"))
		c.Box(0, "status")
		c.Close()
		_ = bar
		c.NextSize(Px(40), Px(20))
		c.Box(0, "no role") // not in the tree
	})
	h.c.Semantics(&tree)
	if len(tree.Nodes) != 5 { // the window, toolbar, Open, Save 2, status
		t.Fatalf("%d nodes", len(tree.Nodes))
	}
	win := tree.Nodes[0]
	if win.Role != RoleWindow || win.Parent != -1 || win.Bounds != [4]float32{0, 0, 300, 200} {
		t.Fatalf("window node %+v", win)
	}
	bar := tree.find("toolbar")
	open, save, status := tree.find("Open"), tree.find("Save 2"), tree.find("status")
	if bar < 0 || open < 0 || save < 0 || status < 0 {
		t.Fatalf("missing nodes: %d %d %d %d", bar, open, save, status)
	}
	// The unnamed box between the toolbar and the buttons is skipped: the parent is
	// the nearest ancestor that has a role.
	if int(tree.Nodes[open].Parent) != bar || int(tree.Nodes[save].Parent) != bar || int(tree.Nodes[status].Parent) != bar {
		t.Fatalf("parents: open %d save %d status %d, want %d", tree.Nodes[open].Parent, tree.Nodes[save].Parent, tree.Nodes[status].Parent, bar)
	}
	if kids := tree.children(bar); len(kids) != 3 || kids[0] != open || kids[1] != save || kids[2] != status {
		t.Fatalf("children of the toolbar %v", kids)
	}
	if kids := tree.children(0); len(kids) != 1 || kids[0] != bar {
		t.Fatalf("children of the window %v", kids)
	}
	if tree.Nodes[open].FirstChild != -1 || tree.Nodes[status].NextSibling != -1 {
		t.Error("leaf links")
	}
	if tree.Nodes[save].State&SemChecked == 0 || tree.Nodes[open].State&SemChecked != 0 {
		t.Error("state bits")
	}
	if string(tree.Text(tree.Nodes[status].Value)) != "ready" {
		t.Errorf("value %q", tree.Text(tree.Nodes[status].Value))
	}
	if tree.Nodes[open].Actions != ActPress|ActFocus || tree.Nodes[status].Actions != 0 {
		t.Errorf("default actions: button %b label %b", tree.Nodes[open].Actions, tree.Nodes[status].Actions)
	}
	if tree.Nodes[open].Bounds != [4]float32{0, 0, 40, 20} || tree.Nodes[save].Bounds != [4]float32{0, 20, 40, 40} {
		t.Errorf("bounds %v %v", tree.Nodes[open].Bounds, tree.Nodes[save].Bounds)
	}
	// The snapshot replaces the previous one.
	h.frame(func(c *Context) {
		c.NextRole(RoleButton)
		c.NextSemName("only")
		c.Box(0, "b")
	})
	h.c.Semantics(&tree)
	if len(tree.Nodes) != 2 || tree.find("only") != 1 || tree.find("Open") >= 0 {
		t.Fatalf("second export: %d nodes", len(tree.Nodes))
	}
}

func TestSemanticsStatesFromTheContext(t *testing.T) {
	h := newHarness(t, 300, 200)
	var tree SemTree
	build := func(c *Context) {
		for _, n := range []string{"a", "b"} {
			c.NextSize(Px(40), Px(20))
			c.NextRole(RoleButton)
			c.NextSemName(n)
			fl := Clickable | Focusable
			if n == "b" {
				fl |= Disabled
			}
			c.Box(fl, n)
		}
		c.NextRole(RoleDialog)
		c.NextSemName("dialog")
		c.BeginOverlay(1, OverlayModal, [4]float32{0, 0, 300, 200}, PlaceCenter)
		c.NextSize(Px(100), Px(50))
		c.NextRole(RoleButton)
		c.NextSemName("ok")
		c.Box(Clickable|Focusable, "ok")
		c.EndOverlay()
	}
	h.frame(build)
	h.frame(build)
	h.c.Semantics(&tree)
	a, b, dlg, ok := tree.find("a"), tree.find("b"), tree.find("dialog"), tree.find("ok")
	if tree.Nodes[b].State&SemDisabled == 0 || tree.Nodes[b].Actions != 0 || tree.Nodes[a].State&SemDisabled != 0 {
		t.Errorf("disabled: a %b b %b actions %b", tree.Nodes[a].State, tree.Nodes[b].State, tree.Nodes[b].Actions)
	}
	if tree.Nodes[dlg].State&SemModal == 0 || tree.Nodes[dlg].Parent != 0 {
		t.Errorf("modal overlay node: state %b parent %d (the window is 0)", tree.Nodes[dlg].State, tree.Nodes[dlg].Parent)
	}
	if int(tree.Nodes[ok].Parent) != dlg {
		t.Errorf("a box in the overlay hangs from the overlay: parent %d, want %d", tree.Nodes[ok].Parent, dlg)
	}
	// The modal opened with the focus inside it: it is the focused node.
	if tree.Nodes[dlg].State&SemFocused == 0 && tree.Nodes[ok].State&SemFocused == 0 {
		t.Error("no focused node")
	}
	if tree.Nodes[a].State&SemFocused != 0 {
		t.Error("a is focused beneath the modal")
	}
}

func TestSemanticsVirtualRowsKnowTheirPlace(t *testing.T) {
	h := newHarness(t, 200, 100)
	var tree SemTree
	const rows = 1000
	build := func(c *Context) {
		c.NextSize(Px(200), Px(100))
		c.NextRole(RoleList)
		c.NextSemName("files")
		c.NextPad(0)
		list := c.Open(ScrollY|Clip, "list")
		first, last := c.ListRange(list, rows, 20, 0)
		for r := first; r < last; r++ {
			c.NextSize(Pct(1), Px(20))
			c.NextRole(RoleListItem)
			c.NextSemName(c.Fmt().Str("row ").Int(r).String())
			if r == first+1 { // an explicit position wins
				c.NextSemPos(7, 8)
			}
			c.BoxKeyed(0, KeyN(c.ParentKey(), uint64(r)), nil)
		}
		c.Close()
	}
	h.frame(build)
	h.frame(build)
	h.c.SetScroll(h.c.Root()+1, 0, 20*500) // the list is the root's first child
	h.frame(build)
	h.frame(build)
	h.c.Semantics(&tree)
	// The window, the list, then only the visible rows.
	if len(tree.Nodes) > 2+7 || len(tree.Nodes) < 2+4 {
		t.Fatalf("%d nodes: only the built rows are exported", len(tree.Nodes))
	}
	checked := 0
	for i := 2; i < len(tree.Nodes); i++ {
		n := tree.Nodes[i]
		name := string(tree.Text(n.Name))
		var row int
		for _, d := range name[4:] {
			row = row*10 + int(d-'0')
		}
		want := [2]int32{int32(row), rows}
		if i == 3 {
			want = [2]int32{7, 8}
		}
		if n.Pos != want {
			t.Errorf("%s: position %v, want %v", name, n.Pos, want)
		}
		if row < 490 {
			t.Errorf("%s exported although the list is scrolled to row 500", name)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no rows")
	}
	if tree.Nodes[1].Role != RoleList || tree.Nodes[1].Actions&ActScrollIntoView == 0 {
		t.Errorf("list node %+v", tree.Nodes[1])
	}
}

func TestSemanticsEditableAndRangeControlsReportTheirValues(t *testing.T) {
	r := newRig(t, true)
	var tree SemTree
	r.buf.SetString("héllo")
	r.buf.SetSelection(1, 4)
	var slider ID
	build := func(c *Context) {
		c.NextSize(Px(200), Px(24))
		c.NextSemName("path")
		r.res = c.TextField("f", &r.buf)
		c.NextSize(Px(100), Px(10))
		c.NextRole(RoleSlider)
		c.NextSemName("volume")
		c.NextSemRange(0, 100, 35, 5)
		slider = c.Box(Focusable|Clickable, "vol")
		c.NextSize(Px(100), Px(10))
		c.NextRole(RoleProgressBar)
		c.NextSemRange(0, 1, 0.25, 0)
		c.Box(0, "progress")
	}
	r.harness.frame(build)
	_ = slider
	r.c.Semantics(&tree)
	f := tree.find("path")
	if f < 0 {
		t.Fatal("text field node missing")
	}
	n := tree.Nodes[f]
	if n.Role != RoleTextField || string(tree.Text(n.Value)) != "héllo" || n.TextSel != [2]int32{1, 4} {
		t.Fatalf("field node: role %d value %q sel %v", n.Role, tree.Text(n.Value), n.TextSel)
	}
	if n.State&SemEditable == 0 || n.State&SemFocused == 0 {
		t.Errorf("field state %b: editable and focused expected", n.State)
	}
	if n.Actions&ActSetValue == 0 || n.Actions&ActFocus == 0 {
		t.Errorf("field actions %b", n.Actions)
	}
	// Typing is reflected in the next export.
	r.in.AppendText("X")
	r.harness.frame(build)
	r.c.Semantics(&tree)
	f = tree.find("path")
	if v := string(tree.Text(tree.Nodes[f].Value)); v != "hXlo" { // bytes 1..4 ("él") were selected and replaced
		t.Fatalf("value after typing: %q", v)
	}
	sl := tree.find("volume")
	if rg := tree.Nodes[sl].Range; rg != [4]float64{0, 100, 35, 5} || tree.Nodes[sl].Role != RoleSlider {
		t.Errorf("slider range %v", rg)
	}
	if tree.Nodes[sl].Actions&(ActIncrement|ActDecrement|ActSetValue) != ActIncrement|ActDecrement|ActSetValue {
		t.Errorf("slider actions %b", tree.Nodes[sl].Actions)
	}
	pb := len(tree.Nodes) - 1
	if tree.Nodes[pb].Role != RoleProgressBar || tree.Nodes[pb].Range != [4]float64{0, 1, 0.25, 0} {
		t.Errorf("progress node %+v", tree.Nodes[pb])
	}
	// A long value is cut at a character boundary.
	long := make([]byte, 0, 5000)
	for len(long) < 4999 {
		long = append(long, "é"...)
	}
	r.buf.SetBytes(long)
	r.harness.frame(build)
	r.c.Semantics(&tree)
	f = tree.find("path")
	if v := tree.Text(tree.Nodes[f].Value); len(v) > maxSemValue || len(v)%2 != 0 {
		t.Errorf("long value: %d bytes", len(v))
	}
}

func TestSemanticActionsReachTheBox(t *testing.T) {
	h := newHarness(t, 300, 200)
	var sigs [3]Signal
	var keys [3]Key
	var scrollBox ID
	build := func(c *Context) {
		for i, n := range []string{"one", "two", "three"} {
			c.NextSize(Px(60), Px(20))
			c.NextRole(RoleButton)
			c.NextSemName(n)
			fl := Clickable | Focusable
			if i == 2 {
				fl |= Disabled
			}
			id := c.Box(fl, n)
			keys[i] = c.KeyOf(id)
			sigs[i] = c.Signal(id)
		}
		c.NextSize(Px(100), Px(60))
		c.NextRole(RoleScrollArea)
		c.NextPad(0)
		scrollBox = c.Open(ScrollY|Clip, "scroll")
		for i := 0; i < 20; i++ {
			c.NextSize(Pct(1), Px(20))
			c.NextRole(RoleListItem)
			c.BoxKeyed(Focusable, KeyN(c.ParentKey(), uint64(i)), nil)
		}
		c.Close()
	}
	h.frame(build)
	h.frame(build)
	h.in.PushAction(SemRequest{Key: keys[1], Action: ActPress})
	h.frame(build)
	if !sigs[1].Activated || sigs[1].Action != ActPress || sigs[0].Activated || sigs[0].Action != 0 {
		t.Fatalf("press on two: %+v / one: %+v", sigs[1], sigs[0])
	}
	h.frame(build)
	if sigs[1].Activated || sigs[1].Action != 0 {
		t.Fatal("the request was delivered again")
	}
	// A disabled box ignores requests.
	h.in.PushAction(SemRequest{Key: keys[2], Action: ActPress})
	h.frame(build)
	if sigs[2] != (Signal{}) {
		t.Fatalf("disabled: %+v", sigs[2])
	}
	// A value request carries its argument.
	h.in.PushAction(SemRequest{Key: keys[0], Action: ActSetValue, Value: 42.5})
	h.frame(build)
	if sigs[0].Action != ActSetValue || sigs[0].ActionValue != 42.5 || sigs[0].Activated {
		t.Fatalf("set value: %+v", sigs[0])
	}
	// Focus requests are done by ui.
	h.in.PushAction(SemRequest{Key: keys[1], Action: ActFocus})
	h.frame(build)
	if h.c.FocusKey() != keys[1] || !h.c.FocusVisible() {
		t.Fatalf("ActFocus: focus %v visible %v", h.c.FocusKey(), h.c.FocusVisible())
	}
	h.in.PushAction(SemRequest{Key: keys[2], Action: ActFocus}) // disabled
	h.frame(build)
	if h.c.FocusKey() != keys[1] {
		t.Fatal("a disabled box was focused")
	}
	// Scroll into view: the 15th item is far below the 60 px view.
	var target Key
	h.frame(func(c *Context) {
		build(c)
		target = KeyN(c.KeyOf(scrollBox), 14)
	})
	h.in.PushAction(SemRequest{Key: target, Action: ActScrollIntoView})
	h.frame(build)
	for i := 0; i < 30 && h.c.Animating(); i++ {
		h.frame(build)
	}
	_, y := h.c.Scroll(scrollBox)
	if y != 15*20-60 { // the smallest move that shows the item at the bottom of the view
		t.Fatalf("scrolled to %v, want %v", y, 15*20-60)
	}
}

func TestSemanticsAllocatesNothing(t *testing.T) {
	h := newHarness(t, 300, 200)
	var tree SemTree
	tree.Reserve(64, 4096)
	var buf TextBuffer
	buf.SetString("some text")
	build := func(c *Context) {
		for i := 0; i < 10; i++ {
			c.NextSize(Px(60), Px(20))
			c.NextRole(RoleButton)
			c.NextSemName("button")
			c.NextSemValueBytes(c.Fmt().Int(i).Bytes())
			c.NextSemRange(0, 10, float64(i), 1)
			c.NextSemPos(i, 10)
			c.Box(Clickable|Focusable, c.Fmt().Str("b").Int(i).String())
		}
		c.NextSize(Px(100), Px(20))
		c.TextField("f", &buf)
	}
	frame := func() {
		h.in.PushAction(SemRequest{Key: 5, Action: ActPress})
		h.frame(build)
		h.c.Semantics(&tree)
	}
	for i := 0; i < 5; i++ {
		frame()
	}
	if n := testing.AllocsPerRun(50, frame); n != 0 {
		t.Fatalf("%v allocations per frame with semantics", n)
	}
	if len(tree.Nodes) != 12 {
		t.Fatalf("%d nodes", len(tree.Nodes))
	}
}

func TestSemanticsCaps(t *testing.T) {
	h := newHarness(t, 100, 100)
	var tree SemTree
	h.c.Reserve(MaxSemNodes + 100)
	h.frame(func(c *Context) {
		for i := 0; i < MaxSemNodes+10; i++ {
			c.NextRole(RoleLabel)
			c.NextSemName("n")
			c.BoxKeyed(0, KeyN(c.ParentKey(), uint64(i)), nil)
		}
	})
	h.c.Semantics(&tree)
	if len(tree.Nodes) != MaxSemNodes || tree.Dropped != 11 { // 10 over, plus the window itself counts as one node
		t.Fatalf("%d nodes, %d dropped", len(tree.Nodes), tree.Dropped)
	}
	if tree.Nodes[MaxSemNodes-1].Parent != 0 {
		t.Error("the nodes that fit are linked")
	}
}
