package script_test

import (
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// An application links this package to play a script on its live window (a
// demo tour in a release build), so it must not pull the test kit's
// dependencies into the binary.
func TestNoTestMachineryLinked(t *testing.T) {
	banned := map[string]bool{"testing": true, "regexp": true, "flag": true, "os/exec": true}
	for _, goos := range []string{"darwin", "linux", "windows"} {
		cmd := exec.Command("go", "list", "-deps", "gonativeui/widget/script")
		cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH=arm64", "CGO_ENABLED=0")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("go list for %s: %v", goos, err)
		}
		pkgs := strings.Fields(string(out))
		if !slices.Contains(pkgs, "gonativeui/ui") {
			t.Fatalf("%s: go list -deps lists %d packages without gonativeui/ui", goos, len(pkgs))
		}
		for _, pkg := range pkgs {
			if banned[pkg] {
				t.Errorf("%s: widget/script depends on %s", goos, pkg)
			}
		}
	}
}
