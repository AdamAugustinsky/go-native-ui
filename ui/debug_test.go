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

//go:noinline
func wakeLong(c *Context) { c.WakeIn(1e9) }

//go:noinline
func wakeShort(c *Context) { c.WakeIn(10e6) }

// The earliest wake names who asked for it, when it was asked: a short WakeIn
// after a long one keeps the short one's caller; a Deadline that is later than
// the WakeIn is not blamed for it; and a focused text field does not take the
// blame for a WakeIn that is shorter than its caret blink.
func TestDebugWakeProvenanceIsRecordedWhenTheWinnerIsChosen(t *testing.T) {
	h := newHarness(t, 400, 300)
	h.c.EnableDebug(true, true)
	h.in.Time = 1e9
	h.frame(func(c *Context) { wakeLong(c); wakeShort(c) })
	kind, _ := h.c.DebugWake()
	pcs := h.c.DebugCallers(DebugCallerWakeIn)
	if kind != DebugWakeIn || len(pcs) < 2 || !strings.Contains(callerName(pcs[1]), "TestDebugWakeProvenance") {
		t.Fatalf("kind %v, callers %v", kind, pcs)
	}
	// the winner's own function is the first frame's caller: wakeShort called WakeIn
	if n := runtime.FuncForPC(pcs[0] - 1).Name(); !strings.HasSuffix(n, "wakeShort") {
		t.Errorf("the first caller is %s, want wakeShort", n)
	}
	h.frame(func(c *Context) { wakeShort(c); wakeLong(c) })
	if pcs := h.c.DebugCallers(DebugCallerWakeIn); len(pcs) == 0 || !strings.HasSuffix(runtime.FuncForPC(pcs[0]-1).Name(), "wakeShort") {
		t.Error("a later, longer WakeIn replaced the caller of the winner")
	}
	// a Deadline that is later than the WakeIn
	dk := HashString(0, "late")
	h.frame(func(c *Context) { c.Deadline(dk, 1e9+500e6); c.WakeIn(10e6) })
	if kind, key := h.c.DebugWake(); kind != DebugWakeIn || key != 0 {
		t.Errorf("a later deadline was blamed: %v %x", kind, uint64(key))
	}
	// and one that is earlier wins, and a WakeIn is no longer the caller
	h.frame(func(c *Context) { c.WakeIn(500e6); c.Deadline(dk, 1e9+20e6) })
	if kind, key := h.c.DebugWake(); kind != DebugWakeDeadline || key != dk || h.c.DebugCallers(DebugCallerWakeIn) != nil {
		t.Errorf("the earlier deadline lost: %v %x", kind, uint64(key))
	}
}

func TestDebugWakeIsNotTheCaretWhenAWakeInIsShorter(t *testing.T) {
	r := newRig(t, true)
	r.c.EnableDebug(true, true)
	field := func(wake func(*Context)) func(c *Context) {
		return func(c *Context) {
			c.NextSize(Px(200), Px(24))
			r.res = c.TextField("f", &r.buf)
			wake(c)
		}
	}
	r.now += 16e6
	r.in.Time = r.now
	r.harness.frame(field(func(c *Context) {}))
	if kind, key := r.c.DebugWake(); kind != DebugWakeField || key == 0 || key != r.c.focusKey {
		t.Fatalf("the focused field's blink is %v %x", kind, uint64(key))
	}
	r.now += 16e6
	r.in.Time = r.now
	r.harness.frame(field(func(c *Context) { c.WakeIn(1e6) }))
	if kind, _ := r.c.DebugWake(); kind != DebugWakeIn {
		t.Errorf("a 1 ms WakeIn with a focused field is reported as %v", kind)
	}
	if len(r.c.DebugCallers(DebugCallerWakeIn)) == 0 {
		t.Error("its caller is not recorded")
	}
}

// The count of collisions beyond the capacity is per frame: it falls to 0 once
// the duplicates are gone, instead of growing for ever.
func TestDebugDupsLostCountsPerFrame(t *testing.T) {
	h := newHarness(t, 400, 300)
	h.c.EnableDebug(true, false)
	many := func(c *Context) {
		for k := 0; k < maxDebugDups+4; k++ {
			c.BoxKeyed(0, KeyN(77, uint64(k)), nil)
			c.BoxKeyed(0, KeyN(77, uint64(k)), nil)
		}
	}
	for i := 0; i < 5; i++ {
		h.frame(many)
	}
	if got := h.c.DebugDupsLost(); got != 4 {
		t.Errorf("%d collisions lost in the last frame after 5 frames, want 4", got)
	}
	h.frame(nil)
	if got := h.c.DebugDupsLost(); got != 0 {
		t.Errorf("%d lost in a frame with no duplicates", got)
	}
}

func TestDebugUnderWithNoRoomReturnsZero(t *testing.T) {
	h := newHarness(t, 400, 300)
	h.c.EnableDebug(true, false)
	h.frame(dbgScene(h, false, false))
	if n := h.c.DebugUnder(50, 40, nil); n != 0 {
		t.Errorf("%d for a nil destination", n)
	}
	if n := h.c.DebugUnder(50, 40, make([]ID, 0)); n != 0 {
		t.Errorf("%d for an empty destination", n)
	}
}

// The room DebugFlagNames needs for every flag is within DebugFlagNamesMax, so
// callers that size their buffer by it never grow it.
func TestDebugFlagNamesMaxCoversEveryFlag(t *testing.T) {
	if n := len(DebugFlagNames(nil, ^Flags(0))); n > DebugFlagNamesMax {
		t.Errorf("every flag needs %d bytes, DebugFlagNamesMax is %d", n, DebugFlagNamesMax)
	}
}

func TestDebugAuditSkipsTheOverlay(t *testing.T) {
	h := newHarness(t, 400, 300)
	h.c.EnableDebug(true, false)
	build := func(c *Context) {
		dbgScene(h, false, false)(c)
		c.BeginDebugOverlay()
		c.NextPos(0, 0)
		c.NextSize(Px(500), Px(20)) // wider than its parent, the window
		c.Box(FloatingX|FloatingY|DrawBackground, "wide")
		c.EndDebugOverlay()
	}
	for i := 0; i < 3; i++ {
		h.frame(build)
	}
	n := len(h.c.order)
	if got := h.c.DebugAudit(nil); len(got) != 0 {
		t.Errorf("the overlay's boxes were audited: %v", got)
	}
	if len(h.c.order) != n {
		t.Error("the frame's order was left cut")
	}
}
