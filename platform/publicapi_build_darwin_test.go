//go:build darwin

package platform

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The private AppKit selector names the framework calls (lights). A build
// with the gnui_publicapi tag must not contain them (DECISIONS.md D53).
var privateSelectorNames = []string{
	"setTitlebarHeight:",
	"titlebarHeight",
	"setStandardWindowButtonGroupOffset:",
	"standardWindowButtonGroupOffset",
}

// rexlook built with gnui_publicapi contains none of the private selector
// names; built without it, it contains them all (so the scan sees them where
// they are).
func TestPublicAPIBuildHasNoPrivateSelectors(t *testing.T) {
	if testing.Short() {
		t.Skip("builds rexlook twice")
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go tool on PATH")
	}
	dir := t.TempDir()
	build := func(name string, tags ...string) []byte {
		out := filepath.Join(dir, name)
		args := append([]string{"build", "-o", out}, tags...)
		cmd := exec.Command(goTool, append(args, "gonativeui/cmd/rexlook")...)
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=darwin")
		if msg, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %v: %v\n%s", cmd.Args[1:], err, msg)
		}
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	public, private := build("rexlook-public", "-tags", "gnui_publicapi"), build("rexlook")
	for _, name := range privateSelectorNames {
		if bytes.Contains(public, []byte(name)) {
			t.Errorf("the gnui_publicapi build of rexlook contains the private selector %q", name)
		}
		if !bytes.Contains(private, []byte(name)) {
			t.Errorf("the ordinary build of rexlook does not contain %q: the scan cannot see it", name)
		}
	}
}
