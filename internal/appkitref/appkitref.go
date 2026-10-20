// Package appkitref says which set of AppKit reference measurements a
// comparison reads. There are two, each taken on one machine (the "machine"
// object in every measurement file, tools/appkitref/machine.h):
//
//   - macbook: the MacBook Pro (Mac15,6, macOS 27.2 26B5091g, built-in display).
//     The committed CI tests read it, and so do the exit-criteria runs.
//   - macmini: the Mac mini (Mac16,10, macOS 27.2 26B5101f, LG UltraGear 5K).
//     Live testing on that machine compares against it.
//
// Select is the one place the choice is made. CI tests do not use it: they
// read the MacBook set unless the run names another with GNUI_APPKIT_REF.
// Live comparisons and the dev tools use Select, which is that variable, else
// the set whose machine is this host's (model and macOS build), else macbook.
package appkitref

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnvVar overrides the choice: macbook or macmini.
const EnvVar = "GNUI_APPKIT_REF"

// Default is the set CI and unknown hosts read.
const Default = "macbook"

// A Set is where a machine's measurements live and which machine took them.
type Set struct {
	Name  string
	Model string // hw.model
	Build string // kern.osversion
}

// Sets are the committed reference sets. A test checks them against the
// "machine" objects of the files.
var Sets = []Set{
	{"macbook", "Mac15,6", "26B5091g"},
	{"macmini", "Mac16,10", "26B5101f"},
}

// FromEnv reports the set GNUI_APPKIT_REF names, if it is set.
func FromEnv() (name string, ok bool, err error) {
	v := strings.TrimSpace(os.Getenv(EnvVar))
	if v == "" {
		return "", false, nil
	}
	for _, s := range Sets {
		if s.Name == v {
			return v, true, nil
		}
	}
	return "", false, fmt.Errorf("%s=%q: want macbook or macmini", EnvVar, v)
}

// Select returns the set for a live comparison on this host: GNUI_APPKIT_REF,
// else the set measured on a machine with this host's model and macOS build,
// else Default.
func Select() (string, error) {
	if n, ok, err := FromEnv(); ok || err != nil {
		return n, err
	}
	if n, ok := Host(); ok {
		return n, nil
	}
	return Default, nil
}

// Host is the set measured on a machine with this host's model and macOS
// build. Recording tools use it, not Select, so that an unknown machine
// never writes over another's files.
func Host() (name string, ok bool) {
	model, build := host()
	for _, s := range Sets {
		if s.Model == model && s.Build == build {
			return s.Name, true
		}
	}
	return "", false
}

// File names the set's copy of a committed file: the MacBook's keeps its
// name (rexui/testdata/appkit-27.2.json), another set's gets "-<set>" before
// the extension (appkit-27.2-macmini.json).
func File(set, path string) string {
	if set == Default {
		return path
	}
	ext := filepath.Ext(path)
	return strings.TrimSuffix(path, ext) + "-" + set + ext
}

// Research is the directory of AppKitRef's runs and appkitscenes' captures,
// outside the repository.
func Research() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Projects", "research", "appkit-ref")
}

// Metrics is the set's AppKitRef metrics.json for the default blue accent
// (macbook-blue/, macmini-blue/), the run the committed recipe numbers come from.
func Metrics(set string) string {
	return filepath.Join(Research(), set+"-blue", "metrics.json")
}

// Scenes is the directory of the set's appkitscenes captures and measured.json
// (scenes/captures for the MacBook, scenes/captures-macmini).
func Scenes(set string) string {
	if set == Default {
		return filepath.Join(Research(), "scenes", "captures")
	}
	return filepath.Join(Research(), "scenes", "captures-"+set)
}
