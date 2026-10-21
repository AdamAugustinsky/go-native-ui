//go:build darwin

package login_test

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The login agent, for real: a gnui run bundle of the demo
// (app/login/internal/demo) registers its "hidden" agent, launchd starts it
// the way a login would (launchctl kickstart), and the app reports, from
// AppKit, that it runs with a window it did not show.
//
//	GNUI_LIVE_LOGIN=1 go test -run Live ./app/login
//
// The bundle is signed ad hoc unless GNUI_LOGIN_DEVCERT=1 (the gnui-dev
// identity). It registers a login item of this Mac and removes it at the end.
// If the system asks for approval in System Settings → Login Items the test
// stops there and skips: the rest is a hand test.
func TestLiveAgentStartsHidden(t *testing.T) {
	if os.Getenv("GNUI_LIVE_LOGIN") == "" {
		t.Skip("set GNUI_LIVE_LOGIN=1 to register the demo's login agent and start it with launchctl")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	gnui := filepath.Join(t.TempDir(), "gnui")
	build := exec.Command("go", "build", "-o", gnui, "./cmd/gnui")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building gnui: %v\n%s", err, out)
	}
	demo := filepath.Join(root, "app/login/internal/demo")
	// The default output is demo/.gnui/dev: always the same path, because the
	// system remembers the path of a bundle id's login items.
	bundle := filepath.Join(demo, ".gnui/dev/Login Demo.app")
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(cache, "gnui-login-demo.log")
	label := "dev.gonativeui.logindemo.dev.hidden"
	domain := fmt.Sprintf("gui/%d/%s", os.Getuid(), label)

	// waitFor polls the demo's report file for a line with prefix key.
	waitFor := func(key string, limit time.Duration) map[string]string {
		t.Helper()
		deadline := time.Now().Add(limit)
		for time.Now().Before(deadline) {
			if f, err := os.Open(logPath); err == nil {
				sc := bufio.NewScanner(f)
				for sc.Scan() {
					if line, ok := strings.CutPrefix(sc.Text(), "R "); ok && strings.Contains(line, key+"=") {
						f.Close()
						kv := map[string]string{}
						for _, w := range strings.Fields(line) {
							if k, v, ok := strings.Cut(w, "="); ok {
								kv[k] = v
							}
						}
						return kv
					}
				}
				f.Close()
			}
			time.Sleep(200 * time.Millisecond)
		}
		pr, _ := exec.Command("launchctl", "print", domain).CombinedOutput()
		log, _ := os.ReadFile(logPath)
		t.Fatalf("no %q line in %s within %v\nlog:\n%s\nlaunchd:\n%s", key, logPath, limit, log, pr)
		return nil
	}
	// mode runs the built bundle (no rebuild: the system ties a login
	// item to the signature it registered) in the given mode.
	mode := func(m string) map[string]string {
		t.Helper()
		os.Remove(logPath)
		if out, err := exec.Command("open", "-n", bundle, "--args", "-mode", m).CombinedOutput(); err != nil {
			t.Fatalf("open: %v\n%s", err, out)
		}
		return waitFor(m+"_agent", 30*time.Second)
	}
	t.Cleanup(func() {
		exec.Command("launchctl", "kill", "TERM", domain).Run()
		if _, err := os.Stat(bundle); err == nil {
			os.Remove(logPath)
			exec.Command("open", "-n", bundle, "--args", "-mode", "disable").Run()
			time.Sleep(3 * time.Second)
		}
	})

	// An agent left registered by an aborted run would point at the old build,
	// which the new one is not: remove it before rebuilding.
	if _, err := os.Stat(bundle); err == nil {
		os.Remove(logPath)
		exec.Command("open", "-n", bundle, "--args", "-mode", "disable").Run()
		waitFor("disable_agent", 30*time.Second)
	}

	args := []string{"run", "-c", filepath.Join(demo, "gnui.json")}
	if os.Getenv("GNUI_LOGIN_DEVCERT") == "" {
		args = append(args, "-adhoc")
	}
	// Build the bundle and read the first state (gnui run also launches it).
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	os.Remove(logPath)
	cmd := exec.CommandContext(ctx, gnui, append(args, "--", "-mode", "status")...)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gnui run: %v\n%s", err, out)
	}
	first := waitFor("status_agent", 10*time.Second)
	// A bundle the system has not seen yet has no item to report on.
	if s := first["agent_state"]; s != "not_registered" && s != "not_found" {
		t.Fatalf("before registering, the agent is %q", s)
	}

	kv := mode("enable")
	if kv["agent_err"] != "none" {
		t.Fatalf("Enable: %s", kv["agent_err"])
	}
	if kv["agent_state"] == "requires_approval" {
		mode("disable")
		t.Skip("the system wants the user to switch the demo on in System Settings → Login Items: " +
			"hand test (open the pane with login.OpenSettings, switch it on, run this test again)")
	}
	if kv["agent_state"] != "enabled" {
		t.Fatalf("after Enable the agent is %q", kv["agent_state"])
	}
	if kv["app_state"] == "enabled" {
		t.Errorf("the app's own login item is on, and only the agent was asked for")
	}

	// launchd has the agent, with the arguments gnui.json gave it.
	out, err := exec.Command("launchctl", "print", domain).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "--start-hidden") {
		t.Fatalf("launchd does not know the agent with its arguments: %v\n%s", err, out)
	}

	// Start it as a login would, and read what AppKit says.
	os.Remove(logPath)
	if out, err := exec.Command("launchctl", "kickstart", "-k", domain).CombinedOutput(); err != nil {
		t.Fatalf("kickstart: %v\n%s", err, out)
	}
	rep := waitFor("report", 40*time.Second)
	if rep["start_hidden"] != "true" || rep["app_start_hidden"] != "true" {
		t.Errorf("StartHidden: login %s, app %s", rep["start_hidden"], rep["app_start_hidden"])
	}
	if rep["windows"] != "1" || rep["visible"] != "0" {
		t.Errorf("the window the app opened is shown: %s windows, %s visible", rep["windows"], rep["visible"])
	}
	if rep["active"] != "false" {
		t.Errorf("the app activated itself at login")
	}
	t.Logf("report: %v", rep)

	exec.Command("launchctl", "kill", "TERM", domain).Run()
	kv = mode("disable")
	if kv["agent_state"] != "not_registered" && kv["agent_state"] != "not_found" {
		t.Errorf("after Disable the agent is %q", kv["agent_state"])
	}
	if out, err := exec.Command("launchctl", "print", domain).CombinedOutput(); err == nil {
		t.Errorf("launchd still has the agent after Disable:\n%s", out)
	}
}
