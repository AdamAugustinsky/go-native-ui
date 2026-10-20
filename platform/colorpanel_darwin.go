//go:build darwin

package platform

import (
	"sync"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// NativeColorPanel reports whether the OS has a colour panel the app can
// drive: true here.
const NativeColorPanel = true

// The shared NSColorPanel (see colorpanel.go). GNUIColorTarget is its target
// and the observer of its closing: its action gnuiColorChanged: reads the
// panel's colour in sRGB and queues it.
var colorPanel struct {
	once    sync.Once
	panel   objc.ID // [NSColorPanel sharedColorPanel]
	target  objc.ID // the GNUIColorTarget instance
	setting bool    // setColor: is running: the action it may fire is not the user's
	shown   bool

	selShared, selSetTarget, selSetAction, selSetContinuous, selSetShowsAlpha, selSetColor objc.SEL
	selColor, selOrderFront, selMakeKey, selClose, selVisible                              objc.SEL
	selSRGB, selUsing, selR, selG, selB, selA                                              objc.SEL
	selChanged, selClosed                                                                  objc.SEL
	clsColor, clsSpace, clsPanel                                                           objc.ID
}

func colorPanelInit() {
	cp := &colorPanel
	cp.once.Do(func() {
		loadFramework("/System/Library/Frameworks/AppKit.framework/AppKit")
		cp.clsPanel, cp.clsColor, cp.clsSpace = objc.Cls("NSColorPanel"), objc.Cls("NSColor"), objc.Cls("NSColorSpace")
		cp.selShared, cp.selSetTarget, cp.selSetAction = objc.Sel("sharedColorPanel"), objc.Sel("setTarget:"), objc.Sel("setAction:")
		cp.selSetContinuous, cp.selSetShowsAlpha, cp.selSetColor = objc.Sel("setContinuous:"), objc.Sel("setShowsAlpha:"), objc.Sel("setColor:")
		cp.selColor, cp.selOrderFront, cp.selMakeKey = objc.Sel("color"), objc.Sel("orderFront:"), objc.Sel("makeKeyAndOrderFront:")
		cp.selClose, cp.selVisible = objc.Sel("close"), objc.Sel("isVisible")
		cp.selSRGB, cp.selUsing = objc.Sel("sRGBColorSpace"), objc.Sel("colorUsingColorSpace:")
		cp.selR, cp.selG, cp.selB, cp.selA = objc.Sel("redComponent"), objc.Sel("greenComponent"), objc.Sel("blueComponent"), objc.Sel("alphaComponent")
		cp.selChanged, cp.selClosed = objc.Sel("gnuiColorChanged:"), objc.Sel("gnuiColorClosed:")
		t := objc.Define("GNUIColorTarget", objc.Cls("NSObject"), nil)
		t.Method(cp.selChanged, "v@:@", colorPanelChanged)
		t.Method(cp.selClosed, "v@:@", colorPanelClosed)
		cp.target = objc.Send(objc.Send(t.Register(), objc.Sel("alloc")), objc.Sel("init"))
	})
}

// colorPanelChanged is the panel's action: the user moved to a colour.
func colorPanelChanged(c *ffi.CB) {
	cp := &colorPanel
	w := theWindow.Load()
	if w == nil || cp.setting || cp.panel == 0 {
		return
	}
	col := objc.Send1(objc.Send(cp.panel, cp.selColor), cp.selUsing, uintptr(objc.Send(cp.clsSpace, cp.selSRGB)))
	if col == 0 {
		return
	}
	r, g, b, a := objc.SendF64(col, cp.selR), objc.SendF64(col, cp.selG), objc.SendF64(col, cp.selB), objc.SendF64(col, cp.selA)
	w.q.push(Event{Kind: EvColor, X: float32(r), Y: float32(g), DX: float32(b), DY: float32(a), Time: Now()})
}

// colorPanelClosed is the notification of the panel's window closing.
func colorPanelClosed(c *ffi.CB) {
	cp := &colorPanel
	if !cp.shown {
		return
	}
	cp.shown = false
	if w := theWindow.Load(); w != nil {
		w.q.push(Event{Kind: EvColor, Code: 1, Time: Now()})
	}
}

// nsColor is the NSColor for sRGB components.
func nsColor(r, g, b, a float32) objc.ID {
	cp := &colorPanel
	sel := objc.Sel("colorWithSRGBRed:green:blue:alpha:") // before F is reset: Sel uses it
	return objc.ID(objc.F.Reset().Arg(uintptr(cp.clsColor)).Arg(uintptr(sel)).
		ArgF64(float64(r)).ArgF64(float64(g)).ArgF64(float64(b)).ArgF64(float64(a)).Call(objc.MsgSend).R0())
}

func setPanelColor(r, g, b, a float32) {
	cp := &colorPanel
	col := nsColor(r, g, b, a)
	cp.setting = true
	objc.Send1(cp.panel, cp.selSetColor, uintptr(col))
	cp.setting = false
}

// ShowColorPanel puts the shared colour panel on screen with the colour
// (sRGB, 0 to 1) and makes this app its target: from now on its changes are
// EvColor events. It shows the opacity slider. Showing it again just moves it
// to the colour. Main thread.
func (w *Window) ShowColorPanel(r, g, b, a float32) error {
	colorPanelInit()
	cp := &colorPanel
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	if cp.panel == 0 {
		cp.panel = objc.Send(cp.clsPanel, cp.selShared)
		if cp.panel == 0 {
			return ErrUnsupported
		}
		nc := objc.Send(objc.Cls("NSNotificationCenter"), objc.Sel("defaultCenter"))
		name := objc.Str("NSWindowWillCloseNotification")
		objc.Send4(nc, objc.Sel("addObserver:selector:name:object:"), uintptr(cp.target), uintptr(cp.selClosed), uintptr(name), uintptr(cp.panel))
	}
	objc.Send1(cp.panel, cp.selSetTarget, uintptr(cp.target))
	objc.Send1(cp.panel, cp.selSetAction, uintptr(cp.selChanged))
	objc.Send1(cp.panel, cp.selSetContinuous, 1)
	objc.Send1(cp.panel, cp.selSetShowsAlpha, 1)
	setPanelColor(r, g, b, a)
	cp.shown = true
	objc.Send1(cp.panel, cp.selOrderFront, 0)
	return nil
}

// SetColorPanelColor moves the open panel to a colour; no EvColor comes back
// for it. Main thread.
func (w *Window) SetColorPanelColor(r, g, b, a float32) error {
	cp := &colorPanel
	if cp.panel == 0 || !cp.shown {
		return ErrUnsupported
	}
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	setPanelColor(r, g, b, a)
	return nil
}

// CloseColorPanel closes the panel, if it is open, without an EvColor; it
// also stops the panel's changes from reaching this app (the target goes).
// Main thread.
func (w *Window) CloseColorPanel() {
	cp := &colorPanel
	if cp.panel == 0 || !cp.shown {
		return
	}
	cp.shown = false
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	objc.Send1(cp.panel, cp.selSetTarget, 0)
	objc.Send(cp.panel, cp.selClose)
}
