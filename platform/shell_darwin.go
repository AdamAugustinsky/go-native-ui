//go:build darwin

package platform

import (
	"os"
	"runtime"
	"strconv"
	"unsafe"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// The native shell (experimental, the phase 6 native-shell spike): a system
// material behind the content (Options.Backdrop) and a native toolbar
// (Options.Toolbar), with the app's own pixels in between.
//
// With a backdrop the content view is a plain layer-backed container that
// holds the effect view (NSGlassEffectView on macOS 26 and later, else an
// NSVisualEffectView blurring what is behind the window) and, above it,
// GNUIView, whose CAMetalLayer is then non-opaque (Native.Transparent). The
// window is non-opaque with a background of white at alpha 0.001, which keeps
// its shadow and its clicks where the app paints nothing. The effect views
// follow the window's appearance by themselves (SetDarkChrome).
//
// The toolbar's delegate and its items' target is GNUIWindowDelegate, so the
// toolbar takes no ffi callback of its own: the methods share the delegate
// class's dispatcher. Items are created once in Open; the delegate hands
// them out by identifier. A click arrives as an action on the main thread,
// inside sendEvent, and is queued as EvCommand.
//
// GNUI_BACKDROP_MATERIAL=n (experiments only) replaces the backdrop by an
// NSVisualEffectView with material n.

var (
	selSetOpaqueWin     = objc.Sel("setOpaque:")
	selSetBGColor       = objc.Sel("setBackgroundColor:")
	selColorWhiteAlpha  = objc.Sel("colorWithWhite:alpha:")
	selSetWantsLayerV   = objc.Sel("setWantsLayer:")
	selSetAutoresizing  = objc.Sel("setAutoresizingMask:")
	selAddSubview       = objc.Sel("addSubview:")
	selSetMaterial      = objc.Sel("setMaterial:")
	selSetBlendingMode  = objc.Sel("setBlendingMode:")
	selSetEffectState   = objc.Sel("setState:")
	selSetGlassStyle    = objc.Sel("setStyle:")
	selSetCornerRadius  = objc.Sel("setCornerRadius:")
	selWinCornerRadius  = objc.Sel("_cornerRadius")
	selInitToolbar      = objc.Sel("initWithIdentifier:")
	selInitToolbarItem  = objc.Sel("initWithItemIdentifier:")
	selSetDisplayMode   = objc.Sel("setDisplayMode:")
	selSetCustomizable  = objc.Sel("setAllowsUserCustomization:")
	selSetToolbar       = objc.Sel("setToolbar:")
	selSetToolbarStyle  = objc.Sel("setToolbarStyle:")
	selSetSubtitle      = objc.Sel("setSubtitle:")
	selSetImage         = objc.Sel("setImage:")
	selSetLabel         = objc.Sel("setLabel:")
	selSetPaletteLabel  = objc.Sel("setPaletteLabel:")
	selSetToolTip       = objc.Sel("setToolTip:")
	selSetTarget        = objc.Sel("setTarget:")
	selSetAction        = objc.Sel("setAction:")
	selSetTag           = objc.Sel("setTag:")
	selTag              = objc.Sel("tag")
	selSetAutovalidates = objc.Sel("setAutovalidates:")
	selSetBordered      = objc.Sel("setBordered:")
	selSetEnabled       = objc.Sel("setEnabled:")
	selSetView          = objc.Sel("setView:")
	selSymbolImage      = objc.Sel("imageWithSystemSymbolName:accessibilityDescription:")
	selSetSegmentCount  = objc.Sel("setSegmentCount:")
	selSetLabelFor      = objc.Sel("setLabel:forSegment:")
	selSetImageFor      = objc.Sel("setImage:forSegment:")
	selSetWidthFor      = objc.Sel("setWidth:forSegment:")
	selSetToolTipFor    = objc.Sel("setToolTip:forSegment:")
	selSetTrackingMode  = objc.Sel("setTrackingMode:")
	selSetSelectedSeg   = objc.Sel("setSelectedSegment:")
	selSelectedSeg      = objc.Sel("selectedSegment")
	selSizeToFit        = objc.Sel("sizeToFit")
	selIsEqualStr       = objc.Sel("isEqualToString:")
	selArrayWithObjs    = objc.Sel("arrayWithObjects:count:")

	selTBDefault  = objc.Sel("toolbarDefaultItemIdentifiers:")
	selTBAllowed  = objc.Sel("toolbarAllowedItemIdentifiers:")
	selTBItemFor  = objc.Sel("toolbar:itemForItemIdentifier:willBeInsertedIntoToolbar:")
	selTBAction   = objc.Sel("gnuiToolbarAction:")
	appKitLib     uintptr // AppKit's handle, for its string constants (set by Open)
	tbWindow      *Window // the window whose toolbar the delegate serves: set before setToolbar:, which asks for the items while Open runs (theWindow is not set yet)
	nsImageClass  objc.ID
	toolbarTabW   = 180.0 // ToolbarItem.TabWidth's default, points
	glassMaterial = map[Backdrop]uintptr{BackdropGlass: 0, BackdropGlassClear: 1}
)

// registerToolbarDelegate adds the toolbar delegate methods and the items'
// action to GNUIWindowDelegate.
func registerToolbarDelegate(d *objc.ClassDef) {
	d.Method(selTBDefault, "@@:@", toolbarIdentifiers)
	d.Method(selTBAllowed, "@@:@", toolbarIdentifiers)
	d.Method(selTBItemFor, "@@:@@c", toolbarItemFor)
	d.Method(selTBAction, "v@:@", toolbarAction)
}

// toolbar is the window's native toolbar.
type toolbar struct {
	tb, idents objc.ID
	items      []tbItem
}

// tbItem is one toolbar item: what Options said, plus its AppKit objects and
// the state shown.
type tbItem struct {
	ToolbarItem
	ident, obj, seg objc.ID      // identifier (NSString), NSToolbarItem, the NSSegmentedControl of a ToolbarTabs
	tabs            []ToolbarTab // the tabs shown (ToolbarTabs)
	enabled         bool
}

// Backdrop is the material the window got (Options.Backdrop): BackdropWindow
// where Liquid Glass was asked for but the system has none, BackdropNone if no
// backdrop was asked for or none could be made. Experimental.
func (w *Window) Backdrop() Backdrop { return w.backdrop }

// HasToolbar reports whether the window has a native toolbar
// (Options.Toolbar). Experimental.
func (w *Window) HasToolbar() bool { return w.tb != nil }

// makeBackdrop builds the container with the effect view and GNUIView, makes
// the window transparent, and returns the container (w.view if no effect view
// could be made).
func (w *Window) makeBackdrop(kind Backdrop, width, height float64) objc.ID {
	F := objc.F
	newView := func(cls objc.ID) objc.ID {
		if cls == 0 {
			return 0
		}
		v := objc.Send(cls, selAlloc)
		return objc.ID(F.Reset().Arg(uintptr(v)).Arg(uintptr(selInitWithFrame)).ArgRect(0, 0, width, height).Call(objc.MsgSend).R0())
	}
	got, material := kind, uintptr(21) // NSVisualEffectMaterialUnderWindowBackground
	if m, err := strconv.Atoi(os.Getenv("GNUI_BACKDROP_MATERIAL")); err == nil {
		got, material = BackdropWindow, uintptr(m)
	}
	var effect objc.ID
	if style, glass := glassMaterial[got]; glass {
		if effect = newView(objc.Cls("NSGlassEffectView")); effect != 0 {
			objc.Send1(effect, selSetGlassStyle, style)
		}
	}
	if effect == 0 {
		got = BackdropWindow
		if effect = newView(objc.Cls("NSVisualEffectView")); effect == 0 {
			return w.view
		}
		objc.Send1(effect, selSetMaterial, material)
		objc.Send1(effect, selSetBlendingMode, 0) // NSVisualEffectBlendingModeBehindWindow
		objc.Send1(effect, selSetEffectState, 0)  // NSVisualEffectStateFollowsWindowActiveState
	}
	container := newView(objc.Cls("NSView"))
	objc.Send1(container, selSetWantsLayerV, 1)
	const fill = 2 | 16 // NSViewWidthSizable | NSViewHeightSizable
	objc.Send1(effect, selSetAutoresizing, fill)
	objc.Send1(w.view, selSetAutoresizing, fill)
	objc.Send1(container, selAddSubview, uintptr(effect))
	objc.Send1(container, selAddSubview, uintptr(w.view)) // above the effect view
	objc.Send1(w.win, selSetOpaqueWin, 0)
	clear := F.Reset().Arg(uintptr(objc.Cls("NSColor"))).Arg(uintptr(selColorWhiteAlpha)).ArgF64(1).ArgF64(0.001).Call(objc.MsgSend).R0()
	objc.Send1(w.win, selSetBGColor, clear)
	w.effect, w.backdrop = effect, got
	return container
}

// fitBackdrop gives Liquid Glass the window's corner radius, so that its rim
// follows the window's edge. The radius depends on the toolbar style, so this
// runs once the toolbar is set. _cornerRadius is private (Ghostty reads it the
// same way); without it the glass keeps square corners, which the window's
// own mask rounds.
func (w *Window) fitBackdrop() {
	if w.effect == 0 || w.backdrop == BackdropWindow || !objc.SendBool1(w.win, selRespondsTo, uintptr(selWinCornerRadius)) {
		return
	}
	r := objc.SendF64(w.win, selWinCornerRadius)
	if r > 0 {
		objc.F.Reset().Arg(uintptr(w.effect)).Arg(uintptr(selSetCornerRadius)).ArgF64(r).Call(objc.MsgSend)
	}
}

// makeToolbar creates the toolbar's items and the toolbar, and puts it in
// the window, with the title and subtitle at its leading edge.
func (w *Window) makeToolbar(o Options, delegate objc.ID) {
	F := objc.F
	nsImageClass = objc.Cls("NSImage")
	t := &toolbar{}
	ids := make([]objc.ID, len(o.Toolbar))
	for i, it := range o.Toolbar {
		x := tbItem{ToolbarItem: it, enabled: !it.Disabled}
		x.Tabs = nil
		switch it.Kind {
		case ToolbarFlexibleSpace:
			ids[i] = stringConstant(appKitLib, "NSToolbarFlexibleSpaceItemIdentifier")
		case ToolbarSpace:
			ids[i] = stringConstant(appKitLib, "NSToolbarSpaceItemIdentifier")
		default:
			x.ident = objc.Retain(objc.Str("gnui.toolbar." + strconv.Itoa(i)))
			ids[i] = x.ident
			x.obj = objc.Send1(objc.Send(objc.Cls("NSToolbarItem"), selAlloc), selInitToolbarItem, uintptr(x.ident))
			if it.Label != "" {
				label := objc.Str(it.Label)
				objc.Send1(x.obj, selSetLabel, uintptr(label))
				objc.Send1(x.obj, selSetPaletteLabel, uintptr(label))
				objc.Send1(x.obj, selSetToolTip, uintptr(label))
			}
			objc.Send1(x.obj, selSetAutovalidates, 0) // enabled is ours (SetToolbarEnabled)
			if it.Kind == ToolbarTabs {
				x.seg = objc.Send(objc.Cls("NSSegmentedControl"), selAlloc)
				x.seg = objc.ID(F.Reset().Arg(uintptr(x.seg)).Arg(uintptr(selInitWithFrame)).ArgRect(0, 0, 100, 24).Call(objc.MsgSend).R0())
				objc.Send1(x.seg, selSetTrackingMode, 0) // NSSegmentSwitchTrackingSelectOne
				objc.Send1(x.seg, selSetTarget, uintptr(delegate))
				objc.Send1(x.seg, selSetAction, uintptr(selTBAction))
				objc.Send1(x.seg, selSetTag, uintptr(i))
				fillTabs(&x, it.Tabs, it.Selected)
				objc.Send1(x.obj, selSetView, uintptr(x.seg))
				if it.Disabled {
					objc.Send1(x.seg, selSetEnabled, 0)
				}
			} else {
				if img := symbolImage(it.Symbol, it.Label); img != 0 {
					objc.Send1(x.obj, selSetImage, uintptr(img))
				}
				objc.Send1(x.obj, selSetBordered, 1)
				objc.Send1(x.obj, selSetTarget, uintptr(delegate))
				objc.Send1(x.obj, selSetAction, uintptr(selTBAction))
			}
			objc.Send1(x.obj, selSetTag, uintptr(i))
			if it.Disabled {
				objc.Send1(x.obj, selSetEnabled, 0)
			}
		}
		t.items = append(t.items, x)
	}
	t.idents = objc.Retain(objc.Send2(clsNSArray, selArrayWithObjs, uintptr(unsafe.Pointer(&ids[0])), uintptr(len(ids))))
	runtime.KeepAlive(ids)
	t.tb = objc.Send1(objc.Send(objc.Cls("NSToolbar"), selAlloc), selInitToolbar, uintptr(objc.Str("GNUIToolbar")))
	objc.Send1(t.tb, selSetDelegate, uintptr(delegate))
	objc.Send1(t.tb, selSetDisplayMode, 2) // NSToolbarDisplayModeIconOnly
	objc.Send1(t.tb, selSetCustomizable, 0)
	w.tb, tbWindow = t, w
	objc.Send1(w.win, selSetTitlebarClear, 1)
	objc.Send1(w.win, selSetTitleVisible, 0) // NSWindowTitleVisible
	if o.Subtitle != "" {
		objc.Send1(w.win, selSetSubtitle, uintptr(objc.Str(o.Subtitle)))
	}
	style := uintptr(3) // NSWindowToolbarStyleUnified
	if o.ToolbarCompact {
		style = 4 // NSWindowToolbarStyleUnifiedCompact
	}
	objc.Send1(w.win, selSetToolbarStyle, style)
	objc.Send1(w.win, selSetToolbar, uintptr(t.tb))
}

// symbolImage is the SF Symbol named name, or 0 (an empty name, or a system
// without it).
func symbolImage(name, label string) objc.ID {
	if name == "" {
		return 0
	}
	var desc uintptr
	if label != "" {
		desc = uintptr(objc.Str(label))
	}
	return objc.Send2(nsImageClass, selSymbolImage, uintptr(objc.Str(name)), desc)
}

// fillTabs makes the segments of x's segmented control the tabs given and
// selects one. It allocates (strings, images); it runs when the tabs change.
func fillTabs(x *tbItem, tabs []ToolbarTab, selected int) {
	seg, F := x.seg, objc.F
	width := float64(x.TabWidth)
	if width <= 0 {
		width = toolbarTabW
	}
	objc.Send1(seg, selSetSegmentCount, uintptr(len(tabs)))
	for i, tab := range tabs {
		label := objc.Str(tab.Label)
		objc.Send2(seg, selSetLabelFor, uintptr(label), uintptr(i))
		objc.Send2(seg, selSetToolTipFor, uintptr(label), uintptr(i))
		objc.Send2(seg, selSetImageFor, uintptr(symbolImage(tab.Symbol, tab.Label)), uintptr(i))
		F.Reset().Arg(uintptr(seg)).Arg(uintptr(selSetWidthFor)).ArgF64(width).Arg(uintptr(i)).Call(objc.MsgSend)
	}
	objc.Send1(seg, selSetSelectedSeg, uintptr(selected))
	objc.Send(seg, selSizeToFit)
	x.tabs = append(x.tabs[:0], tabs...)
	x.Selected = selected
}

// toolbarIdentifiers is toolbarDefaultItemIdentifiers: and
// toolbarAllowedItemIdentifiers: (the toolbar is not customisable).
func toolbarIdentifiers(c *ffi.CB) {
	c.Ret(0)
	if w := tbWindow; w != nil && w.tb != nil {
		c.Ret(uintptr(w.tb.idents))
	}
}

// toolbarItemFor is toolbar:itemForItemIdentifier:willBeInsertedIntoToolbar:
// (self, _cmd, toolbar, identifier, flag): the item made in Open.
func toolbarItemFor(c *ffi.CB) {
	c.Ret(0)
	w := tbWindow
	if w == nil || w.tb == nil {
		return
	}
	ident := c.Int(3)
	for i := range w.tb.items {
		x := &w.tb.items[i]
		if x.ident != 0 && (uintptr(x.ident) == ident || objc.SendBool1(x.ident, selIsEqualStr, ident)) {
			c.Ret(uintptr(x.obj))
			return
		}
	}
}

// toolbarAction is gnuiToolbarAction:, the action of every item (sender: the
// NSToolbarItem of a button, the NSSegmentedControl of tabs; its tag is the
// item's index). It queues EvCommand.
func toolbarAction(c *ffi.CB) {
	w := tbWindow
	if w == nil || w.tb == nil {
		return
	}
	sender := objc.ID(c.Int(2))
	i := -1
	for j := range w.tb.items {
		if x := &w.tb.items[j]; sender != 0 && (sender == x.obj || sender == x.seg) {
			i = j
		}
	}
	if i < 0 { // some other view of the item (AppKit's own button): its tag, if it was passed on
		i = int(objc.Send(sender, selTag))
	}
	if i < 0 || i >= len(w.tb.items) {
		return
	}
	x := &w.tb.items[i]
	id := x.ID
	if x.Kind == ToolbarTabs {
		s := int(int64(objc.Send(x.seg, selSelectedSeg)))
		if s < 0 || s >= len(x.tabs) {
			return
		}
		x.Selected, id = s, x.tabs[s].ID
	}
	w.q.push(Event{Kind: EvCommand, Tag: uint64(id), Time: Now()})
}

// find returns the item with the given id that is not a space, or nil.
func (t *toolbar) find(id uint16) *tbItem {
	if t == nil {
		return nil
	}
	for i := range t.items {
		x := &t.items[i]
		if x.ID == id && x.obj != 0 {
			return x
		}
	}
	return nil
}

// SetToolbarTabs makes the tabs of the ToolbarTabs item id the given ones,
// with tab selected selected. Main thread. It compares with what is shown
// and does nothing when nothing changed; a change of selection alone only
// selects, and other changes rebuild the segments (allocating). So it can be
// called every frame. Experimental.
func (w *Window) SetToolbarTabs(id uint16, tabs []ToolbarTab, selected int) {
	x := w.tb.find(id)
	if x == nil || x.Kind != ToolbarTabs {
		return
	}
	if !sameTabs(x.tabs, tabs) {
		pool := objc.F.Call0(objc.PoolPush)
		fillTabs(x, tabs, selected)
		objc.F.Call1(objc.PoolPop, pool)
		return
	}
	if selected != x.Selected {
		objc.Send1(x.seg, selSetSelectedSeg, uintptr(selected))
		x.Selected = selected
	}
}

func sameTabs(a, b []ToolbarTab) bool {
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

// SetToolbarEnabled enables or disables the toolbar item id. Main thread;
// nothing happens when it is unchanged, so it can be called every frame.
// Experimental.
func (w *Window) SetToolbarEnabled(id uint16, enabled bool) {
	x := w.tb.find(id)
	if x == nil || x.enabled == enabled {
		return
	}
	x.enabled = enabled
	var on uintptr
	if enabled {
		on = 1
	}
	objc.Send1(x.obj, selSetEnabled, on)
	if x.seg != 0 {
		objc.Send1(x.seg, selSetEnabled, on)
	}
}
