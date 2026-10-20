package notes

import (
	"testing"
)

// TestAuditLayout runs the layout audit on the window in light and dark mode,
// with notes in the list and one selected.
func TestAuditLayout(t *testing.T) {
	for _, dark := range []bool{false, true} {
		r := newRig(t, dark)
		r.Frames(4)
		r.AuditLayout("notes")
	}
}
