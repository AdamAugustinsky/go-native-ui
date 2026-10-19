package platform

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// privateSelectors are the private AppKit selectors of the native shell
// (material_private_darwin.go), which a gnui_publicapi build must not contain.
var privateSelectors = []string{"set_variant:", "set_adaptiveAppearance:", "_variant", "_adaptiveAppearance", "_cornerRadius"}

// A Mac App Store build (the gnui_publicapi tag) of an app with the native
// shell has none of the shell's private selectors in its binary; the default
// build has them (so the scan sees them where they are).
func TestPublicAPIBuildHasNoPrivateSelectors(t *testing.T) {
	if testing.Short() {
		t.Skip("builds an app twice")
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go tool here")
	}
	dir := t.TempDir()
	build := func(name string, tags ...string) []byte {
		out := filepath.Join(dir, name)
		args := append([]string{"build", "-o", out}, tags...)
		cmd := exec.Command(goTool, append(args, "gonativeui/cmd/rexlook")...)
		cmd.Dir = ".."
		cmd.Env = append(os.Environ(), "GOOS=darwin", "GOARCH=arm64", "CGO_ENABLED=0")
		if msg, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go build %v: %v\n%s", tags, err, msg)
		}
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	public := build("public", "-tags", "gnui_publicapi")
	for _, s := range privateSelectors {
		if bytes.Contains(public, []byte(s)) {
			t.Errorf("the gnui_publicapi build contains %q", s)
		}
	}
	private := build("default")
	for _, s := range privateSelectors {
		if !bytes.Contains(private, []byte(s)) {
			t.Errorf("the default build lacks %q: the scan cannot see it", s)
		}
	}
}
