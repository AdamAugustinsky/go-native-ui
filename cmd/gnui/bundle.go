package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// buildOptions say how buildBundle assembles an application bundle.
type buildOptions struct {
	Dev     bool   // development variant: bundle id plus ".dev", symbols kept
	Tags    string // go build -tags
	Ldflags string // extra go build -ldflags, e.g. "-X main.version=1.2"
	Out     string // path of the .app to write (replaced if it exists)
	Log     io.Writer
	Verbose bool
}

// darwinArch is the one architecture gnui builds: macOS means Apple silicon
// (DECISIONS.md D55).
const darwinArch = "arm64"

// goTool returns the go command: $GNUI_GO, or go from PATH.
func goTool() (string, error) {
	if g := os.Getenv("GNUI_GO"); g != "" {
		return g, nil
	}
	return exec.LookPath("go")
}

// releaseTag is the build tag of release binaries: it takes the debug overlay's
// Debug menu, chords and inspector panel out of the app (package app,
// debug_release.go). gnui package always sets it; gnui run, a development
// bundle, and a plain go build do not.
const releaseTag = "gnui_release"

// buildArgs are the arguments of go build for one executable: a release build
// (not Dev) is trimmed, stripped and tagged gnui_release, besides the tags the
// caller asked for.
func buildArgs(c *Config, out string, o buildOptions) []string {
	args := []string{"build"}
	ld := o.Ldflags
	tags := o.Tags
	if !o.Dev {
		args = append(args, "-trimpath")
		ld = strings.TrimSpace("-s -w " + ld)
		if !hasTag(tags, releaseTag) {
			tags = strings.Trim(tags+","+releaseTag, ",")
		}
	}
	if ld != "" {
		args = append(args, "-ldflags="+ld)
	}
	if tags != "" {
		args = append(args, "-tags", tags)
	}
	return append(args, "-o", out, c.Main)
}

// hasTag reports whether the -tags list (comma or space separated) has tag.
func hasTag(list, tag string) bool {
	for _, t := range strings.FieldsFunc(list, func(r rune) bool { return r == ',' || r == ' ' }) {
		if t == tag {
			return true
		}
	}
	return false
}

// buildExecutable builds the main package for darwin/arm64 with cgo off.
func buildExecutable(c *Config, out string, o buildOptions) error {
	goBin, err := goTool()
	if err != nil {
		return fmt.Errorf("go not found: put the Go toolchain on PATH or set GNUI_GO (%w)", err)
	}
	args := buildArgs(c, out, o)
	cmd := exec.Command(goBin, args...)
	cmd.Dir = c.dir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=darwin", "GOARCH="+darwinArch)
	if outp, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go build (darwin/%s) failed:\n%s", darwinArch, strings.TrimSpace(string(outp)))
	}
	return nil
}

// buildBundle writes the application bundle for c to o.Out and returns the
// path of a generated entitlements file ("" when the configuration has no
// entitlements); the file lives in a temporary directory the caller removes
// through the returned cleanup function.
func buildBundle(c *Config, o buildOptions) (entitlements string, cleanup func(), err error) {
	cleanup = func() {}
	logf := func(f string, a ...any) {
		if o.Log != nil {
			fmt.Fprintf(o.Log, f+"\n", a...)
		}
	}
	if err := os.RemoveAll(o.Out); err != nil {
		return "", cleanup, err
	}
	contents := filepath.Join(o.Out, "Contents")
	macos := filepath.Join(contents, "MacOS")
	resources := filepath.Join(contents, "Resources")
	for _, d := range []string{macos, resources} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return "", cleanup, err
		}
	}
	tmp, err := os.MkdirTemp("", "gnui-build-*")
	if err != nil {
		return "", cleanup, err
	}
	cleanup = func() { os.RemoveAll(tmp) }
	defer func() {
		if err != nil {
			cleanup()
			cleanup = func() {}
		}
	}()

	// 1. executable
	logf("building darwin/%s", darwinArch)
	exe, err := filepath.Abs(filepath.Join(macos, c.Executable)) // go build runs in the config's directory
	if err != nil {
		return "", cleanup, err
	}
	if err := buildExecutable(c, exe, o); err != nil {
		return "", cleanup, err
	}

	// 2. Info.plist
	info, err := encodePlist(infoPlist(c, o.Dev))
	if err != nil {
		return "", cleanup, fmt.Errorf("Info.plist: %w", err)
	}
	infoPath := filepath.Join(contents, "Info.plist")
	if err := os.WriteFile(infoPath, info, 0o644); err != nil {
		return "", cleanup, err
	}
	if err := lintPlist(infoPath); err != nil {
		return "", cleanup, err
	}

	// 3. icon
	if c.Icon != "" {
		icns, err := makeICNS(c.Path(c.Icon))
		if err != nil {
			return "", cleanup, err
		}
		if err := os.WriteFile(filepath.Join(resources, "AppIcon.icns"), icns, 0o644); err != nil {
			return "", cleanup, err
		}
	}

	// 4. resources and frameworks
	for _, r := range c.Resources {
		if err := copyInto(resources, r.ResourceDest(), c.Path(r.From)); err != nil {
			return "", cleanup, fmt.Errorf("resource %s: %w", r.From, err)
		}
	}
	if len(c.Frameworks) > 0 {
		fw := filepath.Join(contents, "Frameworks")
		if err := os.MkdirAll(fw, 0o755); err != nil {
			return "", cleanup, err
		}
		for _, f := range c.Frameworks {
			if err := copyInto(fw, filepath.Base(filepath.Clean(f)), c.Path(f)); err != nil {
				return "", cleanup, fmt.Errorf("framework %s: %w", f, err)
			}
		}
	}

	// 5. login agents
	if len(c.LoginAgents) > 0 {
		dir := filepath.Join(contents, "Library", "LaunchAgents")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", cleanup, err
		}
		for _, a := range c.LoginAgents {
			data, err := encodePlist(agentPlist(c, a, o.Dev))
			if err != nil {
				return "", cleanup, err
			}
			p := filepath.Join(dir, c.AgentLabel(a, o.Dev)+".plist")
			if err := os.WriteFile(p, data, 0o644); err != nil {
				return "", cleanup, err
			}
			if err := lintPlist(p); err != nil {
				return "", cleanup, err
			}
		}
	}

	// 6. entitlements, outside the bundle
	if ent := entitlementsPlist(c); ent != nil {
		data, err := encodePlist(ent)
		if err != nil {
			return "", cleanup, fmt.Errorf("entitlements: %w", err)
		}
		entitlements = filepath.Join(tmp, "entitlements.plist")
		if err := os.WriteFile(entitlements, data, 0o644); err != nil {
			return "", cleanup, err
		}
		if err := lintPlist(entitlements); err != nil {
			return "", cleanup, err
		}
	}
	return entitlements, cleanup, nil
}

// lintPlist runs plutil -lint where plutil exists (macOS).
func lintPlist(path string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	if _, err := runOut("plutil", "-lint", path); err != nil {
		return fmt.Errorf("generated property list is invalid: %w", err)
	}
	return nil
}

// containedPath returns root/rel for a destination inside a bundle
// directory, or an error if that path could reach outside it. rel must be a
// clean relative path (no "." or ".." components), and no component of it
// that already exists under root may be a symbolic link, so a delete or a
// write at the result cannot be redirected by a link that an earlier copy put
// there. root itself is trusted: it is a directory this run created.
func containedPath(root, rel string) (string, error) {
	if err := checkRelPath(rel); err != nil {
		return "", err
	}
	cur := root
	parts := strings.Split(filepath.ToSlash(filepath.Clean(rel)), "/")
	for i, part := range parts {
		cur = filepath.Join(cur, part)
		st, err := os.Lstat(cur)
		if errors.Is(err, fs.ErrNotExist) {
			break // nothing below exists yet; MkdirAll will create plain directories
		}
		if err != nil {
			return "", err
		}
		if st.Mode()&fs.ModeSymlink != 0 {
			return "", fmt.Errorf("%q passes through a symbolic link at %q; refusing to write through it", rel, strings.Join(parts[:i+1], "/"))
		}
		if i < len(parts)-1 && !st.IsDir() {
			return "", fmt.Errorf("%q: %q is a file, not a directory", rel, strings.Join(parts[:i+1], "/"))
		}
	}
	return filepath.Join(root, filepath.Clean(rel)), nil
}

// copyInto copies src to root/rel after containedPath has approved the destination.
func copyInto(root, rel, src string) error {
	dst, err := containedPath(root, rel)
	if err != nil {
		return err
	}
	return copyTree(src, dst)
}

// inside reports whether p is root or below it (both absolute and clean).
func inside(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// checkLink approves a symbolic link met while copying a tree whose real root
// is realRoot: the target must be relative and must stay inside the tree
// (links like Versions/Current -> A do; "/etc" or "../../x" do not). A link
// that resolves is judged by where it ends up, so a chain of links cannot
// leave the tree either; a dangling one by its lexical target.
func checkLink(realRoot, p, target string) error {
	if filepath.IsAbs(target) {
		return fmt.Errorf("symbolic link %s -> %s is absolute", p, target)
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		if !inside(realRoot, resolved) {
			return fmt.Errorf("symbolic link %s -> %s leaves the copied tree (resolves to %s)", p, target, resolved)
		}
		return nil
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(p))
	if err != nil {
		return err
	}
	if !inside(realRoot, filepath.Join(dir, target)) {
		return fmt.Errorf("symbolic link %s -> %s leaves the copied tree", p, target)
	}
	return nil
}

// copyTree copies a file or a directory tree to dst, keeping modes and the
// symbolic links that stay inside the tree; any other link is an error.
func copyTree(src, dst string) error {
	st, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if st.Mode()&fs.ModeSymlink != 0 {
		// a link given directly: copy what it points to, judged as its own tree
		real, err := filepath.EvalSymlinks(src)
		if err != nil {
			return err
		}
		return copyTree(real, dst)
	}
	var realRoot string
	if st.IsDir() {
		if realRoot, err = filepath.EvalSymlinks(src); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if !st.IsDir() {
		return copyFile(src, dst, st.Mode().Perm())
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		case info.Mode()&fs.ModeSymlink != 0:
			l, err := os.Readlink(p)
			if err != nil {
				return err
			}
			if err := checkLink(realRoot, p, l); err != nil {
				return err
			}
			return os.Symlink(l, target)
		case info.Mode().IsRegular():
			return copyFile(p, target, info.Mode().Perm())
		}
		return nil // sockets, devices
	})
}

func copyFile(src, dst string, perm fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
