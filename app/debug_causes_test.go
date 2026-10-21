package app

import (
	"strings"
	"testing"

	"gonativeui/platform"
	"gonativeui/ui"
)

const stepNs = int64(16e6)

// settleSteps steps the loop until it has nothing owed and returns how many
// frames that drew.
func settleSteps(t *testing.T, o *Offscreen) int {
	t.Helper()
	n := 0
	for i := 0; i < 400; i++ {
		if !o.Step(stepNs) {
			return n
		}
		n++
	}
	t.Fatal("the app never went idle")
	return n
}

func newStepper(t *testing.T, b Builder) *Offscreen {
	t.Helper()
	o := newOverlayRig(t, b, 400, 300, 1)
	o.EnableDebug(true)
	settleSteps(t, o)
	return o
}

func move(x, y float32) platform.Event {
	return platform.Event{Kind: platform.EvPointerMove, X: x, Y: y}
}

// causesOf returns the causes of the last frame that ended.
func causesOf(t *testing.T, o *Offscreen) []RepaintCause {
	t.Helper()
	r, ok := o.DebugFrame(0)
	if !ok {
		t.Fatal("no frame recorded")
	}
	return r.Causes[:r.NCauses]
}

func has(cs []RepaintCause, k Cause) (RepaintCause, bool) {
	for _, c := range cs {
		if c.Kind == k {
			return c, true
		}
	}
	return RepaintCause{}, false
}

func mustHave(t *testing.T, o *Offscreen, k Cause) RepaintCause {
	t.Helper()
	cs := causesOf(t, o)
	c, ok := has(cs, k)
	if !ok {
		var names []string
		for _, c := range cs {
			names = append(names, c.Kind.String())
		}
		t.Fatalf("the frame's causes are %v, want %v among them", names, k)
	}
	if _, bad := has(cs, CauseUnknown); bad {
		t.Errorf("a frame with a known cause is also marked as having none")
	}
	return c
}

// An idle app draws no frames, and the first frame has a cause of its own.
func TestIdleAppRecordsNoFrames(t *testing.T) {
	o := newOverlayRig(t, &scene{}, 400, 300, 1)
	o.EnableDebug(true)
	if !o.Step(stepNs) {
		t.Fatal("the first frame was not drawn")
	}
	mustHave(t, o, CauseFirst)
	settleSteps(t, o)
	n := o.DebugFrames()
	for i := 0; i < 300; i++ {
		if o.Step(stepNs) {
			t.Fatalf("an idle app drew a frame (step %d)", i)
		}
	}
	if o.DebugFrames() != n {
		t.Errorf("%d frames recorded while idle", o.DebugFrames()-n)
	}
	if o.DebugUnexplained() != 0 {
		t.Errorf("%d frames with no cause", o.DebugUnexplained())
	}
}

func TestInputRecordsItsCauseAndTheHoverThatFollows(t *testing.T) {
	o := newStepper(t, &scene{})
	if !o.Step(0, move(100, 80)) {
		t.Fatal("a pointer move drew no frame")
	}
	mustHave(t, o, CauseInput)
	if !o.Step(stepNs) {
		t.Fatal("the hover frame that follows input with the pointer inside was not drawn")
	}
	mustHave(t, o, CauseHover)
	if settleSteps(t, o) != 0 {
		t.Error("frames beyond the hover frame")
	}
}

func TestResizeRecordsItsCauseAndTheSettleFrame(t *testing.T) {
	o := newStepper(t, &scene{})
	if !o.Step(0, platform.Event{Kind: platform.EvResize, W: 300, H: 200, Scale: 1}) {
		t.Fatal("a resize drew no frame")
	}
	mustHave(t, o, CauseResize)
	if !o.Step(stepNs) {
		t.Fatal("the settle frame after a resize was not drawn")
	}
	mustHave(t, o, CauseSettle)
}

func TestVisibleRecordsItsCause(t *testing.T) {
	o := newStepper(t, &scene{})
	if !o.Step(0, platform.Event{Kind: platform.EvVisible, Flags: platform.FlagDown}) {
		t.Fatal("the window showing again drew no frame")
	}
	mustHave(t, o, CauseVisible)
}

func TestTaskWakeAndAppearanceRecordTheirCauses(t *testing.T) {
	o := newStepper(t, &scene{})
	if !o.Step(0, platform.Event{Kind: platform.EvWake}) {
		t.Fatal("a wake drew no frame")
	}
	mustHave(t, o, CauseWake)
	settleSteps(t, o)
	o.dbg.serial-- // the platform's appearance serial has moved since the overlay looked
	if !o.Step(0, platform.Event{Kind: platform.EvWake}) {
		t.Fatal("an appearance wake drew no frame")
	}
	c := mustHave(t, o, CauseAppearance)
	if _, both := has([]RepaintCause{c}, CauseWake); both {
		t.Error("an appearance change is not a task wake")
	}
	settleSteps(t, o)
	if !o.Step(0, platform.Event{Kind: platform.EvWake}) {
		t.Fatal("a second wake drew no frame")
	}
	mustHave(t, o, CauseWake)
}

// An app event draws the windows it concerns (the Handler may have changed
// what they show), and the frame names the event: another app taking the
// activation is one, and was recorded as a menu or as no cause.
func TestAppEventRecordsItsKind(t *testing.T) {
	o := newStepper(t, &scene{})
	o.handler, o.started = DefaultHandler{}, true
	o.wins = append(o.wins, o.App)
	t.Cleanup(func() { o.wins = nil })
	o.AppEvent(&platform.AppEvent{Kind: platform.AppEvDeactivated}, nil)
	if !o.Step(0) {
		t.Fatal("the app's deactivation drew no frame")
	}
	c := mustHave(t, o, CauseApp)
	if EventKind(c.Sub) != EvDeactivated || c.String() != "app(deactivated)" {
		t.Errorf("the cause is %q (sub %d), want app(deactivated)", c.String(), c.Sub)
	}
	if settleSteps(t, o) != 0 {
		t.Error("frames after the app event's")
	}
}

// A box that animates names itself as the cause, with its key, for as long as
// it moves, and the frames stop when it arrives.
func TestAnimationRecordsTheBoxKey(t *testing.T) {
	b := &scene{anim: true}
	o := newStepper(t, b)
	if b.target == 0 {
		t.Fatal("the scene built no button")
	}
	o.Step(0, move(100, 80))
	drew := 0
	for i := 0; i < 400; i++ {
		if !o.Step(stepNs) {
			break
		}
		drew++
		if c, ok := has(causesOf(t, o), CauseAnimation); ok && c.Key != b.target {
			t.Fatalf("the animation names box %x, want the button %x", uint64(c.Key), uint64(b.target))
		}
	}
	if drew < 3 {
		t.Errorf("only %d frames of animation", drew)
	}
	if n := o.DebugCauseCount(CauseAnimation); n == 0 {
		t.Error("no animation frame was recorded")
	}
	if o.DebugUnexplained() != 0 {
		t.Errorf("%d frames with no cause", o.DebugUnexplained())
	}
}

// RequestFrame records who called it, symbolised only when shown.
func TestRequestFrameRecordsTheCaller(t *testing.T) {
	if !devBuild {
		t.Skip("release build")
	}
	b := &scene{}
	o := newStepper(t, b)
	b.requests = 3
	o.Step(0, move(100, 80))
	var c RepaintCause
	for i := 0; i < 10 && c.NCallers == 0; i++ {
		o.Step(stepNs)
		c, _ = has(causesOf(t, o), CauseAnimation)
	}
	if c.Kind != CauseAnimation || c.NCallers == 0 {
		t.Fatalf("no animation cause with a caller: %+v", c)
	}
	var sc symCache
	if s := string(sc.symbol(c.Callers[0])); !strings.Contains(s, "debug_test.go") || !strings.Contains(s, "request") {
		t.Errorf("the caller is %q, want scene.request in debug_test.go", s)
	}
	settleSteps(t, o)
}

// A WakeIn asks for a frame later; the frame it brings names it and its caller,
// and nothing is drawn before it is due.
func TestWakeInRecordsATimerCause(t *testing.T) {
	b := &scene{}
	o := newStepper(t, b)
	b.wakeAtT = o.step.clock + 200e6
	o.Step(0, move(100, 80))
	settleSteps(t, o) // the input and hover frames, which ask for the wait
	for i := 0; i < 2; i++ {
		if o.Step(10e6) {
			t.Fatalf("a frame %d ms into the wait", 10*(i+1))
		}
	}
	if !o.Step(200e6) {
		t.Fatal("the WakeIn did not wake the app")
	}
	c := mustHave(t, o, CauseTimer)
	if ui.DebugWakeKind(c.Sub) != ui.DebugWakeIn {
		t.Errorf("timer of kind %d, want WakeIn", c.Sub)
	}
	if devBuild {
		var sc symCache
		if c.NCallers == 0 || !strings.Contains(string(sc.symbol(c.Callers[0])), "Build") {
			t.Errorf("WakeIn's caller is not recorded as Build: %+v", c)
		}
	}
	settleSteps(t, o)
}

func TestDeadlineRecordsItsKey(t *testing.T) {
	b := &scene{deadlineK: ui.HashString(0, "test-deadline")}
	o := newStepper(t, b)
	b.deadlineAt = o.step.clock + 100e6
	o.Step(0, move(100, 80))
	settleSteps(t, o)
	if b.fired {
		t.Fatal("the deadline fired early")
	}
	if !o.Step(150e6) {
		t.Fatal("the deadline did not wake the app")
	}
	c := mustHave(t, o, CauseTimer)
	if ui.DebugWakeKind(c.Sub) != ui.DebugWakeDeadline || c.Key != b.deadlineK {
		t.Errorf("timer %+v, want the deadline %x", c, uint64(b.deadlineK))
	}
	if !b.fired {
		t.Error("the frame did not see the deadline fire")
	}
	settleSteps(t, o)
}

// A frame that ran with nothing to say for it is called out, so that the idle
// rule can be checked: this is a frame owed by something that does not tell.
func TestAFrameWithNoCauseIsCalledOut(t *testing.T) {
	o := newStepper(t, &scene{})
	o.p.invalidate() // the pacer asked for a frame and nobody noted why
	if !o.Step(stepNs) {
		t.Fatal("no frame")
	}
	r, _ := o.DebugFrame(0)
	if !r.Unexplained() {
		t.Fatalf("the frame has causes %+v, want none known", r.Causes[:r.NCauses])
	}
	if o.DebugUnexplained() != 1 || o.DebugCauseCount(CauseUnknown) != 1 {
		t.Errorf("unexplained %d, count %d", o.DebugUnexplained(), o.DebugCauseCount(CauseUnknown))
	}
	// the HUD of the next frame shows it, in red
	o.Step(0, move(100, 80))
	if s := &o.dbg.snap; s.Unexplained != 1 || !s.HasLast || !s.Last.Unexplained() {
		t.Errorf("the HUD's snapshot does not know about it: %d", s.Unexplained)
	}
}

// The causes of a frame are in the log lines and the HUD text.
func TestCauseText(t *testing.T) {
	var b []byte
	b = appendCause(b, &RepaintCause{Kind: CauseTimer, Sub: uint8(ui.DebugWakeDeadline), Key: 0xabc, N: 3})
	if string(b) != "timer(deadline) #abc x3" {
		t.Errorf("%q", b)
	}
	if CauseUnknown.String() != "NO CAUSE" || CauseAnimation.String() != "anim" {
		t.Error("cause names")
	}
}
