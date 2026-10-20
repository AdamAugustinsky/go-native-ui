package platform

import (
	"os"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/objc"
)

// secureOwner reports which process the system says has secure event input on:
// kCGSSessionSecureInputPID of the session dictionary, the last process to
// enable it (0 when none has).
func secureOwner(t *testing.T) func() int {
	cg, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		t.Skip("no CoreGraphics:", err)
	}
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		t.Skip("no CoreFoundation:", err)
	}
	sym := func(lib uintptr, n string) uintptr {
		p, err := purego.Dlsym(lib, n)
		if err != nil {
			t.Skipf("no %s: %v", n, err)
		}
		return p
	}
	session, get, number, release := sym(cg, "CGSessionCopyCurrentDictionary"), sym(cf, "CFDictionaryGetValue"), sym(cf, "CFNumberGetValue"), sym(cf, "CFRelease")
	return func() int {
		d := objc.F.Call0(session)
		if d == 0 {
			return 0
		}
		defer objc.F.Call1(release, d)
		num := objc.F.Call2(get, d, uintptr(objc.Str("kCGSSessionSecureInputPID")))
		if num == 0 {
			return 0
		}
		var pid int32
		objc.F.Call3(number, num, 3 /* kCFNumberSInt32Type */, uintptr(unsafe.Pointer(&pid)))
		return int(pid)
	}
}

// post sends the application's activation notification name to the observers.
func postAppNotification(name string) {
	nc := objc.Send(objc.Cls("NSNotificationCenter"), objc.Sel("defaultCenter"))
	objc.Send2(nc, objc.Sel("postNotificationName:object:"), uintptr(objc.Str(name)), 0)
}

// TestSecureInputBracketsEnableAndDisable: SetSecureInput(true) turns the
// system's secure event input on for this process once however often it is
// asked, off again with SetSecureInput(false), and while the application is
// inactive it is off whatever was asked, returning with the application. The
// system names the process that enabled it last (kCGSSessionSecureInputPID),
// which is how this process is told from the others that may have it on.
func TestSecureInputBracketsEnableAndDisable(t *testing.T) {
	loadFramework("/System/Library/Frameworks/AppKit.framework/AppKit")
	owner := secureOwner(t)
	pid := os.Getpid()
	var steps []string
	onMain(func() {
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		// While another process has it on (a locked screen's loginwindow, or a
		// test in another package running in parallel), the system's answer
		// cannot tell ours, so only this process's own state is checked. That
		// is decided at every step: the other process can take it or let it go
		// during the test.
		other := func() bool { o := owner(); return o != 0 && o != pid }
		check := func(what string, wantOurs bool) {
			if SecureInputEnabled() != wantOurs || (!other() && (owner() == pid) != wantOurs) {
				steps = append(steps, what+": our state "+boolStr(SecureInputEnabled())+", the system's owner "+itoa(owner())+", want ours "+boolStr(wantOurs))
			}
		}
		if other() {
			t.Logf("process %d has secure input on: only this process's own state is checked while it does", owner())
		}
		SetSecureInput(false) // (the notification observers exist from here on)
		postAppNotification("NSApplicationDidBecomeActiveNotification")
		check("idle", false)
		SetSecureInput(true)
		check("on", true)
		SetSecureInput(true)
		SetSecureInput(true)
		check("on three times", true)
		SetSecureInput(false)
		check("off after three ons: balanced", false)
		SetSecureInput(false)
		check("off twice", false)

		SetSecureInput(true)
		postAppNotification("NSApplicationDidResignActiveNotification")
		check("resigned active while wanted", false)
		SetSecureInput(true)
		check("asked again while inactive", false)
		postAppNotification("NSApplicationDidBecomeActiveNotification")
		check("active again, still wanted", true)
		postAppNotification("NSApplicationDidResignActiveNotification")
		SetSecureInput(false)
		postAppNotification("NSApplicationDidBecomeActiveNotification")
		check("active again, no longer wanted", false)
		SetSecureInput(false)
	})
	for _, s := range steps {
		t.Error(s)
	}
}

// TestSecureInputEndsWithTheWindow: closing the window that wanted secure
// input releases it, and the next activation (or a window opened afterwards)
// does not bring it back.
func TestSecureInputEndsWithTheWindow(t *testing.T) {
	loadFramework("/System/Library/Frameworks/AppKit.framework/AppKit")
	owner := secureOwner(t)
	pid := os.Getpid()
	var steps []string
	var skip string
	onMain(func() {
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		other := owner() != 0 && owner() != pid
		check := func(what string) {
			if SecureInputEnabled() || (!other && owner() == pid) {
				steps = append(steps, what+": secure input is still on (ours "+boolStr(SecureInputEnabled())+", the system's owner "+itoa(owner())+")")
			}
		}
		SetSecureInput(false)
		postAppNotification("NSApplicationDidBecomeActiveNotification")
		w, err := Open(Options{Title: "secure", Width: 200, Height: 100, Hidden: true})
		if err != nil {
			skip = err.Error()
			return
		}
		SetSecureInput(true)
		if !SecureInputEnabled() {
			steps = append(steps, "SetSecureInput(true) did not turn it on")
		}
		w.Close()
		check("after Close")
		postAppNotification("NSApplicationDidResignActiveNotification")
		postAppNotification("NSApplicationDidBecomeActiveNotification")
		check("after the next activation")
		w2, err := Open(Options{Title: "secure 2", Width: 200, Height: 100, Hidden: true})
		if err != nil {
			steps = append(steps, "reopen: "+err.Error())
			return
		}
		postAppNotification("NSApplicationDidBecomeActiveNotification")
		check("in the reopened window")
		w2.Close()
	})
	if skip != "" {
		t.Skip("cannot open a window here: " + skip)
	}
	for _, s := range steps {
		t.Error(s)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "none"
	}
	var b [20]byte
	i := len(b)
	for ; n > 0; n /= 10 {
		i--
		b[i] = byte('0' + n%10)
	}
	return string(b[i:])
}

func boolStr(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
