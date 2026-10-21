//go:build darwin

package live

import (
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/internal/objc"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// axFind returns the first element under o, depth first, with the role and
// label (or value, for static text).
func axFind(o objc.ID, role, label string, depth int) objc.ID {
	kids := objc.Send(o, objc.Sel("accessibilityChildren"))
	n := int(objc.Send(kids, objc.Sel("count")))
	for i := 0; i < n; i++ {
		k := objc.Send1(kids, objc.Sel("objectAtIndex:"), uintptr(i))
		r := string(objc.AppendUTF8(nil, objc.Send(k, objc.Sel("accessibilityRole"))))
		l := objc.Send(k, objc.Sel("accessibilityLabel"))
		if r == role && string(objc.AppendUTF8(nil, l)) == label {
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

// The bridge in a real window: inactive until the window's accessibility
// children are asked for; then the probe's elements are the window's (through
// its content view), a press through the protocol presses the app's button,
// typing shows in the field's value, an unchanged frame's sync allocates
// nothing, and an idle app draws nothing.
func TestAccessibilityBridgeLive(t *testing.T) {
	wantOverlay(t, true)
	p := &probe{}
	fl := &frameLog{Builder: p}
	l := widgettest.StartLive(t, probeConfig(), fl)
	defer l.Close()
	l.Frames(3)
	var active bool
	l.Do(func(a *app.App) { active = a.Win.AXActive() })
	if active {
		t.Fatal("the bridge is active before anything asked")
	}

	var button, field objc.ID
	l.Native(func(w widgettest.Win) {
		button = axFind(w.Window, "AXButton", "Press", 6)
		field = axFind(w.Window, "AXTextField", "Name", 6)
		if button != 0 {
			objc.Retain(button)
		}
		if field != 0 {
			objc.Retain(field)
		}
	})
	l.Do(func(a *app.App) { active = a.Win.AXActive() })
	if !active || button == 0 || field == 0 {
		t.Fatalf("after asking the window: active %v, Press button %#x, Name field %#x", active, button, field)
	}
	defer l.Do(func(*app.App) { objc.Release(button); objc.Release(field) })

	l.Do(func(*app.App) { objc.SendBool(button, objc.Sel("accessibilityPerformPress")) })
	l.Until("the press reaches the button", 120, func() bool { return p.press == 1 })

	l.Click(ui.RoleTextField, "Name")
	l.Type("hi")
	l.Frames(2)
	var value string
	var loc, n uintptr
	l.Do(func(*app.App) {
		value = string(objc.AppendUTF8(nil, objc.Send(field, objc.Sel("accessibilityValue"))))
		f := objc.F.Reset().Arg(uintptr(field)).Arg(uintptr(selSelectedRange)).Call(objc.MsgSend)
		loc, n = f.R0(), f.R1()
	})
	if value != "hi" || loc != 2 || n != 0 {
		t.Errorf("the field's value %q, selection {%d, %d}; want \"hi\", {2, 0}", value, loc, n)
	}

	var allocs float64
	l.Do(func(a *app.App) {
		a.Win.SyncAX()
		allocs = testing.AllocsPerRun(50, a.Win.SyncAX)
	})
	if allocs != 0 {
		t.Errorf("a sync of an unchanged tree allocates %v times", allocs)
	}

	// Idle: with the bridge active and nothing happening, no frame is drawn
	// (the field's caret blinks, so the focus goes to the button first,
	// through the bridge).
	l.Do(func(*app.App) { objc.Send1(button, objc.Sel("setAccessibilityFocused:"), 1) })
	l.Frames(2)
	settled := false
	for i := 0; i < 12 && !settled; i++ { // the focus ring's move animates for a few frames
		b0 := l.Built()
		time.Sleep(250 * time.Millisecond)
		settled = l.Built() == b0
	}
	if !settled {
		t.Fatal("the app did not settle in 3 s")
	}
	before := fl.count()
	time.Sleep(500 * time.Millisecond)
	frames := fl.finish(l)
	report(t, "unexplained AX idle frames", attribute(frames, before, 0), 0)
	t.Logf("AX idle: %d frames", len(frames)-before)
}

var selSelectedRange = objc.Sel("accessibilitySelectedTextRange")
