package demo

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gonativeui/theme"
)

// Capsule is one toolbar capsule of the scene beside AppKit's in the same
// place of the reference capture, in window points.
type Capsule struct {
	Name           string
	X, W           float32
	AppKitX        float32 `json:"appkitX"`
	AppKitW        float32 `json:"appkitW"`
	DX, DW         float32
	AppKitUnpaired bool `json:"appkitUnpaired,omitempty"`
}

// RefCapture is the appkitscenes dump of the scene in o's state in dir (a
// reference set's scenes, internal/appkitref.Scenes): the scene, light or
// dark, active or inactive, at the top or scrolled.
func RefCapture(dir string, o ShotOptions) string {
	mode := "light"
	if o.Preference == theme.ForceDark {
		mode = "dark"
	}
	state, pos := "active", "top"
	if o.Inactive {
		state = "inactive"
	}
	if o.Scrolled {
		pos = "scrolled"
	}
	return filepath.Join(dir, fmt.Sprintf("%s-%s-%s-%s.json", SceneNames[o.Scene], mode, state, pos))
}

// CompareCapsules pairs the toolbar's capsules in nodes (Measure) with the
// glass platters of AppKit's toolbar in the dump at path, left to right.
func CompareCapsules(nodes []Node, path string) ([]Capsule, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var dump struct {
		Tree json.RawMessage `json:"tree"`
	}
	if err := json.Unmarshal(b, &dump); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var tree any
	if err := json.Unmarshal(dump.Tree, &tree); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var ak [][2]float32
	var walk func(v any)
	walk = func(v any) {
		switch n := v.(type) {
		case map[string]any:
			if n["class"] == "NSToolbarPlatterView" {
				if f, ok := n["frame"].(map[string]any); ok {
					x, _ := f["x"].(float64)
					w, _ := f["w"].(float64)
					ak = append(ak, [2]float32{float32(x), float32(w)})
				}
			}
			for _, c := range n {
				walk(c)
			}
		case []any:
			for _, c := range n {
				walk(c)
			}
		}
	}
	walk(tree)
	sort.Slice(ak, func(i, j int) bool { return ak[i][0] < ak[j][0] })

	ours := toolbarCapsules(nodes)
	out := make([]Capsule, 0, len(ours))
	used := make([]bool, len(ak))
	for _, n := range ours {
		c := Capsule{Name: n.Name, X: n.Frame[0], W: n.Frame[2]}
		best := -1
		for i, a := range ak { // the nearest platter not taken, by its centre
			if used[i] {
				continue
			}
			if best < 0 || abs32(a[0]+a[1]/2-(c.X+c.W/2)) < abs32(ak[best][0]+ak[best][1]/2-(c.X+c.W/2)) {
				best = i
			}
		}
		if best >= 0 {
			used[best] = true
			c.AppKitX, c.AppKitW = ak[best][0], ak[best][1]
			c.DX, c.DW = c.X-c.AppKitX, c.W-c.AppKitW
		} else {
			c.AppKitUnpaired = true
		}
		out = append(out, c)
	}
	return out, nil
}

// toolbarCapsules are the nodes that are capsules of the toolbar's band:
// items and groups at its capsules' top, not the parts of a group.
func toolbarCapsules(nodes []Node) []Node {
	var caps []Node
	for _, n := range nodes {
		f := n.Frame
		if f[1] < 7.5 || f[1] > 8.5 || (f[3] != 24 && f[3] != 36) {
			continue
		}
		switch n.Role {
		case "Button", "ComboBox", "Group", "SearchField", "TextField", "RadioGroup":
			caps = append(caps, n)
		}
	}
	var out []Node
	for i, n := range caps {
		inside := false
		for j, g := range caps {
			if j != i && g.Role == "Group" && g.Frame[2] > n.Frame[2] && n.Frame[0] >= g.Frame[0] && n.Frame[0]+n.Frame[2] <= g.Frame[0]+g.Frame[2] {
				inside = true
			}
		}
		if !inside {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Frame[0] < out[j].Frame[0] })
	return out
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
