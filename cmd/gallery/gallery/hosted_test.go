package gallery

import (
	"fmt"
	"strings"
	"testing"

	"gonativeui/theme"
	"gonativeui/ui"
)

// controlNodes are the controls and labels of a semantic tree, by role and
// name, with their frames in pixels.
func controlNodes(t *ui.SemTree) map[string][][4]float32 {
	out := map[string][][4]float32{}
	for i := range t.Nodes {
		n := &t.Nodes[i]
		switch n.Role {
		case ui.RoleButton, ui.RoleComboBox, ui.RoleCheckbox, ui.RoleSwitch, ui.RoleSlider, ui.RoleTextField,
			ui.RoleRadio, ui.RoleSearchField, ui.RoleLabel, ui.RoleTab:
			k := fmt.Sprintf("%d %s", n.Role, t.Text(n.Name))
			out[k] = append(out[k], n.Bounds)
		}
	}
	return out
}

// A catalogue page hosted in the window lays out exactly as its standalone
// scene does: every control of the standalone page is in the hosted one, at the
// same distance from its neighbours (the hosted page is only moved).
func TestHostedCataloguePagesKeepTheirGeometry(t *testing.T) {
	const scale = 2
	for p, name := range AppKitPages {
		pg := NewAppKit(p, theme.ForceLight)
		alone, err := runAppKit(p, theme.ForceLight, scale, nil)
		if err != nil {
			t.Skipf("offscreen rendering: %v", err)
		}
		var at ui.SemTree
		alone.UI.Semantics(&at)
		want := controlNodes(&at)
		alone.Close()

		scene := "catalogue" + strings.TrimPrefix(name, "appkit")
		// a window as large as the page and the sidebar and toolbar round it
		host, err := runScene(ShotOptions{Scene: scene, Width: int(pg.w+400) * scale, Height: int(pg.h+200) * scale, Scale: scale})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var ht ui.SemTree
		host.UI.Semantics(&ht)
		got := controlNodes(&ht)
		host.Close()

		if len(want) < 5 {
			t.Fatalf("%s: only %d kinds of control found in the standalone page", name, len(want))
		}
		// the offset of the first control of the page, then every other
		var dx, dy float32
		found := false
		for k, ws := range want {
			gs := got[k]
			if len(ws) == 1 && len(gs) == 1 {
				dx, dy, found = gs[0][0]-ws[0][0], gs[0][1]-ws[0][1], true
				break
			}
		}
		if !found {
			t.Fatalf("%s: no control in common", name)
		}
		missing, moved := 0, 0
		for k, ws := range want {
			gs := got[k]
			for _, w := range ws {
				ok := false
				for _, g := range gs {
					if abs32(g[0]-w[0]-dx) < 0.5 && abs32(g[1]-w[1]-dy) < 0.5 && abs32(g[2]-g[0]-(w[2]-w[0])) < 0.5 && abs32(g[3]-g[1]-(w[3]-w[1])) < 0.5 {
						ok = true
					}
				}
				if !ok {
					if len(gs) == 0 {
						missing++
					} else {
						moved++
					}
					if missing+moved <= 5 {
						t.Errorf("%s: %q standalone %v, hosted %v (offset %g,%g)", name, k, w, gs, dx, dy)
					}
				}
			}
		}
		if missing+moved > 0 {
			t.Errorf("%s: %d controls missing and %d moved of %d", name, missing, moved, len(want))
		}
	}
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// No scene builds two stateful boxes under one key: a key shared in a frame is
// state shared by accident (issue #9 was two byte labels in one parent). The
// layout spacers (base.Kit.Space and Spacer, which the form uses) share a key
// by design: they have no state. The catalogue pages are rexui/catalog's
// samples, not the gallery's: they are not checked here.
func TestNoSceneHasDuplicateKeys(t *testing.T) {
	r := newRig(t, Options{Still: true})
	c := r.Context()
	for sc := range scCatalogue {
		r.goTo(sc)
		c.EnableDebug(false, false)
		c.EnableDebug(true, false)
		r.Frames(4)
		for _, d := range c.DebugDups() {
			if d.Key == ui.HashString(d.Parent, "space") || d.Key == ui.HashString(d.Parent, "spacer") {
				continue
			}
			var b ui.DebugBox
			c.DebugBoxByKey(d.Key, &b)
			t.Errorf("%s: %d boxes with one key under one parent: %+v, the first at %v %q", defs[sc].title, d.Count+1, d, b.Rect, b.Name)
		}
	}
}
