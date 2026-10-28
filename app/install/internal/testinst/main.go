// Command testinst is the app the Windows installer test builds and runs: it
// installs itself, reports what it saw, and uninstalls itself when the
// uninstall entry starts it with --uninstall.
//
//	INSTTEST_REPORT  folder for the reports (<pid>-<event>.json)
//	INSTTEST_ID      install.Options.ID
//	INSTTEST_NAME    install.Options.Name
//	INSTTEST_SCHEME  the URL scheme to register
//
// Started with a URL of that scheme, it reports "opened" with the URL.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gonativeui/app/install"
)

type report struct {
	Pid       int      `json:"pid"`
	Event     string   `json:"event"`
	Exe       string   `json:"exe"`
	Args      []string `json:"args"`
	Installed string   `json:"installed"`
	IsCopy    bool     `json:"isCopy"`
	Err       string   `json:"err"`
}

func main() {
	scheme := os.Getenv("INSTTEST_SCHEME")
	o := install.Options{
		ID: os.Getenv("INSTTEST_ID"), Name: os.Getenv("INSTTEST_NAME"), Publisher: "Go Native UI tests",
		Version: "1.0.0", Schemes: []string{scheme}, Relaunch: true,
	}
	exe, _ := os.Executable()
	r := report{Pid: os.Getpid(), Exe: exe, Args: os.Args[1:]}
	r.Installed, r.IsCopy = install.Installed()
	switch {
	case len(os.Args) == 2 && os.Args[1] == install.UninstallArg:
		write(r, "uninstalled", install.Uninstall(o))
	case len(os.Args) == 2 && strings.HasPrefix(os.Args[1], scheme+":"):
		write(r, "opened", nil)
	case !r.IsCopy:
		write(r, "installed", install.Install(o))
	default:
		write(r, "running", install.RegisterScheme(scheme))
	}
}

func write(r report, event string, err error) {
	dir := os.Getenv("INSTTEST_REPORT")
	if dir == "" {
		return
	}
	r.Event = event
	if err != nil {
		r.Err = err.Error()
	}
	b, _ := json.Marshal(r)
	name := filepath.Join(dir, fmt.Sprintf("%d-%s.json", r.Pid, event))
	if os.WriteFile(name+".tmp", b, 0o600) == nil {
		os.Rename(name+".tmp", name)
	}
}
