// Command metallib compiles package gpu's two Metal shader sources into the
// precompiled library the framework embeds (internal/metallib/lib), and
// checks that the committed library is the one for the current sources.
//
//	go run ./tools/metallib [-out dir] [-target triple] [-std version] [-by ci|local]
//	go run ./tools/metallib -check [-dir dir] [-allow-absent]
//
// Generating needs Xcode's Metal toolchain (`xcrun -sdk macosx metal` and
// `metallib`; the Command Line Tools alone do not have it), so it runs in
// CI on a macOS runner (.github/workflows/metallib.yml), which uploads the
// result; the library is committed from there. It reads quadMSL and
// backdropMSL out of gpu's Go files, compiles each to AIR, links both into
// shaders.metallib and writes manifest.json beside it: the library's size
// and SHA-256, the sources' hash (metallib.SourceHash) and each source's,
// the compiler version, SDK, target and language version, the date, who
// generated it and the repository commit.
//
// -check needs no toolchain: it fails when the manifest names no library
// (unless -allow-absent), when the library is not the one the manifest
// names, or when the sources changed since it was generated. Libraries are
// not compared byte for byte with a fresh compile, since the compiler's
// output changes from one Xcode to the next: the sources' hash is what
// makes a library current.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gonativeui/internal/metallib"
	"gonativeui/internal/metallib/mslsrc"
)

const libFile = "shaders.metallib"

func main() {
	root, err := repoRoot()
	if err != nil {
		fail(err)
	}
	committed := filepath.Join(root, "internal", "metallib", "lib")
	out := flag.String("out", committed, "directory to write manifest.json and "+libFile+" into")
	target := flag.String("target", "air64-apple-macos14.0", "metal -target: air64 and the macOS deployment target (DECISIONS.md D1)")
	std := flag.String("std", "metal3.1", "Metal language version (metal -std); macOS 14 has 3.1")
	by := flag.String("by", "", `who generates it: "ci" or "local" (default "ci" under GitHub Actions)`)
	check := flag.Bool("check", false, "check the library in -dir against the sources; no toolchain needed")
	dir := flag.String("dir", committed, "with -check: the directory holding manifest.json")
	allowAbsent := flag.Bool("allow-absent", false, "with -check: a manifest that names no library passes")
	flag.Parse()

	quad, backdrop, err := mslsrc.Read(filepath.Join(root, "gpu"))
	if err != nil {
		fail(err)
	}
	if *check {
		if err := checkDir(*dir, quad, backdrop, *allowAbsent); err != nil {
			fail(err)
		}
		return
	}
	if *by == "" {
		*by = "local"
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			*by = "ci"
		}
	}
	m, err := generate(root, *out, quad, backdrop, *target, *std, *by)
	if err != nil {
		fail(err)
	}
	fmt.Printf("metallib: wrote %s (%d bytes, sha256 %s, sources %s)\n", filepath.Join(*out, libFile), m.Bytes, m.SHA256, m.SourceSHA256)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "metallib:", err)
	os.Exit(1)
}

// repoRoot is the directory of go.mod at or above the working directory.
func repoRoot() (string, error) {
	d, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d, nil
		}
		up := filepath.Dir(d)
		if up == d {
			return "", errors.New("no go.mod at or above the working directory")
		}
		d = up
	}
}

// checkDir checks the manifest and library in dir against the sources.
func checkDir(dir, quad, backdrop string, allowAbsent bool) error {
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return err
	}
	m, ok := metallib.Parse(b)
	if !ok {
		return fmt.Errorf("%s/manifest.json: not a schema-%d manifest", dir, metallib.Schema)
	}
	if m.File == "" {
		if allowAbsent {
			fmt.Println("metallib: no precompiled library (allowed)")
			return nil
		}
		return errors.New("no precompiled library: generate it (the metallib workflow's artifact, or go run ./tools/metallib with Xcode) and commit internal/metallib/lib")
	}
	lib, err := os.ReadFile(filepath.Join(dir, m.File))
	if err != nil {
		return err
	}
	if h := metallib.Hash(lib); h != m.SHA256 || int64(len(lib)) != m.Bytes {
		return fmt.Errorf("%s is %d bytes with sha256 %s; the manifest says %d bytes, %s", m.File, len(lib), h, m.Bytes, m.SHA256)
	}
	if h := metallib.SourceHash(quad, backdrop); h != m.SourceSHA256 {
		var changed []string
		for _, s := range m.Sources {
			src := quad
			if s.Name == "backdrop" {
				src = backdrop
			}
			if metallib.Hash([]byte(src)) != s.SHA256 {
				changed = append(changed, s.Name)
			}
		}
		return fmt.Errorf("stale: %s was compiled from other shader sources (changed: %s; sources now %s, manifest %s); regenerate it and commit internal/metallib/lib", m.File, strings.Join(changed, ", "), h, m.SourceSHA256)
	}
	fmt.Printf("metallib: %s is current (sources %s, %s)\n", m.File, m.SourceSHA256, m.Compiler)
	return nil
}

// generate compiles the sources into out and writes the manifest there.
func generate(root, out, quad, backdrop, target, std, by string) (metallib.Manifest, error) {
	tmp, err := os.MkdirTemp("", "metallib")
	if err != nil {
		return metallib.Manifest{}, err
	}
	defer os.RemoveAll(tmp)
	var airs []string
	for _, s := range []struct{ name, src string }{{"quad", quad}, {"backdrop", backdrop}} {
		src := filepath.Join(tmp, s.name+".metal")
		if err := os.WriteFile(src, []byte(s.src), 0o644); err != nil {
			return metallib.Manifest{}, err
		}
		air := filepath.Join(tmp, s.name+".air")
		if _, err := xcrun("metal", "-c", "-target", target, "-std="+std, "-o", air, src); err != nil {
			return metallib.Manifest{}, err
		}
		airs = append(airs, air)
	}
	lib := filepath.Join(tmp, libFile)
	if _, err := xcrun(append([]string{"metallib", "-o", lib}, airs...)...); err != nil {
		return metallib.Manifest{}, err
	}
	data, err := os.ReadFile(lib)
	if err != nil {
		return metallib.Manifest{}, err
	}
	version, err := xcrun("metal", "--version")
	if err != nil {
		return metallib.Manifest{}, err
	}
	sdk, err := xcrun("--show-sdk-version")
	if err != nil {
		return metallib.Manifest{}, err
	}
	m := metallib.Manifest{
		Schema:       metallib.Schema,
		File:         libFile,
		Bytes:        int64(len(data)),
		SHA256:       metallib.Hash(data),
		SourceSHA256: metallib.SourceHash(quad, backdrop),
		Sources: []metallib.Source{
			{Name: "quad", SHA256: metallib.Hash([]byte(quad))},
			{Name: "backdrop", SHA256: metallib.Hash([]byte(backdrop))},
		},
		Compiler:    firstLine(version),
		SDK:         "macosx " + firstLine(sdk),
		Target:      target,
		Std:         std,
		Generated:   time.Now().UTC().Format(time.RFC3339),
		GeneratedBy: by,
		Commit:      commit(root),
	}
	j, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return metallib.Manifest{}, err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return metallib.Manifest{}, err
	}
	if err := os.WriteFile(filepath.Join(out, libFile), data, 0o644); err != nil {
		return metallib.Manifest{}, err
	}
	return m, os.WriteFile(filepath.Join(out, "manifest.json"), append(j, '\n'), 0o644)
}

// xcrun runs an Xcode tool for the macOS SDK and returns its output.
func xcrun(args ...string) (string, error) {
	cmd := exec.Command("xcrun", append([]string{"-sdk", "macosx"}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("xcrun %s: %v\n%s(generating needs Xcode's Metal toolchain; CI's metallib workflow has it)", strings.Join(args, " "), err, errb.String())
	}
	return out.String() + errb.String(), nil
}

// commit is the repository's HEAD, "-dirty" if gpu's shader files are
// modified; empty outside a git checkout.
func commit(root string) string {
	head, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	c := strings.TrimSpace(string(head))
	st, _ := exec.Command("git", "-C", root, "status", "--porcelain", "--", "gpu/shaders_darwin.go", "gpu/backdrop_darwin.go").Output()
	if len(bytes.TrimSpace(st)) > 0 {
		c += "-dirty"
	}
	return c
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(s)
}
