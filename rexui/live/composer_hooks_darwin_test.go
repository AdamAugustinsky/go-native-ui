//go:build darwin

package live

import (
	"testing"
	"unsafe"

	"gonativeui/app"
	"gonativeui/internal/objc"
	"gonativeui/platform"
	"gonativeui/rexui"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// The hosted composer's hooks for a chat app: the edge arrows, the
// placeholder and the Enter policy changed while it runs, a paste of files,
// and the accessory in the send button's place.
func TestLiveComposerHooks(t *testing.T) {
	l, p := startComposer(t, rexui.ComposerSetup{Placeholder: "Message"}, 480, 420)
	p.focus(t, l)

	// the edge arrows: Up in the empty composer edits the last message
	l.Press(ui.KeyUp, 0)
	l.Frames(2)
	if p.edgeUp != 0 {
		t.Fatal("Up reported an edge with the edge arrows off")
	}
	l.Do(func(*app.App) { p.st.SetEdgeArrows(true) })
	l.Press(ui.KeyUp, 0)
	l.Until("Up in the empty composer", 120, func() bool { return p.edgeUp == 1 })
	l.Type("12")
	l.Until("typed", 120, func() bool { return string(p.st.Text()) == "12" })
	l.Press(ui.KeyDown, 0)
	l.Until("Down on the last line", 120, func() bool { return p.edgeDown == 1 })

	// the placeholder and the Enter policy
	l.Do(func(*app.App) {
		p.st.SetPlaceholder("Edit message")
		p.st.SetEnter(platform.CmdEnterSends)
	})
	var ph string
	l.Do(func(*app.App) { ph = goString(objc.Send(cview(p), objc.Sel("accessibilityPlaceholderValue"))) })
	if ph != "Edit message" {
		t.Errorf("the placeholder is %q", ph)
	}
	l.Press(ui.KeyEnter, 0)
	l.Until("Enter adds a line", 120, func() bool { return string(p.st.Text()) == "12\n" })
	if len(p.sent) != 0 {
		t.Fatalf("Enter sent under CmdEnterSends: %q", p.sent)
	}

	// a paste of files: nothing inserted, the app told
	l.Do(func(*app.App) {
		pb := objc.Send(objc.Cls("NSPasteboard"), objc.Sel("pasteboardWithUniqueName"))
		types := objc.Send1(objc.Cls("NSArray"), objc.Sel("arrayWithObject:"), uintptr(objc.Str("public.file-url")))
		objc.Send2(pb, objc.Sel("declareTypes:owner:"), uintptr(types), 0)
		b := []byte("file:///tmp/a.pdf")
		cls, sel := objc.Cls("NSData"), objc.Sel("dataWithBytes:length:") // before the call is assembled: they use its frame
		d := objc.ID(objc.F.Reset().Arg(uintptr(cls)).Arg(uintptr(sel)).ArgPtr(unsafe.Pointer(&b[0])).Arg(uintptr(len(b))).Call(objc.MsgSend).R0())
		objc.Send2(pb, objc.Sel("setData:forType:"), uintptr(d), uintptr(objc.Str("public.file-url")))
		objc.SendBool1(cview(p), objc.Sel("readSelectionFromPasteboard:"), uintptr(pb))
		objc.Send(pb, objc.Sel("releaseGlobally"))
	})
	l.Until("PastedFiles", 120, func() bool { return p.files == 1 })
	if got := string(p.st.Text()); got != "12\n" {
		t.Errorf("a paste of files inserted text: %q", got)
	}

	// the accessory: the microphone of an empty composer
	recorded := 0
	mic := func(k *base.Kit) {
		if rexui.Button(k, "Record", rexui.ButtonOpts{}).Clicked {
			recorded++
		}
	}
	l.Do(func(*app.App) {
		p.st.SetText(nil)
		p.o.Accessory = mic
	})
	l.Frames(3)
	if _, ok := l.Find(ui.RoleButton, "Record"); !ok {
		t.Fatalf("no accessory:\n%s", l.Dump())
	}
	if _, ok := l.Find(ui.RoleButton, "Send"); ok {
		t.Error("the send button shows beside the accessory")
	}
	l.Click(ui.RoleButton, "Record")
	l.Until("the accessory's press is the app's", 120, func() bool { return recorded == 1 })
	l.Do(func(*app.App) { p.o.Accessory = nil })
	l.Frames(3)
	if _, ok := l.Find(ui.RoleButton, "Send"); !ok {
		t.Error("the send button did not come back")
	}
}

func goString(s objc.ID) string {
	if s == 0 {
		return ""
	}
	return string(objc.AppendUTF8(nil, s))
}
