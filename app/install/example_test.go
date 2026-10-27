package install_test

import (
	"errors"
	"os"

	"gonativeui/app/install"
)

// Example is a Windows app's first launch: install for the current user and
// hand over to the installed copy; later launches refresh the URL scheme.
func Example() {
	o := install.Options{
		ID: "app.a3lab.hyperpedia", Name: "HyperPedia", Publisher: "A3 Lab",
		Schemes: []string{"a3kb"}, Relaunch: true,
	}
	if err := install.Supported(); err != nil {
		_ = errors.Is(err, install.ErrNotNeeded) // macOS: a bundle needs nothing
		return
	}
	if len(os.Args) > 1 && os.Args[1] == install.UninstallArg {
		_ = install.Uninstall(o)
		return
	}
	if _, ok := install.Installed(); !ok {
		if install.Install(o) == nil {
			return // the installed copy is starting
		}
	}
	_ = install.RegisterScheme("a3kb")
}
