//go:build darwin

package platform

import (
	"image/color"
	"testing"

	"gonativeui/internal/objc"
)

func childCount(w *Window) int {
	return int(objc.Send(objc.Send(w.win, objc.Sel("childWindows")), objc.Sel("count")))
}

// A panel with a Parent is a child window that reaches past its parent's edge,
// carries a popover material of its own, takes an outline for it, moves with
// SetPanelOrigin, and leaves its parent when closed.
func TestChildPanelReachesPastItsParent(t *testing.T) {
	type result struct {
		err                                 error
		cls                                 string
		kindEffect, maskSet, maskGone       bool
		maskErr, badPNG, noPanelErr         error
		childBefore, childShown, childAfter int
		parentFrame, panelFrame, movedFrame [4]float64
	}
	var r result
	onMain(func() {
		parent, err := Open(Options{Title: "parent", Width: 600, Height: 400, Hidden: true})
		if err != nil {
			r.err = err
			return
		}
		defer parent.Close()
		panel, err := Open(Options{Width: 300, Height: 200, Hidden: true, Materials: true,
			Panel: &Panel{Parent: parent, X: 500, Y: 350, Shadow: true}})
		if err != nil {
			r.err = err
			return
		}
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		r.noPanelErr = parent.SetPanelMask([]byte("x"))
		r.childBefore = childCount(parent)

		panel.mat.rtTest = -1 // the material, whatever the system's Reduce Transparency
		s := panel.scale
		panel.SetMaterials([]MaterialRegion{{Area: AreaPopover, Rect: [4]float32{0, 0, 300 * s, 200 * s}}})
		panel.FlushShell()
		slot := &panel.mat.slots[0]
		r.kindEffect, r.cls = slot.kind == mvEffect, className(slot.view)
		r.badPNG = panel.SetPanelMask([]byte("not a png"))
		r.maskErr = panel.SetPanelMask(pngOf(int(300*s), int(200*s), color.RGBA{0, 0, 0, 255}))
		r.maskSet = objc.Send(slot.view, objc.Sel("maskImage")) != 0
		panel.SetPanelMask(nil)
		r.maskGone = objc.Send(slot.view, objc.Sel("maskImage")) == 0

		panel.Show()
		r.childShown = childCount(parent)
		r.parentFrame, r.panelFrame = frameOf(parent.win), frameOf(panel.win)
		panel.SetPanelOrigin(20, 30)
		r.movedFrame = frameOf(panel.win)
		panel.Close()
		r.childAfter = childCount(parent)
	})
	if r.err != nil {
		t.Skipf("cannot open a window here: %v", r.err)
	}
	if r.noPanelErr == nil {
		t.Error("SetPanelMask on a window that is not a panel succeeded")
	}
	if !r.kindEffect || r.cls != "NSVisualEffectView" {
		t.Errorf("the panel's popover material is %q (effect kind %v)", r.cls, r.kindEffect)
	}
	if r.badPNG == nil {
		t.Error("a mask that is not an image was accepted")
	}
	if r.maskErr != nil || !r.maskSet || !r.maskGone {
		t.Errorf("mask: err %v, set %v, removed %v", r.maskErr, r.maskSet, r.maskGone)
	}
	if r.childBefore != 0 || r.childShown != 1 || r.childAfter != 0 {
		t.Errorf("child windows of the parent: %d before, %d shown, %d after close", r.childBefore, r.childShown, r.childAfter)
	}
	// 500 points from the parent's content left edge with a 300-point panel on a
	// 600-point parent: it ends 200 points past the parent's right edge.
	if r.panelFrame[0]+r.panelFrame[2] <= r.parentFrame[0]+r.parentFrame[2] {
		t.Errorf("the panel %v does not reach past its parent %v", r.panelFrame, r.parentFrame)
	}
	if r.movedFrame[0] >= r.panelFrame[0] || r.movedFrame[1] <= r.panelFrame[1] {
		t.Errorf("SetPanelOrigin(20, 30) left the panel at %v (was %v)", r.movedFrame, r.panelFrame)
	}
}
