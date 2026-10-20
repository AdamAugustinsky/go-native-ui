package ui

import (
	"runtime"
	"strings"
	"testing"
)

// dbgScene builds a clipping box at (20,20) 100×100 with a wide child, a box
// with a role and name, and a popup.
func dbgScene(h *harness, popup, dup bool) func(c *Context) {
	return func(c *Context) {
		c.NextPos(20, 20)
		c.NextSize(Px(100), Px(100))
		cl := c.Open(FloatingX|FloatingY|Clip, "clipper")
		_ = cl
		c.NextPos(10, 10)
		c.NextSize(Px(300), Px(30))
		c.NextRole(RoleButton)
		c.NextSemName("Wide")
		c.Box(FloatingX|FloatingY|Hoverable, "wide")
		c.Close()
		if dup {
			c.Box(0, "same")
			c.Box(0, "same")
		}
		if popup {
			c.BeginOverlay(HashString(0, "pop"), OverlayPopup, [4]float32{200, 200, 260, 220}, PlaceBelow)
			c.NextSize(Px(60), Px(40))
			c.Box(Hoverable, "item")
			c.EndOverlay()
		}
	}
}

func TestDebugUnderAndBoxRecord(t *testing.T) {
	h := newHarness(t, 400, 300)
	h.c.EnableDebug(true, false)
	build := dbgScene(h, true, false)
	for i := 0; i < 3; i++ {
		h.frame(build)
	}
	var ids [8]ID
	n := h.c.DebugUnder(50, 40, ids[:])
	if n != 3 { // root, clipper, wide
		t.Fatalf("%d boxes under (50, 40), want 3", n)
	}
	var b DebugBox
	h.c.DebugBoxOf(ids[n-1], &b)
	if b.Role != RoleButton || string(b.Name) != "Wide" || b.Flags&Hoverable == 0 {
		t.Errorf("innermost %+v", b)
	}
	if b.Clip != [4]float32{20, 20, 120, 120} || b.Rect != [4]float32{30, 30, 330, 60} {
		t.Errorf("rect %v clip %v", b.Rect, b.Clip)
	}
	if DebugRoleName(b.Role) != "button" {
		t.Errorf("role name %q", DebugRoleName(b.Role))
	}
	if got := string(DebugFlagNames(nil, b.Flags)); got != "hover flx fly" {
		t.Errorf("flags %q", got)
	}
	// the clip hides the wide box outside the clipper
	if n := h.c.DebugUnder(200, 40, ids[:]); n != 1 {
		t.Errorf("%d boxes under a point the clipper hides, want only the root", n)
	}
	// a popup is under the pointer after the main tree
	if n := h.c.DebugUnder(210, 230, ids[:]); n < 2 {
		t.Errorf("%d boxes under the popup", n)
	} else if h.c.boxes[ids[n-1]].ovl == 0 {
		t.Error("the innermost box is not in the popup")
	}
	if !h.c.DebugBoxByKey(b.Key, &b) || b.Role != RoleButton {
		t.Error("DebugBoxByKey did not find the box")
	}
	if h.c.DebugBoxByKey(12345, &b) {
		t.Error("DebugBoxByKey found a box that is not there")
	}
}

// The debug overlay's boxes are skipped by DebugUnder and are not duplicates,
// whatever keys they have.
func TestDebugOverlayIsInvisibleToTheDebugReads(t *testing.T) {
	h := newHarness(t, 400, 300)
	h.c.EnableDebug(true, false)
	build := func(c *Context) {
		dbgScene(h, false, false)(c)
		c.BeginDebugOverlay()
		for i := 0; i < 3; i++ { // one key three times
			c.NextPos(0, 0)
			c.NextSize(Px(400), Px(300))
			c.Box(FloatingX|FloatingY|Hoverable, "everywhere")
		}
		c.EndDebugOverlay()
	}
	for i := 0; i < 3; i++ {
		h.frame(build)
	}
	var ids [8]ID
	n := h.c.DebugUnder(50, 40, ids[:])
	for _, id := range ids[:n] {
		if h.c.boxes[id].ovl != 0 {
			t.Error("a box of the debug overlay is under the pointer")
		}
	}
	if d := h.c.DebugDups(); len(d) != 0 {
		t.Errorf("the overlay's boxes are reported as duplicates: %+v", d)
	}
}

func TestDebugDupsAreRecordedOnlyWhenOn(t *testing.T) {
	h := newHarness(t, 400, 300)
	build := dbgScene(h, false, true)
	h.frame(build)
	if len(h.c.DebugDups()) != 0 {
		t.Fatal("recorded with the debug off")
	}
	h.c.EnableDebug(true, false)
	h.frame(build)
	h.frame(build)
	d := h.c.DebugDups()
	if len(d) != 1 || d[0].Count != 1 || d[0].Total != 2 || d[0].Key == 0 || d[0].Parent == 0 {
		t.Fatalf("duplicates %+v", d)
	}
	for i := 0; i < maxDebugDups+4; i++ {
		h.frame(func(c *Context) {
			for k := 0; k < maxDebugDups+4; k++ {
				c.BoxKeyed(0, KeyN(77, uint64(k)), nil)
				c.BoxKeyed(0, KeyN(77, uint64(k)), nil)
			}
		})
	}
	if len(h.c.DebugDups()) != maxDebugDups || h.c.DebugDupsLost() == 0 {
		t.Errorf("%d kept, %d lost", len(h.c.DebugDups()), h.c.DebugDupsLost())
	}
	h.c.EnableDebug(false, false)
	if h.c.DebugOn() || h.c.DebugDups() != nil {
		t.Error("still on")
	}
}

func TestDebugAnimatingNamesTheValueAndWakeTheSource(t *testing.T) {
	h := newHarness(t, 400, 300)
	h.c.EnableDebug(true, true)
	k := HashString(0, "value")
	target := float32(1)
	dl := int64(0)
	build := func(c *Context) {
		c.AnimateValue(k, target, 10)
		if dl != 0 {
			c.Deadline(HashString(0, "dl"), dl)
		}
	}
	h.frame(build)
	var keys [4]Key
	if n := h.c.DebugAnimating(keys[:]); n != 0 {
		t.Fatalf("%d animating at rest", n)
	}
	target = 0
	h.frame(build)
	if n := h.c.DebugAnimating(keys[:]); n != 1 || keys[0] != k {
		t.Errorf("animating %d %x, want the value %x", n, uint64(keys[0]), uint64(k))
	}
	for i := 0; i < 600; i++ {
		h.frame(build)
	}
	if n := h.c.DebugAnimating(keys[:]); n != 0 || h.c.Animating() {
		t.Errorf("still animating after settling: %d", n)
	}

	// what asked for a wake
	h.in.Time = 1e9
	h.frame(func(c *Context) { c.WakeIn(5e6) })
	if kind, _ := h.c.DebugWake(); kind != DebugWakeIn {
		t.Errorf("wake %v, want WakeIn", kind)
	}
	if pcs := h.c.DebugCallers(DebugCallerWakeIn); len(pcs) == 0 {
		t.Error("the caller of WakeIn is not recorded")
	}
	dl = 1e9 + 20e6
	h.frame(build)
	if kind, key := h.c.DebugWake(); kind != DebugWakeDeadline || key != HashString(0, "dl") {
		t.Errorf("wake %v %x, want the deadline", kind, uint64(key))
	}
	h.frame(func(c *Context) { c.RequestFrame() })
	if pcs := h.c.DebugCallers(DebugCallerRequestFrame); len(pcs) == 0 {
		t.Error("the caller of RequestFrame is not recorded")
	} else if !strings.Contains(callerName(pcs[0]), "TestDebugAnimating") {
		t.Errorf("the caller is %s", callerName(pcs[0]))
	}
	h.frame(nil)
	if h.c.DebugCallers(DebugCallerRequestFrame) != nil {
		t.Error("a caller from an earlier frame is reported")
	}
}

// With the recording off, RequestFrame and the boxes cost no allocation, and
// with it on a frame still allocates nothing.
func TestDebugRecordingAllocatesNothing(t *testing.T) {
	for _, on := range []bool{false, true} {
		h := newHarness(t, 400, 300)
		h.c.EnableDebug(on, on)
		build := func(c *Context) {
			dbgScene(h, true, true)(c)
			c.RequestFrame()
			c.WakeIn(1e6)
		}
		for i := 0; i < 5; i++ {
			h.frame(build)
		}
		var ids [8]ID
		if n := testing.AllocsPerRun(50, func() {
			h.frame(build)
			h.c.DebugUnder(50, 40, ids[:])
		}); n != 0 {
			t.Errorf("debug %v: %.1f allocations per frame", on, n)
		}
	}
}

func callerName(pc uintptr) string { return runtimeFuncName(pc - 1) }

func runtimeFuncName(pc uintptr) string {
	if f := runtime.FuncForPC(pc); f != nil {
		return f.Name()
	}
	return "?"
}
