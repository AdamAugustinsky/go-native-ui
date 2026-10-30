//go:build darwin

package platform

import (
	"errors"

	"gonativeui/internal/objc"
)

// A panel with a Parent (panel.go): ordered over the parent as a child window,
// at an offset from the parent's content, with an optional outline for its
// material. The panel itself is main's (panel_darwin.go).

var (
	selAddChild      = objc.SelZ("addChildWindow:ordered:\x00")
	selRemoveChild   = objc.SelZ("removeChildWindow:\x00")
	selSetMaskImage  = objc.SelZ("setMaskImage:\x00")
	selContentRectFR = objc.SelZ("contentRectForFrameRect:\x00")
	selNSImageSize   = objc.SelZ("setSize:\x00")
)

// panelChild is the child-panel state of a Window.
type panelChild struct {
	x, y     float64 // content top-left from the parent's, points
	set      bool    // x, y were given by SetPanelOrigin (else the Panel's)
	attached bool
	mask     objc.ID // the outline, an NSImage; retained
}

// childParent is the window the panel belongs to, nil for a panel without one
// or one whose parent has closed.
func (w *Window) childParent() *Window {
	if w.panel == nil || w.panel.Parent == nil || w.panel.Parent.buried || w.panel.Parent.win == 0 {
		return nil
	}
	return w.panel.Parent
}

// placePanel puts the panel where its parent's content and its offset say.
func (w *Window) placePanel() {
	par := w.childParent()
	if par == nil {
		return
	}
	x, y := float64(w.panel.X), float64(w.panel.Y)
	if w.child.set {
		x, y = w.child.x, w.child.y
	}
	f := objc.SendRect(par.win, selFrame)
	objc.F.Reset().Arg(uintptr(par.win)).Arg(uintptr(selContentRectFR)).ArgRect(f.X, f.Y, f.W, f.H).Call(objc.MsgSend)
	cx, cy, ch := objc.F.F64(0), objc.F.F64(1), objc.F.F64(3)
	own := objc.SendRect(w.view, selBounds)
	objc.F.Reset().Arg(uintptr(w.win)).Arg(uintptr(selSetFrameOrigin)).ArgPoint(cx+x, cy+ch-y-own.H).Call(objc.MsgSend)
}

// SetPanelOrigin moves a child panel to x, y (logical units from its parent's
// content top-left). Main thread. Does nothing for a panel without a Parent.
func (w *Window) SetPanelOrigin(x, y float32) {
	if w.childParent() == nil {
		return
	}
	w.child.x, w.child.y, w.child.set = float64(x), float64(y), true
	w.placePanel()
}

// attachPanel places a child panel and adds it to its parent (showPanel).
func (w *Window) attachPanel() {
	par := w.childParent()
	if par == nil {
		return
	}
	w.placePanel()
	if !w.child.attached {
		objc.Send2(par.win, selAddChild, uintptr(w.win), 1 /* NSWindowAbove */)
		w.child.attached = true
	}
}

// detachPanel takes a child panel off its parent and lets go of its outline
// (Close).
func (w *Window) detachPanel() {
	c := &w.child
	if par := w.childParent(); c.attached && par != nil {
		objc.Send1(par.win, selRemoveChild, uintptr(w.win))
	}
	c.attached = false
	if c.mask != 0 {
		objc.Send(c.mask, selReleaseObj)
		c.mask = 0
	}
}

var errPanelMask = errors.New("platform: the panel's mask is not an image")

// SetPanelMask gives the panel's material an outline: the alpha of png (a PNG
// the size of the panel in pixels) says where the material shows, and the
// window's shadow is recomputed from it. nil or empty removes the outline. It
// is applied to the material views that exist and to those made later. Main
// thread. A window that is not a panel returns ErrNoPanels.
func (w *Window) SetPanelMask(png []byte) error {
	if w.panel == nil {
		return ErrNoPanels
	}
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	var img objc.ID
	if len(png) > 0 {
		dragInit() // NSData, NSImage and the selectors below
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
	if w.child.mask != 0 {
		objc.Send(w.child.mask, selReleaseObj)
	}
	w.child.mask = img
	m := &w.mat
	for i := 0; i < m.n; i++ {
		if s := &m.slots[i]; s.view != 0 && s.kind == mvEffect {
			objc.Send1(s.view, selSetMaskImage, uintptr(img))
		}
	}
	objc.Send(w.win, selInvalidateShadow)
	return nil
}

// maskMaterial gives a material view made for a panel the panel's outline.
func (w *Window) maskMaterial(v objc.ID) {
	if w.child.mask != 0 {
		objc.Send1(v, selSetMaskImage, uintptr(w.child.mask))
	}
}
