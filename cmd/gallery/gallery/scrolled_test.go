package gallery

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"gonativeui/ui"
)

// A scrolled render of a scene is scrolled, whether the gallery opens on the
// scene or a script walks to it (the scene's countdown starts when it is shown,
// not when the window opens).
func TestScrolledScenesAreScrolled(t *testing.T) {
	dump := func(o ShotOptions) string {
		off, err := runScene(o)
		if err != nil {
			if os.Getenv("CI") == "" {
				t.Skipf("offscreen rendering: %v", err)
			}
			t.Fatal(err)
		}
		defer off.Close()
		var tr ui.SemTree
		off.UI.Semantics(&tr)
		var b strings.Builder // the roles, names and rectangles: Dump has no rectangles
		for i := range tr.Nodes {
			n := &tr.Nodes[i]
			fmt.Fprintf(&b, "%d %q %v\n", n.Role, tr.Text(n.Name), n.Bounds)
		}
		return b.String()
	}
	// every scene whose content is taller than the window
	for _, name := range []string{"main", "table", "catalogue", "catalogue-lists"} {
		if dump(ShotOptions{Scene: name}) == dump(ShotOptions{Scene: name, Scrolled: true}) {
			t.Errorf("%s: the scrolled render is the unscrolled one", name)
		}
	}
}
