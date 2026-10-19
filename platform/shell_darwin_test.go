//go:build darwin

package platform

import (
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/objc"
)

// A window with the native shell: the backdrop sits under our view and the
// buttons above it in a container, the surface is told to be transparent, a
// button click arrives as EvCommand, and keeping a button's state allocates
// nothing.
func TestNativeShell(t *testing.T) {
	type result struct {
		opened                  bool
		err                     error
		backdrop                Backdrop
		transparent             bool
		order                   []string // the container's subviews, bottom to top
		viewIndex               int
		opaqueWindow            bool
		cmds                    []uint64
		allocs                  float64
		plusEnabled, hasButtons bool
		plusX                   float64
	}
	var r result
	onMain(func() {
		w, err := Open(Options{
			Title: "shell test", Width: 400, Height: 300, Hidden: true,
			Titlebar: 46, TrafficLights: [2]float32{21, 22}, Backdrop: BackdropGlass,
			Buttons: []NativeButton{
				{ID: 2, Symbol: "command", Label: "Commands", X: -57, Y: 23},
				{ID: 7, Symbol: "plus", Label: "New", X: -23, Y: 23, Style: ButtonGlass},
			},
		})
		if err != nil {
			r.err = err
			return
		}
		defer w.Close()
		r.opened = true
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		r.backdrop, r.transparent, r.hasButtons = w.Backdrop(), w.Native().Transparent, w.HasButtons()
		subs := objc.Send(w.content, objc.Sel("subviews"))
		for i := 0; i < int(objc.Send(subs, objc.Sel("count"))); i++ {
			v := objc.Send1(subs, objc.Sel("objectAtIndex:"), uintptr(i))
			if v == w.view {
				r.viewIndex = i
			}
			r.order = append(r.order, string(objc.AppendUTF8(nil, objc.Send(v, objc.Sel("className")))))
		}
		r.opaqueWindow = objc.SendBool(w.win, objc.Sel("isOpaque"))
		plus := w.buttons[1].obj
		r.plusX = objc.SendRect(plus, selFrame).X

		w.q.reset()
		delegate := objc.Send(w.win, objc.Sel("delegate"))
		objc.Send1(delegate, selButtonAction, uintptr(w.buttons[1].obj)) // a click on +
		objc.Send1(delegate, selButtonAction, uintptr(w.buttons[0].obj)) // and on ⌘
		for _, e := range w.q.ev {
			if e.Kind == EvCommand {
				r.cmds = append(r.cmds, e.Tag)
			}
		}
		w.SetButtonEnabled(7, false)
		r.allocs = testing.AllocsPerRun(100, func() { w.SetButtonEnabled(7, false) })
		r.plusEnabled = objc.SendBool(plus, objc.Sel("isEnabled"))
	})
	if r.err != nil || !r.opened {
		t.Skipf("cannot open a window here: %v", r.err)
	}
	if r.backdrop == BackdropNone || !r.transparent || r.opaqueWindow {
		t.Errorf("backdrop %d, surface transparent %v, window opaque %v: want a backdrop, a transparent surface, a non-opaque window", r.backdrop, r.transparent, r.opaqueWindow)
	}
	if len(r.order) != 4 || r.viewIndex != 1 || !r.hasButtons {
		t.Errorf("container holds %v with our view at %d: want the effect view, ours, then the two buttons", r.order, r.viewIndex)
	}
	if r.plusX != 400-23-14 {
		t.Errorf("+ at x %v, want %v (23 pt from the right edge to its centre)", r.plusX, 400-23-14)
	}
	if len(r.cmds) != 2 || r.cmds[0] != 7 || r.cmds[1] != 2 {
		t.Errorf("commands %v, want [7 2]", r.cmds)
	}
	if r.allocs != 0 {
		t.Errorf("an unchanged SetButtonEnabled allocates %.1f times", r.allocs)
	}
	if r.plusEnabled {
		t.Error("SetButtonEnabled(false) left + enabled")
	}
}

// weakRefs watches objects through the runtime's zeroing weak references, so
// a test can tell that they were freed.
type weakRefs struct{ slots []uintptr }

var fnInitWeak, fnLoadWeakRetained, fnDestroyWeak uintptr

func newWeakRefs(t *testing.T, objs ...objc.ID) *weakRefs {
	if fnInitWeak == 0 {
		h, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			t.Fatal(err)
		}
		fnInitWeak, _ = purego.Dlsym(h, "objc_initWeak")
		fnLoadWeakRetained, _ = purego.Dlsym(h, "objc_loadWeakRetained")
		fnDestroyWeak, _ = purego.Dlsym(h, "objc_destroyWeak")
	}
	r := &weakRefs{slots: make([]uintptr, len(objs))} // heap memory, which does not move
	for i, o := range objs {
		objc.F.Call2(fnInitWeak, uintptr(unsafe.Pointer(&r.slots[i])), uintptr(o))
	}
	return r
}

// alive is how many of the objects still exist; it ends the references.
func (r *weakRefs) alive() int {
	n := 0
	for i := range r.slots {
		p := uintptr(unsafe.Pointer(&r.slots[i]))
		if o := objc.ID(objc.F.Call1(fnLoadWeakRetained, p)); o != 0 {
			n++
			objc.Release(o)
		}
		objc.F.Call1(fnDestroyWeak, p)
	}
	return n
}

// Close frees the shell's native objects (the effect view, the container and
// the buttons) in every open and close cycle, and the framework's own
// counters stay level.
func TestShellFreedOnClose(t *testing.T) {
	var alive [3]int
	var opened bool
	var before, after objc.Counts
	onMain(func() {
		before = objc.Leaks()
		for cycle := range alive {
			pool := objc.F.Call0(objc.PoolPush)
			w, err := Open(Options{
				Title: "shell teardown", Width: 300, Height: 200, Hidden: true,
				Titlebar: 46, TrafficLights: [2]float32{21, 22}, Backdrop: BackdropGlass,
				Buttons: []NativeButton{{ID: 1, Symbol: "plus", X: -23, Y: 23}, {ID: 2, Symbol: "command", X: -57, Y: 23, Style: ButtonGlass}},
			})
			if err != nil {
				objc.F.Call1(objc.PoolPop, pool)
				return
			}
			opened = true
			refs := newWeakRefs(t, w.effect, w.content, w.buttons[0].obj, w.buttons[1].obj)
			w.Close()
			objc.F.Call1(objc.PoolPop, pool)
			alive[cycle] = refs.alive()
		}
		after = objc.Leaks()
	})
	if !opened {
		t.Skip("cannot open a window here")
	}
	for i, n := range alive {
		if n != 0 {
			t.Errorf("cycle %d: %d of the effect view, container and two buttons outlive Close", i, n)
		}
	}
	if after.Delegates != before.Delegates || after.Blocks != before.Blocks || after.Observers != before.Observers {
		t.Errorf("leak counters moved over 3 cycles: %+v → %+v", before, after)
	}
}

// Liquid Glass follows the window's corners: square in full screen, the
// window's radius again once out of it (or when entering failed).
func TestGlassCornersFullScreen(t *testing.T) {
	var opened, glass, responds bool
	var r0, rFull, rBack, rFail float64
	onMain(func() {
		w, err := Open(Options{Title: "glass corners", Width: 300, Height: 200, Hidden: true, Backdrop: BackdropGlass})
		if err != nil {
			return
		}
		defer w.Close()
		opened, glass = true, w.Backdrop() == BackdropGlass
		if !glass {
			return
		}
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		radius := func() float64 { return objc.SendF64(w.effect, objc.Sel("cornerRadius")) }
		d := objc.Send(w.win, objc.Sel("delegate"))
		responds = objc.SendBool1(d, selRespondsTo, uintptr(selWillEnterFull)) && objc.SendBool1(d, selRespondsTo, uintptr(selDidExitFull)) &&
			objc.SendBool1(d, selRespondsTo, uintptr(selDidFailFull))
		if !responds {
			return
		}
		r0 = radius()
		objc.Send1(d, selWillEnterFull, 0)
		rFull = radius()
		objc.Send1(d, selDidExitFull, 0)
		rBack = radius()
		objc.Send1(d, selWillEnterFull, 0)
		objc.Send1(d, selDidFailFull, 0)
		rFail = radius()
	})
	switch {
	case !opened:
		t.Skip("cannot open a window here")
	case !glass:
		t.Skip("no Liquid Glass before macOS 26")
	case !responds:
		t.Fatal("the window delegate takes no full-screen notifications")
	}
	if r0 <= 0 || rFull != 0 || rBack != r0 || rFail != r0 {
		t.Errorf("glass radius %v windowed, %v in full screen, %v after, %v after a failed entry: want >0, 0, %v, %v", r0, rFull, rBack, rFail, r0, r0)
	}
}

// answerYes makes the BOOL method sel of class cls answer YES in this
// process (the IMP of -[NSObject allowsWeakReference], a BOOL method with no
// arguments that is YES for a live object) until restore; nil if it cannot.
// Main thread, AppKit loaded. Nothing outside the process changes.
func answerYes(cls, sel string) (restore func()) {
	h, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil
	}
	getMethod, _ := purego.Dlsym(h, "class_getInstanceMethod")
	getImp, _ := purego.Dlsym(h, "method_getImplementation")
	setImp, _ := purego.Dlsym(h, "method_setImplementation")
	c, nsobj := objc.Cls(cls), objc.Cls("NSObject")
	s, yesSel := objc.Sel(sel), objc.Sel("allowsWeakReference")
	m := objc.F.Call2(getMethod, uintptr(c), uintptr(s))
	yes := objc.F.Call1(getImp, objc.F.Call2(getMethod, uintptr(nsobj), uintptr(yesSel)))
	if m == 0 || yes == 0 {
		return nil
	}
	old := objc.F.Call2(setImp, m, yes)
	return func() { objc.F.Call2(setImp, m, old) }
}

// The titlebar buttons never take the keyboard, as AppKit's titlebar
// controls do not: with full keyboard access on (NSApplication's
// isFullKeyboardAccessEnabled answering YES in this process; the system's
// setting is not touched) the key-view loop
// skips them, and a click on one leaves the first responder, and an input
// method's composition in our view, alone.
func TestButtonsRefuseFocus(t *testing.T) {
	var opened, fka, accepts, hitButton bool
	var loopHit []string
	var firstAfterClick objc.ID
	var view objc.ID
	var marked string
	var cmds []uint64
	onMain(func() {
		w, err := Open(Options{
			Title: "button focus", Width: 300, Height: 200, Hidden: true,
			Titlebar: 46, TrafficLights: [2]float32{21, 22}, Backdrop: BackdropGlass,
			Buttons: []NativeButton{{ID: 2, Symbol: "command", X: -57, Y: 23}, {ID: 7, Symbol: "plus", X: -23, Y: 23, Style: ButtonGlass}},
		})
		if err != nil {
			return
		}
		defer w.Close()
		opened, view = true, w.view
		restore := answerYes("NSApplication", "isFullKeyboardAccessEnabled")
		if restore == nil {
			return
		}
		defer restore()
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		fka = objc.SendBool(w.app, objc.Sel("isFullKeyboardAccessEnabled"))
		for _, b := range w.buttons {
			accepts = accepts || objc.SendBool(b.obj, selAcceptsFirstResp) || objc.SendBool(b.obj, objc.Sel("canBecomeKeyView"))
		}
		// Tab through the key-view loop from our view.
		objc.Send1(w.win, selMakeFirstResp, uintptr(w.view))
		for i := 0; i < 4; i++ {
			objc.Send1(w.win, objc.Sel("selectNextKeyView:"), 0)
			if fr := objc.Send(w.win, objc.Sel("firstResponder")); objc.SendBool1(fr, objc.Sel("isKindOfClass:"), uintptr(objc.Cls("NSButton"))) {
				loopHit = append(loopHit, "next")
			}
		}
		objc.Send1(w.win, selMakeFirstResp, uintptr(w.view))

		// Compose in our view, then click + (press and release through
		// sendEvent: the button's tracking loop takes the posted release).
		w.q.reset()
		r := &imeRig{w: w, view: w.view}
		r.setMarked("にほ", 2, 0)
		f := objc.SendRect(w.buttons[1].obj, selFrame)
		x, y := f.X+f.W/2, f.Y+f.H/2
		num := objc.Send(w.win, objc.Sel("windowNumber"))
		uptime := objc.SendF64(objc.Send(objc.Cls("NSProcessInfo"), objc.Sel("processInfo")), objc.Sel("systemUptime"))
		evCls, evSel := objc.Cls("NSEvent"), objc.Sel("mouseEventWithType:location:modifierFlags:timestamp:windowNumber:context:eventNumber:clickCount:pressure:")
		mouse := func(typ uintptr) objc.ID { // (objc.Sel and Cls must not run while the frame is being built)
			return objc.ID(objc.F.Reset().Arg(uintptr(evCls)).Arg(uintptr(evSel)).
				Arg(typ).ArgPoint(x, y).Arg(0).ArgF64(uptime).Arg(uintptr(num)).Arg(0).Arg(0).Arg(1).ArgF32(1).Call(objc.MsgSend).R0())
		}
		// The click, as NSWindow handles a left mouse down: the view hit
		// becomes first responder if it accepts to, then gets mouseDown:,
		// whose tracking loop takes the release posted before. (The window's
		// own sendEvent: drops clicks while the app is not active, and the
		// test must not activate it and take the user's focus.)
		objc.Send2(w.app, selPostEvent, uintptr(mouse(2)), 0) // NSEventTypeLeftMouseUp
		frame := objc.Send(w.content, selSuperview)
		hit := objc.ID(objc.F.Reset().Arg(uintptr(frame)).Arg(uintptr(selHitTest)).ArgPoint(x, y).Call(objc.MsgSend).R0())
		hitButton = hit == w.buttons[1].obj
		if objc.SendBool(hit, selAcceptsFirstResp) {
			objc.Send1(w.win, selMakeFirstResp, uintptr(hit))
		}
		objc.Send1(hit, objc.Sel("mouseDown:"), uintptr(mouse(1)))
		firstAfterClick = objc.Send(w.win, objc.Sel("firstResponder"))
		marked = string(w.marked)
		for _, e := range w.q.ev {
			if e.Kind == EvCommand {
				cmds = append(cmds, e.Tag)
			}
		}
		// Whatever the tracking loop left in the queue goes.
		for objc.Send4(w.app, selNextEvent, ^uintptr(0), uintptr(w.distantPast), uintptr(defaultMode), 1) != 0 {
		}
	})
	if !opened {
		t.Skip("cannot open a window here")
	}
	if !fka {
		t.Fatal("full keyboard access did not turn on for this process: the test proves nothing")
	}
	if !hitButton {
		t.Fatal("the click's point does not hit +")
	}
	if accepts {
		t.Error("a titlebar button accepts the first responder with full keyboard access on")
	}
	if len(loopHit) > 0 {
		t.Errorf("Tab focused a titlebar button %d times", len(loopHit))
	}
	if firstAfterClick != view {
		t.Errorf("after a click on + the first responder is %#x, want our view %#x", firstAfterClick, view)
	}
	if marked != "にほ" {
		t.Errorf("the composition is %q after a click on +, want it kept", marked)
	}
	if len(cmds) != 1 || cmds[0] != 7 {
		t.Errorf("click on + delivered %v, want [7] (the click must still work)", cmds)
	}
}

// The titlebar buttons refuse first responder but stay what VoiceOver
// needs: accessibility elements with the role of a button, their labels and
// a press action that clicks them, reachable from the window's accessibility
// children. An NSButton's element is its cell (the NSButton itself is
// ignored, AXUnknown, as any NSButton is on 27.2).
func TestButtonsAccessible(t *testing.T) {
	type button struct {
		element, inTree bool
		role, label     string
		actions         []string
	}
	var opened bool
	var got [2]button
	var cmds []uint64
	onMain(func() {
		w, err := Open(Options{
			Title: "button accessibility", Width: 300, Height: 200, Hidden: true,
			Titlebar: 46, TrafficLights: [2]float32{21, 22}, Backdrop: BackdropGlass,
			Buttons: []NativeButton{{ID: 2, Symbol: "command", Label: "Commands", X: -57, Y: 23}, {ID: 7, Symbol: "plus", Label: "New Tab", X: -23, Y: 23, Style: ButtonGlass}},
		})
		if err != nil {
			return
		}
		defer w.Close()
		opened = true
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		str := func(id objc.ID) string {
			if id == 0 {
				return ""
			}
			return string(objc.AppendUTF8(nil, id))
		}
		selChildren, selCount, selAt := objc.Sel("accessibilityChildren"), objc.Sel("count"), objc.Sel("objectAtIndex:")
		var tree []objc.ID
		var walk func(e objc.ID, depth int)
		walk = func(e objc.ID, depth int) {
			kids := objc.Send(e, selChildren)
			for i := 0; kids != 0 && depth < 8 && i < int(objc.Send(kids, selCount)); i++ {
				k := objc.Send1(kids, selAt, uintptr(i))
				tree = append(tree, k)
				walk(k, depth+1)
			}
		}
		walk(w.win, 0)
		w.q.reset()
		for i, b := range w.buttons {
			g := &got[i]
			el := objc.Send(b.obj, objc.Sel("cell"))
			g.element = objc.SendBool(el, objc.Sel("isAccessibilityElement"))
			g.role = str(objc.Send(el, objc.Sel("accessibilityRole")))
			g.label = str(objc.Send(el, objc.Sel("accessibilityLabel")))
			names := objc.Send(el, objc.Sel("accessibilityActionNames"))
			for j := 0; names != 0 && j < int(objc.Send(names, selCount)); j++ {
				g.actions = append(g.actions, str(objc.Send1(names, selAt, uintptr(j))))
			}
			for _, e := range tree {
				g.inTree = g.inTree || e == el
			}
			objc.SendBool(el, objc.Sel("accessibilityPerformPress"))
		}
		for _, e := range w.q.ev {
			if e.Kind == EvCommand {
				cmds = append(cmds, e.Tag)
			}
		}
	})
	if !opened {
		t.Skip("cannot open a window here")
	}
	for i, want := range []string{"Commands", "New Tab"} {
		g := got[i]
		press := false
		for _, a := range g.actions {
			press = press || a == "AXPress"
		}
		if !g.element || g.role != "AXButton" || g.label != want || !press || !g.inTree {
			t.Errorf("%s: element %v, role %q, label %q, actions %v, in the window's accessibility children %v: want an AXButton labelled %q with AXPress, in the tree",
				want, g.element, g.role, g.label, g.actions, g.inTree, want)
		}
	}
	if len(cmds) != 2 || cmds[0] != 2 || cmds[1] != 7 {
		t.Errorf("AXPress on ⌘ and + delivered %v, want [2 7]", cmds)
	}
}
