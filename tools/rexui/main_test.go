package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Each component, copied alone into an app's package, builds there.
func TestAddedComponentsBuild(t *testing.T) {
	exe := "go"
	if runtime.GOOS == "windows" {
		exe = "go.exe"
	}
	gobin := filepath.Join(runtime.GOROOT(), "bin", exe)
	if _, err := os.Stat(gobin); err != nil {
		t.Skip("no go command at", gobin)
	}
	for _, name := range names() {
		dir := filepath.Join(t.TempDir(), "appui")
		files, err := Add(sourceDir(), dir, "", false, name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(files) != len(components[name])+len(shared) {
			t.Fatalf("%s: wrote %v, want its files and %s", name, files, shared)
		}
		for _, f := range files {
			b, _ := os.ReadFile(f)
			if !regexp.MustCompile(`(?m)^package appui\r?$`).Match(b) || strings.Contains(string(b), "package rexui") {
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

// A Windows checkout's CRLF line endings are kept, and the package clause is
// still rewritten.
func TestAddCRLF(t *testing.T) {
	src, dir := t.TempDir(), t.TempDir()
	for _, f := range append([]string{"button.go"}, shared...) {
		b, err := os.ReadFile(filepath.Join(sourceDir(), f))
		if err != nil {
			t.Fatal(err)
		}
		crlf := strings.ReplaceAll(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n", "\r\n")
		if err := os.WriteFile(filepath.Join(src, f), []byte(crlf), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Add(src, dir, "x", false, "button"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "button.go"))
	if !strings.Contains(string(b), "\r\npackage x\r\n") {
		t.Fatal("the package clause of a CRLF file was not rewritten in place")
	}
}

// A destination package named rexui keeps its examples' calls as they are,
// and the rewrite finishes (it once replaced rexui. with itself forever).
func TestAddIntoPackageRexui(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "internal", "rexui")
	type result struct {
		files []string
		err   error
	}
	done := make(chan result, 1)
	go func() {
		files, err := Add(sourceDir(), dir, "", false, "button")
		done <- result{files, err}
	}()
	var res result
	select {
	case res = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("add into ./internal/rexui does not finish")
	}
	if res.err != nil {
		t.Fatal(res.err)
	}
	for _, f := range res.files {
		got, _ := os.ReadFile(f)
		want, _ := os.ReadFile(filepath.Join(sourceDir(), filepath.Base(f)))
		// the source after the header that says where the copy came from
		// (whatever line endings the checkout has)
		if !strings.HasPrefix(string(got), "// Copied from") || !strings.HasSuffix(string(got), string(want)) ||
			len(got)-len(want) > 200 {
			t.Fatalf("%s differs from its source in package rexui", f)
		}
	}
}

// A package named after a Go keyword is refused, pointing at -pkg.
func TestAddRejectsKeywordPackage(t *testing.T) {
	for _, name := range []string{"switch", "func", "type"} {
		dir := filepath.Join(t.TempDir(), "internal", name)
		_, err := Add(sourceDir(), dir, "", false, "switch")
		if err == nil || !strings.Contains(err.Error(), "-pkg") {
			t.Fatalf("package %s: %v", name, err)
		}
		if _, err := os.Stat(dir); err == nil {
			if ents, _ := os.ReadDir(dir); len(ents) > 0 {
				t.Fatalf("package %s: wrote %d files", name, len(ents))
			}
		}
	}
	if _, err := Add(sourceDir(), filepath.Join(t.TempDir(), "switch"), "toggles", false, "switch"); err != nil {
		t.Fatalf("-pkg toggles: %v", err)
	}
}
