//go:build windows

package hotkey

import (
	"errors"
	"fmt"
	"syscall"

	"gonativeui/internal/win32"
	"gonativeui/platform"
	"gonativeui/ui"
)

// RegisterHotKey on the app's hidden window (D70, D68; probe P7). Each hotkey
// is registered on platform.AppWindow with an OS id of ours (0x0001–0xBFFF,
// the range an app owns) and MOD_NOREPEAT, so a chord held down presses once.
// The first Register installs one WM_HOTKEY handler on the app window
// (platform.HandleAppMessage), which looks the OS id up and delivers the
// app's ID, synchronously on the main thread, as an EvService from
// platform.SourceHotkey (platform.DeliverService). A chord another process
// (or this one, under another ID) holds fails with
// ERROR_HOTKEY_ALREADY_REGISTERED, which is ErrTaken; unlike macOS, Windows
// reports another process's chords (TestHotkeyHeldByAnotherProcess).
//
// Keys are physical, as on macOS: a letter, a digit or a punctuation key is
// the key at that place on a US keyboard, whose virtual key in the layout in
// use at registration is MapVirtualKeyW's answer for its scan code.

const (
	wmHotkey     = 0x0312
	modAlt       = 0x0001
	modControl   = 0x0002
	modShift     = 0x0004
	modWin       = 0x0008
	modNoRepeat  = 0x4000
	mapvkVscToVk = 1

	errHotkeyAlreadyRegistered = 1409
	maxOSID                    = 0xBFFF
)

var (
	user32            = win32.LoadSystem("user32.dll")
	pRegisterHotKey   = win32.Proc(user32, "RegisterHotKey")
	pUnregisterHotKey = win32.Proc(user32, "UnregisterHotKey")
	pSendMessageW     = win32.Proc(user32, "SendMessageW")
	pMapVirtualKeyW   = win32.Proc(user32, "MapVirtualKeyW")
)

type slot struct {
	id   ID
	s    Shortcut
	osID uintptr
	hwnd uintptr // the app window it is registered on
	mods uintptr // MOD_* without MOD_NOREPEAT (WM_HOTKEY's lParam)
	vk   uintptr
}

// Main thread only.
var hk struct {
	handler uintptr // the app window the WM_HOTKEY handler is installed on
	slots   [MaxHotkeys]slot
	n       int
	lastOS  uintptr // the last OS id given
	pressed uint64  // presses delivered (tests)
}

// onHotkey is the app window's WM_HOTKEY handler: wParam is the OS id
// (negative for the system's own: IDHOT_SNAPWINDOW, IDHOT_SNAPDESKTOP).
func onHotkey(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
	for i := 0; i < hk.n; i++ {
		if hk.slots[i].osID == wp {
			hk.pressed++
			platform.DeliverService(platform.SourceHotkey, uint64(hk.slots[i].id), 0, 0)
			return 0
		}
	}
	return 0
}

func find(id ID) int {
	for i := 0; i < hk.n; i++ {
		if hk.slots[i].id == id {
			return i
		}
	}
	return -1
}

// nextOSID is an OS id no registered hotkey has.
func nextOSID() uintptr {
	for {
		if hk.lastOS++; hk.lastOS > maxOSID {
			hk.lastOS = 1
		}
		free := true
		for i := 0; i < hk.n; i++ {
			if hk.slots[i].osID == hk.lastOS {
				free = false
				break
			}
		}
		if free {
			return hk.lastOS
		}
	}
}

func osRegister(id ID, s Shortcut) error {
	hwnd := platform.AppWindow()
	if hwnd == 0 || pRegisterHotKey == 0 {
		return ErrNotStarted
	}
	if hk.handler != hwnd {
		if err := platform.HandleAppMessage(wmHotkey, onHotkey); err != nil {
			return fmt.Errorf("hotkey: %w", err)
		}
		hk.handler = hwnd
	}
	vk, ok := virtualKey(s.Key)
	if !ok {
		return ErrInvalid
	}
	mods := winMods(s.Effective())
	i := find(id)
	if i >= 0 && hk.slots[i].hwnd == hwnd && hk.slots[i].mods == mods && hk.slots[i].vk == vk {
		hk.slots[i].s = s
		return nil // the same chord: nothing to do
	}
	if i < 0 && hk.n == MaxHotkeys {
		return ErrFull
	}
	osID := nextOSID()
	r, _, e := syscall.SyscallN(pRegisterHotKey, hwnd, osID, mods|modNoRepeat, vk)
	if r == 0 {
		if e == errHotkeyAlreadyRegistered {
			return ErrTaken
		}
		return fmt.Errorf("hotkey: RegisterHotKey: %w", e)
	}
	if i >= 0 { // the new chord holds: release the old one
		win32.Call(pUnregisterHotKey, hk.slots[i].hwnd, hk.slots[i].osID)
	} else {
		i = hk.n
		hk.n++
	}
	hk.slots[i] = slot{id: id, s: s, osID: osID, hwnd: hwnd, mods: mods, vk: vk}
	return nil
}

func osUnregister(id ID) {
	i := find(id)
	if i < 0 {
		return
	}
	win32.Call(pUnregisterHotKey, hk.slots[i].hwnd, hk.slots[i].osID)
	hk.n--
	hk.slots[i] = hk.slots[hk.n]
	hk.slots[hk.n] = slot{}
}

func osUnregisterAll() {
	for hk.n > 0 {
		osUnregister(hk.slots[hk.n-1].id)
	}
}

func osRegistered(id ID) (Shortcut, bool) {
	if i := find(id); i >= 0 {
		return hk.slots[i].s, true
	}
	return Shortcut{}, false
}

// errNotRegistered is Simulate's error for an ID that is not registered.
var errNotRegistered = errors.New("hotkey: not registered")

// osSimulate sends WM_HOTKEY with the hotkey's OS id, modifiers and key to
// the app window, as the system does for a press: from the main thread the
// window procedure runs before SendMessageW returns, so the press reaches
// the app before Simulate does.
func osSimulate(id ID) error {
	i := find(id)
	if i < 0 {
		return errNotRegistered
	}
	sl := &hk.slots[i]
	win32.Call(pSendMessageW, sl.hwnd, wmHotkey, sl.osID, sl.mods|sl.vk<<16)
	return nil
}

func winMods(m ui.Mods) uintptr {
	var w uintptr
	if m&ui.ModAlt != 0 {
		w |= modAlt
	}
	if m&ui.ModCtrl != 0 {
		w |= modControl
	}
	if m&ui.ModShift != 0 {
		w |= modShift
	}
	if m&ui.ModSuper != 0 {
		w |= modWin
	}
	return w
}

// virtualKey is the virtual key RegisterHotKey takes for k: a named key's
// own; for a letter, a digit or punctuation, the virtual key of the US
// position's scan code in the current layout (physical keys).
func virtualKey(k ui.KeyCode) (uintptr, bool) {
	switch {
	case k >= ui.KeyF1 && k <= ui.KeyF12:
		return uintptr(0x70 + k - ui.KeyF1), true // VK_F1..VK_F12
	case k >= ui.KeyF13 && k <= ui.KeyF20:
		return uintptr(0x7C + k - ui.KeyF13), true // VK_F13..VK_F20
	case k >= ui.KeyNum0 && k <= ui.KeyNum9:
		return uintptr(0x60 + k - ui.KeyNum0), true // VK_NUMPAD0..9
	}
	switch k {
	case ui.KeyEnter:
		return 0x0D, true
	case ui.KeySpace:
		return 0x20, true
	case ui.KeyEscape:
		return 0x1B, true
	case ui.KeyTab:
		return 0x09, true
	case ui.KeyBackspace:
		return 0x08, true
	case ui.KeyDelete:
		return 0x2E, true
	case ui.KeyLeft:
		return 0x25, true
	case ui.KeyRight:
		return 0x27, true
	case ui.KeyUp:
		return 0x26, true
	case ui.KeyDown:
		return 0x28, true
	case ui.KeyHome:
		return 0x24, true
	case ui.KeyEnd:
		return 0x23, true
	case ui.KeyPageUp:
		return 0x21, true
	case ui.KeyPageDown:
		return 0x22, true
	case ui.KeyNumMultiply:
		return 0x6A, true
	case ui.KeyNumAdd:
		return 0x6B, true
	case ui.KeyNumSubtract:
		return 0x6D, true
	case ui.KeyNumDecimal:
		return 0x6E, true
	case ui.KeyNumDivide:
		return 0x6F, true
	case ui.KeyNumClear:
		return 0x0C, true
	}
	sc := scanCode(k)
	if sc == 0 {
		return 0, false
	}
	if pMapVirtualKeyW != 0 {
		if vk := win32.Call(pMapVirtualKeyW, uintptr(sc), mapvkVscToVk); vk != 0 {
			return vk, true
		}
	}
	if (k >= 'A' && k <= 'Z') || (k >= '0' && k <= '9') {
		return uintptr(k), true // VK_A.. and VK_0.. are the ASCII codes
	}
	return 0, false
}

// scanCode is the set-1 scan code of the key at k's place on a US keyboard
// (letters, digits, punctuation); 0 for none.
func scanCode(k ui.KeyCode) uint8 {
	switch {
	case k >= 'A' && k <= 'Z':
		return letterScan[k-'A']
	case k >= '1' && k <= '9':
		return uint8(0x02 + k - '1')
	case k == '0':
		return 0x0B
	}
	switch k {
	case ui.KeyMinus:
		return 0x0C
	case ui.KeyEqual:
		return 0x0D
	case ui.KeyLeftBracket:
		return 0x1A
	case ui.KeyRightBracket:
		return 0x1B
	case ui.KeySemicolon:
		return 0x27
	case ui.KeyQuote:
		return 0x28
	case ui.KeyGrave:
		return 0x29
	case ui.KeyBackslash:
		return 0x2B
	case ui.KeyComma:
		return 0x33
	case ui.KeyPeriod:
		return 0x34
	case ui.KeySlash:
		return 0x35
	}
	return 0
}

var letterScan = [26]uint8{
	0x1E, 0x30, 0x2E, 0x20, 0x12, 0x21, 0x22, 0x23, 0x17, 0x24, 0x25, 0x26, 0x32, // A–M
	0x31, 0x18, 0x19, 0x10, 0x13, 0x1F, 0x14, 0x16, 0x2F, 0x11, 0x2D, 0x15, 0x2C, // N–Z
}
