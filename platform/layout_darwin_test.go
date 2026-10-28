//go:build darwin

package platform

import (
	"testing"

	"github.com/ebitengine/purego"

	"gonativeui/internal/objc"
)

// tisLayoutData returns the UCKeyboardLayout of the installed keyboard layout
// with the given input source id (com.apple.keylayout.French), without
// selecting it or changing any setting; 0 if there is none. Main thread.
func tisLayoutData(id string) uintptr {
	if !layout.inited {
		initLayout()
	}
	if layout.failed {
		return 0
	}
	carbon, err := purego.Dlopen("/System/Library/Frameworks/Carbon.framework/Carbon", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return 0
	}
	cf, _ := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	list, err := purego.Dlsym(carbon, "TISCreateInputSourceList")
	if err != nil {
		return 0
	}
	count, _ := purego.Dlsym(cf, "CFArrayGetCount")
	at, _ := purego.Dlsym(cf, "CFArrayGetValueAtIndex")
	key := stringConstant(carbon, "kTISPropertyInputSourceID")
	filter := objc.Send2(objc.Cls("NSDictionary"), objc.Sel("dictionaryWithObject:forKey:"), uintptr(objc.Str(id)), uintptr(key))
	arr := objc.F.Call2(list, uintptr(filter), 1 /* include all installed */) // (kept: a few bytes for the process)
	if arr == 0 || objc.F.Call1(count, arr) == 0 {
		return 0
	}
	src := objc.F.Call2(at, arr, 0)
	data := objc.F.Call2(layout.getProp, src, layout.prop)
	if data == 0 {
		return 0
	}
	return objc.F.Call1(layout.dataPtr, data)
}

// pinLayout makes the layout with the id the one the platform reads, until
// the returned function puts the real one back. ok is false when it is not
// installed. Main thread.
func pinLayout(id string) (restore func(), ok bool) {
	uchr := tisLayoutData(id)
	if uchr == 0 {
		return nil, false
	}
	if !layout.valid {
		loadLayout()
	}
	saved := struct {
		valid     bool
		src, uchr uintptr
		kbd       uint32
	}{layout.valid, layout.src, layout.uchr, layout.kbd}
	layout.valid, layout.src, layout.uchr = true, 0, uchr
	if layout.kbd == 0 {
		layout.kbd = uint32(objc.F.Call0(layout.kbdType)) & 0xFF
	}
	return func() {
		layout.valid, layout.src, layout.uchr, layout.kbd = saved.valid, saved.src, saved.uchr, saved.kbd
	}, true
}

// TestShortcutCharacterThroughLayouts: the key's character for a shortcut is
// what the layout types with ⌘ held and Shift not, translated by the layout
// itself (UCKeyTranslate); the layouts are read without selecting them.
func TestShortcutCharacterThroughLayouts(t *testing.T) {
	cases := []struct {
		name, layout string
		code         uint32 // the virtual key code (the US layout's key position)
		state        uint32
		want         rune
	}{
		// "Dvorak – QWERTY ⌘" types Dvorak, but QWERTY while ⌘ is held: the
		// key that types "," on Dvorak gives w with ⌘.
		{"Dvorak types , there", "com.apple.keylayout.Dvorak", 0x0D, 0, ','},
		{"Dvorak – QWERTY ⌘ types , without ⌘", "com.apple.keylayout.DVORAK-QWERTYCMD", 0x0D, 0, ','},
		{"Dvorak – QWERTY ⌘ gives w with ⌘", "com.apple.keylayout.DVORAK-QWERTYCMD", 0x0D, ucCmdState, 'w'},
		// Shifted punctuation: ⌘⇧[ on Dvorak is the key that types [ (US
		// minus position) with Shift; the ⌘ translation without Shift is [,
		// and with Shift it would be {, which names no shortcut key.
		{"Dvorak ⌘ at the minus position", "com.apple.keylayout.Dvorak", 0x1B, ucCmdState, '['},
		{"Dvorak with Shift at the minus position (what charactersIgnoringModifiers gave)", "com.apple.keylayout.Dvorak", 0x1B, ucShiftState, '{'},
		{"Dvorak ⌘ at the equal position", "com.apple.keylayout.Dvorak", 0x18, ucCmdState, ']'},
		{"AZERTY ⌘ at the M position types ,", "com.apple.keylayout.French", 0x2E, ucCmdState, ','},
		{"AZERTY with Shift at the M position", "com.apple.keylayout.French", 0x2E, ucShiftState, '?'},
		{"AZERTY ⌘ at the W position types z", "com.apple.keylayout.French", 0x0D, ucCmdState, 'z'},
		{"German ⌘ at the slash position types -", "com.apple.keylayout.German", 0x2C, ucCmdState, '-'},
		{"German with Shift at the slash position", "com.apple.keylayout.German", 0x2C, ucShiftState, '_'},
		{"German ⌘ at the Z position types y", "com.apple.keylayout.German", 0x06, ucCmdState, 'y'},
		{"German ⌘ at the Y position types z", "com.apple.keylayout.German", 0x10, ucCmdState, 'z'},
	}
	var errs []string
	var skip string
	onMain(func() {
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		for _, c := range cases {
			uchr := tisLayoutData(c.layout)
			if uchr == 0 {
				skip = c.layout + " is not installed"
				return
			}
			if got := ucRune(uchr, uint32(objc.F.Call0(layout.kbdType))&0xFF, c.code, c.state); got != c.want {
				errs = append(errs, c.name+": got "+string(got)+", want "+string(c.want))
			}
		}
	})
	if skip != "" {
		t.Skip(skip)
	}
	for _, e := range errs {
		t.Error(e)
	}
}

// TestLayoutCharKeepsShiftOut: the character the platform reports (layoutChar)
// is the unshifted one on every layout, so ⌘⇧ plus the key that types [ is
// the [ key with Shift on Dvorak, AZERTY and German too.
func TestLayoutCharKeepsShiftOut(t *testing.T) {
	cases := []struct {
		layout string
		code   uint32
		want   rune
	}{
		{"com.apple.keylayout.US", 0x21, '['},
		{"com.apple.keylayout.Dvorak", 0x1B, '['},
		{"com.apple.keylayout.DVORAK-QWERTYCMD", 0x0D, 'w'},
		{"com.apple.keylayout.French", 0x2E, ','},
		{"com.apple.keylayout.German", 0x2C, '-'},
		{"com.apple.keylayout.German", 0x10, 'z'},
		{"com.apple.keylayout.US", 0x00, 'a'},
		{"com.apple.keylayout.US", 0x7B, 0}, // the left arrow types nothing
	}
	var errs []string
	var skip string
	onMain(func() {
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		for _, c := range cases {
			restore, ok := pinLayout(c.layout)
			if !ok {
				skip = c.layout + " is not installed"
				return
			}
			got, _ := layoutChar(c.code)
			restore()
			if got != c.want {
				errs = append(errs, c.layout+": "+string(got)+", want "+string(c.want))
			}
		}
	})
	if skip != "" {
		t.Skip(skip)
	}
	for _, e := range errs {
		t.Error(e)
	}
}

// TestNonLatinLayoutsFallBackToThePhysicalKey: ⌘C copies on a Russian, Greek or
// Hebrew layout. macOS's own layouts type the Latin letter with ⌘ held, which
// is what is reported; a character that is not Latin (a layout without that
// table) is reported as none, and the shortcut is then the physical US key.
func TestNonLatinLayoutsFallBackToThePhysicalKey(t *testing.T) {
	for _, c := range []struct{ in, want rune }{
		{'с', 0}, {'α', 0}, {'ש', 0}, {'é', 0}, {'Ä', 0}, {0xF702, 0}, {0x1b, 0}, {0, 0}, // (Cyrillic, Greek, Hebrew, accented, arrow key, Escape)
		{'c', 'c'}, {'C', 'c'}, {'[', '['}, {' ', ' '},
	} {
		if got := shortcutRune(c.in); got != c.want {
			t.Errorf("shortcutRune(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	var errs []string
	var tried int
	onMain(func() {
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		for _, id := range []string{"Russian", "RussianWin", "Greek", "Hebrew", "Arabic", "Ukrainian"} {
			restore, ok := pinLayout("com.apple.keylayout." + id)
			if !ok {
				continue
			}
			tried++
			got, _ := layoutChar(0x08) // the C key
			restore()
			if got != 0 && got != 'c' {
				errs = append(errs, id+": "+string(got)+", want c or none")
			}
		}
	})
	if tried == 0 {
		t.Skip("no non-Latin layout is installed")
	}
	for _, e := range errs {
		t.Error(e)
	}
}

// TestLayoutCharIsCachedAndAllocatesNothing: reading the layout once serves
// every key after it with no allocation; a layout change drops the cache.
func TestLayoutCharIsCachedAndAllocatesNothing(t *testing.T) {
	var allocs float64
	var dropped, reread, viaCenter, viaDist bool
	var notifName string
	var ok bool
	onMain(func() {
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		_, ok = layoutChar(0)
		if !ok {
			return
		}
		src := layout.src
		allocs = testing.AllocsPerRun(200, func() { layoutChar(0x0D) })
		if layout.src != src {
			t.Error("the layout was read again for each key")
		}
		// The user selecting another layout posts a distributed notification,
		// which reaches the observer's method; it only drops the cache, and
		// the next key reads the layout again. (The method is called here
		// directly: whether the system delivers distributed notifications to a
		// test process is up to the machine, tried below.)
		objc.Send1(layout.observer, layout.changed, 0)
		dropped = !layout.valid
		if dropped {
			layoutChar(0)
			reread = layout.valid && layout.uchr != 0
		}
		// AppKit's own notification (local, so it is delivered to a test
		// process too) drops it the same way.
		nc := objc.Send(objc.Cls("NSNotificationCenter"), objc.Sel("defaultCenter"))
		nn, post := objc.Str(ucTextInputChangedNn), objc.Sel("postNotificationName:object:")
		layoutChar(0)
		objc.Send2(nc, post, uintptr(nn), 0)
		viaCenter = !layout.valid
		if viaCenter {
			layoutChar(0)
		}
		// The distributed notification where the machine delivers it, and its name.
		dc := objc.Send(objc.Cls("NSDistributedNotificationCenter"), objc.Sel("defaultCenter"))
		dn, dpost := objc.Str(ucLayoutChangedNn), objc.Sel("postNotificationName:object:userInfo:deliverImmediately:")
		objc.Send4(dc, dpost, uintptr(dn), 0, 0, 1)
		for i := 0; i < 10 && layout.valid; i++ {
			spin(0.05)
		}
		viaDist = !layout.valid
		if viaDist {
			layoutChar(0)
		}
		if nc := stringConstant(carbonHandle(), "kTISNotifySelectedKeyboardInputSourceChanged"); nc != 0 {
			var b [128]byte
			notifName = string(objc.AppendUTF8(b[:0], nc))
		}
	})
	if !ok {
		t.Skip("no keyboard layout data on this machine")
	}
	if allocs != 0 {
		t.Errorf("layoutChar allocates %v per key", allocs)
	}
	if notifName != "" && notifName != ucLayoutChangedNn {
		t.Errorf("the system's notification is %q, the observer listens for %q", notifName, ucLayoutChangedNn)
	}
	if !viaCenter {
		t.Error("AppKit's keyboard-selection notification did not drop the cache")
	}
	if !viaDist {
		t.Log("this machine did not deliver the distributed notification to the test process")
	}
	if !dropped {
		t.Error("the layout-changed notification did not drop the cache")
	} else if !reread {
		t.Error("the layout was not read again after the change")
	}
}

func carbonHandle() uintptr {
	h, _ := purego.Dlopen("/System/Library/Frameworks/Carbon.framework/Carbon", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	return h
}
