package demo

import (
	"runtime"
	"testing"

	"gonativeui/ui"
)

// The Finder's rows show the system's icon of each file's type, as the
// scene's iconForContentType: does, and the inspector its presentation icon:
// with the window's renderer every row in view has its image, none the
// stand-in symbols. Where the system has no icons for these types (an icon
// theme without them) the stand-ins stay.
func TestFinderSystemIcons(t *testing.T) {
	off, d, _, err := render(ShotOptions{Scene: Finder})
	if err != nil {
		t.Fatal(err)
	}
	defer off.Close()
	defer d.Close()
	var tr ui.SemTree
	d.c.Semantics(&tr)
	_, wh := d.c.Size()
	rows, withIcon, images, outside := 0, 0, 0, 0
	inRow := func(i int32) int32 {
		for p := tr.Nodes[i].Parent; p >= 0; p = tr.Nodes[p].Parent {
			if r := tr.Nodes[p].Role; r == ui.RoleRow || r == ui.RoleListItem {
				return p
			}
		}
		return -1
	}
	has := map[int32]bool{}
	for i := range tr.Nodes {
		if tr.Nodes[i].Role == ui.RoleImage {
			images++
			if r := inRow(int32(i)); r >= 0 {
				has[r] = true
			} else {
				outside++
			}
		}
	}
	for i := range tr.Nodes {
		n := &tr.Nodes[i]
		if n.Role != ui.RoleRow || n.Bounds[1] >= float32(wh) || n.Bounds[3] <= 0 {
			continue
		}
		rows++
		if has[int32(i)] {
			withIcon++
		}
	}
	if images == 0 && runtime.GOOS != "darwin" {
		t.Skip("no system icons for the scene's types here")
	}
	if rows == 0 || withIcon != rows {
		t.Errorf("%d of %d rows in view show a system icon, want all", withIcon, rows)
	}
	if outside != 1 {
		t.Errorf("%d images outside the rows, want the inspector's", outside)
	}
}
