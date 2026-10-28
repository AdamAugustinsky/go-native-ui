package rexui_test

import (
	"testing"

	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
)

// TestTextAreaFallback is C4's wave-0 contract: without a hosted editor the
// text area is the drawn field, editable unless read-only.
func TestTextAreaFallback(t *testing.T) {
	st, err := rexui.NewTextArea(nil, rexui.TextAreaSetup{Placeholder: "Instructions"})
	if err != nil || st.Hosted() {
		t.Fatalf("NewTextArea(nil) = %v, hosted %v", err, st.Hosted())
	}
	if lo, hi := st.Lines(); lo != 3 || hi != 10 {
		t.Fatalf("default lines %d..%d", lo, hi)
	}
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		g.k.NextWidth(g.k.Fixed(300))
		rexui.TextArea(g.k, "Bot instructions", st, rexui.TextAreaOpts{})
	}))
	g.click(centre(g.node(ui.RoleTextField, "Bot instructions")))
	g.typeText("Be brief")
	g.frame()
	if string(st.Text()) != "Be brief" {
		t.Fatalf("typing gave %q", st.Text())
	}
	st.SetReadOnly(true)
	g.frame()
	g.typeText("!")
	g.frame()
	if string(st.Text()) != "Be brief" {
		t.Fatalf("a read-only area took %q", st.Text())
	}
}
