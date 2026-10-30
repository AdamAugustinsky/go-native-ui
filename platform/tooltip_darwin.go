//go:build darwin

package platform

import (
	"sync"
	"unsafe"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// Native tooltips on the content view (see tooltip.go). The regions are
// registered with addToolTipRect:owner:userData:; the userData is the region's
// key, and the owner, a tiny object of our own class, answers
// view:stringForToolTip:point:userData: by asking the function given to
// SetTooltipText. AppKit does not retain the owner, so the window does, until
// it closes.
//
// The registered rectangles are in the view's coordinates, which count from
// the bottom edge, so a change in the view's height needs them registered
// again even when the regions (counted from the top) did not change.

// NativeTooltips reports whether the window system shows tooltips for the
// regions given to SetTooltipRegions: true here.
const NativeTooltips = true

// nativeAux is the native dialogs and tooltips state of a Window (N2).
type nativeAux struct {
	closed bool

	rects      regionRefresh
	tips       tooltipMap
	tipH       float64 // the view height (points) and scale the rects were registered for
	tipScale   float32
	tipOwner   objc.ID // the object AppKit asks for strings; 0 until the first region
	tipIdx     int     // 1 + index in tipWindows
	tipText    func(key uint64, dst []byte) []byte
	tipBuf     []byte
	tipApplies int // how many times the rects were registered again (tests)
}

var (
	tipOnce    sync.Once
	tipIndex   *objc.Ivar
	tipClass   objc.ID
	tipFree    []int     // reusable indices in tipWindows
	tipWindows []*Window // the owners' windows, indexed by the owner's index ivar - 1

	selAddToolTipRect    = objc.Sel("addToolTipRect:owner:userData:")
	selRemoveAllToolTips = objc.Sel("removeAllToolTips")
	selIsFlipped         = objc.Sel("isFlipped")
	selViewStringForTip  = objc.Sel("view:stringForToolTip:point:userData:")
)

func tipInit() {
	tipOnce.Do(func() {
		pbInit() // NSString and its selectors
		def := objc.Define("GNUITooltipOwner", objc.Cls("NSObject"), nil)
		// - (NSString *)view:(NSView *)v stringForToolTip:(NSToolTipTag)tag point:(NSPoint)p userData:(void *)d
		def.Method(selViewStringForTip, "@@:@q{CGPoint=dd}^v", tipStringFor)
		tipClass = def.Register()
		tipIndex = def.Index()
	})
}

// SetTooltipText sets where tooltip text comes from: f appends the UTF-8 text
// of the tooltip with the given key to dst and returns it (dst unchanged: no
// tooltip). The system calls it on the main thread, when it shows a tooltip,
// so the text is the app's latest, never stale. Main thread.
func (w *Window) SetTooltipText(f func(key uint64, dst []byte) []byte) { w.aux.tipText = f }

// SetTooltipRegions sets the tooltip regions: where in the content the system
// shows the tooltip of which key (later entries are on top). Main thread. It
// copies into storage the window owns and does nothing when the list equals
// the previous one (and the view did not change height), so it can be called
// every frame without allocating.
func (w *Window) SetTooltipRegions(r []TooltipRegion) {
	a := &w.aux
	if a.closed {
		return
	}
	changed := a.tips.set(r)
	if !changed && a.tipH == w.hPt && a.tipScale == w.scale {
		return
	}
	if len(a.tips.regs) == 0 && a.tipOwner == 0 {
		a.tipH, a.tipScale = w.hPt, w.scale // nothing registered, nothing to move
		return
	}
	a.rects.changed = true
	a.rects.tooltipDirty = true
	if !a.rects.scrolling || a.tipH != w.hPt || a.tipScale != w.scale {
		w.applyTooltips()
	}
}

// applyTooltips replaces the registered tooltip rects with the current regions.
func (w *Window) applyTooltips() {
	a := &w.aux
	a.rects.tooltipDirty = false
	if a.tipOwner == 0 {
		tipInit()
		owner := objc.Send(objc.Send(tipClass, selAlloc), selInit)
		a.tipIdx = keepTipWindow(w)
		tipIndex.Set(owner, uintptr(a.tipIdx))
		a.tipOwner = owner // +1: AppKit does not retain a tooltip owner
	}
	a.tipH, a.tipScale = w.hPt, w.scale
	a.tipApplies++
	objc.Send(w.view, selRemoveAllToolTips)
	if len(a.tips.regs) == 0 {
		return
	}
	s := float64(w.scale)
	flipped := objc.SendBool(w.view, selIsFlipped)
	for _, r := range a.tips.flatten() {
		// Our rectangles count from the top; the view counts from the bottom
		// unless it is flipped.
		x, wd, ht := float64(r.Rect[0])/s, float64(r.Rect[2]-r.Rect[0])/s, float64(r.Rect[3]-r.Rect[1])/s
		y := float64(r.Rect[1]) / s
		if !flipped {
			y = w.hPt - float64(r.Rect[3])/s
		}
		objc.F.Reset().Arg(uintptr(w.view)).Arg(uintptr(selAddToolTipRect)).ArgRect(x, y, wd, ht).
			Arg(uintptr(a.tipOwner)).Arg(uintptr(r.Key)).Call(objc.MsgSend)
	}
}

// tipStringFor is view:stringForToolTip:point:userData: of the owner: self 0,
// _cmd 1, the view 2, the tag 3 (the point is in float registers) and the
// userData, the region's key, 4.
func tipStringFor(c *ffi.CB) {
	c.Ret(0)
	i := int(tipIndex.Get(objc.ID(c.Int(0))))
	if i < 1 || i > len(tipWindows) {
		return
	}
	w := tipWindows[i-1]
	if w == nil || w.aux.tipText == nil {
		return
	}
	b := w.aux.tipText(uint64(c.Int(4)), w.aux.tipBuf[:0])
	w.aux.tipBuf = b
	if len(b) == 0 {
		return
	}
	alloc := objc.Send(clsNSString, selAllocStr) // before the init call is assembled: both use the frame
	str := objc.ID(objc.F.Reset().Arg(uintptr(alloc)).Arg(uintptr(selInitBytes)).
		ArgPtr(unsafe.Pointer(&b[0])).Arg(uintptr(len(b))).Arg(4 /* NSUTF8StringEncoding */).Call(objc.MsgSend).R0())
	c.Ret(uintptr(objc.Send(str, selAutorelease))) // the caller retains what it keeps
}

// closeAux ends what the window owns for N2 before it closes: its sheets are
// aborted (the apps get cancelled results) and its tooltip rects and owner go.
func (w *Window) closeAux() {
	w.abortDialogs()
	a := &w.aux
	a.closed = true
	if a.tipOwner != 0 {
		objc.Send(w.view, selRemoveAllToolTips)
		tipIndex.Set(a.tipOwner, 0)
		freeTipWindow(a.tipIdx)
		a.tipIdx = 0
		objc.Release(a.tipOwner)
		a.tipOwner = 0
	}
	a.tipText = nil
}

// keepTipWindow and freeTipWindow bound the registry by peak live owners.
func keepTipWindow(w *Window) int {
	if n := len(tipFree); n > 0 {
		i := tipFree[n-1]
		tipFree = tipFree[:n-1]
		tipWindows[i] = w
		return i + 1
	}
	tipWindows = append(tipWindows, w)
	return len(tipWindows)
}

func freeTipWindow(index int) {
	i := index - 1
	tipWindows[i] = nil
	tipFree = append(tipFree, i)
}

// FinishRegionFrame flushes deferred native rects on the first still frame.
// Call after both cursor and tooltip regions. It schedules no timer or frame.
func (w *Window) FinishRegionFrame() {
	if !w.aux.rects.changed {
		w.flushRegionRects()
	}
	w.aux.rects.changed = false
}

func (w *Window) flushRegionRects() {
	a := &w.aux
	a.rects.scrolling = false
	if a.closed {
		return
	}
	if a.rects.cursorDirty && w.curs.override == CursorArrow {
		w.rebuildCursorRects()
	}
	if a.rects.tooltipDirty {
		w.applyTooltips()
	}
}
