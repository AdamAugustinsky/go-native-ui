//go:build darwin

package platform

import (
	"fmt"
	"math"
	"testing"

	"github.com/ebitengine/purego"

	"gonativeui/internal/objc"
)

// pxRect is a rectangle in points (x0, y0, x1, y1) as pixels at scale s.
func pxRect(s float32, x0, y0, x1, y1 float32) [4]float32 {
	return [4]float32{x0 * s, y0 * s, x1 * s, y1 * s}
}

// Hosts are made hidden, placed and clipped in step with the next apply
// (their view keeps its unclipped frame inside the clip view), hidden when
// asked, cost nothing while unchanged, and refuse a 33rd; released ids go
// stale, and every native object goes with ReleaseHost and Close.
func TestHostLifecycle(t *testing.T) {
	type result struct {
		err                           error
		hiddenAtFirst, pending        bool
		clipFrame, viewFrame          [4]float64
		shown, clips                  bool
		allocs                        float64
		pendingSame                   bool
		hiddenAfter                   bool
		glassContent                  string
		staleView                     uintptr
		reused                        bool
		staleRelease                  int64
		tooMany                       error
		made                          int
		extAliveWhileHosted, extFreed int
		alive                         int
		base, mid, end                ShellCounts
		holes                         int
	}
	var r result
	onMain(func() {
		r.base = ShellObjects()
		w, err := Open(Options{Title: "hosts", Width: 400, Height: 300, Hidden: true})
		if err != nil {
			r.err = err
			return
		}
		pool := objc.F.Call0(objc.PoolPush)
		s := w.scale
		id, err := w.NewHost(HostView, HostOptions{})
		if err != nil {
			r.err = err
			w.Close()
			return
		}
		sl := w.hosts.slot(id)
		r.hiddenAtFirst = objc.SendBool(sl.clip, objc.Sel("isHidden"))
		// In a list scrolled so that its top 10 pt are cut off.
		regs := []HostRegion{{ID: id, Rect: pxRect(s, 50, 60, 150, 90), Clip: pxRect(s, 50, 70, 150, 90)}}
		w.SetHosts(regs)
		r.pending = ShellPending()
		w.FlushShell()
		r.clipFrame, r.viewFrame = frameOf(sl.clip), frameOf(sl.view)
		r.shown = !objc.SendBool(sl.clip, objc.Sel("isHidden"))
		r.clips = !objc.SendBool1(sl.clip, selRespondsTo, uintptr(objc.Sel("clipsToBounds"))) || objc.SendBool(sl.clip, objc.Sel("clipsToBounds"))
		r.holes = len(w.curs.holes)
		r.allocs = testing.AllocsPerRun(100, func() { w.SetHosts(regs) })
		r.pendingSame = ShellPending()

		// Covered by a drawn popup: hidden.
		regs[0].Hidden = true
		w.SetHosts(regs)
		w.FlushShell()
		r.hiddenAfter = objc.SendBool(sl.clip, objc.Sel("isHidden")) && w.hosts.nVis == 0 && len(w.curs.holes) == 0

		// A glass host's view to fill is its content view.
		gid, _ := w.NewHost(HostGlass, HostOptions{Radius: 12})
		r.glassContent = className(objc.ID(w.HostView(gid)))

		// A released id goes stale, also once its slot is reused.
		w.ReleaseHost(id)
		r.staleView = w.HostView(id)
		id2, _ := w.NewHost(HostView, HostOptions{})
		r.reused = id2&0xFF == id&0xFF && id2 != id && w.HostView(id2) != 0
		n := ShellObjects().Hosts
		w.ReleaseHost(id)
		r.staleRelease = ShellObjects().Hosts - n

		// At most MaxHosts.
		r.made = 2
		for {
			if _, err := w.NewHost(HostView, HostOptions{}); err != nil {
				r.tooMany = err
				break
			}
			r.made++
		}

		// A view the caller gives lives while hosted, and goes with the
		// caller's own reference after the host is released.
		w.ReleaseHost(id2)
		ext := newViewOf(nsViewClass(), 0, 0, 10, 10)
		eid, _ := w.NewHost(HostView, HostOptions{View: uintptr(ext)})
		extRef := newWeakRefs(t, ext)
		objc.Send(ext, selReleaseObj) // the caller's reference
		objc.F.Call1(objc.PoolPop, pool)
		pool = objc.F.Call0(objc.PoolPush)
		r.extAliveWhileHosted = 0
		if objc.ID(w.HostView(eid)) == ext {
			r.extAliveWhileHosted = 1
		}
		w.ReleaseHost(eid)
		objc.F.Call1(objc.PoolPop, pool)
		r.extFreed = 1 - extRef.alive()

		pool = objc.F.Call0(objc.PoolPush)
		r.mid = ShellObjects()
		var objs []objc.ID
		for i := range w.hosts.slots {
			if s := &w.hosts.slots[i]; s.live {
				objs = append(objs, s.clip, s.view)
				if s.content != 0 {
					objs = append(objs, s.content)
				}
			}
		}
		refs := newWeakRefs(t, objs...)
		w.Close()
		objc.F.Call1(objc.PoolPop, pool)
		r.alive = refs.alive()
		r.end = ShellObjects()
	})
	if r.err != nil {
		t.Skipf("cannot open a window here: %v", r.err)
	}
	if !r.hiddenAtFirst || !r.pending {
		t.Errorf("new host hidden %v, SetHosts staged %v: want both", r.hiddenAtFirst, r.pending)
	}
	if r.clipFrame != [4]float64{50, 70, 100, 20} || r.viewFrame != [4]float64{0, -10, 100, 30} || !r.shown || !r.clips {
		t.Errorf("clip frame %v, view frame %v, shown %v, clips %v: want 50,70 100×20, 0,-10 100×30, shown and clipping", r.clipFrame, r.viewFrame, r.shown, r.clips)
	}
	if r.holes != 1 {
		t.Errorf("%d cursor holes for one shown host", r.holes)
	}
	if r.allocs != 0 || r.pendingSame {
		t.Errorf("an unchanged SetHosts allocates %.1f times and stages %v", r.allocs, r.pendingSame)
	}
	if !r.hiddenAfter {
		t.Error("a host marked hidden stays shown or keeps its routing rectangle")
	}
	if r.glassContent != "GNUIContainer" {
		t.Errorf("a glass host's HostView is a %s, want its content view", r.glassContent)
	}
	if r.staleView != 0 || !r.reused || r.staleRelease != 0 {
		t.Errorf("stale id: HostView %#x, slot reused under a new id %v, releasing it again moved the count by %d", r.staleView, r.reused, r.staleRelease)
	}
	if r.tooMany != ErrTooManyHosts || r.made != MaxHosts {
		t.Errorf("made %d hosts, then %v: want %d, then ErrTooManyHosts", r.made, r.tooMany, MaxHosts)
	}
	if r.extAliveWhileHosted != 1 || r.extFreed != 1 {
		t.Errorf("caller's view: hosted %v, freed after release %v", r.extAliveWhileHosted == 1, r.extFreed == 1)
	}
	if r.alive != 0 {
		t.Errorf("%d host views outlive Close", r.alive)
	}
	if r.end.Hosts != r.base.Hosts || r.mid.Hosts-r.base.Hosts != MaxHosts-1 {
		t.Errorf("hosts alive: %d before, %d with the window, %d after Close", r.base.Hosts, r.mid.Hosts, r.end.Hosts)
	}
}

// hostRig is a hidden window at the screen's origin with a text field
// hosted at 100,100 (points) 120×24.
type hostRig struct {
	w     *Window
	id    HostID
	field objc.ID
}

func openHostRig() (*hostRig, error) {
	w, err := Open(Options{Title: "host routing", Width: 400, Height: 300, Hidden: true})
	if err != nil {
		return nil, err
	}
	objc.F.Reset().Arg(uintptr(w.win)).Arg(uintptr(selSetFrameOrigin)).ArgPoint(0, 0).Call(objc.MsgSend)
	field := newViewOf(objc.Cls("NSTextField"), 0, 0, 120, 24)
	id, err := w.NewHost(HostView, HostOptions{View: uintptr(field), Focusable: true})
	objc.Send(field, selReleaseObj)
	if err != nil {
		w.Close()
		return nil, err
	}
	s := w.scale
	w.SetHosts([]HostRegion{{ID: id, Rect: pxRect(s, 100, 100, 220, 124), Clip: pxRect(s, 100, 100, 220, 124)}})
	w.FlushShell()
	return &hostRig{w: w, id: id, field: field}, nil
}

// mouseMove is a mouse-moved event of the window at x, y (points from the
// content's top left).
func (r *hostRig) mouseMove(x, y float64) objc.ID {
	evCls, evSel := objc.Cls("NSEvent"), objc.Sel("mouseEventWithType:location:modifierFlags:timestamp:windowNumber:context:eventNumber:clickCount:pressure:")
	num := objc.Send(r.w.win, objc.Sel("windowNumber"))
	return objc.ID(objc.F.Reset().Arg(uintptr(evCls)).Arg(uintptr(evSel)).
		Arg(5).ArgPoint(x, r.w.hPt-y).Arg(0).ArgF64(0).Arg(uintptr(num)).Arg(0).Arg(0).Arg(0).ArgF32(0).Call(objc.MsgSend).R0())
}

// key is a key-down event of the window with the given virtual key code.
func (r *hostRig) key(code uint16, chars string) objc.ID {
	evCls, evSel := objc.Cls("NSEvent"), objc.Sel("keyEventWithType:location:modifierFlags:timestamp:windowNumber:context:characters:charactersIgnoringModifiers:isARepeat:keyCode:")
	num := objc.Send(r.w.win, objc.Sel("windowNumber"))
	s := objc.Str(chars)
	return objc.ID(objc.F.Reset().Arg(uintptr(evCls)).Arg(uintptr(evSel)).
		Arg(10).ArgPoint(0, 0).Arg(0).ArgF64(0).Arg(uintptr(num)).Arg(0).Arg(uintptr(s)).Arg(uintptr(s)).ArgBool(false).ArgNarrow(uint32(code), 2).Call(objc.MsgSend).R0())
}

// scroll is a scroll-wheel event at x, y (points from the content's top
// left; the window is at the screen's origin, so its coordinates are the
// screen's).
func (r *hostRig) scroll(x, y float64, dy int32) objc.ID {
	create, setLoc := cgScrollFns()
	if create == 0 {
		return 0
	}
	cg := objc.F.Reset().Arg(0).Arg(1 /* kCGScrollEventUnitLine */).Arg(1).Arg(uintptr(uint32(dy))).Arg(0).Arg(0).Call(create).R0()
	if cg == 0 {
		return 0
	}
	main := objc.Send(objc.Cls("NSScreen"), objc.Sel("mainScreen"))
	sh := objc.SendRect(main, selFrame).H
	objc.F.Reset().Arg(cg).ArgPoint(x, sh-(r.w.hPt-y)).Call(setLoc) // CG's global coordinates run down from the top
	ev := objc.Send1(objc.Cls("NSEvent"), objc.Sel("eventWithCGEvent:"), cg)
	objc.Release(objc.ID(cg))
	return ev
}

// gesture is a trackpad scroll event at x, y (as scroll) moving dy pixels,
// with CGScrollPhase phase and CGMomentumScrollPhase momentum.
func (r *hostRig) gesture(x, y float64, dy int32, phase, momentum uintptr) objc.ID {
	create, setLoc := cgScrollFns()
	if create == 0 || cgSetField == 0 {
		return 0
	}
	cg := objc.F.Reset().Arg(0).Arg(0 /* kCGScrollEventUnitPixel */).Arg(1).Arg(uintptr(uint32(dy))).Arg(0).Arg(0).Call(create).R0()
	if cg == 0 {
		return 0
	}
	main := objc.Send(objc.Cls("NSScreen"), objc.Sel("mainScreen"))
	sh := objc.SendRect(main, selFrame).H
	objc.F.Reset().Arg(cg).ArgPoint(x, sh-(r.w.hPt-y)).Call(setLoc)
	objc.F.Reset().Arg(cg).Arg(88).Arg(1).Call(cgSetField)         // kCGScrollWheelEventIsContinuous
	objc.F.Reset().Arg(cg).Arg(99).Arg(phase).Call(cgSetField)     // kCGScrollWheelEventScrollPhase
	objc.F.Reset().Arg(cg).Arg(123).Arg(momentum).Call(cgSetField) // kCGScrollWheelEventMomentumPhase
	ev := objc.Send1(objc.Cls("NSEvent"), objc.Sel("eventWithCGEvent:"), cg)
	objc.Release(objc.ID(cg))
	return ev
}

// CGScrollPhase and CGMomentumScrollPhase values.
const (
	cgBegan, cgChanged, cgEnded, cgMayBegin = 1, 2, 4, 128
	cgMomBegin, cgMomContinue, cgMomEnd     = 1, 2, 3
)

var cgCreateScroll, cgSetLocation, cgSetField uintptr

func cgScrollFns() (uintptr, uintptr) {
	if cgCreateScroll == 0 {
		h, err := loadFramework("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics")
		if err != nil {
			return 0, 0
		}
		cgCreateScroll, _ = purego.Dlsym(h, "CGEventCreateScrollWheelEvent2") // not variadic, unlike CGEventCreateScrollWheelEvent
		cgSetLocation, _ = purego.Dlsym(h, "CGEventSetLocation")
		cgSetField, _ = purego.Dlsym(h, "CGEventSetIntegerValueField")
	}
	return cgCreateScroll, cgSetLocation
}

func kinds(evs []Event) []EventKind {
	var k []EventKind
	for _, e := range evs {
		k = append(k, e.Kind)
	}
	return k
}

// Routing: moves over a host reach ui as one EvPointerLeave, scrolls over it
// are the host's until it hands one back, and keys are the host's while it
// holds the keyboard.
func TestHostRouting(t *testing.T) {
	type result struct {
		err                    error
		onHost, again, offHost []EventKind
		scrollOn, bubbled      []EventKind
		scrollOff              []EventKind
		noScroll               bool
		bubbledAt, offAt       bool // the scrolls carry their own position (FlagPos): over the host, and not
		keyFocused, keyApp     []EventKind
		focusEv                []Event
	}
	var r result
	onMain(func() {
		rig, err := openHostRig()
		if err != nil {
			r.err = err
			return
		}
		w := rig.w
		defer w.Close()
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		take := func() []EventKind { k := kinds(w.q.ev); w.q.reset(); return k }

		w.q.reset()
		w.pointerEvent(rig.mouseMove(150, 110), 5, 1)
		r.onHost = take()
		w.pointerEvent(rig.mouseMove(160, 112), 5, 2)
		r.again = take()
		w.pointerEvent(rig.mouseMove(30, 30), 5, 3)
		r.offHost = take()

		if ev := rig.scroll(150, 110, -3); ev != 0 {
			w.scrollEvent(ev, 4)
			r.scrollOn = take()
			// The host's view did not use it: up the responder chain to the
			// clip view, which queues it for ui.
			objc.Send1(w.hosts.slot(rig.id).clip, selScrollWheel, uintptr(ev))
			if len(w.q.ev) == 1 {
				e := w.q.ev[0]
				r.bubbledAt = e.Flags&FlagPos != 0 && w.overHost(e.X, e.Y)
			}
			r.bubbled = take()
			w.scrollEvent(rig.scroll(30, 30, -3), 5)
			r.offAt = len(w.q.ev) == 1 && w.q.ev[0].Flags&FlagPos != 0 && !w.overHost(w.q.ev[0].X, w.q.ev[0].Y)
			r.scrollOff = take()
		} else {
			r.noScroll = true
		}

		w.FocusHost(rig.id)
		r.focusEv = append(r.focusEv, w.q.ev...)
		w.q.reset()
		w.keyEvent(rig.key(0x00, "a"), 10, 6)
		r.keyFocused = take()
		w.FocusHost(0)
		r.focusEv = append(r.focusEv, w.q.ev...)
		w.q.reset()
		w.keyEvent(rig.key(0x00, "a"), 10, 7)
		r.keyApp = take()
	})
	if r.err != nil {
		t.Skipf("cannot open a window here: %v", r.err)
	}
	if !equalKinds(r.onHost, EvPointerLeave) || len(r.again) != 0 || !equalKinds(r.offHost, EvPointerMove) {
		t.Errorf("moves onto the host %v, over it %v, off it %v: want [leave], [], [move]", r.onHost, r.again, r.offHost)
	}
	if r.noScroll {
		t.Log("no scroll events can be made here: scroll routing untested")
	} else if len(r.scrollOn) != 0 || !equalKinds(r.bubbled, EvScroll) || !equalKinds(r.scrollOff, EvScroll) {
		t.Errorf("scroll over the host %v, handed back %v, elsewhere %v: want [], [scroll], [scroll]", r.scrollOn, r.bubbled, r.scrollOff)
	} else if !r.bubbledAt || !r.offAt {
		t.Errorf("the handed-back scroll carries its position over the host: %v; a scroll elsewhere its own, off the host: %v", r.bubbledAt, r.offAt)
	}
	if len(r.focusEv) != 2 || r.focusEv[0].Kind != EvHostFocus || r.focusEv[0].Tag == 0 || r.focusEv[1].Kind != EvHostFocus || r.focusEv[1].Tag != 0 {
		t.Errorf("focus events %+v: want EvHostFocus with the host, then with 0", r.focusEv)
	}
	if len(r.keyFocused) != 0 || !equalKinds(r.keyApp, EvKey) {
		t.Errorf("key while the host is focused %v, after %v: want [], [key]", r.keyFocused, r.keyApp)
	}
}

func equalKinds(k []EventKind, want ...EventKind) bool {
	if len(k) != len(want) {
		return false
	}
	for i := range k {
		if k[i] != want[i] {
			return false
		}
	}
	return true
}

// Focus hand-off out of a host: Tab and Shift+Tab through the window's
// key-view loop, the standard commands a view hands up the responder chain
// and the keys it does not use come back as EvHostCommand; the focus moving
// into the host by AppKit's own means is EvHostFocus.
func TestHostFocusCommands(t *testing.T) {
	var err error
	var focus []Event
	var cmds []Event
	onMain(func() {
		rig, e := openHostRig()
		if e != nil {
			err = e
			return
		}
		w := rig.w
		defer w.Close()
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		w.q.reset()
		objc.Send1(w.win, selMakeFirstResp, uintptr(rig.field)) // as a click in the field does
		focus = append(focus, w.q.ev...)
		w.q.reset()
		fr := objc.Send(w.win, selFirstResponder) // the field editor
		objc.Send1(w.win, selSelectFollowing, uintptr(fr))
		objc.Send1(w.win, selSelectPreceding, uintptr(fr))
		clip := w.hosts.slot(rig.id).clip
		objc.Send1(clip, selInsertTab, 0)
		objc.Send1(clip, selInsertBacktab, 0)
		objc.Send1(clip, selCancelOperation, 0)
		objc.Send1(clip, selInsertNewline, 0)
		objc.Send1(clip, selKeyDown, uintptr(rig.key(0x35, "\x1b"))) // Escape the view did not use
		cmds = append(cmds, w.q.ev...)
	})
	if err != nil {
		t.Skipf("cannot open a window here: %v", err)
	}
	if len(focus) != 1 || focus[0].Kind != EvHostFocus || HostID(focus[0].Tag)&0xFF != 1 {
		t.Errorf("first responder into the field: %+v, want one EvHostFocus with the host", focus)
	}
	want := []struct {
		k Key
		m Mods
	}{{KeyTab, 0}, {KeyTab, ModShift}, {KeyTab, 0}, {KeyTab, ModShift}, {KeyEscape, 0}, {KeyEnter, 0}, {KeyEscape, 0}}
	if len(cmds) != len(want) {
		t.Fatalf("host commands %+v, want %d", cmds, len(want))
	}
	for i, c := range cmds {
		if c.Kind != EvHostCommand || c.Key != want[i].k || c.Mods != want[i].m || HostID(c.Tag)&0xFF != 1 {
			t.Errorf("command %d: %+v, want EvHostCommand key %v mods %v", i, c, want[i].k, want[i].m)
		}
	}
}

// While a host holds the keyboard, a menu's key equivalent still goes
// through AppKit's menu (the command arrives once), and other keys stay the
// host's.
func TestKeyEquivalentWithHostFocused(t *testing.T) {
	var why string
	var focused bool
	var keys, plainKeys int
	var cmds []uint64
	onMain(func() {
		rig, w0 := openMenuRig(testMenuSpec(), false)
		if w0 != "" {
			why = w0
			return
		}
		w := rig.w
		defer w.Close()
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		field := newViewOf(objc.Cls("NSTextField"), 0, 0, 120, 24)
		id, err := w.NewHost(HostView, HostOptions{View: uintptr(field), Focusable: true})
		objc.Send(field, selReleaseObj)
		if err != nil {
			why = err.Error()
			return
		}
		s := w.scale
		w.SetHosts([]HostRegion{{ID: id, Rect: pxRect(s, 20, 60, 140, 84), Clip: pxRect(s, 20, 60, 140, 84)}})
		w.FlushShell()
		w.FocusHost(id)
		focused = w.hostFocused()
		w.q.reset()
		w.handle(keyDown(w, "s", 1<<20, 0x01))
		keys, cmds, _ = countEvents(w.q.ev)
		w.q.reset()
		w.handle(keyDown(w, "a", 0, 0x00))
		plainKeys, _, _ = countEvents(w.q.ev)
	})
	if why != "" {
		t.Skipf("cannot open a window here: %s", why)
	}
	if !focused {
		t.Fatal("the host did not take the keyboard")
	}
	if keys != 0 || len(cmds) != 1 || cmds[0] != tcSave {
		t.Errorf("⌘S with a host focused: %d key-downs and commands %v, want none and [%d]", keys, cmds, tcSave)
	}
	if plainKeys != 0 {
		t.Errorf("a plain key with a host focused reached ui %d times", plainKeys)
	}
}

// Releasing a host whose view refuses to give up the keyboard (a text field
// whose formatter rejects its text) still leaves no removed view first
// responder: the keyboard goes back to the app's view.
func TestReleaseHostThatRefusesToResign(t *testing.T) {
	var err error
	var refused, toView, holds bool
	onMain(func() {
		rig, e := openHostRig()
		if e != nil {
			err = e
			return
		}
		w := rig.w
		defer w.Close()
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		f := objc.Send(objc.Send(objc.Cls("NSNumberFormatter"), selAlloc), selInitPlain)
		objc.Send1(rig.field, objc.Sel("setFormatter:"), uintptr(f))
		objc.Send(f, selReleaseObj)
		objc.Send1(w.win, selMakeFirstResp, uintptr(rig.field))
		editor := objc.Send(w.win, selFirstResponder)
		objc.Send1(editor, objc.Sel("insertText:"), uintptr(objc.Str("not a number")))
		// The field does not let go of the keyboard by itself.
		refused = !objc.SendBool1(w.win, selMakeFirstResp, uintptr(w.view))
		objc.Send(rig.field, selRetainV) // to look at it after the release
		defer objc.Send(rig.field, selReleaseObj)
		w.ReleaseHost(rig.id)
		fr := objc.Send(w.win, selFirstResponder)
		toView = fr == w.view
		holds = fr == editor || w.hostFocused()
	})
	if err != nil {
		t.Skipf("cannot open a window here: %v", err)
	}
	if !refused {
		t.Skip("the field gave up the keyboard with a rejected text here")
	}
	if !toView || holds {
		t.Errorf("after releasing the host: first responder is the app's view %v, still the field's editor or a host %v", toView, holds)
	}
}

// Each scroll reaches ui once, at its own point, whichever view AppKit gives
// it to: the view under its point (the host's field hands back what it does
// not use), the host though the pointer has left it, or our view though the
// pointer is over the host (AppKit latching a trackpad gesture to the view it
// began in).
func TestHostScrollReachesUIOnce(t *testing.T) {
	type step struct {
		name string
		x, y float64
		to   string // the view AppKit gives it to: "field" (the host's), "clip" (the host's, handed back), "view" (ours)
	}
	steps := []step{
		{"over the host, to the host", 150, 110, "field"},
		{"over ui, to our view", 30, 30, "view"},
		{"over ui, latched to the host", 30, 30, "clip"},
		{"over the host, latched to our view", 150, 110, "view"},
		{"over the host, handed back", 150, 110, "clip"},
	}
	type got struct {
		n    int
		x, y float32
	}
	var err error
	var noScroll bool
	res := make([]got, len(steps))
	onMain(func() {
		rig, e := openHostRig()
		if e != nil {
			err = e
			return
		}
		w := rig.w
		defer w.Close()
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		clip := w.hosts.slot(rig.id).clip
		for i, st := range steps {
			ev := rig.scroll(st.x, st.y, -3)
			if ev == 0 {
				noScroll = true
				return
			}
			w.q.reset()
			w.scrollEvent(ev, 1) // the event loop (handle) sees it first
			switch st.to {
			case "field":
				objc.Send1(rig.field, selScrollWheel, uintptr(ev))
			case "clip":
				objc.Send1(clip, selScrollWheel, uintptr(ev))
			case "view":
				objc.Send1(w.view, selScrollWheel, uintptr(ev))
			}
			for _, q := range w.q.ev {
				if q.Kind == EvScroll {
					res[i].n++
					res[i].x, res[i].y = q.X/w.scale, q.Y/w.scale
				}
			}
		}
	})
	switch {
	case err != nil:
		t.Skipf("cannot open a window here: %v", err)
	case noScroll:
		t.Skip("no scroll events can be made here")
	}
	for i, st := range steps {
		if r := res[i]; r.n != 1 || math.Abs(float64(r.x)-st.x) > 1 || math.Abs(float64(r.y)-st.y) > 1 {
			t.Errorf("%s: queued %d times, at (%v, %v); want once at (%v, %v)", st.name, r.n, r.x, r.y, st.x, st.y)
		}
	}
}

// A trackpad gesture that begins over a hosted NSScrollView is the native
// view's to the end, momentum included: AppKit keeps giving it the events
// once the pointer is over drawn content, and the scroll view takes them all
// (it hands none back), so none reaches ui and no drawn scroller moves with
// it. A plain view in the same place hands the gesture back, and ui gets it
// all. A wheel and a gesture that begin over drawn content afterwards are
// ui's again.
func TestHostOwnsItsGesture(t *testing.T) {
	type res struct {
		err                 error
		noEvents            bool
		toUI, handedBack    []string
		wheelAfter, gesture int
	}
	var r res
	steps := []struct {
		x, y       float64
		dy         int32
		phase, mom uintptr
	}{
		{150, 150, 0, cgMayBegin, 0},
		{150, 150, -20, cgBegan, 0},
		{150, 150, -20, cgChanged, 0},
		{30, 30, -20, cgChanged, 0}, // the pointer is over drawn content now
		{30, 30, 0, cgEnded, 0},
		{30, 30, -15, 0, cgMomBegin},
		{30, 30, -10, 0, cgMomContinue},
		{30, 30, 0, 0, cgMomEnd},
	}
	onMain(func() {
		w, err := Open(Options{Title: "native scroll", Width: 400, Height: 300, Hidden: true})
		if err != nil {
			r.err = err
			return
		}
		defer w.Close()
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		objc.F.Reset().Arg(uintptr(w.win)).Arg(uintptr(selSetFrameOrigin)).ArgPoint(0, 0).Call(objc.MsgSend)
		s := w.scale
		rig := &hostRig{w: w}
		// deliver is one event through the event loop (scrollEvent, as
		// handle does), then to the view AppKit gives it to.
		deliver := func(ev, to objc.ID) {
			w.scrollEvent(ev, 1)
			objc.Send1(to, selScrollWheel, uintptr(ev))
			w.hosts.scrollDelivered()
		}
		// gesture runs the steps over a host showing view v, AppKit keeping
		// them with v, and returns what reached ui.
		gesture := func(v objc.ID) (got []string, ok bool) {
			id, err := w.NewHost(HostView, HostOptions{View: uintptr(v)})
			objc.Send(v, selReleaseObj)
			if err != nil {
				return nil, false
			}
			defer w.ReleaseHost(id)
			w.SetHosts([]HostRegion{{ID: id, Rect: pxRect(s, 100, 100, 300, 200), Clip: pxRect(s, 100, 100, 300, 200)}})
			w.FlushShell()
			w.q.reset()
			for _, st := range steps {
				ev := rig.gesture(st.x, st.y, st.dy, st.phase, st.mom)
				if ev == 0 {
					return nil, false
				}
				deliver(ev, v)
			}
			for _, e := range w.q.ev {
				if e.Kind == EvScroll {
					got = append(got, fmt.Sprintf("dy %v at (%v, %v)", e.DY, e.X/s, e.Y/s))
				}
			}
			return got, true
		}
		sv := newViewOf(objc.Cls("NSScrollView"), 0, 0, 200, 100)
		doc := newViewOf(nsViewClass(), 0, 0, 200, 2000)
		objc.Send1(sv, objc.Sel("setDocumentView:"), uintptr(doc))
		objc.Send(doc, selReleaseObj)
		objc.Send1(sv, objc.Sel("setHasVerticalScroller:"), 1)
		var ok bool
		if r.toUI, ok = gesture(sv); !ok {
			r.noEvents = true
			return
		}
		if r.handedBack, ok = gesture(newViewOf(nsViewClass(), 0, 0, 200, 100)); !ok {
			r.noEvents = true
			return
		}
		w.SetHosts(nil)
		w.FlushShell()
		w.q.reset()
		deliver(rig.scroll(30, 30, -3), w.view)
		r.wheelAfter = len(w.q.ev)
		w.q.reset()
		deliver(rig.gesture(30, 30, -20, cgBegan, 0), w.view)
		deliver(rig.gesture(150, 150, -20, cgChanged, 0), w.view)
		r.gesture = len(w.q.ev)
	})
	switch {
	case r.err != nil:
		t.Skipf("cannot open a window here: %v", r.err)
	case r.noEvents:
		t.Skip("no scroll events can be made here")
	}
	if len(r.toUI) != 0 {
		t.Errorf("the NSScrollView's gesture also reached ui: %v", r.toUI)
	}
	if len(r.handedBack) != len(steps) {
		t.Errorf("a plain view's gesture reached ui as %v: want all %d events, handed back", r.handedBack, len(steps))
	}
	if r.wheelAfter != 1 || r.gesture != 2 {
		t.Errorf("afterwards: a wheel over drawn content queued %d events (want 1), a drawn gesture %d (want 2)", r.wheelAfter, r.gesture)
	}
}

// The mark that keeps one delivery of a scroll from being queued twice lasts
// for that delivery only: the same event delivered again (reposted) is
// queued again.
func TestScrollRedeliveredIsQueuedAgain(t *testing.T) {
	var err error
	var noScroll bool
	var first, second int
	onMain(func() {
		rig, e := openHostRig()
		if e != nil {
			err = e
			return
		}
		w := rig.w
		defer w.Close()
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		ev := rig.scroll(30, 30, -3)
		if ev == 0 {
			noScroll = true
			return
		}
		w.q.reset()
		w.handle(ev) // the event loop: queued, then sendEvent:
		first = len(w.q.ev)
		w.q.reset()                                     // the frame drains it
		objc.Send1(w.view, selScrollWheel, uintptr(ev)) // AppKit delivers it again
		second = len(w.q.ev)
	})
	switch {
	case err != nil:
		t.Skipf("cannot open a window here: %v", err)
	case noScroll:
		t.Skip("no scroll events can be made here")
	}
	if first != 1 || second != 1 {
		t.Errorf("first delivery queued %d, the second %d: want 1 and 1", first, second)
	}
}
