package platform

import (
	"os"
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"gonativeui/internal/win32"
)

// The registry tests change the machine's theme and accent while they run (and
// restore them): they run on CI, or where GNUI_TEST_REGISTRY is set.
func needRegistry(t *testing.T) {
	t.Helper()
	if os.Getenv("CI") == "" && os.Getenv("GNUI_TEST_REGISTRY") == "" {
		t.Skip("changes the user's theme while it runs; set GNUI_TEST_REGISTRY=1 to run")
	}
}

var (
	tAdvapi         = win32.LoadSystem("advapi32.dll")
	tRegCreateKeyEx = win32.Proc(tAdvapi, "RegCreateKeyExW")
	tRegSetValueEx  = win32.Proc(tAdvapi, "RegSetValueExW")
	tRegDeleteValue = win32.Proc(tAdvapi, "RegDeleteValueW")
)

// regKey opens (creating it when missing) HKCU\sub for writing.
func regKey(t *testing.T, sub string) uintptr {
	t.Helper()
	var k uintptr
	const keyWrite, keyRead = 0x20006, 0x20019
	// syscall.SyscallN, not win32.Call: it keeps what the pointers converted in
	// its argument list point to alive for the call.
	r, _, _ := syscall.SyscallN(tRegCreateKeyEx, hkeyCurrentUser, uintptr(unsafe.Pointer(mustUTF16(sub))), 0, 0, 0, keyWrite|keyRead, 0, uintptr(unsafe.Pointer(&k)), 0)
	if uint32(r) != 0 {
		t.Fatalf("RegCreateKeyEx %s: %#x", sub, r)
	}
	t.Cleanup(func() { win32.Call(pRegCloseKey, k) })
	return k
}

func regGet(key uintptr, name string) (uint32, bool) {
	var typ, v uint32
	n := uint32(4)
	r, _, _ := syscall.SyscallN(pRegQueryValueExW, key, uintptr(unsafe.Pointer(mustUTF16(name))), 0,
		uintptr(unsafe.Pointer(&typ)), uintptr(unsafe.Pointer(&v)), uintptr(unsafe.Pointer(&n)))
	return v, uint32(r) == 0 && typ == regDWORD
}

func regSet(t *testing.T, key uintptr, name string, v uint32) {
	t.Helper()
	r, _, _ := syscall.SyscallN(tRegSetValueEx, key, uintptr(unsafe.Pointer(mustUTF16(name))), 0, regDWORD, uintptr(unsafe.Pointer(&v)), 4)
	if uint32(r) != 0 {
		t.Fatalf("RegSetValueEx %s: %#x", name, r)
	}
}

// regRestore puts the value back (or deletes it when it did not exist) when
// the test ends.
func regRestore(t *testing.T, key uintptr, name string) {
	orig, had := regGet(key, name)
	t.Cleanup(func() {
		if had {
			regSet(t, key, name, orig)
		} else {
			syscall.SyscallN(tRegDeleteValue, key, uintptr(unsafe.Pointer(mustUTF16(name))))
		}
	})
}

// waitFor polls cond (the test side only; the code under test never polls).
func waitFor(cond func() bool, d time.Duration) bool {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if cond() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return cond()
}

// The first read is what the first frame waits for (content-first startup):
// loading the entry points, opening and arming both keys, reading the values.
// Meaningful only as the first read in the process.
func TestAppearanceFirstCall(t *testing.T) {
	if apCache.Load() != 0 {
		t.Skip("an earlier test already read the appearance")
	}
	t0 := time.Now()
	a := SystemAppearance()
	d := time.Since(t0)
	t.Logf("first SystemAppearance call: %v (dark=%v accent=%08x)", d, a.Dark, a.Accent)
	if !a.Known {
		t.Error("not known")
	}
	if d > 50*time.Millisecond {
		t.Errorf("the first call took %v", d)
	}
}

func TestAppearanceFollowsDarkMode(t *testing.T) {
	needRegistry(t)
	a0 := SystemAppearance() // starts the watcher
	if !a0.Known {
		t.Fatal("not known")
	}
	key := regKey(t, personalizeKey)
	regRestore(t, key, "AppsUseLightTheme")
	for _, dark := range []bool{!a0.Dark, a0.Dark, !a0.Dark} {
		serial := AppearanceSerial()
		v := uint32(1)
		if dark {
			v = 0
		}
		t0 := time.Now()
		regSet(t, key, "AppsUseLightTheme", v)
		if !waitFor(func() bool { return AppearanceSerial() != serial }, 10*time.Second) {
			t.Fatalf("no change reported within 10 s after AppsUseLightTheme=%d", v)
		}
		t.Logf("AppsUseLightTheme=%d reported after %v", v, time.Since(t0))
		if got := SystemAppearance().Dark; got != dark {
			t.Fatalf("after AppsUseLightTheme=%d: Dark=%v", v, got)
		}
	}
}

func TestAppearanceFollowsAccent(t *testing.T) {
	needRegistry(t)
	SystemAppearance()
	key := regKey(t, dwmKey)
	regRestore(t, key, "AccentColor")
	for _, c := range []struct{ abgr, rgba uint32 }{
		{0xFF563412, 0x123456FF}, // R=0x12 G=0x34 B=0x56
		{0xFF3C78F0, 0xF0783CFF},
	} {
		serial := AppearanceSerial()
		regSet(t, key, "AccentColor", c.abgr)
		t0 := time.Now()
		if !waitFor(func() bool { return SystemAppearance().Accent == c.rgba }, 10*time.Second) {
			t.Fatalf("AccentColor=%08x: Accent=%08x, want %08x", c.abgr, SystemAppearance().Accent, c.rgba)
		}
		if AppearanceSerial() == serial {
			t.Errorf("AccentColor=%08x: the serial did not change", c.abgr)
		}
		t.Logf("AccentColor=%08x reported after %v", c.abgr, time.Since(t0))
	}
}

func TestAppearanceAllocs(t *testing.T) {
	SystemAppearance()
	if n := testing.AllocsPerRun(1000, func() { SystemAppearance(); AppearanceSerial() }); n != 0 {
		t.Errorf("warm SystemAppearance allocates %v per call", n)
	}
}

func TestChromeAttrFor(t *testing.T) {
	for _, c := range []struct{ major, build, want uint32 }{
		{6, 9600, 0},    // Windows 8.1
		{10, 17134, 0},  // 1803
		{10, 17763, 19}, // 1809
		{10, 18362, 19}, // 1903
		{10, 18985, 20}, // first build with 20
		{10, 19045, 20}, // 22H2
		{10, 22000, 20}, // Windows 11
		{10, 26100, 20}, // Windows 11 24H2
	} {
		if got := chromeAttrFor(c.major, c.build); got != c.want {
			t.Errorf("build %d.%d: attribute %d, want %d", c.major, c.build, got, c.want)
		}
	}
}

// The dark title bar on a real top-level window, read back from DWM.
func TestSetDarkChromeReadBack(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	chromeOnce.Do(chromeSetup)
	if chromeAttr == 0 {
		t.Skipf("Windows build %d.%d has no dark title bar attribute", osVersion.major, osVersion.build)
	}
	const wsOverlapped = 0x00CF0000
	class, title := mustUTF16("STATIC"), mustUTF16("appearance test")
	hwnd := win32.Call(pCreateWindowExW, 0, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)), wsOverlapped, 0, 0, 200, 100, 0, 0, 0, 0)
	runtime.KeepAlive(class)
	runtime.KeepAlive(title)
	if hwnd == 0 {
		t.Skip("cannot create a window here")
	}
	defer win32.Call(pDestroyWindow, hwnd)
	w := &Window{hwnd: hwnd}
	read := func() int32 {
		got := int32(-1)
		if r, _, _ := syscall.SyscallN(pDwmGetWindowAttr, hwnd, uintptr(chromeAttr), uintptr(unsafe.Pointer(&got)), 4); win32.HR(r) < 0 {
			t.Fatalf("DwmGetWindowAttribute(%d): %#x", chromeAttr, uint32(r))
		}
		return got
	}
	if hr := setImmersiveDark(hwnd, true); hr < 0 {
		t.Fatalf("DwmSetWindowAttribute(%d): %#x", chromeAttr, uint32(hr))
	}
	if v := read(); v != 1 {
		t.Errorf("after SetDarkChrome(true) the attribute is %d", v)
	}
	w.SetDarkChrome(false)
	if v := read(); v != 0 {
		t.Errorf("after SetDarkChrome(false) the attribute is %d", v)
	}
	w.SetDarkChrome(true)
	if v := read(); v != 1 {
		t.Errorf("after SetDarkChrome(true) again the attribute is %d", v)
	}
}

func TestSetDarkChromeWithoutWindow(t *testing.T) {
	(*Window)(nil).SetDarkChrome(true)
	(&Window{}).SetDarkChrome(true)
}
