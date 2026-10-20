package widgettest

import (
	"strings"
	"testing"

	"gonativeui/ui"
)

// Allow is a finding of the layout audit that a test accepts, with the reason.
// It matches a finding of its Check whose Where contains Where (and, when it
// is set, whose OtherWhere contains Other).
type Allow struct {
	// Check is the check of the findings it accepts.
	Check ui.AuditCheck
	// Where is text that the finding's Where contains.
	Where string
	// Other, when set, is text that the finding's OtherWhere contains.
	Other string
	// Reason says why the finding is right as it is.
	Reason string
}

func (a Allow) matches(f ui.Finding) bool {
	return a.Check == f.Check && strings.Contains(f.Where, a.Where) &&
		(a.Other == "" || strings.Contains(f.OtherWhere, a.Other))
}

// AuditLayout runs the layout audit (ui.Context.Audit) on the last frame of c
// and fails t, naming the scene, for each finding that no allow accepts. It
// returns the findings, accepted ones included. An Allow without a Reason is
// a mistake in the test and fails it too.
func AuditLayout(t testing.TB, scene string, c *ui.Context, allow ...Allow) []ui.Finding {
	t.Helper()
	for _, a := range allow {
		if a.Reason == "" {
			t.Errorf("%s: audit allowance %s %q has no reason", scene, a.Check, a.Where)
		}
	}
	fs := c.Audit(nil)
next:
	for _, f := range fs {
		for _, a := range allow {
			if a.matches(f) {
				continue next
			}
		}
		t.Errorf("%s: layout audit: %s", scene, f)
	}
	return fs
}

// AuditLayout runs the layout audit on the last frame of the rig (see the
// function AuditLayout).
func (r *Rig) AuditLayout(scene string, allow ...Allow) []ui.Finding {
	r.t.Helper()
	return AuditLayout(r.t, scene, r.c, allow...)
}
