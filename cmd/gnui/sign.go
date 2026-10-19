package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// signOptions say how signBundle signs.
type signOptions struct {
	Spec         signSpec
	Hardened     bool   // --options runtime
	Timestamp    bool   // secure timestamp from Apple's server (needs a real identity and the network)
	Entitlements string // entitlements plist for the outermost bundle ("" for none)
	Log          io.Writer
}

// codesignArgs builds the codesign arguments to sign path.
func (o signOptions) codesignArgs(path string, entitlements string) []string {
	args := []string{"--force", "--sign", o.Spec.Identity}
	if o.Spec.Keychain != "" {
		args = append(args, "--keychain", o.Spec.Keychain)
	}
	if o.Hardened {
		args = append(args, "--options", "runtime")
	}
	if o.Timestamp {
		args = append(args, "--timestamp")
	} else {
		args = append(args, "--timestamp=none")
	}
	if entitlements != "" {
		args = append(args, "--entitlements", entitlements)
	}
	return append(args, path)
}

// nestedCode lists the code inside an app bundle that must be signed before
// the bundle itself, deepest first: Mach-O files (other than the main
// executable), and nested .framework, .app, .xpc and .bundle directories.
// Symbolic links are skipped, as are Resources that are not Mach-O.
func nestedCode(app, mainExe string) ([]string, error) {
	var found []string
	err := filepath.WalkDir(app, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == app || d.Type()&fs.ModeSymlink != 0 { // WalkDir does not follow links
			return nil
		}
		if d.IsDir() {
			switch strings.ToLower(filepath.Ext(p)) {
			case ".framework", ".app", ".xpc", ".bundle", ".appex":
				found = append(found, p)
			}
			return nil
		}
		if p == mainExe {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		var head [4]byte
		n, _ := io.ReadFull(f, head[:])
		f.Close()
		if isMachO(head[:n]) {
			found = append(found, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	depth := func(p string) int { return strings.Count(p, string(filepath.Separator)) }
	sort.SliceStable(found, func(i, j int) bool {
		di, dj := depth(found[i]), depth(found[j])
		if di != dj {
			return di > dj
		}
		return found[i] < found[j]
	})
	return found, nil
}

// signBundle signs app inside out: nested code first, then the bundle, which
// is the only signature that carries the entitlements. It returns the signed
// nested paths.
func signBundle(app string, o signOptions) ([]string, error) {
	app = filepath.Clean(app)
	mainExe, err := bundleExecutable(app)
	if err != nil {
		return nil, err
	}
	nested, err := nestedCode(app, mainExe)
	if err != nil {
		return nil, err
	}
	for _, p := range nested {
		if out, err := runOut("codesign", o.codesignArgs(p, "")...); err != nil {
			return nil, fmt.Errorf("signing %s: %w", p, err)
		} else if o.Log != nil && strings.TrimSpace(out) != "" {
			fmt.Fprint(o.Log, out)
		}
	}
	if _, err := runOut("codesign", o.codesignArgs(app, o.Entitlements)...); err != nil {
		return nil, fmt.Errorf("signing %s: %w", app, err)
	}
	return nested, nil
}

// bundleExecutable returns the path of the bundle's main executable, from
// its Info.plist.
func bundleExecutable(app string) (string, error) {
	out, err := runOut("plutil", "-extract", "CFBundleExecutable", "raw", "-o", "-", filepath.Join(app, "Contents", "Info.plist"))
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(out)
	if name == "" {
		return "", fmt.Errorf("%s: no CFBundleExecutable", app)
	}
	return filepath.Join(app, "Contents", "MacOS", name), nil
}

// verifyBundle runs codesign's strict deep verification.
func verifyBundle(app string) error {
	_, err := runOut("codesign", "--verify", "--deep", "--strict", "--verbose=2", app)
	return err
}

// designatedRequirement returns the bundle's designated requirement text.
func designatedRequirement(app string) (string, error) {
	out, err := runOut("codesign", "-d", "-r-", app)
	if err != nil {
		return "", err
	}
	for _, l := range strings.Split(out, "\n") {
		// codesign writes "# designated => …" for an implicit requirement
		// (an ad-hoc signature's cdhash) and "designated => …" for a written one.
		l = strings.TrimPrefix(l, "# ")
		if strings.HasPrefix(l, "designated => ") {
			return strings.TrimPrefix(l, "designated => "), nil
		}
	}
	return "", fmt.Errorf("no designated requirement in codesign output:\n%s", out)
}
