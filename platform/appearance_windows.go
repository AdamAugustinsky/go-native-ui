package platform

import (
	"sync"
	"sync/atomic"
	"unsafe"

	"gonativeui/internal/win32"
)

// System appearance on Windows (see appearance.go, D32).
//
// Dark is HKCU\...\Themes\Personalize AppsUseLightTheme (0 = dark; missing =
// light). The accent is HKCU\...\DWM AccentColor (ABGR), then ColorizationColor
// (ARGB), then DwmGetColorizationColor.
//
// The first SystemAppearance call opens both keys, arms RegNotifyChangeKeyValue
// on each (asynchronous, thread-agnostic, signalling an event) and reads the
// values: a few registry calls. A goroutine then blocks in
// WaitForMultipleObjects on the two events: no polling, no wakeups. When one
// fires it re-arms, reads again and, if the appearance differs from the cache,
// publishes it, bumps the serial and calls Wake. The window procedure is not
// involved. SystemAppearance itself is one atomic load.
//
// A key that does not exist yet (a user who never opened the personalisation
// settings has no Personalize key) is watched through its nearest existing
// ancestor, with a subtree watch, until it appears.

const (
	apValid = 1 << 7 // the cache has been read
	apKnown = 1 << 1
	apDark  = 1 << 0
)

var (
	apCache  atomic.Uint64 // accent<<8 | apValid | apKnown | apDark
	apSerial atomic.Uint32
	apOnce   sync.Once

	apAdvapi, apKernel, apDwm uintptr
	pRegOpenKeyExW            uintptr
	pRegCloseKey              uintptr
	pRegQueryValueExW         uintptr
	pRegNotifyChange          uintptr
	pCreateEventW             uintptr
	pWaitForMultiple          uintptr
	pDwmGetColorization       uintptr
	pDwmSetWindowAttr         uintptr
	pDwmGetWindowAttr         uintptr
	pRtlGetVersion            uintptr
)

const (
	hkeyCurrentUser = 0x80000001
	keyQueryNotify  = 0x0001 | 0x0010 // KEY_QUERY_VALUE | KEY_NOTIFY
	regDWORD        = 4

	regNotifyName        = 0x00000001 // REG_NOTIFY_CHANGE_NAME
	regNotifyLastSet     = 0x00000004 // REG_NOTIFY_CHANGE_LAST_SET
	regNotifyThreadAgnos = 0x10000000 // REG_NOTIFY_THREAD_AGNOSTIC (Windows 8)

	personalizeKey = `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`
	dwmKey         = `Software\Microsoft\Windows\DWM`
)

// regWatch is one watched key: its handle, the event it signals and whether
// the handle is the key itself or (until it exists) an ancestor.
type regWatch struct {
	path  string
	key   uintptr
	exact bool
	event uintptr
}

var (
	apWatch   [2]regWatch
	apHandles [2]uintptr // the events, for WaitForMultipleObjects

	// Out parameters of the registry calls. Package-level because the stack
	// can move during a call; only apStart and then apFollow use them, never
	// at the same time.
	scratchKey                         uintptr
	scratchTyp, scratchVal, scratchLen uint32
)

// SystemAppearance returns the current system appearance (see appearance.go).
func SystemAppearance() Appearance {
	v := apCache.Load()
	if v == 0 {
		apOnce.Do(apStart)
		v = apCache.Load()
	}
	return unpackAppearance(v)
}

// AppearanceSerial counts system appearance changes (see appearance.go).
func AppearanceSerial() uint32 { return apSerial.Load() }

func unpackAppearance(v uint64) Appearance {
	return Appearance{Dark: v&apDark != 0, Known: v&apKnown != 0, Accent: uint32(v >> 8)}
}

func packAppearance(a Appearance) uint64 {
	v := uint64(a.Accent)<<8 | apValid
	if a.Dark {
		v |= apDark
	}
	if a.Known {
		v |= apKnown
	}
	return v
}

// apStart loads the entry points, arms both watches, reads the first value
// and starts the goroutine that follows changes.
func apStart() {
	apAdvapi = win32.LoadSystem("advapi32.dll")
	apKernel = win32.LoadSystem("kernel32.dll")
	apDwm = win32.LoadSystem("dwmapi.dll")
	pRegOpenKeyExW = win32.Proc(apAdvapi, "RegOpenKeyExW")
	pRegCloseKey = win32.Proc(apAdvapi, "RegCloseKey")
	pRegQueryValueExW = win32.Proc(apAdvapi, "RegQueryValueExW")
	pRegNotifyChange = win32.Proc(apAdvapi, "RegNotifyChangeKeyValue")
	pCreateEventW = win32.Proc(apKernel, "CreateEventW")
	pWaitForMultiple = win32.Proc(apKernel, "WaitForMultipleObjects")
	pDwmGetColorization = win32.Proc(apDwm, "DwmGetColorizationColor")
	if pRegOpenKeyExW == 0 || pRegQueryValueExW == 0 {
		apCache.Store(packAppearance(Appearance{})) // cannot ask: light, not known
		return
	}
	following := pRegNotifyChange != 0 && pCreateEventW != 0 && pWaitForMultiple != 0
	apWatch[0].path, apWatch[1].path = personalizeKey, dwmKey
	if following {
		for i := range apWatch {
			w := &apWatch[i]
			// Auto-reset: WaitForMultipleObjects resets the one it returns.
			w.event = win32.Call(pCreateEventW, 0, 0, 0, 0)
			following = following && w.event != 0
		}
	}
	for i := range apWatch {
		w := &apWatch[i]
		w.open()
		if following {
			w.arm()
		}
	}
	apCache.Store(packAppearance(readAppearance()))
	if following {
		go apFollow()
	}
}

// open opens the key, or its nearest existing ancestor.
func (w *regWatch) open() {
	path := w.path
	for {
		sub := mustUTF16(path)
		r := win32.Call(pRegOpenKeyExW, hkeyCurrentUser, uintptr(unsafe.Pointer(sub)), 0, keyQueryNotify, uintptr(unsafe.Pointer(&scratchKey)))
		if r == 0 {
			w.key, w.exact = scratchKey, path == w.path
			return
		}
		i := lastBackslash(path)
		if i < 0 {
			w.key, w.exact = hkeyCurrentUser, false // HKCU itself (never closed)
			return
		}
		path = path[:i]
	}
}

func lastBackslash(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '\\' {
			return i
		}
	}
	return -1
}

// arm asks for the event to be signalled on the next change.
func (w *regWatch) arm() {
	filter, subtree := uintptr(regNotifyLastSet|regNotifyThreadAgnos), uintptr(0)
	if !w.exact {
		filter |= regNotifyName
		subtree = 1
	}
	win32.Call(pRegNotifyChange, w.key, subtree, filter, w.event, 1)
}

// settle runs after the event fired: if the watch stood on an ancestor and the
// key now exists, it moves to the key.
func (w *regWatch) settle() {
	if w.exact {
		return
	}
	old := w.key
	w.open()
	if w.key != old && old != hkeyCurrentUser {
		win32.Call(pRegCloseKey, old)
	}
}

// dword reads a REG_DWORD value of the watched key.
func (w *regWatch) dword(name *uint16) (uint32, bool) {
	if !w.exact {
		return 0, false
	}
	scratchTyp, scratchVal, scratchLen = 0, 0, 4
	r := win32.Call(pRegQueryValueExW, w.key, uintptr(unsafe.Pointer(name)), 0,
		uintptr(unsafe.Pointer(&scratchTyp)), uintptr(unsafe.Pointer(&scratchVal)), uintptr(unsafe.Pointer(&scratchLen)))
	return scratchVal, r == 0 && scratchTyp == regDWORD && scratchLen == 4
}

var (
	nameAppsUseLight = mustUTF16("AppsUseLightTheme")
	nameAccentColor  = mustUTF16("AccentColor")
	nameColorization = mustUTF16("ColorizationColor")
	dwmColor         uint32 // DwmGetColorizationColor's out parameters
	dwmOpaque        int32
)

// readAppearance reads the registry (and DWM) now.
func readAppearance() Appearance {
	a := Appearance{Known: true}
	if v, ok := apWatch[0].dword(nameAppsUseLight); ok && v == 0 {
		a.Dark = true
	}
	if v, ok := apWatch[1].dword(nameAccentColor); ok {
		a.Accent = rgba8(v&0xFF, v>>8&0xFF, v>>16&0xFF) // ABGR
	} else if v, ok := apWatch[1].dword(nameColorization); ok {
		a.Accent = rgba8(v>>16&0xFF, v>>8&0xFF, v&0xFF) // ARGB
	} else if pDwmGetColorization != 0 && win32.HR(win32.Call(pDwmGetColorization, uintptr(unsafe.Pointer(&dwmColor)), uintptr(unsafe.Pointer(&dwmOpaque)))) >= 0 {
		a.Accent = rgba8(dwmColor>>16&0xFF, dwmColor>>8&0xFF, dwmColor&0xFF)
	}
	return a
}

func rgba8(r, g, b uint32) uint32 { return r<<24 | g<<16 | b<<8 | 0xFF }

// apFollow waits for registry changes. It blocks in WaitForMultipleObjects:
// nothing runs until the system signals one of the keys.
func apFollow() {
	apHandles = [2]uintptr{apWatch[0].event, apWatch[1].event}
	for {
		r := win32.Call(pWaitForMultiple, 2, uintptr(unsafe.Pointer(&apHandles)), 0, 0xFFFFFFFF)
		i := int(uint32(r)) // WAIT_OBJECT_0 + index
		if i < 0 || i >= len(apWatch) {
			return // WAIT_FAILED: the handles are gone; stop rather than spin
		}
		apWatch[i].settle()
		apWatch[i].arm() // before reading: a change after the read signals again
		cur := packAppearance(readAppearance())
		if old := apCache.Swap(cur); old != cur {
			apSerial.Add(1)
			Wake()
		}
	}
}

// ---- OS-drawn chrome ----------------------------------------------------------

const (
	dwmwaUseImmersiveDarkModeBefore20H1 = 19 // Windows 10 1809 (17763) up to 19H2
	dwmwaUseImmersiveDarkMode           = 20 // Windows 10 build 18985 and later, Windows 11
)

var (
	chromeOnce sync.Once
	chromeAttr uint32 // the DWMWA_ attribute to use; 0 when the system has none
	darkBool   int32  // BOOL passed to DwmSetWindowAttribute (a global: not on a stack that can move)

	// RTL_OSVERSIONINFOW
	osVersion struct {
		size, major, minor, build, platform uint32
		csd                                 [128]uint16
	}
)

func chromeSetup() {
	ntdll := win32.LoadSystem("ntdll.dll")
	pRtlGetVersion = win32.Proc(ntdll, "RtlGetVersion")
	dwm := win32.LoadSystem("dwmapi.dll")
	pDwmSetWindowAttr = win32.Proc(dwm, "DwmSetWindowAttribute")
	pDwmGetWindowAttr = win32.Proc(dwm, "DwmGetWindowAttribute")
	if pRtlGetVersion == 0 || pDwmSetWindowAttr == 0 {
		return
	}
	osVersion.size = uint32(unsafe.Sizeof(osVersion))
	if win32.HR(win32.Call(pRtlGetVersion, uintptr(unsafe.Pointer(&osVersion)))) < 0 {
		return
	}
	chromeAttr = chromeAttrFor(osVersion.major, osVersion.build)
}

// chromeAttrFor is the DWMWA_ attribute that sets the dark title bar on a
// Windows version: 20 from build 18985 (Windows 10 20H1 and Windows 11), 19 on
// the builds before it that have it (1809 and later), none on older systems.
func chromeAttrFor(major, build uint32) uint32 {
	switch {
	case major < 10:
		return 0
	case build >= 18985:
		return dwmwaUseImmersiveDarkMode
	case build >= 17763:
		return dwmwaUseImmersiveDarkModeBefore20H1
	}
	return 0
}

// SetDarkChrome makes the OS-drawn parts of the window match the app's mode
// (see appearance.go): the title bar, through DWMWA_USE_IMMERSIVE_DARK_MODE.
// Where the system has no such attribute it does nothing.
func (w *Window) SetDarkChrome(dark bool) {
	if w == nil || w.hwnd == 0 {
		return
	}
	setImmersiveDark(w.hwnd, dark)
}

// setImmersiveDark sets the attribute and returns the HRESULT; it is 0 (S_OK)
// without doing anything where the system has no attribute.
func setImmersiveDark(hwnd uintptr, dark bool) int32 {
	chromeOnce.Do(chromeSetup)
	if chromeAttr == 0 {
		return 0
	}
	darkBool = 0
	if dark {
		darkBool = 1
	}
	return win32.HR(win32.Call(pDwmSetWindowAttr, hwnd, uintptr(chromeAttr), uintptr(unsafe.Pointer(&darkBool)), 4))
}

// ReadSystemValues reports nothing on Windows (see appearance.go): the
// theme's own values apply (D34).
func ReadSystemValues(dst *SystemValues) { *dst = SystemValues{} }
