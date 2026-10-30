//go:build darwin

package platform

import (
	"os"

	"gonativeui/internal/objc"
)

// Material regions (N3a): native effect and glass views below the app's view,
// one per region, where the app leaves its pixels clear. Which view each area
// gets follows what AppKit itself uses on macOS 27.2 (D0,
// docs/design/liquid-glass-reference.md §6, the scene dumps):
//
//   - sidebar, inspector, composer and the window backdrop: NSGlassEffectView,
//     regular style (an NSVisualEffectView before macOS 26);
//   - toolbar capsules: NSGlassEffectView with radius half the height, all in
//     one NSGlassEffectContainerView (spacing 4) above the other materials, as
//     AppKit's toolbar groups its item platters;
//   - the toolbar band's scroll-edge bar (AreaTitlebar): an opaque fill of the
//     region's Solid colour under the capsules. AppKit's pocket there is a
//     near-opaque bar (≤ 5 % of neutral content shows through, D0 §4), and no
//     effect material within the window matched its colours (titlebar,
//     headerView, sheet, windowBackground, contentBackground and
//     underPageBackground were measured live, light and dark);
//   - menus, popovers and HUDs: NSVisualEffectView with the matching material,
//     blending within the window, following the window's active state.
//
// Under Reduce Transparency each view is an opaque NSBox of the region's
// Solid colour instead (the window background when it has none). Increase
// Contrast is AppKit's: the effect views draw it themselves.

var (
	selSetSpacing      = objc.SelZ("setSpacing:\x00")
	selSetContentViewV = objc.SelZ("setContentView:\x00")
	selRetainV         = objc.SelZ("retain\x00")
	selSetBoxType      = objc.SelZ("setBoxType:\x00")
	selSetBorderWidth  = objc.SelZ("setBorderWidth:\x00")
	selSetTitlePos     = objc.SelZ("setTitlePosition:\x00")
	selSetFillColor    = objc.SelZ("setFillColor:\x00")
	selColorSRGB       = objc.SelZ("colorWithSRGBRed:green:blue:alpha:\x00")
	selWindowBGColor   = objc.SelZ("windowBackgroundColor\x00")
	selSetInteractive  = objc.SelZ("setEffectIsInteractive:\x00")
	selSetMasksToBnds  = objc.SelZ("setMasksToBounds:\x00")
	selSetCornerCurve  = objc.SelZ("setCornerCurve:\x00")
	selLayerOf         = objc.SelZ("layer\x00")
	selSetMaskedCorner = objc.SelZ("setMaskedCorners:\x00")
)

// forceReduceTransparency (GNUI_REDUCE_TRANSPARENCY=1) shows every material
// as its opaque Solid, as Reduce Transparency does, in this process only: for
// captures of that state without changing the system's setting.
var forceReduceTransparency = os.Getenv("GNUI_REDUCE_TRANSPARENCY") == "1"

// matView is the kind of native view a material slot has.
type matView uint8

const (
	mvNone    matView = iota
	mvGlass           // NSGlassEffectView in the container
	mvEffect          // NSVisualEffectView in the container
	mvCapsule         // NSGlassEffectView in the capsules' glass container
	mvSolid           // NSBox: Reduce Transparency
)

// matSlot is one applied material region and its view.
type matSlot struct {
	r      MaterialRegion
	view   objc.ID // owned by its superview
	kind   matView
	hidden bool
	masked bool // the view's layer clips it to some corners (cornerShape)
}

// matState is the window's material regions: the list asked for (want) and
// what is applied (slots).
type matState struct {
	want  [MaxMaterials]MaterialRegion
	nWant int
	slots [MaxMaterials]matSlot
	n     int
	dirty bool // want differs from slots: apply at the next present
	rt    bool // Reduce Transparency as applied
	// rtTest overrides the system's Reduce Transparency in tests: 1 on, -1
	// off, 0 the system's.
	rtTest int8
	// The capsules' NSGlassEffectContainerView and its (flipped) content view.
	glass, glassContent objc.ID
	// applied counts applyMaterials calls that changed something (tests).
	applied int
}

// SetMaterials sets the window's material regions, in order from bottom to
// top (the toolbar capsules always above the others). The list is copied and
// compared with the last one: unchanged, nothing happens; changed, the
// differences are staged and applied in step with the next present
// (ApplyShell), or by FlushShell. Regions past MaxMaterials are ignored. Main
// thread; allocation-free.
func (w *Window) SetMaterials(r []MaterialRegion) {
	m := &w.mat
	if len(r) > MaxMaterials {
		r = r[:MaxMaterials]
	}
	rt := w.reduceTransparency()
	if rt == m.rt && equalRegions(m.want[:m.nWant], r) {
		return
	}
	copy(m.want[:], r)
	m.nWant = len(r)
	m.dirty = rt != m.rt || !m.matches()
	if m.dirty && len(r) > 0 {
		w.makeContainer(w.wPt, w.hPt)
	}
}

func equalRegions(a, b []MaterialRegion) bool {
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

// matches reports whether want is what the slots show.
func (m *matState) matches() bool {
	if m.nWant != m.n {
		return false
	}
	for i := 0; i < m.n; i++ {
		if m.slots[i].r != m.want[i] {
			return false
		}
	}
	return true
}

// reduceTransparency is the Reduce Transparency setting the materials follow.
func (w *Window) reduceTransparency() bool {
	switch {
	case w.mat.rtTest != 0:
		return w.mat.rtTest > 0
	case forceReduceTransparency:
		return true
	}
	var v SystemValues
	ReadSystemValues(&v)
	if v.Known {
		return v.ReduceTransparency
	}
	if matWorkspace == 0 { // not following the system appearance: ask AppKit
		matWorkspace = objc.Retain(objc.Send(objc.Cls("NSWorkspace"), objc.SelZ("sharedWorkspace\x00")))
	}
	return objc.SendBool(matWorkspace, selReduceTranspWS)
}

var (
	matWorkspace      objc.ID
	selReduceTranspWS = objc.SelZ("accessibilityDisplayShouldReduceTransparency\x00")
)

// viewFor is the kind of view region r gets.
func viewFor(r *MaterialRegion, rt bool) matView {
	switch {
	case rt || r.Area == AreaTitlebar:
		return mvSolid
	case r.Area == AreaToolbarCapsule && clsGlassContainer() != 0:
		return mvCapsule
	case (r.Area == AreaWindow || r.Area == AreaSidebar || r.Area == AreaInspector || r.Area == AreaComposer || r.Area == AreaToolbarCapsule) &&
		clsGlass() != 0:
		return mvGlass
	}
	return mvEffect
}

// The classes are looked up once: objc.Cls allocates.
var glassCls, glassContainerCls, effectCls, boxCls, colorCls objc.ID

func clsGlass() objc.ID {
	if glassCls == 0 {
		glassCls = objc.Cls("NSGlassEffectView")
	}
	return glassCls
}

func clsGlassContainer() objc.ID {
	if glassContainerCls == 0 {
		glassContainerCls = objc.Cls("NSGlassEffectContainerView")
	}
	return glassContainerCls
}

// configureGlass gives glass view v the configuration AppKit gives its own
// glass for the area (macOS 27.2, read from AppKit's views): the sidebar and
// the inspector of an NSSplitViewController use their own private glass
// variants with adaptive appearance 1; toolbar item platters and the composer
// are regular glass with interactive effects (effectIsInteractive). With
// private false (Window.privateAPI: Options.PublicAPIOnly or a build with
// the gnui_publicapi tag) the sidebar and inspector keep the public regular style.
func configureGlass(v objc.ID, a MaterialArea, private bool) {
	switch a {
	case AreaSidebar, AreaInspector:
		if private {
			setSplitGlass(v, a)
		}
	case AreaToolbarCapsule, AreaComposer:
		if objc.SendBool1(v, selRespondsTo, uintptr(selSetInteractive)) {
			objc.Send1(v, selSetInteractive, 1)
		}
	}
}

// effectMaterial is the NSVisualEffectMaterial and blending mode of an area
// shown by an NSVisualEffectView.
func effectMaterial(a MaterialArea) (material, blending uintptr) {
	switch a {
	case AreaWindow:
		return 21, 0 // underWindowBackground, behind the window
	case AreaSidebar, AreaInspector:
		return 7, 0 // sidebar, behind the window
	case AreaToolbarCapsule:
		return 3, 1 // titlebar, within the window
	case AreaMenu:
		return 5, 1
	case AreaPopover:
		return 6, 1
	case AreaHUD:
		return 13, 1
	}
	return 18, 1 // contentBackground
}

// applyMaterials makes the views show want (ApplyShell, FlushShell). A
// region whose view kind is unchanged keeps its view and gets only what
// changed; the views are reordered only when a view was made or removed.
func (w *Window) applyMaterials() {
	m := &w.mat
	m.dirty = false
	rt := w.reduceTransparency()
	reorder := false
	if rt != m.rt { // every view changes kind
		for i := 0; i < m.n; i++ {
			w.dropMaterial(&m.slots[i])
		}
		m.rt = rt
	}
	for i := 0; i < m.nWant; i++ {
		s, r := &m.slots[i], &m.want[i]
		k := viewFor(r, rt)
		// A view made for another area keeps that area's configuration
		// (material, glass variant, interactivity): a new one is made. A
		// solid box has none.
		if s.view != 0 && (s.kind != k || s.kind != mvSolid && s.r.Area != r.Area) {
			w.dropMaterial(s)
		}
		fresh := s.view == 0
		if fresh {
			if s.view = w.newMaterial(r, k); s.view == 0 {
				continue
			}
			s.kind, s.hidden = k, false
			reorder = true
		}
		w.updateMaterial(s, r, fresh)
	}
	for i := m.nWant; i < m.n; i++ {
		w.dropMaterial(&m.slots[i])
	}
	m.n = m.nWant
	if reorder {
		w.orderMaterials()
	}
	m.applied++
}

// newMaterial makes region r's view of kind k and puts it in its superview
// (ordered by orderMaterials). It returns 0 if the system lacks the class.
func (w *Window) newMaterial(r *MaterialRegion, k matView) objc.ID {
	var v objc.ID
	switch k {
	case mvGlass, mvCapsule:
		v = newViewOf(clsGlass(), 0, 0, 0, 0)
		objc.Send1(v, selSetGlassStyle, 0) // regular
		configureGlass(v, r.Area, w.privateAPI())
	case mvEffect:
		if effectCls == 0 {
			effectCls = objc.Cls("NSVisualEffectView")
		}
		v = newViewOf(effectCls, 0, 0, 0, 0)
		material, blending := effectMaterial(r.Area)
		objc.Send1(v, selSetMaterial, material)
		objc.Send1(v, selSetBlendingMode, blending)
		objc.Send1(v, selSetEffectState, 0) // follows the window's active state
		objc.Send1(v, selSetWantsLayerV, 1)
		w.maskMaterial(v)
	case mvSolid:
		if boxCls == 0 {
			boxCls = objc.Cls("NSBox")
		}
		v = newViewOf(boxCls, 0, 0, 0, 0)
		objc.Send1(v, selSetBoxType, 4)  // NSBoxCustom
		objc.Send1(v, selSetTitlePos, 0) // NSNoTitle
		objc.F.Reset().Arg(uintptr(v)).Arg(uintptr(selSetBorderWidth)).ArgF64(0).Call(objc.MsgSend)
	}
	if v == 0 {
		return 0
	}
	liveMaterials.Add(1)
	if k == mvCapsule {
		w.makeGlassContainer()
		objc.Send1(w.mat.glassContent, selAddSubview, uintptr(v))
	} else {
		objc.F.Reset().Arg(uintptr(w.content)).Arg(uintptr(selAddSubviewPos)).Arg(uintptr(v)).Arg(^uintptr(0) /* NSWindowBelow */).Arg(uintptr(w.view)).Call(objc.MsgSend)
	}
	objc.Send(v, selReleaseObj) // its superview owns it
	return v
}

// makeGlassContainer makes the capsules' NSGlassEffectContainerView, the
// window's size, with a flipped content view.
func (w *Window) makeGlassContainer() {
	m := &w.mat
	if m.glass != 0 {
		return
	}
	b := objc.SendRect(w.content, selBounds)
	m.glass = newViewOf(clsGlassContainer(), 0, 0, b.W, b.H)
	objc.F.Reset().Arg(uintptr(m.glass)).Arg(uintptr(selSetSpacing)).ArgF64(4).Call(objc.MsgSend) // AppKit's toolbar: 4
	objc.Send1(m.glass, selSetAutoresizing, 2|16)
	m.glassContent = newViewOf(containerClass, 0, 0, b.W, b.H)
	objc.Send1(m.glassContent, selSetAutoresizing, 2|16)
	objc.Send1(m.glass, selSetContentViewV, uintptr(m.glassContent))
	objc.Send(m.glassContent, selReleaseObj) // the glass container owns it
	objc.F.Reset().Arg(uintptr(w.content)).Arg(uintptr(selAddSubviewPos)).Arg(uintptr(m.glass)).Arg(^uintptr(0)).Arg(uintptr(w.view)).Call(objc.MsgSend)
	objc.Send(m.glass, selReleaseObj)
	liveMaterials.Add(1)
}

// updateMaterial gives slot s's view what changed from s.r to r (all of it
// for a fresh view).
func (w *Window) updateMaterial(s *matSlot, r *MaterialRegion, fresh bool) {
	if !fresh && s.r == *r {
		return
	}
	scale := float64(w.scale)
	if scale <= 0 {
		scale = 1
	}
	hidden := r.Rect[2] <= r.Rect[0] || r.Rect[3] <= r.Rect[1]
	if hidden != s.hidden {
		var h uintptr
		if hidden {
			h = 1
		}
		objc.Send1(s.view, selSetHidden, h)
		s.hidden = hidden
	}
	if fresh || r.Rect != s.r.Rect {
		x, y := float64(r.Rect[0])/scale, float64(r.Rect[1])/scale
		objc.F.Reset().Arg(uintptr(s.view)).Arg(uintptr(selSetFrame)).
			ArgRect(x, y, float64(r.Rect[2])/scale-x, float64(r.Rect[3])/scale-y).Call(objc.MsgSend)
	}
	if fresh || r.Radii != s.r.Radii || s.masked && r.Rect != s.r.Rect { // a masked radius is clamped to the size
		rad, mask := cornerShape(r, scale)
		switch s.kind {
		case mvGlass, mvCapsule, mvSolid:
			if mask == allCorners {
				objc.F.Reset().Arg(uintptr(s.view)).Arg(uintptr(selSetCornerRadius)).ArgF64(rad).Call(objc.MsgSend)
				if s.masked {
					clipCorners(s.view, 0, allCorners)
				}
			} else {
				// The view's own corner radius rounds all four: it is left
				// square and its layer clips the rounded corners.
				objc.F.Reset().Arg(uintptr(s.view)).Arg(uintptr(selSetCornerRadius)).ArgF64(0).Call(objc.MsgSend)
				objc.Send1(s.view, selSetWantsLayerV, 1)
				clipCorners(s.view, rad, mask)
			}
		case mvEffect:
			clipCorners(s.view, rad, mask)
		}
		s.masked = mask != allCorners
	}
	if s.kind == mvSolid && (fresh || r.Solid != s.r.Solid) {
		objc.Send1(s.view, selSetFillColor, uintptr(solidColor(r.Solid)))
	}
	s.r = *r
}

// allCorners is a CACornerMask of the four corners.
const allCorners = 15

// cornerShape is the corner radius (points) and the CACornerMask of the
// corners it rounds for region r. A layer has one radius: the corners with a
// radius take the largest of them, clamped to half the shorter side as the
// drawn box's corners are, and the others stay square. Corners with
// different non-zero radii are therefore drawn with one (a restriction of
// the native views; ui's boxes behind materials use one radius or none).
func cornerShape(r *MaterialRegion, scale float64) (rad float64, mask uintptr) {
	// TL, TR, BR, BL in the layer of a view that is not flipped (y up): the
	// top corners are MaxY (kCALayerMinXMaxYCorner 4, kCALayerMaxXMaxYCorner
	// 8), the bottom ones MinY (kCALayerMaxXMinYCorner 2, kCALayerMinXMinYCorner 1).
	bits := [4]uintptr{4, 8, 2, 1}
	var m float32
	for i, v := range r.Radii {
		if v > 0 {
			mask |= bits[i]
			m = max(m, v)
		}
	}
	if mask == 0 {
		return 0, allCorners
	}
	if mask != allCorners {
		m = min(m, (r.Rect[2]-r.Rect[0])/2, (r.Rect[3]-r.Rect[1])/2)
	}
	return float64(max(m, 0)) / scale, mask
}

// clipCorners rounds the corners in mask of view v's layer with radius rad
// (points) and clips the view to them.
func clipCorners(v objc.ID, rad float64, mask uintptr) {
	l := objc.Send(v, selLayerOf)
	objc.F.Reset().Arg(uintptr(l)).Arg(uintptr(selSetCornerRadius)).ArgF64(rad).Call(objc.MsgSend)
	objc.Send1(l, selSetMaskedCorner, mask)
	objc.Send1(l, selSetMasksToBnds, uintptr(boolU(rad > 0)))
	objc.Send1(l, selSetCornerCurve, uintptr(objc.Str("continuous")))
}

func boolU(b bool) int {
	if b {
		return 1
	}
	return 0
}

// solidColor is the NSColor of a Solid (0: the window background, which
// follows the appearance).
func solidColor(c uint32) objc.ID {
	if colorCls == 0 {
		colorCls = objc.Cls("NSColor")
	}
	cls := colorCls
	if c == 0 {
		return objc.Send(cls, selWindowBGColor)
	}
	f := func(s uint) float64 { return float64(c>>s&0xFF) / 255 }
	return objc.ID(objc.F.Reset().Arg(uintptr(cls)).Arg(uintptr(selColorSRGB)).
		ArgF64(f(24)).ArgF64(f(16)).ArgF64(f(8)).ArgF64(f(0)).Call(objc.MsgSend).R0())
}

// orderMaterials stacks the material views in region order directly under
// our view (above the backdrop), and the capsules' glass container above
// them.
func (w *Window) orderMaterials() {
	m := &w.mat
	below := func(v objc.ID) {
		objc.Send(v, selRetainV) // re-adding moves it; keep it alive meanwhile
		objc.F.Reset().Arg(uintptr(w.content)).Arg(uintptr(selAddSubviewPos)).Arg(uintptr(v)).Arg(^uintptr(0)).Arg(uintptr(w.view)).Call(objc.MsgSend)
		objc.Send(v, selReleaseObj)
	}
	for i := 0; i < m.n; i++ {
		if s := &m.slots[i]; s.view != 0 && s.kind != mvCapsule {
			below(s.view)
		}
	}
	if m.glass != 0 {
		below(m.glass)
	}
}

// dropMaterial removes slot s's view, which frees it.
func (w *Window) dropMaterial(s *matSlot) {
	if s.view != 0 {
		objc.Send(s.view, selRemoveFromSuper)
		liveMaterials.Add(-1)
	}
	*s = matSlot{}
}

// closeMaterials removes every material view (closeShell).
func (w *Window) closeMaterials() {
	m := &w.mat
	for i := range m.slots {
		w.dropMaterial(&m.slots[i])
	}
	if m.glass != 0 {
		objc.Send(m.glass, selRemoveFromSuper)
		liveMaterials.Add(-1)
	}
	*m = matState{}
}
