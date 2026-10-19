package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Each component, copied alone into an app's package, builds there.
func TestAddedComponentsBuild(t *testing.T) {
	gobin := filepath.Join(runtime.GOROOT(), "bin", "go")
	if _, err := os.Stat(gobin); err != nil {
		t.Skip("no go command at", gobin)
	}
	for _, name := range names() {
		dir := filepath.Join(t.TempDir(), "appui")
		files, err := Add(sourceDir(), dir, "", false, name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(files) != len(components[name])+1 {
			t.Fatalf("%s: wrote %v, want its files and %s", name, files, shared)
		}
		for _, f := range files {
			b, _ := os.ReadFile(f)
			if !strings.Contains(string(b), "\npackage appui\n") || strings.Contains(string(b), "package rexui") {
				t.Fatalf("%s: the package clause of %s was not rewritten", name, f)
			}
		}
		// files named on the command line build as a package of the main
		// module, which provides gonativeui's packages
		cmd := exec.Command(gobin, append([]string{"build", "-o", os.DevNull}, files...)...)
		cmd.Dir = filepath.Join(sourceDir(), "..")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s does not build on its own: %v\n%s", name, err, out)
		}
	}
}

func TestAddLeavesExistingFiles(t *testing.T) {
	dir := t.TempDir()
	own := filepath.Join(dir, "button.go")
	if err := os.WriteFile(own, []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(sourceDir(), dir, "x", false, "button"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(own); string(b) != "package x\n" {
		t.Fatal("add overwrote an existing file without -f")
	}
	if _, err := Add(sourceDir(), dir, "x", false, "nosuch"); err == nil {
		t.Fatal("an unknown component was accepted")
	}
}
