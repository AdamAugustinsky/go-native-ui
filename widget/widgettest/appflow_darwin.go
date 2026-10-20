//go:build darwin

package widgettest

import (
	"sync"
	"time"
	"unsafe"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// The AppKit side of app-level flows: what a person does to the application
// besides using a window. All of it is main-thread, like the rest of the
// AppKit helpers (LiveApp.Do, or the main thread of a Scenario).

// PlainWindow is a window of the process that is not the framework's: an
// ordinary titled NSWindow with no content, to stand for "another window"
// (the key window moves to it, it keeps the application alive, the Dock's
// reopen counts it) until the framework opens windows of its own (F4).
type PlainWindow struct {
	// Window is the NSWindow*.
	Window objc.ID
}

// OpenPlainWindow opens a plain titled window at (x, y) in screen points
// (0, 0: the system's choice) and orders it front without activating the
// application. Main thread.
func OpenPlainWindow(title string, x, y float64) PlainWindow {
	str := objc.Str(title)
	sel := objc.Sel("initWithContentRect:styleMask:backing:defer:")
	w := objc.Send(objc.Cls("NSWindow"), objc.Sel("alloc"))
	if x == 0 && y == 0 {
		x, y = 80, 80
	}
	w = objc.ID(objc.F.Reset().Arg(uintptr(w)).Arg(uintptr(sel)).ArgRect(x, y, 240, 160).Arg(1 | 2 | 4 | 8).Arg(2).ArgBool(false).Call(objc.MsgSend).R0())
	objc.Send1(w, objc.Sel("setReleasedWhenClosed:"), 0)
	objc.Send1(w, objc.Sel("setTitle:"), uintptr(str))
	objc.Send(w, objc.Sel("orderFrontRegardless"))
	return PlainWindow{w}
}

// Close closes the window (performClose is not used: no delegate asks).
func (p PlainWindow) Close() { objc.Send(p.Window, objc.Sel("close")) }

// Hide orders the window out, the way the Dock's "hide" and a minimised
// window leave no visible window.
func (p PlainWindow) Hide() { objc.Send1(p.Window, objc.Sel("orderOut:"), 0) }

// Visible reports whether the window is on screen.
func (p PlainWindow) Visible() bool { return objc.SendBool(p.Window, selIsVisible) }

// SetAppAppearance makes the application draw as dark (true) or light (false)
// by setting NSApp's appearance, the flip an app-wide override does (it is
// the application's own, not the system's setting, which a test must not
// change). The platform sees it as a change of the system appearance
// (key-value observing on effectiveAppearance), so ui.Context.SystemAppearance
// follows on the next frame. Main thread.
func SetAppAppearance(dark bool) {
	name := "NSAppearanceNameAqua" // the constants' values are their names
	if dark {
		name = "NSAppearanceNameDarkAqua"
	}
	a := objc.Send1(objc.Cls("NSAppearance"), objc.Sel("appearanceNamed:"), uintptr(objc.Str(name)))
	objc.Send1(NSApp(), objc.Sel("setAppearance:"), uintptr(a))
}

// ResetAppAppearance removes the override: the application follows the system
// again. Main thread.
func ResetAppAppearance() { objc.Send1(NSApp(), objc.Sel("setAppearance:"), 0) }

// MenuItem is one item of the application's main menu, found by its title
// path.
type MenuItem struct {
	// Menu is the NSMenu holding the item and Index its position in it.
	Menu  objc.ID
	Index int
	// Item is the NSMenuItem*.
	Item objc.ID
	// Title is the item's title.
	Title string
	// Enabled is the item's enabled state after the menu validated it, and
	// Checked whether it shows a check mark.
	Enabled, Checked bool
	// KeyEquivalent is the item's key equivalent ("q" for ⌘Q) and Modifiers its
	// NSEventModifierFlags (the ⌘ of ⌘Q is 1<<20).
	KeyEquivalent string
	Modifiers     uint
}

// FindMenuItem finds the item at path in NSApp's main menu: the titles from
// the menu bar down ("File", "Clear"). A title matches the item's own title or
// its submenu's, so the application menu is found by the app's name. ok is
// false when there is no such item. Main thread.
func FindMenuItem(path ...string) (MenuItem, bool) {
	if len(path) == 0 {
		return MenuItem{}, false
	}
	menu := objc.Send(NSApp(), objc.Sel("mainMenu"))
	var cur MenuItem
	for _, want := range path {
		if menu == 0 {
			return MenuItem{}, false
		}
		found := false
		n := int(objc.Send(menu, objc.Sel("numberOfItems")))
		for i := 0; i < n && !found; i++ {
			it := objc.Send1(menu, objc.Sel("itemAtIndex:"), uintptr(i))
			sub := objc.Send(it, objc.Sel("submenu"))
			title := goString(objc.Send(it, selTitle))
			if title != want && sub != 0 && goString(objc.Send(sub, selTitle)) == want {
				title = want
			}
			if title == want {
				cur = MenuItem{Menu: menu, Index: i, Item: it, Title: title}
				menu, found = sub, true
			}
		}
		if !found {
			return MenuItem{}, false
		}
	}
	// validate, as AppKit does when the menu opens, then read the state
	objc.Send(cur.Menu, objc.Sel("update"))
	cur.Enabled = objc.SendBool(cur.Item, objc.Sel("isEnabled"))
	cur.Checked = objc.Send(cur.Item, objc.Sel("state")) == 1
	cur.KeyEquivalent = goString(objc.Send(cur.Item, objc.Sel("keyEquivalent")))
	cur.Modifiers = uint(objc.Send(cur.Item, objc.Sel("keyEquivalentModifierMask")))
	return cur, true
}

// Perform chooses the item as the user does with the mouse: the menu
// validates it and sends its action to its target (performActionForItemAtIndex:).
// It reports false when the item is disabled, which sends nothing.
func (m MenuItem) Perform() bool {
	if !m.Enabled {
		return false
	}
	objc.Send1(m.Menu, objc.Sel("performActionForItemAtIndex:"), uintptr(m.Index))
	return true
}

// AppleEvent is a four-character code pair of an Apple event: class and ID.
type AppleEvent struct {
	// Class and ID are the event's class and ID codes.
	Class, ID uint32
}

// QuitAppleEvent is kAEQuitApplication ('aevt' 'quit'), the event the Dock's
// Quit and the logout send.
var QuitAppleEvent = AppleEvent{0x61657674, 0x71756974}

// SendToSelf sends the Apple event to this process and returns how it went:
// the NSError code of the send (0: sent) and the reply's error number (the
// 'errn' parameter; 0: none). wait asks for the reply (kAEWaitReply) and gives
// the handler timeout seconds; an event sent to the process itself is handled
// inside the send, on the sending thread, so a handler that suspends the event
// answers after the send returned and the send fails with errAETimeout
// (-1712). fill adds parameters. That an event addressed to the own process
// needs no Automation grant is what this relies on; sendErr says when it did
// not. Main thread.
func (e AppleEvent) SendToSelf(wait bool, timeout float64, fill func(ev objc.ID)) (replyErr, sendErr int) {
	cls := objc.Cls("NSAppleEventDescriptor")
	selCur, selMake := objc.Sel("currentProcessDescriptor"), objc.Sel("appleEventWithEventClass:eventID:targetDescriptor:returnID:transactionID:")
	target := objc.Send(cls, selCur)
	ev := objc.ID(objc.F.Reset().Arg(uintptr(cls)).Arg(uintptr(selMake)).Arg(uintptr(e.Class)).Arg(uintptr(e.ID)).
		Arg(uintptr(target)).Arg(^uintptr(0)).Arg(0).Call(objc.MsgSend).R0())
	if fill != nil {
		fill(ev)
	}
	opts := uintptr(1) // kAENoReply
	if wait {
		opts = 3 // kAEWaitReply
	}
	selSend := objc.Sel("sendEventWithOptions:timeout:error:")
	errOut := new(uintptr) // on the heap: the call may call back into Go
	reply := objc.ID(objc.F.Reset().Arg(uintptr(ev)).Arg(uintptr(selSend)).Arg(opts).ArgF64(timeout).
		ArgPtr(unsafe.Pointer(errOut)).Call(objc.MsgSend).R0())
	if *errOut != 0 {
		return 0, int(int64(objc.Send(objc.ID(*errOut), objc.Sel("code"))))
	}
	if reply != 0 {
		if d := objc.Send1(reply, objc.Sel("paramDescriptorForKeyword:"), 0x6572726E); d != 0 { // 'errn'
			replyErr = int(int32(uint32(objc.Send(d, objc.Sel("int32Value")))))
		}
	}
	return replyErr, 0
}

// SendQuitEvent sends the Dock's Quit to this process (QuitAppleEvent) and
// returns the reply's error number and the send's error (see SendToSelf).
// Main thread.
func SendQuitEvent() (replyErr, sendErr int) { return QuitAppleEvent.SendToSelf(true, 30, nil) }

// PressCmdQ posts ⌘Q to the window with number win (0: no window) as the
// keyboard does, to be matched by the main menu's Quit item. Main thread.
func PressCmdQ(win int) { Post(keyEventRaw(true, "q", "q", 0x0C, 1<<20, win, false)) }

// ---- timers on the main run loop ----

var (
	afterOnce sync.Once
	afterObj  objc.ID
	afterSel  objc.SEL
	afterMu   sync.Mutex
	afterFns  = map[int64]func(){}
	afterSeq  int64
)

// After runs f on the main thread d from now, from a timer in the run loop's
// default mode: also inside a nested loop of that mode (a modal session, the
// wait of a deferred quit answer), and outside any frame. Main thread.
func After(d time.Duration, f func()) {
	afterOnce.Do(func() {
		def := objc.Define("GNUIWidgettestAfter", objc.Cls("NSObject"), nil)
		afterSel = objc.Sel("gnuiAfter:")
		def.Method(afterSel, "v@:@", func(c *ffi.CB) {
			id := int64(objc.Send(objc.ID(c.Int(2)), objc.Sel("longLongValue")))
			afterMu.Lock()
			fn := afterFns[id]
			delete(afterFns, id)
			afterMu.Unlock()
			if fn != nil {
				fn()
			}
		})
		afterObj = objc.Send(objc.Send(def.Register(), objc.Sel("alloc")), objc.Sel("init"))
	})
	afterMu.Lock()
	afterSeq++
	id := afterSeq
	afterFns[id] = f
	afterMu.Unlock()
	num := objc.Send1(objc.Cls("NSNumber"), objc.Sel("numberWithLongLong:"), uintptr(id))
	sel := objc.Sel("performSelector:withObject:afterDelay:")
	objc.F.Reset().Arg(uintptr(afterObj)).Arg(uintptr(sel)).Arg(uintptr(afterSel)).Arg(uintptr(num)).ArgF64(d.Seconds()).Call(objc.MsgSend)
}

// RunLoop runs a nested run loop in the default mode for d, as AppKit's own
// loops do inside a call the app makes (an animated zoom, a modal session):
// timers (After), the app's dispatcher and posted events go on. Main thread.
func RunLoop(d time.Duration) {
	rl := objc.Send(objc.Cls("NSRunLoop"), objc.Sel("currentRunLoop"))
	mode := objc.Str("kCFRunLoopDefaultMode")
	cls, sel, run := objc.Cls("NSDate"), objc.Sel("dateWithTimeIntervalSinceNow:"), objc.Sel("runMode:beforeDate:")
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		date := objc.ID(objc.F.Reset().Arg(uintptr(cls)).Arg(uintptr(sel)).ArgF64(0.02).Call(objc.MsgSend).R0())
		objc.Send2(rl, run, uintptr(mode), uintptr(date))
	}
}

// DumpMenu writes NSApp's main menu as indented text: titles, key
// equivalents (⌘ marked "cmd+"), enabled state and check marks, separators as
// "-". It is what a test prints to see how the platform built the menu bar.
// Main thread.
func DumpMenu() string {
	var b []byte
	var walk func(menu objc.ID, depth int)
	walk = func(menu objc.ID, depth int) {
		if menu == 0 {
			return
		}
		objc.Send(menu, objc.Sel("update"))
		n := int(objc.Send(menu, objc.Sel("numberOfItems")))
		for i := 0; i < n; i++ {
			it := objc.Send1(menu, objc.Sel("itemAtIndex:"), uintptr(i))
			for j := 0; j < depth; j++ {
				b = append(b, ' ', ' ')
			}
			if objc.SendBool(it, objc.Sel("isSeparatorItem")) {
				b = append(b, "-\n"...)
				continue
			}
			sub := objc.Send(it, objc.Sel("submenu"))
			title := goString(objc.Send(it, selTitle))
			if title == "" && sub != 0 {
				title = goString(objc.Send(sub, selTitle))
			}
			b = append(b, title...)
			if k := goString(objc.Send(it, objc.Sel("keyEquivalent"))); k != "" {
				if objc.Send(it, objc.Sel("keyEquivalentModifierMask"))&(1<<20) != 0 {
					b = append(b, " [cmd+"...)
				} else {
					b = append(b, " ["...)
				}
				b = append(append(b, k...), ']')
			}
			if !objc.SendBool(it, objc.Sel("isEnabled")) {
				b = append(b, " (disabled)"...)
			}
			if objc.Send(it, objc.Sel("state")) == 1 {
				b = append(b, " (checked)"...)
			}
			b = append(b, '\n')
			walk(sub, depth+1)
		}
	}
	walk(objc.Send(NSApp(), objc.Sel("mainMenu")), 0)
	return string(b)
}

// ---- the sheets of a window ----

// Sheet is the sheet attached to a window: an alert or a file panel the app
// asked for through the platform's native dialogs.
type Sheet struct {
	// Window is the sheet's window (an NSPanel), 0 when none is attached.
	Window objc.ID
	// Parent is the window it is attached to.
	Parent objc.ID
}

// SheetOf is the sheet attached to the window win, if any. Main thread.
func SheetOf(win objc.ID) (Sheet, bool) {
	s := objc.Send(win, selAttachedSheet)
	return Sheet{Window: s, Parent: win}, s != 0
}

// buttons collects the NSButtons under view, depth first.
func buttons(view objc.ID, out *[]objc.ID) {
	if view == 0 {
		return
	}
	if objc.SendBool1(view, objc.Sel("isKindOfClass:"), uintptr(objc.Cls("NSButton"))) {
		*out = append(*out, view)
	}
	subs := objc.Send(view, objc.Sel("subviews"))
	n := int(objc.Send(subs, selCount))
	for i := 0; i < n; i++ {
		buttons(objc.Send1(subs, selObjectAtIndex, uintptr(i)), out)
	}
}

// Buttons are the titles of the sheet's buttons: the alert's buttons and its
// suppression checkbox, or a panel's Cancel and its confirm button.
func (s Sheet) Buttons() []string {
	var bs []objc.ID
	buttons(objc.Send(s.Window, objc.Sel("contentView")), &bs)
	out := make([]string, 0, len(bs))
	for _, b := range bs {
		out = append(out, goString(objc.Send(b, selTitle)))
	}
	return out
}

// Press presses the sheet's button with the title as the user does
// (performClick:), which ends the sheet and calls the app's completion; it
// reports whether there was such a button. An alert's buttons carry the titles
// the app gave in AlertSpec.Buttons.
func (s Sheet) Press(title string) bool {
	var bs []objc.ID
	buttons(objc.Send(s.Window, objc.Sel("contentView")), &bs)
	for _, b := range bs {
		if goString(objc.Send(b, selTitle)) == title {
			objc.Send1(b, objc.Sel("performClick:"), 0)
			return true
		}
	}
	return false
}

// End ends the sheet with the response code (NSModalResponseCancel 0, OK 1,
// or an alert's NSAlertFirstButtonReturn 1000 + button index) as
// endSheet:returnCode: does: what a panel gives when the test has no button
// to press.
func (s Sheet) End(code int) {
	objc.Send2(s.Parent, objc.Sel("endSheet:returnCode:"), uintptr(s.Window), uintptr(code))
}
