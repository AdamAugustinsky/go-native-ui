//go:build darwin

package platform

import (
	"strconv"
	"testing"

	"gonativeui/internal/objc"
)

// A quit Apple event's reason is its kAEQuitReason parameter, built as the
// SDK has it: each documented value is a logout (QuitLogout); no parameter,
// an unknown value, or the code set as an attribute instead is a plain quit.
func TestQuitEventReasonIsTheParameter(t *testing.T) {
	cases := []struct {
		name string
		code uintptr
		attr bool // set as an attribute, not a parameter
		want QuitReason
	}{
		{"none", 0, false, QuitDock},
		{"quitAll", aeQuitAll, false, QuitLogout},
		{"shutDown", aeShutDown, false, QuitLogout},
		{"restart", aeRestart, false, QuitLogout},
		{"reallyLogOut", aeReallyLogOut, false, QuitLogout},
		{"logOut", aeLogOut, false, QuitLogout},
		{"showRestartDialog", aeShowRestartDialog, false, QuitLogout},
		{"showShutdownDialog", aeShowShutdownDlg, false, QuitLogout},
		{"unknown", 0x61626364 /* 'abcd' */, false, QuitDock},
		{"attribute", aeReallyLogOut, true, QuitDock},
	}
	onMain(func() {
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		cls := objc.Cls("NSAppleEventDescriptor")
		target := objc.Send(cls, objc.Sel("currentProcessDescriptor"))
		selMake, selEnum := objc.Sel("appleEventWithEventClass:eventID:targetDescriptor:returnID:transactionID:"), objc.Sel("descriptorWithEnumCode:")
		selParam, selAttr := objc.Sel("setParamDescriptor:forKeyword:"), objc.Sel("setAttributeDescriptor:forKeyword:")
		for _, c := range cases {
			ev := objc.ID(objc.F.Reset().Arg(uintptr(cls)).Arg(uintptr(selMake)).Arg(aeCoreClass).Arg(aeQuit).
				Arg(uintptr(target)).Arg(^uintptr(0)).Arg(0).Call(objc.MsgSend).R0())
			if c.code != 0 {
				why := objc.Send1(cls, selEnum, c.code)
				sel := selParam
				if c.attr {
					sel = selAttr
				}
				objc.Send2(ev, sel, uintptr(why), aeQuitReason)
			}
			if got := quitEventReason(ev); got != c.want {
				t.Errorf("%s: reason %d, want %d", c.name, got, c.want)
			}
		}
	})
}

// DebugAssumeKey is per window and scoped: a window not opted in still loses
// the focus (EvFocus), the opted-in one does not and reports Focused, the
// restore ends it, and a window that closes leaves nothing set.
func TestDebugAssumeKeyIsPerWindowAndScoped(t *testing.T) {
	var got []string
	var skip string
	onMain(func() {
		w, y := openKeyRig()
		if y != "" {
			skip = y
			return
		}
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		losses := func() int {
			n := 0
			for _, e := range w.q.ev {
				if e.Kind == EvFocus && e.Flags&FlagDown == 0 {
					n++
				}
			}
			w.q.reset()
			return n
		}
		w.q.reset()
		other := &Window{}
		restoreOther := DebugAssumeKey(other) // another fixture's window
		windowFocus(false)
		got = append(got, "other-opted", strconv.Itoa(losses()))
		restoreOther()
		restore := DebugAssumeKey(w)
		windowFocus(false)
		got = append(got, "opted", strconv.Itoa(losses()), boolS(w.Focused()))
		restore()
		windowFocus(false)
		got = append(got, "restored", strconv.Itoa(losses()))
		DebugAssumeKey(w) // never restored: the window's closing ends it
		w.Close()
		got = append(got, "after-close", boolS(keyAssumed(w)))
	})
	if skip != "" {
		t.Skip("cannot open a window here: " + skip)
	}
	want := []string{"other-opted", "1", "opted", "0", "true", "restored", "1", "after-close", "false"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func boolS(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
