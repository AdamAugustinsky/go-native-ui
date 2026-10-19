// Command rexui copies rexui's components into an app, so that the app owns
// their code (shadcn's model: no registry, no third-party code). It copies a
// component's file, the files it needs and the shared Tint helpers, rewrites
// the package clause and the examples' package name, and leaves everything
// else as it is; the copies build on widget/base and theme as rexui does.
//
//	go run ./tools/rexui list
//	go run ./tools/rexui add [-pkg name] [-f] COMPONENT... DIR
//
// The package name defaults to DIR's base name. Existing files are left alone
// unless -f is given.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// components are the components and the rexui files each needs, its own
// first; tint.go comes with every one.
var components = map[string][]string{
	"button":      {"button.go"},
	"iconbutton":  {"iconbutton.go", "button.go"},
	"checkbox":    {"checkbox.go"},
	"radio":       {"radio.go", "checkbox.go"},
	"switch":      {"switch.go"},
	"slider":      {"slider.go"},
	"segmented":   {"segmented.go"},
	"textfield":   {"textfield.go", "searchfield.go"},
	"searchfield": {"searchfield.go", "textfield.go"},
	"select":      {"select.go", "button.go"},
	"separator":   {"separator.go"},
	"text":        {"text.go"},
	"card":        {"card.go"},
}

const shared = "tint.go"

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "list":
		for _, n := range names() {
			fmt.Printf("%-12s %s\n", n, strings.Join(components[n], ", "))
		}
	case "add":
		fs := flag.NewFlagSet("add", flag.ExitOnError)
		pkg := fs.String("pkg", "", "package name of the copies (default: DIR's base name)")
		force := fs.Bool("f", false, "overwrite existing files")
		fs.Parse(os.Args[2:])
		args := fs.Args()
		if len(args) < 2 {
			usage()
		}
		dir := args[len(args)-1]
		files, err := Add(sourceDir(), dir, *pkg, *force, args[:len(args)-1]...)
		if err != nil {
			fmt.Fprintln(os.Stderr, "rexui:", err)
			os.Exit(1)
		}
		for _, f := range files {
			fmt.Println(f)
		}
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: rexui list\n       rexui add [-pkg name] [-f] COMPONENT... DIR")
	os.Exit(2)
}

func names() []string {
	n := make([]string, 0, len(components))
	for k := range components {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}

// sourceDir is the rexui package's directory, found from this file's.
func sourceDir() string {
	_, self, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(self), "..", "..", "rexui")
}

var (
	packageClause = regexp.MustCompile(`(?m)^package rexui$`)
	exampleName   = regexp.MustCompile(`(?m)^(//\t.*)\brexui\.`)
)

// Add copies the components named into dir as package pkg (dir's base name
// when empty), with the files they need, from rexui's directory src, and
// returns the files it wrote.
func Add(src, dir, pkg string, force bool, comps ...string) ([]string, error) {
	if pkg == "" {
		pkg = filepath.Base(filepath.Clean(dir))
	}
	if !regexp.MustCompile(`^[a-z][a-z0-9_]*$`).MatchString(pkg) {
		return nil, fmt.Errorf("package name %q: want a lower-case Go identifier (use -pkg)", pkg)
	}
	want := map[string]bool{shared: true}
	for _, c := range comps {
		fs, ok := components[c]
		if !ok {
			return nil, fmt.Errorf("no component %q (rexui list)", c)
		}
		for _, f := range fs {
			want[f] = true
		}
	}
	files := make([]string, 0, len(want))
	for f := range want {
		files = append(files, f)
	}
	sort.Strings(files)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var wrote []string
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(src, f))
		if err != nil {
			return wrote, err
		}
		if !packageClause.Match(b) {
			return wrote, fmt.Errorf("%s: no package clause to rewrite", f)
		}
		b = packageClause.ReplaceAll(b, []byte("package "+pkg))
		// the examples in comments call the copies by their new package
		for exampleName.Match(b) {
			b = exampleName.ReplaceAll(b, []byte("${1}"+pkg+"."))
		}
		b = append([]byte("// Copied from gonativeui/rexui ("+f+") by tools/rexui: this code is the app's own.\n\n"), b...)
		out := filepath.Join(dir, f)
		if _, err := os.Stat(out); err == nil && !force {
			fmt.Fprintf(os.Stderr, "rexui: %s exists, left alone (-f overwrites)\n", out)
			continue
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return wrote, err
		}
		if old, err := os.ReadFile(out); err == nil && bytes.Equal(old, b) {
			continue
		}
		if err := os.WriteFile(out, b, 0o644); err != nil {
			return wrote, err
		}
		wrote = append(wrote, out)
	}
	return wrote, nil
}
