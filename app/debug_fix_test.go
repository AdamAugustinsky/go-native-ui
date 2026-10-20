package app

import (
	"image"
	"strings"
	"testing"

	"gonativeui/platform"
	"gonativeui/ui"
)

// The longest flag combination (every flag) is formatted into a line of the
// card without allocating and without being cut.
func TestFlagFormattingBoundedAndAllocationFree(t *testing.T) {
	all := ^ui.Flags(0)
	names := ui.DebugFlagNames(nil, all)
	if len(names) > ui.DebugFlagNamesMax {
		t.Fatalf("every flag takes %d bytes, DebugFlagNamesMax is %d", len(names), ui.DebugFlagNamesMax)
	}
	var l dbgLine
	if n := testing.AllocsPerRun(100, func() { l = dbgLine{}; flagsLine(&l, all) }); n != 0 {
		t.Errorf("formatting every flag allocates %.1f times", n)
	}
	if !strings.HasPrefix(string(l.b[:l.n]), "click hover focus") {
		t.Errorf("line %q", l.b[:l.n])
	}
	var s InspectSnapshot
	s.Inside, s.NUnder = true, 1
	s.Under[0].Flags = all
	var ls dbgLines
	if n := testing.AllocsPerRun(100, func() { ls.reset(); cardLines(&s, &ls) }); n != 0 {
		t.Errorf("the card of a box with every flag allocates %.1f times", n)
	}
}

func wake() platform.Event { return platform.Event{Kind: platform.EvWake} }

// Each event's invalidation is recorded on its own: a wake that arrives while a
// frame is already owed, and several wakes in one batch, keep their causes.
func TestCoalescedInvalidationsKeepTheirCauses(t *testing.T) {
	o := newStepper(t, &scene{})
	// a batch of input and two wakes
	if !o.Step(0, move(100, 80), wake(), wake()) {
		t.Fatal("no frame")
	}
	cs := causesOf(t, o)
	if c, ok := has(cs, CauseWake); !ok || c.N != 2 {
		t.Errorf("causes %+v, want a wake x2", cs)
	}
	if _, ok := has(cs, CauseInput); !ok {
		t.Errorf("the input of the batch is lost: %+v", cs)
	}
	// the hover frame is owed; a wake that arrives now is a cause too
	o.p.needDraw = true // owed already, as after the frame above
	if !o.Step(0, wake()) {
		t.Fatal("no frame")
	}
	if _, ok := has(causesOf(t, o), CauseWake); !ok {
		t.Errorf("a wake that arrived with a frame already owed lost its cause: %+v", causesOf(t, o))
	}
	settleSteps(t, o)
}

func TestAppearanceThenTaskWakeAreClassified(t *testing.T) {
	o := newStepper(t, &scene{})
	o.dbg.serial-- // the appearance changed since the overlay looked
	// both in one batch: the first wake carries the change, the second is a task's
	if !o.Step(0, wake(), wake()) {
		t.Fatal("no frame")
	}
	cs := causesOf(t, o)
	if c, ok := has(cs, CauseAppearance); !ok || c.N != 1 {
		t.Errorf("causes %+v, want one appearance", cs)
	}
	if c, ok := has(cs, CauseWake); !ok || c.N != 1 {
		t.Errorf("causes %+v, want one task wake", cs)
	}
	if o.dbg.serial != platform.AppearanceSerial() {
		t.Error("the appearance serial is stale after an appearance wake")
	}
	// owed already (the frame is not drawn yet), then a task wake after an
	// appearance wake: still a task wake
	settleSteps(t, o)
	o.dbg.serial--
	o.Step(0, wake())
	o.p.needDraw = true
	if !o.Step(0, wake()) {
		t.Fatal("no frame")
	}
	if _, ok := has(causesOf(t, o), CauseWake); !ok {
		t.Errorf("a task wake after an appearance wake was classified wrongly: %+v", causesOf(t, o))
	}
}

// A direct Offscreen.Frame closes its debug record when it has rendered, once:
// the record is there at once, and Step does not close it again.
func TestOffscreenFrameClosesItsRecord(t *testing.T) {
	o := newOverlayRig(t, &scene{}, 200, 120, 1)
	o.EnableDebug(true)
	for i := 1; i <= 3; i++ {
		o.Frame()
		if o.DebugFrames() != uint64(i) {
			t.Fatalf("%d frames recorded after %d", o.DebugFrames(), i)
		}
		r, ok := o.DebugFrame(0)
		if !ok || r.N != uint64(i) {
			t.Fatalf("after frame %d the last record is %d (ok %v)", i, r.N, ok)
		}
		if r.CPUns[PhaseEncode] == 0 && r.TotalCPUns() == 0 {
			t.Errorf("frame %d has no timing", i)
		}
	}
	if o.dbg.open {
		t.Error("a record is still open")
	}
	// through Step it closes exactly once too
	n := o.DebugFrames()
	o.Step(0)
	if got := o.DebugFrames(); got != n+1 {
		t.Errorf("a Step added %d records", got-n)
	}
}

// Turning the overlay on, or its panel, or the audit, repaints an idle app
// once, with a cause of its own; turning it off clears it. Nothing keeps
// animating.
func TestDebugTogglesRepaintAnIdleApp(t *testing.T) {
	o := newOverlayRig(t, &scene{}, 400, 300, 1)
	settleSteps(t, o)
	base := clone(o.img)
	if o.Step(stepNs) {
		t.Fatal("an idle app drew")
	}
	o.EnableDebug(true)
	if !o.Step(0) {
		t.Fatal("turning the overlay on drew nothing")
	}
	mustHave(t, o, CauseDebug)
	if n := diffIn(o.img, base, image.Rect(8, 190, 330, 292)); n < 500 {
		t.Errorf("the HUD is not there after the repaint (%d pixels)", n)
	}
	if o.Step(stepNs) {
		t.Error("the overlay keeps drawing frames in an idle app")
	}
	withHUD := clone(o.img)
	o.SetDebugAudit(func(dst []DebugFinding) []DebugFinding {
		return append(dst, DebugFinding{Rect: [4]float32{300, 20, 390, 90}, Reason: "x"})
	})
	o.DebugRunAudit()
	if !o.Step(0) {
		t.Fatal("running the audit drew nothing")
	}
	mustHave(t, o, CauseDebug)
	if c := pix(o.img, 345, 20); !near(c, 0xFF2D2DFF, 24) {
		t.Errorf("the finding is not outlined: %08x", c)
	}
	o.SetInspectPanel(true)
	if !o.Step(0) {
		t.Fatal("the panel drew nothing")
	}
	if devBuild {
		if n := diffIn(o.img, withHUD, image.Rect(200, 0, 400, 150)); n < 300 {
			t.Errorf("the panel is not there (%d pixels)", n)
		}
	}
	o.EnableDebug(false)
	if !o.Step(0) {
		t.Fatal("turning the overlay off drew nothing")
	}
	if n := diffIn(o.img, base, o.img.Rect); n != 0 {
		t.Errorf("%d pixels of the overlay are left after turning it off", n)
	}
	if o.Step(stepNs) {
		t.Error("frames after turning it off")
	}
}

// The default audit is the real one: a box that overflows its parent comes back
// as a red finding with the audit's reason, and the overlay's own boxes are not
// audited.
type overflowing struct{ scene }

func (b *overflowing) Build(c *ui.Context) {
	c.NextSize(ui.Px(100), ui.Px(100))
	c.Open(0, "parent")
	c.NextSize(ui.Px(160), ui.Px(30))
	c.NextBG(green)
	c.Box(ui.DrawBackground, "wide child")
	c.Close()
}

func TestDefaultAuditIsTheLayoutAudit(t *testing.T) {
	o := newStepper(t, &overflowing{})
	o.DebugRunAudit()
	if !o.Step(0) {
		t.Fatal("no frame")
	}
	s := &o.dbg.snap
	if s.NFindings != 1 {
		var rs []string
		for _, f := range s.Findings[:s.NFindings] {
			rs = append(rs, f.Reason)
		}
		t.Fatalf("%d findings, want the overflow only: %v", s.NFindings, rs)
	}
	f := s.Findings[0]
	if !strings.Contains(f.Reason, "overflow") || f.Rect[2]-f.Rect[0] != 160 {
		t.Errorf("finding %+v", f)
	}
	if c := pix(o.img, 80, 0); !near(c, 0xFF2D2DFF, 40) && !near(pix(o.img, 80, 1), 0xFF2D2DFF, 40) {
		t.Errorf("not outlined in red: %08x", c)
	}
	// with the overlay on, a second run finds the same, not the HUD's boxes
	o.DebugRunAudit()
	o.Step(0)
	if o.dbg.snap.NFindings != 1 {
		t.Errorf("%d findings on the second run", o.dbg.snap.NFindings)
	}
}
