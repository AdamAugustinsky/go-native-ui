//go:build darwin

package platform

import (
	"testing"

	"github.com/ebitengine/purego"

	"gonativeui/internal/objc"
)

// cgScroll builds a scroll-wheel NSEvent the way the trackpad's events come:
// a CGEvent (CGEventCreateScrollWheelEvent2) with the continuous, phase and
// momentum fields set, wrapped by +[NSEvent eventWithCGEvent:]. The phases
// are CGScrollPhase and CGMomentumScrollPhase values. It never posts the event.
type cgScroll struct {
	create, setInt, setF64, release uintptr
}

func newCGScroll(t *testing.T) *cgScroll {
	if _, err := loadFramework("/System/Library/Frameworks/AppKit.framework/AppKit"); err != nil {
		t.Skip("no AppKit:", err)
	}
	h, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		t.Skip("no CoreGraphics:", err)
	}
	sym := func(n string) uintptr {
		p, err := purego.Dlsym(h, n)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := purego.Dlsym(cf, "CFRelease")
	if err != nil {
		t.Fatal(err)
	}
	return &cgScroll{create: sym("CGEventCreateScrollWheelEvent2"), setInt: sym("CGEventSetIntegerValueField"),
		setF64: sym("CGEventSetDoubleValueField"), release: rel}
}

// event returns an autoreleased NSEvent; line selects line units (a wheel).
func (s *cgScroll) event(line bool, phase, momentum int, dx, dy float64, inverted bool) objc.ID {
	f := objc.F
	units := uint32(0) // kCGScrollEventUnitPixel
	if line {
		units = 1
	}
	cg := f.Reset().Arg(0).Arg32(units).Arg32(2).Arg32(uint32(int32(dy))).Arg32(uint32(int32(dx))).Arg32(0).Call(s.create).R0()
	setInt := func(field uint32, v int64) { f.Reset().Arg(cg).Arg32(field).Arg(uintptr(v)).Call(s.setInt) }
	setF64 := func(field uint32, v float64) { f.Reset().Arg(cg).Arg32(field).ArgF64(v).Call(s.setF64) }
	if !line {
		setInt(88, 1) // kCGScrollWheelEventIsContinuous
		setF64(93, dy)
		setF64(94, dx) // FixedPtDeltaAxis1, 2
		setF64(96, dy)
		setF64(97, dx) // PointDeltaAxis1, 2
	}
	setInt(99, int64(phase))     // kCGScrollWheelEventScrollPhase
	setInt(123, int64(momentum)) // kCGScrollWheelEventMomentumPhase
	if inverted {
		setInt(137, 1) // kCGScrollWheelEventIsDirectionInvertedFromDevice (undocumented)
	}
	ev := objc.Send1(objc.Cls("NSEvent"), objc.Sel("eventWithCGEvent:"), uintptr(cg))
	f.Reset().Arg(cg).Call(s.release)
	return ev
}

func TestScrollEventPhases(t *testing.T) {
	s := newCGScroll(t)
	pool := objc.Send(objc.Send(objc.Cls("NSAutoreleasePool"), objc.Sel("alloc")), objc.Sel("init"))
	defer objc.Send(pool, objc.Sel("drain"))
	cases := []struct {
		name            string
		line            bool
		phase, momentum int
		dx, dy          float64
		want            Event
	}{
		{"maybegin", false, 128, 0, 0, 0, Event{Phase: PhaseMayBegin, Flags: FlagPrecise}},
		{"began", false, 1, 0, 0, 4, Event{Phase: PhaseBegan, Flags: FlagPrecise, DY: 8}},
		{"changed", false, 2, 0, -3, 2, Event{Phase: PhaseChanged, Flags: FlagPrecise, DX: -6, DY: 4}},
		{"ended", false, 4, 0, 0, 0, Event{Phase: PhaseEnded, Flags: FlagPrecise}},
		{"cancelled", false, 8, 0, 0, 0, Event{Phase: PhaseCancelled, Flags: FlagPrecise}},
		{"momentum began", false, 0, 1, 0, -12, Event{Momentum: PhaseBegan, Flags: FlagPrecise, DY: -24}},
		{"momentum changed", false, 0, 2, 0, -8, Event{Momentum: PhaseChanged, Flags: FlagPrecise, DY: -16}},
		{"momentum ended", false, 0, 3, 0, 0, Event{Momentum: PhaseEnded, Flags: FlagPrecise}},
		{"wheel", true, 0, 0, 0, 3, Event{DY: 3 * lineScroll * 2}},
	}
	for _, c := range cases {
		e := scrollEventOf(s.event(c.line, c.phase, c.momentum, c.dx, c.dy, false), 2, 7)
		w := c.want
		w.Kind, w.Time = EvScroll, 7
		if e != w {
			t.Errorf("%s: got %+v, want %+v", c.name, e, w)
		}
	}
	if e := scrollEventOf(s.event(false, 2, 0, 0, 1, true), 1, 0); e.Flags != FlagPrecise|FlagInverted {
		t.Errorf("an inverted device's event: flags %b, want precise and inverted", e.Flags)
	}
}

func TestScrollPhaseOfNSEventPhase(t *testing.T) {
	for p, want := range map[uintptr]ScrollPhase{0: PhaseNone, 1: PhaseBegan, 2: PhaseStationary, 4: PhaseChanged, 8: PhaseEnded, 16: PhaseCancelled, 32: PhaseMayBegin} {
		if got := phaseOf(p); got != want {
			t.Errorf("NSEventPhase %d: %v, want %v", p, got, want)
		}
	}
}

// TestScrollEventsThroughTheQueue: a gesture's events posted to the app's own
// queue (-[NSApplication postEvent:atStart:], the window's events, never the
// system's stream) come out of Wait in order, as EvScroll with their phases
// and the window's scale.
func TestScrollEventsThroughTheQueue(t *testing.T) {
	s := newCGScroll(t)
	type got struct {
		phase, momentum ScrollPhase
		dy              float32
	}
	var evs []got
	var scale float32
	var openErr error
	onMain(func() {
		w, err := Open(Options{Title: "scroll test", Width: 200, Height: 200, Hidden: true})
		if err != nil {
			openErr = err
			return
		}
		defer w.Close()
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		w.Wait(0)
		scale = w.scale
		post := func(phase, momentum int, dy float64) {
			ev := s.event(false, phase, momentum, 0, dy, false)
			objc.Send2(objc.Send(objc.Cls("NSApplication"), objc.Sel("sharedApplication")), objc.Sel("postEvent:atStart:"), uintptr(ev), 0)
		}
		post(128, 0, 0) // MayBegin
		post(1, 0, 3)   // Began
		post(2, 0, 4)   // Changed
		post(4, 0, 0)   // Ended
		post(0, 1, 9)   // momentum began
		post(0, 3, 0)   // momentum ended
		for i := 0; i < 20 && len(evs) < 6; i++ {
			for _, e := range w.Wait(50e6) {
				if e.Kind == EvScroll {
					evs = append(evs, got{e.Phase, e.Momentum, e.DY})
				}
			}
		}
	})
	if openErr != nil {
		t.Skip("no window:", openErr)
	}
	want := []got{{PhaseMayBegin, PhaseNone, 0}, {PhaseBegan, PhaseNone, 3 * scale}, {PhaseChanged, PhaseNone, 4 * scale},
		{PhaseEnded, PhaseNone, 0}, {PhaseNone, PhaseBegan, 9 * scale}, {PhaseNone, PhaseEnded, 0}}
	if len(evs) != len(want) {
		t.Fatalf("got %+v, want %+v", evs, want)
	}
	for i := range want {
		if evs[i] != want[i] {
			t.Errorf("event %d: %+v, want %+v", i, evs[i], want[i])
		}
	}
}
