// Command testapp is the executable of the bundles the selfupdate tests build.
// The tests name it testapp.test, which internal/launchguard takes for a Go
// test binary, so that its test-only Options.Requirement is honoured; under
// another name it runs as a shipped app would.
// It configures the package the way the tests ask (environment variables),
// writes a report of what it saw, and then behaves as its bundle says
// (Contents/Resources/behavior):
//
//	healthy  calls selfupdate.Started and exits
//	hang     never calls Started and waits to be stopped
//	exit     exits at once without calling Started
//
// Started with the arguments "update <zip>", it updates itself from the zip
// instead and relaunches with the argument "relaunched". With "report-to
// <folder>" it only reports, there, that it was opened.
//
//	SUTEST_REPORT          folder for the reports (<pid>-<event>.json)
//	SUTEST_HEALTH_TIMEOUT  selfupdate.Options.HealthTimeout
//	SUTEST_REQUIREMENT     the code requirement; "self" for the app's own
//	                       designated requirement; unset for the tests' identifier
//	SUTEST_ALLOW_DOWNGRADE "1": selfupdate.Options.AllowDowngrade
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gonativeui/app/selfupdate"
)

const requirement = `identifier "dev.gonativeui.selfupdatetest"`

type report struct {
	Pid      int      `json:"pid"`
	Event    string   `json:"event"`
	Args     []string `json:"args"`
	Version  string   `json:"version"`
	Behavior string   `json:"behavior"`
	Status   string   `json:"status"`
	From     string   `json:"from"`
	To       string   `json:"to"`
	Reason   string   `json:"reason"`
	Staged   string   `json:"staged"`
	Err      string   `json:"err"`
}

func main() {
	req := os.Getenv("SUTEST_REQUIREMENT")
	switch req {
	case "":
		req = requirement
	case "self":
		req = ""
	}
	timeout, _ := time.ParseDuration(os.Getenv("SUTEST_HEALTH_TIMEOUT"))
	selfupdate.Configure(selfupdate.Options{Requirement: req, HealthTimeout: timeout, Launcher: launch,
		AllowDowngrade: os.Getenv("SUTEST_ALLOW_DOWNGRADE") == "1"})
	r := report{Pid: os.Getpid(), Args: os.Args[1:], Version: resource("version"), Behavior: resource("behavior")}
	write(r, "start")
	selfupdate.RunHelperIfAsked()

	if len(r.Args) == 2 && r.Args[0] == "report-to" { // opened by Launch Services, which passes no environment
		os.Setenv("SUTEST_REPORT", r.Args[1])
		write(r, "opened")
		time.Sleep(30 * time.Second) // the test finds it running, then stops it
		return
	}
	res := selfupdate.LastResult()
	r.Status, r.From, r.To, r.Reason = res.Status.String(), res.From, res.To, res.Reason
	if len(r.Args) == 2 && r.Args[0] == "update" {
		if err := update(&r, r.Args[1]); err != nil {
			r.Err = err.Error()
			write(r, "failed")
			os.Exit(1)
		}
		write(r, "updating")
		return
	}
	switch r.Behavior {
	case "healthy":
		selfupdate.Started()
		write(r, "started")
	case "hang":
		write(r, "hanging")
		for {
			time.Sleep(time.Hour)
		}
	default:
		write(r, "exiting")
		os.Exit(3)
	}
}

func update(r *report, zip string) error {
	if err := selfupdate.Supported(); err != nil {
		return err
	}
	st, err := selfupdate.Prepare(zip)
	if err != nil {
		return err
	}
	r.Staged = st.Path()
	return st.InstallAndRelaunch([]string{"relaunched"})
}

// launch runs the bundle's executable directly: the tests never go through
// Launch Services. The helper's copy has the executable's name.
func launch(bundle string, args []string) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}
	cmd := exec.Command(filepath.Join(bundle, "Contents", "MacOS", filepath.Base(exe)), args...)
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	go cmd.Wait()
	return cmd.Process.Pid, nil
}

func resource(name string) string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	b, _ := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(exe)), "Resources", name))
	return strings.TrimSpace(string(b))
}

func write(r report, event string) {
	dir := os.Getenv("SUTEST_REPORT")
	if dir == "" {
		return
	}
	r.Event = event
	b, _ := json.Marshal(r)
	name := filepath.Join(dir, fmt.Sprintf("%d-%s.json", r.Pid, event))
	if os.WriteFile(name+".tmp", b, 0o600) == nil {
		os.Rename(name+".tmp", name)
	}
}
