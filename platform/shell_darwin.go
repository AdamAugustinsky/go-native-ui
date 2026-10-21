//go:build darwin

package platform

import (
	"math"
	"os"
	"strconv"
	"sync/atomic"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// The native shell (D41, D52; shell.go has the overview): material views
// below the app's view (material_darwin.go), hosted native views above it
// (host_darwin.go), and the two cases the spike introduced:
//
//   - Options.Backdrop is a material region of AreaWindow made at Open
//     (NSGlassEffectView on macOS 26 and later, else an NSVisualEffectView
//     blurring what is behind the window), pinned under everything and sized
//     with the window by AppKit. The window is non-opaque with a background
//     of white at alpha 0.001, which keeps its shadow and its clicks where
//     the app paints nothing.
//   - Options.Buttons are HostButton hosts made at Open, pinned at their
//     positions by autoresizing: AppKit's hit test gives them their presses
//     (pointerEvent then queues nothing for ui) and everything else still
//     reaches GNUIView, so a custom titlebar band keeps its drag map and its
//     double click.
//
// Either, or Options.Materials, or the first NewHost, makes the content view
// a flipped layer-backed container (GNUIContainer) holding GNUIView. The
// shell's views follow the window's appearance by themselves (SetDarkChrome).
// Buttons' target is GNUIWindowDelegate, so they take no ffi callback of
// their own: the action shares the delegate class's dispatcher. A click
// arrives on the main thread, inside sendEvent, as EvCommand.
//
// GNUI_BACKDROP_MATERIAL=n (experiments only) replaces the backdrop by an
// NSVisualEffectView with material n.

var (
	selSetFrame              = objc.Sel("setFrame:")
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

// shell is the window's native shell state (embedded in Window).
type shell struct {
	effect      objc.ID // the backdrop's effect view (Options.Backdrop), 0 without one
	backdrop    Backdrop
	buttons     []nativeButton // Options.Buttons, each a pinned host
	glassRadius float64        // the backdrop glass's corner radius as last set (fitBackdrop), -1 before
	fullScreen  bool           // between windowWillEnterFullScreen: and its exit
	materials   bool           // Options.Materials: the surface is transparent over an opaque window
	delegate    objc.ID        // the window delegate, the native buttons' target

	mat   matState  // material regions (material_darwin.go)
	hosts hostState // hosted views (host_darwin.go)
}

// Live native shell objects in the process, and applies (ShellObjects).
var liveHosts, liveMaterials, shellApplies, shellSynced atomic.Int64

// ShellObjects counts the native shell objects alive in the process (hosts
// created and not released, and material views) and how their changes were
// applied. The leak gate checks that the objects return to where they were
// after windows open and close.
func ShellObjects() ShellCounts {
	return ShellCounts{Hosts: liveHosts.Load(), Materials: liveMaterials.Load(), Applies: shellApplies.Load(), Synced: shellSynced.Load()}
}

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
func windowWillEnterFullScreen(c *ffi.CB) {
	if w := delegateWindow(objc.ID(c.Int(0))); w != nil {
		w.fullScreen = true
		w.fitBackdrop()
	}
}

// windowDidLeaveFullScreen is windowDidExitFullScreen: and
// windowDidFailToEnterFullScreen:: the window's corners are back.
func windowDidLeaveFullScreen(c *ffi.CB) {
	if w := delegateWindow(objc.ID(c.Int(0))); w != nil {
		w.fullScreen = false
		w.fitBackdrop()
		w.layoutChromeSoon() // AppKit dropped the titlebar's height for full screen
	}
}

// nativeButton is one button of Options.Buttons and its host.
type nativeButton struct {
	NativeButton
	host HostID
	obj  objc.ID // the NSButton
}

// Backdrop is the material the window got (Options.Backdrop): BackdropWindow
// where Liquid Glass was asked for but the system has none, BackdropNone if no
// backdrop was asked for or none could be made. Experimental.
func (w *Window) Backdrop() Backdrop { return w.backdrop }

// HasButtons reports whether the window has native buttons
// (Options.Buttons). Experimental.
func (w *Window) HasButtons() bool { return len(w.buttons) > 0 }

// HasMaterials reports whether the window shows material regions: it was
// opened with Options.Materials (or a backdrop) and its surface is
// transparent.
func (w *Window) HasMaterials() bool { return w.materials || w.backdrop != BackdropNone }

// openShell builds what Options asks of the shell, at Open, before the
// content view is set.
func (w *Window) openShell(o *Options, delegate objc.ID, width, height float64) {
	w.delegate = delegate
	if o.Backdrop != BackdropNone {
		w.makeBackdrop(o.Backdrop, width, height)
		markOpen("backdrop")
	}
	if o.Materials {
		w.makeContainer(width, height)
		w.materials = true
	}
	if len(o.Buttons) > 0 {
		w.makeButtons(o.Buttons, width, height)
		markOpen("buttons")
	}
}

// makeBackdrop puts the effect view under GNUIView (in the container) and
// makes the window transparent. Without an effect view nothing changes. The
// backdrop is the pinned bottom material, outside the region list.
func (w *Window) makeBackdrop(kind Backdrop, width, height float64) {
	got, material := kind, uintptr(21) // NSVisualEffectMaterialUnderWindowBackground
	if m, err := strconv.Atoi(os.Getenv("GNUI_BACKDROP_MATERIAL")); err == nil {
		got, material = BackdropWindow, uintptr(m)
	}
	var effect objc.ID
	if style, glass := glassMaterial[got]; glass {
		if effect = newViewOf(objc.Cls("NSGlassEffectView"), 0, 0, width, height); effect != 0 {
			objc.Send1(effect, selSetGlassStyle, style)
		}
	}
	if effect == 0 {
		got = BackdropWindow
		if effect = newViewOf(objc.Cls("NSVisualEffectView"), 0, 0, width, height); effect == 0 {
			return
		}
		objc.Send1(effect, selSetMaterial, material)
		objc.Send1(effect, selSetBlendingMode, 0) // NSVisualEffectBlendingModeBehindWindow
		objc.Send1(effect, selSetEffectState, 0)  // NSVisualEffectStateFollowsWindowActiveState
	}
	w.makeContainer(width, height)
	objc.Send1(effect, selSetAutoresizing, 2|16) // NSViewWidthSizable | NSViewHeightSizable
	objc.F.Reset().Arg(uintptr(w.content)).Arg(uintptr(selAddSubviewPos)).Arg(uintptr(effect)).Arg(^uintptr(0) /* NSWindowBelow */).Arg(uintptr(w.view)).Call(objc.MsgSend)
	objc.Send(effect, selReleaseObj) // the container owns it now; w.effect does not
	liveMaterials.Add(1)
	objc.Send1(w.win, selSetOpaqueWin, 0)
	clear := objc.F.Reset().Arg(uintptr(objc.Cls("NSColor"))).Arg(uintptr(selColorWhiteAlpha)).ArgF64(1).ArgF64(0.001).Call(objc.MsgSend).R0()
	objc.Send1(w.win, selSetBGColor, clear)
	w.effect, w.backdrop = effect, got
	w.glassRadius = -1 // not set yet: the glass view's own default is not the window's
}

// newViewOf is [[cls alloc] initWithFrame:] (a +1 reference), 0 for a class
// the system does not have.
func newViewOf(cls objc.ID, x, y, width, height float64) objc.ID {
	if cls == 0 {
		return 0
	}
	v := objc.Send(cls, selAlloc)
	return objc.ID(objc.F.Reset().Arg(uintptr(v)).Arg(uintptr(selInitWithFrame)).ArgRect(x, y, width, height).Call(objc.MsgSend).R0())
}

// fitBackdrop gives Liquid Glass the window's corner radius, so that its rim
// follows the window's edge: 0 in full screen, which has no corners.
// The radius is private (windowCornerRadius); without it (Window.privateAPI
// false) the glass keeps square corners, which the window's own
// mask rounds. It runs at
// Open, on every resize and around full screen, and sends nothing while the
// radius is unchanged.
func (w *Window) fitBackdrop() {
	if w.effect == 0 || w.backdrop == BackdropWindow {
		return
	}
	r := 0.0
	if !w.fullScreen && w.privateAPI() && objc.Send(w.win, selStyleMask)&(1<<14) == 0 { // NSWindowStyleMaskFullScreen
		if v, ok := windowCornerRadius(w.win); ok {
			r = max(v, 0)
		}
	}
	if r == w.glassRadius {
		return
	}
	w.glassRadius = r
	objc.F.Reset().Arg(uintptr(w.effect)).Arg(uintptr(selSetCornerRadius)).ArgF64(r).Call(objc.MsgSend)
}

// closeShell takes the shell apart (Close, and Open's failure path): every
// host is released (the buttons drop their target), the material views and
// the backdrop leave the container, which frees them; the container leaves
// the window and goes with its creation reference, so it frees our view's
// hold too. GNUIView itself stays w.view's (Close does not free it).
func (w *Window) closeShell() {
	w.releaseAllHosts()
	w.buttons = nil
	w.closeMaterials()
	if w.effect != 0 {
		objc.Send(w.effect, selRemoveFromSuper)
		w.effect = 0
		liveMaterials.Add(-1)
	}
	if w.content != w.view {
		if objc.Send(w.win, selContentView) == w.content {
			objc.Send1(w.win, selSetContentView, 0)
		}
		objc.Send(w.content, selReleaseObj) // makeContainer's
		w.content = w.view
	}
}

// makeContainer makes the content view a flipped layer-backed container
// (GNUIContainer) holding view, for the native views that go below it (the
// materials) and above it (the hosts). Apple says a layer-hosting view such
// as ours must not have subviews, so they are its siblings. Before the
// content view is set (Open) it only builds the container; later (the first
// NewHost of a window without one) it also swaps it in.
func (w *Window) makeContainer(width, height float64) {
	if w.content != w.view {
		return
	}
	c := newViewOf(containerClass, 0, 0, width, height)
	objc.Send1(c, selSetWantsLayerV, 1)
	objc.Send1(w.view, selSetAutoresizing, 2|16) // NSViewWidthSizable | NSViewHeightSizable
	if objc.Send(w.win, selContentView) == w.view {
		objc.Retain(w.view) // setContentView: below drops the window's hold
		objc.Send1(w.win, selSetContentView, uintptr(c))
		objc.F.Reset().Arg(uintptr(w.view)).Arg(uintptr(selSetFrame)).ArgRect(0, 0, width, height).Call(objc.MsgSend)
		objc.Send1(c, selAddSubview, uintptr(w.view))
		objc.Send(w.view, selReleaseObj)
		objc.Send1(w.win, selMakeFirstResp, uintptr(w.view))
	} else {
		objc.Send1(c, selAddSubview, uintptr(w.view))
	}
	w.content = c
}

// makeButtons puts Options.Buttons above view, in the container, as pinned
// hosts: Liquid Glass circles on macOS 26 and later (ButtonGlass), borderless
// symbol buttons otherwise and for ButtonPlain; both highlight on hover and
// press as AppKit's do and follow the window's key state and appearance.
func (w *Window) makeButtons(bs []NativeButton, width, height float64) {
	w.makeContainer(width, height)
	for _, b := range bs {
		mask := uintptr(4 | 32) // NSViewMaxXMargin | NSViewMaxYMargin (the container is flipped): stays at the top left
		if b.X < 0 {
			mask = 1 | 32 // NSViewMinXMargin: stays at the right edge
		}
		id, err := w.newHost(HostButton, HostOptions{Symbol: b.Symbol, Label: b.Label, Style: b.Style, Command: b.ID, Disabled: b.Disabled}, true)
		if err != nil {
			continue
		}
		s := w.hosts.slot(id)
		s.pinned = true
		objc.Send1(s.clip, selSetAutoresizing, mask)
		objc.Send1(s.clip, selSetHidden, 0)
		s.shown = true
		w.buttons = append(w.buttons, nativeButton{NativeButton: b, host: id, obj: s.view})
	}
	w.fitButtons()
}

// fitButtons places the pinned buttons at their exact, possibly fractional
// frames (rexlook's are), directly in the container as the spike's buttons
// were in its unflipped one, so AppKit draws them and Core Animation aligns
// them to the same pixels at any scale. (Inside a clip view they are not
// aligned and blur at 1x.) A top edge exactly between two pixels rounds
// downward in the flipped container and upward in an unflipped one, so the
// top is nudged by one ulp toward the spike's rounding. It runs at Open;
// autoresizing keeps the buttons in place after that.
func (w *Window) fitButtons() {
	bounds := objc.SendRect(w.content, selBounds)
	for i := range w.buttons {
		b := &w.buttons[i]
		s := w.hosts.slot(b.host)
		if s == nil {
			continue
		}
		size := float64(b.Size)
		if size <= 0 {
			size = 28
		}
		cx := float64(b.X)
		if cx < 0 {
			cx += bounds.W
		}
		top := math.Nextafter(float64(b.Y)-size/2, math.Inf(-1))
		objc.F.Reset().Arg(uintptr(s.clip)).Arg(uintptr(selSetFrame)).ArgRect(cx-size/2, top, size, size).Call(objc.MsgSend)
	}
}

// symbolImage is the SF Symbol named name, or 0 (an empty name, or a system
// without it).
func symbolImage(name, label string) objc.ID {
	if name == "" {
		return 0
	}
	if nsImageClass == 0 {
		nsImageClass = objc.Cls("NSImage")
	}
	var desc uintptr
	if label != "" {
		desc = uintptr(objc.Str(label))
	}
	return objc.Send2(nsImageClass, selSymbolImage, uintptr(objc.Str(name)), desc)
}

// buttonAction is gnuiButtonAction:, the action of every HostButton (sender:
// the NSButton, whose tag is its host slot). It queues EvCommand.
func buttonAction(c *ffi.CB) {
	w := delegateWindow(objc.ID(c.Int(0)))
	if w == nil {
		return
	}
	i := int(objc.Send(objc.ID(c.Int(2)), selTag))
	if i < 0 || i >= MaxHosts || !w.hosts.slots[i].live {
		return
	}
	w.q.push(Event{Kind: EvCommand, Tag: uint64(w.hosts.slots[i].command), Time: Now()})
}

// SetButtonEnabled enables or disables the native button id
// (Options.Buttons; the command of a HostButton). Main thread; nothing
// happens when it is unchanged, so it can be called every frame.
// Experimental.
func (w *Window) SetButtonEnabled(id uint16, enabled bool) {
	for i := range w.hosts.slots {
		s := &w.hosts.slots[i]
		if !s.live || s.kind != HostButton || s.command != id || s.enabled == enabled {
			continue
		}
		s.enabled = enabled
		var on uintptr
		if enabled {
			on = 1
		}
		objc.Send1(s.view, selSetEnabled, on)
	}
}

// ShellPending reports whether a window has staged material or host changes
// for its next present. A surface asks about its own window with its
// Native's ShellPending. Main thread.
func ShellPending() bool {
	for _, w := range openWindows() {
		if w.mat.dirty || w.hosts.dirty {
			return true
		}
	}
	return false
}

// ApplyShell applies every window's staged material and host changes; a
// surface applies its own window's with its Native's ApplyShell. Main thread.
func ApplyShell() {
	for _, w := range openWindows() {
		w.applyShell()
	}
}

// ShellPending reports whether the window n belongs to has staged material
// or host changes for the next present (gpu.Surface then presents in step
// with them). Main thread.
func (n Native) ShellPending() bool {
	w := n.window()
	return w != nil && (w.mat.dirty || w.hosts.dirty)
}

// ApplyShell applies the staged material and host changes of the window n
// belongs to. gpu.Surface calls it inside the Core Animation transaction
// that presents the frame (with actions disabled and presentsWithTransaction
// on), so the native views move together with the pixels drawn for them.
// Main thread.
func (n Native) ApplyShell() {
	if w := n.window(); w != nil {
		w.applyShell()
	}
}

// FlushShell applies whatever is still staged (a frame that was not
// presented through the GPU surface); nothing when nothing is. Main thread.
func (w *Window) FlushShell() {
	if w.mat.dirty || w.hosts.dirty {
		objc.Send(clsCATransaction(), selBegin)
		objc.Send1(clsCATransaction(), selSetDisableActions, 1)
		w.applyShell()
		objc.Send(clsCATransaction(), selCommit)
	}
}

func (w *Window) applyShell() {
	shellApplies.Add(1)
	// The view's layer is the surface's CAMetalLayer, or none; but a window
	// without a surface whose hosts asked for layers has an ordinary backing
	// layer, which does not know presentsWithTransaction (AppKit threw, and
	// the process aborted: TestKeyEquivalentWithHostFocused with -count=3).
	if l := objc.Send(w.view, selLayerOf); objc.SendBool(clsCATransaction(), selDisableActions) &&
		objc.SendBool1(l, selRespondsTo, uintptr(selPresentsWithTx)) && objc.SendBool(l, selPresentsWithTx) {
		shellSynced.Add(1)
	}
	if w.mat.dirty {
		w.applyMaterials()
	}
	if w.hosts.dirty {
		w.applyHosts()
	}
}

var (
	selBegin             = objc.Sel("begin")
	selCommit            = objc.Sel("commit")
	selSetDisableActions = objc.Sel("setDisableActions:")
	selDisableActions    = objc.Sel("disableActions")
	selPresentsWithTx    = objc.Sel("presentsWithTransaction")
	selSetHidden         = objc.Sel("setHidden:")
	caTransaction        objc.ID
)

func clsCATransaction() objc.ID {
	if caTransaction == 0 {
		caTransaction = objc.Cls("CATransaction")
	}
	return caTransaction
}
