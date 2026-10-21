package rexui_test

import (
	"testing"

	"gonativeui/rexui"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// ComposerSignal.Send is the send button's box, whose key the app can put
// in its Tab order: it is the Send node's, and the button focused by it
// sends with Space. With an Accessory in its place it is 0.
func TestComposerSendID(t *testing.T) {
	cr := newComposerRig(t, 0, rexui.ComposerSetup{Placeholder: "Message"})
	cr.focus()
	cr.typeText("hi")
	cr.settle()
	if cr.sig.Send == 0 {
		t.Fatal("no send button box in the signal")
	}
	key := cr.c.KeyOf(cr.sig.Send)
	if n := cr.node(ui.RoleButton, "Send"); n.Key != key {
		t.Fatalf("the signal's send box has key %x, the Send button %x", key, n.Key)
	}
	cr.c.SetFocus(key)
	cr.c.SetFocusVisible(true)
	cr.frame()
	cr.key(ui.KeySpace, 0)
	cr.settle()
	if cr.submits != 1 {
		t.Fatalf("Space on the send button focused by the signal's box reported %d sends", cr.submits)
	}
	cr.o.Accessory = func(*base.Kit) {}
	cr.settle()
	if cr.sig.Send != 0 {
		t.Errorf("with an Accessory the signal names a send button (%d)", cr.sig.Send)
	}
}
