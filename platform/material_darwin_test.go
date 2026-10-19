//go:build darwin

package platform

import (
	"testing"

	"gonativeui/internal/objc"
)

// className is v's class name.
func className(v objc.ID) string {
	return string(objc.AppendUTF8(nil, objc.Send(v, objc.Sel("className"))))
}

// subviewClasses are v's subviews' class names, bottom to top.
func subviewClasses(v objc.ID) []string {
	var out []string
	subs := objc.Send(v, selSubviews)
	for i := 0; i < int(objc.Send(subs, objc.Sel("count"))); i++ {
		out = append(out, className(objc.Send1(subs, objc.Sel("objectAtIndex:"), uintptr(i))))
	}
	return out
}

// The sidebar's and the inspector's glass configurations listed for this
// macOS are the ones AppKit's NSSplitViewController gives its own glass.
func TestSplitGlassMatchesAppKit(t *testing.T) {
	var known, read [2]glassConfig
	var listed bool
	var major int
	onMain(func() {
		w, err := Open(Options{Title: "split glass", Width: 300, Height: 200, Hidden: true}) // AppKit set up as for an app
		if err != nil {
			return
		}
		defer w.Close()
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		major = osMajor()
		known, listed = knownSplitGlass[major]
		read = readSplitGlass()
	})
	if !read[0].ok || !read[1].ok {
		t.Skipf("macOS %d: AppKit's split view has no configurable glass", major)
	}
	if !listed {
		t.Skipf("macOS %d is not listed: AppKit's %+v is read at run time", major, read)
	}
	if known != read {
		t.Errorf("macOS %d: knownSplitGlass %+v, AppKit uses %+v", major, known, read)
	}
}

// matRig is a hidden 600×400 window with Options.Materials and Finder-like
// regions: a sidebar, the toolbar band's bar and two toolbar capsules.
type matRig struct {
	w     *Window
	s     float32
	regs  []MaterialRegion
	base  ShellCounts
	glass bool // the system has NSGlassEffectView and its container
}

func openMatRig() (*matRig, error) {
	base := ShellObjects()
	w, err := Open(Options{Title: "materials", Width: 600, Height: 400, Hidden: true, Materials: true})
	if err != nil {
		return nil, err
	}
	s := w.scale
	px := func(x0, y0, x1, y1 float32) [4]float32 { return [4]float32{x0 * s, y0 * s, x1 * s, y1 * s} }
	r := &matRig{w: w, s: s, base: base, glass: clsGlass() != 0 && clsGlassContainer() != 0}
	r.regs = []MaterialRegion{
		{Area: AreaSidebar, Token: 1, Rect: px(0, 0, 200, 400), Solid: 0xECECECFF},
		{Area: AreaTitlebar, Token: 2, Rect: px(200, 0, 600, 52), Solid: 0xF7F7F7FF},
		{Area: AreaToolbarCapsule, Token: 3, Rect: px(220, 8, 300, 44), Radii: px(18, 18, 18, 18)},
		{Area: AreaToolbarCapsule, Token: 3, Rect: px(310, 8, 346, 44), Radii: px(18, 18, 18, 18)},
	}
	return r, nil
}

func frameOf(v objc.ID) [4]float64 {
	f := objc.SendRect(v, selFrame)
	return [4]float64{f.X, f.Y, f.W, f.H}
}

func srgbOf(c objc.ID) [3]float64 {
	c = objc.Send1(c, objc.Sel("colorUsingColorSpace:"), uintptr(objc.Send(objc.Cls("NSColorSpace"), objc.Sel("sRGBColorSpace"))))
	return [3]float64{objc.SendF64(c, objc.Sel("redComponent")), objc.SendF64(c, objc.Sel("greenComponent")), objc.SendF64(c, objc.Sel("blueComponent"))}
}

// Material regions become native views below the app's view only at the
// next apply (in step with a present), in region order with the capsules in
// one glass container above them; an unchanged list costs nothing, a moved
// region keeps its view, a dropped one is freed, and Close frees them all.
func TestMaterialsApply(t *testing.T) {
	type result struct {
		err                          error
		glass                        bool
		pendingBefore, pendingAfter  bool
		before, after                []string
		inGlass                      []string
		sideFrame, capFrame, moved   [4]float64
		capRadius                    float64
		sideVariant, wantVariant     int64
		barFill                      [3]float64
		created                      int64
		allocs                       float64
		pendingSame, keptView, order bool
		droppedAlive, closedAlive    int
		appliedSame                  bool
		after2                       ShellCounts
		base                         ShellCounts
	}
	var r result
	onMain(func() {
		rig, err := openMatRig()
		if err != nil {
			r.err = err
			return
		}
		w := rig.w
		pool := objc.F.Call0(objc.PoolPush)
		r.glass, r.base = rig.glass, rig.base
		r.before = subviewClasses(w.content)
		w.mat.rtTest = -1 // the materials, whatever the system's Reduce Transparency (CI runners have it on)
		w.SetMaterials(rig.regs)
		r.pendingBefore = ShellPending()
		w.FlushShell()
		r.pendingAfter = ShellPending()
		r.after = subviewClasses(w.content)
		r.created = ShellObjects().Materials - rig.base.Materials
		m := &w.mat
		r.sideFrame = frameOf(m.slots[0].view)
		r.capFrame = frameOf(m.slots[2].view)
		if m.slots[2].kind == mvEffect { // before macOS 26: the radius is the layer's
			r.capRadius = objc.SendF64(objc.Send(m.slots[2].view, selLayerOf), objc.Sel("cornerRadius"))
		} else {
			r.capRadius = objc.SendF64(m.slots[2].view, objc.Sel("cornerRadius"))
		}
		if m.glass != 0 {
			r.inGlass = subviewClasses(m.glassContent)
		}
		if objc.SendBool1(m.slots[0].view, selRespondsTo, uintptr(selVariant)) {
			r.sideVariant, r.wantVariant = int64(objc.Send(m.slots[0].view, selVariant)), splitGlass()[0].variant
		}
		r.barFill = srgbOf(objc.Send(m.slots[1].view, objc.Sel("fillColor")))

		// Unchanged: nothing staged, nothing allocated.
		applied := m.applied
		r.allocs = testing.AllocsPerRun(100, func() { w.SetMaterials(rig.regs) })
		r.pendingSame = ShellPending()
		w.FlushShell()
		r.appliedSame = m.applied != applied

		// A capsule moves: same view, new frame, no reordering.
		capView := m.slots[3].view
		order := subviewClasses(w.content)
		rig.regs[3].Rect[0] += 10 * rig.s
		rig.regs[3].Rect[2] += 10 * rig.s
		w.SetMaterials(rig.regs)
		w.FlushShell()
		r.keptView = m.slots[3].view == capView
		r.moved = frameOf(capView)
		r.order = equalStrings(order, subviewClasses(w.content))

		// The last capsule goes: its view is freed.
		refs := newWeakRefs(t, capView)
		w.SetMaterials(rig.regs[:3])
		w.FlushShell()
		objc.F.Call1(objc.PoolPop, pool)
		r.droppedAlive = refs.alive()

		pool = objc.F.Call0(objc.PoolPush)
		views := []objc.ID{w.content}
		for i := 0; i < m.n; i++ {
			views = append(views, m.slots[i].view)
		}
		if m.glass != 0 {
			views = append(views, m.glass, m.glassContent)
		}
		refs = newWeakRefs(t, views...)
		w.Close()
		objc.F.Call1(objc.PoolPop, pool)
		r.closedAlive = refs.alive()
		r.after2 = ShellObjects()
	})
	if r.err != nil {
		t.Skipf("cannot open a window here: %v", r.err)
	}
	if !r.pendingBefore || r.pendingAfter {
		t.Errorf("ShellPending %v after SetMaterials, %v after FlushShell: want true, false", r.pendingBefore, r.pendingAfter)
	}
	if !equalStrings(r.before, []string{"GNUIView"}) {
		t.Errorf("before the apply the container holds %v, want only our view", r.before)
	}
	want := []string{"NSVisualEffectView", "NSBox", "NSVisualEffectView", "NSVisualEffectView", "GNUIView"}
	if r.glass {
		want = []string{"NSGlassEffectView", "NSBox", "NSGlassEffectContainerView", "GNUIView"}
		if !equalStrings(r.inGlass, []string{"NSGlassEffectView", "NSGlassEffectView"}) {
			t.Errorf("the glass container holds %v, want the two capsules", r.inGlass)
		}
		if r.created != 5 {
			t.Errorf("%d material objects made, want 5 (4 views and the glass container)", r.created)
		}
	}
	if !equalStrings(r.after, want) {
		t.Errorf("container holds %v, want %v", r.after, want)
	}
	if r.sideFrame != [4]float64{0, 0, 200, 400} || r.capFrame != [4]float64{220, 8, 80, 36} || r.capRadius != 18 {
		t.Errorf("sidebar frame %v, capsule frame %v radius %v: want 0,0 200×400 and 220,8 80×36 radius 18 (points, flipped)", r.sideFrame, r.capFrame, r.capRadius)
	}
	if r.sideVariant != r.wantVariant {
		t.Errorf("sidebar glass variant %d, want AppKit's %d", r.sideVariant, r.wantVariant)
	}
	if d := r.barFill[0] - 0xF7/255.0; d > 0.002 || d < -0.002 {
		t.Errorf("the toolbar band's bar is %v, want its Solid F7F7F7", r.barFill)
	}
	if r.allocs != 0 || r.pendingSame || r.appliedSame {
		t.Errorf("an unchanged SetMaterials allocates %.1f times, stages %v, applies %v: want 0, false, false", r.allocs, r.pendingSame, r.appliedSame)
	}
	if !r.keptView || r.moved != [4]float64{320, 8, 36, 36} || !r.order {
		t.Errorf("moved capsule: same view %v, frame %v, order kept %v: want true, 320,8 36×36, true", r.keptView, r.moved, r.order)
	}
	if r.droppedAlive != 0 {
		t.Error("a dropped region's view outlives the apply")
	}
	if r.closedAlive != 0 {
		t.Errorf("%d of the container and the material views outlive Close", r.closedAlive)
	}
	if r.after2.Materials != r.base.Materials {
		t.Errorf("material views alive: %d before, %d after Close", r.base.Materials, r.after2.Materials)
	}
}

// Under Reduce Transparency every material is an opaque box of its Solid
// (the window background without one), and the glass comes back when it is
// off again.
func TestMaterialsReduceTransparency(t *testing.T) {
	var err error
	var kinds, back []string
	var side [3]float64
	var capIsWindowBG bool
	var glass bool
	onMain(func() {
		rig, e := openMatRig()
		if e != nil {
			err = e
			return
		}
		w := rig.w
		defer w.Close()
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		glass = rig.glass
		w.mat.rtTest = -1
		w.SetMaterials(rig.regs)
		w.FlushShell()
		w.mat.rtTest = 1
		w.SetMaterials(rig.regs) // the same list: Reduce Transparency changed
		w.FlushShell()
		kinds = subviewClasses(w.content)
		side = srgbOf(objc.Send(w.mat.slots[0].view, objc.Sel("fillColor")))
		capIsWindowBG = objc.SendBool1(objc.Send(w.mat.slots[2].view, objc.Sel("fillColor")), objc.Sel("isEqual:"), uintptr(objc.Send(objc.Cls("NSColor"), selWindowBGColor)))
		w.mat.rtTest = -1
		w.SetMaterials(rig.regs)
		w.FlushShell()
		back = subviewClasses(w.content)
	})
	if err != nil {
		t.Skipf("cannot open a window here: %v", err)
	}
	wantKinds := []string{"NSBox", "NSBox", "NSBox", "NSBox", "GNUIView"}
	if glass { // the capsules' glass container stays, empty
		wantKinds = []string{"NSBox", "NSBox", "NSBox", "NSBox", "NSGlassEffectContainerView", "GNUIView"}
	}
	if !equalStrings(kinds, wantKinds) {
		t.Errorf("with Reduce Transparency the container holds %v, want %v", kinds, wantKinds)
	}
	if d := side[0] - 0xEC/255.0; d > 0.002 || d < -0.002 {
		t.Errorf("the sidebar's box is %v, want its Solid ECECEC", side)
	}
	if !capIsWindowBG {
		t.Error("a capsule without a Solid is not the window background")
	}
	if glass && (len(back) < 1 || back[0] != "NSGlassEffectView") {
		t.Errorf("after Reduce Transparency the container holds %v, want the glass back", back)
	}
}
