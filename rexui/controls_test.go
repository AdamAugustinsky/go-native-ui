package rexui_test

import (
	"testing"

	"gonativeui/gpu"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
)

// pressAt moves the pointer to (x, y) and presses the left button, a frame
// each; the frame after the press is the first of the hold.
func pressAt(g *rig, x, y float32) {
	g.in.MouseX, g.in.MouseY = x, y
	g.frame()
	g.in.SetButton(ui.ButtonLeft, true, 1)
	g.frame()
}

func releaseButton(g *rig) {
	g.in.SetButton(ui.ButtonLeft, false, 0)
	g.frame()
}

// upper and lower are points in the middle of a stepper's two halves.
func halves(n ui.SemNode) (ux, uy, dx, dy float32) {
	x := (n.Bounds[0] + n.Bounds[2]) / 2
	h := n.Bounds[3] - n.Bounds[1]
	return x, n.Bounds[1] + h/4, x, n.Bounds[1] + 3*h/4
}

func TestStepperClicksKeysAndLimits(t *testing.T) {
	v, wrapped := float32(5), float32(10)
	var changed int
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		if rexui.Stepper(g.k, "Count", &v, 0, 10, 1, rexui.StepperOpts{}).Changed {
			changed++
		}
		rexui.Stepper(g.k, "Wrapping", &wrapped, 0, 10, 1, rexui.StepperOpts{Wrap: true})
	}))
	n := g.node(ui.RoleSlider, "Count")
	if r := n.Range; r[0] != 0 || r[1] != 10 || r[2] != 5 || r[3] != 1 {
		t.Fatalf("the stepper reports range %v", r)
	}
	ux, uy, dx, dy := halves(n)
	g.click(ux, uy)
	if v != 6 || changed != 1 {
		t.Fatalf("a click on the upper half: value %v after %d changes, want 6 after one", v, changed)
	}
	g.click(dx, dy)
	g.click(dx, dy)
	if v != 4 {
		t.Fatalf("two clicks on the lower half: %v, want 4", v)
	}
	// with the focus the arrow keys step
	g.key(ui.KeyUp, 0)
	g.key(ui.KeyUp, 0)
	g.key(ui.KeyDown, 0)
	if v != 5 {
		t.Fatalf("Up, Up, Down from 4: %v, want 5", v)
	}
	// the ends
	v = 10
	g.click(ux, uy)
	if v != 10 {
		t.Fatalf("a step up from the top: %v, want 10", v)
	}
	n = g.node(ui.RoleSlider, "Wrapping")
	wx, wy, _, _ := halves(n)
	g.click(wx, wy)
	if wrapped != 0 {
		t.Fatalf("a step up from the top of a wrapping stepper: %v, want 0", wrapped)
	}
	// the accessibility actions
	before := v
	g.in.PushAction(ui.SemRequest{Key: g.node(ui.RoleSlider, "Count").Key, Action: ui.ActDecrement})
	g.frame()
	g.in.PushAction(ui.SemRequest{Key: g.node(ui.RoleSlider, "Count").Key, Action: ui.ActSetValue, Value: 3})
	g.frame()
	if before != 10 || v != 3 {
		t.Fatalf("decrement from %v then set value 3 gave %v", before, v)
	}
}

// frameNs runs one frame that lasted ns nanoseconds (the clock and dt both).
func (g *rig) frameNs(ns int64) {
	g.time += ns
	g.in.Time = g.time
	g.c.Begin(&g.in, g.w, g.h, g.scale, float32(ns)/1e9)
	g.k.Begin(g.c)
	g.build(g)
	g.k.End()
	g.c.End(&g.list)
	g.in.ClearFrame()
}

// A held half makes exactly the steps due by the hold's elapsed time (the
// clock in whole milliseconds since the frame it went down): one, one more
// at the delay and one per interval, none twice, whatever the frame rate and
// however the frames land on those boundaries.
func TestStepperRepeatCountsAreExact(t *testing.T) {
	rc := rexui.StepperLook
	delay, every := int64(rc.RepeatDelay*1000+0.5), int64(rc.RepeatInterval*1000+0.5)
	due := func(e int64) int {
		n := 1
		if e >= delay {
			n += 1 + int((e-delay)/every)
		}
		return n
	}
	for _, tc := range []struct {
		name string
		dts  []int64 // the frames' lengths, repeated
	}{
		{"60 Hz", []int64{16_666_667}},
		{"120 Hz", []int64{8_333_333}},
		{"10 ms, on the boundaries", []int64{10_000_000}},
		{"uneven", []int64{16_666_667, 7_000_000, 33_333_333, 1_000_000, 12_500_000, 4_999_999, 20_000_001, 3_000_000}},
	} {
		v := float32(0)
		g := newRig(t, theme.Light, nil, column(func(g *rig) {
			rexui.Stepper(g.k, "Count", &v, 0, 100000, 1, rexui.StepperOpts{})
		}))
		ux, uy, _, _ := halves(g.node(ui.RoleSlider, "Count"))
		g.in.MouseX, g.in.MouseY = ux, uy
		g.frame()
		g.time = (g.time/1e6 + 1) * 1e6 // on a millisecond
		g.in.SetButton(ui.ButtonLeft, true, 1)
		var t0 int64 = -1
		for i := 0; g.time-max(t0, 0)*1e6 < 1_500_000_000 || t0 < 0; i++ {
			g.frameNs(tc.dts[i%len(tc.dts)])
			if t0 < 0 {
				if v == 0 {
					continue
				}
				t0 = g.time / 1e6 // the hold's first frame, in ms
			}
			e := g.time/1e6 - t0
			if want := due(e); int(v) != want {
				t.Fatalf("%s: held %d ms: %v steps, want %d", tc.name, e, v, want)
			}
		}
		releaseButton(g)
	}
}

// A held half steps once when it goes down, once more after the recipe's delay
// and then at its interval, only while the pointer is over it, and stops when
// the button goes up.
func TestStepperRepeatsWhileHeld(t *testing.T) {
	v := float32(0)
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		rexui.Stepper(g.k, "Count", &v, 0, 1000, 1, rexui.StepperOpts{})
	}))
	ux, uy, _, _ := halves(g.node(ui.RoleSlider, "Count"))
	rc := rexui.StepperLook
	frames := func(ms float32) int { return int(ms/1000*60 + 0.5) }

	pressAt(g, ux, uy)
	g.frames(frames(rc.RepeatDelay*1000) - 3)
	if v != 1 {
		t.Fatalf("held for just under the delay: %v steps, want 1", v)
	}
	g.frames(frames(400) - frames(rc.RepeatDelay*1000) + 3)
	// at 400 ms: the press, the first repeat at the delay and then one per
	// interval
	want := 2 + int((400-rc.RepeatDelay*1000)/(rc.RepeatInterval*1000))
	if d := int(v) - want; d < -1 || d > 1 {
		t.Fatalf("held for 400 ms: %v steps, want about %d", v, want)
	}
	// away from the half, the repeat pauses
	g.in.MouseY = g.node(ui.RoleSlider, "Count").Bounds[3] + 40
	g.frame()
	at := v
	g.frames(30)
	if v != at {
		t.Fatalf("held with the pointer off the half: %v became %v", at, v)
	}
	g.in.MouseY = uy
	g.frame()
	releaseButton(g)
	at = v
	g.frames(60)
	if v != at {
		t.Fatalf("after the release: %v became %v", at, v)
	}
	// a second hold starts afresh
	pressAt(g, ux, uy)
	g.frames(5)
	if d := v - at; d != 1 {
		t.Fatalf("a second hold of 5 frames: %v steps, want 1", d)
	}
	releaseButton(g)
}

func TestStepperDisabledAndField(t *testing.T) {
	v, w := float32(5), float32(5)
	var buf, buf2 ui.TextBuffer
	buf.Reserve(32)
	buf2.Reserve(32)
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		g.k.BeginDisabled(true)
		rexui.Stepper(g.k, "Off", &v, 0, 10, 1, rexui.StepperOpts{})
		g.k.EndDisabled()
		rexui.StepperField(g.k, "Copies", &buf2, &w, 0, 10, 1, rexui.StepperFieldOpts{})
	}))
	ux, uy, _, _ := halves(g.node(ui.RoleSlider, "Off"))
	g.click(ux, uy)
	g.key(ui.KeyUp, 0)
	if v != 5 {
		t.Fatalf("a disabled stepper changed to %v", v)
	}
	// the field shows the value, the stepper steps it, and typing sets it
	if buf2.String() != "5" {
		t.Fatalf("the field shows %q, want 5", buf2.String())
	}
	n := g.node(ui.RoleSlider, "Copies")
	x, y, _, _ := halves(n)
	g.click(x, y)
	g.frame()
	if w != 6 || buf2.String() != "6" {
		t.Fatalf("after a click on the stepper: value %v, field %q, want 6", w, buf2.String())
	}
	g.click(centre(g.node(ui.RoleTextField, "Copies")))
	buf2.SelectAll()
	g.typeText("42")
	g.key(ui.KeyEnter, 0)
	g.frame()
	if w != 10 || buf2.String() != "10" {
		t.Fatalf("after typing 42 and Enter: value %v, field %q, want 10 (clamped)", w, buf2.String())
	}
}

func TestDisclosureToggles(t *testing.T) {
	var open, push bool
	var changed int
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		if rexui.Disclosure(g.k, "Details", &open, rexui.DisclosureOpts{}).Changed {
			changed++
		}
		rexui.Disclosure(g.k, "More", &push, rexui.DisclosureOpts{Variant: rexui.DisclosurePush})
	}))
	n := g.node(ui.RoleButton, "Details")
	if n.State&ui.SemExpanded != 0 {
		t.Fatal("a closed disclosure reports expanded")
	}
	g.click(centre(n))
	if !open || changed != 1 {
		t.Fatalf("a click: open %v after %d changes", open, changed)
	}
	if g.node(ui.RoleButton, "Details").State&ui.SemExpanded == 0 {
		t.Fatal("an open disclosure does not report expanded")
	}
	g.key(ui.KeySpace, 0)
	if open {
		t.Fatal("Space with the focus did not close it")
	}
	g.click(centre(g.node(ui.RoleButton, "More")))
	if !push {
		t.Fatal("the push disclosure did not open")
	}
	// the chevron turns over the Standard duration, and stops asking for
	// frames when it has arrived
	g.k.ReduceMotion = false
	g.settle()
	g.click(centre(g.node(ui.RoleButton, "Details")))
	g.frame()
	if !g.c.Animating() {
		t.Fatal("the chevron is not turning in the frame after the click")
	}
	g.settle()
	if g.c.Animating() {
		t.Fatal("the chevron still asks for frames after it arrived")
	}
}

func TestHelpButtonAndToggleButton(t *testing.T) {
	var helps int
	on := false
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		if rexui.HelpButton(g.k, "Help", rexui.HelpButtonOpts{}).Clicked {
			helps++
		}
		rexui.ToggleButton(g.k, "Bold", &on, rexui.ToggleOpts{})
	}))
	g.click(centre(g.node(ui.RoleButton, "Help")))
	if helps != 1 {
		t.Fatalf("a click on the help button gave %d clicks", helps)
	}
	accent := g.k.Theme().Colors[theme.Accent] >> 8
	accented := func() bool {
		for _, q := range g.list.Quads {
			if q.Mode == gpu.ModeShape && q.Color[0]&0xFF == 0xFF && q.Color[0]>>8 == accent {
				return true
			}
		}
		return false
	}
	if accented() {
		t.Fatal("a toggle button that is off is drawn in the accent")
	}
	n := g.node(ui.RoleCheckbox, "Bold")
	g.click(centre(n))
	g.settle()
	if !on || !accented() || g.node(ui.RoleCheckbox, "Bold").State&ui.SemChecked == 0 {
		t.Fatalf("after a click: on %v, in the accent %v, checked %v", on, accented(), g.node(ui.RoleCheckbox, "Bold").State&ui.SemChecked != 0)
	}
	g.key(ui.KeySpace, 0)
	g.settle()
	if on || accented() {
		t.Fatalf("Space with the focus: on %v, in the accent %v", on, accented())
	}
}

func TestLinkReportsAndNeverOpens(t *testing.T) {
	var clicks int
	disabled := false
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		g.k.BeginDisabled(disabled)
		if rexui.Link(g.k, "Learn more", rexui.LinkOpts{}).Clicked {
			clicks++
		}
		g.k.EndDisabled()
	}))
	n := g.node(ui.RoleLink, "Learn more")
	if n.Actions&ui.ActPress == 0 {
		t.Fatal("a link cannot be pressed by an assistive technology")
	}
	x, y := centre(n)
	g.in.MouseX, g.in.MouseY = x, y
	g.frame()
	g.frame()
	var cursors []ui.CursorRegion
	cursors = g.c.CursorRegions(cursors)
	pointing := false
	for _, c := range cursors {
		if c.Cursor == ui.CursorPointer && x >= c.Rect[0] && x < c.Rect[2] && y >= c.Rect[1] && y < c.Rect[3] {
			pointing = true
		}
	}
	if !pointing {
		t.Fatalf("no pointing hand over the link: %v", cursors)
	}
	g.click(x, y)
	if clicks != 1 {
		t.Fatalf("a click gave %d clicks", clicks)
	}
	g.key(ui.KeyEnter, 0)
	if clicks != 2 {
		t.Fatalf("Enter with the focus gave %d clicks in all, want 2", clicks)
	}
	disabled = true
	g.frames(2)
	g.click(x, y)
	if clicks != 2 {
		t.Fatalf("a disabled link was clicked: %d", clicks)
	}
	if cursors = g.c.CursorRegions(cursors[:0]); len(cursors) != 0 {
		t.Fatalf("a disabled link shows the pointing hand: %v", cursors)
	}
}

func TestProgressAndLevelReport(t *testing.T) {
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		g.k.NextWidth(g.k.Fixed(200))
		rexui.ProgressBar(g.k, "Copying", 0.25, rexui.ProgressOpts{})
		g.k.NextWidth(g.k.Fixed(200))
		rexui.ProgressBar(g.k, "Connecting", -1, rexui.ProgressOpts{})
		g.k.NextWidth(g.k.Fixed(160))
		rexui.LevelIndicator(g.k, "Disk", 9, 0, 10, rexui.LevelOpts{Warning: 7, Critical: 9})
	}))
	if n := g.node(ui.RoleProgressBar, "Copying"); n.Range[1] != 1 || n.Range[2] != 0.25 {
		t.Fatalf("the determinate bar reports range %v", n.Range)
	}
	if n := g.node(ui.RoleProgressBar, "Connecting"); n.Range != ([4]float64{}) {
		t.Fatalf("the indeterminate bar reports range %v, want none", n.Range)
	}
	if n := g.node(ui.RoleProgressBar, "Disk"); n.Range[0] != 0 || n.Range[1] != 10 || n.Range[2] != 9 {
		t.Fatalf("the level reports range %v", n.Range)
	}
	red := g.k.Theme().Colors[theme.Red] >> 8
	found := false
	for _, q := range g.list.Quads {
		if q.Mode == gpu.ModeShape && q.Color[0]&0xFF == 0xFF && q.Color[0]>>8 == red {
			found = true
		}
	}
	if !found {
		t.Fatal("a level at its critical value is not drawn in red")
	}
}

// motion builds a window of the animations and reports what the frame asked
// for: an animation that frames are requested for (Animating) and the time
// until the next step of the stepped ones (WakeAfter).
func motionRig(t *testing.T, sys *theme.SystemValues, below float32) *rig {
	return newRig(t, theme.Light, sys, func(g *rig) {
		g.k.NextPad(20)
		g.k.Column("col")
		if below > 0 {
			g.k.Space(below)
		}
		g.k.NextWidth(g.k.Fixed(200))
		rexui.ProgressBar(g.k, "Bar", -1, rexui.ProgressOpts{})
		rexui.Spinner(g.k, "Spinner", rexui.SpinnerOpts{})
		rexui.Spinner(g.k, "Small", rexui.SpinnerOpts{Size: theme.SizeSmall})
		g.k.EndColumn()
	})
}

func asked(g *rig) (frames bool, wake int64) {
	g.frames(3)
	return g.c.Animating(), g.c.WakeAfter()
}

// Indeterminate bars and spinners ask for frames only while they run: not in
// an idle window, nor under Reduce Motion, in a hidden window or out of view.
func TestLoopingMotionAsksForFramesOnlyWhileRunning(t *testing.T) {
	// idle: nothing that loops, nothing asked
	idle := newRig(t, theme.Light, nil, column(func(g *rig) {
		rexui.Button(g.k, "OK", rexui.ButtonOpts{})
		rexui.ProgressBar(g.k, "Done", 1, rexui.ProgressOpts{})
	}))
	idle.k.ReduceMotion = false
	if f, w := asked(idle); f || w != 0 {
		t.Fatalf("an idle window asks for frames: animating %v, wake %d", f, w)
	}

	g := motionRig(t, nil, 0)
	g.k.ReduceMotion = false
	f, w := asked(g)
	if !f {
		t.Fatal("a running indeterminate bar asks for no frames")
	}
	if period := int64(rexui.SpinnerLook.Period * 1e9); w < 1 || w > period/8 {
		t.Fatalf("a running spinner wants a frame in %d ns, want within a step (%d)", w, period/8)
	}

	g.in.Hidden = true
	if f, w := asked(g); f || w != 0 {
		t.Fatalf("a hidden window: animating %v, wake %d, want neither", f, w)
	}
	g.in.Hidden = false
	if f, _ := asked(g); !f {
		t.Fatal("the bar did not resume when the window showed again")
	}

	g.k.ReduceMotion = true
	if f, w := asked(g); f || w != 0 {
		t.Fatalf("Reduce Motion (the kit's): animating %v, wake %d", f, w)
	}

	// the system's setting
	sys := &theme.SystemValues{Known: true, Settings: theme.Settings{ReduceMotion: true}}
	s := motionRig(t, sys, 0)
	s.k.ReduceMotion = false
	if f, w := asked(s); f || w != 0 {
		t.Fatalf("the system's Reduce Motion: animating %v, wake %d", f, w)
	}

	// out of view: below the window's bottom edge
	o := motionRig(t, nil, 2000)
	o.k.ReduceMotion = false
	if f, w := asked(o); f || w != 0 {
		t.Fatalf("below the window: animating %v, wake %d", f, w)
	}
}

// A spinner and an indeterminate bar that a flow's MaxLines hides (their
// rectangles are empty, as before their first layout) ask for no frames;
// the same in the flow's first line run.
func TestLoopingMotionHiddenInAFlowAsksNoFrames(t *testing.T) {
	for _, hidden := range []bool{true, false} {
		g := newRig(t, theme.Light, nil, func(g *rig) {
			c := g.c
			c.NextSize(ui.Px(300), ui.Children(0))
			c.BeginFlow("flow", ui.FlowOpts{GapX: 6, GapY: 6, MaxLines: 1})
			if hidden { // a full line before them
				c.NextSize(ui.Px(300), ui.Px(20))
				c.Box(0, "wide")
			}
			rexui.Spinner(g.k, "Spinner", rexui.SpinnerOpts{})
			g.k.NextWidth(g.k.Fixed(100))
			rexui.ProgressBar(g.k, "Bar", -1, rexui.ProgressOpts{})
			c.EndFlow()
		})
		g.k.ReduceMotion = false
		g.frames(3)
		f, w := asked(g)
		if hidden && (f || w != 0) {
			t.Fatalf("hidden by MaxLines: animating %v, wake %d, want neither", f, w)
		}
		if !hidden && (!f || w == 0) {
			t.Fatalf("in the flow's first line: animating %v, wake %d, want both", f, w)
		}
	}
}

// The spinner steps once per eighth of its period and is the same a period
// later; the bar's segment moves with the clock.
func TestSpinnerSteps(t *testing.T) {
	g := motionRig(t, nil, 0)
	g.k.ReduceMotion = false
	period := int64(rexui.SpinnerLook.Period * 1e9)
	// the colours of the spokes (discs of the spinners' sizes) at a time
	spin := func(at int64) []uint32 {
		g.time = at - 16_666_667 // the rig's frame adds one frame
		g.frame()
		var out []uint32
		for _, q := range g.list.Quads {
			if q.Mode == gpu.ModeShape && q.Radii[0] > 0 && q.Rect[2]-q.Rect[0] == q.Rect[3]-q.Rect[1] && q.Rect[2]-q.Rect[0] < 12 {
				out = append(out, q.Color[0])
			}
		}
		return out
	}
	same := func(a, b []uint32) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}
	base := spin(7 * period)
	if len(base) == 0 {
		t.Fatal("found no spokes")
	}
	if !same(base, spin(7*period+period/16)) {
		t.Fatal("the spinner changed within a step")
	}
	if same(base, spin(7*period+period/8+period/64)) {
		t.Fatal("the spinner did not step after an eighth of its period")
	}
	if !same(base, spin(8*period)) {
		t.Fatal("the spinner is not the same a period later")
	}
}

// The looping animations allocate nothing per frame while they run.
func TestLoopingMotionAllocatesNothing(t *testing.T) {
	g := motionRig(t, nil, 0)
	g.k.ReduceMotion = false
	g.frames(5)
	if n := testing.AllocsPerRun(50, g.frame); n != 0 {
		t.Fatalf("%.1f allocations per frame while animating", n)
	}
}
