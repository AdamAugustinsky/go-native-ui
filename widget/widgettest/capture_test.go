package widgettest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A fake shot.sh that writes a PNG where -out says and prints GnuiShot's
// JSON, to check what Capture asks for and how it reads the answer.
const fakeShot = `#!/bin/sh
echo "$@" > "$FAKE_ARGS"
case "$FAKE_MODE" in
perm) echo '{"error":"screen recording permission needed","exitCode":2}'; exit 2;;
nowin) echo '{"error":"no matching window","exitCode":4}'; exit 4;;
esac
while [ $# -gt 0 ]; do case $1 in -out) out=$2;; esac; shift; done
printf 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==' | base64 -d > "$out"
echo '{"exitCode":0}'
`

func TestCaptureRunsGnuiShot(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "shot.sh")
	os.WriteFile(script, []byte(fakeShot), 0o755)
	args := filepath.Join(dir, "args")
	t.Setenv("FAKE_ARGS", args)
	t.Setenv("GNUI_SHOTS_DIR", filepath.Join(dir, "shots"))

	path, res := captureWith(script, 4242, "TestX/sub", "main window")
	if res.ExitCode != 0 || res.Err != "" {
		t.Fatalf("result %+v", res)
	}
	if got, _ := os.ReadFile(args); !strings.Contains(string(got), "window -id 4242 -out ") {
		t.Errorf("GnuiShot was run with %q", got)
	}
	if filepath.Base(path) != "TestX_sub-main_window.png" {
		t.Errorf("picture at %s", path)
	}
	if st, err := os.Stat(path); err != nil || st.Size() == 0 {
		t.Errorf("no picture: %v", err)
	}
	t.Setenv("FAKE_MODE", "perm")
	if _, res := captureWith(script, 1, "T", "n"); res.ExitCode != 2 {
		t.Errorf("a missing grant came back as %+v", res)
	}
	t.Setenv("FAKE_MODE", "nowin")
	if _, res := captureWith(script, 1, "T", "n"); res.ExitCode != 4 || !strings.Contains(res.Err, "no matching") {
		t.Errorf("a missing window came back as %+v", res)
	}
	if _, res := captureWith(filepath.Join(dir, "missing.sh"), 1, "T", "n"); res.ExitCode == 0 {
		t.Errorf("a missing script came back as ok: %+v", res)
	}
}

// Without GNUI_GNUISHOT the capture is skipped, as the harness promises.
func TestCaptureSkipsWithoutTheEnvironment(t *testing.T) {
	t.Setenv("GNUI_GNUISHOT", "")
	if CaptureEnabled() {
		t.Fatal("enabled without GNUI_GNUISHOT")
	}
	// Capture itself would call t.Skip, which ends this goroutine: run it in a sub-test.
	skipped := false
	t.Run("capture", func(t *testing.T) {
		defer func() { skipped = t.Skipped() }()
		Capture(t, WindowID(1), "x")
		t.Error("Capture returned without GNUI_GNUISHOT")
	})
	if !skipped {
		t.Error("the sub-test was not skipped")
	}
}
