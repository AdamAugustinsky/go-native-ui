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
		if v, ok := glassVariant(m.slots[0].view); ok {
			r.sideVariant, r.wantVariant = v, v
			if want, ok := splitVariant(0); ok {
				r.wantVariant = want
			}
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

// With the private API off at run time (Options.PublicAPIOnly) or left out
// of the build (gnui_publicapi), the sidebar and the inspector are plain
// public glass, regular style, with no private variant set, and the backdrop
// glass does not take the window's private corner radius.
func TestMaterialsWithoutPrivateAPI(t *testing.T) {
	var err error
	var glass, readable bool
	var styles [2]int64
	var got, plain [2]int64
	var radius, privRadius float64
	var backdrop bool
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
		if glass = rig.glass; !glass {
			return
		}
		w.publicOnly, w.mat.rtTest = true, -1
		r := [4]float32{0, 0, 200, 400}
		w.SetMaterials([]MaterialRegion{{Area: AreaSidebar, Rect: r}, {Area: AreaInspector, Rect: r}})
		w.FlushShell()
		fresh := newViewOf(clsGlass(), 0, 0, 10, 10)
		objc.Send1(fresh, selSetGlassStyle, 0) // regular, as the shell sets it
		for i := range 2 {
			v := w.mat.slots[i].view
			styles[i] = int64(objc.Send(v, objc.Sel("style")))
			var ok bool
			if got[i], ok = glassVariant(v); ok {
				plain[i], readable = func() int64 { p, _ := glassVariant(fresh); return p }(), true
			}
		}
		objc.Send(fresh, selReleaseObj)

		bw, e := Open(Options{Title: "backdrop", Width: 300, Height: 200, Hidden: true, Titlebar: 46, Backdrop: BackdropGlass})
		if e != nil || bw.effect == 0 {
			if bw != nil {
				bw.Close()
			}
			return
		}
		defer bw.Close()
		viewRadius := func() float64 { return objc.SendF64(bw.effect, objc.Sel("cornerRadius")) }
		backdrop, privRadius = true, viewRadius()
		bw.publicOnly = true
		bw.fitBackdrop()
		radius = viewRadius()
	})
	switch {
	case err != nil:
		t.Skipf("cannot open a window here: %v", err)
	case !glass:
		t.Skip("no Liquid Glass here")
	}
	if styles != [2]int64{} {
		t.Errorf("sidebar and inspector glass styles %v without private API, want regular (0)", styles)
	}
	if readable && got != plain {
		t.Errorf("sidebar and inspector glass variants %v without private API, want the public default %v", got, plain)
	}
	if backdrop && radius != 0 {
		t.Errorf("the backdrop glass took the corner radius %v without private API (%v with it)", radius, privRadius)
	}
}

// A toolbar band's bar (AreaTitlebar) over the whole titlebar leaves the
// window buttons above it: a click at the close button's centre hits the
// button, and SystemButtons still reports them.
func TestTitlebarRegionLeavesWindowButtons(t *testing.T) {
	var err error
	var hitButton bool
	var sys [4]float32
	onMain(func() {
		w, e := Open(Options{Title: "titlebar region", Width: 600, Height: 400, Hidden: true, Materials: true,
			Titlebar: 52, TrafficLights: [2]float32{26, 26}})
		if e != nil {
			err = e
			return
		}
		defer w.Close()
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		s := w.scale
		w.SetMaterials([]MaterialRegion{{Area: AreaTitlebar, Rect: [4]float32{0, 0, 600 * s, 52 * s}, Solid: 0xFFFFFFFF}})
		w.FlushShell()
		w.layoutChrome()
		sys = w.SystemButtons()
		close := objc.Send1(w.win, selStdButton, 0)
		b := objc.SendRect(close, selBounds)
		objc.F.Reset().Arg(uintptr(close)).Arg(uintptr(selConvertPointToView)).ArgPoint(b.W/2, b.H/2).Arg(0).Call(objc.MsgSend)
		x, y := objc.F.F64(0), objc.F.F64(1)
		frame := objc.Send(w.content, selSuperview)
		hit := objc.ID(objc.F.Reset().Arg(uintptr(frame)).Arg(uintptr(selHitTest)).ArgPoint(x, y).Call(objc.MsgSend).R0())
		for v := hit; v != 0; v = objc.Send(v, selSuperview) {
			hitButton = hitButton || v == close
		}
	})
	if err != nil {
		t.Skipf("cannot open a window here: %v", err)
	}
	if !hitButton {
		t.Error("a click at the close button's centre does not reach it under the toolbar band's bar")
	}
	if sys[2] <= sys[0] || sys[3] <= sys[1] {
		t.Errorf("SystemButtons %v with a titlebar region: want the buttons' rectangle", sys)
	}
}

// A slot whose region changes area gets that area's configuration, not the
// one its view was made with: a menu that becomes a HUD shows the HUD
// material, a sidebar that becomes an inspector the inspector's glass.
func TestMaterialAreaChangeReconfigures(t *testing.T) {
	var err error
	var mat int64
	var glass bool
	var variant, want int64
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
		w.mat.rtTest = -1
		r := [4]float32{0, 0, 200, 200}
		w.SetMaterials([]MaterialRegion{{Area: AreaMenu, Rect: r}})
		w.FlushShell()
		w.SetMaterials([]MaterialRegion{{Area: AreaHUD, Rect: r}})
		w.FlushShell()
		mat = int64(objc.Send(w.mat.slots[0].view, objc.Sel("material")))

		var ok bool
		if want, ok = splitVariant(1); !rig.glass || !ok {
			return
		}
		glass = true
		w.SetMaterials([]MaterialRegion{{Area: AreaSidebar, Rect: r}})
		w.FlushShell()
		w.SetMaterials([]MaterialRegion{{Area: AreaInspector, Rect: r}})
		w.FlushShell()
		variant, _ = glassVariant(w.mat.slots[0].view)
	})
	if err != nil {
		t.Skipf("cannot open a window here: %v", err)
	}
	if mat != 13 {
		t.Errorf("a menu region that became a HUD shows material %d, want 13", mat)
	}
	if !glass {
		t.Log("no configurable Liquid Glass here: sidebar → inspector untested")
	} else if variant != want {
		t.Errorf("a sidebar region that became an inspector has glass variant %d, want %d", variant, want)
	}
}

// Corner radii that differ round only the corners that have one (layer
// maskedCorners), not all four with the largest; a single radius keeps the
// views' own corner radius.
func TestMaterialCorners(t *testing.T) {
	var err error
	var boxRadius, effectMasks, boxMasks, uniformMasks int64
	var boxLayerRadius float64
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
		w.mat.rtTest = -1
		s := w.scale
		p := func(x0, y0, x1, y1 float32) [4]float32 { return [4]float32{x0 * s, y0 * s, x1 * s, y1 * s} }
		w.SetMaterials([]MaterialRegion{
			{Area: AreaTitlebar, Rect: p(20, 60, 180, 220), Radii: p(0, 40, 0, 0), Solid: 0xFF0000FF}, // top right
			{Area: AreaMenu, Rect: p(220, 60, 380, 220), Radii: p(40, 0, 0, 0)},                       // top left
			{Area: AreaPopover, Rect: p(420, 60, 580, 220), Radii: p(12, 12, 12, 12)},
		})
		w.FlushShell()
		box, effect, uniform := w.mat.slots[0].view, w.mat.slots[1].view, w.mat.slots[2].view
		masks := func(v objc.ID) int64 { return int64(objc.Send(objc.Send(v, selLayerOf), objc.Sel("maskedCorners"))) }
		boxRadius = int64(objc.SendF64(box, objc.Sel("cornerRadius")))
		boxLayerRadius = objc.SendF64(objc.Send(box, selLayerOf), objc.Sel("cornerRadius"))
		boxMasks, effectMasks, uniformMasks = masks(box), masks(effect), masks(uniform)
	})
	if err != nil {
		t.Skipf("cannot open a window here: %v", err)
	}
	// In a layer that is not flipped, the top corners are MaxY: top left 4, top right 8.
	if boxRadius != 0 || boxLayerRadius != 40 || boxMasks != 8 {
		t.Errorf("a solid box rounded top right only: box radius %d, layer radius %v, corners %b; want 0, 40, 1000", boxRadius, boxLayerRadius, boxMasks)
	}
	if effectMasks != 4 {
		t.Errorf("an effect view rounded top left only has corners %b, want 100", effectMasks)
	}
	if uniformMasks != allCorners {
		t.Errorf("one radius on all corners masks %b", uniformMasks)
	}
}

func TestCornerShape(t *testing.T) {
	for _, c := range []struct {
		radii [4]float32
		rect  [4]float32
		rad   float64
		mask  uintptr
	}{
		{[4]float32{}, [4]float32{0, 0, 100, 100}, 0, allCorners},
		{[4]float32{8, 8, 8, 8}, [4]float32{0, 0, 100, 100}, 4, allCorners},
		{[4]float32{0, 8, 8, 0}, [4]float32{0, 0, 100, 100}, 4, 8 | 2},   // a sidebar's inner edge
		{[4]float32{6, 0, 0, 10}, [4]float32{0, 0, 100, 100}, 5, 4 | 1},  // different radii: the largest
		{[4]float32{0, 0, 80, 80}, [4]float32{0, 0, 100, 40}, 10, 2 | 1}, // clamped to half the height
	} {
		r := MaterialRegion{Rect: c.rect, Radii: c.radii}
		if rad, mask := cornerShape(&r, 2); rad != c.rad || mask != c.mask {
			t.Errorf("radii %v in %v: radius %v, corners %b; want %v, %b", c.radii, c.rect, rad, mask, c.rad, c.mask)
		}
	}
}
