//go:build darwin

package platform

import (
	"testing"
	"time"

	"gonativeui/internal/objc"
)

// The first read is what the first frame waits for (content-first startup).
// Creating NSApplication (about 25 ms) is not its cost: Open pays that too,
// so it is done first and timed apart. Meaningful when the test runs alone
// (go test -run FirstCall): other tests may have read already.
func TestAppearanceFirstCall(t *testing.T) {
	var a Appearance
	var d, dApp time.Duration
	onMain(func() {
		t0 := time.Now()
		loadFramework("/System/Library/Frameworks/AppKit.framework/AppKit")
		objc.Send(objc.Cls("NSApplication"), selSharedApplication)
		t1 := time.Now()
		a = SystemAppearance()
		d, dApp = time.Since(t1), t1.Sub(t0)
	})
	t.Logf("NSApplication %v, then the first SystemAppearance call: %v (dark=%v accent=%08x)", dApp, d, a.Dark, a.Accent)
	if !a.Known {
		t.Fatal("effectiveAppearance not readable")
	}
	if a.Accent&0xFF != 0xFF {
		t.Errorf("accent %08x should be opaque", a.Accent)
	}
	if d > 100*time.Millisecond {
		t.Errorf("first call took %v", d)
	}
}

func TestAppearanceAllocs(t *testing.T) {
	onMain(func() { SystemAppearance() })
	var warm, reread float64
	onMain(func() {
		warm = testing.AllocsPerRun(200, func() { SystemAppearance() })
		reread = testing.AllocsPerRun(50, func() {
			apStale.Store(true)
			SystemAppearance()
		})
	})
	if warm != 0 {
		t.Errorf("warm SystemAppearance allocates %v per call", warm)
	}
	if reread != 0 {
		t.Errorf("re-reading allocates %v per call", reread)
	}
}

func postNotification(center string, name string) {
	c := objc.Send(objc.Cls(center), objc.Sel("defaultCenter"))
	objc.Send2(c, objc.Sel("postNotificationName:object:"), uintptr(objc.Str(name)), 0)
}

// A system-colours notification (what an accent change posts) bumps the serial
// and makes the next call read again.
func TestAppearanceNotificationBumpsSerial(t *testing.T) {
	var before, after uint32
	var stale bool
	onMain(func() {
		SystemAppearance()
		before = AppearanceSerial()
		postNotification("NSNotificationCenter", "NSSystemColorsDidChangeNotification")
		after = AppearanceSerial()
		stale = apStale.Load()
		SystemAppearance()
	})
	if after != before+1 || !stale {
		t.Fatalf("serial %d -> %d, stale=%v", before, after, stale)
	}
	if apStale.Load() {
		t.Error("the read did not clear the stale flag")
	}
}

// The app's own appearance drives effectiveAppearance: forcing dark on NSApp
// must reach SystemAppearance through the key-value observer, with the new
// value already readable when the serial changes.
func TestAppearanceFollowsEffectiveAppearance(t *testing.T) {
	type result struct {
		serial0, serial1, serial2 uint32
		dark0, dark1, dark2       bool
		known                     bool
	}
	var r result
	onMain(func() {
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		a := SystemAppearance()
		r.known, r.dark0, r.serial0 = a.Known, a.Dark, AppearanceSerial()
		app := apApp
		defer objc.Send1(app, selSetAppearance, 0)
		force := func(name objc.ID) {
			objc.Send1(app, selSetAppearance, uintptr(objc.Send1(apClsAppearance, selAppearanceNamed, uintptr(name))))
		}
		force(apDarkName)
		r.serial1, r.dark1 = AppearanceSerial(), SystemAppearance().Dark
		force(apAquaName)
		r.serial2, r.dark2 = AppearanceSerial(), SystemAppearance().Dark
	})
	if !r.known {
		t.Fatal("not known")
	}
	if r.serial1 == r.serial0 || !r.dark1 {
		t.Errorf("forced dark: serial %d -> %d, dark=%v", r.serial0, r.serial1, r.dark1)
	}
	if r.serial2 == r.serial1 || r.dark2 {
		t.Errorf("forced light: serial %d -> %d, dark=%v", r.serial1, r.serial2, r.dark2)
	}
}

// SetDarkChrome sets the window's NSAppearance; read it back by name.
func TestSetDarkChromeRoundTrip(t *testing.T) {
	var got [3]bool
	var names [3]string
	onMain(func() {
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		SystemAppearance() // resolves the constants
		win := objc.Send(objc.Cls("NSWindow"), selAlloc)
		win = objc.ID(objc.F.Reset().Arg(uintptr(win)).Arg(uintptr(selInitWindow)).
			ArgRect(0, 0, 200, 100).Arg(1).Arg(2).ArgBool(false).Call(objc.MsgSend).R0())
		if win == 0 {
			return
		}
		objc.Send1(win, selSetReleased, 0)
		defer objc.Send(win, selClose)
		w := &Window{win: win}
		read := func(i int) {
			a := objc.Send(win, selAppearance)
			if a == 0 {
				return
			}
			got[i] = true
			names[i] = string(objc.AppendUTF8(nil, objc.Send(a, objc.Sel("name"))))
		}
		w.SetDarkChrome(true)
		read(0)
		w.SetDarkChrome(false)
		read(1)
		w.SetDarkChrome(true)
		read(2)
	})
	if !got[0] {
		t.Skip("cannot create an NSWindow here")
	}
	for i, want := range [3]string{"NSAppearanceNameDarkAqua", "NSAppearanceNameAqua", "NSAppearanceNameDarkAqua"} {
		if !got[i] || names[i] != want {
			t.Errorf("step %d: appearance %q, want %q", i, names[i], want)
		}
	}
}

// A nil or window-less Window is ignored.
func TestSetDarkChromeWithoutWindow(t *testing.T) {
	(*Window)(nil).SetDarkChrome(true)
	(&Window{}).SetDarkChrome(true)
}
