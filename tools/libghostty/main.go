// Command libghostty builds libghostty-vt, Ghostty's terminal library, at
// the commit internal/ghosttyvt pins, as the shared libraries package term
// loads at run time (D74): darwin/arm64 (.dylib, install name @rpath/…),
// linux/amd64 and linux/arm64 (.so), windows/amd64 and windows/arm64
// (.dll), all cross-compiled by Zig from a Mac.
//
//	go run ./tools/libghostty [-src dir] [-zig path] [-out dir] [-targets list]
//	go run ./tools/libghostty -check   # verify existing artifacts only
//
// The source is a checkout or an export of Ghostty at ghosttyvt.Commit
// (default $GHOSTTY_SRC, else ~/Projects/research/ghostty-pin): a git
// checkout must be at the commit, an export must hold the commit in a
// .pinned-commit file. Zig must be ghosttyvt.ZigVersion. The flags mirror
// Ghostty's CI (zig build -Demit-lib-vt, the -gnu ABI on Linux and
// Windows) with -Doptimize=ReleaseFast.
//
// Artifacts are not committed. They go to a git-ignored cache,
// <out>/<short commit>/<goos>-<goarch>/<file> (default out:
// <repo>/.cache/libghostty-vt), beside manifest.json (commit, Zig version,
// and per artifact its size, SHA-256 and exported functions) and Ghostty's
// LICENSE. Each artifact must export every function ghosttyvt.Names lists;
// the Linux libraries are stripped of debug sections, which Zig leaves in
// them. gnui.json's nativeLibs names the cache directory to bundle from, and
// the env override GNUI_LIBGHOSTTY_VT points tests and `go run` at one
// library.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"gonativeui/internal/ghosttyvt"
)

// target is one library to build.
type target struct {
	GOOS, GOARCH, Zig string
}

var allTargets = []target{
	{"darwin", "arm64", "aarch64-macos"},
	{"linux", "amd64", "x86_64-linux-gnu"},
	{"linux", "arm64", "aarch64-linux-gnu"},
	{"windows", "amd64", "x86_64-windows-gnu"},
	{"windows", "arm64", "aarch64-windows-gnu"},
}

// Manifest is manifest.json.
type Manifest struct {
	Commit     string     `json:"commit"`
	Zig        string     `json:"zig"`
	Optimize   string     `json:"optimize"`
	Built      string     `json:"built"`
	Host       string     `json:"host"`
	Artifacts  []Artifact `json:"artifacts"`
	SourceNote string     `json:"source_note"`
}

// Artifact is one library in the manifest.
type Artifact struct {
	Target   string   `json:"target"`
	Zig      string   `json:"zig_target"`
	File     string   `json:"file"`
	Bytes    int64    `json:"bytes"`
	SHA256   string   `json:"sha256"`
	Exports  int      `json:"exports"`
	Required int      `json:"required_exports"`
	Missing  []string `json:"missing,omitempty"`
	Stripped bool     `json:"stripped_debug,omitempty"`
}

func main() {
	home, _ := os.UserHomeDir()
	defSrc := os.Getenv("GHOSTTY_SRC")
	if defSrc == "" {
		defSrc = filepath.Join(home, "Projects", "research", "ghostty-pin")
	}
	defZig := os.Getenv("ZIG")
	if defZig == "" {
		defZig = "zig"
	}
	src := flag.String("src", defSrc, "Ghostty source at the pinned commit")
	zig := flag.String("zig", defZig, "the zig executable")
	out := flag.String("out", "", "artifact cache (default <repo>/.cache/libghostty-vt)")
	targets := flag.String("targets", "", "comma-separated goos/goarch list (default: all five)")
	check := flag.Bool("check", false, "verify the cached artifacts and rewrite the manifest; build nothing")
	flag.Parse()
	if *out == "" {
		root, err := repoRoot()
		if err != nil {
			fail(err)
		}
		*out = filepath.Join(root, ".cache", "libghostty-vt")
	}
	sel, err := pick(*targets)
	if err != nil {
		fail(err)
	}
	dir := filepath.Join(*out, ghosttyvt.Short)
	if !*check {
		if err := build(*src, *zig, dir, sel); err != nil {
			fail(err)
		}
	}
	m, err := verify(dir, sel)
	if err != nil {
		fail(err)
	}
	if !*check {
		if lic, err := os.ReadFile(filepath.Join(*src, "LICENSE")); err == nil {
			os.WriteFile(filepath.Join(dir, "LICENSE"), lic, 0o644)
		}
	}
	data, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), append(data, '\n'), 0o644); err != nil {
		fail(err)
	}
	fmt.Printf("libghostty-vt %s (Zig %s) in %s\n", ghosttyvt.Short, ghosttyvt.ZigVersion, dir)
	bad := false
	for _, a := range m.Artifacts {
		fmt.Printf("  %-14s %-22s %9d bytes  %d exports  sha256 %s\n", a.Target, a.File, a.Bytes, a.Exports, a.SHA256[:16])
		if len(a.Missing) > 0 {
			fmt.Printf("    missing: %s\n", strings.Join(a.Missing, ", "))
			bad = true
		}
	}
	if bad {
		fail(errors.New("an artifact lacks functions term calls"))
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "libghostty:", err)
	os.Exit(1)
}

func pick(list string) ([]target, error) {
	if list == "" {
		return allTargets, nil
	}
	var sel []target
	for _, s := range strings.Split(list, ",") {
		found := false
		for _, t := range allTargets {
			if t.GOOS+"/"+t.GOARCH == strings.TrimSpace(s) {
				sel, found = append(sel, t), true
			}
		}
		if !found {
			return nil, fmt.Errorf("unknown target %q", s)
		}
	}
	return sel, nil
}

// repoRoot is the directory of the gonativeui go.mod at or above the
// working directory.
func repoRoot() (string, error) {
	d, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if b, err := os.ReadFile(filepath.Join(d, "go.mod")); err == nil && bytes.HasPrefix(b, []byte("module gonativeui")) {
			return d, nil
		}
		p := filepath.Dir(d)
		if p == d {
			return "", errors.New("not inside the gonativeui module")
		}
		d = p
	}
}

// sourceCommit is the commit the source tree is at.
func sourceCommit(src string) (string, error) {
	if b, err := os.ReadFile(filepath.Join(src, ".pinned-commit")); err == nil {
		return strings.TrimSpace(string(b)), nil
	}
	b, err := exec.Command("git", "-C", src, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("%s is neither a git checkout nor an export with .pinned-commit: %v", src, err)
	}
	return strings.TrimSpace(string(b)), nil
}

func build(src, zig, dir string, sel []target) error {
	c, err := sourceCommit(src)
	if err != nil {
		return err
	}
	if c != ghosttyvt.Commit {
		return fmt.Errorf("%s is at %s, the pin is %s", src, c, ghosttyvt.Commit)
	}
	v, err := exec.Command(zig, "version").Output()
	if err != nil {
		return fmt.Errorf("zig: %v", err)
	}
	if got := strings.TrimSpace(string(v)); got != ghosttyvt.ZigVersion {
		return fmt.Errorf("zig is %s, Ghostty at the pin needs %s", got, ghosttyvt.ZigVersion)
	}
	for _, t := range sel {
		if t.GOOS == "darwin" && runtime.GOOS != "darwin" {
			return errors.New("the darwin library builds only on macOS (Zig needs the Apple SDK)")
		}
		prefix, err := os.MkdirTemp("", "libghostty-"+t.GOOS+"-"+t.GOARCH+"-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(prefix)
		fmt.Printf("zig build -Demit-lib-vt -Dtarget=%s\n", t.Zig)
		start := time.Now()
		cmd := exec.Command(zig, "build", "-Demit-lib-vt", "-Doptimize=ReleaseFast", "-Demit-xcframework=false",
			"-Dtarget="+t.Zig, "--prefix", prefix)
		cmd.Dir = src
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s/%s: %v", t.GOOS, t.GOARCH, err)
		}
		fmt.Printf("  %s/%s built in %s\n", t.GOOS, t.GOARCH, time.Since(start).Round(time.Second))
		built, err := builtLib(prefix, t)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(built)
		if err != nil {
			return err
		}
		if t.GOOS == "linux" {
			if data, err = stripELF(data); err != nil {
				return fmt.Errorf("stripping %s: %v", built, err)
			}
		}
		dst := filepath.Join(dir, t.GOOS+"-"+t.GOARCH, ghosttyvt.FileName(t.GOOS))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, data, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// builtLib is the shared library zig installed under prefix: the real file
// behind the .dylib and .so version links, or the DLL in bin.
func builtLib(prefix string, t target) (string, error) {
	switch t.GOOS {
	case "windows":
		return filepath.Join(prefix, "bin", "ghostty-vt.dll"), nil
	case "darwin":
		return filepath.EvalSymlinks(filepath.Join(prefix, "lib", "libghostty-vt.dylib"))
	}
	m, _ := filepath.Glob(filepath.Join(prefix, "lib", "libghostty-vt.so.*.*.*"))
	if len(m) != 1 {
		return "", fmt.Errorf("%s/%s: no libghostty-vt.so.x.y.z in %s", t.GOOS, t.GOARCH, prefix)
	}
	return m[0], nil
}

// verify checks each cached artifact and describes it.
func verify(dir string, sel []target) (*Manifest, error) {
	m := &Manifest{
		Commit:     ghosttyvt.Commit,
		Zig:        ghosttyvt.ZigVersion,
		Optimize:   "ReleaseFast",
		Built:      time.Now().UTC().Format(time.RFC3339),
		Host:       runtime.GOOS + "/" + runtime.GOARCH,
		SourceNote: "zig build -Demit-lib-vt -Doptimize=ReleaseFast -Dtarget=<zig_target>; Linux debug sections stripped",
	}
	for _, t := range sel {
		p := filepath.Join(dir, t.GOOS+"-"+t.GOARCH, ghosttyvt.FileName(t.GOOS))
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("%s/%s: %v (build it first)", t.GOOS, t.GOARCH, err)
		}
		exports, err := listExports(t.GOOS, data)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", p, err)
		}
		have := make(map[string]bool, len(exports))
		for _, e := range exports {
			have[e] = true
		}
		var missing []string
		for _, n := range ghosttyvt.Names {
			if !have[n] {
				missing = append(missing, n)
			}
		}
		sort.Strings(missing)
		sum := sha256.Sum256(data)
		m.Artifacts = append(m.Artifacts, Artifact{
			Target: t.GOOS + "/" + t.GOARCH, Zig: t.Zig, File: filepath.Join(t.GOOS+"-"+t.GOARCH, filepath.Base(p)),
			Bytes: int64(len(data)), SHA256: hex.EncodeToString(sum[:]), Exports: len(exports),
			Required: len(ghosttyvt.Names), Missing: missing, Stripped: t.GOOS == "linux",
		})
		if os.Getenv("LIBGHOSTTY_LIST_EXPORTS") != "" {
			fmt.Printf("== %s/%s exports (%d)\n", t.GOOS, t.GOARCH, len(exports))
			w := io.Writer(os.Stdout)
			for _, e := range exports {
				fmt.Fprintln(w, " ", e)
			}
		}
	}
	return m, nil
}
