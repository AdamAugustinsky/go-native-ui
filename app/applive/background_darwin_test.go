//go:build darwin

package applive

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/ui"
)

// A window opened while the app is in the background (open -g) is not the
// key window: its first frame and those after it are drawn inactive, as
// AppKit has it. When the app is activated later, the window becomes key and
// the app draws it active. With MODE=fg (open, in the foreground) the state
// is AppKit's too.
func TestBackgroundLaunchDrawsInactive(t *testing.T) {
	for _, mode := range []string{"bg", "fg"} {
		t.Run(mode, func(t *testing.T) {
			r := launchBundle(t, "bgkey", mode == "bg", "MODE="+mode)
			if r.get("key") != r.get("focused") || r.get("first") != r.get("key") {
				t.Fatalf("the app's focus is not AppKit's key state: %v\n%s", r.kv, r.out)
			}
			if mode == "bg" {
				if r.get("key") != "false" || r.get("active") != "false" {
					t.Skipf("the app was active although opened with open -g: %v", r.kv)
				}
				if r.get("after_key") != "true" || r.get("after_focused") != "true" {
					t.Fatalf("after the activation: %v\n%s", r.kv, r.out)
				}
			}
			t.Logf("%v", r.kv)
		})
	}
}

// launchBundle runs scenario name through LaunchServices, as a Finder or
// open(1) launch is: the test binary in a temporary app bundle, opened with
// open -n (-g when background). For a background launch it activates the
// app once the scenario reported "first" (open on the bundle again, which
// activates the running instance, as a click on its Dock icon would). It waits for the app to quit.
func launchBundle(t *testing.T, name string, background bool, env ...string) result {
	t.Helper()
	dir := t.TempDir()
	id := fmt.Sprintf("dev.gnui.applive.%s%d", name, os.Getpid())
	bundle := filepath.Join(dir, "GnuiLive.app")
	macos := filepath.Join(bundle, "Contents", "MacOS")
	if err := os.MkdirAll(macos, 0o755); err != nil {
		t.Fatal(err)
	}
	bin, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(macos, "applive"), bin, 0o755); err != nil {
		t.Fatal(err)
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>applive</string>
<key>CFBundleIdentifier</key><string>` + id + `</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleName</key><string>GnuiLive</string>
</dict></plist>`
	if err := os.WriteFile(filepath.Join(bundle, "Contents", "Info.plist"), []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.txt")
	args := []string{"-n", "-W", "--stdout", out, "--stderr", out, "--env", "GNUI_APPLIVE=" + name, "--env", "GNUI_NOANIM=1"}
	if background {
		args = append(args, "-g")
	}
	for _, e := range env {
		args = append(args, "--env", e)
	}
	args = append(args, bundle, "--args", "-test.run=^$")
	cmd := exec.Command("open", args...)
	t.Cleanup(func() { exec.Command("pkill", "-9", "-f", bundle).Run() }) // a failed test quits what it opened
	t0 := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	activated := !background
	limit := time.After(60 * time.Second)
	for {
		select {
		case err := <-done:
			r := readResult(out)
			r.took = time.Since(t0)
			if err != nil {
				t.Fatalf("open %v: %v\n%s", args, err, r.out)
			}
			if why := r.kv["skip"]; why != "" {
				t.Skipf("%s: %s", name, why)
			}
			if strings.Contains(r.out, "FAIL") {
				t.Fatalf("%s:\n%s", name, r.out)
			}
			return r
		case <-limit:
			t.Fatalf("%s: did not end within 60 s\n%s", name, readResult(out).out)
		case <-time.After(100 * time.Millisecond):
			if !activated && readResult(out).kv["first"] != "" {
				activated = true
				if b, err := exec.Command("open", bundle).CombinedOutput(); err != nil { // the running instance
					t.Fatalf("activating: %v %s", err, b)
				}
			}
		}
	}
}

// readResult reads the "R key=value" lines a scenario wrote to path.
func readResult(path string) result {
	b, _ := os.ReadFile(path)
	r := result{kv: map[string]string{}, out: string(b)}
	sc := bufio.NewScanner(strings.NewReader(r.out))
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "R ") {
			continue
		}
		for _, f := range strings.Fields(line[2:]) {
			if k, v, ok := strings.Cut(f, "="); ok {
				r.kv[k] = v
			}
		}
	}
	return r
}

// focusView records the window focus each frame saw (ui.Input.Focused).
type focusView struct {
	first, last atomic.Int32 // 0: no frame yet, 1 focused, 2 not
}

func (v *focusView) Build(c *ui.Context) {
	f := int32(2)
	if c.Input().Focused {
		f = 1
	}
	v.first.CompareAndSwap(0, f)
	v.last.Store(f)
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.Box(0, "bg")
}

func focusName(f int32) string {
	switch f {
	case 1:
		return "true"
	case 2:
		return "false"
	}
	return "none"
}

func init() {
	// bgkey: a window opened by an app launched in the background (MODE bg)
	// or the foreground (fg): what its frames saw and what AppKit says, then
	// (bg) the same once the app is activated.
	scenarios["bgkey"] = func() {
		v := &focusView{}
		var reported bool
		h := &handler{}
		h.start = func(a *app.App) error {
			if _, err := a.OpenWindow(app.WindowConfig{Title: "bgkey", Width: 320, Height: 200}, v); err != nil {
				say("skip=no-window:%v", strings.ReplaceAll(err.Error(), " ", "_"))
				os.Exit(0)
			}
			after(1500*time.Millisecond, 1)
			after(20*time.Second, 3)
			return nil
		}
		h.event = func(a *app.App, e *app.Event) bool {
			switch {
			case e.Kind == app.EvPost && e.ID == 1:
				st := a.Win.DebugState()
				say("first=%s focused=%s key=%v active=%v", focusName(v.first.Load()), focusName(v.last.Load()), st.Key, st.AppActive)
				reported = true
				if os.Getenv("MODE") == "fg" || st.AppActive {
					a.Quit()
				}
			case e.Kind == app.EvActivated && reported:
				after(700*time.Millisecond, 2)
			case e.Kind == app.EvPost && e.ID == 2:
				st := a.Win.DebugState()
				say("after_focused=%s after_key=%v after_active=%v", focusName(v.last.Load()), st.Key, st.AppActive)
				a.Quit()
			case e.Kind == app.EvPost && e.ID == 3:
				say("activated=false")
				a.Quit()
			case e.Kind == app.EvReopen:
			default:
				return false
			}
			return true
		}
		app.Main(app.AppConfig{KeepRunning: true}, h)
	}
}
