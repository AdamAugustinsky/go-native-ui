//go:build darwin

package platform

import (
	"errors"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// Panels on AppKit (the contract is panel.go): GNUIPanelWindow, a borderless
// GNUIWindow that can become key but not main, with a clear background and a
// shadow, added to its parent with addChildWindow:ordered: when it is shown, so
// that it is ordered over the parent and moves with it. Its content is the same
// as any window's: the GNUIView with the CAMetalLayer, and a container for the
// material views below it (Options.Materials is forced on).

var (
	panelWindowClass objc.ID

	selAddChild      = objc.Sel("addChildWindow:ordered:")
	selRemoveChild   = objc.Sel("removeChildWindow:")
	selSetHasShadow  = objc.Sel("setHasShadow:")
	selInvalShadow   = objc.Sel("invalidateShadow")
	selSetMaskImage  = objc.Sel("setMaskImage:")
	selContentRectFR = objc.Sel("contentRectForFrameRect:")
	selClearColor    = objc.Sel("clearColor")
	selSetHidesOnDea = objc.Sel("setHidesOnDeactivate:")
	selOrderFront    = objc.Sel("orderFront:")
	selCanBecomeKey  = objc.Sel("canBecomeKeyWindow")
	selNSImageSize   = objc.Sel("setSize:")
)

// panelState is a panel's: its parent, where it is in the parent's content,
// and the outline of its material.
type panelState struct {
	on       bool
	parent   *Window
	x, y     float64 // content top-left from the parent's, points
	attached bool
	mask     objc.ID // the outline, an NSImage; retained
}

func registerPanelClass() {
	d := objc.Define("GNUIPanelWindow", windowClass, nil)
	d.Method(selCanBecomeKey, "c@:", retYes)
	d.Method(selCanBecomeMain, "c@:", func(c *ffi.CB) { c.RetBool(false) })
	panelWindowClass = d.Register()
}

// IsPanel reports whether the window is a panel (Options.Panel).
func (w *Window) IsPanel() bool { return w.panel.on }

// checkPanel is Open's check of Options.Panel.
func checkPanel(p *PanelOptions) error {
	if p.Parent == nil || p.Parent.buried || p.Parent.win == 0 {
		return ErrPanelParent
	}
	return nil
}

// initPanel makes the freshly opened window look and sit like a panel.
func (w *Window) initPanel(p *PanelOptions) {
	w.panel = panelState{on: true, parent: p.Parent, x: float64(p.X), y: float64(p.Y)}
	objc.Send1(w.win, selSetOpaqueWin, 0)
	objc.Send1(w.win, selSetBGColor, uintptr(objc.Send(objc.Cls("NSColor"), selClearColor)))
	objc.Send1(w.win, selSetHasShadow, 1)
	objc.Send1(w.win, selSetHidesOnDea, 0)
	w.placePanel()
}

// placePanel puts the panel where its parent's content and its offset say.
func (w *Window) placePanel() {
	p := &w.panel
	par := p.parent
	if par == nil || par.win == 0 {
		return
	}
	f := objc.SendRect(par.win, selFrame)
	objc.F.Reset().Arg(uintptr(par.win)).Arg(uintptr(selContentRectFR)).ArgRect(f.X, f.Y, f.W, f.H).Call(objc.MsgSend)
	cx, cy, ch := objc.F.F64(0), objc.F.F64(1), objc.F.F64(3)
	own := objc.SendRect(w.view, selBounds)
	objc.F.Reset().Arg(uintptr(w.win)).Arg(uintptr(selSetFrameOrigin)).ArgPoint(cx+p.x, cy+ch-p.y-own.H).Call(objc.MsgSend)
}

// SetPanelOrigin moves a panel to x, y (logical units from its parent's content
// top-left). Main thread. Does nothing for a window that is not a panel.
func (w *Window) SetPanelOrigin(x, y float32) {
	if !w.panel.on {
		return
	}
	w.panel.x, w.panel.y = float64(x), float64(y)
	w.placePanel()
}

// showPanel orders the panel over its parent without activating the
// application.
func (w *Window) showPanel() {
	p := &w.panel
	w.placePanel()
	if !p.attached && p.parent != nil && p.parent.win != 0 {
		objc.Send2(p.parent.win, selAddChild, uintptr(w.win), 1 /* NSWindowAbove */)
		p.attached = true
	}
	objc.Send1(w.win, selOrderFront, 0)
	if w.link != 0 {
		w.setRateRange(w.link)
	}
}

// detachPanel takes the panel off its parent (Close).
func (w *Window) detachPanel() {
	p := &w.panel
	if p.attached && p.parent != nil && p.parent.win != 0 {
		objc.Send1(p.parent.win, selRemoveChild, uintptr(w.win))
	}
	p.attached = false
	if p.mask != 0 {
		objc.Send(p.mask, selReleaseObj)
		p.mask = 0
	}
	p.parent = nil
}

var errPanelMask = errors.New("platform: the panel's mask is not an image")

// SetPanelMask gives the panel's material an outline: the alpha of png (a PNG
// the size of the panel in pixels) says where the material shows, and the
// window's shadow is recomputed from it. nil or empty removes the outline. The
// outline is applied to the material views that exist and to those made later.
// Main thread. A window that is not a panel returns ErrNoPanel.
func (w *Window) SetPanelMask(png []byte) error {
	p := &w.panel
	if !p.on {
		return ErrNoPanel
	}
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	var img objc.ID
	if len(png) > 0 {
		dragInit()
		data := objc.ID(objc.F.Reset().Arg(uintptr(clsNSData)).Arg(uintptr(selDataWithBytes)).ArgPtr(dragBytes(png)).Arg(uintptr(len(png))).Call(objc.MsgSend).R0())
		// +1; initWithData: releases the allocation itself when it fails.
		if data == 0 {
			return errPanelMask
		}
		if img = objc.Send1(objc.Send(clsNSImage2, selAlloc), selInitWithData, uintptr(data)); img == 0 {
			return errPanelMask
		}
		objc.F.Reset().Arg(uintptr(img)).Arg(uintptr(selNSImageSize)).ArgPoint(w.wPt, w.hPt).Call(objc.MsgSend)
	}
	if p.mask != 0 {
		objc.Send(p.mask, selReleaseObj)
	}
	p.mask = img
	m := &w.mat
	for i := 0; i < m.n; i++ {
		if s := &m.slots[i]; s.view != 0 && s.kind == mvEffect {
			objc.Send1(s.view, selSetMaskImage, uintptr(img))
		}
	}
	objc.Send(w.win, selInvalShadow)
	return nil
}

// maskMaterial gives a material view made for a panel the panel's outline.
func (w *Window) maskMaterial(v objc.ID) {
	if w.panel.mask != 0 {
		objc.Send1(v, selSetMaskImage, uintptr(w.panel.mask))
	}
}
