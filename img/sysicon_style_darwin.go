//go:build darwin

package img

import (
	"sync"
	"unsafe"

	"gonativeui/internal/ffi"
)

// IconStyle identifies the user's icon style setting (System Settings ›
// Appearance › Icon and widget style: Default, Dark, Clear, Tinted; macOS 26
// and later), which changes the art of application icons: a hash of the
// AppleIconAppearanceTheme preference, or 0 when it is not set. Fold it into
// HashFileIcon's variant so that application icons are requested again after
// the user changes the style. It reads the preference (CFPreferences, any
// thread) and allocates, so call it when the app learns that the appearance
// changed, not every frame; the first call looks up its symbols.
func IconStyle() uint64 {
	if !styleLoad() {
		return 0
	}
	fr := new(cframe)
	key := fr.Reset().Arg(0).ArgPtr(unsafe.Pointer(&styleKey[0])).Arg(cfUTF8).Call(styleFn.createStr).R0()
	if key == 0 {
		return 0
	}
	defer fr.Call1(styleFn.release, key)
	val := fr.Reset().Arg(key).Arg(styleFn.anyApp).Arg(styleFn.curUser).Arg(styleFn.anyHost).Call(styleFn.copyValue).R0()
	if val == 0 {
		return 0
	}
	defer fr.Call1(styleFn.release, val)
	if fr.Call1(styleFn.typeID, val) != fr.Call0(styleFn.strTypeID) {
		return 0
	}
	var buf [128]byte
	if !fr.Reset().Arg(val).ArgPtr(unsafe.Pointer(&buf[0])).Arg(uintptr(len(buf))).Arg(cfUTF8).Call(styleFn.getCStr).Bool() {
		return 0
	}
	n := 0
	for n < len(buf) && buf[n] != 0 {
		n++
	}
	return HashBytes(buf[:n])
}

const cfUTF8 = 0x08000100 // kCFStringEncodingUTF8

var styleKey = []byte("AppleIconAppearanceTheme\x00")

var styleFn struct {
	once sync.Once
	ok   bool

	createStr, copyValue, getCStr, release, typeID, strTypeID uintptr
	anyApp, curUser, anyHost                                  uintptr
}

func styleLoad() bool {
	styleFn.once.Do(func() {
		cf, err := dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation")
		if err != nil {
			return
		}
		fn := func(name string) uintptr {
			p, _ := dlsym(cf, name)
			return p
		}
		val := func(name string) uintptr { // a CFStringRef variable
			if p := fn(name); p != 0 {
				return *(*uintptr)(ffi.PtrFromC(p))
			}
			return 0
		}
		f := &styleFn
		f.createStr, f.copyValue, f.getCStr = fn("CFStringCreateWithCString"), fn("CFPreferencesCopyValue"), fn("CFStringGetCString")
		f.release, f.typeID, f.strTypeID = fn("CFRelease"), fn("CFGetTypeID"), fn("CFStringGetTypeID")
		f.anyApp, f.curUser, f.anyHost = val("kCFPreferencesAnyApplication"), val("kCFPreferencesCurrentUser"), val("kCFPreferencesAnyHost")
		f.ok = f.createStr != 0 && f.copyValue != 0 && f.getCStr != 0 && f.release != 0 && f.typeID != 0 && f.strTypeID != 0 &&
			f.anyApp != 0 && f.curUser != 0 && f.anyHost != 0
	})
	return styleFn.ok
}
