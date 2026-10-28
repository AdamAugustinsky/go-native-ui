// Command testappwin is the executable the Windows selfupdate tests build.
// The tests link a version resource into it and make variants by patching
// the built file: the file version, the behavior marker below, the machine
// type, the AppUserModelID. It configures the package from the environment,
// writes a report of what it saw, and then behaves as its marker says:
//
//	healthy  calls selfupdate.Started and exits
//	hang     never calls Started and waits to be stopped
//	exit     exits at once without calling Started
//
// Started with "update <file> [sha256]", it updates itself from the file
// (with PrepareSHA256 when the digest is given) and relaunches with the
// argument "relaunched"; with "supported" it reports Supported's answer.
//
//	SUTEST_REPORT          folder for the reports (<pid>-<event>.json)
//	SUTEST_HEALTH_TIMEOUT  selfupdate.Options.HealthTimeout
//	SUTEST_UNSIGNED        "1": selfupdate.Options.Unsigned
//	SUTEST_ALLOW_DOWNGRADE "1": selfupdate.Options.AllowDowngrade
package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gonativeui/app/selfupdate"
	"gonativeui/internal/winres"
)

// marker is patched in the built file by the tests (the same length).
var marker = "SUTEST-BEHAVIOR=healthy                 "

type report struct {
	Pid      int      `json:"pid"`
	Event    string   `json:"event"`
	Args     []string `json:"args"`
	Exe      string   `json:"exe"`
	Version  string   `json:"version"`
	Behavior string   `json:"behavior"`
	Status   string   `json:"status"`
	From     string   `json:"from"`
	To       string   `json:"to"`
	Reason   string   `json:"reason"`
	Err      string   `json:"err"`
}

func main() {
	timeout, _ := time.ParseDuration(os.Getenv("SUTEST_HEALTH_TIMEOUT"))
	selfupdate.Configure(selfupdate.Options{HealthTimeout: timeout,
		Unsigned:       os.Getenv("SUTEST_UNSIGNED") == "1",
		AllowDowngrade: os.Getenv("SUTEST_ALLOW_DOWNGRADE") == "1"})
	exe, _ := os.Executable()
	r := report{Pid: os.Getpid(), Args: os.Args[1:], Exe: exe, Version: version(exe),
		Behavior: strings.TrimSpace(strings.TrimPrefix(marker, "SUTEST-BEHAVIOR="))}
	write(r, "start")
	selfupdate.RunHelperIfAsked()

	res := selfupdate.LastResult()
	r.Status, r.From, r.To, r.Reason = res.Status.String(), res.From, res.To, res.Reason
	switch {
	case len(r.Args) >= 2 && r.Args[0] == "update":
		if err := update(r.Args[1:]); err != nil {
			r.Err = err.Error()
			write(r, "failed")
			os.Exit(1)
		}
		write(r, "updating")
		return
	case len(r.Args) == 1 && r.Args[0] == "supported":
		if err := selfupdate.Supported(); err != nil {
			r.Err = err.Error()
		}
		write(r, "supported")
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

func update(args []string) error {
	if err := selfupdate.Supported(); err != nil {
		return err
	}
	var st *selfupdate.Staged
	var err error
	if len(args) == 2 {
		var sum [32]byte
		b, derr := hex.DecodeString(args[1])
		if derr != nil || len(b) != 32 {
			return fmt.Errorf("bad digest %q", args[1])
		}
		copy(sum[:], b)
		st, err = selfupdate.PrepareSHA256(args[0], sum)
	} else {
		st, err = selfupdate.Prepare(args[0])
	}
	if err != nil {
		return err
	}
	return st.InstallAndRelaunch([]string{"relaunched"})
}

// version is the executable's four-part file version.
func version(exe string) string {
	f, err := os.Open(exe)
	if err != nil {
		return ""
	}
	defer f.Close()
	st, _ := f.Stat()
	pf, err := winres.Read(f, st.Size())
	if err != nil {
		return ""
	}
	res, ok := winres.Find(pf.Resources, winres.TypeVersion, 1)
	if !ok {
		return ""
	}
	v, err := winres.ParseVersion(res.Data)
	if err != nil {
		return ""
	}
	return winres.QuadString(v.File)
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
