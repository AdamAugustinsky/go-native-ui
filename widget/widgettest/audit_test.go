package widgettest

import (
	"testing"

	"gonativeui/ui"
)

// An allowance with Within accepts only findings whose rectangle is centred
// in it.
func TestAllowWithin(t *testing.T) {
	a := Allow{Check: ui.AuditOverflow, Within: [4]float32{100, 100, 200, 200}, Reason: "sample"}
	in := ui.Finding{Check: ui.AuditOverflow, Rect: [4]float32{110, 120, 250, 140}} // centre (180, 130)
	out := ui.Finding{Check: ui.AuditOverflow, Rect: [4]float32{10, 120, 50, 140}}
	other := ui.Finding{Check: ui.AuditCovered, Rect: in.Rect}
	if !a.matches(in) || a.matches(out) || a.matches(other) {
		t.Errorf("within: %v %v %v, want true false false", a.matches(in), a.matches(out), a.matches(other))
	}
	if all := (Allow{Check: ui.AuditOverflow}); !all.matches(out) {
		t.Error("an allowance without Within matches anywhere")
	}
}
