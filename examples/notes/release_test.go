package main

import (
	"os/exec"
	"strings"
	"testing"
)

// Test the compiled dependency graph, not source layout: copying an example
// must not make its diagnostic CLI a release dependency.
func TestReleaseDropsDiagnostics(t *testing.T) {
	for _, example := range []string{"notes", "images", "richtext"} {
		t.Run(example, func(t *testing.T) {
			for _, tags := range []string{"", "gnui_release"} {
				out, err := exec.Command("go", "list", "-deps", "-tags", tags, "gonativeui/examples/"+example).CombinedOutput()
				if err != nil {
					t.Fatalf("go list: %v\n%s", err, out)
				}
				deps := "\n" + string(out)
				for _, pkg := range []string{"flag", "encoding/json"} {
					have := strings.Contains(deps, "\n"+pkg+"\n")
					if have != (tags == "") {
						t.Errorf("tags %q: dependency %s present = %v, want %v", tags, pkg, have, tags == "")
					}
				}
			}
		})
	}
}
