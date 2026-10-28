//go:build darwin

package platform

import (
	"unicode"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// The character a key types for a shortcut. A shortcut names its key by what
// the key types on the user's layout, with ⌘ held (that is what "Dvorak –
// QWERTY ⌘" changes, and what AppKit matches key equivalents by), and without
// Shift, which is a modifier of the shortcut of its own: ⌘⇧[ is the [ key
// with Shift, not the { key. The layout's own UCKeyTranslate gives that
// character for a virtual key code with just ⌘ in the modifier state. A menu
// item is the exception: AppKit matches its key equivalent against the
// event's charactersIgnoringModifiers, which keeps Shift, so an item for
// ⇧⌘[ must say "{" (shiftedChar). The
// layout data comes from the Text Input Sources (TIS) API, is kept until the
// user selects another layout (a distributed notification), and is read on
// the main thread only, so the per-key path allocates nothing.

const (
	ucCmdState           = 1 << (8 - 8) // cmdKey >> 8: the modifier state UCKeyTranslate takes
	ucShiftState         = 1 << (9 - 8) // shiftKey >> 8
	ucNoDeadKeys         = 1 << 0       // kUCKeyTranslateNoDeadKeysBit
	ucLayoutChangedNn    = "com.apple.Carbon.TISNotifySelectedKeyboardInputSourceChanged"
	ucTextInputChangedNn = "NSTextInputContextKeyboardSelectionDidChangeNotification"
)

var layout struct {
	inited, failed bool
	valid          bool // src and uchr describe the layout selected now
	copyCurrent    uintptr
	copyASCII      uintptr
	getProp        uintptr
	prop           uintptr // kTISPropertyUnicodeKeyLayoutData
	dataPtr        uintptr // CFDataGetBytePtr
	translate      uintptr // UCKeyTranslate
	kbdType        uintptr // LMGetKbdType
	release        uintptr // CFRelease
	src            uintptr // the retained input source whose data uchr points into
	uchr           uintptr // the UCKeyboardLayout
	kbd            uint32

	observer objc.ID // the distributed notification's observer and its selector
	changed  objc.SEL

	dead   uint32 // UCKeyTranslate's scratch: the dead key state, the length, the characters
	actual uintptr
	buf    [8]uint16
}

func initLayout() {
	layout.inited = true
	carbon, err := purego.Dlopen("/System/Library/Frameworks/Carbon.framework/Carbon", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		layout.failed = true
		return
	}
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		layout.failed = true
		return
	}
	sym := func(lib uintptr, name string) uintptr {
		p, err := purego.Dlsym(lib, name)
		if err != nil {
			layout.failed = true
		}
		return p
	}
	layout.copyCurrent = sym(carbon, "TISCopyCurrentKeyboardLayoutInputSource")
	layout.copyASCII = sym(carbon, "TISCopyCurrentASCIICapableKeyboardLayoutInputSource")
	layout.getProp = sym(carbon, "TISGetInputSourceProperty")
	layout.translate = sym(carbon, "UCKeyTranslate")
	layout.kbdType = sym(carbon, "LMGetKbdType")
	layout.dataPtr = sym(cf, "CFDataGetBytePtr")
	layout.release = sym(cf, "CFRelease")
	layout.prop = uintptr(stringConstant(carbon, "kTISPropertyUnicodeKeyLayoutData")) // an extern CFStringRef
	if layout.failed || layout.prop == 0 {
		layout.failed = true
		return
	}
	// The user selecting another layout: the Text Input Sources' distributed
	// notification, and AppKit's own for the text input context (a local one,
	// which every application with a text view gets), each only drops what is
	// cached (the next key reads the new layout).
	sel := objc.Sel("gnuiLayoutChanged:")
	def := objc.Define("GNUILayoutObserver", objc.Cls("NSObject"), nil)
	def.Method(sel, "v@:@", func(*ffi.CB) { dropLayout(); refreshShiftedKeys() })
	obs := objc.Send(objc.Send(def.Register(), selAlloc), selInitPlain)
	layout.observer, layout.changed = obs, sel
	dc := objc.Send(objc.Cls("NSDistributedNotificationCenter"), objc.Sel("defaultCenter"))
	// (Delivered at once: the default would hold it while the application is inactive.)
	nameStr := stringConstant(carbon, "kTISNotifySelectedKeyboardInputSourceChanged")
	if nameStr == 0 {
		nameStr = objc.Str(ucLayoutChangedNn)
	}
	name, add := nameStr, objc.Sel("addObserver:selector:name:object:suspensionBehavior:") // (before the frame is filled: both use it)
	objc.F.Reset().Arg(uintptr(dc)).Arg(uintptr(add)).Arg(uintptr(obs)).Arg(uintptr(sel)).
		Arg(uintptr(name)).Arg(0).Arg(4 /* NSNotificationSuspensionBehaviorDeliverImmediately */).Call(objc.MsgSend)
	nc := objc.Send(objc.Cls("NSNotificationCenter"), objc.Sel("defaultCenter"))
	objc.Send4(nc, objc.Sel("addObserver:selector:name:object:"), uintptr(obs), uintptr(sel), uintptr(objc.Str(ucTextInputChangedNn)), 0)
}

// dropLayout forgets the cached layout.
func dropLayout() {
	layout.valid, layout.uchr = false, 0
	if layout.src != 0 {
		objc.F.Call1(layout.release, layout.src)
		layout.src = 0
	}
}

// loadLayout reads the selected layout's data. An input method that is no
// keyboard layout (it has no uchr data) types through the ASCII-capable
// layout the user has chosen for it.
func loadLayout() {
	layout.valid = true
	for _, fn := range [...]uintptr{layout.copyCurrent, layout.copyASCII} {
		src := objc.F.Call0(fn)
		if src == 0 {
			continue
		}
		data := objc.F.Call2(layout.getProp, src, layout.prop)
		if data == 0 {
			objc.F.Call1(layout.release, src)
			continue
		}
		layout.src, layout.uchr = src, objc.F.Call1(layout.dataPtr, data)
		layout.kbd = uint32(objc.F.Call0(layout.kbdType)) & 0xFF // (a UInt8)
		return
	}
}

// layoutChar is the character the key with virtual key code code types on the
// selected layout with ⌘ held and nothing else: lower-cased, 0 for a key that
// types none or none in ASCII (a layout of another script: the shortcut is
// then the physical key, as AppKit does). ok is false when the system's
// layout data cannot be read at all.
func layoutChar(code uint32) (r rune, ok bool) {
	if !haveLayout() {
		return 0, false
	}
	return shortcutRune(ucRune(layout.uchr, layout.kbd, code, ucCmdState)), true
}

// haveLayout loads the selected layout's data if it is not cached, and
// reports whether there is any.
func haveLayout() bool {
	if !layout.inited {
		initLayout()
	}
	if layout.failed {
		return false
	}
	if !layout.valid {
		loadLayout()
	}
	return layout.uchr != 0
}

// lastMainKey is the last virtual key code of the main block (kVK_ANSI_Grave,
// 0x32): the letter, digit and punctuation keys, the ISO section key (0x0A)
// among them, and not the keypad, whose keys type the same characters.
const lastMainKey = 0x32

// shiftedChar is the character the key that types key for a shortcut
// (layoutChar) types with Shift held: what an event's
// charactersIgnoringModifiers is for ⇧⌘ plus that key, and so what an NSMenu
// item's key equivalent must be to match it ({ for the [ key on a US layout,
// # for 3, and what the layout makes of them elsewhere). 0 when no key of the
// layout types key with ⌘ held or its Shift character is not printable ASCII;
// the item then keeps key. Main thread.
func shiftedChar(key rune) rune {
	if !haveLayout() {
		return 0
	}
	for code := uint32(0); code <= lastMainKey; code++ {
		if shortcutRune(ucRune(layout.uchr, layout.kbd, code, ucCmdState)) != key {
			continue
		}
		if r := ucRune(layout.uchr, layout.kbd, code, ucShiftState); r > 0x20 && r <= 0x7e {
			return r
		}
	}
	return 0
}

// ucRune translates virtual key code code through the UCKeyboardLayout uchr
// with the modifier state state (Carbon's modifier bits shifted right by 8:
// 1 ⌘, 2 Shift, 4 Caps Lock, 8 Option, 16 Control) and returns the one
// character it types, 0 if none or more than one. Main thread.
func ucRune(uchr uintptr, kbd, code, state uint32) rune {
	layout.dead, layout.actual = 0, 0
	st := objc.F.Reset().Arg(uchr).Arg32(code).Arg32(0 /* kUCKeyActionDown */).Arg32(state).Arg32(kbd).Arg32(ucNoDeadKeys).
		ArgPtr(unsafe.Pointer(&layout.dead)).Arg(uintptr(len(layout.buf))).ArgPtr(unsafe.Pointer(&layout.actual)).ArgPtr(unsafe.Pointer(&layout.buf[0])).
		Call(layout.translate).R0()
	if int32(st) != 0 || layout.actual != 1 {
		return 0
	}
	return rune(layout.buf[0])
}

// shortcutRune is r as Event.Char: a printable ASCII character, a letter
// lower-cased; 0 for anything else.
func shortcutRune(r rune) rune {
	if r < 0x20 || r > 0x7e {
		return 0
	}
	return unicode.ToLower(r)
}
