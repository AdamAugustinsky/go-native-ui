package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const developerIDPrefix = "Developer ID Application:"

// signingPlan is the identity a release package is signed with and what that
// identity allows.
type signingPlan struct {
	Spec      signSpec
	Real      bool // a Developer ID (or other Apple-issued) identity: timestamp, notarization possible
	Developer bool // the identity name starts with "Developer ID Application:"
}

func cmdPackage(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("package", stderr)
	cfgPath := fs.String("c", "gnui.json", "path of the gnui.json")
	outDir := fs.String("o", "dist", "output directory")
	arch := fs.String("arch", "universal", "universal, arm64 or amd64")
	identity := fs.String("identity", "", `signing identity (name or SHA-1), or "-" for ad-hoc; default $GNUI_SIGN_IDENTITY, then a "Developer ID Application" identity from the keychains, then ad-hoc`)
	devCert := fs.Bool("dev-cert", false, "sign with the gnui-dev identity instead (a bundle for this Mac and the Macs that were given the same certificate)")
	noHardened := fs.Bool("no-hardened", false, "do not enable the hardened runtime")
	noTimestamp := fs.Bool("no-timestamp", false, "do not ask Apple's server for a secure timestamp")
	notarize := fs.Bool("notarize", false, "notarize and staple (needs a Developer ID identity and -notary-profile)")
	profile := fs.String("notary-profile", os.Getenv("GNUI_NOTARY_PROFILE"), "notarytool keychain profile (xcrun notarytool store-credentials); default $GNUI_NOTARY_PROFILE")
	dmg := fs.Bool("dmg", false, "also build a .dmg (signed and notarized when the identity allows)")
	tags := fs.String("tags", "", "go build -tags")
	ldflags := fs.String("ldflags", "", `extra go build -ldflags ("-s -w" is always added)`)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageError{fmt.Errorf("unexpected argument %q", fs.Arg(0))}
	}
	var archs []string
	switch *arch {
	case "universal":
		archs = []string{"arm64", "amd64"}
	case "arm64", "amd64":
		archs = []string{*arch}
	default:
		return usageError{fmt.Errorf("-arch %q must be universal, arm64 or amd64", *arch)}
	}
	if runtime.GOOS != "darwin" {
		return errors.New("packaging and signing need macOS")
	}
	c, err := LoadConfig(*cfgPath)
	if err != nil {
		return err
	}
	plan, err := planSigning(*identity, *devCert)
	if err != nil {
		return err
	}
	if *notarize {
		if !plan.Developer {
			return needIdentityError{fmt.Errorf("-notarize needs a %q identity in a keychain, and none was selected (signing with %s).\n%s", developerIDPrefix, plan.Spec.Label, appleIdentityHelp)}
		}
		if *profile == "" {
			return needIdentityError{fmt.Errorf("-notarize needs notarytool credentials: pass -notary-profile <name> or set GNUI_NOTARY_PROFILE.\n%s", appleIdentityHelp)}
		}
	}

	app := filepath.Join(*outDir, c.Name+".app")
	fmt.Fprintf(stdout, "packaging %s %s (%s) -> %s\n", c.Name, c.Version, c.ID, app)
	ent, cleanup, err := buildBundle(c, buildOptions{Archs: archs, Tags: *tags, Ldflags: *ldflags, Out: app, Log: stdout})
	if err != nil {
		return err
	}
	defer cleanup()

	opts := signOptions{
		Spec:         plan.Spec,
		Hardened:     !*noHardened,
		Timestamp:    plan.Real && !*noTimestamp,
		Entitlements: ent,
	}
	fmt.Fprintf(stdout, "signing with %s%s\n", plan.Spec.Label, runtimeNote(opts))
	if _, err := signBundle(app, opts); err != nil {
		return err
	}
	if err := verifyBundle(app); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "verified: codesign --verify --deep --strict")
	if dr, err := designatedRequirement(app); err == nil {
		fmt.Fprintf(stdout, "designated requirement: %s\n", dr)
	}

	var skipped []string
	if *notarize {
		if err := notarizeAndStaple(app, *profile, stdout); err != nil {
			return err
		}
		if out, err := runOut("spctl", "--assess", "--type", "execute", "-vv", app); err != nil {
			return fmt.Errorf("Gatekeeper rejects the notarized app: %w", err)
		} else {
			fmt.Fprint(stdout, strings.TrimSpace(out), "\n")
		}
	} else if plan.Developer {
		skipped = append(skipped, "notarize and staple: not requested (add -notarize -notary-profile <name>)")
	} else {
		skipped = append(skipped, "notarize and staple: needs a Developer ID Application identity and notarytool credentials")
	}

	if *dmg {
		path, err := buildDMG(c, app, *outDir, plan, !*noTimestamp, *notarize, *profile, stdout)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "dmg: %s\n", path)
		if !plan.Developer {
			skipped = append(skipped, "dmg signature and notarization: needs a Developer ID Application identity; the disk image is unsigned")
		}
	}

	fmt.Fprintf(stdout, "built %s\n", app)
	if !plan.Developer {
		fmt.Fprintf(stdout, "\nnote: signed with %s, not a Developer ID: Gatekeeper rejects this bundle on other Macs.\n", plan.Spec.Label)
	}
	for _, s := range skipped {
		fmt.Fprintf(stdout, "skipped: %s\n", s)
	}
	if len(skipped) > 0 && !plan.Developer {
		fmt.Fprintf(stdout, "\n%s\n", appleIdentityHelp)
	}
	return nil
}

const appleIdentityHelp = `To distribute outside this Mac you need your Apple Developer ID:
  1. In Xcode (Settings > Accounts) or at developer.apple.com create a "Developer ID Application" certificate and install it in your keychain.
  2. Store notarization credentials once:  xcrun notarytool store-credentials <profile> --apple-id <you> --team-id <TEAMID>
  3. gnui package -identity "Developer ID Application: Name (TEAMID)" -notarize -notary-profile <profile> -dmg`

func runtimeNote(o signOptions) string {
	var parts []string
	if o.Hardened {
		parts = append(parts, "hardened runtime")
	}
	if o.Timestamp {
		parts = append(parts, "secure timestamp")
	}
	if o.Entitlements != "" {
		parts = append(parts, "entitlements")
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

// planSigning picks the identity for a release package.
func planSigning(flagIdentity string, devCert bool) (signingPlan, error) {
	if devCert {
		spec, err := devSignSpec()
		if err != nil {
			return signingPlan{}, err
		}
		return signingPlan{Spec: spec}, nil
	}
	id := flagIdentity
	if id == "" {
		id = os.Getenv("GNUI_SIGN_IDENTITY")
	}
	switch id {
	case "-":
		return signingPlan{Spec: signSpec{Identity: "-", Label: "an ad-hoc signature"}}, nil
	case "":
		ids, err := listIdentities("")
		if err == nil {
			for _, i := range ids {
				if i.Valid && strings.HasPrefix(i.Name, developerIDPrefix) {
					return signingPlan{Spec: signSpec{Identity: i.Hash, Label: i.Name}, Real: true, Developer: true}, nil
				}
			}
		}
		return signingPlan{Spec: signSpec{Identity: "-", Label: "an ad-hoc signature (no Developer ID Application identity found)"}}, nil
	}
	return signingPlan{
		Spec:      signSpec{Identity: id, Label: id},
		Real:      true,
		Developer: strings.HasPrefix(id, developerIDPrefix),
	}, nil
}

// notarizeAndStaple submits path (an .app, zipped first, or a .dmg) to Apple's
// notary service, waits, and staples the ticket.
func notarizeAndStaple(path, profile string, out io.Writer) error {
	submit := path
	if strings.HasSuffix(path, ".app") {
		zip := path + ".zip"
		defer os.Remove(zip)
		if _, err := runOut("ditto", "-c", "-k", "--keepParent", path, zip); err != nil {
			return err
		}
		submit = zip
	}
	fmt.Fprintf(out, "notarizing %s (this waits for Apple)\n", filepath.Base(path))
	text, err := runOut("xcrun", "notarytool", "submit", submit, "--keychain-profile", profile, "--wait")
	fmt.Fprint(out, text)
	if err != nil {
		return err
	}
	if !strings.Contains(text, "status: Accepted") {
		return fmt.Errorf("notarization of %s was not accepted; see `xcrun notarytool log <id> --keychain-profile %s`", filepath.Base(path), profile)
	}
	if _, err := runOut("xcrun", "stapler", "staple", path); err != nil {
		return err
	}
	if _, err := runOut("xcrun", "stapler", "validate", path); err != nil {
		return err
	}
	fmt.Fprintf(out, "stapled %s\n", filepath.Base(path))
	return nil
}

// buildDMG writes <Name>-<version>.dmg next to the app with the app and an
// Applications link in it, signs it when the identity is a Developer ID, and
// notarizes and staples it when asked.
func buildDMG(c *Config, app, outDir string, plan signingPlan, timestamp, notarize bool, profile string, out io.Writer) (string, error) {
	stage, err := os.MkdirTemp("", "gnui-dmg-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	if err := copyTree(app, filepath.Join(stage, filepath.Base(app))); err != nil {
		return "", err
	}
	if err := os.Symlink("/Applications", filepath.Join(stage, "Applications")); err != nil {
		return "", err
	}
	dmg := filepath.Join(outDir, fmt.Sprintf("%s-%s.dmg", strings.ReplaceAll(c.Name, " ", ""), c.Version))
	os.Remove(dmg)
	if _, err := runOut("hdiutil", "create", "-volname", c.Name, "-srcfolder", stage, "-ov", "-format", "UDZO", dmg); err != nil {
		return "", err
	}
	if plan.Developer {
		args := []string{"--force", "--sign", plan.Spec.Identity}
		if timestamp {
			args = append(args, "--timestamp")
		}
		if _, err := runOut("codesign", append(args, dmg)...); err != nil {
			return "", err
		}
		if notarize {
			if err := notarizeAndStaple(dmg, profile, out); err != nil {
				return "", err
			}
		}
	}
	return dmg, nil
}

// cmdSign signs an existing bundle, nested code first.
func cmdSign(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("sign", stderr)
	identity := fs.String("identity", "", `identity name or SHA-1, "-" for ad-hoc; default: the gnui-dev identity`)
	adhoc := fs.Bool("adhoc", false, "sign ad-hoc")
	hardened := fs.Bool("hardened", false, "enable the hardened runtime")
	ent := fs.String("entitlements", "", "entitlements plist for the bundle")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 || !strings.HasSuffix(strings.TrimSuffix(fs.Arg(0), "/"), ".app") {
		return usageError{errors.New("give exactly one path ending in .app")}
	}
	if runtime.GOOS != "darwin" {
		return errors.New("signing needs macOS")
	}
	app := strings.TrimSuffix(fs.Arg(0), "/")
	if _, err := os.Stat(app); err != nil {
		return err
	}
	var spec signSpec
	switch {
	case *adhoc:
		spec = signSpec{Identity: "-", Label: "an ad-hoc signature"}
	case *identity != "":
		spec = signSpec{Identity: *identity, Label: *identity}
	default:
		var err error
		if spec, err = devSignSpec(); err != nil {
			return err
		}
	}
	if _, err := signBundle(app, signOptions{Spec: spec, Hardened: *hardened, Entitlements: *ent}); err != nil {
		return err
	}
	if err := verifyBundle(app); err != nil {
		return err
	}
	dr, _ := designatedRequirement(app)
	fmt.Fprintf(stdout, "signed %s with %s\ndesignated requirement: %s\n", app, spec.Label, dr)
	return nil
}
