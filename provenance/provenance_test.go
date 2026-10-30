package provenance

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gonativeui/internal/ghosttyvt"
	"gonativeui/internal/metallib"
)

const (
	testCommit = "0123456789abcdef0123456789abcdef01234567"
	testShort  = "012345678"
)

// frameworkTree writes the parts of a framework tree Read looks at into a
// new directory: the Ghostty pin, a manifest naming no shader library and
// the shader sources.
func frameworkTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, "internal/ghosttyvt/ghosttyvt.go", "package ghosttyvt\n\nconst Commit = \""+testCommit+"\"\n\nconst ZigVersion = \"0.17.0\"\n\nconst Short = \""+testShort+"\"\n")
	write(t, dir, "internal/metallib/lib/manifest.json", `{"schema": 1}`)
	write(t, dir, "gpu/shaders_darwin.go", "package gpu\n\nconst quadMSL = `quad`\n")
	write(t, dir, "gpu/backdrop_darwin.go", "package gpu\n\nconst backdropMSL = `backdrop`\n")
	return dir
}

func write(t *testing.T, dir, name, s string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// A git checkout's revision is its HEAD, dirty once anything is modified
// or added; the Ghostty pin is the tree's, and no shader library is "none".
func TestReadGitCheckout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := frameworkTree(t)
	run(t, dir, "git", "init", "-q")
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "tree")
	head := run(t, dir, "git", "rev-parse", "HEAD")
	info, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	f := info.Framework
	if f.Revision != head || f.Dirty == nil || *f.Dirty || f.Source != "git" {
		t.Fatalf("clean checkout: framework %+v, want %s, clean, from git", f, head)
	}
	if info.Ghostty.Commit != testCommit || info.Ghostty.ZigVersion != "0.17.0" || info.Ghostty.ABICheck != "type-manifest" || info.Shaders.Artifact != "none" {
		t.Fatalf("clean checkout: %+v", info)
	}
	write(t, dir, "gpu/shaders_darwin.go", "package gpu\n\nconst quadMSL = `quad, edited`\n")
	if info, err = Read(dir); err != nil {
		t.Fatal(err)
	}
	if f := info.Framework; f.Revision != head || f.Dirty == nil || !*f.Dirty {
		t.Fatalf("modified checkout: framework %+v, want %s, dirty", f, head)
	}
}

// A tree that is not a git checkout has its revision from the stamp file;
// built Ghostty libraries in its cache are listed; a shader library whose
// sources changed is refused.
func TestReadStampedTree(t *testing.T) {
	dir := frameworkTree(t)
	if _, err := Read(dir); err == nil {
		t.Fatal("Read of a tree with neither git nor a stamp succeeded")
	}
	write(t, dir, StampFile, testCommit+"-dirty\n")
	lib := []byte("a dylib")
	cache := ".cache/libghostty-vt/" + testShort + "/"
	write(t, dir, cache+"darwin-arm64/libghostty-vt.dylib", string(lib))
	write(t, dir, cache+"manifest.json", `{"commit": "`+testCommit+`", "artifacts": [
		{"target": "darwin/arm64", "file": "darwin-arm64/libghostty-vt.dylib", "sha256": "`+metallib.Hash(lib)+`"},
		{"target": "linux/amd64", "file": "linux-amd64/libghostty-vt.so", "sha256": "00"}]}`)
	info, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if f := info.Framework; f.Revision != testCommit || f.Dirty == nil || !*f.Dirty || f.Source != "stamp" {
		t.Fatalf("framework %+v, want the stamp's commit, dirty", f)
	}
	want := []Library{{GOOS: "darwin", GOARCH: "arm64", File: "libghostty-vt.dylib", SHA256: metallib.Hash(lib)}}
	if got := info.Ghostty.Libraries; len(got) != 1 || got[0] != want[0] {
		t.Fatalf("libraries %+v, want %+v (the one on disk)", got, want)
	}

	shaders := []byte("MTLB")
	write(t, dir, "internal/metallib/lib/shaders.metallib", string(shaders))
	manifest := func(sources string) string {
		b, _ := json.Marshal(metallib.Manifest{Schema: 1, File: "shaders.metallib", Bytes: 4, SHA256: metallib.Hash(shaders), SourceSHA256: sources, Compiler: "Apple metal version 1", Target: "air64-apple-macos14.0", GeneratedBy: "ci"})
		return string(b)
	}
	write(t, dir, "internal/metallib/lib/manifest.json", manifest(metallib.SourceHash("quad", "backdrop")))
	if info, err = Read(dir); err != nil {
		t.Fatal(err)
	}
	if s := info.Shaders; s.Artifact != "metallib" || s.MetallibSHA256 != metallib.Hash(shaders) || s.GeneratedBy != "ci" {
		t.Fatalf("shaders %+v", s)
	}
	write(t, dir, "internal/metallib/lib/manifest.json", manifest(metallib.SourceHash("quad, before", "backdrop")))
	if _, err := Read(dir); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("Read with a stale shader library: %v, want an error", err)
	}
}

// A binary linked with -X gonativeui/provenance.FrameworkRevision reports
// the revision the packager gave, from ldflags, beside what is compiled in.
func TestCompiledWithLdflags(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a program")
	}
	bin := filepath.Join(t.TempDir(), "compiled")
	if runtime.GOOS == "windows" {
		bin += ".exe" // Windows runs only a file with its extension
	}
	for _, c := range []struct {
		x     string
		rev   string
		dirty bool
	}{{testCommit, testCommit, false}, {testCommit + "-dirty", testCommit, true}, {"not-a-hash", "", false}} {
		run(t, ".", "go", "build", "-o", bin, "-ldflags", "-X gonativeui/provenance.FrameworkRevision="+c.x, "./testdata/compiled")
		var info Info
		if err := json.Unmarshal([]byte(run(t, ".", bin)), &info); err != nil {
			t.Fatal(err)
		}
		f := info.Framework
		if c.rev == "" {
			if f.Source == "ldflags" {
				t.Errorf("-X %s: framework %+v, want the value ignored", c.x, f)
			}
			continue
		}
		if f.Revision != c.rev || f.Dirty == nil || *f.Dirty != c.dirty || f.Source != "ldflags" {
			t.Errorf("-X %s: framework %+v, want %s, dirty %v, from ldflags", c.x, f, c.rev, c.dirty)
		}
		if info.Schema != Schema || info.Ghostty.Commit != ghosttyvt.Commit || info.Go.GOOS != runtime.GOOS || info.Shaders.Artifact == "" {
			t.Errorf("-X %s: %+v", c.x, info)
		}
	}
}
