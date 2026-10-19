package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// doctor collects check results.
type doctor struct {
	w     io.Writer
	fails int
	warns int
}

func (d *doctor) ok(format string, a ...any) {
	fmt.Fprintf(d.w, "ok    %s\n", fmt.Sprintf(format, a...))
}
func (d *doctor) info(format string, a ...any) {
	fmt.Fprintf(d.w, "info  %s\n", fmt.Sprintf(format, a...))
}
func (d *doctor) warn(format string, a ...any) {
	d.warns++
	fmt.Fprintf(d.w, "warn  %s\n", fmt.Sprintf(format, a...))
}
func (d *doctor) fail(format string, a ...any) {
	d.fails++
	fmt.Fprintf(d.w, "FAIL  %s\n", fmt.Sprintf(format, a...))
}

func cmdDoctor(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("doctor", stderr)
	cfgPath := fs.String("c", "gnui.json", "path of a gnui.json to check (skipped when the file does not exist)")
	setup := fs.Bool("setup-dev-cert", false, "create the gnui-dev signing identity if it does not exist")
	searchList := fs.Bool("search-list", false, "add the gnui-dev keychain to your user keychain search list (macOS 15 needs it for codesign to find the key)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	d := &doctor{w: stdout}
	if runtime.GOOS != "darwin" {
		d.fail("gnui package, run and sign need macOS; this is %s/%s", runtime.GOOS, runtime.GOARCH)
		return fmt.Errorf("%d check(s) failed", d.fails)
	}

	// toolchain
	if v, err := runOut("sw_vers", "-productVersion"); err == nil {
		d.ok("macOS %s on %s", strings.TrimSpace(v), runtime.GOARCH)
	}
	if goBin, err := goTool(); err != nil {
		d.fail("go not found: put the Go toolchain on PATH or set GNUI_GO")
	} else if v, err := runOut(goBin, "version"); err != nil {
		d.fail("go does not run: %v", err)
	} else {
		d.ok("%s (cgo is always off for gnui builds)", strings.TrimSpace(v))
	}
	for _, t := range []string{"codesign", "security", "plutil", "hdiutil", "spctl", "ditto", "open"} {
		if p, err := lookPathAbs(t); err != nil {
			d.fail("%s not found (ships with macOS)", t)
		} else {
			d.ok("%s: %s", t, p)
		}
	}
	if _, err := runOut("xcrun", "--find", "notarytool"); err != nil {
		d.warn("xcrun notarytool not found: notarizing needs the Xcode Command Line Tools (xcode-select --install)")
	} else if _, err := runOut("xcrun", "--find", "stapler"); err != nil {
		d.warn("xcrun stapler not found: stapling needs the Xcode Command Line Tools")
	} else {
		d.ok("notarytool and stapler (Command Line Tools)")
	}

	// gnui.json
	if _, err := os.Stat(*cfgPath); err == nil {
		if c, err := LoadConfig(*cfgPath); err != nil {
			d.fail("%v", err)
		} else {
			d.ok("%s: %s (%s), main %s", *cfgPath, c.Name, c.ID, c.Main)
			if c.Icon != "" {
				if _, err := os.Stat(c.Path(c.Icon)); err != nil {
					d.fail("icon %s: %v", c.Icon, err)
				}
			}
			for _, r := range c.Resources {
				if _, err := os.Stat(c.Path(r.From)); err != nil {
					d.fail("resource %s: %v", r.From, err)
				}
			}
			for _, f := range c.Frameworks {
				if _, err := os.Stat(c.Path(f)); err != nil {
					d.fail("framework %s: %v", f, err)
				}
			}
			if len(c.Usage) == 0 {
				d.info("no usage strings in gnui.json: an app that asks for the camera, microphone, location… needs them")
			}
		}
	} else if *cfgPath != "gnui.json" {
		d.fail("%s: %v", *cfgPath, err)
	} else {
		d.info("no gnui.json in this directory")
	}

	// development identity
	if *setup {
		created, err := setupDevIdentity(func(f string, a ...any) { fmt.Fprintf(stdout, "      "+f+"\n", a...) })
		if err != nil {
			d.fail("creating the %s identity: %v", devIdentityName, err)
		} else if created {
			d.ok("created the %s identity in %s", devIdentityName, devKeychainPath())
		}
	}
	if *searchList {
		if err := addToSearchList(); err != nil {
			d.fail("adding %s to the keychain search list: %v", filepath.Base(devKeychainPath()), err)
		} else {
			d.ok("%s is on the user keychain search list", filepath.Base(devKeychainPath()))
		}
	}
	d.checkDevIdentity()

	// release identity
	if ids, err := listIdentities(""); err == nil {
		var found []string
		for _, i := range ids {
			if strings.HasPrefix(i.Name, developerIDPrefix) {
				state := "valid"
				if !i.Valid {
					state = i.Status
				}
				found = append(found, fmt.Sprintf("%s [%s]", i.Name, state))
			}
		}
		if len(found) == 0 {
			d.warn("no Developer ID Application identity in the keychains: gnui package signs ad-hoc and cannot notarize or build a signed dmg (needed only for distribution)")
		} else {
			for _, f := range found {
				d.ok("release identity: %s", f)
			}
		}
	}

	fmt.Fprintf(stdout, "\n%d failed, %d warnings\n", d.fails, d.warns)
	if d.fails > 0 {
		return fmt.Errorf("%d check(s) failed", d.fails)
	}
	return nil
}

func lookPathAbs(name string) (string, error) {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", errors.New("not found")
}

// checkDevIdentity reports on the gnui-dev identity: existence, trust,
// validity period, and a real signing round trip.
func (d *doctor) checkDevIdentity() {
	kc := devKeychainPath()
	id, err := findDevIdentity()
	switch {
	case err != nil:
		d.fail("%s identity: %v", devIdentityName, err)
		return
	case id == nil:
		d.warn("%s identity not set up (gnui run needs it): run `gnui doctor -setup-dev-cert`", devIdentityName)
		return
	}
	d.ok("%s identity %s in %s", devIdentityName, id.Hash, kc)

	if pemSum, nb, na, err := certInfo(devCertPath()); err == nil {
		switch {
		case pemSum != id.Hash:
			d.warn("%s does not match the identity in the keychain (%s)", devCertPath(), pemSum)
		case time.Now().Before(nb):
			d.fail("%s certificate is not valid before %s", devIdentityName, nb.Format("2006-01-02"))
		case time.Now().After(na):
			d.fail("%s certificate expired %s: delete %s and run `gnui doctor -setup-dev-cert` (privacy grants must be given again)", devIdentityName, na.Format("2006-01-02"), kc)
		case time.Until(na) < 30*24*time.Hour:
			d.warn("%s certificate expires %s", devIdentityName, na.Format("2006-01-02"))
		default:
			d.ok("%s certificate valid until %s", devIdentityName, na.Format("2006-01-02"))
		}
	} else {
		d.info("no exported certificate at %s (%v)", devCertPath(), err)
	}

	if id.Valid {
		d.ok("%s is trusted for code signing", devIdentityName)
	} else {
		d.info("%s is not marked trusted for code signing (%s). That is fine for signing and for the designated requirement. "+
			"Only if a privacy grant (Screen Recording, Documents…) does not survive a rebuild, trust it once, at the keyboard of this Mac "+
			"(macOS asks for the account password):\n      %s",
			devIdentityName, id.Status, trustCommand())
	}

	if err := signRoundTrip(); err != nil {
		d.fail("signing with %s: %v", devIdentityName, err)
	} else {
		d.ok("codesign signs and verifies with %s without a prompt", devIdentityName)
	}
}

// signRoundTrip signs a throw-away copy of a system executable with the
// development identity and verifies it.
func signRoundTrip() error {
	spec, err := devSignSpec()
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "gnui-doctor-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	p := filepath.Join(dir, "probe")
	if err := copyFile("/usr/bin/true", p, 0o755); err != nil {
		return err
	}
	o := signOptions{Spec: spec}
	if _, err := runOut("codesign", o.codesignArgs(p, "")...); err != nil {
		return searchListHint(err, spec)
	}
	_, err = runOut("codesign", "--verify", "--strict", p)
	return err
}
