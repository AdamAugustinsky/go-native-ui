package ui

import "testing"

// Hosted views take their place among their siblings in the tree, as nodes
// of RoleHost with their host's id; the frame's announcement is exported for
// that frame only.
func TestSemanticsHostsAndAnnouncements(t *testing.T) {
	h := newHarness(t, 300, 200)
	var tree SemTree
	build := func(announce bool) func(c *Context) {
		return func(c *Context) {
			c.NextRole(RoleGroup)
			c.NextSemName("composer")
			c.Open(0, "g")
			c.NextSize(Px(40), Px(20))
			c.NextRole(RoleButton)
			c.NextSemName("Attach")
			c.Box(Clickable, "a")
			c.NextSize(Px(100), Px(20))
			c.Host(7, 99, HostFocusable)
			c.NextSize(Px(40), Px(20))
			c.NextRole(RoleButton)
			c.NextSemName("Send")
			c.Box(Clickable, "s")
			c.Close()
			if announce {
				c.Announce("Message sent")
			}
		}
	}
	h.frame(build(true))
	h.c.Semantics(&tree)
	g := tree.find("composer")
	kids := tree.children(g)
	if len(kids) != 3 {
		t.Fatalf("the group has %d children, want 3", len(kids))
	}
	host := tree.Nodes[kids[1]]
	if host.Role != RoleHost || host.Host != 7 || host.Bounds[2]-host.Bounds[0] != 100 ||
		string(tree.Text(tree.Nodes[kids[0]].Name)) != "Attach" || string(tree.Text(tree.Nodes[kids[2]].Name)) != "Send" {
		t.Fatalf("children %+v / %+v", host, kids)
	}
	if got := string(tree.Text(tree.Announce)); got != "Message sent" {
		t.Errorf("announcement %q", got)
	}
	h.frame(build(false))
	h.c.Semantics(&tree)
	if tree.Announce != [2]uint32{} {
		t.Errorf("the next frame announces %q again", tree.Text(tree.Announce))
	}
	if n := testing.AllocsPerRun(20, func() { h.c.Semantics(&tree) }); n != 0 {
		t.Errorf("the export with hosts allocates %v times", n)
	}
}

// A text field's value and a paragraph's body have geometry: lines, ranges,
// rectangles and offsets, by the node's key.
func TestSemanticsTextGeometry(t *testing.T) {
	r := newRig(t, true)
	r.buf.SetString("hello")
	r.buf.SetSelection(1, 3)
	r.harness.frame(func(c *Context) {
		c.NextSize(Px(200), Px(24))
		c.NextSemName("to")
		r.res = c.TextField("f", &r.buf)
	})
	var tree SemTree
	r.c.Semantics(&tree)
	n := tree.Nodes[tree.find("to")]
	if n.Text != SemTextField {
		t.Fatalf("the field's text kind is %d", n.Text)
	}
	if l, ok := r.c.SemTextLine(n.Key, 4); !ok || l != 0 {
		t.Errorf("line of 4: %d %v", l, ok)
	}
	if a, b, ok := r.c.SemTextLineRange(n.Key, 0); !ok || a != 0 || b != 5 {
		t.Errorf("line 0: %d..%d %v", a, b, ok)
	}
	if _, _, ok := r.c.SemTextLineRange(n.Key, 1); ok {
		t.Error("a field has a second line")
	}
	b1, ok1 := r.c.SemTextBounds(n.Key, 1, 3)
	b2, _ := r.c.SemTextBounds(n.Key, 0, 5)
	fr := r.c.Rect(r.res.ID)
	if !ok1 || b1[2] <= b1[0] || b1[0] <= b2[0] || b1[2] >= b2[2] || b2[0] < fr[0] || b2[3] > fr[3] || b1[3] <= b1[1] {
		t.Errorf("bounds of 1..3 %v, of 0..5 %v, in the field %v", b1, b2, fr)
	}
	if off, ok := r.c.SemTextOffset(n.Key, b1[0]+0.5, b1[1]+1); !ok || off != 1 {
		t.Errorf("offset at the left of 1..3: %d %v", off, ok)
	}

	p := linkHarness(t)
	var pt SemTree
	p.c.Semantics(&pt)
	pn := pt.Nodes[pt.find("go to example now")]
	if pn.Text != SemTextRich {
		t.Fatalf("the paragraph's text kind is %d", pn.Text)
	}
	if b, ok := p.c.SemTextBounds(pn.Key, 6, 13); !ok || b != [4]float32{48, 0, 104, 14} {
		t.Errorf("paragraph bounds of 6..13: %v", b)
	}
	if off, ok := p.c.SemTextOffset(pn.Key, 50, 7); !ok || off != 6 {
		t.Errorf("paragraph offset at 50: %d", off)
	}
	if a, b, ok := p.c.SemTextLineRange(pn.Key, 0); !ok || a != 0 || b != 17 {
		t.Errorf("paragraph line 0: %d..%d", a, b)
	}
	if _, ok := p.c.SemTextLine(Key(12345), 0); ok {
		t.Error("an unknown key has text")
	}
}

// A paragraph in a selection scope reports its part of the selection.
func TestSemanticsRichSelection(t *testing.T) {
	r := newScopeRig(t, newSelModel(3, words(3)), false, 0, 3)
	r.press(r.at(1, 0, 2))
	r.dragTo(r.at(1, 0, 9))
	r.release()
	var tree SemTree
	r.c.Semantics(&tree)
	key := r.c.boxes[r.boxes[[2]uint64{1, 0}]].key
	for _, n := range tree.Nodes {
		if n.Key == key {
			if n.Text != SemTextRich || n.TextSel != [2]int32{2, 9} {
				t.Fatalf("selected paragraph: text %d sel %v", n.Text, n.TextSel)
			}
			return
		}
	}
	t.Fatal("the paragraph is not in the tree")
}
