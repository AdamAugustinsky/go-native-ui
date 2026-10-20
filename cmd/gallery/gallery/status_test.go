package gallery

import (
	"strings"
	"testing"

	"gonativeui/ui"
)

// The Table's status line shows whole: a size left pending by the file icons
// of the rows must not reach its label, which then fades out after one
// character.
func TestTableStatusLineIsAsWideAsItsText(t *testing.T) {
	r := newRig(t, Options{Still: true})
	r.goTo(scTable)
	tr := r.Tree()
	var status, rows int32
	for _, n := range tr.Nodes {
		if n.Role != ui.RoleLabel {
			continue
		}
		w := n.Bounds[2] - n.Bounds[0]
		switch name := string(tr.Text(n.Name)); {
		case strings.Contains(name, " selected"):
			status = int32(w)
		case strings.HasSuffix(name, " rows"):
			rows = int32(w)
		}
	}
	if status == 0 || rows == 0 {
		t.Fatalf("labels not found: status %d, rows %d", status, rows)
	}
	if status < rows { // "0 selected · Ready" is longer than "1,000,000 rows"
		t.Errorf("the status label is %d px wide, the shorter row count's %d", status, rows)
	}
}
