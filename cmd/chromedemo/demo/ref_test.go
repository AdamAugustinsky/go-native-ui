package demo

import (
	"os"
	"path/filepath"
	"testing"

	"gonativeui/theme"
)

// The toolbar's capsules pair with AppKit's glass platters by place: the
// parts of a group are not capsules, and a capsule with no platter left is
// unpaired.
func TestCompareCapsules(t *testing.T) {
	dump := `{"tree": {"class": "NSView", "subviews": [
		{"class": "NSToolbarPlatterView", "frame": {"x": 646, "y": 8, "w": 47.5, "h": 36}},
		{"class": "NSView", "subviews": [{"class": "NSToolbarPlatterView", "frame": {"x": 96, "y": 8, "w": 36, "h": 36}}]},
		{"class": "NSToolbarButton", "frame": {"x": 646, "y": 8, "w": 47.5, "h": 36}}]}}`
	path := filepath.Join(t.TempDir(), "finder-light-active-top.json")
	if err := os.WriteFile(path, []byte(dump), 0o644); err != nil {
		t.Fatal(err)
	}
	nodes := []Node{
		{Role: "Button", Name: "Sidebar", Frame: [4]float32{96, 8, 36, 36}},
		{Role: "Group", Name: "share-tag", Frame: [4]float32{702, 8, 84, 36}},
		{Role: "Button", Name: "Share", Frame: [4]float32{702, 8, 36.5, 36}},
		{Role: "Button", Name: "Group", Frame: [4]float32{646, 8, 48, 36}},
		{Role: "Label", Name: "Documents", Frame: [4]float32{284, 16.5, 88, 19}},
	}
	caps, err := CompareCapsules(nodes, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(caps) != 3 || caps[0].Name != "Sidebar" || caps[1].Name != "Group" || caps[2].Name != "share-tag" {
		t.Fatalf("capsules %+v, want Sidebar, Group, share-tag", caps)
	}
	if c := caps[1]; c.AppKitX != 646 || c.DW != 0.5 || c.DX != 0 {
		t.Errorf("Group beside AppKit's: %+v", c)
	}
	if !caps[2].AppKitUnpaired {
		t.Errorf("share-tag paired with %+v, want none left", caps[2])
	}
	o := ShotOptions{Scene: Settings, Preference: theme.ForceDark, Inactive: true, Scrolled: true}
	if got := filepath.Base(RefCapture("/x", o)); got != "settings-dark-inactive-scrolled.json" {
		t.Errorf("RefCapture: %s", got)
	}
}
