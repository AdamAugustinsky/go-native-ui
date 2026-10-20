package platform

import (
	"strconv"
	"strings"
	"testing"
)

// axBuild builds AXTrees by hand: nodes are appended under a parent in order.
type axBuild struct {
	t    AXTree
	last map[int32]int32
}

func newAXBuild() *axBuild {
	b := &axBuild{last: map[int32]int32{}}
	b.t.Nodes = append(b.t.Nodes, AXNode{Role: AXRoleWindow, Parent: -1, FirstChild: -1, NextSibling: -1, Bounds: [4]float32{0, 0, 400, 300}})
	return b
}

// add appends a node with key, role and name under parent and returns it.
func (b *axBuild) add(parent int32, key uint64, role AXRole, name string) *AXNode {
	i := int32(len(b.t.Nodes))
	n := AXNode{Key: key, Role: role, Parent: parent, FirstChild: -1, NextSibling: -1, Bounds: [4]float32{0, 0, 10, 10}}
	if name != "" {
		n.Name = [2]uint32{uint32(len(b.t.Bytes)), uint32(len(name))}
		b.t.Bytes = append(b.t.Bytes, name...)
	}
	if l, ok := b.last[parent]; ok {
		b.t.Nodes[l].NextSibling = i
	} else {
		b.t.Nodes[parent].FirstChild = i
	}
	b.last[parent] = i
	b.t.Nodes = append(b.t.Nodes, n)
	return &b.t.Nodes[i]
}

func (b *axBuild) value(n *AXNode, v string) {
	n.Value = [2]uint32{uint32(len(b.t.Bytes)), uint32(len(v))}
	b.t.Bytes = append(b.t.Bytes, v...)
}

// idx is the index of the node with key k.
func (b *axBuild) idx(k uint64) int32 {
	for i := range b.t.Nodes {
		if b.t.Nodes[i].Key == k {
			return int32(i)
		}
	}
	panic("no node " + strconv.FormatUint(k, 10))
}

// exporter hands out *next (copied, as AXExport fills its argument).
type exporter struct{ next *AXTree }

func (e *exporter) export(dst *AXTree) bool {
	dst.Nodes = append(dst.Nodes[:0], e.next.Nodes...)
	dst.Bytes = append(dst.Bytes[:0], e.next.Bytes...)
	dst.Announce = e.next.Announce
	return true
}

// events writes the events of the last sync as "note:key" words.
func events(c *axCore) string {
	var w []string
	for _, e := range c.events {
		name := ""
		for k := 0; k < len(noteNames); k++ {
			if e.note == 1<<k {
				name = noteNames[k]
			}
		}
		key := "window"
		switch {
		case e.slot >= 0 && e.note != noteDestroyed:
			key = strconv.FormatUint(c.slots[e.slot].key, 10)
		case e.note == noteDestroyed:
			key = strconv.FormatUint(e.key, 10)
		}
		w = append(w, name+":"+key)
	}
	return strings.Join(w, " ")
}

// listTree is a window with a list (10) of rows 11.. (keys rows), a label
// (20) with text label, and a button (30).
func listTree(rows []uint64, label string) *axBuild {
	b := newAXBuild()
	l := b.add(0, 10, AXRoleList, "Files")
	_ = l
	for _, r := range rows {
		b.add(1, r, AXRoleListItem, "row "+strconv.FormatUint(r, 10))
	}
	b.add(0, 20, AXRoleLabel, label)
	b.add(0, 30, AXRoleButton, "Send").Actions = AXPress | AXFocus
	return b
}

// The diff tells what changed, element by element: insert, remove, reorder,
// a value, the focus, a selection, a live region, an announcement; an
// unchanged tree tells nothing.
func TestAXDiffNotifications(t *testing.T) {
	var c axCore
	e := &exporter{next: &listTree([]uint64{11, 12, 13}, "3 files").t}
	c.begin(e.export)
	if !c.active || len(c.events) != 0 || len(c.byKey) != 6 {
		t.Fatalf("begin: active %v, %d events, %d elements", c.active, len(c.events), len(c.byKey))
	}
	step := func(name string, b *axBuild, want string) {
		t.Helper()
		e.next = &b.t
		c.sync(e.export)
		if got := events(&c); got != want {
			t.Errorf("%s: events %q, want %q", name, got, want)
		}
	}
	step("unchanged", listTree([]uint64{11, 12, 13}, "3 files"), "")
	step("label text", listTree([]uint64{11, 12, 13}, "4 files"), "value:20 title:20")
	step("insert", listTree([]uint64{11, 12, 14, 13}, "4 files"), "created:14 layout:10")
	step("remove", listTree([]uint64{11, 14, 13}, "4 files"), "layout:10 destroyed:12")
	step("reorder", listTree([]uint64{13, 11, 14}, "4 files"), "layout:10")

	b := listTree([]uint64{13, 11, 14}, "4 files")
	b.t.Nodes[b.idx(30)].State |= AXFocused
	step("focus", b, "focus:30")
	b = listTree([]uint64{13, 11, 14}, "4 files")
	b.t.Nodes[b.idx(11)].State |= AXSelected
	step("select", b, "selected-rows:10 focus:window")
	b = listTree([]uint64{13, 11, 14}, "4 files")
	b.t.Nodes[b.idx(11)].State |= AXSelected
	b.t.Nodes[b.idx(20)].State |= AXLive
	step("live, unchanged", b, "")
	b = listTree([]uint64{13, 11, 14}, "5 files")
	b.t.Nodes[b.idx(11)].State |= AXSelected
	b.t.Nodes[b.idx(20)].State |= AXLive
	step("live, changed", b, "value:20 title:20 live:20")
	b.t.Announce = [2]uint32{uint32(len(b.t.Bytes)), 4}
	b.t.Bytes = append(b.t.Bytes, "Sent"...)
	step("announce", b, "live:window")
	b = newAXBuild()
	b.add(0, 30, AXRoleButton, "Send")
	step("most gone", b, "layout:window destroyed:10 destroyed:11 destroyed:13 destroyed:20 destroyed:14") // (in slot order)
	if len(c.byKey) != 1 || len(c.free) != 6 {
		t.Errorf("%d elements and %d free slots, want 1 and 6", len(c.byKey), len(c.free))
	}
}

// A slot reused by another key gets a new generation: an element of the old
// one is stale.
func TestAXSlotGenerations(t *testing.T) {
	var c axCore
	e := &exporter{next: &listTree([]uint64{11}, "x").t}
	c.begin(e.export)
	s := c.byKey[11]
	gen := c.slots[s].gen
	e.next = &listTree(nil, "x").t
	c.sync(e.export)
	e.next = &listTree([]uint64{99}, "x").t
	c.sync(e.export)
	if s2 := c.byKey[99]; s2 != s || c.slots[s2].gen == gen {
		t.Errorf("key 99 got slot %d gen %d; key 11 had slot %d gen %d", s2, c.slots[s2].gen, s, gen)
	}
}

// Once active, a sync of an unchanged tree allocates nothing, and nor does
// one with a changed value once the event storage has grown.
func TestAXSyncAllocatesNothing(t *testing.T) {
	var c axCore
	a, b := listTree([]uint64{11, 12, 13}, "3 files"), listTree([]uint64{11, 12, 13}, "4 files")
	e := &exporter{next: &a.t}
	c.begin(e.export)
	if n := testing.AllocsPerRun(100, func() { c.sync(e.export) }); n != 0 {
		t.Errorf("unchanged: %v allocations per sync", n)
	}
	flip := false
	if n := testing.AllocsPerRun(100, func() {
		flip = !flip
		if flip {
			e.next = &b.t
		} else {
			e.next = &a.t
		}
		c.sync(e.export)
	}); n != 0 {
		t.Errorf("changing: %v allocations per sync", n)
	}
}

// A modal overlay makes the content beneath it inaccessible: the window's
// children start at it, and a hit test cannot reach what it covers.
func TestAXModalHidesContent(t *testing.T) {
	b := newAXBuild()
	g := b.add(0, 1, AXRoleGroup, "")
	g.Bounds = [4]float32{0, 0, 400, 300}
	b.add(1, 2, AXRoleButton, "Behind").Bounds = [4]float32{10, 10, 100, 40}
	d := b.add(0, 3, AXRoleDialog, "Delete?")
	d.State |= AXModal
	d.Bounds = [4]float32{100, 100, 300, 200}
	b.add(3, 4, AXRoleButton, "Cancel").Bounds = [4]float32{110, 150, 190, 180}
	kids := axChildren(&b.t, 0, nil)
	if len(kids) != 1 || b.t.Nodes[kids[0]].Key != 3 {
		t.Fatalf("window children %v, want only the dialog", kids)
	}
	if h := axHit(&b.t, 20, 20); h != 0 {
		t.Errorf("hit beneath the dialog found node %d, want the window", h)
	}
	if h := axHit(&b.t, 120, 160); b.t.Nodes[h].Key != 4 {
		t.Errorf("hit in the dialog found key %d, want 4", b.t.Nodes[h].Key)
	}
	if axExposed(&b.t, b.idx(2)) || !axExposed(&b.t, b.idx(4)) {
		t.Error("exposure: the button behind the dialog is exposed, or the dialog's is not")
	}
	got := AXDump(&b.t)
	want := "AXWindow\n  AXGroup/AXApplicationDialog \"Delete?\"\n    AXButton \"Cancel\"\n"
	if got != want {
		t.Errorf("dump:\n%s\nwant:\n%s", got, want)
	}
}

// The roles map to macOS's, with values, states, levels and actions.
func TestAXDumpMapsRoles(t *testing.T) {
	b := newAXBuild()
	sw := b.add(0, 1, AXRoleSwitch, "Wi-Fi")
	sw.State, sw.Actions = AXChecked, AXPress|AXFocus
	s := b.add(0, 2, AXRoleSlider, "Volume")
	s.Range, s.Actions = [4]float64{0, 10, 4, 1}, AXIncrement|AXDecrement|AXSetValue
	f := b.add(0, 3, AXRoleSearchField, "Search")
	b.value(f, "héllo")
	f.Text, f.TextSel = AXTextField, [2]int32{1, 3}
	ol := b.add(0, 4, AXRoleList, "Files")
	r := b.add(b.idx(4), 5, AXRoleListItem, "src")
	r.Level, r.State, r.Pos, r.Actions = 1, AXExpanded|AXSelected, [2]int32{0, 2}, AXPress|AXExpand|AXCollapse
	b.add(b.idx(4), 6, AXRoleListItem, "main.go").Level = 2
	_ = ol
	h := b.add(0, 7, AXRoleHeading, "Settings")
	h.Level = 1
	b.add(0, 8, AXRoleHost, "").Host = 0x101
	b.add(0, 9, AXRoleMenuBar, "")
	b.add(b.idx(9), 10, AXRoleMenuItem, "File")
	want := `AXWindow
  AXCheckBox/AXSwitch "Wi-Fi" value=1 {Press}
  AXSlider "Volume" value=4 {Increment,Decrement}
  AXTextField/AXSearchField "Search" value="héllo" sel=1+1
  AXOutline "Files"
    AXRow/AXOutlineRow "src" [selected expanded level=1 index=0/2] {Press,Disclose}
    AXRow/AXOutlineRow "main.go" [level=2]
  AXHeading value="Settings" [level=1]
  Host #257
  AXMenuBar
    AXMenuBarItem "File"
`
	if got := AXDump(&b.t); got != want {
		t.Errorf("dump:\n%s\nwant:\n%s", got, want)
	}
}
