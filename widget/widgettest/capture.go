package widgettest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Captures of the real window, with the OS's own drawing around it (title
// bar, traffic lights, shadow, native controls), by GnuiShot
// (tools/gnuishot), which needs the Screen Recording grant and an awake,
// unlocked display: a test run on a Mac at someone's keyboard, or over SSH on
// the test Mac. It is off unless GNUI_GNUISHOT names the script that runs it:
//
//	GNUI_GNUISHOT=~/gnui-test/shot.sh go test ./widget/widgettest/live -run Capture
//
// (GNUI_GNUISHOT=1 means ~/gnui-test/shot.sh; APP in the environment names
// another GnuiShot.app, as shot.sh reads it). The pictures go to
// $GNUI_SHOTS_DIR, or the temporary directory's gnui-shots, and the test logs
// the path of each, for an agent to open.

// Windowed is anything that knows the window server's number of a window: a
// LiveApp, or a WindowID.
type Windowed interface {
	// WindowNumber is the window's number, which GnuiShot's -id takes.
	WindowNumber() int
}

// WindowID is a window server window number as a Windowed.
type WindowID int

// WindowNumber is the number.
func (w WindowID) WindowNumber() int { return int(w) }

// CaptureEnabled reports whether GNUI_GNUISHOT asks for captures.
func CaptureEnabled() bool { return os.Getenv("GNUI_GNUISHOT") != "" }

// Capture takes a picture of the window with GnuiShot and returns the path of
// the PNG. The test is skipped when GNUI_GNUISHOT is not set, and when
// GnuiShot reports that it has no Screen Recording grant (its own exit code
// 2: independent evidence that this machine cannot capture); any other
// failure fails the test.
func Capture(t testing.TB, win Windowed, name string) string {
	t.Helper()
	script := os.Getenv("GNUI_GNUISHOT")
	if script == "" {
		t.Skip("captures are off: GNUI_GNUISHOT does not name GnuiShot's shot.sh")
	}
	path, res := captureWith(script, win.WindowNumber(), t.Name(), name)
	switch {
	case res.ExitCode == 0 && res.Err == "":
		t.Logf("capture %s: %s", name, path)
		return path
	case res.ExitCode == 2:
		t.Skipf("GnuiShot has no Screen Recording permission: %s", res.Err)
	}
	t.Fatalf("GnuiShot failed for %s (exit %d): %s %s", name, res.ExitCode, res.Err, res.Detail)
	return ""
}

// CaptureImage is Capture that also decodes the picture, for AssertImage.
func CaptureImage(t testing.TB, win Windowed, name string) image.Image {
	t.Helper()
	path := Capture(t, win, name)
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatalf("widgettest: %s: %v", path, err)
	}
	return img
}

// shotResult is the part of GnuiShot's JSON a test reads.
type shotResult struct {
	ExitCode int    `json:"exitCode"`
	Err      string `json:"error"`
	Detail   string `json:"detail"`
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// captureWith runs the script for the window and returns where the picture
// went and what GnuiShot said.
func captureWith(script string, window int, test, name string) (string, shotResult) {
	if script == "1" {
		script = filepath.Join(os.Getenv("HOME"), "gnui-test", "shot.sh")
	}
	if strings.HasPrefix(script, "~/") {
		script = filepath.Join(os.Getenv("HOME"), script[2:])
	}
	dir := os.Getenv("GNUI_SHOTS_DIR")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "gnui-shots")
	}
	os.MkdirAll(dir, 0o755)
	out := filepath.Join(dir, unsafeName.ReplaceAllString(test+"-"+name, "_")+".png")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", script, "window", "-id", fmt.Sprint(window), "-out", out, "-wait-ms", "150")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	err := cmd.Run()
	var res shotResult
	if json.Unmarshal(stdout.Bytes(), &res) != nil {
		res = shotResult{ExitCode: 1, Err: "no JSON from " + script, Detail: strings.TrimSpace(stdout.String())}
	}
	if err != nil && res.ExitCode == 0 {
		res.ExitCode, res.Err = 1, err.Error()
	}
	return out, res
}
