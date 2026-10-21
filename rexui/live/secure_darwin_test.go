//go:build darwin

package live

import (
	"os"
	"strings"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/app"
	"gonativeui/internal/objc"
	"gonativeui/platform"
	"gonativeui/rexui"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget/base"
	"gonativeui/widget/widgettest"
)

// pwLab is an app-lock window: a password field and a plain one after it.
type pwLab struct {
	k    *base.Kit
	pw   ui.TextBuffer
	note ui.TextBuffer
}

func (p *pwLab) Init(a *app.App) error {
	p.k = base.NewKit(a.UI, a.Text, rex.Theme())
	return nil
}

func (p *pwLab) Build(c *ui.Context) {
	k := p.k
	k.Begin(c)
	k.NextPad(20)
	k.NextGap(12)
	k.Column("col")
	rexui.TextField(k, "Password", &p.pw, "Password", rexui.FieldOpts{Secure: true})
	rexui.TextField(k, "Note", &p.note, "", rexui.FieldOpts{})
	k.EndColumn()
	k.End()
}

// secureSystem reads the system's side of secure event input: Carbon's
// IsSecureEventInputEnabled (any process) and the process that turned it on
// last (kCGSSessionSecureInputPID, 0 for none). The function it returns runs
// on the main thread.
func secureSystem(t *testing.T) func() (on bool, owner int) {
	t.Helper()
	open := func(fw string) uintptr {
		h, err := purego.Dlopen("/System/Library/Frameworks/"+fw+".framework/"+fw, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			t.Skip(err)
		}
		return h
	}
	carbon, cg, cf := open("Carbon"), open("CoreGraphics"), open("CoreFoundation")
	sym := func(lib uintptr, n string) uintptr {
		p, err := purego.Dlsym(lib, n)
		if err != nil {
			t.Skip(err)
		}
		return p
	}
	isOn, session, get, number, release := sym(carbon, "IsSecureEventInputEnabled"), sym(cg, "CGSessionCopyCurrentDictionary"),
		sym(cf, "CFDictionaryGetValue"), sym(cf, "CFNumberGetValue"), sym(cf, "CFRelease")
	return func() (bool, int) {
		key := objc.Str("kCGSSessionSecureInputPID")
		on := objc.F.Call0(isOn)&0xFF != 0
		d := objc.F.Call0(session)
		if d == 0 {
			return on, 0
		}
		defer objc.F.Call1(release, d)
		num := objc.F.Call2(get, d, uintptr(key))
		if num == 0 {
			return on, 0
		}
		var pid int32
		objc.F.Call3(number, num, 3 /* kCFNumberSInt32Type */, uintptr(unsafe.Pointer(&pid)))
		return on, int(pid)
	}
}

// romanOnly reports whether the view's input context takes Roman input
// sources only.
func romanOnly(v objc.ID) bool {
	selCtx, selAllowed, selCount, selAt := objc.Sel("inputContext"), objc.Sel("allowedInputSourceLocales"), objc.Sel("count"), objc.Sel("objectAtIndex:")
	arr := objc.Send(objc.Send(v, selCtx), selAllowed)
	return arr != 0 && objc.Send(arr, selCount) == 1 &&
		string(objc.AppendUTF8(nil, objc.Send1(arr, selAt, 0))) == "NSAllRomanInputSourcesLocaleIdentifier"
}

// A password field in a real window, from the real keyboard path: typing
// shows bullets (the accessibility bridge reports AXSecureTextField with
// bullets for a value), secure event input is on while it has the focus and
// off after Tab, Copy and Cut leave the system pasteboard alone while paste
// works, an input method's marked text never reaches the text before its
// commit, and closing the window ends secure input.
func TestLiveSecureField(t *testing.T) {
	sys := secureSystem(t)
	p := &pwLab{}
	l := widgettest.StartLive(t, app.Config{Title: "app lock", Width: 420, Height: 220}, p)
	l.Frames(4)
	var faked, closed bool // the test process may not be the active app: tell secure input it is, as the platform tests do
	defer func() {
		if faked && !closed {
			l.Do(func(*app.App) { postApp("NSApplicationDidResignActiveNotification") })
		}
	}()
	pid := os.Getpid()
	check := func(what string, want bool) {
		t.Helper()
		var ours, roman, on bool
		var owner int
		l.Native(func(w widgettest.Win) {
			ours, roman = platform.SecureInputEnabled(), romanOnly(w.View)
			on, owner = sys()
		})
		other := owner != 0 && owner != pid
		if other {
			t.Logf("%s: the system names process %d (IsSecureEventInputEnabled %v): only this process's own state is checked", what, owner, on)
		}
		if ours != want || roman != want || (!other && (on != want || (owner == pid) != want)) {
			t.Errorf("%s: ours %v, Roman only %v, system %v (owner %d, us %d), want %v", what, ours, roman, on, owner, pid, want)
		}
	}
	check("before the focus", false)

	l.Click(ui.RoleTextField, "Password")
	l.Do(func(a *app.App) {
		if !platform.SecureInputEnabled() && !objc.SendBool(objc.Send(objc.Cls("NSApplication"), objc.Sel("sharedApplication")), objc.Sel("isActive")) {
			postApp("NSApplicationDidBecomeActiveNotification")
			faked = true
		}
	})
	if faked {
		t.Log("the test process is not the active app: its activation was posted to secure input")
	}
	l.Type("hunter2")
	if got := p.pw.String(); got != "hunter2" {
		t.Fatalf("typed: %q", got)
	}
	check("focused", true)

	var subrole, value string
	l.Native(func(w widgettest.Win) {
		if f := axFind(w.Window, "AXTextField", "Password", 6); f != 0 {
			subrole = string(objc.AppendUTF8(nil, objc.Send(f, objc.Sel("accessibilitySubrole"))))
			value = string(objc.AppendUTF8(nil, objc.Send(f, objc.Sel("accessibilityValue"))))
		}
	})
	l.Frames(2)
	if subrole != "AXSecureTextField" || value != strings.Repeat("•", 7) {
		t.Errorf("accessibility: subrole %q, value %q", subrole, value)
	}
	if n := l.Must(ui.RoleTextField, "Password"); n.State&ui.SemSecure == 0 {
		t.Errorf("semantic state %b", n.State)
	}

	l.Do(func(*app.App) { platform.SetClipboard("sentinel") })
	l.Press('A', ui.ModSuper)
	l.Press('C', ui.ModSuper)
	l.Press('X', ui.ModSuper)
	var clip string
	l.Do(func(*app.App) { clip = platform.Clipboard() })
	if clip != "sentinel" || p.pw.String() != "hunter2" {
		t.Errorf("copy and cut: pasteboard %q, text %q", clip, p.pw.String())
	}
	l.Do(func(*app.App) { platform.SetClipboard("from the pasteboard") })
	l.Press('V', ui.ModSuper)
	l.Until("the paste", 120, func() bool { return p.pw.String() == "from the pasteboard" })

	// Marked text from an input method: masked, and not the text's until it
	// commits.
	l.Native(func(w widgettest.Win) {
		widgettest.TextInput(w.View).SetMarkedText("か", widgettest.Range{Loc: 1}, widgettest.NoRange)
	})
	l.Frames(2)
	if got := p.pw.String(); got != "from the pasteboard" {
		t.Errorf("marked text reached the buffer: %q", got)
	}
	l.Native(func(w widgettest.Win) {
		if f := axFind(w.Window, "AXTextField", "Password", 6); f != 0 {
			value = string(objc.AppendUTF8(nil, objc.Send(f, objc.Sel("accessibilityValue"))))
		}
	})
	if strings.Trim(value, "•") != "" {
		t.Errorf("accessibility value while composing: %q", value)
	}
	l.Native(func(w widgettest.Win) { widgettest.TextInput(w.View).InsertText("仮", widgettest.NoRange) })
	l.Frames(2)
	if got := p.pw.String(); got != "from the pasteboard仮" {
		t.Errorf("commit: %q", got)
	}

	l.Press(ui.KeyTab, 0)
	l.Type("ok")
	if p.note.String() != "ok" {
		t.Fatalf("Tab to the plain field: %q", p.note.String())
	}
	check("after Tab", false)
	l.Click(ui.RoleTextField, "Password")
	check("back in the field", true)

	p.pw.Wipe()
	l.Frames(2)
	l.Close()
	closed = true
	var ours, on bool
	var owner int
	widgettest.OnMain(func() {
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		ours = platform.SecureInputEnabled()
		on, owner = sys()
		if faked {
			postApp("NSApplicationDidResignActiveNotification")
		}
	})
	if ours || (owner == pid && on) {
		t.Errorf("after the window closed: ours %v, system %v (owner %d)", ours, on, owner)
	}
}

// axFind returns the first element under o, depth first, with the role and
// label. Main thread.
func axFind(o objc.ID, role, label string, depth int) objc.ID {
	selKids, selCount, selAt := objc.Sel("accessibilityChildren"), objc.Sel("count"), objc.Sel("objectAtIndex:")
	selRole, selLabel := objc.Sel("accessibilityRole"), objc.Sel("accessibilityLabel")
	kids := objc.Send(o, selKids)
	for i := 0; i < int(objc.Send(kids, selCount)); i++ {
		k := objc.Send1(kids, selAt, uintptr(i))
		if string(objc.AppendUTF8(nil, objc.Send(k, selRole))) == role && string(objc.AppendUTF8(nil, objc.Send(k, selLabel))) == label {
			return k
		}
		if depth > 0 {
			if f := axFind(k, role, label, depth-1); f != 0 {
				return f
			}
		}
	}
	return 0
}

// postApp sends the application's activation notification name to its
// observers. Main thread.
func postApp(name string) {
	nc := objc.Send(objc.Cls("NSNotificationCenter"), objc.Sel("defaultCenter"))
	sel, str := objc.Sel("postNotificationName:object:"), objc.Str(name)
	objc.Send2(nc, sel, uintptr(str), 0)
}
