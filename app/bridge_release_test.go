//go:build gnui_release

package app

import (
	"os"
	"path/filepath"
	"testing"
)

// A release build has no agent bridge: GNUI_INSPECT is not read, no socket is
// made and no listener or goroutine is started. (cmd/gnui's release test checks
// that the code is not even linked into a packaged binary.)
func TestReleaseBuildHasNoAgentBridge(t *testing.T) {
	d, err := os.MkdirTemp("", "gi")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(d)
	sock := filepath.Join(d, "s")
	t.Setenv("GNUI_INSPECT", sock)
	o := newOverlayRig(t, &scene{}, 100, 100, 1)
	stop := o.App.inspectStart()
	defer stop()
	if _, err := os.Lstat(sock); err == nil {
		t.Fatal("a release build made the bridge's socket")
	}
	if o.App.inspDrain != nil {
		t.Error("a release build hooked the bridge into the safe point")
	}
}
