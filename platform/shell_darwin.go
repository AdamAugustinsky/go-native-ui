//go:build darwin

package platform

import (
	"os"
	"strconv"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// The native shell (experimental, the phase 6 native-shell spike): a system
// material behind the content (Options.Backdrop) and native buttons above it
// (Options.Buttons), with the app's own pixels in between.
//
// Either makes the content view a plain layer-backed container holding
// GNUIView. A backdrop puts the effect view (NSGlassEffectView on macOS 26
// and later, else an NSVisualEffectView blurring what is behind the window)
// below GNUIView, whose CAMetalLayer is then non-opaque (Native.Transparent).
// The window is non-opaque with a background of white at alpha 0.001, which
// keeps its shadow and its clicks where the app paints nothing. The effect
// views follow the window's appearance by themselves (SetDarkChrome).
//
// Buttons are NSButtons above GNUIView: AppKit's hit test gives them their
// presses (pointerEvent then queues nothing for ui) and everything else still
// reaches GNUIView, so a custom titlebar band keeps its drag map and its
// double click. Their target is GNUIWindowDelegate, so they take no ffi
// callback of their own: the action shares the delegate class's dispatcher.
// A click arrives on the main thread, inside sendEvent, as EvCommand.
//
// GNUI_BACKDROP_MATERIAL=n (experiments only) replaces the backdrop by an
// NSVisualEffectView with material n.

var (
	selSetOpaqueWin          = objc.Sel("setOpaque:")
	selSetBGColor            = objc.Sel("setBackgroundColor:")
	selColorWhiteAlpha       = objc.Sel("colorWithWhite:alpha:")
	selSetWantsLayerV        = objc.Sel("setWantsLayer:")
	selSetAutoresizing       = objc.Sel("setAutoresizingMask:")
	selAddSubview            = objc.Sel("addSubview:")
	selAddSubviewPos         = objc.Sel("addSubview:positioned:relativeTo:")
	selSetMaterial           = objc.Sel("setMaterial:")
	selSetBlendingMode       = objc.Sel("setBlendingMode:")
	selSetEffectState        = objc.Sel("setState:")
	selSetGlassStyle         = objc.Sel("setStyle:")
	selSetCornerRadius       = objc.Sel("setCornerRadius:")
	selWinCornerRadius       = objc.Sel("_cornerRadius")
	selSetImage              = objc.Sel("setImage:")
	selSetImagePosition      = objc.Sel("setImagePosition:")
	selSetBezelStyle         = objc.Sel("setBezelStyle:")
	selSetBorderShape        = objc.Sel("setBorderShape:")
	selSetShowsBorderOnHover = objc.Sel("setShowsBorderOnlyWhileMouseInside:")
	selSetToolTip            = objc.Sel("setToolTip:")
	selSetTarget             = objc.Sel("setTarget:")
	selSetAction             = objc.Sel("setAction:")
	selSetTag                = objc.Sel("setTag:")
	selTag                   = objc.Sel("tag")
	selSetBordered           = objc.Sel("setBordered:")
	selSetEnabled            = objc.Sel("setEnabled:")
	selSymbolImage           = objc.Sel("imageWithSystemSymbolName:accessibilityDescription:")
	selButtonAction          = objc.Sel("gnuiButtonAction:")
	selSetRefusesFirstResp   = objc.Sel("setRefusesFirstResponder:")
	selRemoveFromSuper       = objc.Sel("removeFromSuperview")
	selContentView           = objc.Sel("contentView")
	selWillEnterFull         = objc.Sel("windowWillEnterFullScreen:")
	selDidFailFull           = objc.Sel("windowDidFailToEnterFullScreen:")
	selDidExitFull           = objc.Sel("windowDidExitFullScreen:")

	nsImageClass  objc.ID
	glassMaterial = map[Backdrop]uintptr{BackdropGlass: 0, BackdropGlassClear: 1}
)

// registerShell adds the native buttons' action and the full-screen
// notifications (the glass's corners) to GNUIWindowDelegate.
func registerShell(d *objc.ClassDef) {
	d.Method(selButtonAction, "v@:@", buttonAction)
	d.Method(selWillEnterFull, "v@:@", windowWillEnterFullScreen)
	d.Method(selDidFailFull, "v@:@", windowDidLeaveFullScreen)
	d.Method(selDidExitFull, "v@:@", windowDidLeaveFullScreen)
}

// windowWillEnterFullScreen: squares the glass's corners before the zoom
// starts: a full-screen window has none.
func windowWillEnterFullScreen(*ffi.CB) {
	if w := theWindow.Load(); w != nil {
		w.fullScreen = true
		w.fitBackdrop()
	}
}

// windowDidLeaveFullScreen is windowDidExitFullScreen: and
// windowDidFailToEnterFullScreen:: the window's corners are back.
func windowDidLeaveFullScreen(*ffi.CB) {
	if w := theWindow.Load(); w != nil {
		w.fullScreen = false
		w.fitBackdrop()
		w.layoutChromeSoon() // AppKit dropped the titlebar's height for full screen
	}
}

// nativeButton is one button of Options.Buttons and its NSButton.
type nativeButton struct {
	NativeButton
	obj     objc.ID
	enabled bool
}

// Backdrop is the material the window got (Options.Backdrop): BackdropWindow
// where Liquid Glass was asked for but the system has none, BackdropNone if no
// backdrop was asked for or none could be made. Experimental.
func (w *Window) Backdrop() Backdrop { return w.backdrop }

// HasButtons reports whether the window has native buttons
// (Options.Buttons). Experimental.
func (w *Window) HasButtons() bool { return len(w.buttons) > 0 }

// makeBackdrop puts the effect view under GNUIView (in the container) and
// makes the window transparent. Without an effect view nothing changes.
func (w *Window) makeBackdrop(kind Backdrop, width, height float64) {
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
			return
		}
		objc.Send1(effect, selSetMaterial, material)
		objc.Send1(effect, selSetBlendingMode, 0) // NSVisualEffectBlendingModeBehindWindow
		objc.Send1(effect, selSetEffectState, 0)  // NSVisualEffectStateFollowsWindowActiveState
	}
	w.makeContainer(width, height)
	objc.Send1(effect, selSetAutoresizing, 2|16) // NSViewWidthSizable | NSViewHeightSizable
	F.Reset().Arg(uintptr(w.content)).Arg(uintptr(selAddSubviewPos)).Arg(uintptr(effect)).Arg(^uintptr(0) /* NSWindowBelow */).Arg(uintptr(w.view)).Call(objc.MsgSend)
	objc.Send(effect, selReleaseObj) // the container owns it now; w.effect does not
	objc.Send1(w.win, selSetOpaqueWin, 0)
	clear := F.Reset().Arg(uintptr(objc.Cls("NSColor"))).Arg(uintptr(selColorWhiteAlpha)).ArgF64(1).ArgF64(0.001).Call(objc.MsgSend).R0()
	objc.Send1(w.win, selSetBGColor, clear)
	w.effect, w.backdrop = effect, got
}

// fitBackdrop gives Liquid Glass the window's corner radius, so that its rim
// follows the window's edge: 0 in full screen, which has no corners.
// _cornerRadius is private (Ghostty reads it the same way); without it the
// glass keeps square corners, which the window's own mask rounds. It runs at
// Open, on every resize and around full screen, and sends nothing while the
// radius is unchanged.
func (w *Window) fitBackdrop() {
	if w.effect == 0 || w.backdrop == BackdropWindow {
		return
	}
	r := 0.0
	if !w.fullScreen && objc.Send(w.win, selStyleMask)&(1<<14) == 0 && // NSWindowStyleMaskFullScreen
		objc.SendBool1(w.win, selRespondsTo, uintptr(selWinCornerRadius)) {
		r = max(objc.SendF64(w.win, selWinCornerRadius), 0)
	}
	if r == w.glassRadius {
		return
	}
	w.glassRadius = r
	objc.F.Reset().Arg(uintptr(w.effect)).Arg(uintptr(selSetCornerRadius)).ArgF64(r).Call(objc.MsgSend)
}

// closeShell takes the shell apart (Close, and Open's failure path): the
// buttons drop their target, the delegate, and leave the container with the
// effect view, which frees both; the container leaves the window and goes
// with its creation reference, so it frees our view's hold too. GNUIView
// itself stays w.view's (Close does not free it).
func (w *Window) closeShell() {
	for i := range w.buttons {
		objc.Send1(w.buttons[i].obj, selSetTarget, 0)
		objc.Send(w.buttons[i].obj, selRemoveFromSuper)
	}
	w.buttons = nil
	if w.effect != 0 {
		objc.Send(w.effect, selRemoveFromSuper)
		w.effect = 0
	}
	if w.content != w.view {
		if objc.Send(w.win, selContentView) == w.content {
			objc.Send1(w.win, selSetContentView, 0)
		}
		objc.Send(w.content, selReleaseObj) // makeContainer's
		w.content = w.view
	}
}

// makeContainer makes the content view a plain layer-backed container
// holding view, for the native views that go below it (the backdrop) and
// above it (the buttons). Apple says a layer-hosting view such as ours must
// not have subviews, so they are its siblings.
func (w *Window) makeContainer(width, height float64) {
	if w.content != w.view {
		return
	}
	c := objc.Send(objc.Cls("NSView"), selAlloc)
	c = objc.ID(objc.F.Reset().Arg(uintptr(c)).Arg(uintptr(selInitWithFrame)).ArgRect(0, 0, width, height).Call(objc.MsgSend).R0())
	objc.Send1(c, selSetWantsLayerV, 1)
	objc.Send1(w.view, selSetAutoresizing, 2|16) // NSViewWidthSizable | NSViewHeightSizable
	objc.Send1(c, selAddSubview, uintptr(w.view))
	w.content = c
}

// makeButtons puts the native buttons above view, in the container.
// Liquid Glass circles on macOS 26 and later (ButtonGlass), borderless
// symbol buttons otherwise and for ButtonPlain; both highlight on hover and
// press as AppKit's do and follow the window's key state and appearance.
func (w *Window) makeButtons(bs []NativeButton, delegate objc.ID, width, height float64) {
	w.makeContainer(width, height)
	glass := objc.Cls("NSGlassEffectView") != 0 // macOS 26: NSBezelStyleGlass exists too
	nsImageClass = objc.Cls("NSImage")
	for i, b := range bs {
		size := float64(b.Size)
		if size <= 0 {
			size = 28
		}
		cx := float64(b.X)
		mask := uintptr(4 | 8) // NSViewMaxXMargin | NSViewMinYMargin: stays at the top left
		if cx < 0 {
			cx, mask = width+cx, 1|8 // NSViewMinXMargin: stays at the right edge
		}
		x, y := cx-size/2, height-float64(b.Y)-size/2 // the container is not flipped
		o := objc.Send(objc.Cls("NSButton"), selAlloc)
		o = objc.ID(objc.F.Reset().Arg(uintptr(o)).Arg(uintptr(selInitWithFrame)).ArgRect(x, y, size, size).Call(objc.MsgSend).R0())
		objc.Send1(o, selSetTitle, uintptr(objc.Str("")))
		if img := symbolImage(b.Symbol, b.Label); img != 0 {
			objc.Send1(o, selSetImage, uintptr(img))
		}
		objc.Send1(o, selSetImagePosition, 1) // NSImageOnly
		if glass && b.Style == ButtonGlass {
			objc.Send1(o, selSetBezelStyle, 16) // NSBezelStyleGlass
			objc.Send1(o, selSetBorderShape, 3) // NSControlBorderShapeCircle
		} else {
			objc.Send1(o, selSetBordered, 0)
			objc.Send1(o, selSetShowsBorderOnHover, 1) // AppKit's hover highlight for a borderless button (macOS 26), ignored before
		}
		if b.Label != "" {
			objc.Send1(o, selSetToolTip, uintptr(objc.Str(b.Label)))
		}
		objc.Send1(o, selSetTarget, uintptr(delegate))
		objc.Send1(o, selSetAction, uintptr(selButtonAction))
		objc.Send1(o, selSetTag, uintptr(i))
		objc.Send1(o, selSetAutoresizing, mask)
		if b.Disabled {
			objc.Send1(o, selSetEnabled, 0)
		}
		// Titlebar controls never take the keyboard, even with full keyboard
		// access on: Tab stays with the content, and a click does not move
		// the first responder (nor end an input method's composition).
		objc.Send1(o, selSetRefusesFirstResp, 1)
		objc.Send1(w.content, selAddSubview, uintptr(o)) // above view
		objc.Send(o, selReleaseObj)                      // the container owns it now; w.buttons does not
		w.buttons = append(w.buttons, nativeButton{NativeButton: b, obj: o, enabled: !b.Disabled})
	}
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

// buttonAction is gnuiButtonAction:, the action of every native button
// (sender: the NSButton, whose tag is its index). It queues EvCommand.
func buttonAction(c *ffi.CB) {
	w := theWindow.Load()
	if w == nil {
		return
	}
	i := int(objc.Send(objc.ID(c.Int(2)), selTag))
	if i < 0 || i >= len(w.buttons) {
		return
	}
	w.q.push(Event{Kind: EvCommand, Tag: uint64(w.buttons[i].ID), Time: Now()})
}

// SetButtonEnabled enables or disables the native button id. Main thread;
// nothing happens when it is unchanged, so it can be called every frame.
// Experimental.
func (w *Window) SetButtonEnabled(id uint16, enabled bool) {
	for i := range w.buttons {
		b := &w.buttons[i]
		if b.ID != id || b.enabled == enabled {
			continue
		}
		b.enabled = enabled
		var on uintptr
		if enabled {
			on = 1
		}
		objc.Send1(b.obj, selSetEnabled, on)
	}
}
