//go:build darwin

package demo

import (
	"math"
	"strings"
	"syscall"
	"testing"
)

// The Notes toolbar's items are AppKit's to the half point (the Mac mini's
// Notes scene, captures-macmini 26B5101f, its tree's NSToolbarButton and
// platter frames): a group's middle part has no end room, so Checklist is
// 31 between Format's 35.5 and Table's 36.5, and New Note ends 3.5 before
// the flexible space. Capsules are checked to the half point; a group's
// parts by width (each within half a point, their places add up to a point).
func TestNotesToolbarMatchesAppKitTree(t *testing.T) {
	if v, _ := syscall.Sysctl("kern.osproductversion"); !strings.HasPrefix(v, "27.") {
		t.Skipf("macOS %s: the symbol widths are measured on 27", v)
	}
	nodes, err := Measure(ShotOptions{Scene: Notes})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []struct {
		name string
		x, w float32
		part bool
	}{
		{"Sidebar", 91.5, 31.5, false}, {"Delete", 137.5, 27, false}, {"New Note", 477, 28.5, false},
		{"format", 517, 119, false}, {"Format", 517, 35.5, true}, {"Checklist", 560.5, 31, true}, {"Table", 599.5, 36.5, true},
		{"insert-share", 644.5, 70.5, false}, {"Media", 644.5, 36.5, true}, {"Share", 683, 32, true},
		{"Search", 725, 167, false},
	} {
		found := false
		for _, n := range nodes {
			if n.Name != w.name || n.Frame[1] != 8 {
				continue
			}
			found = true
			dx := math.Abs(float64(n.Frame[0] - w.x))
			if w.part {
				dx = max(dx-0.5, 0)
			}
			if dx > 0.5 || math.Abs(float64(n.Frame[2]-w.w)) > 0.5 {
				t.Errorf("%s: x %v, %v wide; AppKit's %v, %v", w.name, n.Frame[0], n.Frame[2], w.x, w.w)
			}
		}
		if !found {
			t.Errorf("no %s in the toolbar", w.name)
		}
	}
}
