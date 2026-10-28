//go:build darwin

package platform

import (
	"errors"
	"image/color"
	"testing"

	"gonativeui/internal/objc"
)

// A panel is a borderless child window that reaches past its parent's edge,
// carries a popover material of its own, takes an outline for it, and leaves its
// parent when closed.
func TestPanelReachesPastItsParent(t *testing.T) {
	type result struct {
		err                                 error
		isPanel, parentIsPanel              bool
		parentFrame, panelFrame             [4]float64
		borderless, opaque, shadow          bool
		kindEffect                          bool
		cls                                 string
		maskSet, maskGone                   bool
		maskErr, notPanelErr, badPNG        error
		childBefore, childShown, childAfter int
		base, afterClose                    ShellCounts
	}
	var r result
	onMain(func() {
		parent, err := Open(Options{Title: "parent", Width: 600, Height: 400, Hidden: true})
		if err != nil {
			r.err = err
			return
		}
		defer parent.Close()
		panel, err := Open(Options{Width: 300, Height: 200, Hidden: true, Panel: &PanelOptions{Parent: parent, X: 500, Y: 350}})
		if err != nil {
			r.err = err
			return
		}
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		r.isPanel, r.parentIsPanel = panel.IsPanel(), parent.IsPanel()
		r.notPanelErr = parent.SetPanelMask([]byte("x"))
		r.childBefore = int(objc.Send(objc.Send(parent.win, objc.Sel("childWindows")), objc.Sel("count")))

		panel.mat.rtTest = -1 // the material, whatever the system's Reduce Transparency
		s := panel.scale
		panel.SetMaterials([]MaterialRegion{{Area: AreaPopover, Rect: [4]float32{0, 0, 300 * s, 200 * s}}})
		panel.FlushShell()
		slot := &panel.mat.slots[0]
		r.kindEffect = slot.kind == mvEffect
		r.cls = className(slot.view)
		r.badPNG = panel.SetPanelMask([]byte("not a png"))
		r.maskErr = panel.SetPanelMask(pngOf(int(300*s), int(200*s), color.RGBA{0, 0, 0, 255}))
		r.maskSet = objc.Send(slot.view, objc.Sel("maskImage")) != 0
		panel.SetPanelMask(nil)
		r.maskGone = objc.Send(slot.view, objc.Sel("maskImage")) == 0

		panel.Show()
		r.childShown = int(objc.Send(objc.Send(parent.win, objc.Sel("childWindows")), objc.Sel("count")))
		r.parentFrame, r.panelFrame = frameOf2(parent.win), frameOf2(panel.win)
		r.borderless = objc.Send(panel.win, selStyleMask) == 0
		r.opaque = objc.SendBool(panel.win, objc.Sel("isOpaque"))
		r.shadow = objc.SendBool(panel.win, objc.Sel("hasShadow"))
		panel.Close()
		r.childAfter = int(objc.Send(objc.Send(parent.win, objc.Sel("childWindows")), objc.Sel("count")))
	})
	if r.err != nil {
		t.Skipf("cannot open a window here: %v", r.err)
	}
	if !r.isPanel || r.parentIsPanel {
		t.Errorf("IsPanel: panel %v, parent %v", r.isPanel, r.parentIsPanel)
	}
	if !errors.Is(r.notPanelErr, ErrNoPanel) {
		t.Errorf("SetPanelMask on a window that is not a panel = %v", r.notPanelErr)
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
	if !r.borderless || r.opaque || !r.shadow {
		t.Errorf("panel: borderless %v, opaque %v, shadow %v", r.borderless, r.opaque, r.shadow)
	}
	// 500 points from the parent's content left edge with a 300-point panel on a
	// 600-point parent: it ends 200 points past the parent's right edge.
	if r.panelFrame[0]+r.panelFrame[2] <= r.parentFrame[0]+r.parentFrame[2] {
		t.Errorf("the panel %v does not reach past its parent %v", r.panelFrame, r.parentFrame)
	}
}

func TestPanelNeedsAParent(t *testing.T) {
	var err error
	onMain(func() {
		_, err = Open(Options{Width: 100, Height: 100, Hidden: true, Panel: &PanelOptions{}})
	})
	if !errors.Is(err, ErrPanelParent) {
		t.Errorf("Open of a panel without a parent = %v", err)
	}
}

func frameOf2(win objc.ID) [4]float64 {
	f := objc.SendRect(win, selFrame)
	return [4]float64{f.X, f.Y, f.W, f.H}
}
