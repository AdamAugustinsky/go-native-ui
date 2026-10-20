package demo

import (
	"testing"

	"gonativeui/ui"
)

// With legacy scrollers the settings form's boxes end their margin (20 pt)
// before the bar, as AppKit's do beside it, rather than running under it.
func TestSettingsFormBesideLegacyScroller(t *testing.T) {
	r, _ := newRigOpts(t, Options{Scene: Settings, Still: true, Legacy: true})
	r.Frames(30)
	area, ok := r.Find(ui.RoleScrollArea, "Settings")
	if !ok {
		t.Fatal("no Settings scroll area")
	}
	end := area.Bounds[2]/2 - 17 - formPad
	tree := r.Tree()
	n := 0
	for _, nd := range tree.Nodes {
		if nd.Role != ui.RoleGroup || nd.Bounds[0] < area.Bounds[0] {
			continue
		}
		n++
		if right := nd.Bounds[2] / 2; right > end+0.5 {
			t.Errorf("a box ends at %v, past %v (the legacy bar's room and the margin)", right, end)
		}
	}
	if n == 0 {
		t.Fatal("no boxes in the form")
	}
}
