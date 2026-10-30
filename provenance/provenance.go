// Package provenance describes the framework build an application ships:
// the framework's revision, the libghostty-vt pin, the precompiled shader
// library and the Go toolchain, as a small versioned JSON document (schema
// "gnui.provenance/v1") for app packagers. See
// docs/design/build-provenance.md for the contract.
//
// Compiled reports what is compiled into the running binary; Read reports a
// framework source tree, for the packager that builds from it (`gnui
// provenance`). A field this package cannot establish is omitted, never
// guessed, and each block's source says where its identity came from.
package provenance

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"

	"gonativeui/internal/ghosttyvt"
	"gonativeui/internal/goconst"
	"gonativeui/internal/metallib"
	"gonativeui/internal/metallib/mslsrc"
)

// Schema is the value of Info.Schema. A change that removes or renames a
// field, or changes a field's meaning, makes a new schema; adding a field
// does not.
const Schema = "gnui.provenance/v1"

// Module is the framework's module path.
const Module = "gonativeui"

// FrameworkRevision is the framework's git commit as the packager asserts
// it, for a binary that cannot stamp it itself (the framework is a replaced
// module, so the Go toolchain records no revision for it): set it with
//
//	-ldflags "-X gonativeui/provenance.FrameworkRevision=<40 hex>"
//
// optionally with "-dirty" after the hash. Compiled reports it with source
// "ldflags"; a value that is not a 40-digit hex hash is ignored.
var FrameworkRevision string

// Info is one provenance document.
type Info struct {
	Schema    string    `json:"schema"`    // Schema
	Framework Framework `json:"framework"` // the framework's revision
	Ghostty   Ghostty   `json:"ghostty"`   // the libghostty-vt pin
	Shaders   Shaders   `json:"shaders"`   // the precompiled shader library
	Go        Go        `json:"go"`        // the toolchain and target
}

// Framework identifies the framework's source.
type Framework struct {
	Module   string `json:"module"`             // always "gonativeui"
	Revision string `json:"revision,omitempty"` // the 40-hex git commit; omitted when unknown
	Dirty    *bool  `json:"dirty,omitempty"`    // modified since Revision; omitted when unknown
	Source   string `json:"source,omitempty"`   // "git", "stamp", "ldflags" or "buildinfo"; omitted with Revision
}

// Ghostty identifies the libghostty-vt build the terminal binds.
type Ghostty struct {
	Commit     string    `json:"commit,omitempty"`      // the Ghostty commit the libraries are built from
	ZigVersion string    `json:"zig_version,omitempty"` // the Zig release that builds them
	ABICheck   string    `json:"abi_check,omitempty"`   // how the C ABI is checked: "type-manifest" (term's tests against ghostty_type_json)
	Libraries  []Library `json:"libraries,omitempty"`   // built libraries found in the tree's cache (Read only)
}

// Library is one built libghostty-vt.
type Library struct {
	GOOS   string `json:"goos"`   // the system it is built for
	GOARCH string `json:"goarch"` // the architecture it is built for
	File   string `json:"file"`   // its file name, as bundled
	SHA256 string `json:"sha256"` // of the file, checked against the cache's manifest
}

// Shaders identifies the precompiled Metal library.
type Shaders struct {
	Artifact       string `json:"artifact"`                  // "metallib", or "none": the source is compiled at run time
	MetallibSHA256 string `json:"metallib_sha256,omitempty"` // of the .metallib
	SourceSHA256   string `json:"source_sha256,omitempty"`   // of the shader sources it was compiled from
	Compiler       string `json:"compiler,omitempty"`        // `xcrun metal --version`, first line
	Target         string `json:"target,omitempty"`          // the compile target, e.g. air64-apple-macos14.0
	GeneratedBy    string `json:"generated_by,omitempty"`    // "ci" or "local"
}

// Go identifies the toolchain and target.
type Go struct {
	Version string `json:"version,omitempty"` // runtime.Version() or `go env GOVERSION`
	GOOS    string `json:"goos,omitempty"`    // the target system
	GOARCH  string `json:"goarch,omitempty"`  // the target architecture
}

// abiCheck is Ghostty.ABICheck.
const abiCheck = "type-manifest"

// Compiled is what is compiled into the running binary: the framework
// revision from FrameworkRevision, else from the build info when the
// framework is the main module and the toolchain stamped its commit, else
// none; the Ghostty pin; the shader library embedded on macOS ("none"
// elsewhere, and on macOS until one is generated); the Go toolchain and
// target.
func Compiled() Info {
	info := Info{
		Schema:    Schema,
		Framework: Framework{Module: Module},
		Ghostty:   Ghostty{Commit: ghosttyvt.Commit, ZigVersion: ghosttyvt.ZigVersion, ABICheck: abiCheck},
		Shaders:   Shaders{Artifact: "none"},
		Go:        Go{Version: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH},
	}
	if rev, dirty, ok := parseRevision(FrameworkRevision); ok {
		info.Framework.Revision, info.Framework.Dirty, info.Framework.Source = rev, &dirty, "ldflags"
	} else if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Path == Module {
		var modified *bool
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				m := s.Value == "true"
				modified = &m
			}
		}
		if _, _, ok := parseRevision(rev); ok {
			info.Framework.Revision, info.Framework.Dirty, info.Framework.Source = rev, modified, "buildinfo"
		}
	}
	if runtime.GOOS == "darwin" {
		if m, ok := metallib.Embedded(); ok && m.File != "" {
			info.Shaders = shadersOf(m)
		}
	}
	return info
}

func shadersOf(m metallib.Manifest) Shaders {
	return Shaders{Artifact: "metallib", MetallibSHA256: m.SHA256, SourceSHA256: m.SourceSHA256, Compiler: m.Compiler, Target: m.Target, GeneratedBy: m.GeneratedBy}
}

// parseRevision splits "<40 hex>[-dirty]".
func parseRevision(s string) (rev string, dirty, ok bool) {
	s = strings.TrimSpace(s)
	rev, dirty = strings.CutSuffix(s, "-dirty")
	if len(rev) != 40 || strings.Trim(rev, "0123456789abcdef") != "" {
		return "", false, false
	}
	return rev, dirty, true
}

// StampFile is the file in a framework tree that is not a git checkout (an
// export or an unpacked archive) holding its revision: "<40 hex>", or
// "<40 hex>-dirty" for a modified tree.
const StampFile = ".gnui-revision"

// Read describes the framework source tree at dir, the one an application
// is built from: its revision from git when dir is the top of a git
// checkout (dirty if `git status --porcelain` lists anything), else from
// StampFile; the Ghostty pin in the tree, and the libghostty-vt libraries
// built for it in the tree's cache (.cache/libghostty-vt, written by
// tools/libghostty) if any; the tree's precompiled shader library, checked
// against its manifest and the tree's shader sources; and the Go toolchain
// `go env` reports in dir. It fails when dir has no revision, is not a
// framework tree, or holds a library that is corrupt or stale.
func Read(dir string) (Info, error) {
	info := Info{Schema: Schema, Framework: Framework{Module: Module}}
	var err error
	if info.Framework, err = readFramework(dir); err != nil {
		return Info{}, err
	}
	if info.Ghostty, err = readGhostty(dir); err != nil {
		return Info{}, err
	}
	if info.Shaders, err = readShaders(dir); err != nil {
		return Info{}, err
	}
	info.Go = Go{Version: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	cmd := exec.Command("go", "env", "GOVERSION", "GOOS", "GOARCH")
	cmd.Dir = dir
	if out, err := cmd.Output(); err == nil {
		if f := strings.Fields(string(out)); len(f) == 3 {
			info.Go = Go{Version: f[0], GOOS: f[1], GOARCH: f[2]}
		}
	}
	return info, nil
}

func readFramework(dir string) (Framework, error) {
	f := Framework{Module: Module}
	if top, err := git(dir, "rev-parse", "--show-toplevel"); err == nil && sameDir(top, dir) {
		head, err := git(dir, "rev-parse", "HEAD")
		if err != nil {
			return f, fmt.Errorf("provenance: %s: %w", dir, err)
		}
		st, err := git(dir, "status", "--porcelain")
		if err != nil {
			return f, fmt.Errorf("provenance: %s: %w", dir, err)
		}
		dirty := st != ""
		f.Revision, f.Dirty, f.Source = head, &dirty, "git"
		return f, nil
	}
	b, err := os.ReadFile(filepath.Join(dir, StampFile))
	if err != nil {
		return f, fmt.Errorf("provenance: %s is neither the top of a git checkout nor stamped with %s", dir, StampFile)
	}
	rev, dirty, ok := parseRevision(string(b))
	if !ok {
		return f, fmt.Errorf("provenance: %s: want a 40-hex commit, optionally -dirty", filepath.Join(dir, StampFile))
	}
	f.Revision, f.Dirty, f.Source = rev, &dirty, "stamp"
	return f, nil
}

func git(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}

func sameDir(a, b string) bool {
	fa, errA := os.Stat(a)
	fb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}

func readGhostty(dir string) (Ghostty, error) {
	file := filepath.Join(dir, "internal", "ghosttyvt", "ghosttyvt.go")
	commit, err := goconst.String(file, "Commit")
	if err != nil {
		return Ghostty{}, fmt.Errorf("provenance: not a framework tree: %w", err)
	}
	g := Ghostty{Commit: commit, ABICheck: abiCheck}
	g.ZigVersion, _ = goconst.String(file, "ZigVersion")
	short, _ := goconst.String(file, "Short")
	cache := filepath.Join(dir, ".cache", "libghostty-vt", short)
	b, err := os.ReadFile(filepath.Join(cache, "manifest.json"))
	if short == "" || err != nil {
		return g, nil // none built: the artifacts are not committed
	}
	var m struct {
		Commit    string `json:"commit"`
		Artifacts []struct {
			Target string `json:"target"`
			File   string `json:"file"`
			SHA256 string `json:"sha256"`
		} `json:"artifacts"`
	}
	if json.Unmarshal(b, &m) != nil || m.Commit != commit {
		return g, nil
	}
	for _, a := range m.Artifacts {
		goos, goarch, _ := strings.Cut(a.Target, "/")
		data, err := os.ReadFile(filepath.Join(cache, a.File))
		if err != nil {
			continue // listed but not on disk here
		}
		if h := metallib.Hash(data); h != a.SHA256 {
			return Ghostty{}, fmt.Errorf("provenance: %s has sha256 %s; its manifest says %s", filepath.Join(cache, a.File), h, a.SHA256)
		}
		g.Libraries = append(g.Libraries, Library{GOOS: goos, GOARCH: goarch, File: filepath.Base(a.File), SHA256: a.SHA256})
	}
	return g, nil
}

func readShaders(dir string) (Shaders, error) {
	lib := filepath.Join(dir, "internal", "metallib", "lib")
	b, err := os.ReadFile(filepath.Join(lib, "manifest.json"))
	if err != nil {
		return Shaders{}, fmt.Errorf("provenance: not a framework tree: %w", err)
	}
	m, ok := metallib.Parse(b)
	if !ok {
		return Shaders{}, fmt.Errorf("provenance: %s: not a schema-%d manifest", filepath.Join(lib, "manifest.json"), metallib.Schema)
	}
	if m.File == "" {
		return Shaders{Artifact: "none"}, nil
	}
	data, err := os.ReadFile(filepath.Join(lib, m.File))
	if err != nil {
		return Shaders{}, fmt.Errorf("provenance: %w", err)
	}
	if metallib.Hash(data) != m.SHA256 {
		return Shaders{}, fmt.Errorf("provenance: %s is not the library its manifest names", m.File)
	}
	quad, backdrop, err := mslsrc.Read(filepath.Join(dir, "gpu"))
	if err != nil {
		return Shaders{}, fmt.Errorf("provenance: %w", err)
	}
	if metallib.SourceHash(quad, backdrop) != m.SourceSHA256 {
		return Shaders{}, errors.New("provenance: the precompiled shader library is stale (go run ./tools/metallib -check)")
	}
	return shadersOf(m), nil
}

// JSON is info indented, with a final newline, as `gnui provenance -json`
// prints it.
func (info Info) JSON() []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetIndent("", "  ")
	enc.Encode(info) // cannot fail: plain strings, bools and slices
	return b.Bytes()
}
