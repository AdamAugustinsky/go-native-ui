//go:build darwin

package widgettest

import (
	"fmt"
	"sync"
	"unicode/utf16"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/objc"
	"gonativeui/ui"
)

// Keyboard layouts. A key event's characters are not a property of the key:
// the same virtual key types "1" or "!" or "&" by the layout and the
// modifiers. KeyLayout asks the system, the way an application's input
// handling does (UCKeyTranslate over the layout's data), so a test can press
// Shift+1 on the US layout and get "!", or the same physical key on the French
// layout and get "1", without changing the user's settings: a layout is read
// from the list of installed input sources, never selected.

// KeyLayout is an installed keyboard layout.
type KeyLayout struct {
	// ID is the input source's ID, e.g. "com.apple.keylayout.US".
	ID string

	data    uintptr // the layout's uchr data (a CFData's bytes), owned by the retained source
	kbdType uint32  // the keyboard type UCKeyTranslate wants (ANSI, ISO, JIS)
}

// US is the ID of the US layout, which the virtual key codes of KeyFor name
// the physical keys of.
const US = "com.apple.keylayout.US"

var layouts struct {
	once sync.Once
	err  error

	createList, getProp, copyCurrent, translate, kbdType, dataBytes uintptr

	mu    sync.Mutex
	cache map[string]*KeyLayout
}

func loadLayouts() error {
	l := &layouts
	l.once.Do(func() {
		l.cache = map[string]*KeyLayout{}
		hi, err := purego.Dlopen("/System/Library/Frameworks/Carbon.framework/Carbon", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			l.err = err
			return
		}
		cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			l.err = err
			return
		}
		cg, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			l.err = err
			return
		}
		sym := func(h uintptr, name string) uintptr {
			p, e := purego.Dlsym(h, name)
			if e != nil && l.err == nil {
				l.err = e
			}
			return p
		}
		l.createList = sym(hi, "TISCreateInputSourceList")
		l.getProp = sym(hi, "TISGetInputSourceProperty")
		l.copyCurrent = sym(hi, "TISCopyCurrentKeyboardLayoutInputSource")
		l.translate = sym(hi, "UCKeyTranslate")
		l.dataBytes = sym(cf, "CFDataGetBytePtr")
		l.kbdType = sym(cg, "CGEventSourceGetKeyboardType")
	})
	return l.err
}

// CurrentKeyLayout is the keyboard layout the user types with now. Main
// thread.
func CurrentKeyLayout() (*KeyLayout, error) {
	if err := loadLayouts(); err != nil {
		return nil, err
	}
	src := objc.F.Call0(layouts.copyCurrent)
	if src == 0 {
		return nil, fmt.Errorf("widgettest: the system has no current keyboard layout")
	}
	return layoutOf(objc.ID(src), "")
}

// NamedKeyLayout is the installed keyboard layout with the input source ID id
// ("com.apple.keylayout.US", ".French", ".German", ".Dvorak"...), whether or
// not the user has enabled it. The user's settings are not touched. Main
// thread.
func NamedKeyLayout(id string) (*KeyLayout, error) {
	if err := loadLayouts(); err != nil {
		return nil, err
	}
	layouts.mu.Lock()
	k := layouts.cache[id]
	layouts.mu.Unlock()
	if k != nil {
		return k, nil
	}
	val, key := objc.Str(id), objc.Str("TISPropertyInputSourceID")
	filter := objc.Send2(objc.Cls("NSDictionary"), objc.Sel("dictionaryWithObject:forKey:"), uintptr(val), uintptr(key))
	list := objc.ID(objc.F.Reset().Arg(uintptr(filter)).ArgBool(true).Call(layouts.createList).R0()) // includeAllInstalled
	if list == 0 || objc.Send(list, selCount) == 0 {
		return nil, fmt.Errorf("widgettest: no installed keyboard layout %q", id)
	}
	k, err := layoutOf(objc.Send1(list, selObjectAtIndex, 0), id)
	if err != nil {
		return nil, err
	}
	layouts.mu.Lock()
	layouts.cache[id] = k
	layouts.mu.Unlock()
	return k, nil
}

// layoutOf reads the layout data of an input source, retaining the source
// for good (a layout is a few kilobytes, and tests use a handful).
func layoutOf(src objc.ID, id string) (*KeyLayout, error) {
	objc.Retain(src)
	dataKey, idKey := objc.Str("TISPropertyUnicodeKeyLayoutData"), objc.Str("TISPropertyInputSourceID")
	data := objc.F.Reset().Arg(uintptr(src)).Arg(uintptr(dataKey)).Call(layouts.getProp).R0()
	if data == 0 {
		return nil, fmt.Errorf("widgettest: the keyboard layout has no key layout data (an input method, not a layout)")
	}
	if id == "" {
		sid := objc.F.Reset().Arg(uintptr(src)).Arg(uintptr(idKey)).Call(layouts.getProp).R0()
		id = goString(objc.ID(sid))
	}
	bytes := objc.F.Call1(layouts.dataBytes, data)
	kt := objc.F.Call1(layouts.kbdType, 0)
	return &KeyLayout{ID: id, data: bytes, kbdType: uint32(kt)}, nil
}

// Translate is the characters the virtual key vk types on this layout with the
// modifiers held (Shift and Alt/Option take part; Ctrl and ⌘ do not, as in the
// characters AppKit gives key equivalents), dead keys not resolved. Main
// thread.
func (k *KeyLayout) Translate(vk uint16, mods ui.Mods) string {
	var state uint32 // Carbon modifiers >> 8: shift 2, option 8
	if mods&ui.ModShift != 0 {
		state |= 2
	}
	if mods&ui.ModAlt != 0 {
		state |= 8
	}
	dead := new(uint32)
	n := new(uintptr)
	buf := new([4]uint16)
	f := objc.F.Reset().Arg(k.data).Arg(uintptr(vk)).Arg(0).Arg(uintptr(state)).Arg(uintptr(k.kbdType)).
		Arg(1). // kUCKeyTranslateNoDeadKeysMask
		ArgPtr(unsafe.Pointer(dead)).Arg(uintptr(len(buf))).ArgPtr(unsafe.Pointer(n)).ArgPtr(unsafe.Pointer(&buf[0]))
	if st := int32(f.Call(layouts.translate).R0()); st != 0 || *n == 0 || *n > uintptr(len(buf)) {
		return ""
	}
	return string(utf16.Decode(buf[:*n]))
}
